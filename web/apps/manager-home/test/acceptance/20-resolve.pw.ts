// The resolve leg: a shortage the yard team flagged, resolved end to end
// through the core's own resolve route, with the queue's count dropping
// after it. The seed's two open shortages live on one Point Loma pick: the
// Class 2 base rock shortfall ("Only 8 yards left in the bin") and the HPB dust
// one ("Pile is down to fines").
import { test, expect } from "@playwright/test";

test("a shortage resolves end to end and the count drops", async ({ page }) => {
  await page.goto("/home/");
  await page.locator("[data-flow='yard-manager']").click();
  const needsCount = page.locator("[data-widget='needs.you'] [data-needs='tasks.open_shortages'] .needs-count");
  await expect(needsCount).toHaveText("2");

  await page.locator("[data-needs='tasks.open_shortages'] button", { hasText: "Resolve" }).click();
  await expect(page.getByRole("heading", { name: "Shortages · open" })).toBeVisible();

  // The view lists what the yard flagged, with the resolver words beside it. Both seeded
  // shortages are here, on the one Point Loma pick, in the queue's own order.
  await expect(page.locator(".shortage[data-shortage]")).toContainText("Only 8 yards left in the bin");
  await expect(page.locator(".shortage[data-shortage]")).toContainText("Pile is down to fines");
  await expect(page.locator("[data-test='resolver-words']")).toHaveText(
    "Shortages are resolved by the yard team and the yard manager.",
  );

  // The resolve: the first open shortage, one of the three stated resolutions, with a note.
  const first = page.locator(".shortage[data-shortage]:has([data-action='open-resolve'])").first();
  await first.getByRole("button", { name: "Resolve" }).click();
  await page.getByRole("button", { name: "Backorder the shortfall" }).click();
  await page.locator("[data-field='resolution-note']").fill("Customer ok with Thursday");
  await page.locator("[data-action='confirm-resolve']").click();

  await expect(page.locator("[data-test='shortages-notice']")).toContainText("1 still open");
  await expect(page.locator(".resolved-word").first()).toContainText(
    "Backordered the shortfall; the order keeps the short line open. Customer ok with Thursday",
  );
  // Back on the landing, the queue's own count dropped: 2 became 1.
  await page.getByRole("button", { name: "Back to Home" }).click();
  await expect(needsCount).toHaveText("1");
});
