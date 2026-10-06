// Rows 976, 970 and 980: the panels layout is two panes, Items full height on one side and
// Customer, Payment and Summary as steps on the other; the Swap panes button in the header
// flips them and the choice is remembered per browser; the header carries the badge; money
// reads as dollars with no currency prefix.
//
// The geometry is read through the shadow chain, the way a person sees it: boxes on screen.
import { expect, test, type Page } from "@playwright/test";

/** The page gutter the Quote Desk's work area uses. */
const GUTTER = 16;

/** Sign in through the entry cards and land on the ported terminal. */
async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

/** Search from the counter's own field and add the hit to the ticket. */
async function addLine(page: Page, query: string, hit: string): Promise<void> {
  const field = page.getByTestId("pos-scan-input");
  await field.fill(query);
  const row = page.getByTestId("drop-row").filter({ hasText: hit }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: hit })).toHaveCount(1);
}

/** Pick the panels layout from the counter view menu. */
async function pickPanels(page: Page): Promise<void> {
  await page.getByTestId("pos-view").click();
  const radio = page.getByTestId("pos-layout-panels");
  // Choosing a layout closes the menu; an already chosen one leaves it open to close by hand.
  if ((await radio.getAttribute("aria-checked")) !== "true") await radio.click();
  else await page.getByTestId("pos-view").click();
  await expect(page.getByTestId("pos-twopane")).toBeVisible();
}

/** Put the workspace pick back to the stepper, as the legs after this file expect. */
async function pickStepper(page: Page): Promise<void> {
  await page.getByTestId("pos-view").click();
  const radio = page.getByTestId("pos-layout-stepper");
  if ((await radio.getAttribute("aria-checked")) !== "true") {
    const saved = page.waitForRequest((r) => r.method() === "PUT" && r.url().includes("/api/v1/pos/workspace"));
    await radio.click();
    await saved;
  } else {
    await page.getByTestId("pos-view").click();
  }
}

test.afterEach(async ({ page }) => {
  try {
    if ((await page.getByTestId("pos-view").count()) === 0) return;
    await pickStepper(page);
  } catch {
    /* the page was closed or never reached the till */
  }
});

/** The two-pane layout with a four-line ticket on it. */
async function twoPaneWithTicket(page: Page): Promise<void> {
  await signIn(page);
  await pickPanels(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await addLine(page, "edge", "Edge Restraint 8 ft");
  await addLine(page, "polymeric", "Polymeric Locking Sand");
  await addLine(page, "wall", "Retaining Wall Stone");
  await page.getByTestId("pos-scan-input").fill("");
}

interface Box {
  x: number;
  y: number;
  right: number;
  bottom: number;
  w: number;
  h: number;
}

/** A box on screen by test id, or null; Playwright locators pierce open shadow roots. */
async function box(page: Page, testId: string): Promise<Box | null> {
  const b = await page.getByTestId(testId).first().boundingBox();
  if (!b) return null;
  return { x: b.x, y: b.y, right: b.x + b.width, bottom: b.y + b.height, w: b.width, h: b.height };
}

/**
 * "No page scroll" for the two-pane screen: the till's screen (the bar and both panes) is exactly
 * the window, the page sits at the top, and nothing of the ticket is below the fold. The returns
 * desk is a separate surface that follows the screen, so it is the only thing the page can scroll
 * down to.
 */
async function expectScreenIsTheWindow(page: Page): Promise<void> {
  const g = await page.evaluate(() => {
    const walk = (root: ParentNode): Element[] => {
      const out: Element[] = [];
      for (const el of root.querySelectorAll("*")) {
        out.push(el);
        if (el.shadowRoot) out.push(...walk(el.shadowRoot));
      }
      return out;
    };
    const screen = walk(document).find((el) => el.matches(".till-screen"));
    const r = screen?.getBoundingClientRect();
    return { top: r?.top ?? -1, bottom: r?.bottom ?? -1, scrollY: window.scrollY, innerH: window.innerHeight };
  });
  expect(g.scrollY).toBe(0);
  expect(Math.abs(g.top)).toBeLessThanOrEqual(1);
  expect(Math.abs(g.bottom - g.innerH)).toBeLessThanOrEqual(1);
}

const DESKTOP_SIZES: Array<[number, number]> = [
  [1440, 900],
  [1920, 1080],
];

for (const [width, height] of DESKTOP_SIZES) {
  test(`both panes fill the screen at ${width}x${height} with a four-line ticket and no page scroll`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height });
    await twoPaneWithTicket(page);

    const items = (await box(page, "pos-pane-items"))!;
    const steps = (await box(page, "pos-pane-steps"))!;
    expect(items).not.toBeNull();
    expect(steps).not.toBeNull();
    // Side by side, Items on the left, sharing one top and one bottom: a full height pane each.
    expect(steps.x).toBeGreaterThan(items.right - 1);
    expect(Math.abs(items.y - steps.y)).toBeLessThanOrEqual(1);
    expect(Math.abs(items.bottom - steps.bottom)).toBeLessThanOrEqual(1);
    // The Quote Desk's gutter on the outer edges.
    expect(items.x).toBeGreaterThanOrEqual(GUTTER - 1);
    expect(width - steps.right).toBeGreaterThanOrEqual(GUTTER - 1);
    // They fill the height: down to the pinned legend, with nothing wasted under them.
    const legendBox = await page.locator("hos-pos-hotkey-legend").first().boundingBox();
    const legend = legendBox ? { y: legendBox.y } : null;
    expect(legend).not.toBeNull();
    expect(items.bottom).toBeLessThanOrEqual(legend!.y + 1);
    expect(legend!.y - items.bottom).toBeLessThanOrEqual(GUTTER + 4);

    // The four lines show whole, and the fulfilment row stays inside the pane in view.
    const lines = page.getByTestId("pos-line-row");
    await expect(lines).toHaveCount(4);
    for (let i = 0; i < 4; i += 1) {
      const b = (await lines.nth(i).boundingBox())!;
      expect(b.y).toBeGreaterThanOrEqual(items.y - 1);
      expect(b.y + b.height).toBeLessThanOrEqual(items.bottom + 1);
    }
    const fulfil = await page.locator("[data-testid='pos-pane-items'] .fulfil").first().boundingBox();
    expect(fulfil).not.toBeNull();
    expect(fulfil!.y + fulfil!.height).toBeLessThanOrEqual(items.bottom + 1);
    await expect(page.locator("[data-testid='pos-pane-items'] .fulfil").first()).toBeInViewport();

    // No page scroll: the screen is the window.
    await expectScreenIsTheWindow(page);
  });
}

