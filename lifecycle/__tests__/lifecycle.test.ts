/**
 * Fiscal convention: monetary values in the Drenyra ecosystem are BigInt cents;
 * no float is ever used for money; sequence/revision counters are JSON integers,
 * never floats.
 *
 * Lifecycle (contracts/lifecycle.md):
 * - Legal chain draft → reviewed → promoted → superseded; anything else throws
 *   INVALID_TRANSITION (fail closed; stricter than the 0.1-draft table).
 * - supersede requires a target id, marks the old observation superseded, and
 *   records a `supersedes` relation (readers route to the successor).
 * - Vigencia: an expired observation (expiresAt < now) surfaces as stale in
 *   search results, never as current fact.
 */

import { describe, expect, it } from "vitest";

import type { MemoryContent, MemoryScope } from "../../core/types.js";
import { InMemoryMemoryStore } from "../../store/memory-store.js";
import { scopeFirstSearch } from "../../search/scope-first.js";
import {
  applyTransition,
  supersede,
  transitionAuthority,
} from "../transitions.js";

const SCOPE: MemoryScope = {
  kind: "company",
  organizationId: "org-001",
  companyId: "acme",
  ruc: "20100039201",
  period: "202401",
};
const T = "2026-01-15T12:00:00.000Z";
const PROVENANCE = { actor: "test-agent", timestamp: T, source: "vitest" };

const CONTENT: MemoryContent = {
  what: "IGV base rate is 18 percent",
  why: "standard rate for goods",
  where: "Peru",
  learned: "applies to all invoices",
};

describe("transitionAuthority", () => {
  it("allows the legal chain draft → reviewed → promoted → superseded", () => {
    expect(() => transitionAuthority("draft", "reviewed")).not.toThrow();
    expect(() => transitionAuthority("reviewed", "promoted")).not.toThrow();
    expect(() => transitionAuthority("promoted", "superseded")).not.toThrow();
  });

  it("rejects promoted → draft with INVALID_TRANSITION", () => {
    expect(() => transitionAuthority("promoted", "draft")).toThrow(
      /INVALID_TRANSITION/,
    );
  });

  it("rejects draft → promoted (only adjacent forward transitions are legal)", () => {
    expect(() => transitionAuthority("draft", "promoted")).toThrow(
      /INVALID_TRANSITION/,
    );
  });

  it("rejects self-transitions and non-terminal jumps", () => {
    expect(() => transitionAuthority("reviewed", "reviewed")).toThrow(
      /INVALID_TRANSITION/,
    );
    expect(() => transitionAuthority("draft", "superseded")).toThrow(
      /INVALID_TRANSITION/,
    );
  });
});

describe("applyTransition", () => {
  it("applies a legal transition and records an audit-trail entry", async () => {
    const store = new InMemoryMemoryStore();
    const saved = await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      provenance: PROVENANCE,
    });

    const reviewed = applyTransition(
      store,
      saved.observation.identity.id,
      "reviewed",
      { actor: "reviewer", timestamp: T },
    );
    expect(reviewed.authorityStatus).toBe("reviewed");
    expect(store.findById(saved.observation.identity.id)?.authorityStatus).toBe(
      "reviewed",
    );

    const log = store.transitionLog();
    expect(
      log.some(
        (entry) =>
          entry.observationId === saved.observation.identity.id &&
          entry.from === "draft" &&
          entry.to === "reviewed" &&
          entry.actor === "reviewer",
      ),
    ).toBe(true);
  });

  it("rejects illegal transitions and leaves the observation unchanged", async () => {
    const store = new InMemoryMemoryStore();
    const saved = await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      provenance: PROVENANCE,
    });

    expect(() =>
      applyTransition(store, saved.observation.identity.id, "promoted", {
        actor: "owner",
        timestamp: T,
      }),
    ).toThrow(/INVALID_TRANSITION/);
    expect(store.findById(saved.observation.identity.id)?.authorityStatus).toBe(
      "draft",
    );
  });
});

