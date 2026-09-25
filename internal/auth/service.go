// SPDX-License-Identifier: Apache-2.0

// Package auth implements DBR² authentication: the local master admin
// (username + password, Argon2id, lockout, optional TOTP), Entra ID via OIDC,
// server-side sessions and personal API tokens.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// Errors mapped to API responses.
var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrTOTPRequired       = errors.New("TOTP code required")
	ErrInvalidTOTP        = errors.New("invalid TOTP code")
	ErrNotLocalAccount    = errors.New("operation only available to the master admin")
	ErrTOTPNotPending     = errors.New("no TOTP enrollment in progress")
	ErrNotFound           = errors.New("not found")
	// ErrWrongCurrentPassword is returned by ChangePassword (400, not 401, so
	// clients do not mistake it for an expired session).
	ErrWrongCurrentPassword = errors.New("current password is incorrect")
)

// LockedError reports a locked account.
type LockedError struct{ RetryAfter time.Duration }

func (e *LockedError) Error() string {
	return fmt.Sprintf("account locked; retry after %s", e.RetryAfter.Round(time.Second))
}

// RateLimitedError reports throttled login attempts from one source.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string { return "too many login attempts" }

// Options configure the Service.
type Options struct {
	OrgID               uuid.UUID
	MasterAdminUsername string
	SessionTTL          time.Duration
	SessionIdle         time.Duration
	SecretKey           []byte
	// LoginRateLimit is the number of login attempts allowed per source IP
	// per minute.
	LoginRateLimit int
}

// Service is the authentication service.
type Service struct {
	pool    *pgxpool.Pool
	q       *store.Queries
	audit   *audit.Recorder
	box     *SecretBox
	opts    Options
	limiter *RateLimiter
	log     *slog.Logger
	now     func() time.Time
	oidc    map[string]*OIDCProvider
}

// NewService builds the service.
func NewService(pool *pgxpool.Pool, rec *audit.Recorder, log *slog.Logger, opts Options) (*Service, error) {
	box, err := NewSecretBox(opts.SecretKey)
	if err != nil {
		return nil, err
	}
	if opts.LoginRateLimit <= 0 {
		opts.LoginRateLimit = 10
	}
	return &Service{
		pool: pool, q: store.New(pool), audit: rec, box: box, opts: opts,
		limiter: NewRateLimiter(opts.LoginRateLimit, time.Minute),
		log:     log, now: time.Now, oidc: map[string]*OIDCProvider{},
	}, nil
}

// OrgID returns the organization the service operates on.
func (s *Service) OrgID() uuid.UUID { return s.opts.OrgID }

// inTx runs fn in a transaction with a querier and a transaction-bound audit
// recorder, so an action and its audit record commit atomically.
func (s *Service) inTx(ctx context.Context, fn func(q *store.Queries, rec *audit.Recorder) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		return fn(q, s.audit.WithQuerier(q))
	})
}

// RequestMeta carries request context for auditing.
type RequestMeta struct {
	IP        netip.Addr
	UserAgent string
	RequestID string
}

// ---- Master admin bootstrap -------------------------------------------------

// EnsureMasterAdmin creates the master admin on first start. The generated
// initial password is handed to deliver (which writes it to a root-only file);
// it is never logged.
func (s *Service) EnsureMasterAdmin(ctx context.Context, deliver func(username, password string) error) (created bool, err error) {
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		if _, err := q.GetMasterAdmin(ctx, s.opts.OrgID); err == nil {
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		pw, err := GeneratePassword()
		if err != nil {
			return err
		}
		hash, err := HashPassword(pw)
		if err != nil {
			return err
		}
		u, err := q.CreateMasterAdmin(ctx, store.CreateMasterAdminParams{
			OrgID: s.opts.OrgID, Username: s.opts.MasterAdminUsername, DisplayName: "Master Admin",
		})
		if err != nil {
			return err
		}
		if err := q.CreateLocalCredential(ctx, store.CreateLocalCredentialParams{UserID: u.ID, PasswordHash: hash}); err != nil {
			return err
		}
		if _, err := rec.Record(ctx, audit.Event{
			OrgID: s.opts.OrgID, Type: audit.MasterAdminBootstrapped, ActorDisplay: "dbr2-server", ActorKind: audit.ActorSystem,
			TargetType: "user", TargetID: u.ID.String(), Result: audit.Success,
			Details: map[string]any{"username": u.Username},
		}); err != nil {
			return err
		}
		if err := deliver(u.Username, pw); err != nil {
			return fmt.Errorf("deliver initial master admin password: %w", err)
		}
		created = true
		return nil
	})
	return created, err
}

