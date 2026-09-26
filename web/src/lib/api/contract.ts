// SPDX-License-Identifier: Apache-2.0
//
// Bridges the generated OpenAPI Zod schemas (./generated/zod.gen.ts, from
// api/openapi.yaml) and the console. Responses are validated with the
// generated schemas; `contract()` only adds one normalisation on top:
//
//   Go encodes a nil slice as `null`, so the contract declares most arrays as
//   `type: [array, "null"]`. Every array-valued object property (nullable or
//   optional) is normalised to `[]`, so components can always `.map()`.
//
// Unknown object keys are stripped, never rejected (additive API changes,
// ADR-0015 MINOR bumps, keep working). Everything else — required fields,
// enums, date-times, integer ranges — is exactly what the contract says.

import { z } from "zod";

// The console runs under a strict CSP without 'unsafe-eval' (src/proxy.ts).
// Zod's JIT probes `new Function`, which the browser reports as a CSP
// violation even though Zod catches the error: parse without it.
z.config({ jitless: true });

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type Simplify<T> = { [K in keyof T]: T[K] } & {};

type ArrayKeys<T> = {
  [K in keyof T]-?: NonNullable<T[K]> extends readonly unknown[] ? K : never;
}[keyof T];

type NormalizedObject<T> = Simplify<
  { [K in keyof T as K extends ArrayKeys<T> ? K : never]-?: Normalized<NonNullable<T[K]>> } & {
    [K in keyof T as K extends ArrayKeys<T> ? never : K]: Normalized<T[K]>;
  }
>;

/** `T` with every array-valued property made a required, non-null array (deeply). */
export type Normalized<T> = T extends readonly (infer U)[]
  ? Normalized<U>[]
  : T extends object
    ? NormalizedObject<T>
    : T;

// ---------------------------------------------------------------------------
// Runtime
// ---------------------------------------------------------------------------

interface Def {
  type: string;
  innerType?: z.ZodType;
  element?: z.ZodType;
  options?: z.ZodType[];
  shape?: Record<string, z.ZodType>;
}

const defOf = (s: z.ZodType): Def => (s as unknown as { _zod: { def: Def } })._zod.def;

/** The element schema when `s` is an array, possibly wrapped in optional / nullable. */
function listElement(s: z.ZodType): z.ZodType | null {
  let cur = s;
  for (;;) {
    const d = defOf(cur);
    if (d.type === "array" && d.element) return d.element;
    if ((d.type === "optional" || d.type === "nullable") && d.innerType) {
      cur = d.innerType;
      continue;
    }
    return null;
  }
}

const cache = new WeakMap<z.ZodType, z.ZodType>();

function normalize(s: z.ZodType): z.ZodType {
  const hit = cache.get(s);
  if (hit) return hit;
  const d = defOf(s);
  let out: z.ZodType = s;
  switch (d.type) {
    case "object": {
      const shape: Record<string, z.ZodType> = {};
      for (const [key, field] of Object.entries(d.shape ?? {})) {
        const element = listElement(field);
        shape[key] = element
          ? z
              .array(normalize(element))
              .nullish()
              .transform((v) => v ?? [])
          : normalize(field);
      }
      out = z.object(shape);
      break;
    }
    case "array":
      out = z.array(normalize(d.element!));
      break;
    case "nullable":
      out = normalize(d.innerType!).nullable();
      break;
    case "optional":
      out = normalize(d.innerType!).optional();
      break;
    case "union":
      out = z.union(d.options!.map(normalize) as [z.ZodType, z.ZodType, ...z.ZodType[]]);
      break;
    default:
      break;
  }
  cache.set(s, out);
  return out;
}

/**
 * A response schema for the console: the generated contract schema with
 * nullable / optional arrays normalised to `[]`.
 */
export function contract<S extends z.ZodType>(schema: S): z.ZodType<Normalized<z.output<S>>> {
  return normalize(schema) as unknown as z.ZodType<Normalized<z.output<S>>>;
}

