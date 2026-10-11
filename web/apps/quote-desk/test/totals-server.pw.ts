// The totals card shows the server's figures only (PR 296 P2-4): the subtotal is the server's, not the
// browser's sum of the lines, and a delivery fee the server refused leaves the screen.
import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function startQuote(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
}

test("the Subtotal is the server's figure, not the browser's sum of the lines", async ({ page }) => {
  // The card paints the server's answer to a terms read, so only the reads are rewritten: the
  // save's own PUT (which also repaints) passes through untouched, and every re-read after it
  // carries the server's figure again, so the Subtotal shown is never a browser sum of the lines.
  await page.route("**/api/v1/quotes/*/terms", async (route) => {
    if (route.request().method() !== "GET") {
      await route.continue();
      return;
    }
    const response = await route.fetch();
    const body = (await response.json()) as Record<string, unknown>;
    body.subtotal_cents = 987654;
    await route.fulfill({ response, json: body });
  });
  await startQuote(page);
  await expect(page.getByTestId("qb-totals")).toContainText("$9,876.54");
  await expect(page.getByTestId("qb-totals")).not.toContainText("Products");
  // No handler outlives the test: a /terms exchange still inside the handler when the page and
  // context close has its response disposed under it and fails the run (the CI flake this guards).
  await page.unrouteAll({ behavior: "wait" });
});

test("a delivery fee the server refused leaves the screen, the box and the deposit estimate", async ({ page }) => {
  await startQuote(page);
  const saved = page.waitForResponse((r) => /\/quotes\/[^/]+\/terms$/.test(r.url()) && r.request().method() === "PUT");
  await page.getByTestId("qb-fulfil-delivery").click();
  await saved;
  await expect(page.getByTestId("qb-delivery-fee")).toBeVisible();
  await page.route("**/api/v1/quotes/*/terms", async (route) => {
    if (route.request().method() === "PUT") {
      await route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: "quote: this quote can no longer be changed" }) });
      return;
    }
    await route.continue();
  });
  const fee = page.getByTestId("qb-delivery-fee");
  await fee.fill("25");
  await fee.blur();
  await expect(page.getByTestId("qb-error")).toBeVisible();
  await expect(page.getByTestId("qb-totals")).not.toContainText("Delivery");
  await expect(page.getByTestId("qb-totals")).not.toContainText("$25.00");
  await expect(fee).toHaveValue("");
});
