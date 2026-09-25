// SPDX-License-Identifier: Apache-2.0
"use client";

import { useCurrentUser } from "@/components/auth-guard";
import { ChangePasswordCard } from "@/components/settings/change-password-card";
import { TotpCard } from "@/components/settings/totp-card";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export function SecuritySettings() {
  const me = useCurrentUser();

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Security settings</h1>
        <p className="text-sm text-muted-foreground">
          Signed in as <span className="font-medium text-foreground">{me.username}</span>
          {me.email ? ` (${me.email})` : ""}.
        </p>
      </div>

      {me.kind === "master_admin" ? (
        <div className="grid gap-6 lg:grid-cols-2">
          <ChangePasswordCard />
          <TotpCard totpEnabled={me.totp_enabled} />
        </div>
      ) : (
        <Card className="max-w-2xl">
          <CardHeader>
            <CardTitle>Managed by your identity provider</CardTitle>
            <CardDescription>You signed in with single sign-on.</CardDescription>
          </CardHeader>
          <CardContent className="text-sm">
            Your password and multi-factor authentication are managed by your organisation&apos;s
            identity provider (for example Microsoft Entra ID), not by DBR².
          </CardContent>
        </Card>
      )}
    </div>
  );
}
