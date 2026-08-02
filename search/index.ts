/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * `drenyra-engram/search` — public entry for scope-first search
 * (filter before ranking; institutional observations require explicit opt-in).
 */
export { scopeFirstSearch } from "./scope-first.js";
export type {
  ScopeFirstSearchInput,
  ScopeFirstSearchResult,
} from "./scope-first.js";
