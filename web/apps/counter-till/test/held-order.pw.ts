// Item 9.8 (row 1047 item 6, board 03): how a Cash On Demand order held from picking reads, on the
// Summary after a pay before release ring with a balance and on the Pay balance order card.
import { expect, test, type Page } from "@playwright/test";

const HOLD_TITLE = "This order cannot reach picking until the balance is paid.";
const HOLD_LINE =
  "Cash On Demand means pay before release, never cash on delivery. The deposit put it in process; the hold lifts on its own when the last dollar lands.";

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

async function openStep(page: Page, step: "customer" | "items" | "payment" | "summary"): Promise<void> {
  const head = page.getByTestId(`step-head-${step}`);
  for (let attempt = 0; attempt < 5; attempt += 1) {
    if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
    try {
      await expect(head).toHaveAttribute("aria-expanded", "true", { timeout: 1_000 });
      await page.waitForTimeout(250);
      if ((await head.getAttribute("aria-expanded")) === "true") return;
    } catch {
      // closed again by an advance: press once more
    }
  }
  await expect(head).toHaveAttribute("aria-expanded", "true");
}

async function ringCodDeposit(page: Page): Promise<void> {
  await page.getByRole("button", { name: /Select customer/ }).click();
  await page.getByPlaceholder("Search name or account #…").fill("C10");
  await page.locator("button.row").filter({ hasText: "Cash On Demand Landscaping" }).first().click();
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill("holland");
  await page.getByTestId("drop-row").filter({ hasText: "Holland Stone 60 mm" }).first().click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
  await page.getByRole("button", { name: "Will-call" }).click();
  await openStep(page, "payment");
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER").click();
  await page.getByTestId("deposit-amount").fill("1.00");
  await page.getByTestId("deposit-commit").click();
  await expect(page.getByTestId("sold-panel")).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  expect((await page.request.post("/_stub/reseed-owing")).ok()).toBe(true);
});

test("the Summary after a pay before release ring reads the held card, board 03", async ({ page }) => {
  await signIn(page);
  await ringCodDeposit(page);
  const held = page.getByTestId("held-order");
  await expect(held).toBeVisible();
  await expect(held.getByTestId("held-chip-process")).toHaveText("In process: deposit taken");
  await expect(held.getByTestId("held-chip-hold")).toHaveText("Held from picking");
  await expect(held.getByTestId("held-title")).toHaveText(HOLD_TITLE);
  await expect(held.getByTestId("held-line")).toHaveText(HOLD_LINE);
  // Item 9 visual review P3-4: the account words are the customer's own standing (a zero terms
  // account is the Cash On Demand posture), never asserted for every ring.
  await expect(held).toContainText("Cash On Demand Landscaping · Cash On Demand account");
  await expect(held).toContainText("Order total");
  await expect(held).toContainText("$4.52");
  await expect(held).toContainText("Deposit paid, Cash");
  await expect(held).toContainText("$1.00");
  await expect(held).toContainText("Balance due");
  await expect(held).toContainText("$3.52");
  await expect(held).toContainText("On the order");
  await expect(held).toContainText("Holland Stone 60 mm");
  await expect(held.getByTestId("held-pay-balance")).toHaveText("Pay balance");
  await expect(held.getByTestId("held-print-slip")).toHaveText("Print hold slip");
});

test("a credit deposit sale is not held, so no held card shows", async ({ page }) => {
  await signIn(page);
  await page.getByRole("button", { name: /Select customer/ }).click();
  await page.getByPlaceholder("Search name or account #…").fill("C02");
  await page.locator("button.row").filter({ hasText: "Fenwick Builders Ltd" }).first().click();
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill("holland");
  await page.getByTestId("drop-row").filter({ hasText: "Holland Stone 60 mm" }).first().click();
  await page.getByRole("button", { name: "Will-call" }).click();
  await openStep(page, "payment");
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_ON_ACCOUNT").click();
  await page.getByTestId("deposit-amount").fill("1.00");
  await page.getByTestId("deposit-commit").click();
  await expect(page.getByTestId("sold-panel")).toBeVisible();
  await expect(page.getByTestId("held-order")).toHaveCount(0);
});

