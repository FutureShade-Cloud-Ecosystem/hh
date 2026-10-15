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
// in the reviewed words, a contractor's Charcoal pavers ring at the account's
// rule price with the margin column refused (the stub's policy word is
// MANAGER_UP, so the Sales till shows none of the margin surface and a
// Sales-manager story holds the allowed side), a ticket parks and resumes with
// its customer, a second press on Complete commits exactly one sale, and the
// surface's two modes both work: the layout pick and its persistence, the
// panel toggles, and the pop-out.
//
// The sale read comparison works through the real path: this spec mints its
// own demo session (the same public route the app's cards use) and reads the
// committed sale back through the same proxy, with the bearer and org header
// the staff routes require.
import { expect, test, type Page, type Response, type Route } from "@playwright/test";

// A leg that pins the stub's clock never leaves it pinned: the stub is one server for the whole
// suite, and the next leg's session tokens are minted on whatever clock it holds.
test.afterEach(async ({ request }) => {
  await request.post("/_stub/clock", { data: {} });
});

/** The screen-side rendering of integer cents, for comparing against the app. */

function dollars(cents: number): string {
  const negative = cents < 0;
  const magnitude = Math.abs(cents);
  const whole = Math.floor(magnitude / 100);
  const remainder = magnitude - whole * 100;
  return `${negative ? "-" : ""}$${whole.toLocaleString("en-CA")}.${String(remainder).padStart(2, "0")}`;
}

/** The dealer's day, computed the way the stub validates it, for comparison. */
function houseDay(at: Date = new Date()): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "America/Toronto",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(at);
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
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await searchAndAdd(page, "polymeric", "Polymeric Locking Sand");
  await searchAndAdd(page, "clear", "Clear Stone");
  const lines = page.getByTestId("pos-line-row");
  await expect(lines).toHaveCount(3);
  const charcoalQty = page.getByRole("textbox", { name: "Quantity: Charcoal Stone 60 mm" });
  await charcoalQty.fill("3");
  await charcoalQty.blur();
  await expect(lines.filter({ hasText: "Charcoal Stone 60 mm" }).locator(".ltot")).toHaveText("$12.00");

  // The walk-in's retail on every line. The margin surface is not part of
  // what this caller's till shows: the policy answers MANAGER_UP and Sales is
  // frontline (tier 3), so the column, the footer's margin and profit
  // segments, and the eye all refuse, even though the glance itself still
  // answers (P3-10: the surface follows the policy word, not the glance).
  await expect(page.getByTestId("pos-line-margin")).toHaveCount(0);
  await expect(page.getByTestId("pos-lines")).not.toContainText("Margin");
  const txbar = page.getByTestId("pos-txbar");
  await expect(txbar).not.toContainText("Margin");
  await expect(txbar).not.toContainText("Est. Profit");
  await expect(page.getByTestId("margin-eye")).toHaveCount(0);

  // The cash quick entry: what the customer hands over is typed, and the
  // change due is the ring's own answer. The till computes no total before
  // the ring, so the tile states no change figure either.
  await openStep(page, "payment");
  // The pre-ring summary, figure by figure, on a ticket the stub's prices
  // pin: 12.00 + 14.00 + 38.00 is a $64.00 subtotal, and that is the only
  // figure the card assembles: the tax and the total are the ring's own
  // answer, named in words, never computed here.
  const totals = page.getByTestId("pos-totals-card");
  // The card's whole shape: the Subtotal and, from the server's sale-preview
  // (row 985), the Tax and the Total. Exactly three rows: a fourth would be a
  // figure the till computes itself.
  await expect(totals.getByTestId("pos-preview-total")).toBeVisible();
  await expect(totals.locator(".trow")).toHaveCount(3);
  await expect(totals).toContainText("Subtotal");
  await expect(totals).toContainText("$64.00");
  await expect(totals.getByTestId("pos-preview-tax")).toContainText("$8.32");
  await expect(totals.getByTestId("pos-preview-total")).toContainText("$72.32");
  // The absences are pinned on locators that hold the content (the review's
  // P2-1: the step panel is a shadow slot and sees none of this body), and
  // the positive assertions above prove these locators see the card they
  // judge: the card carries no estimate mark, and the Complete button names
  // the server's stated total.
  await expect(totals).not.toContainText("(est.");
  await expect(page.getByTestId("pos-complete-sale")).toContainText("total $72.32");
  await page.getByLabel("Cash tendered in dollars: optional; leave empty to ring the exact total").fill("80.00");
  await expect(page.getByTestId("cash-change-preview")).toContainText("Change due");
  await expect(page.getByTestId("cash-change-preview")).toContainText("the ring's own answer");
  // No change figure pre-ring: the server states it on the confirmation.
  await expect(page.getByTestId("cash-change-preview")).not.toContainText("$");

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

  // The collapsed Items card's summary now reads the SERVER's own figure, and
  // no mark stands beside it: the ring is done, and no browser assembly has
  // any business on screen next to the confirmation's committed total (before
  // the ring the till assembles no total at all to disagree with it).
  const itemsSummary = page.getByTestId("step-summary-items");
  await expect(itemsSummary).toHaveText(`3 items · ${dollars(read.total_cents as number)}`);
  await expect(itemsSummary).not.toContainText("(est.)");

  // The printed receipt is the stub's demo mirror of the core's own 80 mm
  // document, same words, same layout. An over-tendered cash sale carries the
  // change due row with the ring's own figure, the fulfilment reads as the
  // counter's plain words and never as the wire's constant, and the totals
  // and the tender are the sale read's own figures.
  const receiptPage = page.context().waitForEvent("page");
  await sold.getByTestId("sold-print").click();
  const printed = await receiptPage;
  const printedText = await printed.locator("body").innerText();
  expect(printedText).toContain(`Change due\t${dollars(sale.change_due_cents as number)}`);
  expect(printedText).toContain("Collected at the counter");
  expect(printedText).not.toContain("COLLECT_NOW");
  // The customer row is required on every sale: a walk-in prints the counter's own word.
  expect(printedText).toContain("Customer: Walk-in");
  expect(printedText).toContain(`Subtotal\t${dollars(read.subtotal_cents as number)}`);
  expect(printedText).toContain(`\t${dollars(read.tax_cents as number)}`);
  expect(printedText).toContain(`Total\t${dollars(read.total_cents as number)}`);
  // The tender row names the method and its payment reference on one line.
  const cashWords = dollars(read.total_cents as number).replace("$", "\\$");
  expect(printedText).toMatch(new RegExp(`CASH PMT-\\d+\\t${cashWords}`));
  await printed.close();

  // The full-page document (format=page) is the same receipt laid out as a
  // sheet: the same figures on its own print CSS, and the stub's print count
  // marks it REPRINT #1, the second time this sale's paper was asked for.
  const pagePrinting = page.context().waitForEvent("page");
  await sold.getByTestId("sold-print-page").click();
  const printedPage = await pagePrinting;
  const pageText = await printedPage.locator("body").innerText();
  expect(pageText).toContain("Collected at the counter");
  expect(pageText).toContain(`Total\t${dollars(read.total_cents as number)}`);
  expect(pageText).toContain(`Change due\t${dollars(sale.change_due_cents as number)}`);
  expect(pageText).toContain("REPRINT #1");
  await printedPage.close();

  // The reprint path: the More menu's any-past-sale receipt, fetched by the
  // order id, comes back MARKED with its ordinal, the stub's count mirroring
  // the core's own (two prints of this sale came before it).
  await page.locator("details.more > summary").filter({ hasText: /^\s*More\s*$/ }).click();
  await page.getByLabel("Open order by id or #SO number").fill(String(sale.order_id));
  const reprintPrinting = page.context().waitForEvent("page");
  await page.locator("details.more").getByRole("button", { name: "Print receipt" }).click();
  const reprinted = await reprintPrinting;
  const reprintedText = await reprinted.locator("body").innerText();
  expect(reprintedText).toContain("REPRINT #2");
  expect(reprintedText).toContain(`Total\t${dollars(read.total_cents as number)}`);
  expect(reprintedText).toContain(`Change due\t${dollars(sale.change_due_cents as number)}`);
  await reprinted.close();
});

