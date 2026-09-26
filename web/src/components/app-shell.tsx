// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation } from "@tanstack/react-query";
import { InfoIcon, LogOutIcon, ShieldIcon, UserIcon } from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import type { ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { api } from "@/lib/api/endpoints";
import { PERMISSION_APPLICATION_READ, PERMISSION_HOST_READ } from "@/lib/api/fleet-schemas";
import { PERMISSION_BACKUP_READ, PERMISSION_REPOSITORY_READ } from "@/lib/api/protection-schemas";
import { PERMISSION_RESTORE_READ } from "@/lib/api/restore-schemas";
import { PERMISSION_AUDIT_READ } from "@/lib/api/schemas";
import { cn } from "@/lib/utils";
import { PRODUCT_NAME } from "@/lib/version";

interface NavItem {
  href: string;
  label: string;
  /** Path prefix that marks the item active. */
  match: string;
  visible: boolean;
}

export function AppShell({ children }: { children: ReactNode }) {
  const me = useCurrentUser();
  const pathname = usePathname();

  const logout = useMutation({
    mutationFn: api.logout,
    meta: { skipSessionCheck: true },
    // A full page load deliberately discards every cached query and all
    // in-memory state, whatever the outcome of the logout call.
    // eslint-disable-next-line @next/next/no-location-assign-relative-destination
    onSettled: () => window.location.assign("/login"),
  });

  const items: NavItem[] = [
    { href: "/", label: "Dashboard", match: "/", visible: true },
    {
      href: "/hosts",
      label: "Hosts",
      match: "/hosts",
      visible: hasPermission(me, PERMISSION_HOST_READ),
    },
    {
      href: "/applications",
      label: "Applications",
      match: "/applications",
      visible: hasPermission(me, PERMISSION_APPLICATION_READ),
    },
    {
      href: "/repositories",
      label: "Repositories",
      match: "/repositories",
      visible: hasPermission(me, PERMISSION_REPOSITORY_READ),
    },
    {
      href: "/recovery-points",
      label: "Recovery points",
      match: "/recovery-points",
      visible: hasPermission(me, PERMISSION_BACKUP_READ),
    },
    {
      href: "/restores",
      label: "Restores",
      match: "/restores",
      visible: hasPermission(me, PERMISSION_RESTORE_READ),
    },
    {
      href: "/alerts",
      label: "Alerts",
      match: "/alerts",
      visible: hasPermission(me, PERMISSION_BACKUP_READ),
    },
    {
      href: "/audit",
      label: "Audit",
      match: "/audit",
      visible: hasPermission(me, PERMISSION_AUDIT_READ),
    },
    { href: "/settings/security", label: "Settings", match: "/settings", visible: true },
  ];

  const isActive = (item: NavItem) =>
    item.match === "/" ? pathname === "/" : pathname.startsWith(item.match);

  return (
    <>
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-2 focus:top-2 focus:z-50 focus:rounded focus:bg-background focus:px-3 focus:py-2"
      >
        Skip to content
      </a>
      <header className="border-b bg-background">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4">
          <Link href="/" className="text-lg font-semibold tracking-tight">
            {PRODUCT_NAME}
          </Link>
          <nav aria-label="Main" className="-mx-1 flex min-w-0 items-center gap-1 overflow-x-auto px-1">
            {items
              .filter((i) => i.visible)
              .map((item) => (
                <Link
                  key={item.href}
                  href={item.href}
                  aria-current={isActive(item) ? "page" : undefined}
                  className={cn(
                    "rounded-md px-3 py-1.5 text-sm whitespace-nowrap text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
                    isActive(item) && "bg-muted font-medium text-foreground",
                  )}
                >
                  {item.label}
                </Link>
              ))}
          </nav>
          <div className="ml-auto flex items-center gap-1">
            <ThemeToggle />
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" className="gap-2" aria-label={`User menu for ${me.display_name}`}>
                  <UserIcon aria-hidden="true" />
                  <span className="hidden max-w-40 truncate sm:inline">{me.display_name}</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-60">
                <DropdownMenuLabel className="font-normal">
                  <div className="truncate font-medium text-foreground">{me.display_name}</div>
                  <div className="truncate text-xs text-muted-foreground">
                    {me.username} · {me.kind === "master_admin" ? "master admin" : "single sign-on"}
                  </div>
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuGroup>
                  <DropdownMenuItem asChild>
                    <Link href="/settings/security">
                      <ShieldIcon aria-hidden="true" /> Security settings
                    </Link>
                  </DropdownMenuItem>
                  <DropdownMenuItem asChild>
                    <Link href="/about">
                      <InfoIcon aria-hidden="true" /> About
                    </Link>
                  </DropdownMenuItem>
                </DropdownMenuGroup>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  disabled={logout.isPending}
                  onSelect={(e) => {
                    e.preventDefault();
                    logout.mutate();
                  }}
                >
                  <LogOutIcon aria-hidden="true" /> {logout.isPending ? "Signing out…" : "Sign out"}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>
      </header>
      <main id="main" className="mx-auto w-full max-w-6xl flex-1 px-4 py-8">
        {children}
      </main>
    </>
  );
}
