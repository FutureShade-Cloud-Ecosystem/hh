import { openReturns } from "./helpers.js";
import {
  expect,
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
  pickCustomer,
  ring,
  ringCash,
  searchAndAdd,
  tapRoleCard,
  test,
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
  // The same entry page move every sign-in holds, belt included.
  await tapRoleCard(page, "Sales manager");
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
  expect(await openingFloat(page, bearer, drawerId)).toBe(30000);
  await shot(page, "manager-02-drawer", testInfo, { clip: TOP_BAR_CLIP });
});

test("a sale: the same engine prices, the manager's margin column, no price field", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  const holland = lineRow(page, "Holland Paver, Charcoal (demo)");
  await expect(holland.locator(".lprice")).toHaveText(cad(595));
  // The engine price is the clerk's; the margin column is the manager's: the
  // policy word is MANAGER_UP and the manager holds a tier 2 role, so the
  // till keeps the column the clerk's screen refuses (P3-10).
  await expect(holland.getByTestId("pos-line-margin")).toContainText("%");
  await expect(page.getByTestId("pos-lines").getByRole("textbox")).toHaveCount(1);
  await shot(page, "manager-03-sale", testInfo);
});

test("a contractor's pavers: the account price the clerk sees, and the margin column the policy allows the manager", async ({
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

test("an account that cannot carry the sale on account: the same words to the manager", async ({
  page,
}, testInfo) => {
  await signIn(page, "Sales manager");
  // The same no credit account as the clerk's leg (20-sales): the shape
  // refusal and its words do not bend for the manager's role.
  await pickCustomer(page, "DEMO-C07", "DEMO-C07");
  await searchAndAdd(page, "DEMO-PAV-HOLLAND", "Holland Paver, Charcoal (demo)");
  await openStep(page, "payment");
  await page.getByRole("button", { name: "Charge Account" }).click();
  await page.getByTestId("pos-complete-sale").click();
  const refusal = page.getByRole("alert").filter({ hasText: "cannot carry" });
  await expect(refusal).toContainText("This account cannot carry $6.72 on account.");
  await expect(refusal).toContainText("Pay in full now, or take a deposit with the balance due later.");
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
  // Sign out: the entry page with the coming roles comes back.
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
