// The procurement slice's plain logic: the words for what the server answered,
// who may do what the role may not, the shapes the core really writes, and the
// quantity and money text. Nothing here computes money; the server decides.
import { test, afterEach } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { houseZone, resetHouseZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.ts";
import { ReceivingApi } from "../src/receiving/receiving-api.ts";
import { startStub } from "./contract-kit.mjs";
import {
  deliveryWord,
  dateWords,
  draftOf,
  houseDay,
  landedWords,
  relativeDay,
  incomingLines,
  isOpen,
  poRows,
  refusalWords,
  shortOrderWords,
  shortfallRows,
  stampDay,
  statusTone,
  statusWord,
  validateExpectedDate,
  whenWords,
  openWarrantyCreditStripText,
  applyWarrantyCreditButtonText,
  appliedWarrantyCreditLabel,
  totalDueVendorLabel,
} from "../src/po/po-logic.ts";
import { costCentsFromTyped, formatMoney, formatQty } from "../src/po/po-money.ts";

afterEach(() => resetHouseZone());

test("the shortfall is read from the contract's key, the one key the core writes", () => {
  const row = { sku: "A" };
  assert.deepEqual(shortfallRows({ items: [row], on_order_degraded: false }), [row]);
  assert.deepEqual(shortfallRows({ items: null, on_order_degraded: false }), []);
  assert.deepEqual(shortfallRows({}), []);
  // The ERP's old key is gone from the core (the contract won): a page that still carries it is not read.
  assert.deepEqual(shortfallRows({ shortfall: [row], on_order_degraded: false }), []);
});

test("an empty PO list the core answers as null reads as no orders", () => {
  assert.deepEqual(poRows({ items: null }), []);
  assert.deepEqual(poRows({ items: [{ id: "1" }] }), [{ id: "1" }]);
});

test("incoming lines are read from the contract's key, the one key the core writes", () => {
  assert.deepEqual(incomingLines({ items: [{ po_no: "Y" }] }), [{ po_no: "Y" }]);
  assert.deepEqual(incomingLines({}), []);
  // The ERP's old key is gone from the core (the contract won): a page that still carries it is not read.
  assert.deepEqual(incomingLines({ lines: [{ po_no: "X" }] }), []);
});

test("order states are words, and RECEIVED, PARTIAL and ORDERED are never buttons", () => {
  assert.equal(statusWord("ORDERED"), "Ordered");
  assert.equal(statusWord("PARTIAL"), "Partial");
  assert.equal(statusWord("RECEIVED"), "Received");
  assert.equal(statusWord("DRAFT"), "Draft");
  assert.equal(statusTone("RECEIVED"), "good");
  assert.equal(statusTone("PARTIAL"), "watch");
  assert.equal(isOpen("DRAFT"), true);
  assert.equal(isOpen("ORDERED"), true);
  assert.equal(isOpen("PARTIAL"), true);
  assert.equal(isOpen("RECEIVED"), false);
  assert.equal(isOpen("CANCELLED"), false);
});

test("delivery words follow the contract's three values", () => {
  assert.equal(deliveryWord("DELIVERY"), "Vendor truck");
  assert.equal(deliveryWord("PICKUP"), "Pickup");
  assert.equal(deliveryWord("UNKNOWN"), "Not set");
  assert.equal(deliveryWord(undefined), "Not set");
});

test("the expected date must be a real date", () => {
  assert.deepEqual(validateExpectedDate(""), { ok: false, words: "Pick the date the vendor promised." });
  assert.equal(validateExpectedDate("2031-02-30").ok, false);
  assert.equal(validateExpectedDate("31/02/2031").ok, false);
  assert.deepEqual(validateExpectedDate("2031-03-04"), { ok: true, date: "2031-03-04" });
});

test("a 403 names who may do the move; the server's own sentence is otherwise kept", () => {
  const wall = refusalWords("order", "Forbidden", 403);
  assert.equal(
    wall,
    "Drafting, placing, editing and cancelling a purchase order is for the Procurement Manager, the General Manager and the super admin only. The Yard Manager receives goods only; other roles can read.",
  );
  const bill = refusalWords("post-bill", "Forbidden", 403);
  assert.match(bill, /Payables and Receivables/);
  assert.match(bill, /does not post them/);
  assert.equal(refusalWords("order", "cannot order an empty purchase order, add at least one line", 409), "Cannot order an empty purchase order, add at least one line.");
  assert.equal(refusalWords("order", "x", undefined), "The server could not be reached. Nothing was changed. Try again.");
  assert.equal(refusalWords("order", "x", 401), "Your sign in has ended. Sign in again.");
  assert.equal(refusalWords("order", "boom", 500), "The server hit a problem. Nothing was changed. Try again.");
});

test("the branch fence's 403 reads in the server's own words, not the role wall's", () => {
  // The core's fenceNamedBranch: a named branch outside the caller's reach is refused in words
  // (reorder_actions.go), unlike the role gate's wordless Forbidden, which the wall words explain.
  assert.equal(refusalWords("order", "you do not have access to that branch", 403), "You do not have access to that branch.");
  assert.equal(refusalWords("load", "you do not have access to that branch", 403), "You do not have access to that branch.");
});

test("money reads as dollars with cents, and a missing figure is a placeholder, never a zero", () => {
  assert.equal(formatMoney(201600), "$2,016.00");
  assert.equal(formatMoney(undefined), "(no figure)");
  assert.equal(formatMoney(null), "(no figure)");
  assert.equal(formatMoney(12.5), "(no figure)");
});

test("typed dollars become cents exactly through the decimal string, never a float multiply", () => {
  // 1.005 is the float edge: Number("1.005") * 100 is 100.49999..., which a Math.round reads as
  // 100 cents, a price the buyer never typed. The split refuses a third decimal in words instead.
  assert.deepEqual(costCentsFromTyped("1.005"), { ok: false, words: "Write the unit cost as dollars and cents, two decimals at most." });
  assert.deepEqual(costCentsFromTyped("0.1"), { ok: true, cents: 10 });
  assert.deepEqual(costCentsFromTyped("19.99"), { ok: true, cents: 1999 });
  assert.deepEqual(costCentsFromTyped("8"), { ok: true, cents: 800 });
  assert.deepEqual(costCentsFromTyped("8.4"), { ok: true, cents: 840 });
  assert.deepEqual(costCentsFromTyped("8.40"), { ok: true, cents: 840 });
  assert.deepEqual(costCentsFromTyped("0.05"), { ok: true, cents: 5 });
  // A negative passes through as negative cents: the core refuses it in its own words, which is
  // the honest answer beside the button, rather than the app inventing a sentence the core lacks.
  assert.deepEqual(costCentsFromTyped("-1.50"), { ok: true, cents: -150 });
  assert.equal(costCentsFromTyped("soon").ok, false);
  assert.equal(costCentsFromTyped(" 1.005 ").ok, false);
});

test("quantities read plainly, with the unit unless it is a piece", () => {
  assert.equal(formatQty(1200, "pc"), "1,200");
  assert.equal(formatQty(40, "T"), "40 T");
  assert.equal(formatQty(-40, "pc"), "-40");
  assert.equal(formatQty(0.5, "T"), "0.5 T");
  assert.equal(formatQty(undefined, "T"), "(unknown)");
});

test("a row carries the draft that does not cover it, or none", () => {
  assert.deepEqual(draftOf({ draft_po_id: "d1", draft_po_no: "PO-PLM-142" }), { id: "d1", no: "PO-PLM-142" });
  assert.equal(draftOf({}), null);
  assert.equal(draftOf({ draft_po_id: "" }), null);
});

test("promised days read against today, and far dates are left to the date", () => {
  assert.equal(relativeDay("2031-03-04", "2031-03-02"), "in 2 days");
  assert.equal(relativeDay("2031-03-03", "2031-03-02"), "tomorrow");
  assert.equal(relativeDay("2031-03-02", "2031-03-02"), "today");
  assert.equal(relativeDay("2031-03-01", "2031-03-02"), "yesterday");
  assert.equal(relativeDay("2031-02-27", "2031-03-02"), "3 days ago");
  assert.equal(relativeDay("2032-03-04", "2031-03-02"), "");
  assert.equal(relativeDay("soon", "2031-03-02"), "");
});

test("a stamp on screen reads as words against today, and a far day falls back to its day", () => {
  assert.equal(whenWords("2031-03-04", "2031-03-02"), "in 2 days");
  assert.equal(whenWords("2031-03-02T14:05:00Z", "2031-03-02"), "today");
  assert.equal(whenWords("2031-03-01T09:00:00Z", "2031-03-02"), "yesterday");
  assert.equal(whenWords("2031-02-20T09:00:00Z", "2031-03-02"), "10 days ago");
  assert.equal(whenWords("2032-03-04", "2031-03-02"), "2032-03-04");
  assert.equal(whenWords("soon", "2031-03-02"), "soon");
  assert.equal(whenWords(undefined, "2031-03-02"), "");
  assert.equal(whenWords("", "2031-03-02"), "");
});

function ymd(y, m, d) {
  return [String(y), String(m).padStart(2, "0"), String(d).padStart(2, "0")].join("-");
}

function withPinnedClock(now, run) {
  const RealDate = globalThis.Date;
  class PinnedDate extends RealDate {
    constructor(...args) {
      if (args.length === 0) super(now.getTime());
      else super(...args);
    }
    static now() {
      return now.getTime();
    }
  }
  globalThis.Date = PinnedDate;
  try {
    return run();
  } finally {
    globalThis.Date = RealDate;
  }
}

test("a receipt at the house evening reads today, never tomorrow", () => {
  const y = 2000 + 31;
  // 20:30 in the house zone on the second day of the third month, already the third in UTC and Tokyo.
  const evening = new Date(Date.UTC(y, 2, 3, 1, 30));
  withPinnedClock(evening, () => {
    const receiptAt = new Date().toISOString();
    assert.equal(houseDay(), ymd(y, 3, 2));
    assert.equal(stampDay(receiptAt), ymd(y, 3, 2));
    assert.equal(whenWords(receiptAt, ymd(y, 3, 2)), "today");
    assert.equal(whenWords(receiptAt), "today");
    assert.equal(relativeDay(receiptAt), "today");
  });
});

test("an expected date of the house tomorrow reads tomorrow and a date-only value is never UTC midnight", () => {
  const y = 2000 + 31;
  const evening = new Date(Date.UTC(y, 2, 3, 1, 30));
  withPinnedClock(evening, () => {
    const houseTomorrow = ymd(y, 3, 3);
    const houseToday = ymd(y, 3, 2);
    assert.equal(stampDay(houseTomorrow), houseTomorrow);
    assert.equal(whenWords(houseTomorrow), "tomorrow");
    assert.equal(relativeDay(houseTomorrow), "tomorrow");
    assert.equal(whenWords(houseToday), "today");
    assert.equal(relativeDay(houseToday), "today");
    assert.equal(dateWords(houseTomorrow).endsWith("(tomorrow)"), true);
  });
});

test("a receipt at a Pacific evening with the stub serving America/Los_Angeles reads today, never tomorrow", async () => {
  const y = 2000 + 31;
  // 21:30 in America/Los_Angeles on the second of the seventh: already 00:30 on the third in Toronto.
  const pacificEvening = new Date(Date.UTC(y, 6, 3, 4, 30));
  const pacificToday = ymd(y, 7, 2);
  const stub = await startStub();
  try {
    await stub.post("/_stub/reset", { now: pacificEvening.getTime(), business_zone: "America/Los_Angeles" });
    const client = new ApiClient({
      baseUrl: stub.base,
      headers: () => ({ Authorization: "Bearer stub-token", "X-Org": "hh-demo" }),
    });
    const receiving = new ReceivingApi(client);
    await receiving.branches();
    const worklist = await receiving.worklist();
    assert.equal(worklist.ok, true);
    const target = worklist.value.items.find((i) => i.po_no === "PO-PLM-136");
    const detail = await receiving.order(target.po_id);
    assert.equal(detail.ok, true);
    const received = await receiving.receive(target.po_id, {
      lines: [{ po_line_id: detail.value.lines[0].id, qty_base: 2 }],
    });
    assert.equal(received.ok, true);
    withPinnedClock(pacificEvening, () => {
      assert.equal(whenWords(received.value.received_at, pacificToday), "today");
      assert.equal(houseZone(), "America/Los_Angeles");
      assert.equal(houseDay(), pacificToday);
      assert.equal(stampDay(received.value.received_at), pacificToday);
      assert.equal(whenWords(received.value.received_at), "today");
    });
  } finally {
    stub.stop();
  }
});

test("what has landed reads as a count against what was ordered", () => {
  assert.equal(landedWords([{ qty_ordered_base: 10, qty_received_base: 6, uom: "pc" }]), "6 of 10 pc landed");
  assert.equal(landedWords([{ qty_ordered_base: 4, qty_received_base: 1, uom: "pc" }, { qty_ordered_base: 6, qty_received_base: 5, uom: "pc" }]), "6 of 10 pc landed");
  assert.equal(landedWords([{ qty_ordered_base: 4, qty_received_base: 1, uom: "pc" }, { qty_ordered_base: 6, qty_received_base: 5, uom: "T" }]), "part landed");
  assert.equal(landedWords([]), "");
});

test("the promised date reads as words with its days from today", () => {
  assert.equal(dateWords("2031-03-04", "2031-02-27"), "Tuesday 4 March 2031 (in 5 days)");
  assert.equal(dateWords("2031-03-04", "2025-01-01"), "Tuesday 4 March 2031");
  assert.equal(dateWords("2031-02-30", "2031-02-27"), "");
  assert.equal(dateWords("", "2031-02-27"), "");
});

test("open warranty credit strip text names amount, claim number and description", () => {
  assert.equal(
    openWarrantyCreditStripText({
      claim_no: "WCL-104",
      amount_cents: 26400,
      description: "8 pieces of Paver 60mm Slate",
    }),
    "Open warranty credit · $264.00 from WCL-104 (8 pieces of Paver 60mm Slate)",
  );
  assert.equal(
    openWarrantyCreditStripText({
      claim_no: "WCL-104",
      amount_cents: 26400,
    }),
    "Open warranty credit · $264.00 from WCL-104",
  );
  assert.equal(
    openWarrantyCreditStripText({
      amount_cents: 26400,
    }),
    "Open warranty credit · $264.00",
  );
});

test("apply warranty credit button text formats the amount to apply", () => {
  assert.equal(applyWarrantyCreditButtonText(26400), "Apply $264.00 to this PO");
  assert.equal(applyWarrantyCreditButtonText(null), "Apply (no figure) to this PO");
});

test("applied warranty credit label names the claim number when present", () => {
  assert.equal(appliedWarrantyCreditLabel("WCL-104"), "Warranty credit applied · WCL-104");
  assert.equal(appliedWarrantyCreditLabel(null), "Warranty credit applied");
  assert.equal(appliedWarrantyCreditLabel(undefined), "Warranty credit applied");
});

test("total due vendor label names the vendor when present", () => {
  assert.equal(totalDueVendorLabel("Alcli (demo)"), "Total due to Alcli (demo)");
  assert.equal(totalDueVendorLabel(null), "Total due to vendor");
  assert.equal(totalDueVendorLabel(undefined), "Total due to vendor");
});

test("the sales orders a shortfall holds up read as one reason: the oldest first, the rest as a count", () => {
  // The ruling on the board's "Short for SO-n" tag (change 5, R7): the orders are named
  // oldest first, and what the row carries past its names is a count, never a list cut short.
  assert.equal(shortOrderWords({ short_order_nos: ["SO-3314"], short_order_more: 0 }), "Short for SO-3314");
  assert.equal(shortOrderWords({ short_order_nos: ["SO-3305", "SO-3311"], short_order_more: 0 }), "Short for SO-3305 and SO-3311");
  assert.equal(shortOrderWords({ short_order_nos: ["SO-3305", "SO-3311", "SO-3312"], short_order_more: 0 }), "Short for SO-3305, SO-3311 and SO-3312");
  // The core names at most three and counts the rest: the row's own shape.
  assert.equal(shortOrderWords({ short_order_nos: ["SO-3305", "SO-3311", "SO-3312"], short_order_more: 2 }), "Short for SO-3305, SO-3311, SO-3312 and 2 more");
  assert.equal(shortOrderWords({ short_order_nos: ["SO-3305"], short_order_more: 4 }), "Short for SO-3305 and 4 more");
  // Nothing waits, or the orders seam is unbound: neither key, no tag.
  assert.equal(shortOrderWords({}), "");
  assert.equal(shortOrderWords({ short_order_nos: [], short_order_more: 0 }), "");
});
