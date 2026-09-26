// SPDX-License-Identifier: Apache-2.0
"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useId } from "react";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export const ALL = "all";

/**
 * Filter values kept in the URL query (`?host=…&state=…`). Values equal to
 * their default are left out; unknown values fall back to the default when
 * `allowed` lists the valid ones.
 */
export function useUrlFilters<K extends string>(
  defaults: Record<K, string>,
  allowed: Partial<Record<K, readonly string[]>> = {},
): [Record<K, string>, (next: Partial<Record<K, string>>) => void] {
  const params = useSearchParams();
  const router = useRouter();
  const pathname = usePathname();
  const values = {} as Record<K, string>;
  for (const key of Object.keys(defaults) as K[]) {
    const raw = params.get(key);
    const ok = raw !== null && raw !== "" && (!allowed[key] || allowed[key]!.includes(raw));
    values[key] = ok ? raw : defaults[key];
  }
  const update = (next: Partial<Record<K, string>>) => {
    const merged = { ...values, ...next };
    const qs = new URLSearchParams();
    for (const key of Object.keys(defaults) as K[]) {
      if (merged[key] !== defaults[key]) qs.set(key, merged[key]);
    }
    const s = qs.toString();
    router.replace(s ? `${pathname}?${s}` : pathname, { scroll: false });
  };
  return [values, update];
}

export interface FilterOption {
  value: string;
  label: string;
}

/** A labelled select for a list filter. */
export function FilterSelect({
  label,
  value,
  onChange,
  options,
  allLabel,
  className = "min-w-40",
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: FilterOption[];
  /** Label of the "no filter" option (value {@link ALL}); omit to offer none. */
  allLabel?: string;
  className?: string;
}) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger id={id} className={className}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {allLabel && <SelectItem value={ALL}>{allLabel}</SelectItem>}
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

/** Distinct `[id, name]` pairs, sorted by name (host / application filter options). */
export function distinctOptions<T>(items: T[], id: (t: T) => string | null, name: (t: T) => string | null): FilterOption[] {
  const m = new Map<string, string>();
  for (const t of items) {
    const k = id(t);
    if (k) m.set(k, name(t) ?? k);
  }
  return [...m.entries()].map(([value, label]) => ({ value, label })).sort((a, b) => a.label.localeCompare(b.label));
}
