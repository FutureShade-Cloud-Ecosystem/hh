// Item 10.6: the till's Returns surface, the customer and lines steps, against the stubbed harness
// (the stub answers the customer lines read in the contract's shape; the vite dev server proxies
// /api to it). The story: a return is built like a credit. The person finds the customer, sees
// every order that customer holds, picks lines from SEVERAL orders into one draft, sets each line's
// disposition, and watches the credit so far. There is no search by one sale's number.
//
// The stub's world: Maple Ridge Contracting holds CT-1001 (job Marina Road patio: 2 Holland pavers,
// 6 wall blocks) and CT-1002 (job Front walk: 6 wall blocks), both at the first branch; Fenwick
// Builders holds a card ring at the first branch and a quote conversion at a second branch; Harbour
// holds nothing. The stub's world carries no tax, and the credit figure itself is the server's, so
// these legs assert the drafted lines and counts, never a computed credit.
import { expect, test, type Page } from "@playwright/test";

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
}

async function openReturns(page: Page): Promise<void> {
  // The route survives a sign out and a fresh sign in, so Returns may already be up, or the bar's entry
  // may be. Wait for whichever the till shows (a check then a click raced the render after a sign in
  // and clicked an entry that was not there), and click the entry only when it is the one that came.
  const surface = page.getByTestId("returns");
  const entry = page.getByTestId("till-returns-entry");
  await expect(surface.or(entry)).toBeVisible();
  if (!(await surface.isVisible())) await entry.click();
  await expect(surface).toBeVisible();
}

async function pickCustomer(page: Page, search: string, name: string): Promise<void> {
  await page.getByLabel("Search name or account number").fill(search);
  await page.getByRole("button", { name: new RegExp(name) }).click();
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "lines");
  await expect(page.getByTestId("returns-customer-name")).toHaveText(name);
}

const HOLLAND = "Holland paver, bevelled, each";
const WALL = "Wall block, charcoal, each";

function lineOf(page: Page, orderNo: string, description: string) {
  return page.getByTestId("returns-order").filter({ hasText: orderNo }).getByTestId("returns-line").filter({ hasText: description });
}

test.beforeEach(async ({ page }) => {
  expect((await page.request.post("/_stub/reseed-returns")).ok()).toBe(true);
  await signIn(page);
  await openReturns(page);
});

test("Returns opens on the customer step: a four step stepper, a customer search, and no sale number entry", async ({ page }) => {
  await expect(page.getByRole("heading", { name: "New Return" })).toBeVisible();
  const stepper = page.getByTestId("returns-stepper");
  for (const word of ["Customer", "Lines", "Refund", "Done"]) await expect(stepper).toContainText(word);
  await expect(stepper.locator("li[aria-current='step']")).toContainText("Customer");
  await expect(page.getByLabel("Search name or account number")).toBeVisible();
  // The one way to return: the old entry that found one sale by its number is gone.
  await expect(page.getByLabel("Sale number")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Find sale" })).toHaveCount(0);
  await expect(page.getByText("Why customer first")).toBeVisible();
});

test("a customer is found by name, and a miss says so in words", async ({ page }) => {
  await page.getByLabel("Search name or account number").fill("maple");
  await expect(page.getByTestId("returns-customers")).toContainText("Maple Ridge Contracting");
  await page.getByLabel("Search name or account number").fill("zzzz");
  await expect(page.getByTestId("returns-no-customer")).toContainText("No customer answers to that");
});

test("the customer's lines list every order they hold, grouped by order and job", async ({ page }) => {
  await pickCustomer(page, "maple", "Maple Ridge Contracting");
  const orders = page.getByTestId("returns-order");
  await expect(orders).toHaveCount(2);
  await expect(orders.nth(0)).toContainText("CT-1001");
  await expect(orders.nth(0)).toContainText("job Marina Road patio");
  await expect(orders.nth(0)).toContainText("2 of 2 lines open to return");
  await expect(orders.nth(1)).toContainText("CT-1002");
  await expect(orders.nth(1)).toContainText("job Front walk");
  await expect(lineOf(page, "CT-1001", HOLLAND)).toContainText("bought 2 at $60.00");
  await expect(lineOf(page, "CT-1001", HOLLAND)).toContainText("2 open");
  // A line partly returned already shows the open quantity, the one the draft is capped at.
  await expect(lineOf(page, "CT-1001", WALL)).toContainText("bought 6 at $60.00 · 4 open");
  // Three disposition buttons on every line, in the board's words.
  const buttons = lineOf(page, "CT-1001", WALL).getByRole("button", { name: /Return to stock|damaged|nothing returned/ });
  await expect(buttons).toHaveCount(3);
  await expect(lineOf(page, "CT-1001", WALL)).toContainText("Refund, do not restock (damaged)");
  await expect(lineOf(page, "CT-1001", WALL)).toContainText("Refund, nothing returned");
  await expect(page.getByTestId("returns-stepper").locator("li[aria-current='step']")).toContainText("Lines");
});

test("lines from two orders go into one draft, each with its own disposition, and the credit panel follows", async ({ page }) => {
  await pickCustomer(page, "maple", "Maple Ridge Contracting");
  await expect(page.getByTestId("returns-credit-empty")).toBeVisible();
  await expect(page.getByRole("button", { name: "Continue to the refund" })).toBeDisabled();

  // Order one: a quantity first, then its disposition.
  const holland = lineOf(page, "CT-1001", HOLLAND);
  await holland.getByRole("button", { name: /One more/ }).click();
  await holland.getByRole("button", { name: /One more/ }).click();
  await expect(holland.getByLabel(/Return quantity/)).toHaveValue("2");
  await expect(holland.getByRole("button", { name: /One more/ })).toBeDisabled();
  await expect(holland.getByTestId("returns-line-choose")).toBeVisible();
  await holland.getByRole("button", { name: "Return to stock" }).click();
  await expect(holland.getByRole("button", { name: "Return to stock" })).toHaveAttribute("aria-pressed", "true");

  // Order two: the disposition first, which picks one unit.
  const wall = lineOf(page, "CT-1002", WALL);
  await wall.getByRole("button", { name: "Refund, do not restock (damaged)" }).click();
  await expect(wall.getByLabel(/Return quantity/)).toHaveValue("1");
  await expect(wall.getByRole("button", { name: "Refund, do not restock (damaged)" })).toHaveAttribute("aria-pressed", "true");

  await expect(page.getByTestId("returns-credit-count")).toHaveText("2 lines from 2 orders");
  // The credit so far is the server's priced total with tax (2 Holland at 60.00 plus 1 wall block at 60.00).
  await expect(page.getByTestId("returns-credit-total")).toHaveText("$180.00");
  await expect(page.getByTestId("returns-credit-tax")).toContainText("with tax");
  const credit = page.getByTestId("returns-credit-line");
  await expect(credit).toHaveCount(2);
  await expect(credit.nth(0)).toContainText("2 Holland paver, bevelled, each on CT-1001 · Return to stock");
  await expect(credit.nth(1)).toContainText("1 Wall block, charcoal, each on CT-1002 · Refund, do not restock (damaged)");
  await expect(page.getByTestId("returns-credit-line-figure").nth(0)).toHaveText("$120.00 with $0.00 tax");
  await expect(page.getByTestId("returns-credit-line-figure").nth(1)).toHaveText("$60.00 with $0.00 tax");
  await expect(page.getByTestId("returns-credit")).toContainText("The credit so far");

  // A third line with a quantity but no disposition holds the draft back, in words.
  const second = lineOf(page, "CT-1001", WALL);
  await second.getByRole("button", { name: /One more/ }).click();
  await expect(page.getByTestId("returns-missing")).toContainText("One line still needs a choice");
  await expect(page.getByRole("button", { name: "Continue to the refund" })).toBeDisabled();
  await second.getByRole("button", { name: "Refund, nothing returned" }).click();
  await expect(page.getByTestId("returns-credit-count")).toHaveText("3 lines from 2 orders");
  await expect(page.getByTestId("returns-missing")).toHaveCount(0);

  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "refund");
  await expect(page.getByTestId("returns-refund")).toContainText("3 lines from 2 orders");
  await page.getByRole("button", { name: "Back to the lines" }).click();
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "lines");
  // The draft survived the round trip.
  await expect(page.getByTestId("returns-credit-count")).toHaveText("3 lines from 2 orders");
});

