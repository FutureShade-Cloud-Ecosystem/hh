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
  await page.getByTestId("till-returns-entry").click();
  await expect(page.getByTestId("returns")).toBeVisible();
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
  const credit = page.getByTestId("returns-credit-line");
  await expect(credit).toHaveCount(2);
  await expect(credit.nth(0)).toContainText("2 Holland paver, bevelled, each on CT-1001 · Return to stock");
  await expect(credit.nth(1)).toContainText("1 Wall block, charcoal, each on CT-1002 · Refund, do not restock (damaged)");
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
  await expect(page.getByTestId("returns-refund-wait")).toContainText("3 lines from 2 orders");
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

test("the walk-in is refused in words: it is not a returns customer", async ({ page }) => {
  // The customer search excludes the house walk-in, so a row for it is staged here and the stub's
  // customer lines read answers it the way the core does (422 with its words).
  await page.route("**/api/v1/customers/search*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify([
        {
          id: "00000000-0000-4000-8000-0000000000ff",
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
  await expect(page.getByTestId("returns-notice")).toContainText("A walk-in sale is not returned from here");
  await expect(page.getByTestId("returns-notice")).toContainText("return a walk-in sale by its own order number");
  await expect(page.getByTestId("returns")).toHaveAttribute("data-step", "customer");
});
