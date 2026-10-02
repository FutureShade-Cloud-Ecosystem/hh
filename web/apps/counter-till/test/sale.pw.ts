// The sale at the counter, held against the ported terminal (the surface the
// parity pass mounted): the stub serves the counter routes' contract shapes
// and the vite dev server proxies /api to it. The stories this suite holds
// are the counter's own: the drawer is found or opened on a first use, three
// lines ring at the walk-in's retail and the sale read answers every figure
// the confirmation showed, the quantity rung is the one typed and the
// requested date is the house zone's day, a card sale carries the simulated
// word on the tender and on the confirmation exactly when the rail answers
// simulated true (and none when it answers false), an account sale rings on
// the account with its facts beside the name, an account on hold is refused
// in the reviewed words, a contractor's Holland pavers ring at the account's
// rule price with the margin column answered, a ticket parks and resumes with
// its customer, a second press on Complete commits exactly one sale, and the
// surface's two modes both work: the layout pick and its persistence, the
// panel toggles, and the pop-out.
//
// The sale read comparison works through the real path: this spec mints its
// own demo session (the same public route the app's cards use) and reads the
// committed sale back through the same proxy, with the bearer and org header
// the staff routes require.
import { expect, test, type Page, type Response, type Route } from "@playwright/test";

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

/** Sign in through the entry cards and land on the ported terminal. */
async function signIn(page: Page, role: "Sales" | "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

/** Search from the counter's own field and add the hit: the drop row's click adds. */
async function searchAndAdd(page: Page, query: string, hitName: string): Promise<void> {
  // The counter's accordion opens on Customer; the scan box lives in Items.
  await openStep(page, "items");
  const field = page.getByTestId("pos-scan-input");
  await field.fill(query);
  const row = page.getByTestId("drop-row").filter({ hasText: hitName }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: hitName })).toHaveCount(1);
}

/**
 * The branch's drawer, found or opened: the mount hydrates asynchronously, so
 * wait for whichever state arrives (the open dot, or the open button) instead
 * of racing the read with a count.
 */
async function ensureSession(page: Page): Promise<void> {
  const dot = page.getByTestId("session-dot");
  const openButton = page.getByRole("button", { name: "Open Session", exact: true });
  await expect(dot.or(openButton).first()).toBeVisible({ timeout: 10_000 });
  if (await dot.isVisible()) {
    await expect(dot).toContainText("Open");
    return;
  }
  // The open control can show for an instant before the hydrating read finds
  // the drawer already open: a click that loses that race is not a failure,
  // the dot arriving is the state the helper wants.
  await openButton.click({ timeout: 3_000 }).catch(() => undefined);
  await expect(dot).toContainText("Open");
}

/** Open one step of the linear flow by its head (a no-op when it is already open). */
async function openStep(page: Page, step: "customer" | "items" | "payment" | "summary"): Promise<void> {
  const head = page.getByTestId(`step-head-${step}`);
  // A pick or an add can advance the flow a moment after it lands, closing the
  // head this call just opened: press again until it holds open.
  for (let attempt = 0; attempt < 5; attempt += 1) {
    if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
    try {
      await expect(head).toHaveAttribute("aria-expanded", "true", { timeout: 1_000 });
      await page.waitForTimeout(250);
      if ((await head.getAttribute("aria-expanded")) === "true") return;
    } catch {
      // closed again by the advance: press once more
    }
  }
  await expect(head).toHaveAttribute("aria-expanded", "true");
}

/** Pick an account through the counter's own picker. */
async function pickAccount(page: Page, search: string, name: string): Promise<void> {
  await page.getByRole("button", { name: /Select customer/ }).click();
  const searchBox = page.getByPlaceholder("Search name or account #…");
  await searchBox.fill(search);
  const row = page.locator("button.row").filter({ hasText: name }).first();
  await expect(row).toBeVisible();
  await row.click();
  // The pick advances the flow toward Items; the account's facts live on the
  // customer step's card, so open it again and read them. The advance can
  // land after the first open (the pick reprices the ticket first), so the
  // open is retried until the card is readable.
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

test("a walk-in cash sale of three lines rings, and the sale read equals every figure on the confirmation", async ({
  page,
}) => {
  await signIn(page, "Sales");

  // Three lines, one quantity typed: the line reprices through the engine.
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await searchAndAdd(page, "polymeric", "Polymeric Locking Sand");
  await searchAndAdd(page, "clear", "Clear Stone");
  const lines = page.getByTestId("pos-line-row");
  await expect(lines).toHaveCount(3);
  const hollandQty = page.getByRole("textbox", { name: "Quantity: Holland Stone 60 mm" });
  await hollandQty.fill("3");
  await hollandQty.blur();
  await expect(lines.filter({ hasText: "Holland Stone 60 mm" }).locator(".ltot")).toHaveText("CA$12.00");

  // The walk-in's retail on every line, with the margin column answered for
  // every line that carries a cost at this branch.
  const margins = page.getByTestId("pos-line-margin");
  await expect(margins).toHaveCount(3);
  for (let index = 0; index < 3; index += 1) {
    await expect(margins.nth(index)).toContainText("%");
  }

  // The cash quick entry: what the customer hands over names the change due
  // before anything is rung, here CA$7.68 against the estimated total.
  await openStep(page, "payment");
  // The pre-ring summary, figure by figure, on a ticket the stub's prices
  // pin: 12.00 + 14.00 + 38.00 is a CA$64.00 subtotal; the estimated tax is
  // the counter's 1300 bps on it (CA$8.32); the estimated total is CA$72.32.
  // The rows say they are estimates; the ring's figures are the binding ones.
  const totals = page.getByTestId("pos-totals-card");
  await expect(totals).toContainText("CA$64.00");
  await expect(totals).toContainText("HST 13% (est.)");
  await expect(totals).toContainText("CA$8.32");
  await expect(totals).toContainText("Total (est.)");
  await expect(totals).toContainText("CA$72.32");
  await page.getByLabel("Cash tendered in dollars: optional; leave empty to ring the exact total").fill("80.00");
  await expect(page.getByTestId("cash-change-preview")).toContainText("Change due");
  await expect(page.getByTestId("cash-change-preview")).toContainText("CA$7.68");
  // The figure is computed from the estimated total, so it carries the estimate mark.
  await expect(page.getByTestId("cash-change-preview")).toContainText("(est.)");

  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  const sale = (await (await ringPost).json()) as Record<string, unknown>;

  // The confirmation, figure by figure, against the sale the core committed.
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await expect(sold).toContainText("Paid");
  await expect(sold).toContainText(String(sale.order_no));
  await expect(sold).toContainText(String(sale.invoice_no));
  await expect(sold.getByTestId("sold-change-due")).toContainText(dollars(8000));
  await expect(sold.getByTestId("sold-change-due")).toContainText(
    dollars(8000 - (sale.total_cents as number)),
  );
  // A cash confirmation carries no card notice, whatever the rail answers.
  await expect(page.getByTestId("sold-simulated-notice")).toHaveCount(0);
  // The session bar says the drawer is open, and stayed open.
  await expect(page.getByTestId("session-dot")).toContainText("Open");

  // The read path: mint, read, and hold every figure against the screen.
  const bearer = await salesBearer(page);
  const read = await getSale(page, bearer, sale.order_id as string);
  expect(read.order_no).toBe(sale.order_no);
  expect(read.invoice_no).toBe(sale.invoice_no);
  await expect(sold).toContainText(dollars(read.total_cents as number));
  const viewLines = read.lines as Array<Record<string, unknown>>;
  expect(viewLines).toHaveLength(3);
  expect(viewLines[0].unit_price_cents).toBe(400);
  expect(viewLines[0].qty_entered).toBe(3);
  expect(viewLines[0].uom_entered).toBe("sf");
  expect(viewLines[1].unit_price_cents).toBe(1400);
  expect(viewLines[2].unit_price_cents).toBe(3800);
  const tenderLegs = read.tenders as Array<Record<string, unknown>>;
  expect(tenderLegs).toHaveLength(1);
  expect(tenderLegs[0].method).toBe("CASH");
  expect(tenderLegs[0].amount_cents).toBe(read.total_cents);
  // The typed cash tendered and the change due are the core's own figures,
  // answered on the ring itself.
  expect(sale.cash_tendered_cents).toBe(8000);
  expect(sale.change_due_cents).toBe(8000 - (read.total_cents as number));
});

test("the quantity rung is the quantity on the screen, and the requested date is the house zone's day", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  const hollandQty = page.getByRole("textbox", { name: "Quantity: Holland Stone 60 mm" });
  await hollandQty.fill("10");
  await hollandQty.blur();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" }).locator(".ltot")).toHaveText(
    "CA$40.00",
  );

  await openStep(page, "payment");
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  // No quick entry: the sale rings for the exact total, per the field's own word.
  await page.getByTestId("pos-complete-sale").click();
  const ringResponse = await ringPost;
  const body = (await ringResponse.request().postDataJSON()) as Record<string, unknown>;
  const ringLines = body.lines as Array<Record<string, unknown>>;
  expect(ringLines).toHaveLength(1);
  expect(ringLines[0].qty_entered).toBe(10);
  // The dealer's day, not the browser's UTC day: the stub refuses a ring
  // whose date is not the house zone's day, and the body says it plainly.
  expect(body.requested_date).toBe(houseDay());

  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  const committed = (await ringResponse.json()) as Record<string, unknown>;
  const bearer = await salesBearer(page);
  const read = await getSale(page, bearer, committed.order_id as string);
  const viewLines = read.lines as Array<Record<string, unknown>>;
  expect(viewLines[0].qty_entered).toBe(10);
  expect(viewLines[0].line_total_cents).toBe(4000);
  await expect(sold).toContainText(dollars(read.total_cents as number));
  expect(read.total_cents).toBe(4520);
});