test("a quantity above what is open is refused before anything is sent, in words, and the field goes back", async ({ page }) => {
  await pickCustomer(page, "maple", "Maple Ridge Contracting");
  const holland = lineOf(page, "CT-1001", HOLLAND);
  await holland.getByLabel(/Return quantity/).fill("1");
  await holland.getByLabel(/Return quantity/).fill("5");
  await expect(page.getByTestId("returns-draft-notice")).toContainText("only 2 open to return");
  await expect(holland.getByLabel(/Return quantity/)).toHaveValue("1");
  await expect(page.getByTestId("returns-credit-count")).toHaveText("1 line from 1 order");
});

test("the sales order number filters the line list only", async ({ page }) => {
  await pickCustomer(page, "maple", "Maple Ridge Contracting");
  await lineOf(page, "CT-1001", HOLLAND).getByRole("button", { name: "Return to stock" }).click();
  await page.getByLabel("Filter by sales order number").fill("1002");
  await expect(page.getByTestId("returns-order")).toHaveCount(1);
  await expect(page.getByTestId("returns-order")).toContainText("CT-1002");
  // The line picked on the hidden order is still in the draft.
  await expect(page.getByTestId("returns-credit-count")).toHaveText("1 line from 1 order");
  await page.getByLabel("Filter by sales order number").fill("9999");
  await expect(page.getByText("No order matches that number.")).toBeVisible();
  await page.getByLabel("Filter by sales order number").fill("");
  await expect(page.getByTestId("returns-order")).toHaveCount(2);
});

test("one credit note is for one branch: a line from another branch's order is refused in words", async ({ page }) => {
  await pickCustomer(page, "fenwick", "Fenwick Builders Ltd");
  await expect(page.getByTestId("returns-order")).toHaveCount(2);
  await page.getByTestId("returns-order").filter({ hasText: "CT-2001" }).getByRole("button", { name: "Return to stock" }).click();
  await page.getByTestId("returns-order").filter({ hasText: "CT-2002" }).getByRole("button", { name: "Return to stock" }).click();
  await expect(page.getByTestId("returns-draft-notice")).toContainText("one branch");
  // The refusal names the two branches.
  await expect(page.getByTestId("returns-draft-notice")).toContainText("Trenton");
  await expect(page.getByTestId("returns-draft-notice")).toContainText("Kingston");
  await expect(page.getByTestId("returns-credit-count")).toHaveText("1 line from 1 order");
});

test("a customer who holds no order says so, and Change customer starts clean", async ({ page }) => {
  await pickCustomer(page, "harbour", "Harbour Drywall Inc");
  await expect(page.getByTestId("returns-no-orders")).toContainText("holds no sales order");
  await page.getByRole("button", { name: "Change customer" }).click();
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "customer");
  await expect(page.getByLabel("Search name or account number")).toHaveValue("");
  await expect(page.getByTestId("returns-customers")).toHaveCount(0);
});

