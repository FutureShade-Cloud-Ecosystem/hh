// The eleven screens as the Sales manager: the same till, the same prices and
// the same margin column, the manager's own path on the big refund (no refusal
// and no dialog), and the manager's wider reach past the counter's walls. One
// screenshot names every screen of this pass too.
import { expect, test, type Page } from "@playwright/test";
import {
  SEEDED,
  TRENTON_ID,
  expectedCash,
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

test("the way in: the manager's card opens the same till under its own name", async ({
  page,
}, testInfo) => {
  await page.goto("/");
  await paceMint(page);
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales manager");
  await shot(page, "manager-01-way-in", testInfo);
});

test("the drawer: the same shared counter drawer the clerk found", async ({ page }, testInfo) => {
  await signIn(page, "Sales manager");
  await expect(page.getByTestId("drawer-status")).toContainText("Trenton");
  await expect(page.getByTestId("drawer-status")).toContainText("MAIN: open with a float of $200.00");
  await shot(page, "manager-02-drawer", testInfo);
});

test("a sale: the same engine prices, the same margin column, no price field", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$5.95");
  await expect(page.getByTestId("line-margin").first()).toContainText("%");
  await expect(page.getByTestId("ticket-lines").getByRole("textbox")).toHaveCount(1);
  await shot(page, "manager-03-sale", testInfo);
});

test("a contractor's pavers: the account price and the margin column, the same as the clerk sees", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  await pickCustomer(page, "Bay Ridge", "Bay Ridge Landscaping (demo)");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  await expect(page.getByTestId("line-unit-price").first()).toHaveText("$5.24");
  await expect(page.getByTestId("ticket-line").first()).toContainText("ACCOUNT price");
  await expect(page.getByTestId("line-margin").first()).toContainText("%");
  await shot(page, "manager-04-contractor-pavers", testInfo);
});

test("an account the credit gate refuses: the same words to the manager", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  await pickCustomer(page, "DEMO-C07", "DEMO-C07");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  await page.getByRole("button", { name: "Account", exact: true }).click();
  await page.getByRole("button", { name: "Ring the sale" }).click();
  await expect(page.getByTestId("tender-refusal")).toContainText("the credit gate refused it");
  await expect(page.getByTestId("tender-refusal")).toContainText("Cash or card still works");
  await shot(page, "manager-05-credit-refused", testInfo);
});

test("tender and receipt: a card sale under the manager carries the same simulated notice", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  await searchAndAdd(page, "DEMO-PAV-COURTSTONE", "Courtstone, Basalt (demo)");
  await page.getByRole("button", { name: "Card", exact: true }).click();
  await expect(page.getByTestId("simulated-notice")).toHaveText("Simulated card: no card was charged");
  await page.getByRole("button", { name: "Ring the sale" }).click();
  const receipt = page.getByTestId("receipt");
  await expect(receipt.getByTestId("receipt-simulated-notice")).toHaveText(
    "Simulated card: no card was charged",
  );
  await shot(page, "manager-06-receipt-card", testInfo);
});

test("parked sales: a ticket parks and either role picks it up, at the same counter", async ({
  page,
}, testInfo) => {
  // The hand-over the till's rules name, on the image and across the two
  // logins: the manager parks, the clerk picks it up; the clerk parks, the
  // manager picks it up. Each pickup re-prices the ticket through the engine,
  // and the other branch's ticket never shows at any point.
  const list = page.getByTestId("parked-list");
  const park = async () => {
    await searchAndAdd(page, "DEMO-MULCH-BLACK", "Black Cedar Mulch (demo)");
    await page.getByRole("button", { name: "Park this ticket" }).click();
  };
  const resumeFirst = async () => {
    const rows = list.getByTestId("parked-ticket");
    await rows.first().getByRole("button", { name: "Resume" }).click();
    await expect(page.getByTestId("ticket-line")).toHaveCount(1);
    await expect(page.getByTestId("line-unit-price").first()).toContainText("$");
    await page.getByRole("button", { name: "Park this ticket" }).click();
    await expect(list).not.toContainText(SEEDED.kingstonTicket);
  };

  await signIn(page, "Sales manager");
  // The list's first read is in flight at sign-in; wait for it to land
  // before counting what the world left waiting.
  await expect(page.getByText("Reading the parked tickets.")).toHaveCount(0);
  const before = await list.getByTestId("parked-ticket").count();
  await expect(list).not.toContainText(SEEDED.kingstonTicket);
  await park();
  await expect(list.getByTestId("parked-ticket")).toHaveCount(before + 1);
  await shot(page, "manager-07-parked", testInfo);
  await resumeFirst();

  // The manager's ticket waits; the clerk picks it up.
  await page.getByRole("button", { name: "Sign out" }).click();
  await signIn(page, "Sales");
  await resumeFirst();

  // And the clerk's ticket waits; the manager picks it up.
  await page.getByRole("button", { name: "Sign out" }).click();
  await signIn(page, "Sales manager");
  await resumeFirst();
});

