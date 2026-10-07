// Row 1047 item 3, the till's side: a Discount action on a ticket line and on the ticket, percent
// or dollars, with a required "Approved by" picker of the managers the ring itself would accept.
// The discount is verbal (no queue, no second login) and recorded: the line and the totals show it
// from the server's cents, the sale body names the approver, and the paid card says who approved.
// Both layouts, Panels and Steps; DISCOUNT_SHOTS_DIR, when set, also writes the light and dark
// screens at 1440x900 for the review.
import { expect, test, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";

type Layout = "panels" | "stepper";

async function signIn(page: Page, layout: Layout): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  await page.getByTestId("pos-view").click();
  const radio = page.getByTestId(`pos-layout-${layout}`);
  if ((await radio.getAttribute("aria-checked")) !== "true") await radio.click();
  else await page.getByTestId("pos-view").click();
  if (layout === "panels") await expect(page.getByTestId("pos-twopane")).toBeVisible();
}

async function ensureSession(page: Page): Promise<void> {
  const dot = page.getByTestId("session-dot");
  const openButton = page.getByRole("button", { name: "Open Session", exact: true });
  await expect(dot.or(openButton).first()).toBeVisible({ timeout: 10_000 });
  if (await dot.isVisible()) return;
  await openButton.click({ timeout: 3_000 }).catch(() => undefined);
  await expect(dot).toContainText("Open");
}

/** Open one step of the Steps layout by its head (the Panels layout has them all up already). */
async function openStep(page: Page, step: "items" | "payment"): Promise<void> {
  const head = page.getByTestId(`step-head-${step}`);
  if ((await head.count()) === 0) return;
  for (let attempt = 0; attempt < 5; attempt += 1) {
    if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
    try {
      await expect(head).toHaveAttribute("aria-expanded", "true", { timeout: 1_000 });
      return;
    } catch {
      /* the flow advanced after the press: press again */
    }
  }
  await expect(head).toHaveAttribute("aria-expanded", "true");
}

async function addLine(page: Page, query: string, hit: string): Promise<void> {
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill(query);
  const row = page.getByTestId("drop-row").filter({ hasText: hit }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: hit })).toHaveCount(1);
  await page.getByTestId("pos-scan-input").fill("");
}

/** Ten of the Holland stone: 40.00 at the rule price, so a ten percent discount is a clean 4.00. */
async function tenHolland(page: Page): Promise<void> {
  await addLine(page, "holland", "Holland Stone 60 mm");
  const qty = page.getByLabel("Quantity: Holland Stone 60 mm", { exact: true });
  await qty.fill("10");
  await qty.press("Enter");
  await expect(page.getByTestId("pos-line-row").first().locator(".ltot")).toContainText("$40.00");
}

async function openPaymentStep(page: Page, layout: Layout): Promise<void> {
  if (layout === "panels") {
    await page.locator("hos-pos-flow-steps").first().getByRole("button", { name: /Payment/ }).click();
  } else {
    await openStep(page, "payment");
  }
  await expect(page.getByTestId("pos-totals-card")).toBeVisible();
}

async function giveDiscount(
  page: Page,
  opener: () => Promise<void>,
  mode: "percent" | "amount",
  value: string,
  approver: string,
): Promise<void> {
  await opener();
  const dialog = page.getByTestId("discount-dialog");
  await expect(dialog).toBeVisible();
  await page.getByTestId(`discount-mode-${mode}`).click();
  await page.getByTestId("discount-value").fill(value);
  await page.getByTestId("discount-approver").selectOption({ label: approver });
  await page.getByTestId("discount-apply").click();
  await expect(dialog).toHaveCount(0);
}

test.afterEach(async ({ page }) => {
  try {
    if ((await page.getByTestId("pos-view").count()) === 0) return;
    await page.getByTestId("pos-view").click();
    const radio = page.getByTestId("pos-layout-stepper");
    if ((await radio.getAttribute("aria-checked")) === "true") {
      await page.getByTestId("pos-view").click();
      return;
    }
    const saved = page.waitForRequest((r) => r.method() === "PUT" && r.url().includes("/api/v1/pos/workspace"));
    await radio.click();
    await saved;
  } catch {
    /* the page never reached the till */
  }
});

