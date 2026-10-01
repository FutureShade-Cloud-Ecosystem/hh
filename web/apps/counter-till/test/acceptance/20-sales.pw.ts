// The eleven screens of the plan's section 2 as the clerk: the way in, the
// drawer, a sale, a contractor's pavers, an account the credit gate refuses,
// tender and receipt, parked sales, a return within 250.00, a return above it
// with the manager walking over, a Kingston sale refused, and beyond the till.
// Driven against the demo image's own app and core; every figure the screen
// shows is held against what the core answers through its own reads, and a
// screenshot names every screen.
import { expect, test, type Page } from "@playwright/test";
import {
  SEEDED,
  SEEDED_IDS,
  TRENTON_ID,
  dollars,
  expectedCash,
  marginPercent,
  mint,
  openDrawer,
  paceMint,
  pickCustomer,
  returnLineInputs,
  searchAndAdd,
  shot,
  signIn,
  staffGet,
  staffPost,
} from "./helpers.js";

async function ringCash(page: Page, tendered: string): Promise<string> {
  await page.getByLabel("Cash tendered, in dollars").fill(tendered);
  await page.getByRole("button", { name: "Ring the sale" }).click();
  const receipt = page.getByTestId("receipt");
  await expect(receipt).toBeVisible();
  return (await receipt.getAttribute("data-order-id")) as string;
}

async function readSale(page: Page, bearer: string, orderId: string) {
  const answer = await staffGet(page, bearer, `/api/v1/pos/sales/${orderId}`);
  expect(answer.status).toBe(200);
  return answer.body;
}

test("the way in: two cards, the eleven coming roles untapped, and the banner", async ({
  page,
}, testInfo) => {
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toBeVisible();
  // The coming roles: shown, labelled Coming, and dead controls.
  for (const role of ["Outside Sales", "Yard Team", "Driver", "Branch Manager", "General Manager", "HR"]) {
    const coming = page.getByRole("list", { name: "Coming roles" }).getByRole("button", { name: role });
    await expect(coming).toBeDisabled();
  }
  await shot(page, "sales-01-way-in", testInfo);
});

test("the drawer: the branch's MAIN counter drawer, found or opened on first use", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  // The strip names the invented dealer, not the migrated trading name.
  await expect(page.getByTestId("drawer-status")).toContainText("Hardscape House (demo), Trenton");
  await expect(page.getByTestId("drawer-status")).toContainText("MAIN: open with a float of $200.00");
  await shot(page, "sales-02-drawer", testInfo);
});

test("a sale: lines priced by the engine, no price field, and the margin column answers", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$5.95");
  await expect(page.getByTestId("line-margin").first()).toContainText("%");
  // The ticket carries a quantity input per line and no price field anywhere.
  const ticketInputs = page.getByTestId("ticket-lines").getByRole("textbox");
  await expect(ticketInputs).toHaveCount(1);
  await expect(ticketInputs.first()).toHaveAttribute("data-qty-key", /.+/);
  await shot(page, "sales-03-sale", testInfo);
});

test("a contractor's pavers: the account price rings, and a below floor line rings too", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  // The contractor's own price: below list, with the ACCOUNT basis word.
  await pickCustomer(page, "Bay Ridge", "Bay Ridge Landscaping (demo)");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$5.24");
  await expect(page.getByTestId("ticket-line").first()).toContainText("ACCOUNT price");
  await expect(page.getByTestId("line-margin").first()).toContainText("%");
  await shot(page, "sales-04-contractor-pavers", testInfo);

  // The floor is the quote flow's: a line priced below the floor margin rings
  // anyway, its low margin shown in the column. The column's words are held
  // to the glance's own answer for this line, and the glance's basis points
  // are held below the bulk category's floor of 20 percent, so the screen
  // shows a genuinely below floor margin and the sale rings regardless.
  await pickCustomer(page, "Quinte", "Quinte Paving & Design (demo)");
  await searchAndAdd(page, "DEMO-AGG-GA", "Granular A (demo)");
  await expect(page.getByTestId("line-unit-price").last()).toHaveText("$29.25");
  const bearer = await mint(page, "sales");
  const glanced = await staffPost(page, bearer, "/api/v1/pos/margin-glance", {
    branch_id: TRENTON_ID,
    customer_id: SEEDED_IDS.quinteC03,
    customer_type: "CONTRACTOR",
    lines: [{ product_id: SEEDED_IDS.granularA, qty_entered: 1, uom_entered: "T" }],
  });
  expect(glanced.status).toBe(200);
  const glanceLines = glanced.body.lines as Array<{ margin_bps?: number }>;
  expect(glanceLines).toHaveLength(1);
  const belowFloorBps = glanceLines[0].margin_bps as number;
  expect(belowFloorBps).toBeLessThan(2000);
  await expect(page.getByTestId("line-margin").last()).toHaveText(marginPercent(belowFloorBps));
  // The shot holds the populated ticket, so the picture shows the low margin
  // the column carries, before the ring empties it.
  await shot(page, "sales-04-below-floor-populated", testInfo);
  const orderId = await ringCash(page, "100.00");
  await expect(page.getByTestId("receipt")).toBeVisible();
  const sale = await readSale(page, bearer, orderId);
  expect(sale.total_cents).toBeLessThan(10000);
  await shot(page, "sales-04-below-floor-rung", testInfo);
});

