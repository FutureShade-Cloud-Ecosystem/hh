// Round 16 (FROM-PRINCIPAL 37 ii): the Cash On Demand account's card at the payment step. The
// demo world's C10 carries the 500000 line the core's Cash On Demand rule keys on, and the card
// answered it with "Credit available $5,000.00": a figure with no meaning for an account whose
// sales pay before release. The card now says the account's posture, read against the server's
// payment options; a credit account still shows its figure, and an account with no line still
// says so.
import { expect, test, type Page } from "@playwright/test";

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

async function toPayment(page: Page, search: string, name: string): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  await page.getByRole("button", { name: /Select customer/ }).click();
  await page.getByPlaceholder("Search name or account #…").fill(search);
  await page.locator("button.row").filter({ hasText: name }).first().click();
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill("holland");
  await page.getByTestId("drop-row").filter({ hasText: "Holland Stone 60 mm" }).first().click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
  await page.getByRole("button", { name: "Will-call" }).click();
  await openStep(page, "payment");
}

test("the Cash On Demand account's card says the posture, not a credit figure; a credit account keeps its figure", async ({ page }) => {
  await toPayment(page, "C10", "Cash On Demand Landscaping");
  const codCard = page.getByTestId("pay-customer-card");
  await expect(codCard).toContainText("C10");
  await expect(codCard.getByTestId("cust-cod-posture")).toHaveText("Cash On Demand: pay before release");
  await expect(codCard).not.toContainText("Credit available");
  await expect(codCard).not.toContainText("$5,000.00");
  // The deposit sentence the payment step shows for the same account keeps saying it in the
  // server's words: the card's line and the panel's sentence are two surfaces, one posture.
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER").click();
  await expect(page.getByTestId("deposit-sentence")).toContainText("Cash On Demand means pay before release");

  await toPayment(page, "C02", "Fenwick Builders Ltd");
  const creditCard = page.getByTestId("pay-customer-card");
  await expect(creditCard).toContainText(/Credit available\s*\$[\d,]+\.\d\d/i);
  await expect(creditCard.getByTestId("cust-cod-posture")).toHaveCount(0);

  await toPayment(page, "C07", "Lachance Retail");
  const retailCard = page.getByTestId("pay-customer-card");
  await expect(retailCard).toContainText("No credit line");
  await expect(retailCard.getByTestId("cust-cod-posture")).toHaveCount(0);
});
