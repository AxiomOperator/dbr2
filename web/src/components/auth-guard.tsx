// SPDX-License-Identifier: Apache-2.0
"use client";

import { useRouter } from "next/navigation";
import { createContext, useContext, useEffect, type ReactNode } from "react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { errorMessage, isApiError } from "@/lib/api/client";
import { useMe } from "@/lib/api/hooks";
import type { Me } from "@/lib/api/schemas";
import { loginPathFor } from "@/lib/return-to";

const MeContext = createContext<Me | null>(null);

/** The signed-in user. Only valid below {@link AuthGuard}. */
export function useCurrentUser(): Me {
  const me = useContext(MeContext);
  if (!me) throw new Error("useCurrentUser must be used inside <AuthGuard>");
  return me;
}

/** Provides a known user without the /auth/me round trip (tests, previews). */
export function CurrentUserProvider({ me, children }: { me: Me; children: ReactNode }) {
  return <MeContext.Provider value={me}>{children}</MeContext.Provider>;
}

export function hasPermission(me: Me, permission: string): boolean {
  return me.permissions.includes(permission);
}

/**
 * Client-side guard: resolves `/api/v1/auth/me`; on 401 redirects to
 * `/login?return_to=<current path>`. The backend enforces authorization on
 * every API call regardless — this only decides what to render.
 */
export function AuthGuard({ children }: { children: ReactNode }) {
  const me = useMe();
  const router = useRouter();
  const unauthenticated = isApiError(me.error) && me.error.status === 401;

  useEffect(() => {
    if (unauthenticated) {
      const here = `${window.location.pathname}${window.location.search}`;
      router.replace(loginPathFor(here));
    }
  }, [unauthenticated, router]);

  if (me.isSuccess) {
    return <MeContext.Provider value={me.data}>{children}</MeContext.Provider>;
  }

  if (me.isError && !unauthenticated) {
    return (
      <main className="mx-auto w-full max-w-lg p-6">
        <Alert variant="destructive">
          <AlertTitle>Could not load your session</AlertTitle>
          <AlertDescription>
            <p>{errorMessage(me.error)}</p>
            <Button className="mt-3" variant="outline" onClick={() => void me.refetch()}>
              Retry
            </Button>
          </AlertDescription>
        </Alert>
      </main>
    );
  }

  return (
    <div className="mx-auto w-full max-w-6xl space-y-4 p-6" aria-busy="true" aria-live="polite">
      <span className="sr-only">Loading…</span>
      <Skeleton className="h-8 w-64" />
      <Skeleton className="h-32 w-full" />
    </div>
  );
}
