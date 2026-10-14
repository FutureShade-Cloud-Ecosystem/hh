// The eleven screens of the plan's section 2 as the clerk: the way in, the
// drawer, a sale, a contractor's pavers, an account that cannot carry the
// sale on account, tender and receipt, parked sales, a return within 250.00,
// a return above it that commits with no approval at the demo dealer, a
// Julian sale refused, and beyond the till.
// The Driver, yard manager, Yard Team and Financial Controller cards are live
// on the front door, so the till's entry serves the two Counter Till roles
// only: none of those rows is in the till (no coming list is drawn).
// Driven against the demo image's own app and core; every figure the screen
// shows is held against what the core answers through its own reads, and a
// screenshot names every screen.
import { openReturns } from "./helpers.js";
import type { Page } from "@playwright/test";
import {
  expect,
  SEEDED,
  SEEDED_IDS,
  TOP_BAR_CLIP,
  POINT_LOMA_ID,
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
  pickCustomer,
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

test("the way in: two cards, every other live role starts at the front door, and the banner", async ({
  page,
}, testInfo) => {
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toBeVisible();
  // The yard manager, the Yard Team and the Driver are flows the demo mints
  // (the front door shows the cards), but the till's entry serves the
  // matrix's Counter Till roles only; they sign in at the front door, so no
  // card of theirs renders here.
  await expect(page.getByRole("button", { name: "Yard manager", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Yard Team", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Driver", exact: true })).toHaveCount(0);
  // No role is coming: every role of the matrix signs in at the front door, so
  // the till draws no coming list.
  await expect(page.getByRole("list", { name: "Coming roles" })).toHaveCount(0);
  // The yard manager, the Yard Team, Payables and Receivables and the
  // Financial Controller have live cards on the front door, so the till's
  // list drops them, as it dropped the Driver: its coming list is the front
  // door's coming rows.
  for (const role of ["Yard manager", "Yard Team", "Payables and Receivables", "Financial Controller", "Logistics Manager", "General Manager", "HR", "Branch Manager", "Outside Sales"]) {
    await expect(page.getByRole("list", { name: "Coming roles" }).getByRole("button", { name: role })).toHaveCount(0);
  }
  await shot(page, "sales-01-way-in", testInfo);
});

test("the drawer: the branch's MAIN counter drawer, found open with the till's own float", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  // The identity line names the invented dealer and the branch, not the
  // migrated trading name; the session bar says the drawer is open.
  await expect(page.getByTestId("pagectx")).toContainText("Gable Landscape Supply, Point Loma");
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
  await searchAndAdd(page, "DEMO-PAV-CHARCOAL", "Interlocking paver 60 mm, charcoal");
  const charcoal = lineRow(page, "Interlocking paver 60 mm, charcoal");
  await expect(charcoal.locator(".lprice")).toHaveText(cad(595));
  // The box's policy word is MANAGER_UP and the clerk is frontline (tier 3),
  // so the till refuses the whole margin surface (P3-10: the surface follows
  // the policy word): no margin cells, and the glance is refused server-side
  // too (the world leg holds that half; the surface's refusal is this
  // story's).
  await expect(page.getByTestId("pos-line-margin")).toHaveCount(0);
  // The line carries a quantity input and no price field anywhere: the price
  // is text, and the one textbox of the line is its quantity.
  await expect(charcoal.getByRole("textbox")).toHaveCount(1);
  await expect(charcoal.getByRole("textbox")).toHaveAttribute("aria-label", /^Quantity: /);
  await expect(page.getByTestId("pos-lines").getByRole("textbox")).toHaveCount(1);
  await shot(page, "sales-03-sale", testInfo);
});

