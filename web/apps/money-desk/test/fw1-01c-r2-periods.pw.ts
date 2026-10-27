// FW1-01c round 2: the visual read's findings on PR 614, measured in a real browser.
// The Periods tab's own controls reach the 44 px touch bar on a phone, the panel head's buttons sit on
// the heading's centre line, the gap above the table is not doubled, the trailing action cells sit at
// the table's right edge, and the review's own bottom inset matches its top.
//
// Red first: today the Open, Close and Reopen links are 36 px tall on a phone, the head's buttons sit
// 5.5 px above the heading's centre line and the gap above the table is doubled.
//
// Every measure is one layout read inside the desk's shadow root: the page may move under a measurement
// (the review takes focus) and the viewport may change under one (a resize).
import { test, expect, openApprovals, openTab } from "./gl-helpers.js";
import type { Locator } from "@playwright/test";

const PHONE = { width: 390, height: 844 };
const DESK = { width: 1440, height: 900 };

async function measure<T>(locator: Locator, fn: (element: Element) => T): Promise<T> {
  return await locator.evaluate(fn);
}

test("on a phone the late posted Open and the periods Close and Reopen reach 44 by 44", async ({ page, request }) => {
  await page.setViewportSize(PHONE);
  await openApprovals(page, request);
  await openTab(page, "periods");
  const controls = [
    ["the late posted Open", page.getByRole("button", { name: "Open JE-0002" })],
    ["the periods Close", page.getByRole("button", { name: "Close August 2026" })],
    ["the periods Reopen", page.getByRole("button", { name: "Reopen January 2026" })],
  ] as const;
  for (const [what, control] of controls) {
    const size = await measure(control, (element) => {
      const r = element.getBoundingClientRect();
      return { height: r.height, width: r.width };
    });
    expect(size.height, `${what} is ${size.height} px tall`).toBeGreaterThanOrEqual(44);
    expect(size.width, `${what} is ${size.width} px wide`).toBeGreaterThanOrEqual(44);
  }
});

test("the panel head's buttons sit on the heading's centre line", async ({ page, request }) => {
  await page.setViewportSize(DESK);
  await openApprovals(page, request);
  await openTab(page, "periods");
  const offset = await measure(page.locator("[data-periods] .gl-panel-head"), (head) => {
    const heading = head.querySelector("h3");
    const button = head.querySelector(".gl-actions button");
    if (heading === null || button === null) return -1;
    const hr = heading.getBoundingClientRect();
    const br = button.getBoundingClientRect();
    return Math.abs(hr.top + hr.height / 2 - (br.top + br.height / 2));
  });
  expect(offset, `the buttons sit ${offset} px off the heading's centre line`).toBeLessThanOrEqual(2);
});

test("the gap above the table is not doubled", async ({ page, request }) => {
  for (const size of [DESK, PHONE]) {
    await page.setViewportSize(size);
    await openApprovals(page, request);
    await openTab(page, "periods");
    const gap = await measure(page.locator("[data-periods]"), (panel) => {
      const actions = panel.querySelector(".gl-panel-head .gl-actions");
      const table = panel.querySelector("table");
      if (actions === null || table === null) return -1;
      const ar = actions.getBoundingClientRect();
      const tr = table.getBoundingClientRect();
      return tr.top - ar.bottom;
    });
    expect(gap, `the gap above the table is ${gap} px at ${size.width} px wide`).toBeLessThanOrEqual(16);
  }
});

test("the trailing action cells sit at the table's right edge", async ({ page, request }) => {
  for (const size of [DESK, PHONE]) {
    await page.setViewportSize(size);
    await openApprovals(page, request);
    await openTab(page, "periods");
    const insets = await measure(page.locator("[data-periods]"), (panel) => {
      const table = panel.querySelector("table");
      const close = panel.querySelector('[data-action="gl-close-period"]');
      if (table === null || close === null) return { close: -1 };
      const tr = table.getBoundingClientRect();
      const cr = close.getBoundingClientRect();
      return { close: tr.right - cr.right };
    });
    expect(insets.close, `the periods Close sits ${insets.close} px inside the table's right edge`).toBeLessThanOrEqual(20);
    const lateInset = await measure(page.locator("[data-late-posted]"), (panel) => {
      const table = panel.querySelector("table");
      const open = panel.querySelector('[data-action="gl-open-journal"]');
      if (table === null || open === null) return -1;
      const tr = table.getBoundingClientRect();
      const or = open.getBoundingClientRect();
      return tr.right - or.right;
    });
    expect(lateInset, `the late posted Open sits ${lateInset} px inside the table's right edge`).toBeLessThanOrEqual(20);
  }
});

test("the review's own bottom inset matches its top", async ({ page, request }) => {
  await page.setViewportSize(DESK);
  await openApprovals(page, request);
  await openTab(page, "periods");
  await page.locator("[data-periods] .gl-panel-head .gl-actions button").last().click();
  await expect(page.locator("[data-confirm]")).toBeVisible();
  const insets = await measure(page.locator("[data-confirm]"), (panel) => {
    const heading = panel.querySelector("h2");
    const actions = panel.querySelector(".gl-actions");
    if (heading === null || actions === null) return { top: -1, bottom: -1 };
    const pr = panel.getBoundingClientRect();
    const hr = heading.getBoundingClientRect();
    const ar = actions.getBoundingClientRect();
    return { top: hr.top - pr.top, bottom: pr.bottom - ar.bottom };
  });
  expect(
    Math.abs(insets.bottom - insets.top),
    `the review's bottom inset is ${insets.bottom} px and its top inset is ${insets.top} px`,
  ).toBeLessThanOrEqual(4);
});
