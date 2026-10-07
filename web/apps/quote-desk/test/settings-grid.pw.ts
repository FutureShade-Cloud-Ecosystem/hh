// The settings grid (visual review round 1, P3): "Send by text (SMS)" sat low
// in the grid and Save policy touched the row above. The four switches now
// share a row of their own, and Save policy has its own breathing room.
import { expect, test } from "@playwright/test";
import { row, showList } from "./switch-helpers.js";

// The stub keeps the person's layout row, which now remembers the open quote: every test starts from the seeded world.
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the policy switches share one row and Save policy keeps its distance", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  await page.locator("qd-quote-settings").getByTestId("qs-screen").locator("summary").click();
  await expect(page.getByTestId("qs-policy")).toBeVisible();
  const policy = page.getByTestId("qs-policy");
  const tops: number[] = [];
  for (const label of ["Per-line discounts", "Product images on sent quotes", "Send by email", "Send by text"]) {
    const box = (await policy.locator("label.check").filter({ hasText: label }).boundingBox())!;
    tops.push(box.y);
  }
  expect(Math.max(...tops) - Math.min(...tops)).toBeLessThan(4);
  // Nothing above Save policy touches it.
  const save = (await page.getByTestId("qs-policy-save").boundingBox())!;
  const split = (await policy.locator("#qs-arr").boundingBox())!;
  const sms = (await policy.locator("label.check").filter({ hasText: "Send by text" }).boundingBox())!;
  const above = Math.max(split.y + split.height, sms.y + sms.height);
  expect(save.y - above).toBeGreaterThanOrEqual(12);
});
