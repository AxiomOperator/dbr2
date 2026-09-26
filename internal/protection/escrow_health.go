// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/escrow"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// Escrow health thresholds (ADR-0008).
const (
	EscrowReconfirmAfter = 90 * 24 * time.Hour
	EscrowDrillEvery     = 365 * 24 * time.Hour
)

// EscrowProblem is one escrow health finding.
type EscrowProblem struct {
	Code         string `json:"code" enum:"too_few_recipients,not_confirmed,recipients_changed,reconfirm_due,drill_due"`
	Severity     string `json:"severity" enum:"critical,warning"`
	RepositoryID string `json:"repository_id,omitempty"`
	Message      string `json:"message"`
}

// EscrowHealth is the escrow health report.
type EscrowHealth struct {
	Healthy    bool            `json:"healthy"`
	Problems   []EscrowProblem `json:"problems"`
	Recipients int             `json:"recipients"`
	LastDrill  *time.Time      `json:"last_drill_at"`
	CheckedAt  time.Time       `json:"checked_at"`
}

// CheckEscrowHealth evaluates escrow (pure given its inputs; see
// escrowProblems).
func (s *Service) CheckEscrowHealth(ctx context.Context) (EscrowHealth, error) {
	recips, err := s.q.ListEscrowRecipients(ctx, s.opts.OrgID)
	if err != nil {
		return EscrowHealth{}, err
	}
	repos, err := s.q.ListRepositories(ctx, s.opts.OrgID)
	if err != nil {
		return EscrowHealth{}, err
	}
	var lastDrill *time.Time
	if d, err := s.q.LastCompletedEscrowDrill(ctx, s.opts.OrgID); err == nil {
		lastDrill = d.CompletedAt
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return EscrowHealth{}, err
	}
	now := time.Now()
	h := EscrowHealth{Recipients: len(recips), LastDrill: lastDrill, CheckedAt: now,
		Problems: escrowProblems(recips, repos, lastDrill, s.opts.MinEscrowRecipients, now)}
	h.Healthy = len(h.Problems) == 0
	return h, nil
}

func escrowProblems(recips []store.EscrowRecipient, repos []store.Repository, lastDrill *time.Time, min int, now time.Time) []EscrowProblem {
	out := []EscrowProblem{}
	if len(recips) < min {
		out = append(out, EscrowProblem{Code: "too_few_recipients", Severity: "critical",
			Message: fmt.Sprintf("%d escrow recipients are configured; %d are required", len(recips), min)})
	}
	current := make([]uuid.UUID, 0, len(recips))
	for _, r := range recips {
		current = append(current, r.ID)
	}
	slices.SortFunc(current, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, r := range repos {
		if r.Status == "retired" {
			continue
		}
		got := append([]uuid.UUID(nil), r.EscrowRecipientIds...)
		slices.SortFunc(got, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
		switch {
		case r.EscrowConfirmedAt == nil:
			out = append(out, EscrowProblem{Code: "not_confirmed", Severity: "critical", RepositoryID: r.ID.String(),
				Message: "the escrow package of Repository " + r.Name + " has not been confirmed"})
		case now.Sub(*r.EscrowConfirmedAt) > EscrowReconfirmAfter:
			out = append(out, EscrowProblem{Code: "reconfirm_due", Severity: "warning", RepositoryID: r.ID.String(),
				Message: "escrow of Repository " + r.Name + " was last confirmed more than 90 days ago; re-confirm it"})
		}
		if !slices.Equal(got, current) {
			out = append(out, EscrowProblem{Code: "recipients_changed", Severity: "critical", RepositoryID: r.ID.String(),
				Message: "the escrow recipients changed since the package of Repository " + r.Name + " was generated; regenerate it"})
		}
	}
	if lastDrill == nil || now.Sub(*lastDrill) > EscrowDrillEvery {
		out = append(out, EscrowProblem{Code: "drill_due", Severity: "warning",
			Message: "no escrow drill in the last 12 months: decrypt a drill package with an escrow identity from the safe"})
	}
	return out
}

var escrowAlertMu sync.Mutex
var lastEscrowSignature string

// CheckEscrowAndAlert raises escrow.unhealthy when the set of problems
// changes (called periodically by the server).
func (s *Service) CheckEscrowAndAlert(ctx context.Context) error {
	h, err := s.CheckEscrowHealth(ctx)
	if err != nil {
		return err
	}
	var codes []string
	for _, p := range h.Problems {
		codes = append(codes, p.Code+":"+p.RepositoryID)
	}
	sig := strings.Join(codes, ",")
	escrowAlertMu.Lock()
	changed := sig != lastEscrowSignature
	lastEscrowSignature = sig
	escrowAlertMu.Unlock()
	if !changed || h.Healthy {
		return nil
	}
	sev := "warning"
	var msgs []string
	for _, p := range h.Problems {
		if p.Severity == "critical" {
			sev = "critical"
		}
		msgs = append(msgs, p.Message)
	}
	details := map[string]any{"problems": h.Problems}
	return s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		if _, err := rec.Record(ctx, s.systemEvent(audit.EscrowUnhealthy, "escrow", "platform", audit.Failure, details)); err != nil {
			return err
		}
		return s.alert(ctx, q, sev, audit.EscrowUnhealthy, "escrow", "platform", "Escrow needs attention: "+strings.Join(msgs, "; "), details)
	})
}

