// SPDX-License-Identifier: Apache-2.0
//
// The console's own four-part version (ADR-0015). `NEXT_PUBLIC_DBR2_VERSION`
// is inlined at build time (`$(cat VERSION).$BUILD`); local builds use BUILD 0.

export const DEFAULT_CONSOLE_VERSION = "0.1.0.0";

export function consoleVersion(): string {
  const v = process.env.NEXT_PUBLIC_DBR2_VERSION;
  return v && v.trim() !== "" ? v.trim() : DEFAULT_CONSOLE_VERSION;
}

export const PRODUCT_NAME = "DBR²";
export const API_DOCS_PATH = "/api/docs";
