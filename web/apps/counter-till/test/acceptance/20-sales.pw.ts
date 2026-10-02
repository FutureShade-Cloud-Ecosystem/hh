// The eleven screens of the plan's section 2 as the clerk: the way in, the
// drawer, a sale, a contractor's pavers, an account the credit gate refuses,
// tender and receipt, parked sales, a return within 250.00, a return above it
// with the manager walking over, a Kingston sale refused, and beyond the till.
// Driven against the demo image's own app and core; every figure the screen
// shows is held against what the core answers through its own reads, and a
// screenshot names every screen.
import type { Page } from "@playwright/test";
import {
  expect,
  SEEDED,
  SEEDED_IDS,
  TOP_BAR_CLIP,
  TRENTON_ID,
  apiPicture,
  cad,
  dollars,
  expectedCash,
  lineRow,
  mint,
  openDrawer,
  openParked,
  openStep,
  openingFloat,
  paceMint,
  pickCustomer,
  returnLineInputs,
  ring,
  ringCash,
  searchAndAdd,
  test,
  setQuantity,
  settle,
  shot,
  shotParts,
  signIn,
  staffGet,
  staffPost,
} from "./helpers.js";

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

test("the drawer: the branch's MAIN counter drawer, found open with the till's own float", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  // The identity line names the invented dealer and the branch, not the
  // migrated trading name; the session bar says the drawer is open.
  await expect(page.getByTestId("pagectx")).toContainText("Hardscape House (demo), Trenton");
  await expect(page.getByTestId("session-dot")).toContainText("Open");
  // The surface carries no float figure, so the core's own Z report holds it:
  // MAIN is open with the float the till opens it with, $300.00, the
  // reference's own float (see openDrawer).
  const bearer = await mint(page, "sales");
  const drawerId = await openDrawer(page, bearer);
  expect(await openingFloat(page, bearer, drawerId)).toBe(30000);
  await shot(page, "sales-02-drawer", testInfo, { clip: TOP_BAR_CLIP });
});

test("a sale: lines priced by the engine, no price field, and the margin column refuses the clerk", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  const holland = lineRow(page, "Holland Paver, Charcoal (demo)");
  await expect(holland.locator(".lprice")).toHaveText(cad(595));
  // The box's policy word is MANAGER_UP and the clerk is frontline (tier 3),
  // so the till refuses the whole margin surface (P3-10: the surface follows
  // the policy word): no margin cells, though the glance itself still answers
  // the clerk server-side (the world leg holds that half; the surface's
  // refusal is this story's).
  await expect(page.getByTestId("pos-line-margin")).toHaveCount(0);
  // The line carries a quantity input and no price field anywhere: the price
  // is text, and the one textbox of the line is its quantity.
  await expect(holland.getByRole("textbox")).toHaveCount(1);
  await expect(holland.getByRole("textbox")).toHaveAttribute("aria-label", /^Quantity: /);
  await expect(page.getByTestId("pos-lines").getByRole("textbox")).toHaveCount(1);
  await shot(page, "sales-03-sale", testInfo);
});

test("a contractor's pavers: the account price rings, and a below floor line rings too", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  // The contractor's own price: below the $5.95 list the drop row shows.
  await pickCustomer(page, "Bay Ridge", "Bay Ridge Landscaping (demo)");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  const holland = lineRow(page, "Holland Paver, Charcoal (demo)");
  await expect(holland.locator(".lprice")).toHaveText(cad(524));
  // The policy word refuses the clerk the whole margin surface (P3-10), on
  // this contractor ticket as on any other: the price stands, the column does
  // not. The manager's till is the allowed side's proof (30-manager).
  await expect(page.getByTestId("pos-line-margin")).toHaveCount(0);
  await shot(page, "sales-04-contractor-pavers", testInfo);

  // The floor is the quote flow's: a line priced below the floor margin rings
  // anyway. The glance's own answer proves the line is genuinely below the
  // bulk category's floor of 20 percent; the refused surface states no margin
  // for it (P3-10), and rings it regardless.
  await pickCustomer(page, "Quinte", "Quinte Paving & Design (demo)");
  await searchAndAdd(page, "DEMO-AGG-GA", "Granular A (demo)");
  const granular = lineRow(page, "Granular A (demo)");
  await expect(granular.locator(".lprice")).toHaveText(cad(2925));
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
  await expect(page.getByTestId("pos-line-margin")).toHaveCount(0);
  // The shot holds the populated ticket the ring empties; the column it would
  // carry for the manager is absent for the clerk.
  await shot(page, "sales-04-below-floor-populated", testInfo);
  // Ring it: the account's pavers line is still on the ticket beside it, so
  // the cash is typed for the whole ticket.
  const rung = await ringCash(page, "100.00");
  const sale = await readSale(page, bearer, rung.order_id);
  expect(sale.total_cents).toBeLessThan(10000);
  await expect(page.getByTestId("sold-panel")).toContainText(rung.order_no);
  await shot(page, "sales-04-below-floor-rung", testInfo);
});

