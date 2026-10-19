// Yard Pick, sub item 2: the pick queue as the landing, the Point Loma
// branch's open picks in their two halves (unclaimed and claimed), and
// claiming a pick.
import { test, expect } from "@playwright/test";
import { resetStub, useScenario, yardHandoff } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

test("the board lands on the Point Loma queue, in its two halves", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("[data-branch]")).toContainText("Point Loma");
  await expect(page.getByRole("heading", { name: "Pick queue" })).toBeVisible();
  await expect(page.locator("[data-queue-sub]")).toContainText("5 orders, in departure order");
  await expect(page.locator("[data-unclaimed]")).toHaveCount(0);
  await expect(page.locator("[data-claimed]")).toHaveCount(0);
  await expect(page.locator("[data-pick-card]")).toHaveCount(5);
});

test("a card carries the order, the customer, the lines, the priority and the status", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("[data-pick-card] [data-order-no]")).toHaveText(["SO-1042", "SO-1039", "SO-1040", "SO-1044", "SO-1041"]);
  const card = page.locator("[data-pick-card]", { hasText: "SO-1042" });
  await expect(card.locator("[data-order-no]")).toHaveText("SO-1042");
  await expect(card).toContainText("Bluffside Builders");
  await expect(card).toContainText("2 lines");
  await expect(card).toContainText("Wanted today");
  await expect(card.locator("[data-run-line]")).toHaveText("R5 · leaves 8:15 · load 2nd");
  await expect(card.locator("[data-task-status]")).toHaveText("Queued");
  await expect(card.getByRole("button", { name: "Claim" })).toHaveCount(0);
});

test("a claimed pick is marked as yours, with the made to order note on the mixed load", async ({ page }) => {
  await page.goto("/");
  const card = page.locator("[data-pick-card]", { hasText: "SO-1041" });
  await expect(card.locator("[data-task-status]")).toHaveText("Picking");
  const mine = page.locator("[data-pick-card]", { hasText: "SO-1040" });
  await expect(mine).toContainText("Claimed by you");
  await expect(page.locator("[data-pick-card]", { hasText: "SO-1039" })).toContainText("Mixed load: check each pallet");
});

test("a held order waits on the board with its words and no claim to press", async ({ page }) => {
  await page.goto("/");
  const held = page.locator("[data-pick-card]", { hasText: "SO-1044" });
  await expect(held.locator("[data-held]")).toContainText("Balance due");
  await expect(held.locator("[data-hold-pill]")).toHaveText("On hold");
  // A button the server will not honour invites a refusal: the held card offers none.
  await expect(held.getByRole("button")).toHaveCount(0);
});

test("claiming a pick makes it mine and opens it", async ({ page }) => {
  await page.goto("/");
  const card = page.locator("[data-pick-card]", { hasText: "SO-1039" });
  await card.click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1039");
  await expect(page.locator("[data-notice]")).toContainText("Claimed. It is yours to pull.");
  await expect(page.locator("[data-task-status]")).toHaveText("Picking");
  await page.getByRole("button", { name: "Back to the queue" }).click();
  const nowMine = page.locator("[data-pick-card]", { hasText: "SO-1039" });
  await expect(nowMine).toContainText("Claimed by you");
  await expect(nowMine.locator("[data-task-status]")).toHaveText("Picking");
});

test("an open shortage leads its card with a Short pill and the first item", async ({ page }) => {
  await page.goto("/");
  const card = page.locator("[data-pick-card]", { hasText: "SO-1041" });
  await expect(card.locator("[data-queue-shortage]")).toContainText("1 open shortage");
  await expect(card.locator("[data-queue-shortage]")).toContainText("short 22 SQFT of Flagstone");
  await expect(card.locator("[data-short-pill]")).toHaveText("Short");
});

test("the board carries its footnote in the board's own words", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("[data-queue-footnote]")).toContainText("The queue is fed by dispatched orders.");
  await expect(page.locator("[data-queue-footnote]")).toContainText("a short pick stays on the board until the yard manager clears it");
});

test("the empty branch state says so in words", async ({ page, request }) => {
  await useScenario(request, "empty");
  await page.goto("/");
  await expect(page.locator("[data-empty]")).toContainText("Nothing to pick here.");
});

test("a login off the yard is told so, in words", async ({ page, request }) => {
  await useScenario(request, "not-yard");
  await page.goto("/");
  await expect(page.locator("[data-error]")).toContainText("You are not set up on a yard.");
});