// ResetMasterPassword sets a new master admin password from the server host
// (`dbr2-server admin reset-master-password`), clears lockout, optionally
// disables TOTP and revokes every session.
func (s *Service) ResetMasterPassword(ctx context.Context, newPassword string, disableTOTP bool, operator string) error {
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		u, err := q.GetMasterAdmin(ctx, s.opts.OrgID)
		if err != nil {
			return fmt.Errorf("master admin not found: %w", err)
		}
		if err := CheckPasswordPolicy(newPassword, u.Username); err != nil {
			return err
		}
		hash, err := HashPassword(newPassword)
		if err != nil {
			return err
		}
		if err := q.UpdatePassword(ctx, store.UpdatePasswordParams{UserID: u.ID, PasswordHash: hash}); err != nil {
			return err
		}
		if disableTOTP {
			if err := q.DisableTOTP(ctx, u.ID); err != nil {
				return err
			}
		}
		if err := q.RevokeUserSessions(ctx, u.ID); err != nil {
			return err
		}
		_, err = rec.Record(ctx, audit.Event{
			OrgID: s.opts.OrgID, Type: audit.MasterAdminPasswordReset, ActorDisplay: operator, ActorKind: audit.ActorSystem,
			TargetType: "user", TargetID: u.ID.String(), Result: audit.Success,
			Reason: "offline reset on the server host", Details: map[string]any{"totp_disabled": disableTOTP},
		})
		return err
	})
}

// ---- Password login ---------------------------------------------------------

// LoginInput is a master admin login attempt.
type LoginInput struct {
	Username string
	Password string
	TOTPCode string
	Meta     RequestMeta
}

// Session is a newly issued session.
type Session struct {
	Token     string
	ExpiresAt time.Time
	Principal *Principal
}

