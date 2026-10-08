// The row 1214 ruling (tracker sequence 72): the COD fulfilment tile is out, the Pay Before
// Release payment option is in. The server offers the shape to the Cash On Demand account alone
// (the stub mirrors the core's menu field by field); the till draws what it is given, computes
// nothing, and rings the COD sale the tile used to ring: no deposit, the invoice number at ring,
// due today, fulfilment locked until paid, on the fulfilment of the chosen tile.
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

/** Mint a demo session as Sales and return the bearer header's value. */
async function salesBearer(page: Page): Promise<string> {
  const minted = await page.request.post("/api/v1/demo/session", { data: { flow: "sales" } });
  expect(minted.ok()).toBe(true);
  const body = (await minted.json()) as { token?: string };
  expect(typeof body.token).toBe("string");
  return body.token as string;
}

/** Read one committed sale back through the contract's own read. */
async function getSale(
  page: Page,
  bearer: string,
  orderId: string,
): Promise<Record<string, unknown>> {
  const read = await page.request.get(`/api/v1/pos/sales/${orderId}`, {
    headers: { Authorization: `Bearer ${bearer}`, "X-Org": "hh-demo" },
  });
  expect(read.ok()).toBe(true);
  return (await read.json()) as Record<string, unknown>;
}

test("the Cash On Demand account's payment step offers Pay Before Release, and the COD tile is gone", async ({
  page,
}) => {
  await signIn(page);
  await pickAccount(page, "C10", "Cash On Demand Landscaping");
  await addHolland(page);
  await chooseWillCall(page);
  await openStep(page, "payment");

  // The server's menu for this account: pay in full, the new option named exactly, the deposit.
  const shapes = page.getByTestId("pay-shapes");
  await expect(shapes.getByTestId("pay-shape-PAY_IN_FULL_NOW")).toBeVisible();
  const option = shapes.getByTestId("pay-shape-PAY_BEFORE_RELEASE");
  await expect(option).toHaveText("Pay Before Release");
  await expect(shapes.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER")).toBeVisible();

  // The payment step's sale column keeps the three fulfilment tiles; the COD tile is gone from
  // the whole terminal, not just this column.
  const fulfil = page.getByTestId("pay-fulfil");
  await expect(fulfil.getByRole("button", { name: "Collect now" })).toBeVisible();
  await expect(fulfil.getByRole("button", { name: "Will-call" })).toBeVisible();
  await expect(fulfil.getByRole("button", { name: "Delivery" })).toBeVisible();
  await expect(page.getByRole("button", { name: /^COD/ })).toHaveCount(0);

  // Choosing the option: the quiet posture line under the menu, no deposit panel, and the ring
  // rides the COD sale with no deposit on the Will-call tile's fulfilment.
  await option.click();
  await expect(page.getByTestId("pay-before-release-hint")).toBeVisible();
  await expect(page.getByTestId("deposit-panel")).toHaveCount(0);
  const ringPost = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/pos/sales") && request.method() === "POST",
  );
  const ringAnswer = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  const body = (await (await ringPost).postDataJSON()) as Record<string, unknown>;
  expect(body.sale_type).toBe("COD");
  expect(body.fulfilment).toBe("WILL_CALL");
  expect(body.deposit_cents).toBeUndefined();
  expect(body.tender).toBe("CASH");

  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  const committed = (await (await ringAnswer).json()) as Record<string, unknown>;
  const bearer = await salesBearer(page);
  const read = await getSale(page, bearer, committed.order_id as string);
  expect(read.sale_type).toBe("COD");
});

test("the pay before release option rides Collect now too, and only the Cash On Demand account is offered it", async ({
  page,
}) => {
  await signIn(page);
  await pickAccount(page, "C10", "Cash On Demand Landscaping");
  await addHolland(page);
  await openStep(page, "payment");

  // Collect now keeps the menu standing for the pay before release shape alone (any
  // fulfilment: the goods never leave at ring), and the standing quiet line says where the
  // deposit went.
  await expect(page.getByTestId("pay-shape-PAY_BEFORE_RELEASE")).toBeVisible();
  await expect(page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER")).toHaveCount(0);
  await expect(page.getByTestId("deposit-hint")).toHaveText("A deposit needs Will-call or Delivery.");
  await page.getByTestId("pay-shape-PAY_BEFORE_RELEASE").click();
  const ringPost = page.waitForRequest(
    (request) => request.url().endsWith("/api/v1/pos/sales") && request.method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  const body = (await (await ringPost).postDataJSON()) as Record<string, unknown>;
  expect(body.sale_type).toBe("COD");
  expect(body.fulfilment).toBe("PICKUP");
  await expect(page.getByTestId("sold-panel")).toBeVisible();

  // The credit account (terms with a line) is not offered the shape: it is the Cash On Demand
  // account's alone, and the server says so in the menu it answers.
  await signIn(page);
  await pickAccount(page, "C02", "Fenwick Builders Ltd");
  await addHolland(page);
  await chooseWillCall(page);
  await openStep(page, "payment");
  await expect(page.getByTestId("pay-shape-PAY_BEFORE_RELEASE")).toHaveCount(0);
  await expect(page.getByTestId("pay-shape-ALL_ON_ACCOUNT")).toBeVisible();

  // The retail account with no line is not offered it either: its menu is the walk-in's own.
  await signIn(page);
  await pickAccount(page, "C07", "Lachance Retail");
  await addHolland(page);
  await chooseWillCall(page);
  await openStep(page, "payment");
  await expect(page.getByTestId("pay-shape-PAY_BEFORE_RELEASE")).toHaveCount(0);
  await expect(page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER")).toBeVisible();
});
