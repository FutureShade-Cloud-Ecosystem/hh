// Catalog and Product legs in a real browser against the shared tenant stub.
// Visual references: the ref's CatalogPage (search above everything) and
// ProductPage (the single add to a plan action). Phone first (390), then
// desktop (1440).
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

async function openCatalog(page: Page): Promise<void> {
  await page.getByTestId("frame-selector").click();
  const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
  await panel.getByRole("button", { name: "Catalog", exact: true }).click();
  await expect(page.getByTestId("surface-title")).toHaveText("Catalog");
}

test.beforeEach(async ({ request }) => {
  const response = await request.post("/_stub/reset");
  expect(response.ok()).toBe(true);
});

test.describe("phone 390", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the catalog searches as the person types, and a miss says so", async ({ page }) => {
    await signIn(page, "owner");
    await openCatalog(page);
    await expect(page.getByTestId("catalog-state")).toContainText("Search the dealer's catalog");
    await page.getByTestId("catalog-search").fill("paver");
    await expect(page.getByTestId("catalog-rows")).toBeVisible();
    await expect(page.getByTestId("catalog-row")).toHaveCount(5);
    await expect(page.getByTestId("catalog-row").first()).toContainText("CPV-1204");
    await page.screenshot({ path: join(SHOTS, "catalog-phone.png"), fullPage: true });
    await page.getByTestId("catalog-search").fill("zzzz");
    await expect(page.getByTestId("catalog-state")).toContainText("No matches");
  });

  test("a product opens from the catalog, names no price, and Back returns to the search", async ({ page }) => {
    await signIn(page, "pm");
    await openCatalog(page);
    await page.getByTestId("catalog-search").fill("flagstone");
    await page.getByTestId("catalog-row").first().click();
    await expect(page.getByTestId("surface-title")).toHaveText("Irregular flagstone, buff");
    await expect(page.locator("hh-pro-frame")).toContainText("GRQ-4410");
    await expect(page.locator("hh-pro-frame")).toContainText("In stock at the yard");
    const frame = page.locator("hh-pro-frame");
    const text = await frame.innerText();
    expect(text).not.toMatch(/\$[0-9]/);
    await page.screenshot({ path: join(SHOTS, "product-phone.png"), fullPage: true });
    await page.getByTestId("back").click();
    await expect(page.getByTestId("surface-title")).toHaveText("Catalog");
    await expect(page.getByTestId("catalog-search")).toHaveValue("flagstone");
    await expect(page.getByTestId("catalog-row")).toHaveCount(1);
  });

  test("add to a plan puts the line on the plan and opens it", async ({ page }) => {
    await signIn(page, "buyer");
    await openCatalog(page);
    await page.getByTestId("catalog-search").fill("steel edging");
    await page.getByTestId("catalog-row").first().click();
    await expect(page.getByTestId("surface-title")).toHaveText("Steel edging, 4 in");
    await page.getByRole("button", { name: "Add to a plan" }).click();
    const sheet = page.getByTestId("add-plan-modal-sheet");
    await expect(sheet).toBeVisible();
    await expect(page.getByTestId("plan-qty")).toHaveValue("1");
    await expect(page.getByTestId("plan-uom")).toHaveValue("LF");
    await page.locator("[data-testid='choose-plan']", { hasText: "La Jolla terrace pavers" }).click();
    await page.getByTestId("plan-qty").fill("40");
    await sheet.getByRole("button", { name: "Add", exact: true }).click();
    await expect(sheet).toHaveCount(0);
    await expect(page.getByTestId("notice")).toContainText("Added 40 LF to La Jolla terrace pavers");
    await expect(page.getByTestId("surface-title")).toHaveText("La Jolla terrace pavers");
    await expect(page.getByTestId("scope-line").last()).toContainText("40 LF");
  });

  test("add to a new plan names the plan and lands on it", async ({ page }) => {
    await signIn(page, "owner");
    await openCatalog(page);
    await page.getByTestId("catalog-search").fill("drain");
    await page.getByTestId("catalog-row").first().click();
    await page.getByRole("button", { name: "Add to a plan" }).click();
    await page.getByTestId("choose-new-plan").click();
    await page.getByTestId("new-plan-name").fill("French drain run");
    await page.getByTestId("plan-qty").fill("120");
    await page.getByTestId("add-plan-modal-sheet").getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("French drain run");
    await expect(page.getByTestId("scope-line")).toHaveCount(1);
    await expect(page.getByTestId("scope-line")).toContainText("120 LF");
  });
});

test.describe("desktop 1440", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the catalog and the product read across the page", async ({ page }) => {
    await signIn(page, "owner");
    await openCatalog(page);
    await page.getByTestId("catalog-search").fill("paver");
    await expect(page.getByTestId("catalog-row")).toHaveCount(5);
    await page.screenshot({ path: join(SHOTS, "catalog-desktop.png"), fullPage: true });
    await page.getByTestId("catalog-row").first().click();
    await expect(page.getByTestId("surface-title")).toHaveText("Sandstone paver 6 x 9");
    await page.screenshot({ path: join(SHOTS, "product-desktop.png"), fullPage: true });
  });
});
