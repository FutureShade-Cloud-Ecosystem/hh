// HH Drive, sub item 4b: the end of the run. The run closes only when every
// stop has an answer a driver records, and the screen names the stop that
// stands in the way.
import { test, expect, deliverStop, driverHandoff, failStop, resetStub, stubLog, useScenario } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(driverHandoff);
});

test("the end of the run is one tap from the run sheet and, with stops open, names the one in the way", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "End of run" }).click();
  await expect(page.getByRole("heading", { name: "End of run" })).toBeVisible();
  const card = page.locator("[data-close-run]");
  await expect(card).toContainText("3 stops left · 0 delivered");
  await expect(card.locator("[data-close-blocked]")).toContainText(
    "Stop 1 · Northline Hardscapes Ltd (demo) is not loaded yet. Every stop has to be delivered or marked not delivered before the run can be closed.",
  );
  await expect(page.getByRole("button", { name: "Complete run" })).toHaveCount(0);
});

test("with every stop answered the run closes, and the sheet and the end screen both say so", async ({ page, request }) => {
  await page.goto("/");
  await deliverStop(page, 1);
  await deliverStop(page, 2);
  await failStop(page, 3, "Site not ready");
  await page.getByRole("button", { name: "End of run" }).click();
  const card = page.locator("[data-close-run]");
  await expect(card).toContainText("Run finished · 2 delivered · 1 could not be delivered");
  await expect(card.locator("[data-recap-stop]")).toHaveCount(3);
  await page.getByRole("button", { name: "Complete run" }).click();
  await expect(page.getByRole("status")).toContainText("RUN-R5 is closed. Thanks: that is the day.");
  await expect(card.locator("[data-close-blocked]")).toContainText("This run is closed. Nothing else is needed from you today.");
  await expect(page.getByRole("button", { name: "Complete run" })).toHaveCount(0);
  const closes = (await stubLog(request)).filter((c) => c.path.endsWith("/complete"));
  expect(closes).toHaveLength(1);
  expect(closes[0].method).toBe("POST");
  await page.getByRole("button", { name: "Back to my run" }).click();
  await expect(page.locator("[data-progress]")).toHaveText("Run finished · 2 delivered · 1 could not be delivered");
});

test("a stop dispatch cancelled blocks a driver's close and says who closes it", async ({ page, request }) => {
  await useScenario(request, "has-cancelled");
  await page.goto("/");
  await deliverStop(page, 1);
  await deliverStop(page, 2);
  await page.getByRole("button", { name: "End of run" }).click();
  await expect(page.locator("[data-close-blocked]")).toContainText(
    "Stop 3 · County of Hastings (demo) is cancelled, and that is not an outcome a driver records. Ask dispatch to close this run.",
  );
  await expect(page.getByRole("button", { name: "Complete run" })).toHaveCount(0);
});

test("when the core refuses the close the driver reads its words and the run stays open", async ({ page, request }) => {
  await page.goto("/");
  await deliverStop(page, 1);
  await deliverStop(page, 2);
  await failStop(page, 3, "Site not ready");
  await useScenario(request, "close-refused");
  await page.getByRole("button", { name: "End of run" }).click();
  await page.getByRole("button", { name: "Complete run" }).click();
  await expect(page.getByRole("alert")).toContainText("Stop 2 (SO-1002) is on the truck, every stop must be delivered or marked not delivered before this run can be closed.");
  await expect(page.getByRole("button", { name: "Complete run" })).toBeVisible();
});

test("Back from the end screen returns to the run sheet", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "End of run" }).click();
  await page.getByRole("button", { name: "Back to my run" }).click();
  await expect(page.getByRole("heading", { name: "My run" })).toBeVisible();
});