test("the card's Pay balance action opens the Pay balance surface on that order", async ({ page }) => {
  await signIn(page);
  await ringCodDeposit(page);
  await page.getByTestId("held-pay-balance").click();
  expect(new URL(page.url()).hash).toContain("#/pay-balance");
  await expect(page.getByTestId("pay-balance-order")).toContainText(/Order · SO-|Order · ORD-/i);
  await expect(page.getByTestId("pay-balance-owed")).toContainText("$3.52");
});

test("Print hold slip prints the order number, customer, balance due and the hold sentence", async ({ page }) => {
  await signIn(page);
  await ringCodDeposit(page);
  const popup = page.waitForEvent("popup");
  await page.getByTestId("held-print-slip").click();
  const slip = await popup;
  await slip.waitForLoadState("load");
  const text = await slip.locator("body").innerText();
  expect(text).toContain("Cash On Demand Landscaping");
  expect(text).toContain("$3.52");
  expect(text).toContain(HOLD_TITLE);
});

test("the Pay balance order card reads the held chips for a held order, and none for an order that is not", async ({
  page,
}) => {
  await signIn(page);
  await page.getByTestId("till-pay-balance-entry").click();
  await page.getByTestId("pay-balance-term").fill("DEMO-TRT-D02");
  await page.getByTestId("pay-balance-find").click();
  const held = page.getByTestId("held-order");
  await expect(held.getByTestId("held-chip-process")).toHaveText("In process: deposit taken");
  await expect(held.getByTestId("held-chip-hold")).toHaveText("Held from picking");
  await expect(held.getByTestId("held-title")).toHaveText(HOLD_TITLE);
  await expect(held.getByTestId("held-print-slip")).toBeVisible();
  await page.getByTestId("pay-balance-term").fill("DEMO-TRT-D01");
  await page.getByTestId("pay-balance-find").click();
  await expect(page.getByTestId("pay-balance-order")).toContainText(/DEMO-TRT-D01/);
  await expect(page.getByTestId("held-order")).toHaveCount(0);
});

// Item 9 visual review P3-2: the held panel must sit clear of the order's rows beneath it on the
// Pay balance card (it touched the Order total row before, both widths).
test("the held card leaves a gap above the order's rows on the Pay balance card", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("till-pay-balance-entry").click();
  await page.getByTestId("pay-balance-term").fill("DEMO-TRT-D02");
  await page.getByTestId("pay-balance-find").click();
  const held = page.getByTestId("held-order");
  await expect(held).toBeVisible();
  const heldBox = await held.boundingBox();
  const totalBox = await page.getByTestId("pay-balance-order").locator(".figure").first().boundingBox();
  expect(heldBox).not.toBeNull();
  expect(totalBox).not.toBeNull();
  const gap = totalBox!.y - (heldBox!.y + heldBox!.height);
  expect(gap).toBeGreaterThanOrEqual(12);
});

// Item 9 visual review P3-4: the Summary's held card takes its account words from the customer's
// own standing. The walk-in has no account, so the card names none.
test("a walk-in pay before release ring holds, and its card claims no account", async ({ page }) => {
  await signIn(page);
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill("holland");
  await page.getByTestId("drop-row").filter({ hasText: "Holland Stone 60 mm" }).first().click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
  await page.getByRole("button", { name: "Will-call" }).click();
  await openStep(page, "payment");
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER").click();
  await page.getByTestId("deposit-amount").fill("1.00");
  await page.getByTestId("deposit-commit").click();
  await expect(page.getByTestId("sold-panel")).toBeVisible();
  const held = page.getByTestId("held-order");
  await expect(held).toBeVisible();
  await expect(held).toContainText("Walk-in");
  await expect(held).not.toContainText("Cash On Demand account");
});

test("at 390 the held card does not scroll sideways", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await page.getByTestId("till-pay-balance-entry").click();
  await page.getByTestId("pay-balance-term").fill("DEMO-TRT-D02");
  await page.getByTestId("pay-balance-find").click();
  await expect(page.getByTestId("held-order")).toBeVisible();
  const read = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, view: window.innerWidth }));
  expect(read.doc).toBeLessThanOrEqual(read.view);
});
