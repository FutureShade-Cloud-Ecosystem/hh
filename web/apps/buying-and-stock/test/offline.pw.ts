// CP-3, Buying and Stock in the Work shell. Placing a purchase order commits
// money, so it resolves online: with no signal the press is refused with the
// honest "no signal" state, nothing is sent and nothing is kept, and the draft
// stays open on the glass for the signal's return. (The manifest allows
// purchase orders and counts an outbox, server wins and manual; this slice
// builds the refusal only, so neither queues today.)
import type { Page } from "@playwright/test";
import { test, expect, openHome } from "./helpers.js";

function dialedWrites(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (r) => {
    if (["POST", "PUT", "PATCH", "DELETE"].includes(r.method()) && new URL(r.url()).pathname.startsWith("/api/")) seen.push(`${r.method()} ${new URL(r.url()).pathname}`);
  });
  return seen;
}

test("with no signal placing an order is refused with no signal, sends nothing, and places once the signal is back", async ({ page, request, context }) => {
  await openHome(page, request);
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  await expect(page.locator("[data-draft]")).toBeVisible();
  await page.locator("[data-field='expected']").fill("2031-03-04");
  const dialed = dialedWrites(page);

  await context.setOffline(true);
  await page.getByRole("button", { name: "Place order" }).click();
  await expect(page.locator("[data-draft] [data-error]")).toContainText("No signal");
  expect(dialed).toEqual([]);
  await expect(page.locator("[data-draft]")).toBeVisible();

  await context.setOffline(false);
  await page.getByRole("button", { name: "Place order" }).click();
  await expect(page.locator("[data-notice] strong")).toContainText("Placed:");
  expect(dialed.length).toBeGreaterThan(0);
});