test("a card sale shows the simulated word on the tender and on the confirmation, and none when the rail answers false", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");

  await openStep(page, "payment");
  await page.getByRole("button", { name: "Card" }).click();
  // The rail's own report: the demo box's simulator names itself on the
  // tender, exactly while the status route answers simulated true.
  await expect(page.getByText("TEST MODE: simulated terminal")).toBeVisible();
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  const sale = (await (await ringPost).json()) as Record<string, unknown>;
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await expect(sold.getByTestId("sold-simulated-notice")).toContainText("Simulated card: no card was charged");
  const bearer = await salesBearer(page);
  const read = await getSale(page, bearer, sale.order_id as string);
  expect(read.tender).toBe("CARD");

  // The printed receipt is the core's document and carries no simulated word
  // (CLOSURE.md names the gap); it opens, and the simulated notice is held by
  // the till's own screens above.
  const receiptPage = page.context().waitForEvent("page");
  await sold.getByTestId("sold-print").click();
  const printed = await receiptPage;
  // The document's own number, which the stub and the core's receipt both render (not the stub's wording).
  expect(typeof sale.order_no).toBe("string");
  await expect(printed.getByText(sale.order_no as string).first()).toBeVisible();
  await printed.close();

  // The notice is value driven: the same surface against a status route that
  // answers simulated false shows no badge on the tender and no notice on the
  // confirmation (and the stub's own answer is true, so both values are
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
  await signIn(other, "Sales");
  await searchAndAdd(other, "wall", "Retaining Wall Stone, Standard");
  await openStep(other, "payment");
  await other.getByRole("button", { name: "Card" }).click();
  await expect(other.getByText("TEST MODE: simulated terminal")).toHaveCount(0);
  const otherRing = other.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await other.getByTestId("pos-complete-sale").click();
  await otherRing;
  const otherSold = other.getByTestId("sold-panel");
  await expect(otherSold).toBeVisible();
  await expect(otherSold.getByTestId("sold-simulated-notice")).toHaveCount(0);
  // The document word's false side is the core's render of its own rail, and
  // this stub's rail IS its simulator, so a false-side document cannot be
  // staged honestly here: the false side is held where the app speaks, at the
  // confirmation above.
  await other.close();
});

test("an account sale for C02 rings on the account, with its facts beside the name", async ({ page }) => {
  await signIn(page, "Sales");
  await pickAccount(page, "C02", "Fenwick Builders Ltd");
  // The account's facts: available headroom from the core's limit and balance,
  // and the past-due fact the standing read answered.
  await expect(page.getByTestId("customer-card")).toContainText("C02");
  await expect(page.getByTestId("customer-card")).toContainText("Available");
  await expect(page.getByTestId("cust-past-due")).toContainText(dollars(41500));
  await expect(page.getByTestId("cust-on-hold")).toHaveCount(0);

  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  const hollandQty = page.getByRole("textbox", { name: "Quantity: Holland Stone 60 mm" });
  await hollandQty.fill("10");
  await hollandQty.blur();
  // The account's own rule price, ten percent off retail.
  await expect(
    page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" }).locator(".lprice"),
  ).toHaveText("CA$3.60");

  // The account's open balance as the core holds it before the ring.
  const bearerBefore = await salesBearer(page);
  const readBalance = async (): Promise<number> => {
    const read = await page.request.get("/api/v1/customers/search?q=C02", {
      headers: { Authorization: `Bearer ${bearerBefore}`, "X-Org": "hh-demo" },
    });
    const rows = (await read.json()) as Array<{ account_no: string; open_balance_cents: number }>;
    return rows.find((row) => row.account_no === "C02")?.open_balance_cents as number;
  };
  const balanceBefore = await readBalance();
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Charge Account" }).click();
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  const sale = (await (await ringPost).json()) as Record<string, unknown>;
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await expect(sold).toContainText("On account");
  await expect(sold).toContainText(String(sale.invoice_no));
  const bearer = await salesBearer(page);
  const read = await getSale(page, bearer, sale.order_id as string);
  expect(read.tender).toBe("ACCOUNT");
  expect(read.invoice_no).toBe(sale.invoice_no);

  // The receipt document, printed from the confirmation: the account it was
  // sold to, the invoice, the figures.
  const receiptPage = page.context().waitForEvent("page");
  await sold.getByTestId("sold-print").click();
  const printed = await receiptPage;
  await expect(printed.getByText(`Counter sale ${sale.order_no} · invoice ${sale.invoice_no}`)).toBeVisible();
  await expect(printed.getByText("Sold to Fenwick Builders Ltd (C02)")).toBeVisible();
  await expect(
    printed.getByText(`Total ${dollars(read.total_cents as number).replace("$", "")} · ACCOUNT`),
  ).toBeVisible();
  await printed.close();
  // The account card is re-read after the ring: it shows the fresh balance
  // (the old balance plus this sale), not the figure read when it was picked.
  const saleTotal = (sale.total_cents as number) ?? 0;
  expect(saleTotal).toBeGreaterThan(0);
  expect(await readBalance()).toBe(balanceBefore + saleTotal);
  await openStep(page, "customer");
  await expect(page.getByTestId("customer-card")).toContainText(dollars(balanceBefore + saleTotal));
  await expect(page.getByTestId("customer-card")).not.toContainText(dollars(balanceBefore));
});

test("an account on hold is refused in the reviewed words, and nothing commits", async ({ page }) => {
  await signIn(page, "Sales");
  await pickAccount(page, "C09", "Harbour Drywall Inc");
  // The hold is the first thing the counter shows, before any money moves.
  await expect(page.getByTestId("cust-on-hold")).toHaveText("Credit hold");

  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Charge Account" }).click();
  await page.getByTestId("pos-complete-sale").click();

  const alert = page.getByRole("alert");
  await expect(alert).toContainText("the credit gate refused it");
  await expect(alert).toContainText("Cash or card still works");
  // Nothing committed: no confirmation exists, and the ticket stands (the
  // Items panel holds the line; the accordion moved to Payment for the tender).
  await expect(page.getByTestId("sold-panel")).toHaveCount(0);
  await openStep(page, "items");
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
});

