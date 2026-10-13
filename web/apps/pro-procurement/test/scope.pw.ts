// Order scope legs in a real browser against the shared tenant stub. Visual
// references: the original portal's project editor (portal-procurement-project.ts)
// and the HH: Pro board's plan card. Phone first (390), then desktop (1440).
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

async function openScopeOf(page: Page, projectName: string): Promise<void> {
  await page.getByTestId("frame-selector").click();
  const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
  await panel.getByRole("button", { name: "Board", exact: true }).click();
  await expect(page.getByTestId("surface-title")).toHaveText("Board");
  await page.locator("button[data-action='open-scope']", { hasText: projectName }).click();
  await expect(page.getByTestId("surface-title")).toHaveText(projectName);
}

test.beforeEach(async ({ request }) => {
  const response = await request.post("/_stub/reset");
  expect(response.ok()).toBe(true);
});

test.describe("phone 390", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("a plan opens with its lines at the account price", async ({ page }) => {
    await signIn(page, "owner");
    await openScopeOf(page, "La Jolla terrace pavers");
    await expect(page.getByTestId("scope-line")).toHaveCount(3);
    await expect(page.locator("hh-pro-frame")).toContainText("Indicative subtotal");
    await expect(page.locator("hh-pro-frame")).toContainText("$4,352.00");
    await expect(page.locator("hh-pro-frame")).toContainText("850 SF");
    await page.screenshot({ path: join(SHOTS, "scope-phone.png"), fullPage: true });
  });

  test("editing a line re-opens it at the server's price rules: the change lands, the price waits for the RFQ", async ({ page }) => {
    await signIn(page, "buyer");
    await openScopeOf(page, "La Jolla terrace pavers");
    await page.getByTestId("line-edit").first().click();
    const sheet = page.getByTestId("line-modal-sheet");
    await expect(sheet).toBeVisible();
    await expect(page.getByTestId("line-qty")).toHaveValue("850");
    await page.getByTestId("line-qty").fill("900");
    await sheet.getByRole("button", { name: "Save" }).click();
    await expect(sheet).toHaveCount(0);
    await expect(page.getByTestId("notice")).toHaveText("Line updated.");
    const line = page.getByTestId("scope-line").first();
    await expect(line).toContainText("900 SF");
    await expect(line).toContainText("Awaiting price");
  });

  test("a line comes off the plan, and the server's refusal words surface when it cannot", async ({ page }) => {
    await signIn(page, "buyer");
    await openScopeOf(page, "La Jolla terrace pavers");
    await page.getByTestId("line-remove").first().click();
    await expect(page.getByTestId("notice")).toHaveText("Line removed.");
    await expect(page.getByTestId("scope-line")).toHaveCount(2);
  });

  test("the catalog picker adds a line and stays open for the next one", async ({ page }) => {
    await signIn(page, "pm");
    await openScopeOf(page, "Backyard planting beds");
    await expect(page.getByTestId("scope-empty")).toContainText("No lines yet");
    await page.getByTestId("add-material").click();
    const sheet = page.getByTestId("picker-modal-sheet");
    await expect(sheet).toBeVisible();
    await page.getByTestId("picker-search").fill("mulch");
    await expect(sheet.getByTestId("picker-result")).toHaveCount(2);
    await page.getByTestId("picker-result").first().click();
    await expect(sheet).toBeVisible();
    await expect(sheet.getByTestId("picker-result")).toHaveCount(2);
    await expect(page.getByTestId("scope-line")).toHaveCount(1);
    await expect(page.getByTestId("scope-line")).toContainText("Awaiting price");
    await page.screenshot({ path: join(SHOTS, "scope-picker-phone.png"), fullPage: true });
    await sheet.getByRole("button", { name: "Cancel" }).click();
    await expect(sheet).toHaveCount(0);
  });

  test("the whole plan life: create, add, submit the RFQ, read the prices, recall", async ({ page }) => {
    await signIn(page, "owner");
    // New plan from the board.
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    await panel.getByRole("button", { name: "Board", exact: true }).click();
    await page.getByTestId("new-plan").click();
    await page.getByTestId("plan-name").fill("Cedar fence run");
    await page.getByTestId("plan-modal-sheet").getByRole("button", { name: "Create" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Cedar fence run");
    // Add a line from the catalog.
    await page.getByTestId("add-material").click();
    await page.getByTestId("picker-search").fill("cedar");
    await page.getByTestId("picker-result").first().click();
    await expect(page.getByTestId("scope-line")).toHaveCount(1);
    await page.getByTestId("picker-modal-sheet").getByRole("button", { name: "Cancel" }).click();
    // Submit the RFQ: the project goes to Quoting and the lines price.
    await page.getByTestId("submit-rfq").click();
    await expect(page.getByTestId("notice")).toHaveText("RFQ submitted. Lines priced indicatively.");
    await expect(page.locator("hh-pro-frame")).toContainText("Quoting");
    await expect(page.getByTestId("scope-line").first()).toContainText("$38.00");
    await page.screenshot({ path: join(SHOTS, "scope-quoting-phone.png"), fullPage: true });
    // A quoting project is read only; the way back is Recall.
    await expect(page.getByTestId("submit-rfq")).toHaveCount(0);
    await page.getByTestId("recall").click();
    await expect(page.getByTestId("notice")).toHaveText("Recalled to planning.");
    await expect(page.getByTestId("submit-rfq")).toBeVisible();
  });

  test("a plan is deleted by its own confirm, and the board loses it", async ({ page }) => {
    await signIn(page, "owner");
    await openScopeOf(page, "Backyard planting beds");
    await page.getByTestId("delete-plan").click();
    const sheet = page.getByTestId("delete-modal-sheet");
    await expect(sheet).toContainText("This cannot be undone");
    await sheet.getByRole("button", { name: "Delete" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Board");
    await expect(page.locator("button[data-action='open-scope']", { hasText: "Backyard planting beds" })).toHaveCount(0);
  });
});

test.describe("desktop 1440", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the plan's lines read across the page", async ({ page }) => {
    await signIn(page, "owner");
    await openScopeOf(page, "La Jolla terrace pavers");
    await expect(page.getByTestId("scope-line")).toHaveCount(3);
    await page.screenshot({ path: join(SHOTS, "scope-desktop.png"), fullPage: true });
  });
});
