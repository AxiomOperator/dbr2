// SPDX-License-Identifier: Apache-2.0

// Package escrow builds age-encrypted key-escrow packages (ADR-0008). A
// package is encrypted to every escrow recipient's public key, so creating
// one never needs the private identities kept offline in the safe. Each
// package carries a one-time confirmation code: an administrator proves the
// package was decrypted and stored by entering it.
package escrow

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"filippo.io/age/armor"
)

// Kind values.
const KindRepositoryPassword = "repository-password"

// Payload is the decrypted content of a package.
type Payload struct {
	FormatVersion    int       `json:"format_version"`
	Kind             string    `json:"kind"`
	CreatedAt        time.Time `json:"created_at"`
	RepositoryID     string    `json:"repository_id,omitempty"`
	RepositoryName   string    `json:"repository_name,omitempty"`
	KopiaRepository  string    `json:"kopia_repository_id,omitempty"`
	StoragePath      string    `json:"storage_path,omitempty"`
	Secret           string    `json:"secret"`
	ConfirmationCode string    `json:"confirmation_code"`
	Instructions     string    `json:"instructions"`
}

// RepositoryInstructions explain recovery without DBR² (ADR-0003).
const RepositoryInstructions = "This is the Kopia repository password of a DBR² Repository. " +
	"Store this file offline. To recover data without DBR²: mount the Repository storage read-only, then " +
	"`kopia repository connect filesystem --path <storage path>` with this password, " +
	"`kopia snapshot list --all --tags dbr2-kind:manifest` to find recovery manifests, and " +
	"`kopia restore <snapshot-id> <target>` for each component listed in a manifest."

// ParseRecipient accepts an age X25519 recipient (age1…) or an SSH public
// key (ssh-ed25519 / ssh-rsa).
func ParseRecipient(s string) (age.Recipient, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "age1") && !strings.HasPrefix(s, "age1pq"):
		return age.ParseX25519Recipient(s)
	case strings.HasPrefix(s, "ssh-"):
		return agessh.ParseRecipient(s)
	}
	return nil, errors.New("escrow: recipient must be an age X25519 public key (age1…) or an SSH ed25519/RSA public key")
}

// NewConfirmationCode returns a human-typeable code (XXXX-XXXX-XXXX-XXXX).
func NewConfirmationCode() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16], nil
}

// HashCode normalizes and hashes a confirmation code for storage.
func HashCode(code string) []byte {
	n := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	h := sha256.Sum256([]byte("dbr2-escrow-confirm:" + n))
	return h[:]
}

// CheckCode compares a submitted code with a stored hash in constant time.
func CheckCode(code string, hash []byte) bool {
	return len(hash) > 0 && subtle.ConstantTimeCompare(HashCode(code), hash) == 1
}

// Seal encrypts p to every recipient as an ASCII-armored age file.
func Seal(p Payload, recipients []string) ([]byte, error) {
	if len(recipients) == 0 {
		return nil, errors.New("escrow: no escrow recipients configured")
	}
	rs := make([]age.Recipient, 0, len(recipients))
	for _, r := range recipients {
		rc, err := ParseRecipient(r)
		if err != nil {
			return nil, err
		}
		rs = append(rs, rc)
	}
	if p.FormatVersion == 0 {
		p.FormatVersion = 1
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, rs...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if err := aw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Open decrypts a package with identities (escrow drills, tests and the
// platform recovery runbook).
func Open(pkg []byte, ids ...age.Identity) (*Payload, error) {
	r, err := age.Decrypt(armor.NewReader(bytes.NewReader(pkg)), ids...)
	if err != nil {
		return nil, fmt.Errorf("escrow: %w", err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var p Payload
	return &p, json.Unmarshal(b, &p)
}
