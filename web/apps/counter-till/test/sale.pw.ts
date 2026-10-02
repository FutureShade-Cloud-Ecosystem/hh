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
import { expect, test, type Page, type Response } from "@playwright/test";

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

/** Open one step of the linear flow by its head (a no-op when it is already open). */
async function openStep(page: Page, step: "customer" | "items" | "payment" | "summary"): Promise<void> {
  const head = page.getByTestId(`step-head-${step}`);
  if ((await head.getAttribute("aria-expanded")) !== "true") {
    await head.click();
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
  // The pick advances the flow to Items; the account's facts live on the
  // customer step's card, so open it again to read them.
  await openStep(page, "customer");
  await expect(page.getByTestId("customer-card")).toContainText(name);
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
  // before anything is rung.
  await openStep(page, "payment");
  await page.getByLabel("Cash tendered in dollars: optional; leave empty to ring the exact total").fill("80.00");
  await expect(page.getByTestId("cash-change-preview")).toContainText("Change due");

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
  await expect(holland.getByTestId("pos-line-margin")).toHaveText("15.8%");
  // The placeholder is real too: the edge restraint carries no cost at this
  // branch, so its margin slot renders the explicit unknown, never a zero.
  await searchAndAdd(page, "edge", "Edge Restraint 8 ft");
  const edge = page.getByTestId("pos-line-row").filter({ hasText: "Edge Restraint 8 ft" });
  await expect(edge.locator(".lprice")).toHaveText("CA$26.24");
  await expect(edge.getByTestId("pos-line-margin")).toHaveText(":");
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
  const sessionDot = page.getByTestId("session-dot");
  if ((await sessionDot.count()) === 0) {
    await page.getByRole("button", { name: "Open Session", exact: true }).click();
  }
  await expect(sessionDot).toContainText("Open");
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
  // pick back is the same gesture.
  await page.getByTestId("pos-view").click();
  await page.getByTestId("pos-layout-stepper").click();
  await expect(page.getByTestId("pos-flow-column")).toBeVisible();
});

test("the pop-out opens the till on a second window, signed in as the same role", async ({
  page,
  context,
}) => {
  await signIn(page, "Sales");
  // The second window rings against the SAME branch drawer, so make sure the
  // drawer is open before popping.
  const openerDot = page.getByTestId("session-dot");
  if ((await openerDot.count()) === 0) {
    await page.getByRole("button", { name: "Open Session", exact: true }).click();
  }
  await expect(openerDot).toContainText("Open");
  const popped = context.waitForEvent("page");
  await page.getByRole("button", { name: "Pop out" }).click();
  const second = await popped;
  await second.waitForLoadState();
  // The second window signs itself in through the same public cards route the
  // entry page taps, and lands on the terminal, against the same branch.
  await expect(second.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  await expect(second.getByTestId("signed-in-role")).toHaveText("Sales");
  await expect(second.getByTestId("session-dot")).toContainText("Open");
  await second.close();
});
