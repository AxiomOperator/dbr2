// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/pki"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/version"
)

// Reject codes sent to agents.
const (
	RejectPending         = "pending_approval"
	RejectSuspended       = "suspended"
	RejectRevoked         = "revoked"
	RejectProtocolVersion = "protocol_version_mismatch"
	RejectUnknownAgent    = "unknown_agent"
	RejectSuperseded      = "superseded"
	RejectProtocolError   = "protocol_error"
)

// Config configures the gateway.
type Config struct {
	OrgID      uuid.UUID
	Hostnames  []string // server certificate SANs
	InstanceID string
	// HeartbeatInterval between gateway heartbeats (latency + lease).
	HeartbeatInterval time.Duration
}

// Gateway is the Agent Gateway.
type Gateway struct {
	cfg   Config
	pool  *pgxpool.Pool
	q     *store.Queries
	box   Box
	ca    *pki.CA
	audit *audit.Recorder
	log   *slog.Logger

	serverCert atomic.Pointer[tls.Certificate]

	mu       sync.Mutex
	sessions map[string]*session
	changed  chan struct{}
	waiters  map[string]*waiter

	latency metric.Float64Histogram
}

type session struct {
	id      string
	agentID string
	send    chan *agentv1.ConnectResponse
	done    chan struct{}
	cancel  context.CancelCauseFunc
	once    sync.Once
}

func (s *session) close(cause error) {
	s.once.Do(func() {
		s.cancel(cause)
		close(s.done)
	})
}

// enqueue queues a message for the session's sender; false if it is closed.
func (s *session) enqueue(m *agentv1.ConnectResponse) bool {
	select {
	case <-s.done:
		return false
	case s.send <- m:
		return true
	}
}

type waiter struct {
	agentID string
	updates chan *agentv1.CommandUpdate
}

// New builds the gateway.
func New(cfg Config, pool *pgxpool.Pool, box Box, ca *pki.CA, rec *audit.Recorder, log *slog.Logger) (*Gateway, error) {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 15 * time.Second
	}
	if cfg.InstanceID == "" {
		cfg.InstanceID = uuid.NewString()
	}
	g := &Gateway{cfg: cfg, pool: pool, q: store.New(pool), box: box, ca: ca, audit: rec, log: log.With("component", "agent-gateway"),
		sessions: map[string]*session{}, changed: make(chan struct{}), waiters: map[string]*waiter{}}
	if err := g.rotateServerCert(); err != nil {
		return nil, err
	}
	meter := otel.Meter("dbr2/gateway")
	var err error
	if g.latency, err = meter.Float64Histogram("agent.latency", metric.WithUnit("ms"),
		metric.WithDescription("Agent Gateway ↔ agent heartbeat round-trip time")); err != nil {
		return nil, err
	}
	gauge, err := meter.Int64ObservableGauge("agent.connection_state",
		metric.WithDescription("1 when the agent has an active gateway session, else 0"))
	if err != nil {
		return nil, err
	}
	if _, err := meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		agents, err := g.q.ListAgents(ctx, g.cfg.OrgID)
		if err != nil {
			return err
		}
		for _, a := range agents {
			v := int64(0)
			if g.Connected(a.ID.String()) {
				v = 1
			}
			o.ObserveInt64(gauge, v, metric.WithAttributes(attribute.String("agent.id", a.ID.String()),
				attribute.String("host.name", a.Hostname), attribute.String("agent.status", a.Status)))
		}
		return nil
	}, gauge); err != nil {
		return nil, err
	}
	return g, nil
}

// CA returns the agent CA.
func (g *Gateway) CA() *pki.CA { return g.ca }

// rotateServerCert (re)issues the gateway's server certificate.
func (g *Gateway) rotateServerCert() error {
	c, err := g.ca.ServerCertificate(g.cfg.Hostnames)
	if err != nil {
		return err
	}
	g.serverCert.Store(&c)
	return nil
}

