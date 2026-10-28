// The deployment settings surface's decisions, with no DOM and no network:
// the four sections' form checks in the core's own words, the words only
// zone (no raw enum on a screen), the refusals naming who may act, and the
// door's calls. The brand half is the parity ledger's own hold: the pair is
// registered in the contract and not yet served by the core, so its reads
// wait, and the words say what waits.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { SettingsApi } from "../src/settings-api.js";
import {
  BRAND_WAITS_WORDS,
  counterPolicyRefusalWords,
  chipSetError,
  linePresentationWord,
  marginVisibilityWord,
  policyHistoryRefusalWords,
  policyHistoryWho,
  restockingFeeError,
  tourLineError,
  tourPlatformWords,
  workspaceArrangementWord,
  workspaceSplitError,
} from "../src/settings-logic.js";

// -- the forms' checks, in the core's own words --------------------------------------------------------

test("a walkthrough line speaks in headlines: at most 72 characters, and never nothing", () => {
  assert.equal(tourLineError("Tap the yard queue to see what is waiting."), "");
  assert.equal(
    tourLineError("x".repeat(73)),
    "A step says one line and the limit is 72 characters, because a walkthrough speaks in headlines.",
  );
  assert.equal(tourLineError("x".repeat(72)), "");
  assert.equal(tourLineError(""), "A step with nothing to say is a step to delete.");
});

test("quick chips hold at most 8 categories, each 1 to 40 characters with no comma", () => {
  assert.equal(chipSetError(["Pavers", "Wall block"]), "");
  assert.equal(chipSetError([]), "");
  assert.equal(chipSetError(["Pavers", "Wall block", "a".repeat(41)]), "Each quick chip category must be 1 to 40 characters with no comma.");
  assert.equal(chipSetError(["Pavers,Wall"]), "Each quick chip category must be 1 to 40 characters with no comma.");
  assert.equal(chipSetError(Array.from({ length: 9 }, (_, i) => `c${i}`)), "Quick chips hold at most 8 categories.");
});

test("a counter split outside 20 to 80 is refused in the core's words", () => {
  assert.equal(workspaceSplitError(58), "");
  assert.equal(workspaceSplitError(19), "Workspace split must be between 20 and 80.");
  assert.equal(workspaceSplitError(81), "Workspace split must be between 20 and 80.");
});

test("a restocking fee is a whole number of basis points, never negative", () => {
  assert.equal(restockingFeeError(0), "");
  assert.equal(restockingFeeError(250), "");
  assert.equal(restockingFeeError(-1), "The restocking fee cannot be negative.");
  assert.equal(restockingFeeError(1.5), "The restocking fee is a whole number of basis points.");
});

// -- the words only zone -------------------------------------------------------------------------------

test("the visibility and presentation classes render as words, never a raw enum", () => {
  assert.equal(marginVisibilityWord("MANAGER_UP"), "Managers and above");
  assert.equal(marginVisibilityWord("ALL_STAFF"), "Everyone");
  assert.equal(marginVisibilityWord("HIDDEN"), "Hidden");
  assert.equal(linePresentationWord("LINES"), "Lines");
  assert.equal(linePresentationWord("CARDS"), "Cards");
  assert.equal(workspaceArrangementWord("LOOKUP_LEFT"), "Lookup on the left, the sale on the right");
  assert.equal(workspaceArrangementWord("SALE_LEFT"), "Sale on the left, the lookup on the right");
});

test("the policy history names who changed a knob, or somebody when the trail carries no name", () => {
  assert.equal(policyHistoryWho(""), "somebody");
  // UX-106 round 3: an actor the trail cannot name reads as a word, never as
  // the id's own first block.
  assert.equal(policyHistoryWho("da110000-0000-4000-9000-000000000001"), "Unknown actor");
});

// -- the refusals --------------------------------------------------------------------------------------

test("a shared Forbidden at the counter becomes words naming who may act", () => {
  assert.equal(counterPolicyRefusalWords("Forbidden", 403), "You may not set the counter's policy. Setting it is the dealer's administrators' own.");
  assert.equal(counterPolicyRefusalWords("pos: line_presentation must be LINES or CARDS", 422), "pos: line_presentation must be LINES or CARDS");
});

test("the policy history's shared Forbidden becomes words naming who may read the trail", () => {
  assert.equal(
    policyHistoryRefusalWords("Forbidden", 403),
    "You may not read the policy's history. The trail of who changed a knob is the administrators' own.",
  );
  assert.equal(policyHistoryRefusalWords("try again", 500), "try again");
});

test("the walkthrough's copy and its demo switch are the platform administrator's, and the core's sentence passes whole", () => {
  assert.equal(
    tourPlatformWords("tour: the walkthrough is the platform administrator's to set up, GENERAL_MANAGER cannot change its copy or its demo switch"),
    "tour: the walkthrough is the platform administrator's to set up, GENERAL_MANAGER cannot change its copy or its demo switch",
  );
});

test("the brand half's waits words name what waits, and never how the product is built", () => {
  assert.match(BRAND_WAITS_WORDS, /Waits for the platform/);
  assert.doesNotMatch(BRAND_WAITS_WORDS, /ledger|contract|route|table/i);
});

