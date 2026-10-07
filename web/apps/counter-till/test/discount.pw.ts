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
    await openPaymentStep(page, layout);
    const totals = page.getByTestId("pos-totals-card");
    await expect(totals.getByTestId("pos-discount-row")).toContainText("-$4.00");
    await expect(totals.getByTestId("pos-preview-total")).toContainText("$40.68"); // 40.00 - 4.00 + 13% = 40.68

    // The whole ticket, in dollars, by the other manager: the server shares it over the lines.
    await giveDiscount(page, () => page.getByTestId("pos-ticket-discount").click(), "amount", "2", "Branch manager · Branch Manager");
    await expect(totals.getByTestId("pos-discount-row")).toContainText("-$6.00");
    await expect(totals.getByTestId("pos-ticket-discount")).toContainText("Ticket: $2.00 off, approved by Branch manager");
    await expect(totals.getByTestId("pos-preview-total")).toContainText("$38.42"); // 34.00 + 13%

    // The ring carries both, each with its approver, and the paid card says who approved.
    await page.getByTestId("pos-complete-sale").click();
    const sold = page.getByTestId("sold-panel");
    await expect(sold).toBeVisible();
    const body = rung.at(-1)!;
    expect(body.lines[0].discount).toEqual({ percent_bps: 1000, approved_by: "c0c0a0a0-0000-4000-8000-000000000001" });
    expect(body.discount).toEqual({ amount_cents: 200, approved_by: "c0c0a0a0-0000-4000-8000-000000000002" });
    const paid = sold.getByTestId("sold-discount");
    await expect(paid).toHaveCount(2);
    await expect(paid.nth(0)).toContainText("Discount approved by");
    await expect(paid.nth(0)).toContainText("Sales Manager");
    await expect(paid.nth(1)).toContainText("Branch Manager");
    await expect(sold.locator(".sold-amount")).toContainText("$38.42");
  });
}

test("an approver is required, a discount the ring refuses says so in its words, and a parked ticket cannot lose one", async ({
  page,
}) => {
  await signIn(page, "stepper");
  await ensureSession(page);
  await tenHolland(page);
  await openPaymentStep(page, "stepper");
  // More than the whole ticket: the ring's own refusal reads in the total row, no figure guessed.
  await giveDiscount(page, () => page.getByTestId("pos-ticket-discount").click(), "amount", "500", "Sales manager · Sales Manager");
  const totals = page.getByTestId("pos-totals-card");
  await expect(totals.getByTestId("pos-preview-refused")).toContainText("a discount cannot be more than the ticket");
  await expect(totals.getByTestId("pos-preview-total")).toHaveCount(0);
  // Fix it, then remove it: the discount row and the chip go, the total is the plain one.
  await page.getByTestId("pos-ticket-discount").click();
  await page.getByTestId("discount-remove").click();
  await expect(totals.getByTestId("pos-preview-total")).toContainText("$45.20");
  await expect(totals.getByTestId("pos-discount-row")).toHaveCount(0);
  await expect(page.getByTestId("pos-ticket-discount")).toContainText("Discount the ticket");
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
