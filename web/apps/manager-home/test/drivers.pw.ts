// Manager Home, item 2E: the Drivers view. The landing's chip opens the
// board, the rows group by the day's four states, each row carries the
// driver's role label, truck, run, place in the day and change line, and the
// empty and refused days speak in the board's own words. Read only: nothing
// here moves a stop.
import { test, expect } from "@playwright/test";
import { managerHandoff, resetStub, useScenario } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

async function openDrivers(page: import("@playwright/test").Page): Promise<void> {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator(".view-link", { hasText: "Drivers" }).click();
  await expect(page.getByRole("heading", { name: "Drivers" })).toBeVisible();
}

test("the board groups the day's rows by state, on a stop first and done last", async ({ page }) => {
  await openDrivers(page);
  await expect(page.locator("[data-group='on-stop'] .driver-row")).toHaveCount(1);
  await expect(page.locator("[data-group='between-stops'] .driver-row")).toHaveCount(1);
  await expect(page.locator("[data-group='not-started'] .driver-row")).toHaveCount(1);
  await expect(page.locator("[data-group='done'] .driver-row")).toHaveCount(1);
  await expect(page.locator("[data-test='drivers-count']")).toHaveText("4 runs on the board today");
  await expect(page.locator("[data-group='on-stop'] .eyebrow")).toHaveText("En route · 1");
  await expect(page.locator("[data-group='between-stops'] .eyebrow")).toHaveText("Next stop not started · 1");
});

test("a row reads as the driver's day: role label, truck, run, place, status word, change line", async ({ page }) => {
  await openDrivers(page);
  const onStop = page.locator("[data-group='on-stop'] .driver-row").first();
  await expect(onStop.locator(".driver")).toHaveText("Driver persona (demo)");
  await expect(onStop.locator(".who .detail")).toContainText("RUN-1");
  await expect(onStop.locator(".who .detail")).toContainText("T1");
  await expect(onStop.locator(".state-word")).toHaveText("En route");
  await expect(onStop.locator("[data-test='driver-place']")).toContainText("stop 2 of 3");
  await expect(onStop.locator("[data-test='driver-place']")).toContainText("1 done");
  await expect(onStop.locator("[data-test='driver-place']")).toContainText("2 left");
  await expect(onStop.locator("[data-test='driver-since']")).toContainText("last change");
});

test("a planned run whose own truck code is empty takes the registry's code", async ({ page }) => {
  await openDrivers(page);
  const planned = page.locator("[data-group='not-started'] .driver-row").first();
  await expect(planned.locator(".driver")).toHaveText("Driver three (demo)");
  await expect(planned.locator(".who .detail")).toContainText("T3");
  await expect(planned.locator(".state-word")).toHaveText("Ready");
});

test("the roster line counts the drivers no run holds, and never the inactive", async ({ page }) => {
  await openDrivers(page);
  await expect(page.locator("[data-test='drivers-idle']")).toHaveText(
    "1 of the 5 drivers on the roster holds no run today.",
  );
});

test("refresh re-reads the board, and an emptied day says so in the board's own words", async ({ request, page }) => {
  await openDrivers(page);
  await expect(page.locator("[data-test='drivers-count']")).toBeVisible();
  await useScenario(request, "no-runs");
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.locator("[data-test='drivers-empty']")).toHaveText("No runs on the board today");
});

test("a day with no runs from the start opens on the board's empty words", async ({ request, page }) => {
  await useScenario(request, "no-runs");
  await openDrivers(page);
  await expect(page.locator("[data-test='drivers-empty']")).toHaveText("No runs on the board today");
});

test("a board the gate refuses shows the core's own word, never a paraphrase", async ({ request, page }) => {
  await useScenario(request, "drivers-refused");
  await openDrivers(page);
  await expect(page.locator("[data-test='drivers-refusal']")).toHaveText("Forbidden");
});
