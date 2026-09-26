// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/gateway"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// Database strategies (Phase 8).
const (
	StrategyLogical = "logical"
	StrategyVolume  = "volume"
	StrategyBoth    = "both"
)

// DefaultMaxQuiesce is the owner-set default maximum quiesce (ADR-0005).
const DefaultMaxQuiesce = time.Hour

// HookSpec is one configured hook; Container is a service name, container
// name or container ID of the application.
type HookSpec struct {
	Container      string   `json:"container"`
	Command        []string `json:"command"`
	TimeoutSeconds uint32   `json:"timeout_seconds,omitempty"`
	Optional       bool     `json:"optional,omitempty"`
}

// BackupSettings are an application's backup settings.
type BackupSettings struct {
	RepositoryID       *uuid.UUID
	ConsistencyMode    *string // nil = automatic (ADR-0005 minimal-downtime default)
	MaxQuiesceSeconds  int32
	PreHooks           []HookSpec
	PostHooks          []HookSpec
	OptionalComponents []string
	ExcludedComponents []string
	// DatabaseStrategy for detected databases (Phase 8): logical (dumps
	// only; their data volumes are skipped), volume (no dumps) or both
	// (default; the volume copy may be crash-consistent).
	DatabaseStrategy string
	UpdatedAt        *time.Time
}

// EffectiveMode applies the minimal-downtime default: Quiesced when hooks
// are defined, otherwise Live (crash-consistent).
func (b BackupSettings) EffectiveMode() string {
	if b.ConsistencyMode != nil {
		return *b.ConsistencyMode
	}
	if len(b.PreHooks) > 0 || len(b.PostHooks) > 0 {
		return manifest.ModeQuiesced
	}
	return manifest.ModeLive
}

// GetBackupSettings returns an application's settings (defaults if unset).
func (s *Service) GetBackupSettings(ctx context.Context, appID uuid.UUID) (BackupSettings, error) {
	if _, err := s.fleet.GetApplication(ctx, appID); err != nil {
		return BackupSettings{}, err
	}
	return s.backupSettings(ctx, s.q, appID)
}

func (s *Service) backupSettings(ctx context.Context, q *store.Queries, appID uuid.UUID) (BackupSettings, error) {
	row, err := q.GetApplicationBackupSettings(ctx, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return BackupSettings{MaxQuiesceSeconds: int32(DefaultMaxQuiesce / time.Second), DatabaseStrategy: StrategyBoth}, nil
	}
	if err != nil {
		return BackupSettings{}, err
	}
	b := BackupSettings{RepositoryID: row.RepositoryID, ConsistencyMode: row.ConsistencyMode, MaxQuiesceSeconds: row.MaxQuiesceSeconds,
		OptionalComponents: row.OptionalComponents, ExcludedComponents: row.ExcludedComponents, UpdatedAt: &row.UpdatedAt,
		DatabaseStrategy: row.DatabaseStrategy}
	_ = json.Unmarshal(row.PreHooks, &b.PreHooks)
	_ = json.Unmarshal(row.PostHooks, &b.PostHooks)
	return b, nil
}

func validHooks(hs []HookSpec) error {
	for i, h := range hs {
		if strings.TrimSpace(h.Container) == "" || len(h.Command) == 0 || strings.TrimSpace(h.Command[0]) == "" {
			return fmt.Errorf("%w: hook %d needs a container and a command", ErrInvalid, i+1)
		}
		if h.TimeoutSeconds > 3600 {
			return fmt.Errorf("%w: hook %d timeout exceeds 3600 s", ErrInvalid, i+1)
		}
	}
	return nil
}

