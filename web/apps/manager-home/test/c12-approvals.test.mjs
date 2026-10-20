// WALK-C12 Manager Home Approvals unit tests: kind counts, Margin detail card,
// Credit detail card, and Journal entry Not yours to decide detail card.
import { test } from "node:test";
import assert from "node:assert/strict";
import { BOARD_APPROVAL_ITEMS, itemsForKind, kindCount, pendingCounts } from "../src/approvals-logic.ts";
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

test("UX-47 kindCount follows the core's rows, never a constant", () => {
  const row = (id, policy_type, status) => ({ id, branch_id: "br-1", policy_type, detail: "Quote Q-2001", subject_ref: "quote:1", amount_cents: 0, status });
  const rows = [row("r1", "MARGIN_BELOW_MINIMUM", "PENDING"), row("r2", "MARGIN_BELOW_MINIMUM", "APPROVED")];
  assert.equal(kindCount("MARGIN_BELOW_MINIMUM", rows), 1);
  assert.equal(kindCount("CREDIT_LIMIT_EXCEEDED", rows), 0);
  assert.equal(kindCount("MARGIN_BELOW_MINIMUM", [], [], { MARGIN_BELOW_MINIMUM: 3 }), 3);
  const counts = pendingCounts(rows);
  assert.equal(counts.MARGIN_BELOW_MINIMUM, 1);
  assert.equal(counts.CREDIT_LIMIT_EXCEEDED, 0);
});

test("UX-47 itemsForKind never invents a request and lists waiting before decided", () => {
  const row = (id, status) => ({ id, branch_id: "br-1", policy_type: "CREDIT_LIMIT_EXCEEDED", detail: "Order SO-1 over its limit", subject_ref: "order:1", amount_cents: 500, status });
  assert.deepEqual(itemsForKind("CREDIT_LIMIT_EXCEEDED", []), []);
  const items = itemsForKind("CREDIT_LIMIT_EXCEEDED", [row("d1", "APPROVED"), row("p1", "PENDING")]);
  assert.deepEqual(items.map((i) => [i.id, i.status]), [["p1", "PENDING"], ["d1", "APPROVED"]]);
  assert.equal(items[0].title, "SO-1");
  assert.equal(items[0].amountLabel, "at stake");
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
  assert.match(marginMarkup, /Q-2001 · Seat wall block, tan blend/);
  assert.match(marginMarkup, /18\.4%/);
  assert.match(marginMarkup, /22\.0%/);
  assert.match(marginMarkup, /3\.6 points/);
  assert.match(marginMarkup, /\$35\.59 on this line/);
  assert.match(marginMarkup, /Approve at a different price/);
  assert.doesNotMatch(marginMarkup, /Jordan Lee|Elena Ruiz|Priya Shah|Morgan Ellis|Decline/);
  assert.match(marginMarkup, /Reason, needed to reject/);

  const creditMarkup = flattenTemplate(
    renderApprovals({
      phase: "ready",
      kind: "CREDIT_LIMIT_EXCEEDED",
      rows: [{ id: "0e000000-0000-4000-9000-0000000000c1", branch_id: "br-1", policy_type: "CREDIT_LIMIT_EXCEEDED", detail: "x", subject_ref: "order:1", amount_cents: 0, status: "PENDING" }],
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
      rows: [{ id: "0e000000-0000-4000-9000-0000000000j1", branch_id: "br-1", policy_type: "JE_OVER_THRESHOLD", detail: "x", subject_ref: "je:1", amount_cents: 0, status: "PENDING" }],
      error: "",
      refusal: "",
      busyId: "",
    }),
  );
  assert.match(jeMarkup, /JE-0087 · Reclassify freight in to cost of goods/);
  assert.match(jeMarkup, /5100 Cost of goods sold/);
  assert.match(jeMarkup, /5400 Freight in/);
  assert.match(jeMarkup, /DECIDED BY THE CONTROLLER/);
  assert.match(jeMarkup, /Message the financial controller/);
  assert.match(jeMarkup, /Open the journal entry/);
  assert.equal(BOARD_APPROVAL_ITEMS.length, 4);
});
