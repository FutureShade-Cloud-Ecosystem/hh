// The order tracking projection and the section gate, as pure logic: what a
// person reads off an order without the network. The timeline is derived from
// the contract's state map (the kit's board.ts) and is silent where the
// contract is silent, exactly as the kit is; these cases pin that silence.
import { test } from "node:test";
import assert from "node:assert/strict";

import { flowFor, timelineSteps, promisedWords, lateWords, houseToday, fulfillmentWords } from "../src/timeline.ts";
import { sectionsFor, ordersSurfaceOf } from "../src/sections.ts";

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
