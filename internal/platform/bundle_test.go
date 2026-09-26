// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"
)

func testBundle(t *testing.T, recipients ...age.Recipient) ([]byte, *Manifest) {
	t.Helper()
	m := &Manifest{FormatVersion: FormatVersion, Kind: Kind, CreatedAt: time.Date(2026, 9, 25, 2, 15, 0, 0, time.UTC),
		Tables: []Table{{Name: "agents", Rows: 1, Columns: []string{"id"}, File: "db/agents.copy"}},
		Repositories: []RepositoryState{
			{ID: "r1", Name: "primary", File: "reposerver/r1.tar"},
			{ID: "r2", Name: "offline", Missing: true, Error: "connection refused"},
		}}
	var buf bytes.Buffer
	w, err := NewWriter(&buf, recipients, m)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"db/agents.copy": "PGCOPY", "secrets/" + SecretKeyFile: "a2V5",
		"secrets/" + InternalTokenFile: "tok", "reposerver/r1.tar": "TAR"} {
		if err := w.Add(name, []byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Add("db/agents.copy", nil); err == nil {
		t.Fatal("duplicate entry accepted")
	}
	if err := w.Add("../escape", nil); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), m
}

func TestBundleRoundTripAndTamper(t *testing.T) {
	id1, _ := age.GenerateX25519Identity()
	id2, _ := age.GenerateX25519Identity()
	other, _ := age.GenerateX25519Identity()
	data, m := testBundle(t, id1.Recipient(), id2.Recipient())

	for _, id := range []age.Identity{id1, id2} {
		b, err := Open(bytes.NewReader(data), id)
		if err != nil {
			t.Fatal(err)
		}
		if b.Secret(InternalTokenFile) != "tok" || string(b.Files["db/agents.copy"]) != "PGCOPY" || len(b.Manifest.Files) != 4 {
			t.Fatalf("bundle content: %+v", b.Manifest)
		}
		if got := b.Manifest.MissingRepositories(); len(got) != 1 || got[0] != "r2" {
			t.Fatalf("missing: %v", got)
		}
	}
	if len(m.Files) != 4 {
		t.Fatalf("writer manifest files: %v", m.Files)
	}
	if _, err := Open(bytes.NewReader(data), other); err == nil {
		t.Fatal("decrypted with a non-recipient identity")
	}
	for _, i := range []int{len(data) / 2, len(data) - 1} {
		bad := bytes.Clone(data)
		bad[i] ^= 0x01
		if _, err := Open(bytes.NewReader(bad), id1); err == nil {
			t.Fatalf("tampered byte %d accepted", i)
		}
	}
	if _, err := Open(bytes.NewReader(data[:len(data)-100]), id1); err == nil {
		t.Fatal("truncated bundle accepted")
	}
}

func TestBundleDigestMismatch(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	m := &Manifest{FormatVersion: FormatVersion, Kind: Kind, Files: map[string]FileDigest{"secrets/" + SecretKeyFile: {SHA256: "00", Size: 1}}}
	var buf bytes.Buffer
	w, err := NewWriter(&buf, []age.Recipient{id.Recipient()}, m)
	if err != nil {
		t.Fatal(err)
	}
	// Bypass Add's bookkeeping: write the entry, then keep the wrong digest.
	delete(m.Files, "secrets/"+SecretKeyFile)
	if err := w.Add("secrets/"+SecretKeyFile, []byte("k")); err != nil {
		t.Fatal(err)
	}
	m.Files["secrets/"+SecretKeyFile] = FileDigest{SHA256: "00", Size: 1}
	if _, err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(&buf, id); !errors.Is(err, ErrVerify) {
		t.Fatalf("want ErrVerify, got %v", err)
	}
}

func TestNoRecipients(t *testing.T) {
	if _, err := NewWriter(&bytes.Buffer{}, nil, &Manifest{}); err == nil {
		t.Fatal("bundle without recipients")
	}
}

func TestFileNameAndIdentities(t *testing.T) {
	if got := FileName(time.Date(2026, 9, 25, 2, 15, 0, 0, time.FixedZone("x", 3600))); got != "dbr2-platform-20260925T011500Z.tar.zst.age" {
		t.Fatal(got)
	}
	id, _ := age.GenerateX25519Identity()
	p := filepath.Join(t.TempDir(), "id.txt")
	if err := os.WriteFile(p, []byte("# created: now\n"+id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ids, err := LoadIdentities(p)
	if err != nil || len(ids) != 1 {
		t.Fatalf("identities: %v %v", ids, err)
	}
}

func TestWriteSecretsAndState(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	data, _ := testBundle(t, id.Recipient())
	b, err := Open(bytes.NewReader(data), id)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files, err := WriteSecrets(filepath.Join(dir, "secrets"), b)
	if err != nil || len(files) != 2 {
		t.Fatalf("secrets: %v %v", files, err)
	}
	st, err := os.Stat(filepath.Join(dir, "secrets", SecretKeyFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode: %v %v", st, err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "secrets", InternalTokenFile))
	if string(got) != "tok" {
		t.Fatalf("token file %q", got)
	}
	state, err := WriteReposerverState(filepath.Join(dir, "state"), b)
	if err != nil || len(state) != 1 || filepath.Base(state[0]) != "r1.tar" {
		t.Fatalf("state: %v %v", state, err)
	}
}
