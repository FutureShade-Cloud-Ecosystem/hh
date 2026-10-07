// The second coder's pure logic, unit tested on the Node runner before any of
// it is written against a screen: the edit panel's decision tables (dates,
// the catalogue announcement, the discount bound, the share and convert
// sentences, the comparison words), the change history's grouping and words,
// the settings screens' validation and curation drafts, and the wire readers
// that refuse what they cannot prove. The screens render these answers and
// own no rule of their own.
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  catalogHitStatus,
  comparisonBasis,
  comparisonDeltaLabel,
  convertBody,
  FULFILMENT_CHOICES,
  frozenSentence,
  isShareable,
  shareLinkState,
  shareSentence,
  validateDate,
  validateDiscPct,
} from "../src/_quote-edit.js";
import {
  changeActor,
  fieldLabel,
  formatChangeValue,
  groupChanges,
  historyHeadline,
  summariseGroup,
} from "../src/_quote-history.js";
import {
  acceptanceNotice,
  buildItemsBody,
  chipChoices,
  toggleChip,
  curationDraft,
  moveDraftItem,
  policyBody,
  scopeRuleSummary,
  toggleDraftItem,
  validatePolicy,
  writeRefusalLine,
} from "../src/_quote-settings.js";
import {
  readAcceptance,
  readCatalogHits,
  readComparison,
  readHistory,
  readPolicy,
  readScopeRules,
  readShareLinks,
} from "../src/_panel-wire.js";
import { QB_ACTIONS, QB_COLUMNS, QB_PANELS } from "../src/quote-surface.js";

// ── the edit panel's tables ────────────────────────────────────────────────────

test("validateDate: empty clears the expiry, a real YYYY-MM-DD passes, a made up day is refused", () => {
  assert.equal(validateDate(""), null);
  assert.equal(validateDate("  "), null);
  assert.equal(validateDate("2031-04-09"), null);
  assert.match(validateDate("04/09/2031"), /YYYY-MM-DD/);
  assert.match(validateDate("2031-02-31"), /not a real date/);
  assert.match(validateDate("2031-13-01"), /not a real date/);
});

test("catalogHitStatus speaks silence away: a short query, none, one and many", () => {
  assert.equal(catalogHitStatus({ query: "p", count: 0 }), "Type at least 2 characters to search products.");
  assert.equal(catalogHitStatus({ query: "pave", count: 0 }), "No products match pave.");
  assert.equal(catalogHitStatus({ query: "pave", count: 1 }), "1 product match. Use the up and down arrows to choose.");
  assert.equal(
    catalogHitStatus({ query: "pave", count: 3, subject: "alternate products" }),
    "3 alternate products match. Use the up and down arrows to choose.",
  );
});

test("validateDiscPct: blank clears, a whole percent from 0 to 100 passes, everything else is refused", () => {
  assert.equal(validateDiscPct(""), null);
  assert.equal(validateDiscPct("0"), null);
  assert.equal(validateDiscPct("100"), null);
  assert.match(validateDiscPct("101"), /whole percent from 0 to 100/);
  assert.match(validateDiscPct("-1"), /whole percent from 0 to 100/);
  assert.match(validateDiscPct("2.5"), /whole percent from 0 to 100/);
  assert.match(validateDiscPct("ten"), /whole percent from 0 to 100/);
});

test("the share offer: only a draft or sent quote that has not expired can be shared, and the sentence says why not", () => {
  assert.equal(isShareable({ status: "DRAFT", expired: false }), true);
  assert.equal(isShareable({ status: "SENT", expired: false }), true);
  assert.equal(isShareable({ status: "SENT", expired: true }), false);
  assert.equal(isShareable({ status: "ACCEPTED", expired: false }), false);
  assert.equal(shareSentence({ status: "ACCEPTED", expired: false }), "This quote is accepted and cannot be shared.");
  assert.equal(shareSentence({ status: "SENT", expired: true }), "This quote has expired and cannot be shared.");
  assert.equal(shareSentence({ status: "DRAFT", expired: false }), "");
});

