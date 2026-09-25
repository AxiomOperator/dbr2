// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	goruntime "runtime"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/pki"
	"github.com/AxiomOperator/dbr2/internal/runtime"
	"github.com/AxiomOperator/dbr2/internal/version"
)

// Agent is a running dbr2-agent.
type Agent struct {
	cfg     *Config
	rt      runtime.ContainerRuntime
	journal *Journal
	log     *slog.Logger
	started time.Time

	mu      sync.Mutex
	cur     *outbound // current session's outbound queue
	running map[string]bool

	certMu sync.RWMutex
	cert   *tls.Certificate
	pool   *x509.CertPool

	discovering sync.Mutex // one discovery at a time
	// RenewAfter overrides the renewal point (tests); zero = 2/3 of lifetime.
	RenewAfter time.Duration
	// MaxBackoff caps every reconnect wait, including the gateway's
	// retry_after (tests); zero = no cap.
	MaxBackoff time.Duration
}

type outbound struct {
	ch   chan *agentv1.ConnectRequest
	done chan struct{}
}

func (o *outbound) send(m *agentv1.ConnectRequest) bool {
	select {
	case <-o.done:
		return false
	case o.ch <- m:
		return true
	case <-time.After(10 * time.Second):
		return false
	}
}

// New loads identity and state. rt may be nil (commands needing the runtime
// then fail with a retryable error).
func New(cfg *Config, rt runtime.ContainerRuntime, log *slog.Logger) (*Agent, error) {
	a := &Agent{cfg: cfg, rt: rt, log: log, started: time.Now(), running: map[string]bool{}}
	if err := a.loadIdentity(); err != nil {
		return nil, err
	}
	j, err := OpenJournal(cfg.path(jrnlFile))
	if err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}
	a.journal = j
	return a, nil
}

func (a *Agent) loadIdentity() error {
	cert, err := tls.LoadX509KeyPair(a.cfg.path(certFile), a.cfg.path(keyFile))
	if err != nil {
		return fmt.Errorf("load agent certificate: %w (%w)", err, ErrNotEnrolled)
	}
	if cert.Leaf == nil {
		cert.Leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}
	caPEM, err := os.ReadFile(a.cfg.path(caFile))
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return errors.New("invalid ca.crt")
	}
	a.certMu.Lock()
	a.cert, a.pool = &cert, pool
	a.certMu.Unlock()
	return nil
}

// ID returns the agent identity from its certificate.
func (a *Agent) ID() string {
	a.certMu.RLock()
	defer a.certMu.RUnlock()
	id, _ := pki.AgentID(a.cert.Leaf)
	return id
}

// CertNotAfter returns the current certificate's expiry.
func (a *Agent) CertNotAfter() time.Time {
	a.certMu.RLock()
	defer a.certMu.RUnlock()
	return a.cert.Leaf.NotAfter
}

// Journal exposes the journal (status command).
func (a *Agent) Journal() *Journal { return a.journal }

func (a *Agent) tlsConfig() (*tls.Config, error) {
	host, _, err := net.SplitHostPort(a.cfg.Server)
	if err != nil {
		return nil, err
	}
	a.certMu.RLock()
	pool := a.pool
	a.certMu.RUnlock()
	return &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: pool, ServerName: host,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			a.certMu.RLock()
			defer a.certMu.RUnlock()
			return a.cert, nil
		},
	}, nil
}

