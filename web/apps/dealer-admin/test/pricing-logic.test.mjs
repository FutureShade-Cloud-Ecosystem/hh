// The Pricing surface's pure decisions, on their own: the words the tabs say
// (the enum maps the pricing words file carried), the money and percent
// formats, the form input encoders, the floor warning's reading of the
// server's own grading, and the empty and refusal sentences. No browser, no
// network. Red first: this file landed before pricing-logic.ts existed, and
// the run below it failed on the missing module (see the report).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  PRICING_TABS,
  affectedFloorItems,
  alreadyUnderFloorCount,
  auditActionWord,
  bpsFromPctInput,
 calcTypeWord,
  categoryName,
  centsFromMoneyInput,
  clearWords,
  floorConfirmVerdict,
  floorNoteLines,
  marginEmptyWords,
  needsFloorConfirm,
  priceSourceWord,
  pricingEmptyWords,
  pricingRefusalWords,
  scopeWord,
} from "../src/pricing-logic.ts";

// -- the tab frame --------------------------------------------------------------------------

test("the Pricing surface names its twelve tabs in the order the work reasons about them", () => {
  assert.deepEqual(
    PRICING_TABS.map((t) => t.id),
    [
      "margin",
      "rules",
      "customers",
      "breaks",
      "promos",
      "rebates",
      "uom",
      "batches",
      "pricefile",
      "lowmargin",
      "audit",
      "changelog",
    ],
  );
});

test("each tab carries the list words and the one line under them", () => {
  const margin = PRICING_TABS.find((t) => t.id === "margin");
  assert.equal(margin.title, "Margin policy");
  assert.match(margin.blurb, /floor/);
  const rebates = PRICING_TABS.find((t) => t.id === "rebates");
  assert.equal(rebates.title, "Rebates");
});

// -- the words a stored enum carries ----------------------------------------------------------

test("a rule's scope is a word, never the CHECK constraint's constant", () => {
  assert.equal(scopeWord("TIER_DEFAULT"), "Tier default");
  assert.equal(scopeWord("TIER_CATEGORY"), "Tier + category");
  assert.equal(scopeWord("CUSTOMER_PRODUCT"), "Customer + product");
  assert.equal(scopeWord("CUSTOMER_CATEGORY"), "Customer + category");
});

test("an unmapped scope degrades honestly, as itself opened out", () => {
  assert.equal(scopeWord("STRANGE_SCOPE"), "strange scope");
  assert.equal(scopeWord(""), "");
});

test("a calc type says what it does to the price", () => {
  assert.equal(calcTypeWord("ABSOLUTE"), "Fixed price");
  assert.equal(calcTypeWord("PCT_OFF_LIST"), "Percent off list");
  assert.equal(calcTypeWord("MARKUP_ON_COST"), "Markup on cost");
  assert.equal(calcTypeWord("ODD"), "odd");
});

test("the audit action is told as something that happened, the bypass named", () => {
  assert.equal(auditActionWord("CREATE"), "Created");
  assert.equal(auditActionWord("UPDATE"), "Updated");
  assert.equal(auditActionWord("DELETE"), "Deleted");
  assert.equal(auditActionWord("BYPASS"), "Bypassed");
  assert.equal(auditActionWord("STRANGE"), "strange");
});

test("a price source says which rule set the waterfall stopped at", () => {
  assert.equal(priceSourceWord("CUSTOMER_PRODUCT_RULE"), "Customer + product");
  assert.equal(priceSourceWord("CUSTOMER_CATEGORY_RULE"), "Customer + category");
  assert.equal(priceSourceWord("CUSTOMER_OVERRIDE"), "Customer override");
  assert.equal(priceSourceWord("QUANTITY_BREAK"), "Quantity break");
  assert.equal(priceSourceWord("TIER_CATEGORY"), "Tier + category");
  assert.equal(priceSourceWord("TIER_DEFAULT"), "Tier default");
  assert.equal(priceSourceWord("PROMOTION"), "Promotion");
  assert.equal(priceSourceWord("LIST"), "List price");
});