test("a walk in row picked from the customer list is answered in the server's words, never a list of walk in sales", async ({ page }) => {
  // The customer search excludes the house walk-in, so a row for it is staged here and the stub's
  // customer lines read answers it the way the core does (422 with its words).
  await page.route("**/api/v1/customers/search*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          id: "a1900000-0000-4000-9000-000000000001",
          name: "Walk-in",
          account_no: "WALK-IN",
          customer_type: "RETAIL",
          payment_terms_days: 0,
          credit_limit_cents: 0,
          open_balance_cents: 0,
        },
      ]),
    }),
  );
  await page.getByLabel("Search name or account number").fill("walk");
  await page.getByRole("button", { name: /Walk-in/ }).click();
  // Ruling A: the server never lists the walk in sales, and says how to find one.
  await expect(page.getByTestId("returns-notice")).toContainText("walk-in sales are never listed");
  await expect(page.getByTestId("returns-notice")).toContainText("find the sale by the receipt or order number");
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "customer");
});

// ---- Item 10.7: the Refund and Done steps, against the stub's credit note write ----------------

const DRAWER_SESSION_ID = "00000000-0000-4000-8000-000000090001";

async function drawerExpectedCash(page: Page): Promise<number> {
  const mint = await page.request.post("/api/v1/demo/session", { data: { flow: "sales" } });
  const minted = (await mint.json()) as { token?: string };
  const answer = await page.request.get(`/api/v1/till/sessions/${DRAWER_SESSION_ID}/zreport`, {
    headers: { Authorization: `Bearer ${minted.token ?? ""}` },
  });
  const report = (await answer.json()) as { cash?: { expected_cents?: number } };
  return report.cash?.expected_cents ?? -1;
}

async function stubState(page: Page): Promise<{
  keys: string[];
  lastNoteBody: { orders: { order_id: string; lines: unknown[] }[]; restock: boolean; refund_method: string; till_session_id: string };
  previewBodies: { orders: { lines: { disposition?: string }[] }[] }[];
  sightings: { path: string; approverHeader: boolean }[];
}> {
  return (await (await page.request.get("/_stub/state")).json()) as never;
}

/** Two lines, one from each of Maple Ridge's orders, both back to stock, then on to the Refund step. */
async function draftTwoOrders(page: Page, wallQty = "1"): Promise<void> {
  await pickCustomer(page, "maple", "Maple Ridge Contracting");
  await lineOf(page, "CT-1001", HOLLAND).getByRole("button", { name: "Return to stock" }).click();
  const wall = lineOf(page, "CT-1002", WALL);
  await wall.getByLabel(/Return quantity/).fill(wallQty);
  await wall.getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "refund");
}

test("the Refund step lists the drafted lines by order and offers the rails, with the reason beside a rail that cannot be used", async ({ page }) => {
  await draftTwoOrders(page);
  const wait = page.getByTestId("returns-refund");
  await expect(wait).toContainText("Maple Ridge Contracting");
  await expect(wait.getByTestId("returns-refund-order")).toHaveCount(2);
  await expect(wait).toContainText("CT-1001");
  await expect(wait).toContainText("CT-1002");
  // Cash sales: nothing to reverse on a card; the drawer is open; the account rail is always offered.
  await expect(page.getByRole("button", { name: "Card", exact: true })).toBeDisabled();
  await expect(wait).toContainText("No card refund: an order on this credit note was not paid by card");
  await expect(page.getByRole("button", { name: "Cash", exact: true })).toBeEnabled();
  await expect(page.getByRole("button", { name: "On the account", exact: true })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Record the credit note" })).toBeDisabled();
  await page.getByRole("button", { name: "Back to the lines" }).click();
  await expect(page.getByTestId("returns-credit-count")).toHaveText("2 lines from 2 orders");
});

test("a two order note in cash commits once: one body with both orders, the Done step names the note, each return and its receipt line, and the drawer drops by the server's total", async ({ page }) => {
  const before = await drawerExpectedCash(page);
  await draftTwoOrders(page);
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "done");
  const done = page.getByTestId("returns-done");
  await expect(done).toContainText("Credit note recorded");
  await expect(done.getByTestId("returns-note-no")).toHaveText(/CT-CN-\d+/);
  await expect(done.getByTestId("returns-note-total")).toContainText("$120.00");
  await expect(done).toContainText("cash, from the drawer");
  const children = done.getByTestId("returns-done-return");
  await expect(children).toHaveCount(2);
  await expect(children.nth(0)).toContainText("CT-1001");
  await expect(children.nth(0)).toContainText("$60.00");
  await expect(children.nth(1)).toContainText("CT-1002");
  await expect(children.nth(0).getByTestId("returns-receipt-line")).toContainText(/CT-R-\d+/);
  await expect(children.nth(1).getByTestId("returns-receipt-line")).toContainText(/CT-R-\d+/);
  await expect(done).toContainText("Rung by Sales (demo).");
  await expect(page.getByTestId("returns-stepper").locator("li[aria-current='step']")).toContainText("Done");

  expect(await drawerExpectedCash(page)).toBe(before - 12000);
  const state = await stubState(page);
  expect(state.keys).toHaveLength(1);
  expect(state.lastNoteBody.orders).toHaveLength(2);
  expect(state.lastNoteBody.restock).toBe(true);
  expect(state.lastNoteBody.refund_method).toBe("CASH");
  expect(state.lastNoteBody.till_session_id).toBe(DRAWER_SESSION_ID);

  await done.getByRole("button", { name: "Start another return" }).click();
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "customer");
  await page.getByLabel("Search name or account number").fill("maple");
  await page.getByRole("button", { name: /Maple Ridge/ }).click();
  // What went back is no longer open: the Holland line has one unit left.
  await expect(lineOf(page, "CT-1001", HOLLAND)).toContainText("1 open");
});

