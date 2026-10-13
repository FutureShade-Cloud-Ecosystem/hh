// Manager Home's reporting decisions, on their own: the cell formatters the
// column kinds drive, the parameter form's defaults and its one guard, the
// catalogue order, the result summary line, the reachability rules that mirror
// the core's guard tiers, and the words a walled refusal becomes. No browser,
// no network. Every case is a fact the core holds (the wire contract, the
// guard's tier window) or the original reporting console's own decision,
// ported and narrowed.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CATEGORY_ORDER,
  COMMISSION_RULE_KEEPER_WORDS,
  REPORTING_PANES,
  basisLabel,
  bps,
  canAdminCommissionRules,
  canOpenReporting,
  csvName,
  defaultParams,
  findTemplate,
  formatCell,
  isNumericKind,
  lineIsUnpaid,
  missingRequired,
  orderCategories,
  periodLabel,
  printAtNeeded,
  qty,
  reportingRefusalWords,
  commissionRuleRefusalWords,
  resultSummary,
  runParams,
  totalCell,
  unattributedWords,
} from "../src/reporting-logic.ts";

const COL = (name, kind, total = false) => ({ name, label: name, kind, total });

test("cents render through the shared formatter, sign and grouping included", () => {
  assert.equal(formatCell("cents", 1968), "$19.68");
  assert.equal(formatCell("cents", 123456), "$1,234.56");
  assert.equal(formatCell("cents", -12345), "-$123.45");
  assert.equal(formatCell("cents", null), "");
  assert.equal(formatCell("cents", undefined), "");
});

test("the other column kinds render as a person reads them", () => {
  assert.equal(formatCell("int", 1234), "1,234");
  assert.equal(formatCell("qty", 12.5), "12.5");
  assert.equal(formatCell("qty", 4), "4");
  assert.equal(formatCell("date", "2026-10-14"), "2026-10-14");
  assert.equal(formatCell("bool", true), "Yes");
  assert.equal(formatCell("bool", false), "No");
  assert.equal(formatCell("text", "Trenton"), "Trenton");
});

test("qty trims the wire's padded decimal and bps reads as a percentage", () => {
  assert.equal(qty(12), "12");
  assert.equal(qty(12.5), "12.5");
  assert.equal(qty(0.75), "0.75");
  assert.equal(bps(500), "5.00%");
  assert.equal(bps(0), "0.00%");
  assert.equal(bps(125), "1.25%");
});

test("numeric kinds right align; text kinds do not", () => {
  assert.equal(isNumericKind("cents"), true);
  assert.equal(isNumericKind("int"), true);
  assert.equal(isNumericKind("qty"), true);
  assert.equal(isNumericKind("text"), false);
  assert.equal(isNumericKind("date"), false);
  assert.equal(isNumericKind("bool"), false);
});

test("a totals cell answers the column's total only, formatted by its kind", () => {
  const col = COL("total_cents", "cents", true);
  const plain = COL("day", "date", false);
  assert.equal(totalCell(col, { total_cents: 5000 }), "$50.00");
  assert.equal(totalCell(col, {}), "");
  assert.equal(totalCell(plain, { day: 1 }), "");
});

test("date parameters default to the month to date, the form is never submitted empty", () => {
  const params = [
    { name: "from_date", kind: "date", required: true, label: "From date" },
    { name: "to_date", kind: "date", required: true, label: "To date" },
    { name: "branch_id", kind: "uuid", required: false, label: "Branch" },
  ];
  const values = defaultParams(params, new Date(Date.UTC(2026, 9, 14)));
  assert.equal(values.from_date, "2026-10-01");
  assert.equal(values.to_date, "2026-10-14");
  assert.equal("branch_id" in values, false);
});

test("missing required parameters name the labels the form shows", () => {
  const params = [
    { name: "from_date", kind: "date", required: true, label: "From date" },
    { name: "to_date", kind: "date", required: true, label: "To date" },
    { name: "branch_id", kind: "uuid", required: false, label: "Branch" },
  ];
  assert.deepEqual(missingRequired(params, {}), ["From date", "To date"]);
  assert.deepEqual(missingRequired(params, { from_date: "  ", to_date: "2026-10-14" }), ["From date"]);
  assert.deepEqual(missingRequired(params, { from_date: "2026-10-01", to_date: "2026-10-14" }), []);
});

test("run params drop empty values rather than sending them, an absence is no filter", () => {
  assert.deepEqual(runParams({ from_date: "2026-10-01", to_date: "  ", branch_id: "" }), { from_date: "2026-10-01" });
});

test("a consolidated print names the branch it prints at, a branched one needs no pick", () => {
  assert.equal(printAtNeeded({}), true);
  assert.equal(printAtNeeded({ branch_id: "" }), true);
  assert.equal(printAtNeeded({ branch_id: "5d1b3f6e-0000-4000-9000-000000000001" }), false);
});

test("the export name rides the code and its dates, the core's own build", () => {
  assert.equal(csvName("sales_by_day", { from_date: "2026-10-01", to_date: "2026-10-14" }), "sales_by_day_2026-10-01_2026-10-14.csv");
  assert.equal(csvName("sales_by_day", {}), "sales_by_day.csv");
});

