export const RUC_CLASSIFICATION = {
	VALID: "valid",
	INVALID_SHAPE: "invalid_shape",
	INVALID_CHECKSUM: "invalid_checksum",
} as const;
export type RucClassification =
	(typeof RUC_CLASSIFICATION)[keyof typeof RUC_CLASSIFICATION];

const RUC_WEIGHTS = [5, 4, 3, 2, 7, 6, 5, 4, 3, 2] as const;

/** Pure identifier validation only: no SUNAT lookup, membership, or authority. */
export function classifyRuc(ruc: string): RucClassification {
	if (!/^[0-9]{11}$/.test(ruc)) return RUC_CLASSIFICATION.INVALID_SHAPE;
	const sum = RUC_WEIGHTS.reduce(
		(total, weight, index) => total + Number(ruc[index]) * weight,
		0,
	);
	let expected = 11 - (sum % 11);
	if (expected === 10 || expected === 11) expected -= 10;
	return expected === Number(ruc[10])
		? RUC_CLASSIFICATION.VALID
		: RUC_CLASSIFICATION.INVALID_CHECKSUM;
}

export function isValidFiscalRuc(ruc: string): boolean {
	return classifyRuc(ruc) === RUC_CLASSIFICATION.VALID;
}