// TLSConfig returns the gateway TLS configuration: TLS 1.3, server
// certificate rotated a week before expiry, client certificates optional at
// the TLS layer (enrollment has none) and enforced per RPC.
func (g *Gateway) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		ClientAuth: tls.VerifyClientCertIfGiven,
		ClientCAs:  g.ca.Pool(),
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			c := g.serverCert.Load()
			if time.Until(c.Leaf.NotAfter) < 7*24*time.Hour {
				if err := g.rotateServerCert(); err != nil {
					return nil, err
				}
				c = g.serverCert.Load()
			}
			return c, nil
		},
	}
}

// GRPCServer builds the agent-facing gRPC server (enrollment + sessions).
func (g *Gateway) GRPCServer() *grpc.Server {
	s := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(g.TLSConfig())),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
		grpc.MaxRecvMsgSize(64<<20), // inventories of large hosts
	)
	agentv1.RegisterEnrollmentServiceServer(s, &enrollServer{g: g})
	agentv1.RegisterAgentServiceServer(s, &agentServer{g: g})
	return s
}

// Connected reports whether the agent has a live session on this instance.
func (g *Gateway) Connected(agentID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.sessions[agentID]
	return ok
}

// Disconnect ends the agent's session with a reject code (suspend/revoke).
func (g *Gateway) Disconnect(agentID, code, message string) {
	g.mu.Lock()
	s := g.sessions[agentID]
	g.mu.Unlock()
	if s == nil {
		return
	}
	select {
	case s.send <- &agentv1.ConnectResponse{Body: &agentv1.ConnectResponse_Reject{Reject: &agentv1.Reject{Code: code, Message: message}}}:
	case <-time.After(2 * time.Second):
	}
	time.Sleep(100 * time.Millisecond) // let the sender flush the reject
	s.close(errors.New(code))
}

// ForceReconnect drops the agent's session without a reject; the agent
// reconnects immediately (operations: pick up a renewed certificate or a
// moved gateway; tests: simulate a network interruption).
func (g *Gateway) ForceReconnect(agentID string) bool {
	g.mu.Lock()
	s := g.sessions[agentID]
	g.mu.Unlock()
	if s == nil {
		return false
	}
	s.close(errors.New("forced reconnect"))
	return true
}

func (g *Gateway) register(s *session) {
	g.mu.Lock()
	old := g.sessions[s.agentID]
	g.sessions[s.agentID] = s
	close(g.changed)
	g.changed = make(chan struct{})
	g.mu.Unlock()
	if old != nil {
		old.enqueue(&agentv1.ConnectResponse{Body: &agentv1.ConnectResponse_Reject{Reject: &agentv1.Reject{Code: RejectSuperseded, Message: "a newer session replaced this one"}}})
		old.close(errors.New(RejectSuperseded))
	}
}

func (g *Gateway) unregister(s *session) {
	g.mu.Lock()
	if g.sessions[s.agentID] == s {
		delete(g.sessions, s.agentID)
		close(g.changed)
		g.changed = make(chan struct{})
	}
	g.mu.Unlock()
	s.close(errors.New("session ended"))
}

// waitSession blocks until the agent has a session or ctx ends.
func (g *Gateway) waitSession(ctx context.Context, agentID string) (*session, error) {
	for {
		g.mu.Lock()
		s := g.sessions[agentID]
		ch := g.changed
		g.mu.Unlock()
		if s != nil {
			return s, nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, fmt.Errorf("agent %s not connected: %w", agentID, ctx.Err())
		}
	}
}

// ErrAgentNotActive is returned when dispatching to a non-active agent.
var ErrAgentNotActive = errors.New("agent is not active")

