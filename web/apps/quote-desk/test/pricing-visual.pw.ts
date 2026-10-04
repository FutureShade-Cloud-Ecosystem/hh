// The pricing panel at the console's own size (1440 by 900), light and dark, beside the
// ERP's pricing console shots. Screenshots are written only when QD_SHOT_DIR names a
// directory. The assertions hold either way: every tab fits the viewport width in both
// themes and the panel's text stays readable (its colour is not its background's).
import { expect, test, type Page } from "@playwright/test";
import { shot } from "./shot";
import { openPricing, openTab, signIn } from "./pricing-helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.use({ viewport: { width: 1440, height: 900 } });

async function setTheme(page: Page, word: "Light" | "Dark"): Promise<void> {
  const control = page.getByTestId("theme-control");
  for (let i = 0; i < 3; i += 1) {
    if ((await control.textContent())?.includes(word)) return;
    await control.click();
  }
  await expect(control).toContainText(word);
}

for (const theme of ["Light", "Dark"] as const) {
  test(`every tab fits 1440 wide and reads in ${theme}`, async ({ page }) => {
    await signIn(page);
    await setTheme(page, theme);
    await openPricing(page);
    const tag = theme.toLowerCase();
    for (const tab of ["margin", "rules", "customers", "breaks", "audit"] as const) {
      await openTab(page, tab);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(overflow).toBeLessThanOrEqual(0);
      const colours = await page.getByTestId("pricing-panel").evaluate((el) => {
        const cs = getComputedStyle(el);
        return { text: cs.color, back: getComputedStyle(document.body).backgroundColor };
      });
      expect(colours.text).not.toBe(colours.back);
      await shot(page, `pricing-${tab}-${tag}`);
    }
  });
}

test("the rule editor and the floor warning in dark", async ({ page }) => {
  await signIn(page);
  await setTheme(page, "Dark");
  await openPricing(page);
  await openTab(page, "rules");
  await page.getByTestId("pr-row").filter({ hasText: "Contractor, Pavers" }).getByTestId("pr-edit").click();
  await expect(page.getByTestId("pr-dialog")).toBeVisible();
  await shot(page, "pricing-rule-editor-dark");
  await page.getByTestId("pr-priority").fill("90");
  await page.getByTestId("pr-value").fill("40");
  await page.getByTestId("pr-save").click();
  await expect(page.getByTestId("floor-dialog")).toBeVisible();
  await shot(page, "pricing-floor-warning-dark");
});
