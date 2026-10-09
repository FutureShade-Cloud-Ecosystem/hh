// Yard Pick, sub item 5: stage the pick. The location is required (a staged
// pick nobody can find is worse than an unstaged one), the ref is derived
// when the picker does not type one, and the counter is told it is ready.
import { test, expect } from "@playwright/test";
import { resetStub, yardHandoff } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

/** Opens the claimed pick, starts it, and records a full pick: ready to stage. */
async function readyToStage(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-pick-card]", { hasText: "SO-1040" }).getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1040");
  await page.getByRole("button", { name: "Start picking" }).click();
  await page.locator("[data-line]", { hasText: "Granular A gravel" }).locator(".line-row").click();
  await page.getByRole("button", { name: /^All \d+/ }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("pulled in full");
}

test("the stage form demands a spot before it will stage", async ({ page }) => {
  await readyToStage(page);
  await expect(page.locator("[data-stage-form]")).toContainText("Where did you put it?");
  await page.getByRole("button", { name: "Stage", exact: true }).click();
  await expect(page.locator("[data-error]")).toContainText("Where did you put it? A stage location is required.");
});

test("staging parks the pick with a derived ref, and the counter is told", async ({ page }) => {
  await readyToStage(page);
  await page.getByLabel("The stage location").fill("Bay 3");
  await page.getByRole("button", { name: "Stage", exact: true }).click();
  await expect(page.locator("[data-notice]")).toContainText("Staged at Bay 3, ref STG-");
  await expect(page.locator("[data-notice]")).toContainText("The counter has been told it is ready.");
  await expect(page.locator("[data-staged]")).toContainText("Staged at Bay 3");
  await expect(page.locator("[data-staged]")).toContainText(/Ref STG-[A-HJ-NP-Z2-9]{6}/);
  await expect(page.locator("[data-task-status]")).toHaveText("Staged");
  // The board agrees.
  await page.getByRole("button", { name: "Back to the queue" }).click();
  await expect(page.locator("[data-pick-card]", { hasText: "SO-1040" }).locator("[data-task-status]")).toHaveText("Staged");
});

test("a staged pick offers no stage door again, and the record stays readable", async ({ page }) => {
  await readyToStage(page);
  await page.getByLabel("The stage location").fill("Bay 3");
  await page.getByRole("button", { name: "Stage", exact: true }).click();
  await expect(page.locator("[data-stage-form]")).toHaveCount(0);
  await expect(page.locator("[data-pick-summary]")).toContainText("pulled in full");
});