test("the frozen sentence names the expiry date when the quote lapsed, else the status", () => {
  assert.equal(frozenSentence({ status: "ACCEPTED", expired: false, valid_until: "" }), "This quote is accepted and can no longer be changed.");
  assert.equal(
    frozenSentence({ status: "SENT", expired: true, valid_until: "2030-01-05" }),
    "This quote expired on 2030-01-05 and can no longer be changed.",
  );
});

test("a quote the server offers no edit on says so in its own words: lapsed and frozen quotes name why, an open one names its owner", () => {
  assert.equal(
    frozenSentence({ status: "SENT", expired: false, valid_until: "", owner_name: "Sales manager (demo)" }),
    "No changes are offered to you on this quote. It belongs to Sales manager (demo).",
  );
  assert.equal(frozenSentence({ status: "DRAFT", expired: false, valid_until: "" }), "No changes are offered to you on this quote.");
  assert.equal(frozenSentence({ status: "CONVERTED", expired: false, valid_until: "" }), "This quote is converted and can no longer be changed.");
});

test("share link states: live, revoked, expired", () => {
  assert.equal(shareLinkState({ live: true, revokedAt: null }), "Active");
  assert.equal(shareLinkState({ live: false, revokedAt: "x" }), "Revoked");
  assert.equal(shareLinkState({ live: false, revokedAt: null }), "Expired");
});

test("the comparison words: a delta with its sign, the basis, and a blocked row says below floor", () => {
  assert.equal(comparisonDeltaLabel({ is_current: true, delta_cents: 0 }), "none");
  assert.equal(comparisonDeltaLabel({ is_current: false, delta_cents: -253 }), "-$2.53");
  assert.equal(comparisonDeltaLabel({ is_current: false, delta_cents: 41 }), "+$0.41");
  assert.equal(comparisonBasis({ blocked: false, source: "TIER_CATEGORY" }), "TIER_CATEGORY");
  assert.equal(comparisonBasis({ blocked: true, source: "X", block_policy: "category_margin_floor" }), "below floor (category_margin_floor)");
  assert.equal(comparisonBasis({ blocked: true, source: "X" }), "below floor");
});

test("the convert body says nothing when no fulfilment was chosen, and the choice list has the two the contract names", () => {
  assert.deepEqual(convertBody(""), {});
  assert.deepEqual(convertBody("WILL_CALL"), { fulfillment: "WILL_CALL" });
  assert.deepEqual(convertBody("DELIVERY"), {});
  assert.deepEqual(
    FULFILMENT_CHOICES.map((c) => c.value),
    ["", "WILL_CALL"],
  );
});

// ── the change history ─────────────────────────────────────────────────────────

const change = (over) => ({ field: "qty_entered", line_no: 1, occurred_at: "at-1", ...over });

test("groupChanges folds rows that share a request id, keeps order, and lets an unattributed row stand alone", () => {
  const rows = [
    change({ field: "line_added", request_id: "r1" }),
    change({ field: "qty_entered", request_id: "r1" }),
    change({ field: "valid_until", line_no: 0 }),
    change({ field: "qty_entered", request_id: "r2" }),
    change({ field: "total_cents", request_id: "r1" }),
  ];
  const groups = groupChanges(rows);
  assert.equal(groups.length, 3);
  assert.deepEqual(groups[0].map((c) => c.field), ["line_added", "qty_entered", "total_cents"]);
  assert.deepEqual(groups[1].map((c) => c.field), ["valid_until"]);
  assert.deepEqual(groups[2].map((c) => c.field), ["qty_entered"]);
});

test("summariseGroup leads with the action, never a derived column; a discount change leads over its consequences", () => {
  const group = [change({ field: "line_total_cents" }), change({ field: "disc_pct" }), change({ field: "unit_price_cents" })];
  assert.equal(summariseGroup(group).field, "disc_pct");
  assert.equal(summariseGroup([change({ field: "qty_base" }), change({ field: "qty_entered" })]).field, "qty_entered");
  assert.equal(summariseGroup([]), undefined);
});