test("an account the credit gate refuses: refused in words, cash and card intact", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await pickCustomer(page, "DEMO-C07", "DEMO-C07");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  await page.getByRole("button", { name: "Account", exact: true }).click();
  await page.getByRole("button", { name: "Ring the sale" }).click();
  await expect(page.getByTestId("tender-refusal")).toContainText("the credit gate refused it");
  await expect(page.getByTestId("tender-refusal")).toContainText("Cash or card still works");
  await expect(page.getByTestId("receipt")).toHaveCount(0);
  await expect(page.getByTestId("ticket-line")).toHaveCount(1);
  await shot(page, "sales-05-credit-refused", testInfo);
});

test("tender and receipt: cash with change due, held against the core's own read", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-EDGE-ALU", "Aluminum Edging 8ft (demo)");
  const orderId = await ringCash(page, "60.00");
  const receipt = page.getByTestId("receipt");
  const bearer = await mint(page, "sales");
  const sale = await readSale(page, bearer, orderId);
  await expect(receipt.getByTestId("receipt-order-no")).toHaveText(String(sale.order_no));
  await expect(receipt.getByTestId("receipt-invoice-no")).toHaveText(String(sale.invoice_no));
  await expect(receipt.getByTestId("receipt-subtotal")).toHaveText(dollars(sale.subtotal_cents as number));
  await expect(receipt.getByTestId("receipt-tax")).toHaveText(dollars(sale.tax_cents as number));
  await expect(receipt.getByTestId("receipt-total")).toHaveText(dollars(sale.total_cents as number));
  await expect(receipt.getByTestId("receipt-tendered")).toHaveText(dollars(6000));
  await expect(receipt.getByTestId("receipt-change-due")).toHaveText(
    dollars(6000 - (sale.total_cents as number)),
  );
  // A cash receipt carries no card notice.
  await expect(receipt.getByTestId("receipt-simulated-notice")).toHaveCount(0);
  await shot(page, "sales-06-receipt-cash", testInfo);
});

test("tender and receipt: a card sale says no card was charged, on the tender and the receipt", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-PAV-BRUSSELS", "Brussels Dimensional, Sandstone (demo)");
  await page.getByRole("button", { name: "Card", exact: true }).click();
  await expect(page.getByTestId("simulated-notice")).toHaveText("Simulated card: no card was charged");
  await page.getByRole("button", { name: "Ring the sale" }).click();
  const receipt = page.getByTestId("receipt");
  await expect(receipt).toBeVisible();
  await expect(receipt.getByTestId("receipt-simulated-notice")).toHaveText(
    "Simulated card: no card was charged",
  );
  await shot(page, "sales-06-receipt-card", testInfo);
});

test("tender and receipt: an account sale bills the account, invoice and fresh balance named", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  const bearer = await mint(page, "sales");
  const before = await staffGet(
    page,
    bearer,
    `/api/v1/customers/search?q=Bay%20Ridge&branch_id=5d1b3f6e-0000-4000-9000-000000000001`,
  );
  const rows = before.body as unknown as Array<Record<string, unknown>>;
  const balanceBefore = rows[0].open_balance_cents as number;
  await pickCustomer(page, "Bay Ridge", "Bay Ridge Landscaping (demo)");
  await searchAndAdd(page, "DEMO-STEP-SLAB", "Step Slab 24x48, Natural (demo)");
  await page.getByRole("button", { name: "Account", exact: true }).click();
  await page.getByRole("button", { name: "Ring the sale" }).click();
  const receipt = page.getByTestId("receipt");
  await expect(receipt).toBeVisible();
  const invoiceNo = await receipt.getByTestId("receipt-invoice-no").textContent();
  const sale = await readSale(page, bearer, (await receipt.getAttribute("data-order-id")) as string);
  expect(sale.invoice_no).toBe(invoiceNo);
  await expect(receipt.getByTestId("receipt-account")).toContainText("Bay Ridge Landscaping (demo)");
  await expect(receipt.getByTestId("receipt-account")).toContainText(invoiceNo as string);
  await expect(receipt.getByTestId("receipt-account")).toContainText(
    dollars(balanceBefore + (sale.total_cents as number)),
  );
  await shot(page, "sales-06-receipt-account", testInfo);
});

