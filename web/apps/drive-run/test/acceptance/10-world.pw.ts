// The Driver's world leg, and the four legs every role carries (the roadmap's
// section 2): the card, the wall, the branch and the cost. Read from the demo
// image's own core, as the rules name them.
//
// The world these legs assume is the seeded one: the demo mint row (flows.go
// and populate.go) and a run dispatched to the seeded driver on the dealer's
// local day with at least two stops, day keyed so every day holds one. A world
// without them fails these legs loudly: a run that skipped would pass
// vacuously.
import { expect, servedDay, myRuns, staff, test, shot, arriveFromFrontDoor } from "./helpers.js";

test("L-card: the front door lists the Driver, with its role code and label", async ({ page }) => {
  const flows = await page.request.get("/api/v1/demo/flows");
  expect(flows.ok()).toBe(true);
  const body = (await flows.json()) as { flows: Array<{ flow: string; role_code: string; label: string }> };
  const driver = body.flows.find((row) => row.flow === "driver");
  expect(driver, "the flows table carries the Driver's row").toBeTruthy();
  expect(driver?.role_code).toBe("driver");
  expect(driver?.label).toBe("Driver");
});

test("L-card: the card lands on its run, the driver's own, on the dealer's day", async ({ page }, testInfo) => {
  const runs = await myRuns(page);
  expect(runs.length, `a run is dispatched to the seeded driver on ${await servedDay(page)}`).toBeGreaterThanOrEqual(1);
  const run = runs[0];
  expect(run.status).toBe("DISPATCHED");
  expect(run.stops.length, "the run carries at least two stops: one to deliver, one to hand back").toBeGreaterThanOrEqual(2);
  await arriveFromFrontDoor(page);
  // The sheet renders every run the driver holds today, the dispatched run first and any planned
  // run behind it, so the stop count is the FIRST card's: the run the core answered first.
  const card = page.locator("[data-run]").first();
  await expect(card).toContainText(run.run_no);
  await expect(card.locator("[data-stop]")).toHaveCount(run.stops.length);
  await expect(page.getByTestId("header-word")).toHaveText("Driver");
  await shot(page, "10-run-sheet", testInfo);
});

test("L-wall: a surface at None for the Driver is refused by the server", async ({ page }) => {
  // The counter (counter-till none), dispatch's board reads (dispatch-board none) and the price rules.
  const run = (await myRuns(page))[0];
  const counter = await staff(page, "POST", "/api/v1/pos/counter-prices", { branch_id: run.branch_id, customer_id: "", items: [] });
  expect(counter.status).toBe(403);
  const trucks = await staff(page, "GET", `/api/v1/dispatch/trucks?branch_id=${run.branch_id}`);
  expect(trucks.status).toBe(403);
  const rules = await staff(page, "POST", "/api/v1/pricing/rules", {});
  expect(rules.status).toBe(403);
});

test("L-branch: the Driver reads its own branch only", async ({ page }) => {
  const runs = await myRuns(page);
  const branch = runs[0].branch_id as string;
  for (const run of runs) expect(run.branch_id).toBe(branch);
  const branches = await staff(page, "GET", "/api/v1/branches");
  expect(branches.status).toBe(200);
  const rows = (Array.isArray(branches.body) ? branches.body : branches.body.items) as Array<Record<string, unknown>>;
  expect(rows).toHaveLength(1);
  expect(rows[0].id).toBe(branch);
});

test("L-cost: cost and margin are hidden for the Driver: the glance is refused outright", async ({ page }) => {
  const run = (await myRuns(page))[0];
  const glance = await staff(page, "POST", "/api/v1/pos/margin-glance", { branch_id: run.branch_id, customer_id: "", customer_type: "", lines: [] });
  expect(glance.status).toBe(403);
});