// RegenerateEscrow re-seals a Repository's password to the current
// recipients (after recipients change or on schedule). The password is
// read from the reposerver and not stored; the Repository stays usable
// but escrow is unconfirmed until the new package's code is entered.
func (s *Service) RegenerateEscrow(ctx context.Context, p *auth.Principal, id uuid.UUID, m auth.RequestMeta) (*CreatedRepository, error) {
	repo, err := s.q.GetRepository(ctx, store.GetRepositoryParams{ID: id, OrgID: s.opts.OrgID})
	if err != nil {
		return nil, notFound(err)
	}
	recips, err := s.q.ListEscrowRecipients(ctx, s.opts.OrgID)
	if err != nil {
		return nil, err
	}
	if len(recips) < s.opts.MinEscrowRecipients {
		return nil, fmt.Errorf("%w: %d escrow recipients are required", ErrConflict, s.opts.MinEscrowRecipients)
	}
	pw, err := s.repoClient(repo.ManagementUrl).RepositoryPassword(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: reposerver: %v", ErrConflict, err)
	}
	code, err := escrow.NewConfirmationCode()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(recips))
	ids := make([]uuid.UUID, 0, len(recips))
	for _, r := range recips {
		keys, ids = append(keys, r.PublicKey), append(ids, r.ID)
	}
	pkg, err := escrow.Seal(escrow.Payload{Kind: escrow.KindRepositoryPassword, CreatedAt: time.Now().UTC(), RepositoryID: repo.ID.String(),
		RepositoryName: repo.Name, KopiaRepository: deref(repo.KopiaRepositoryID), Secret: pw, ConfirmationCode: code,
		Instructions: escrow.RepositoryInstructions}, keys)
	if err != nil {
		return nil, err
	}
	var out CreatedRepository
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		r, err := q.RegenerateRepositoryEscrow(ctx, store.RegenerateRepositoryEscrowParams{ID: id, OrgID: s.opts.OrgID, EscrowPackage: pkg,
			EscrowRecipientIds: ids, EscrowConfirmHash: escrow.HashCode(code)})
		if err != nil {
			return err
		}
		out = CreatedRepository{Repository: r, EscrowPackage: pkg}
		ev := s.event(p, m, audit.EscrowRegenerated)
		ev.TargetType, ev.TargetID = "repository", id.String()
		ev.Details = map[string]any{"escrow_recipients": len(ids)}
		_, err = rec.Record(ctx, ev)
		return err
	})
	return &out, err
}

// StartEscrowDrill creates a drill package: a random secret sealed to the
// current recipients. An escrow holder decrypts it with the identity from
// the safe and enters the confirmation code (ADR-0008 annual drill).
func (s *Service) StartEscrowDrill(ctx context.Context, p *auth.Principal, m auth.RequestMeta) (store.EscrowDrill, error) {
	recips, err := s.q.ListEscrowRecipients(ctx, s.opts.OrgID)
	if err != nil {
		return store.EscrowDrill{}, err
	}
	if len(recips) == 0 {
		return store.EscrowDrill{}, fmt.Errorf("%w: no escrow recipients are configured", ErrConflict)
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return store.EscrowDrill{}, err
	}
	code, err := escrow.NewConfirmationCode()
	if err != nil {
		return store.EscrowDrill{}, err
	}
	keys := make([]string, 0, len(recips))
	ids := make([]uuid.UUID, 0, len(recips))
	for _, r := range recips {
		keys, ids = append(keys, r.PublicKey), append(ids, r.ID)
	}
	pkg, err := escrow.Seal(escrow.Payload{Kind: "escrow-drill", CreatedAt: time.Now().UTC(), Secret: base64.RawURLEncoding.EncodeToString(b),
		ConfirmationCode: code, Instructions: "DBR² escrow drill: decrypt this file with an escrow identity and enter the confirmation code in DBR². It contains no real secret."}, keys)
	if err != nil {
		return store.EscrowDrill{}, err
	}
	var out store.EscrowDrill
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		d, err := q.CreateEscrowDrill(ctx, store.CreateEscrowDrillParams{OrgID: s.opts.OrgID, Package: pkg, CodeHash: escrow.HashCode(code),
			RecipientIds: ids, CreatedBy: &p.UserID})
		if err != nil {
			return err
		}
		out = d
		ev := s.event(p, m, audit.EscrowDrillStarted)
		ev.TargetType, ev.TargetID = "escrow_drill", d.ID.String()
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// CompleteEscrowDrill checks the drill's confirmation code.
func (s *Service) CompleteEscrowDrill(ctx context.Context, p *auth.Principal, id uuid.UUID, code string, m auth.RequestMeta) (store.EscrowDrill, error) {
	d, err := s.q.GetEscrowDrill(ctx, store.GetEscrowDrillParams{ID: id, OrgID: s.opts.OrgID})
	if err != nil {
		return store.EscrowDrill{}, notFound(err)
	}
	ev := s.event(p, m, audit.EscrowDrillCompleted)
	ev.TargetType, ev.TargetID = "escrow_drill", id.String()
	if !escrow.CheckCode(code, d.CodeHash) {
		ev.Result = audit.Failure
		_, _ = s.audit.Record(ctx, ev)
		return store.EscrowDrill{}, fmt.Errorf("%w: the confirmation code does not match this drill package", ErrInvalid)
	}
	var out store.EscrowDrill
	err = s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		r, err := q.CompleteEscrowDrill(ctx, store.CompleteEscrowDrillParams{ID: id, OrgID: s.opts.OrgID, CompletedBy: &p.UserID})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: the drill was already completed", ErrConflict)
		}
		if err != nil {
			return err
		}
		out = r
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

// ListEscrowDrills lists recent drills.
func (s *Service) ListEscrowDrills(ctx context.Context) ([]store.EscrowDrill, error) {
	return s.q.ListEscrowDrills(ctx, s.opts.OrgID)
}