test("a contractor's pavers: the account price rings, and a below floor line rings too", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  // The contractor's own price: below the $5.95 list the drop row shows.
  await pickCustomer(page, "Ridgeview", "Ridgeview Outdoor Living");
  await searchAndAdd(page, "DEMO-PAV-CHARCOAL", "Interlocking paver 60 mm, charcoal");
  const charcoal = lineRow(page, "Interlocking paver 60 mm, charcoal");
  await expect(charcoal.locator(".lprice")).toHaveText(cad(524));
  // The policy word refuses the clerk the whole margin surface (P3-10), on
  // this contractor ticket as on any other: the price stands, the column does
  // not. The manager's till is the allowed side's proof (30-manager).
  await expect(page.getByTestId("pos-line-margin")).toHaveCount(0);
  await shot(page, "sales-04-contractor-pavers", testInfo);

  // The floor is the quote flow's: a line priced below the floor margin rings
  // anyway. The glance refuses the clerk (the policy word, honoured
  // server-side since the hardening train, row 651), so the manager's read is
  // the proof the line is genuinely below the bulk category's floor of 20
  // percent; the refused surface states no margin for it (P3-10), and rings
  // it regardless.
  await pickCustomer(page, "Fieldstone", "Fieldstone Pools and Patios");
  await searchAndAdd(page, "DEMO-AGG-BASE", "Class 2 base rock");
  const granular = lineRow(page, "Class 2 base rock");
  await expect(granular.locator(".lprice")).toHaveText(cad(2925));
  const bearer = await mint(page, "sales");
  const manager = await mint(page, "sales-manager");
  const glanced = await staffPost(page, manager, "/api/v1/pos/margin-glance", {
    branch_id: POINT_LOMA_ID,
    customer_id: SEEDED_IDS.fieldstoneC03,
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

test("an account that cannot carry the sale on account: refused in words, nothing rung", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  // DEMO-C07 is a no credit account (zero day terms, no credit line): item
  // 9's ring refuses the on account shape itself, before the credit gate
  // runs, and the till shows the server's own words for the $6.72 the sale
  // would have booked on the account.
  await pickCustomer(page, "DEMO-C07", "DEMO-C07");
  await searchAndAdd(page, "DEMO-PAV-CHARCOAL", "Interlocking paver 60 mm, charcoal");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Charge Account" }).click();
  await page.getByTestId("pos-complete-sale").click();
  const refusal = page.getByRole("alert").filter({ hasText: "cannot carry" });
  await expect(refusal).toContainText("This account cannot carry $6.72 on account.");
  await expect(refusal).toContainText("Pay in full now, or take a deposit with the balance due later.");
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
  await searchAndAdd(page, "DEMO-EDGE-ALU", "Aluminum edging 8 ft");
  // The pre-ring payment step, held against the core's own prices (rows 985
  // and 661, round 7): the card's shape is the Subtotal row, then the Tax and
  // Total rows the server's sale-preview states (three rows, never the
  // browser's own arithmetic, never the old "added at the ring" words), and
  // below, once the sale is rung and read, the core's own tax and total for
  // this sale must equal what the card showed before the ring. This is the
  // proof behind the testers' page's sentence that before the ring the till
  // shows the lines' prices, their subtotal, and the server's tax and total.
  await openStep(page, "payment");
  const preRingCard = page.getByTestId("pos-totals-card");
  await expect(preRingCard.getByTestId("pos-preview-total")).toBeVisible();
  await expect(preRingCard.locator(".trow")).toHaveCount(3);
  await expect(preRingCard).toContainText("Subtotal");
  await expect(preRingCard.getByTestId("pos-preview-tax")).toContainText("Tax");
  await expect(preRingCard.getByTestId("pos-preview-total")).toContainText("Total");
  await expect(preRingCard.getByTestId("pos-preview-pending")).toHaveCount(0);
  await expect(preRingCard.getByTestId("pos-ring-words")).toHaveCount(0);
  await expect(preRingCard).not.toContainText("added at the ring");
  await expect(preRingCard).not.toContainText("(est.");
  const previewTax = (await preRingCard.getByTestId("pos-preview-tax").innerText()).replace(/^Tax\s*/, "").trim();
  const previewTotal = (await preRingCard.getByTestId("pos-preview-total").innerText()).replace(/^Total\s*/, "").trim();
  // The cash tile words the ring instead of computing a change figure: the
  // proof behind the testers' page's sentence that before the ring the till
  // states no change figure, even with an amount typed.
  await page.getByLabel(/Cash tendered in dollars/).fill("60.00");
  await expect(page.getByTestId("cash-change-preview")).toContainText("the ring's own answer");
  await expect(page.getByTestId("cash-change-preview")).not.toContainText("$");
  const rung = await ringCash(page, "60.00");
  const sold = page.getByTestId("sold-panel");
  const bearer = await mint(page, "sales");
  const sale = await readSale(page, bearer, rung.order_id);
  const total = sale.total_cents as number;
  await expect(sold).toContainText("Paid");
  await expect(sold).toContainText(String(sale.order_no));
  await expect(sold).toContainText(String(sale.invoice_no));
  await expect(sold).toContainText(cad(total));
  // What the card showed before the ring is the core's own figure for this sale.
  expect(previewTax).toBe(cad(sale.tax_cents as number));
  expect(previewTotal).toBe(cad(total));
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
  // The customer row is required on every sale: the walk-in sale prints the
  // counter's own word beside the walk-in account.
  expect(printedText).toContain("Customer: Walk-in");
  expect(printedText).toContain("Gable Landscape Supply");
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

  // The reprint path: the More menu's any-past-sale receipt, fetched by the
  // order id the past sale carries, arrives MARKED with its ordinal (two
  // prints of this sale came before it) and carries the sale's figures again.
  await page.locator("details.more > summary").filter({ hasText: /^\s*More\s*$/ }).click();
  await page.getByLabel("Open order by id or #SO number").fill(rung.order_id);
  const reprintPrinting = page.context().waitForEvent("page");
  await page.locator("details.more").getByRole("button", { name: "Print receipt" }).click();
  const reprinted = await reprintPrinting;
  const reprintedText = await reprinted.locator("body").innerText();
  expect(reprintedText).toContain("REPRINT #2");
  expect(reprintedText).toContain(`Total\t${dollars(total)}`);
  expect(reprintedText).toContain(`Change due\t${dollars(rung.change_due_cents as number)}`);
  await reprinted.close();
  // A cash confirmation carries no card notice.
  await expect(sold.getByTestId("sold-simulated-notice")).toHaveCount(0);
  // The ring's advance closed the payment step; reopened, the card is the
  // same template with the preview rows dropped once the ring is done, so the
  // core's own figures judge it where it lives: the browser's one assembly
  // (the subtotal) equals the core's, and the core's tax and total for THIS
  // sale stand nowhere on the card (the pre-ring pins above prove this
  // locator sees what it judges).
  await openStep(page, "payment");
  await expect(preRingCard).toContainText(cad(sale.subtotal_cents as number));
  await expect(preRingCard).not.toContainText(cad(sale.tax_cents as number));
  await expect(preRingCard).not.toContainText(cad(total));
  // The confirmation's view is restored for this story's shot.
  await openStep(page, "summary");
  await expect(sold).toContainText("Paid");
  await shot(page, "sales-06-receipt-cash", testInfo);
});

test("tender and receipt: a card sale says no card was charged, on the tender and the receipt", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-PAV-SANDSTONE", "Dimensional paver, sandstone");
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
      `/api/v1/customers/search?q=Bay%20Ridge&branch_id=${POINT_LOMA_ID}`,
    );
    const rows = read.body as unknown as Array<Record<string, unknown>>;
    return rows[0].open_balance_cents as number;
  };
  const balanceBefore = await balanceOf();
  await pickCustomer(page, "Ridgeview", "Ridgeview Outdoor Living");
  await searchAndAdd(page, "DEMO-STEP-SLAB", "Step slab 24x48, natural");
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
  await searchAndAdd(page, "DEMO-MULCH-BLACK", "Black cedar mulch");
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  const dialog = await openParked(page);
  const parked = dialog.locator(".ticket");
  // What the world left waiting counts first: the seed's walk-in ticket is
  // among it, and the ticket just parked comes on top.
  const waiting = await parked.count();
  await expect(dialog).toContainText(SEEDED.pointLomaTicket);
  await expect(dialog).toContainText("Saved carts for this branch");
  await expect(dialog).not.toContainText(SEEDED.julianTicket);
  expect(waiting).toBeGreaterThanOrEqual(2);
  await shot(page, "sales-07-parked", testInfo);
  // Pick the seeded walk-in ticket up, as anyone at this counter may: it
  // prices fresh through the engine.
  await parked.filter({ hasText: SEEDED.pointLomaTicket }).getByRole("button", { name: "Resume" }).click();
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
  await expect(again).not.toContainText(SEEDED.pointLomaTicket);
  await expect(again).not.toContainText(SEEDED.julianTicket);
});

