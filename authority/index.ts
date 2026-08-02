/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * `drenyra-engram/authority` — public entry for the non-authorization boundary.
 * Memory guides; it never authorizes.
 */
export { assertNonAuthorizing, NON_AUTHORIZING_BOUNDARY } from "./boundary.js";
export type { NonAuthorizing } from "./boundary.js";
