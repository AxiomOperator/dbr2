// SPDX-License-Identifier: Apache-2.0

package reposerver

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const platformPW = "platform-test-repository-password-0123456789" // gitleaks:allow (test fixture)

// scriptRunner answers `repository status --json`.
type scriptRunner struct {
	mu   sync.Mutex
	err  error
	pw   func() string
	seen []string
}

func (r *scriptRunner) Run(_ context.Context, inv Invocation) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, strings.Join(inv.Args, " ")+" pw="+r.pw())
	if r.err != nil {
		return nil, r.err
	}
	return []byte(`{"objectFormat":{"splitter":"DYNAMIC-4M-BUZHASH"}}`), nil
}

type fakeServer struct {
	mu      sync.Mutex
	started bool
	fp      string
}

func (f *fakeServer) Start(context.Context) { f.mu.Lock(); f.started = true; f.mu.Unlock() }
func (f *fakeServer) Running() bool         { f.mu.Lock(); defer f.mu.Unlock(); return f.started }
func (f *fakeServer) SetFingerprint(fp string) {
	f.mu.Lock()
	f.fp = fp
	f.mu.Unlock()
}

// newPlatformService is a reposerver as Run builds it: certificate and
// control password generated, storage holding a Kopia repository.
func newPlatformService(t *testing.T) (*Service, *scriptRunner, *fakeServer) {
	t.Helper()
	dir := t.TempDir()
	st := State{Dir: filepath.Join(dir, "state")}
	if err := st.Prepare(); err != nil {
		t.Fatal(err)
	}
	fp, _, err := st.EnsureCert([]string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	ctrl, err := st.EnsureControlPassword()
	if err != nil {
		t.Fatal(err)
	}
	storage := filepath.Join(dir, "repo")
	_ = os.MkdirAll(storage, 0o700)
	if err := os.WriteFile(filepath.Join(storage, formatBlob), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := &fakeServer{}
	s := &Service{RepositoryID: "repo-1", StoragePath: storage, KopiaAddr: "127.0.0.1:51515", State: st,
		GuardCheck: func() error { return nil }, CertSHA256: fp, ControlPassword: ctrl, Server: srv,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Lifetime: context.Background()}
	r := &scriptRunner{pw: s.Password}
	s.Runner = r
	return s, r, srv
}

// initializedService fakes an initialized reposerver's state.
func initializedService(t *testing.T) *Service {
	t.Helper()
	s, _, _ := newPlatformService(t)
	if err := writeFileAtomic(s.State.PasswordFile(), []byte(platformPW+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := `{"storage":{"type":"filesystem","config":{"path":"` + s.StoragePath + `","dirShards":null}},"caching":{"cacheDirectory":"cache"},"hostname":"dbr2","username":"reposerver"}`
	if err := writeFileAtomic(s.State.ConfigFile(), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(s.State.CacheDir(), "cached-blob"), []byte("x"), 0o600)
	if err := s.State.SaveInfo(&RepoInfo{RepositoryID: "repo-1", Splitter: "DYNAMIC-4M-BUZHASH"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Load(); !ok || err != nil {
		t.Fatalf("load %v %v", ok, err)
	}
	return s
}

func tarOf(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		body := h.Linkname
		if h.Typeflag == tar.TypeReg {
			h.Size, h.Linkname = int64(len(body)), ""
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(body))
		}
	}
	_ = tw.Close()
	return buf.Bytes()
}

func members(t *testing.T, b []byte) map[string]tar.Header {
	t.Helper()
	out := map[string]tar.Header{}
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = *h
	}
}

func TestStateExportImport(t *testing.T) {
	ctx := context.Background()
	a := initializedService(t)
	if pw, err := a.RepositoryPassword(ctx); err != nil || pw != platformPW {
		t.Fatalf("password %v", err)
	}
	archive, err := a.ExportState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := members(t, archive)
	if len(m) != len(stateMembers) {
		t.Fatalf("members %v", m)
	}
	for _, name := range stateMembers {
		if h, ok := m[name]; !ok || h.Mode != 0o600 || h.Typeflag != tar.TypeReg {
			t.Fatalf("%s: %+v", name, h)
		}
	}

	b, runner, srv := newPlatformService(t)
	if _, err := b.RepositoryPassword(ctx); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("password before import: %v", err)
	}
	if _, err := b.ExportState(ctx); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("export before import: %v", err)
	}
	st, err := b.ImportState(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Initialized || st.CertSHA256 != a.CertSHA256 || st.Splitter != "DYNAMIC-4M-BUZHASH" || !srv.started || srv.fp != a.CertSHA256 {
		t.Fatalf("status %+v server %+v", st, srv)
	}
	if b.certSHA256() != a.CertSHA256 || b.controlPassword() != a.ControlPassword || b.Password() != platformPW {
		t.Fatal("credentials not switched")
	}
	if len(runner.seen) != 1 || runner.seen[0] != "repository status --json pw="+platformPW {
		t.Fatalf("runner %v", runner.seen)
	}
	for _, name := range []string{stPassword, stControl, stCert, stKey} {
		want, _ := os.ReadFile(a.State.member(name))
		got, _ := os.ReadFile(b.State.member(name))
		if !bytes.Equal(got, want) {
			t.Fatalf("%s differs", name)
		}
		if fi, _ := os.Stat(b.State.member(name)); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", name, fi.Mode())
		}
	}
	// The Kopia config now points at b's storage.
	var cfg struct {
		Storage struct {
			Config struct{ Path string } `json:"config"`
		} `json:"storage"`
	}
	cb, _ := os.ReadFile(b.State.ConfigFile())
	if err := json.Unmarshal(cb, &cfg); err != nil || cfg.Storage.Config.Path != b.StoragePath {
		t.Fatalf("config path %q %v", cfg.Storage.Config.Path, err)
	}
	if ri, err := b.State.LoadInfo(); err != nil || ri.RepositoryID != "repo-1" {
		t.Fatalf("info %+v %v", ri, err)
	}
	if fi, _ := os.Stat(b.State.Dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode %v", fi.Mode())
	}
	// Once initialized, a second import is refused.
	if _, err := b.ImportState(ctx, archive); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second import: %v", err)
	}
	// The export of the rebuilt reposerver matches the original.
	again, err := b.ExportState(ctx)
	if err != nil || len(members(t, again)) != len(stateMembers) {
		t.Fatalf("re-export %v", err)
	}
}