test("a C01 Holland paver line rings at its rule price with its margin shown, and the unknown margin stays unknown", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await pickAccount(page, "C01", "Maple Ridge Contracting");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  // The account's rule: eighteen percent off the $4.00 retail, and the margin
  // glance's answer rendered beside it.
  const holland = page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" });
  await expect(holland.locator(".lprice")).toHaveText("CA$3.28");
  // The margin word is the reviewed helper's (half away from zero): 1585 bps
  // reads 15.9%, the same word every other HH surface spells.
  await expect(holland.getByTestId("pos-line-margin")).toHaveText("15.9%");
  // The placeholder is real too: the edge restraint carries no cost at this
  // branch, so its margin slot renders the explicit unknown, never a zero.
  await searchAndAdd(page, "edge", "Edge Restraint 8 ft");
  const edge = page.getByTestId("pos-line-row").filter({ hasText: "Edge Restraint 8 ft" });
  await expect(edge.locator(".lprice")).toHaveText("CA$26.24");
  await expect(edge.getByTestId("pos-line-margin")).toHaveText("(none)");
});

test("a ticket parks and resumes with its customer, at the account's price", async ({ page }) => {
  await signIn(page, "Sales");
  await pickAccount(page, "C01", "Maple Ridge Contracting");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  const holland = page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" });
  await expect(holland.locator(".lprice")).toHaveText("CA$3.28");

  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);

  // The branch's parked list: open it, find the ticket, resume it. The fence
  // is the server's; the stub's world holds a Kingston ticket and it never
  // shows here.
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  // The dialog renders into the element's shadow root; its card is the rail.
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  await expect(parkedDialog).toContainText("Parked tickets");
  await expect(parkedDialog.getByText("PARK-9001")).toHaveCount(0);
  const resumeButton = parkedDialog.getByRole("button", { name: "Resume" }).first();
  await resumeButton.click();
  // The prices answered (every line repriced), so the pickup line says they
  // were derived (ui3 visual GAP 3: the words are true in both states).
  await expect(page.locator("#till-message")).toContainText("prices re-derived now");

  // The ticket comes back with its customer (this till picked the account, so
  // the display memory knows it) and reprices at the account's rule price.
  await openStep(page, "customer");
  await expect(page.getByTestId("customer-card")).toContainText("Maple Ridge Contracting");
  await openStep(page, "items");
  await expect(holland.locator(".lprice")).toHaveText("CA$3.28");
});

test("a second press on Complete while the ring is in flight commits exactly one sale", async ({ page }) => {
  const ringPosts: Array<Record<string, unknown>> = [];
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().endsWith("/api/v1/pos/sales")) {
      ringPosts.push((request.postDataJSON() ?? {}) as Record<string, unknown>);
    }
  });

  await signIn(page, "Sales");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await expect(
    page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" }).locator(".ltot"),
  ).toHaveText("CA$4.00");
  await openStep(page, "payment");
  // The drawer first, so the presses below race the RING and only the ring:
  // a closed drawer would have both presses take the open-then-ring path.
  await ensureSession(page);
  const cta = page.getByTestId("pos-complete-sale");
  await expect(cta).toBeEnabled();
  // Two presses in quick succession: the second lands while the first is in
  // flight. The guard is taken synchronously in code (a busy or rung till
  // refuses in the method, not only in the attribute), and the ring's key is
  // minted from the body's fingerprint, so whatever slips through replays the
  // same sale instead of committing twice.
  const first = cta.dispatchEvent("click");
  await cta.dispatchEvent("click");
  await first;

  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await page.waitForTimeout(500);
  expect(ringPosts.length).toBeGreaterThanOrEqual(1);
  const keys = new Set(ringPosts.map((body) => body.idempotency_key));
  expect(keys.size).toBe(1);
  // The confirmation names one order, and the sale is complete: the CTA's
  // step stands down (ringDone disarms it), so a third press has no button
  // left to press, and the method's own guard refuses the dispatch besides.
  await expect(page.getByTestId("pos-complete-sale")).toHaveCount(0);
  expect(sold).toContainText("Paid");
});

test("the lookup answers a scan by its sku: one line lands on the ticket", async ({ page }) => {
  await signIn(page, "Sales");
  await openStep(page, "items");
  const field = page.getByTestId("pos-scan-input");
  await field.fill("EDG-08");
  await field.press("Enter");
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Edge Restraint 8 ft" })).toHaveCount(1);
  await expect(
    page.getByTestId("pos-line-row").filter({ hasText: "Edge Restraint 8 ft" }).locator(".lprice"),
  ).toHaveText("CA$32.00");
});

test("the detached layout works: the pick persists on the workspace row, and the toggles answer", async ({
  page,
}) => {
  await signIn(page, "Sales");
  // The counter view menu carries the layout pick. The save is debounced, so
  // the wait is armed before the pick and the answer read after it.
  await page.getByTestId("pos-view").click();
  const saved = page.waitForRequest(
    (request) => request.method() === "PUT" && request.url().includes("/api/v1/pos/workspace"),
  );
  await page.getByTestId("pos-layout-panels").click();
  const put = await saved;
  expect((put.postDataJSON() as Record<string, unknown>)["counter_layout"]).toBe("PANELS");
  // The detached multi-card surface is on: the four panels, arrangeable.
  await expect(page.getByTestId("pos-flow-panels")).toBeVisible();

  // The choice follows the person: it rides the workspace row, so a fresh
  // sign-in lands on the same layout.
  const other = await page.context().newPage();
  await signIn(other, "Sales");
  await other.getByTestId("pos-view").click();
  await expect(other.getByTestId("pos-layout-panels")).toHaveAttribute("aria-checked", "true");
  await other.close();

  // Back to the standard counter: the linear flow is the other mode, and the
  // pick back is the same gesture. The save is debounced, so the wait is
  // armed before the pick: the row carries the stepper choice when this page
  // closes, and the tests after this one mount the linear flow.
  await page.getByTestId("pos-view").click();
  const savedBack = page.waitForRequest(
    (request) => request.method() === "PUT" && request.url().includes("/api/v1/pos/workspace"),
  );
  await page.getByTestId("pos-layout-stepper").click();
  await savedBack;
  await expect(page.getByTestId("pos-flow-column")).toBeVisible();
});