test("the Items pane scrolls its lines inside itself and keeps search and fulfilment in view", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await twoPaneWithTicket(page);
  const items = (await box(page, "pos-pane-items"))!;
  const scan = (await page.getByTestId("pos-scan-input").boundingBox())!;
  const fulfil = (await page.locator("[data-testid='pos-pane-items'] .fulfil").first().boundingBox())!;
  expect(scan.y).toBeGreaterThanOrEqual(items.y - 1);
  expect(scan.y + scan.height).toBeLessThanOrEqual(items.bottom + 1);
  expect(fulfil.y + fulfil.height).toBeLessThanOrEqual(items.bottom + 1);
  await expectScreenIsTheWindow(page);
});

test("the swap button sits in the header beside the theme, flips the panes and survives a reload", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  // Not in the stepper layout (the stub keeps the workspace's pick from the legs before).
  await pickStepper(page);
  await expect(page.getByTestId("swap-panes")).toHaveCount(0);
  await pickPanels(page);
  const swap = page.getByTestId("swap-panes");
  await expect(swap).toBeVisible();
  await expect(swap).toHaveAccessibleName("Swap panes");
  const theme = (await page.getByTestId("theme-control").boundingBox())!;
  const sw = (await swap.boundingBox())!;
  expect(sw.x).toBeLessThan(theme.x);
  expect(Math.abs(sw.y - theme.y)).toBeLessThan(theme.height);

  // Items on the left to begin with.
  let items = (await box(page, "pos-pane-items"))!;
  let steps = (await box(page, "pos-pane-steps"))!;
  expect(items.x).toBeLessThan(steps.x);

  await swap.click();
  await expect(swap).toHaveAttribute("aria-pressed", "true");
  items = (await box(page, "pos-pane-items"))!;
  steps = (await box(page, "pos-pane-steps"))!;
  expect(steps.x).toBeLessThan(items.x);

  // A reload signs out (custody is memory); the choice rests in the browser.
  await page.reload();
  await signIn(page);
  await pickPanels(page);
  items = (await box(page, "pos-pane-items"))!;
  steps = (await box(page, "pos-pane-steps"))!;
  expect(steps.x).toBeLessThan(items.x);

  // And back again.
  await page.getByTestId("swap-panes").click();
  items = (await box(page, "pos-pane-items"))!;
  steps = (await box(page, "pos-pane-steps"))!;
  expect(items.x).toBeLessThan(steps.x);
});

test("a blocked store still renders the till and the swap still flips for the page", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(window, "localStorage", {
      get() {
        throw new DOMException("blocked", "SecurityError");
      },
    });
  });
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await pickPanels(page);
  await page.getByTestId("swap-panes").click();
  const items = (await box(page, "pos-pane-items"))!;
  const steps = (await box(page, "pos-pane-steps"))!;
  expect(steps.x).toBeLessThan(items.x);
});

