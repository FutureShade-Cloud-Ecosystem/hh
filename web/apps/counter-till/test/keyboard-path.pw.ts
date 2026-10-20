// The counter's keyboard only path (UX-45): a typed name is added by Enter, the search clears and
// keeps focus after an add, F2 always lands in the search, the payment method group says how it
// is walked, and a scanner's sequence (digits then Enter) still resolves exactly as before.
import { expect, test, type Page } from "@playwright/test";

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  const dot = page.getByTestId("session-dot");
  const openButton = page.getByRole("button", { name: "Open Session", exact: true });
  await expect(dot.or(openButton).first()).toBeVisible({ timeout: 10_000 });
  if (!(await dot.isVisible())) await openButton.click({ timeout: 3_000 }).catch(() => undefined);
  await expect(dot).toContainText("Open");
}

/** Put the caret in the search with the shortcut the legend shows. */
async function toSearch(page: Page): Promise<void> {
  const field = page.getByTestId("pos-scan-input");
  await page.keyboard.press("F2");
  await expect(field).toBeFocused();
}

const lines = (page: Page) => page.getByTestId("pos-line-row");

test("Enter on a typed name adds the highlighted result", async ({ page }) => {
  await signIn(page);
  await toSearch(page);
  // keyboard.type has no pause between keys: the fastest typist, the case that read as a scanner.
  await page.keyboard.type("charcoal");
  await expect(page.getByTestId("drop-row").first()).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(lines(page).filter({ hasText: "Charcoal Stone 60 mm" })).toHaveCount(1);
  await expect(page.getByTestId("pos-scan-msg")).not.toContainText("No product");
});

test("a scanner's sequence still resolves a code on Enter, and an unknown code is still loud", async ({ page }) => {
  await signIn(page);
  await toSearch(page);
  await page.keyboard.type("HPV-60");
  await page.keyboard.press("Enter");
  await expect(lines(page).filter({ hasText: "Charcoal Stone 60 mm" })).toHaveCount(1);
  await expect(page.getByTestId("pos-scan-input")).toHaveValue("");

  await page.keyboard.type("0099887766");
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("pos-scan-msg")).toContainText("No product for 0099887766");
  await expect(lines(page)).toHaveCount(1);
});