// Run connects to the gateway until ctx is cancelled, reconnecting with
// exponential backoff (honouring retry_after from rejects).
func (a *Agent) Run(ctx context.Context) error {
	defer a.journal.Close()
	go a.periodic(ctx)
	backoff := time.Second
	for {
		wait, err := a.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if wait > 0 {
			backoff = time.Second
		} else {
			if err != nil {
				a.log.Warn("gateway session ended", "err", err, "retry_in", backoff)
			}
			wait = backoff + time.Duration(rand.Int64N(int64(backoff)/2+1))
			backoff = min(backoff*2, time.Minute)
		}
		if a.MaxBackoff > 0 && wait > a.MaxBackoff {
			wait = a.MaxBackoff
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// errRejected carries the gateway's reject (the caller waits retry_after).
type errRejected struct{ r *agentv1.Reject }

func (e errRejected) Error() string { return "rejected: " + e.r.Code + ": " + e.r.Message }

// session runs one gateway session. It returns a positive wait when the
// gateway asked the agent to back off.
func (a *Agent) session(ctx context.Context) (time.Duration, error) {
	if err := a.maybeRenew(ctx); err != nil {
		a.log.Warn("certificate renewal failed; will retry", "err", err)
	}
	tc, err := a.tlsConfig()
	if err != nil {
		return 0, err
	}
	conn, err := grpc.NewClient(a.cfg.Server, grpc.WithTransportCredentials(credentials.NewTLS(tc)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(64<<20)))
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := agentv1.NewAgentServiceClient(conn).Connect(sctx)
	if err != nil {
		return 0, err
	}

	a.mu.Lock()
	inflight := make([]string, 0, len(a.running))
	for id := range a.running {
		inflight = append(inflight, id)
	}
	a.mu.Unlock()
	hostname, _ := os.Hostname()
	if err := stream.Send(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_Hello{Hello: &agentv1.Hello{
		AgentVersion: version.Of(version.Agent), ProtocolVersion: version.Of(version.AgentProtocol),
		Hostname: hostname, OsRelease: OSRelease(), Architecture: goruntime.GOARCH, InflightCommandIds: inflight,
	}}}); err != nil {
		return 0, err
	}
	first, err := stream.Recv()
	if err != nil {
		return 0, err
	}
	if r := first.GetReject(); r != nil {
		wait := time.Duration(r.RetryAfterSeconds) * time.Second
		if wait == 0 {
			wait = 30 * time.Second
		}
		a.log.Warn("gateway refused the session", "code", r.Code, "message", r.Message, "retry_in", wait)
		return wait, errRejected{r}
	}
	if first.GetWelcome() == nil {
		return 0, errors.New("expected Welcome")
	}
	a.log.Info("connected to gateway", "server", a.cfg.Server, "session", first.GetWelcome().SessionId,
		"gateway_version", first.GetWelcome().GatewayVersion)

	out := &outbound{ch: make(chan *agentv1.ConnectRequest, 64), done: make(chan struct{})}
	a.mu.Lock()
	a.cur = out
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.cur == out {
			a.cur = nil
		}
		a.mu.Unlock()
		close(out.done)
	}()

	sendErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-sctx.Done():
				return
			case m := <-out.ch:
				if err := stream.Send(m); err != nil {
					sendErr <- err
					cancel()
					return
				}
			}
		}
	}()
	// Replay unacknowledged results and report health right away.
	for _, u := range a.journal.Unacked() {
		out.send(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_CommandUpdate{CommandUpdate: u}})
	}
	go a.sendHealth(sctx)

	for {
		m, err := stream.Recv()
		if err != nil {
			select {
			case e := <-sendErr:
				return 0, e
			default:
			}
			return 0, err
		}
		switch b := m.Body.(type) {
		case *agentv1.ConnectResponse_Heartbeat:
			out.send(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_Heartbeat{Heartbeat: &agentv1.Heartbeat{
				SentUnixMs: time.Now().UnixMilli(), EchoUnixMs: b.Heartbeat.SentUnixMs}}})
		case *agentv1.ConnectResponse_Command:
			a.handleCommand(ctx, b.Command)
		case *agentv1.ConnectResponse_CommandAck:
			if err := a.journal.Ack(b.CommandAck.CommandId); err != nil {
				a.log.Error("journal ack failed", "err", err)
			}
		case *agentv1.ConnectResponse_Reject:
			wait := time.Duration(b.Reject.RetryAfterSeconds) * time.Second
			a.log.Warn("gateway ended the session", "code", b.Reject.Code, "message", b.Reject.Message)
			if wait == 0 {
				wait = 5 * time.Second
			}
			return wait, errRejected{b.Reject}
		}
	}
}

// emit sends on the current session, if any. Terminal results that cannot be
// sent stay in the journal and are replayed on the next session.
func (a *Agent) emit(m *agentv1.ConnectRequest) {
	a.mu.Lock()
	out := a.cur
	a.mu.Unlock()
	if out != nil {
		out.send(m)
	}
}

func update(id string, st agentv1.CommandState) *agentv1.CommandUpdate {
	return &agentv1.CommandUpdate{CommandId: id, State: st}
}

func kindOf(c *agentv1.Command) string {
	switch c.Kind.(type) {
	case *agentv1.Command_Discover:
		return "discover"
	case *agentv1.Command_Echo:
		return "echo"
	}
	return "unknown"
}

// handleCommand implements idempotent execution (ADR-0001): a command_id
// runs at most once; repeats get the journaled result or the running state.
func (a *Agent) handleCommand(root context.Context, c *agentv1.Command) {
	if u, ok := a.journal.Result(c.CommandId); ok {
		a.emit(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_CommandUpdate{CommandUpdate: u}})
		return
	}
	a.mu.Lock()
	if a.running[c.CommandId] {
		a.mu.Unlock()
		a.emit(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_CommandUpdate{CommandUpdate: update(c.CommandId, agentv1.CommandState_COMMAND_STATE_RUNNING)}})
		return
	}
	a.running[c.CommandId] = true
	a.mu.Unlock()
	kind := kindOf(c)
	if err := a.journal.Accept(c.CommandId, kind); err != nil {
		a.log.Error("journal accept failed", "err", err)
	}
	a.emit(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_CommandUpdate{CommandUpdate: update(c.CommandId, agentv1.CommandState_COMMAND_STATE_ACCEPTED)}})

	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.running, c.CommandId)
			a.mu.Unlock()
		}()
		// Execution is bound to the agent's lifetime and the command
		// deadline, not to the session: it survives disconnects.
		ctx := root
		if c.DeadlineUnixMs > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(root, time.UnixMilli(c.DeadlineUnixMs))
			defer cancel()
		}
		u := a.execute(ctx, c)
		if root.Err() != nil {
			return // shutting down: not journaled as finished; re-dispatched later
		}
		if err := a.journal.Finish(u, kind); err != nil {
			a.log.Error("journal finish failed", "err", err)
		}
		a.emit(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_CommandUpdate{CommandUpdate: u}})
	}()
}

