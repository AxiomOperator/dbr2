// SPDX-License-Identifier: Apache-2.0
"use client";

import { useMutation } from "@tanstack/react-query";
import {
  AppWindowIcon,
  ArchiveRestoreIcon,
  BellIcon,
  ChartPieIcon,
  ChevronDownIcon,
  ContainerIcon,
  CpuIcon,
  DatabaseIcon,
  FileCheckIcon,
  FlaskConicalIcon,
  HardDriveIcon,
  InfoIcon,
  LayoutDashboardIcon,
  ListChecksIcon,
  LogOutIcon,
  MenuIcon,
  ScrollTextIcon,
  ServerCogIcon,
  ServerIcon,
  SettingsIcon,
  ShieldIcon,
  ShieldCheckIcon,
  UserIcon,
  UsersIcon,
  XIcon,
  type LucideIcon,
} from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useId, useState, type ReactNode } from "react";
import { hasPermission, useCurrentUser } from "@/components/auth-guard";
import { LiveIndicator } from "@/components/live/live-events";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
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
import { useAlerts } from "@/lib/api/hooks";
import {
  PERMISSION_BACKUP_READ,
  PERMISSION_POLICY_READ,
  PERMISSION_REPOSITORY_MANAGE,
  PERMISSION_REPOSITORY_READ,
} from "@/lib/api/protection-schemas";
import { PERMISSION_RESTORE_READ } from "@/lib/api/restore-schemas";
import { PERMISSION_AUDIT_READ, type Me } from "@/lib/api/schemas";
import { PERMISSION_USER_READ } from "@/lib/api/users-schemas";
import { cn } from "@/lib/utils";
import { PRODUCT_NAME } from "@/lib/version";

export interface NavItem {
  href: string;
  label: string;
  icon: LucideIcon;
  /** Path prefixes that mark the item active ("/" matches only the dashboard). */
  match: string[];
  /** Permission required to see the item (none = every signed-in user). */
  permission?: string;
  /** Renders a count badge (Notifications). */
  badge?: "alerts";
}

export interface NavGroup {
  id: string;
  label: string | null;
  items: NavItem[];
}

/** The console navigation (Phase 6 information architecture). */
export const NAV_GROUPS: NavGroup[] = [
  {
    id: "overview",
    label: null,
    items: [{ href: "/", label: "Dashboard", icon: LayoutDashboardIcon, match: ["/"] }],
  },
  {
    id: "docker",
    label: "Docker",
    items: [
      { href: "/hosts", label: "Hosts", icon: ServerIcon, match: ["/hosts"], permission: PERMISSION_HOST_READ },
      {
        href: "/applications",
        label: "Applications",
        icon: AppWindowIcon,
        match: ["/applications"],
        permission: PERMISSION_APPLICATION_READ,
      },
      { href: "/containers", label: "Containers", icon: ContainerIcon, match: ["/containers"], permission: PERMISSION_HOST_READ },
      { href: "/volumes", label: "Volumes", icon: HardDriveIcon, match: ["/volumes"], permission: PERMISSION_HOST_READ },
    ],
  },
  {
    id: "protection",
    label: "Protection",
    items: [
      { href: "/policies", label: "Policies", icon: ShieldCheckIcon, match: ["/policies"], permission: PERMISSION_POLICY_READ },
      { href: "/contracts", label: "Contracts", icon: FileCheckIcon, match: ["/contracts"], permission: PERMISSION_POLICY_READ },
      { href: "/jobs", label: "Jobs", icon: ListChecksIcon, match: ["/jobs"], permission: PERMISSION_BACKUP_READ },
      {
        href: "/recovery-points",
        label: "Recovery Points",
        icon: DatabaseIcon,
        match: ["/recovery-points"],
        permission: PERMISSION_BACKUP_READ,
      },
    ],
  },
  {
    id: "recovery",
    label: "Recovery",
    items: [
      { href: "/restores", label: "Restore", icon: ArchiveRestoreIcon, match: ["/restores"], permission: PERMISSION_RESTORE_READ },
      {
        href: "/restore-testing",
        label: "Restore Testing",
        icon: FlaskConicalIcon,
        match: ["/restore-testing"],
        permission: PERMISSION_RESTORE_READ,
      },
    ],
  },
  {
    id: "storage",
    label: "Storage",
    items: [
      {
        href: "/repositories",
        label: "Repositories",
        icon: HardDriveIcon,
        match: ["/repositories"],
        permission: PERMISSION_REPOSITORY_READ,
      },
      { href: "/usage", label: "Usage", icon: ChartPieIcon, match: ["/usage"], permission: PERMISSION_REPOSITORY_READ },
    ],
  },
  {
    id: "system",
    label: "System",
    items: [
      { href: "/agents", label: "Agents", icon: CpuIcon, match: ["/agents"], permission: PERMISSION_HOST_READ },
      { href: "/users", label: "Users", icon: UsersIcon, match: ["/users"], permission: PERMISSION_USER_READ },
      {
        href: "/notifications",
        label: "Notifications",
        icon: BellIcon,
        match: ["/notifications"],
        permission: PERMISSION_BACKUP_READ,
        badge: "alerts",
      },
      {
        href: "/platform",
        label: "Platform protection",
        icon: ServerCogIcon,
        match: ["/platform"],
        permission: PERMISSION_REPOSITORY_MANAGE,
      },
      { href: "/audit", label: "Audit Log", icon: ScrollTextIcon, match: ["/audit"], permission: PERMISSION_AUDIT_READ },
      { href: "/settings/security", label: "Settings", icon: SettingsIcon, match: ["/settings"] },
    ],
  },
];

