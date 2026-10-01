// W7's exit test, run as Sales against the stubbed harness (the stub serves
// the counter routes' contract shapes; the vite dev server proxies /api to
// it). The story this holds, screen by screen from the plan's section 2: the
// drawer is found or opened on a first use, three lines ring at the walk-in's
// retail and the sale read answers every figure the screen showed, a card
// sale carries the simulated notice on the tender and the receipt (and shows
// none when the rail answers false), an account sale shows its invoice and
// its balance, an account on hold is refused in words, a contractor's Holland
// pavers ring at the account's rule price with the margin shown, the
// quantity the till sends is the one typed on the screen, the requested date
// is the house zone's day, and a ticket parks and resumes priced fresh: for a
// walk-in, and across a sign-out for an account's ticket.
//
// The sale read comparison works through the real path: this spec mints its
// own demo session (the same public route the app's cards use) and reads the
// committed sale back through the same proxy, with the bearer and org header
// the staff routes require.
import { expect, test, type Page } from "@playwright/test";

/** The screen-side rendering of integer cents, for comparing against the app. */
function dollars(cents: number): string {
  const negative = cents < 0;
  const magnitude = Math.abs(cents);
  const whole = Math.floor(magnitude / 100);
  const remainder = magnitude - whole * 100;
  return `${negative ? "-" : ""}$${whole.toLocaleString("en-CA")}.${String(remainder).padStart(2, "0")}`;
}

/** The dealer's day, computed the way the stub validates it, for comparison. */
function houseDay(): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "America/Toronto",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date());
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

async function searchAndAdd(page: Page, query: string, hitName: string): Promise<void> {
  await page.getByLabel("Search the catalogue by sku or name").fill(query);
  await page.getByRole("button", { name: "Search", exact: true }).click();
  const hit = page.getByRole("button", { name: `Add ${hitName} to the ticket` });
  await expect(hit).toBeVisible();
  await hit.click();
}