test("field labels and values: money as dollars, the discount as a percent, an empty side as none, an unknown field as itself", () => {
  assert.equal(fieldLabel("qty_entered"), "Quantity");
  assert.equal(fieldLabel("disc_pct"), "Discount");
  assert.equal(fieldLabel("floor_cleared_by"), "Floor cleared by");
  assert.equal(fieldLabel("something_new"), "something_new");
  assert.equal(formatChangeValue("unit_price_cents", "2057"), "$20.57");
  assert.equal(formatChangeValue("disc_pct", "10"), "10%");
  assert.equal(formatChangeValue("qty_entered", undefined), "none");
  assert.equal(formatChangeValue("qty_entered", ""), "none");
  assert.equal(formatChangeValue("unit_price_cents", "not-a-number"), "not-a-number");
  assert.equal(formatChangeValue("disc_pct", ""), "none");
});

test("the actor is the display name, else words for a person unnamed, else the system; the headline names field, line and both sides", () => {
  assert.equal(changeActor({ actor_name: "Sales (demo)" }), "Sales (demo)");
  assert.equal(changeActor({ actor_id: "abcdef1234567890" }), "a staff member");
  assert.equal(changeActor({}), "the system");
  assert.equal(
    historyHeadline(change({ field: "disc_pct", line_no: 2, before: "", after: "10" })),
    "Discount, line 2: none → 10%",
  );
  assert.equal(
    historyHeadline(change({ field: "valid_until", line_no: 0, before: "2030-01-01", after: "2030-02-01" })),
    "Expiry date: 2030-01-01 → 2030-02-01",
  );
});

// ── the settings screens ───────────────────────────────────────────────────────

const POLICY = {
  cost_visibility: "MANAGER_UP",
  default_validity_days: 30,
  default_deposit_pct: 10,
  line_discounts_enabled: true,
  send_email_enabled: true,
  send_sms_enabled: false,
  include_product_images: true,
  quick_chip_set: ["Pavers"],
  quantity_notice: "All quantities are to be confirmed by the customer.",
  workspace_split_pct: 58,
  workspace_arrangement: "PRIMARY_LEFT",
};

test("validatePolicy mirrors the server's bounds and names the knob", () => {
  assert.equal(validatePolicy(POLICY), null);
  assert.match(validatePolicy({ ...POLICY, default_validity_days: 0 }), /Default validity/);
  assert.match(validatePolicy({ ...POLICY, default_validity_days: 366 }), /Default validity/);
  assert.match(validatePolicy({ ...POLICY, default_deposit_pct: 101 }), /Default deposit/);
  assert.match(validatePolicy({ ...POLICY, workspace_split_pct: 19 }), /panel split/);
  assert.match(validatePolicy({ ...POLICY, workspace_split_pct: 81 }), /panel split/);
  assert.equal(validatePolicy({ ...POLICY, quantity_notice: "x".repeat(500) }), null);
  assert.equal(validatePolicy({ ...POLICY, quantity_notice: "" }), null);
  assert.match(validatePolicy({ ...POLICY, quantity_notice: "x".repeat(501) }), /quantity notice is limited to 500/);
  assert.match(validatePolicy({ ...POLICY, default_validity_days: 1.5 }), /Default validity/);
  assert.match(validatePolicy({ ...POLICY, cost_visibility: "EVERYONE" }), /visibility/);
  assert.match(validatePolicy({ ...POLICY, workspace_arrangement: "SIDEWAYS" }), /arrangement/);
});

test("policyBody carries the scope: a branch row names the branch, the dealer default sends none", () => {
  assert.equal(policyBody(POLICY, "").branch_id, undefined);
  assert.equal(policyBody(POLICY, "b-1").branch_id, "b-1");
  assert.deepEqual(policyBody(POLICY, "").quick_chip_set, ["Pavers"]);
  assert.equal("effective_chips" in policyBody({ ...POLICY, effective_chips: ["Pavers"] }, ""), false);
});