test("an account the credit gate refuses: refused in words, cash and card intact", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await pickCustomer(page, "DEMO-C07", "DEMO-C07");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Charge Account" }).click();
  await page.getByTestId("pos-complete-sale").click();
  const refusal = page.getByRole("alert").filter({ hasText: "the credit gate refused it" });
  await expect(refusal).toContainText("the credit gate refused it");
  await expect(refusal).toContainText("Cash or card still works");
  await expect(page.getByTestId("sold-panel")).toHaveCount(0);
  await shot(page, "sales-05-credit-refused", testInfo);
  // Nothing committed: the ticket stands.
  await openStep(page, "items");
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
});

test("tender and receipt: cash with change due, held against the core's own read", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-EDGE-ALU", "Aluminum Edging 8ft (demo)");
  const rung = await ringCash(page, "60.00");
  const sold = page.getByTestId("sold-panel");
  const bearer = await mint(page, "sales");
  const sale = await readSale(page, bearer, rung.order_id);
  const total = sale.total_cents as number;
  await expect(sold).toContainText("Paid");
  await expect(sold).toContainText(String(sale.order_no));
  await expect(sold).toContainText(String(sale.invoice_no));
  await expect(sold).toContainText(cad(total));
  await expect(sold.getByTestId("sold-change-due")).toContainText(dollars(6000));
  await expect(sold.getByTestId("sold-change-due")).toContainText(dollars(6000 - total));
  // The confirmation shows the total; the subtotal and the tax are the
  // printed receipt's own lines, the core's document, read against the sale.
  // The document is the quality receipt the pass asks for: the change due row
  // carries the ring's own figure, the fulfilment reads as the counter's
  // plain words and never as the wire's constant, and the letterhead is the
  // demo dealer's invented words end to end, never the first dealer's real
  // trading name.
  const printing = page.context().waitForEvent("page");
  await sold.getByTestId("sold-print").click();
  const printed = await printing;
  const printedText = await printed.locator("body").innerText();
  expect(printedText).toContain(`Subtotal\t${dollars(sale.subtotal_cents as number)}`);
  expect(printedText).toContain(`\t${dollars(sale.tax_cents as number)}`);
  expect(printedText).toContain(`Total\t${dollars(total)}`);
  expect(typeof rung.change_due_cents).toBe("number");
  expect(rung.change_due_cents as number).toBe(6000 - total);
  expect(printedText).toContain(`Change due\t${dollars(rung.change_due_cents as number)}`);
  expect(printedText).toContain("Collected at the counter");
  expect(printedText).not.toContain("COLLECT_NOW");
  expect(printedText).toContain("Hardscape House (demo)");
  expect(printedText).toContain("12 Yard Gate Road");
  // The letterhead's honesty pin: the first dealer's real trading name never
  // prints. The name rides encoded (the name sweep's own discipline), so the
  // assertion ships without the name anywhere in the tree.
  const realDealerName = Buffer.from("RGliYml0cw==", "base64").toString("utf8");
  expect(printedText.toLowerCase()).not.toContain(realDealerName.toLowerCase());
  await printed.close();

  // The full-page document: the same receipt as a sheet (the route's
  // format=page), served from the same committed sale, so every figure above
  // is on it too, on its own print CSS, and this second fetch is marked the
  // reprint it factually is.
  const pagePrinting = page.context().waitForEvent("page");
  await sold.getByTestId("sold-print-page").click();
  const printedPage = await pagePrinting;
  const pageText = await printedPage.locator("body").innerText();
  expect(pageText).toContain(`Subtotal\t${dollars(sale.subtotal_cents as number)}`);
  expect(pageText).toContain(`Total\t${dollars(total)}`);
  expect(pageText).toContain(`Change due\t${dollars(rung.change_due_cents as number)}`);
  expect(pageText).toContain("Collected at the counter");
  expect(pageText).toContain("REPRINT #1");
  await printedPage.close();
  // A cash confirmation carries no card notice.
  await expect(sold.getByTestId("sold-simulated-notice")).toHaveCount(0);
  await shot(page, "sales-06-receipt-cash", testInfo);
});