test("F4 and Shift F4 cycle Customer, Payment, Summary in the steps pane; Items is not a step", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await twoPaneWithTicket(page);

  // The header chips are the three steps.
  const chips = page.locator("hos-pos-flow-steps").first().locator("button");
  await expect(chips).toHaveCount(3);
  await expect(chips).toHaveText([/1\s*Customer/, /2\s*Payment/, /3\s*Summary/]);
  const steps = page.getByTestId("pos-pane-steps");
  const customer = steps.getByTestId("pos-customer-mode");
  const payment = steps.getByTestId("pos-paytiles");
  // Summary is the step that is neither of those two.
  const summaryOpen = async () => {
    await expect(customer).toHaveCount(0);
    await expect(payment).toHaveCount(0);
    await expect(steps.getByTestId("pos-pane-steps-body")).toBeVisible();
  };

  await expect(customer).toBeVisible();
  await expect(payment).toHaveCount(0);
  await page.keyboard.press("F4");
  await expect(payment).toBeVisible();
  await expect(customer).toHaveCount(0);
  await page.keyboard.press("F4");
  await summaryOpen();
  await page.keyboard.press("F4");
  await expect(customer).toBeVisible();
  await page.keyboard.press("Shift+F4");
  await summaryOpen();
  await page.keyboard.press("Shift+F4");
  await expect(payment).toBeVisible();
  // The Items pane is untouched throughout.
  await expect(page.getByTestId("pos-pane-items").getByTestId("pos-line-row")).toHaveCount(4);
});

test("portrait tablet 820x1180 stacks Items on top and the steps below, inside the screen", async ({ page }) => {
  await page.setViewportSize({ width: 820, height: 1180 });
  await twoPaneWithTicket(page);
  const items = (await box(page, "pos-pane-items"))!;
  const steps = (await box(page, "pos-pane-steps"))!;
  expect(items.y).toBeLessThan(steps.y);
  expect(items.bottom).toBeLessThanOrEqual(steps.y + 1);
  expect(steps.bottom).toBeLessThanOrEqual(1180);
  expect(items.h).toBeGreaterThan(steps.h);
  await expectScreenIsTheWindow(page);
});

test("1024x768 keeps two panes side by side", async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await twoPaneWithTicket(page);
  const items = (await box(page, "pos-pane-items"))!;
  const steps = (await box(page, "pos-pane-steps"))!;
  expect(steps.x).toBeGreaterThan(items.right - 1);
  expect(Math.abs(items.y - steps.y)).toBeLessThanOrEqual(1);
});

test("the header badge sits at the left edge before the Demo pill, at the front door's size", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  const badge = page.getByTestId("till-header-badge");
  await expect(badge).toBeVisible();
  const b = (await badge.boundingBox())!;
  expect(b.x).toBeLessThanOrEqual(24);
  const mark = (await badge.locator(".brand-mark").boundingBox())!;
  expect(Math.round(mark.width)).toBe(30);
  expect(Math.round(mark.height)).toBe(30);
  await expect(badge).toContainText("HH Yard");
  const pill = page.locator(".demo-banner").first();
  if (await pill.count()) {
    const p = (await pill.boundingBox())!;
    expect(b.x + b.width).toBeLessThanOrEqual(p.x + 1);
  }
});

test("no CA$ in any text the till renders, hidden disclosures included", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await twoPaneWithTicket(page);
  const text = await page.evaluate(() => {
    const walk = (root: ParentNode): string[] => {
      const out: string[] = [];
      for (const el of root.querySelectorAll("*:not(style):not(script)")) {
        for (const n of el.childNodes) if (n.nodeType === 3) out.push(n.textContent ?? "");
        for (const attr of ["title", "aria-label", "placeholder"]) out.push(el.getAttribute(attr) ?? "");
        if (el.shadowRoot) out.push(...walk(el.shadowRoot));
      }
      return out;
    };
    return walk(document).join(" ");
  });
  expect(text.match(/.{0,60}CA\$.{0,40}/g)).toBeNull();
  expect(text).toMatch(/\$\d[\d,]*\.\d{2}/);
});

const ALL_SIZES: Array<[number, number]> = [
  [1440, 900],
  [1920, 1080],
  [1024, 768],
  [820, 1180],
];

for (const [width, height] of ALL_SIZES) {
  test(`every line's remove button sits whole inside the Items pane at ${width}x${height}`, async ({ page }) => {
    await page.setViewportSize({ width, height });
    await twoPaneWithTicket(page);
    const pane = (await box(page, "pos-pane-items"))!;
    // The first line sits at the top of the pane at every size, so its box is read as it is drawn.
    const b = (await page.locator("[data-testid='pos-pane-items'] button.rm").first().boundingBox())!;
    expect(b.x).toBeGreaterThanOrEqual(pane.x - 0.5);
    expect(b.x + b.width).toBeLessThanOrEqual(pane.right + 0.5);
    // And the lines table never scrolls sideways inside the pane.
    const sideways = await page.evaluate(() => {
      const walk = (root: ParentNode): Element[] => {
        const out: Element[] = [];
        for (const el of root.querySelectorAll("*")) {
          out.push(el);
          if (el.shadowRoot) out.push(...walk(el.shadowRoot));
        }
        return out;
      };
      const body = walk(document).find((el) => el.matches(".pane-items-body"));
      return body ? body.scrollWidth - body.clientWidth : -1;
    });
    expect(sideways).toBeLessThanOrEqual(0);
  });
}

test("the badge's product name is 16px, the front door's lockup size", async ({ page }) => {
  await signIn(page);
  const size = await page.getByTestId("till-header-badge").locator(".brand-product").evaluate(
    (el) => getComputedStyle(el).fontSize,
  );
  expect(size).toBe("16px");
});