/** The groups and items `me` may see (groups without visible items are dropped). */
export function visibleNav(me: Me): NavGroup[] {
  return NAV_GROUPS.map((g) => ({
    ...g,
    items: g.items.filter((i) => !i.permission || hasPermission(me, i.permission)),
  })).filter((g) => g.items.length > 0);
}

export function isActive(item: NavItem, pathname: string): boolean {
  return item.match.some((m) => (m === "/" ? pathname === "/" : pathname === m || pathname.startsWith(`${m}/`)));
}

const COLLAPSED_KEY = "dbr2.nav.collapsed"; // gitleaks:allow (localStorage key name)

function readCollapsed(): string[] {
  try {
    const v = JSON.parse(window.localStorage.getItem(COLLAPSED_KEY) ?? "[]");
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
  } catch {
    return [];
  }
}

function AlertsBadge() {
  const me = useCurrentUser();
  const alerts = useAlerts(false, { enabled: hasPermission(me, PERMISSION_BACKUP_READ) });
  const open = alerts.data?.length ?? 0;
  if (open === 0) return null;
  const critical = alerts.data?.some((a) => a.severity === "critical");
  return (
    <span
      className={cn(
        "ml-auto rounded-full px-1.5 py-0.5 text-[11px] leading-none font-medium tabular-nums",
        critical ? "bg-destructive text-white" : "bg-amber-500 text-black",
      )}
      data-testid="alerts-badge"
    >
      {open}
      <span className="sr-only"> open {open === 1 ? "alert" : "alerts"}</span>
    </span>
  );
}

function NavLink({ item, pathname, onNavigate }: { item: NavItem; pathname: string; onNavigate?: () => void }) {
  const active = isActive(item, pathname);
  const Icon = item.icon;
  return (
    <Link
      href={item.href}
      aria-current={active ? "page" : undefined}
      onClick={onNavigate}
      className={cn(
        "flex items-center gap-2 rounded-md px-2.5 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
        active && "bg-muted font-medium text-foreground",
      )}
    >
      <Icon aria-hidden="true" className="size-4 shrink-0" />
      <span className="truncate">{item.label}</span>
      {item.badge === "alerts" && <AlertsBadge />}
    </Link>
  );
}

