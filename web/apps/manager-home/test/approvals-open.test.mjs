// Gap 2 of the per role homes: the Approvals view opens on the first kind that
// has something waiting, not on Margin whatever is waiting.
import { test } from "node:test";
import assert from "node:assert/strict";
import { openingKind, waitingKinds } from "../src/approvals-logic.ts";

const row = (policy_type, status = "PENDING") => ({ id: "", branch_id: "", policy_type, detail: "", subject_ref: "", amount_cents: 0, status });

test("two freight raises waiting open on Freight above quote, not Margin", () => {
  assert.equal(openingKind([row("FREIGHT_ABOVE_QUOTE"), row("FREIGHT_ABOVE_QUOTE")]), "FREIGHT_ABOVE_QUOTE");
});

test("the closed vocabulary's own order decides between kinds", () => {
  assert.equal(openingKind([row("FREIGHT_ABOVE_QUOTE"), row("JE_OVER_THRESHOLD")]), "JE_OVER_THRESHOLD");
  assert.equal(openingKind([row("JE_OVER_THRESHOLD"), row("MARGIN_BELOW_MINIMUM")]), "MARGIN_BELOW_MINIMUM");
});

test("a decided row does not count as waiting", () => {
  assert.equal(openingKind([row("MARGIN_BELOW_MINIMUM", "APPROVED"), row("COD_CONSTRAINT")]), "COD_CONSTRAINT");
});

test("nothing waiting, or a kind outside the vocabulary, keeps the first kind", () => {
  assert.equal(openingKind([]), "MARGIN_BELOW_MINIMUM");
  assert.equal(openingKind([row("SOMETHING_NEW")]), "MARGIN_BELOW_MINIMUM");
});

test("waitingKinds lists every waiting kind in the vocabulary's order, once", () => {
  assert.deepEqual(
    waitingKinds([row("FREIGHT_ABOVE_QUOTE"), row("COD_CONSTRAINT"), row("FREIGHT_ABOVE_QUOTE"), row("MARGIN_BELOW_MINIMUM", "APPROVED")]),
    ["COD_CONSTRAINT", "FREIGHT_ABOVE_QUOTE"],
  );
  assert.deepEqual(waitingKinds([]), []);
});
