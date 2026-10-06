// Round 7 polish, the till's own legs: the Feedback control covers no working control in any
// layout, and the Panels screen is the window with no page scroll. Geometry is read through the shadow chain, the way a person sees it: boxes on screen.
import { expect, test, type Page } from "@playwright/test";

const SIZES: Array<[number, number]> = [
  [1440, 900],
  [1920, 1080],
];

/** Sign in through the entry cards and land on the ported terminal. */
async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

/** Search from the counter's own field and add the hit to the ticket. */
async function addLine(page: Page, query: string, hit: string): Promise<void> {
  await page.getByTestId("pos-scan-input").fill(query);
  const row = page.getByTestId("drop-row").filter({ hasText: hit }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: hit })).toHaveCount(1);
  await page.getByTestId("pos-scan-input").fill("");
}

async function pickLayout(page: Page, layout: "panels" | "stepper"): Promise<void> {
  await page.getByTestId("pos-view").click();
  const radio = page.getByTestId(`pos-layout-${layout}`);
  if ((await radio.getAttribute("aria-checked")) !== "true") {
    const saved = page.waitForRequest((r) => r.method() === "PUT" && r.url().includes("/api/v1/pos/workspace"));
    await radio.click();
    await saved;
  } else {
    await page.getByTestId("pos-view").click();
  }
  if (layout === "panels") await expect(page.getByTestId("pos-twopane")).toBeVisible();
}

test.afterEach(async ({ page }) => {
  try {
    if ((await page.getByTestId("pos-view").count()) === 0) return;
    await pickLayout(page, "stepper");
  } catch {
    /* the page was closed or never reached the till */
  }
});

/**
 * Every visible control on the page (through every open shadow root) as a box, except the
 * Feedback control itself, plus the Feedback control's own box: the control must meet none of them.
 */
async function feedbackMeets(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const walk = (root: ParentNode, out: Element[]): Element[] => {
      for (const el of root.querySelectorAll("*")) {
        out.push(el);
        if (el.shadowRoot) walk(el.shadowRoot, out);
      }
      return out;
    };
    const all = walk(document, []);
    // The bar's Feedback button; a floating pill is what the till carried before, and its box counts too.
    const feedback = all.find((el) => (el as HTMLElement).dataset?.testid === "feedback-button")
      ?? all.find((el) => (el as HTMLElement).dataset?.testid === "feedback-pill");
    if (feedback === undefined) return ["no Feedback control is on screen"];
    const f = feedback.getBoundingClientRect();
    const out: string[] = [];
    for (const el of all) {
      if (el === feedback || feedback.contains(el) || el.contains(feedback)) continue;
      if (!el.matches("button, a[href], input, select, textarea, [role='button'], [role='tab']")) continue;
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      const style = getComputedStyle(el);
      if (style.visibility === "hidden" || style.display === "none") continue;
      const meets = r.left < f.right && r.right > f.left && r.top < f.bottom && r.bottom > f.top;
      if (meets) out.push(`${el.tagName.toLowerCase()} ${(el.textContent ?? "").trim().slice(0, 20)} at ${Math.round(r.left)},${Math.round(r.top)}`);
    }
    return out;
  });
}

for (const [width, height] of SIZES) {
  test(`the Feedback control covers no control at ${width}x${height}, in the steps and the panels, swapped or not`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height });
    await signIn(page);
    await pickLayout(page, "panels");
    await addLine(page, "holland", "Holland Stone 60 mm");
    expect(await feedbackMeets(page)).toEqual([]);
    await page.getByTestId("swap-panes").click();
    await expect(page.getByTestId("swap-panes")).toHaveAttribute("aria-pressed", "true");
    expect(await feedbackMeets(page)).toEqual([]);
    await page.getByTestId("swap-panes").click();
    await pickLayout(page, "stepper");
    expect(await feedbackMeets(page)).toEqual([]);
    // The panel it opens leaves the page's controls where they were.
    await page.getByTestId("feedback-button").click();
    await expect(page.getByTestId("feedback-panel")).toBeVisible();
  });

  test(`the Panels view has no page scroll at ${width}x${height} for a short ticket`, async ({ page }) => {
    await page.setViewportSize({ width, height });
    await signIn(page);
    await pickLayout(page, "panels");
    await addLine(page, "holland", "Holland Stone 60 mm");
    const g = await page.evaluate(() => ({
      scrollHeight: document.documentElement.scrollHeight,
      innerHeight: window.innerHeight,
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth: window.innerWidth,
    }));
    expect(g.scrollHeight).toBeLessThanOrEqual(g.innerHeight);
    expect(g.scrollWidth).toBeLessThanOrEqual(g.innerWidth);
  });
}

/** The branch's drawer, found or opened: a ring needs an open till session. */
async function ensureSession(page: Page): Promise<void> {
  const dot = page.getByTestId("session-dot");
  const openButton = page.getByRole("button", { name: "Open Session", exact: true });
  await expect(dot.or(openButton).first()).toBeVisible({ timeout: 10_000 });
  if (await dot.isVisible()) return;
  await openButton.click({ timeout: 3_000 }).catch(() => undefined);
  await expect(dot).toContainText("Open");
}

test("the Paid card of a split sale says each leg with its amount", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await pickLayout(page, "panels");
  await ensureSession(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await page.locator("hos-pos-flow-steps").first().getByRole("button", { name: /Payment/ }).click();
  await page.getByRole("button", { name: "Split" }).click();
  const panel = page.getByTestId("pos-split-panel");
  await panel.getByLabel("Tender 1 method").selectOption({ label: "Cash" });
  await panel.getByLabel("Tender 2 method").selectOption({ label: "Card" });
  await panel.getByTestId("split-amount-0").fill("1.00");
  await expect(panel.getByTestId("split-amount-1")).toHaveValue("3.52");
  await panel.getByTestId("split-ring").click();
  const sold = page.getByTestId("sold-panel");
  await expect(sold).toBeVisible();
  await expect(sold).toContainText("Split: Cash $1.00 and Card $3.52");
});
