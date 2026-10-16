// WALK-C10 Manager Home Landing unit tests: the four money KPI cards in combined
// and side-by-side branch mode, the Deliveries today card with its three branch
// runs, and the Needs you, by money at stake ranked rows with inline actions.
import { test } from "node:test";
import assert from "node:assert/strict";
import { KpiDemo } from "../src/manager-api.ts";
import { renderC10Landing, renderDeliveriesCard, renderKpiCard, renderNeedsYouCard } from "../src/views.ts";

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

test("WALK-C10 KPI cards carry combined and Point Loma beside Julian figures", () => {
  const kpis = KpiDemo.figures("br-plm");
  assert.equal(kpis.length, 4);
  assert.deepEqual(
    kpis.map((k) => k.title),
    ["Sales today", "Gross margin", "AR overdue", "Cash collected today"],
  );
  const combinedHtml = flattenTemplate(renderKpiCard(kpis[0], "combined"));
  assert.match(combinedHtml, /\$48,320\.00/);
  assert.match(combinedHtml, /\$44,150\.00 same day last week, up 9\.4%/);

  const splitHtml = flattenTemplate(renderKpiCard(kpis[0], "split"));
  assert.match(splitHtml, /Point Loma/);
  assert.match(splitHtml, /\$29,870\.00/);
  assert.match(splitHtml, /Julian/);
  assert.match(splitHtml, /\$18,450\.00/);
});

test("WALK-C10 Deliveries today card renders combined 17 and split 14 with three branch runs", () => {
  const deliveries = KpiDemo.deliveries("br-plm");
  assert.equal(deliveries.count, 17);
  assert.equal(deliveries.runs.length, 3);
  assert.deepEqual(
    deliveries.runs.map((r) => `${r.runId} ${r.branch}`),
    ["R1 Point Loma", "R5 Point Loma", "J2 Julian"],
  );
  const combinedHtml = flattenTemplate(renderDeliveriesCard(deliveries, "combined"));
  assert.match(combinedHtml, />17</);
  assert.match(combinedHtml, /12 from Point Loma, 5 from Julian\. 5 done, 6 on the road, 6 still to leave\./);

  const splitHtml = flattenTemplate(renderDeliveriesCard(deliveries, "split"));
  assert.match(splitHtml, />14</);
  assert.match(splitHtml, /9 from Point Loma, 5 from Julian\. 5 done, 6 on the road, 3 still to leave\./);
});

test("WALK-C10 Needs you, by money at stake lists five ranked rows with inline actions", () => {
  const needs = KpiDemo.needs("br-plm");
  assert.equal(needs.length, 5);
  assert.deepEqual(
    needs.map((n) => n.amountLabel),
    ["$11,350.00", "$771.46", "$100.80", "$56.80", "not priced"],
  );
  const markup = flattenTemplate(renderNeedsYouCard(needs));
  assert.match(markup, /Credit hold: Fieldstone Pools and Patios/);
  assert.match(markup, /Approve credit hold/);
  assert.match(markup, /Margin floor: Q-2041, line 2/);
  assert.match(markup, /Approve margin/);
  assert.match(markup, /Issue warranty claim/);
  assert.match(markup, /Message a branch/);

  const fullLanding = flattenTemplate(renderC10Landing("combined", KpiDemo.figures("br-plm"), deliveriesForTest(), needs));
  assert.match(fullLanding, /Both branches combined/);
  assert.match(fullLanding, /Point Loma beside Julian/);
});

function deliveriesForTest() {
  return KpiDemo.deliveries("br-plm");
}
