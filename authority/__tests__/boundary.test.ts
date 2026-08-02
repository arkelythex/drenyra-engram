/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Non-authorization boundary (contracts/provenance.md).
 *
 * Memory informs decisions; it never authorizes. This test proves:
 * - The exported `publicApi` satisfies `NonAuthorizing` (compile-time guard —
 *   the generic constraint below would fail typecheck if the surface ever grew
 *   an authorization member) and passes `assertNonAuthorizing`.
 * - No exported function name matches /^(authorize|approve|allow)/i.
 * - The runtime guard rejects surfaces that grow approve/authorize/allow methods.
 */

import { describe, expect, it } from "vitest";

import { assertNonAuthorizing, type NonAuthorizing } from "../boundary.js";
import { publicApi } from "../../index.js";

function acceptNonAuthorizing<T extends NonAuthorizing>(value: T): T {
  return value;
}

describe("non-authorization boundary", () => {
  it("passes on the exported public API (compile-time + runtime reflection)", () => {
    // Compile-time guard: `T extends NonAuthorizing` fails to compile the day
    // `publicApi` grows an authorize/approve/allow member.
    const checked = acceptNonAuthorizing(publicApi);
    expect(checked).toBe(publicApi);

    // Runtime reflection guard over the same surface.
    expect(() => assertNonAuthorizing(publicApi)).not.toThrow();
  });

  it("has no exported function whose name matches authorize/approve/allow", () => {
    const forbidden = Object.keys(publicApi).filter(
      (name) =>
        typeof publicApi[name as keyof typeof publicApi] === "function" &&
        /^(authorize|approve|allow)/i.test(name),
    );
    expect(forbidden).toEqual([]);
  });

  it("exports the documented non-authorization constant", () => {
    expect(typeof publicApi.NON_AUTHORIZING_BOUNDARY).toBe("string");
    expect(publicApi.NON_AUTHORIZING_BOUNDARY.length).toBeGreaterThan(0);
  });

  it("rejects a surface that grows an approve method", () => {
    expect(() => assertNonAuthorizing({ approve: () => true })).toThrow(
      /NON_AUTHORIZING_BOUNDARY_VIOLATION/,
    );
  });

  it("rejects surfaces that grow authorize or allow methods", () => {
    expect(() => assertNonAuthorizing({ authorize: () => true })).toThrow(
      /NON_AUTHORIZING_BOUNDARY_VIOLATION/,
    );
    expect(() => assertNonAuthorizing({ allowWrite: () => true })).toThrow(
      /NON_AUTHORIZING_BOUNDARY_VIOLATION/,
    );
  });

  it("ignores non-function members and unrelated names", () => {
    expect(() =>
      assertNonAuthorizing({
        NON_AUTHORIZING_BOUNDARY: "memory guides",
        scopeFirstSearch: publicApi.scopeFirstSearch,
      }),
    ).not.toThrow();
  });
});
