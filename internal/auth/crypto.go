// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// SecretBox encrypts small secrets at rest (e.g. TOTP seeds) with AES-256-GCM
// from the Go standard library; the associated data binds each ciphertext to
// its purpose and owner so values cannot be swapped between rows.
type SecretBox struct{ aead cipher.AEAD }

// NewSecretBox builds a SecretBox from a 32-byte key (DBR2_SECRET_KEY).
func NewSecretBox(key []byte) (*SecretBox, error) {
	if len(key) != 32 {
		return nil, errors.New("auth: secret key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretBox{aead: aead}, nil
}

// Seal encrypts plaintext; the output is nonce || ciphertext.
func (b *SecretBox) Seal(plaintext []byte, ad string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, []byte(ad)), nil
}

// Open decrypts a value produced by Seal with the same associated data.
func (b *SecretBox) Open(sealed []byte, ad string) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("auth: ciphertext too short")
	}
	return b.aead.Open(nil, sealed[:n], sealed[n:], []byte(ad))
}

// Token prefixes make leaked credentials recognisable (secret scanning).
const (
	SessionTokenPrefix = "dbr2s_"
	APITokenPrefix     = "dbr2pat_"
)

// NewToken returns prefix + 32 random bytes (base64url).
func NewToken(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the SHA-256 digest stored instead of the token itself.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// HasPrefix reports whether token carries the given DBR² prefix.
func HasPrefix(token, prefix string) bool { return strings.HasPrefix(token, prefix) }
