// Item 9.6 (row 1047 item 6): the deposit at the payment step. The menu and line two's words come
// from the preview's payment_options; line one is the deposit (Card or Cash), line two the rest;
// split tender stays reachable as a quiet secondary choice. The stub mirrors the core's rules.
import { expect, test, type Page } from "@playwright/test";

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

async function pickAccount(page: Page, search: string, name: string): Promise<void> {
  await page.getByRole("button", { name: /Select customer/ }).click();
  await page.getByPlaceholder("Search name or account #…").fill(search);
  const row = page.locator("button.row").filter({ hasText: name }).first();
  await expect(row).toBeVisible();
  await row.click();
}

async function addHolland(page: Page): Promise<void> {
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill("holland");
  const row = page.getByTestId("drop-row").filter({ hasText: "Holland Stone 60 mm" }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
}

async function chooseWillCall(page: Page): Promise<void> {
  await openStep(page, "items");
  await page.getByRole("button", { name: "Will-call" }).click();
}

test("a credit customer's payment step offers the deposit, and the ring carries it on account", async ({ page }) => {
  await signIn(page);
  await pickAccount(page, "C02", "Fenwick Builders Ltd");
  await addHolland(page);
  await chooseWillCall(page);
  await openStep(page, "payment");

  await expect(page.getByTestId("pay-shape-PAY_IN_FULL_NOW")).toBeVisible();
  await expect(page.getByTestId("pay-shape-ALL_ON_ACCOUNT")).toBeVisible();
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_ON_ACCOUNT").click();

  const panel = page.getByTestId("deposit-panel");
  await expect(panel).toContainText("THE DEPOSIT, CASH OR CARD");
  await expect(panel).toContainText("DEPOSIT AND BALANCE");
  await panel.getByRole("button", { name: "Cash", exact: true }).click();
  await expect(page.getByTestId("deposit-line-one")).toContainText("Deposit · Cash");
  await expect(page.getByTestId("deposit-line-one")).toContainText("now");
  await page.getByTestId("deposit-amount").fill("1.00");
  const lineTwo = page.getByTestId("deposit-line-two");
  await expect(lineTwo).toContainText("On account");
  await expect(lineTwo).toContainText("later");
  // The rest is the server's total less the deposit: $3.07 on a $4.07 sale.
  await expect(lineTwo).toContainText("$3.07");
  await expect(page.getByTestId("deposit-sentence")).toHaveText(
    "A credit customer: the rest rides their account and is paid from the statement.",
  );
  await expect(page.getByTestId("deposit-commit")).toHaveText("Ring sale with $1.00 deposit");

  const ringPost = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/pos/sales") && request.method() === "POST",
  );
  await page.getByTestId("deposit-commit").click();
  const body = (await (await ringPost).postDataJSON()) as Record<string, unknown>;
  expect(body.tender).toBe("ACCOUNT");
  expect(body.sale_type).toBe("COLLECT_LATER");
  expect(body.deposit_cents).toBe(100);
  expect(body.deposit_method).toBe("CASH");
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await expect(sold).toContainText("Deposit taken");
});

test("the Cash On Demand customer's step says the balance is due later, and the ring rides the pay before release sale", async ({
  page,
}) => {
  await signIn(page);
  await pickAccount(page, "C10", "Cash On Demand Landscaping");
  await addHolland(page);
  await chooseWillCall(page);
  await openStep(page, "payment");

  await expect(page.getByTestId("pay-shape-ALL_ON_ACCOUNT")).toHaveCount(0);
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER").click();
  await page.getByTestId("deposit-amount").fill("1.00");
  await expect(page.getByTestId("deposit-line-two")).toContainText("Balance due later");
  // The Cash On Demand account carries no rule price: $4.52 less the deposit.
  await expect(page.getByTestId("deposit-line-two")).toContainText("$3.52");
  await expect(page.getByTestId("deposit-sentence")).toHaveText(
    "Cash On Demand means pay before release, never cash on delivery: the deposit puts the order in process, and picking waits for paid in full.",
  );

  const ringPost = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/pos/sales") && request.method() === "POST",
  );
  await page.getByTestId("deposit-commit").click();
  const body = (await (await ringPost).postDataJSON()) as Record<string, unknown>;
  expect(body.sale_type).toBe("COD");
  expect(body.fulfilment).toBe("WILL_CALL");
  expect(body.deposit_cents).toBe(100);
  await expect(page.getByTestId("sold-panel")).toBeVisible();
});

test("a card deposit rides a card token and the card type shows", async ({ page }) => {
  await signIn(page);
  await pickAccount(page, "C10", "Cash On Demand Landscaping");
  await addHolland(page);
  await chooseWillCall(page);
  await openStep(page, "payment");
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER").click();
  await page.getByTestId("deposit-panel").getByRole("button", { name: "Card", exact: true }).click();
  await expect(page.getByTestId("deposit-line-one")).toContainText("Deposit · Card");
  await page.getByTestId("deposit-amount").fill("2.00");
  const ringPost = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/pos/sales") && request.method() === "POST",
  );
  await page.getByTestId("deposit-commit").click();
  const body = (await (await ringPost).postDataJSON()) as Record<string, unknown>;
  expect(body.deposit_method).toBe("CARD");
  expect(typeof body.payment_token).toBe("string");
});

