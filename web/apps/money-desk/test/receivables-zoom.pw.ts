// UX-29: the Receivables page at 200 percent zoom (a 720x450 viewport at pixel ratio 2) never scrolls
// sideways and keeps every "Record payment" on screen; on a phone every control on it reaches 44 px.
import { test, expect, openDesk } from "./helpers.js";

test.describe("200 percent zoom", () => {
  test.use({ viewport: { width: 720, height: 450 }, deviceScaleFactor: 2 });

  test("the page does not scroll sideways and every Record payment sits inside the viewport", async ({ page, request }) => {
    await openDesk(page, request);
    await expect(page.locator("[data-register-row]").first()).toBeVisible();
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
    const buttons = page.getByRole("button", { name: "Record payment", exact: true });
    expect(await buttons.count()).toBeGreaterThan(0);
    const width = await page.evaluate(() => document.documentElement.clientWidth);
    for (const box of await buttons.evaluateAll((els) => els.map((el) => { const r = el.getBoundingClientRect(); return { left: r.left, right: r.right }; }))) {
      expect(box.left).toBeGreaterThanOrEqual(0);
      expect(box.right).toBeLessThanOrEqual(width);
    }
  });
});

test.describe("phone touch targets", () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, hasTouch: true, isMobile: true });

  test("every control on the Receivables page is at least 44 px high", async ({ page, request }) => {
    await openDesk(page, request);
    await expect(page.locator("[data-register-row]").first()).toBeVisible();
    await expect.poll(async () => {
      const small = await page.evaluate(() => {
        const out: string[] = [];
        const seen = (el: Element, root: Document | ShadowRoot): void => {
          for (const c of root.querySelectorAll("button, a[href], input, select, textarea, [role='button'], [role='tab'], [role='switch']")) {
            const r = c.getBoundingClientRect();
            if (r.width === 0 || r.height === 0) continue;
            if (r.height < 43.5) out.push(`${(c.textContent ?? "").trim().slice(0, 30) || c.getAttribute("aria-label") || c.tagName} ${Math.round(r.width)}x${Math.round(r.height)}`);
          }
          for (const h of root.querySelectorAll("*")) if (h.shadowRoot) seen(h, h.shadowRoot);
        };
        seen(document.body, document);
        return out;
      });
      return small.join(" | ");
    }).toBe("");
  });
});
