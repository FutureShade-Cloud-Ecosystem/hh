// The landed cost slice's plain logic: the words for the states and the bases, the one check made
// before a capture is sent (amounts through the decimal string, the rate, the retro on-hand text),
// the body the core expects, and the words that settle an action against what the core now holds.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  basisFigure,
  basisWord,
  buildCreate,
  confirmWords,
  receivedOrders,
  retroLines,
  settleWords,
  statusTone,
  statusWord,
  retroCandidates,
} from "../src/landed/landed-logic.ts";
import { refusalWords } from "../src/po/po-logic.ts";

const PO = {
  id: "c0000000-0000-4000-8000-000000000066",
  po_no: "PO-PLM-66",
  branch_id: "b0000000-0000-4000-8000-000000000001",
  status: "RECEIVED",
  lines: [
    { id: "L1", product_id: "P1", sku: "ALC-STEP-48", description: "Alcli Step 48in", qty_received_base: 10, uom: "pc" },
    { id: "L2", product_id: "P2", sku: "X", description: "Never received", qty_received_base: 0, uom: "pc" },
  ],
};

const text = (over = {}) => ({
  poNo: "PO-PLM-66",
  currency: "CAD",
  fx: "",
  freight: "480.00",
  duty: "",
  handling: "35.5",
  basis: "VALUE",
  excludeSpecial: true,
  retro: false,
  retroOfId: "",
  onHand: {},
  ...over,
});

test("states and bases read in words, never the wire code", () => {
  assert.equal(statusWord("DRAFT"), "Draft");
  assert.equal(statusWord("POSTED"), "Applied");
  assert.equal(statusWord("RETRO_ADJUSTED"), "Retro adjusted");
  assert.equal(statusWord("VOIDED"), "Voided");
  assert.equal(statusWord("SOMETHING"), "SOMETHING");
  assert.equal(basisWord("VALUE"), "By value");
  assert.equal(basisWord("WEIGHT"), "By weight");
  assert.equal(basisWord("QUANTITY"), "By quantity");
  assert.equal(statusTone("POSTED"), "good");
  assert.equal(statusTone("VOIDED"), "stop");
  assert.equal(statusTone("DRAFT"), "plain");
});

test("only orders with goods received can carry a landed cost", () => {
  const rows = [
    { id: "1", status: "DRAFT" },
    { id: "2", status: "ORDERED" },
    { id: "3", status: "PARTIAL" },
    { id: "4", status: "RECEIVED" },
    { id: "5", status: "CLOSED" },
    { id: "6", status: "CANCELLED" },
  ];
  assert.deepEqual(receivedOrders(rows).map((r) => r.id), ["3", "4", "5"]);
});

test("a capture builds the body the core expects, amounts as exact cents", () => {
  const made = buildCreate(text(), PO);
  assert.equal(made.ok, true);
  assert.deepEqual(made.body, {
    po_id: PO.id,
    po_ref: "PO-PLM-66",
    branch_id: PO.branch_id,
    currency: "CAD",
    freight_cents: 48000,
    duty_cents: 0,
    handling_cents: 3550,
    allocation_basis: "VALUE",
    exclude_special_buys: true,
  });
});

test("a USD capture carries the rate; a CAD capture never does", () => {
  const usd = buildCreate(text({ currency: "USD", fx: "1.352" }), PO);
  assert.equal(usd.ok, true);
  assert.equal(usd.body.currency, "USD");
  assert.equal(usd.body.fx_rate, 1.352);
  const cad = buildCreate(text({ fx: "1.5" }), PO);
  assert.equal(cad.body.fx_rate, undefined);
  const noRate = buildCreate(text({ currency: "USD", fx: "" }), PO);
  assert.equal(noRate.ok, false);
  assert.match(noRate.words, /exchange rate/);
});

test("a capture that cannot be right is refused in words and sends nothing", () => {
  assert.match(buildCreate(text({ poNo: "" }), null).words, /Pick the purchase order/);
  assert.match(buildCreate(text({ freight: "12.345" }), PO).words, /freight as dollars and cents/);
  assert.match(buildCreate(text({ duty: "-5" }), PO).words, /duty as dollars and cents/);
  assert.match(buildCreate(text({ handling: "abc" }), PO).words, /handling as dollars and cents/);
  assert.match(buildCreate(text({ freight: "", duty: "", handling: "" }), PO).words, /at least one of freight, duty or handling/);
  assert.match(buildCreate(text({ freight: "0", duty: "0.00", handling: "" }), PO).words, /at least one of freight, duty or handling/);
});