test("the quantity rung is the quantity on the screen, and the requested date is the house zone's day", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  const charcoalQty = page.getByRole("textbox", { name: "Quantity: Charcoal Stone 60 mm" });
  await charcoalQty.fill("10");
  await charcoalQty.blur();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" }).locator(".ltot")).toHaveText(
    "$40.00",
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

// The two hours at which the dealer's day and the UTC day disagree and agree. Each leg pins the
// page's clock AND the stub's clock to its own instant, so it is red on wrong code at any wall
// hour (the same pin the drive run's legs take): the stub builds its day on the pinned clock, and
// a ring asked for in the browser's UTC day is refused in the evening hour, where the two days
// differ, so the sale never commits. Built from the host's own date so no calendar literal is
// written here.
function utcAt(hours: number, minutes: number): number {
  const at = new Date();
  at.setUTCHours(hours, minutes, 0, 0);
  return at.getTime();
}

for (const [label, instant, differs] of [
  ["half past midnight UTC, the previous evening in the dealer's zone", utcAt(0, 30), true],
  ["the middle of the dealer's afternoon", utcAt(19, 0), false],
] as const) {
  test(`the ring asks for the dealer's local day, not the browser's UTC day: ${label}`, async ({ page, request }) => {
    await page.clock.install({ time: instant });
    const pinned = await request.post("/_stub/clock", { data: { now: instant } });
    expect(pinned.ok()).toBe(true);
    await signIn(page, "Sales");
    await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
    await openStep(page, "payment");
    const ringPost = page.waitForResponse(
      (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
    );
    await page.getByTestId("pos-complete-sale").click();
    const ringResponse = await ringPost;
    const body = (await ringResponse.request().postDataJSON()) as Record<string, unknown>;
    const dealersDay = houseDay(new Date(instant));
    const utcDay = new Date(instant).toISOString().slice(0, 10);
    expect(body.requested_date).toBe(dealersDay);
    // The leg means something only where the two days differ (and agrees where they do not).
    expect(dealersDay !== utcDay).toBe(differs);
    // The wrong day is not merely asserted against: the stub refuses it, so the sale commits.
    await expect(page.getByTestId("sold-panel")).toBeVisible();
  });
}

test("a card sale shows the simulated word on the tender and on the confirmation, and none when the rail answers false", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");

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

  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  const charcoalQty = page.getByRole("textbox", { name: "Quantity: Charcoal Stone 60 mm" });
  await charcoalQty.fill("10");
  await charcoalQty.blur();
  // The account's own rule price, ten percent off retail.
  await expect(
    page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" }).locator(".lprice"),
  ).toHaveText("$3.60");

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

  // The receipt document, printed from the confirmation: the demo mirror of
  // the core's own document, the account it was sold to, the invoice, the
  // figures, the ON ACCOUNT row where a payments row does not exist.
  const receiptPage = page.context().waitForEvent("page");
  await sold.getByTestId("sold-print").click();
  const printed = await receiptPage;
  const printedText = await printed.locator("body").innerText();
  expect(printedText).toContain(`Customer: Fenwick Builders Ltd (C02)`);
  expect(printedText).toContain(`Invoice ${String(sale.invoice_no)}`);
  expect(printedText).toContain(`Subtotal\t${dollars(read.subtotal_cents as number)}`);
  expect(printedText).toContain(`\t${dollars(read.tax_cents as number)}`);
  expect(printedText).toContain(`Total\t${dollars(read.total_cents as number)}`);
  // The ON ACCOUNT row is the core's synthetic tender: the account it bills,
  // the invoice as its reference, the total as its amount.
  const accountWords = dollars(read.total_cents as number).replace("$", "\\$");
  expect(printedText).toMatch(new RegExp(`ON ACCOUNT ${String(sale.invoice_no)}\\t${accountWords}`));
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

  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
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

test("the collapsed Items summary carries the priced lines' subtotal before the ring", async ({ page }) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  // Another step open: the Items card collapses and its summary speaks. The
  // figure is the priced lines' own subtotal (the only figure the till
  // assembles), unmarked: there is no estimate on the sale surface to mark.
  await openStep(page, "customer");
  await expect(page.getByTestId("step-summary-items")).toHaveText("1 item · subtotal $4.00");
  await expect(page.getByTestId("step-summary-items")).not.toContainText("(est.");
});

test("the read-back states the server's tax and total, and the confirmation reads the server's figures unmarked", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await searchAndAdd(page, "polymeric", "Polymeric Locking Sand");
  // The read-back, pre-ring: the priced lines' subtotal ($18.00), the server's
  // stated tax and total (row 985), and no estimate mark anywhere on it.
  await openStep(page, "summary");
  const readback = page.getByTestId("pos-readback");
  await expect(readback).toContainText("$18.00");
  await expect(readback.getByTestId("readback-preview-tax")).toContainText("$2.34");
  await expect(readback.getByTestId("readback-preview-total")).toContainText("$20.34");
  await expect(readback).not.toContainText("(est.");
  // The ring: the confirmation carries the server's own figures, unmarked.
  await openStep(page, "payment");
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  const sale = (await (await ringPost).json()) as Record<string, unknown>;
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await expect(sold).toContainText(dollars(sale.total_cents as number));
  await expect(sold).not.toContainText("(est.");
  // And the collapsed Items summary echoes the server's total, still unmarked.
  await expect(page.getByTestId("step-summary-items")).toHaveText(
    `2 items · ${dollars(sale.total_cents as number)}`,
  );
});

// The row 1214 ruling removed the COD fulfilment tile: the pay before release ring is the
// Cash On Demand account's payment option now, the server offers it and the payment step
// draws it (test/paystep-pbr.pw.ts holds those legs, the ring, its body and its read back).
// The two refusals the tile made reachable from this terminal have no route here anymore,
// and both stay pinned where the rule lives, in the core: a terms account's COD ring and
// preview (TestSale_CODSaleType_RefusedForATermsAccount,
// TestSalePreview_CODSaleType_RefusedForATermsAccount) and the walk-in no-account COD
// constraint (the credit gate's COD_CONSTRAINT, whose counter words _pos.ts still maps for
// any ring the server refuses). The stub keeps both arms, mirroring the core's answers for
// any client that rings them.

test("the account's available credit says it is a derived figure, in the picker row and on the card", async ({
  page,
}) => {
  await signIn(page, "Sales");
  // The picker's row figure is the browser's own subtraction (limit less
  // balance, floored at zero): the counter's customer search row carries
  // limit and balance only, so the label says the figure is derived.
  await page.getByRole("button", { name: /Select customer/ }).click();
  const searchBox = page.getByPlaceholder("Search name or account #…");
  await searchBox.fill("C02");
  const row = page.locator("button.row").filter({ hasText: "Fenwick Builders Ltd" }).first();
  await expect(row).toBeVisible();
  await expect(row.locator(".credit")).toContainText("Available (derived)");
  // The same figure rides the resolved account's card, and the word goes with
  // it there too. The pick advances the flow to Items, so the customer step
  // opens again before the card is read.
  await row.click();
  await openStep(page, "customer");
  await expect(page.getByTestId("customer-card")).toContainText("Available (derived)");
});

test("a deposit sale's confirmation labels the balance due a derived figure", async ({ page }) => {
  await signIn(page, "Sales");
  await pickAccount(page, "C02", "Fenwick Builders Ltd");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  // Item 9.6: the deposit is line one of the payment step on a fulfilment that waits; the
  // credit account's line two is the rest on account.
  await openStep(page, "items");
  await page.getByRole("button", { name: "Will-call" }).click();
  await openStep(page, "payment");
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_ON_ACCOUNT").click();
  await page.getByTestId("deposit-amount").fill("1.00");
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("deposit-commit").click();
  const sale = (await (await ringPost).json()) as Record<string, unknown>;
  expect(sale.deposit_cents).toBe(100);

  // The confirmation's balance (total less the deposit) is browser
  // arithmetic on server-stated operands, so its label says so. The
  // headline is the deposit branch's own word, and the deposit fact row
  // stands beside it.
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await expect(sold).toContainText("Deposit taken");
  await expect(sold.locator(".sold-amount-label")).toHaveText("Balance due (derived)");
  await expect(sold).toContainText("$3.07");
  await expect(sold).toContainText("$1.00");
});

test("a C01 Charcoal paver line rings at its rule price, and the margin column refuses the Sales till", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await pickAccount(page, "C01", "Maple Ridge Contracting");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  // The account's rule: eighteen percent off the $4.00 retail, rung as the
  // engine answered it.
  const charcoal = page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" });
  await expect(charcoal.locator(".lprice")).toHaveText("$3.28");
  // The margin word beside the price does not render for this caller: the
  // policy's word is MANAGER_UP and Sales is tier 3, so no margin cell shows,
  // not even the explicit unknown for the line whose cost this branch does
  // not carry (the glance answers; the refusal is the surface's own rule).
  await searchAndAdd(page, "edge", "Edge Restraint 8 ft");
  const edge = page.getByTestId("pos-line-row").filter({ hasText: "Edge Restraint 8 ft" });
  await expect(edge.locator(".lprice")).toHaveText("$26.24");
  await expect(page.getByTestId("pos-line-margin")).toHaveCount(0);
});

