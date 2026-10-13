// Yard Pick, sub item 3: a pick in progress. Record what was pulled, line
// by line, with the reason a short line needs, and change the skid count on
// the skid stepper with a reason.
import { test, expect } from "@playwright/test";
import { resetStub, stubLog, touchTicket, yardHandoff } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

/** Opens the claimed pick and starts it: the state the record doors need. */
async function openInProgress(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-pick-card]", { hasText: "SO-1040" }).getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1040");
  await page.getByRole("button", { name: "Start picking" }).click();
  await expect(page.locator("[data-task-status]")).toHaveText("Picking");
}

test("a claimed pick opens, starts, and shows its pull list", async ({ page }) => {
  await openInProgress(page);
  await expect(page.locator("[data-pull-list]")).toContainText("Pull list · 4 lines");
  await expect(page.locator("[data-line]")).toHaveCount(4);
  await expect(page.locator("[data-line]", { hasText: "Class 2 base rock gravel" })).toContainText("10 TONNE");
  await expect(page.getByRole("button", { name: "Save pick" })).toBeVisible();
});

test("a line opens the quantity editor: steppers, All, None, and the number holds", async ({ page }) => {
  await openInProgress(page);
  await page.locator("[data-line]", { hasText: "Class 2 base rock gravel" }).locator(".line-row").click();
  await expect(page.locator("[data-qty-edit]")).toBeVisible();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await expect(page.locator("[data-field=qty]")).toHaveValue("2");
  await page.getByRole("button", { name: "None" }).click();
  await expect(page.locator("[data-field=qty]")).toHaveValue("0");
  await page.getByRole("button", { name: "All 10" }).click();
  await expect(page.locator("[data-field=qty]")).toHaveValue("10");
  // The number holds through a re-render that opens another line.
  await page.locator("[data-line]", { hasText: "Geotextile fabric" }).locator(".line-row").click();
  await page.locator("[data-line]", { hasText: "Class 2 base rock gravel" }).locator(".line-row").click();
  await expect(page.locator("[data-field=qty]")).toHaveValue("10");
});

test("recording a full pick says so, and the line carries its tick", async ({ page }) => {
  await openInProgress(page);
  for (const what of ["Class 2 base rock gravel", "Granular B gravel", "Geotextile fabric", "Corrugated pipe"]) {
    await page.locator("[data-line]", { hasText: what }).locator(".line-row").click();
    await page.getByRole("button", { name: /^All \d+/ }).click();
  }
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("All 4 lines pulled in full.");
  await expect(page.locator("[data-pick-summary]")).toContainText("All 4 lines pulled in full.");
});

test("a short line needs its reason before the save, and the server backorders it", async ({ page }) => {
  await openInProgress(page);
  await page.locator("[data-line]", { hasText: "Class 2 base rock gravel" }).locator(".line-row").click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("button", { name: "Add one on" }).click();
  // Four of ten pulled: the reason rows are offered and the save is refused without one.
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-error]")).toContainText("A short line needs the reason.");
  await page.getByRole("radio", { name: "Not enough on the yard" }).click();
  await page.getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Picked 4 of 10 TONNE. The rest (6 TONNE) is on backorder");
  const line = page.locator("[data-line]", { hasText: "Class 2 base rock gravel" });
  await expect(line).toContainText("6 TONNE on backorder");
  // Words only on the line: the core's code never reaches a picker.
  await expect(line).toContainText("Not enough on the yard");
  await expect(line).not.toContainText("NOT_IN_YARD");
  // The name still reads on one row: the figures do not squeeze it to a column.
  await expect(line.locator(".line-desc")).toBeVisible();
});

test("a quantity above what was ordered is refused in the server's own words", async ({ page }) => {
  await openInProgress(page);
  await page.locator("[data-line]", { hasText: "Geotextile fabric" }).locator(".line-row").click();
  await page.locator("[data-field=qty]").fill("3");
  await page.getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  // The record goes through the outbox (the core's pick record door), the
  // picker is connected so it is sent at once, and the refusal is the
  // server's sentence, right there (and a Not accepted card, to dismiss).
  await expect(page.locator("[data-error]")).toContainText("that is more than was ordered, the yard cannot raise a quantity");
  await expect(page.locator("[data-pick-pending]")).toHaveCount(0);
});

