// W2-4 (b): receivables against the real core.
//
// WHAT THIS PROVES. The Money Desk's receivables landing on a real core box:
// the aging buckets the screen carries, the open invoices the register lists
// and the foot it totals, the tie of that total to account 1100, a customer's
// statement, and a received payment applied.
//
// THE ASSUMPTIONS (the harness interface, W2-4-HARNESS-INTERFACE-r1):
//   1. asStaff(context, "financial_controller") plants the world's financial
//      controller's session on the browser context, so page.goto("/") lands
//      signed in on the real core. The controller lands on the ledger; the
//      receivables desk is one tap away.
//   2. coreURL() is the box's base URL. The spec reads the core's own API
//      through it with the very credentials the app presented, so a figure the
//      screen shows is compared with the core's own answer and never with the
//      app's own arithmetic.
//   3. world.ts holds the world's known figures. Until W2-1b's world lands they
//      read today's seed value, and world.ts's header names the line that
//      switches each one to the world's figure.
//   4. The box is fresh for a run: the payment leg writes to the book and the
//      tie leg reads the book before that write. box.sh up and box.sh down are
//      the harness's way (part (a)).
//   5. The Money Desk is served with its /api going to the core, so the spec's
//      own read of the core's API and the app's own calls are the same server.
import { expect, test } from "@playwright/test";
import type { BrowserContext, Page } from "@playwright/test";
import { asStaff, coreURL } from "./session.js";
import {
  AR_1100_GL_CENTS,
  AR_AGING_CARDS,
  AR_CONTROL_ACCOUNT_CODE,
  AR_OPEN_INVOICES,
  AR_PAYMENT_CENTS,
  AR_PAYMENT_INVOICE_NO,
  AR_REGISTER_CENTS,
  AR_REGISTER_COUNT,
  AR_STATEMENT_CLOSING_CENTS,
  AR_STATEMENT_CUSTOMER_NAME,
  AR_STATEMENT_STRIP,
  AR_TOTAL_CENTS,
} from "./world.js";

/** The credentials the app itself presented, read off its own first call. */
interface CoreCredentials {
  readonly bearer: string;
  readonly org: string;
}

interface CoreLanding {
  readonly cards: readonly { readonly id: string; readonly total_cents: number; readonly accounts: number }[];
}

interface CoreRegisterPage {
  readonly counts: { readonly open: number; readonly paid: number };
  readonly items: readonly {
    readonly id: string;
    readonly invoice_no: string;
    readonly customer_name: string;
    readonly status: string;
    readonly balance_cents: number;
  }[];
}

interface CoreTieoutPage {
  readonly items: readonly {
    readonly subledger: string;
    readonly account_code: string;
    readonly subledger_cents: number;
    readonly gl_cents: number;
    readonly delta_cents: number;
    readonly in_tie: boolean;
  }[];
}

/** Cents as the desk reads them: 1087000 reads "$10,870.00". Built from the digits, never a float. */
function money(cents: number): string {
  const negative = cents < 0;
  const digits = String(Math.abs(cents)).padStart(3, "0");
  const grouped = digits.slice(0, -2).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `${negative ? "-" : ""}$${grouped}.${digits.slice(-2)}`;
}

/** Signs in as the world's financial controller and lands on the receivables desk. */
async function openDesk(page: Page, context: BrowserContext): Promise<CoreCredentials> {
  const credentials: { bearer: string; org: string } = { bearer: "", org: "" };
  page.on("request", (request) => {
    if (credentials.bearer !== "" || !request.url().includes("/api/v1/")) return;
    credentials.bearer = request.headers()["authorization"] ?? "";
    credentials.org = request.headers()["x-org"] ?? "";
  });
  await asStaff(context, "financial_controller");
  await page.goto("/");
  // The controller lands on the ledger; the receivables desk is one tap away.
  const receivables = page.getByRole("button", { name: "Receivables", exact: true });
  await expect(receivables).toBeVisible({ timeout: 30_000 });
  await receivables.click();
  await expect(page.locator("[data-card]")).toHaveCount(AR_AGING_CARDS.length);
  expect(credentials.bearer).not.toBe("");
  return credentials;
}

/** The core's own answer, read with the app's own credentials. */
async function coreRead<T>(credentials: CoreCredentials, path: string): Promise<T> {
  const response = await fetch(`${coreURL()}${path}`, {
    headers: { authorization: credentials.bearer, "x-org": credentials.org },
  });
  if (!response.ok) {
    throw new Error(`the core refused ${path}: ${response.status} ${await response.text()}`);
  }
  return (await response.json()) as T;
}

