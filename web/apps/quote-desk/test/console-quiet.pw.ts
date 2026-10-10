// Round 10 item 23 (row 1092): repeated 403 and 404 answers in the console on every quote page. Each read the
// server refused for this role is asked once for the session, and an id the server answered "not found" is not
// asked again; the console carries no refusal after the first.
import { expect, test, type Page } from "@playwright/test";
import { open, seed, showList, signIn } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

function refusals(page: Page): string[] {
  const seen: string[] = [];
  page.on("response", (r) => {
    if (r.status() === 403 || r.status() === 404) seen.push(`${r.status()} ${new URL(r.url()).pathname}`);
  });
  return seen;
}

test("a read the server would refuse this role is not asked at all, and the grant is asked once", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  const seen = refusals(page);
  const asked: string[] = [];
  page.on("request", (r) => asked.push(new URL(r.url()).pathname));
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-context")).toContainText("Q-1004");
  await showList(page);
  await open(page, "Q-1001");
  await showList(page);
  await open(page, "Q-1004");
  await page.getByRole("button", { name: /^Increase quantity/ }).first().click();
  await page.waitForTimeout(800);
  expect(asked.filter((line) => line.includes("/settings/acceptance") || line.includes("/quote-assist/scope-rules"))).toHaveLength(0);
  expect(seen).toHaveLength(0);
  expect(asked.filter((line) => line.endsWith("/quotes/settings-grant"))).toHaveLength(1);
});

test("a read the server refuses this role anyway (a grant that outran a role change) is asked once for the session", async ({ page }) => {
  await page.request.put("/_stub/grant-all-true", { data: { on: true } });
  await seed(page, { customer_id: "c-fenwick" });
  const seen = refusals(page);
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-context")).toContainText("Q-1004");
  await showList(page);
  await open(page, "Q-1001");
  await showList(page);
  await open(page, "Q-1004");
  // An edit re-reads every panel of the quote and the session panels; a refusal already known is not asked again.
  await page.getByRole("button", { name: /^Increase quantity/ }).first().click();
  await page.waitForTimeout(800);
  expect(seen.filter((line) => line.includes("/settings/acceptance"))).toHaveLength(1);
  expect(seen.filter((line) => line.includes("/quote-assist/scope-rules"))).toHaveLength(1);
});

test("a quote the server says is not there is not asked for again", async ({ page }) => {
  const seen = refusals(page);
  await signIn(page);
  await page.evaluate(() => {
    window.location.hash = "#/quotes/no-such-quote";
  });
  await expect(page.getByTestId("qb-unreadable").or(page.getByText(/may not exist|could not be read|no longer exists/i)).first()).toBeVisible();
  await page.waitForTimeout(500);
  const first = seen.filter((line) => line.includes("/quotes/no-such-quote")).length;
  expect(first, "one refusal for the one quote, however many panels read it").toBe(1);
  await page.evaluate(() => {
    window.location.hash = "#/quotes";
  });
  await page.evaluate(() => {
    window.location.hash = "#/quotes/no-such-quote";
  });
  await page.waitForTimeout(800);
  expect(seen.filter((line) => line.includes("/quotes/no-such-quote")).length).toBe(first);
});