test("a return within 250.00 under the manager: refunded, and the drawer drops by it", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  await searchAndAdd(page, "DEMO-EDGE-ALU", "Aluminum Edging 8ft (demo)");
  await ringCash(page, "60.00");
  const orderNo = (await page.getByTestId("receipt").getByTestId("receipt-order-no").textContent()) as string;
  const bearer = await mint(page, "sales-manager");
  const drawerId = await openDrawer(page, bearer);
  const before = await expectedCash(page, bearer, drawerId);
  await page.getByLabel("Sale number").fill(orderNo);
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-sale")).toContainText(orderNo);
  await returnLineInputs(page).first().fill("1");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();
  const refund = page.getByTestId("refund-receipt");
  await expect(refund).toBeVisible();
  await expect(refund).toContainText("Rung by Sales manager (demo).");
  await expect(refund).toContainText("Approved by the Sales manager on this till: Sales manager (demo).");
  const refunded = (await refund.textContent()) as string;
  const totalMatch = refunded.match(/Total refunded \$([0-9,]+)\.([0-9]{2})/);
  expect(totalMatch).toBeTruthy();
  const refundedCents =
    Number(totalMatch?.[1].replaceAll(",", "") as string) * 100 + Number(totalMatch?.[2]);
  const after = await expectedCash(page, bearer, drawerId);
  expect(after).toBe(before - refundedCents);
  await shot(page, "manager-08-return-within", testInfo);
});

test("a return above 250.00 as the manager: no refusal, no dialog, recorded as the manager's own decision", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
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

  const refund = page.getByTestId("refund-receipt");
  await expect(refund).toBeVisible();
  await expect(refund).toContainText("$847.50");
  await expect(refund).toContainText("Approved by the Sales manager on this till: Sales manager (demo).");
  await expect(refund).toContainText("Rung by Sales manager (demo).");
  // The approver role's own commit never earns a refusal: no dialog and no
  // refusal block render at any point of the path.
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByTestId("refund-refusal")).toHaveCount(0);
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales manager");
  await shot(page, "manager-09-own-refund", testInfo);
});

test("a Kingston sale: the same wall to the manager", async ({ page }, testInfo) => {
  await signIn(page, "Sales manager");
  await page.getByLabel("Sale number").fill(SEEDED.kingstonSale);
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-notice")).toContainText("The lookup was refused");
  await expect(page.getByTestId("returns-notice")).toContainText("not reachable");
  await expect(page.getByTestId("returns-sale")).toHaveCount(0);
  await shot(page, "manager-10-kingston", testInfo);
});

test("beyond the till: the manager's reach past the counter's walls, and the cards come back", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  const manager = await mint(page, "sales-manager");
  const clerk = await mint(page, "sales");
  // The role matrix holds on the demo box: a dispatch read the manager's
  // configuration admits is refused to the clerk.
  const clerkDispatch = await staffGet(
    page,
    clerk,
    `/api/v1/dispatch/trucks?branch_id=${TRENTON_ID}`,
  );
  expect(clerkDispatch.status).toBe(403);
  const managerDispatch = await staffGet(
    page,
    manager,
    `/api/v1/dispatch/trucks?branch_id=${TRENTON_ID}`,
  );
  expect(managerDispatch.status).toBe(200);
  // A price rule write: refused to the clerk, past the gate for the manager
  // (the route's own validation refuses the empty body, and nothing is
  // written).
  const clerkWrite = await staffPost(page, clerk, "/api/v1/pricing/rules", {});
  expect(clerkWrite.status).toBe(403);
  const managerWrite = await staffPost(page, manager, "/api/v1/pricing/rules", {});
  expect([400, 422]).toContain(managerWrite.status);
  // Sign out: the entry page with the eleven coming roles comes back.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toBeVisible();
  await shot(page, "manager-11-beyond", testInfo);
});
