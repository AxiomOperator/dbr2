// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AxiomOperator/dbr2/internal/auth"
)

// Problem is the RFC 9457 error body with a stable machine-readable code.
type Problem struct {
	Title  string              `json:"title" doc:"Short summary of the problem type." example:"Unauthorized"`
	Status int                 `json:"status" doc:"HTTP status code." example:"401"`
	Detail string              `json:"detail,omitempty" doc:"Explanation specific to this occurrence." example:"invalid username or password"`
	Code   string              `json:"code,omitempty" doc:"Stable machine-readable error code." example:"invalid_credentials"`
	Errors []*huma.ErrorDetail `json:"errors,omitempty" doc:"Individual validation errors."`
}

func (p *Problem) Error() string  { return p.Detail }
func (p *Problem) GetStatus() int { return p.Status }
func (p *Problem) ContentType(ct string) string {
	if ct == "application/json" {
		return "application/problem+json"
	}
	return ct
}

// Error codes returned in Problem.Code.
const (
	CodeUnauthorized       = "unauthorized"
	CodeForbidden          = "forbidden"
	CodeCSRF               = "csrf_rejected"
	CodeInvalidCredentials = "invalid_credentials"
	CodeTOTPRequired       = "totp_required"
	CodeInvalidTOTP        = "invalid_totp"
	CodeAccountLocked      = "account_locked"
	CodeRateLimited        = "rate_limited"
	CodeWeakPassword       = "weak_password"
	CodeNotFound           = "not_found"
	CodeValidation         = "validation_failed"
	CodeConflict           = "conflict"
	CodeInternal           = "internal_error"
)

func init() {
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		p := &Problem{Status: status, Title: http.StatusText(status), Detail: msg}
		for _, e := range errs {
			if e == nil {
				continue
			}
			var d huma.ErrorDetailer
			if errors.As(e, &d) {
				p.Errors = append(p.Errors, d.ErrorDetail())
			} else {
				p.Errors = append(p.Errors, &huma.ErrorDetail{Message: e.Error()})
			}
		}
		switch status {
		case http.StatusUnauthorized:
			p.Code = CodeUnauthorized
		case http.StatusForbidden:
			p.Code = CodeForbidden
		case http.StatusNotFound:
			p.Code = CodeNotFound
		case http.StatusUnprocessableEntity, http.StatusBadRequest:
			p.Code = CodeValidation
		case http.StatusInternalServerError:
			p.Code = CodeInternal
		}
		return p
	}
}

func problem(status int, code, detail string) *Problem {
	return &Problem{Status: status, Title: http.StatusText(status), Detail: detail, Code: code}
}

func withRetryAfter(p *Problem, d time.Duration) error {
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return huma.ErrorWithHeaders(p, http.Header{"Retry-After": {strconv.Itoa(secs)}})
}

// fail converts service errors into API problems. Unknown errors become a
// generic 500: the details are logged with the request ID, never returned.
func (d *Deps) fail(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if mapped := mapError(err); mapped != nil {
		return mapped
	}
	d.Log.ErrorContext(ctx, "request failed", "err", err, "request_id", metaFrom(ctx).RequestID)
	return problem(http.StatusInternalServerError, CodeInternal, "internal error (see server logs for request "+metaFrom(ctx).RequestID+")")
}

// mapError maps known service errors; it returns nil for unknown errors.
func mapError(err error) error {
	var locked *auth.LockedError
	var limited *auth.RateLimitedError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &locked):
		return withRetryAfter(problem(http.StatusLocked, CodeAccountLocked, "account temporarily locked after repeated failures"), locked.RetryAfter)
	case errors.As(err, &limited):
		return withRetryAfter(problem(http.StatusTooManyRequests, CodeRateLimited, "too many login attempts"), limited.RetryAfter)
	case errors.Is(err, auth.ErrInvalidCredentials):
		return problem(http.StatusUnauthorized, CodeInvalidCredentials, "invalid username or password")
	case errors.Is(err, auth.ErrTOTPRequired):
		return problem(http.StatusUnauthorized, CodeTOTPRequired, "a TOTP code is required")
	case errors.Is(err, auth.ErrInvalidTOTP):
		return problem(http.StatusUnauthorized, CodeInvalidTOTP, "invalid TOTP code")
	case errors.Is(err, auth.ErrWrongCurrentPassword):
		return problem(http.StatusBadRequest, CodeInvalidCredentials, "current password is incorrect")
	case errors.Is(err, auth.ErrWeakPassword):
		return problem(http.StatusBadRequest, CodeWeakPassword, err.Error())
	case errors.Is(err, auth.ErrNotLocalAccount), errors.Is(err, auth.ErrMasterAdminImmutable), errors.Is(err, auth.ErrSelfLockout):
		return problem(http.StatusForbidden, CodeForbidden, err.Error())
	case errors.Is(err, auth.ErrInvalidRole), errors.Is(err, auth.ErrOIDCUnknown), errors.Is(err, auth.ErrTOTPNotPending):
		return problem(http.StatusBadRequest, CodeValidation, err.Error())
	case errors.Is(err, auth.ErrNotFound):
		return problem(http.StatusNotFound, CodeNotFound, "not found")
	}
	return nil
}