// Dispatch sends cmd to the agent and returns its terminal update, calling
// onUpdate for progress (ADR-0001). It is idempotent on command_id: if the
// session drops, the command is re-sent on the next session and the agent
// returns its journaled result or keeps reporting the running execution.
func (g *Gateway) Dispatch(ctx context.Context, agentID string, cmd *agentv1.Command, onUpdate func(*agentv1.CommandUpdate)) (*agentv1.CommandUpdate, error) {
	id, err := uuid.Parse(agentID)
	if err != nil {
		return nil, fmt.Errorf("invalid agent id: %w", err)
	}
	a, err := g.q.GetAgent(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("agent %s: %w", agentID, err)
	}
	if a.Status != "active" {
		return nil, fmt.Errorf("%w (status %s)", ErrAgentNotActive, a.Status)
	}
	if cmd.CommandId == "" {
		return nil, errors.New("command_id is required")
	}
	w := &waiter{agentID: agentID, updates: make(chan *agentv1.CommandUpdate, 64)}
	g.mu.Lock()
	g.waiters[cmd.CommandId] = w // a retried dispatch replaces the stale waiter
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		if g.waiters[cmd.CommandId] == w {
			delete(g.waiters, cmd.CommandId)
		}
		g.mu.Unlock()
	}()
	_ = g.q.UpsertAgentCommand(ctx, store.UpsertAgentCommandParams{CommandID: cmd.CommandId, AgentID: id, Kind: commandKind(cmd), State: "dispatched"})

	msg := &agentv1.ConnectResponse{Body: &agentv1.ConnectResponse_Command{Command: cmd}}
	for {
		s, err := g.waitSession(ctx, agentID)
		if err != nil {
			return nil, err
		}
		if !s.enqueue(msg) {
			continue
		}
	wait:
		for {
			select {
			case u := <-w.updates:
				if onUpdate != nil {
					onUpdate(u)
				}
				if terminal(u.State) {
					return u, nil
				}
			case <-s.done:
				break wait // re-send on the next session
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
}

func terminal(s agentv1.CommandState) bool {
	return s == agentv1.CommandState_COMMAND_STATE_SUCCEEDED || s == agentv1.CommandState_COMMAND_STATE_FAILED
}

func commandKind(c *agentv1.Command) string {
	switch c.Kind.(type) {
	case *agentv1.Command_Discover:
		return "discover"
	case *agentv1.Command_Echo:
		return "echo"
	}
	return "unknown"
}

// deliver routes an agent's command update to its waiter and applies side
// effects of terminal results (inventory ingestion) before acknowledging.
func (g *Gateway) deliver(ctx context.Context, s *session, u *agentv1.CommandUpdate) {
	agentID, _ := uuid.Parse(s.agentID)
	stateName := strings.ToLower(strings.TrimPrefix(u.State.String(), "COMMAND_STATE_"))
	kind := "unknown"
	if d := u.GetDiscover(); d != nil {
		kind = "discover"
	} else if u.GetEcho() != nil {
		kind = "echo"
	}
	if terminal(u.State) {
		if d := u.GetDiscover(); d != nil && u.State == agentv1.CommandState_COMMAND_STATE_SUCCEEDED {
			if _, err := g.IngestInventory(ctx, s.agentID, d.InventoryJson); err != nil {
				g.log.ErrorContext(ctx, "inventory ingestion failed; result not acknowledged", "agent_id", s.agentID, "command_id", u.CommandId, "err", err)
				u = &agentv1.CommandUpdate{CommandId: u.CommandId, State: agentv1.CommandState_COMMAND_STATE_FAILED,
					Error: "gateway could not store the inventory: " + err.Error(), Retryable: true}
				g.route(s.agentID, u)
				return
			}
		}
		_ = g.q.UpsertAgentCommand(ctx, store.UpsertAgentCommandParams{CommandID: u.CommandId, AgentID: agentID, Kind: kind, State: stateName, Error: strPtr(u.Error)})
		s.enqueue(&agentv1.ConnectResponse{Body: &agentv1.ConnectResponse_CommandAck{CommandAck: &agentv1.CommandAck{CommandId: u.CommandId}}})
	}
	g.route(s.agentID, u)
}

func (g *Gateway) route(agentID string, u *agentv1.CommandUpdate) {
	g.mu.Lock()
	w := g.waiters[u.CommandId]
	g.mu.Unlock()
	if w == nil || w.agentID != agentID {
		return
	}
	if terminal(u.State) {
		select {
		case w.updates <- u:
		case <-time.After(5 * time.Second):
			g.log.Warn("dispatch waiter did not accept the terminal update", "command_id", u.CommandId)
		}
		return
	}
	select {
	case w.updates <- u:
	default: // drop progress rather than block the session
	}
}

// ---- Connect -----------------------------------------------------------------

type agentServer struct {
	agentv1.UnimplementedAgentServiceServer
	g *Gateway
}

// peerAgent returns the verified client certificate's agent identity.
func peerAgent(ctx context.Context) (string, *x509.Certificate, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", nil, status.Error(codes.Unauthenticated, "no peer")
	}
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(ti.State.VerifiedChains) == 0 || len(ti.State.PeerCertificates) == 0 {
		return "", nil, status.Error(codes.Unauthenticated, "a client certificate issued by the DBR² CA is required")
	}
	cert := ti.State.PeerCertificates[0]
	id, err := pki.AgentID(cert)
	if err != nil {
		return "", nil, status.Error(codes.Unauthenticated, err.Error())
	}
	return id, cert, nil
}

func reject(stream agentv1.AgentService_ConnectServer, code, msg string, retry uint32) error {
	return stream.Send(&agentv1.ConnectResponse{Body: &agentv1.ConnectResponse_Reject{Reject: &agentv1.Reject{Code: code, Message: msg, RetryAfterSeconds: retry}}})
}

// Connect is the agent session (ADR-0001).
func (a *agentServer) Connect(stream agentv1.AgentService_ConnectServer) error {
	g := a.g
	ctx := stream.Context()
	agentID, cert, err := peerAgent(ctx)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(agentID)
	if err != nil {
		return reject(stream, RejectUnknownAgent, "malformed agent identity", 3600)
	}
	certRow, err := g.q.GetAgentCertificate(ctx, pki.SerialHex(cert))
	if err != nil || certRow.AgentID != id {
		return reject(stream, RejectUnknownAgent, "certificate not issued to this agent", 3600)
	}
	if certRow.RevokedAt != nil {
		return reject(stream, RejectRevoked, "certificate revoked", 3600)
	}
	ag, err := g.q.GetAgent(ctx, id)
	if err != nil {
		return reject(stream, RejectUnknownAgent, "agent not found", 3600)
	}

	// The first message must be Hello.
	first, err := recvWithTimeout(stream, 15*time.Second)
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return reject(stream, RejectProtocolError, "first message must be Hello", 0)
	}
	_ = g.q.UpdateAgentHello(ctx, store.UpdateAgentHelloParams{
		ID: id, AgentVersion: hello.AgentVersion, ProtocolVersion: hello.ProtocolVersion,
		OsRelease: strPtr(hello.OsRelease), Architecture: strPtr(hello.Architecture), Hostname: nonEmpty(hello.Hostname, ag.Hostname),
	})
	if err := CheckProtocol(hello.ProtocolVersion); err != nil {
		return reject(stream, RejectProtocolVersion, err.Error(), 3600)
	}
	switch ag.Status {
	case "pending":
		return reject(stream, RejectPending, "waiting for an administrator to approve this host", 30)
	case "suspended":
		return reject(stream, RejectSuspended, "this agent is suspended", 300)
	case "revoked":
		return reject(stream, RejectRevoked, "this agent is revoked", 3600)
	}

	sctx, cancel := context.WithCancelCause(ctx)
	s := &session{id: uuid.NewString(), agentID: agentID, send: make(chan *agentv1.ConnectResponse, 64), done: make(chan struct{}), cancel: cancel}
	g.register(s)
	defer g.unregister(s)
	remote := ""
	if p, ok := peer.FromContext(ctx); ok {
		remote = p.Addr.String()
	}
	_ = g.q.UpsertAgentSession(ctx, store.UpsertAgentSessionParams{AgentID: id, SessionID: s.id, GatewayInstance: g.cfg.InstanceID, RemoteAddr: &remote})
	defer func() {
		_ = g.q.DeleteAgentSession(context.WithoutCancel(ctx), store.DeleteAgentSessionParams{AgentID: id, SessionID: s.id})
	}()
	g.log.InfoContext(ctx, "agent connected", "agent_id", agentID, "hostname", hello.Hostname, "agent_version", hello.AgentVersion,
		"outdated", Outdated(hello.AgentVersion), "remote", remote, "inflight", len(hello.InflightCommandIds))

	if err := stream.Send(&agentv1.ConnectResponse{Body: &agentv1.ConnectResponse_Welcome{Welcome: &agentv1.Welcome{
		SessionId: s.id, GatewayVersion: version.Of(version.Server), ProtocolVersion: version.Of(version.AgentProtocol),
		HeartbeatIntervalSeconds: uint32(g.cfg.HeartbeatInterval / time.Second),
	}}}); err != nil {
		return err
	}

	// Sender: the only goroutine calling stream.Send from here on.
	sendErr := make(chan error, 1)
	go func() {
		t := time.NewTicker(g.cfg.HeartbeatInterval)
		defer t.Stop()
		for {
			var m *agentv1.ConnectResponse
			select {
			case <-sctx.Done():
				sendErr <- nil
				return
			case m = <-s.send:
			case <-t.C:
				m = &agentv1.ConnectResponse{Body: &agentv1.ConnectResponse_Heartbeat{Heartbeat: &agentv1.Heartbeat{SentUnixMs: time.Now().UnixMilli()}}}
			}
			if err := stream.Send(m); err != nil {
				sendErr <- err
				s.close(err)
				return
			}
			if m.GetReject() != nil {
				s.close(errors.New(m.GetReject().Code))
			}
		}
	}()

	recvErr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			switch b := m.Body.(type) {
			case *agentv1.ConnectRequest_Heartbeat:
				if b.Heartbeat.EchoUnixMs > 0 {
					rtt := float64(time.Now().UnixMilli() - b.Heartbeat.EchoUnixMs)
					g.latency.Record(sctx, rtt, metric.WithAttributes(attribute.String("agent.id", agentID)))
					_ = g.q.UpdateAgentLatency(sctx, store.UpdateAgentLatencyParams{ID: id, LastLatencyMs: int32Ptr(int32(rtt))})
				}
				_ = g.q.TouchAgentSession(sctx, store.TouchAgentSessionParams{AgentID: id, SessionID: s.id})
			case *agentv1.ConnectRequest_CommandUpdate:
				g.deliver(sctx, s, b.CommandUpdate)
			case *agentv1.ConnectRequest_Inventory:
				if _, err := g.IngestInventory(sctx, agentID, b.Inventory.InventoryJson); err != nil {
					g.log.ErrorContext(sctx, "periodic inventory ingestion failed", "agent_id", agentID, "err", err)
				}
			case *agentv1.ConnectRequest_Health:
				h := b.Health
				_ = g.q.UpdateAgentHealth(sctx, store.UpdateAgentHealthParams{ID: id, DockerReachable: &h.DockerReachable,
					DockerVersion: strPtr(h.DockerVersion), HealthError: strPtr(h.DockerError)})
			}
		}
	}()

	select {
	case err = <-recvErr:
	case err = <-sendErr:
	case <-sctx.Done():
		err = nil
	}
	g.log.InfoContext(ctx, "agent disconnected", "agent_id", agentID, "reason", context.Cause(sctx), "err", err)
	return nil
}

