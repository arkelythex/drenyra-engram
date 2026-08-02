/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * The non-authorization boundary (contracts/provenance.md).
 *
 * Memory informs decisions. It NEVER authorizes them:
 *
 *   Memoria orienta.      Memory guides.
 *   Política restringe.   Policy restricts.
 *   Evidencia demuestra.  Evidence demonstrates.
 *   Receipt certifica.    Receipt certifies.
 *   Profesional autoriza. A professional authorizes.
 *
 * Two independent enforcement layers:
 *   1. Type-level — the public API object satisfies `NonAuthorizing`, so adding
 *      an `authorize`/`approve`/`allow` member to the exported surface fails
 *      `bun run typecheck` (index.ts `satisfies` + this module's constraint).
 *   2. Runtime reflection — `assertNonAuthorizing` rejects any object whose
 *      function member names start with `authorize`/`approve`/`allow`. It runs
 *      on the public API at module load in index.ts and fails fast.
 */

/** The documented boundary constant (provenance.md). */
export const NON_AUTHORIZING_BOUNDARY = [
  "Memoria orienta.      Memory guides.",
  "Política restringe.   Policy restricts.",
  "Evidencia demuestra.  Evidence demonstrates.",
  "Receipt certifica.    Receipt certifies.",
  "Profesional autoriza. A professional authorizes.",
].join("\n");

/**
 * Type-level guard: an API surface that satisfies this type carries no
 * authorization member. The `never` members make any non-`never` property
 * named `authorize`, `approve` or `allow` a compile-time error. The index
 * signature keeps the type non-weak so ordinary API surfaces (which share no
 * property names) remain assignable.
 */
export type NonAuthorizing = {
  readonly authorize?: never;
  readonly approve?: never;
  readonly allow?: never;
} & Record<string, unknown>;

const FORBIDDEN_NAME = /^(authorize|approve|allow)/i;

/**
 * Runtime reflection guard: throws if any exported function member of `api`
 * starts with `authorize`, `approve` or `allow`. No authorization verdict may
 * ever exist in the public surface of this engine.
 */
export function assertNonAuthorizing(api: object): void {
  const offenders = Object.keys(api).filter((name) => {
    const member = (api as Record<string, unknown>)[name];
    return typeof member === "function" && FORBIDDEN_NAME.test(name);
  });
  if (offenders.length > 0) {
    throw new Error(
      `NON_AUTHORIZING_BOUNDARY_VIOLATION: memory never authorizes operations — forbidden API member(s): ${offenders.join(", ")}`,
    );
  }
}
