// SPDX-License-Identifier: Apache-2.0
//
// Open-redirect protection for the `?return_to=` login parameter. Only
// same-origin, relative, absolute-path references are accepted.

export const DEFAULT_RETURN_TO = "/";

/**
 * Returns `value` if it is a safe same-origin path (e.g. `/audit?x=1`),
 * otherwise {@link DEFAULT_RETURN_TO}.
 *
 * Rejected: absolute URLs (`https://evil`), scheme-relative (`//evil`),
 * backslash tricks (`/\evil`, `\\evil`), anything not starting with `/`,
 * control characters (browsers strip tab/newline, turning `/\t/evil` into
 * `//evil`), paths back to `/login` (redirect loops) and API paths.
 */
export function sanitizeReturnTo(value: string | null | undefined): string {
  if (typeof value !== "string" || value.length === 0 || value.length > 2048) {
    return DEFAULT_RETURN_TO;
  }
  if (/[\u0000-\u001f\u007f]/.test(value)) return DEFAULT_RETURN_TO;
  if (!value.startsWith("/")) return DEFAULT_RETURN_TO;
  if (value.startsWith("//") || value.includes("\\")) return DEFAULT_RETURN_TO;

  // Resolve against a dummy origin and make sure it did not escape it.
  let url: URL;
  try {
    url = new URL(value, "http://dbr2.invalid");
  } catch {
    return DEFAULT_RETURN_TO;
  }
  if (url.origin !== "http://dbr2.invalid") return DEFAULT_RETURN_TO;

  const path = url.pathname;
  if (path === "/login" || path.startsWith("/login/")) return DEFAULT_RETURN_TO;
  if (path === "/api" || path.startsWith("/api/")) return DEFAULT_RETURN_TO;

  return `${url.pathname}${url.search}${url.hash}`;
}

/** Builds `/login?return_to=<path>` for the given current location. */
export function loginPathFor(currentPath: string): string {
  const safe = sanitizeReturnTo(currentPath);
  return safe === DEFAULT_RETURN_TO ? "/login" : `/login?return_to=${encodeURIComponent(safe)}`;
}