/** Grouped, collapsible, permission-gated navigation. */
export function SidebarNav({ onNavigate }: { onNavigate?: () => void }) {
  const me = useCurrentUser();
  const pathname = usePathname();
  const baseId = useId();
  // Rendered only below the AuthGuard (never on the server): read storage once.
  const [collapsed, setCollapsed] = useState<string[]>(() => (typeof window === "undefined" ? [] : readCollapsed()));
  const toggle = (id: string, open: boolean) => {
    setCollapsed((cur) => {
      const next = open ? cur.filter((x) => x !== id) : [...new Set([...cur, id])];
      try {
        window.localStorage.setItem(COLLAPSED_KEY, JSON.stringify(next));
      } catch {
        // Storage unavailable: the choice lasts for this page only.
      }
      return next;
    });
  };

  return (
    <nav aria-label="Main" className="space-y-3 text-sm">
      {visibleNav(me).map((group) => {
        if (!group.label) {
          return (
            <ul key={group.id} className="space-y-0.5">
              {group.items.map((item) => (
                <li key={item.href}>
                  <NavLink item={item} pathname={pathname} onNavigate={onNavigate} />
                </li>
              ))}
            </ul>
          );
        }
        const hasActive = group.items.some((i) => isActive(i, pathname));
        const open = hasActive || !collapsed.includes(group.id);
        return (
          <Collapsible key={group.id} open={open} onOpenChange={(o) => toggle(group.id, o)}>
            <CollapsibleTrigger asChild>
              <button
                type="button"
                id={`${baseId}-${group.id}`}
                className="flex w-full items-center justify-between rounded-md px-2.5 py-1 text-xs font-semibold tracking-wide text-muted-foreground uppercase hover:text-foreground"
              >
                {group.label}
                <ChevronDownIcon
                  aria-hidden="true"
                  className={cn("size-3.5 transition-transform motion-reduce:transition-none", !open && "-rotate-90")}
                />
              </button>
            </CollapsibleTrigger>
            <CollapsibleContent>
              <ul className="mt-0.5 space-y-0.5" aria-labelledby={`${baseId}-${group.id}`}>
                {group.items.map((item) => (
                  <li key={item.href}>
                    <NavLink item={item} pathname={pathname} onNavigate={onNavigate} />
                  </li>
                ))}
              </ul>
            </CollapsibleContent>
          </Collapsible>
        );
      })}
    </nav>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  const me = useCurrentUser();
  const [mobileOpen, setMobileOpen] = useState(false);
  const mobileId = useId();

  const logout = useMutation({
    mutationFn: api.logout,
    meta: { skipSessionCheck: true },
    // A full page load deliberately discards every cached query and all
    // in-memory state, whatever the outcome of the logout call.
    // eslint-disable-next-line @next/next/no-location-assign-relative-destination
    onSettled: () => window.location.assign("/login"),
  });

  return (
    <>
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-2 focus:top-2 focus:z-50 focus:rounded focus:bg-background focus:px-3 focus:py-2"
      >
        Skip to content
      </a>
      <header className="sticky top-0 z-40 border-b bg-background">
        <div className="mx-auto flex h-14 max-w-7xl items-center gap-3 px-4">
          <Button
            variant="ghost"
            size="icon"
            className="md:hidden"
            aria-label={mobileOpen ? "Close navigation" : "Open navigation"}
            aria-expanded={mobileOpen}
            aria-controls={mobileId}
            onClick={() => setMobileOpen((o) => !o)}
          >
            {mobileOpen ? <XIcon aria-hidden="true" /> : <MenuIcon aria-hidden="true" />}
          </Button>
          <Link href="/" className="text-lg font-semibold tracking-tight">
            {PRODUCT_NAME}
          </Link>
          <div className="ml-auto flex items-center gap-1">
            <LiveIndicator />
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
        {mobileOpen && (
          <div id={mobileId} className="max-h-[calc(100vh-3.5rem)] overflow-y-auto border-t px-4 py-3 md:hidden">
            <SidebarNav onNavigate={() => setMobileOpen(false)} />
          </div>
        )}
      </header>
      <div className="mx-auto flex w-full max-w-7xl flex-1">
        <aside className="hidden w-56 shrink-0 border-r md:block">
          <div className="sticky top-14 max-h-[calc(100vh-3.5rem)] overflow-y-auto px-3 py-6">
            <SidebarNav />
          </div>
        </aside>
        <main id="main" className="min-w-0 flex-1 px-4 py-8 md:px-8">
          {children}
        </main>
      </div>
    </>
  );
}
