// SPDX-License-Identifier: Apache-2.0

package reposerver

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Platform self-protection (Phase 9, ADR-0008): the state needed to rebuild
// a reposerver on the same storage is exported as a tar and imported into
// a fresh reposerver during platform recovery.

// State archive members (slash-separated, relative to the state dir).
const (
	stPassword = "repository-password"
	stControl  = "control-password"
	stCert     = "tls.crt"
	stKey      = "tls.key"
	stInfo     = "repository.json"
	stConfig   = "kopia/repository.config"
)

var (
	// stateMembers is the archive layout in export order; the Kopia
	// caches and logs are never part of it.
	stateMembers = []string{stPassword, stControl, stCert, stKey, stInfo, stConfig}
	// optionalMembers may be missing on import (repository.json is
	// rebuilt from the repository).
	optionalMembers = []string{stInfo}
)

// MaxStateArchive bounds a state archive.
const MaxStateArchive = 4 << 20

func (s State) member(name string) string { return filepath.Join(s.Dir, filepath.FromSlash(name)) }

// RepositoryPassword returns the repository password (needed to rebuild
// escrow packages).
func (s *Service) RepositoryPassword(context.Context) (string, error) {
	if !s.Initialized() {
		return "", ErrNotInitialized
	}
	pw := s.Password()
	if pw == "" {
		return "", ErrNotInitialized
	}
	s.Log.Info("repository password read through the management API")
	return pw, nil
}

