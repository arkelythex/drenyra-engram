/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * `drenyra-engram/store` — public entry for the storage surface
 * (in-memory reference adapter, never canonical — ADR-002).
 */
export { InMemoryMemoryStore } from "./memory-store.js";
export type { MemoryStore } from "./memory-store.js";
