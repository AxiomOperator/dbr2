// SPDX-License-Identifier: Apache-2.0
"use client";

// Minimal toast notifications: a polite live region in the bottom-right
// corner. Kept in-house (no extra dependency) because the console only needs
// short confirmations such as "Discovery started (workflow …)".

import { CircleAlertIcon, CircleCheckIcon, TriangleAlertIcon, XIcon } from "lucide-react";
import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface ToastInput {
  title: string;
  description?: ReactNode;
  variant?: "default" | "destructive" | "warning";
  /** Milliseconds before auto-dismiss (default 8 s). */
  duration?: number;
}

interface ToastItem extends ToastInput {
  id: number;
}

const ToastContext = createContext<((t: ToastInput) => void) | null>(null);

/** Returns `toast(input)`. Outside a provider it is a no-op. */
export function useToast(): (t: ToastInput) => void {
  const ctx = useContext(ToastContext);
  return ctx ?? noop;
}

function noop() {}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const nextId = useRef(1);

  const dismiss = useCallback((id: number) => {
    setItems((cur) => cur.filter((t) => t.id !== id));
  }, []);

  const toast = useCallback(
    (input: ToastInput) => {
      const id = nextId.current++;
      setItems((cur) => [...cur.slice(-3), { ...input, id }]);
      window.setTimeout(() => dismiss(id), input.duration ?? 8000);
    },
    [dismiss],
  );

  const value = useMemo(() => toast, [toast]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        role="status"
        aria-live="polite"
        className="pointer-events-none fixed right-4 bottom-4 z-[60] flex w-full max-w-sm flex-col gap-2"
      >
        {items.map((t) => (
          <div
            key={t.id}
            className={cn(
              "pointer-events-auto flex items-start gap-3 rounded-lg border bg-popover p-3 text-sm text-popover-foreground shadow-lg",
              t.variant === "destructive" && "border-destructive/50",
              t.variant === "warning" && "border-amber-500/60",
            )}
          >
            {t.variant === "destructive" ? (
              <CircleAlertIcon className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden="true" />
            ) : t.variant === "warning" ? (
              <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden="true" />
            ) : (
              <CircleCheckIcon
                className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400"
                aria-hidden="true"
              />
            )}
            <div className="min-w-0 flex-1 space-y-1">
              <p className="font-medium">{t.title}</p>
              {t.description && <div className="break-words text-muted-foreground">{t.description}</div>}
            </div>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="Dismiss notification"
              onClick={() => dismiss(t.id)}
            >
              <XIcon aria-hidden="true" />
            </Button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
