// PR 121 round 2 follow-up, the smaller faults: a column box that keeps a good save when the config re-read fails,
// Send that sends an unsaved terms edit before it mints, one busy flag shared by a scan and a send, an error that
// stays with its quote, and a list that keeps the page a quote was opened from. Answers are held behind gates.
import { expect, test } from "@playwright/test";
import { gate, open, settle, signIn, writes } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a column box keeps a good save when the config re-read after it fails", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  const box = page.locator('[data-field="column"][data-column="line.margin_pct"]');
  await page.getByTestId("qb-columns-btn").click();
  const before = await box.isChecked();
  await page.route("**/api/v1/surfaces/*/config**", (route) =>
    route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "settings: the config store is down" }) }),
  );
  const saved = page.waitForResponse((r) => r.request().method() === "PUT" && r.url().includes("/my-items"));
  const reread = page.waitForResponse((r) => r.request().method() === "GET" && r.url().includes("/config"));
  await box.click();
  await saved;
  await reread;
  await settle(page);
  // The box is uncontrolled and follows the state on the next render: any later press would show a revert.
  await page.getByTestId("qb-po").fill("any later render");
  await settle(page);
  expect(await box.isChecked()).toBe(!before);
});

test("Send sends an unsaved terms edit before it mints the link", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  const po = page.getByTestId("qb-po");
  await expect(po).toBeVisible();
  const seen = writes(page);
  await po.fill("PO-BEFORE-SEND");
  await po.press("Control+Enter");
  await expect(page.getByTestId("qb-notice")).toContainText("Quote sent");
  const order = seen.map((w) => (w.includes("/terms") ? "terms" : w.includes("/share-links") ? "mint" : "other"));
  expect(order.slice(0, 2)).toEqual(["terms", "mint"]);
  expect(seen[0]).toContain("PO-BEFORE-SEND");
});

test("a scan's add does not re-enable Send while a send is still out", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1001\/share-links$/, async (route) => {
    if (route.request().method() === "POST") await held.opened;
    await route.continue();
  });
  const asked = page.waitForRequest((r) => r.method() === "POST" && r.url().endsWith("/quotes/q-1001/share-links"));
  await page.getByTestId("qb-send").click();
  await asked;
  await expect(page.getByTestId("qb-send")).toBeDisabled();
  const scan = page.getByTestId("pos-scan-input");
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(3);
  await settle(page);
  await expect(page.getByTestId("qb-send")).toBeDisabled();
  const minted = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/quotes/q-1001/share-links"));
  held.open();
  await minted;
  await expect(page.getByTestId("qb-notice")).toContainText("Quote sent");
});

test("a refusal on one quote is not shown on the next quote opened", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  await page.route(/\/api\/v1\/quotes\/q-1001\/lines\/1$/, (route) =>
    route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: "quote: refused for this probe" }) }),
  );
  await page.getByRole("button", { name: /^Increase quantity/ }).first().click();
  await expect(page.getByTestId("qb-error")).toContainText(/refused for this probe/i);
  await open(page, "Q-1003");
  await expect(page.getByTestId("qb-error")).toHaveText("");
  await expect(page.getByTestId("qb-notice")).toHaveText("");
});

test("an edit to a quote opened from the second page keeps that page in the list", async ({ page, request }) => {
  await request.put("/_stub/quote-page-size", { data: { size: 1 } });
  await signIn(page, "Sales manager");
  const rows = page.getByTestId("qb-list-row");
  await expect(rows).toHaveCount(1);
  await page.getByRole("button", { name: /more/i }).click();
  await expect(rows).toHaveCount(2);
  await page.getByRole("button", { name: /more/i }).click();
  await expect(rows).toHaveCount(3);
  await open(page, "Q-1001");
  await page.getByRole("button", { name: /^Increase quantity/ }).first().click();
  await expect(page.getByTestId("qb-line-total").first()).not.toHaveText("");
  await settle(page);
  await expect(rows).toHaveCount(3);
  await expect(rows.filter({ hasText: "Q-1001" })).toHaveAttribute("aria-current", "true");
  await request.put("/_stub/quote-page-size", { data: { size: 0 } });
});

test("the convert copy does not say credit is checked for every customer", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1003");
  await page.getByText("Quote detail & admin").click();
  const panel = page.locator("qd-quote-edit");
  await expect(panel).toContainText("Converting it commits the order");
  await expect(panel).not.toContainText("their credit is checked");
  await expect(panel).toContainText("On an account customer the credit is checked");
});
