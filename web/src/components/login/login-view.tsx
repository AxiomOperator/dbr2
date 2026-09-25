// SPDX-License-Identifier: Apache-2.0
"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useRouter, useSearchParams } from "next/navigation";
import { useEffect } from "react";
import { LoginForm } from "@/components/login/login-form";
import { ThemeToggle } from "@/components/theme-toggle";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { errorMessage } from "@/lib/api/client";
import { queryKeys } from "@/lib/api/endpoints";
import { useAuthProviders, useMe } from "@/lib/api/hooks";
import type { Me, OidcProvider } from "@/lib/api/schemas";
import { sanitizeReturnTo } from "@/lib/return-to";
import { PRODUCT_NAME } from "@/lib/version";

const OIDC_ERROR_MESSAGES: Record<string, string> = {
  access_denied: "Sign-in was cancelled or denied by the identity provider.",
  invalid_state: "The sign-in attempt expired or could not be verified. Please try again.",
  state_mismatch: "The sign-in attempt expired or could not be verified. Please try again.",
  expired: "The sign-in attempt expired. Please try again.",
  no_roles:
    "Your account is not assigned to any DBR² role. Ask an administrator to map one of your groups to a role.",
  forbidden:
    "Your account is not allowed to use DBR². Ask an administrator to map one of your groups to a role.",
  provider_error: "The identity provider reported an error. Please try again.",
  provider_unavailable: "The identity provider could not be reached. Please try again later.",
  session_expired: "Your session has expired. Please sign in again.",
};

/** Message for `/login?error=<code>`; unknown or malformed codes get a generic text. */
export function oidcErrorMessage(code: string): string {
  const known = OIDC_ERROR_MESSAGES[code];
  if (known) return known;
  const safe = /^[A-Za-z0-9_.-]{1,64}$/.test(code) ? ` (${code})` : "";
  return `Single sign-on failed${safe}. Please try again or contact an administrator.`;
}

function providerLabel(p: OidcProvider): string {
  return p.id === "entra" ? "Sign in with Microsoft" : `Sign in with ${p.display_name}`;
}

/** OIDC login is a full-page navigation; only same-origin relative URLs are accepted. */
export function oidcHref(p: OidcProvider, returnTo: string): string | null {
  if (!p.login_url.startsWith("/") || p.login_url.startsWith("//") || p.login_url.includes("\\")) {
    return null;
  }
  const sep = p.login_url.includes("?") ? "&" : "?";
  return `${p.login_url}${sep}return_to=${encodeURIComponent(returnTo)}`;
}

export function LoginView() {
  const params = useSearchParams();
  const router = useRouter();
  const queryClient = useQueryClient();
  const returnTo = sanitizeReturnTo(params.get("return_to"));
  const errorCode = params.get("error");
  const providers = useAuthProviders();
  const me = useMe();

  // Already signed in (e.g. back button): go straight to the target.
  useEffect(() => {
    if (me.isSuccess) router.replace(returnTo);
  }, [me.isSuccess, returnTo, router]);

  function onSuccess(user: Me) {
    queryClient.setQueryData(queryKeys.me, user);
    router.replace(returnTo);
  }

  const oidc = providers.data?.oidc ?? [];
  const showPasswordForm = providers.isError || providers.data?.master_admin !== false;

  return (
    <main className="flex flex-1 items-center justify-center px-4 py-12">
      <div className="w-full max-w-sm space-y-4">
        <div className="flex items-center justify-between">
          <span className="text-2xl font-semibold tracking-tight">{PRODUCT_NAME}</span>
          <ThemeToggle />
        </div>
        <Card>
          <CardHeader>
            <CardTitle>
              <h1>Sign in</h1>
            </CardTitle>
            <CardDescription>Docker Backup, Recovery &amp; Restore</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {errorCode && (
              <Alert variant="destructive">
                <AlertTitle>Sign-in failed</AlertTitle>
                <AlertDescription>{oidcErrorMessage(errorCode)}</AlertDescription>
              </Alert>
            )}
            {providers.isError && (
              <Alert variant="destructive">
                <AlertTitle>API unavailable</AlertTitle>
                <AlertDescription>{errorMessage(providers.error)}</AlertDescription>
              </Alert>
            )}

            {providers.isPending ? (
              <div className="space-y-3" aria-busy="true">
                <span className="sr-only">Loading sign-in options…</span>
                <Skeleton className="h-9 w-full" />
                <Skeleton className="h-9 w-full" />
              </div>
            ) : (
              <>
                {oidc.length > 0 && (
                  <ul className="space-y-2" aria-label="Single sign-on providers">
                    {oidc.map((p) => {
                      const href = oidcHref(p, returnTo);
                      if (!href) return null;
                      return (
                        <li key={p.id}>
                          <a href={href} className={buttonVariants({ variant: "outline", className: "w-full" })}>
                            {providerLabel(p)}
                          </a>
                        </li>
                      );
                    })}
                  </ul>
                )}
                {oidc.length > 0 && showPasswordForm && (
                  <div className="flex items-center gap-3 text-xs text-muted-foreground">
                    <Separator className="flex-1" />
                    <span>or with the master admin account</span>
                    <Separator className="flex-1" />
                  </div>
                )}
                {showPasswordForm && <LoginForm onSuccess={onSuccess} />}
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </main>
  );
}
