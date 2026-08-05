/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Drenyra Engram — public API (v2 reference mirror).
 *
 * AccountingMemory kernel: memory core, in-memory store, scope-first search,
 * approval-gated lifecycle transitions, and the non-authorization boundary.
 *
 * The `publicApi` object is the guarded surface: it satisfies `NonAuthorizing`
 * (compile-time) and passes `assertNonAuthorizing` (runtime reflection) at
 * module load. In v2, `approve`/`reject` ARE part of the surface — they are the
 * PROFESSIONAL review of a memory (the human gate on pending_review), never
 * authorization of business actions. The boundary bans authorize/allow/execute/
 * declare/file/pay.
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
	applyGateTransition,
	approve,
	assertHumanApproval,
	canApprove,
	canReject,
	canVoid,
	initialStatus,
	isGated,
	reject,
	supersedePrev,
	voidMemory,
} from "./lifecycle/transitions.js";
export type { TransitionMeta } from "./lifecycle/transitions.js";
export {
	assertNonAuthorizing,
	NON_AUTHORIZING_BOUNDARY,
} from "./authority/boundary.js";
export type { NonAuthorizing } from "./authority/boundary.js";

import {
	assertValidMemory,
	assertValidScope,
	assertValidSource,
	assertValidValidity,
	isValidPeriod,
	isValidRuc,
	scopeEquals,
} from "./core/types.js";
import { InMemoryMemoryStore } from "./store/memory-store.js";
import { scopeFirstSearch } from "./search/scope-first.js";
import {
	applyGateTransition,
	assertHumanApproval,
	initialStatus,
	isGated,
} from "./lifecycle/transitions.js";
import {
	assertNonAuthorizing,
	NON_AUTHORIZING_BOUNDARY,
	type NonAuthorizing,
} from "./authority/boundary.js";

/**
 * The exported runtime surface. `satisfies NonAuthorizing` is the compile-time
 * guard: growing an `authorize`/`allow`/`execute` member fails typecheck.
 */
export const publicApi = {
	InMemoryMemoryStore,
	scopeFirstSearch,
	initialStatus,
	isGated,
	assertHumanApproval,
	applyGateTransition,
	assertNonAuthorizing,
	NON_AUTHORIZING_BOUNDARY,
	isValidRuc,
	isValidPeriod,
	assertValidScope,
	assertValidSource,
	assertValidValidity,
	assertValidMemory,
	scopeEquals,
} satisfies NonAuthorizing;

// Runtime self-check: memory never authorizes. Runs at import; fails fast.
assertNonAuthorizing(publicApi);