// LoginPassword authenticates the master admin and issues a session.
func (s *Service) LoginPassword(ctx context.Context, in LoginInput) (*Session, error) {
	anon := audit.Event{OrgID: s.opts.OrgID, ActorDisplay: in.Username, ActorKind: audit.ActorAnonymous,
		SourceIP: in.Meta.IP, RequestID: in.Meta.RequestID, TargetType: "user"}

	if ok, wait := s.limiter.Allow(ipKey(in.Meta.IP)); !ok {
		ev := anon
		ev.Type, ev.Result = audit.LoginRateLimited, audit.Denied
		if _, err := s.audit.Record(ctx, ev); err != nil {
			return nil, err
		}
		return nil, &RateLimitedError{RetryAfter: wait}
	}

	var (
		sess     *Session
		loginErr error
	)
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		u, err := q.GetMasterAdmin(ctx, s.opts.OrgID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !strings.EqualFold(u.Username, in.Username)) {
			_, _ = VerifyPassword(in.Password, dummyHash) // equalise timing
			ev := anon
			ev.Type, ev.Result, ev.Reason = audit.LoginFailed, audit.Failure, "unknown username"
			if _, err := rec.Record(ctx, ev); err != nil {
				return err
			}
			loginErr = ErrInvalidCredentials
			return nil
		} else if err != nil {
			return err
		}
		ev := anon
		ev.ActorUserID, ev.ActorDisplay, ev.TargetID = &u.ID, u.Username, u.ID.String()

		cred, err := q.GetLocalCredentialForUpdate(ctx, u.ID)
		if err != nil {
			return err
		}
		now := s.now()
		if cred.LockedUntil != nil && cred.LockedUntil.After(now) {
			ev.Type, ev.Result, ev.Reason = audit.LoginLocked, audit.Denied, "account locked"
			if _, err := rec.Record(ctx, ev); err != nil {
				return err
			}
			loginErr = &LockedError{RetryAfter: cred.LockedUntil.Sub(now)}
			return nil
		}

		fail := func(reason string, e error) error {
			n := int(cred.FailedAttempts) + 1
			var until *time.Time
			if d := LockDuration(n); d > 0 {
				t := now.Add(d)
				until = &t
			}
			if err := q.RecordLoginFailure(ctx, store.RecordLoginFailureParams{UserID: u.ID, FailedAttempts: int32(n), LockedUntil: until}); err != nil {
				return err
			}
			ev.Type, ev.Result, ev.Reason = audit.LoginFailed, audit.Failure, reason
			ev.Details = map[string]any{"failed_attempts": n, "locked": until != nil}
			if _, err := rec.Record(ctx, ev); err != nil {
				return err
			}
			loginErr = e
			return nil
		}

		ok, err := VerifyPassword(in.Password, cred.PasswordHash)
		if err != nil {
			return err
		}
		if !ok {
			return fail("wrong password", ErrInvalidCredentials)
		}
		if cred.TotpEnabled {
			if in.TOTPCode == "" {
				loginErr = ErrTOTPRequired // password correct; not counted as a failure
				return nil
			}
			secret, err := s.openTOTP(cred.TotpSecretEnc, u.ID)
			if err != nil {
				return err
			}
			step, ok := MatchTOTP(secret, in.TOTPCode, now)
			if !ok {
				return fail("wrong TOTP code", ErrInvalidTOTP)
			}
			n, err := q.AdvanceTOTPStep(ctx, store.AdvanceTOTPStepParams{UserID: u.ID, TotpLastStep: step})
			if err != nil {
				return err
			}
			if n == 0 {
				return fail("replayed TOTP code", ErrInvalidTOTP)
			}
		}

		if err := q.ResetLoginFailures(ctx, u.ID); err != nil {
			return err
		}
		if err := q.TouchUserLogin(ctx, u.ID); err != nil {
			return err
		}
		sess, err = s.createSession(ctx, q, u.ID, "password", in.Meta)
		if err != nil {
			return err
		}
		ev.Type, ev.Result = audit.LoginSucceeded, audit.Success
		ev.ActorKind = audit.ActorUser
		ev.Details = map[string]any{"method": "password", "totp": cred.TotpEnabled}
		if _, err := rec.Record(ctx, ev); err != nil {
			return err
		}
		// Every master admin login raises a notification (final_stack →
		// Authentication); delivery channels arrive in Phase 7.
		return q.EnqueueNotification(ctx, store.EnqueueNotificationParams{
			OrgID: s.opts.OrgID, EventType: "auth.master_admin.login", Severity: "warning",
			Payload: mustJSON(map[string]any{"username": u.Username, "source_ip": ipKey(in.Meta.IP), "at": now.UTC()}),
		})
	})
	if err != nil {
		return nil, err
	}
	if loginErr != nil {
		return nil, loginErr
	}
	return sess, nil
}