func TestStateImportRefusals(t *testing.T) {
	ctx := context.Background()
	archive, err := initializedService(t).ExportState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := ParseStateArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	reg := func(name, body string) tar.Header {
		return tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Linkname: body}
	}
	valid := func(extra ...tar.Header) []tar.Header {
		var hs []tar.Header
		for _, n := range stateMembers {
			if n != stInfo {
				hs = append(hs, reg(n, string(files[n])))
			}
		}
		return append(hs, extra...)
	}
	// Layout validation.
	for name, b := range map[string][]byte{
		"traversal":  tarOf(t, valid(reg("../../etc/passwd", "x"))...),
		"dot-dot":    tarOf(t, valid(reg("kopia/../tls.key", "x"))...),
		"cache":      tarOf(t, valid(reg("kopia/cache/blob", "x"))...),
		"absolute":   tarOf(t, valid(reg("/repository-password", "x"))...),
		"symlink":    tarOf(t, append(valid()[1:], tar.Header{Name: stPassword, Typeflag: tar.TypeSymlink, Linkname: "/etc/shadow"})...),
		"duplicate":  tarOf(t, valid(reg(stCert, string(files[stCert])))...),
		"missing":    tarOf(t, valid()[1:]...),
		"not a tar":  []byte("hello"),
		"bad cert":   tarOf(t, append(valid()[:2], append([]tar.Header{reg(stCert, "junk")}, valid()[3:]...)...)...),
		"short pass": tarOf(t, append([]tar.Header{reg(stPassword, "short")}, valid()[1:]...)...),
	} {
		s, _, _ := newPlatformService(t)
		if _, err := s.ImportState(ctx, b); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(s.State.PasswordFile()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: state written", name)
		}
	}
	// A reposerver whose state already holds a repository password.
	s, _, _ := newPlatformService(t)
	_ = os.WriteFile(s.State.PasswordFile(), []byte("existing\n"), 0o600)
	if _, err := s.ImportState(ctx, archive); !errors.Is(err, ErrStatePresent) {
		t.Fatalf("state present: %v", err)
	}
	// A different repository.
	s, _, _ = newPlatformService(t)
	s.RepositoryID = "other"
	if _, err := s.ImportState(ctx, archive); !errors.Is(err, ErrInvalidState) || !strings.Contains(err.Error(), "repo-1") {
		t.Fatalf("other repository: %v", err)
	}
	// The storage guard still applies, and the storage must hold a repository.
	s, _, _ = newPlatformService(t)
	s.GuardCheck = func() error { return errors.New("not a mount point") }
	if _, err := s.ImportState(ctx, archive); !errors.Is(err, ErrStorageNotReady) {
		t.Fatalf("guard: %v", err)
	}
	s, _, _ = newPlatformService(t)
	_ = os.Remove(filepath.Join(s.StoragePath, formatBlob))
	if _, err := s.ImportState(ctx, archive); !errors.Is(err, ErrStorageNotReady) {
		t.Fatalf("empty storage: %v", err)
	}
	// A password that does not open the repository rolls everything back.
	s, runner, srv := newPlatformService(t)
	certBefore, _ := os.ReadFile(s.State.CertFile())
	runner.err = errors.New("kopia: invalid repository password")
	if _, err := s.ImportState(ctx, archive); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if certAfter, _ := os.ReadFile(s.State.CertFile()); !bytes.Equal(certAfter, certBefore) {
		t.Fatal("certificate not rolled back")
	}
	for _, f := range []string{s.State.PasswordFile(), s.State.ConfigFile(), filepath.Join(s.State.Dir, stInfo)} {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s left behind", f)
		}
	}
	if s.Initialized() || s.Password() != "" || srv.started {
		t.Fatal("service changed after a failed import")
	}
}