test("a ticket parks and resumes with its customer, at the account's price", async ({ page }) => {
  await signIn(page, "Sales");
  await pickAccount(page, "C01", "Maple Ridge Contracting");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  const charcoal = page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" });
  await expect(charcoal.locator(".lprice")).toHaveText("$3.28");

  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);

  // The branch's parked list: open it, find the ticket, resume it. The fence
  // is the server's; the stub's world holds a Julian ticket and it never
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
  await expect(charcoal.locator(".lprice")).toHaveText("$3.28");
});

test("a second press on Complete while the ring is in flight commits exactly one sale", async ({ page }) => {
  const ringPosts: Array<Record<string, unknown>> = [];
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().endsWith("/api/v1/pos/sales")) {
      ringPosts.push((request.postDataJSON() ?? {}) as Record<string, unknown>);
    }
  });

  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await expect(
    page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" }).locator(".ltot"),
  ).toHaveText("$4.00");
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
  ).toHaveText("$32.00");
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
  await expect(second.getByTestId("pagectx")).toContainText("Point Loma Yard");
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
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await searchAndAdd(page, "polymeric", "Polymeric Locking Sand");
  const polymeric = page.getByTestId("pos-line-row").filter({ hasText: "Polymeric Locking Sand" });
  await expect(polymeric.locator(".lprice")).toHaveText("price pending");
  // The total cell says the same word every other surface does.
  await expect(polymeric.locator(".ltot")).toHaveText("(not priced)");

  // An ordinary act on the OTHER line reprices that line and leaves the
  // unanswered one saying so.
  const charcoalQty = page.getByRole("textbox", { name: "Quantity: Charcoal Stone 60 mm" });
  await charcoalQty.fill("3");
  await charcoalQty.blur();
  await expect(
    page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" }).locator(".ltot"),
  ).toHaveText("$12.00");
  await expect(polymeric.locator(".lprice")).toHaveText("price pending");
  // A pending line beside a priced one keeps the whole summary on the marker:
  // no figure ($12.00 for the priced line alone, or any sum) is stated while
  // a line has no answer. The ring button and the change preview say the same.
  await openStep(page, "payment");
  const totals = page.getByTestId("pos-totals-card");
  await expect(totals).toContainText("(not priced)");
  await expect(totals).not.toContainText("$12.00");
  await expect(page.getByTestId("pos-complete-sale")).toContainText("(not priced)");
  await expect(page.getByTestId("pos-complete-sale")).not.toContainText("$");
  // The typed cash tile: the same ring words whatever the lines' price
  // state, and no figure computed from a total the till does not have.
  await page.getByTestId("cash-tendered").fill("20");
  await expect(page.getByTestId("cash-change-preview")).toContainText("the ring's own answer");
  await expect(page.getByTestId("cash-change-preview")).not.toContainText("$");
  await page.getByTestId("cash-tendered").fill("1");
  await expect(page.getByTestId("cash-change-preview")).toContainText("the ring's own answer");
  await expect(page.getByTestId("cash-change-preview")).not.toContainText("Less than the total");
  await page.getByTestId("cash-tendered").fill("");
  // The split panel works from the server's total (its sale-preview prices the
  // pending line itself): Total to split is stated, and the legs over it read
  // as over, never as settled.
  await page.getByRole("button", { name: "Split" }).click();
  const splitPanel = page.getByTestId("pos-split-panel");
  await expect(splitPanel.getByTestId("split-total")).toContainText("Total to split $");
  await splitPanel.getByTestId("split-amount-0").fill("5.00");
  await splitPanel.getByTestId("split-amount-1").fill("500.00");
  await expect(page.getByTestId("split-remaining")).toContainText("Over by $");
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
  // price ($11.48), the line's total the picked quantity's answer.
  await expect(polymeric.locator(".lprice")).toHaveText("$11.48");
  await expect(polymeric.locator(".ltot")).toHaveText("$22.96");
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "price pending" })).toHaveCount(0);
  // Every line priced: the subtotal returns, and the Complete button names the
  // server's stated total (row 985), or the subtotal with the tax left to the
  // ring while the preview has not answered.
  await openStep(page, "payment");
  await expect(totals).not.toContainText("(not priced)");
  const cta = page.getByTestId("pos-complete-sale");
  await expect(cta).toContainText("total $");
  // The exact shape, both session states: nothing can follow the words but
  // the pay key's own keycap chip (the roster's word, aria-hidden, never sale
  // wording). The chip joined the button after the pin was written; the
  // pattern admits it by shape, not by the roster's choice of key.
  await expect(cta).toHaveText(
    /^\s*(Open session · then complete|Complete sale) · (total \$[\d,]+\.\d{2}|subtotal (\$[\d,]+\.\d{2}|\(not priced\)) \+ tax at the ring)(\s+[A-Za-z0-9+]+)?\s*$/,
  );
  await expect(cta).not.toContainText("(est.");
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
    if (heldOne && body.product_id === "p-charcoal-60") {
      heldOne = false;
      await new Promise<void>((resolve) => {
        releaseFirst = resolve;
      });
    }
    await route.fallback();
  });

  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  const charcoal = page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" });
  const charcoalQty = page.getByRole("textbox", { name: "Quantity: Charcoal Stone 60 mm" });
  await charcoalQty.fill("3");
  await charcoalQty.blur();
  await expect(charcoal.locator(".ltot")).toHaveText("$12.00");

  releaseFirst?.();
  // The stale answer (priced for one unit) landed on nothing: the line still
  // carries the newest answer's figures.
  await page.waitForTimeout(600);
  await expect(charcoal.locator(".lprice")).toHaveText("$4.00");
  await expect(charcoal.locator(".ltot")).toHaveText("$12.00");
});

