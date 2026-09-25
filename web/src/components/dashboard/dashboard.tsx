// SPDX-License-Identifier: Apache-2.0
"use client";

import { ExternalLinkIcon } from "lucide-react";
import { useCurrentUser } from "@/components/auth-guard";
import { PlatformStatusCard } from "@/components/dashboard/platform-status-card";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { API_DOCS_PATH } from "@/lib/version";

export function Dashboard() {
  const me = useCurrentUser();

  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">Welcome, {me.display_name}</h1>
        <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
          <span>Roles:</span>
          {me.roles.length === 0 ? (
            <span>none assigned</span>
          ) : (
            <ul className="flex flex-wrap gap-1.5" aria-label="Your roles">
              {me.roles.map((role) => (
                <li key={role}>
                  <Badge variant="secondary">{role}</Badge>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      <div className="grid gap-6 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Getting started</CardTitle>
            <CardDescription>This is the Phase 1 console foundation.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4 text-sm">
            <p>
              Docker hosts, applications, backup policies, recovery points and restores arrive in
              later phases. Until then you can manage your sign-in security, review the audit log
              and explore the API.
            </p>
            <a
              href={API_DOCS_PATH}
              target="_blank"
              rel="noopener noreferrer"
              className={buttonVariants({ variant: "outline" })}
            >
              Open API docs <ExternalLinkIcon aria-hidden="true" />
              <span className="sr-only">(opens in a new tab)</span>
            </a>
          </CardContent>
        </Card>
        <PlatformStatusCard />
      </div>
    </div>
  );
}