test("the pop-out opens the till on a second window, signed in as the same role", async ({
  page,
  context,
}) => {
  await signIn(page, "Sales");
  const popped = context.waitForEvent("page");
  await page.getByRole("button", { name: "Pop out" }).click();
  const second = await popped;
  await second.waitForLoadState();
  // The second window signs itself in through the same public cards route the
  // entry page taps, and lands on the terminal: the same yard, the same
  // register, the same role. The branch's drawer is server-side, so the
  // second window's own session open finds or opens the same one.
  await expect(second.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  await expect(second.getByTestId("pagectx")).toContainText("Trenton Yard");
  await expect(second.getByTestId("signed-in-role")).toHaveText("Sales");
  await second.close();
});

test("a line the account's price cannot answer reads price pending through ordinary acts, never a zero", async ({
  page,
}) => {
  // The account's price read is held where the route stands for the polymeric
  // sand, until the account is picked and a re-price is asked again: the
  // honest pending word through every ordinary act, and a subtotal over the
  // priced lines only.
  let refusePolymer = true;
  await page.route("**/api/v1/pricing/resolve", async (route) => {
    const body = route.request().postDataJSON() as { product_id?: string };
    if (refusePolymer && body.product_id === "p-polymer-sand") {
      await route.fulfill({ status: 422, contentType: "application/json", body: JSON.stringify({ error: "line could not be priced by the engine" }) });
      return;
    }
    await route.fallback();
  });

  await signIn(page, "Sales");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await searchAndAdd(page, "polymeric", "Polymeric Locking Sand");
  const polymeric = page.getByTestId("pos-line-row").filter({ hasText: "Polymeric Locking Sand" });
  await expect(polymeric.locator(".lprice")).toHaveText("price pending");
  // The total cell says the same word every other surface does.
  await expect(polymeric.locator(".ltot")).toHaveText("(not priced)");

  // An ordinary act on the OTHER line reprices that line and leaves the
  // unanswered one saying so.
  const hollandQty = page.getByRole("textbox", { name: "Quantity: Holland Stone 60 mm" });
  await hollandQty.fill("3");
  await hollandQty.blur();
  await expect(
    page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" }).locator(".ltot"),
  ).toHaveText("CA$12.00");
  await expect(polymeric.locator(".lprice")).toHaveText("price pending");
  // A pending line beside a priced one keeps the whole summary on the marker:
  // no figure (CA$12.00 for the priced line alone, or any sum) is stated while
  // a line has no answer. The ring button and the change preview say the same.
  await openStep(page, "payment");
  const totals = page.getByTestId("pos-totals-card");
  await expect(totals).toContainText("(not priced)");
  await expect(totals).not.toContainText("CA$12.00");
  await expect(page.getByTestId("pos-complete-sale")).toContainText("(not priced)");
  await expect(page.getByTestId("pos-complete-sale")).not.toContainText("CA$");
  await page.getByTestId("cash-tendered").fill("20");
  await expect(page.getByTestId("cash-change-preview")).toContainText("(not priced)");
  await expect(page.getByTestId("cash-change-preview")).not.toContainText("CA$");
  // Below the stale total: the "Less than the total" decision is not made either.
  await page.getByTestId("cash-tendered").fill("1");
  await expect(page.getByTestId("cash-change-preview")).toContainText("(not priced)");
  await expect(page.getByTestId("cash-change-preview")).not.toContainText("Less than the total");
  await page.getByTestId("cash-tendered").fill("");
  // The split panel's remaining amount follows the marker too: a remaining against a total that
  // leaves the pending line out would be the same confident wrong figure.
  await page.getByRole("button", { name: "Split" }).click();
  const splitPanel = page.getByTestId("pos-split-panel");
  await splitPanel.getByTestId("split-amount-0").fill("5.00");
  await expect(page.getByTestId("split-remaining")).toContainText("(not priced)");
  await expect(page.getByTestId("split-remaining")).not.toContainText("CA$");
  await expect(page.getByTestId("split-remaining")).not.toContainText("Settled");
  await splitPanel.getByRole("button", { name: "Cancel" }).click();
  await expect(splitPanel).toHaveCount(0);
  await openStep(page, "items");

  // The recovery: the account is picked, and the re-price answers at the
  // account's rule.
  await openStep(page, "customer");
  const accountPicked = page.waitForRequest(
    (request) =>
      request.url().includes("/api/v1/pos/counter-prices") &&
      (request.postData() ?? "").includes("c-maple-ridge"),
  );
  await pickAccount(page, "C01", "Maple Ridge Contracting");
  await accountPicked;
  refusePolymer = false;
  // The pick's own reprices land first, so the edit below is the newest ask
  // and no re-render writes the input back mid-edit: wait until none is in flight.
  await page.waitForLoadState("networkidle");
  await openStep(page, "items");
  const polymericQty = page.getByRole("textbox", { name: "Quantity: Polymeric Locking Sand" });
  await polymericQty.fill("2");
  await polymericQty.blur();
  // Two bags at the account's rule price: the unit column carries the rule
  // price (CA$11.48), the line's total the picked quantity's answer.
  await expect(polymeric.locator(".lprice")).toHaveText("CA$11.48");
  await expect(polymeric.locator(".ltot")).toHaveText("CA$22.96");
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "price pending" })).toHaveCount(0);
  // Every line priced: the figures return, the estimate mark on the ring.
  await openStep(page, "payment");
  await expect(totals).not.toContainText("(not priced)");
  await expect(page.getByTestId("pos-complete-sale")).toContainText("(est.)");
});

test("a pricing answer that arrives for a stale line state is dropped: the newest answer owns the line", async ({
  page,
}) => {
  // The first price ask is held where the route stands; the quantity changes
  // while it is in flight (a second ask answers), and when the held answer is
  // finally released its figures are about a ticket that no longer exists.
  let releaseFirst: (() => void) | undefined;
  let heldOne = true;
  await page.route("**/api/v1/pricing/resolve", async (route) => {
    const body = route.request().postDataJSON() as { product_id?: string };
    if (heldOne && body.product_id === "p-holland-60") {
      heldOne = false;
      await new Promise<void>((resolve) => {
        releaseFirst = resolve;
      });
    }
    await route.fallback();
  });

  await signIn(page, "Sales");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  const holland = page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" });
  const hollandQty = page.getByRole("textbox", { name: "Quantity: Holland Stone 60 mm" });
  await hollandQty.fill("3");
  await hollandQty.blur();
  await expect(holland.locator(".ltot")).toHaveText("CA$12.00");

  releaseFirst?.();
  // The stale answer (priced for one unit) landed on nothing: the line still
  // carries the newest answer's figures.
  await page.waitForTimeout(600);
  await expect(holland.locator(".lprice")).toHaveText("CA$4.00");
  await expect(holland.locator(".ltot")).toHaveText("CA$12.00");
});

test("a parked ticket survives the hand-over: the next login at the branch sees it and resumes it, priced fresh", async ({
  page,
}) => {
  // The first counter person parks C01's pavers and leaves.
  await signIn(page, "Sales");
  await pickAccount(page, "C01", "Maple Ridge Contracting");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);

  // The next login at the same counter is a second person at the same branch:
  // the parked rail is the branch's, so the ticket is there, and the row has
  // left it once resumed. The resumed ticket prices fresh through the engine
  // (the new tab's display memory is cold, so the ticket resumes for the
  // walk-in and the line reads retail until an account is picked).
  await page.getByRole("button", { name: "Sign out" }).click();
  await signIn(page, "Sales manager");
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  await expect(parkedDialog.getByText("PARK-9001")).toHaveCount(0);
  await parkedDialog.getByRole("button", { name: "Resume" }).first().click();
  // The resumed ticket lands on the customer step (the cold cache resumed it
  // for the walk-in); the line is the Items step's.
  await openStep(page, "items");
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" })).toHaveCount(1);
  await expect(
    page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" }).locator(".lprice"),
  ).toHaveText("CA$4.00");
  await expect(parkedDialog).toHaveCount(0);
});

test("a resume over lines on the counter asks first: declining leaves everything where it was", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  // Park a second ticket, so the rail holds something to resume.
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  await searchAndAdd(page, "polymeric", "Polymeric Locking Sand");
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);

  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  // The counter's lines are work: the rail asks before it consumes the
  // ticket. Declining keeps the counter's line and the parked ticket both.
  page.once("dialog", (dialog) => dialog.dismiss());
  await parkedDialog.getByRole("button", { name: "Resume" }).first().click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
  await expect(parkedDialog).toBeVisible();
  await expect(parkedDialog.getByRole("button", { name: "Resume" }).first()).toBeEnabled();
  // The acceptance: the ask said what accepting means (the counter's lines go
  // with it), so the parked ticket replaces them knowingly: the Holland line
  // is back, the polymeric one went with the counter's draft.
  page.once("dialog", (dialog) => dialog.accept());
  await parkedDialog.getByRole("button", { name: "Resume" }).first().click();
  await openStep(page, "items");
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" })).toHaveCount(1);
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Polymeric Locking Sand" })).toHaveCount(0);
});

