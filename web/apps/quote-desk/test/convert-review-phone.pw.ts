// Convert's review lines on a phone (320, 360, 390 and 480 wide), light and brand: the product name stands on its own row,
// then the quantity, the unit price and the total, each whole. No header, label or figure breaks inside a word or a number,
// and nothing scrolls the page sideways. Measured on what is drawn: every word of every visible text is laid on one line.
import { expect, test, type Page } from "@playwright/test";
import { shot } from "./shot";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function theme(page: Page, word: "Light" | "Brand"): Promise<void> {
  const control = page.getByTestId("theme-control");
  for (let i = 0; i < 3; i += 1) {
    if ((await control.textContent())?.includes(word)) return;
    await control.click();
  }
  await expect(control).toContainText(word);
}

/** The visible words of the review card that a line wrap cuts in two, and its widest overhang past the screen. */
async function broken(page: Page): Promise<{ words: string[]; texts: number }> {
  return page.getByTestId("qc-review").evaluate((card) => {
    const words: string[] = [];
    let texts = 0;
    const walk = (root: ParentNode): void => {
      for (const el of Array.from(root.querySelectorAll("*"))) {
        if (el.shadowRoot !== null) walk(el.shadowRoot);
        const cs = getComputedStyle(el);
        if (cs.display === "none" || cs.visibility === "hidden") continue;
        for (const node of Array.from(el.childNodes)) {
          if (node.nodeType !== Node.TEXT_NODE) continue;
          const text = node.textContent ?? "";
          const hostBox = (el as HTMLElement).getBoundingClientRect();
          if (hostBox.width === 0 || hostBox.height === 0) continue;
          // A visually hidden header (clipped to a pixel) is not drawn; its words are not measured.
          if (hostBox.width <= 1 || hostBox.height <= 1) continue;
          const re = /\S+/g;
          for (let m = re.exec(text); m !== null; m = re.exec(text)) {
            const range = document.createRange();
            range.setStart(node, m.index);
            range.setEnd(node, m.index + m[0].length);
            const tops = new Set(Array.from(range.getClientRects()).map((r) => Math.round(r.top)));
            texts += 1;
            if (tops.size > 1) words.push(m[0]);
          }
        }
      }
    };
    walk(card);
    return { words, texts };
  });
}

for (const mode of ["Light", "Brand"] as const) {
  for (const width of [320, 360, 390, 480]) {
    test(`Convert's review at ${width} in ${mode}: no header, label or figure breaks inside a word or number`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      const seeded = await page.request.post("/_stub/seed-quote", {
        data: { status: "ACCEPTED", customer_id: "c-fenwick", lines: [{ p: "p-wall-std", q: 4 }, { p: "p-charcoal-60", q: 120 }] },
      });
      const id = ((await seeded.json()) as { id: string }).id;
      await page.goto("/");
      await page.getByRole("button", { name: "Sales", exact: true }).click();
      await expect(page.getByTestId("qb-list")).toBeVisible();
      await theme(page, mode);
      await page.evaluate((quote) => {
        window.location.hash = `#/quotes/${quote}/convert`;
      }, id);
      await expect(page.getByTestId("qc-review")).toBeVisible();
      await expect(page.getByTestId("qc-line")).toHaveCount(2);
      const got = await broken(page);
      expect(got.texts, "the check saw the review's words").toBeGreaterThan(10);
      expect(got.words, "words cut across lines").toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
      if (width === 390) await shot(page, `convert-390-${mode.toLowerCase()}`);
      if (width <= 480) {
        // The name is on its own row: above its quantity, unit price and total, which share the row below.
        const line = page.getByTestId("qc-line").first();
        await expect(line.locator(".lbl")).toHaveText(["Qty", "Unit price", "Total"]);
        for (const label of await line.locator(".lbl").all()) await expect(label).toBeVisible();
        const name = await line.getByTestId("qc-line-name").boundingBox();
        const qty = await line.getByTestId("qc-line-qty").boundingBox();
        const total = await line.getByTestId("qc-line-total").boundingBox();
        expect(name && qty && total, "the line's parts are drawn").toBeTruthy();
        expect(name!.y + name!.height).toBeLessThanOrEqual(qty!.y + 1);
        expect(Math.abs(qty!.y - total!.y)).toBeLessThanOrEqual(2);
      }
    });
  }
}
