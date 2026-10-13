// Yard Pick, sub item 4: the short pick sheet. A short pick recorded with a
// reason backorders the line and the order's rep is told (Phase 0 item 6),
// and the sheet sends the deliberate shortage to the counter. The resolve
// carries the shortage resolvers list's wall in plain words.
import { test, expect } from "@playwright/test";
import { repNotices, resetStub, useScenario, yardHandoff } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

/** Opens the pick that is already in progress (SO-1041). */
async function openInProgress(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-pick-card]", { hasText: "SO-1041" }).getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1041");
  await expect(page.locator("[data-task-status]")).toHaveText("Picking");
}

test("a short pick with a reason backorders the line and the rep's read holds the notice", async ({ page, request }) => {
  await openInProgress(page);
  await page.locator("[data-line]", { hasText: "Flagstone" }).locator(".line-row").click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("radio", { name: "Damaged stock" }).click();
  await page.getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Picked 2 of 46 SQFT. The rest (44 SQFT) is on backorder and the order's rep has been told.");
  // The rep's API read holds the notice, in the core's own sentence shape.
  const notices = await repNotices(request);
  const notice = notices.find((n) => n.topic === "yard.short_pick" && n.order_no === "SO-1041");
  expect(notice).toBeDefined();
  expect(notice?.body).toContain("Short pick on order SO-1041, line 1 (Flagstone, random, mixed grey)");
  expect(notice?.body).toContain("44.0000 SQFT on backorder");
  expect(notice?.body).toContain("Reason: Damaged");
});

test("the shortage sheet is the board's: the asked and short figures, radio rows, a labelled note, the banner", async ({ page }) => {
  await openInProgress(page);
  await page.getByRole("button", { name: "Report a shortage" }).click();
  const sheet = page.locator("[data-shortage-sheet]");
  await expect(sheet).toContainText("Short pick · line 1");
  await expect(sheet).toContainText("Flagstone, random, mixed grey");
  // The two labelled figures on one row: what was asked, and the short tile.
  await expect(sheet.locator("[data-tile=asked]")).toContainText("ASKED");
  await expect(sheet.locator("[data-tile=asked]")).toContainText("46 SQFT");
  await expect(sheet.locator("[data-tile=short]")).toContainText("SHORT");
  await page.getByLabel("How many are short").fill("10");
  await expect(sheet.locator("[data-tile=short]")).toContainText("10 SQFT");
  // The reasons are big radio rows, in words.
  await expect(sheet.getByRole("radio")).toHaveCount(5);
  await expect(sheet.getByRole("radio", { name: "Not enough on the yard" })).toBeVisible();
  // The note field is labelled on the sheet itself.
  await expect(sheet.getByPlaceholder("Note for the counter (optional)")).toBeVisible();
  // The banner says what sending does, in the board's words.
  await expect(sheet.locator("[data-sheet-banner]")).toContainText("When you send, the line is backordered and the order's rep is told.");
  await page.getByRole("button", { name: "Report short pick" }).click();
  await expect(page.locator("[data-error]")).toContainText("Give the reason.");
  await page.getByRole("radio", { name: "Not enough on the yard" }).click();
  await page.getByRole("button", { name: "Report short pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Shortage sent: 10 SQFT of Flagstone, random, mixed grey. The counter has been told.");
  await expect(page.locator("[data-shortage]", { hasText: "Short 10 SQFT" })).toBeVisible();
});

test("the open shortage is on the pick and can be settled with a stated resolution", async ({ page }) => {
  await openInProgress(page);
  const row = page.locator("[data-shortage]", { hasText: "22 SQFT" });
  await expect(row).toContainText("Open: the counter has not settled it.");
  await row.getByRole("button", { name: "Mark it settled" }).click();
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator("[data-error]")).toContainText("Say how it was settled.");
  await page.getByLabel("How the shortage was settled").fill("Substituted mixed blue from Yard C");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Shortage cleared: Substituted mixed blue from Yard C");
  await expect(page.locator("[data-shortage]", { hasText: "22 SQFT" })).toContainText("Cleared: Substituted mixed blue from Yard C");
});

test("the shortage resolvers wall parks the scope and the stall says so in words", async ({ page, request }) => {
  await openInProgress(page);
  await page.locator("[data-shortage]", { hasText: "22 SQFT" }).getByRole("button", { name: "Mark it settled" }).click();
  await page.getByLabel("How the shortage was settled").fill("Substituted mixed blue from Yard C");
  await useScenario(request, "resolve-walled");
  await page.getByRole("button", { name: "Send" }).click();
  // The settle queues (the glass shows it landed), then the replay meets the
  // role gate's wall: the scope parks, the work is kept, and the stall names
  // the truth in the store's own words.
  await expect(page.locator("[data-notice]")).toContainText("Shortage cleared");
  await expect(page.locator("[data-stall-strip]")).toBeVisible();
  await expect(page.locator("[data-stall]")).toContainText("The server refuses writes to shortages for this sign in (403)");
  await expect(page.locator("[data-stall]")).toContainText("kept, not sent, and tried again on every cycle");
});
