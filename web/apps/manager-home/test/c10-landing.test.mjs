// WALK-C10 Manager Home Landing unit tests, FW1-14 rewrite: every figure the
// TODAY cards draw comes from the core, and a card the core cannot answer says
// "Not connected yet" with no figure. The old KpiDemo constants are gone; these
// legs pin the mapping from the core's own reads (the viz series, the dispatch
// board, the approvals queue) to the cards, so a hardcoded figure can not come
// back unnoticed.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  LANDING_NOT_CONNECTED,
  landingKpisFrom,
  landingDeliveriesFrom,
  landingNeedsFrom,

} from "../src/manager-api.ts";
import { renderKpiCard, renderDeliveriesCard, renderNeedsYouCard } from "../src/views.ts";


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

// The core's viz answer for a controller: sales by branch, margin in basis
// points (the branch split carries NO total, rates are never summed; the
// weighted all branches rate rides the none composition), the AR aging buckets
// and the past-due-by-branch split. Numbers the core computed, not a board's.
const BRANCH_NAMES = new Map([["TRT", "Trenton"], ["KGN", "Kingston"]]);
const CORE_TILES = [
  {
    tile_id: "viz.sales.today-branch-split", state: "ok", total: 4_832_00,
    points: [
      { label: "TRT", number: 2_987_00 },
      { label: "KGN", number: 1_845_00 },
    ],
  },
  {
    tile_id: "viz.margin.gross_bps-branch-list", state: "ok", total: null,
    points: [
      { label: "TRT", number: 3_240 },
      { label: "KGN", number: 3_070 },
    ],
  },
  {
    // The provider's own weighted aggregate: 22000 margin cents on 70000 sales
    // cents across the two branches, 3120 bps. NOT 3240+3070=6310, the sum the
    // old code showed, and NOT 3155, the mean.
    tile_id: "viz.margin.gross_bps-none-list", state: "ok", total: 3_120,
    points: [{ label: "All branches", number: 3_120 }],
  },
  {
    tile_id: "viz.ar.aging-bucket-split", state: "ok", total: 96_240_00,
    points: [
      { label: "Current", number: 61_300_00 },
      { label: "1-30", number: 18_400_00 },
      { label: "31-60", number: 9_000_00 },
      { label: "60+", number: 7_540_00 },
    ],
  },
  {
    tile_id: "viz.ar.aging-branch-split", state: "ok", total: null,
    points: [
      { label: "TRT", number: 18_400_00 },
      { label: "KGN", number: 16_540_00 },
    ],
  },
];

test("the KPI cards carry the core's figures: sales, margin, and AR past due", () => {
  const kpis = landingKpisFrom(CORE_TILES, BRANCH_NAMES);
  assert.deepEqual(
    kpis.map((k) => k.title),
    ["Sales today", "Gross margin", "Receivables past due", "Cash collected today"],
  );

  const sales = kpis[0];
  assert.equal(sales.connected, true);
  assert.equal(sales.value, "$4,832.00");
  // Branch rows name the branches, never their three letter codes.
  assert.deepEqual(
    sales.branches.map((b) => `${b.label} ${b.value}`),
    ["Trenton $2,987.00", "Kingston $1,845.00"],
  );
  const salesHtml = flattenTemplate(renderKpiCard(sales, "combined"));
  assert.match(salesHtml, /\$4,832\.00/);
  assert.match(salesHtml, /Counter sales committed today/);

  // FROM-PRINCIPAL 132: the all branches gross margin is the core's own
  // weighted ratio (3120 bps), never the sum of the branch rates (6310) and
  // never their mean (3155).
  const margin = kpis[1];
  assert.equal(margin.connected, true);
  assert.equal(margin.value, "31.2%");
  const marginSplit = flattenTemplate(renderKpiCard(margin, "split"));
  assert.match(marginSplit, /Trenton/);
  assert.match(marginSplit, /32\.4%/);
  assert.match(marginSplit, /Kingston/);
  assert.doesNotMatch(marginSplit, /TRT|KGN/);

  // Past due is every bucket but Current, the core's own arithmetic, and the
  // card says what it covers rather than "AR overdue". Split rows are the
  // branch arm's past due per branch, and they tie with the headline.
  const pastDue = kpis[2];
  assert.equal(pastDue.connected, true);
  assert.equal(pastDue.value, "$34,940.00");
  assert.deepEqual(
    pastDue.branches.map((b) => `${b.label} ${b.value}`),
    ["Trenton $18,400.00", "Kingston $16,540.00"],
  );
});

test("a margin card with branch points but no aggregate says Not connected yet", () => {
  // The provider answered the branch series but no weighted aggregate: the
  // card shows words, never the sum or the mean of the branch rates.
  const kpis = landingKpisFrom(CORE_TILES.filter((t) => t.tile_id !== "viz.margin.gross_bps-none-list"), BRANCH_NAMES);
  assert.equal(kpis[1].connected, false);
  const markup = flattenTemplate(renderKpiCard(kpis[1], "combined"));
  assert.match(markup, new RegExp(LANDING_NOT_CONNECTED));
  assert.doesNotMatch(markup, /%/);
});

test("a card the core cannot answer says Not connected yet, with no figure", () => {
  const kpis = landingKpisFrom(CORE_TILES, BRANCH_NAMES);
  const cash = kpis[3];
  assert.equal(cash.connected, false);
  const markup = flattenTemplate(renderKpiCard(cash, "combined"));
  assert.match(markup, new RegExp(LANDING_NOT_CONNECTED));
  assert.doesNotMatch(markup, /\$/);

  // No viz answer at all: every card is honest, none invents a number.
  const empty = landingKpisFrom([], BRANCH_NAMES);
  assert.equal(empty.length, 4);
  assert.ok(empty.every((k) => k.connected === false));
  const tilesAbsent = landingKpisFrom([
    { tile_id: "viz.sales.today-branch-split", state: "error", total: null, points: [] },
  ]);
  assert.equal(tilesAbsent[0].connected, false);
});

