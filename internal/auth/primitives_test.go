// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifyPassword("correct horse battery staple", h); !ok || err != nil {
		t.Fatalf("verify failed: %v %v", ok, err)
	}
	if ok, _ := VerifyPassword("wrong", h); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse battery staple")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
	if _, err := VerifyPassword("x", "$bcrypt$..."); err == nil {
		t.Fatal("expected format error")
	}
}

func TestPasswordPolicy(t *testing.T) {
	for _, pw := range []string{"short", "aaaaaaaaaaaaaaa", "dbr2-admin-xyz"} {
		user := "dbr2-admin-xyz"
		if err := CheckPasswordPolicy(pw, user); !errors.Is(err, ErrWeakPassword) {
			t.Errorf("%q accepted", pw)
		}
	}
	if err := CheckPasswordPolicy("a-perfectly-fine-passphrase", "dbr2-admin"); err != nil {
		t.Fatal(err)
	}
	g, _ := GeneratePassword()
	if CheckPasswordPolicy(g, "dbr2-admin") != nil {
		t.Fatal("generated password violates policy")
	}
}

func TestLockDurationIsProgressiveAndCapped(t *testing.T) {
	want := map[int]time.Duration{0: 0, 4: 0, 5: time.Minute, 6: 2 * time.Minute, 7: 4 * time.Minute, 20: time.Hour, 200: time.Hour}
	for n, d := range want {
		if got := LockDuration(n); got != d {
			t.Errorf("LockDuration(%d) = %v, want %v", n, got, d)
		}
	}
}

func TestRateLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewRateLimiter(3, time.Minute)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("attempt %d rejected", i)
		}
	}
	ok, wait := l.Allow("1.2.3.4")
	if ok || wait != time.Minute {
		t.Fatalf("4th attempt: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("5.6.7.8"); !ok {
		t.Fatal("other key throttled")
	}
	now = now.Add(time.Minute)
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("window did not reset")
	}
}

func TestSecretBoxBindsAssociatedData(t *testing.T) {
	b, err := NewSecretBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := b.Seal([]byte("seed"), "totp:user-1")
	if got, err := b.Open(sealed, "totp:user-1"); err != nil || string(got) != "seed" {
		t.Fatalf("open: %v %q", err, got)
	}
	if _, err := b.Open(sealed, "totp:user-2"); err == nil {
		t.Fatal("ciphertext accepted under different associated data")
	}
	if _, err := NewSecretBox([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestTOTPMatchAndSkew(t *testing.T) {
	key, err := NewTOTPKey("dbr2-admin")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	code, _ := totp.GenerateCodeCustom(key.Secret(), now, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	step, ok := MatchTOTP(key.Secret(), code, now)
	if !ok || step != now.Unix()/30 {
		t.Fatalf("match failed: %v %d", ok, step)
	}
	if _, ok := MatchTOTP(key.Secret(), code, now.Add(30*time.Second)); !ok {
		t.Fatal("previous step should be accepted within skew")
	}
	if _, ok := MatchTOTP(key.Secret(), code, now.Add(2*time.Minute)); ok {
		t.Fatal("stale code accepted")
	}
	if _, ok := MatchTOTP(key.Secret(), "12345", now); ok {
		t.Fatal("malformed code accepted")
	}
}

func TestTokens(t *testing.T) {
	a, _ := NewToken(SessionTokenPrefix)
	b, _ := NewToken(SessionTokenPrefix)
	if a == b || !HasPrefix(a, SessionTokenPrefix) || len(HashToken(a)) != 32 {
		t.Fatal("bad token generation")
	}
}
