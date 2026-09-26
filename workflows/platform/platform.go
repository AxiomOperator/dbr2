// SPDX-License-Identifier: Apache-2.0

// Package platform is the Platform Protection workflow (ADR-0008): it
// streams the age-encrypted Platform Recovery Bundle from dbr2-server and
// tees it into the System Repository (a pinned Kopia snapshot written by
// maint@dbr2) and into the bundle directory (a separate NFS export or
// directory that is not part of any Repository). The worker only ever sees
// ciphertext.
package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/internal/manifest"
	"github.com/AxiomOperator/dbr2/workflows/backup"
)

// Workflow identity and defaults.
const (
	// Operation is the temporalx platform operation name.
	Operation = "protection"
	// WorkflowID is the workflow ID of manual runs (the schedule's runs get
	// Temporal's timestamp suffix; its overlap policy is "skip").
	WorkflowID = "platform/" + Operation
	// SourcePath is the Kopia source path (maint@dbr2:/platform).
	SourcePath = "/platform"
	// KindTag marks platform snapshots (dbr2-kind=platform).
	KindTag = "platform"
	// TagBackupID carries the platform backup ID on the snapshot.
	TagBackupID = "dbr2-platform-backup"
	// DefaultBundleDir and DefaultKeep match the worker configuration.
	DefaultBundleDir = "/var/lib/dbr2/platform-bundles"
	DefaultKeep      = 14

	filePrefix = "dbr2-platform-"
	fileSuffix = ".tar.zst.age"
)

// States of a platform backup.
const (
	StateSucceeded = "succeeded"
	StatePartial   = "partial"
	StateFailed    = "failed"
)

// FileName returns the bundle file name for t (UTC).
func FileName(t time.Time) string {
	return filePrefix + t.UTC().Format("20060102T150405Z") + fileSuffix
}

// Input starts a run.
type Input struct {
	// Trigger is scheduled or manual.
	Trigger     string
	RequestedBy string
}

// Begin is the recorded run.
type Begin struct {
	BackupID             string
	SystemRepositoryID   string
	SystemRepositoryName string
}

// Outcome is the result of a run.
type Outcome struct {
	BackupID     string
	State        string
	FileName     string
	SHA256       string
	SizeBytes    int64
	SnapshotID   string
	RepositoryID string
	BundlePath   string
	Error        string
}

// PlatformProtectionWorkflow backs up the platform (ADR-0008).
func PlatformProtectionWorkflow(ctx workflow.Context, in Input) (Outcome, error) {
	var a *Activities
	if in.Trigger == "" {
		in.Trigger = "scheduled"
	}
	short := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{InitialInterval: 5 * time.Second, MaximumAttempts: 10},
	})
	var b Begin
	if err := workflow.ExecuteActivity(short, a.BeginPlatformBackup, in).Get(ctx, &b); err != nil {
		return Outcome{}, err
	}
	long := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Hour, HeartbeatTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: 30 * time.Second, MaximumAttempts: 3},
	})
	var out Outcome
	runErr := workflow.ExecuteActivity(long, a.RunPlatformBackup, b).Get(ctx, &out)
	if runErr != nil {
		out = Outcome{BackupID: b.BackupID, State: StateFailed, Error: runErr.Error()}
	}
	// Record the outcome even when the run was cancelled.
	rctx := short
	if ctx.Err() != nil {
		dc, _ := workflow.NewDisconnectedContext(ctx)
		rctx = workflow.WithActivityOptions(dc, workflow.ActivityOptions{StartToCloseTimeout: time.Minute,
			RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 5}})
	}
	var final string
	if err := workflow.ExecuteActivity(rctx, a.RecordPlatformBackupOutcome, out).Get(rctx, &final); err != nil {
		return out, err
	}
	out.State = final
	return out, runErr
}

// Activities implement the workflow steps (dbr2-worker).
type Activities struct {
	Platform controlv1.PlatformServiceClient
	Token    string
	Maint    *backup.MaintSessions
	// BundleDir receives the bundle files ("" = disabled).
	BundleDir string
	// Keep is how many bundle files are kept in BundleDir (newest first).
	Keep int
	// Now is replaceable in tests.
	Now func() time.Time
}