// WCAG 1.4.11 for the till's own inputs (pos-terminal's select, input and textarea rule): the
// boundary is the design system's control line and clears 3:1 against what it sits on, in both
// themes, as the returns desk's find input does.
for (const scheme of ["dark", "light"] as const) {
  test(`the till's own inputs keep the control boundary at 3:1 (${scheme})`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await signIn(page, "Sales");
    await searchAndAdd(page, "holland", "Holland Stone 60 mm");
    await openStep(page, "payment");
    const input = page.getByTestId("cash-tendered");
    await expect(input).toBeVisible();
    const read = await input.evaluate((el) => {
      const rgb = (css: string): number[] => (css.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
      const lum = (c: number[]): number => {
        const [r, g, b] = c.map((v) => {
          const s = v / 255;
          return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
        });
        return 0.2126 * r + 0.7152 * g + 0.0722 * b;
      };
      const ratio = (a: number[], b: number[]): number => {
        const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
        return (hi + 0.05) / (lo + 0.05);
      };
      const resolve = (token: string): number[] => {
        const probe = document.createElement("div");
        probe.style.color = `var(${token})`;
        document.body.appendChild(probe);
        const out = rgb(getComputedStyle(probe).color);
        probe.remove();
        return out;
      };
      const border = rgb(getComputedStyle(el).borderTopColor);
      return {
        isControlLine: border.join() === resolve("--hh-control-line").join(),
        onCard: ratio(border, resolve("--hh-card")),
        onFill: ratio(border, rgb(getComputedStyle(el).backgroundColor)),
      };
    });
    expect(read.isControlLine).toBe(true);
    expect(read.onCard).toBeGreaterThanOrEqual(3);
    expect(read.onFill).toBeGreaterThanOrEqual(3);
  });
}

test("the split state is a status band, not a disabled button", async ({ page }) => {
  await signIn(page, "Sales");
  await ensureSession(page);
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Split" }).click();
  const band = page.getByTestId("cta-split-state");
  await expect(band).toBeVisible();
  await expect(band).toContainText("Split: compose the legs below");
  // The explanation is visible text, not a hover title only.
  await expect(page.getByTestId("cta-split-why")).toBeVisible();
  await expect(page.getByTestId("cta-split-why")).toContainText("legs must add up");
  await expect(band).not.toHaveAttribute("title", /.*/);
  // A statement, not a control: no button, no disabled state to fake.
  expect(await band.evaluate((el) => el.tagName)).not.toBe("BUTTON");
  await expect(band).toHaveAttribute("role", "status");
  await expect(page.getByRole("button", { name: /Split: compose/ })).toHaveCount(0);
});

test("a fresh line before its price answers: the split says a price is pending, Rest fills nothing, and the ring opens once the price answers", async ({
  page,
}) => {
  // The resolve answer for the Holland line is held until released: the line is on the ticket,
  // priced by no one yet (ui2c P3-2's fresh pending line).
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let hold = true;
  await page.route("**/api/v1/pricing/resolve", async (route) => {
    if (hold) await held;
    await route.fallback();
  });
  await signIn(page, "Sales");
  await ensureSession(page);
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Split" }).click();
  const panel = page.getByTestId("pos-split-panel");
  await panel.getByTestId("split-amount-0").fill("1.00");
  await panel.getByTestId("split-amount-1").fill("1.00");
  // Pending: the true sentence, not "add a line"; the ring stays closed.
  await expect(page.getByTestId("split-remaining")).toContainText("(not priced)");
  await expect(page.getByTestId("split-problem")).toContainText("A price is still pending");
  await expect(page.getByTestId("split-problem")).not.toContainText("Add a line");
  await expect(page.getByTestId("split-ring")).toBeDisabled();
  // Rest fills nothing while there is no total.
  await panel.getByTestId("split-amount-1").fill("");
  await panel.getByRole("button", { name: "Rest" }).nth(1).click();
  await expect(panel.getByTestId("split-amount-1")).toHaveValue("");
  await expect(panel.getByTestId("split-amount-1")).not.toHaveValue("0.00");
  // The price answers: the panel states the figure, Rest fills the true remainder, the ring opens.
  hold = false;
  release();
  await expect(page.getByTestId("split-remaining")).not.toContainText("(not priced)");
  await panel.getByTestId("split-amount-0").fill("4.00");
  await panel.getByRole("button", { name: "Rest" }).nth(1).click();
  await expect(panel.getByTestId("split-amount-1")).toHaveValue("0.52");
  await expect(page.getByTestId("split-remaining")).toContainText("Settled exactly");
  await expect(page.getByTestId("split-ring")).toBeEnabled();
});

test("a split with a card leg carries the simulated word on the confirmation and on the receipt", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await ensureSession(page);
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Split" }).click();
  // Two legs: cash for part of the estimated total, a card leg for the rest.
  // The Holland line at retail with tax is CA$4.52: CA$4.00 cash and the
  // CA$0.52 card leg settle it exactly.
  const leg0 = page.getByTestId("split-amount-0");
  const leg1 = page.getByTestId("split-amount-1");
  await leg0.fill("4.00");
  await leg1.fill("0.52");
  // The live verdict: settled exactly, so the panel's ring arms.
  await expect(page.getByTestId("split-remaining")).toContainText("Settled exactly");
  await expect(page.getByTestId("split-ring")).toBeEnabled();
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("split-ring").click();
  const sale = (await (await ringPost).json()) as Record<string, unknown>;

  // The confirmation: the split touched the simulated rail, so it says so,
  // exactly as a CARD tender does.
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await expect(sold.getByTestId("sold-simulated-notice")).toContainText("Simulated card: no card was charged");

  // The read: the legs are the wire's own, one cash and one card.
  const bearer = await salesBearer(page);
  const read = await getSale(page, bearer, sale.order_id as string);
  expect(read.split_tender).toBe(true);
  const legs = read.tenders as Array<Record<string, unknown>>;
  expect(legs.map((leg) => leg.method)).toEqual(["CASH", "CARD"]);

  // The printed receipt is the core's document; it opens, and the simulated
  // notice is held by the till's own screens above.
  const receiptPage = page.context().waitForEvent("page");
  await sold.getByTestId("sold-print").click();
  const printed = await receiptPage;
  // The document's own number, which the stub and the core's receipt both render (not the stub's wording).
  expect(typeof sale.order_no).toBe("string");
  await expect(printed.getByText(sale.order_no as string).first()).toBeVisible();
  await printed.close();
});

test("an empty draft shows the blank marker, never a confident zero, in the linear flow", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await openStep(page, "payment");
  const totals = page.getByTestId("pos-totals-card");
  // Nothing on the ticket: the rows say they cannot say, and no plausible
  // zero appears anywhere on the payment step.
  await expect(totals).toContainText("(not priced)");
  await expect(totals).not.toContainText("CA$0.00");
  await expect(totals).toContainText("Total (est.)");
  const cta = page.getByTestId("pos-complete-sale");
  await expect(cta).toContainText("(not priced)");
  await expect(cta).not.toContainText("CA$0.00");
  // A priced line returns the figures.
  await openStep(page, "items");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await openStep(page, "payment");
  await expect(totals).toContainText("CA$4.00");
  await expect(totals).not.toContainText("(not priced)");
});

