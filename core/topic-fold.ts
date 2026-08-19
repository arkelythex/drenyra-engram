/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Canonical topic-key fold (sdd-060-tenant-cli, FR-TEN-3): the TypeScript twin
 * of internal/core/topic_fold.go. PURE and deterministic; joins the Go↔TS
 * golden parity mechanism (config golden_parity) — the same
 * testdata/golden/topic-fold.json vectors run from both runtimes and must agree
 * byte-identically.
 *
 * Fold contract:
 *   1. lower-case;
 *   2. every rune that is not a letter or digit becomes a SINGLE SPACE
 *      separator (punctuation, slashes, hyphens, dots, marks all fold to the
 *      same separator, preserving word boundaries);
 *   3. whitespace runs (real or punctuation-derived) collapse to one space and
 *      the result is trimmed.
 *
 * Accent folding is explicitly out of scope (the fold is conservative so
 * genuinely distinct keys never collide). "rule/IGV credit" and
 * "rule/igv-credit" both fold to "rule igv credit".
 */

/** FoldTopicKey normalizes a raw topic key for drift comparison. */
export function foldTopicKey(raw: string): string {
	let out = "";
	let pendingSpace = false;
	for (const ch of raw.toLowerCase()) {
		if (ch === " " || ch === "\t" || ch === "\n" || ch === "\r") {
			pendingSpace = true;
		} else if (isFoldChar(ch)) {
			if (pendingSpace && out.length > 0) out += " ";
			pendingSpace = false;
			out += ch;
		} else {
			pendingSpace = true; // punctuation → separator space
		}
	}
	return out;
}

/** isFoldChar reports whether the character survives the fold (letters/digits). */
function isFoldChar(ch: string): boolean {
	const code = ch.codePointAt(0) ?? 0;
	return (
		(ch >= "a" && ch <= "z") ||
		(ch >= "A" && ch <= "Z") ||
		(ch >= "0" && ch <= "9") ||
		(code >= 0x00c0 && code <= 0x024f) || // Latin-1 supplement + extended
		(code >= 0x0370 && code <= 0x03ff) // Greek
	);
}
