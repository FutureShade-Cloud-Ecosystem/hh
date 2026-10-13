// The counts slice's plain logic: the sheet's five states and their words, the
// maker-checker gates as tests hold them, and the entry's own checks. Nothing
// here computes money; the server values every variance.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  canApprove,
  canPost,
  confirmPostWords,
  countsFromTyped,
  entryState,
  reconcilePeriodWord,
  reconStatusWord,
  sheetScopeWord,
  sheetStatusTone,
  sheetStatusWord,
  signTone,
  varianceNeedsReason,
} from "../src/counts/counts-logic.ts";

const sheet = (over = {}) => ({
  id: "s1",
  sheet_no: "CS-PLM-0001",
  branch_id: "b1",
  scope: "FULL",
  status: "OPEN",
  notes: "",
  counted_by: "",
  approved_by: "",
  ...over,
});

test("a sheet's five states are said as words, and an unknown one degrades honestly", () => {
  assert.equal(sheetStatusWord("OPEN"), "Open");
  assert.equal(sheetStatusWord("FROZEN"), "Frozen");
  assert.equal(sheetStatusWord("COUNTED"), "Counted");
  assert.equal(sheetStatusWord("POSTED"), "Posted");
  assert.equal(sheetStatusWord("CANCELLED"), "Cancelled");
  assert.equal(sheetStatusWord("LOST"), "lost");
});

test("the sheet's tone never carries the meaning alone; the word sits beside it", () => {
  assert.equal(sheetStatusTone("POSTED"), "good");
  assert.equal(sheetStatusTone("CANCELLED"), "stop");
  assert.equal(sheetStatusTone("COUNTED"), "watch");
  assert.equal(sheetStatusTone("FROZEN"), "watch");
  assert.equal(sheetStatusTone("OPEN"), "watch");
});

test("a sheet's scope is said as the counter says it, and a category scope stays raw", () => {
  assert.equal(sheetScopeWord("FULL"), "Full count");
  assert.equal(sheetScopeWord("AD_HOC"), "Ad-hoc");
  assert.equal(sheetScopeWord("CATEGORY:42"), "category:42");
});

test("the entry pane shows one state at a time: freeze first, then the count, then the read", () => {
  assert.equal(entryState(sheet({ status: "OPEN" })), "freeze");
  assert.equal(entryState(sheet({ status: "FROZEN" })), "entry");
  assert.equal(entryState(sheet({ status: "COUNTED" })), "entry");
  assert.equal(entryState(sheet({ status: "POSTED" })), "read");
  assert.equal(entryState(sheet({ status: "CANCELLED" })), "read");
});

test("the maker-checker gates follow the status and the stamps, never the person's word", () => {
  const counted = sheet({ status: "COUNTED", counted_by: "maker", approved_by: "" });
  assert.equal(canApprove({ ...counted, counted_by: "maker" }, "maker"), false, "the maker cannot approve");
  assert.equal(canApprove({ ...counted }, "checker"), true);
  assert.equal(canApprove(sheet({ status: "FROZEN" }), "checker"), false);
  assert.equal(canApprove(sheet({ status: "POSTED", counted_by: "maker", approved_by: "checker" }), "checker"), false);
  assert.equal(canPost(sheet({ status: "COUNTED", counted_by: "maker", approved_by: "checker" })), true);
  assert.equal(canPost(sheet({ status: "COUNTED", counted_by: "maker", approved_by: "" })), false);
  assert.equal(canPost(sheet({ status: "FROZEN" })), false);
});

test("a variance needs a reason; a flat count does not", () => {
  assert.equal(varianceNeedsReason({ counted_qty_base: 8, snapshot_qty_base: 10 }), true);
  assert.equal(varianceNeedsReason({ counted_qty_base: 10, snapshot_qty_base: 10 }), false);
  assert.equal(varianceNeedsReason({ counted_qty_base: null, snapshot_qty_base: 10 }), false);
});

test("the counted field is read through its decimal string, junk and blanks are refused in words", () => {
  assert.deepEqual(countsFromTyped("12"), { ok: true, qty: 12 });
  assert.deepEqual(countsFromTyped("12.5"), { ok: true, qty: 12.5 });
  assert.deepEqual(countsFromTyped("-3.25"), { ok: true, qty: -3.25 });
  assert.equal(countsFromTyped("").ok, false);
  assert.equal(countsFromTyped("about ten").ok, false);
  assert.notEqual(countsFromTyped("").words, "");
});

test("the post confirm names the branch, the sheet and the value, so the press is made with the facts", () => {
  const words = confirmPostWords(
    { sheet_no: "CS-PLM-0002", branch_name: "Point Loma yard" },
    { variance_lines: 2, net_variance_value_cents: -12500 },
  );
  assert.ok(words.includes("CS-PLM-0002"), words);
  assert.ok(words.includes("Point Loma yard"), words);
  assert.ok(words.includes("2"), words);
});

test("the recon verdict is said as the trial balance says it", () => {
  assert.equal(reconStatusWord("TIED"), "Tied");
  assert.equal(reconStatusWord("OUT_OF_TIE"), "Out of tie");
  assert.equal(reconStatusWord("OPEN"), "Open");
  assert.equal(signTone(-12500), "stop");
  assert.equal(signTone(400), "good");
  assert.equal(signTone(0), "plain");
  assert.equal(reconcilePeriodWord(2031, 3), "March 2031");
});