// ExportState returns the state archive (a tar; every member 0600).
func (s *Service) ExportState(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Initialized() {
		return nil, ErrNotInitialized
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now().UTC().Truncate(time.Second)
	for _, name := range stateMembers {
		b, err := os.ReadFile(s.State.member(name))
		if err != nil {
			return nil, fmt.Errorf("state export: %w", err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), ModTime: now,
			Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(b); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	s.Log.Warn("reposerver state exported through the management API (contains the repository password)")
	return buf.Bytes(), nil
}

func invalidState(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidState, fmt.Sprintf(format, args...))
}

// ParseStateArchive reads and validates a state archive: only the expected
// member names (no path traversal, no links or devices), each at most
// once; directory entries for kopia/ are ignored.
func ParseStateArchive(b []byte) (map[string][]byte, error) {
	files := map[string][]byte{}
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, invalidState("%v", err)
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag == tar.TypeDir && strings.TrimSuffix(name, "/") == "kopia" {
			continue
		}
		if name != path.Clean(name) || !slices.Contains(stateMembers, name) {
			return nil, invalidState("unexpected member %q", h.Name)
		}
		if h.Typeflag != tar.TypeReg {
			return nil, invalidState("%s is not a regular file", name)
		}
		if _, dup := files[name]; dup {
			return nil, invalidState("duplicate member %s", name)
		}
		data, err := io.ReadAll(io.LimitReader(tr, MaxStateArchive))
		if err != nil {
			return nil, invalidState("%s: %v", name, err)
		}
		files[name] = data
	}
	for _, name := range stateMembers {
		if _, ok := files[name]; !ok && !slices.Contains(optionalMembers, name) {
			return nil, invalidState("missing %s", name)
		}
	}
	return files, nil
}

// checkState validates the members' content and returns the certificate
// fingerprint, the passwords and the Kopia config (storage path adjusted
// to this reposerver's storage path).
func (s *Service) checkState(files map[string][]byte) (fp, password, control string, config []byte, info *RepoInfo, err error) {
	password = strings.TrimSpace(string(files[stPassword]))
	control = strings.TrimSpace(string(files[stControl]))
	if !validPassword(password, MinRepositoryPassword) {
		return "", "", "", nil, nil, invalidState("the repository password is malformed")
	}
	if control == "" || strings.ContainsAny(control, "\r\n\x00") {
		return "", "", "", nil, nil, invalidState("the control password is malformed")
	}
	if _, err := tls.X509KeyPair(files[stCert], files[stKey]); err != nil {
		return "", "", "", nil, nil, invalidState("TLS certificate and key: %v", err)
	}
	blk, _ := pem.Decode(files[stCert])
	if blk == nil || blk.Type != "CERTIFICATE" {
		return "", "", "", nil, nil, invalidState("tls.crt does not start with a PEM certificate")
	}
	if _, err := x509.ParseCertificate(blk.Bytes); err != nil {
		return "", "", "", nil, nil, invalidState("TLS certificate: %v", err)
	}
	fp = Fingerprint(blk.Bytes)
	var cfg map[string]any
	if err := json.Unmarshal(files[stConfig], &cfg); err != nil {
		return "", "", "", nil, nil, invalidState("Kopia repository config: %v", err)
	}
	st, _ := cfg["storage"].(map[string]any)
	sc, _ := st["config"].(map[string]any)
	if st == nil || st["type"] != "filesystem" || sc == nil {
		return "", "", "", nil, nil, invalidState("the Kopia repository config is not a filesystem repository")
	}
	if p, _ := sc["path"].(string); p != s.StoragePath {
		s.Log.Warn("imported Kopia config points at another storage path; using this reposerver's", "was", p, "now", s.StoragePath)
		sc["path"] = s.StoragePath
	}
	if config, err = json.MarshalIndent(cfg, "", "  "); err != nil {
		return "", "", "", nil, nil, err
	}
	if b, ok := files[stInfo]; ok {
		info = &RepoInfo{}
		if err := json.Unmarshal(b, info); err != nil {
			return "", "", "", nil, nil, invalidState("repository.json: %v", err)
		}
		if info.RepositoryID != s.RepositoryID {
			return "", "", "", nil, nil, invalidState("the archive is for repository %q, this reposerver serves %q", info.RepositoryID, s.RepositoryID)
		}
	}
	return fp, password, control, config, info, nil
}

// ImportState restores an exported state into a reposerver that was never
// initialized (platform recovery), verifies that the password opens the
// repository on the guarded storage, and starts serving.
func (s *Service) ImportState(ctx context.Context, archive []byte) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Initialized() {
		return Status{}, ErrAlreadyInitialized
	}
	if pw, err := ReadSecret(s.State.PasswordFile()); err == nil && pw != "" {
		return Status{}, ErrStatePresent
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Status{}, err
	}
	files, err := ParseStateArchive(archive)
	if err != nil {
		return Status{}, err
	}
	fp, password, control, config, info, err := s.checkState(files)
	if err != nil {
		return Status{}, err
	}
	if err := s.guard(); err != nil {
		return Status{}, err
	}
	if _, err := os.Stat(filepath.Join(s.StoragePath, formatBlob)); err != nil {
		return Status{}, fmt.Errorf("%w: the storage holds no Kopia repository (%v)", ErrStorageNotReady, err)
	}
	if err := s.State.Prepare(); err != nil {
		return Status{}, err
	}
	files[stConfig] = config
	delete(files, stInfo) // written last, once the repository opened

	// Keep what is there (the certificate and control password generated
	// at start-up) to put it back if the import fails.
	prev := map[string][]byte{}
	for name := range files {
		if b, err := os.ReadFile(s.State.member(name)); err == nil {
			prev[name] = b
		}
	}
	rollback := func() {
		for name := range files {
			if b, ok := prev[name]; ok {
				_ = writeFileAtomic(s.State.member(name), b, 0o600)
			} else {
				_ = os.Remove(s.State.member(name))
			}
		}
		s.password.Store(nil)
	}
	for _, name := range stateMembers {
		b, ok := files[name]
		if !ok {
			continue
		}
		if err := writeFileAtomic(s.State.member(name), b, 0o600); err != nil {
			rollback()
			return Status{}, fmt.Errorf("write %s: %w", name, err)
		}
	}
	s.password.Store(&password)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	actual, err := s.repoSplitter(cctx)
	if err != nil {
		rollback()
		if strings.Contains(err.Error(), "invalid repository password") {
			return Status{}, ErrInvalidPassword
		}
		return Status{}, err
	}
	if info == nil {
		info = &RepoInfo{RepositoryID: s.RepositoryID, InitializedAt: time.Now().UTC()}
	}
	info.Splitter = actual
	if err := s.State.SaveInfo(info); err != nil {
		rollback()
		return Status{}, err
	}
	s.creds.Store(&serverCreds{certSHA256: fp, controlPassword: control})
	if f, ok := s.Server.(interface{ SetFingerprint(string) }); ok {
		f.SetFingerprint(fp)
	}
	s.info.Store(info)
	s.Log.Warn("reposerver state imported; serving the existing repository", "repository_id", s.RepositoryID,
		"cert_sha256", fp, "splitter", actual)
	s.startServer(ctx)
	return s.Status(ctx), nil
}