func (s *Service) createSession(ctx context.Context, q *store.Queries, userID uuid.UUID, method string, m RequestMeta) (*Session, error) {
	tok, err := NewToken(SessionTokenPrefix)
	if err != nil {
		return nil, err
	}
	exp := s.now().Add(s.opts.SessionTTL)
	var ip *netip.Addr
	if m.IP.IsValid() {
		ip = &m.IP
	}
	ua := truncate(m.UserAgent, 512)
	if err := q.CreateSession(ctx, store.CreateSessionParams{
		ID: HashToken(tok), UserID: userID, AuthMethod: method, ExpiresAt: exp, SourceIp: ip, UserAgent: &ua,
	}); err != nil {
		return nil, err
	}
	p, err := s.loadPrincipal(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	p.SessionID = HashToken(tok)
	return &Session{Token: tok, ExpiresAt: exp, Principal: p}, nil
}

// ---- Credential resolution --------------------------------------------------

// ResolveSession validates a session token and returns its principal.
func (s *Service) ResolveSession(ctx context.Context, token string) (*Principal, error) {
	if !HasPrefix(token, SessionTokenPrefix) {
		return nil, ErrNotFound
	}
	id := HashToken(token)
	row, err := s.q.GetActiveSession(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	now := s.now()
	if now.Sub(row.LastSeenAt) > s.opts.SessionIdle {
		_ = s.q.RevokeSession(ctx, id)
		return nil, ErrNotFound
	}
	if now.Sub(row.LastSeenAt) > time.Minute { // throttle writes
		if err := s.q.TouchSession(ctx, id); err != nil {
			return nil, err
		}
	}
	p, err := s.loadPrincipal(ctx, s.q, row.UserID)
	if err != nil {
		return nil, err
	}
	p.SessionID = id
	return p, nil
}

// ResolveAPIToken validates a personal API token.
func (s *Service) ResolveAPIToken(ctx context.Context, token string) (*Principal, error) {
	if !HasPrefix(token, APITokenPrefix) {
		return nil, ErrNotFound
	}
	row, err := s.q.GetActiveAPIToken(ctx, HashToken(token))
	if err != nil {
		return nil, ErrNotFound
	}
	if err := s.q.TouchAPIToken(ctx, row.ID); err != nil {
		return nil, err
	}
	p, err := s.loadPrincipal(ctx, s.q, row.UserID)
	if err != nil {
		return nil, err
	}
	p.Credential, p.APITokenID = CredentialAPIToken, row.ID
	return p, nil
}

func (s *Service) loadPrincipal(ctx context.Context, q *store.Queries, userID uuid.UUID) (*Principal, error) {
	u, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.Disabled {
		return nil, ErrNotFound
	}
	rows, err := q.ListUserRoles(ctx, userID)
	if err != nil {
		return nil, err
	}
	seen := map[rbac.Role]bool{}
	var roles []rbac.Role
	add := func(r rbac.Role) {
		if !seen[r] && rbac.Valid(r) {
			seen[r] = true
			roles = append(roles, r)
		}
	}
	if u.Kind == KindMasterAdmin {
		add(rbac.Administrator) // cannot be demoted
	}
	for _, r := range rows {
		add(rbac.Role(r.Role))
	}
	p := &Principal{
		UserID: u.ID, OrgID: u.OrgID, Username: u.Username, DisplayName: u.DisplayName,
		Kind: u.Kind, Roles: roles, Permissions: rbac.Resolve(roles),
	}
	if u.Email != nil {
		p.Email = *u.Email
	}
	if u.Kind == KindMasterAdmin {
		cred, err := q.GetLocalCredential(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		p.TOTPEnabled = cred.TotpEnabled
	}
	return p, nil
}

// Logout revokes the principal's current session.
func (s *Service) Logout(ctx context.Context, p *Principal, m RequestMeta) error {
	if p.SessionID == nil {
		return nil
	}
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		if err := q.RevokeSession(ctx, p.SessionID); err != nil {
			return err
		}
		_, err := rec.Record(ctx, s.userEvent(p, m, audit.Logout, audit.Success))
		return err
	})
}

// ---- Master admin self-service ----------------------------------------------

// ChangePassword changes the master admin password and revokes all other sessions.
func (s *Service) ChangePassword(ctx context.Context, p *Principal, current, next string, m RequestMeta) error {
	if !p.IsMasterAdmin() {
		return ErrNotLocalAccount
	}
	if err := CheckPasswordPolicy(next, p.Username); err != nil {
		return err
	}
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		cred, err := q.GetLocalCredentialForUpdate(ctx, p.UserID)
		if err != nil {
			return err
		}
		ok, err := VerifyPassword(current, cred.PasswordHash)
		if err != nil {
			return err
		}
		if !ok {
			ev := s.userEvent(p, m, audit.PasswordChanged, audit.Failure)
			ev.Reason = "current password incorrect"
			if _, err := rec.Record(ctx, ev); err != nil {
				return err
			}
			return ErrWrongCurrentPassword
		}
		hash, err := HashPassword(next)
		if err != nil {
			return err
		}
		if err := q.UpdatePassword(ctx, store.UpdatePasswordParams{UserID: p.UserID, PasswordHash: hash}); err != nil {
			return err
		}
		if p.SessionID != nil {
			if err := q.RevokeUserSessionsExcept(ctx, store.RevokeUserSessionsExceptParams{UserID: p.UserID, ID: p.SessionID}); err != nil {
				return err
			}
		}
		_, err = rec.Record(ctx, s.userEvent(p, m, audit.PasswordChanged, audit.Success))
		return err
	})
}

