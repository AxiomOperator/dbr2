// SPDX-License-Identifier: Apache-2.0

// Package protection is the server-side service for Repositories, key
// escrow, backup settings, host limits, backups, the recovery-point index
// and alerts (Phase 4). It also implements the internal PlatformService used
// by dbr2-worker (control channel).
package protection

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/escrow"
	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/gateway"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/repoclient"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/temporalx"
	"github.com/AxiomOperator/dbr2/workflows/backup"
)

// Errors mapped to API responses.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid request")
	ErrConflict = errors.New("conflict")
)

// Splitter for new repositories (ADR-0002, spike-measured).
const Splitter = "DYNAMIC-1M-BUZHASH"

// Options configure the service.
type Options struct {
	OrgID     uuid.UUID
	TaskQueue string
	// InternalToken authenticates to reposerver management APIs.
	InternalToken string
	// MinEscrowRecipients required to create a Repository (ADR-0008: two).
	MinEscrowRecipients int
	// Log receives background errors (schedule sync, evaluations).
	Log *slog.Logger
}

// Service implements the protection domain.
type Service struct {
	opts     Options
	pool     *pgxpool.Pool
	q        *store.Queries
	audit    *audit.Recorder
	fleet    *fleet.Service
	gw       *gateway.Gateway
	temporal client.Client
	// repoClient builds management clients (replaceable in tests).
	repoClient func(managementURL string) RepoManager
	now        func() time.Time
	events     atomic.Pointer[events.Bus]
	// platformExp is set by SetPlatformExporter (platform_backup_rpc.go).
	platformExp atomic.Pointer[platformExporterBox]
}

// SetEvents enables live updates (SSE).
func (s *Service) SetEvents(b *events.Bus) { s.events.Store(b) }

func (s *Service) publish(ctx context.Context, typ string, perm rbac.Permission, data any) {
	if b := s.events.Load(); b != nil {
		b.Publish(ctx, events.New(typ, perm, data))
	}
}

// RepoManager is the subset of the reposerver management API used here.
type RepoManager interface {
	Status(ctx context.Context) (*repoclient.Status, error)
	Initialize(ctx context.Context, password, splitter string) (*repoclient.Status, error)
	SetUser(ctx context.Context, username, password string) error
	GrantRead(ctx context.Context, user, sourceUser, sourceHost string) (string, error)
	RepositoryPassword(ctx context.Context) (string, error)
	RevokeRead(ctx context.Context, id string) error
}

// New builds the service.
func New(opts Options, pool *pgxpool.Pool, rec *audit.Recorder, fl *fleet.Service, gw *gateway.Gateway, tc client.Client) *Service {
	if opts.MinEscrowRecipients == 0 {
		opts.MinEscrowRecipients = 2
	}
	s := &Service{opts: opts, pool: pool, q: store.New(pool), audit: rec, fleet: fl, gw: gw, temporal: tc, now: time.Now}
	s.repoClient = func(u string) RepoManager { return repoclient.New(u, opts.InternalToken) }
	return s
}

// SetRepoClient replaces the management client factory (tests).
func (s *Service) SetRepoClient(f func(managementURL string) RepoManager) { s.repoClient = f }

func (s *Service) event(p *auth.Principal, m auth.RequestMeta, typ string) audit.Event {
	return audit.Event{OrgID: s.opts.OrgID, Type: typ, ActorUserID: &p.UserID, ActorDisplay: p.Username, ActorKind: audit.ActorUser,
		SourceIP: m.IP, RequestID: m.RequestID, Result: audit.Success}
}

func (s *Service) systemEvent(typ, targetType, targetID, result string, details map[string]any) audit.Event {
	return audit.Event{OrgID: s.opts.OrgID, Type: typ, ActorKind: audit.ActorSystem, ActorDisplay: "dbr2-worker",
		TargetType: targetType, TargetID: targetID, Result: result, Details: details}
}

func (s *Service) inTx(ctx context.Context, fn func(q *store.Queries, rec *audit.Recorder) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		return fn(q, s.audit.WithQuerier(q))
	})
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *Service) alert(ctx context.Context, q *store.Queries, severity, typ, targetType, targetID, msg string, details map[string]any) error {
	payload, _ := json.Marshal(details)
	if payload == nil {
		payload = []byte("{}")
	}
	s.publish(ctx, events.AlertCreated, rbac.BackupRead, map[string]any{"severity": severity, "type": typ,
		"target_type": targetType, "target_id": targetID, "message": msg})
	return q.InsertNotification(ctx, store.InsertNotificationParams{OrgID: s.opts.OrgID, Severity: severity, EventType: typ,
		TargetType: &targetType, TargetID: &targetID, Message: msg, Payload: payload})
}

