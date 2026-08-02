/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * `drenyra-engram/lifecycle` — public entry for lifecycle transitions
 * (draft → reviewed → promoted → superseded; unknown states fail closed).
 */
export {
  applyTransition,
  isLegalTransition,
  supersede,
  transitionAuthority,
} from "./transitions.js";
export type { SupersedeInput, TransitionMeta } from "./transitions.js";
