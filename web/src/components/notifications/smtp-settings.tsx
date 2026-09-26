// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { AccessDenied, QueryError, RowsSkeleton } from "@/components/common/states";
import { useToast } from "@/components/toast";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { actionErrorMessage } from "@/lib/api/client";
import { api, queryKeys } from "@/lib/api/endpoints";
import {
  SMTP_TLS_LABEL,
  SMTP_TLS_MODES,
  SmtpRequestSchema,
  type SmtpRequest,
  type SmtpSettings,
  type SmtpTls,
} from "@/lib/api/policy-schemas";
import { PERMISSION_POLICY_MANAGE, PERMISSION_POLICY_READ } from "@/lib/api/protection-schemas";
import { useSmtpSettings } from "@/lib/api/hooks";
import { formatDateTime } from "@/lib/format";
import { secretField, type SecretAction } from "@/lib/notifications";

export interface SmtpDraft {
  host: string;
  port: string;
  tls: SmtpTls;
  username: string;
  from: string;
  passwordAction: SecretAction;
  password: string;
}

export function smtpToDraft(s: SmtpSettings): SmtpDraft {
  return {
    host: s.host,
    port: s.port ? String(s.port) : "587",
    tls: s.tls || "starttls",
    username: s.username,
    from: s.from,
    passwordAction: "keep",
    password: "",
  };
}

/** PUT body: `password` omitted = keep, "" = remove, value = replace. */
export function smtpDraftToRequest(d: SmtpDraft): { ok: true; value: SmtpRequest } | { ok: false; error: string } {
  const port = Number(d.port);
  if (d.passwordAction === "replace" && d.password === "") return { ok: false, error: "Enter the new password, or keep the stored one." };
  const password = secretField(d.passwordAction, d.password);
  const parsed = SmtpRequestSchema.safeParse({
    host: d.host,
    port: d.port.trim() === "" ? NaN : port,
    tls: d.tls,
    username: d.username.trim() || undefined,
    from: d.from,
    ...(password !== undefined ? { password } : {}),
  });
  if (!parsed.success) return { ok: false, error: parsed.error.issues[0]?.message ?? "Check the form." };
  return { ok: true, value: parsed.data };
}

