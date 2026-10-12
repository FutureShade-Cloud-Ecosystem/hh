// The fit leg, added in round 2: no screen and no state of the app may scroll
// sideways at 390. The visual review measured the pick screen at 654 px with
// the line editor open and 407 px after a short pick saves or the pick stages;
// this leg walks every screen and every state the app can reach and fails
// naming each one that overflows, in both themes (the fit is a layout fact
// and should not depend on the sheet). It also holds every control to the
// app's own 56 px floor, the other half of a screen that fits a gloved hand.
import { test, expect, type Page } from "@playwright/test";
import { chooseReason, resetStub, useScenario, yardHandoff } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

async function noSideways(page: Page, where: string): Promise<void> {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect.soft(overflow, `${where}: the page runs ${overflow} px past the frame`).toBeLessThanOrEqual(0);
  const small = await page.evaluate((floor) => {
    const out: string[] = [];
    const walk = (root: Document | ShadowRoot): void => {
      for (const el of Array.from(root.querySelectorAll("button, input, textarea, select, a[href], [role=radio]"))) {
        if (el instanceof HTMLInputElement && el.type === "hidden") continue;
        const box = el.getBoundingClientRect();
        if (box.width === 0 || box.height === 0) continue;
        if (Math.round(box.height) < floor) {
          const name = (el.getAttribute("aria-label") ?? el.textContent ?? el.getAttribute("data-field") ?? "").trim().replace(/\s+/g, " ").slice(0, 40);
          out.push(`${name || el.tagName} ${Math.round(box.width)} by ${Math.round(box.height)}`);
        }
      }
      for (const el of Array.from(root.querySelectorAll("*"))) if (el.shadowRoot !== null) walk(el.shadowRoot);
    };
    walk(document);
    return out;
  }, 56);
  expect.soft(small, `${where}: controls under the app's own 56 px floor`).toEqual([]);
}

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

test("every screen and state fits the 390 frame, in both themes", async ({ page, request }) => {
  for (const theme of ["light", "brand"] as const) {
    // Each theme walks the morning world again: the first walk stages a pick.
    await resetStub(request);
    // The landing.
    await page.goto("/");
    await expect(page.locator("[data-pick-card]")).toHaveCount(5);
    if (theme === "brand") await page.getByRole("button", { name: "Brand theme" }).click();
    await noSideways(page, `${theme}: the queue`);

    // The held order, scrolled to.
    await page.locator("[data-pick-card]", { hasText: "SO-1044" }).scrollIntoViewIfNeeded();
    await noSideways(page, `${theme}: the held order`);

    // A pick opened and started.
    await page.locator("[data-pick-card]", { hasText: "SO-1040" }).getByRole("button", { name: "Open" }).click();
    await page.getByRole("button", { name: "Start picking" }).click();
    await expect(page.locator("[data-task-status]")).toHaveText("Picking");
    await noSideways(page, `${theme}: the pick started`);

    // The line editor open on a short line, before and after the reason.
    await page.locator("[data-line]", { hasText: "Class 2 Base Rock gravel" }).locator(".line-row").click();
    for (let i = 0; i < 4; i += 1) await page.getByRole("button", { name: "Add one on" }).click();
    await noSideways(page, `${theme}: the line editor open`);
    await chooseReason(page, "line", "NOT_IN_YARD");
    await noSideways(page, `${theme}: the line editor with the reason chosen`);

    // The saved short pick, the state the review found at 407 px.
    await page.getByRole("button", { name: "Done" }).click();
    await page.getByRole("button", { name: "Save pick" }).click();
    await expect(page.locator("[data-notice]")).toContainText("on backorder");
    await noSideways(page, `${theme}: the saved short pick`);

    // The skid stepper with the reason rows open.
    await page.getByRole("button", { name: "One skid more" }).click();
    await noSideways(page, `${theme}: the skid stepper changed`);
    await chooseReason(page, "skid", "COUNT_CORRECTION");
    await page.getByRole("button", { name: "Save skid count" }).click();
    await expect(page.locator("[data-notice]")).toContainText("now holds");
    await noSideways(page, `${theme}: the skid count saved`);

    // The shortage sheet, with a typed quantity.
    await page.getByRole("button", { name: "Report a shortage" }).click();
    await expect(page.locator("[data-shortage-sheet]")).toBeVisible();
    await page.getByLabel("How many are short").fill("3");
    await chooseReason(page, "sheet", "DAMAGED");
    await noSideways(page, `${theme}: the shortage sheet open`);

    // The shortage list with the resolve form open.
    await page.locator("[data-shortage-sheet]").getByRole("button", { name: /^(Cancel|Never mind)$/ }).click();
    // The pick that already holds the shortages: SO-1041.
    await page.getByRole("button", { name: "Back to the queue" }).click();
    await page.locator("[data-pick-card]", { hasText: "SO-1041" }).getByRole("button", { name: "Open" }).click();
    await expect(page.locator("[data-pick-order]")).toHaveText("SO-1041");
    await noSideways(page, `${theme}: the pick that carries shortages`);
    await page.locator("[data-shortage]", { hasText: "22 SQFT" }).scrollIntoViewIfNeeded();
    await noSideways(page, `${theme}: the shortages`);
    await page.locator("[data-shortage]", { hasText: "22 SQFT" }).getByRole("button", { name: "Mark it settled" }).click();
    await noSideways(page, `${theme}: the resolve form open`);

    // Staged.
    await page.getByRole("button", { name: "Back to the queue" }).click();
    await page.locator("[data-pick-card]", { hasText: "SO-1040" }).getByRole("button", { name: "Open" }).click();
    await page.getByLabel("The stage location").fill("Bay 3");
    await page.getByRole("button", { name: "Stage", exact: true }).click();
    await expect(page.locator("[data-notice]")).toContainText("Staged at Bay 3");
    await noSideways(page, `${theme}: staged`);

    // The board after.
    await page.getByRole("button", { name: "Back to the queue" }).click();
    await expect(page.locator("[data-pick-card]", { hasText: "SO-1040" }).locator("[data-task-status]")).toHaveText("Staged");
    await noSideways(page, `${theme}: the queue after`);
  }
});

test("the way in, with no handoff, fits the frame", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  await page.goto("/");
  await expect(page.locator("[data-flow]")).toHaveCount(1);
  await noSideways(page, "the way in");
  await context.close();
});

test("the empty branch fits the frame", async ({ page, request }) => {
  await useScenario(request, "empty");
  await page.goto("/");
  await expect(page.locator("[data-empty]")).toBeVisible();
  await noSideways(page, "the empty branch");
});

test("a login off the yard fits the frame", async ({ page, request }) => {
  await useScenario(request, "not-yard");
  await page.goto("/");
  await expect(page.locator("[data-error]")).toContainText("not set up on a yard");
  await noSideways(page, "the not on a yard error");
});
