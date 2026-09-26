// SPDX-License-Identifier: Apache-2.0
"use client";

import { CircleCheckIcon, CircleHelpIcon, CircleOffIcon, CircleXIcon, PauseCircleIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { ContractState } from "@/lib/api/policy-schemas";

export const CONTRACT_STATE_LABEL: Record<ContractState | "none", string> = {
  satisfied: "Satisfied",
  violated: "Violated",
  unknown: "Unknown",
  none: "No contract",
};

/** Recovery Contract state (icon + text, never color alone). */
export function ContractStateBadge({ state }: { state: ContractState | "none" }) {
  switch (state) {
    case "satisfied":
      return (
        <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
          <CircleCheckIcon aria-hidden="true" /> {CONTRACT_STATE_LABEL.satisfied}
        </Badge>
      );
    case "violated":
      return (
        <Badge variant="destructive">
          <CircleXIcon aria-hidden="true" /> {CONTRACT_STATE_LABEL.violated}
        </Badge>
      );
    case "unknown":
      return (
        <Badge variant="outline">
          <CircleHelpIcon aria-hidden="true" /> {CONTRACT_STATE_LABEL.unknown}
        </Badge>
      );
    case "none":
      return (
        <Badge variant="outline" className="text-muted-foreground">
          <CircleOffIcon aria-hidden="true" /> {CONTRACT_STATE_LABEL.none}
        </Badge>
      );
  }
}

/** Enabled / Disabled policy. */
export function PolicyEnabledBadge({ enabled }: { enabled: boolean }) {
  return enabled ? (
    <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
      <CircleCheckIcon aria-hidden="true" /> Enabled
    </Badge>
  ) : (
    <Badge variant="outline" className="text-muted-foreground">
      <PauseCircleIcon aria-hidden="true" /> Disabled
    </Badge>
  );
}