/** The core's own answer to a write-shaped read, with the app's own credentials. */
async function corePost<T>(credentials: CoreCredentials, path: string, body: unknown): Promise<T> {
  const response = await fetch(`${coreURL()}${path}`, {
    method: "POST",
    headers: {
      authorization: credentials.bearer,
      "x-org": credentials.org,
      "content-type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    throw new Error(`the core refused ${path}: ${response.status} ${await response.text()}`);
  }
  return (await response.json()) as T;
}

/** The register's rows as the screen shows them: the invoice, the account and the balance. */
async function readRows(page: Page): Promise<{ invoice: string; customer: string; account: string; balance: string }[]> {
  return page.locator("[data-register-row]").evaluateAll((nodes) =>
    nodes.map((row) => {
      const cells = row.querySelectorAll("td");
      const text = (cell: Element | undefined, selector: string): string =>
        cell?.querySelector(selector)?.textContent?.trim() ?? "";
      return {
        invoice: text(cells[0], "span.who"),
        customer: text(cells[1], "span.who"),
        account: text(cells[1], "span.dim"),
        balance: cells[5]?.textContent?.trim() ?? "",
      };
    }),
  );
}

test("the register carries the world's aging buckets and lists the world's open invoices", async ({ page, context }) => {
  const credentials = await openDesk(page, context);

  // The buckets on the screen are the core's own cards, in the core's own order.
  const cards = page.locator("[data-card]");
  await expect(cards).toHaveCount(AR_AGING_CARDS.length);
  expect(await cards.evaluateAll((nodes) => nodes.map((node) => node.getAttribute("data-card") ?? ""))).toEqual(
    AR_AGING_CARDS.map(([id]) => id),
  );
  expect(await page.locator("[data-card] .tile-figure").allTextContents()).toEqual(
    AR_AGING_CARDS.map(([, cents]) => money(cents)),
  );
  for (const [index, [, , accounts]] of AR_AGING_CARDS.entries()) {
    await expect(cards.nth(index)).toContainText(`${accounts} ${accounts === 1 ? "account" : "accounts"}`);
  }

  // And the core's landing says the same thing about its own cards.
  const landing = await coreRead<CoreLanding>(credentials, "/api/v1/ar/landing");
  expect(landing.cards.map((card) => [card.id, card.total_cents, card.accounts])).toEqual(
    AR_AGING_CARDS.map(([id, cents, accounts]) => [id, cents, accounts]),
  );

  // The register lists every open invoice of the world, and nothing else.
  await expect(page.locator("[data-register-row]")).toHaveCount(AR_REGISTER_COUNT);
  const rows = await readRows(page);
  const shownBalances = rows.map((row): [string, string] => [row.invoice, row.balance]);
  shownBalances.sort((left, right) => left[0].localeCompare(right[0]));
  const wantedBalances = AR_OPEN_INVOICES.map(([invoice, cents]): [string, string] => [invoice, money(cents)]);
  wantedBalances.sort((left, right) => left[0].localeCompare(right[0]));
  expect(shownBalances).toEqual(wantedBalances);
  const paid = rows.find((row) => row.invoice === AR_PAYMENT_INVOICE_NO);
  expect(paid).not.toBeUndefined();
  expect(paid?.customer).toBe(AR_STATEMENT_CUSTOMER_NAME);

  // The foot is the server's own sum of the rows it listed, not the browser's.
  await expect(page.locator("[data-register-foot]")).toContainText(
    `${AR_REGISTER_COUNT} ${AR_REGISTER_COUNT === 1 ? "invoice" : "invoices"}, ${money(AR_REGISTER_CENTS)}`,
  );
  const register = await coreRead<CoreRegisterPage>(credentials, "/api/v1/ar/register");
  expect(register.counts.open).toBe(AR_REGISTER_COUNT);
  const open = register.items.filter((item) => item.status !== "PAID");
  const coreOpen = open.map((item): [string, number] => [item.invoice_no, item.balance_cents]);
  coreOpen.sort((left, right) => left[0].localeCompare(right[0]));
  const wantedOpen = AR_OPEN_INVOICES.map(([invoice, cents]): [string, number] => [invoice, cents]);
  wantedOpen.sort((left, right) => left[0].localeCompare(right[0]));
  expect(coreOpen).toEqual(wantedOpen);
  const shown = open.map((item) => item.id);
  const selection = await corePost<{ count: number; balance_cents: number }>(credentials, "/api/v1/ar/register/selection", {
    invoice_ids: shown,
  });
  expect(selection).toEqual({ count: AR_REGISTER_COUNT, balance_cents: AR_REGISTER_CENTS });
});

test("the aging buckets tie to account 1100", async ({ page, context }) => {
  const credentials = await openDesk(page, context);

  // The figures on the screen are the world's card figures, as the register
  // leg reads them: a blank screen fails here, never a computed world alone.
  const figures = await page.locator("[data-card] .tile-figure").allTextContents();
  expect(figures).toEqual(AR_AGING_CARDS.map(([, cents]) => money(cents)));
  const shown = AR_AGING_CARDS.reduce((total, [, cents]) => total + cents, 0);
  expect(shown).toBe(AR_TOTAL_CENTS);

  // The core's own tie-out reads the same subledger, and says what the GL side holds.
  const tieout = await coreRead<CoreTieoutPage>(credentials, "/api/v1/gl/tie-out");
  const receivables = tieout.items.filter((item) => item.account_code === AR_CONTROL_ACCOUNT_CODE);
  expect(receivables).toHaveLength(1);
  const tie = receivables[0];
  expect(tie.subledger).toBe("AR");
  expect(tie.subledger_cents).toBe(shown);
  expect(tie.gl_cents).toBe(AR_1100_GL_CENTS);
  expect(tie.delta_cents).toBe(tie.subledger_cents - tie.gl_cents);
  expect(tie.in_tie).toBe(tie.delta_cents === 0);
});

test("a customer statement closes on the world's balance", async ({ page, context }) => {
  const credentials = await openDesk(page, context);

  // The account id is read off the register's own row, before the account opens.
  const customer = await customerId(page, AR_STATEMENT_CUSTOMER_NAME);
  await page
    .locator("[data-register-row]", { hasText: AR_PAYMENT_INVOICE_NO })
    .getByRole("button", { name: "Record payment" })
    .click();
  await expect(page.getByRole("heading", { name: AR_STATEMENT_CUSTOMER_NAME })).toBeVisible();
  await page.getByRole("button", { name: "Statement", exact: true }).click();

  const run = page.getByRole("button", { name: "Run a statement" });
  if ((await run.count()) > 0) await run.click();

  await expect(page.locator("[data-closing]")).toContainText(money(AR_STATEMENT_CLOSING_CENTS));
  const bands = await page.locator("[data-statement] dl.strip dt").allTextContents();
  expect(bands).toEqual(AR_STATEMENT_STRIP.map(([band]) => band));
  const amounts = await page.locator("[data-statement] dl.strip dd").allTextContents();
  // The last cell is the closing balance, and it carries the screen reader's
  // label with the figure: the four aging bands are the ones to compare.
  expect(amounts.slice(0, -1)).toEqual(AR_STATEMENT_STRIP.slice(0, -1).map(([, cents]) => money(cents)));
  expect(amounts[amounts.length - 1]).toContain(money(AR_STATEMENT_CLOSING_CENTS));

  // The core's own statement agrees with what the screen printed.
  const statement = await coreRead<{ current_cents: number; closing_balance_cents: number }>(
    credentials,
    `/api/v1/ar/customers/${customer}/statement`,
  );
  expect(statement.closing_balance_cents).toBe(AR_STATEMENT_CLOSING_CENTS);
});

test("a received payment is applied and the invoice closes", async ({ page, context }) => {
  const credentials = await openDesk(page, context);

  await page
    .locator("[data-register-row]", { hasText: AR_PAYMENT_INVOICE_NO })
    .getByRole("button", { name: "Record payment" })
    .click();
  await expect(page.getByRole("heading", { name: AR_STATEMENT_CUSTOMER_NAME })).toBeVisible();

  // The panel opens with the invoice picked and the server's split already in hand.
  await expect(page.locator("[data-field='receive-amount']")).toHaveValue(money(AR_PAYMENT_CENTS));
  await expect(page.locator("[data-applied]")).toHaveText(`${money(AR_PAYMENT_CENTS)} of ${money(AR_PAYMENT_CENTS)}`);
  await expect(page.locator("[data-allocation]")).toContainText(`${AR_PAYMENT_INVOICE_NO} · closes`);

  await page.locator("[data-field='receive-reference']").fill("W2-4B-1");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText(AR_PAYMENT_INVOICE_NO);
  await expect(page.locator("[data-notice]")).toContainText(money(AR_PAYMENT_CENTS));

  // The core's own book agrees: the invoice is paid and owes nothing.
  const register = await coreRead<CoreRegisterPage>(credentials, "/api/v1/ar/register");
  const invoice = register.items.find((item) => item.invoice_no === AR_PAYMENT_INVOICE_NO);
  expect(invoice).not.toBeUndefined();
  expect(invoice?.status).toBe("PAID");
  expect(invoice?.balance_cents).toBe(0);
});

/** The customer id the core holds for the named account, read off the screen's own row. */
async function customerId(page: Page, name: string): Promise<string> {
  const id = await page
    .locator("[data-register-row]", { hasText: name })
    .first()
    .locator("[data-action='workspace-open']")
    .getAttribute("data-customer-id");
  if (id === null) throw new Error(`the register lists no account named ${name}`);
  return id;
}