test("a parked ticket survives the hand-over: the next login at the branch sees it and resumes it, priced fresh", async ({
  page,
}) => {
  // The first counter person parks C01's pavers and leaves.
  await signIn(page, "Sales");
  await pickAccount(page, "C01", "Maple Ridge Contracting");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
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
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" })).toHaveCount(1);
  await expect(
    page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" }).locator(".lprice"),
  ).toHaveText("$4.00");
  await expect(parkedDialog).toHaveCount(0);
});

test("a resume over lines on the counter asks first: declining leaves everything where it was", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
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
  // with it), so the parked ticket replaces them knowingly: the Charcoal line
  // is back, the polymeric one went with the counter's draft.
  page.once("dialog", (dialog) => dialog.accept());
  await parkedDialog.getByRole("button", { name: "Resume" }).first().click();
  await openStep(page, "items");
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" })).toHaveCount(1);
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Polymeric Locking Sand" })).toHaveCount(0);
});

// WCAG 1.4.11 for the till's own inputs (pos-terminal's select, input and textarea rule): the
// boundary is the design system's control line and clears 3:1 against what it sits on, in both
// themes, as the returns desk's find input does.
for (const scheme of ["dark", "light"] as const) {
  test(`the till's own inputs keep the control boundary at 3:1 (${scheme})`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await signIn(page, "Sales");
    await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
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
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Split" }).click();
  const band = page.getByTestId("cta-split-state");
  await expect(band).toBeVisible();
  await expect(band).toContainText("Split: compose the legs below");
  // The explanation is visible text, not a hover title only.
  await expect(page.getByTestId("cta-split-why")).toBeVisible();
  await expect(page.getByTestId("cta-split-why")).toContainText("The legs settle the stated total");
  await expect(band).not.toHaveAttribute("title", /.*/);
  // A statement, not a control: no button, no disabled state to fake.
  expect(await band.evaluate((el) => el.tagName)).not.toBe("BUTTON");
  await expect(band).toHaveAttribute("role", "status");
  await expect(page.getByRole("button", { name: /Split: compose/ })).toHaveCount(0);
});