// TOTPEnrollment is a pending TOTP setup.
type TOTPEnrollment struct {
	Secret     string
	OTPAuthURL string
}

// BeginTOTP starts TOTP enrollment for the master admin.
func (s *Service) BeginTOTP(ctx context.Context, p *Principal) (*TOTPEnrollment, error) {
	if !p.IsMasterAdmin() {
		return nil, ErrNotLocalAccount
	}
	key, err := NewTOTPKey(p.Username)
	if err != nil {
		return nil, err
	}
	sealed, err := s.box.Seal([]byte(key.Secret()), totpAD("pending", p.UserID))
	if err != nil {
		return nil, err
	}
	if err := s.q.SetPendingTOTP(ctx, store.SetPendingTOTPParams{UserID: p.UserID, TotpPendingSecretEnc: sealed}); err != nil {
		return nil, err
	}
	return &TOTPEnrollment{Secret: key.Secret(), OTPAuthURL: key.URL()}, nil
}

// ConfirmTOTP completes enrollment with a valid code.
func (s *Service) ConfirmTOTP(ctx context.Context, p *Principal, code string, m RequestMeta) error {
	if !p.IsMasterAdmin() {
		return ErrNotLocalAccount
	}
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		cred, err := q.GetLocalCredentialForUpdate(ctx, p.UserID)
		if err != nil {
			return err
		}
		if cred.TotpPendingSecretEnc == nil {
			return ErrTOTPNotPending
		}
		secret, err := s.box.Open(cred.TotpPendingSecretEnc, totpAD("pending", p.UserID))
		if err != nil {
			return err
		}
		step, ok := MatchTOTP(string(secret), code, s.now())
		if !ok {
			return ErrInvalidTOTP
		}
		// Re-seal under the active purpose so pending and active values
		// cannot be confused.
		active, err := s.box.Seal(secret, totpAD("active", p.UserID))
		if err != nil {
			return err
		}
		if err := q.SetPendingTOTP(ctx, store.SetPendingTOTPParams{UserID: p.UserID, TotpPendingSecretEnc: active}); err != nil {
			return err
		}
		if err := q.EnableTOTP(ctx, store.EnableTOTPParams{UserID: p.UserID, TotpLastStep: step}); err != nil {
			return err
		}
		_, err = rec.Record(ctx, s.userEvent(p, m, audit.TOTPEnabled, audit.Success))
		return err
	})
}