test("a two order return: lines from both orders, one credit note in cash, and the drawer drops by its total", async ({
  page,
}, testInfo) => {
  // Item 10: a return is built like a credit, the customer first. Two sales are rung to the same
  // account (one per ticket), then Returns lists both orders under the customer and one draft takes
  // a line from each, each with its own disposition, then the Refund step in cash and the Done step:
  // one credit note, each order's return with its number, and the drawer's expected cash drops by the
  // note's total, the server's figure read off the Done step.
  await signIn(page, "Sales");
  await pickCustomer(page, "Ridgeview", "Ridgeview Outdoor Living");
  await searchAndAdd(page, "DEMO-PAV-CHARCOAL", "Interlocking paver 60 mm, charcoal");
  const first = await ringCash(page, "20.00");
  await page.goto("/");
  await signIn(page, "Sales");
  await pickCustomer(page, "Ridgeview", "Ridgeview Outdoor Living");
  await searchAndAdd(page, "DEMO-EDGE-ALU", "Aluminum edging 8 ft");
  const second = await ringCash(page, "60.00");

  await openReturns(page);
  await page.getByLabel("Search name or account number").fill("Ridgeview");
  await page.getByRole("button", { name: /Ridgeview Outdoor Living/ }).click();
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "lines");
  const orderOne = page.getByTestId("returns-order").filter({ hasText: first.order_no });
  const orderTwo = page.getByTestId("returns-order").filter({ hasText: second.order_no });
  await expect(orderOne).toHaveCount(1);
  await expect(orderTwo).toHaveCount(1);
  await orderOne.getByRole("button", { name: "Return to stock" }).first().click();
  await orderTwo.getByRole("button", { name: "Return to stock" }).first().click();
  await expect(orderOne.getByTestId("returns-line").first()).toHaveAttribute("data-picked", "true");
  await expect(orderTwo.getByTestId("returns-line").first()).toHaveAttribute("data-picked", "true");
  await expect(page.getByTestId("returns-credit-count")).toHaveText("2 lines from 2 orders");
  // The credit so far is the server's preview (the commit's own pricing), money with tax.
  const totalOf = (text: string): number => {
    const match = text.match(/\$([0-9,]+)\.([0-9]{2})/);
    expect(match).toBeTruthy();
    return Number(match?.[1].replaceAll(",", "") as string) * 100 + Number(match?.[2]);
  };
  await expect(page.getByTestId("returns-credit-total")).toBeVisible();
  const previewed = totalOf((await page.getByTestId("returns-credit-total").textContent()) as string);
  expect(previewed).toBeGreaterThan(0);
  await expect(page.getByRole("button", { name: "Continue to the refund" })).toBeEnabled();
  await shot(page, "sales-08-return-two-orders", testInfo);

  const bearer = await mint(page, "sales");
  const drawerId = await openDrawer(page, bearer);
  const before = await expectedCash(page, bearer, drawerId);
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();
  const done = page.getByTestId("returns-done");
  await expect(done).toContainText("Credit note recorded");
  await expect(done.getByTestId("returns-done-return")).toHaveCount(2);
  await expect(done).toContainText(first.order_no);
  await expect(done).toContainText(second.order_no);
  await expect(done).toContainText("Rung by Sales (demo).");
  const totalText = (await done.getByTestId("returns-note-total").textContent()) as string;
  const totalMatch = totalText.match(/\$([0-9,]+)\.([0-9]{2})/);
  expect(totalMatch).toBeTruthy();
  // The string goes to cents through its parts, never through a float multiply.
  const creditedCents = Number(totalMatch?.[1].replaceAll(",", "") as string) * 100 + Number(totalMatch?.[2]);
  // The commit books exactly what the preview priced.
  expect(creditedCents).toBe(previewed);
  expect(await expectedCash(page, bearer, drawerId)).toBe(before - creditedCents);
  await shot(page, "sales-09-credit-note-done", testInfo);
});