test("a fresh line before its price answers: the split works from the server's stated total and rings", async ({
  page,
}) => {
  // The resolve answer for the Charcoal line is held until released: the line is on the ticket,
  // priced by no one yet (ui2c P3-2's fresh pending line). The server's sale-preview prices the
  // line itself, so the panel states the total, fills the last leg, and arms the ring when the
  // legs equal it.
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
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Split" }).click();
  const panel = page.getByTestId("pos-split-panel");
  // The server's sale-preview prices the pending line itself, so the panel states the total and
  // fills the last leg with what is left of it.
  await expect(panel.getByTestId("split-total")).toHaveText("Total to split $4.52");
  await panel.getByTestId("split-amount-0").fill("4.00");
  await expect(panel.getByTestId("split-amount-1")).toHaveValue("0.52");
  await expect(page.getByTestId("split-remaining")).toHaveText("Left to assign $0.00");
  await expect(page.getByTestId("split-ring")).toBeEnabled();
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("split-ring").click();
  const sale = (await (await ringPost).json()) as Record<string, unknown>;
  // The server's own answer: the legs settle its total exactly, browser blind or not.
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await expect(sold).toContainText(dollars(sale.total_cents as number));
  hold = false;
  release();
});

test("a split with a card leg carries the simulated word on the confirmation and on the receipt", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await ensureSession(page);
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Split" }).click();
  // Two legs: cash for part, a card leg for the rest. The Charcoal line at
  // retail with tax comes to $4.52 at the ring: $4.00 cash and the $0.52
  // card leg settle it.
  const leg0 = page.getByTestId("split-amount-0");
  const leg1 = page.getByTestId("split-amount-1");
  await leg0.fill("4.00");
  await leg1.fill("0.52");
  // The legs equal the server's stated total, so what is left is nothing and the ring arms.
  await expect(page.getByTestId("split-remaining")).toHaveText("Left to assign $0.00");
  await expect(page.getByTestId("split-ring")).toBeEnabled();
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("split-ring").click();
  const sale = (await (await ringPost).json()) as Record<string, unknown>;

  // The confirmation: the split touched the simulated rail, so it says so,
  // exactly as a CARD tender does, and the tender row names the split and
  // both its legs, never one leg's word for two legs' money.
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await expect(sold.getByTestId("sold-simulated-notice")).toContainText("Simulated card: no card was charged");
  await expect(sold).toContainText("Split: Cash $4.00 and Card $0.52");

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

test("the split panel states the total to split and what is left to assign", async ({ page }) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Split" }).click();
  const panel = page.getByTestId("pos-split-panel");
  // The total the legs must settle is the server's (its sale-preview): the panel states it and
  // the amount left to assign, and the shape refusals stay the panel's own.
  await expect(panel.getByTestId("split-total")).toHaveText("Total to split $4.52");
  await panel.getByTestId("split-amount-0").fill("4.00");
  await expect(page.getByTestId("split-remaining")).toHaveText("Left to assign $0.00");
  await panel.getByTestId("split-amount-1").fill("");
  await expect(page.getByTestId("split-remaining")).toHaveText("Left to assign $0.52");
  await expect(page.getByTestId("split-problem")).toContainText("Every tender needs an amount greater than zero");
  await expect(page.getByTestId("split-ring")).toBeDisabled();
  await panel.getByTestId("split-amount-1").fill("0.52");
  await expect(page.getByTestId("split-ring")).toBeEnabled();
});

test("a short typed cash amount rides the ring and the server refuses it: nothing commits", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await openStep(page, "payment");
  // $1.00 against a line that will ring at $4.52: the till computes no
  // total, so it cannot judge the entry short, and the typed amount rides the
  // ring for the server to judge.
  await page
    .getByLabel("Cash tendered in dollars: optional; leave empty to ring the exact total")
    .fill("1.00");
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  const answer = await ringPost;
  expect(answer.status()).toBe(400);
  // The refusal is the server's own words, and the ticket stands: no
  // confirmation exists, the line is still on the Items step, and the sale
  // can be corrected.
  await expect(page.getByRole("alert")).toContainText(
    "the cash tendered does not cover the sale's total",
  );
  await expect(page.getByTestId("sold-panel")).toHaveCount(0);
  await openStep(page, "items");
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
});

test("a split the server refuses reads its split_total_mismatch in dollars: nothing commits", async ({
  page,
}) => {
  // The preview is made to state $4.00 for a sale the ring will total at $4.52, so the legs the
  // panel arms ($3.00 and $1.00) meet the preview and miss the ring: the server's own refusal.
  await page.route("**/api/v1/pos/sale-preview", async (route) => {
    const real = await route.fetch();
    const body = (await real.json()) as Record<string, unknown>;
    await route.fulfill({
      response: real,
      json: { ...body, subtotal_cents: 400, tax_cents: 0, tax_lines: [], total_cents: 400 },
    });
  });
  await signIn(page, "Sales");
  await ensureSession(page);
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Split" }).click();
  const panel = page.getByTestId("pos-split-panel");
  await expect(panel.getByTestId("split-total")).toHaveText("Total to split $4.00");
  await panel.getByTestId("split-amount-0").fill("3.00");
  await expect(panel.getByTestId("split-amount-1")).toHaveValue("1.00");
  await expect(page.getByTestId("split-ring")).toBeEnabled();
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("split-ring").click();
  const answer = await ringPost;
  expect(answer.status()).toBe(422);
  await expect(page.getByRole("alert")).toContainText("The tenders add up to $4.00, but the sale total is $4.52.");
  await expect(page.getByRole("alert")).not.toContainText("cents");
  // Nothing committed, and the panel stays open for the correction.
  await expect(page.getByTestId("sold-panel")).toHaveCount(0);
  await expect(page.getByTestId("split-ring")).toBeEnabled();
});

