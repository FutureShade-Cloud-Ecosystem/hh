import { expect, test } from "@playwright/test";
import {
  SEEDED,
  TOP_BAR_CLIP,
  TRENTON_ID,
  apiPicture,
  cad,
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
  setQuantity,
  shot,
  shotParts,
  signIn,
  staffGet,
  staffPost,
} from "./helpers.js";

test("the way in: the manager's card opens the same till under its own name", async ({
  page,
}, testInfo) => {
  await page.goto("/");
  await paceMint(page);
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales manager");
  // The till has loaded under the manager's name: the drawer open, the
  // identity line, the parked button's count.
  await expect(page.getByTestId("session-dot")).toContainText("Open", { timeout: 15_000 });
  await expect(page.getByTestId("pagectx")).toContainText("Trenton");
  await expect(page.getByRole("button", { name: /^Parked \([1-9]\d*\)/ })).toBeVisible();
  await shot(page, "manager-01-way-in", testInfo);
});

test("the drawer: the same shared counter drawer the clerk found", async ({ page }, testInfo) => {
  await signIn(page, "Sales manager");
  await expect(page.getByTestId("pagectx")).toContainText("Trenton");
  await expect(page.getByTestId("session-dot")).toContainText("Open");
  // The same drawer the clerk found, and the core's Z report holds its float.
  const bearer = await mint(page, "sales-manager");
  const drawerId = await openDrawer(page, bearer);
  expect(await openingFloat(page, bearer, drawerId)).toBe(20000);
  await shot(page, "manager-02-drawer", testInfo, { clip: TOP_BAR_CLIP });
});

test("a sale: the same engine prices, the same margin column, no price field", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  const holland = lineRow(page, "Holland Paver, Charcoal (demo)");
  await expect(holland.locator(".lprice")).toHaveText(cad(595));
  await expect(holland.getByTestId("pos-line-margin")).toContainText("%");
  await expect(page.getByTestId("pos-lines").getByRole("textbox")).toHaveCount(1);
  await shot(page, "manager-03-sale", testInfo);
});

test("a contractor's pavers: the account price and the margin column, the same as the clerk sees", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  await pickCustomer(page, "Bay Ridge", "Bay Ridge Landscaping (demo)");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  const holland = lineRow(page, "Holland Paver, Charcoal (demo)");
  await expect(holland.locator(".lprice")).toHaveText(cad(524));
  await expect(holland.getByTestId("pos-line-margin")).toContainText("%");
  await shot(page, "manager-04-contractor-pavers", testInfo);
});

test("an account the credit gate refuses: the same words to the manager", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  await pickCustomer(page, "DEMO-C07", "DEMO-C07");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Charge Account" }).click();
  await page.getByTestId("pos-complete-sale").click();
  const refusal = page.getByRole("alert").filter({ hasText: "the credit gate refused it" });
  await expect(refusal).toContainText("the credit gate refused it");
  await expect(refusal).toContainText("Cash or card still works");
  await expect(page.getByTestId("sold-panel")).toHaveCount(0);
  await shot(page, "manager-05-credit-refused", testInfo);
});

test("tender and receipt: a card sale under the manager carries the same simulated notice", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  await searchAndAdd(page, "DEMO-PAV-COURTSTONE", "Courtstone, Basalt (demo)");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Card", exact: true }).click();
  await expect(page.getByText("TEST MODE: simulated terminal")).toBeVisible();
  await ring(page);
  await expect(page.getByTestId("sold-panel").getByTestId("sold-simulated-notice")).toContainText(
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
  const park = async () => {
    await searchAndAdd(page, "DEMO-MULCH-BLACK", "Black Cedar Mulch (demo)");
    await page.getByRole("button", { name: "Park ticket" }).click();
    await expect(page.getByTestId("pos-line-row")).toHaveCount(0);
  };
  const resumeFirst = async () => {
    const dialog = await openParked(page);
    await dialog.locator(".ticket").first().getByRole("button", { name: "Resume" }).click();
    await expect(dialog).toHaveCount(0);
    await openStep(page, "items");
    await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
    await expect(page.getByTestId("pos-line-row").locator(".lprice")).toContainText("$");
    await page.getByRole("button", { name: "Park ticket" }).click();
    const again = await openParked(page);
    await expect(again).not.toContainText(SEEDED.kingstonTicket);
    await again.getByRole("button", { name: "Close" }).click();
    await expect(again).toHaveCount(0);
  };

  await signIn(page, "Sales manager");
  // The list's first read is in flight at sign-in; wait for it to land
  // before counting what the world left waiting.
  const first = await openParked(page);
  const before = await first.locator(".ticket").count();
  await expect(first).not.toContainText(SEEDED.kingstonTicket);
  await first.getByRole("button", { name: "Close" }).click();
  await expect(first).toHaveCount(0);
  await park();
  const list = await openParked(page);
  await expect(list.locator(".ticket")).toHaveCount(before + 1);
  await shot(page, "manager-07-parked", testInfo);
  await list.getByRole("button", { name: "Close" }).click();
  await expect(list).toHaveCount(0);
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
  const rung = await ringCash(page, "60.00");
  const orderNo = rung.order_no;
  const bearer = await mint(page, "sales-manager");
  const drawerId = await openDrawer(page, bearer);
  const before = await expectedCash(page, bearer, drawerId);
  const zPath = `/api/v1/till/sessions/${drawerId}/zreport`;
  const zBefore = await apiPicture(page, bearer, zPath);
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
  // The picture holds the claim's three places: the drawer's Z report before
  // the refund, the desk's refund receipt, and the Z report after it, each as
  // the browser shows the core's answer; the expected cash drops by the refund.
  const receiptPicture = await page.getByTestId("refund-receipt").screenshot();
  const zAfter = await apiPicture(page, bearer, zPath);
  await shotParts(page, "manager-08-return-within", testInfo, [zBefore, receiptPicture, zAfter]);
});

test("a return above 250.00 as the manager: no refusal, no dialog, recorded as the manager's own decision", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  await searchAndAdd(page, "DEMO-WALL-BLOCK", "Retaining Wall Block 8x18 (demo)");
  await setQuantity(page, "Retaining Wall Block 8x18 (demo)", "60");
  await expect(lineRow(page, "Retaining Wall Block 8x18 (demo)").locator(".lprice")).toHaveText(cad(1250));
  await expect(lineRow(page, "Retaining Wall Block 8x18 (demo)").locator(".ltot")).toHaveText(cad(75000));
  const rung = await ringCash(page, "900.00");
  const orderNo = rung.order_no;

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
  await expect(page.getByRole("list", { name: "Coming roles" })).toBeVisible();
  // The picture holds both halves of the claim: the cards back on the entry
  // page, and the dispatch read the manager's bearer reaches, as the browser
  // shows the core's answer (the clerk's is the 403 asserted above).
  const cards = await page.screenshot({ fullPage: true });
  const dispatch = await apiPicture(page, manager, `/api/v1/dispatch/trucks?branch_id=${TRENTON_ID}`);
  await shotParts(page, "manager-11-beyond", testInfo, [dispatch, cards]);
});
