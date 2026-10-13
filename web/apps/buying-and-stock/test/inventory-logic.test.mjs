// The inventory slice's plain logic: the words the surfaces say, the rules the
// buttons follow, and the check made before a request is sent. Nothing here
// computes money; the server decides every figure and every refusal.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  configWord,
  defaultShipETA,
  holdModeWord,
  inTransitBase,
  moveDirectionWord,
  moveTypeWord,
  qtyFromTyped,
  refusalWords,
  reservationStatusWord,
  saleTypeWord,
  transferActions,
  transferStatusWord,
  transitDaysFor,
} from "../src/inventory/inventory-logic.ts";

test("the ledger's move types and directions are said, not shouted", () => {
  assert.equal(moveTypeWord("RECEIPT"), "Receipt");
  assert.equal(moveTypeWord("SHIP"), "Shipment");
  assert.equal(moveTypeWord("BREAK_PALLET"), "Pallet break");
  assert.equal(moveTypeWord("COUNT"), "Count");
  assert.equal(moveDirectionWord("IN"), "In");
  assert.equal(moveDirectionWord("OUT"), "Out");
  // A ninth move type degrades honestly: strange, never confident.
  assert.equal(moveTypeWord("REGRAM"), "regram");
  assert.equal(moveDirectionWord("SIDEWAYS"), "sideways");
});

test("a transfer's buttons follow its status, and a shipped transfer offers no cancel", () => {
  assert.deepEqual(transferActions("DRAFT"), { ship: true, receive: false, cancel: true });
  assert.deepEqual(transferActions("IN_TRANSIT"), { ship: false, receive: true, cancel: false });
  assert.deepEqual(transferActions("RECEIVED"), { ship: false, receive: false, cancel: false });
  assert.deepEqual(transferActions("CANCELLED"), { ship: false, receive: false, cancel: false });
  assert.equal(transferStatusWord("IN_TRANSIT"), "In transit");
  assert.equal(transferStatusWord("ARRIVING"), "arriving");
});

test("a transfer line's units still on the truck are the shipped less the received", () => {
  assert.equal(inTransitBase({ qty_shipped_base: 40, qty_received_base: 0 }), 40);
  assert.equal(inTransitBase({ qty_shipped_base: 40, qty_received_base: 40 }), 0);
});

test("an unconfigured route promises nothing: null, never zero days", () => {
  const pairs = [
    { from_branch_id: "a", to_branch_id: "b", transit_days: 1, configured: true },
    { from_branch_id: "a", to_branch_id: "c", transit_days: 0, configured: false },
  ];
  assert.equal(transitDaysFor(pairs, "a", "b"), 1);
  assert.equal(transitDaysFor(pairs, "a", "c"), null);
  assert.equal(transitDaysFor(pairs, "a", "zz"), null);
  assert.equal(configWord(pairs[0]), "1 day");
  assert.equal(configWord({ ...pairs[0], transit_days: 3 }), "3 days");
  assert.equal(configWord(pairs[1]), "not set");
});

test("the ship pre-fill is the ship day plus the route's days, and an unconfigured route pre-fills nothing", () => {
  const pairs = [{ from_branch_id: "a", to_branch_id: "b", transit_days: 4, configured: true }];
  assert.equal(defaultShipETA(pairs, "a", "b", "2031-03-01"), "2031-03-05");
  assert.equal(defaultShipETA(pairs, "a", "c", "2031-03-01"), "");
  assert.equal(defaultShipETA([{ from_branch_id: "a", to_branch_id: "b", transit_days: 0, configured: false }], "a", "b", "2031-03-01"), "");
});

test("holds and holds and sales read as words", () => {
  assert.equal(holdModeWord("HARD"), "Hard hold");
  assert.equal(holdModeWord("SOFT"), "Soft hold");
  assert.equal(holdModeWord("NONE"), "No hold");
  assert.equal(reservationStatusWord("ACTIVE"), "Active");
  assert.equal(reservationStatusWord("EXPIRED"), "Expired");
  assert.equal(saleTypeWord("COLLECT_LATER"), "Collect later");
  assert.equal(saleTypeWord("DELIVERED"), "Delivered");
});

test("a counted quantity is read through its decimal string, junk is refused in words", () => {
  assert.deepEqual(qtyFromTyped("12"), { ok: true, qty: 12 });
  assert.deepEqual(qtyFromTyped("12.5"), { ok: true, qty: 12.5 });
  assert.deepEqual(qtyFromTyped("-3"), { ok: true, qty: -3 });
  assert.equal(qtyFromTyped("").ok, false);
  assert.equal(qtyFromTyped("abc").ok, false);
  assert.equal(qtyFromTyped("1.2.3").ok, false);
});

test("a walled move is named for who may do it, and the branch fence's own sentence is kept", () => {
  assert.equal(
    refusalWords("write", "Forbidden", 403),
    "Writing here is for the seats the role matrix holds at work or manage: the Procurement Manager and the Yard Manager among the demo roles. A read-only role can look but not touch.",
  );
  assert.equal(
    refusalWords("manage", "Forbidden", 403),
    "Moving stock between branches, holding it, and setting points and policy are manage seats' moves on this surface: the Procurement Manager's among the demo roles.",
  );
  assert.equal(
    refusalWords("receive", "Forbidden", 403),
    "Receiving an order into stock is a work move: the Yard Manager's and the Procurement Manager's among the demo roles.",
  );
  assert.equal(
    refusalWords("count-approve", "Forbidden", 403),
    "Approving a count and posting its variance are manager seats' moves: the Procurement Manager's among the demo roles. The maker never approves their own count.",
  );
  assert.equal(
    refusalWords("transit-days", "Forbidden", 403),
    "Setting a route's transit time is the tech admin's; no demo role on this box holds it.",
  );
  // The branch fence's 403 carries its own sentence, which is kept, capitalised and ended.
  assert.equal(refusalWords("reserve", "caller cannot access this branch", 403), "Caller cannot access this branch.");
  assert.equal(refusalWords("reserve", "The server fell over", 500), "The server hit a problem. Nothing was changed. Try again.");
  assert.equal(refusalWords("reserve", "", undefined), "The server could not be reached. Nothing was changed. Try again.");
});
