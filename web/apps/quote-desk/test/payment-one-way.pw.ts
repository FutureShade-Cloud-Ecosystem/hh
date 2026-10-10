// Round 11 review P2-3 (the principal's ruling): there is ONE way to convert, the guided surface. The payment panel on
// an accepted quote converts nothing in one action: for a no-credit customer, a Cash On Demand one and an account one
// alike it says how they pay and sends the person to #/quotes/<id>/convert, whose Payment step takes the payment
// before release.
import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { showList, signIn, writes } from "./switch-helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

for (const [label, quoteNo, words] of [
  ["a no-credit customer", "Q-2102", /no credit account/],
  ["an account customer", "Q-1003", /on account \(30 days\)/],
] as const) {
  test(`${label}'s accepted quote offers no tender, and the person is sent to the guided surface`, async ({ page }) => {
    await signIn(page);
    await showList(page);
    await page.getByTestId("qb-list-row").filter({ hasText: quoteNo }).first().click();
    const panel = page.locator("qd-order-payment");
    await expect(panel.getByTestId("op-box")).toContainText(words);
    await expect(panel.getByTestId("op-tenders")).toHaveCount(0);
    await expect(panel.getByTestId("op-box")).not.toContainText("this one action");
    const seen = writes(page);
    await panel.getByTestId("op-convert").click();
    await expect(page.getByTestId("qc-title")).toBeVisible();
    expect(new URL(page.url()).hash).toMatch(/^#\/quotes\/[^/]+\/convert$/);
    await page.waitForTimeout(300);
    expect(seen.filter((w) => w.includes("/order-payment") || w.includes("/convert"))).toEqual([]);
  });
}

test("the Cash On Demand reading is one function the panel and the guided surface share", () => {
  const panel = readFileSync(new URL("../src/order-payment-panel.ts", import.meta.url), "utf8");
  expect(panel).toContain("acceptedPaymentWords(");
  expect(panel).not.toMatch(/creditLimitCents\s*[<>]=?\s*0/);
  expect(panel).not.toMatch(/paymentTermsDays\s*===?\s*0/);
  const convert = readFileSync(new URL("../src/_quote-convert.ts", import.meta.url), "utf8");
  expect(convert).toContain("export function accountKind(");
  // The guided surface's standing and the panel's words both go through it.
  expect(convert.match(/accountKind\(/g)?.length ?? 0).toBeGreaterThanOrEqual(4);
});

test("while the customer's account is being read the panel says so, not the wrong words", async ({ page }) => {
  await signIn(page);
  let release: () => void = () => undefined;
  const gate = new Promise<void>((resolve) => (release = resolve));
  await page.route(/\/api\/v1\/customers\/c0000000-0000-4000-9000-000000000001$/, async (route) => {
    await gate;
    await route.continue();
  });
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1003" }).first().click();
  const box = page.locator("qd-order-payment").getByTestId("op-box");
  await expect(box).toContainText("Reading the customer's account");
  await expect(box).not.toContainText("could not be read");
  release();
  await expect(box).toContainText("on account (30 days)");
});
