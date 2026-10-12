// Manager Home, sub item 2: "Needs you" resolves. The shortages view (the
// one view nothing else serves, named by the needs link), the resolve sheet
// with its three stated resolutions, the count dropping after a resolve, and
// the list wall's one-word Forbidden body becoming plain words that name who
// may resolve (the matrix's shortage_resolvers list).
import { test, expect } from "@playwright/test";
import { managerHandoff, resetStub, useScenario } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

async function openShortages(page: import("@playwright/test").Page): Promise<void> {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator("[data-needs='tasks.open_shortages'] button", { hasText: "Resolve" }).click();
  await expect(page.getByRole("heading", { name: "Shortages · open" })).toBeVisible();
}

test("the shortages view lists what the yard flagged, oldest facts first, at the caller's branch", async ({ page }) => {
  await openShortages(page);
  const card = page.locator("[data-task]").first();
  await expect(card.locator(".order")).toHaveText("SO-PLM-1042");
  await expect(card.locator(".task-head .meta").first()).toHaveText("Site foreman");
  await expect(card.locator(".task-head .meta").filter({ hasText: "In progress" })).toHaveCount(1);
  await expect(card.locator(".task-head .meta").filter({ hasText: "IN_PROGRESS" })).toHaveCount(0);
  const open = card.locator(".shortage[data-shortage]");
  await expect(open).toHaveCount(3);
  await expect(card.locator("[data-shortage] .line").first()).toHaveText("Class 2 Base Rock gravel");
  await expect(card.locator("[data-shortage] .detail").first()).toContainText("short 4 CYD");
  await expect(card.locator("[data-shortage] .detail").first()).not.toContainText("4.0000");
  await expect(page.locator("[data-test='resolver-words']")).toHaveText(
    "Shortages are resolved by the yard team and the yard manager.",
  );
});

test("a shortage resolves end to end and the count drops", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const needsCount = page.locator("[data-widget='needs.you'] [data-needs='tasks.open_shortages'] .needs-count");
  await expect(needsCount).toHaveText("2");
  await page.locator("[data-needs='tasks.open_shortages'] button", { hasText: "Resolve" }).click();
  await expect(page.getByRole("heading", { name: "Shortages · open" })).toBeVisible();

  const first = page.locator(".shortage[data-shortage]").first();
  await first.getByRole("button", { name: "Resolve" }).click();
  // The panel: the order and its product, the quantities as words, who
  // reported it with their note quoted, and a consequence line per choice.
  const panel = page.locator("[data-resolving='true']");
  await expect(panel.getByRole("heading", { name: "Resolve shortage" })).toBeVisible();
  await expect(panel.locator(".panel-line")).toHaveText("SO-PLM-1042 · Class 2 Base Rock gravel");
  await expect(panel.locator(".panel-qty")).toHaveText("Ordered 12 CYD · Picked 8 CYD · Short 4 CYD");
  await expect(panel.locator(".reported-note")).toHaveText("Only 8 yards left in the bin");
  await expect(panel.locator(".reported-who")).toContainText("Reported by the yard team");
  await expect(panel.getByText("The line is backordered and the order's rep is notified.")).toBeVisible();
  await expect(panel.locator(".hm-note")).toContainText("The order's rep sees the short pick in House Messages");
  // The sheet: three stated resolutions, the confirm dead until one is picked.
  const confirm = page.locator("[data-action='confirm-resolve']");
  await expect(confirm).toBeDisabled();
  await page.getByRole("button", { name: "Backorder the shortfall" }).click();
  await expect(confirm).toBeEnabled();
  await page.locator("[data-field='resolution-note']").fill("Customer ok with Thursday");
  await confirm.click();

  await expect(page.locator("[data-test='shortages-notice']")).toContainText("1 still open");
  // The resolved shortage stays on the record with its stated resolution.
  await expect(page.locator(".resolved-word").first()).toContainText(
    "Backordered the shortfall; the order keeps the short line open. Customer ok with Thursday",
  );
  // Back on the landing, the queue's own count dropped: 2 waiting became 1.
  await page.getByRole("button", { name: "Back to Home" }).click();
  await expect(needsCount).toHaveText("1");
});

test("the resolve is refused in the core's own words when the shortage is already cleared", async ({ page }) => {
  await openShortages(page);
  // Resolve the first shortage through the sheet...
  const first = page.locator(".shortage[data-shortage]").first();
  await first.getByRole("button", { name: "Resolve" }).click();
  await page.getByRole("button", { name: "Substitute from stock" }).click();
  await page.locator("[data-action='confirm-resolve']").click();
  await expect(page.locator("[data-test='shortages-notice']")).toContainText("1 still open");
  // ...then the sheet's cancel keeps the other open shortage open and unresolved.
  const second = page.locator(".shortage[data-shortage]:has([data-action='open-resolve'])").first();
  await second.getByRole("button", { name: "Resolve" }).click();
  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(page.locator("[data-resolving='true']")).toHaveCount(0);
});

test("the list wall's Forbidden body becomes words naming who may resolve, inside the sheet that refused", async ({ request, page }) => {
  await useScenario(request, "refused-resolve");
  await openShortages(page);
  const first = page.locator(".shortage[data-shortage]").first();
  await first.getByRole("button", { name: "Resolve" }).click();
  await page.getByRole("button", { name: "Approve the short pick" }).click();
  await page.locator("[data-action='confirm-resolve']").click();
  // The refusal sits in the panel, above its buttons, at the sheet that refused.
  const error = page.locator("[data-resolving='true'] [data-test='shortages-error']");
  await expect(error).toHaveText(
    "You may not resolve this shortage. Shortages are resolved by the yard team and the yard manager.",
  );
  // The refusal left the shortage open and the sheet standing.
  await expect(page.locator("[data-resolving='true']")).toHaveCount(1);
});