// -- the door -------------------------------------------------------------------------------------------

function apiAnswering(answer) {
  const calls = [];
  const fetch = async (url, init) => {
    calls.push({ url: String(url), init: init ?? {} });
    return new Response(JSON.stringify(answer.body), {
      status: answer.status ?? 200,
      headers: { "content-type": "application/json" },
    });
  };
  return { api: new SettingsApi(new ApiClient({ baseUrl: "http://core.test", fetch })), calls };
}

test("the card rail read projects the status and never a credential", async () => {
  const { api, calls } = apiAnswering({
    body: { configured: true, merchant_id: "m-1", processor: "run_payments", currency: "usd", simulated: true, secret_thing: "no" },
  });
  const result = await api.paymentsConfig();
  assert.equal(result.ok, true);
  assert.deepEqual(result.value, { configured: true, merchant_id: "m-1", processor: "run_payments", currency: "usd", simulated: true });
  assert.match(calls[0].url, /\/api\/v1\/payments\/config$/);
});

test("the card rail has no write door: the stored credentials are never replaced from here", () => {
  const { api } = apiAnswering({ body: {} });
  assert.equal(typeof api.putPaymentsConfig, "undefined");
});

test("the counter policy read and write ride the pos policy route", async () => {
  const { api, calls } = apiAnswering({
    body: {
      branch_id: "",
      cod_enabled: true,
      restocking_fee_bps: 250,
      notes: "",
      line_presentation: "LINES",
      quick_chips_enabled: true,
      workspace_split_pct: 58,
      workspace_arrangement: "LOOKUP_LEFT",
      lookup_presentation: "CARDS",
      margin_visibility: "MANAGER_UP",
      quick_chip_set: ["Pavers"],
    },
  });
  const read = await api.counterPolicy();
  assert.equal(read.ok, true);
  assert.equal(read.value.restocking_fee_bps, 250);
  assert.match(calls[0].url, /\/api\/v1\/pos\/policy$/);

  const policy = read.value;
  const write = await api.putCounterPolicy({ ...policy, restocking_fee_bps: 300 });
  assert.equal(write.ok, true);
  assert.equal(calls[1].init.method, "PUT");
  const sent = JSON.parse(calls[1].init.body);
  assert.equal(sent.restocking_fee_bps, 300);
});

test("the counter policy history answers the page", async () => {
  const { api } = apiAnswering({
    body: { items: [{ field: "restocking_fee_bps", before: "250", after: "300", who: "u1", when: new Date(Date.now() - 86400000).toISOString() }], next_cursor: null, limit: 50 },
  });
  const result = await api.counterPolicyHistory();
  assert.equal(result.ok, true);
  assert.equal(result.value.length, 1);
  assert.equal(result.value[0].field, "restocking_fee_bps");
});

test("the walkthrough read projects the chapters and the server's own can_edit", async () => {
  const { api, calls } = apiAnswering({
    body: { chapters: [{ chapter_key: "counter", title: "At the counter", steps: [{ step_key: "one", line: "Start here." }] }], can_edit: false },
  });
  const result = await api.tour();
  assert.equal(result.ok, true);
  assert.equal(result.value.can_edit, false);
  assert.equal(result.value.chapters[0].chapter_key, "counter");
  assert.match(calls[0].url, /\/api\/v1\/tour$/);
});

test("the walkthrough's demo switch and step copy ride the platform routes", async () => {
  const { api, calls } = apiAnswering({ body: { saved: true } });
  assert.equal((await api.putTourPolicy("", true)).ok, true);
  assert.equal(calls[0].init.method, "PUT");
  assert.match(calls[0].url, /\/api\/v1\/platform\/tour\/policy$/);
  assert.deepEqual(JSON.parse(calls[0].init.body), { branch_id: "", demo_mode: true });

  assert.equal(
    (await api.putTourStepCopy({ chapter_key: "counter", step_key: "one", line: "Start here.", route: "", anchor: "" })).ok,
    true,
  );
  assert.match(calls[1].url, /\/api\/v1\/platform\/tour\/steps$/);
  assert.deepEqual(JSON.parse(calls[1].init.body), { chapter_key: "counter", step_key: "one", line: "Start here.", route: "", anchor: "" });
});

test("the brand half rides the tenant brand route with the dealer's org", async () => {
  const { api, calls } = apiAnswering({
    body: { tenant_id: "hh-demo", display_name: "Gable Landscape Supply", footer_text: "", logo_attachment_id: null, primary_color: null },
  });
  const read = await api.brand("hh-demo");
  assert.equal(read.ok, true);
  assert.equal(read.value.display_name, "Gable Landscape Supply");
  assert.match(calls[0].url, /\/api\/v1\/tenants\/hh-demo\/brand$/);
  assert.equal((await api.putBrand("hh-demo", read.value)).ok, true);
  assert.equal(calls[1].init.method, "PUT");
  assert.match(calls[1].url, /\/api\/v1\/tenants\/hh-demo\/brand$/);
});