test("a late freight capture needs the landed cost it follows and a count of what is left on every received line", () => {
  const lines = retroLines(PO);
  assert.deepEqual(lines.map((l) => l.id), ["L1"]);
  const noOriginal = buildCreate(text({ retro: true, retroOfId: "", onHand: { L1: "4" } }), PO);
  assert.match(noOriginal.words, /Pick the landed cost this late freight follows/);
  const noCount = buildCreate(text({ retro: true, retroOfId: "lc-1", onHand: {} }), PO);
  assert.match(noCount.words, /still on hand/);
  const badCount = buildCreate(text({ retro: true, retroOfId: "lc-1", onHand: { L1: "-1" } }), PO);
  assert.match(badCount.words, /still on hand/);
  const ok = buildCreate(text({ retro: true, retroOfId: "lc-1", onHand: { L1: "0" } }), PO);
  assert.equal(ok.ok, true);
  assert.equal(ok.body.retro_of_id, "lc-1");
  assert.deepEqual(ok.body.on_hand, [{ product_id: "P1", po_line_id: "L1", on_hand_base: 0 }]);
});

test("a retro capture can follow only an applied landed cost of the same order", () => {
  const items = [
    { id: "a", po_id: PO.id, status: "POSTED", retro_of_id: undefined },
    { id: "b", po_id: PO.id, status: "DRAFT" },
    { id: "c", po_id: "other", status: "POSTED" },
    { id: "d", po_id: PO.id, status: "VOIDED" },
    { id: "e", po_id: PO.id, status: "POSTED", retro_of_id: "a" },
  ];
  assert.deepEqual(retroCandidates(items, PO.id).map((i) => i.id), ["a"]);
});

test("the confirm step names the order, the basis and the server's own total", () => {
  const apply = confirmWords("apply", { po_ref: "PO-PLM-66", allocation_basis: "WEIGHT", retro_of_id: undefined }, 51550);
  assert.match(apply, /PO-PLM-66/);
  assert.match(apply, /\$515\.50/);
  assert.match(apply, /by weight/i);
  assert.match(apply, /raises the cost of the goods/i);
  const voided = confirmWords("void", { po_ref: "PO-PLM-66", allocation_basis: "VALUE" }, 51550);
  assert.match(voided, /reverses/i);
  const retro = confirmWords("retro", { po_ref: "PO-PLM-66", allocation_basis: "VALUE", retro_of_id: "x" }, 51550);
  assert.match(retro, /sold/i);
  assert.equal(confirmWords("apply", { po_ref: "", allocation_basis: "VALUE" }, undefined).includes("(no figure)"), true);
});

test("an action is settled against what the core now holds, not what its answer said", () => {
  // The core answers an apply of a document that is not a draft with the same body as a real one.
  assert.equal(settleWords("apply", "POSTED", "DRAFT"), null);
  assert.match(settleWords("apply", "POSTED", "POSTED") ?? "", /was already applied/i);
  assert.match(settleWords("apply", "DRAFT", "DRAFT") ?? "", /still a draft/i);
  assert.equal(settleWords("retro", "RETRO_ADJUSTED", "DRAFT"), null);
  assert.equal(settleWords("void", "VOIDED", "POSTED"), null);
  assert.match(settleWords("void", "POSTED", "POSTED") ?? "", /not voided/i);
});

test("a wall refusal names who may capture a landed cost", () => {
  assert.match(refusalWords("landed", "Forbidden", 403), /Procurement Manager/);
  assert.match(refusalWords("landed", "caller cannot access this branch", 403), /^Caller cannot access this branch\.$/);
  assert.match(refusalWords("landed", "landed cost has no po_id to allocate across", 422), /no po_id to allocate across/);
});

test("the weight a share was split by reads in its own unit, never as bare cents", () => {
  assert.equal(basisFigure("VALUE", 25000), "$250.00");
  assert.equal(basisFigure("WEIGHT", 35), "35 kg");
  assert.equal(basisFigure("QUANTITY", 10), "10");
});
