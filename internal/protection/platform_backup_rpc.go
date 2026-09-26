// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/audit"
	"github.com/AxiomOperator/dbr2/internal/auth"
	"github.com/AxiomOperator/dbr2/internal/platform"
	"github.com/AxiomOperator/dbr2/internal/store"
	"github.com/AxiomOperator/dbr2/internal/temporalx"
	platformwf "github.com/AxiomOperator/dbr2/workflows/platform"
)

// PlatformExporter produces the encrypted Platform Recovery Bundle
// (internal/platform.Exporter).
type PlatformExporter interface {
	Export(ctx context.Context, w io.Writer) (*platform.Result, error)
}

type platformExporterBox struct{ e PlatformExporter }

// SetPlatformExporter enables ExportPlatform (ADR-0008 platform self-backup).
func (s *Service) SetPlatformExporter(e PlatformExporter) {
	s.platformExp.Store(&platformExporterBox{e: e})
}

func (s *Service) platformExporter() PlatformExporter {
	if b := s.platformExp.Load(); b != nil {
		return b.e
	}
	return nil
}

// Platform backup run limits.
const (
	// platformChunkSize is the ExportPlatform message size.
	platformChunkSize = 1 << 20
	// platformStaleAfter marks a still-running record as abandoned.
	platformStaleAfter = 6 * time.Hour
)

