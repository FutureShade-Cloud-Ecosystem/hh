// The inventory slice's plain logic: the words the surfaces say, the rules the
// buttons follow, and the check made before a request is sent. Nothing here
// computes money; the server decides every figure and every refusal.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  configWord,
  defaultShipETA,
  holdModeWord,
  inTransitBase,
  moveDirectionWord,
  moveTypeWord,
  qtyFromTyped,
  reorderFieldsFromTyped,
  receiveTransferConfirm,
  cancelTransferConfirm,
  releaseConfirm,
  expireConfirm,
  bootstrapConfirm,
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
  assert.equal(qtyFromTyped("-3").ok, false);
  assert.equal(qtyFromTyped("-3").words, "A count is zero or more.");
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

test("on a dealer's own box the permission hints drop the demo roles clause and keep the rest", () => {
  const box = { dealer: { name: "Acme Stone" } };
  const hint = (action) => refusalWords(action, "Forbidden", 403, box);
  assert.equal(
    hint("write"),
    "Writing here is for the seats the role matrix holds at work or manage: the Procurement Manager and the Yard Manager. A read-only role can look but not touch.",
  );
  assert.equal(
    hint("reserve"),
    "Holding stock, moving it between branches and setting the allocation policy are manage seats' moves on this surface: the Procurement Manager's.",
  );
  assert.equal(
    hint("manage"),
    "Moving stock between branches, holding it, and setting points and policy are manage seats' moves on this surface: the Procurement Manager's.",
  );
  assert.equal(hint("receive"), "Receiving an order into stock is a work move: the Yard Manager's and the Procurement Manager's.");
  assert.equal(
    hint("count-approve"),
    "Approving a count and posting its variance are manager seats' moves: the Procurement Manager's. The maker never approves their own count.",
  );
  assert.equal(hint("transit-days"), "Setting a route's transit time is the tech admin's; no other role on this box holds it.");
  for (const action of ["write", "reserve", "manage", "receive", "count-approve", "transit-days"]) {
    assert.doesNotMatch(hint(action), /demo/i, action);
  }
});

test("band-identity is imported once in the app root", () => {
  const root = readFileSync(new URL("../src/app-root.ts", import.meta.url), "utf8");
  assert.equal(root.match(/from "\.\/band-identity\.js"/g)?.length, 1);
});

test("a blank reorder point or order quantity is refused in words, never saved as a zero", () => {
  assert.deepEqual(reorderFieldsFromTyped("12", "10"), { ok: true, point: 12, reorderQty: 10 });
  assert.deepEqual(reorderFieldsFromTyped("0", "0"), { ok: true, point: 0, reorderQty: 0 });
  for (const [point, qty] of [["", "10"], ["12", ""], ["  ", "10"], ["", ""]]) {
    const check = reorderFieldsFromTyped(point, qty);
    assert.equal(check.ok, false, `[${point}] [${qty}]`);
    assert.ok(check.words.includes("blank"), check.words);
  }
  assert.equal(reorderFieldsFromTyped("-1", "10").ok, false);
  assert.equal(reorderFieldsFromTyped("abc", "10").ok, false);
});

test("the receive truck confirm names the transfer, the yard and each line's in transit quantity", () => {
  const c = receiveTransferConfirm(
    { transfer_no: "TRF-PLM-0001", lines: [{ product_name: "Copperline Paver 60mm", uom: "pc", qty_shipped_base: 40, qty_received_base: 10 }] },
    "Julian yard",
  );
  const text = [c.title, ...c.lines].join(" ");
  assert.ok(text.includes("TRF-PLM-0001") && text.includes("Julian yard"), text);
  assert.ok(text.includes("Copperline Paver 60mm") && text.includes("30"), text);
});

test("the cancel transfer confirm names the number and says nothing moves", () => {
  const c = cancelTransferConfirm({ transfer_no: "TRF-PLM-0002" });
  assert.ok([c.title, ...c.lines].join(" ").includes("TRF-PLM-0002"));
  assert.ok(c.lines.join(" ").includes("Nothing moves"));
});

test("the release confirm names the product, the yard and the quantity; the sweep says every expired hold", () => {
  const r = releaseConfirm({ product: "GLS-PAV-60 Copperline Paver 60mm", yard: "Point Loma yard", qty: 12 });
  const text = [r.title, ...r.lines].join(" ");
  assert.ok(text.includes("Copperline") && text.includes("Point Loma yard") && text.includes("12"), text);
  assert.ok([expireConfirm().title, ...expireConfirm().lines].join(" ").includes("every expired hold"));
});

test("the bootstrap confirm names the yard it will open lines at", () => {
  const b = bootstrapConfirm("Julian yard");
  assert.ok([b.title, ...b.lines].join(" ").includes("Julian yard"));
});

test("UX-134: a malformed success reads as the server's problem, a lost signal as unreachable", () => {
  const malformed = refusalWords("write", "not valid JSON", undefined, undefined, "malformed_success_body");
  assert.equal(malformed, refusalWords("write", "boom", 503));
  assert.doesNotMatch(malformed, /could not be reached/);
  assert.match(refusalWords("write", "network down", undefined), /could not be reached/);
});
