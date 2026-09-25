// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/subtle"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	totpPeriod = 30
	totpSkew   = 1 // accept the previous and next 30 s step
	totpIssuer = "DBR2"
)

// NewTOTPKey generates a TOTP secret for account.
func NewTOTPKey(account string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: account,
		Period:      totpPeriod,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
}

// MatchTOTP validates code against secret at time now and returns the matched
// time step. Callers persist the step and reject any step <= the last used
// one, which makes every code single-use (replay protection).
func MatchTOTP(secret, code string, now time.Time) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	current := now.Unix() / totpPeriod
	for d := -totpSkew; d <= totpSkew; d++ {
		step := current + int64(d)
		want, err := totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriod, 0).UTC(), totp.ValidateOpts{
			Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
