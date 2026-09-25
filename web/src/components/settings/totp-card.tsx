// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import QRCode from "qrcode";
import { useEffect, useId, useState, type FormEvent } from "react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ErrorCodes, errorMessage, isApiError } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import { TotpCodeSchema, type TotpEnrollResponse } from "@/lib/api/schemas";

function totpErrorMessage(err: unknown): string {
  if (isApiError(err) && err.code === ErrorCodes.invalidTotp) {
    return "That code is not valid. Check the time on your device and try again.";
  }
  return errorMessage(err);
}

/** Renders an otpauth:// URL as an SVG QR code (generated locally, never sent anywhere). */
function OtpQrCode({ value }: { value: string }) {
  const [src, setSrc] = useState<string | null>(null);
  useEffect(() => {
    let cancelled = false;
    QRCode.toString(value, { type: "svg", errorCorrectionLevel: "M", margin: 2 })
      .then((svg) => {
        if (!cancelled) setSrc(`data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`);
      })
      .catch(() => {
        if (!cancelled) setSrc(null);
      });
    return () => {
      cancelled = true;
    };
  }, [value]);

  if (!src) return <div className="size-48 animate-pulse rounded-md bg-muted" aria-hidden="true" />;
  return (
    // eslint-disable-next-line @next/next/no-img-element -- data: URL, nothing to optimise
    <img
      src={src}
      alt="QR code for your authenticator app"
      width={192}
      height={192}
      className="size-48 rounded-md bg-white p-1"
    />
  );
}

function CodeField({
  id,
  label,
  value,
  onChange,
  disabled,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
}) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        inputMode="numeric"
        autoComplete="one-time-code"
        pattern="[0-9]{6}"
        maxLength={6}
        required
        className="max-w-40 font-mono tracking-widest"
        value={value}
        onChange={(e) => onChange(e.target.value.replace(/\D/g, ""))}
        disabled={disabled}
      />
    </div>
  );
}

export function TotpCard({ totpEnabled }: { totpEnabled: boolean }) {
  const id = useId();
  const queryClient = useQueryClient();
  const [enrollment, setEnrollment] = useState<TotpEnrollResponse | null>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const refreshMe = () => queryClient.invalidateQueries({ queryKey: queryKeys.me });

  const enroll = useMutation({
    mutationFn: api.totpEnroll,
    onSuccess: (data) => {
      setEnrollment(data);
      setCode("");
      setError(null);
      setNotice(null);
    },
    onError: (err) => setError(errorMessage(err)),
  });

  const confirm = useMutation({
    mutationFn: api.totpConfirm,
    onSuccess: async () => {
      setEnrollment(null);
      setCode("");
      setNotice("Two-factor authentication is now enabled.");
      await refreshMe();
    },
    onError: (err) => setError(totpErrorMessage(err)),
  });

  const disable = useMutation({
    mutationFn: api.totpDisable,
    onSuccess: async () => {
      setCode("");
      setNotice("Two-factor authentication has been disabled.");
      await refreshMe();
    },
    onError: (err) => setError(totpErrorMessage(err)),
  });

  function submitCode(e: FormEvent<HTMLFormElement>, action: "confirm" | "disable") {
    e.preventDefault();
    setError(null);
    setNotice(null);
    const parsed = TotpCodeSchema.safeParse(code);
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Enter your 6-digit code.");
      return;
    }
    (action === "confirm" ? confirm : disable).mutate({ code: parsed.data });
  }

  const busy = enroll.isPending || confirm.isPending || disable.isPending;

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Two-factor authentication</h2>
        </CardTitle>
        <CardDescription>
          Time-based one-time codes (TOTP) for the master admin. Optional, and recommended.
        </CardDescription>
        <CardAction>
          <Badge variant={totpEnabled ? "secondary" : "outline"}>
            {totpEnabled ? "Enabled" : "Disabled"}
          </Badge>
        </CardAction>
      </CardHeader>
      <CardContent className="space-y-4">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {notice && (
          <Alert role="status">
            <AlertDescription>{notice}</AlertDescription>
          </Alert>
        )}

        {totpEnabled && (
          <form onSubmit={(e) => submitCode(e, "disable")} noValidate className="space-y-4">
            <p className="text-sm text-muted-foreground">
              To turn off two-factor authentication, enter a current code from your authenticator
              app.
            </p>
            <CodeField
              id={`${id}-disable`}
              label="Authentication code"
              value={code}
              onChange={setCode}
              disabled={busy}
            />
            <Button type="submit" variant="destructive" disabled={busy}>
              {disable.isPending ? "Disabling…" : "Disable two-factor authentication"}
            </Button>
          </form>
        )}

        {!totpEnabled && !enrollment && (
          <Button onClick={() => enroll.mutate()} disabled={busy}>
            {enroll.isPending ? "Preparing…" : "Set up authenticator app"}
          </Button>
        )}

        {!totpEnabled && enrollment && (
          <form onSubmit={(e) => submitCode(e, "confirm")} noValidate className="space-y-4">
            <ol className="list-decimal space-y-3 pl-5 text-sm">
              <li>
                Scan this QR code with your authenticator app.
                <div className="mt-2">
                  <OtpQrCode value={enrollment.otpauth_url} />
                </div>
              </li>
              <li>
                Or enter this secret manually:{" "}
                <code className="rounded bg-muted px-1.5 py-0.5 font-mono break-all">
                  {enrollment.secret}
                </code>
              </li>
              <li>Enter the 6-digit code the app shows to finish.</li>
            </ol>
            <CodeField
              id={`${id}-confirm`}
              label="Authentication code"
              value={code}
              onChange={setCode}
              disabled={busy}
            />
            <div className="flex gap-2">
              <Button type="submit" disabled={busy}>
                {confirm.isPending ? "Verifying…" : "Enable"}
              </Button>
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setEnrollment(null);
                  setCode("");
                  setError(null);
                }}
                disabled={busy}
              >
                Cancel
              </Button>
            </div>
          </form>
        )}
      </CardContent>
    </Card>
  );
}
