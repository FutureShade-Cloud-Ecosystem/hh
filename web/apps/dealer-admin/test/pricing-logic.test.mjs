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

// -- the quantity breaks tab: a band and a value in its own unit ----------------------

import {
  bandWords,
  breakItemWords,
  breaksEmptyWords,
} from "../src/pricing-logic.ts";

test("a break's band reads as the quantities it covers; an open top says so", () => {
  assert.equal(bandWords(10, 20), "10 to 20");
  assert.equal(bandWords(10, null), "10 and up");
  assert.equal(bandWords(0, null), "any quantity");
  assert.equal(bandWords(Number.NaN, 20), "");
});

test("a break's value says what the tier does, in the tier's own unit", () => {
  assert.equal(breakItemWords({ value_type: "PCT_OFF", discount_bps: 500 }), "5.00% off");
  assert.equal(breakItemWords({ value_type: "ABS_OFF", discount_cents: 250 }), "CA$2.50 off");
  assert.equal(breakItemWords({ value_type: "SURCHARGE_PCT", surcharge_bps: 300 }), "3.00% surcharge");
  assert.equal(breakItemWords({ value_type: "SURCHARGE_ABS", surcharge_cents: 100 }), "CA$1.00 surcharge");
  assert.equal(breakItemWords({ value_type: "ODD" }), "");
});

test("the breaks tab's empty words are its own", () => {
  assert.match(breaksEmptyWords(), /No quantity breaks yet/);
});

// -- the promotions and clearance tab -------------------------------------------------

import {
  clearanceValueWords,
  promoValueWords,
  promosEmptyWords,
} from "../src/pricing-logic.ts";

test("a promotion's value says what the calc does; the fixed price names its unit", () => {
  assert.equal(promoValueWords({ calc_type: "PCT_OFF", discount_bps: 1000 }), "10.00% off");
  assert.equal(promoValueWords({ calc_type: "AMT_OFF", discount_cents: 150 }), "CA$1.50 off");
  assert.equal(promoValueWords({ calc_type: "FIXED_PRICE", price_cents: 425, price_uom: "SF" }), "CA$4.25 a SF, fixed");
  assert.equal(promoValueWords({ calc_type: "ODD" }), "");
});

test("a clearance item without a price is the badge only case, said plainly", () => {
  assert.equal(clearanceValueWords({ clearance_price_cents: null, price_uom: "" }), "badge only, prices through the waterfall");
  assert.equal(clearanceValueWords({ clearance_price_cents: 350, price_uom: "EA" }), "CA$3.50 an EA");
});

test("the promotions tab's empty words are its own", () => {
  assert.match(promosEmptyWords(), /No promotions or clearance yet/);
});

// -- the rebates tab -----------------------------------------------------------------------------------

import {
  accrualResultWords,
  rebateEmptyWords,
  rebateRateWords,
  vendorWords,
} from "../src/pricing-logic.ts";

test("a rebate's rate renders in the shape its basis means, the original's own rule", () => {
  assert.equal(rebateRateWords({ basis: "PCT_OF_SALE", rate_bps: 200, rate_cents_per_unit: null }), "2.00% of sale");
  assert.equal(rebateRateWords({ basis: "PCT_OF_COST", rate_bps: 150, rate_cents_per_unit: null }), "1.50% of cost");
  assert.equal(rebateRateWords({ basis: "AMT_PER_UNIT", rate_bps: null, rate_cents_per_unit: 40 }), "CA$0.40 a base unit");
  assert.equal(rebateRateWords({ basis: "ODD", rate_bps: null, rate_cents_per_unit: null }), "");
});

test("a vendor shows its name, else the id's own block", () => {
  assert.equal(vendorWords("Northline Supply", "vd1"), "Northline Supply");
  assert.equal(vendorWords("", "vd110000-0000-4000-9000-0000000000a1"), "vd110000");
  assert.equal(vendorWords("", ""), "");
});

test("the accrual verdict is the server's own counts, told as what happened", () => {
  const words = accrualResultWords({ accrued: [{ id: "1" }], skipped_count: 2, total_cents: 140 });
  assert.match(words, /1 accrual/);
  assert.match(words, /2 lines were already accrued/);
  assert.match(words, /CA\$1\.40/);
});