test("parked sales: the branch's list, a park and a pickup, never the other branch's", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-MULCH-BLACK", "Black Cedar Mulch (demo)");
  await page.getByRole("button", { name: "Park this ticket" }).click();
  const parked = page.getByTestId("parked-list").getByTestId("parked-ticket");
  await expect(parked).toHaveCount(2);
  // The parked header names the invented dealer, as the drawer strip does.
  await expect(page.getByLabel("Parked tickets")).toContainText(
    "On this branch's counter: Hardscape House (demo), Trenton.",
  );
  // The seed's Trenton walk-in ticket is on the list; Kingston's never is.
  await expect(page.getByTestId("parked-list")).toContainText(SEEDED.trentonTicket);
  await expect(page.getByTestId("parked-list")).not.toContainText(SEEDED.kingstonTicket);
  await shot(page, "sales-07-parked", testInfo);
  // Pick the seeded walk-in ticket up, as anyone at this counter may: it
  // prices fresh through the engine.
  const seededRow = parked.filter({ hasText: SEEDED.trentonTicket });
  await seededRow.getByRole("button", { name: "Resume" }).click();
  await expect(page.getByTestId("ticket-line")).toHaveCount(1);
  await expect(page.getByTestId("line-unit-price").first()).toContainText("$");
  await expect(page.getByTestId("parked-list")).not.toContainText(SEEDED.trentonTicket);
  // Park the counter's ticket again: it comes back as a new ticket of this
  // round (a new number), beside the one already waiting, and the other
  // branch's ticket still never shows.
  await page.getByRole("button", { name: "Park this ticket" }).click();
  await expect(page.getByTestId("parked-list").getByTestId("parked-ticket")).toHaveCount(2);
  await expect(page.getByTestId("parked-list")).not.toContainText(SEEDED.trentonTicket);
  await expect(page.getByTestId("parked-list")).not.toContainText(SEEDED.kingstonTicket);
});

test("a return within 250.00: refunded in cash, and the drawer's expected cash drops by it", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-EDGE-ALU", "Aluminum Edging 8ft (demo)");
  await ringCash(page, "60.00");
  const orderNo = (await page.getByTestId("receipt").getByTestId("receipt-order-no").textContent()) as string;
  const bearer = await mint(page, "sales");
  const drawerId = await openDrawer(page, bearer);
  const before = await expectedCash(page, bearer, drawerId);
  await page.getByLabel("Sale number").fill(orderNo);
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-sale")).toContainText(orderNo);
  // The seeded card sale beside it: the desk cannot send its refund by card
  // at all, the method greyed with its reason in words (the page's advice to
  // refund such a sale in cash or to the account).
  await page.getByLabel("Sale number").fill(SEEDED.trentonCardSale);
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-sale")).toContainText(SEEDED.trentonCardSale);
  await expect(page.getByRole("button", { name: "Card", exact: true })).toBeDisabled();
  await expect(page.getByTestId("returns")).toContainText(
    "No card refund: this sale was not paid by card",
  );
  // Back to the rung sale for this screen's own return.
  await page.getByLabel("Sale number").fill(orderNo);
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-sale")).toContainText(orderNo);
  await returnLineInputs(page).first().fill("1");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();
  const refund = page.getByTestId("refund-receipt");
  await expect(refund).toBeVisible();
  await expect(refund).toContainText("cash, from the drawer");
  await expect(refund).toContainText("Rung by Sales (demo).");
  const refunded = await refund.textContent();
  const totalMatch = refunded?.match(/Total refunded \$([0-9,]+)\.([0-9]{2})/);
  expect(totalMatch).toBeTruthy();
  // The string goes to cents through its parts, never through a float multiply.
  const refundedCents =
    Number(totalMatch?.[1].replaceAll(",", "") as string) * 100 + Number(totalMatch?.[2]);
  const after = await expectedCash(page, bearer, drawerId);
  expect(after).toBe(before - refundedCents);
  await shot(page, "sales-08-return-within", testInfo);
});

