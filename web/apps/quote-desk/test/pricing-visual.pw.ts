// The pricing panel at the console's own size (1440 by 900), light and dark, beside the
// ERP's pricing console shots. Screenshots are written only when QD_SHOT_DIR names a
// directory. The assertions hold either way: every tab fits the viewport width in both
// themes and the panel's text stays readable (its colour is not its background's).
import { expect, test, type Page } from "@playwright/test";
import { shot } from "./shot";
import { openPricing, openTab, setMode, signIn } from "./pricing-helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.use({ viewport: { width: 1440, height: 900 } });

async function setTheme(page: Page, word: "Light" | "Brand"): Promise<void> {
  const control = page.getByTestId("theme-control");
  for (let i = 0; i < 3; i += 1) {
    if ((await control.textContent())?.includes(word)) return;
    await control.click();
  }
  await expect(control).toContainText(word);
}

/** What each tab shows once it has loaded and holds rows: the shots and the checks are of these, never of a loading line. */
async function populated(page: Page, tab: "margin" | "rules" | "customers" | "breaks" | "audit"): Promise<void> {
  await openTab(page, tab);
  if (tab === "margin") await expect(page.getByTestId("pm-row")).toHaveCount(6);
  if (tab === "rules") await expect(page.getByTestId("pr-row")).toHaveCount(5);
  if (tab === "customers") {
    const picker = page.locator("qd-pricing-customers hos-customer-picker");
    await picker.getByPlaceholder("Search name or account #…").fill("Maple");
    await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
    await page.getByTestId("pc-add").click();
    await page.getByTestId("pc-value").fill("6");
    await page.getByTestId("pc-save").click();
    await expect(page.getByTestId("pc-edit")).toHaveCount(1);
  }
  if (tab === "breaks") await expect(page.getByTestId("pb-schedule")).toHaveCount(2);
  if (tab === "audit") {
    await expect(page.getByTestId("pa-row").first()).toBeVisible();
    // the actor names land after the rows; the wide cells they fill are the point of the check
    await expect(page.getByTestId("pa-row").first().locator("td").last()).toContainText(/manager|principal|system/);
  }
  await expect(page.getByText(/^Loading the /)).toHaveCount(0);
}

/** Each tab's own card, by the testid its panel carries. */
const TAB_PANEL: Record<"margin" | "rules" | "customers" | "breaks" | "audit", string> = {
  margin: "pm-panel",
  rules: "pr-panel",
  customers: "pc-panel",
  breaks: "pb-panel",
  audit: "pa-panel",
};

/**
 * The card's own overflow, not only the document's: a card that hides its overflow clips what
 * sticks out (the log's Actor column at 820) while the document still measures clean, so every
 * tab's own panel is asked whether anything sticks out of it. A locator pierces the panel's
 * shadow roots; an element that hides its overflow still reports it in scrollWidth.
 */
async function clippedBy(page: Page, tab: "margin" | "rules" | "customers" | "breaks" | "audit"): Promise<number> {
  return page.getByTestId(TAB_PANEL[tab]).evaluate((card) => card.scrollWidth - card.clientWidth);
}

for (const theme of ["Light", "Brand"] as const) {
  test(`every tab, loaded and populated, fits 1440 wide and reads in ${theme}`, async ({ page }) => {
    await signIn(page);
    await setTheme(page, theme);
    await openPricing(page);
    const tag = theme.toLowerCase();
    // the log has rows to show once a price has been saved: a margin policy row saved first
    await openTab(page, "margin");
    await page.locator('input[data-field="target-ca700000-0000-4000-9000-000000000001"]').fill("34");
    await page.locator('tr[data-category-id="ca700000-0000-4000-9000-000000000001"]').getByTestId("pm-save").click();
    await expect(page.getByTestId("pm-saved")).toBeVisible();
    for (const tab of ["margin", "rules", "customers", "breaks", "audit"] as const) {
      await populated(page, tab);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(overflow).toBeLessThanOrEqual(0);
      expect(await clippedBy(page, tab), `${tab} must not clip inside its own card at 1440`).toBeLessThanOrEqual(0);
      const colours = await page.getByTestId("pricing-panel").evaluate((el) => {
        const cs = getComputedStyle(el);
        return { text: cs.color, back: getComputedStyle(document.body).backgroundColor };
      });
      expect(colours.text).not.toBe(colours.back);
      await shot(page, `pricing-${tab}-${tag}`);
    }
  });
}