test("the catalogue orders the way a person looks for it, new categories appear at the end", () => {
  const cat = (code) => ({ code, label: code, templates: [] });
  const ordered = orderCategories([cat("TAX"), cat("SALES"), cat("MARGIN"), cat("AR"), cat("PAYMENTS"), cat("INVENTORY"), cat("PURCHASING"), cat("DELIVERY"), cat("WARRANTY"), cat("CREDIT"), cat("COMMISSION"), cat("BRAND_NEW")]);
  assert.deepEqual(
    ordered.map((c) => c.code),
    ["SALES", "MARGIN", "AR", "PAYMENTS", "INVENTORY", "PURCHASING", "DELIVERY", "WARRANTY", "CREDIT", "TAX", "COMMISSION", "BRAND_NEW"],
  );
  assert.equal(CATEGORY_ORDER[0], "SALES");
});

test("findTemplate locates a template across the grouped catalogue", () => {
  const cats = [{ code: "SALES", label: "Sales", templates: [{ code: "sales_by_day", name: "Sales by day" }] }];
  assert.equal(findTemplate(cats, "sales_by_day")?.name, "Sales by day");
  assert.equal(findTemplate(cats, "nope"), null);
});

test("the result summary names the row count and the leading money total", () => {
  const res = {
    code: "sales_by_day",
    name: "Sales by day",
    category: "SALES",
    columns: [COL("day", "date"), { name: "total_cents", label: "Total", kind: "cents", total: true }],
    rows: [],
    rowCount: 83,
    totals: { total_cents: 123456 },
    params: {},
  };
  assert.equal(resultSummary(res), "83 rows · Total $1,234.56");
  assert.equal(resultSummary({ ...res, rowCount: 1 }), "1 row · Total $1,234.56");
  assert.equal(resultSummary({ ...res, columns: [COL("day", "date")] }), "83 rows");
  assert.equal(resultSummary(null), "");
});

test("the panes are the four the surface owns, in order", () => {
  assert.deepEqual(
    REPORTING_PANES.map((p) => p.pane),
    ["templates", "runs", "rules", "accruals"],
  );
});

test("the view opens for the guard's manager window, tier one and two, and stays closed below", () => {
  assert.equal(canOpenReporting(1), true);
  assert.equal(canOpenReporting(2), true);
  assert.equal(canOpenReporting(3), false);
  assert.equal(canOpenReporting(0), false);
});

test("commission rules are written by the guard's admin window, tier one global only", () => {
  assert.equal(canAdminCommissionRules(1, "GLOBAL"), true);
  assert.equal(canAdminCommissionRules(1, "BRANCH"), false);
  assert.equal(canAdminCommissionRules(2, "GLOBAL"), false);
  assert.equal(canAdminCommissionRules(2, "BRANCH"), false);
});

test("the rule wall's Forbidden body becomes words naming who keeps the rules", () => {
  assert.equal(commissionRuleRefusalWords("Forbidden", 403), `You may not write commission rules. ${COMMISSION_RULE_KEEPER_WORDS}`);
  assert.equal(commissionRuleRefusalWords("rep_user_id must be a uuid", 400), "rep_user_id must be a uuid");
  assert.equal(commissionRuleRefusalWords("", 500), "Try again in a moment.");
  assert.match(COMMISSION_RULE_KEEPER_WORDS, /general manager/);
});

test("a reporting refusal keeps the server's own sentence, the one word wall becomes plain words", () => {
  assert.equal(reportingRefusalWords("this report is outside your branch scope", 403), "this report is outside your branch scope");
  assert.equal(reportingRefusalWords("Forbidden", 403), "This report is beyond the catalogue: the runs and the commission register are the manager roles'.");
  assert.equal(reportingRefusalWords('unknown report parameter "branch"', 400), 'unknown report parameter "branch"');
  assert.equal(reportingRefusalWords("internal", 500), "Try again in a moment.");
});

test("a commission line that earned nothing is marked, a zero with a rate is a real zero", () => {
  assert.equal(lineIsUnpaid({ noRuleReason: "no rule covers this category" }), true);
  assert.equal(lineIsUnpaid({ rateBps: 0, commissionCents: 0 }), true);
  assert.equal(lineIsUnpaid({ rateBps: 500, commissionCents: 0 }), false);
  assert.equal(lineIsUnpaid({ rateBps: 500, commissionCents: 1234 }), false);
});

test("basis and period labels read as words", () => {
  assert.equal(basisLabel("MARGIN"), "on gross margin");
  assert.equal(basisLabel("REVENUE"), "on revenue");
  assert.equal(basisLabel("OTHER"), "OTHER");
  assert.equal(
    periodLabel({ rep_user_id: "u1", rep_name: "Outside sales (demo)", period_start: "2026-10-01", period_end: "2026-10-14" }),
    "Outside sales (demo) · 2026-10-01 to 2026-10-14",
  );
  assert.equal(
    periodLabel({ rep_user_id: "u1", rep_name: "", period_start: "2026-10-01", period_end: "2026-10-14" }),
    "u1 · 2026-10-01 to 2026-10-14",
  );
});

test("the unattributed caveat is the server's own numbers, stated, or nothing", () => {
  const periods = [{ unattributedCount: 2, unattributedCents: 12345 }];
  assert.equal(
    unattributedWords(periods),
    "$123.45 of revenue over 2 lines is not attributed to any rep (the house queue). It earns no commission and is not in the figures above.",
  );
  assert.equal(unattributedWords([{ unattributedCount: 1, unattributedCents: 500 }]), "$5.00 of revenue over 1 line is not attributed to any rep (the house queue). It earns no commission and is not in the figures above.");
  assert.equal(unattributedWords([{ unattributedCount: 0, unattributedCents: 0 }]), "");
  assert.equal(unattributedWords([]), "");
});
