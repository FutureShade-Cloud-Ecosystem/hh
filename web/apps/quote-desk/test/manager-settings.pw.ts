// Round 10 item 4 (row 1034): the Sales manager's own settings read. Margin policy is reachable, Screen settings
// does not say the quote policy is unreadable, and the scope rules, which this role does not hold, are not offered at all. Where the server refuses a
// manager, that is a core item for the principal, said in the report and not hidden here.
import { expect, test } from "@playwright/test";
import { open, signIn } from "./switch-helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the Sales manager reaches the margin policy and reads every settings box the role holds", async ({ page }) => {
  await signIn(page, "Sales manager");
  await page.getByTestId("desk-tab-pricing").click();
  await expect(page.getByTestId("pricing-panel")).toBeVisible();
  await expect(page.getByTestId("pm-row").first()).toBeVisible();
  await page.getByTestId("desk-tab-quotes").click();
  await open(page, "Q-1001");
  await page.getByText("Quote detail & admin").click();
  await page.locator("qd-quote-settings").getByText("Screen settings", { exact: true }).click();
  await page.getByText("Acceptance settings", { exact: true }).click();
  // The scope rules are the General Manager's (the grant says so): the box is not offered to this role.
  await expect(page.locator("qd-quote-settings").getByText("Scope suggestion rules", { exact: true })).toHaveCount(0);
  const text = await page.locator("qd-quote-settings").evaluate((el) => (el.shadowRoot?.textContent ?? "") + [...(el.shadowRoot?.querySelectorAll("*") ?? [])].map((c) => c.shadowRoot?.textContent ?? "").join(" "));
  expect(text).not.toMatch(/not readable|Forbidden|could not be read/i);
});

test("the Demo chip sits in the band's row at 1440 and does not wrap under the lockup", async ({ page }) => {
  await signIn(page, "Sales manager");
  const rows = await page.locator("hh-shell-header").evaluate((header) => {
    const root = header.shadowRoot as ShadowRoot;
    const lockup = root.querySelector(".lockup")?.getBoundingClientRect();
    const demo = root.querySelector(".demo")?.getBoundingClientRect();
    return lockup === undefined || demo === undefined ? null : { lockupTop: lockup.top, lockupBottom: lockup.bottom, demoTop: demo.top, demoBottom: demo.bottom };
  });
  expect(rows).not.toBeNull();
  // The chip starts above the lockup's bottom edge: the same row, not a second one.
  expect(rows!.demoTop).toBeLessThan(rows!.lockupBottom);
});
