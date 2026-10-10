// Round 10 item 6 (row 1034): "Include product images" is one line at the desktop and phone widths, not squeezed
// into three beside a stretched checkbox.
import { expect, test } from "@playwright/test";
import { open, signIn } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

for (const width of [1440, 390]) {
  test(`Include product images is one line at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await signIn(page);
    await open(page, "Q-1001");
    const box = page.getByTestId("qb-terms-images").locator("xpath=ancestor::label[1]");
    await expect(box).toBeVisible();
    // The words sit on one line: the label's text has one line box.
    const lines = await box.evaluate((el) => {
      const text = [...el.childNodes].find((n) => n.nodeType === Node.TEXT_NODE && (n.textContent ?? "").trim() !== "");
      if (text === undefined) return 0;
      const range = document.createRange();
      range.selectNodeContents(text);
      return new Set([...range.getClientRects()].map((r) => Math.round(r.top))).size;
    });
    expect(lines, "one line of words").toBe(1);
    const check = (await page.getByTestId("qb-terms-images").boundingBox())!;
    expect(check.width, "the checkbox is a checkbox, not a stretched field").toBeLessThanOrEqual(24);
  });
}
