// Board legs in a real browser against the shared tenant stub. Visual
// references: the ref's BoardPage (stage columns, New plan) and the HH: Pro
// board renders. Phone first (390), then desktop (1440): the four stage
// columns stack on the phone and sit side by side on desktop. Screenshots go
// to PRO_SHOT_DIR when it is set (the report folder), else test/shots, which
// stays out of git.
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

async function openBoard(page: Page): Promise<void> {
  await page.getByTestId("frame-selector").click();
  const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
  await panel.getByRole("button", { name: "Board", exact: true }).click();
  await expect(page.getByTestId("surface-title")).toHaveText("Board");
}

test.beforeEach(async ({ request }) => {
  const response = await request.post("/_stub/reset");
  expect(response.ok()).toBe(true);
});

test.describe("phone 390", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the board shows the four stages with the cards the contract's map puts there", async ({ page }) => {
    await signIn(page, "owner");
    await openBoard(page);
    await expect(page.getByTestId("frame-selector")).toContainText("Board");
    const board = page.locator("hh-pro-frame");
    await expect(board.locator("section[data-stage='plan']")).toContainText("La Jolla terrace pavers");
    await expect(board.locator("section[data-stage='plan']")).toContainText("Backyard planting beds");
    await expect(board.locator("section[data-stage='quote']")).toContainText("Q-7001");
    await expect(board.locator("section[data-stage='order']")).toContainText("SO-30504");
    await expect(board.locator("section[data-stage='invoice']")).toContainText("SO-30501");
    await expect(board).toContainText("2 plans");
    await expect(board).toContainText("5 open orders");
    await page.screenshot({ path: join(SHOTS, "board-phone.png"), fullPage: true });
  });

  test("New plan opens the modal, refuses an empty name, and creates on a name", async ({ page }) => {
    await signIn(page, "buyer");
    await openBoard(page);
    await page.getByTestId("new-plan").click();
    const sheet = page.getByTestId("plan-modal-sheet");
    await expect(sheet).toBeVisible();
    await sheet.getByRole("button", { name: "Create" }).click();
    await expect(page.getByTestId("plan-error")).toHaveText("Give the plan a name first.");
    await page.getByTestId("plan-name").fill("Cedar fence run");
    await sheet.getByRole("button", { name: "Create" }).click();
    await expect(page.getByTestId("plan-modal-sheet")).toHaveCount(0);
    await expect(page.getByTestId("surface-title")).not.toHaveText("Board");
  });

  test("an empty world shows the board's own empty words", async ({ page, request }) => {
    for (const what of ["projects", "quotes", "orders"]) {
      await request.post("/_stub/control", { data: { empty: what } });
    }
    await signIn(page, "owner");
    await openBoard(page);
    const state = page.getByTestId("surface-state");
    await expect(state).toHaveAttribute("data-state", "empty");
    await expect(state).toContainText("Nothing is in motion");
  });

  test("Buy again opens the reorder preview, and the draft lands in Plan", async ({ page }) => {
    await signIn(page, "owner");
    await openBoard(page);
    const card = page.locator("[data-testid='board-order']", { hasText: "SO-30503" });
    await card.getByTestId("buy-again").click();
    const sheet = page.getByTestId("reorder-modal-sheet");
    await expect(sheet).toContainText("Reorder SO-30503");
    await expect(sheet).toContainText("today's rates");
    await expect(page.getByTestId("reorder-line")).toHaveCount(2);
    await page.screenshot({ path: join(SHOTS, "reorder-phone.png"), fullPage: true });
    // One line comes off; the draft carries the rest.
    await page.getByTestId("reorder-line").first().click();
    await sheet.getByRole("button", { name: /Create draft order \(1 line\)/ }).click();
    await expect(sheet).toHaveCount(0);
    await expect(page.getByTestId("notice")).toContainText("Draft order SO-");
    await expect(page.locator("section[data-stage='plan']")).toContainText("SO-30507");
  });

  test("a role without submit_rfq is not offered the board at all", async ({ page }) => {
    await signIn(page, "field");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    await expect(panel.getByRole("button", { name: "Board", exact: true })).toHaveCount(0);
    await expect(panel.getByRole("button", { name: "Catalog", exact: true })).toHaveCount(0);
  });
});

test.describe("desktop 1440", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the four stage columns sit side by side", async ({ page }) => {
    await signIn(page, "owner");
    await openBoard(page);
    const plan = page.locator("section[data-stage='plan']");
    const quote = page.locator("section[data-stage='quote']");
    const order = page.locator("section[data-stage='order']");
    const invoice = page.locator("section[data-stage='invoice']");
    await expect(plan).toBeVisible();
    const planBox = await plan.boundingBox();
    const quoteBox = await quote.boundingBox();
    const orderBox = await order.boundingBox();
    const invoiceBox = await invoice.boundingBox();
    if (planBox === null || quoteBox === null || orderBox === null || invoiceBox === null) throw new Error("a lane did not render");
    expect(quoteBox.x).toBeGreaterThan(planBox.x);
    expect(orderBox.x).toBeGreaterThan(quoteBox.x);
    expect(invoiceBox.x).toBeGreaterThan(orderBox.x);
    expect(Math.abs(planBox.y - invoiceBox.y)).toBeLessThan(50);
    await page.screenshot({ path: join(SHOTS, "board-desktop.png"), fullPage: true });
  });
});
