// The Counter Till's W8 exit test: the returns story end to end against the
// stubbed harness (the stub serves the demo routes and the till's return
// paths in the contract's shapes; the vite dev server proxies /api to it).
// The story, as the plan's screens 8 to 10 spell it: the sale found by its
// number, one line returned in cash within the approval amount and the
// drawer's Z report dropping by it, a return above the amount refused with
// the amount and the approvers named, the manager's card through the dialog
// completing it with the receipt naming both and the header still reading the
// clerk, the same return completing with no dialog when the approver role is
// the one signed in, and the card rail saying in words that nothing reached a
// card. The stub's world carries no tax, so every figure is a line's own
// total: 1 of the Holland paver line is $60.00, the full Wall block line is
// $360.00 over the $250.00 approval amount, the second cash sale's line the
// same again, and the card sale's edger $89.00.
import { expect, test, type Page } from "@playwright/test";

// The stub's open drawer at the demo branch, by its own id: the Z report is
// read through the same /api proxy the app uses, under a bearer the stub's
// own demo mint issued, exactly as the real gate would ask one.
const DRAWER_SESSION_ID = "till-session-trenton-1";
const HOLLAND_LINE = "Quantity to return: Holland paver, bevelled, each";
const WALLBLOCK_LINE = "Quantity to return: Wall block, charcoal, each";
const EDGER_LINE = "Quantity to return: Garden edger, grey, each";

async function findSale(page: Page, orderNo: string): Promise<void> {
  await page.getByLabel("Sale number").fill(orderNo);
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-sale")).toContainText(orderNo);
}

async function signIn(page: Page, role: "Sales" | "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("signed-in-role")).toHaveText(role);
}

async function drawerExpectedCash(page: Page): Promise<number> {
  const mint = await page.request.post("/api/v1/demo/session", { data: { flow: "sales" } });
  const minted = (await mint.json()) as { token?: string };
  const answer = await page.request.get(`/api/v1/till/sessions/${DRAWER_SESSION_ID}/zreport`, {
    headers: { Authorization: `Bearer ${minted.token ?? ""}` },
  });
  const report = (await answer.json()) as { cash?: { expected_cents?: number } };
  if (typeof report.cash?.expected_cents !== "number") {
    throw new Error(`the Z report did not answer an expected cash figure: ${JSON.stringify(report)}`);
  }
  return report.cash.expected_cents;
}

test("a return of one line in cash within 250.00 commits, and the drawer's Z report drops by it", async ({ page }) => {
  await signIn(page, "Sales");
  await findSale(page, "CT-1001");
  await expect(page.getByText("Holland paver, bevelled, each")).toBeVisible();
  await expect(page.getByText("sold 2, returned 0")).toBeVisible();
  // A cash sale has no card capture to reverse: the card method is greyed,
  // and its reason is in words beside it.
  await expect(page.getByRole("button", { name: "Card", exact: true })).toBeDisabled();
  await expect(page.getByText("No card refund: this sale was not paid by card", { exact: false })).toBeVisible();

  const before = await drawerExpectedCash(page);
  await page.getByLabel(HOLLAND_LINE).fill("1");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();
  const receipt = page.getByTestId("refund-receipt");
  await expect(receipt).toContainText("$60.00");
  await expect(receipt).toContainText("cash, from the drawer");
  await expect(receipt).toContainText("Rung by Sales (demo).");

  const after = await drawerExpectedCash(page);
  // The Holland paver line at 1 of 2: 6000 cents, the refund the receipt
  // shows; the drawer's expected cash drops by exactly it.
  expect(after).toBe(before - 6000);
});

test("a return above 250.00 is refused with the amount and the approvers named, and completes through the Sales manager's card with the header still reading Sales", async ({ page }) => {
  await signIn(page, "Sales");
  await findSale(page, "CT-1001");
  await page.getByLabel(WALLBLOCK_LINE).fill("6");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();

  const refusal = page.getByTestId("refund-refusal");
  await expect(refusal).toContainText("$250.00");
  await expect(refusal).toContainText("Sales manager");
  await expect(refusal).toContainText("Branch manager");
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await refusal.getByRole("button", { name: "Manager approval" }).click();
  const dialog = page.getByRole("dialog", { name: "Manager approval" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("the till stays the clerk's");
  await dialog.getByRole("button", { name: "Sales manager" }).click();

  const receipt = page.getByTestId("refund-receipt");
  await expect(receipt).toContainText("$360.00");
  await expect(receipt).toContainText("Approved by Sales manager (demo).");
  await expect(receipt).toContainText("Rung by Sales (demo).");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  // The till is still the clerk's: the manager's credential rode the one
  // call and nothing else, and the chrome never changed hands.
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
});

test("as Sales manager the same return completes with no refusal and no dialog", async ({ page }) => {
  await signIn(page, "Sales manager");
  await findSale(page, "CT-1002");
  await page.getByLabel(WALLBLOCK_LINE).fill("6");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();

  const receipt = page.getByTestId("refund-receipt");
  await expect(receipt).toContainText("$360.00");
  await expect(receipt).toContainText("Rung by Sales manager (demo).");
  // The approver role's own commit never earns a refusal, so no dialog and
  // no refusal block render at any point of the path.
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByTestId("refund-refusal")).toHaveCount(0);
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales manager");
});

test("a card return shows the simulated notice: nothing reached a card", async ({ page }) => {
  await signIn(page, "Sales");
  await findSale(page, "CT-2001");
  const card = page.getByRole("button", { name: "Card", exact: true });
  await expect(card).toBeEnabled();
  await card.click();
  await expect(page.getByTestId("simulated-notice")).toContainText("Simulated refund: nothing reached a card.");

  await page.getByLabel(EDGER_LINE).fill("1");
  await page.getByRole("button", { name: "Refund card" }).click();
  const receipt = page.getByTestId("refund-receipt");
  await expect(receipt).toContainText("$89.00");
  await expect(receipt).toContainText("Simulated refund: nothing reached a card.");
});

test("an unknown sale number is refused in words and lays out nothing", async ({ page }) => {
  await signIn(page, "Sales");
  await page.getByLabel("Sale number").fill("CT-9999");
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-notice")).toContainText(
    "No sale numbered CT-9999 answers at this counter: check the number and try again.",
  );
  await expect(page.getByTestId("returns-sale")).toHaveCount(0);
  await expect(page.getByTestId("refund-refusal")).toHaveCount(0);
});
