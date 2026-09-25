// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ErrorCodes, errorMessage, isApiError } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import { ChangePasswordRequestSchema, MIN_PASSWORD_LENGTH } from "@/lib/api/schemas";

export function ChangePasswordCard() {
  const id = useId();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  const change = useMutation({
    mutationFn: api.changePassword,
    onSuccess: () => {
      setCurrent("");
      setNext("");
      setConfirm("");
      setDone(true);
    },
    onError: (err) => {
      if (isApiError(err) && err.code === ErrorCodes.weakPassword) {
        setError(err.detail || "The new password does not meet the password policy.");
      } else if (isApiError(err) && err.code === ErrorCodes.invalidCredentials) {
        setError("The current password is incorrect.");
      } else {
        setError(errorMessage(err));
      }
    },
  });

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setDone(false);
    const parsed = ChangePasswordRequestSchema.safeParse({
      current_password: current,
      new_password: next,
    });
    if (!parsed.success) {
      setError(parsed.error.issues[0]?.message ?? "Check the form and try again.");
      return;
    }
    if (next !== confirm) {
      setError("The new passwords do not match.");
      return;
    }
    if (next === current) {
      setError("The new password must be different from the current one.");
      return;
    }
    change.mutate(parsed.data);
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Change password</h2>
        </CardTitle>
        <CardDescription>
          Master admin password. Use at least {MIN_PASSWORD_LENGTH} characters; a passphrase is
          best.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} noValidate className="space-y-4">
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          {done && (
            <Alert role="status">
              <AlertDescription>Password changed.</AlertDescription>
            </Alert>
          )}
          <div className="space-y-2">
            <Label htmlFor={`${id}-current`}>Current password</Label>
            <Input
              id={`${id}-current`}
              type="password"
              autoComplete="current-password"
              required
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-new`}>New password</Label>
            <Input
              id={`${id}-new`}
              type="password"
              autoComplete="new-password"
              minLength={MIN_PASSWORD_LENGTH}
              required
              value={next}
              onChange={(e) => setNext(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-confirm`}>Confirm new password</Label>
            <Input
              id={`${id}-confirm`}
              type="password"
              autoComplete="new-password"
              required
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
          </div>
          <Button type="submit" disabled={change.isPending}>
            {change.isPending ? "Saving…" : "Change password"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
