// Round 7 folds: the compose view and the panels, the refused create's reason, the hold expiry panel at New Quote and
// the sign out flush's store close.
import { expect, test } from "@playwright/test";
import { signIn } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const QUOTE_PANELS = ["qd-quote-edit", "qd-quote-assist", "qd-quote-history", "qd-quote-followups", "qd-quote-hold-expiry", "qd-quote-availability", "qd-quote-guard"];

test("compose draws none of the quote panels and leaves no blank gap in the detail", async ({ page }) => {
  await signIn(page, "Sales");
  await expect(page.getByTestId("qb-title")).toHaveText("New Quote");
  await page.getByText("Quote detail & admin").click();
  const seen = await page.locator("qd-quote-builder").evaluate((builder, tags) => {
    const root = builder.shadowRoot as ShadowRoot;
    const panels = tags.map((tag) => {
      const el = root.querySelector(tag) as HTMLElement | null;
      const drawn = el === null ? [] : [...(el.shadowRoot?.children ?? [])].filter((c) => c.tagName !== "STYLE").map((c) => c.tagName);
      return { tag, mounted: el !== null, height: el?.getBoundingClientRect().height ?? -1, drawn };
    });
    const seams = root.querySelector(".seams") as HTMLElement;
    const boxes = [...seams.children].map((c) => c.getBoundingClientRect()).filter((r) => r.height > 0);
    const gaps = boxes.slice(1).map((r, i) => r.top - (boxes[i] as DOMRect).bottom);
    return { panels, gaps };
  }, QUOTE_PANELS);
  for (const panel of seen.panels) {
    expect(panel.mounted, `${panel.tag} stays mounted so late answers have somewhere to land`).toBe(true);
    expect(panel.height, `${panel.tag} takes no room in compose`).toBe(0);
    expect(panel.drawn, `${panel.tag} draws nothing without a quote`).toEqual([]);
  }
  expect(seen.gaps.length).toBeGreaterThan(0);
  for (const gap of seen.gaps) expect(gap, "no blank space between the boxes that do show").toBeLessThanOrEqual(14);
  await expect(page.getByTestId("qg-box")).toHaveCount(0);
});

test("an open quote draws its panels in the detail", async ({ page }) => {
  await signIn(page, "Sales");
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qg-box")).toBeVisible();
  await expect(page.getByTestId("qb-quote-panels")).toBeVisible();
});
