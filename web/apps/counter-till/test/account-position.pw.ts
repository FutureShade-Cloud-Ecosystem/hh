// UX-97: the till shows only the figures the server sends, under the dealer's account position policy.
// The stub stands at the core's default (MANAGER_UP): a Sales clerk is refused and a manager admitted.
// The refused clerk's customer search carries no limit, balance or available credit (the keys are
// absent, never 0), the picker row and the card say nothing of them or say so in words, and the account
// check names no figure. The admitted manager sees the core's available credit labelled "Available";
// on a held account the figure steps aside for "On hold" so it cannot read as "can buy". With
// UX97_SHOTS_DIR set, the legs write the picker and the card at 1440 and 390, light and charcoal.
import { expect, test, type Page } from "@playwright/test";

const shots = process.env.UX97_SHOTS_DIR ?? "";

async function signIn(page: Page, role: "Sales" | "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

async function openStep(page: Page, step: string): Promise<void> {
  const head = page.getByTestId(`step-head-${step}`);
  if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
}

/** Open the picker, search, and return the answer the server gave plus the row. */
async function search(page: Page, term: string, name: string) {
  const box = page.getByPlaceholder("Search name or account #…");
  if (!(await box.isVisible())) await page.getByRole("button", { name: /Select customer/ }).click();
  const answered = page.waitForResponse(
    (r) => r.url().includes("/api/v1/customers/search") && r.request().method() === "GET",
  );
  await box.fill(term);
  const body = (await (await answered).json()) as Array<Record<string, unknown>>;
  const row = page.locator("button.row").filter({ hasText: name }).first();
  await expect(row).toBeVisible();
  return { body, row };
}

async function pickAndOpenCard(page: Page, row: ReturnType<Page["locator"]>, name: string): Promise<void> {
  await row.click();
  const card = page.getByTestId("customer-card");
  for (let attempt = 0; attempt < 6; attempt += 1) {
    await openStep(page, "customer");
    try {
      await expect(card).toContainText(name, { timeout: 700 });
      return;
    } catch {
      // the advance closed the step again: open it once more
    }
  }
  await expect(card).toContainText(name);
}

async function theme(page: Page, which: "light" | "charcoal"): Promise<void> {
  await page.evaluate((t) => {
    if (t === "light") document.documentElement.setAttribute("data-theme", "light");
    else document.documentElement.removeAttribute("data-theme");
  }, which);
}

const FIGURE_KEYS = ["credit_limit_cents", "open_balance_cents", "available_credit_cents"];

test("a refused clerk's search carries no figure, the row and the card say nothing of one, and the account check names none", async ({
  page,
}) => {
  await signIn(page, "Sales");
  const { body, row } = await search(page, "C02", "Fenwick Builders Ltd");
  expect(body.length).toBeGreaterThan(0);
  for (const hit of body) for (const key of FIGURE_KEYS) expect(Object.keys(hit), `${key} is absent, not zero`).not.toContain(key);
  await expect(row.locator(".credit")).toHaveCount(0);
  await expect(row).not.toContainText("$");

  await pickAndOpenCard(page, row, "Fenwick Builders Ltd");
  const card = page.getByTestId("customer-card");
  await expect(card.getByTestId("cust-position-withheld")).toHaveText("Credit position not shown for your role");
  await expect(card).not.toContainText("Available");
  await expect(card).not.toContainText("Outstanding");
  await expect(card).not.toContainText("derived");
  // The past due fact is the standing read, not the account position, and stays.
  await expect(page.getByTestId("cust-past-due")).toBeVisible();

  // The account check says so in words; it never claims a zero credit line.
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill("charcoal");
  await page.getByTestId("drop-row").filter({ hasText: "Charcoal Stone 60 mm" }).first().click();
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Charge Account" }).click();
  const check = page.getByTestId("pos-account-check");
  await expect(check).toHaveAttribute("data-standing", "good_standing");
  await expect(check).toContainText("Credit position not shown for your role");
  await expect(check).not.toContainText("No credit available");
  await expect(check).not.toContainText("used up");
  await expect(check).not.toContainText("$");
});

test("a refused clerk meets a held account's hold in words, with no figure", async ({ page }) => {
  await signIn(page, "Sales");
  const { row } = await search(page, "C09", "Harbour Drywall Inc");
  await pickAndOpenCard(page, row, "Harbour Drywall Inc");
  const card = page.getByTestId("customer-card");
  await expect(card.getByTestId("cust-on-hold")).toBeVisible();
  await expect(card.getByTestId("cust-position-withheld")).toBeVisible();
  await expect(card.getByTestId("cust-available")).toHaveCount(0);
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill("charcoal");
  await page.getByTestId("drop-row").filter({ hasText: "Charcoal Stone 60 mm" }).first().click();
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Charge Account" }).click();
  const check = page.getByTestId("pos-account-check");
  await expect(check).toHaveAttribute("data-standing", "blocked");
  await expect(check).toContainText("Credit position not shown for your role, and the account is on hold");
  await expect(check).not.toContainText("Available credit");
});

test("an admitted manager sees the core's available credit labelled Available, never the browser's subtraction", async ({
  page,
}) => {
  await signIn(page, "Sales manager");
  const { body, row } = await search(page, "C02", "Fenwick Builders Ltd");
  const hit = body.find((h) => h.account_no === "C02") as Record<string, number>;
  expect(hit.credit_limit_cents).toBe(300000);
  expect(hit.open_balance_cents).toBe(87500);
  expect(hit.available_credit_cents).toBe(212500);
  await expect(row.locator(".credit")).toContainText("Available");
  await expect(row.locator(".credit")).toContainText("$2,125.00");
  await expect(row.locator(".credit")).not.toContainText("derived");
  await pickAndOpenCard(page, row, "Fenwick Builders Ltd");
  const card = page.getByTestId("customer-card");
  await expect(card.getByTestId("cust-available")).toHaveText(/Available\s*\$2,125\.00/);
  await expect(card).toContainText("Outstanding");
  await expect(card.getByTestId("cust-position-withheld")).toHaveCount(0);
});

test("on a held account the available figure steps aside for On hold beside the hold tag", async ({ page }) => {
  await signIn(page, "Sales manager");
  const { row } = await search(page, "C09", "Harbour Drywall Inc");
  await pickAndOpenCard(page, row, "Harbour Drywall Inc");
  const card = page.getByTestId("customer-card");
  await expect(card.getByTestId("cust-on-hold")).toHaveText("Credit hold");
  // No green headroom beside the red hold tag: the figure's place says On hold.
  await expect(card.getByTestId("cust-available")).toHaveCount(0);
  await expect(card.getByTestId("cust-available-on-hold")).toHaveText(/Available\s*On hold/);
  await expect(card.locator(".num.avail")).toHaveCount(0);
});

if (shots !== "") {
  for (const [width, height] of [
    [1440, 900],
    [390, 844],
  ] as const) {
    for (const mode of ["light", "charcoal"] as const) {
      for (const [role, label] of [
        ["Sales manager", "allowed"],
        ["Sales", "refused"],
      ] as const) {
        test(`shots: the picker and the card at ${width}, ${mode}, ${label}`, async ({ page }) => {
          await page.setViewportSize({ width, height });
          await signIn(page, role);
          await theme(page, mode);
          const { row } = await search(page, "C0", "Fenwick Builders Ltd");
          await page.screenshot({ path: `${shots}/picker-${width}-${mode}-${label}.png` });
          await pickAndOpenCard(page, row, "Fenwick Builders Ltd");
          await theme(page, mode);
          await page.screenshot({ path: `${shots}/card-${width}-${mode}-${label}.png` });
          // A held account: the hold beside the figure's place.
          await page.getByTestId("customer-change").click();
          const held = await search(page, "C09", "Harbour Drywall Inc");
          await pickAndOpenCard(page, held.row, "Harbour Drywall Inc");
          await theme(page, mode);
          await page.screenshot({ path: `${shots}/card-held-${width}-${mode}-${label}.png` });
        });
      }
    }
  }
}
