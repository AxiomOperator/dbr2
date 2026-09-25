// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { DEFAULT_RETURN_TO, loginPathFor, sanitizeReturnTo } from "./return-to";

describe("sanitizeReturnTo", () => {
  it.each([
    ["/", "/"],
    ["/audit", "/audit"],
    ["/settings/security", "/settings/security"],
    ["/audit?limit=50&cursor=abc", "/audit?limit=50&cursor=abc"],
    ["/audit#top", "/audit#top"],
    ["/a/../audit", "/audit"],
  ])("accepts same-origin path %s", (input, expected) => {
    expect(sanitizeReturnTo(input)).toBe(expected);
  });

  it.each([
    [null],
    [undefined],
    [""],
    ["audit"],
    ["https://evil.example/"],
    ["http://evil.example"],
    ["//evil.example"],
    ["//evil.example/path"],
    ["///evil.example"],
    ["/\\evil.example"],
    ["\\\\evil.example"],
    ["/\t/evil.example"],
    ["/\n/evil.example"],
    ["javascript:alert(1)"],
    ["data:text/html,hi"],
    [" /audit"],
    ["/login"],
    ["/login?return_to=/audit"],
    ["/api/v1/auth/logout"],
    ["/api"],
    ["/" + "a".repeat(3000)],
  ])("rejects %j", (input) => {
    expect(sanitizeReturnTo(input as string | null | undefined)).toBe(DEFAULT_RETURN_TO);
  });
});

describe("loginPathFor", () => {
  it("encodes the current path as return_to", () => {
    expect(loginPathFor("/audit?x=1")).toBe("/login?return_to=%2Faudit%3Fx%3D1");
  });

  it("omits return_to for the dashboard and unsafe values", () => {
    expect(loginPathFor("/")).toBe("/login");
    expect(loginPathFor("//evil.example")).toBe("/login");
  });
});
