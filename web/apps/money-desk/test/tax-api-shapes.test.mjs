// The tax slice's four list reads hold each answer to the one shape the contract declares: an object keyed by
// the list's own name (rates, profiles, pending_profiles, evidence), the key the generated client types as the
// answer's field. A bare array or a page of items is not readable, never half read. The core answers its rates,
// profiles and evidence lists with JSON null for an empty table (a Go nil slice; the handler has no nil guard),
// so null reads as an empty list there; the review queue builds its own slice and its contract says it is never
// null, so null is refused there.
import { test } from "node:test";
import assert from "node:assert/strict";
import { TaxApi } from "../src/tax/tax-api.ts";

// Stands in for the generated client: each read's answer is staged by operationId and handed back as a Result,
// so a test stages exactly the body a server wrote, right or wrong.
class StagedClient {
  constructor(staged) {
    this.staged = staged;
  }
  request(opts) {
    return opts.operationId in this.staged
      ? Promise.resolve({ ok: true, value: this.staged[opts.operationId] })
      : Promise.resolve({ ok: false, error: `nothing staged for ${opts.operationId}` });
  }
}

const RATE = { id: "7a000000-0000-4000-8000-000000000001", jurisdiction: "CA", reason_code: "STD_ON_HST_13", rate_bps: 775, effective_from: "2030-01-01" };
const PROFILE = { id: "7b000000-0000-4000-8000-000000000001", code: "RESALE", name: "Resale certificate", rate_bps: 0, requires_photo: false, requires_number: true, requires_attestation: false, status: "ACTIVE" };
const EVIDENCE = { id: "7c000000-0000-4000-8000-000000000001", number_mask: "\u2022\u2022\u2022\u2022 4821", attestation: true };

async function stagedRead(operationId, body, read) {
  const api = new TaxApi(new StagedClient({ [operationId]: body }));
  return read(api);
}

test("the rate table is read from its key alone, and a bare array or a page is not readable", async () => {
  assert.deepEqual(await stagedRead("taxListRates", { rates: [RATE] }, (api) => api.rates()), { ok: true, value: [RATE] });
  for (const shape of [[RATE], { items: [RATE] }]) {
    assert.deepEqual(
      await stagedRead("taxListRates", shape, (api) => api.rates()),
      { ok: false, error: "The rates were not readable." },
      `a ${Array.isArray(shape) ? "bare array" : "page of items"} is not the contract's shape`,
    );
  }
  // An empty table reaches the wire as JSON null (the core's nil slice), read as an empty list.
  assert.deepEqual(await stagedRead("taxListRates", { rates: null }, (api) => api.rates()), { ok: true, value: [] });
});

test("the profiles are read from their key alone, and a bare array or a page is not readable", async () => {
  assert.deepEqual(await stagedRead("taxListProfiles", { profiles: [PROFILE] }, (api) => api.profiles()), { ok: true, value: [PROFILE] });
  for (const shape of [[PROFILE], { items: [PROFILE] }]) {
    assert.deepEqual(
      await stagedRead("taxListProfiles", shape, (api) => api.profiles()),
      { ok: false, error: "The profiles were not readable." },
      `a ${Array.isArray(shape) ? "bare array" : "page of items"} is not the contract's shape`,
    );
  }
  assert.deepEqual(await stagedRead("taxListProfiles", { profiles: null }, (api) => api.profiles()), { ok: true, value: [] });
});

test("the review queue is read from its key alone, and a bare array, a page or a null is not readable", async () => {
  assert.deepEqual(await stagedRead("taxReviewQueue", { pending_profiles: [PROFILE] }, (api) => api.reviewQueue()), { ok: true, value: [PROFILE] });
  for (const shape of [[PROFILE], { items: [PROFILE] }, { pending_profiles: null }]) {
    assert.deepEqual(
      await stagedRead("taxReviewQueue", shape, (api) => api.reviewQueue()),
      { ok: false, error: "The profiles waiting for review were not readable." },
      `the queue is never null on the core's wire, so a ${Array.isArray(shape) ? "bare array" : typeof shape === "object" ? "page of items" : "null"} is not the contract's shape`,
    );
  }
});

test("the evidence list is read from its key alone, and a bare array or a page is not readable", async () => {
  assert.deepEqual(await stagedRead("taxListEvidence", { evidence: [EVIDENCE] }, (api) => api.evidence("")), { ok: true, value: [EVIDENCE] });
  for (const shape of [[EVIDENCE], { items: [EVIDENCE] }]) {
    assert.deepEqual(
      await stagedRead("taxListEvidence", shape, (api) => api.evidence("")),
      { ok: false, error: "The evidence records were not readable." },
      `a ${Array.isArray(shape) ? "bare array" : "page of items"} is not the contract's shape`,
    );
  }
  assert.deepEqual(await stagedRead("taxListEvidence", { evidence: null }, (api) => api.evidence("")), { ok: true, value: [] });
});