// ---- Escrow recipients --------------------------------------------------------

// ListEscrowRecipients lists active recipients.
func (s *Service) ListEscrowRecipients(ctx context.Context) ([]store.EscrowRecipient, error) {
	return s.q.ListEscrowRecipients(ctx, s.opts.OrgID)
}

// AddEscrowRecipient registers an age (or SSH) public key.
func (s *Service) AddEscrowRecipient(ctx context.Context, p *auth.Principal, name, publicKey string, m auth.RequestMeta) (store.EscrowRecipient, error) {
	publicKey = strings.TrimSpace(publicKey)
	if strings.TrimSpace(name) == "" {
		return store.EscrowRecipient{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if strings.Contains(publicKey, "AGE-SECRET-KEY") {
		return store.EscrowRecipient{}, fmt.Errorf("%w: that is a private identity — paste the public key (age1…) only, and keep the identity offline", ErrInvalid)
	}
	if _, err := escrow.ParseRecipient(publicKey); err != nil {
		return store.EscrowRecipient{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var out store.EscrowRecipient
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		r, err := q.CreateEscrowRecipient(ctx, store.CreateEscrowRecipientParams{OrgID: s.opts.OrgID, Name: strings.TrimSpace(name), PublicKey: publicKey, CreatedBy: &p.UserID})
		if err != nil {
			if strings.Contains(err.Error(), "escrow_recipients_key") {
				return fmt.Errorf("%w: that public key is already registered", ErrConflict)
			}
			return err
		}
		out = r
		ev := s.event(p, m, audit.EscrowRecipientAdded)
		ev.TargetType, ev.TargetID = "escrow_recipient", r.ID.String()
		ev.Details = map[string]any{"name": r.Name, "public_key": publicKey}
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// RemoveEscrowRecipient removes a recipient (existing packages stay valid
// for it; new packages are not encrypted to it).
func (s *Service) RemoveEscrowRecipient(ctx context.Context, p *auth.Principal, id uuid.UUID, m auth.RequestMeta) error {
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		n, err := q.RemoveEscrowRecipient(ctx, store.RemoveEscrowRecipientParams{ID: id, OrgID: s.opts.OrgID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		ev := s.event(p, m, audit.EscrowRecipientRemoved)
		ev.TargetType, ev.TargetID = "escrow_recipient", id.String()
		_, err = rec.Record(ctx, ev)
		return err
	})
}

// ---- Repositories -----------------------------------------------------------------

// CreateRepositoryInput creates a Repository on an uninitialized reposerver.
type CreateRepositoryInput struct {
	Name          string
	Description   string
	Backend       string // nfs | filesystem
	ManagementURL string
	ServerURL     string
	// InternalServerURL is how dbr2-worker reaches the Kopia server on the
	// deployment network, e.g. https://dbr2-reposerver:51515 ("" = ServerURL).
	InternalServerURL string
	Default           bool
}

// CreatedRepository is returned once: the escrow package must be stored and
// its confirmation code entered before the Repository can be used.
type CreatedRepository struct {
	Repository    store.Repository
	EscrowPackage []byte
}

func validURL(raw string, schemes ...string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	for _, s := range schemes {
		if u.Scheme == s {
			return true
		}
	}
	return false
}

// CreateRepository initializes the Kopia repository through the reposerver
// with a generated password, seals the password to every escrow recipient
// and records the Repository as awaiting_escrow (ADR-0008). The password is
// not stored by dbr2-server.
func (s *Service) CreateRepository(ctx context.Context, p *auth.Principal, in CreateRepositoryInput, m auth.RequestMeta) (*CreatedRepository, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || (in.Backend != "nfs" && in.Backend != "filesystem") {
		return nil, fmt.Errorf("%w: name and backend (nfs|filesystem) are required", ErrInvalid)
	}
	if !validURL(in.ManagementURL, "http", "https") || !validURL(in.ServerURL, "https") ||
		(in.InternalServerURL != "" && !validURL(in.InternalServerURL, "https")) {
		return nil, fmt.Errorf("%w: management_url must be http(s); server_url and internal_server_url https", ErrInvalid)
	}
	recips, err := s.q.ListEscrowRecipients(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	if len(recips) < s.opts.MinEscrowRecipients {
		return nil, fmt.Errorf("%w: %d escrow recipients are required before a Repository can be created (%d configured)",
			ErrConflict, s.opts.MinEscrowRecipients, len(recips))
	}
	mc := s.repoClient(in.ManagementURL)
	st, err := mc.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: reposerver unreachable: %v", ErrConflict, err)
	}
	if st.Initialized {
		return nil, fmt.Errorf("%w: the reposerver already hosts an initialized repository; its password cannot be escrowed by DBR² — use an empty storage path", ErrConflict)
	}
	if !st.StorageHealthy {
		return nil, fmt.Errorf("%w: repository storage is not ready: %s", ErrConflict, st.StorageError)
	}
	pwb := make([]byte, 32)
	if _, err := rand.Read(pwb); err != nil {
		return nil, err
	}
	password := base64.RawURLEncoding.EncodeToString(pwb)
	code, err := escrow.NewConfirmationCode()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(recips))
	ids := make([]uuid.UUID, 0, len(recips))
	for _, r := range recips {
		keys = append(keys, r.PublicKey)
		ids = append(ids, r.ID)
	}
	repoID := uuid.New()
	payload := escrow.Payload{Kind: escrow.KindRepositoryPassword, CreatedAt: s.now().UTC(), RepositoryID: repoID.String(),
		RepositoryName: in.Name, StoragePath: st.StoragePath, Secret: password, ConfirmationCode: code, Instructions: escrow.RepositoryInstructions}
	// Seal before initializing: if sealing fails nothing exists yet.
	if _, err := escrow.Seal(payload, keys); err != nil {
		return nil, err
	}
	st, err = mc.Initialize(ctx, password, Splitter)
	if err != nil {
		return nil, fmt.Errorf("%w: initialize repository: %v", ErrConflict, err)
	}
	payload.KopiaRepository = st.RepositoryID
	pkg, err := escrow.Seal(payload, keys)
	if err != nil {
		return nil, err
	}
	var out CreatedRepository
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		r, err := q.CreateRepository(ctx, store.CreateRepositoryParams{OrgID: s.opts.OrgID, Name: in.Name, Description: in.Description,
			Backend: in.Backend, ManagementUrl: in.ManagementURL, ServerUrl: in.ServerURL, InternalServerUrl: in.InternalServerURL, CertSha256: st.CertSHA256,
			KopiaRepositoryID: strPtr(st.RepositoryID), Splitter: strPtr(Splitter), IsDefault: in.Default, EscrowPackage: pkg,
			EscrowRecipientIds: ids, EscrowConfirmHash: escrow.HashCode(code), CreatedBy: &p.UserID})
		if err != nil {
			if strings.Contains(err.Error(), "repositories_org_id_name_key") || strings.Contains(err.Error(), "repositories_default") {
				return fmt.Errorf("%w: a Repository with that name (or a default Repository) already exists", ErrConflict)
			}
			return err
		}
		out = CreatedRepository{Repository: r, EscrowPackage: pkg}
		ev := s.event(p, m, audit.RepositoryCreated)
		ev.TargetType, ev.TargetID = "repository", r.ID.String()
		ev.Details = map[string]any{"name": r.Name, "backend": r.Backend, "server_url": r.ServerUrl, "cert_sha256": r.CertSha256,
			"kopia_repository_id": st.RepositoryID, "escrow_recipients": len(ids), "splitter": Splitter}
		_, err = rec.Record(ctx, ev)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// RepositoryView is a Repository with its live reposerver status.
type RepositoryView struct {
	store.Repository
	Live      *repoclient.Status
	LiveError string
	// Usage is the per-host logical size of the latest recovery points.
	Usage []store.RepositoryUsageByHostRow
}

// ListRepositories returns every Repository with live status.
func (s *Service) ListRepositories(ctx context.Context) ([]RepositoryView, error) {
	rows, err := s.q.ListRepositories(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	out := make([]RepositoryView, len(rows))
	for i, r := range rows {
		out[i] = s.view(ctx, r)
	}
	return out, nil
}

// GetRepository returns one Repository with live status.
func (s *Service) GetRepository(ctx context.Context, id uuid.UUID) (RepositoryView, error) {
	r, err := s.q.GetRepository(ctx, store.GetRepositoryParams{ID: id, OrgID: s.opts.OrgID})
	if err != nil {
		return RepositoryView{}, notFound(err)
	}
	return s.view(ctx, r), nil
}

func (s *Service) view(ctx context.Context, r store.Repository) RepositoryView {
	v := RepositoryView{Repository: r}
	v.Usage, _ = s.q.RepositoryUsageByHost(ctx, r.ID)
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	st, err := s.repoClient(r.ManagementUrl).Status(cctx)
	if err != nil {
		v.LiveError = err.Error()
		return v
	}
	v.Live = st
	if st.CertSHA256 != "" && st.CertSHA256 != r.CertSha256 {
		// The reposerver's certificate is stable; a change means its state
		// was replaced. Agents keep pinning the recorded fingerprint.
		v.LiveError = "reposerver certificate fingerprint changed (recorded " + short(r.CertSha256) + ", now " + short(st.CertSHA256) + ")"
	}
	return v
}

func short(s string) string {
	if len(s) > 16 {
		return s[:16] + "…"
	}
	return s
}

// EscrowPackage returns the stored (encrypted) escrow package.
func (s *Service) EscrowPackage(ctx context.Context, p *auth.Principal, id uuid.UUID, m auth.RequestMeta) ([]byte, string, error) {
	r, err := s.q.GetRepository(ctx, store.GetRepositoryParams{ID: id, OrgID: s.opts.OrgID})
	if err != nil {
		return nil, "", notFound(err)
	}
	if len(r.EscrowPackage) == 0 {
		return nil, "", ErrNotFound
	}
	ev := s.event(p, m, audit.RepositoryEscrowExported)
	ev.TargetType, ev.TargetID = "repository", id.String()
	if _, err := s.audit.Record(ctx, ev); err != nil {
		return nil, "", err
	}
	return r.EscrowPackage, "dbr2-escrow-repository-" + safeName(r.Name) + ".age", nil
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
}

// ConfirmEscrow checks the confirmation code from the decrypted package and
// makes the Repository ready (ADR-0008).
func (s *Service) ConfirmEscrow(ctx context.Context, p *auth.Principal, id uuid.UUID, code string, m auth.RequestMeta) (store.Repository, error) {
	var out store.Repository
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		r, err := q.GetRepository(ctx, store.GetRepositoryParams{ID: id, OrgID: s.opts.OrgID})
		if err != nil {
			return notFound(err)
		}
		ev := s.event(p, m, audit.RepositoryEscrowConfirm)
		ev.TargetType, ev.TargetID = "repository", id.String()
		if !escrow.CheckCode(code, r.EscrowConfirmHash) {
			ev.Result = audit.Failure
			if _, err := s.audit.Record(ctx, ev); err != nil { // outside the tx: failures stay recorded
				return err
			}
			return fmt.Errorf("%w: the confirmation code does not match this Repository's escrow package", ErrInvalid)
		}
		out, err = q.ConfirmRepositoryEscrow(ctx, store.ConfirmRepositoryEscrowParams{ID: id, OrgID: s.opts.OrgID, EscrowConfirmedBy: &p.UserID})
		if err != nil {
			return err
		}
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// StartReindex starts the reindex workflow for a Repository (ADR-0003).
func (s *Service) StartReindex(ctx context.Context, p *auth.Principal, id uuid.UUID, m auth.RequestMeta) (string, error) {
	if _, err := s.q.GetRepository(ctx, store.GetRepositoryParams{ID: id, OrgID: s.opts.OrgID}); err != nil {
		return "", notFound(err)
	}
	if s.temporal == nil {
		return "", errors.New("workflow engine unavailable")
	}
	run, err := temporalx.StartRepositoryOperation(ctx, s.temporal, s.opts.TaskQueue, id.String(), "reindex", backup.Reindex, id.String())
	if err != nil {
		return "", err
	}
	ev := s.event(p, m, audit.RepositoryReindexRequest)
	ev.TargetType, ev.TargetID = "repository", id.String()
	ev.Details = map[string]any{"workflow_id": run.GetID(), "run_id": run.GetRunID()}
	if _, err := s.audit.Record(ctx, ev); err != nil {
		return "", err
	}
	return run.GetID(), nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