func (a *Activities) auth(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+a.Token)
}

func platformErr(err error) error {
	switch status.Code(err) {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.NotFound, codes.PermissionDenied, codes.AlreadyExists:
		return temporal.NewNonRetryableApplicationError(status.Convert(err).Message(), status.Code(err).String(), err)
	}
	return err
}

// BeginPlatformBackup records the running backup.
func (a *Activities) BeginPlatformBackup(ctx context.Context, in Input) (Begin, error) {
	info := activity.GetInfo(ctx)
	resp, err := a.Platform.BeginPlatformBackup(a.auth(ctx), &controlv1.BeginPlatformBackupRequest{
		WorkflowId: info.WorkflowExecution.ID, RunId: info.WorkflowExecution.RunID, Trigger: in.Trigger, RequestedBy: in.RequestedBy})
	if err != nil {
		return Begin{}, platformErr(err)
	}
	b := Begin{BackupID: resp.BackupId}
	if r := resp.SystemRepository; r != nil {
		b.SystemRepositoryID, b.SystemRepositoryName = r.Id, r.Name
	}
	return b, nil
}

// RecordPlatformBackupOutcome stores the outcome; it returns the final state.
func (a *Activities) RecordPlatformBackupOutcome(ctx context.Context, o Outcome) (string, error) {
	resp, err := a.Platform.RecordPlatformBackup(a.auth(ctx), &controlv1.RecordPlatformBackupRequest{
		BackupId: o.BackupID, State: o.State, SizeBytes: o.SizeBytes, Sha256: o.SHA256, FileName: o.FileName,
		SnapshotId: o.SnapshotID, RepositoryId: o.RepositoryID, BundlePath: o.BundlePath, Error: o.Error})
	if err != nil {
		return "", platformErr(err)
	}
	return resp.State, nil
}

// snapshotTarget writes the stream into the System Repository.
type snapshotTarget struct {
	pw   *io.PipeWriter
	pr   *io.PipeReader
	done chan struct{}
	snap *engine.Snapshot
	err  error
}

