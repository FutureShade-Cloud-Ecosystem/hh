// The availability chips' pure table: the server grades each line GREEN, THIN,
// NONE or UNKNOWN and the app only words it. Ported from the ERP's
// _order-depth availability helpers (there is no separate pure file there) and
// adapted for HH: the peek shows every branch row the server returns, and the
// average cost appears only when the server sent one.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  availabilityChip,
  availabilityLabel,
  branchRowLabel,
  formatQty,
  shouldOfferPeek,
  softHeldNote,
} from "../src/_quote-availability.js";

const branch = (over = {}) => ({
  product_id: "p", branch_id: "b1", branch_code: "PLM", branch_name: "Point Loma",
  on_hand_base: 0, reserved_base: 0, in_transit_base: 0, available_base: 0, soft_held_base: 0, ...over,
});
const line = (grade, available, qty, over = {}) => ({ line_no: 1, product_id: "p", qty_base: qty, grade, branch: branch({ available_base: available, ...over }) });

test("each grade has its own words, and unknown is not none", () => {
  assert.equal(availabilityLabel(line("GREEN", 800, 1)), "800 available");
  assert.equal(availabilityLabel(line("THIN", 40, 60)), "only 40 of 60");
  assert.equal(availabilityLabel(line("NONE", 0, 5)), "none available");
  assert.equal(availabilityLabel(line("UNKNOWN", 0, 5)), "not reported");
  assert.equal(availabilityLabel(null), "not reported");
  assert.equal(availabilityLabel(undefined), "not reported");
  assert.notEqual(availabilityLabel(line("UNKNOWN", 0, 5)), availabilityLabel(line("NONE", 0, 5)));
});

test("an unreported grade never prints a quantity, not even a zero", () => {
  assert.doesNotMatch(availabilityLabel(line("UNKNOWN", 0, 5)), /\d/);
  assert.doesNotMatch(availabilityLabel(line("WHATEVER", 7, 5)), /\d/);
});

test("each grade has a distinct chip look; a stranger falls to the neutral one", () => {
  const looks = ["GREEN", "THIN", "NONE", "UNKNOWN"].map(availabilityChip);
  assert.equal(new Set(looks).size, 4);
  assert.equal(availabilityChip("odd"), availabilityChip("UNKNOWN"));
});

test("the peek is offered only behind a short line with a product", () => {
  assert.equal(shouldOfferPeek(line("THIN", 4, 12)), true);
  assert.equal(shouldOfferPeek(line("NONE", 0, 12)), true);
  assert.equal(shouldOfferPeek(line("GREEN", 100, 12)), false);
  assert.equal(shouldOfferPeek(line("UNKNOWN", 0, 12)), false);
  assert.equal(shouldOfferPeek({ ...line("NONE", 0, 1), product_id: "" }), false);
  assert.equal(shouldOfferPeek(null), false);
});

test("a branch row says its code, what is available and what is inbound", () => {
  assert.equal(branchRowLabel(branch({ branch_code: "JUL", available_base: 60 }), "other"), "JUL: 60 available");
  assert.equal(branchRowLabel(branch({ branch_code: "JUL", available_base: 60, in_transit_base: 10 }), "other"), "JUL: 60 available (+10 inbound)");
  assert.equal(branchRowLabel(branch({ available_base: 0 }), "other"), "PLM: none available");
});

test("this branch's own row is marked", () => {
  assert.match(branchRowLabel(branch({ available_base: 2 }), "b1"), /this branch/);
  assert.doesNotMatch(branchRowLabel(branch({ available_base: 2 }), "b2"), /this branch/);
});

test("average cost appears only when the server sent one, and never as a zero for unknown", () => {
  assert.doesNotMatch(branchRowLabel(branch({ available_base: 5 }), "x"), /cost/);
  assert.match(branchRowLabel(branch({ available_base: 5, avg_cost_cents: 2599 }), "x"), /avg cost \$25\.99/);
  assert.doesNotMatch(branchRowLabel(branch({ available_base: 5, avg_cost_cents: undefined }), "x"), /cost|\$0/);
  assert.match(branchRowLabel(branch({ available_base: 5, avg_cost_cents: 0 }), "x"), /avg cost \$0\.00/);
});

test("soft held note speaks only when something is soft held", () => {
  assert.equal(softHeldNote(line("GREEN", 5, 1, { soft_held_base: 0 })), "");
  assert.equal(softHeldNote(line("GREEN", 5, 1, { soft_held_base: 19 })), "(19 on other quotes, not held against you)");
});

test("formatQty trims zeros and survives garbage", () => {
  assert.equal(formatQty(4.5), "4.5");
  assert.equal(formatQty(60), "60");
  assert.equal(formatQty(Number.NaN), "0");
});

test("no word carries an em or en dash", () => {
  const all = [availabilityLabel(line("THIN", 1, 2)), branchRowLabel(branch({ available_base: 1, in_transit_base: 2, avg_cost_cents: 5 }), "b1"), softHeldNote(line("GREEN", 1, 1, { soft_held_base: 3 }))];
  for (const s of all) assert.doesNotMatch(s, /[\u2013\u2014]/);
});