function SmtpForm({ initial, canManage }: { initial: SmtpSettings; canManage: boolean }) {
  const id = useId();
  const toast = useToast();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<SmtpDraft>(() => smtpToDraft(initial));
  const [error, setError] = useState<string | null>(null);
  const set = (p: Partial<SmtpDraft>) => setDraft((d) => ({ ...d, ...p }));
  const save = useMutation({
    mutationFn: api.updateSmtpSettings,
    onSuccess: (saved) => {
      queryClient.setQueryData(queryKeys.smtpSettings, saved);
      toast({ title: "SMTP settings saved", description: "Use “Send test” on an email channel to check them." });
    },
    onError: (err) => setError(actionErrorMessage(err)),
  });
  const disabled = !canManage || save.isPending;
  const pwOptions: { value: SecretAction; label: string }[] = initial.password_set
    ? [
        { value: "keep", label: "Keep the stored password" },
        { value: "replace", label: "Replace it" },
        { value: "remove", label: "Remove it" },
      ]
    : [
        { value: "keep", label: "No password" },
        { value: "replace", label: "Set a password" },
      ];

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const res = smtpDraftToRequest(draft);
    if (!res.ok) {
      setError(res.error);
      return;
    }
    save.mutate(res.value);
  }

  return (
    <form onSubmit={submit} noValidate>
      <CardContent className="space-y-4">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <div className="grid gap-4 sm:grid-cols-[1fr_8rem]">
          <div className="space-y-2">
            <Label htmlFor={`${id}-host`}>SMTP host</Label>
            <Input
              id={`${id}-host`}
              value={draft.host}
              onChange={(e) => set({ host: e.target.value })}
              placeholder="smtp.example.com"
              spellCheck={false}
              aria-required="true"
              disabled={disabled}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-port`}>Port</Label>
            <Input
              id={`${id}-port`}
              type="number"
              inputMode="numeric"
              min={1}
              max={65535}
              value={draft.port}
              onChange={(e) => set({ port: e.target.value })}
              disabled={disabled}
            />
          </div>
        </div>
        <div className="space-y-2">
          <Label htmlFor={`${id}-tls`}>Encryption</Label>
          <Select
            value={draft.tls}
            onValueChange={(v) => set({ tls: v as SmtpTls, port: v === "tls" && draft.port === "587" ? "465" : v === "starttls" && draft.port === "465" ? "587" : draft.port })}
            disabled={disabled}
          >
            <SelectTrigger id={`${id}-tls`} className="w-full sm:w-96">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {SMTP_TLS_MODES.map((m) => (
                <SelectItem key={m} value={m}>
                  {SMTP_TLS_LABEL[m]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-2">
          <Label htmlFor={`${id}-from`}>Sender</Label>
          <Input
            id={`${id}-from`}
            value={draft.from}
            onChange={(e) => set({ from: e.target.value })}
            placeholder="DBR2 <dbr2@example.com>"
            aria-required="true"
            disabled={disabled}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor={`${id}-user`}>Username (optional)</Label>
          <Input
            id={`${id}-user`}
            value={draft.username}
            onChange={(e) => set({ username: e.target.value })}
            autoComplete="off"
            aria-describedby={`${id}-user-help`}
            disabled={disabled}
          />
          <p id={`${id}-user-help`} className="text-xs text-muted-foreground">
            PLAIN authentication is used when a username is set.
          </p>
        </div>
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">
            Password{" "}
            <span className="font-normal text-muted-foreground">
              ({initial.password_set ? "a password is stored" : "none stored"}; write-only)
            </span>
          </legend>
          <div className="flex flex-col gap-1.5" role="radiogroup" aria-label="Password">
            {pwOptions.map((o) => (
              <label key={o.value} className="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name={`${id}-pw`}
                  value={o.value}
                  checked={draft.passwordAction === o.value}
                  onChange={() => set({ passwordAction: o.value, password: "" })}
                  disabled={disabled}
                  className="size-4 accent-primary"
                />
                {o.label}
              </label>
            ))}
          </div>
          {draft.passwordAction === "replace" && (
            <div className="space-y-1.5">
              <Label htmlFor={`${id}-pw`}>{initial.password_set ? "New password" : "Password"}</Label>
              <Input
                id={`${id}-pw`}
                type="password"
                autoComplete="new-password"
                value={draft.password}
                onChange={(e) => set({ password: e.target.value })}
                maxLength={500}
                className="max-w-80"
                disabled={disabled}
              />
            </div>
          )}
        </fieldset>
      </CardContent>
      <CardFooter className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-xs text-muted-foreground">
          {initial.updated_at ? `Last changed ${formatDateTime(initial.updated_at)}. ` : ""}Changes are audited.
        </p>
        {canManage && (
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save SMTP settings"}
          </Button>
        )}
      </CardFooter>
    </form>
  );
}

export function SmtpSettingsPanel() {
  const me = useCurrentUser();
  const canRead = hasPermission(me, PERMISSION_POLICY_READ);
  const canManage = hasPermission(me, PERMISSION_POLICY_MANAGE);
  const smtp = useSmtpSettings({ enabled: canRead });
  if (!canRead) return <AccessDenied what="Viewing the SMTP settings" permission={PERMISSION_POLICY_READ} />;
  return (
    <Card className="max-w-3xl">
      <CardHeader>
        <CardTitle>
          <h2 className="flex flex-wrap items-center gap-2">
            SMTP relay
            {smtp.data &&
              (smtp.data.configured ? (
                <Badge variant="secondary">Configured</Badge>
              ) : (
                <Badge variant="outline" className="border-amber-500/60 text-amber-700 dark:text-amber-400">
                  Not configured
                </Badge>
              ))}
          </h2>
        </CardTitle>
        <CardDescription>
          Email channels send through this relay.
          {!canManage && (
            <>
              {" "}
              Read-only: editing requires <code>{PERMISSION_POLICY_MANAGE}</code>.
            </>
          )}
        </CardDescription>
      </CardHeader>
      {smtp.isPending ? (
        <CardContent>
          <RowsSkeleton label="Loading the SMTP settings…" rows={4} />
        </CardContent>
      ) : smtp.isError ? (
        <CardContent>
          <QueryError title="Could not load the SMTP settings" error={smtp.error} onRetry={() => void smtp.refetch()} />
        </CardContent>
      ) : (
        <SmtpForm key={smtp.data.updated_at ?? "new"} initial={smtp.data} canManage={canManage} />
      )}
    </Card>
  );
}