for (const layout of ["stepper", "panels"] as const) {
  test(`${layout}: a line discount and a ticket discount are recorded with their approvers, shown from the server's cents, and carried on the ring`, async ({
    page,
  }) => {
    const rung: Array<Record<string, any>> = [];
    page.on("request", (request) => {
      if (request.url().endsWith("/api/v1/pos/sales") && request.method() === "POST") {
        rung.push(request.postDataJSON() as Record<string, any>);
      }
    });
    await signIn(page, layout);
    await ensureSession(page);
    await tenHolland(page);

    // The dialog will not apply without a value and without the manager who said yes.
    await openStep(page, "items");
    await page.getByTestId("pos-line-discount").click();
    const apply = page.getByTestId("discount-apply");
    await expect(apply).toBeDisabled();
    await page.getByTestId("discount-value").fill("10");
    await expect(apply).toBeDisabled();
    await expect(page.getByTestId("discount-approver")).toHaveValue("");
    await page.getByTestId("discount-value").fill("0");
    await page.getByTestId("discount-approver").selectOption({ label: "Sales manager · Sales Manager" });
    await expect(apply).toBeDisabled();
    await page.getByTestId("discount-value").fill("10");
    await expect(apply).toBeEnabled();
    await apply.click();

    const chip = page.getByTestId("pos-line-discount-chip");
    await expect(chip).toContainText("10% off ($4.00), approved by Sales manager");
    // The line reads like the receipt: its price less its own discount.
    await expect(page.getByTestId("pos-line-row").first().locator(".ltot")).toContainText("$36.00");
    await openPaymentStep(page, layout);
    const totals = page.getByTestId("pos-totals-card");
    await expect(totals).toContainText("Subtotal");
    await expect(totals.locator(".trow").filter({ hasText: "Subtotal" })).toContainText("$36.00");
    await expect(totals.getByTestId("pos-ticket-discount-row")).toHaveCount(0);
    await expect(totals.getByTestId("pos-preview-total")).toContainText("$40.68"); // 40.00 - 4.00 + 13% = 40.68

    // The whole ticket, in dollars, by the other manager: the server shares it over the lines.
    await giveDiscount(page, () => page.getByTestId("pos-ticket-discount").click(), "amount", "2", "Branch manager · Branch Manager");
    await expect(totals.getByTestId("pos-ticket-discount-row")).toContainText("-$2.00");
    await expect(totals.locator(".trow").filter({ hasText: "Subtotal" })).toContainText("$34.00");
    await expect(totals.getByTestId("pos-ticket-discount")).toContainText("Ticket: $2.00 off, approved by Branch manager");
    await expect(totals.getByTestId("pos-preview-total")).toContainText("$38.42"); // 34.00 + 13%

    // The ring carries both, each with its approver, and the paid card says who approved.
    await page.getByTestId("pos-complete-sale").click();
    const sold = page.getByTestId("sold-panel");
    await expect(sold).toBeVisible();
    const body = rung.at(-1)!;
    expect(body.lines[0].discount).toEqual({
      percent_bps: 1000,
      approved_by: "c0c0a0a0-0000-4000-8000-000000000001",
      approved_qty: 10,
      approved_base_cents: 4000,
    });
    expect(body.discount).toEqual({
      amount_cents: 200,
      approved_by: "c0c0a0a0-0000-4000-8000-000000000002",
      approved_base_cents: 3600,
    });
    const paid = sold.getByTestId("sold-discount");
    await expect(paid).toHaveCount(2);
    await expect(paid.nth(0)).toContainText("Holland Stone 60 mm");
    await expect(paid.nth(0)).toContainText("Discount approved by Sales Manager");
    await expect(paid.nth(0)).toContainText("-$4.00");
    await expect(paid.nth(1)).toContainText("Whole ticket");
    await expect(paid.nth(1)).toContainText("Discount approved by Branch Manager");
    await expect(paid.nth(1)).toContainText("-$2.00");
    await expect(sold.locator(".sold-amount")).toContainText("$38.42");
    // Round 9 (PR 249 review P3-5): the Paid card adds up by itself: the lines at their price, the
    // discounts that came off, then the subtotal they leave.
    const lines = sold.getByTestId("sold-line");
    await expect(lines).toHaveCount(1);
    await expect(lines.first()).toContainText("Holland Stone 60 mm");
    await expect(lines.first()).toContainText("$40.00");
    await expect(sold.getByTestId("sold-subtotal")).toContainText("$34.00");
    // PR 253 review P3-1: the tax the sale carried, so the card adds up to what was paid.
    await expect(sold.getByTestId("sold-tax")).toContainText("$4.42");
  });
}