// -- the figures ------------------------------------------------------------------------------

test("money renders from the server's cents, never recomputed", () => {
  assert.equal(centsFromMoneyInput("12.34"), 1234);
  assert.equal(centsFromMoneyInput("12"), 1200);
  assert.equal(centsFromMoneyInput(""), null);
  assert.equal(centsFromMoneyInput("abc"), null);
  assert.equal(centsFromMoneyInput("-1"), null);
});

test("a percent the person typed becomes integer basis points, or nothing", () => {
  assert.equal(bpsFromPctInput("30"), 3000);
  assert.equal(bpsFromPctInput("12.5"), 1250);
  assert.equal(bpsFromPctInput(""), null);
  assert.equal(bpsFromPctInput("abc"), null);
  assert.equal(bpsFromPctInput("-3"), null);
});

// -- the category names -------------------------------------------------------------------------

test("a category shows its catalog name; an unknown id shows its own block", () => {
  const cats = [{ id: "ca7-1", code: "PAV", name: "Pavers", is_active: true }];
  assert.equal(categoryName("ca7-1", cats), "Pavers");
  assert.equal(categoryName("9a11e111-0000-4000-9000-0000000000ff", cats), "9a11e111");
  assert.equal(categoryName("", cats), "");
});

// -- the states --------------------------------------------------------------------------------

test("the margin tab's empty words are its own", () => {
  assert.match(marginEmptyWords(), /No margin policies yet/);
});

test("an unwired tab says so plainly and names nothing false", () => {
  assert.match(pricingEmptyWords("Not wired"), /not wired/);
});

test("a clearing write answers with the server's own status word, or its own error words", () => {
  assert.equal(clearWords({ status: "deleted" }), "deleted");
  assert.equal(clearWords(null), "");
});

// -- the refusals ------------------------------------------------------------------------------

test("the shared Forbidden body becomes words naming who may act on pricing", () => {
  const words = pricingRefusalWords("Forbidden", 403, "write");
  assert.match(words, /manager/);
  assert.match(words, /Forbidden/);
});

test("a refusal that is not the shared body stays the server's own words", () => {
  assert.equal(pricingRefusalWords("category not found", 404, "write"), "category not found");
});

// -- the floor warning: the server graded, the client only reads the grade ---------------------

const PREVIEW = {
  items: [
    { product_id: "p1", sku: "PAV-1", name: "Paver one", final_cents: 420, margin_bps: 1500, floor_bps: 2000, previous_final_cents: 480, previous_margin_bps: 2600, already_under_floor: false },
    { product_id: "p2", sku: "WAL-1", name: "Wall one", final_cents: 100, margin_bps: 500, floor_bps: 2000, previous_final_cents: 120, previous_margin_bps: 900, already_under_floor: true },
    { product_id: "p3", sku: "SLB-1", name: "Slab one", final_cents: 900, margin_bps: 3000, floor_bps: 2000, previous_final_cents: 900, previous_margin_bps: 3000, already_under_floor: true },
  ],
  evaluated: 40,
  not_graded: 2,
  no_cost: 1,
  truncated: false,
};

test("the warning names the newly under and the under and cheaper; the no worse are not asked about", () => {
  const affected = affectedFloorItems(PREVIEW);
  assert.deepEqual(affected.map((i) => i.product_id), ["p1", "p2"]);
  assert.equal(alreadyUnderFloorCount(PREVIEW), 1);
});

test("the editor is asked to confirm when the change makes a product worse or the grading has gaps", () => {
  assert.equal(needsFloorConfirm(PREVIEW), true);
  assert.equal(
    needsFloorConfirm({ items: [], evaluated: 40, not_graded: 0, no_cost: 0, truncated: false }),
    false,
    "a clean grading never holds the save",
  );
  assert.equal(
    needsFloorConfirm({ items: [], evaluated: 40, not_graded: 3, no_cost: 0, truncated: false }),
    true,
    "a product the check could not grade is a gap, never a silent save",
  );
  assert.equal(
    needsFloorConfirm({ items: [], evaluated: 40, not_graded: 0, no_cost: 2, truncated: false }),
    true,
    "a product with no cost on file is a gap the editor must see",
  );
});

