// Gap 1 of the per role homes (read-2d): every needs row and every widget card
// links to the live app that owns it, and says "not on this box yet" only where
// no app exists. Pure decisions, no browser.
import { test } from "node:test";
import assert from "node:assert/strict";
import { LIVE_APP_HREFS, isLiveHref, needsLink, widgetLink } from "../src/landing-links.ts";
import { needsRowTarget } from "../src/manager-logic.ts";

const row = (source_key, view = "", screen = "") => ({ source_key, what: "", count: 1, age: "", severity: "good", module: "", unit: "", screen, view, action: "" });

test("the closed table names exactly the four live apps the front door serves", () => {
  assert.deepEqual(
    Object.fromEntries(Object.entries(LIVE_APP_HREFS)),
    { "Money Desk": "/money-desk/", "Buying and Stock": "/buying-and-stock/", "Counter Till": "/till/", "Yard Pick": "/yard/" },
  );
  assert.equal(isLiveHref("/till/"), true);
  assert.equal(isLiveHref("https://elsewhere.example/"), false);
  assert.equal(isLiveHref("/dispatch/"), false);
});

test("needs rows go to the app that owns the source, dispatch stays coming", () => {
  assert.deepEqual(needsLink("ar.escalation"), { app: "Money Desk", href: "/money-desk/" });
  assert.deepEqual(needsLink("pos.parked"), { app: "Counter Till", href: "/till/" });
  assert.deepEqual(needsLink("tasks.stale_picks"), { app: "Yard Pick", href: "/yard/" });
  assert.deepEqual(needsLink("weights.pending"), { app: "Yard Pick", href: "/yard/" });
  assert.equal(needsLink("dispatch.unassigned"), null);
  assert.equal(needsLink("dispatch.due_to_commit"), null);
  assert.equal(needsLink("nothing.known"), null);
});

test("needsRowTarget names a view, a live app, or the honest coming", () => {
  assert.deepEqual(needsRowTarget(row("tasks.open_shortages")), { view: "shortages" });
  assert.deepEqual(needsRowTarget(row("ar.escalation", "Escalation queue")), { view: null, app: "Money Desk", href: "/money-desk/" });
  assert.deepEqual(needsRowTarget(row("pos.parked")), { view: null, app: "Counter Till", href: "/till/" });
  assert.deepEqual(needsRowTarget(row("tasks.stale_picks")), { view: null, app: "Yard Pick", href: "/yard/" });
  const dispatch = needsRowTarget(row("dispatch.unassigned", "Staging · unassigned", "dispatch"));
  assert.equal(dispatch.view, null);
  assert.equal(dispatch.screen, "Staging · unassigned");
  assert.equal("href" in dispatch, false);
});

test("a widget card links by the module that measures it", () => {
  assert.deepEqual(widgetLink({ module: "billing" }), { kind: "app", app: "Money Desk", href: "/money-desk/" });
  assert.deepEqual(widgetLink({ module: "pos" }), { kind: "app", app: "Counter Till", href: "/till/" });
  assert.deepEqual(widgetLink({ module: "tasks" }), { kind: "app", app: "Yard Pick", href: "/yard/" });
  assert.deepEqual(widgetLink({ module: "inventory" }), { kind: "app", app: "Buying and Stock", href: "/buying-and-stock/" });
  assert.deepEqual(widgetLink({ module: "purchasing" }), { kind: "app", app: "Buying and Stock", href: "/buying-and-stock/" });
  assert.deepEqual(widgetLink({ module: "warranty" }), { kind: "view", view: "warranty" });
  assert.deepEqual(widgetLink({ module: "dispatch" }), { kind: "coming" });
  assert.deepEqual(widgetLink({ module: "order" }), { kind: "coming" });
  assert.deepEqual(widgetLink({ module: "" }), { kind: "coming" });
});
