// SPDX-License-Identifier: Apache-2.0

// Package platform implements DBR²'s platform self-protection (ADR-0008):
// the Platform Recovery Bundle export (dbr2-server), opening and verifying
// a bundle with an escrow identity, and restoring it into a fresh
// installation (`dbr2-server admin restore-platform`).
//
// A bundle is a tar archive, compressed with zstd and encrypted with age to
// every escrow recipient (binary age, not armored):
//
//	db/<table>.copy            every table of the dbr2 database's public
//	                           schema (except goose_db_version) as
//	                           COPY … (FORMAT binary), read in one
//	                           REPEATABLE READ snapshot
//	secrets/dbr2_secret_key    DBR2_SECRET_KEY (seals the CA key and the
//	secrets/dbr2_internal_token      discovered secrets), the internal token
//	secrets/dbr2_entra_client_secret and the Entra client secret (if set)
//	reposerver/<id>.tar        each Repository's reposerver state (its
//	                           GET /v1/state-export archive)
//	manifest.json              LAST entry: versions, schema version, row
//	                           counts, the SHA-256 of every other entry
//
// The plaintext exists only inside dbr2-server (export) and inside the
// restore process; it is never written to disk unencrypted.
package platform

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"github.com/klauspost/compress/zstd"
)

// Bundle constants.
const (
	FormatVersion = 1
	Kind          = "dbr2-platform-recovery-bundle"
	ManifestName  = "manifest.json"
	// FileSuffix is the bundle file extension.
	FileSuffix = ".tar.zst.age"
	// GooseTable is the migration bookkeeping table (not exported: the
	// target is migrated to the bundle's schema version instead).
	GooseTable = "goose_db_version"
)

// Secret file names (identical to the Compose secret files).
const (
	SecretKeyFile         = "dbr2_secret_key"
	InternalTokenFile     = "dbr2_internal_token"
	EntraClientSecretFile = "dbr2_entra_client_secret"
)

// TemporalNote documents why the Temporal database is not in the bundle.
const TemporalNote = "The Temporal database is not included: platform recovery starts with a fresh Temporal " +
	"database (ADR-0008 §3). Schedules are recreated from PostgreSQL; in-flight workflows are reconciled. " +
	"Temporal forensic history is covered by the VM-level (Veeam) backup of the DBR² server."

// FileName returns the bundle file name for a creation time
// (dbr2-platform-20260925T021500Z.tar.zst.age).
func FileName(t time.Time) string {
	return "dbr2-platform-" + t.UTC().Format("20060102T150405Z") + FileSuffix
}

// Manifest is manifest.json. It never contains secret values.
type Manifest struct {
	FormatVersion   int               `json:"format_version"`
	Kind            string            `json:"kind"`
	CreatedAt       time.Time         `json:"created_at"`
	OrgID           string            `json:"org_id"`
	PlatformVersion string            `json:"platform_version"`
	Components      map[string]string `json:"components"`
	// SchemaVersion is the goose migration version of the exported database.
	SchemaVersion    int64             `json:"schema_version"`
	Tables           []Table           `json:"tables"`
	Secrets          []string          `json:"secrets"`
	Repositories     []RepositoryState `json:"repositories"`
	EscrowRecipients []string          `json:"escrow_recipients"`
	Temporal         TemporalInfo      `json:"temporal"`
	// Files maps every archive entry except manifest.json to its digest.
	Files map[string]FileDigest `json:"files"`
}

// Table is one exported table.
type Table struct {
	Name    string   `json:"name"`
	Rows    int64    `json:"rows"`
	Columns []string `json:"columns"`
	File    string   `json:"file"`
}

// FileDigest is the SHA-256 (hex) and size of an entry.
type FileDigest struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// RepositoryState records a Repository's reposerver state export.
type RepositoryState struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ManagementURL string `json:"management_url"`
	File          string `json:"file,omitempty"`
	// Missing is true when the reposerver was unreachable (the platform
	// backup is then Partial).
	Missing bool   `json:"missing,omitempty"`
	Error   string `json:"error,omitempty"`
}

// TemporalInfo states that Temporal is excluded.
type TemporalInfo struct {
	Included bool   `json:"included"`
	Note     string `json:"note"`
}