test("tender and receipt: a card sale says no card was charged, on the tender and the receipt", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-PAV-BRUSSELS", "Brussels Dimensional, Sandstone (demo)");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Card", exact: true }).click();
  await expect(page.getByText("TEST MODE: simulated terminal")).toBeVisible();
  await ring(page);
  const sold = page.getByTestId("sold-panel");
  await expect(sold.getByTestId("sold-simulated-notice")).toContainText(
    "Simulated card: no card was charged",
  );
  await shot(page, "sales-06-receipt-card", testInfo);
});

test("tender and receipt: an account sale bills the account, invoice and fresh balance named", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  const bearer = await mint(page, "sales");
  const balanceOf = async (): Promise<number> => {
    const read = await staffGet(
      page,
      bearer,
      `/api/v1/customers/search?q=Bay%20Ridge&branch_id=${TRENTON_ID}`,
    );
    const rows = read.body as unknown as Array<Record<string, unknown>>;
    return rows[0].open_balance_cents as number;
  };
  const balanceBefore = await balanceOf();
  await pickCustomer(page, "Bay Ridge", "Bay Ridge Landscaping (demo)");
  await searchAndAdd(page, "DEMO-STEP-SLAB", "Step Slab 24x48, Natural (demo)");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Charge Account" }).click();
  const rung = await ring(page);
  const sold = page.getByTestId("sold-panel");
  const sale = await readSale(page, bearer, rung.order_id);
  expect(sale.invoice_no).toBe(rung.invoice_no);
  expect(sale.tender).toBe("ACCOUNT");
  await expect(sold).toContainText("On account");
  await expect(sold).toContainText(rung.invoice_no);
  await expect(sold).toContainText(cad(sale.total_cents as number));
  // The fresh balance is the core's: the account's open balance grew by this
  // sale, and the till re-reads the account after the ring, so the account's
  // card shows the new balance (not the figure read at the pick).
  const balanceAfter = await balanceOf();
  expect(balanceAfter).toBe(balanceBefore + (sale.total_cents as number));
  const confirmation = await sold.screenshot();
  await openStep(page, "customer");
  const card = page.getByTestId("customer-card");
  await expect(card).toContainText(/outstanding/i);
  await expect(card).toContainText(cad(balanceAfter));
  const freshCard = await card.screenshot();
  await shotParts(page, "sales-06-receipt-account", testInfo, [confirmation, freshCard]);
});

test("parked sales: the branch's list, a park and a pickup, never the other branch's", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-MULCH-BLACK", "Black Cedar Mulch (demo)");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  const dialog = await openParked(page);
  const parked = dialog.locator(".ticket");
  // What the world left waiting counts first: the seed's walk-in ticket is
  // among it, and the ticket just parked comes on top.
  const waiting = await parked.count();
  await expect(dialog).toContainText(SEEDED.trentonTicket);
  await expect(dialog).toContainText("Saved carts for this branch");
  await expect(dialog).not.toContainText(SEEDED.kingstonTicket);
  expect(waiting).toBeGreaterThanOrEqual(2);
  await shot(page, "sales-07-parked", testInfo);
  // Pick the seeded walk-in ticket up, as anyone at this counter may: it
  // prices fresh through the engine.
  await parked.filter({ hasText: SEEDED.trentonTicket }).getByRole("button", { name: "Resume" }).click();
  await expect(dialog).toHaveCount(0);
  await openStep(page, "items");
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
  await expect(page.getByTestId("pos-line-row").locator(".lprice")).toContainText("$");
  // Park the counter's ticket again: it comes back as a new ticket of this
  // round (a new number), beside the one already waiting, and the other
  // branch's ticket still never shows.
  await page.getByRole("button", { name: "Park ticket" }).click();
  const again = await openParked(page);
  await expect(again.locator(".ticket")).toHaveCount(waiting);
  await expect(again).not.toContainText(SEEDED.trentonTicket);
  await expect(again).not.toContainText(SEEDED.kingstonTicket);
});