test("a walk-in cash sale of three lines rings with change due, and the sale read equals every figure on screen", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();

  // The drawer: a first use opens it with the invented float.
  const drawer = page.getByTestId("drawer-status");
  await expect(drawer).toContainText("Trenton MAIN: open with a float of $200.00");

  // Three lines, one quantity typed and re-priced.
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await searchAndAdd(page, "polymeric", "Polymeric Locking Sand");
  await searchAndAdd(page, "clear", "Clear Stone");
  const lines = page.getByTestId("ticket-line");
  await expect(lines).toHaveCount(3);
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$4.00");
  const hollandQty = page.getByLabel("Quantity of Holland Stone 60 mm");
  await hollandQty.fill("3");
  await page.getByRole("button", { name: "Re-price the ticket" }).click();
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$4.00");

  // The walk-in's retail, with the margin column answered for every line
  // that carries a cost at this branch: the edge restraint is not on this
  // ticket, so all three show a percent.
  const margins = page.getByTestId("line-margin");
  await expect(margins).toHaveCount(3);
  for (let index = 0; index < 3; index += 1) {
    await expect(margins.nth(index)).toContainText("%");
  }

  // Tender cash and ring.
  await page.getByLabel("Cash tendered, in dollars").fill("80.00");
  await page.getByRole("button", { name: "Ring the sale" }).click();

  // The receipt, figure by figure.
  const receipt = page.getByTestId("receipt");
  await expect(receipt).toBeVisible();
  const orderNo = await receipt.getByTestId("receipt-order-no").textContent();
  const invoiceNo = await receipt.getByTestId("receipt-invoice-no").textContent();
  expect(orderNo).toMatch(/^ORD-/);
  expect(invoiceNo).toMatch(/^INV-/);
  await expect(receipt.getByTestId("receipt-total")).toContainText("$");
  // A cash receipt carries no card notice, whatever the rail answers.
  await expect(receipt.getByTestId("receipt-simulated-notice")).toHaveCount(0);

  // The read path: mint, read, and hold every figure against the screen.
  const bearer = await salesBearer(page);
  const orderId = await receipt.getAttribute("data-order-id");
  expect(orderId).toBeTruthy();
  const sale = await getSale(page, bearer, orderId as string);
  expect(sale.order_no).toBe(orderNo);
  expect(sale.invoice_no).toBe(invoiceNo);
  await expect(receipt.getByTestId("receipt-subtotal")).toHaveText(dollars(sale.subtotal_cents as number));
  await expect(receipt.getByTestId("receipt-tax")).toHaveText(dollars(sale.tax_cents as number));
  await expect(receipt.getByTestId("receipt-total")).toHaveText(dollars(sale.total_cents as number));
  await expect(receipt.getByTestId("receipt-tendered")).toHaveText(dollars(8000));
  // The change due is the core's figure; the read carries the parts, so the
  // test puts them together where the test lives.
  await expect(receipt.getByTestId("receipt-change-due")).toHaveText(
    dollars((8000 as number) - (sale.total_cents as number)),
  );
  const viewLines = sale.lines as Array<Record<string, unknown>>;
  expect(viewLines).toHaveLength(3);
  expect(viewLines[0].unit_price_cents).toBe(400);
  expect(viewLines[0].qty_entered).toBe(3);
  expect(viewLines[0].uom_entered).toBe("sf");
  expect(viewLines[1].unit_price_cents).toBe(1400);
  expect(viewLines[2].unit_price_cents).toBe(3800);
  const tenderLegs = sale.tenders as Array<Record<string, unknown>>;
  expect(tenderLegs).toHaveLength(1);
  expect(tenderLegs[0].method).toBe("CASH");
  expect(tenderLegs[0].amount_cents).toBe(sale.total_cents);
  // The tender row on the screen agrees with the read.
  await expect(receipt.getByTestId("receipt-tender")).toContainText("CASH");
  await expect(receipt.getByTestId("receipt-tender")).toContainText(dollars(sale.total_cents as number));
});

test("the quantity rung is the quantity on the screen, and the requested date is the house zone's day", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("drawer-status")).toContainText("open");

  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$4.00");

  // Type ten and ring without leaving the field, without re-pricing: what
  // leaves the till is the ten on the screen, and the ticket re-prices first,
  // because the engine prices by quantity.
  await page.getByLabel("Quantity of Holland Stone 60 mm").fill("10");
  await page.getByLabel("Cash tendered, in dollars").fill("60.00");
  const ring = page.waitForRequest(
    (request) => request.method() === "POST" && request.url().includes("/api/v1/pos/sales"),
  );
  await page.getByRole("button", { name: "Ring the sale" }).click();
  const ringRequest = await ring;
  const body = (await ringRequest.postDataJSON()) as Record<string, unknown>;
  const ringLines = body.lines as Array<Record<string, unknown>>;
  expect(ringLines).toHaveLength(1);
  expect(ringLines[0].qty_entered).toBe(10);
  // The dealer's day, not the browser's UTC day: the stub refuses a ring
  // whose date is not the house zone's day, and the body says it plainly.
  expect(body.requested_date).toBe(houseDay());

  const receipt = page.getByTestId("receipt");
  await expect(receipt).toBeVisible();
  const bearer = await salesBearer(page);
  const sale = await getSale(page, bearer, (await receipt.getAttribute("data-order-id")) as string);
  const viewLines = sale.lines as Array<Record<string, unknown>>;
  expect(viewLines[0].qty_entered).toBe(10);
  expect(viewLines[0].line_total_cents).toBe(4000);
  await expect(receipt.getByTestId("receipt-total")).toHaveText(dollars(sale.total_cents as number));
  expect(sale.total_cents).toBe(4520);
  await expect(receipt.getByTestId("receipt-change-due")).toHaveText(
    dollars((6000 as number) - (sale.total_cents as number)),
  );
});