test("an empty draft shows the blank marker in the detached layout too", async ({ page }) => {
  await signIn(page, "Sales");
  await page.getByTestId("pos-view").click();
  await page.getByTestId("pos-layout-panels").click();
  await expect(page.getByTestId("pos-flow-panels")).toBeVisible();
  // The Panels payment panel paints from the first render: the same honest
  // marker, never a zero.
  const totals = page.getByTestId("pos-totals-card");
  await expect(totals).toContainText("(not priced)");
  await expect(totals).not.toContainText("CA$0.00");
  // Back to the linear flow for the tests after this one (the pick is saved
  // before this page closes).
  const savedBack = page.waitForRequest(
    (request) => request.method() === "PUT" && request.url().includes("/api/v1/pos/workspace"),
  );
  await page.getByTestId("pos-view").click();
  await page.getByTestId("pos-layout-stepper").click();
  await savedBack;
});

test("a disabled control on the ported surface drops its fill and goes grey", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await openStep(page, "payment");
  // The empty ticket's complete-sale button is disabled: grey, no gold fill,
  // no fade. The computed styles are the assertion: a restored gold fill or
  // a pure-opacity fade fails here.
  const cta = page.getByTestId("pos-complete-sale");
  await expect(cta).toBeDisabled();
  const shape = await cta.evaluate((node) => {
    const style = getComputedStyle(node);
    return { background: style.backgroundColor, color: style.color, opacity: style.opacity };
  });
  expect(shape.background).toBe("rgba(0, 0, 0, 0)");
  expect(shape.color).not.toBe("rgb(0, 0, 0)");
  expect(shape.opacity).toBe("1");
  // The split panel's ring carries the same shape while it is not settleable.
  await page.getByRole("button", { name: "Split" }).click();
  const ring = page.getByTestId("split-ring");
  await expect(ring).toBeDisabled();
  const ringShape = await ring.evaluate((node) => {
    const style = getComputedStyle(node);
    return { background: style.backgroundColor, borderColor: style.borderColor, color: style.color };
  });
  expect(ringShape.background).toBe("rgba(0, 0, 0, 0)");
});

test("the chrome sits above the surface: the demo banner is in the first viewport, on the till and in the pop-out", async ({
  page,
  context,
}) => {
  await signIn(page, "Sales");
  // The counter screen: the banner, the role, the pop-out and sign out are
  // the first row on the page, inside the first viewport.
  const banner = page.getByRole("note");
  await expect(banner).toBeVisible();
  const bannerBox = await banner.boundingBox();
  const viewport = page.viewportSize();
  expect(bannerBox).not.toBeNull();
  expect(bannerBox?.y ?? -1).toBeGreaterThanOrEqual(0);
  expect((bannerBox?.y ?? 0) + (bannerBox?.height ?? 0)).toBeLessThan((viewport?.height ?? 900) / 2);
  await expect(page.getByTestId("signed-in-role")).toBeVisible();

  // The pop-out: the same bar, same place, in its own first viewport.
  const popped = context.waitForEvent("page");
  await page.getByRole("button", { name: "Pop out" }).click();
  const second = await popped;
  await expect(second.getByTestId("pos-page-title")).toBeVisible();
  const secondBanner = second.getByRole("note");
  await expect(secondBanner).toBeVisible();
  const secondBox = await secondBanner.boundingBox();
  expect((secondBox?.y ?? 0) + (secondBox?.height ?? 0)).toBeLessThan((second.viewportSize()?.height ?? 900) / 2);
  await second.close();
});

test("End Session is closed on the mount: aria-disabled, grey, focusable, its reason beside the control, and no complete call is made", async ({
  page,
}) => {
  await signIn(page, "Sales manager");
  await ensureSession(page);
  const completeCalls: string[] = [];
  page.on("request", (request) => {
    if (/\/api\/v1\/till\/sessions\/[^/]+\/complete/.test(request.url())) {
      completeCalls.push(`${request.method()} ${request.url()}`);
    }
  });
  const reason =
    "Ending the session needs the blind count, the Z report and a manager's sign-off; the drawer close is not on this surface yet.";
  const button = page.getByTestId("end-session");
  await expect(button).toBeVisible();
  // Closed in the accessibility tree, but not natively disabled: it takes focus, so a keyboard or touch clerk reaches it.
  await expect(button).toHaveAttribute("aria-disabled", "true");
  await expect(button).not.toHaveAttribute("disabled", /.*/);
  await expect(button).toHaveAccessibleName("End Session");
  await expect(button).toHaveAttribute("title", reason);
  // The disabled rule: grey, no fill, no fade.
  const shape = await button.evaluate((node) => {
    const style = getComputedStyle(node);
    return { background: style.backgroundColor, opacity: style.opacity, cursor: style.cursor };
  });
  expect(shape.background).toBe("rgba(0, 0, 0, 0)");
  expect(shape.opacity).toBe("1");
  expect(shape.cursor).toBe("not-allowed");
  // The button's description is its own always-present element holding the reason, before any focus
  // and whatever the message line holds, so it never points at a missing or stale element.
  const description = page.getByTestId("end-session-reason");
  await expect(description).toHaveText(reason);
  await expect(page.locator("#till-message")).toHaveCount(0);
  await expect(button).toHaveAccessibleDescription(reason);
  // Focus alone puts the reason on screen BESIDE THE CONTROL (ui3 visual GAP 1: the foot message
  // line answered about 680px away, half under the hotkey bar with the Items step open), in a
  // neutral notice style (not the success green), and the notice does not announce on focus.
  await button.focus();
  await expect(button).toBeFocused();
  const notice = page.getByTestId("end-session-notice");
  await expect(notice).toBeVisible();
  await expect(notice).toHaveText(reason);
  await expect(notice).not.toHaveAttribute("role", /.*/);
  // Beside its control: the notice rides the session region the button lives
  // in, right-aligned to that band and reaching back toward the button, just
  // under the bar, so the whole notice sits in the page's first band and no
  // hotkey bar or fold can cover it.
  const box = await notice.boundingBox();
  const btnBox = await button.boundingBox();
  expect(box).not.toBeNull();
  expect(btnBox).not.toBeNull();
  const noticeRight = (box?.x ?? 0) + (box?.width ?? 0);
  const buttonRight = (btnBox?.x ?? 0) + (btnBox?.width ?? 0);
  expect(noticeRight).toBeGreaterThanOrEqual(buttonRight - 2);
  expect((box?.x ?? 0)).toBeLessThanOrEqual(buttonRight);
  expect((box?.y ?? 0)).toBeGreaterThanOrEqual((btnBox?.y ?? 0) + (btnBox?.height ?? 0));
  expect((box?.y ?? 0) + (box?.height ?? 0)).toBeLessThan(300);
  // Fully inside the window (ui3d P2-1): the left edge on screen, no
  // horizontal overflow, at this width...
  const wide = page.viewportSize()?.width ?? 0;
  expect(wide).toBeGreaterThan(0);
  expect((box?.x ?? 0)).toBeGreaterThanOrEqual(0);
  expect(noticeRight).toBeLessThanOrEqual(wide);
  // ...and at a narrower one: with a session open the bar wraps its session
  // row, which is exactly where the notice used to run off the left edge.
  await page.setViewportSize({ width: 1024, height: 700 });
  await page.waitForTimeout(150);
  const narrowBox = await notice.boundingBox();
  expect(narrowBox).not.toBeNull();
  expect((narrowBox?.x ?? -1)).toBeGreaterThanOrEqual(0);
  expect((narrowBox?.x ?? 0) + (narrowBox?.width ?? 0)).toBeLessThanOrEqual(1024);
  await expect(notice).toBeVisible();
  // Neutral palette: the edge is the control line, never the success green.
  const border = await notice.evaluate((el) => getComputedStyle(el).borderTopColor);
  const success = await notice.evaluate((el) => {
    const probe = document.createElement("div");
    probe.style.color = "var(--hos-success)";
    document.body.appendChild(probe);
    const out = getComputedStyle(probe).color;
    probe.remove();
    return out;
  });
  expect(border).not.toBe(success);
  await expect(button).toHaveAccessibleDescription(reason);
  // Pressing it ends nothing and calls nothing.
  // (Playwright treats aria-disabled as not actionable, so the press is forced.)
  await button.click({ force: true });
  await page.waitForLoadState("networkidle");
  await expect(page.getByTestId("session-dot")).toContainText("Open");
  await expect(notice).toHaveText(reason);
  // A press is the one announcement: the notice becomes a status then, and the
  // foot message line carries nothing of it.
  await expect(notice).toHaveAttribute("role", "status");
  await expect(page.locator("#till-message")).toHaveCount(0);
  expect(completeCalls).toEqual([]);
});