// MissingRepositories returns the IDs whose state is missing.
func (m *Manifest) MissingRepositories() []string {
	var out []string
	for _, r := range m.Repositories {
		if r.Missing {
			out = append(out, r.ID)
		}
	}
	return out
}

// ---- writing ---------------------------------------------------------------

// Writer writes a bundle: entries are added one at a time (each is hashed
// and recorded), and Close appends manifest.json and seals the stream.
type Writer struct {
	age  io.WriteCloser
	zw   *zstd.Encoder
	tw   *tar.Writer
	now  time.Time
	m    *Manifest
	done bool
}

// NewWriter starts a bundle encrypted to recipients (at least one).
func NewWriter(w io.Writer, recipients []age.Recipient, m *Manifest) (*Writer, error) {
	if len(recipients) == 0 {
		return nil, errors.New("platform: no escrow recipients configured; the bundle cannot be encrypted")
	}
	aw, err := age.Encrypt(w, recipients...)
	if err != nil {
		return nil, err
	}
	zw, err := zstd.NewWriter(aw, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, err
	}
	if m.Files == nil {
		m.Files = map[string]FileDigest{}
	}
	return &Writer{age: aw, zw: zw, tw: tar.NewWriter(zw), now: m.CreatedAt, m: m}, nil
}

// Add writes one entry (0600 regular file) and records its digest.
func (b *Writer) Add(name string, data []byte) error {
	if b.done {
		return errors.New("platform: bundle already closed")
	}
	if err := validName(name); err != nil || name == ManifestName {
		return fmt.Errorf("platform: invalid bundle entry %q", name)
	}
	if _, dup := b.m.Files[name]; dup {
		return fmt.Errorf("platform: duplicate bundle entry %q", name)
	}
	if err := b.tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: b.now,
		Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		return err
	}
	if _, err := b.tw.Write(data); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	b.m.Files[name] = FileDigest{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
	return nil
}