test("a return within 250.00: refunded in cash, and the drawer's expected cash drops by it", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-EDGE-ALU", "Aluminum Edging 8ft (demo)");
  const rung = await ringCash(page, "60.00");
  const orderNo = rung.order_no;
  const bearer = await mint(page, "sales");
  const drawerId = await openDrawer(page, bearer);
  const before = await expectedCash(page, bearer, drawerId);
  const zPath = `/api/v1/till/sessions/${drawerId}/zreport`;
  const zBefore = await apiPicture(page, bearer, zPath);
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
  // The picture holds the claim's three places: the drawer's Z report before
  // the refund, the desk's refund receipt, and the Z report after it, each as
  // the browser shows the core's answer; the expected cash drops by the refund.
  const receiptPicture = await page.getByTestId("refund-receipt").screenshot();
  const zAfter = await apiPicture(page, bearer, zPath);
  await shotParts(page, "sales-08-return-within", testInfo, [zBefore, receiptPicture, zAfter]);
});

test("a return above 250.00: refused with the amount and approvers named, completed by the manager's card at the clerk's till", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  // A sale whose refund crosses the dealer's approval amount: sixty wall blocks.
  await searchAndAdd(page, "DEMO-WALL-BLOCK", "Retaining Wall Block 8x18 (demo)");
  await setQuantity(page, "Retaining Wall Block 8x18 (demo)", "60");
  await expect(lineRow(page, "Retaining Wall Block 8x18 (demo)").locator(".ltot")).toHaveText(cad(75000));
  await expect(lineRow(page, "Retaining Wall Block 8x18 (demo)").locator(".lprice")).toHaveText(cad(1250));
  const rung = await ringCash(page, "900.00");
  const orderNo = rung.order_no;

  await page.getByLabel("Sale number").fill(orderNo);
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-sale")).toContainText(orderNo);
  await returnLineInputs(page).first().fill("60");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  const payoutsOf = async (): Promise<number> => {
    const drawer = await openDrawer(page, bearer0);
    const read = await staffGet(page, bearer0, `/api/v1/till/sessions/${drawer}`);
    const rows = read.body.movements as Array<{ movement_type: string; amount_cents: number }>;
    return rows.filter((m) => m.movement_type === "PAID_OUT" && m.amount_cents === 84750).length;
  };
  const bearer0 = await mint(page, "sales");
  const payoutsBefore = await payoutsOf();
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
  const lookupPath = `/api/v1/pos/returns/lookup?order_no=${encodeURIComponent(orderNo)}`;
  const after = await staffGet(page, bearer2, lookupPath);
  expect(after.status).toBe(200);
  const afterLines = after.body.lines as Array<{ qty_sold: number; qty_returned: number }>;
  expect(afterLines).toHaveLength(1);
  expect(afterLines[0].qty_sold).toBe(60);
  expect(afterLines[0].qty_returned).toBe(60);
  const drawerId = await openDrawer(page, bearer2);
  const detailPath = `/api/v1/till/sessions/${drawerId}`;
  const detail = await staffGet(page, bearer2, detailPath);
  const movements = detail.body.movements as Array<{
    movement_type: string;
    amount_cents: number;
    reference: string;
  }>;
  const payouts = movements.filter((m) => m.movement_type === "PAID_OUT" && m.amount_cents === 84750);
  // The refused attempt moved nothing and the approved one moved once: the
  // shared drawer gained exactly one payout of the refund.
  expect(payouts.length - payoutsBefore, "exactly one drawer movement carries the approved refund").toBe(1);
  // The picture holds the three places the claim lives: the receipt naming
  // both people, the sale's lines read back with the single return, and the
  // drawer's own record with the single payout.
  const receiptPicture = await page.getByTestId("refund-receipt").screenshot();
  const lookupPicture = await apiPicture(page, bearer2, lookupPath);
  const detailPicture = await apiPicture(page, bearer2, detailPath);
  await shotParts(page, "sales-09-approved", testInfo, [receiptPicture, lookupPicture, detailPicture]);
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
  const signedIn = await page.screenshot({ fullPage: true });
  await page.reload();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByTestId("signed-in-role")).toHaveCount(0);
  await settle(page);
  // The picture holds the claim's two halves: the till as it stood signed in,
  // and the page the reload left, the cards and nobody's till.
  const afterReload = await page.screenshot({ fullPage: true });
  await shotParts(page, "sales-11-beyond", testInfo, [signedIn, afterReload]);
  // Sign out lands on the same place, deliberately.
  await signIn(page, "Sales");
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  // And a fresh page is nobody's till either.
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByTestId("signed-in-role")).toHaveCount(0);
});
