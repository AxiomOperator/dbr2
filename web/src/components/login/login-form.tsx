// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ErrorCodes, errorMessage, formatRetryAfter, isApiError } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import { LoginRequestSchema, TotpCodeSchema, type Me } from "@/lib/api/schemas";

type Step = "password" | "totp";

/** Maps a login failure to the message shown to the operator. */
export function loginErrorMessage(err: unknown): string {
  if (!isApiError(err)) return errorMessage(err);
  switch (err.code) {
    case ErrorCodes.invalidCredentials:
      return "Incorrect username or password.";
    case ErrorCodes.invalidTotp:
      return "That authentication code is not valid. Check your authenticator app and try again.";
    case ErrorCodes.accountLocked:
      return `This account is temporarily locked after too many failed sign-in attempts. ${formatRetryAfter(err.retryAfterSeconds)}`;
    case ErrorCodes.rateLimited:
      return `Too many sign-in attempts. ${formatRetryAfter(err.retryAfterSeconds)}`;
    default:
      return errorMessage(err);
  }
}

export interface LoginFormProps {
  onSuccess: (user: Me) => void;
}

/**
 * Master-admin username/password form with a second TOTP step when the
 * backend answers `401 totp_required`.
 */
export function LoginForm({ onSuccess }: LoginFormProps) {
  const id = useId();
  const [step, setStep] = useState<Step>("password");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const totpRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);

  const login = useMutation({
    mutationFn: api.login,
    meta: { skipSessionCheck: true },
    onSuccess: (res) => onSuccess(res.user),
    onError: (err) => {
      if (isApiError(err) && err.code === ErrorCodes.totpRequired) {
        setError(null);
        setTotpCode("");
        setStep("totp");
        return;
      }
      if (isApiError(err) && err.code === ErrorCodes.invalidCredentials) {
        setStep("password");
        setPassword("");
        setTotpCode("");
      }
      if (isApiError(err) && err.code === ErrorCodes.invalidTotp) {
        setTotpCode("");
      }
      setError(loginErrorMessage(err));
    },
  });

  // Move focus to the relevant field when the step changes (not on first render).
  const prevStep = useRef<Step>(step);
  useEffect(() => {
    if (prevStep.current === step) return;
    prevStep.current = step;
    if (step === "totp") totpRef.current?.focus();
    else passwordRef.current?.focus();
  }, [step]);

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    if (step === "totp") {
      const code = TotpCodeSchema.safeParse(totpCode);
      if (!code.success) {
        setError(code.error.issues[0]?.message ?? "Enter your 6-digit code.");
        return;
      }
    }
    const parsed = LoginRequestSchema.safeParse({
      username,
      password,
      totp_code: step === "totp" ? totpCode : undefined,
    });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Check the form and try again.");
      return;
    }
    login.mutate(parsed.data);
  }

  const errorId = `${id}-error`;
  const busy = login.isPending;

  return (
    <form onSubmit={submit} noValidate className="space-y-4" aria-describedby={error ? errorId : undefined}>
      {error && (
        <Alert variant="destructive" id={errorId}>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {step === "password" ? (
        <>
          <div className="space-y-2">
            <Label htmlFor={`${id}-username`}>Username</Label>
            <Input
              id={`${id}-username`}
              name="username"
              autoComplete="username"
              autoFocus
              autoCapitalize="none"
              spellCheck={false}
              required
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              disabled={busy}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-password`}>Password</Label>
            <Input
              id={`${id}-password`}
              ref={passwordRef}
              name="password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={busy}
            />
          </div>
        </>
      ) : (
        <div className="space-y-2">
          <Label htmlFor={`${id}-totp`}>Authentication code</Label>
          <p id={`${id}-totp-hint`} className="text-sm text-muted-foreground">
            Enter the 6-digit code from your authenticator app for <strong>{username}</strong>.
          </p>
          <Input
            id={`${id}-totp`}
            ref={totpRef}
            name="totp_code"
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9]{6}"
            maxLength={6}
            required
            aria-describedby={`${id}-totp-hint`}
            value={totpCode}
            onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, ""))}
            disabled={busy}
          />
        </div>
      )}

      <div className="flex gap-2">
        {step === "totp" && (
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              setStep("password");
              setTotpCode("");
              setError(null);
            }}
            disabled={busy}
          >
            Back
          </Button>
        )}
        <Button type="submit" className="flex-1" disabled={busy}>
          {busy ? "Signing in…" : step === "totp" ? "Verify" : "Sign in"}
        </Button>
      </div>
    </form>
  );
}