test("a card sale shows the simulated notice on the tender and on the receipt, and none when the rail answers false", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("drawer-status")).toContainText("open");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$4.00");

  await page.getByRole("button", { name: "Card", exact: true }).click();
  await expect(page.getByTestId("simulated-notice")).toHaveText("Simulated card: no card was charged");
  await page.getByRole("button", { name: "Ring the sale" }).click();
  const receipt = page.getByTestId("receipt");
  await expect(receipt).toBeVisible();
  await expect(receipt.getByTestId("receipt-simulated-notice")).toHaveText("Simulated card: no card was charged");

  // The notice is value driven: the same screen against a status route that
  // answers simulated false shows no notice on the tender and none on the
  // receipt either (and the stub's own answer is true, so both values are
  // covered by the suite).
  const other = await page.context().newPage();
  await other.route("**/api/v1/payments/config", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        configured: true,
        merchant_id: "demo-merchant",
        processor: "simulator",
        currency: "CAD",
        simulated: false,
      }),
    }),
  );
  await other.goto("/");
  await other.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(other.getByTestId("drawer-status")).toContainText("open");
  await searchAndAdd(other, "wall", "Retaining Wall Stone, Standard");
  await expect(other.getByTestId("line-unit-price").first()).toHaveText("$52.00");
  await other.getByRole("button", { name: "Card", exact: true }).click();
  await expect(other.getByTestId("simulated-notice")).toHaveCount(0);
  await other.getByRole("button", { name: "Ring the sale" }).click();
  const falseReceipt = other.getByTestId("receipt");
  await expect(falseReceipt).toBeVisible();
  await expect(falseReceipt.getByTestId("receipt-simulated-notice")).toHaveCount(0);
  await other.close();
});

test("an account sale for C02 shows its invoice and its balance, and the receipt keeps the account it was rung for", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("drawer-status")).toContainText("open");

  // The account's balance before, through the contract's own search read.
  const bearer = await salesBearer(page);
  const before = await page.request.get("/api/v1/customers/search?q=C02", {
    headers: { Authorization: `Bearer ${bearer}`, "X-Org": "hh-demo" },
  });
  const rows = (await before.json()) as Array<Record<string, unknown>>;
  expect(rows).toHaveLength(1);
  const balanceBefore = rows[0].open_balance_cents as number;

  // Pick the account: its facts show beside its name.
  await page.getByLabel("Find an account by name or account number").fill("C02");
  await page.getByRole("button", { name: "Find", exact: true }).click();
  await page.getByTestId("customer-hits").getByRole("button", { name: "Use" }).click();
  await expect(page.getByTestId("customer-name")).toContainText("Fenwick Builders Ltd");
  await expect(page.getByTestId("account-balance")).toHaveText(dollars(balanceBefore));
  await expect(page.getByTestId("account-standing")).toContainText(dollars(300000));
  await expect(page.getByTestId("hold-flag")).toHaveCount(0);

  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  const hollandQty = page.getByLabel("Quantity of Holland Stone 60 mm");
  await hollandQty.fill("10");
  await page.getByRole("button", { name: "Re-price the ticket" }).click();
  // The account's own rule price, ten percent off retail.
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$3.60");

  await page.getByRole("button", { name: "Account", exact: true }).click();
  await page.getByRole("button", { name: "Ring the sale" }).click();

  const receipt = page.getByTestId("receipt");
  await expect(receipt).toBeVisible();
  const invoiceNo = await receipt.getByTestId("receipt-invoice-no").textContent();
  expect(invoiceNo).toMatch(/^INV-/);
  const sale = await getSale(page, bearer, (await receipt.getAttribute("data-order-id")) as string);
  expect(sale.invoice_no).toBe(invoiceNo);
  expect(sale.tender).toBe("ACCOUNT");
  // The balance on the receipt is the freshly read one: before, plus the
  // sale the core just committed.
  await expect(receipt.getByTestId("receipt-account")).toContainText("Fenwick Builders Ltd (C02)");
  await expect(receipt.getByTestId("receipt-account")).toContainText(invoiceNo as string);
  await expect(receipt.getByTestId("receipt-account")).toContainText(
    dollars(balanceBefore + (sale.total_cents as number)),
  );

  // The receipt speaks for the sale, not for whoever the counter serves now:
  // picking another account and going back to the walk-in moves nothing on
  // it.
  await page.getByLabel("Find an account by name or account number").fill("C01");
  await page.getByRole("button", { name: "Find", exact: true }).click();
  await page.getByTestId("customer-hits").getByRole("button", { name: "Use" }).click();
  await expect(page.getByTestId("customer-name")).toContainText("Maple Ridge Contracting");
  await expect(receipt.getByTestId("receipt-account")).toContainText("Fenwick Builders Ltd (C02)");
  await page.getByRole("button", { name: "Back to the walk-in" }).click();
  await expect(page.getByTestId("customer-name")).toContainText("Walk-in");
  await expect(receipt.getByTestId("receipt-account")).toContainText("Fenwick Builders Ltd (C02)");
});

