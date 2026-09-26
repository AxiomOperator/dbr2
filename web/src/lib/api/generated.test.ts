// SPDX-License-Identifier: Apache-2.0
// @vitest-environment node
//
// Fails while src/lib/api/generated/ is stale relative to ../api/openapi.yaml:
// the generator runs into a temporary directory with the committed
// configuration and every file must match byte for byte. Fix with
// `npm run generate:api` (or `make web-generate`) and commit the result.

import { mkdtemp, readdir, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createClient } from "@hey-api/openapi-ts";
import { afterAll, describe, expect, it } from "vitest";
import config, { GENERATED_HEADER } from "../../../openapi-ts.config";

const WEB = resolve(__dirname, "../../..");
const COMMITTED = join(WEB, "src/lib/api/generated");

let tmp: string | undefined;

afterAll(async () => {
  if (tmp) await rm(tmp, { recursive: true, force: true });
});

describe("generated OpenAPI client code", () => {
  it(
    "is up to date with api/openapi.yaml",
    async () => {
      tmp = await mkdtemp(join(tmpdir(), "dbr2-openapi-"));
      const base = Array.isArray(config) ? config[0] : config;
      await createClient({
        ...base,
        input: join(WEB, "../api/openapi.yaml"),
        output: { ...(base.output as object), path: tmp },
        logs: { level: "silent" },
      } as Parameters<typeof createClient>[0]);

      const fresh = (await readdir(tmp)).sort();
      const committed = (await readdir(COMMITTED)).sort();
      expect(committed, "file list (run `npm run generate:api`)").toEqual(fresh);
      for (const name of fresh) {
        const [want, got] = await Promise.all([readFile(join(tmp, name), "utf8"), readFile(join(COMMITTED, name), "utf8")]);
        expect(got, `${name} is stale: run \`npm run generate:api\` and commit the result`).toBe(want);
      }
    },
    60_000,
  );

  it("carries the generated-code marker the SPDX check skips", async () => {
    for (const name of await readdir(COMMITTED)) {
      const head = (await readFile(join(COMMITTED, name), "utf8")).split("\n").slice(0, 5);
      expect(head).toContain(GENERATED_HEADER[0]);
      expect(head.some((l) => /^\/\/ Code generated .* DO NOT EDIT\.$/.test(l))).toBe(true);
    }
  });
});