test.describe("at the tablet's 820 wide", () => {
  test.use({ viewport: { width: 820, height: 1000 } });

  test("the scope chip stays on one line and no tab overflows", async ({ page }) => {
    await signIn(page);
    await openPricing(page);
    await openTab(page, "rules");
    await expect(page.getByTestId("pr-row")).toHaveCount(5);
    const chips = page.locator("qd-pricing-rules .scopechip");
    for (let i = 0; i < (await chips.count()); i += 1) {
      const box = await chips.nth(i).boundingBox();
      const line = await chips.nth(i).evaluate((el) => parseFloat(getComputedStyle(el).lineHeight) || 16);
      expect(box === null ? 0 : box.height).toBeLessThanOrEqual(line * 1.6);
    }
    await shot(page, "pricing-rules-820-light");
    // the customers step above leaves a floor confirmation in the log, and a seeded rule note
    // carrying a long link is wider still: a URL has no break opportunity, so at this width
    // the log's table cannot shrink inside its card and the audit check really faces the row
    // the card must not clip (the natural rows alone fit the 820 card).
    await setMode(page, { product_names: false });
    await page.request.post("/_stub/pricing-seed-wide-row");
    for (const tab of ["margin", "rules", "customers", "breaks", "audit"] as const) {
      await populated(page, tab);
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
      expect(await clippedBy(page, tab), `${tab} must not clip inside its own card at 820`).toBeLessThanOrEqual(0);
    }
    await setMode(page, { product_names: true });
  });
});

test("the margin boxes are filled boxes, and the code and id inputs sit left", async ({ page }) => {
  await signIn(page);
  await openPricing(page);
  await openTab(page, "margin");
  await expect(page.getByTestId("pm-row")).toHaveCount(6);
  const cell = page.locator('input[data-field^="target-"]').first();
  const look = await cell.evaluate((el) => {
    const cs = getComputedStyle(el);
    const row = el.closest("tr");
    return { radius: parseFloat(cs.borderTopLeftRadius), back: cs.backgroundColor, card: row === null ? "rgb(0, 0, 0)" : getComputedStyle(row).backgroundColor, width: el.getBoundingClientRect().width };
  });
  expect(look.radius).toBeLessThan(12);
  expect(look.width).toBeGreaterThanOrEqual(96);
  expect(look.back).not.toBe("rgba(0, 0, 0, 0)");
  // The reference (shots/pricing/margin-policy.png, measured) is a border and a steady fill, the
  // same fill on the striped row and the plain one. In the light theme they are the line token
  // (#94A4B4, rgb(148,164,180), after the design system's round 7 step up from the reference's
  // rgb(219,227,234)) around the page token (#E7ECF1, rgb(231,236,241), the design system's moved light page), and the dark theme
  // answers with the same two tokens' dark halves, so the check compares the box to the
  // reference's own numbers in light and to the tokens in dark, never to a formula the box
  // itself feeds back (round 3 P3: the old check could not see this difference).
  const plain = page.locator("qd-pricing-margin tbody tr:nth-child(1) input[data-field^='target-']");
  const striped = page.locator("qd-pricing-margin tbody tr:nth-child(2) input[data-field^='target-']");
  await setTheme(page, "Light");
  for (const box of [plain, striped]) {
    const got = await box.evaluate((el) => ({ border: getComputedStyle(el).borderTopColor, fill: getComputedStyle(el).backgroundColor }));
    expect(got.border, "the light line token").toBe("rgb(148, 164, 180)");
    expect(got.fill, "the light page token, steady over the stripe").toBe("rgb(231, 236, 241)");
  }
  await setTheme(page, "Brand");
  for (const box of [plain, striped]) {
    const got = await box.evaluate((el) => {
      const probe = document.createElement("div");
      probe.style.border = "1px solid var(--hos-line, var(--hh-line))";
      probe.style.background = "var(--hos-deep-earth, var(--hh-page))";
      el.parentElement?.append(probe);
      const want = { border: getComputedStyle(probe).borderTopColor, fill: getComputedStyle(probe).backgroundColor };
      probe.remove();
      return { got: { border: getComputedStyle(el).borderTopColor, fill: getComputedStyle(el).backgroundColor }, want };
    });
    expect(got.got.border, "the dark theme's line token").toBe(got.want.border);
    expect(got.got.fill, "the dark theme's page token, steady over the stripe").toBe(got.want.fill);
  }
  await openTab(page, "breaks");
  await page.getByTestId("pb-new").click();
  expect(await page.getByTestId("pb-code").evaluate((el) => getComputedStyle(el).textAlign)).toBe("left");
  await page.getByTestId("pb-scope").selectOption("product");
  expect(await page.getByTestId("pb-product").evaluate((el) => getComputedStyle(el).textAlign)).toBe("left");
});

test("the rule editor and the floor warning in brand mode", async ({ page }) => {
  await signIn(page);
  await setTheme(page, "Brand");
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
