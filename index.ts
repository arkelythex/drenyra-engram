/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Drenyra Engram — public API.
 *
 * Vertical 1 (pre-alpha): memory core, in-memory store, scope-first search,
 * lifecycle transitions, and the non-authorization boundary.
 *
 * The `publicApi` object is the guarded surface: it satisfies `NonAuthorizing`
 * (compile-time) and passes `assertNonAuthorizing` (runtime reflection) at
 * module load. Memory guides; it never authorizes.
 */

export * from "./core/types.js";
export { InMemoryMemoryStore } from "./store/memory-store.js";
export type { MemoryStore } from "./store/memory-store.js";
export { scopeFirstSearch } from "./search/scope-first.js";
export type {
  ScopeFirstSearchInput,
  ScopeFirstSearchResult,
} from "./search/scope-first.js";
export {
  applyTransition,
  isLegalTransition,
  supersede,
  transitionAuthority,
} from "./lifecycle/transitions.js";
export type { SupersedeInput, TransitionMeta } from "./lifecycle/transitions.js";
export { assertNonAuthorizing, NON_AUTHORIZING_BOUNDARY } from "./authority/boundary.js";
export type { NonAuthorizing } from "./authority/boundary.js";

import {
  assertValidScope,
  assertValidValidity,
  isValidPeriod,
  isValidRuc,
  scopeEquals,
} from "./core/types.js";
import { InMemoryMemoryStore } from "./store/memory-store.js";
import { scopeFirstSearch } from "./search/scope-first.js";
import {
  applyTransition,
  isLegalTransition,
  supersede,
  transitionAuthority,
} from "./lifecycle/transitions.js";
import {
  assertNonAuthorizing,
  NON_AUTHORIZING_BOUNDARY,
  type NonAuthorizing,
} from "./authority/boundary.js";

/**
 * The exported runtime surface. `satisfies NonAuthorizing` is the compile-time
 * guard: growing an `authorize`/`approve`/`allow` member fails typecheck.
 */
export const publicApi = {
  InMemoryMemoryStore,
  scopeFirstSearch,
  transitionAuthority,
  applyTransition,
  supersede,
  isLegalTransition,
  assertNonAuthorizing,
  NON_AUTHORIZING_BOUNDARY,
  isValidRuc,
  isValidPeriod,
  assertValidScope,
  assertValidValidity,
  scopeEquals,
} satisfies NonAuthorizing;

// Runtime self-check: memory never authorizes. Runs at import; fails fast.
assertNonAuthorizing(publicApi);