test("an account sale for C09 is refused in words", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("drawer-status")).toContainText("open");

  await page.getByLabel("Find an account by name or account number").fill("C09");
  await page.getByRole("button", { name: "Find", exact: true }).click();
  await page.getByTestId("customer-hits").getByRole("button", { name: "Use" }).click();
  await expect(page.getByTestId("customer-name")).toContainText("Harbour Drywall Inc");
  // The hold is the first thing the counter shows, before any money moves.
  await expect(page.getByTestId("hold-flag")).toHaveText("On hold");

  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await page.getByRole("button", { name: "Account", exact: true }).click();
  await page.getByRole("button", { name: "Ring the sale" }).click();

  await expect(page.getByTestId("tender-refusal")).toContainText("the credit gate refused it");
  await expect(page.getByTestId("tender-refusal")).toContainText("Cash or card still works");
  // Nothing committed: no receipt exists, and the ticket stands.
  await expect(page.getByTestId("receipt")).toHaveCount(0);
  await expect(page.getByTestId("ticket-line")).toHaveCount(1);
});

test("a C01 Holland paver line rings at its rule price with its margin shown", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("drawer-status")).toContainText("open");

  await page.getByLabel("Find an account by name or account number").fill("C01");
  await page.getByRole("button", { name: "Find", exact: true }).click();
  await page.getByTestId("customer-hits").getByRole("button", { name: "Use" }).click();
  await expect(page.getByTestId("customer-name")).toContainText("Maple Ridge Contracting");

  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  // The account's rule: eighteen percent off the $4.00 retail, and the
  // margin glance's answer rendered beside it.
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$3.28");
  await expect(page.getByTestId("line-margin").first()).toHaveText("15.9%");
  await expect(page.getByTestId("ticket-line").first()).toContainText("ACCOUNT price");
  // The placeholder is real too: the edge restraint carries no cost at this
  // branch, so its margin slot renders the explicit unknown, never a zero.
  await searchAndAdd(page, "edge", "Edge Restraint 8 ft");
  await expect(page.getByTestId("line-unpriced")).toHaveCount(0);
  await expect(page.getByTestId("line-margin-none")).toHaveText("(no margin)");
});

