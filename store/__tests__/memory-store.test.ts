/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * InMemoryMemoryStore behavior (contracts/memory.md):
 * - Upsert by topicKey + exact scope: first save is revision 1 (`created`);
 *   a second save of the same chain is revision 2 (`updated`) with history
 *   preserved — both revisions remain retrievable by id.
 * - Scope is part of identity: the same topicKey under a different scope is a
 *   different chain (a fresh revision 1, never a revision of the other tenant).
 * - Reads: findById, findByTopicKey (latest), findByScope (exact), list, and
 *   listByAuthority. A promoted observation is never edited in place.
 */

import { describe, expect, it } from "vitest";

import type { MemoryScope, SaveMemoryInput } from "../../core/types.js";
import { InMemoryMemoryStore } from "../memory-store.js";

const ORG = "org-001";
const RUC_A = "20100039201";
const RUC_B = "20600995804";
const PERIOD = "202401";
const T = "2026-01-15T12:00:00.000Z";

const scope = (ruc: string = RUC_A): MemoryScope => ({
  kind: "company",
  organizationId: ORG,
  companyId: "acme",
  ruc,
  period: PERIOD,
});

function input(topicKey: string, what: string): SaveMemoryInput {
  return {
    topicKey,
    title: "IGV base rate",
    type: "policy",
    scope: scope(),
    content: {
      what,
      why: "standard rate for goods",
      where: "Peru",
      learned: "applies to all invoices",
    },
    provenance: { actor: "test-agent", timestamp: T, source: "vitest" },
  };
}

describe("InMemoryMemoryStore", () => {
  it("creates revision 1 with outcome created on first save", async () => {
    const store = new InMemoryMemoryStore();
    const result = await store.save(input("tax.igv.rate", "first version"));

    expect(result.outcome).toBe("created");
    expect(result.observation.revision).toBe(1);
    expect(result.observation.authorityStatus).toBe("draft");
    expect(result.observation.identity.topicKey).toBe("tax.igv.rate");
  });

  it("creates revision 2 on a second save of the same chain and preserves history", async () => {
    const store = new InMemoryMemoryStore();
    const first = await store.save(input("tax.igv.rate", "first version"));
    const second = await store.save(input("tax.igv.rate", "second version"));

    expect(second.outcome).toBe("updated");
    expect(second.observation.revision).toBe(2);
    expect(second.observation.identity.id).not.toBe(first.observation.identity.id);

    // History preserved: both revisions remain retrievable by id.
    expect(store.findById(first.observation.identity.id)?.revision).toBe(1);
    expect(store.findById(first.observation.identity.id)?.content.what).toBe(
      "first version",
    );
    expect(store.findById(second.observation.identity.id)?.revision).toBe(2);
  });

  it("returns undefined for unknown ids", async () => {
    const store = new InMemoryMemoryStore();
    expect(store.findById("does-not-exist")).toBeUndefined();
  });

  it("findByTopicKey returns the latest revision for the exact scope", async () => {
    const store = new InMemoryMemoryStore();
    await store.save(input("tax.igv.rate", "first version"));
    await store.save(input("tax.igv.rate", "second version"));

    const latest = store.findByTopicKey("tax.igv.rate", scope());
    expect(latest?.revision).toBe(2);
    expect(latest?.content.what).toBe("second version");
  });

  it("treats scope as part of identity — same topicKey under another scope is a new chain", async () => {
    const store = new InMemoryMemoryStore();
    const a = await store.save({ ...input("tax.igv.rate", "first version"), scope: scope(RUC_A) });
    const b = await store.save({ ...input("tax.igv.rate", "first version"), scope: scope(RUC_B) });

    expect(a.outcome).toBe("created");
    expect(b.outcome).toBe("created");
    expect(b.observation.revision).toBe(1);
    expect(store.findById(b.observation.identity.id)?.scope).toEqual(scope(RUC_B));
  });

  it("findByScope filters by exact scope", async () => {
    const store = new InMemoryMemoryStore();
    const a = await store.save({ ...input("topic.a", "version a"), scope: scope(RUC_A) });
    await store.save({ ...input("topic.b", "version b"), scope: scope(RUC_B) });

    const inA = store.findByScope(scope(RUC_A));
    expect(inA).toHaveLength(1);
    expect(inA[0].identity.id).toBe(a.observation.identity.id);
  });

  it("list returns every stored revision", async () => {
    const store = new InMemoryMemoryStore();
    await store.save(input("tax.igv.rate", "first version"));
    await store.save(input("tax.igv.rate", "second version"));

    expect(store.list()).toHaveLength(2);
  });

  it("listByAuthority filters by authority status", async () => {
    const store = new InMemoryMemoryStore();
    const first = await store.save(input("tax.igv.rate", "first version"));
    await store.save(input("tax.igv.rate", "second version"));

    expect(store.listByAuthority("draft")).toHaveLength(2);

    store.applyStatusTransition(first.observation.identity.id, "reviewed", {
      actor: "reviewer",
      timestamp: T,
    });

    expect(store.listByAuthority("draft")).toHaveLength(1);
    expect(store.listByAuthority("reviewed")).toHaveLength(1);
  });

  it("never edits a promoted observation in place — a new save creates a new revision", async () => {
    const store = new InMemoryMemoryStore();
    const first = await store.save(input("tax.igv.rate", "first version"));

    store.applyStatusTransition(first.observation.identity.id, "reviewed", {
      actor: "reviewer",
      timestamp: T,
    });
    store.applyStatusTransition(first.observation.identity.id, "promoted", {
      actor: "owner",
      timestamp: T,
    });

    const second = await store.save(input("tax.igv.rate", "second version"));
    expect(second.observation.revision).toBe(2);

    const promoted = store.findById(first.observation.identity.id);
    expect(promoted?.authorityStatus).toBe("promoted");
    expect(promoted?.content.what).toBe("first version");
  });
});