// PutBackupSettings replaces an application's settings.
func (s *Service) PutBackupSettings(ctx context.Context, p *auth.Principal, appID uuid.UUID, b BackupSettings, m auth.RequestMeta) (BackupSettings, error) {
	if _, err := s.fleet.GetApplication(ctx, appID); err != nil {
		return BackupSettings{}, err
	}
	if b.ConsistencyMode != nil && *b.ConsistencyMode != manifest.ModeLive && *b.ConsistencyMode != manifest.ModeQuiesced && *b.ConsistencyMode != manifest.ModeOffline {
		return BackupSettings{}, fmt.Errorf("%w: consistency_mode must be live, quiesced or offline", ErrInvalid)
	}
	if b.MaxQuiesceSeconds == 0 {
		b.MaxQuiesceSeconds = int32(DefaultMaxQuiesce / time.Second)
	}
	if b.DatabaseStrategy == "" {
		b.DatabaseStrategy = StrategyBoth
	}
	if b.DatabaseStrategy != StrategyBoth && b.DatabaseStrategy != StrategyLogical && b.DatabaseStrategy != StrategyVolume {
		return BackupSettings{}, fmt.Errorf("%w: database_strategy must be logical, volume or both", ErrInvalid)
	}
	if b.MaxQuiesceSeconds < 60 || b.MaxQuiesceSeconds > 86400 {
		return BackupSettings{}, fmt.Errorf("%w: max_quiesce_seconds must be 60–86400", ErrInvalid)
	}
	if err := errors.Join(validHooks(b.PreHooks), validHooks(b.PostHooks)); err != nil {
		return BackupSettings{}, err
	}
	if b.RepositoryID != nil {
		if _, err := s.q.GetRepository(ctx, store.GetRepositoryParams{ID: *b.RepositoryID, OrgID: s.opts.OrgID}); err != nil {
			return BackupSettings{}, fmt.Errorf("%w: unknown repository", ErrInvalid)
		}
	}
	pre, _ := json.Marshal(nonNil(b.PreHooks))
	post, _ := json.Marshal(nonNil(b.PostHooks))
	var out BackupSettings
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		before, err := s.backupSettings(ctx, q, appID)
		if err != nil {
			return err
		}
		if _, err := q.UpsertApplicationBackupSettings(ctx, store.UpsertApplicationBackupSettingsParams{ApplicationID: appID,
			RepositoryID: b.RepositoryID, ConsistencyMode: b.ConsistencyMode, MaxQuiesceSeconds: b.MaxQuiesceSeconds,
			PreHooks: pre, PostHooks: post, OptionalComponents: nonNilS(b.OptionalComponents), ExcludedComponents: nonNilS(b.ExcludedComponents),
			UpdatedBy: &p.UserID, DatabaseStrategy: b.DatabaseStrategy}); err != nil {
			return err
		}
		if out, err = s.backupSettings(ctx, q, appID); err != nil {
			return err
		}
		ev := s.event(p, m, audit.BackupSettingsUpdated)
		ev.TargetType, ev.TargetID, ev.Before, ev.After = "application", appID.String(), before, out
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

func nonNil(h []HookSpec) []HookSpec {
	if h == nil {
		return []HookSpec{}
	}
	return h
}

func nonNilS(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// HostSettings are a host's limits.
type HostSettings struct {
	MaxConcurrentJobs int32
	// Backup window in minutes of the day (both nil = always open). A
	// window may wrap midnight (start > end).
	WindowStart *int32
	WindowEnd   *int32
	Timezone    string
}

// GetHostSettings returns a host's limits (defaults if unset).
func (s *Service) GetHostSettings(ctx context.Context, agentID uuid.UUID) (HostSettings, error) {
	if _, err := s.fleet.GetAgent(ctx, agentID); err != nil {
		return HostSettings{}, err
	}
	return s.hostSettings(ctx, s.q, agentID)
}

func (s *Service) hostSettings(ctx context.Context, q *store.Queries, agentID uuid.UUID) (HostSettings, error) {
	row, err := q.GetHostSettings(ctx, agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return HostSettings{MaxConcurrentJobs: gateway.DefaultMaxConcurrentJobs, Timezone: "UTC"}, nil
	}
	if err != nil {
		return HostSettings{}, err
	}
	return HostSettings{MaxConcurrentJobs: row.MaxConcurrentJobs, WindowStart: row.BackupWindowStart, WindowEnd: row.BackupWindowEnd, Timezone: row.BackupWindowTimezone}, nil
}

// PutHostSettings replaces a host's limits; the agent reconnects to pick
// up a new concurrency limit.
func (s *Service) PutHostSettings(ctx context.Context, p *auth.Principal, agentID uuid.UUID, h HostSettings, m auth.RequestMeta) (HostSettings, error) {
	if _, err := s.fleet.GetAgent(ctx, agentID); err != nil {
		return HostSettings{}, err
	}
	if h.MaxConcurrentJobs < 1 || h.MaxConcurrentJobs > 16 {
		return HostSettings{}, fmt.Errorf("%w: max_concurrent_jobs must be 1–16", ErrInvalid)
	}
	if (h.WindowStart == nil) != (h.WindowEnd == nil) {
		return HostSettings{}, fmt.Errorf("%w: set both backup window start and end, or neither", ErrInvalid)
	}
	for _, v := range []*int32{h.WindowStart, h.WindowEnd} {
		if v != nil && (*v < 0 || *v > 1439) {
			return HostSettings{}, fmt.Errorf("%w: backup window bounds are minutes of the day (0–1439)", ErrInvalid)
		}
	}
	if h.Timezone == "" {
		h.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(h.Timezone); err != nil {
		return HostSettings{}, fmt.Errorf("%w: unknown timezone %q", ErrInvalid, h.Timezone)
	}
	var out HostSettings
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		before, err := s.hostSettings(ctx, q, agentID)
		if err != nil {
			return err
		}
		if _, err := q.UpsertHostSettings(ctx, store.UpsertHostSettingsParams{AgentID: agentID, MaxConcurrentJobs: h.MaxConcurrentJobs,
			BackupWindowStart: h.WindowStart, BackupWindowEnd: h.WindowEnd, BackupWindowTimezone: h.Timezone, UpdatedBy: &p.UserID}); err != nil {
			return err
		}
		out = h
		ev := s.event(p, m, audit.HostSettingsUpdated)
		ev.TargetType, ev.TargetID, ev.Before, ev.After = "agent", agentID.String(), before, out
		_, err = rec.Record(ctx, ev)
		return err
	})
	if err == nil && s.gw != nil {
		s.gw.ForceReconnect(agentID.String())
	}
	return out, err
}

// WaitForWindow returns how long to wait until the window opens (0 when
// open or unset).
func (h HostSettings) WaitForWindow(now time.Time) time.Duration {
	if h.WindowStart == nil || h.WindowEnd == nil {
		return 0
	}
	loc, err := time.LoadLocation(h.Timezone)
	if err != nil {
		loc = time.UTC
	}
	t := now.In(loc)
	minute := int32(t.Hour()*60 + t.Minute())
	start, end := *h.WindowStart, *h.WindowEnd
	open := start <= end && minute >= start && minute < end || start > end && (minute >= start || minute < end)
	if open || start == end {
		return 0
	}
	wait := start - minute
	if wait < 0 {
		wait += 1440
	}
	return time.Duration(wait)*time.Minute - time.Duration(t.Second())*time.Second
}