// DisableTOTP turns TOTP off after verifying a current code.
func (s *Service) DisableTOTP(ctx context.Context, p *Principal, code string, m RequestMeta) error {
	if !p.IsMasterAdmin() {
		return ErrNotLocalAccount
	}
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		cred, err := q.GetLocalCredentialForUpdate(ctx, p.UserID)
		if err != nil {
			return err
		}
		if !cred.TotpEnabled {
			return nil
		}
		secret, err := s.openTOTP(cred.TotpSecretEnc, p.UserID)
		if err != nil {
			return err
		}
		step, ok := MatchTOTP(secret, code, s.now())
		if !ok || step <= cred.TotpLastStep {
			return ErrInvalidTOTP
		}
		if err := q.DisableTOTP(ctx, p.UserID); err != nil {
			return err
		}
		_, err = rec.Record(ctx, s.userEvent(p, m, audit.TOTPDisabled, audit.Success))
		return err
	})
}

func (s *Service) openTOTP(sealed []byte, userID uuid.UUID) (string, error) {
	b, err := s.box.Open(sealed, totpAD("active", userID))
	return string(b), err
}

func totpAD(state string, userID uuid.UUID) string {
	return "dbr2:totp:" + state + ":" + userID.String()
}

// ---- API tokens ---------------------------------------------------------------

// APIToken describes a personal API token (the secret is shown once).
type APIToken struct {
	store.CreateAPITokenRow
	Token string
}

// CreateAPIToken issues a personal API token for p.
func (s *Service) CreateAPIToken(ctx context.Context, p *Principal, name string, ttl time.Duration, m RequestMeta) (*APIToken, error) {
	tok, err := NewToken(APITokenPrefix)
	if err != nil {
		return nil, err
	}
	var exp *time.Time
	if ttl > 0 {
		t := s.now().Add(ttl)
		exp = &t
	}
	var out *APIToken
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		row, err := q.CreateAPIToken(ctx, store.CreateAPITokenParams{
			UserID: p.UserID, Name: name, TokenHash: HashToken(tok), Prefix: tok[:len(APITokenPrefix)+6], ExpiresAt: exp,
		})
		if err != nil {
			return err
		}
		out = &APIToken{CreateAPITokenRow: row, Token: tok}
		ev := s.userEvent(p, m, audit.APITokenCreated, audit.Success)
		ev.TargetType, ev.TargetID = "api_token", row.ID.String()
		ev.Details = map[string]any{"name": name, "expires_at": exp}
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// ListAPITokens lists p's tokens.
func (s *Service) ListAPITokens(ctx context.Context, p *Principal) ([]store.ListUserAPITokensRow, error) {
	return s.q.ListUserAPITokens(ctx, p.UserID)
}

// RevokeAPIToken revokes one of p's tokens.
func (s *Service) RevokeAPIToken(ctx context.Context, p *Principal, id uuid.UUID, m RequestMeta) error {
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		n, err := q.RevokeAPIToken(ctx, store.RevokeAPITokenParams{ID: id, UserID: p.UserID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		ev := s.userEvent(p, m, audit.APITokenRevoked, audit.Success)
		ev.TargetType, ev.TargetID = "api_token", id.String()
		_, err = rec.Record(ctx, ev)
		return err
	})
}

// ---- helpers ------------------------------------------------------------------

func (s *Service) userEvent(p *Principal, m RequestMeta, typ, result string) audit.Event {
	return audit.Event{
		OrgID: p.OrgID, Type: typ, ActorUserID: &p.UserID, ActorDisplay: p.Username, ActorKind: audit.ActorUser,
		SourceIP: m.IP, RequestID: m.RequestID, TargetType: "user", TargetID: p.UserID.String(), Result: result,
	}
}

// UserEvent builds an audit event attributed to p (used by other packages).
func (s *Service) UserEvent(p *Principal, m RequestMeta, typ, result string) audit.Event {
	return s.userEvent(p, m, typ, result)
}

func ipKey(a netip.Addr) string {
	if !a.IsValid() {
		return "unknown"
	}
	return a.String()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
