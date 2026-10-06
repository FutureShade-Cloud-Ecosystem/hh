// The refusal sentence under Send Quote (visual review round 1, P3): a
// below-floor discount speaks the plain message in the outcome line and opens
// the refusal dialog with the same words; the line stayed after the dialog
// closed, repeating it. The line now goes when the dialog closes.
import { expect, test } from "@playwright/test";

/** The compose page under the split-list navigation: the New quote button walks the desk to #/quotes/new. */
async function newQuote(page: Page): Promise<void> {
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
}

// The stub keeps the person's layout row, which now remembers the open quote: every test starts from the seeded world.
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the refusal sentence under the actions goes when the refusal dialog closes", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await newQuote(page);
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  const scan = page.getByTestId("pos-scan-input");
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const disc = page.getByTestId("qb-line-disc");
  await disc.fill("15");
  await disc.blur();
  const dlg = page.locator("qd-floor-refusal").getByRole("dialog");
  await expect(dlg).toBeVisible();
  // While the dialog is open the outcome line carries the plain message.
  await expect(page.getByTestId("qb-error")).toContainText("below the margin floor");
  await page.keyboard.press("Escape");
  await expect(dlg).toHaveCount(0);
  await expect(page.getByTestId("qb-error")).toHaveText("", { useInnerText: true });
});
