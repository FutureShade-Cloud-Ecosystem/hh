// The bills area's plain logic: the words for the match states and what each line's variance
// means (the server's figures, said; nothing is recomputed), the check made before a bill is
// entered (amounts through the decimal string, quantities as decimals, dates as days), the confirm
// steps for posting a bill and for accepting a variance, and the match rules a super admin sets.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  acceptWords,
  billBody,
  billRows,
  billStatusTone,
  billStatusWord,
  canWriteBills,
  matchTone,
  matchWord,
  policyBody,
  postConfirmWords,
  prefillCost,
  prefillQty,
  priceVarianceWords,
  qtyVarianceWords,
  settleBill,
  toleranceBody,
  toleranceWords,
  varianceRefusal,
} from "../src/bills/bills-logic.ts";
import { refusalWords } from "../src/po/po-logic.ts";

const PO = {
  id: "c1",
  po_no: "PO-TRT-67",
  currency: "CAD",
  lines: [
    { id: "L1", sku: "UNI-WALL-CAP", description: "Unilock Wall Cap", qty_ordered_base: 10, qty_received_base: 6, qty_billed_base: 2, unit_cost_cents: 24000, uom: "pc" },
    { id: "L2", sku: "X", description: "Nothing left to bill", qty_ordered_base: 4, qty_received_base: 0, qty_billed_base: 0, unit_cost_cents: 1000, uom: "pc" },
  ],
};

const form = (over = {}) => ({
  billNo: "INV-7790",
  billDate: "",
  dueDate: "",
  tax: "",
  currency: "CAD",
  fx: "",
  lines: { L1: { qty: "4", cost: "240.00" } },
  ...over,
});

test("the list the core answers as null reads as no bills", () => {
  assert.deepEqual(billRows({ items: null }), []);
  assert.deepEqual(billRows({ items: [{ id: "1" }] }), [{ id: "1" }]);
});

test("match states and bill states read in words, never the wire code, and colour never stands alone", () => {
  assert.equal(matchWord("MATCHED"), "Matched");
  assert.equal(matchWord("VARIANCE"), "Variance");
  assert.equal(matchWord("SHORT_BILLED"), "Short billed");
  assert.equal(matchWord("UNMATCHED"), "Unmatched");
  assert.equal(matchTone("MATCHED"), "good");
  assert.equal(matchTone("VARIANCE"), "stop");
  assert.equal(matchTone("SHORT_BILLED"), "watch");
  assert.equal(matchTone("UNMATCHED"), "plain");
  assert.equal(billStatusWord("DRAFT"), "Draft");
  assert.equal(billStatusWord("POSTED"), "Posted");
  assert.equal(billStatusWord("VOID"), "Void");
  assert.equal(billStatusTone("POSTED"), "good");
  assert.equal(billStatusTone("VOID"), "stop");
});

test("a line's variance is said from the server's figures: billed ahead of receipt, received not yet billed, over the price", () => {
  // A piece is the default unit and is written bare; any other unit stands beside the figure.
  assert.equal(qtyVarianceWords(4, "pc"), "4 billed that have not arrived");
  assert.equal(qtyVarianceWords(-2, "T"), "2 T received and not yet billed");
  assert.equal(qtyVarianceWords(0, "pc"), "Billed quantity matches what arrived");
  assert.equal(qtyVarianceWords(undefined, "pc"), "(unknown)");
  assert.equal(priceVarianceWords(2000), "$20.00 over the order's price");
  assert.equal(priceVarianceWords(-150), "$1.50 under the order's price");
  assert.equal(priceVarianceWords(0), "Price matches the order");
  assert.equal(priceVarianceWords(undefined), "(no figure)");
});

test("only the money seats see the bill controls; the wall stays the authority", () => {
  assert.equal(canWriteBills("payables_receivables"), true);
  assert.equal(canWriteBills("financial_controller"), true);
  assert.equal(canWriteBills("general_manager"), true);
  assert.equal(canWriteBills("super_admin"), true);
  assert.equal(canWriteBills("procurement_manager"), false);
  assert.equal(canWriteBills("yard_manager"), false);
  assert.equal(canWriteBills(""), false);
});

