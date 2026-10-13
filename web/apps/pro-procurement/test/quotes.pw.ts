// Quotes legs in a real browser against the shared tenant stub. Visual
// references: the original portal's quotes screen (portal-quotes.ts) and the
// ref's quote document. Phone first (390), then desktop (1440).
import { test, expect, type Page } from "@playwright/test";
import { join } from "node:path";

const SHOTS = process.env.PRO_SHOT_DIR ?? join("test", "shots");
const PASSWORD = "stub-password";
const EMAIL = (who: string): string => `contractor.${who}@hh-demo.invalid`;

async function signIn(page: Page, who: string): Promise<void> {
  await page.goto("/");
  await page.getByTestId("signin-email").fill(EMAIL(who));
  await page.getByTestId("signin-password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.locator("hh-pro-frame")).toBeVisible();
}

async function openQuotes(page: Page): Promise<void> {
  await page.getByTestId("frame-selector").click();
  const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
  await panel.getByRole("button", { name: "Quote studio", exact: true }).click();
  await expect(page.getByTestId("surface-title")).toHaveText("Quotes");
}

test.beforeEach(async ({ request }) => {
  const response = await request.post("/_stub/reset");
  expect(response.ok()).toBe(true);
});

test.describe("phone 390", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the quotes list opens from the selector, and a quote reads with its lines and totals", async ({ page }) => {
    await signIn(page, "owner");
    await openQuotes(page);
    await expect(page.getByTestId("quotes-row")).toHaveCount(4);
    await page.getByTestId("quotes-row").first().click();
    await expect(page.getByTestId("surface-title")).toHaveText("Quote Q-7001");
    await expect(page.getByTestId("quote-line")).toHaveCount(2);
    await expect(page.getByTestId("quote-totals")).toContainText("Subtotal");
    await expect(page.getByTestId("quote-totals")).toContainText("Tax");
    await page.screenshot({ path: join(SHOTS, "quote-phone.png"), fullPage: true });
    await page.getByTestId("back").click();
    await expect(page.getByTestId("surface-title")).toHaveText("Quotes");
  });

  test("accepting here carries the core's words: the acceptance lives on the dealer's link", async ({ page }) => {
    await signIn(page, "buyer");
    await openQuotes(page);
    await page.locator("[data-testid='quotes-row']", { hasText: "Q-7001" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Quote Q-7001");
    await page.getByTestId("accept-quote").click();
    const sheet = page.getByTestId("quote-modal-sheet");
    await expect(sheet).toContainText("Accept this quote?");
    await sheet.getByRole("button", { name: "Accept", exact: true }).click();
    await expect(page.getByTestId("quote-modal-error")).toContainText("link the dealer sent you");
    await page.screenshot({ path: join(SHOTS, "quote-accept-refusal-phone.png"), fullPage: true });
    await sheet.getByRole("button", { name: "Cancel" }).click();
    await expect(sheet).toHaveCount(0);
  });

  test("a sent quote is rejected by its own confirm, and reads Lost after", async ({ page }) => {
    await signIn(page, "pm");
    await openQuotes(page);
    await page.locator("[data-testid='quotes-row']", { hasText: "Q-7001" }).click();
    await page.getByTestId("reject-quote").click();
    const sheet = page.getByTestId("quote-modal-sheet");
    await sheet.getByRole("button", { name: "Reject", exact: true }).click();
    await expect(page.getByTestId("notice")).toHaveText("The quote was rejected.");
    await expect(page.locator("hh-pro-frame")).toContainText("Lost");
    await expect(page.getByTestId("quote-actions")).toHaveCount(0);
  });

  test("an accepted quote converts with a collection choice, and the draft lands on the board", async ({ page }) => {
    await signIn(page, "owner");
    await openQuotes(page);
    await page.locator("[data-testid='quotes-row']", { hasText: "Q-7002" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Quote Q-7002");
    await page.getByTestId("convert-quote").click();
    const sheet = page.getByTestId("quote-modal-sheet");
    await expect(sheet).toContainText("Convert to an order");
    // No collection chosen yet: the Convert control holds until one is.
    await expect(sheet.getByRole("button", { name: "Convert", exact: true })).toBeDisabled();
    await page.getByTestId("choose-pickup").click();
    await expect(sheet.getByRole("button", { name: "Convert", exact: true })).toBeEnabled();
    await sheet.getByRole("button", { name: "Convert", exact: true }).click();
    await expect(page.getByTestId("notice")).toContainText("Converted to order SO-");
    await expect(page.locator("hh-pro-frame")).toContainText("Ordered");
    await page.screenshot({ path: join(SHOTS, "quote-converted-phone.png"), fullPage: true });
    // The draft order is on the board's Plan column.
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    await panel.getByRole("button", { name: "Board", exact: true }).click();
    await expect(page.locator("section[data-stage='plan']")).toContainText("SO-30507");
  });
});

test.describe("desktop 1440", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the list and the detail read across the page", async ({ page }) => {
    await signIn(page, "owner");
    await openQuotes(page);
    await expect(page.getByTestId("quotes-row")).toHaveCount(4);
    await page.screenshot({ path: join(SHOTS, "quotes-desktop.png"), fullPage: true });
    await page.getByTestId("quotes-row").first().click();
    await expect(page.getByTestId("surface-title")).toHaveText("Quote Q-7001");
    await page.screenshot({ path: join(SHOTS, "quote-desktop.png"), fullPage: true });
  });
});
