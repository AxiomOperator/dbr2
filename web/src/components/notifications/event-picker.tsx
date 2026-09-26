// SPDX-License-Identifier: Apache-2.0
"use client";

// Event subscription picker: "all events" (empty list), per-group prefix
// wildcards (backup.*) that cover the group's events, individual events, and
// custom patterns (exact names or prefix wildcards the catalogue lacks).

import { PlusIcon, XIcon } from "lucide-react";
import { useId, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  coveredEvents,
  isValidEventPattern,
  KNOWN_EVENT_TYPES,
  NOTIFICATION_EVENTS,
} from "@/lib/notifications";

const wildcard = (group: string) => `${group}.*`;

/** Toggles a group's wildcard; turning it on drops the now-redundant exact events of the group. */
export function toggleWildcard(events: string[], group: string, on: boolean): string[] {
  const w = wildcard(group);
  if (!on) return events.filter((e) => e !== w);
  return [...events.filter((e) => !e.startsWith(`${group}.`)), w];
}

export function toggleEvent(events: string[], type: string, on: boolean): string[] {
  return on ? [...new Set([...events, type])] : events.filter((e) => e !== type);
}

/** Entries that are neither a catalogue event nor a catalogue group's wildcard. */
export function customPatterns(events: string[]): string[] {
  const known = new Set([...KNOWN_EVENT_TYPES, ...NOTIFICATION_EVENTS.map((g) => wildcard(g.group)), "*"]);
  return events.filter((e) => !known.has(e));
}

export function EventPicker({
  value,
  onChange,
  disabled,
}: {
  value: string[];
  onChange: (next: string[]) => void;
  disabled?: boolean;
}) {
  const id = useId();
  const [custom, setCustom] = useState("");
  const [customError, setCustomError] = useState<string | null>(null);
  const all = value.length === 0 || value.includes("*");
  const covered = coveredEvents(value);
  const extra = customPatterns(value);

  function addCustom() {
    const p = custom.trim();
    if (!p) return;
    if (!isValidEventPattern(p)) {
      setCustomError("Use an event type such as backup.failed, or a prefix wildcard such as backup.*");
      return;
    }
    setCustomError(null);
    setCustom("");
    onChange([...new Set([...value.filter((e) => e !== "*"), p])]);
  }

  return (
    <fieldset className="space-y-3" aria-describedby={`${id}-summary`}>
      <legend className="text-sm font-medium">Events</legend>
      <div className="flex items-center gap-2">
        <Checkbox
          id={`${id}-all`}
          checked={all}
          onCheckedChange={(v) => onChange(v === true ? [] : ["backup.failed"])}
          disabled={disabled}
        />
        <Label htmlFor={`${id}-all`} className="font-normal">
          All events (including types added in future versions)
        </Label>
      </div>
      {!all && (
        <div className="grid gap-3 sm:grid-cols-2">
          {NOTIFICATION_EVENTS.map((g) => {
            const wildOn = value.includes(wildcard(g.group));
            return (
              <div key={g.group} className="space-y-1.5 rounded-md border p-2.5" role="group" aria-label={`${g.group} events`}>
                <div className="flex items-center gap-2">
                  <Checkbox
                    id={`${id}-${g.group}-all`}
                    checked={wildOn}
                    onCheckedChange={(v) => onChange(toggleWildcard(value, g.group, v === true))}
                    disabled={disabled}
                  />
                  <Label htmlFor={`${id}-${g.group}-all`} className="font-mono text-xs">
                    {wildcard(g.group)}
                  </Label>
                </div>
                <ul className="space-y-1 pl-5">
                  {g.events.map((e) => (
                    <li key={e.type} className="flex items-center gap-2">
                      <Checkbox
                        id={`${id}-${e.type}`}
                        checked={wildOn || value.includes(e.type)}
                        onCheckedChange={(v) => onChange(toggleEvent(value, e.type, v === true))}
                        disabled={disabled || wildOn}
                        aria-label={`${e.type}: ${e.label}`}
                      />
                      <Label htmlFor={`${id}-${e.type}`} className="font-normal">
                        <span className="font-mono text-xs">{e.type}</span>{" "}
                        <span className="text-xs text-muted-foreground">{e.label}</span>
                      </Label>
                    </li>
                  ))}
                </ul>
              </div>
            );
          })}
        </div>
      )}
      {!all && (
        <div className="space-y-1.5">
          <Label htmlFor={`${id}-custom`}>Other event type or wildcard</Label>
          <div className="flex flex-wrap gap-2">
            <Input
              id={`${id}-custom`}
              value={custom}
              onChange={(e) => setCustom(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  addCustom();
                }
              }}
              placeholder="e.g. repository.* or backup.settings.updated"
              className="max-w-72 font-mono text-xs"
              spellCheck={false}
              aria-invalid={customError ? true : undefined}
              aria-describedby={customError ? `${id}-custom-error` : undefined}
              disabled={disabled}
            />
            <Button type="button" variant="outline" size="sm" onClick={addCustom} disabled={disabled}>
              <PlusIcon aria-hidden="true" /> Add
            </Button>
          </div>
          {customError && (
            <p id={`${id}-custom-error`} className="text-xs text-destructive">
              {customError}
            </p>
          )}
          {extra.length > 0 && (
            <ul className="flex flex-wrap gap-1.5" aria-label="Other patterns">
              {extra.map((p) => (
                <li key={p}>
                  <Badge variant="secondary" className="gap-1 font-mono">
                    {p}
                    {!disabled && (
                      <button
                        type="button"
                        onClick={() => onChange(value.filter((e) => e !== p))}
                        aria-label={`Remove ${p}`}
                        className="rounded-sm hover:text-destructive"
                      >
                        <XIcon aria-hidden="true" className="size-3" />
                      </button>
                    )}
                  </Badge>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      <p id={`${id}-summary`} className="text-xs text-muted-foreground" aria-live="polite" data-testid="events-summary">
        {all
          ? "Every event is delivered (still filtered by the minimum severity)."
          : `Delivers ${covered.length} of the ${KNOWN_EVENT_TYPES.length} listed event types${extra.length ? ` plus ${extra.length} other pattern${extra.length === 1 ? "" : "s"}` : ""}.`}
      </p>
    </fieldset>
  );
}