test("a swap: the credit pays a new order, the leftover goes back by cash, both documents name each other", async ({
  page,
}, testInfo) => {
  // Item 10, the swap: a cash sale is rung to the Ridgeview account, then Returns takes its line back to
  // stock and the replacement goods (the server prices them) are paid by the credit. The leftover goes back
  // by the original tender, cash from the drawer, so the drawer's expected cash drops by exactly the
  // leftover the Done step states. The figures are the server's: the preview before and the commit after
  // must say the same new order total.
  await signIn(page, "Sales");
  await pickCustomer(page, "Ridgeview", "Ridgeview Outdoor Living");
  await searchAndAdd(page, "DEMO-PAV-CHARCOAL", "Interlocking paver 60 mm, charcoal");
  await setQuantity(page, "Interlocking paver 60 mm, charcoal", "40");
  const rung = await ringCash(page, "400.00");
  const bearer = await mint(page, "sales");
  const drawerId = await openDrawer(page, bearer);
  const before = await expectedCash(page, bearer, drawerId);

  await openReturns(page);
  await page.getByLabel("Search name or account number").fill("Ridgeview");
  await page.getByRole("button", { name: /Ridgeview Outdoor Living/ }).click();
  const order = page.getByTestId("returns-order").filter({ hasText: rung.order_no });
  await expect(order).toHaveCount(1);
  await order.getByRole("button", { name: "Return to stock" }).first().click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await page.getByLabel("Search replacement goods").fill("Aluminum");
  await page.getByTestId("returns-products").getByRole("button", { name: /Aluminum Edging 8ft/ }).first().click();
  const swap = page.getByTestId("returns-swap");
  await expect(swap.getByTestId("swap-new-total")).toBeVisible();
  const centsOf = (text: string): number => {
    const match = text.match(/\$([0-9,]+)\.([0-9]{2})/);
    expect(match).toBeTruthy();
    return Number(match?.[1].replaceAll(",", "") as string) * 100 + Number(match?.[2]);
  };
  const newTotal = centsOf((await swap.getByTestId("swap-new-total").textContent()) as string);
  const leftover = centsOf((await swap.getByTestId("swap-leftover").textContent()) as string);
  expect(newTotal).toBeGreaterThan(0);
  expect(leftover).toBeGreaterThan(0);
  await shot(page, "sales-10-swap-preview", testInfo);
  await page.getByRole("button", { name: "Ring the swap" }).click();
  const done = page.getByTestId("returns-done");
  await expect(done).toContainText("Swap rung");
  await expect(done.getByTestId("returns-swap-link")).toContainText("point at each other");
  await expect(done.getByTestId("returns-done-return")).toHaveCount(1);
  expect(centsOf((await done.getByTestId("returns-swap-sale").textContent()) as string)).toBe(newTotal);
  expect(await expectedCash(page, bearer, drawerId)).toBe(before - leftover);
  await shot(page, "sales-11-swap-done", testInfo);
});