test("an empty draft shows the blank marker, never a confident zero, in the linear flow", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await openStep(page, "payment");
  const totals = page.getByTestId("pos-totals-card");
  // Nothing on the ticket: the rows say they cannot say, and no plausible
  // zero appears anywhere on the payment step. The tax-and-total row names
  // the ring; there is no Total row to carry an estimate.
  await expect(totals).toContainText("(not priced)");
  await expect(totals).not.toContainText("$0.00");
  await expect(totals.getByTestId("pos-ring-words")).toContainText("added at the ring");
  const cta = page.getByTestId("pos-complete-sale");
  await expect(cta).toContainText("(not priced)");
  await expect(cta).not.toContainText("$0.00");
  // A priced line returns the figures.
  await openStep(page, "items");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await openStep(page, "payment");
  await expect(totals).toContainText("$4.00");
  await expect(totals).not.toContainText("(not priced)");
});

test("an empty draft shows the blank marker in the detached layout too", async ({ page }) => {
  await signIn(page, "Sales");
  await page.getByTestId("pos-view").click();
  await page.getByTestId("pos-layout-panels").click();
  await expect(page.getByTestId("pos-flow-panels")).toBeVisible();
  // The two-pane view holds Payment as a step in the steps pane (it is no longer a panel on
  // screen from the first render), so the Payment chip opens it; then the same honest marker,
  // never a zero.
  await page.locator("hos-pos-flow-steps").first().getByRole("button", { name: /Payment/ }).click();
  const totals = page.getByTestId("pos-totals-card");
  await expect(totals).toContainText("(not priced)");
  await expect(totals).not.toContainText("$0.00");
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
  // An empty ticket's split choice says so in the band's own words: the panel
  // itself does not mount with nothing to settle (the positions gate repair:
  // no new bound position carries the cart's emptiness into the panel, so the
  // terminal's band owns the empty words).
  await page.getByRole("button", { name: "Split" }).click();
  await expect(page.getByTestId("cta-split-why")).toContainText("Add a line before splitting the tender.");
  await expect(page.getByTestId("split-ring")).toHaveCount(0);
});

test("the split panel's ring wears the disabled rule's shape until the legs meet the shape rule", async ({
  page,
}) => {
  await signIn(page, "Sales");
  // The stub's own Charcoal line: this suite runs against the stub's world,
  // whose catalogue names it differently from the demo image's (the demo
  // image's DEMO-PAV-CHARCOAL name belongs to the acceptance run).
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await openStep(page, "payment");
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
  await field.fill("charcoal");
  const row = page.getByTestId("drop-row").filter({ hasText: "Charcoal Stone 60 mm" }).first();
  await expect(row).toBeVisible();
  // The reference's row words: the stock after the SKU, the counter's own
  // branch (the fence), never guessed.
  await expect(row).toContainText("HPV-60");
  await expect(row).toContainText("120 on hand");
});

test("the manager-up till keeps the margin column, the segments and the eye, and the eye's name and title say the same thing in both states", async ({
  page,
}) => {
  // The allowed side of the policy word (P3-10): MANAGER_UP admits the Sales
  // manager (tier 2), so the column renders its figures, the footer carries
  // the margin and profit segments, and the eye rides with them.
  await signIn(page, "Sales manager");
  await ensureSession(page);
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  const margins = page.getByTestId("pos-line-margin");
  await expect(margins).toHaveCount(1);
  await expect(margins.first()).toContainText("%");
  const managerTxbar = page.getByTestId("pos-txbar");
  await expect(managerTxbar).toContainText("Margin");
  await expect(managerTxbar).toContainText("Est. Profit");
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

// The row 1214 ruling moved the branch's Cash On Demand switch off the tile and into the
// server's own menu: with cod_enabled off, the core's preview drops the pay before release
// shape and the due later deposit (its own leg,
// TestSalePreview_PaymentOptions_CodOffDropsTheDueLaterDeposit, pins that answer) and the
// terminal draws what it is given. The stub's policy is fixed on, so this one page answers
// the preview with the core's cod-off menu, the same way the leg this replaces answered the
// policy read with cod_enabled false. The switch's on side is the offered shape itself,
// pinned where the option rings (test/paystep-pbr.pw.ts).
test("with Cash On Demand off at the branch, the server's menu drops the pay before release shape", async ({ page }) => {
  await page.route("**/api/v1/pos/sale-preview*", async (route) => {
    const real = await route.fetch();
    const body = (await real.json()) as Record<string, unknown>;
    await route.fulfill({
      response: real,
      json: { ...body, payment_options: [{ shape: "PAY_IN_FULL_NOW" }] },
    });
  });
  await signIn(page, "Sales");
  await ensureSession(page);
  await pickAccount(page, "C10", "Cash On Demand Landscaping");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await openStep(page, "payment");
  // Pay in full now is all the server offered, so no menu stands at all and the tender
  // tiles carry the step alone: the terminal never promises a shape the ring would refuse.
  await expect(page.getByTestId("pay-shapes")).toHaveCount(0);
  await expect(page.getByTestId("pay-shape-PAY_BEFORE_RELEASE")).toHaveCount(0);
  await expect(page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER")).toHaveCount(0);
  await expect(page.getByTestId("deposit-panel")).toHaveCount(0);
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
  await expect(ticket).not.toContainText("$");
});

test("the parked list shows a ticket's park-time subtotal", async ({ page }) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  // The row's figure is the park-time snapshot of the priced lines' subtotal
  // (a resumed ticket reprices through the engine), named as a subtotal and
  // carrying no estimate mark: there is no estimate on the sale surface to
  // mark. The rail lists the branch's tickets in insertion order: the ticket
  // this story just parked is the rail's last.
  const ticket = parkedDialog.locator(".ticket").last();
  await expect(ticket.locator(".mtot")).toHaveText("subtotal $4.00");
  await expect(ticket.locator(".meta")).toContainText("1 line");
});

test("a parked ticket this window holds no price memory for lists the same marker, never a bare line count", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
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
    await expect(tickets.nth(i)).not.toContainText("$");
  }
});