func (a *Activities) startSnapshot(ctx context.Context, repositoryID, backupID, fileName string) (*snapshotTarget, error) {
	rep, err := a.Maint.Get(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	t := &snapshotTarget{pw: pw, pr: pr, done: make(chan struct{})}
	go func() {
		defer close(t.done)
		t.snap, t.err = rep.SnapshotStream(ctx, fileName, pr, engine.SnapshotRequest{
			Source: engine.Source{User: manifest.ManifestSourceUser, Host: manifest.ManifestSourceHost, Path: SourcePath},
			Pins:   []string{engine.Pin}, Description: "DBR² Platform Recovery Bundle " + backupID,
			Tags: map[string]string{engine.TagKind: KindTag, TagBackupID: backupID},
		})
		if t.err == nil && t.snap != nil && t.snap.Errors > 0 {
			t.err = fmt.Errorf("snapshot recorded %d errors", t.snap.Errors)
		}
		// Unblock the writer if the snapshot ended early.
		_ = pr.CloseWithError(errors.New("snapshot ended"))
	}()
	return t, nil
}

// finish closes the stream (with cause != nil on abort) and waits.
func (t *snapshotTarget) finish(cause error) (*engine.Snapshot, error) {
	if cause != nil {
		_ = t.pw.CloseWithError(cause)
	} else {
		_ = t.pw.Close()
	}
	<-t.done
	if cause != nil && t.err == nil {
		return nil, cause
	}
	return t.snap, t.err
}

// RunPlatformBackup streams the bundle and tees it into both targets. At
// least one target must succeed; one failed or missing target makes the run
// Partial.
func (a *Activities) RunPlatformBackup(ctx context.Context, b Begin) (Outcome, error) {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	out := Outcome{BackupID: b.BackupID, FileName: FileName(now())}
	var problems []string
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Target 1: the bundle directory.
	var file *os.File
	if a.BundleDir == "" {
		problems = append(problems, "bundle directory: not configured")
	} else if f, err := createTemp(a.BundleDir); err != nil {
		problems = append(problems, "bundle directory: "+err.Error())
	} else {
		file = f
	}
	abortFile := func(msg string) {
		if file != nil {
			name := file.Name()
			_ = file.Close()
			_ = os.Remove(name)
			file = nil
			problems = append(problems, "bundle directory: "+msg)
		}
	}
	defer abortFile("aborted")

	// Target 2: the System Repository.
	var snap *snapshotTarget
	if b.SystemRepositoryID == "" {
		problems = append(problems, "System Repository: none designated")
	} else if t, err := a.startSnapshot(ctx, b.SystemRepositoryID, b.BackupID, out.FileName); err != nil {
		a.Maint.Invalidate(b.SystemRepositoryID)
		problems = append(problems, "System Repository: "+err.Error())
	} else {
		snap = t
	}
	if file == nil && snap == nil {
		return out, temporal.NewNonRetryableApplicationError("no platform backup target is available: "+strings.Join(problems, "; "), "NoTarget", nil)
	}
	abortSnap := func(cause error) {
		if snap != nil {
			_, _ = snap.finish(cause)
			a.Maint.Invalidate(b.SystemRepositoryID)
			snap = nil
		}
	}
	defer abortSnap(errors.New("aborted"))

	stream, err := a.Platform.ExportPlatform(a.auth(ctx), &controlv1.ExportPlatformRequest{BackupId: b.BackupID})
	if err != nil {
		return out, platformErr(err)
	}
	h := sha256.New()
	var summary *controlv1.ExportPlatformSummary
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, platformErr(err)
		}
		if d := msg.GetData(); len(d) > 0 {
			_, _ = h.Write(d)
			out.SizeBytes += int64(len(d))
			if file != nil {
				if _, err := file.Write(d); err != nil {
					abortFile(err.Error())
				}
			}
			if snap != nil {
				if _, err := snap.pw.Write(d); err != nil {
					_, serr := snap.finish(err)
					a.Maint.Invalidate(b.SystemRepositoryID)
					snap = nil
					problems = append(problems, "System Repository: "+errText(serr, err))
				}
			}
			if file == nil && snap == nil {
				return out, fmt.Errorf("every platform backup target failed: %s", strings.Join(problems, "; "))
			}
			activity.RecordHeartbeat(ctx, out.SizeBytes)
		}
		if s := msg.GetSummary(); s != nil {
			summary = s
		}
	}
	if summary == nil {
		return out, errors.New("the bundle stream ended without its summary (truncated)")
	}
	out.SHA256 = hex.EncodeToString(h.Sum(nil))

	if snap != nil {
		s, err := snap.finish(nil)
		snap = nil
		if err != nil {
			a.Maint.Invalidate(b.SystemRepositoryID)
			problems = append(problems, "System Repository: "+err.Error())
		} else {
			out.SnapshotID, out.RepositoryID = s.ID, b.SystemRepositoryID
		}
	}
	if file != nil {
		final := filepath.Join(a.BundleDir, out.FileName)
		if err := commitFile(file, final); err != nil {
			_ = os.Remove(file.Name())
			problems = append(problems, "bundle directory: "+err.Error())
		} else {
			out.BundlePath = final
			if err := prune(a.BundleDir, a.Keep); err != nil {
				activity.GetLogger(ctx).Warn("pruning old platform bundles failed", "error", err)
			}
		}
		file = nil
	}
	if out.SnapshotID == "" && out.BundlePath == "" {
		return out, fmt.Errorf("every platform backup target failed: %s", strings.Join(problems, "; "))
	}
	out.State = StateSucceeded
	if len(problems) > 0 {
		out.State = StatePartial
		out.Error = strings.Join(problems, "; ")
	}
	return out, nil
}

func errText(primary, fallback error) string {
	if primary != nil {
		return primary.Error()
	}
	return fallback.Error()
}

func createTemp(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, "."+filePrefix+"*.tmp")
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	return f, nil
}

// commitFile syncs and atomically renames the temp file to final.
func commitFile(f *os.File, final string) error {
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), final); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(final)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// prune keeps the newest keep bundle files (names sort by timestamp).
func prune(dir string, keep int) error {
	if keep <= 0 {
		keep = DefaultKeep
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), filePrefix) && strings.HasSuffix(e.Name(), fileSuffix) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var errs []error
	for _, n := range names[min(keep, len(names)):] {
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
