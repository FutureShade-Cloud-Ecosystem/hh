// Round 10 item 23 (row 1092): the focus ring on the quote's title does not overlap the subtitle under it.
import { expect, test } from "@playwright/test";
import { open, signIn } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the title's focus ring stays above the subtitle, light and brand", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  for (const theme of ["light", "dark"]) {
    await page.evaluate((mode) => document.documentElement.setAttribute("data-theme", mode), theme);
    const gap = await page.getByTestId("qb-title").evaluate((title) => {
      (title as HTMLElement).focus();
      const style = getComputedStyle(title);
      const ring = (parseFloat(style.outlineOffset) || 0) + (parseFloat(style.outlineWidth) || 0);
      const subtitle = title.parentElement?.querySelector('[data-testid="qb-context"]') as HTMLElement;
      return subtitle.getBoundingClientRect().top - (title.getBoundingClientRect().bottom + ring);
    });
    expect(gap, `${theme}: the ring's lower edge is above the subtitle's top`).toBeGreaterThanOrEqual(0);
  }
});