test("a discount the ring refuses says so in its words, and removing it puts the plain total back", async ({ page }) => {
  await signIn(page, "stepper");
  await ensureSession(page);
  await tenHolland(page);
  // More than the whole line: the ring's own refusal reads in the total row, no figure guessed.
  await giveDiscount(page, () => page.getByTestId("pos-line-discount").click(), "amount", "500", "Sales manager · Sales Manager");
  await openPaymentStep(page, "stepper");
  const totals = page.getByTestId("pos-totals-card");
  await expect(totals.getByTestId("pos-preview-refused")).toContainText("a discount cannot be more than the line");
  await expect(totals.getByTestId("pos-preview-total")).toHaveCount(0);
  // Remove it: the total is the plain one.
  await openStep(page, "items");
  await page.getByTestId("pos-line-discount-chip").click();
  await page.getByTestId("discount-remove").click();
  await openPaymentStep(page, "stepper");
  await expect(totals.getByTestId("pos-preview-total")).toContainText("$45.20");
  await expect(totals.getByTestId("pos-ticket-discount")).toContainText("Discount the ticket");
});

test("when the managers cannot be read the dialog says so and applies nothing", async ({ page }) => {
  await page.route("**/api/v1/pos/discount-approvers**", (route) => route.fulfill({ status: 500, body: "{}" }));
  await signIn(page, "stepper");
  await ensureSession(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await openStep(page, "items");
  await page.getByTestId("pos-line-discount").click();
  await expect(page.getByTestId("discount-approvers-failed")).toContainText("could not be read");
  await page.getByTestId("discount-value").fill("10");
  await expect(page.getByTestId("discount-apply")).toBeDisabled();
  await page.getByTestId("discount-cancel").click();
  await expect(page.getByTestId("discount-dialog")).toHaveCount(0);
  await expect(page.getByTestId("pos-line-discount")).toBeVisible();
});

// The review screens: light and dark at 1440x900, Panels and Steps. Written only when asked.
const shots = process.env.DISCOUNT_SHOTS_DIR ?? "";
test.describe("review screens", () => {
  test.skip(shots === "", "DISCOUNT_SHOTS_DIR is not set");
  test.use({ viewport: { width: 1440, height: 900 } });
  for (const layout of ["stepper", "panels"] as const) {
    for (const theme of ["light", "dark"] as const) {
      test(`${layout} ${theme}`, async ({ page }) => {
        mkdirSync(shots, { recursive: true });
        await signIn(page, layout);
        await ensureSession(page);
        const control = page.getByTestId("theme-control");
        if (theme === "dark") await control.click();
        await tenHolland(page);
        const name = `${layout === "stepper" ? "steps" : "panels"}-${theme}`;
        // The dialog, filled and ready.
        await openStep(page, "items");
        await page.getByTestId("pos-line-discount").click();
        await page.getByTestId("discount-value").fill("10");
        await page.getByTestId("discount-approver").selectOption({ label: "Sales manager · Sales Manager" });
        await page.screenshot({ path: `${shots}/${name}-1-dialog.png` });
        await page.getByTestId("discount-apply").click();
        await openPaymentStep(page, layout);
        await giveDiscount(page, () => page.getByTestId("pos-ticket-discount").click(), "amount", "2", "Branch manager · Branch Manager");
        await expect(page.getByTestId("pos-preview-total")).toContainText("$38.42");
        await page.screenshot({ path: `${shots}/${name}-2-ticket.png` });
        await page.getByTestId("pos-complete-sale").click();
        await expect(page.getByTestId("sold-panel")).toBeVisible();
        await page.screenshot({ path: `${shots}/${name}-3-paid.png` });
      });
    }
  }
});

test("a manager's yes is for the line and quantity it was given: change either and the discount goes, in words", async ({
  page,
}) => {
  await signIn(page, "stepper");
  await ensureSession(page);
  await tenHolland(page);
  await giveDiscount(page, () => page.getByTestId("pos-line-discount").click(), "percent", "10", "Sales manager · Sales Manager");
  await expect(page.getByTestId("pos-line-discount-chip")).toContainText("10% off ($4.00)");
  const qty = page.getByLabel("Quantity: Holland Stone 60 mm", { exact: true });
  await qty.fill("100");
  await qty.press("Enter");
  await expect(page.getByTestId("pos-line-discount-chip")).toHaveCount(0);
  await expect(page.getByTestId("pos-line-discount")).toBeVisible();
  await expect(page.getByTestId("pos-line-row").first().locator(".ltot")).toContainText("$400.00");
  await expect(page.getByText("The discount was removed because the quantity changed. Ask a manager again.")).toBeVisible();

  // A ticket discount is for the ticket it was given on: another line removes it.
  await openStep(page, "payment");
  await giveDiscount(page, () => page.getByTestId("pos-ticket-discount").click(), "amount", "2", "Branch manager · Branch Manager");
  await expect(page.getByTestId("pos-ticket-discount-row")).toContainText("-$2.00");
  await addLine(page, "edge", "Edge Restraint 8 ft");
  await openStep(page, "payment");
  await expect(page.getByTestId("pos-ticket-discount-row")).toHaveCount(0);
  await expect(page.getByTestId("pos-ticket-discount")).toContainText("Discount the ticket");
  await expect(page.getByText("The ticket discount was removed because the ticket changed. Ask a manager again.")).toBeVisible();
});

test("a ticket discount cannot take the whole ticket: the dialog says the rule and will not apply past it", async ({ page }) => {
  await signIn(page, "stepper");
  await ensureSession(page);
  await tenHolland(page);
  await openPaymentStep(page, "stepper");
  await page.getByTestId("pos-ticket-discount").click();
  await expect(page.getByTestId("discount-ticket-rule")).toContainText(
    "A ticket discount must leave something to pay: the most you can take off is $39.99 (the ticket total less one cent).",
  );
  await page.getByTestId("discount-approver").selectOption({ label: "Sales manager · Sales Manager" });
  await page.getByTestId("discount-value").fill("100");
  await expect(page.getByTestId("discount-apply")).toBeDisabled();
  await page.getByTestId("discount-mode-amount").click();
  await page.getByTestId("discount-value").fill("40");
  await expect(page.getByTestId("discount-apply")).toBeDisabled();
  await page.getByTestId("discount-value").fill("39.99");
  await expect(page.getByTestId("discount-apply")).toBeEnabled();
  // A line discount has no such rule: it can be free beside other lines.
  await page.getByTestId("discount-cancel").click();
  await openStep(page, "items");
  await page.getByTestId("pos-line-discount").click();
  await expect(page.getByTestId("discount-ticket-rule")).toHaveCount(0);
  await page.getByTestId("discount-cancel").click();
});

// Round 9: the Summary reads like the receipt. A discounted line shows its price less its own
// discount (with the discount said under it), the ticket discount is its own row, and the lines, less
// that row, add to the subtotal.
test("the Summary shows line discounts on their lines, the ticket discount row, and a subtotal that sums", async ({
  page,
}) => {
  await signIn(page, "stepper");
  await ensureSession(page);
  await tenHolland(page);
  await openStep(page, "items");
  await page.getByTestId("pos-line-discount").click();
  await page.getByTestId("discount-value").fill("10");
  await page.getByTestId("discount-approver").selectOption({ label: "Sales manager · Sales Manager" });
  await page.getByTestId("discount-apply").click();
  await expect(page.getByTestId("pos-line-discount-chip")).toContainText("10% off");
  await openPaymentStep(page, "stepper");
  await giveDiscount(page, () => page.getByTestId("pos-ticket-discount").click(), "amount", "2", "Branch manager · Branch Manager");

  await page.getByTestId("step-head-summary").click();
  const readback = page.getByTestId("pos-readback");
  await expect(readback).toBeVisible();
  const line = readback.locator("li").first();
  // The line at its price less its own discount, and the discount said on the line.
  await expect(line.locator(".m").first()).toHaveText("$36.00");
  await expect(line.getByTestId("readback-line-discount")).toContainText("10% off");
  await expect(line.getByTestId("readback-line-discount")).toContainText("-$4.00");
  // The ticket discount is its own row; the subtotal sums the lines less it.
  await expect(readback.getByTestId("pos-ticket-discount-row")).toContainText("-$2.00");
  await expect(readback.locator(".rbrow").filter({ hasText: "Subtotal" })).toContainText("$34.00");
  const lineTotals = await readback.locator("li > .m").allInnerTexts();
  const cents = (t: string) => Math.round(Number(t.replace(/[^0-9.-]/g, "")) * 100);
  const sum = lineTotals.reduce((a, t) => a + cents(t), 0);
  expect(sum - 200).toBe(3400);
});

// Round 9 (P3 from PR 249's review): when a price rule changes server side while a discounted line
// sits on the ticket, the ring refuses the stale yes. The till then re-reads the lines' prices, so
// the discount drops with the price-changed words and the person is not stuck re-sending the same
// stale base.
test("a stale discount refusal re-reads the prices and drops the discount in words", async ({ page }) => {
  await signIn(page, "stepper");
  await ensureSession(page);
  await tenHolland(page);
  await openStep(page, "items");
  await page.getByTestId("pos-line-discount").click();
  await page.getByTestId("discount-value").fill("10");
  await page.getByTestId("discount-approver").selectOption({ label: "Sales manager · Sales Manager" });
  await page.getByTestId("discount-apply").click();
  await expect(page.getByTestId("pos-line-discount-chip")).toContainText("10% off");
  await openPaymentStep(page, "stepper");

  // From now on the price rule has moved: every resolve answers 10% higher; the ring refuses once.
  let resolves = 0;
  await page.route("**/api/v1/pricing/resolve", async (route) => {
    const real = await route.fetch();
    const body = (await real.json()) as Record<string, number>;
    resolves += 1;
    body["final_cents"] = Math.round((body["final_cents"] as number) * 1.1);
    body["line_total_cents"] = Math.round((body["line_total_cents"] as number) * 1.1);
    await route.fulfill({ response: real, json: body });
  });
  let refused = false;
  await page.route("**/api/v1/pos/sales", async (route) => {
    if (route.request().method() === "POST" && !refused) {
      refused = true;
      await route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify({ error: "pos: the discount was approved for a different quantity or price: ask a manager again" }),
      });
      return;
    }
    await route.continue();
  });
  await page.getByTestId("pos-complete-sale").click();
  await expect.poll(() => resolves, { timeout: 8000 }).toBeGreaterThan(0);
  // The discount dropped with the price words, and the Discount action is back for a fresh yes.
  await openStep(page, "items");
  await expect(page.getByTestId("pos-line-discount-chip")).toHaveCount(0);
  await expect(page.getByTestId("pos-line-discount")).toBeVisible();
  await expect(page.getByText(/price changed|price .*changed/i).first()).toBeVisible();
});

