// SPDX-License-Identifier: Apache-2.0
"use client";

import { MonitorIcon, MoonIcon, SunIcon } from "lucide-react";
import { useTheme } from "next-themes";
import { useSyncExternalStore } from "react";
import { Button } from "@/components/ui/button";

const ORDER = ["system", "light", "dark"] as const;
const LABEL: Record<(typeof ORDER)[number], string> = {
  system: "System theme",
  light: "Light theme",
  dark: "Dark theme",
};

const subscribe = () => () => {};

/** Cycles system → light → dark. Renders a stable placeholder until hydrated. */
export function ThemeToggle() {
  const { theme, setTheme } = useTheme();
  const mounted = useSyncExternalStore(
    subscribe,
    () => true,
    () => false,
  );
  const current = (mounted && (ORDER as readonly string[]).includes(theme ?? "")
    ? theme
    : "system") as (typeof ORDER)[number];
  const next = ORDER[(ORDER.indexOf(current) + 1) % ORDER.length];
  const Icon = current === "light" ? SunIcon : current === "dark" ? MoonIcon : MonitorIcon;

  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={() => setTheme(next)}
      aria-label={`${LABEL[current]} (switch to ${LABEL[next].toLowerCase()})`}
      title={LABEL[current]}
    >
      <Icon aria-hidden="true" />
    </Button>
  );
}
