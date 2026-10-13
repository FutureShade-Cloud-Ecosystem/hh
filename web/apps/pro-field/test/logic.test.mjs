// The order tracking projection and the section gate, as pure logic: what a
// person reads off an order without the network. The timeline is derived from
// the contract's state map (the kit's board.ts) and is silent where the
// contract is silent, exactly as the kit is; these cases pin that silence.
import { test } from "node:test";
import assert from "node:assert/strict";

import { flowFor, timelineSteps, promisedWords, lateWords, houseToday, fulfillmentWords, instructionsEditable, instructionsNote } from "../src/timeline.ts";
import { sectionsFor, ordersSurfaceOf } from "../src/sections.ts";
import { RETURN_STATUS_WORDS, RETURN_REASON_WORDS, returnChipTone, returnLineWords, returnRequestLines } from "../src/returns.ts";

function order(status, fulfillment, extra = {}) {
  return { status, fulfillment, requested_date: "2042-05-01", ...extra };
}

test("a delivery order walks the delivery flow, a pickup order the will-call one", () => {
  assert.deepEqual(flowFor("DELIVERY"), ["submitted", "confirmed", "picking", "out-for-delivery", "delivered"]);
  assert.deepEqual(flowFor("PICKUP"), ["submitted", "confirmed", "picking", "ready-willcall", "delivered"]);
  assert.deepEqual(flowFor("WILL_CALL"), ["submitted", "confirmed", "picking", "ready-willcall", "delivered"]);
});

test("each step of the journey reads a word, never the stored state string", () => {
  const words = timelineSteps(order("PICKING", "DELIVERY")).map((s) => s.words);
  assert.deepEqual(words, ["Submitted", "Confirmed", "Picking", "Out for delivery", "Delivered"]);
  const text = JSON.stringify(timelineSteps(order("PICKING", "DELIVERY")));
  assert.doesNotMatch(text, /PICKING|OUT_FOR/);
});

test("the order's own state is the one current step; what came before is done, the rest upcoming", () => {
  const steps = timelineSteps(order("PICKING", "DELIVERY"));
  assert.deepEqual(steps.map((s) => s.state), ["done", "done", "current", "upcoming", "upcoming"]);

  const confirmed = timelineSteps(order("CONFIRMED", "DELIVERY"));
  assert.deepEqual(confirmed.map((s) => s.state), ["done", "current", "upcoming", "upcoming", "upcoming"]);

  const ready = timelineSteps(order("READY", "WILL_CALL"));
  assert.deepEqual(ready.map((s) => s.state), ["done", "done", "done", "current", "upcoming"]);

  const fulfilled = timelineSteps(order("FULFILLED", "DELIVERY"));
  assert.deepEqual(fulfilled.map((s) => s.state), ["done", "done", "done", "done", "current"]);
});

test("an invoiced or closed order has walked the whole journey with nothing left current", () => {
  for (const status of ["INVOICED", "CLOSED"]) {
    const steps = timelineSteps(order(status, "DELIVERY"));
    assert.deepEqual(steps.map((s) => s.state), ["done", "done", "done", "done", "done"], status);
  }
});

test("a ready delivery holds at the yard: the road step is a stop's to say, so nothing is current", () => {
  const steps = timelineSteps(order("READY", "DELIVERY"));
  assert.deepEqual(steps.map((s) => s.state), ["done", "done", "done", "upcoming", "upcoming"]);
});

test("a part delivered order has had a truck go out, and nothing is current about the rest", () => {
  const steps = timelineSteps(order("PARTIALLY_FULFILLED", "DELIVERY"));
  assert.deepEqual(steps.map((s) => s.state), ["done", "done", "done", "done", "upcoming"]);
});

test("a cancelled order stops rather than promising steps", () => {
  const steps = timelineSteps(order("CANCELLED", "DELIVERY"));
  assert.ok(steps.every((s) => s.state === "upcoming"));
});

test("the promised line reads the asked-for day, and says ready-by for a pickup", () => {
  assert.deepEqual(promisedWords(order("PICKING", "DELIVERY")), { label: "Promised for", day: "May 1" });
  assert.deepEqual(promisedWords(order("READY", "WILL_CALL")), { label: "Ready by", day: "May 1" });
  assert.equal(promisedWords({ status: "PICKING", fulfillment: "DELIVERY", requested_date: "" }).day, "n/a");
});

test("a live order past its asked-for day reads as late, a finished one does not", () => {
  const today = "2042-05-09";
  assert.ok(lateWords(order("PICKING", "DELIVERY"), today).includes("Past the requested day"));
  assert.ok(lateWords(order("READY", "WILL_CALL"), today).includes("not collected yet"));
  assert.equal(lateWords(order("FULFILLED", "DELIVERY"), today), "");
  assert.equal(lateWords(order("CANCELLED", "DELIVERY"), today), "");
  assert.equal(lateWords(order("PICKING", "DELIVERY"), "2042-04-30"), "");
  assert.equal(lateWords(order("DRAFT", "DELIVERY"), today), "");
});

test("the dealer's today comes from the clock in the dealer's own zone, as a bare day", () => {
  const today = houseToday(new Date("2042-05-09T02:30:00.000Z"));
  assert.match(today, /^\d{4}-\d{2}-\d{2}$/);
  // 02:30 UTC is the previous afternoon in San Diego, so the dealer's day is not the UTC day.
  assert.notEqual(today, "2042-05-09");
});