test("writeRefusalLine speaks the server's sentence the way a counter person reads it, or a plain one when it sent none", () => {
  assert.equal(
    writeRefusalLine(403, "quote: the dealer wide default is set by the General Manager"),
    "The dealer wide default is set by the General Manager",
  );
  assert.equal(writeRefusalLine(403, "quoteassist: only an administrator changes the scope rules"), "Only an administrator changes the scope rules");
  assert.equal(writeRefusalLine(403, ""), "The server refused this change for your role.");
  assert.equal(writeRefusalLine(422, "quote: default validity must be between 1 and 365 days"), "Default validity must be between 1 and 365 days");
  assert.equal(writeRefusalLine(undefined, "no route"), "No route");
  assert.equal(writeRefusalLine(500, ""), "The change was not saved.");
});

test("a zero difference reads as a plain amount, not a signed one", () => {
  assert.equal(comparisonDeltaLabel({ is_current: false, delta_cents: 0 }), "$0.00");
});

test("the curation draft resolves the registry, hides cost columns by default, and moves and toggles without touching the minimum panels", () => {
  const panels = curationDraft("panel", []);
  assert.deepEqual(panels.map((p) => p.id), QB_PANELS.map((p) => p.id));
  const columns = curationDraft("column", []);
  assert.equal(columns.find((c) => c.id === "line.unit_cost").visible, false);
  assert.equal(columns.find((c) => c.id === "line.item").visible, true);
  const moved = moveDraftItem(columns, "line.item", -1);
  assert.equal(moved[0].id, "line.item");
  assert.equal(moveDraftItem(columns, QB_COLUMNS[0].id, -1)[0].id, QB_COLUMNS[0].id);
  const toggled = toggleDraftItem(panels, "addons");
  assert.equal(toggled.find((p) => p.id === "addons").visible, false);
  assert.equal(toggleDraftItem(panels, "lines").find((p) => p.id === "lines").visible, true);
});

test("buildItemsBody writes every kind in draft order with positions, and the minimum panels stay visible", () => {
  const body = buildItemsBody("counter", {
    panel: toggleDraftItem(curationDraft("panel", []), "lines"),
    column: curationDraft("column", []),
    action: curationDraft("action", []),
  });
  assert.equal(body.profile, "counter");
  assert.equal(body.items.length, QB_PANELS.length + QB_COLUMNS.length + QB_ACTIONS.length);
  const lines = body.items.find((i) => i.kind === "panel" && i.id === "lines");
  assert.equal(lines.visible, true);
  const first = body.items.filter((i) => i.kind === "column").map((i) => i.position);
  assert.deepEqual(first, QB_COLUMNS.map((_, i) => i));
});

test("the chip vocabulary is the active products' applications, sorted and unique; picking appends and picking again removes", () => {
  const products = [
    { is_active: true, application: "Pavers" },
    { is_active: true, application: "Aggregate" },
    { is_active: false, application: "Retired" },
    { is_active: true, application: "Pavers" },
    { is_active: true },
  ];
  assert.deepEqual(chipChoices(products), ["Aggregate", "Pavers"]);
  assert.deepEqual(toggleChip(["Pavers"], "Walls"), ["Pavers", "Walls"]);
  assert.deepEqual(toggleChip(["Pavers", "Walls"], "Pavers"), ["Walls"]);
});

test("the acceptance notice says what each position of the one switch means", () => {
  assert.match(acceptanceNotice(true), /^Multiple acceptance is on/);
  assert.match(acceptanceNotice(false), /^Multiple acceptance is off/);
  assert.equal(new RegExp("[" + String.fromCharCode(0x2013, 0x2014) + "]").test(acceptanceNotice(true) + acceptanceNotice(false)), false);
});

