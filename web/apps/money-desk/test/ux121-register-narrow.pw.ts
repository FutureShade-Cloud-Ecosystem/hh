// UX-121: the register's narrow reading at 390 px. The posted day must stay on
// one line and the source word must not break in the middle of a word, in the
// light sheet and in the charcoal half alike.
//
// Red first: today the day ("Aug 23, 2026") wraps after the comma under the
// memo, and the source word ADJUSTMENT breaks mid-word.
//
// The legs measure the lines each cell takes, the way a person reads them: a
// range over the cell's own words, one line box per line, so a word that
// straddles two lines is counted as the break it is.
import type { APIRequestContext, Page } from "@playwright/test";
import { test, expect, controllerHandoff, resetStub, openTab } from "./gl-helpers.js";

const THEMES = ["light", "charcoal"] as const;

/** The lines an element's text takes, and every word with the lines it spans. */
async function measured(locator: ReturnType<Page["locator"]>): Promise<{ lines: number; words: { word: string; lines: number }[] }> {
  return locator.evaluate((node) => {
    const tops = (range: Range): Set<number> =>
      new Set(Array.from(range.getClientRects()).map((rect) => Math.round(rect.top)));
    const whole = document.createRange();
    whole.selectNodeContents(node);
    const words: { word: string; lines: number }[] = [];
    const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT);
    let current = walker.nextNode();
    while (current !== null) {
      const text = current.textContent ?? "";
      const pattern = /\S+/g;
      let match = pattern.exec(text);
      while (match !== null) {
        const word = document.createRange();
        word.setStart(current, match.index);
        word.setEnd(current, match.index + match[0].length);
        words.push({ word: match[0], lines: tops(word).size });
        match = pattern.exec(text);
      }
      current = walker.nextNode();
    }
    return { lines: tops(whole).size, words };
  });
}

/** Signs in as the Financial Controller and stands on the register. */
async function openRegister(page: Page, request: APIRequestContext): Promise<void> {
  await resetStub(request);
  await page.addInitScript(controllerHandoff);
  await page.goto("/");
  await expect(page.locator("[data-approval-id]")).toHaveCount(5);
  await openTab(page, "journals");
  await expect(page.getByRole("table", { name: "Journal register" })).toBeVisible();
}

/** The charcoal half: one tap on the header's account menu, then the theme. */
async function charcoalOn(page: Page): Promise<void> {
  await page.locator("hh-shell-header button.account").click();
  await page.getByRole("button", { name: "Theme: Light" }).click();
}

for (const theme of THEMES) {
  test(`at 390 the register keeps every posted day on one line and every source word whole, ${theme}`, async ({ page, request }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 390, height: 844 });
    await openRegister(page, request);
    if (theme === "charcoal") await charcoalOn(page);

    const register = page.getByRole("table", { name: "Journal register" });
    const rows = register.locator("tbody tr");
    const rowCount = await rows.count();
    expect(rowCount, "the register is empty").toBeGreaterThan(0);

    for (let index = 0; index < rowCount; index += 1) {
      const row = rows.nth(index);
      const number = await row.locator(".gl-link").innerText();

      // The posted day, under the memo on a phone: one line, the day whole.
      const date = await measured(row.locator("[data-posted-narrow]"));
      expect.soft(date.lines, `${number}: the posted day takes ${date.lines} lines: ${JSON.stringify(date.words)}`).toBe(1);

      // The source: plain words a bookkeeper reads, never the core's enum.
      const source = await measured(row.locator(".gl-row-detail"));
      expect.soft(source.words.length, `${number}: the source cell says nothing`).toBeGreaterThan(0);
      expect.soft(/^[A-Z][A-Z0-9_]*$/.test(source.words.map((w) => w.word).join(" ")), `${number}: the source is the enum, not a plain word`).toBe(false);
      // And no word of it broken across two lines.
      for (const word of source.words) {
        expect.soft(word.lines, `${number}: the source word "${word.word}" is broken across lines: ${JSON.stringify(source.words)}`).toBe(1);
      }
    }

    // The register still reads without a sideways scroll.
    const overflow = await page.locator(".gl-scroll").first().evaluate((el) => el.scrollWidth - el.clientWidth);
    expect(overflow, "the register scrolls sideways on a phone").toBeLessThanOrEqual(0);
  });
}