func (a *Agent) execute(ctx context.Context, c *agentv1.Command) *agentv1.CommandUpdate {
	fail := func(err error, retryable bool) *agentv1.CommandUpdate {
		u := update(c.CommandId, agentv1.CommandState_COMMAND_STATE_FAILED)
		u.Error, u.Retryable = err.Error(), retryable
		return u
	}
	switch k := c.Kind.(type) {
	case *agentv1.Command_Echo:
		select {
		case <-time.After(time.Duration(k.Echo.DurationMs) * time.Millisecond):
		case <-ctx.Done():
			return fail(ctx.Err(), true)
		}
		u := update(c.CommandId, agentv1.CommandState_COMMAND_STATE_SUCCEEDED)
		u.Result = &agentv1.CommandUpdate_Echo{Echo: &agentv1.EchoResult{Message: k.Echo.Message}}
		return u
	case *agentv1.Command_Discover:
		data, err := a.discover(ctx, k.Discover.IncludeFilesystemChanges)
		if err != nil {
			return fail(err, true)
		}
		u := update(c.CommandId, agentv1.CommandState_COMMAND_STATE_SUCCEEDED)
		u.Result = &agentv1.CommandUpdate_Discover{Discover: &agentv1.DiscoverResult{InventoryJson: data}}
		return u
	}
	return fail(fmt.Errorf("unsupported command %T (upgrade the agent)", c.Kind), false)
}

func (a *Agent) discover(ctx context.Context, changes bool) ([]byte, error) {
	if a.rt == nil {
		return nil, errors.New("container runtime unavailable")
	}
	a.discovering.Lock()
	defer a.discovering.Unlock()
	inv, err := a.rt.Discover(ctx, runtime.DiscoverOptions{FilesystemChanges: changes})
	if err != nil {
		return nil, err
	}
	return json.Marshal(inv)
}

// periodic pushes the inventory every discovery interval.
func (a *Agent) periodic(ctx context.Context) {
	t := time.NewTicker(a.cfg.DiscoveryInterval)
	defer t.Stop()
	first := time.After(10 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-first:
		case <-t.C:
		}
		a.mu.Lock()
		connected := a.cur != nil
		a.mu.Unlock()
		if !connected {
			continue
		}
		data, err := a.discover(ctx, true)
		if err != nil {
			a.log.Warn("periodic discovery failed", "err", err)
			continue
		}
		a.emit(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_Inventory{Inventory: &agentv1.InventoryReport{InventoryJson: data}}})
	}
}

func (a *Agent) sendHealth(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		h := &agentv1.HealthReport{UptimeSeconds: int64(time.Since(a.started).Seconds())}
		if a.rt == nil {
			h.DockerError = "container runtime unavailable"
		} else {
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			v, err := a.rt.Ping(pctx)
			cancel()
			h.DockerReachable, h.DockerVersion = err == nil, v
			if err != nil {
				h.DockerError = err.Error()
			}
		}
		a.emit(&agentv1.ConnectRequest{Body: &agentv1.ConnectRequest_Health{Health: h}})
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// maybeRenew renews the certificate after 2/3 of its lifetime with a fresh
// key (the private key never leaves the host).
func (a *Agent) maybeRenew(ctx context.Context) error {
	a.certMu.RLock()
	leaf := a.cert.Leaf
	a.certMu.RUnlock()
	after := a.RenewAfter
	if after == 0 {
		after = leaf.NotAfter.Sub(leaf.NotBefore) * 2 / 3
	}
	if time.Now().Before(leaf.NotBefore.Add(after)) {
		return nil
	}
	tc, err := a.tlsConfig()
	if err != nil {
		return err
	}
	conn, err := grpc.NewClient(a.cfg.Server, grpc.WithTransportCredentials(credentials.NewTLS(tc)))
	if err != nil {
		return err
	}
	defer conn.Close()
	host, _ := os.Hostname()
	keyPEM, csr, err := pki.NewAgentKey(host)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := agentv1.NewEnrollmentServiceClient(conn).Renew(cctx, &agentv1.RenewRequest{CsrDer: csr})
	if err != nil {
		return err
	}
	if err := writeFileAtomic(a.cfg.path(keyFile), keyPEM, 0o600); err != nil {
		return err
	}
	if err := writeFileAtomic(a.cfg.path(certFile), pki.PEMCert(resp.CertificateDer), 0o644); err != nil {
		return err
	}
	if err := a.loadIdentity(); err != nil {
		return err
	}
	a.log.Info("agent certificate renewed", "not_after", a.CertNotAfter())
	return nil
}
