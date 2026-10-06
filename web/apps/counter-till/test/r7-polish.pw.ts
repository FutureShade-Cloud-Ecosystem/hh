// Round 7 polish, the till's own legs: the Feedback control covers no working control in any
// layout. Geometry is read through the shadow chain, the way a person sees it: boxes on screen.
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
}