test("scopeRuleSummary counts the rules that are on, with the plural and the switched-off note", () => {
  const rules = [
    { code: "A", enabled: true },
    { code: "B", enabled: false },
  ];
  assert.deepEqual(scopeRuleSummary(rules), { on: 1, total: 2, anyOff: true, line: "1 of 2 rules are on." });
  assert.equal(scopeRuleSummary([{ code: "A", enabled: true }]).line, "1 of 1 rule is on.");
  assert.equal(scopeRuleSummary([]).total, 0);
});

// ── the wire readers ───────────────────────────────────────────────────────────

test("readHistory proves each row and refuses the whole list on one unreadable row", () => {
  const good = { quote_id: "q-1", changes: [{ field: "qty_entered", line_no: 1, before: "1", after: "2", occurred_at: "at-1", request_id: "r", actor_name: "Sales (demo)" }] };
  assert.equal(readHistory(good).length, 1);
  assert.equal(readHistory(good)[0].actor_name, "Sales (demo)");
  assert.equal(readHistory({ quote_id: "q-1", changes: [{ field: "x", occurred_at: "t" }] }), null);
  assert.equal(readHistory({ quote_id: "q-1" }), null);
  assert.equal(readHistory(null), null);
  assert.deepEqual(readHistory({ quote_id: "q-1", changes: [] }), []);
});

test("readShareLinks needs the id, the expiry, the live flag; the token is never read from a list", () => {
  const rows = readShareLinks({ items: [{ id: "l-1", quote_id: "q", expires_at: "at-2", created_at: "at-1", live: true, token: "must-not-ride" }] });
  assert.equal(rows.length, 1);
  assert.equal("token" in rows[0], false);
  assert.equal(readShareLinks({ items: [{ id: "l-1", live: "yes" }] }), null);
  assert.equal(readShareLinks({}), null);
});

test("readComparison proves the money fields and keeps the unavailable reason", () => {
  const row = { product_id: "p", is_current: false, qty_entered: "1", uom_entered: "pc", unit_price_cents: 100, price_uom: "pc", line_total_cents: 100, list_price_cents: 120, discount_bps: 0, source: "TIER", delta_cents: -5, blocked: false };
  const out = readComparison({ quote_id: "q", line_no: 1, options: [row, { ...row, unavailable: "cannot be priced in this unit" }] });
  assert.equal(out.length, 2);
  assert.equal(out[1].unavailable, "cannot be priced in this unit");
  assert.equal(readComparison({ quote_id: "q", line_no: 1, options: [{ ...row, unit_price_cents: 1.5 }] }), null);
  assert.equal(readComparison({ options: "x" }), null);
});

test("readCatalogHits keeps the id, sku, name and unit and drops a hit it cannot read", () => {
  assert.equal(readCatalogHits({ items: [{ product_id: "p", sku: "S", name: "N", category: "C", base_uom: "sf" }] }).length, 1);
  assert.equal(readCatalogHits({ items: [{ product_id: "p", sku: "S" }] }), null);
  assert.deepEqual(readCatalogHits({ items: [] }), []);
});

test("readPolicy proves every knob; readAcceptance proves both booleans; readScopeRules proves each rule", () => {
  assert.equal(readPolicy(POLICY).default_validity_days, 30);
  assert.equal(readPolicy({ ...POLICY, cost_visibility: "SOME" }), null);
  assert.equal(readPolicy({ ...POLICY, quick_chip_set: [1] }), null);
  assert.equal(readPolicy({ ...POLICY, workspace_arrangement: "x" }), null);
  assert.deepEqual(readAcceptance({ allow_multi_accept: false, switch_wired: true }), { allowMultiAccept: false, switchWired: true });
  assert.equal(readAcceptance({ allow_multi_accept: "no", switch_wired: true }), null);
  const rule = { code: "R", title: "T", advice: "A", when_apps: ["PAVER"], expect_apps: ["BASE"], suggest_query: "q", enabled: true, sort_order: 1 };
  assert.equal(readScopeRules({ rules: [rule] }).length, 1);
  assert.equal(readScopeRules({ rules: [{ ...rule, enabled: "on" }] }), null);
  assert.equal(readScopeRules({ rules: [{ ...rule, when_apps: [1] }] }), null);
});
