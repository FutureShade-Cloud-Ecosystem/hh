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

test("after an add the search is empty and focused, and F2 always returns to it", async ({ page }) => {
  await signIn(page);
  const field = page.getByTestId("pos-scan-input");
  await toSearch(page);
  await page.keyboard.type("charcoal");
  const row = page.getByTestId("drop-row").first();
  await expect(row).toBeVisible();
  await row.focus();
  await page.keyboard.press("Enter");
  await expect(lines(page)).toHaveCount(1);
  await expect(field).toBeFocused();
  await expect(field).toHaveValue("");

  // The next name does not append to the last one.
  await page.keyboard.type("polymer");
  await expect(field).toHaveValue("polymer");
  await expect(page.getByTestId("drop-row").first()).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(lines(page)).toHaveCount(2);
  await expect(field).toHaveValue("");

  // F2 from anywhere in the sale: here from a result row that holds focus.
  await page.keyboard.type("charcoal");
  await expect(page.getByTestId("drop-row").first()).toBeVisible();
  await page.getByTestId("drop-row").first().focus();
  await expect(page.getByTestId("drop-row").first()).toBeFocused();
  await page.keyboard.press("F2");
  await expect(field).toBeFocused();
});

test("the payment method group says how it is walked", async ({ page }) => {
  await signIn(page);
  await page.keyboard.press("F4");
  await page.keyboard.press("F4");
  const group = page.getByTestId("pos-paytiles");
  await expect(group).toBeVisible();
  const hint = page.getByTestId("pos-paytiles-hint");
  await expect(hint).toHaveText("Left and Right choose the payment method");
  await expect(hint).toBeVisible();
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