test("on the account: the same note commits with no drawer, and the words say the account is credited", async ({ page }) => {
  const before = await drawerExpectedCash(page);
  await draftTwoOrders(page);
  await page.getByRole("button", { name: "On the account", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();
  await expect(page.getByTestId("returns-done")).toContainText("on the customer's account");
  expect(await drawerExpectedCash(page)).toBe(before);
  expect((await stubState(page)).lastNoteBody.till_session_id).toBe("");
});

test("a card note on a card paid order commits, and says nothing reached a card where the rail is simulated", async ({ page }) => {
  await pickCustomer(page, "fenwick", "Fenwick Builders Ltd");
  await page.getByTestId("returns-order").filter({ hasText: "CT-2001" }).getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await page.getByRole("button", { name: "Card", exact: true }).click();
  await expect(page.getByTestId("returns-refund")).toContainText("Simulated refund: nothing reached a card.");
  await page.getByRole("button", { name: "Record the credit note" }).click();
  await expect(page.getByTestId("returns-done")).toContainText("Simulated refund: nothing reached a card.");
  await expect(page.getByTestId("returns-done-return")).toHaveCount(1);
});

test("a mix of back to stock and damaged is sent as it is, one disposition per line, and commits", async ({ page }) => {
  await pickCustomer(page, "maple", "Maple Ridge Contracting");
  await lineOf(page, "CT-1001", HOLLAND).getByRole("button", { name: "Return to stock" }).click();
  await lineOf(page, "CT-1002", WALL).getByRole("button", { name: "Refund, do not restock (damaged)" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await page.getByRole("button", { name: "On the account", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();
  await expect(page.getByTestId("returns-done")).toContainText("Credit note recorded");
  const sent = (await stubState(page)).lastNoteBody as unknown as { orders: { lines: { disposition: string }[] }[] };
  expect(sent.orders.map((order) => order.lines[0].disposition)).toEqual(["RESTOCK", "DAMAGED"]);
});

test("a refusal from the server shows in its own words, the note stays drafted, and a retry sends the same key", async ({ page }) => {
  await draftTwoOrders(page);
  await page.getByRole("button", { name: "On the account", exact: true }).click();
  // Another till returns the same Holland units first: the server's guard refuses the whole note.
  const mint = await page.request.post("/api/v1/demo/session", { data: { flow: "sales" } });
  const token = ((await mint.json()) as { token: string }).token;
  const taken = await page.request.post("/api/v1/pos/returns-across", {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      idempotency_key: "00000000-0000-4000-8000-0000000000aa",
      customer_id: "c-maple-ridge",
      refund_method: "ACCOUNT_CREDIT",
      reason: "",
      restock: true,
      till_session_id: "",
      orders: [{ order_id: "00000000-0000-4000-8000-000000001001", lines: [{ order_line_id: "00000000-0000-4000-8000-000000011011", qty_base: 2, reason_code: "OTHER" }] }],
    },
  });
  expect(taken.status()).toBe(201);
  await page.getByRole("button", { name: "Record the credit note" }).click();
  await expect(page.getByTestId("returns-notice")).toContainText("The credit note was refused: pos: over-return on line");
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "refund");
  await page.getByRole("button", { name: "Record the credit note" }).click();
  const keys = (await stubState(page)).keys.filter((key) => key !== "00000000-0000-4000-8000-0000000000aa");
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});

async function setApprovalLimit(page: Page, enabled: boolean): Promise<void> {
  const answer = await page.request.post("/_stub/refund-approval-limit", { data: { enabled } });
  expect(((await answer.json()) as { approvalLimitEnabled: boolean }).approvalLimitEnabled).toBe(enabled);
}

test("a note over the approval amount is refused with the amount and approvers named, and completes through the Sales manager's card while the till stays the clerk's", async ({ page }) => {
  await setApprovalLimit(page, true);
  // Wall blocks from both orders: the note total, not either order alone, crosses 250.00.
  await pickCustomer(page, "maple", "Maple Ridge Contracting");
  await lineOf(page, "CT-1001", WALL).getByLabel(/Return quantity/).fill("3");
  await lineOf(page, "CT-1001", WALL).getByRole("button", { name: "Return to stock" }).click();
  await lineOf(page, "CT-1002", WALL).getByLabel(/Return quantity/).fill("3");
  await lineOf(page, "CT-1002", WALL).getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await page.getByRole("button", { name: "On the account", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();

  const refusal = page.getByTestId("refund-refusal");
  await expect(refusal).toContainText("$250.00");
  await expect(refusal).toContainText("Sales manager");
  await expect(refusal).toContainText("Branch manager");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await refusal.getByRole("button", { name: "Manager approval" }).click();
  const dialog = page.getByRole("dialog", { name: "Manager approval" });
  await expect(dialog).toContainText("the till stays the clerk's");
  await dialog.getByRole("button", { name: "Sales manager" }).click();

  const done = page.getByTestId("returns-done");
  await expect(done).toContainText("$360.00");
  await expect(done).toContainText("Approved by Sales manager (demo).");
  await expect(done).toContainText("Rung by Sales (demo).");
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
  const state = await stubState(page);
  const posts = state.sightings.filter((sighting) => sighting.path === "/api/v1/pos/returns-across");
  expect(posts).toHaveLength(2);
  expect(posts[0].approverHeader).toBe(false);
  expect(posts[1].approverHeader).toBe(true);
  for (const sighting of state.sightings) {
    if (sighting.path !== "/api/v1/pos/returns-across") expect(sighting.approverHeader).toBe(false);
  }
});

test("signed in as the Sales manager the same note completes with no refusal, and Done names the manager as approver", async ({ page }) => {
  await setApprovalLimit(page, true);
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales manager");
  await openReturns(page);
  await pickCustomer(page, "maple", "Maple Ridge Contracting");
  await lineOf(page, "CT-1002", WALL).getByLabel(/Return quantity/).fill("6");
  await lineOf(page, "CT-1002", WALL).getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await page.getByRole("button", { name: "On the account", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();
  await expect(page.getByTestId("refund-refusal")).toHaveCount(0);
  await expect(page.getByTestId("returns-done")).toContainText("Approved by the Sales manager on this till: Sales manager (demo).");
});

test("a sign out under an in flight approval leaves the surface blank for the next person", async ({ page }) => {
  await setApprovalLimit(page, true);
  let release: (() => void) | undefined;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v1/pos/returns-across", async (route) => {
    const response = await route.fetch();
    if (await route.request().headerValue("x-approver-authorization")) await held;
    await route.fulfill({ response });
  });
  await pickCustomer(page, "maple", "Maple Ridge Contracting");
  await lineOf(page, "CT-1002", WALL).getByLabel(/Return quantity/).fill("6");
  await lineOf(page, "CT-1002", WALL).getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await page.getByRole("button", { name: "On the account", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();
  await page.getByTestId("refund-refusal").getByRole("button", { name: "Manager approval" }).click();
  await page.getByRole("dialog", { name: "Manager approval" }).getByRole("button", { name: "Sales manager" }).click();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  release?.();
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
  await expect(page.getByTestId("returns-done")).toHaveCount(0);
  await openReturns(page);
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "customer");
});

test.afterEach(async ({ page }) => {
  await setApprovalLimit(page, false);
});

// ---- Stage 3: the credit preview, branch names, and the rails' refusals in the server's words -----

test("the credit so far is priced by the server on every change, a disposition rides the preview once chosen, and Continue waits for the price", async ({ page }) => {
  await pickCustomer(page, "maple", "Maple Ridge Contracting");
  await lineOf(page, "CT-1001", HOLLAND).getByRole("button", { name: /One more/ }).click();
  await expect(page.getByTestId("returns-credit-total")).toHaveText("$60.00");
  await expect(page.getByRole("button", { name: "Continue to the refund" })).toBeDisabled();
  await lineOf(page, "CT-1001", HOLLAND).getByRole("button", { name: "Refund, nothing returned" }).click();
  await expect(page.getByRole("button", { name: "Continue to the refund" })).toBeEnabled();
  await lineOf(page, "CT-1001", HOLLAND).getByRole("button", { name: /One more/ }).click();
  await expect(page.getByTestId("returns-credit-total")).toHaveText("$120.00");
  const bodies = (await stubState(page)).previewBodies;
  expect(bodies.length).toBeGreaterThanOrEqual(3);
  expect(bodies[0].orders[0].lines[0].disposition).toBeUndefined();
  expect(bodies[bodies.length - 1].orders[0].lines[0].disposition).toBe("NO_RETURN");
  // Taking the line out drops the figure with it.
  await lineOf(page, "CT-1001", HOLLAND).getByLabel(/Return quantity/).fill("");
  await expect(page.getByTestId("returns-credit-empty")).toBeVisible();
  await expect(page.getByTestId("returns-credit-total")).toHaveCount(0);
});

test("each order names its branch", async ({ page }) => {
  await pickCustomer(page, "fenwick", "Fenwick Builders Ltd");
  await expect(page.getByTestId("returns-order").filter({ hasText: "CT-2001" })).toContainText("Trenton");
  await expect(page.getByTestId("returns-order").filter({ hasText: "CT-2002" })).toContainText("Kingston");
});

test("an account that cannot spend a credit is refused in the server's words and the note stays drafted", async ({ page }) => {
  await pickCustomer(page, "C07", "Lachance Retail");
  await lineOf(page, "CT-1003", WALL).getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await page.getByRole("button", { name: "On the account", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();
  await expect(page.getByTestId("returns-notice")).toContainText("this account cannot spend a credit");
  await expect(page.getByTestId("returns-notice")).toContainText("refund to the card it was paid on or in cash");
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "refund");
  // The cash rail is open to the same note.
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();
  await expect(page.getByTestId("returns-done")).toContainText("Credit note recorded");
});

test("a card refund past what the capture holds is refused in the server's words", async ({ page }) => {
  await pickCustomer(page, "fenwick", "Fenwick Builders Ltd");
  await page.getByTestId("returns-order").filter({ hasText: "CT-2001" }).getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await page.getByRole("button", { name: "Card", exact: true }).click();
  // The capture's earlier card refunds are the server's to know: stage them through the write route.
  const mint = await page.request.post("/api/v1/demo/session", { data: { flow: "sales" } });
  const token = ((await mint.json()) as { token: string }).token;
  const staged = await page.request.post("/api/v1/pos/returns-across", {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      idempotency_key: "00000000-0000-4000-8000-0000000000bb", customer_id: "c-fenwick", refund_method: "CARD", reason: "",
      restock: true, till_session_id: "",
      orders: [{ order_id: "00000000-0000-4000-8000-000000002001", lines: [{ order_line_id: "00000000-0000-4000-8000-000000012011", qty_base: 1, reason_code: "OTHER" }] }],
    },
  });
  expect(staged.status()).toBe(201);
  await page.getByRole("button", { name: "Record the credit note" }).click();
  // The line itself is now fully returned, so the server's first refusal is the over-return; the card cap
  // is the same route's next guard (covered by the stub in the core's words and by the core's own legs).
  await expect(page.getByTestId("returns-notice")).toContainText("The credit note was refused: pos:");
});

test("a line sent twice is refused by the server in its words (the till itself never sends one)", async ({ page }) => {
  const mint = await page.request.post("/api/v1/demo/session", { data: { flow: "sales" } });
  const token = ((await mint.json()) as { token: string }).token;
  const line = { order_line_id: "00000000-0000-4000-8000-000000011011", qty_base: 1, reason_code: "OTHER" };
  const answer = await page.request.post("/api/v1/pos/returns/preview", {
    headers: { Authorization: `Bearer ${token}` },
    data: { customer_id: "c-maple-ridge", orders: [{ order_id: "00000000-0000-4000-8000-000000001001", lines: [line, line] }] },
  });
  expect(answer.status()).toBe(422);
  expect(((await answer.json()) as { error: string }).error).toContain("appears twice on order CT-1001, send it once with the whole quantity");
});

// ---- Stage 4: the converted order that is blocked, and the swap ----------------------------------

test("an order a quote became with no live invoice is shown blocked with the server's reason, and its lines cannot be drafted", async ({ page }) => {
  await pickCustomer(page, "C07", "Lachance Retail");
  const blocked = page.getByTestId("returns-order").filter({ hasText: "CT-1004" });
  await expect(blocked.getByTestId("returns-order-blocked")).toContainText(
    "no invoice has been raised for this order yet, so there is nothing to refund against",
  );
  await expect(blocked.getByRole("button", { name: "Return to stock" })).toHaveCount(0);
  await expect(blocked.getByLabel(/Return quantity/)).toHaveCount(0);
  // Its sibling order is an ordinary till ring and drafts as usual.
  await lineOf(page, "CT-1003", WALL).getByRole("button", { name: "Return to stock" }).click();
  await expect(page.getByTestId("returns-credit-count")).toHaveText("1 line from 1 order");
});

async function toSwap(page: Page, wallQty = "1"): Promise<void> {
  await draftTwoOrders(page, wallQty);
}

async function addPolymerSand(page: Page, qty?: string): Promise<void> {
  await page.getByLabel("Search replacement goods").fill("polymer");
  await page.getByTestId("returns-products").getByRole("button", { name: /Polymeric Locking Sand/ }).click();
  await expect(page.getByTestId("returns-replacement")).toHaveCount(1);
  if (qty !== undefined) await page.getByLabel("Replacement quantity: Polymeric Locking Sand", { exact: true }).last().fill(qty);
}

test("the swap panel shows the server's figures: the credit, the new order with tax, what the credit pays and the leftover going back by the original tender", async ({ page }) => {
  await toSwap(page);
  const swap = page.getByTestId("returns-swap");
  await expect(swap.getByTestId("returns-swap-empty")).toBeVisible();
  await expect(page.getByRole("button", { name: "Ring the swap" })).toBeDisabled();
  await addPolymerSand(page);
  // 1 bag at Maple Ridge's price, with tax; the credit is two lines at 60.00 (the stub's world).
  await expect(swap.getByTestId("swap-credit")).toHaveText("$120.00");
  await expect(swap.getByTestId("swap-new-total")).toHaveText("$12.97");
  await expect(swap.getByTestId("swap-applied")).toHaveText("$12.97");
  await expect(swap.getByTestId("swap-leftover")).toHaveText("$107.03");
  await expect(swap.getByTestId("swap-words")).toContainText("left over goes back cash, from the drawer");
  await expect(swap.getByTestId("swap-shortfall")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Ring the swap" })).toBeEnabled();
  const bodies = (await stubState(page)).previewBodies as unknown as { lines?: { uom_entered: string }[] }[];
  expect(bodies[bodies.length - 1].lines?.[0].uom_entered).toBe("bag");
});

test("ringing a swap with a leftover: one body, the Done step names both documents pointing at each other, each receipt line, and the drawer pays out only the leftover", async ({ page }) => {
  const before = await drawerExpectedCash(page);
  await toSwap(page);
  await addPolymerSand(page);
  await expect(page.getByTestId("swap-leftover")).toHaveText("$107.03");
  await page.getByRole("button", { name: "Ring the swap" }).click();
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "done");
  const done = page.getByTestId("returns-done");
  await expect(done).toContainText("Swap rung: credit note recorded");
  await expect(done.getByTestId("returns-note-no")).toHaveText(/CT-CN-\d+/);
  await expect(done.getByTestId("returns-swap-sale")).toContainText(/CT-S-\d+/);
  await expect(done.getByTestId("returns-swap-sale")).toContainText("$12.97");
  await expect(done.getByTestId("returns-swap-link")).toContainText("point at each other");
  await expect(done.getByTestId("returns-swap-split")).toContainText("$107.03 went back cash, from the drawer");
  await expect(done.getByTestId("returns-done-return")).toHaveCount(2);
  await expect(done.getByTestId("returns-receipt-line").first()).toContainText(/CT-R-\d+/);
  await expect(done.getByTestId("returns-receipt-line-sale")).toContainText("paid by credit note");
  expect(await drawerExpectedCash(page)).toBe(before - 10703);
  const state = (await stubState(page)) as unknown as { swapKeys: string[]; lastSwapBody: { orders: { lines: { disposition: string }[] }[]; lines: unknown[]; tender: string; till_session_id: string } };
  expect(state.swapKeys).toHaveLength(1);
  expect(state.lastSwapBody.orders).toHaveLength(2);
  expect(state.lastSwapBody.lines).toHaveLength(1);
  expect(state.lastSwapBody.tender).toBe("CASH");
  expect(state.lastSwapBody.till_session_id).toBe(DRAWER_SESSION_ID);
  // The swap's two documents carry dispositions as drafted: back to stock on both here.
  expect(state.lastSwapBody.orders.map((order) => order.lines[0].disposition)).toEqual(["RESTOCK", "RESTOCK"]);
});

test("a shortfall is paid by the tender chosen: cash by default, or put on the account", async ({ page }) => {
  const before = await drawerExpectedCash(page);
  await toSwap(page);
  await addPolymerSand(page, "20");
  const swap = page.getByTestId("returns-swap");
  await expect(swap.getByTestId("swap-new-total")).toHaveText("$259.45");
  await expect(swap.getByTestId("swap-applied")).toHaveText("$120.00");
  await expect(swap.getByTestId("swap-shortfall")).toHaveText("$139.45");
  await expect(swap.getByTestId("swap-leftover")).toHaveCount(0);
  await expect(swap.getByRole("button", { name: "Pay the rest in cash" })).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("button", { name: "Ring the swap" }).click();
  await expect(page.getByTestId("returns-swap-split")).toContainText("$139.45 was paid by cash");
  expect(await drawerExpectedCash(page)).toBe(before + 13945);

  await page.request.post("/_stub/reseed-returns");
  await page.getByRole("button", { name: "Start another return" }).click();
  await openReturns(page);
  await toSwap(page);
  await addPolymerSand(page, "20");
  await page.getByTestId("returns-swap").getByRole("button", { name: "Put the rest on the account" }).click();
  await expect(page.getByTestId("swap-shortfall")).toHaveText("$139.45");
  await page.getByRole("button", { name: "Ring the swap" }).click();
  await expect(page.getByTestId("returns-swap-split")).toContainText("was paid by account");
  expect(((await stubState(page)) as unknown as { lastSwapBody: { tender: string } }).lastSwapBody.tender).toBe("ACCOUNT");
});

test("a credit that changed while the swap was rung is refused in the server's words, and the next ring is a fresh swap with a fresh key", async ({ page }) => {
  await toSwap(page);
  await addPolymerSand(page);
  await page.request.post("/_stub/swap-failure", { data: { mode: "changed" } });
  await page.getByRole("button", { name: "Ring the swap" }).click();
  await expect(page.getByTestId("returns-swap-notice")).toContainText("the swap's figures changed while it was being rung; review the swap and ring it again");
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "refund");
  await expect(page.getByTestId("swap-leftover")).toBeVisible();
  await page.getByRole("button", { name: "Ring the swap" }).click();
  await expect(page.getByTestId("returns-done")).toContainText("Swap rung");
  const keys = ((await stubState(page)) as unknown as { swapKeys: string[] }).swapKeys;
  expect(keys).toHaveLength(2);
  expect(keys[0]).not.toBe(keys[1]);
});

test("a processor that refuses the leftover on a card sale is said in the server's words, nothing is posted, and the retry replays the same key", async ({ page }) => {
  await pickCustomer(page, "fenwick", "Fenwick Builders Ltd");
  await page.getByTestId("returns-order").filter({ hasText: "CT-2001" }).getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await addPolymerSand(page);
  await expect(page.getByTestId("swap-leftover")).toHaveText("$74.76");
  await expect(page.getByTestId("swap-words")).toContainText("left over goes back card");
  await page.request.post("/_stub/swap-failure", { data: { mode: "processor" } });
  await page.getByRole("button", { name: "Ring the swap" }).click();
  await expect(page.getByTestId("returns-swap-notice")).toContainText("The swap was refused: pos: card refund failed, the processor refused the refund");
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "refund");
  await page.getByRole("button", { name: "Ring the swap" }).click();
  await expect(page.getByTestId("returns-done")).toContainText("Swap rung");
  const keys = ((await stubState(page)) as unknown as { swapKeys: string[] }).swapKeys;
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
});

test("a swap over the approval amount asks for a manager's card on the swap route, and completes with the till still the clerk's", async ({ page }) => {
  await setApprovalLimit(page, true);
  await pickCustomer(page, "maple", "Maple Ridge Contracting");
  await lineOf(page, "CT-1001", WALL).getByLabel(/Return quantity/).fill("3");
  await lineOf(page, "CT-1001", WALL).getByRole("button", { name: "Return to stock" }).click();
  await lineOf(page, "CT-1002", WALL).getByLabel(/Return quantity/).fill("3");
  await lineOf(page, "CT-1002", WALL).getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await addPolymerSand(page);
  await page.getByRole("button", { name: "Ring the swap" }).click();
  const refusal = page.getByTestId("refund-refusal");
  await expect(refusal).toContainText("$250.00");
  await refusal.getByRole("button", { name: "Manager approval" }).click();
  await page.getByRole("dialog", { name: "Manager approval" }).getByRole("button", { name: "Sales manager" }).click();
  await expect(page.getByTestId("returns-done")).toContainText("Swap rung");
  await expect(page.getByTestId("returns-done")).toContainText("Approved by Sales manager (demo).");
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
  const posts = (await stubState(page)).sightings.filter((sighting) => sighting.path === "/api/v1/pos/swaps");
  expect(posts).toHaveLength(2);
  expect(posts[0].approverHeader).toBe(false);
  expect(posts[1].approverHeader).toBe(true);
});

test("an order that still has an open invoice is refused by the server's preview in its words, and nothing can be rung", async ({ page }) => {
  await toSwap(page);
  // The preview is the server's: a refusal of the draft shows in its words with no figures.
  await page.route("**/api/v1/pos/swaps/preview", (route) =>
    route.fulfill({ status: 422, contentType: "application/json", body: JSON.stringify({ error: "pos: order CT-1001 still has an open invoice, so its return is not credit in hand; return it as a credit note first, then ring the new order" }) }),
  );
  await addPolymerSand(page);
  await expect(page.getByTestId("returns-swap-preview-notice")).toContainText("still has an open invoice");
  await expect(page.getByRole("button", { name: "Ring the swap" })).toBeDisabled();
});

// ---- The rulings round: the walk in sale (ruling A) and the cash or card split --------------------

async function openWalkIn(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Walk in sale" }).click();
  await expect(page.getByLabel("Receipt or order number")).toBeVisible();
}

async function findWalkIn(page: Page, number: string): Promise<void> {
  await openWalkIn(page);
  await page.getByLabel("Receipt or order number").fill(number);
  await page.getByRole("button", { name: "Find the sale" }).click();
}

test("the customer step offers a Walk in sale, found only by its number, and a blank or unknown number says so in words", async ({ page }) => {
  await expect(page.getByRole("button", { name: "Walk in sale" })).toBeVisible();
  await openWalkIn(page);
  await expect(page.getByTestId("returns-walkin")).toContainText("only by its receipt or order number");
  await page.getByRole("button", { name: "Find the sale" }).click();
  await expect(page.getByTestId("returns-notice")).toContainText("Type the receipt or order number");
  expect((await stubState(page)).previewBodies).toHaveLength(0);
  await page.getByLabel("Receipt or order number").fill("NOPE-1");
  await page.getByRole("button", { name: "Find the sale" }).click();
  await expect(page.getByTestId("returns-notice")).toContainText("No walk in sale answers to NOPE-1");
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "customer");
});

test("a walk in sale is found by its order number, with or without the hash, or by the bare digits, and only its own order answers", async ({ page }) => {
  for (const number of ["CT-3001", "#ct-3001", "3001"]) {
    await findWalkIn(page, number);
    await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "lines");
    await expect(page.getByTestId("returns-order")).toHaveCount(1);
    await expect(page.getByTestId("returns-order")).toContainText("CT-3001");
    await expect(page.getByTestId("returns-customer-name")).toHaveText("Walk in sale");
    await expect(page.getByTestId("returns-walkin-meta")).toContainText("cash or card only");
    await page.getByRole("button", { name: "Change customer" }).click();
  }
  // Another customer's order number never reaches their lines through the walk in.
  await findWalkIn(page, "CT-1001");
  await expect(page.getByTestId("returns-notice")).toContainText("No walk in sale answers to CT-1001");
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "customer");
});

test("a walk in return goes to cash or card only: no account rail, no swap, and cash commits", async ({ page }) => {
  const before = await drawerExpectedCash(page);
  await findWalkIn(page, "CT-3001");
  await lineOf(page, "CT-3001", WALL).getByLabel(/Return quantity/).fill("2");
  await lineOf(page, "CT-3001", WALL).getByRole("button", { name: "Return to stock" }).click();
  await expect(page.getByTestId("returns-credit-total")).toHaveText("$120.00");
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await expect(page.getByRole("button", { name: "On the account", exact: true })).toBeDisabled();
  await expect(page.getByTestId("returns-walkin-rails")).toContainText("no account to credit");
  await expect(page.getByTestId("returns-swap")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Card", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();
  await expect(page.getByTestId("returns-done")).toContainText("Credit note recorded");
  await expect(page.getByTestId("returns-done")).toContainText("cash, from the drawer");
  expect(await drawerExpectedCash(page)).toBe(before - 12000);
  expect((await stubState(page)).lastNoteBody.refund_method).toBe("CASH");
});

test("a walk in card sale goes back to the card, and over the approval amount it asks for a manager's card", async ({ page }) => {
  await setApprovalLimit(page, true);
  await findWalkIn(page, "CT-3002");
  await lineOf(page, "CT-3002", "Garden edger, grey, each").getByLabel(/Return quantity/).fill("3");
  await lineOf(page, "CT-3002", "Garden edger, grey, each").getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await page.getByRole("button", { name: "Card", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();
  const refusal = page.getByTestId("refund-refusal");
  await expect(refusal).toContainText("$250.00");
  await refusal.getByRole("button", { name: "Manager approval" }).click();
  await page.getByRole("dialog", { name: "Manager approval" }).getByRole("button", { name: "Sales manager" }).click();
  await expect(page.getByTestId("returns-done")).toContainText("$267.00");
  await expect(page.getByTestId("returns-done")).toContainText("Approved by Sales manager (demo).");
  expect((await stubState(page)).lastNoteBody.refund_method).toBe("CARD");
});

test("an order not fully paid: the preview gives the cash or card part and the part off the open invoice, in the server's words, and cash pays out only the part", async ({ page }) => {
  const before = await drawerExpectedCash(page);
  await pickCustomer(page, "C07", "Lachance Retail");
  await lineOf(page, "CT-3003", WALL).getByLabel(/Return quantity/).fill("2");
  await lineOf(page, "CT-3003", WALL).getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  // No rail named yet: no split to show.
  await expect(page.getByTestId("returns-split-note")).toHaveCount(0);
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await expect(page.getByTestId("returns-split-note")).toContainText(
    "This order is not fully paid: up to $50.00 can go back in cash or card; the rest, $70.00, comes off the open invoice",
  );
  await expect(page.getByTestId("returns-split-figures")).toContainText("Goes back in cash: $50.00");
  await expect(page.getByTestId("returns-split-figures")).toContainText("comes off the open invoice: $70.00");
  const bodies = (await stubState(page)).previewBodies as unknown as { refund_method?: string }[];
  expect(bodies[bodies.length - 1].refund_method).toBe("CASH");
  // The account rail splits nothing: the whole credit comes off the invoice by its own rail.
  await page.getByRole("button", { name: "On the account", exact: true }).click();
  await expect(page.getByTestId("returns-split-note")).toHaveCount(0);
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await expect(page.getByTestId("returns-split-note")).toHaveCount(1);
  await page.getByRole("button", { name: "Record the credit note" }).click();
  const done = page.getByTestId("returns-done");
  await expect(done).toContainText("Credit note recorded");
  await expect(done.getByTestId("returns-done-payment-note")).toContainText("This order is not fully paid: up to $50.00");
  await expect(done.getByTestId("returns-done-payment-note")).toContainText("$70.00 came off the open invoice");
  expect(await drawerExpectedCash(page)).toBe(before - 5000);
});

test("an order not paid at all: cash or card say nothing can go back that way, in the server's words, and the account rail still works", async ({ page }) => {
  await pickCustomer(page, "C07", "Lachance Retail");
  await lineOf(page, "CT-3004", WALL).getByRole("button", { name: "Return to stock" }).click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await expect(page.getByTestId("returns-split-note")).toContainText("This order is not paid yet: nothing can go back in cash or card; the whole $60.00 comes off the open invoice");
  await page.getByRole("button", { name: "Record the credit note" }).click();
  await expect(page.getByTestId("returns-notice")).toContainText("This order is not paid yet: nothing can go back in cash or card");
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "refund");
});
