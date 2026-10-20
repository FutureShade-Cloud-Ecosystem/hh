// Round 7 polish, the till's own legs: the Feedback control covers no working control in any
// layout, and the Panels screen is the window with no page scroll. Geometry is read through the shadow chain, the way a person sees it: boxes on screen.
import { expect, test, type Page } from "@playwright/test";
import { closeAccountMenu, openAccountMenu } from "./account-menu";

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
    // The response, not the request: a test that ends before the save lands leaves the stub on the old pick.
    const saved = page.waitForResponse((r) => r.request().method() === "PUT" && r.url().includes("/api/v1/pos/workspace"));
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
  // Feedback is no longer a floating control: it is a button in the band's account menu, so it
  // covers nothing while closed and the menu is a popover under the band while open.
  const out: string[] = [];
  if (await page.getByTestId("feedback-pill").isVisible()) out.push("a floating Feedback pill is on screen");
  await openAccountMenu(page);
  const button = (await page.getByTestId("feedback-button").boundingBox())!;
  const band = (await page.locator("hh-shell-header").boundingBox())!;
  if (button.y < band.y + band.height - 1) out.push("the Feedback button is not under the band");
  await closeAccountMenu(page);
  return out;
}

for (const [width, height] of SIZES) {
  test(`the Feedback control covers no control at ${width}x${height}, in the steps and the panels, swapped or not`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height });
    await signIn(page);
    await pickLayout(page, "panels");
    await addLine(page, "charcoal", "Charcoal Stone 60 mm");
    expect(await feedbackMeets(page)).toEqual([]);
    await page.getByTestId("swap-panes").click();
    await expect(page.getByTestId("swap-panes")).toHaveAttribute("aria-pressed", "true");
    expect(await feedbackMeets(page)).toEqual([]);
    await page.getByTestId("swap-panes").click();
    await pickLayout(page, "stepper");
    expect(await feedbackMeets(page)).toEqual([]);
    // The panel it opens leaves the page's controls where they were.
    await openAccountMenu(page);
    await page.getByTestId("feedback-button").click();
    await closeAccountMenu(page);
    await expect(page.getByTestId("feedback-panel")).toBeVisible();
  });

  test(`the Panels view has no page scroll at ${width}x${height} for a short ticket`, async ({ page }) => {
    await page.setViewportSize({ width, height });
    await signIn(page);
    await pickLayout(page, "panels");
    await addLine(page, "charcoal", "Charcoal Stone 60 mm");
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
  await addLine(page, "charcoal", "Charcoal Stone 60 mm");
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

/** Choose the theme through the bar's cycling control until its label says it. */
async function pickTheme(page: Page, word: "Light" | "Brand"): Promise<void> {
  const control = page.getByTestId("theme-control");
  await openAccountMenu(page);
  for (let turn = 0; turn < 3; turn += 1) {
    if (((await control.innerText()) ?? "").includes(word)) break;
    await control.click();
  }
  await expect(control).toContainText(word);
  await closeAccountMenu(page);
}

test("light theme: panes and cards have an edge that reads, and every input shares one white fill", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await pickLayout(page, "panels");
  await addLine(page, "charcoal", "Charcoal Stone 60 mm");
  await pickTheme(page, "Light");
  const read = await page.evaluate(() => {
    const walk = (root: ParentNode, out: Element[]): Element[] => {
      for (const el of root.querySelectorAll("*")) {
        out.push(el);
        if (el.shadowRoot) walk(el.shadowRoot, out);
      }
      return out;
    };
    const all = walk(document, []);
    const rgb = (text: string): number[] => (text.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
    const lum = (c: number[]): number => {
      const [r, g, b] = c.map((v) => {
        const s = v / 255;
        return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
      });
      return 0.2126 * (r ?? 0) + 0.7152 * (g ?? 0) + 0.0722 * (b ?? 0);
    };
    const ratio = (a: number[], b: number[]): number => {
      const [hi, lo] = [Math.max(lum(a), lum(b)), Math.min(lum(a), lum(b))];
      return (hi + 0.05) / (lo + 0.05);
    };
    const pane = all.find((el) => (el as HTMLElement).dataset?.testid === "pos-pane-items") as HTMLElement;
    const paneStyle = getComputedStyle(pane);
    const edge = ratio(rgb(paneStyle.borderTopColor), rgb(paneStyle.backgroundColor));
    const fills = new Set<string>();
    let inputs = 0;
    for (const el of all) {
      if (!el.matches("input:not([type=hidden]):not([type=checkbox]):not([type=radio]), select, textarea")) continue;
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      inputs += 1;
      fills.add(getComputedStyle(el).backgroundColor);
    }
    const strip = all.find((el) => el.matches(".strip"));
    return { edge, inputs, fills: [...fills], pane: paneStyle.backgroundColor, strip: strip ? getComputedStyle(strip).backgroundColor : "" };
  });
  expect(read.edge).toBeGreaterThanOrEqual(2.5);
  expect(read.inputs).toBeGreaterThan(1);
  expect(read.fills).toEqual(["rgb(255, 255, 255)"]);
  // The selected customer's field-like strip takes the same white.
  expect(read.strip).toBe("rgb(255, 255, 255)");
});

// The charcoal brand half (row 1763) insets its fields one step below the page: #121517.
test("brand theme keeps its inset fields: the input fill is the charcoal field tone", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await pickLayout(page, "panels");
  await pickTheme(page, "Brand");
  const fill = await page.getByTestId("pos-scan-input").evaluate((el) => getComputedStyle(el).backgroundColor);
  expect(fill).toBe("rgb(18, 21, 23)");
  await pickTheme(page, "Light");
});

for (const layout of ["panels", "stepper"] as const) {
  test(`a popped window keeps the ${layout} layout, hides Pop out and keeps Sign out`, async ({ page, context }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await signIn(page);
    await pickLayout(page, layout);
    if (layout === "panels") await page.getByTestId("swap-panes").click();
    await expect(page.getByRole("button", { name: "Pop out" })).toBeVisible();
    // The server's workspace row does not carry the pick to the new window (a save not yet landed, a
    // second mint): the window must keep the layout from what the opener told it, not from the row.
    await context.route("**/api/v1/pos/workspace**", (route) =>
      route.request().method() === "GET" ? route.fulfill({ status: 500, body: "{}" }) : route.continue(),
    );

    const poppedPromise = context.waitForEvent("page");
    await page.getByRole("button", { name: "Pop out" }).click();
    const popped = await poppedPromise;
    await popped.waitForLoadState();
    await expect(popped.getByTestId("pos-page-title")).toHaveText("New Counter Sale");

    if (layout === "panels") {
      await expect(popped.getByTestId("pos-twopane")).toBeVisible();
      await expect(popped.getByTestId("swap-panes")).toHaveAttribute("aria-pressed", "true");
    } else {
      await expect(popped.getByTestId("pos-twopane")).toHaveCount(0);
      await expect(popped.getByTestId("swap-panes")).toHaveCount(0);
    }
    await expect(popped.getByRole("button", { name: "Pop out" })).toHaveCount(0);
    await openAccountMenu(popped);
    await expect(popped.getByRole("button", { name: "Sign out" })).toBeVisible();
    await closeAccountMenu(popped);
    // The opener keeps its own Pop out.
    await expect(page.getByRole("button", { name: "Pop out" })).toBeVisible();
    if (layout === "panels") await page.getByTestId("swap-panes").click();
    await popped.close();
  });
}

test("a popped window never saves the layout its opener told it, even when the workspace read fails", async ({
  page,
  context,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await pickLayout(page, "panels");
  await context.route("**/api/v1/pos/workspace**", (route) =>
    route.request().method() === "GET" ? route.fulfill({ status: 500, body: "{}" }) : route.continue(),
  );
  const poppedPromise = context.waitForEvent("page");
  await page.getByRole("button", { name: "Pop out" }).click();
  const popped = await poppedPromise;
  const puts: string[] = [];
  let rereads = 0;
  popped.on("request", (request) => {
    if (!request.url().includes("/api/v1/pos/workspace")) return;
    if (request.method() === "PUT") puts.push(request.postData() ?? "");
    if (request.method() === "GET") rereads += 1;
  });
  await expect(popped.getByTestId("pos-twopane")).toBeVisible();
  // Another workspace gesture (the results view flip) saves through the failed re-read: no save may
  // carry the hinted layout (it is held back until the row reads again).
  await popped.getByTestId("pos-browse").locator("summary").click();
  rereads = 0; // the window's own load read is behind us; only the gesture's save counts from here
  const flip = popped.locator('[data-testid^="lookup-view-"][aria-pressed="false"]').first();
  await flip.click();
  await expect(popped.locator('[data-testid^="lookup-view-"][aria-pressed="false"]')).toHaveCount(1);
  // A save DID fire (it re-read the stored row, which is refused here), so the hold back is what kept
  // the PUT away, not a save that never ran; and no PUT carries the hinted layout.
  await expect.poll(() => rereads, { timeout: 5000 }).toBeGreaterThan(0);
  await popped.waitForTimeout(500);
  expect(puts, "the held back save wrote nothing while the row could not be read").toEqual([]);
  for (const body of puts) expect(body).not.toContain('"counter_layout":"PANELS"');
  await popped.close();
});

test("a popped window's own unsaved choices are saved once the workspace read recovers, under the stored layout", async ({
  page,
  context,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await pickLayout(page, "panels");
  let refusing = true;
  await context.route("**/api/v1/pos/workspace**", (route) =>
    refusing && route.request().method() === "GET" ? route.fulfill({ status: 500, body: "{}" }) : route.continue(),
  );
  const poppedPromise = context.waitForEvent("page");
  await page.getByRole("button", { name: "Pop out" }).click();
  const popped = await poppedPromise;
  const puts: string[] = [];
  popped.on("request", (request) => {
    if (request.method() === "PUT" && request.url().includes("/api/v1/pos/workspace")) puts.push(request.postData() ?? "");
  });
  await expect(popped.getByTestId("pos-twopane")).toBeVisible();
  // The person's own choice in the popped window (the results view flip) while the row cannot be read.
  await popped.getByTestId("pos-browse").locator("summary").click();
  const pressedBefore = await popped.locator('[data-testid^="lookup-view-"][aria-pressed="true"]').getAttribute("data-testid");
  await popped.locator('[data-testid^="lookup-view-"][aria-pressed="false"]').first().click();
  await popped.waitForTimeout(800);
  expect(puts, "nothing is written while the row cannot be read").toEqual([]);
  // The stored layout moves on elsewhere (the opener goes back to Steps), then the read recovers.
  await pickLayout(page, "stepper");
  refusing = false;
  // The held back choice is saved by itself, without another gesture, over the layout the row holds now:
  // the hinted PANELS is never what lands.
  await expect.poll(() => puts.length, { timeout: 15_000 }).toBeGreaterThan(0);
  const saved = puts[puts.length - 1] ?? "";
  expect(saved).toContain('"counter_layout":"STEPPER"');
  expect(saved).toContain(pressedBefore === "lookup-view-cards" ? '"lookup_presentation":"ROWS"' : '"lookup_presentation":"CARDS"');
  await popped.close();
});