test("the skid stepper says Not counted until it is changed, then changes with a reason", async ({ page }) => {
  await openInProgress(page);
  await expect(page.locator("[data-skids]")).toBeVisible();
  // The honest unknown is words, not dots and not a confident zero.
  await expect(page.locator("[data-skid-count]")).toHaveText("Not counted");
  // Nothing to save yet: the reason rows are not even offered.
  await expect(page.locator("[data-skids]").getByRole("radio")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Save skid count" })).toHaveCount(0);
  await page.getByRole("button", { name: "One skid more" }).click();
  await expect(page.locator("[data-skid-count]")).toHaveText("1");
  await page.getByRole("button", { name: "Save skid count" }).click();
  await expect(page.locator("[data-error]")).toContainText("Give the reason for the skid count.");
  await page.getByRole("radio", { name: "Count corrected" }).click();
  await page.getByRole("button", { name: "One skid more" }).click();
  await expect(page.locator("[data-skid-count]")).toHaveText("2");
  await page.getByRole("button", { name: "Save skid count" }).click();
  await expect(page.locator("[data-notice]")).toContainText("SO-1040 now holds 2 skids.");
  // A change names both numbers, and the deposit figures never show.
  await page.getByRole("radio", { name: "Skids condensed" }).click();
  for (let i = 0; i < 3; i += 1) await page.getByRole("button", { name: "One skid more" }).click();
  await page.getByRole("button", { name: "Save skid count" }).click();
  await expect(page.locator("[data-notice]")).toContainText("SO-1040 changed from 2 to 5 skids.");
});

test("the skids sit with the pull list, and the shortages come before the stage form", async ({ page }) => {
  await openInProgress(page);
  // On SO-1040 there is no shortage and no stage above the skids; the shape is
  // proved where it bit: SO-1041 carries an open shortage.
  await page.getByRole("button", { name: "Back to the queue" }).click();
  await page.locator("[data-pick-card]", { hasText: "SO-1041" }).getByRole("button", { name: "Open" }).click();
  const shortages = page.locator("[data-shortages]");
  const stage = page.locator("[data-stage-form]");
  await expect(shortages).toBeVisible();
  await expect(stage).toBeVisible();
  const shortagesAfter = await shortages.boundingBox();
  const stageAfter = await stage.boundingBox();
  expect(shortagesAfter?.y, "the shortages read before the stage form").toBeLessThan(stageAfter?.y ?? Number.MAX_VALUE);
  const pull = page.locator("[data-pull-list]");
  const skids = page.locator("[data-skids]");
  const pullBox = await pull.boundingBox();
  const skidsBox = await skids.boundingBox();
  expect(skidsBox?.y, "the skid stepper sits with the pull list").toBeGreaterThan(pullBox?.y ?? 0);
  const recordDoor = page.locator("[data-record-door]");
  const recordBox = await recordDoor.boundingBox();
  expect(skidsBox?.y).toBeLessThan(recordBox?.y ?? Number.MAX_VALUE);
});

test("the record goes to the server as one pick_tasks op, in the ticket's own order, with the door's nine line legs", async ({ page, request }) => {
  await openInProgress(page);
  await page.locator("[data-line]", { hasText: "Class 2 base rock gravel" }).locator(".line-row").click();
  await page.getByRole("button", { name: /^All \d+/ }).click();
  await page.locator("[data-line]", { hasText: "Geotextile fabric" }).locator(".line-row").click();
  await page.getByRole("button", { name: /^All \d+/ }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("pulled");
  await expect(page.locator("[data-pick-pending]")).toHaveCount(0);
  // The record is one op on the pick_tasks sync door: the row address and the
  // lines, in the ticket's own order, and no call on the old direct route.
  const log = await stubLog(request);
  expect(log.filter((c) => c.path.endsWith("/lines"))).toHaveLength(0);
  const ops = log
    .filter((c) => c.path === "/api/v1/sync/pick_tasks" && c.method === "POST")
    .flatMap((c) => ((c.body as { ops?: Array<{ payload: Record<string, unknown> }> } | null)?.ops ?? []))
    .filter((op) => "picked" in op.payload);
  expect(ops).toHaveLength(1);
  expect(Object.keys(ops[0]!.payload).sort()).toEqual(["id", "picked"]);
  expect(ops[0]!.payload.picked).toEqual([
    { order_line_no: 1, product_id: "1a000000-0000-4000-9000-0000000000c1", sku: "GRAN-A", description: "Class 2 base rock gravel, bulk", qty_ordered: "10.0000", qty_picked: "10", uom: "TONNE", short_reason: "", short_note: "" },
    { order_line_no: 3, product_id: "1a000000-0000-4000-9000-0000000000c3", sku: "FAB-GEO", description: "Geotextile fabric, 4 by 300 ft", qty_ordered: "2.0000", qty_picked: "2", uom: "ROLL", short_reason: "", short_note: "" },
  ]);
});

test("a connected record the server superseded is an error in words, never a saved notice", async ({ page, request }) => {
  // SO-1041 is in progress already, so the device has written nothing to it: its record names the pulled seq as its base.
  // The boot pull must have rested first, or the record would go blind behind an in-flight cycle and meet no stale check.
  await page.goto("/");
  await expect(page.locator("[data-staleness]")).toContainText("Pulled");
  await page.locator("[data-pick-card]", { hasText: "SO-1041" }).getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1041");
  await expect(page.locator("[data-task-status]")).toHaveText("Picking");
  await page.waitForLoadState("networkidle");
  // The desk changes the OPEN ticket after the device's last pull (the seq moves, the pick stays open).
  await touchTicket(request, "0a000000-0000-4000-9000-000000000041");
  await page.locator("[data-line]", { hasText: "Flagstone" }).locator(".line-row").click();
  await page.getByRole("button", { name: /^All \d+/ }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-error]")).toContainText("changed on the server first");
  await expect(page.locator("[data-error]")).toContainText("not saved");
  await expect(page.locator("[data-notice]")).toHaveCount(0);
});
