// SPDX-License-Identifier: Apache-2.0
"use client";

import Link from "next/link";
import { useVersion } from "@/lib/api/hooks";
import { API_DOCS_PATH, consoleVersion, PRODUCT_NAME } from "@/lib/version";

/** Footer shown on every page: console + platform version (ADR-0015). */
export function SiteFooter() {
  const version = useVersion();
  const platform = version.isPending
    ? "…"
    : version.isError
      ? "unavailable"
      : version.data.platform;

  return (
    <footer className="border-t bg-background">
      <div className="mx-auto flex max-w-6xl flex-wrap items-center justify-between gap-2 px-4 py-3 text-xs text-muted-foreground">
        <p data-testid="version-footer">
          {PRODUCT_NAME} console <span className="font-mono">{consoleVersion()}</span> · platform{" "}
          <span className="font-mono">{platform}</span>
        </p>
        <nav aria-label="Footer" className="flex gap-4">
          <Link href="/about" className="hover:text-foreground hover:underline">
            About
          </Link>
          <a
            href={API_DOCS_PATH}
            target="_blank"
            rel="noopener noreferrer"
            className="hover:text-foreground hover:underline"
          >
            API docs
          </a>
        </nav>
      </div>
    </footer>
  );
}
