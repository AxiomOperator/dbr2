// SPDX-License-Identifier: Apache-2.0
import { AppShell } from "@/components/app-shell";
import { AuthGuard } from "@/components/auth-guard";
import { LiveEventsProvider } from "@/components/live/live-events";

/**
 * Every page in this group requires a session (client-side guard on
 * /auth/me) and shares one live-updates stream (Server-Sent Events).
 */
export default function ConsoleLayout({ children }: LayoutProps<"/">) {
  return (
    <AuthGuard>
      <LiveEventsProvider>
        <AppShell>{children}</AppShell>
      </LiveEventsProvider>
    </AuthGuard>
  );
}