// Close writes manifest.json and finishes the encrypted stream. It returns
// the manifest as written.
func (b *Writer) Close() ([]byte, error) {
	if b.done {
		return nil, errors.New("platform: bundle already closed")
	}
	b.done = true
	mj, err := json.MarshalIndent(b.m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := b.tw.WriteHeader(&tar.Header{Name: ManifestName, Mode: 0o600, Size: int64(len(mj)), ModTime: b.now,
		Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		return nil, err
	}
	if _, err := b.tw.Write(mj); err != nil {
		return nil, err
	}
	if err := b.tw.Close(); err != nil {
		return nil, err
	}
	if err := b.zw.Close(); err != nil {
		return nil, err
	}
	if err := b.age.Close(); err != nil {
		return nil, err
	}
	return mj, nil
}

func validName(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || path.Clean(name) != name || strings.Contains(name, "..") {
		return fmt.Errorf("invalid entry name %q", name)
	}
	return nil
}

// ---- reading ---------------------------------------------------------------

// Bundle is a decrypted, verified bundle held in memory.
type Bundle struct {
	Manifest     Manifest
	ManifestJSON []byte
	Files        map[string][]byte
}

// ErrVerify is returned when a bundle fails verification.
var ErrVerify = errors.New("platform: bundle verification failed")

// MaxEntrySize bounds a single decompressed entry (defense against
// decompression bombs; the platform database is far smaller).
const MaxEntrySize = 8 << 30

// Open decrypts a bundle with the escrow identities and verifies every entry
// against manifest.json. Tampering is detected by age (authenticated
// encryption) and by the digests.
func Open(r io.Reader, ids ...age.Identity) (*Bundle, error) {
	if len(ids) == 0 {
		return nil, errors.New("platform: no escrow identity given")
	}
	dr, err := age.Decrypt(r, ids...)
	if err != nil {
		return nil, fmt.Errorf("platform: decrypt bundle: %w", err)
	}
	zr, err := zstd.NewReader(dr)
	if err != nil {
		return nil, fmt.Errorf("platform: decompress bundle: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	b := &Bundle{Files: map[string][]byte{}}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: read archive: %v", ErrVerify, err)
		}
		if h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("%w: entry %q is not a regular file", ErrVerify, h.Name)
		}
		if err := validName(h.Name); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrVerify, err)
		}
		if h.Size < 0 || h.Size > MaxEntrySize {
			return nil, fmt.Errorf("%w: entry %q is too large", ErrVerify, h.Name)
		}
		if _, dup := b.Files[h.Name]; dup || (h.Name == ManifestName && b.ManifestJSON != nil) {
			return nil, fmt.Errorf("%w: duplicate entry %q", ErrVerify, h.Name)
		}
		data := make([]byte, h.Size)
		if _, err := io.ReadFull(tr, data); err != nil {
			return nil, fmt.Errorf("%w: read %q: %v", ErrVerify, h.Name, err)
		}
		if h.Name == ManifestName {
			b.ManifestJSON = data
			continue
		}
		b.Files[h.Name] = data
	}
	// Drain to the end of the age stream so a truncated file fails the
	// authentication of its last chunk.
	if _, err := io.Copy(io.Discard, zr); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrVerify, err)
	}
	if b.ManifestJSON == nil {
		return nil, fmt.Errorf("%w: manifest.json is missing (truncated bundle?)", ErrVerify)
	}
	if err := json.Unmarshal(b.ManifestJSON, &b.Manifest); err != nil {
		return nil, fmt.Errorf("%w: manifest.json: %v", ErrVerify, err)
	}
	if err := b.verify(); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Bundle) verify() error {
	m := &b.Manifest
	if m.Kind != Kind || m.FormatVersion != FormatVersion {
		return fmt.Errorf("%w: unsupported bundle (kind %q, format_version %d)", ErrVerify, m.Kind, m.FormatVersion)
	}
	if len(m.Files) != len(b.Files) {
		return fmt.Errorf("%w: manifest lists %d entries, archive has %d", ErrVerify, len(m.Files), len(b.Files))
	}
	names := make([]string, 0, len(b.Files))
	for n := range b.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d, ok := m.Files[n]
		if !ok {
			return fmt.Errorf("%w: entry %q is not in the manifest", ErrVerify, n)
		}
		sum := sha256.Sum256(b.Files[n])
		if hex.EncodeToString(sum[:]) != d.SHA256 || int64(len(b.Files[n])) != d.Size {
			return fmt.Errorf("%w: SHA-256 mismatch for %q", ErrVerify, n)
		}
	}
	for _, t := range m.Tables {
		if _, ok := b.Files[t.File]; !ok || t.File != "db/"+t.Name+".copy" {
			return fmt.Errorf("%w: table %q has no data entry", ErrVerify, t.Name)
		}
	}
	for _, r := range m.Repositories {
		if r.Missing {
			continue
		}
		if _, ok := b.Files[r.File]; !ok {
			return fmt.Errorf("%w: reposerver state of %s is missing", ErrVerify, r.ID)
		}
	}
	if _, ok := b.Files["secrets/"+SecretKeyFile]; !ok {
		return fmt.Errorf("%w: secrets/%s is missing", ErrVerify, SecretKeyFile)
	}
	return nil
}

// Secret returns a secret file's content ("" when absent).
func (b *Bundle) Secret(name string) string { return string(b.Files["secrets/"+name]) }

// LoadIdentities reads age identities from a file: an age X25519 identity
// file (AGE-SECRET-KEY-1…, as written by age-keygen) or an unencrypted SSH
// private key (ed25519 or RSA).
func LoadIdentities(file string) ([]age.Identity, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return ParseIdentities(b)
}

// ParseIdentities parses identity file contents (see LoadIdentities).
func ParseIdentities(b []byte) ([]age.Identity, error) {
	if bytes.Contains(b, []byte("PRIVATE KEY-----")) {
		id, err := agessh.ParseIdentity(b)
		if err != nil {
			return nil, fmt.Errorf("platform: SSH identity: %w (decrypt a passphrase-protected key to a temporary file first)", err)
		}
		return []age.Identity{id}, nil
	}
	ids, err := age.ParseIdentities(bufio.NewReader(bytes.NewReader(b)))
	if err != nil {
		return nil, fmt.Errorf("platform: age identity: %w", err)
	}
	return ids, nil
}
