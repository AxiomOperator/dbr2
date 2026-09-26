// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
	"google.golang.org/grpc"

	controlv1 "github.com/AxiomOperator/dbr2/internal/agentpb/control/v1"
	"github.com/AxiomOperator/dbr2/internal/engine"
	"github.com/AxiomOperator/dbr2/workflows/backup"
)

// fakePlatform serves ExportPlatform from a byte slice.
type fakePlatform struct {
	controlv1.PlatformServiceClient
	bundle    []byte
	noSummary bool
	recorded  *controlv1.RecordPlatformBackupRequest
}

type fakeStream struct {
	grpc.ClientStream
	msgs []*controlv1.ExportPlatformResponse
}

func (s *fakeStream) Recv() (*controlv1.ExportPlatformResponse, error) {
	if len(s.msgs) == 0 {
		return nil, io.EOF
	}
	m := s.msgs[0]
	s.msgs = s.msgs[1:]
	return m, nil
}

func (f *fakePlatform) ExportPlatform(context.Context, *controlv1.ExportPlatformRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[controlv1.ExportPlatformResponse], error) {
	s := &fakeStream{}
	for i := 0; i < len(f.bundle); i += 7 {
		s.msgs = append(s.msgs, &controlv1.ExportPlatformResponse{Data: f.bundle[i:min(i+7, len(f.bundle))]})
	}
	if !f.noSummary {
		s.msgs = append(s.msgs, &controlv1.ExportPlatformResponse{Summary: &controlv1.ExportPlatformSummary{ManifestJson: []byte("{}")}})
	}
	return s, nil
}

func (f *fakePlatform) GetRepository(_ context.Context, in *controlv1.GetRepositoryRequest, _ ...grpc.CallOption) (*controlv1.GetRepositoryResponse, error) {
	return &controlv1.GetRepositoryResponse{Repository: &controlv1.Repository{Id: in.RepositoryId, Name: "system", ServerUrl: "https://x", ManagementUrl: "http://m"}}, nil
}

func (f *fakePlatform) RecordPlatformBackup(_ context.Context, in *controlv1.RecordPlatformBackupRequest, _ ...grpc.CallOption) (*controlv1.RecordPlatformBackupResponse, error) {
	f.recorded = in
	return &controlv1.RecordPlatformBackupResponse{State: in.State}, nil
}

// fakeRepo records stream snapshots.
type fakeRepo struct {
	engine.Repository
	got  bytes.Buffer
	req  engine.SnapshotRequest
	fail bool
}

func (r *fakeRepo) SnapshotStream(_ context.Context, _ string, rd io.Reader, req engine.SnapshotRequest) (*engine.Snapshot, error) {
	r.req = req
	if r.fail {
		return nil, errors.New("repository offline")
	}
	if _, err := io.Copy(&r.got, rd); err != nil {
		return nil, err
	}
	return &engine.Snapshot{ID: "k123"}, nil
}

func (r *fakeRepo) Close(context.Context) error { return nil }

func newActs(t *testing.T, p *fakePlatform, repo *fakeRepo, dir string) *Activities {
	m := backup.NewMaintSessions(p, "tok", t.TempDir())
	m.SetPassword = func(context.Context, string, string, string) error { return nil }
	m.Connect = func(context.Context, engine.ServerConnection) (engine.Repository, error) { return repo, nil }
	return &Activities{Platform: p, Token: "tok", Maint: m, BundleDir: dir, Keep: 2,
		Now: func() time.Time { return time.Date(2026, 9, 25, 2, 15, 0, 0, time.UTC) }}
}

func run(t *testing.T, a *Activities, b Begin) (Outcome, error) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a)
	v, err := env.ExecuteActivity(a.RunPlatformBackup, b)
	var out Outcome
	if err == nil {
		err = v.Get(&out)
	}
	return out, err
}