test("the fulfillment reads as the words a person uses, never the stored string", () => {
  assert.equal(fulfillmentWords("DELIVERY"), "Delivery");
  assert.equal(fulfillmentWords("PICKUP"), "Pickup");
  assert.equal(fulfillmentWords("WILL_CALL"), "Will call");
  assert.equal(fulfillmentWords("SOMETHING_ELSE"), "");
});

test("the sections a role sees: orders and account for everyone, projects and team by capability", () => {
  const crew = sectionsFor((c) => c === "view_orders_deliveries" || c === "edit_delivery_instructions");
  assert.deepEqual(crew.map((s) => s.id), ["orders", "account"]);

  const owner = sectionsFor(() => true);
  assert.deepEqual(owner.map((s) => s.id), ["orders", "projects", "team", "account"]);

  const ap = sectionsFor((c) => ["view_billing", "view_orders_deliveries", "manage_payment_methods"].includes(c));
  assert.deepEqual(ap.map((s) => s.id), ["orders", "projects", "account"]);

  for (const section of sectionsFor(() => true)) assert.doesNotMatch(section.label, /_/);
});

test("the catalogue's field surfaces are the order filters this app serves", () => {
  assert.equal(ordersSurfaceOf("tracking").fulfillments, null);
  assert.deepEqual(ordersSurfaceOf("deliveries").fulfillments, ["DELIVERY"]);
  assert.deepEqual(ordersSurfaceOf("pickups").fulfillments, ["PICKUP", "WILL_CALL"]);
  assert.equal(ordersSurfaceOf("no-such-surface"), undefined);
});

test("delivery instructions are editable on a live delivery, and read only once the truck has gone", () => {
  assert.equal(instructionsEditable(order("PICKING", "DELIVERY"), true), true);
  assert.equal(instructionsEditable(order("PARTIALLY_FULFILLED", "DELIVERY"), true), true);
  assert.equal(instructionsEditable(order("CONFIRMED", "DELIVERY"), true), true);
  assert.equal(instructionsEditable(order("FULFILLED", "DELIVERY"), true), false);
  assert.equal(instructionsEditable(order("INVOICED", "DELIVERY"), true), false);
  assert.equal(instructionsEditable(order("CANCELLED", "DELIVERY"), true), false);
  assert.equal(instructionsEditable(order("READY", "WILL_CALL"), true), false);
  assert.equal(instructionsEditable(order("PICKING", "DELIVERY"), false), false);
});

test("a locked order says why its notes are read only, and says so differently from a role refusal", () => {
  assert.equal(instructionsNote(order("FULFILLED", "DELIVERY"), true), "The truck already left with these notes.");
  assert.equal(instructionsNote(order("READY", "WILL_CALL"), true), "This order is not a delivery, so its notes are set by the dealer.");
  assert.equal(instructionsNote(order("PICKING", "DELIVERY"), false), "Your role reads these notes; changing them is another role's to do.");
});

test("return statuses and reasons read as words, never stored strings", () => {
  for (const status of ["PENDING", "APPROVED", "REJECTED"]) {
    assert.ok(RETURN_STATUS_WORDS[status].length > 0, status);
    assert.doesNotMatch(RETURN_STATUS_WORDS[status], /_/);
  }
  for (const reason of ["DAMAGED", "DEFECT", "WRONG_ITEM", "NO_LONGER_NEEDED", "OTHER"]) {
    assert.ok(RETURN_REASON_WORDS[reason].length > 0, reason);
    assert.doesNotMatch(RETURN_REASON_WORDS[reason], /_/);
  }
  assert.equal(returnChipTone("PENDING"), "watch");
  assert.equal(returnChipTone("APPROVED"), "good");
  assert.equal(returnChipTone("REJECTED"), "stop");
});

test("an eligible line reads sold and returned as words a person checks", () => {
  const words = returnLineWords({ order_line_id: "l1", product_id: "p1", description: "Sandstone paver 6 x 9", qty_sold: 420, qty_returned: 10, unit_price_cents: 512, line_total_cents: 100, tax_cents: 0 });
  assert.ok(words.includes("Sandstone paver 6 x 9"));
  assert.ok(words.includes("410"), words);
  assert.doesNotMatch(words, /qty_|undefined|null|NaN/);
});

test("a return request is built from the typed quantities, or refused in plain words", () => {
  const lines = [
    { order_line_id: "l1", product_id: "p1", description: "Paver", qty_sold: 420, qty_returned: 0, unit_price_cents: 1, line_total_cents: 1, tax_cents: 0 },
    { order_line_id: "l2", product_id: "p2", description: "Edging", qty_sold: 160, qty_returned: 0, unit_price_cents: 1, line_total_cents: 1, tax_cents: 0 },
  ];
  const typed = new Map([["l1", "12"]]);
  const built = returnRequestLines(typed, lines);
  assert.deepEqual(built, { ok: true, lines: [{ order_line_id: "l1", qty_base: 12 }] });

  const nothing = returnRequestLines(new Map(), lines);
  assert.equal(nothing.ok, false);
  assert.ok(nothing.words.includes("Say how many"));

  const over = returnRequestLines(new Map([["l1", "421"]]), lines);
  assert.equal(over.ok, false);
  assert.match(over.words, /no more than 420/i);

  const junk = returnRequestLines(new Map([["l1", "1.5 pizzas"]]), lines);
  assert.equal(junk.ok, false);
  assert.ok(junk.words.includes("whole number"));
});
