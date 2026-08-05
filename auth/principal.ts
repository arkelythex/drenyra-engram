/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money.
 *
 * Verified approval principal construction (v0.4.0 Step 1, ADR-003) — the
 * TypeScript mirror of `internal/auth/resolver.go`'s ONLY factory path.
 *
 * A principal is NEVER assembled from caller-declared claims: this factory
 * validates every field and throws on invalid input, and the returned object is
 * branded with a private symbol so a plain object literal can never be a
 * `VerifiedApprovalPrincipal` at runtime. The narrow `principalSnapshot` view
 * omits sessionId, token material, cookies and unrelated claims.
 */

import type {
	AccountingRole,
	AssuranceLevel,
	AuthenticationMethod,
	PrincipalSnapshot,
	VerifiedApprovalPrincipal,
} from "../core/types.js";
import {
	ACCOUNTING_ROLES,
	ASSURANCE_LEVELS,
	AUTHENTICATION_METHODS,
} from "../core/types.js";

/** Private brand: only the factory can mint a verified principal. */
const PRINCIPAL_BRAND = Symbol("verified-approval-principal");

/** Input shape for the factory — the mirror of auth.AuthenticationAssertion. */
export interface VerifiedPrincipalInput {
	subjectId: string;
	tenantId: string;
	membershipId: string;
	companyScopes: readonly string[];
	roles: readonly AccountingRole[];
	authenticationMethod: AuthenticationMethod;
	assuranceLevel: AssuranceLevel;
	/** RFC3339 authentication timestamp. */
	authenticatedAt: string;
	/** Optional session continuity id; never a raw token. */
	sessionId?: string;
}

function isNonEmptyString(value: unknown): value is string {
	return typeof value === "string" && value.trim().length > 0;
}

function invalid(detail: string): never {
	throw new Error(`PRINCIPAL_INVALID: ${detail}`);
}

/**
 * The ONLY way to obtain a `VerifiedApprovalPrincipal`. Validates every field
 * and throws `PRINCIPAL_INVALID` on malformed input; returns a brand-tagged,
 * read-only principal otherwise. There is no public plain-object constructor.
 */
export function createVerifiedApprovalPrincipal(
	input: VerifiedPrincipalInput,
): VerifiedApprovalPrincipal {
	if (!isNonEmptyString(input.subjectId)) {
		invalid("subjectId must be a non-empty string");
	}
	if (!isNonEmptyString(input.tenantId)) {
		invalid("tenantId must be a non-empty string");
	}
	if (!isNonEmptyString(input.membershipId)) {
		invalid("membershipId must be a non-empty string");
	}
	if (
		!Array.isArray(input.companyScopes) ||
		input.companyScopes.length === 0 ||
		!input.companyScopes.every((s) => isNonEmptyString(s))
	) {
		invalid("companyScopes must be a non-empty array of non-empty strings");
	}
	if (
		!Array.isArray(input.roles) ||
		input.roles.length === 0 ||
		!input.roles.every(
			(role): role is AccountingRole =>
				(ACCOUNTING_ROLES as readonly string[]).includes(role),
		)
	) {
		invalid(`roles must be known accounting roles (${ACCOUNTING_ROLES.join("|")})`);
	}
	if (!(AUTHENTICATION_METHODS as readonly string[]).includes(input.authenticationMethod)) {
		invalid(
			`authenticationMethod must be one of ${AUTHENTICATION_METHODS.join("|")}`,
		);
	}
	if (!(ASSURANCE_LEVELS as readonly string[]).includes(input.assuranceLevel)) {
		invalid(`assuranceLevel must be one of ${ASSURANCE_LEVELS.join("|")}`);
	}
	if (
		!isNonEmptyString(input.authenticatedAt) ||
		Number.isNaN(Date.parse(input.authenticatedAt))
	) {
		invalid("authenticatedAt must be a parseable RFC3339 date string");
	}
	if (
		input.sessionId !== undefined &&
		(typeof input.sessionId !== "string" || input.sessionId.length === 0)
	) {
		invalid("sessionId must be a non-empty string when present");
	}

	const principal = {
		subjectId: input.subjectId,
		tenantId: input.tenantId,
		membershipId: input.membershipId,
		companyScopes: Object.freeze([...input.companyScopes]),
		roles: Object.freeze([...input.roles]),
		authenticationMethod: input.authenticationMethod,
		assuranceLevel: input.assuranceLevel,
		authenticatedAt: input.authenticatedAt,
		...(input.sessionId === undefined ? {} : { sessionId: input.sessionId }),
	} as VerifiedApprovalPrincipal;
	// Brand BEFORE freezing: a frozen object cannot gain properties.
	Object.defineProperty(principal, PRINCIPAL_BRAND, {
		value: true,
		enumerable: false,
	});
	return Object.freeze(principal);
}

/** Runtime brand guard: only factory-minted principals pass. */
export function isVerifiedApprovalPrincipal(
	value: unknown,
): value is VerifiedApprovalPrincipal {
	return (
		typeof value === "object" &&
		value !== null &&
		(value as Record<PropertyKey, unknown>)[PRINCIPAL_BRAND] === true
	);
}

/** Sorts and deduplicates a role list canonically (Go parity). */
export function canonicalRoles(
	roles: readonly AccountingRole[],
): AccountingRole[] {
	return [...new Set(roles)].sort();
}

/**
 * The deliberately narrow, serializable view of a principal. Deliberately omits
 * sessionId, token material, cookies and unrelated claims; roles are sorted and
 * deduplicated so Go and TypeScript produce identical JSON bytes.
 */
export function principalSnapshot(
	principal: VerifiedApprovalPrincipal,
): PrincipalSnapshot {
	return {
		subjectId: principal.subjectId,
		membershipId: principal.membershipId,
		roles: canonicalRoles(principal.roles),
		authenticationMethod: principal.authenticationMethod,
		assuranceLevel: principal.assuranceLevel,
		authenticatedAt: principal.authenticatedAt,
	};
}