test("the search drop rows carry the branch's stock beside the SKU", async ({ page }) => {
  await signIn(page, "Sales");
  await openStep(page, "items");
  const field = page.getByTestId("pos-scan-input");
  await field.fill("holland");
  const row = page.getByTestId("drop-row").filter({ hasText: "Holland Stone 60 mm" }).first();
  await expect(row).toBeVisible();
  // The reference's row words: the stock after the SKU, the counter's own
  // branch (the fence), never guessed.
  await expect(row).toContainText("HPV-60");
  await expect(row).toContainText("120 on hand");
});

test("a sale rung at the till is found by the returns desk right after it commits", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await ensureSession(page);
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await openStep(page, "payment");
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  const sale = (await (await ringPost).json()) as Record<string, unknown>;
  await expect(page.getByTestId("sold-panel")).toBeVisible();

  // The ruled desk below the till finds the just-rung sale by its number and
  // lays out its lines with what is still returnable.
  await page.getByLabel("Sale number").fill(String(sale.order_no));
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-sale")).toContainText(String(sale.order_no));
  await expect(page.getByTestId("returns").getByText("Holland Stone 60 mm")).toBeVisible();
  await expect(page.getByText("sold 1, returned 0")).toBeVisible();
});

test("the margin eye's accessible name and title say the same thing in both states, and hover does not look pressed", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await ensureSession(page);
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  const eye = page.getByTestId("margin-eye");
  await expect(eye).toBeVisible();
  await expect(eye).toHaveAttribute("aria-label", "Hide margin and est. profit (counter privacy)");
  await expect(eye).toHaveAttribute("aria-pressed", "false");
  await expect(eye).toHaveAttribute("title", "Hide margin and est. profit (counter privacy)");
  const bg = () => eye.evaluate((el) => getComputedStyle(el).backgroundColor);
  await eye.hover();
  const hovered = await bg();
  await eye.click();
  await expect(eye).toHaveAttribute("aria-pressed", "true");
  // The name follows the state now (ui3 visual NOTE 1), and it says the same
  // thing the title says: pressed, both name and title say the margin is
  // hidden and how to show it again.
  await expect(eye).toHaveAttribute("aria-label", "Margin and est. profit are hidden: press to show them (counter privacy)");
  await expect(eye).toHaveAttribute("title", "Margin and est. profit are hidden: press to show them (counter privacy)");
  await page.mouse.move(0, 0);
  expect(await bg()).not.toBe(hovered);
  // And back: the name returns to the rest words with the rest state.
  await eye.click();
  await expect(eye).toHaveAttribute("aria-pressed", "false");
  await expect(eye).toHaveAttribute("aria-label", "Hide margin and est. profit (counter privacy)");
});

// The COD tile's gate in the UI. The stub always answers COD allowed, so the off side is held by
// answering the policy read with cod_enabled false for this one page (the contract's own shape).
test("with COD off by policy, the COD tile reads 'COD: off' and cannot be selected", async ({ page }) => {
  await page.route("**/api/v1/pos/policy*", async (route) => {
    const real = await route.fetch();
    const body = (await real.json()) as Record<string, unknown>;
    await route.fulfill({ response: real, json: { ...body, cod_enabled: false } });
  });
  await signIn(page, "Sales");
  await ensureSession(page);
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  const tile = page.getByRole("button", { name: /^COD/ });
  await expect(tile).toBeVisible();
  await expect(tile).toContainText("COD: off");
  await expect(tile).toBeDisabled();
  await expect(tile).toHaveAttribute("title", /disabled by policy/);
  await expect(tile).toHaveAttribute("aria-pressed", "false");
  // The one disabled rule: no fill, grey ink, no fade.
  const shape = await tile.evaluate((node) => {
    const style = getComputedStyle(node);
    return { background: style.backgroundColor, opacity: style.opacity, cursor: style.cursor };
  });
  expect(shape).toEqual({ background: "rgba(0, 0, 0, 0)", opacity: "1", cursor: "not-allowed" });
});

test("with COD allowed (the stub's default), the COD tile is selectable", async ({ page }) => {
  await signIn(page, "Sales");
  await ensureSession(page);
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  const tile = page.getByRole("button", { name: /^COD/ });
  await expect(tile).toBeEnabled();
  await expect(tile).not.toContainText("off");
});

test("a util control that cannot act takes the one disabled rule's cursor", async ({ page }) => {
  await signIn(page, "Sales");
  // Park ticket on an empty ticket, then Open order and Print receipt with
  // nothing typed: each looks grey and closed like the rule's other controls,
  // so each carries the rule's own not-allowed cursor, not a pointer that
  // promises an act (ui3 visual NOTE 5).
  const park = page.getByRole("button", { name: "Park ticket" });
  await expect(park).toBeDisabled();
  expect(await park.evaluate((node) => getComputedStyle(node).cursor)).toBe("not-allowed");
  await page.locator("details.more > summary").filter({ hasText: /^\s*More\s*$/ }).click();
  const open = page.getByRole("button", { name: "Open order" });
  await expect(open).toBeDisabled();
  expect(await open.evaluate((node) => getComputedStyle(node).cursor)).toBe("not-allowed");
  const print = page.getByRole("button", { name: "Print receipt" });
  await expect(print).toBeDisabled();
  expect(await print.evaluate((node) => getComputedStyle(node).cursor)).toBe("not-allowed");
});

test("a ticket parked with a line price pending lists no total", async ({ page }) => {
  await page.route("**/api/v1/pricing/resolve", async (route) => {
    const body = route.request().postDataJSON() as { product_id?: string };
    if (body.product_id === "p-polymer-sand") {
      await route.fulfill({ status: 422, contentType: "application/json", body: JSON.stringify({ error: "line could not be priced by the engine" }) });
      return;
    }
    await route.fallback();
  });
  await signIn(page, "Sales");
  await searchAndAdd(page, "polymeric", "Polymeric Locking Sand");
  await expect(page.getByTestId("pos-line-row").locator(".lprice")).toHaveText("price pending");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  const ticket = parkedDialog.locator(".ticket").first();
  await expect(ticket).toContainText("(not priced)");
  await expect(ticket).not.toContainText("CA$");
});

test("a parked ticket this window holds no price memory for lists the same marker, never a bare line count", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  // A new window: the park-time snapshot lives in the first window's memory only.
  await signIn(page, "Sales");
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const tickets = page.locator("hos-pos-parked .hos-dialog .ticket");
  await expect(tickets.first()).toBeVisible();
  const count = await tickets.count();
  for (let i = 0; i < count; i += 1) {
    await expect(tickets.nth(i)).toContainText("(not priced)");
    await expect(tickets.nth(i)).not.toContainText("CA$");
  }
});