test("a pickup whose prices never answer reads the pending markers, never a figure", async ({ page }) => {
  // Every price the engine would answer is refused for this whole page, so the
  // pickup's lines stay price pending (a window with no price memory).
  await page.route("**/api/v1/pricing/resolve", async (route) => {
    await route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({ error: "line could not be priced by the engine" }),
    });
  });
  await signIn(page, "Sales manager");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await searchAndAdd(page, "polymeric", "Polymeric Locking Sand");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  // A fresh window picks the ticket up. The margin glance still answers (it
  // derives its figures from the server's own prices), and the policy word
  // allows the manager, so the column renders on this sign-in: the pickup's
  // markers-versus-figures rule is pinned where it can bite (P3-1).
  await signIn(page, "Sales manager");
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  // The rail lists the branch's tickets in insertion order, and the suite's
  // earlier stories leave theirs parked: the ticket this test just parked is
  // the rail's last.
  // The glance is debounced, so the pins below hold only once it has
  // actually answered: without the wait, they could pass in the
  // quiet window before the answer lands, which proves nothing.
  const glanceAnswered = page.waitForResponse((r) => r.url().endsWith("/api/v1/pos/margin-glance"));
  await parkedDialog.getByRole("button", { name: "Resume" }).last().click();
  await openStep(page, "items");
  const rows = page.getByTestId("pos-line-row");
  await expect(rows).toHaveCount(2);
  for (let index = 0; index < 2; index += 1) {
    await expect(rows.nth(index)).toContainText("(not priced)");
  }
  await glanceAnswered;
  // The answer has landed, and the column renders only once it has (and once
  // the policy word allows the caller), so the cells' arrival is the
  // determined wait past it: the same render paints the footer, so the
  // silence pinned below cannot slip a late render past a fixed beat (P3-3).
  const margins = page.getByTestId("pos-line-margin");
  await expect(margins).toHaveCount(2);
  for (let index = 0; index < 2; index += 1) {
    await expect(margins.nth(index)).toHaveText("(none)");
  }
  // The footer bar keeps the same rule (ui3d P2-2): the glance answers the
  // manager with a ticket margin and a profit figure derived from the
  // server's own prices, and while a line is pending neither segment may
  // state one.
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
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
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
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
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
  const row = page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" });
  await expect(row).toHaveCount(1);
  refusePrices = false;
  const qty = page.getByRole("textbox", { name: "Quantity: Charcoal Stone 60 mm" });
  await qty.fill("2");
  await qty.blur();
  await expect(row.locator(".ltot")).toHaveText("$8.00");
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
  // figure, and nothing states $0.00 for a line nobody has priced yet.
  const held: Route[] = [];
  await page.route("**/api/v1/pricing/resolve", async (route) => {
    held.push(route); // held in flight: answered only when the test releases it
  });
  await signIn(page, "Sales manager");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  await signIn(page, "Sales manager");
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  // The rail lists the branch's tickets in insertion order, and the suite's
  // earlier stories leave theirs parked: the ticket this test just parked is
  // the rail's last.
  await parkedDialog.getByRole("button", { name: "Resume" }).last().click();
  await expect(parkedDialog).not.toBeVisible();
  await openStep(page, "items");
  const row = page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" });
  await expect(row).toHaveCount(1);
  // Before any answer: the pending marker on the cell.
  await expect(row.locator(".ltot")).toHaveText("(not priced)");
  // The column renders only once the resume glance has actually answered and
  // the policy word allows this caller, so the cell's arrival is the
  // determined wait (P3-3): the footer's silence below is pinned past the
  // answer, never inside the quiet window ahead of it, and the render that
  // painted the cell painted the bar with it.
  await expect(page.getByTestId("pos-line-margin")).toHaveCount(1);
  const txbar = page.getByTestId("pos-txbar");
  await expect(txbar).toContainText("1 Item");
  await expect(txbar).not.toContainText("Margin");
  await expect(txbar).not.toContainText("Est. Profit");
  // The answer lands: the line's own price returns, and the footer's margin
  // and profit segments come back with it: the glance answers the manager and
  // the policy word allows this caller (P3-10), so the bar states the figures
  // exactly when the ticket is priced. The segments' arrival is itself the
  // determined wait: the pin retries until the priced render lands, so it
  // cannot pass inside the quiet window ahead of it.
  for (const route of held) await route.fallback().catch(() => undefined);
  await expect(row.locator(".ltot")).toHaveText("$4.00");
  await expect(txbar).toContainText("Margin");
  await expect(txbar).toContainText("Est. Profit");
});

test("a park landing while the parked dialog is open converges the open dialog's list", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");

  // The branch's parked count as the world holds it right now, read through
  // the contract's own read (the demo bearer and org header; the fence to the
  // caller's branch is the server's, so the query names no branch).
  const bearer = await salesBearer(page);
  const beforeRead = await page.request.get("/api/v1/pos/parked", {
    headers: { Authorization: `Bearer ${bearer}`, "X-Org": "hh-demo" },
  });
  expect(beforeRead.ok()).toBe(true);
  const before = ((await beforeRead.json()) as { items: unknown[] }).items.length;

  // Hold the park POST in flight: the park commits only when this story
  // releases it. A hand parks and opens the dialog in one motion, and the
  // button's click returns at dispatch, so the dialog's one read racing the
  // park commit is exactly what this hold makes deterministic.
  let releasePark: () => void = () => undefined;
  const parkGate = new Promise<void>((resolve) => {
    releasePark = resolve;
  });
  await page.route((url) => url.pathname === "/api/v1/pos/parked", async (route) => {
    if (route.request().method() === "POST") await parkGate;
    await route.continue();
  });

  const parkPost = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" && response.url().includes("/api/v1/pos/parked"),
  );
  await page.getByRole("button", { name: "Park ticket" }).click();
  // The dialog's first list read is the next parked-list GET in this window:
  // nothing else reads the list between the held park and the dialog opening.
  const firstRead = page.waitForResponse(
    (response) =>
      response.request().method() === "GET" && response.url().includes("/api/v1/pos/parked"),
  );
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  await firstRead;
  // Open while the park is in flight, the dialog lists the old world only.
  await expect(parkedDialog.locator(".ticket")).toHaveCount(before);

  releasePark();
  const created = (await (await parkPost).json()) as { ticket_no: string };

  // The list changed under the open dialog: the dialog converges with it, the
  // just-parked ticket shows in the open rail, and the button's count and the
  // dialog's rows agree on the same screen.
  await expect(parkedDialog.getByText(created.ticket_no)).toBeVisible({ timeout: 10_000 });
  await expect(
    page.getByRole("button", { name: new RegExp(`Parked \\(${before + 1}\\)`) }),
  ).toBeVisible();
});

