// Row 955: every job its own surface. Returns is no longer a section under the sale: the bar carries
// a Returns entry, the surface lives at #/returns, and the sale screen carries only the sale.
import { expect, test, type Page } from "@playwright/test";

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

const pageScrolls = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollHeight > document.documentElement.clientHeight);

test("the sale screen carries only the sale: no returns desk under it, and the page does not scroll", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await expect(page.getByTestId("returns")).toHaveCount(0);
  await expect(page.getByTestId("till-returns-entry")).toBeVisible();
  // The entry wears the bar's button face like Pop out beside it: a bordered, padded box.
  const face = await page.getByTestId("till-returns-entry").evaluate((el) => {
    const s = getComputedStyle(el);
    return { border: s.borderTopWidth, padding: parseFloat(s.paddingLeft) };
  });
  expect(face.border).toBe("1px");
  expect(face.padding).toBeGreaterThanOrEqual(8);
  expect(await pageScrolls(page)).toBe(false);
});

test("Returns opens as its own surface at #/returns, the sale waits unharmed, and Back to the sale returns to it", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  const head = page.getByTestId("step-head-items");
  if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
  await page.getByTestId("pos-scan-input").fill("holland");
  await page.getByTestId("drop-row").filter({ hasText: "Holland Stone 60 mm" }).first().click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);

  await page.getByTestId("till-returns-entry").click();
  expect(new URL(page.url()).hash).toBe("#/returns");
  await expect(page.getByTestId("returns")).toBeVisible();
  await expect(page.getByTestId("pos-page-title")).toBeHidden();
  expect(await pageScrolls(page)).toBe(false);
  // The sale's keys stay quiet while it is out of sight.
  await page.keyboard.press("F4");
  await expect(page.getByTestId("returns")).toBeVisible();

  await page.getByTestId("till-sale-entry").click();
  await expect(page.getByTestId("returns")).toHaveCount(0);
  await expect(page.getByTestId("pos-page-title")).toBeVisible();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
});

test("a direct visit to #/returns after sign in lands on the returns desk", async ({ page }) => {
  // Custody is memory, so a fresh load starts on the cards; the route in the address survives sign in.
  await page.goto("/#/returns");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("returns")).toBeVisible();
  await page.getByTestId("till-sale-entry").click();
});