test("the grading's coverage is said in lines, and a clean grading says nothing", () => {
  const lines = floorNoteLines(PREVIEW);
  assert.equal(lines.length, 2);
  assert.match(lines.join(" "), /2 products could not be graded/);
  assert.match(lines.join(" "), /1 product has no cost on file/);
  const truncated = floorNoteLines({ ...PREVIEW, truncated: true, not_graded: 0, no_cost: 0 });
  assert.equal(truncated.length, 1);
  assert.match(truncated[0], /stopped at its cap/);
  assert.equal(floorNoteLines({ items: [], evaluated: 40, not_graded: 0, no_cost: 0, truncated: false }).length, 0);
});

test("the confirmation's verdict is the server's own word, shown whole", () => {
  assert.match(floorConfirmVerdict({ status: "logged" }), /logged/);
  assert.match(floorConfirmVerdict(null), /not recorded/);
});

// -- the rules tab: a rule read as a person reads it ---------------------------------

import {
  ruleCalcWords,
  ruleEmptyWords,
  ruleValueWords,
  validityWords,
} from "../src/pricing-logic.ts";

const RULE = {
  id: "9a11e111-0000-4000-9000-000000000001",
  scope: "TIER_CATEGORY",
  category_id: "ca7-1",
  calc_type: "PCT_OFF_LIST",
  discount_bps: 1000,
  price_uom: "SF",
  priority: 10,
  detail_text: "Trade discount",
  is_active: true,
  version: 3,
};

test("a rule's value says what the calc does, in the calc's own unit", () => {
  assert.equal(ruleValueWords({ ...RULE, calc_type: "ABSOLUTE", price_cents: 480 }), "CA$4.80 a SF");
  assert.equal(ruleValueWords({ ...RULE, calc_type: "PCT_OFF_LIST", discount_bps: 1000 }), "10.00% off list");
  assert.equal(ruleValueWords({ ...RULE, calc_type: "MARKUP_ON_COST", markup_bps: 2500 }), "25.00% on cost");
  assert.equal(ruleValueWords({ ...RULE, calc_type: "ODD" }), "");
});

test("a rule's validity reads as the window it holds, or nothing when it is open", () => {
  assert.equal(validityWords("", ""), "");
  assert.equal(validityWords("2026-03-01T00:00:00Z", ""), "from 2026-03-01");
  assert.equal(validityWords("", "2026-11-30T00:00:00Z"), "to 2026-11-30");
  assert.equal(validityWords("2026-03-01T00:00:00Z", "2026-11-30T00:00:00Z"), "2026-03-01 to 2026-11-30");
  assert.equal(validityWords("not a date", ""), "");
});

test("the rules tab's empty words are its own", () => {
  assert.match(ruleEmptyWords(), /No pricing rules yet/);
});

test("a rule's calc cell keeps the unit beside the figure", () => {
  assert.equal(ruleCalcWords("SF"), "a SF");
  assert.equal(ruleCalcWords(""), "");
});

// -- the customer prices tab: two different empties, a value in its unit ------------------------------

import {
  overrideEmptyWords,
  overrideValueWords,
} from "../src/pricing-logic.ts";

test("the overrides tab's two empties are different sentences, the original's own rule", () => {
  assert.match(overrideEmptyWords(false), /Type a customer id/);
  assert.match(overrideEmptyWords(true), /No category prices for this customer/);
});

test("an override's value says what the calc does; a fixed price is not an override", () => {
  assert.equal(overrideValueWords({ calc_type: "PCT_OFF_LIST", discount_bps: 800 }), "8.00% off list");
  assert.equal(overrideValueWords({ calc_type: "MARKUP_ON_COST", markup_bps: 1200 }), "12.00% on cost");
  assert.equal(overrideValueWords({ calc_type: "ABSOLUTE", price_cents: 100 }), "");
  assert.equal(overrideValueWords({ calc_type: "ODD" }), "");
});
