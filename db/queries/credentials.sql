-- SPDX-License-Identifier: Apache-2.0

-- name: CreateLocalCredential :exec
INSERT INTO local_credentials (user_id, password_hash, must_change_password)
VALUES ($1, $2, $3);

-- name: GetLocalCredentialForUpdate :one
SELECT * FROM local_credentials WHERE user_id = $1 FOR UPDATE;

-- name: GetLocalCredential :one
SELECT * FROM local_credentials WHERE user_id = $1;

-- name: RecordLoginFailure :exec
UPDATE local_credentials SET failed_attempts = $2, locked_until = $3 WHERE user_id = $1;

-- name: ResetLoginFailures :exec
UPDATE local_credentials SET failed_attempts = 0, locked_until = NULL WHERE user_id = $1;

-- name: UpdatePassword :exec
UPDATE local_credentials
SET password_hash = $2, must_change_password = $3, password_changed_at = now(),
    failed_attempts = 0, locked_until = NULL
WHERE user_id = $1;

-- name: SetPendingTOTP :exec
UPDATE local_credentials SET totp_pending_secret_enc = $2 WHERE user_id = $1;

-- name: EnableTOTP :exec
UPDATE local_credentials
SET totp_secret_enc = totp_pending_secret_enc, totp_pending_secret_enc = NULL,
    totp_enabled = true, totp_last_step = $2
WHERE user_id = $1 AND totp_pending_secret_enc IS NOT NULL;

-- name: DisableTOTP :exec
UPDATE local_credentials
SET totp_secret_enc = NULL, totp_pending_secret_enc = NULL, totp_enabled = false, totp_last_step = 0
WHERE user_id = $1;

-- name: AdvanceTOTPStep :execrows
-- Rejects replay: a TOTP time step can be used at most once.
UPDATE local_credentials SET totp_last_step = $2 WHERE user_id = $1 AND totp_last_step < $2;