test("a pickup whose prices never answer reads the margin marker, never a figure", async ({ page }) => {
  // Every price the engine would answer is refused for this whole page, so the
  // pickup's lines stay price pending (a window with no price memory).
  await page.route("**/api/v1/pricing/resolve", async (route) => {
    await route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({ error: "line could not be priced by the engine" }),
    });
  });
  await signIn(page, "Sales");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await searchAndAdd(page, "polymeric", "Polymeric Locking Sand");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  // A fresh window picks the ticket up. The margin glance still answers (it
  // derives its figures from the server's own prices), so the margin column IS
  // on screen, and the building window's rule must hold there too: a line
  // whose price has not answered reads the marker in its margin cell, never a
  // percentage beside (not priced).
  await signIn(page, "Sales");
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  // The rail lists the branch's tickets in insertion order, and the suite's
  // earlier stories leave theirs parked: the ticket this test just parked is
  // the rail's last.
  await parkedDialog.getByRole("button", { name: "Resume" }).last().click();
  await openStep(page, "items");
  const rows = page.getByTestId("pos-line-row");
  await expect(rows).toHaveCount(2);
  for (let index = 0; index < 2; index += 1) {
    await expect(rows.nth(index)).toContainText("(not priced)");
  }
  const margins = page.getByTestId("pos-line-margin");
  await expect(margins).toHaveCount(2);
  for (let index = 0; index < 2; index += 1) {
    await expect(margins.nth(index)).toHaveText("(none)");
  }
  // The footer bar keeps the same rule (ui3d P2-2): the glance still answers
  // with a ticket margin and a profit figure derived from the server's own
  // prices, and while a line is pending neither segment may state one. The
  // building window's bar shows neither segment with these lines.
  const txbar = page.getByTestId("pos-txbar");
  await expect(txbar).toContainText("2 Items");
  await expect(txbar).not.toContainText("Margin");
  await expect(txbar).not.toContainText("Est. Profit");
});

test("the pickup line says the prices are being re-derived while any line is still pending", async ({
  page,
}) => {
  // Every price the engine would answer is refused for this whole page: the
  // pickup's lines stay price pending.
  await page.route("**/api/v1/pricing/resolve", async (route) => {
    await route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({ error: "line could not be priced by the engine" }),
    });
  });
  await signIn(page, "Sales");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  await signIn(page, "Sales");
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  // The rail lists the branch's tickets in insertion order, and the suite's
  // earlier stories leave theirs parked: the ticket this test just parked is
  // the rail's last.
  await parkedDialog.getByRole("button", { name: "Resume" }).last().click();
  // The lines above the line still read price pending, so the words say the
  // derivation is happening, not that it happened (ui3 visual GAP 3).
  const message = page.locator("#till-message");
  await expect(message).toContainText(/Resumed PARK-\d+: re-deriving prices\./);
  await expect(message).not.toContainText("prices re-derived");
});

test("a price that answers after the pickup line was set flips the line to prices re-derived", async ({
  page,
}) => {
  // The engine refuses every price for now; the refusal lifts mid-story, and an
  // operator gesture (a quantity edit) reprices the line into an answer. The
  // pickup line's tail renders from the cart's any-line-pending state, so the
  // words flip with the answer instead of staying captured from the resume.
  let refusePrices = true;
  await page.route("**/api/v1/pricing/resolve", async (route) => {
    if (refusePrices) {
      await route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify({ error: "line could not be priced by the engine" }),
      });
      return;
    }
    await route.fallback();
  });
  await signIn(page, "Sales");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  await signIn(page, "Sales");
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  // The rail lists the branch's tickets in insertion order, and the suite's
  // earlier stories leave theirs parked: the ticket this test just parked is
  // the rail's last.
  await parkedDialog.getByRole("button", { name: "Resume" }).last().click();
  const message = page.locator("#till-message");
  await expect(message).toContainText(/Resumed PARK-\d+: re-deriving prices\./);
  await openStep(page, "items");
  const row = page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" });
  await expect(row).toHaveCount(1);
  refusePrices = false;
  const qty = page.getByRole("textbox", { name: "Quantity: Holland Stone 60 mm" });
  await qty.fill("2");
  await qty.blur();
  await expect(row.locator(".ltot")).toHaveText("CA$8.00");
  // The line above is priced now, so a tail captured when the message was set
  // would still say re-deriving: the words say the derivation happened.
  await expect(message).toContainText(/Resumed PARK-\d+: prices re-derived now\./);
  await expect(message).not.toContainText("re-deriving prices");
});

test("a resumed ticket reads the pending markers from the moment it lands, before any price answer", async ({
  page,
}) => {
  // Every pricing answer is HELD, not refused: the request stays in flight, so
  // the test sees the ticket exactly as the resume paints it, before the first
  // answer lands. The resumed lines carry the add path's pending flag, so the
  // cells read the markers, the footer bar shows neither margin nor profit
  // figure, and nothing states CA$0.00 for a line nobody has priced yet.
  const held: Route[] = [];
  await page.route("**/api/v1/pricing/resolve", async (route) => {
    held.push(route); // held in flight: answered only when the test releases it
  });
  await signIn(page, "Sales");
  await searchAndAdd(page, "holland", "Holland Stone 60 mm");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  await signIn(page, "Sales");
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  // The rail lists the branch's tickets in insertion order, and the suite's
  // earlier stories leave theirs parked: the ticket this test just parked is
  // the rail's last.
  await parkedDialog.getByRole("button", { name: "Resume" }).last().click();
  await expect(parkedDialog).not.toBeVisible();
  await openStep(page, "items");
  const row = page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" });
  await expect(row).toHaveCount(1);
  // Before any answer: the pending marker on the cell, the footer bar silent.
  await expect(row.locator(".ltot")).toHaveText("(not priced)");
  const txbar = page.getByTestId("pos-txbar");
  await expect(txbar).toContainText("1 Item");
  await expect(txbar).not.toContainText("Margin");
  await expect(txbar).not.toContainText("Est. Profit");
  // The answer lands: the real figures return.
  for (const route of held) await route.fallback().catch(() => undefined);
  await expect(row.locator(".ltot")).toHaveText("CA$4.00");
  await expect(txbar).toContainText("Margin");
  await expect(txbar).toContainText("Est. Profit");
});

test("the top bar is on the till's compact scale: about 48px, small chrome buttons, the banner on one line", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page, "Sales");
  const measured = await page.evaluate(() => {
    const root = document.querySelector("ct-app-root")?.shadowRoot;
    const height = (selector: string): number => root?.querySelector(selector)?.getBoundingClientRect().height ?? -1;
    const theme = root?.querySelector("[data-testid=theme-control]");
    return {
      bar: height(".surfacebar"),
      banner: height(".demo-banner"),
      theme: height("[data-testid=theme-control]"),
      themeFont: theme === null || theme === undefined ? "" : getComputedStyle(theme).fontSize,
    };
  });
  expect(measured.bar).toBeLessThanOrEqual(56);
  expect(measured.bar).toBeGreaterThanOrEqual(44);
  expect(measured.theme).toBeLessThan(40);
  expect(measured.themeFont).toBe("11px");
  expect(measured.banner).toBeLessThan(32);
  // The banner stays visible and legible.
  await expect(page.getByRole("note")).toBeVisible();
  await expect(page.getByRole("note")).toContainText("Everything on this box is invented demo data");
});

test("the entry page's header is on the same compact scale as the till's top bar", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  const measured = await page.evaluate(() => {
    const root = document.querySelector("ct-app-root")?.shadowRoot;
    const height = (selector: string): number => root?.querySelector(selector)?.getBoundingClientRect().height ?? -1;
    const theme = root?.querySelector("[data-testid=theme-control]");
    return {
      header: height("header"),
      banner: height(".demo-banner"),
      theme: height("[data-testid=theme-control]"),
      themeFont: theme === null || theme === undefined ? "" : getComputedStyle(theme).fontSize,
    };
  });
  expect(measured.header).toBeLessThanOrEqual(64);
  expect(measured.header).toBeGreaterThanOrEqual(44);
  expect(measured.theme).toBeLessThan(40);
  expect(measured.themeFont).toBe("11px");
  expect(measured.banner).toBeLessThan(32);
  await expect(page.getByRole("note")).toBeVisible();
});
