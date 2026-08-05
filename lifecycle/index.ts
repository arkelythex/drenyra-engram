/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * `drenyra-engram/lifecycle` — public entry for the v2 approval-gated lifecycle
 * (active → pending_review → approved/rejected; voided; superseded; unknown
 * states fail closed). The human gate is enforced by the transitions module.
 */
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
} from "./transitions.js";
export type { TransitionMeta } from "./transitions.js";
