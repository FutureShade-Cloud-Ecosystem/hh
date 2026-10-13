// The desk's client with no signal. A write with no signal is refused before
// anything dials (the shell's guard rides the client's fetch), and the two
// writes the core admits through sync, a new quote and a quote's expiry date,
// are kept on the device by the draft keeper instead of being lost. Money never
// queues: the keeper is offered nothing else, and a server's refusal is never
// kept.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { NO_SIGNAL_ERROR, fixedSignal, isNoSignal, noSignalFetch } from "@hardscapehouse/work-offline";
import { KEPT_DRAFT_WORDS, KEPT_VALIDITY_WORDS, QdClient } from "../src/qd-client.js";

const MAPLE = "c0000000-0000-4000-9000-000000000001";
const QUOTE = "0d000000-0000-4000-9000-000000000001";

function rig({ online = false, answer } = {}) {
  const calls = [];
  const signal = fixedSignal(online);
  const base = async (url, init) => {
    calls.push(`${init.method} ${url}`);
    const a = answer ?? { status: 200, body: "{}" };
    return { status: a.status, ok: a.status < 400, text: async () => a.body, arrayBuffer: async () => new ArrayBuffer(0) };
  };
  const client = new QdClient(new ApiClient({ baseUrl: "", fetch: noSignalFetch(base, signal) }), () => "u");
  const kept = [];
  const keeper = {
    keep: async (customerId, validUntil) => (kept.push(["new", customerId, validUntil]), { ok: true, value: { id: "x" } }),
    keepValidUntil: async (quoteId, validUntil) => (kept.push(["validity", quoteId, validUntil]), { ok: true, value: { id: quoteId } }),
  };
  return { client, calls, kept, keeper, signal };
}

test("offline, a new quote is kept on the device and the person is told so in plain words", async () => {
  const r = rig();
  r.client.setDraftKeeper(r.keeper);
  const result = await r.client.createQuote(MAPLE, "6f1c2b3a-0000-4000-8000-000000000001", "2999-12-31");
  assert.equal(result.ok, false);
  assert.equal(result.error, KEPT_DRAFT_WORDS);
  assert.equal(result.status, undefined);
  assert.deepEqual(r.kept, [["new", MAPLE, "2999-12-31"]]);
  assert.deepEqual(r.calls, [], "nothing dialed");
  assert.match(KEPT_DRAFT_WORDS, /^No signal\./);
  assert.match(KEPT_DRAFT_WORDS, /saved on this device/);
});

test("offline with no keeper, a new quote is the plain no signal refusal", async () => {
  const r = rig();
  const result = await r.client.createQuote(MAPLE, "b", "");
  assert.equal(isNoSignal(result), true);
  assert.equal(result.error, NO_SIGNAL_ERROR);
});

test("a keeper that cannot keep the draft says why, and the person is not told it was saved", async () => {
  const r = rig();
  r.client.setDraftKeeper({ ...r.keeper, keep: async () => ({ ok: false, error: "Pick a customer from the list first." }) });
  const result = await r.client.createQuote("", "b", "");
  assert.equal(result.ok, false);
  assert.equal(result.error, "Pick a customer from the list first.");
});

test("online, a new quote goes to the server and the keeper is never offered it", async () => {
  const r = rig({ online: true, answer: { status: 200, body: JSON.stringify({ id: "q-1", quote_no: "Q-1", customer_name: "A", customer_id: MAPLE, branch_id: "b", status: "DRAFT", subtotal_cents: 0, tax_cents: 0, total_cents: 0, lines: [], editable_fields: [], can_convert: false }) } });
  r.client.setDraftKeeper(r.keeper);
  await r.client.createQuote(MAPLE, "b", "");
  assert.deepEqual(r.kept, []);
  assert.equal(r.calls.length, 1);
});

test("a server refusal is the server's word and is never kept", async () => {
  const r = rig({ online: true, answer: { status: 409, body: JSON.stringify({ error: "no customer with that id" }) } });
  r.client.setDraftKeeper(r.keeper);
  const result = await r.client.createQuote(MAPLE, "b", "");
  assert.equal(result.ok, false);
  assert.equal(result.status, 409);
  assert.deepEqual(r.kept, []);
});

test("offline, an expiry change is kept, and says so", async () => {
  const r = rig();
  r.client.setDraftKeeper(r.keeper);
  const result = await r.client.setValidUntil(QUOTE, "2999-06-30");
  assert.equal(result.ok, false);
  assert.equal(result.error, KEPT_VALIDITY_WORDS);
  assert.deepEqual(r.kept, [["validity", QUOTE, "2999-06-30"]]);
});

test("offline, every other write is the plain refusal and the keeper is offered none of them", async () => {
  const r = rig();
  r.client.setDraftKeeper(r.keeper);
  const refusals = [
    await r.client.addLine(QUOTE, "p", "1", "sf"),
    await r.client.editLine(QUOTE, 1, { discPct: 5 }),
    await r.client.removeLine(QUOTE, 1),
    await r.client.mintShareLink(QUOTE),
    await r.client.shareBySMS(QUOTE),
    await r.client.convertWithFulfilment(QUOTE, "PICKUP", "00000000-0000-4000-8000-000000000001"),
    await r.client.decideApproval("a", true, "ok"),
    await r.client.requestFloorApproval(QUOTE, 1, 5, "reason"),
  ];
  for (const refusal of refusals) {
    assert.equal(refusal.ok, false);
    assert.equal(isNoSignal(refusal), true, JSON.stringify(refusal));
  }
  assert.deepEqual(r.kept, []);
  assert.deepEqual(r.calls, []);
});