test("the stops card counts the dispatch board's own runs and stops, under its own words", () => {
  const branches = [
    { id: "b-trt", code: "TRT", name: "Trenton" },
    { id: "b-kgn", code: "KGN", name: "Kingston" },
  ];
  const board = (runId, branchId, stops) => ({
    branch_id: branchId,
    date: "2026-10-26",
    runs: [{
      id: `r-${runId}`, run_no: runId, status: stops.every((s) => s.delivered) ? "COMPLETED" : "DISPATCHED",
      driver_id: "", driver_name: "A Driver", truck_id: "", truck_code: "", stop_count: stops.length,
      created_at: "2026-10-26T10:40:00Z", dispatched_at: "2026-10-26T11:40:00Z", completed_at: null, reopened_at: "",
    }],
    stops: stops.map((s, i) => ({
      id: `s-${runId}-${i}`, run_id: `r-${runId}`, stop_no: i + 1,
      status: s.delivered ? "DELIVERED" : s.enRoute ? "EN_ROUTE" : s.cancelled ? "CANCELLED" : "READY",
      loaded_at: "2026-10-26T10:00:00Z",
      en_route_at: s.enRoute || s.delivered ? "2026-10-26T11:40:00Z" : null,
      delivered_at: s.delivered ? "2026-10-26T12:40:00Z" : null,
      reopened_at: "",
    })),
    unassigned: [],
  });
  const deliveries = landingDeliveriesFrom(branches, [
    board("TRT-1", "b-trt", [{ delivered: true, enRoute: false }, { delivered: false, enRoute: true }, { delivered: false, enRoute: false }]),
    board("KGN-2", "b-kgn", [{ delivered: false, enRoute: true }, { delivered: false, enRoute: false }, { cancelled: true, enRoute: false }]),
  ]);
  assert.notEqual(deliveries, null);
  // A cancelled stop is not a stop the day still holds: the core's own measure excludes
  // CANCELLED, and the card counts the board the same way.
  assert.equal(deliveries.count, 5);
  assert.deepEqual(
    deliveries.branches.map((b) => `${b.label} ${b.count}`),
    ["Trenton 3", "Kingston 2"],
  );
  assert.match(deliveries.summary, /1 done, 2 on the road, 2 still to leave/);
  assert.equal(deliveries.runs.length, 2);
  assert.match(deliveries.runs[0].stops, /1 of 3 stops done/);
  // The singular for 1: "0 of 1 stop done", never "0 of 1 stops done".
  const single = landingDeliveriesFrom(branches, [board("TRT-9", "b-trt", [{ delivered: false, enRoute: false }])]);
  assert.equal(single.runs[0].stops, "0 of 1 stop done");
  const markup = flattenTemplate(renderDeliveriesCard(deliveries, "combined"));
  assert.match(markup, />5</);
  assert.match(markup, /TRT-1 Trenton/);
  // FROM-PRINCIPAL 127: the hero's deliveries.today measure counts the day's stops still to
  // do, this card counts the day's whole book of stops. Different measures never share words.
  assert.match(markup, /Stops today/);
  assert.doesNotMatch(markup, /Deliveries today/);
  // No invented run, no borrowed count.
  assert.doesNotMatch(markup, /R1|left 6:40 AM/);

  const none = landingDeliveriesFrom(branches, []);
  assert.equal(none, null);
});

test("the needs card ranks the caller's pending approvals by the money at stake", () => {
  const rows = [
    {
      id: "ap-1", branch_id: "b-trt", policy_type: "MARGIN_BELOW_MINIMUM",
      detail: "Quote Q-2041 line 2 is below the margin floor", subject_ref: "", amount_cents: 7_714_6, status: "PENDING",
    },
    {
      id: "ap-2", branch_id: "b-kgn", policy_type: "CREDIT_LIMIT_EXCEEDED",
      detail: "Order SO-1049 exceeds the credit limit", subject_ref: "", amount_cents: 11_350_00, status: "PENDING",
    },
    {
      id: "ap-3", branch_id: "b-trt", policy_type: "COD_CONSTRAINT",
      detail: "A cash on demand account has an unpaid balance", subject_ref: "", amount_cents: 0, status: "PENDING",
    },
    {
      id: "ap-4", branch_id: "b-trt", policy_type: "MARGIN_BELOW_MINIMUM",
      detail: "Already decided", subject_ref: "", amount_cents: 5_00, status: "APPROVED",
    },
  ];
  const needs = landingNeedsFrom(rows);
  assert.deepEqual(
    needs.map((n) => n.id),
    ["ap-2", "ap-1", "ap-3"],
  );
  assert.equal(needs[0].amountLabel, "$11,350.00");
  assert.equal(needs[2].amountLabel, "Not priced");
  assert.equal(needs[0].targetView, "approvals");
  const markup = flattenTemplate(renderNeedsYouCard(needs));
  assert.match(markup, /Q-2041/);
  assert.match(markup, /\$11,350\.00/);
  // The invented board rows are gone: no Fieldstone, no invented warranty claim.
  assert.doesNotMatch(markup, /Fieldstone|RT-1017/);

  assert.deepEqual(landingNeedsFrom([]), []);
});