test("a walk in return: found by its receipt number, cash or card only, and the drawer drops by its total", async ({
  page,
}, testInfo) => {
  // Item 10, ruling A: a walk in sale (rung with no customer) is a returns customer found only by its
  // receipt or order number. The account rail is not offered (it goes back to cash or card), and a cash
  // refund moves the drawer's expected cash by exactly the note's total, the server's figure on Done.
  await signIn(page, "Sales");
  await searchAndAdd(page, "DEMO-EDGE-ALU", "Aluminum edging 8 ft");
  const rung = await ringCash(page, "60.00");
  const bearer = await mint(page, "sales");
  const drawerId = await openDrawer(page, bearer);
  const before = await expectedCash(page, bearer, drawerId);

  await openReturns(page);
  await page.getByRole("button", { name: "Walk in sale" }).click();
  await page.getByLabel("Receipt or order number").fill(rung.order_no);
  await page.getByRole("button", { name: "Find the sale" }).click();
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "lines");
  const order = page.getByTestId("returns-order").filter({ hasText: rung.order_no });
  await expect(order).toHaveCount(1);
  await order.getByRole("button", { name: "Return to stock" }).first().click();
  await page.getByRole("button", { name: "Continue to the refund" }).click();
  await expect(page.getByRole("button", { name: "On the account", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Record the credit note" }).click();
  const done = page.getByTestId("returns-done");
  await expect(done).toContainText("Credit note recorded");
  const totalText = (await done.getByTestId("returns-note-total").textContent()) as string;
  const totalMatch = totalText.match(/\$([0-9,]+)\.([0-9]{2})/);
  expect(totalMatch).toBeTruthy();
  // The string goes to cents through its parts, never through a float multiply.
  const creditedCents = Number(totalMatch?.[1].replaceAll(",", "") as string) * 100 + Number(totalMatch?.[2]);
  expect(await expectedCash(page, bearer, drawerId)).toBe(before - creditedCents);
  await shot(page, "sales-12-walk-in-return-done", testInfo);
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
