// SPDX-License-Identifier: Apache-2.0
import type { Metadata } from "next";
import { headers } from "next/headers";
import { SiteFooter } from "@/components/site-footer";
import { NONCE_HEADER } from "@/lib/csp";
import { Providers } from "./providers";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "DBR² console",
    template: "%s · DBR²",
  },
  description: "DBR² — Docker Backup, Recovery & Restore",
  robots: { index: false, follow: false },
};

export default async function RootLayout({ children }: LayoutProps<"/">) {
  // The CSP nonce from src/proxy.ts. Reading headers() also makes every page
  // render per request, which nonce-based CSP requires.
  const nonce = (await headers()).get(NONCE_HEADER) ?? undefined;
  return (
    // next-themes sets the `class` attribute before hydration.
    <html lang="en" className="h-full antialiased" suppressHydrationWarning>
      <body className="flex min-h-full flex-col">
        <Providers nonce={nonce}>
          <div className="flex flex-1 flex-col">{children}</div>
          <SiteFooter />
        </Providers>
      </body>
    </html>
  );
}