test("a return above 250.00: refused with the amount and approvers named, completed by the manager's card at the clerk's till", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  // A sale whose refund crosses the dealer's approval amount: sixty wall blocks.
  await searchAndAdd(page, "DEMO-WALL-BLOCK", "Retaining Wall Block 8x18 (demo)");
  await page.getByLabel("Quantity of Retaining Wall Block 8x18 (demo)").fill("60");
  await page.getByRole("button", { name: "Re-price the ticket" }).click();
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$12.50");
  await ringCash(page, "900.00");
  const orderNo = (await page.getByTestId("receipt").getByTestId("receipt-order-no").textContent()) as string;

  await page.getByLabel("Sale number").fill(orderNo);
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-sale")).toContainText(orderNo);
  await returnLineInputs(page).first().fill("60");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();

  const refusal = page.getByTestId("refund-refusal");
  await expect(refusal).toContainText("$250.00");
  await expect(refusal).toContainText("Sales manager");
  await expect(refusal).toContainText("Branch manager");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await shot(page, "sales-09-refused", testInfo);

  // The manager walks over and taps their own card at the clerk's till.
  await refusal.getByRole("button", { name: "Manager approval" }).click();
  const dialog = page.getByRole("dialog", { name: "Manager approval" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("the till stays the clerk's");
  await shot(page, "sales-09-dialog", testInfo);
  // The approver's card mints the manager credential: the same paced tap a
  // person gives it.
  await paceMint(page);
  await dialog.getByRole("button", { name: "Sales manager" }).click();

  const refund = page.getByTestId("refund-receipt");
  await expect(refund).toBeVisible();
  await expect(refund).toContainText("$847.50");
  await expect(refund).toContainText("Approved by Sales manager (demo).");
  await expect(refund).toContainText("Rung by Sales (demo).");
  // The till is still the clerk's.
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  // The approved refund went through once: the core holds exactly one return
  // for the sale (its line fully returned) and exactly one drawer movement
  // paying it out of the shared drawer, named for the return.
  const bearer2 = await mint(page, "sales");
  const after = await staffGet(
    page,
    bearer2,
    `/api/v1/pos/returns/lookup?order_no=${encodeURIComponent(orderNo)}`,
  );
  expect(after.status).toBe(200);
  const afterLines = after.body.lines as Array<{ qty_sold: number; qty_returned: number }>;
  expect(afterLines).toHaveLength(1);
  expect(afterLines[0].qty_sold).toBe(60);
  expect(afterLines[0].qty_returned).toBe(60);
  const drawerId = await openDrawer(page, bearer2);
  const detail = await staffGet(page, bearer2, `/api/v1/till/sessions/${drawerId}`);
  const movements = detail.body.movements as Array<{
    movement_type: string;
    amount_cents: number;
    reference: string;
  }>;
  const payouts = movements.filter((m) => m.movement_type === "PAID_OUT" && m.amount_cents === 84750);
  expect(payouts, "exactly one drawer movement carries the approved refund").toHaveLength(1);
  await shot(page, "sales-09-approved", testInfo);
});

test("a Kingston sale: refused in words before anything lays out", async ({ page }, testInfo) => {
  await signIn(page, "Sales");
  await page.getByLabel("Sale number").fill(SEEDED.kingstonSale);
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-notice")).toContainText("The lookup was refused");
  await expect(page.getByTestId("returns-notice")).toContainText("not reachable");
  await expect(page.getByTestId("returns-sale")).toHaveCount(0);
  await expect(page.getByTestId("refund-refusal")).toHaveCount(0);
  await shot(page, "sales-10-kingston", testInfo);
});

test("beyond the till: sign out, a reload, and a fresh page each leave the till nobody's", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  // The page tells the tester a reload ends the session, and the box works
  // exactly that way: the mint lives in the tab alone, so a signed in reload
  // returns the cards, not the till.
  await page.reload();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByTestId("signed-in-role")).toHaveCount(0);
  await shot(page, "sales-11-beyond", testInfo);
  // Sign out lands on the same place, deliberately.
  await signIn(page, "Sales");
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  // And a fresh page is nobody's till either.
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByTestId("signed-in-role")).toHaveCount(0);
});