test("a bill form starts from the order: what has arrived and not been billed, at the order's price", () => {
  assert.equal(prefillQty(PO.lines[0]), "4");
  assert.equal(prefillQty(PO.lines[1]), "");
  assert.equal(prefillQty({ qty_received_base: 6.25, qty_billed_base: 2.1 }), "4.15");
  assert.equal(prefillCost(PO.lines[0]), "240.00");
  assert.equal(prefillCost({}), "");
});

test("a bill is built from the lines that were written, amounts as exact cents", () => {
  const made = billBody(form({ tax: "31.2", billDate: "2031-03-04", dueDate: "2031-04-03" }), PO);
  assert.deepEqual(made, {
    ok: true,
    body: {
      bill_no: "INV-7790",
      po_id: "c1",
      bill_date: "2031-03-04",
      due_date: "2031-04-03",
      tax_cents: 3120,
      currency: "CAD",
      lines: [{ po_line_id: "L1", description: "Unilock Wall Cap", qty_billed_base: 4, unit_cost_cents: 24000 }],
    },
  });
  const plain = billBody(form(), PO);
  assert.equal(plain.body.tax_cents, undefined);
  assert.equal(plain.body.bill_date, undefined);
  assert.equal(plain.body.due_date, undefined);
});

test("a USD bill carries its own rate, which is the vendor's and not the order's", () => {
  const usd = billBody(form({ currency: "USD", fx: "1.352" }), PO);
  assert.equal(usd.body.currency, "USD");
  assert.equal(usd.body.fx_rate, 1.352);
  assert.match(billBody(form({ currency: "USD", fx: "" }), PO).words, /exchange rate/);
});

test("a bill that cannot be right is refused in words before anything is sent", () => {
  assert.match(billBody(form({ billNo: "  " }), PO).words, /invoice number/);
  assert.match(billBody(form(), null).words, /Pick the purchase order/);
  assert.match(billBody(form({ lines: {} }), PO).words, /at least one line/);
  assert.match(billBody(form({ lines: { L1: { qty: "0", cost: "1" } } }), PO).words, /how many UNI-WALL-CAP were billed/);
  assert.match(billBody(form({ lines: { L1: { qty: "x", cost: "1" } } }), PO).words, /how many UNI-WALL-CAP were billed/);
  assert.match(billBody(form({ lines: { L1: { qty: "4", cost: "" } } }), PO).words, /unit price of UNI-WALL-CAP as dollars and cents/);
  assert.match(billBody(form({ lines: { L1: { qty: "4", cost: "1.234" } } }), PO).words, /unit price of UNI-WALL-CAP as dollars and cents/);
  assert.match(billBody(form({ tax: "-5" }), PO).words, /tax as dollars and cents/);
  assert.match(billBody(form({ billDate: "03/04/2031" }), PO).words, /bill date as year, month and day/);
  assert.match(billBody(form({ dueDate: "2031-02-30" }), PO).words, /due date is not a real date/);
});

test("the post step names the bill, the vendor, the order and the server's total", () => {
  const words = postConfirmWords({ bill_no: "INV-7790", vendor_name: "Unilock (demo)", po_no: "PO-TRT-67", total_cents: 96000, match_state: "MATCHED" });
  assert.match(words, /Post bill INV-7790 from Unilock \(demo\) for \$960\.00 against PO-TRT-67/);
  assert.match(words, /books the amount owed/i);
  assert.match(words, /cannot be edited after/i);
  assert.match(postConfirmWords({ bill_no: "A", vendor_name: "V", po_no: "P", total_cents: undefined, match_state: "MATCHED" }), /\(no figure\)/);
});