describe("supersede", () => {
  it("marks the old observation superseded and records a supersedes relation", async () => {
    const store = new InMemoryMemoryStore();
    const first = await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      provenance: PROVENANCE,
    });
    const second = await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate (updated)",
      type: "policy",
      scope: SCOPE,
      content: { ...CONTENT, what: "IGV base rate is 18 percent since 2011" },
      provenance: PROVENANCE,
    });

    applyTransition(store, first.observation.identity.id, "reviewed", {
      actor: "reviewer",
      timestamp: T,
    });
    applyTransition(store, first.observation.identity.id, "promoted", {
      actor: "owner",
      timestamp: T,
    });
    applyTransition(store, second.observation.identity.id, "reviewed", {
      actor: "reviewer",
      timestamp: T,
    });
    applyTransition(store, second.observation.identity.id, "promoted", {
      actor: "owner",
      timestamp: T,
    });

    supersede({
      store,
      observationId: first.observation.identity.id,
      targetId: second.observation.identity.id,
      actor: "owner",
      timestamp: T,
    });

    expect(store.findById(first.observation.identity.id)?.authorityStatus).toBe(
      "superseded",
    );
    expect(store.findById(second.observation.identity.id)?.authorityStatus).toBe(
      "promoted",
    );
    expect(
      store.relations().some(
        (record) =>
          record.fromId === first.observation.identity.id &&
          record.toId === second.observation.identity.id &&
          record.relation === "supersedes",
      ),
    ).toBe(true);
  });

  it("routes readers of a superseded observation to its successor", async () => {
    const store = new InMemoryMemoryStore();
    const first = await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      provenance: PROVENANCE,
    });
    const second = await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate (updated)",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      provenance: PROVENANCE,
    });

    applyTransition(store, first.observation.identity.id, "reviewed", {
      actor: "reviewer",
      timestamp: T,
    });
    applyTransition(store, first.observation.identity.id, "promoted", {
      actor: "owner",
      timestamp: T,
    });

    supersede({
      store,
      observationId: first.observation.identity.id,
      targetId: second.observation.identity.id,
      actor: "owner",
      timestamp: T,
    });

    expect(store.successorOf(first.observation.identity.id)?.identity.id).toBe(
      second.observation.identity.id,
    );
  });

  it("refuses to supersede an observation that is not promoted", async () => {
    const store = new InMemoryMemoryStore();
    const first = await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      provenance: PROVENANCE,
    });
    const second = await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate (updated)",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      provenance: PROVENANCE,
    });

    expect(() =>
      supersede({
        store,
        observationId: first.observation.identity.id,
        targetId: second.observation.identity.id,
        actor: "owner",
        timestamp: T,
      }),
    ).toThrow(/INVALID_TRANSITION/);
  });

  it("throws when the target observation does not exist", async () => {
    const store = new InMemoryMemoryStore();
    const first = await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      provenance: PROVENANCE,
    });

    applyTransition(store, first.observation.identity.id, "reviewed", {
      actor: "reviewer",
      timestamp: T,
    });
    applyTransition(store, first.observation.identity.id, "promoted", {
      actor: "owner",
      timestamp: T,
    });

    expect(() =>
      supersede({
        store,
        observationId: first.observation.identity.id,
        targetId: "missing-target",
        actor: "owner",
        timestamp: T,
      }),
    ).toThrow(/OBSERVATION_NOT_FOUND/);
  });

  it("throws when superseding itself", async () => {
    const store = new InMemoryMemoryStore();
    const first = await store.save({
      topicKey: "tax.igv.rate",
      title: "IGV base rate",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      provenance: PROVENANCE,
    });

    expect(() =>
      supersede({
        store,
        observationId: first.observation.identity.id,
        targetId: first.observation.identity.id,
        actor: "owner",
        timestamp: T,
      }),
    ).toThrow(/INVALID_SUPERSEDE_TARGET/);
  });
});

describe("vigencia — stale reads", () => {
  it("surfaces an expired observation as stale in search results", async () => {
    const store = new InMemoryMemoryStore();
    await store.save({
      topicKey: "tax.igv.expired-rule",
      title: "Old IGV rule",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      validity: { expiresAt: "2000-01-01T00:00:00.000Z" },
      provenance: PROVENANCE,
    });

    const results = scopeFirstSearch(store, {
      query: "old igv",
      scope: SCOPE,
      matchMode: "any",
    });
    expect(results).toHaveLength(1);
    expect(results[0].stale).toBe(true);
  });

  it("does not flag valid or validity-less observations as stale", async () => {
    const store = new InMemoryMemoryStore();
    const future = new Date(Date.now() + 86_400_000).toISOString();
    await store.save({
      topicKey: "tax.igv.valid-rule",
      title: "IGV current rule",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      validity: { expiresAt: future },
      provenance: PROVENANCE,
    });
    await store.save({
      topicKey: "tax.igv.no-window",
      title: "IGV rule without window",
      type: "policy",
      scope: SCOPE,
      content: CONTENT,
      provenance: PROVENANCE,
    });

    const results = scopeFirstSearch(store, {
      query: "igv rule",
      scope: SCOPE,
      matchMode: "any",
    });
    expect(results).toHaveLength(2);
    expect(results.every((result) => result.stale === false)).toBe(true);
  });
});