func TestRunTeesIntoBothTargets(t *testing.T) {
	bundle := bytes.Repeat([]byte("ciphertext"), 50)
	p := &fakePlatform{bundle: bundle}
	repo := &fakeRepo{}
	dir := t.TempDir()
	// Older bundles beyond Keep are pruned; unrelated files stay.
	for _, n := range []string{"dbr2-platform-20260101T000000Z.tar.zst.age", "dbr2-platform-20260102T000000Z.tar.zst.age", "notes.txt"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	out, err := run(t, newActs(t, p, repo, dir), Begin{BackupID: "pb_1", SystemRepositoryID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bundle)
	if out.State != StateSucceeded || out.SHA256 != hex.EncodeToString(sum[:]) || out.SizeBytes != int64(len(bundle)) ||
		out.SnapshotID != "k123" || out.RepositoryID != "r1" || out.FileName != "dbr2-platform-20260925T021500Z.tar.zst.age" {
		t.Fatalf("outcome %+v", out)
	}
	if !bytes.Equal(repo.got.Bytes(), bundle) || repo.req.Tags["dbr2-kind"] != "platform" || repo.req.Source.String() != "maint@dbr2:/platform" ||
		len(repo.req.Pins) != 1 {
		t.Fatalf("snapshot request %+v", repo.req)
	}
	got, err := os.ReadFile(out.BundlePath)
	if err != nil || !bytes.Equal(got, bundle) {
		t.Fatalf("bundle file: %v", err)
	}
	st, _ := os.Stat(out.BundlePath)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 3 || names[0] != "dbr2-platform-20260102T000000Z.tar.zst.age" || names[2] != "notes.txt" {
		t.Fatalf("after prune: %v", names)
	}
}

func TestRunPartialWhenOneTargetFails(t *testing.T) {
	p := &fakePlatform{bundle: []byte("ciphertext")}
	out, err := run(t, newActs(t, p, &fakeRepo{fail: true}, t.TempDir()), Begin{BackupID: "pb_1", SystemRepositoryID: "r1"})
	if err != nil || out.State != StatePartial || out.BundlePath == "" || out.SnapshotID != "" {
		t.Fatalf("outcome %+v err %v", out, err)
	}
	// No System Repository designated: bundle only, Partial.
	out, err = run(t, newActs(t, p, &fakeRepo{}, t.TempDir()), Begin{BackupID: "pb_1"})
	if err != nil || out.State != StatePartial {
		t.Fatalf("outcome %+v err %v", out, err)
	}
}

func TestRunFailsWithoutTargetsOrSummary(t *testing.T) {
	p := &fakePlatform{bundle: []byte("ciphertext")}
	if _, err := run(t, newActs(t, p, &fakeRepo{}, ""), Begin{BackupID: "pb_1"}); err == nil {
		t.Fatal("ran without any target")
	}
	dir := t.TempDir()
	p.noSummary = true
	if _, err := run(t, newActs(t, p, &fakeRepo{}, dir), Begin{BackupID: "pb_1", SystemRepositoryID: "r1"}); err == nil {
		t.Fatal("truncated stream accepted")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestWorkflowRecordsFailure(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	var a *Activities
	env.RegisterActivity(&Activities{})
	env.OnActivity(a.BeginPlatformBackup, mockAny, mockAny).Return(Begin{BackupID: "pb_1"}, nil)
	env.OnActivity(a.RunPlatformBackup, mockAny, mockAny).Return(Outcome{}, errors.New("boom"))
	var recorded Outcome
	env.OnActivity(a.RecordPlatformBackupOutcome, mockAny, mockAny).Return(func(_ context.Context, o Outcome) (string, error) {
		recorded = o
		return o.State, nil
	})
	env.ExecuteWorkflow(PlatformProtectionWorkflow, Input{Trigger: "manual"})
	if env.GetWorkflowError() == nil || recorded.State != StateFailed || recorded.BackupID != "pb_1" {
		t.Fatalf("err %v recorded %+v", env.GetWorkflowError(), recorded)
	}
}

var mockAny = mock.Anything
