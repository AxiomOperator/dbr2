// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP-aligned; ~tens of ms per hash on server CPUs).
const (
	argonMemoryKiB = 64 * 1024
	argonTime      = 3
	argonThreads   = 2
	argonKeyLen    = 32
	argonSaltLen   = 16
)

// MinPasswordLength is the minimum master admin password length.
const MinPasswordLength = 12

// ErrWeakPassword is returned when a new password violates the policy.
var ErrWeakPassword = errors.New("password does not meet the policy")

// HashPassword returns a PHC-formatted Argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks password against a PHC Argon2id hash in constant time.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("auth: unsupported password hash format")
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return false, errors.New("auth: unsupported argon2 version")
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, errors.New("auth: malformed argon2 parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is verified when no account matches, so response time does not
// reveal whether a username exists.
var dummyHash, _ = HashPassword("dbr2-timing-equaliser-not-a-real-password")

// CheckPasswordPolicy validates a new master admin password.
func CheckPasswordPolicy(password, username string) error {
	n := utf8.RuneCountInString(password)
	switch {
	case n < MinPasswordLength:
		return fmt.Errorf("%w: must be at least %d characters", ErrWeakPassword, MinPasswordLength)
	case n > 256:
		return fmt.Errorf("%w: must be at most 256 characters", ErrWeakPassword)
	case strings.EqualFold(password, username):
		return fmt.Errorf("%w: must not equal the username", ErrWeakPassword)
	case len(strings.Trim(password, password[:1])) == 0:
		return fmt.Errorf("%w: must not repeat a single character", ErrWeakPassword)
	}
	return nil
}

// GeneratePassword returns a random password (base64url, ~143 bits).
func GeneratePassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