test("the rebates tab's empty words are its own", () => {
  assert.match(rebateEmptyWords(), /No rebate programs yet/);
});

// -- the units of measure tab: the sentences the counter reads out ---------------------

import {
  chainLabel,
  pickSentence,
  uomEmptyWords,
  uomUnconfirmedWords,
  weightSentence,
} from "../src/pricing-logic.ts";

const PLAN = {
  product_id: "fa110000-0000-4000-9000-0000000000a1",
  qty_entered: 77,
  uom_entered: "SF",
  pick_uom: "PLT",
  pick_qty_exact: 6.16,
  pick_qty: 7,
  rounded_up: true,
  overage_qty: 0.84,
  weight_uom: "T",
  weight_qty: 7.7,
  weight_steps: [{ from_uom: "SF", to_uom: "PLT", factor: 0.08, source: "SEEDED_DEFAULT" }],
  unconfirmed: true,
};

test("the pick sentence always names the overage when there is one, the original's own rule", () => {
  assert.equal(pickSentence(PLAN), "77 SF = 6.16 PLT, picks 7 PLT (rounded up; 0.84 PLT over).");
  assert.equal(pickSentence({ ...PLAN, rounded_up: false, pick_qty: 6.16, overage_qty: 0 }), "77 SF = 6.16 PLT, picks exactly.");
  assert.equal(pickSentence({ ...PLAN, pick_uom: undefined }), "");
  assert.equal(pickSentence(null), "");
});

test("the weight sentence names the chain it rode in on", () => {
  assert.equal(weightSentence(PLAN), "Load weight 7.7 T (via SF → PLT).");
  assert.equal(weightSentence({ ...PLAN, weight_uom: undefined }), "");
});

test("a chain reads as its hops, never one composed factor", () => {
  assert.equal(chainLabel(PLAN.weight_steps), "SF → PLT");
  assert.equal(chainLabel([{ from_uom: "SF", to_uom: "PLT", factor: 1 }, { from_uom: "PLT", to_uom: "T", factor: 1 }]), "SF → PLT → T");
  assert.equal(chainLabel(undefined), "");
});

test("the uom tab's warn line and empty words are its own", () => {
  assert.match(uomUnconfirmedWords(1, 2), /1 of 2 factors are seeded defaults/);
  assert.match(uomEmptyWords(), /No conversions configured/);
});

// -- the batches and imports tab -------------------------------------------------------------------------

import {
  batchEmptyWords,
  batchLineWords,
  batchStatusWord,
  derivationWord,
  importResultWords,
} from "../src/pricing-logic.ts";

test("a batch status keeps its own word, voice lowered; a derivation says its anchor", () => {
  assert.equal(batchStatusWord("STAGED"), "Staged");
  assert.equal(batchStatusWord("APPLIED"), "Applied");
  assert.equal(batchStatusWord("STRANGE_STATE"), "strange state");
  assert.equal(derivationWord("TRADE_PLUS_FOB"), "Trade + freight");
  assert.equal(derivationWord("NONE"), "Not derived");
});

test("a batch line reads as before to after, with the outlier and the unmatched named", () => {
  const words = batchLineWords({ sku: "PAV-60-CH", before_retail_cents: 480, after_retail_cents: 520, is_outlier: true, unmatched: false });
  assert.match(words, /PAV-60-CH/);
  assert.match(words, /CA\$4\.80/);
  assert.match(words, /CA\$5\.20/);
  assert.match(words, /outlier/);
  assert.match(batchLineWords({ sku: "XX", before_retail_cents: null, after_retail_cents: null, is_outlier: false, unmatched: true }), /no catalog product/);
});

test("an import's result is the server's own counts", () => {
  const words = importResultWords({ total_lines: 120, matched_lines: 118, unmatched: [{ sku: "XX" }], outliers: [{ sku: "YY" }] });
  assert.match(words, /120 rows parsed/);
  assert.match(words, /118 matched/);
  assert.match(words, /1 SKU has no catalog product/);
  assert.match(words, /1 outlier/);
});

test("the batches tab's empty words are its own", () => {
  assert.match(batchEmptyWords(), /No price batches yet/);
});
