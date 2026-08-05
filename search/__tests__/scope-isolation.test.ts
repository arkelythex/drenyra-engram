/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * THE MANDATORY cross-tenant isolation conformance test (contracts/scope.md).
 *
 * Covers:
 * - A company-A observation is NEVER surfaced by a company-B query, even with
 *   identical content and identical topicKey (the scope filter runs before any
 *   scoring, so leakage is structurally impossible, and this test proves it).
 * - Institutional observations are returned ONLY for institutional queries or
 *   an explicit `includeInstitutional` flag — never in a plain company query.
 * - `matchMode` "all" requires every query token; "any" requires at least one.
 */

import { describe, expect, it } from "vitest";

import type { MemoryContent, MemoryScope } from "../../core/types.js";
import { InMemoryMemoryStore } from "../../store/memory-store.js";
import { scopeFirstSearch } from "../scope-first.js";

const ORG = "org-001";
const RUC_A = "20100039201";
const RUC_B = "20600995804";
const PERIOD = "202401";

const companyA = (): MemoryScope => ({
  kind: "company",
  organizationId: ORG,
  companyId: "acme-a",
  ruc: RUC_A,
  period: PERIOD,
});

const companyB = (): MemoryScope => ({
  kind: "company",
  organizationId: ORG,
  companyId: "acme-b",
  ruc: RUC_B,
  period: PERIOD,
});

const SOURCE = { system: "vitest", actorId: "test-agent", actorKind: "agent" as const };

const IGV_CONTENT: MemoryContent = {
  what: "IGV base rate is 18 percent",
  why: "standard rate for goods",
  where: "Peru",
  learned: "applies to all invoices",
};

const INSTITUTIONAL_CONTENT: MemoryContent = {
  what: "Banking rules apply to all clients",
  why: "cross-company convention",
  where: "Peru",
  learned: "institutional knowledge",
};

describe("cross-tenant scope isolation", () => {
  it("must NEVER leak a company-A observation into a company-B query", async () => {
    const store = new InMemoryMemoryStore();

    // Identical text, identical topicKey — differing only by scope (ruc).
    await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate",
      kind: "rule" as const,
          fiscalEffect: "none" as const,
          effectiveAt: "2024-01-01T00:00:00.000Z",
      scope: companyA(),
      content: IGV_CONTENT,
      source: SOURCE,
    });
    await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate",
      kind: "rule" as const,
          fiscalEffect: "none" as const,
          effectiveAt: "2024-01-01T00:00:00.000Z",
      scope: companyB(),
      content: IGV_CONTENT,
      source: SOURCE,
    });

    // From company B: exactly one result, and it is B's own observation.
    const fromB = scopeFirstSearch(store, {
      query: "IGV base rate",
      scope: companyB(),
      matchMode: "any",
    });
    expect(fromB).toHaveLength(1);
    if (fromB[0].memory.scope.kind === "company") {
      expect(fromB[0].memory.scope.ruc).toBe(RUC_B);
    }
    expect(
      fromB.some(
        (result) =>
          result.memory.scope.kind === "company" &&
          result.memory.scope.ruc === RUC_A,
      ),
    ).toBe(false);

    // From company A: exactly one result, and it is A's own observation.
    const fromA = scopeFirstSearch(store, {
      query: "IGV base rate",
      scope: companyA(),
      matchMode: "any",
    });
    expect(fromA).toHaveLength(1);
    if (fromA[0].memory.scope.kind === "company") {
      expect(fromA[0].memory.scope.ruc).toBe(RUC_A);
    }
    expect(
      fromA.some(
        (result) =>
          result.memory.scope.kind === "company" &&
          result.memory.scope.ruc === RUC_B,
      ),
    ).toBe(false);
  });

  it("only surfaces institutional observations on explicit intent, never in a plain company query", async () => {
    const store = new InMemoryMemoryStore();

    await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate",
      kind: "rule" as const,
          fiscalEffect: "none" as const,
          effectiveAt: "2024-01-01T00:00:00.000Z",
      scope: companyA(),
      content: IGV_CONTENT,
      source: SOURCE,
    });
    await store.save({
      topicKey: "policy.banking-rules",
      title: "Banking rules",
      kind: "rule" as const,
          fiscalEffect: "none" as const,
          effectiveAt: "2024-01-01T00:00:00.000Z",
      scope: { kind: "institutional" },
      content: INSTITUTIONAL_CONTENT,
      source: SOURCE,
    });

    // Plain company-A query: institutional observation must NOT appear.
    const plainA = scopeFirstSearch(store, {
      query: "banking rules",
      scope: companyA(),
      matchMode: "any",
    });
    expect(plainA).toHaveLength(0);

    // Explicit flag: institutional observation appears alongside A's own.
    const explicitA = scopeFirstSearch(store, {
      query: "banking rules",
      scope: companyA(),
      matchMode: "any",
      includeInstitutional: true,
    });
    expect(
      explicitA.some((result) => result.memory.scope.kind === "institutional"),
    ).toBe(true);

    // Institutional query scope: institutional observation returned, and A's
    // scoped observation never leaks into it.
    const institutionalQuery = scopeFirstSearch(store, {
      query: "banking rules",
      scope: { kind: "institutional" },
      matchMode: "any",
    });
    expect(institutionalQuery).toHaveLength(1);
    expect(institutionalQuery[0].memory.scope).toEqual({ kind: "institutional" });
    expect(
      institutionalQuery.some((result) => result.memory.scope.kind === "company"),
    ).toBe(false);
  });

  it("matchMode all requires every token; matchMode any requires at least one", async () => {
    const store = new InMemoryMemoryStore();

    await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate",
      kind: "rule" as const,
          fiscalEffect: "none" as const,
          effectiveAt: "2024-01-01T00:00:00.000Z",
      scope: companyB(),
      content: IGV_CONTENT,
      source: SOURCE,
    });

    // "payroll" is not in B's observation: "all" rejects, "any" accepts.
    const allMode = scopeFirstSearch(store, {
      query: "payroll igv rate",
      scope: companyB(),
      matchMode: "all",
    });
    expect(allMode).toHaveLength(0);

    const anyMode = scopeFirstSearch(store, {
      query: "payroll igv rate",
      scope: companyB(),
      matchMode: "any",
    });
    expect(anyMode).toHaveLength(1);
    expect(anyMode[0].score).toBeGreaterThan(0);
  });
});