test("the core's variance refusal is recognised and its figure read, then the acceptance is asked for in words", () => {
  const core = "bill INV-7790 is in VARIANCE (2000 cents against the purchase order), post it with accept_variance to record who accepted it";
  assert.equal(varianceRefusal(core), 2000);
  assert.equal(varianceRefusal("bill INV-7790 is in VARIANCE (-150 cents against the purchase order), post it with accept_variance to record who accepted it"), -150);
  assert.equal(varianceRefusal("bill is already POSTED"), null);
  const words = acceptWords({ bill_no: "INV-7790" }, 2000);
  assert.match(words, /Bill INV-7790 is outside the match tolerance: the core puts the difference at \$20\.00/);
  assert.match(words, /records you as the person who accepted it/);
  assert.match(acceptWords({ bill_no: "INV-7790" }, -150), /\$1\.50/);
});

test("a post is settled against the bill as the core now holds it", () => {
  assert.equal(settleBill("POSTED", "DRAFT"), null);
  assert.match(settleBill("POSTED", "POSTED") ?? "", /already posted/i);
  assert.match(settleBill("DRAFT", "DRAFT") ?? "", /still a draft/i);
  assert.match(settleBill("VOID", "DRAFT") ?? "", /void/i);
});

test("the match rules are checked as the core checks them, and read back in words", () => {
  assert.deepEqual(toleranceBody({ qty: "2", price: "1", abs: "0.50" }, ""), { ok: true, body: { qty_tolerance_pct: 2, price_tolerance_pct: 1, price_tolerance_abs_cents: 50 } });
  assert.deepEqual(toleranceBody({ qty: "0", price: "0.5", abs: "" }, "b1").body, { qty_tolerance_pct: 0, price_tolerance_pct: 0.5, price_tolerance_abs_cents: 0, branch_id: "b1" });
  assert.match(toleranceBody({ qty: "26", price: "1", abs: "" }, "").words, /above 25% is not a tolerance, it is an off switch/);
  assert.match(toleranceBody({ qty: "2", price: "25.5", abs: "" }, "").words, /above 25%/);
  assert.match(toleranceBody({ qty: "-1", price: "1", abs: "" }, "").words, /percentage/);
  assert.match(toleranceBody({ qty: "x", price: "1", abs: "" }, "").words, /percentage/);
  assert.match(toleranceBody({ qty: "2", price: "1", abs: "1.234" }, "").words, /dollars and cents/);
  assert.equal(
    toleranceWords({ qty_tolerance_pct: 2, price_tolerance_pct: 1, price_tolerance_abs_cents: 50, is_branch_override: false, resolved_from_default: true }),
    "A line matches when its quantity is within 2% of what was received and its price within 1% of the order's price or $0.50, whichever allows more. This is the dealer default.",
  );
  assert.match(
    toleranceWords({ qty_tolerance_pct: 0, price_tolerance_pct: 0, price_tolerance_abs_cents: 0, is_branch_override: true, resolved_from_default: false }),
    /This branch has its own rule\.$/,
  );
});

test("the cost policy is one of three, the untagged fallback one of two", () => {
  assert.deepEqual(policyBody({ visibility: "HIDDEN", fallback: "REFUSE" }, ""), { ok: true, body: { cost_visibility: "HIDDEN", untagged_fallback: "REFUSE" } });
  assert.deepEqual(policyBody({ visibility: "ALL_STAFF", fallback: "CONFIRM" }, "b1").body, { cost_visibility: "ALL_STAFF", untagged_fallback: "CONFIRM", branch_id: "b1" });
  assert.match(policyBody({ visibility: "EVERYONE", fallback: "CONFIRM" }, "").words, /Pick who sees cost/);
});

test("a wall refusal names who posts bills and who sets the rules", () => {
  assert.match(refusalWords("bill", "Forbidden", 403), /Entering and posting a vendor bill is for Payables and Receivables/);
  assert.match(refusalWords("tolerance", "Forbidden", 403), /set by the super admin only/);
  assert.equal(refusalWords("bill", "bill is already POSTED", 409), "Bill is already POSTED.");
});
