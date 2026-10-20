// UX-50: quote list failure state (never "No quotes." or raw server error text
// on a failed GET) and panel divider ARIA value attributes.
import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signIn(page: Page, role = "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

test("quotes list failure shows plain recovery words and Refresh in place of No quotes, never server text", async ({ page }) => {
  let failList = true;
  await page.route(/\/api\/v1\/quotes(\?.*)?$/, async (route) => {
    if (route.request().method() === "GET" && failList) {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Boom" }),
      });
      return;
    }
    await route.continue();
  });

  await signIn(page);
  const list = page.getByTestId("qb-list");
  await expect(list).toContainText("The quotes did not load. Check the connection and press Refresh.");
  await expect(list.getByRole("button", { name: "Refresh", exact: true })).toBeVisible();
  await expect(page.getByText("No quotes.")).toHaveCount(0);
  await expect(page.getByText("Boom")).toHaveCount(0);

  // Pressing Refresh when the server recovers brings the quotes in; an empty tab shows "No quotes." only on a 200.
  failList = false;
  await list.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByTestId("qb-list-row")).toHaveCount(5);
  await page.getByTestId("qd-list-tabs").getByRole("button", { name: "Lost", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toHaveText("No quotes.");
});

test("approvals and follow-ups lists show plain recovery words on a 500, never server text or the empty state", async ({ page }) => {
  await page.route(/\/api\/v1\/staff\/approvals(\?.*)?$/, async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Boom" }),
      });
      return;
    }
    await route.continue();
  });
  await page.route(/\/api\/v1\/quotes\/q-1001\/followups$/, async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Boom" }),
      });
      return;
    }
    await route.continue();
  });

  await signIn(page);
  await page.getByTestId("qd-list-tabs").getByRole("button", { name: "Approvals", exact: true }).click();
  const approvals = page.locator("qd-approvals");
  await expect(approvals.getByTestId("ap-error")).toContainText("The approvals did not load. Check the connection and press Refresh.");
  await expect(approvals.getByTestId("ap-empty")).toHaveCount(0);
  await expect(page.getByText("Boom")).toHaveCount(0);

  await page.getByTestId("qd-list-tabs").getByRole("button", { name: "All", exact: true }).click();
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await page.getByText("Quote detail & admin").click();
  const followups = page.locator("qd-quote-followups");
  await expect(followups.getByTestId("fu-error")).toHaveText("The follow-ups did not load. Check the connection and press Refresh.");
  await expect(followups.getByTestId("fu-empty")).toHaveCount(0);
  await expect(followups.getByTestId("fu-summary")).toHaveCount(0);
  await expect(page.getByText("Boom")).toHaveCount(0);
});

test("a 403 on quotes, approvals or follow-ups lists shows the refusal words with no Check the connection, no Refresh button and no raw server text", async ({ page }) => {
  let forbidQuotes = false;
  await page.route(/\/api\/v1\/quotes(\?.*)?$/, async (route) => {
    if (route.request().method() === "GET" && forbidQuotes) {
      await route.fulfill({
        status: 403,
        contentType: "application/json",
        body: JSON.stringify({ error: "identity: raw server refusal detail" }),
      });
      return;
    }
    await route.continue();
  });
  await page.route(/\/api\/v1\/staff\/approvals(\?.*)?$/, async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 403,
        contentType: "application/json",
        body: JSON.stringify({ error: "identity: raw server refusal detail" }),
      });
      return;
    }
    await route.continue();
  });
  await page.route(/\/api\/v1\/quotes\/q-1001\/followups$/, async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 403,
        contentType: "application/json",
        body: JSON.stringify({ error: "identity: raw server refusal detail" }),
      });
      return;
    }
    await route.continue();
  });

  await signIn(page);
  await page.getByTestId("qd-list-tabs").getByRole("button", { name: "Approvals", exact: true }).click();
  const approvals = page.locator("qd-approvals");
  await expect(approvals.getByTestId("ap-error")).toHaveText("This quote belongs to another branch.");
  await expect(approvals.getByTestId("ap-error")).not.toContainText("Check the connection");
  await expect(approvals.getByRole("button", { name: "Refresh" })).toHaveCount(0);
  await expect(page.getByText("raw server refusal detail")).toHaveCount(0);

  await page.getByTestId("qd-list-tabs").getByRole("button", { name: "All", exact: true }).click();
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await page.getByText("Quote detail & admin").click();
  const followups = page.locator("qd-quote-followups");
  await expect(followups.getByTestId("fu-error")).toHaveText("This quote belongs to another branch.");
  await expect(followups.getByTestId("fu-error")).not.toContainText("Check the connection");
  await expect(followups.getByRole("button", { name: "Refresh" })).toHaveCount(0);
  await expect(page.getByText("raw server refusal detail")).toHaveCount(0);

  forbidQuotes = true;
  await signIn(page);
  const list = page.getByTestId("qb-list");
  await expect(list).toHaveText("This quote belongs to another branch.");
  await expect(list).not.toContainText("Check the connection");
  await expect(list.getByRole("button", { name: "Refresh" })).toHaveCount(0);
  await expect(page.getByText("raw server refusal detail")).toHaveCount(0);
});

test("the panel divider carries aria-valuenow, aria-valuemin and aria-valuemax and updates on keyboard resize", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  const divider = page.getByTestId("qb-divider");
  await expect(divider).toBeVisible();
  await expect(divider).toHaveAttribute("role", "separator");
  await expect(divider).toHaveAttribute("aria-valuemin", "20");
  await expect(divider).toHaveAttribute("aria-valuemax", "80");
  await expect(divider).toHaveAttribute("aria-valuenow", "58");

  await divider.focus();
  await divider.press("ArrowRight");
  await expect(divider).toHaveAttribute("aria-valuenow", "60");
});


