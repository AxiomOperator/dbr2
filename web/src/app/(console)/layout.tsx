// SPDX-License-Identifier: Apache-2.0
import { AppShell } from "@/components/app-shell";
import { AuthGuard } from "@/components/auth-guard";

/** Every page in this group requires a session (client-side guard on /auth/me). */
export default function ConsoleLayout({ children }: LayoutProps<"/">) {
  return (
    <AuthGuard>
      <AppShell>{children}</AppShell>
    </AuthGuard>
  );
}
