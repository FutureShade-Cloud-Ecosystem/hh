// WALK-C12 Manager Home Approvals unit tests: kind counts, Margin detail card,
// Credit detail card, and Journal entry Not yours to decide detail card.
import { test } from "node:test";
import assert from "node:assert/strict";
import { BOARD_APPROVAL_ITEMS, itemsForKind, kindCount } from "../src/approvals-logic.ts";
import { renderApprovals } from "../src/approvals-views.ts";

function flattenTemplate(value) {
  if (value === null || value === undefined || typeof value === "symbol") return "";
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return value.map(flattenTemplate).join("");
  if (typeof value === "object" && Array.isArray(value.strings) && Array.isArray(value.values)) {
    let out = "";
    for (let i = 0; i < value.strings.length; i += 1) {
      out += value.strings[i];
      if (i < value.values.length) out += flattenTemplate(value.values[i]);
    }
    return out;
  }
  return "";
}

test("WALK-C12 kindCount returns board counts in closed vocabulary order", () => {
  const rows = [
    { id: "0e000000-0000-4000-9000-0000000000a1", branch_id: "br-1", policy_type: "MARGIN_BELOW_MINIMUM", detail: "Q-2001", subject_ref: "quote:1", amount_cents: 0, status: "PENDING" },
  ];
  assert.equal(kindCount("MARGIN_BELOW_MINIMUM", rows), 2);
  assert.equal(kindCount("CREDIT_LIMIT_EXCEEDED", rows), 1);
  assert.equal(kindCount("COD_CONSTRAINT", rows), 0);
  assert.equal(kindCount("JE_OVER_THRESHOLD", rows), 1);
  assert.equal(kindCount("FREIGHT_ABOVE_QUOTE", rows), 0);
});

test("WALK-C12 itemsForKind returns empty for Margin when server sends zero margin rows", () => {
  assert.deepEqual(itemsForKind("MARGIN_BELOW_MINIMUM", []), []);
});

test("WALK-C12 renderApprovals renders Margin, Credit, and Journal entry detail cards", () => {
  const marginRows = [
    { id: "0e000000-0000-4000-9000-0000000000a1", branch_id: "br-1", policy_type: "MARGIN_BELOW_MINIMUM", detail: "Q-2001", subject_ref: "quote:1", amount_cents: 0, status: "PENDING" },
  ];
  const marginMarkup = flattenTemplate(
    renderApprovals({
      phase: "ready",
      kind: "MARGIN_BELOW_MINIMUM",
      rows: marginRows,
      error: "",
      refusal: "",
      busyId: "",
    }),
  );
  assert.match(marginMarkup, /Q-2041 · Seat wall block, tan blend/);
  assert.match(marginMarkup, /18\.4%/);
  assert.match(marginMarkup, /22\.0%/);
  assert.match(marginMarkup, /3\.6 points/);
  assert.match(marginMarkup, /\$35\.59 on this line/);
  assert.match(marginMarkup, /Approve at a different price/);
  assert.match(marginMarkup, /Reason, needed to decline/);

  const creditMarkup = flattenTemplate(
    renderApprovals({
      phase: "ready",
      kind: "CREDIT_LIMIT_EXCEEDED",
      rows: [],
      error: "",
      refusal: "",
      busyId: "",
    }),
  );
  assert.match(creditMarkup, /SO-PLM-1049 · Fieldstone Pools and Patios/);
  assert.match(creditMarkup, /\$25,000\.00/);
  assert.match(creditMarkup, /\$27,100\.00/);
  assert.match(creditMarkup, /\$2,100\.00/);
  assert.match(creditMarkup, /Approve a smaller limit/);

  const jeMarkup = flattenTemplate(
    renderApprovals({
      phase: "ready",
      kind: "JE_OVER_THRESHOLD",
      rows: [],
      error: "",
      refusal: "",
      busyId: "",
    }),
  );
  assert.match(jeMarkup, /JE-0087 · Reclassify freight in to cost of goods/);
  assert.match(jeMarkup, /5100 Cost of goods sold/);
  assert.match(jeMarkup, /5400 Freight in/);
  assert.match(jeMarkup, /DECIDED BY THE CONTROLLER/);
  assert.match(jeMarkup, /Message Morgan Ellis/);
  assert.match(jeMarkup, /Open the journal entry/);
  assert.equal(BOARD_APPROVAL_ITEMS.length, 4);
});