test("a stale parked-list read that answers last writes nothing: the open dialog keeps the newest list", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");

  // Hold the park POST (the park commits only when released), and take the
  // dialog's first list read without answering it: its response is fetched
  // now, from the uncommitted world, and delivered last, after the newer
  // reads have answered. Two reads of the same dialog are then in flight at
  // once, and the stale one resolves last: whatever writes last wins unless
  // the dialog orders its own reads.
  let releasePark: () => void = () => undefined;
  const parkGate = new Promise<void>((resolve) => {
    releasePark = resolve;
  });
  const staleDelivery: Array<() => Promise<void>> = [];
  let heldFirstRead = false;
  await page.route((url) => url.pathname === "/api/v1/pos/parked", async (route) => {
    if (route.request().method() === "POST") {
      await parkGate;
      await route.continue();
      return;
    }
    if (route.request().method() === "GET" && !heldFirstRead) {
      heldFirstRead = true;
      const stale = await route.fetch(); // answered now, from the old world
      staleDelivery.push(async () => {
        await route.fulfill({ response: stale }); // delivered last
      });
      return;
    }
    await route.continue();
  });

  const parkPost = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" && response.url().includes("/api/v1/pos/parked"),
  );
  await page.getByRole("button", { name: "Park ticket" }).click();
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  expect(heldFirstRead).toBe(true);

  releasePark();
  const created = (await (await parkPost).json()) as { ticket_no: string };
  // The reads issued after the park answer with the committed world: the open
  // dialog converges on the just-parked ticket.
  await expect(parkedDialog.getByText(created.ticket_no)).toBeVisible({ timeout: 10_000 });

  // The stale read, answered before the park committed, lands last: the
  // newest list must still be the one on screen.
  for (const deliver of staleDelivery) await deliver();
  await expect(parkedDialog.getByText(created.ticket_no)).toBeVisible({ timeout: 10_000 });
});

test("a discard inside the open parked dialog moves the button's count while the dialog stays open", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");

  // The branch's parked count before this story parks anything, read through
  // the contract's own read.
  const bearer = await salesBearer(page);
  const beforeRead = await page.request.get("/api/v1/pos/parked", {
    headers: { Authorization: `Bearer ${bearer}`, "X-Org": "hh-demo" },
  });
  expect(beforeRead.ok()).toBe(true);
  const before = ((await beforeRead.json()) as { items: unknown[] }).items.length;

  const parkPost = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" && response.url().includes("/api/v1/pos/parked"),
  );
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();
  const created = (await (await parkPost).json()) as { ticket_no: string };

  // Discard this story's own ticket: the row leaves the open rail, the rail
  // stays open, and the button beside it must move with the list, not keep
  // naming a ticket the branch no longer holds.
  const row = parkedDialog.locator(".ticket").filter({ hasText: created.ticket_no });
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: "Discard" }).click();
  await expect(row).toHaveCount(0);
  await expect(parkedDialog).toBeVisible();
  await expect(
    page.getByRole("button", { name: new RegExp(`Parked \\(${before}\\)`) }),
  ).toBeVisible({ timeout: 10_000 });
});

test("a failed reload's error banner does not outlive the next good reload", async ({ page }) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  // Park once with the dialog closed, then put one line back on the counter:
  // the second park lands under the open dialog (F9 parks through the rail),
  // and the third needs its own line, scanned straight onto the counter while
  // the rail is up (the scan box adds on an exact sku's Enter, no pick click,
  // so nothing under the rail's scrim needs a click).
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  await searchAndAdd(page, "edge", "Edge Restraint 8 ft");

  // Refuse exactly the read the first park under the dialog triggers (the
  // host's convergence reload, the dialog's open read and the count read
  // answering before it); every later read answers.
  let reads = 0;
  await page.route((url) => url.pathname === "/api/v1/pos/parked", async (route) => {
    if (route.request().method() === "GET") {
      reads += 1;
      if (reads === 3) {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ error: "reload refused once" }),
        });
        return;
      }
    }
    await route.continue();
  });

  await page.getByRole("button", { name: /^Parked \(/ }).click();
  const parkedDialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(parkedDialog).toBeVisible();

  // The park lands, the host's reload read is refused: the banner is up.
  await page.keyboard.press("F9");
  const banner = parkedDialog.locator(".banner.bad");
  await expect(banner).toBeVisible({ timeout: 10_000 });
  await expect(banner).toContainText("parked 500");

  // Scan one more line onto the counter and park again under the open rail:
  // this reload answers. The banner the refused read raised must not outlive
  // the good one, and the rail stays open and honest underneath it.
  const field = page.getByTestId("pos-scan-input");
  await field.fill("EDG-08");
  await field.press("Enter");
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Edge Restraint 8 ft" })).toHaveCount(1);
  await page.keyboard.press("F9");
  await expect(banner).toBeHidden({ timeout: 10_000 });
  await expect(parkedDialog).toBeVisible();
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

test("the sold-actions buttons drop their fill while a receipt loads, in the colour-is-the-state rule", async ({
  page,
}) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  await openStep(page, "payment");
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  await (await ringPost).json();
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  // Hold the receipt read in flight so the loading state is a state, not a blink: while the
  // document fetch runs, both controls are disabled, and a disabled control drops its fill and
  // goes grey (colour is the state; fade is not a state, and a word change is not a state).
  await page.route("**/receipt*", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    await route.continue();
  });
  await sold.getByTestId("sold-print").click();
  const sibling = sold.getByTestId("sold-print-page");
  await expect(sibling).toBeDisabled();
  const sib = (await sibling.evaluate((el) => ({
    cursor: getComputedStyle(el).cursor,
    background: getComputedStyle(el).backgroundColor,
    opacity: getComputedStyle(el).opacity,
  }))) as { cursor: string; background: string; opacity: string };
  expect(sib.cursor).toBe("not-allowed");
  expect(sib.background).toBe("rgba(0, 0, 0, 0)");
  expect(Number(sib.opacity)).toBe(1);
  const primary = (await sold.getByTestId("sold-print").evaluate((el) => ({
    cursor: getComputedStyle(el).cursor,
    background: getComputedStyle(el).backgroundColor,
  }))) as { cursor: string; background: string };
  expect(primary.cursor).toBe("not-allowed");
  expect(primary.background).toBe("rgba(0, 0, 0, 0)");
  await page.unroute("**/receipt*");
});
