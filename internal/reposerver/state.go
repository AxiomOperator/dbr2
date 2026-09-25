// SPDX-License-Identifier: Apache-2.0

package reposerver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State is the reposerver's private state directory (0700):
//
//	repository-password   the Repository password (0600; with the admin's age
//	                      escrow package the ONLY copy — ADR-0002/ADR-0008)
//	control-password      Kopia server control password (0600)
//	tls.crt, tls.key      self-signed ECDSA P-256 server certificate (clients
//	                      pin its SHA-256 fingerprint)
//	repository.json       DBR² repository facts written on initialize
//	kopia/repository.config, kopia/cache/   Kopia's direct connection
type State struct{ Dir string }

// CertValidity is the server certificate lifetime. Clients pin the
// fingerprint (Kopia has no CA-chain option), so rotation is an explicit,
// coordinated operation rather than an expiry event.
const CertValidity = 10 * 365 * 24 * time.Hour

func (s State) path(name string) string { return filepath.Join(s.Dir, name) }

// PasswordFile is the repository password file.
func (s State) PasswordFile() string { return s.path("repository-password") }

// ControlPasswordFile is the Kopia server control password file.
func (s State) ControlPasswordFile() string { return s.path("control-password") }

// CertFile is the server certificate (PEM).
func (s State) CertFile() string { return s.path("tls.crt") }

// KeyFile is the server private key (PEM).
func (s State) KeyFile() string { return s.path("tls.key") }

// KopiaDir holds Kopia's config and cache.
func (s State) KopiaDir() string { return s.path("kopia") }

// ConfigFile is Kopia's direct repository config.
func (s State) ConfigFile() string { return filepath.Join(s.KopiaDir(), "repository.config") }

// CacheDir is Kopia's cache directory.
func (s State) CacheDir() string { return filepath.Join(s.KopiaDir(), "cache") }

func (s State) infoFile() string { return s.path("repository.json") }

// Prepare creates the directory tree with private permissions.
func (s State) Prepare() error {
	for _, d := range []string{s.Dir, s.KopiaDir(), s.CacheDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("state dir: %w", err)
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return fmt.Errorf("state dir: %w", err)
		}
	}
	return nil
}

// RepoInfo is persisted on a successful initialize.
type RepoInfo struct {
	RepositoryID  string    `json:"repository_id"`
	Splitter      string    `json:"splitter"`
	InitializedAt time.Time `json:"initialized_at"`
}

// LoadInfo returns the persisted repository facts (os.ErrNotExist before
// initialization).
func (s State) LoadInfo() (*RepoInfo, error) {
	b, err := os.ReadFile(s.infoFile())
	if err != nil {
		return nil, err
	}
	var ri RepoInfo
	if err := json.Unmarshal(b, &ri); err != nil {
		return nil, fmt.Errorf("%s: %w", s.infoFile(), err)
	}
	return &ri, nil
}

// SaveInfo persists ri atomically.
func (s State) SaveInfo(ri *RepoInfo) error {
	b, err := json.MarshalIndent(ri, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.infoFile(), append(b, '\n'), 0o600)
}

// ReadSecret reads a secret file ("" and os.ErrNotExist when absent).
func ReadSecret(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// EnsureControlPassword returns the control password, generating it once.
func (s State) EnsureControlPassword() (string, error) {
	pw, err := ReadSecret(s.ControlPasswordFile())
	if err == nil && pw != "" {
		return pw, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	pw, err = randomHex(32)
	if err != nil {
		return "", err
	}
	return pw, writeFileAtomic(s.ControlPasswordFile(), []byte(pw+"\n"), 0o600)
}

// EnsureCert returns the server certificate's SHA-256 fingerprint,
// generating a self-signed ECDSA P-256 certificate once. names are the SANs
// (DNS names or IP addresses) used when generating.
func (s State) EnsureCert(names []string) (fingerprint string, missing []string, err error) {
	if b, err := os.ReadFile(s.CertFile()); err == nil {
		if _, kerr := os.Stat(s.KeyFile()); kerr != nil {
			return "", nil, fmt.Errorf("%s exists but %s does not: %w", s.CertFile(), s.KeyFile(), kerr)
		}
		blk, _ := pem.Decode(b)
		if blk == nil || blk.Type != "CERTIFICATE" {
			return "", nil, fmt.Errorf("%s: not a PEM certificate", s.CertFile())
		}
		cert, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", s.CertFile(), err)
		}
		for _, n := range names {
			if cert.VerifyHostname(n) != nil {
				missing = append(missing, n)
			}
		}
		return Fingerprint(blk.Bytes), missing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	certPEM, keyPEM, der, err := SelfSignedCert(names, CertValidity)
	if err != nil {
		return "", nil, err
	}
	if err := writeFileAtomic(s.KeyFile(), keyPEM, 0o600); err != nil {
		return "", nil, err
	}
	if err := writeFileAtomic(s.CertFile(), certPEM, 0o644); err != nil {
		return "", nil, err
	}
	return Fingerprint(der), nil, nil
}

// SelfSignedCert creates a self-signed ECDSA P-256 TLS server certificate.
func SelfSignedCert(names []string, validity time.Duration) (certPEM, keyPEM, der []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "dbr2-reposerver", Organization: []string{"DBR²"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, nil, nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
	return certPEM, keyPEM, der, nil
}

// Fingerprint is the lowercase hex SHA-256 of a DER certificate — the value
// Kopia clients pin (repo.APIServerInfo.TrustedServerCertificateFingerprint).
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeFileAtomic writes via a temp file + rename, fsyncing the data.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) //nolint:errcheck // no-op after a successful rename
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