// Round 9 (P3-2): a red banner from an earlier edit (the discount removed because the quantity
// changed) does not stay on screen through a good sale, nor beside a fresh discount.
test("an earlier red banner clears on a fresh discount and on a completed sale", async ({ page }) => {
  await signIn(page, "stepper");
  await ensureSession(page);
  await tenHolland(page);
  await openStep(page, "items");
  const give = async () => {
    await page.getByTestId("pos-line-discount").click();
    await page.getByTestId("discount-value").fill("10");
    await page.getByTestId("discount-approver").selectOption({ label: "Sales manager · Sales Manager" });
    await page.getByTestId("discount-apply").click();
    await expect(page.getByTestId("pos-line-discount-chip")).toContainText("10% off");
  };
  await give();
  // Nudging the quantity removes the discount, in words (a red banner).
  const qty = page.getByLabel("Quantity: Holland Stone 60 mm", { exact: true });
  await qty.fill("9");
  await qty.press("Enter");
  const banner = page.locator("#till-message");
  await expect(banner).toContainText("discount was removed");
  // A fresh discount retires that banner.
  await give();
  await expect(banner).toHaveCount(0);
  // Remove it again, then ring: the Paid card carries no leftover error.
  await qty.fill("8");
  await qty.press("Enter");
  await expect(banner).toContainText("discount was removed");
  await openPaymentStep(page, "stepper");
  await page.getByTestId("pos-complete-sale").click();
  await expect(page.getByTestId("sold-panel")).toBeVisible();
  await expect(banner).toHaveCount(0);
});