func recvWithTimeout(stream agentv1.AgentService_ConnectServer, d time.Duration) (*agentv1.ConnectRequest, error) {
	type res struct {
		m   *agentv1.ConnectRequest
		err error
	}
	ch := make(chan res, 1)
	go func() {
		m, err := stream.Recv()
		ch <- res{m, err}
	}()
	select {
	case r := <-ch:
		return r.m, r.err
	case <-time.After(d):
		return nil, status.Error(codes.DeadlineExceeded, "no Hello received")
	}
}

// CheckProtocol enforces ADR-0015: the agent-protocol MAJOR must match.
func CheckProtocol(agentProtocol string) error {
	want := major(version.Of(version.AgentProtocol))
	if got := major(agentProtocol); got != want || got < 0 {
		return fmt.Errorf("agent speaks protocol %q; this gateway requires major version %d (upgrade the agent)", agentProtocol, want)
	}
	return nil
}

// Outdated reports whether an agent is older than the agent version this
// server ships (it still connects; the console flags it).
func Outdated(agentVersion string) bool {
	return compare(agentVersion, version.Of(version.Agent)) < 0
}

func major(v string) int {
	n, err := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	if err != nil {
		return -1
	}
	return n
}

// compare compares MAJOR.MINOR.BUGFIX (build ignored).
func compare(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func int32Ptr(v int32) *int32 { return &v }

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