test("collect now offers no deposit, and the server's refusal shows in its words", async ({ page }) => {
  await signIn(page);
  await pickAccount(page, "C02", "Fenwick Builders Ltd");
  await addHolland(page);
  await openStep(page, "payment");
  await expect(page.getByTestId("pay-shape-PAY_IN_FULL_NOW")).toHaveCount(0);
  await expect(page.getByTestId("pay-shape-DEPOSIT_NOW_REST_ON_ACCOUNT")).toHaveCount(0);
  await expect(page.getByTestId("deposit-panel")).toHaveCount(0);

  // Even so, an answer of deposit_not_collect_now is worded, never coded.
  await page.route("**/api/v1/pos/sales", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    await route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        error: "deposit_not_collect_now",
        total_cents: 407,
        deposit_cents: 100,
        message: "A collect now sale is paid in full, the goods do not leave on a deposit. Take the whole $4.07 now.",
      }),
    });
  });
  await page.getByTestId("pos-complete-sale").click();
  await expect(page.getByRole("alert")).toContainText("Take the whole $4.07 now.");
  await expect(page.getByRole("alert")).not.toContainText("deposit_not_collect_now");
});

test("split tender stays reachable, quiet and secondary, with its behaviour unchanged", async ({ page }) => {
  await signIn(page);
  await addHolland(page);
  await openStep(page, "payment");
  const split = page.getByTestId("tender-split");
  await expect(split).toHaveText("Split between two tenders");
  await split.click();
  await expect(page.getByTestId("cta-split-state")).toContainText("Split: compose the legs below");
});

test("at 390 the deposit step does not scroll sideways and no label clips", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await pickAccount(page, "C10", "Cash On Demand Landscaping");
  await addHolland(page);
  await chooseWillCall(page);
  await openStep(page, "payment");
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER").click();
  await page.getByTestId("deposit-amount").fill("1.00");
  await expect(page.getByTestId("deposit-commit")).toBeVisible();
  const read = await page.evaluate(() => {
    const clipped: string[] = [];
    const panel = document.querySelector("hos-pos-terminal")?.shadowRoot?.querySelector('[data-testid="deposit-panel"]');
    for (const el of Array.from(panel?.querySelectorAll("button, span, label, .sentence") ?? [])) {
      const node = el as HTMLElement;
      if (node.scrollWidth > node.clientWidth + 1 && getComputedStyle(node).overflow !== "visible") clipped.push(node.textContent?.trim() ?? "");
    }
    return { doc: document.documentElement.scrollWidth, view: window.innerWidth, clipped };
  });
  expect(read.doc).toBeLessThanOrEqual(read.view);
  expect(read.clipped).toEqual([]);
});

// Item 9.5: a supplier's demanded deposit on a special order, for a credit customer: one quiet
// control on the payment step (shown only while the tender tiles stand and the server allows),
// the amount billed to the account as its own invoice, and the Summary's line for it.
test("a credit customer's supplier deposit is billed to the account", async ({ page }) => {
  await signIn(page);
  await pickAccount(page, "C02", "Fenwick Builders Ltd");
  await addHolland(page);
  await chooseWillCall(page);
  await openStep(page, "payment");

  // The control stands beside the tender tiles (pay in full now, no deposit shape chosen), only
  // for the account tender: on Cash it is gone.
  await expect(page.getByTestId("supplier-deposit")).toHaveCount(0);
  await page.getByRole("button", { name: "Charge Account" }).click();
  await expect(page.getByTestId("supplier-deposit")).toBeVisible();
  await expect(page.getByTestId("supplier-deposit")).toContainText("Supplier asked for a deposit");
  await page.getByTestId("supplier-deposit-amount").fill("1.00");
  await expect(page.getByTestId("supplier-deposit")).toContainText("billed to the account");

  const ringPost = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/pos/sales") && request.method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  const body = (await (await ringPost).postDataJSON()) as Record<string, unknown>;
  expect(body.tender).toBe("ACCOUNT");
  expect(body.supplier_deposit_cents).toBe(100);
  expect(body.deposit_cents).toBeUndefined();
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await expect(sold).toContainText("Supplier deposit, billed to the account: $1.00");
});

// The Cash On Demand account never sees the control: its deposit is line one at the counter.
test("the Cash On Demand customer sees no supplier deposit control", async ({ page }) => {
  await signIn(page);
  await pickAccount(page, "C10", "Cash On Demand Landscaping");
  await addHolland(page);
  await chooseWillCall(page);
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Charge Account" }).click().catch(() => undefined);
  await expect(page.getByTestId("supplier-deposit")).toHaveCount(0);
});