test("a ticket parks and resumes, re-priced, and the counter's ticket is never thrown away silently", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("drawer-status")).toContainText("open");

  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$4.00");

  await page.getByRole("button", { name: "Park this ticket" }).click();
  await expect(page.getByTestId("ticket-line")).toHaveCount(0);
  const parked = page.getByTestId("parked-list").getByTestId("parked-ticket");
  await expect(parked).toHaveCount(1);
  // The ticket parked with no account picked, so the row says Walk-in, and
  // it carries its line count.
  await expect(parked.first()).toContainText("Walk-in");
  await expect(parked.first()).toContainText("1 line");
  // The fence: the stub's world holds a Kingston ticket, and it never shows.
  await expect(page.getByTestId("parked-list")).not.toContainText("PARK-9001");

  // Resuming over lines on the counter refuses in words, and nothing moves.
  await searchAndAdd(page, "polymeric", "Polymeric Locking Sand");
  await expect(page.getByTestId("ticket-line")).toHaveCount(1);
  await parked.first().getByRole("button", { name: "Resume" }).click();
  await expect(page.getByTestId("parked-panel-note").first()).toBeVisible();
  await expect(page.getByTestId("parked-list").getByTestId("parked-ticket")).toHaveCount(1);
  await expect(page.getByTestId("ticket-line")).toHaveCount(1);

  // Park the counter's line too, then resume the first ticket: the row goes
  // with it, and the lines come back priced fresh, in an engine call this
  // test watches leave.
  await page.getByRole("button", { name: "Park this ticket" }).click();
  await expect(page.getByTestId("parked-list").getByTestId("parked-ticket")).toHaveCount(2);
  const reprice = page.waitForRequest((request) => request.url().includes("/api/v1/pos/counter-prices"));
  await page.getByTestId("parked-list").getByTestId("parked-ticket").first().getByRole("button", { name: "Resume" }).click();
  await reprice;
  await expect(page.getByTestId("ticket-line")).toHaveCount(1);
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$4.00");
  await expect(page.getByTestId("line-margin").first()).toContainText("%");
  // The resumed row has left the list: one ticket remains parked.
  await expect(page.getByTestId("parked-list").getByTestId("parked-ticket")).toHaveCount(1);
});

test("an account's parked ticket survives the hand-over: the next login resumes it for the account, at the account's price", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("drawer-status")).toContainText("open");

  // Sales rings up C01's pavers and parks the ticket.
  await page.getByLabel("Find an account by name or account number").fill("C01");
  await page.getByRole("button", { name: "Find", exact: true }).click();
  await page.getByTestId("customer-hits").getByRole("button", { name: "Use" }).click();
  await expect(page.getByTestId("customer-name")).toContainText("Maple Ridge Contracting");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$3.28");
  await page.getByRole("button", { name: "Park this ticket" }).click();
  const c01Row = page
    .getByTestId("parked-list")
    .getByTestId("parked-ticket")
    .filter({ hasText: "Maple Ridge Contracting (C01)" });
  await expect(c01Row).toHaveCount(1);

  // The hand-over the till's rules name: anyone at the counter picks up any
  // parked sale at their branch. Sign out; the Sales manager takes the till.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("drawer-status")).toContainText("open");

  // The row names the account, never a walk-in.
  const managerRows = page.getByTestId("parked-list").getByTestId("parked-ticket");
  const managerRow = managerRows.filter({ hasText: "Maple Ridge Contracting (C01)" });
  await expect(managerRow).toHaveCount(1);

  // Resume: the customer is the account, and the line re-prices at the
  // account's rule price, in an engine call for the account's id.
  const accountPrice = page.waitForRequest((request) => {
    if (!request.url().includes("/api/v1/pos/counter-prices")) return false;
    return (request.postData() ?? "").includes("c-maple-ridge");
  });
  await managerRow.getByRole("button", { name: "Resume" }).click();
  await accountPrice;
  await expect(page.getByTestId("customer-name")).toContainText("Maple Ridge Contracting");
  // The account number shows beside the name: the ticket is C01's again.
  await expect(page.getByText("(C01, CONTRACTOR)")).toBeVisible();
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$3.28");
  await expect(page.getByTestId("line-margin").first()).toHaveText("15.9%");
  await expect(page.getByTestId("ticket-panel-priced-for")).toContainText("CONTRACTOR");
  // The resumed row has left the list.
  await expect(managerRows.filter({ hasText: "Maple Ridge Contracting (C01)" })).toHaveCount(0);
});