// BeginPlatformBackup records a running platform backup (idempotent for
// retries of the same workflow run).
func (p *Platform) BeginPlatformBackup(ctx context.Context, req *controlv1.BeginPlatformBackupRequest) (*controlv1.BeginPlatformBackupResponse, error) {
	s := p.s
	if req.WorkflowId == "" || req.RunId == "" {
		return nil, status.Error(codes.InvalidArgument, "workflow_id and run_id are required")
	}
	var resp *controlv1.BeginPlatformBackupResponse
	err := s.inTx(ctx, func(q *store.Queries, _ *audit.Recorder) error {
		if _, err := q.AbandonRunningPlatformBackups(ctx, store.AbandonRunningPlatformBackupsParams{
			OrgID: s.opts.OrgID, StartedAt: s.now().Add(-platformStaleAfter)}); err != nil {
			return err
		}
		var sys *store.Repository
		if r, err := q.GetSystemRepository(ctx, s.opts.OrgID); err == nil {
			sys = &r
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		running, err := q.GetRunningPlatformBackup(ctx, s.opts.OrgID)
		switch {
		case err == nil && pbStr(running.WorkflowID) == req.WorkflowId && pbStr(running.RunID) == req.RunId:
			resp = &controlv1.BeginPlatformBackupResponse{BackupId: running.ID}
		case err == nil:
			return fmt.Errorf("%w: platform backup %s is already running", ErrConflict, running.ID)
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		default:
			var by, repoID *uuid.UUID
			if u, err := uuid.Parse(req.RequestedBy); err == nil {
				by = &u
			}
			if sys != nil {
				repoID = &sys.ID
			}
			trigger := req.Trigger
			if trigger == "" {
				trigger = "scheduled"
			}
			row, err := q.CreatePlatformBackup(ctx, store.CreatePlatformBackupParams{ID: "pb_" + ulid.Make().String(), OrgID: s.opts.OrgID,
				Trigger: trigger, RequestedBy: by, WorkflowID: &req.WorkflowId, RunID: &req.RunId, RepositoryID: repoID})
			if err != nil {
				return err
			}
			resp = &controlv1.BeginPlatformBackupResponse{BackupId: row.ID}
		}
		if sys != nil {
			resp.SystemRepository = repoMsg(*sys)
		}
		return nil
	})
	if err != nil {
		return nil, grpcErr(err)
	}
	return resp, nil
}

// chunkSender batches writes into ~1 MiB ExportPlatform messages.
type chunkSender struct {
	stream grpc.ServerStreamingServer[controlv1.ExportPlatformResponse]
	buf    []byte
}

func (c *chunkSender) Write(b []byte) (int, error) {
	n := len(b)
	for len(b) > 0 {
		k := min(platformChunkSize-len(c.buf), len(b))
		c.buf = append(c.buf, b[:k]...)
		b = b[k:]
		if len(c.buf) == platformChunkSize {
			if err := c.flush(); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

func (c *chunkSender) flush() error {
	if len(c.buf) == 0 {
		return nil
	}
	err := c.stream.Send(&controlv1.ExportPlatformResponse{Data: c.buf})
	c.buf = make([]byte, 0, platformChunkSize)
	return err
}

// ExportPlatform streams the encrypted Platform Recovery Bundle.
func (p *Platform) ExportPlatform(req *controlv1.ExportPlatformRequest, stream grpc.ServerStreamingServer[controlv1.ExportPlatformResponse]) error {
	s := p.s
	ctx := stream.Context()
	row, err := s.q.GetPlatformBackup(ctx, req.BackupId)
	if err != nil || row.OrgID != s.opts.OrgID {
		return status.Errorf(codes.NotFound, "platform backup %q not found", req.BackupId)
	}
	if row.State != "running" {
		return precondition("platform backup %s is %s", row.ID, row.State)
	}
	exp := s.platformExporter()
	if exp == nil {
		return precondition("platform export is not configured on this dbr2-server")
	}
	cs := &chunkSender{stream: stream, buf: make([]byte, 0, platformChunkSize)}
	res, err := exp.Export(ctx, cs)
	if err != nil {
		if ctx.Err() != nil {
			return status.FromContextError(ctx.Err()).Err()
		}
		if strings.Contains(err.Error(), "no escrow recipients") {
			return precondition("%v", err)
		}
		return status.Errorf(codes.Internal, "platform export: %v", err)
	}
	if err := cs.flush(); err != nil {
		return err
	}
	if err := s.q.SetPlatformBackupManifest(ctx, store.SetPlatformBackupManifestParams{ID: row.ID, Manifest: res.ManifestJSON}); err != nil {
		return grpcErr(err)
	}
	return stream.Send(&controlv1.ExportPlatformResponse{Summary: &controlv1.ExportPlatformSummary{
		ManifestJson: res.ManifestJSON, MissingRepositoryIds: res.Manifest.MissingRepositories()}})
}

// RecordPlatformBackup stores a run's outcome with its audit event, and an
// alert when it is partial (warning) or failed (critical).
func (p *Platform) RecordPlatformBackup(ctx context.Context, req *controlv1.RecordPlatformBackupRequest) (*controlv1.RecordPlatformBackupResponse, error) {
	s := p.s
	state := req.State
	switch state {
	case platformwf.StateSucceeded, platformwf.StatePartial, platformwf.StateFailed:
	default:
		return nil, status.Errorf(codes.InvalidArgument, "state must be succeeded, partial or failed")
	}
	var final string
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		row, err := q.GetPlatformBackup(ctx, req.BackupId)
		if err != nil || row.OrgID != s.opts.OrgID {
			return fmt.Errorf("%w: platform backup %q", ErrNotFound, req.BackupId)
		}
		if row.State != "running" { // retry of an already recorded outcome
			final = row.State
			return nil
		}
		var missing []string
		if len(row.Manifest) > 0 {
			var m platform.Manifest
			if json.Unmarshal(row.Manifest, &m) == nil {
				missing = m.MissingRepositories()
			}
		}
		errText := req.Error
		if state == platformwf.StateSucceeded && len(missing) > 0 {
			state = platformwf.StatePartial
		}
		if len(missing) > 0 && state != platformwf.StateFailed {
			errText = joinNonEmpty("; ", errText, "reposerver state missing for Repositories "+strings.Join(missing, ", "))
		}
		var repoID *uuid.UUID
		if u, err := uuid.Parse(req.RepositoryId); err == nil {
			repoID = &u
		}
		if _, err := q.FinishPlatformBackup(ctx, store.FinishPlatformBackupParams{ID: row.ID, State: state, SizeBytes: req.SizeBytes,
			Sha256: strPtr(req.Sha256), FileName: strPtr(req.FileName), SnapshotID: strPtr(req.SnapshotId), RepositoryID: repoID,
			BundlePath: strPtr(req.BundlePath), Error: strPtr(errText)}); err != nil {
			return err
		}
		final = state
		details := map[string]any{"backup_id": row.ID, "trigger": row.Trigger, "file_name": req.FileName, "sha256": req.Sha256,
			"size_bytes": req.SizeBytes, "snapshot_id": req.SnapshotId, "repository_id": req.RepositoryId, "bundle_path": req.BundlePath}
		if len(missing) > 0 {
			details["missing_repositories"] = missing
		}
		if errText != "" {
			details["error"] = errText
		}
		typ, result, severity, msg := audit.PlatformBackupSucceeded, audit.Success, "", ""
		switch state {
		case platformwf.StatePartial:
			typ, severity = audit.PlatformBackupPartial, "warning"
			msg = "Platform backup is partial: " + errText
		case platformwf.StateFailed:
			typ, result, severity = audit.PlatformBackupFailed, audit.Failure, "critical"
			msg = "Platform backup failed: " + errText
		}
		if _, err := rec.Record(ctx, s.systemEvent(typ, "platform_backup", row.ID, result, details)); err != nil {
			return err
		}
		if severity != "" {
			return s.alert(ctx, q, severity, typ, "platform_backup", row.ID, msg, details)
		}
		return nil
	})
	if err != nil {
		return nil, grpcErr(err)
	}
	return &controlv1.RecordPlatformBackupResponse{State: final}, nil
}

// ---- API service methods -----------------------------------------------------

// ListPlatformBackups returns the newest platform backups.
func (s *Service) ListPlatformBackups(ctx context.Context, limit int32) ([]store.PlatformBackup, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	return s.q.ListPlatformBackups(ctx, store.ListPlatformBackupsParams{OrgID: s.opts.OrgID, Limit: limit})
}

// StartPlatformBackup starts the Platform Protection workflow now.
func (s *Service) StartPlatformBackup(ctx context.Context, p *auth.Principal, m auth.RequestMeta) (string, error) {
	if s.temporal == nil {
		return "", errors.New("workflow engine unavailable")
	}
	run, err := temporalx.StartPlatformOperation(ctx, s.temporal, s.opts.TaskQueue, platformwf.Operation,
		platformwf.PlatformProtectionWorkflow, platformwf.Input{Trigger: "manual", RequestedBy: p.UserID.String()})
	if err != nil {
		return "", err
	}
	ev := s.event(p, m, audit.PlatformBackupRequested)
	ev.TargetType, ev.TargetID = "platform", "protection"
	ev.Details = map[string]any{"workflow_id": run.GetID(), "run_id": run.GetRunID()}
	if _, err := s.audit.Record(ctx, ev); err != nil {
		return "", err
	}
	return run.GetID(), nil
}

// SetSystemRepository designates the System Repository (at most one).
func (s *Service) SetSystemRepository(ctx context.Context, p *auth.Principal, id uuid.UUID, m auth.RequestMeta) (store.Repository, error) {
	var out store.Repository
	err := s.inTx(ctx, func(q *store.Queries, rec *audit.Recorder) error {
		r, err := q.GetRepository(ctx, store.GetRepositoryParams{ID: id, OrgID: s.opts.OrgID})
		if err != nil {
			return notFound(err)
		}
		if r.Status == "retired" {
			return fmt.Errorf("%w: Repository %s is retired", ErrInvalid, r.Name)
		}
		var before *string
		if prev, err := q.GetSystemRepository(ctx, s.opts.OrgID); err == nil {
			v := prev.ID.String()
			before = &v
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := q.ClearSystemRepository(ctx, s.opts.OrgID); err != nil {
			return err
		}
		if out, err = q.SetSystemRepository(ctx, store.SetSystemRepositoryParams{ID: id, OrgID: s.opts.OrgID}); err != nil {
			return err
		}
		ev := s.event(p, m, audit.RepositorySystemDesignated)
		ev.TargetType, ev.TargetID = "repository", id.String()
		ev.Details = map[string]any{"name": r.Name, "previous_system_repository_id": before}
		_, err = rec.Record(ctx, ev)
		return err
	})
	return out, err
}

func pbStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
