// The contrast leg, added in round 2: every word a picker reads on a screen
// stands at 4.5 to 1 or more against the surface it is painted on, in both
// themes. The review measured "Report a shortage" at 4.19 to 1 in light; this
// leg computes the ratio for every visible text node, through shadow roots,
// against the composited background behind it, and fails naming every offender
// with its words and colours.
import { test, expect, type Page } from "@playwright/test";
import { chooseReason, resetStub, yardHandoff } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

interface Offender {
  ratio: number;
  text: string;
  fg: string;
  bg: string;
}

async function offenders(page: Page): Promise<Offender[]> {
  return page.evaluate(() => {
    const parse = (value: string): [number, number, number, number] => {
      const m = /^rgba?\(([\d.]+),\s*([\d.]+),\s*([\d.]+)(?:,\s*([\d.]+))?\)$/.exec(value.trim());
      if (m !== null) return [Number(m[1]), Number(m[2]), Number(m[3]), m[4] === undefined ? 1 : Number(m[4])];
      // Chrome answers a mixed or alpha colour in the colour() form.
      const c = /^color\(srgb\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)(?:\s*\/\s*([\d.]+%?))?\)$/.exec(value.trim());
      if (c === null) throw new Error(`a colour the leg cannot read: ${value}`);
      const alpha = c[4] === undefined ? 1 : c[4].endsWith("%") ? Number(c[4].slice(0, -1)) / 100 : Number(c[4]);
      return [Number(c[1]) * 255, Number(c[2]) * 255, Number(c[3]) * 255, alpha];
    };
    const lum = (r: number, g: number, b: number): number => {
      const f = (c: number): number => {
        const v = c / 255;
        return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    // The flat ancestor chain of an element, crossing shadow boundaries and
    // following a slotted element into the slot it is painted under.
    const chain = (el: Element): Element[] => {
      const out: Element[] = [];
      let node: Element | null = el;
      while (node !== null) {
        out.push(node);
        const root = node.getRootNode();
        node = node.assignedSlot ?? node.parentElement ?? (root instanceof ShadowRoot ? root.host : null);
      }
      return out;
    };
    const backgroundUnder = (el: Element): [number, number, number] => {
      const layers: Array<[number, number, number, number]> = [];
      for (const node of chain(el)) {
        const bg = parse(getComputedStyle(node).backgroundColor);
        if (bg[3] > 0) layers.push(bg);
      }
      const base = parse(getComputedStyle(document.documentElement).backgroundColor);
      let [r, g, b] = [base[0], base[1], base[2]];
      for (let i = layers.length - 1; i >= 0; i -= 1) {
        const layer = layers[i] as [number, number, number, number];
        r = r * (1 - layer[3]) + layer[0] * layer[3];
        g = g * (1 - layer[3]) + layer[1] * layer[3];
        b = b * (1 - layer[3]) + layer[2] * layer[3];
      }
      return [r, g, b];
    };
    const out: Offender[] = [];
    const seen = new Set<string>();
    const walk = (root: Document | ShadowRoot): void => {
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
        const el = node.parentElement;
        if (el === null) continue;
        const text = (node.textContent ?? "").trim();
        if (text === "") continue;
        if (el.closest("[aria-hidden=true]") !== null) continue;
        if (el.getClientRects().length === 0) continue;
        const style = getComputedStyle(el);
        if (style.visibility === "hidden" || style.display === "none") continue;
        const fg = parse(style.color);
        const bg = backgroundUnder(el);
        const l1 = lum(fg[0], fg[1], fg[2]);
        const l2 = lum(bg[0], bg[1], bg[2]);
        const ratio = (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
        if (ratio < 4.5) {
          const key = `${text}:${style.color}`;
          if (!seen.has(key)) {
            seen.add(key);
            out.push({ ratio: Math.round(ratio * 100) / 100, text: text.slice(0, 60), fg: style.color, bg: `rgb(${Math.round(bg[0])}, ${Math.round(bg[1])}, ${Math.round(bg[2])})` });
          }
        }
      }
      for (const el of Array.from(root.querySelectorAll("*"))) {
        if (el.shadowRoot !== null) walk(el.shadowRoot);
      }
    };
    walk(document);
    return out.sort((a, b) => a.ratio - b.ratio);
  });
}

async function expectReadable(page: Page, where: string): Promise<void> {
  const found = await offenders(page);
  expect.soft(
    found.map((o) => `${o.ratio}:1 "${o.text}" ${o.fg} on ${o.bg}`),
    `${where}: text under 4.5 to 1`,
  ).toEqual([]);
}

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

test("every word on the walked screens reads at 4.5 to 1 or more, in both themes", async ({ page, request }) => {
  for (const theme of ["light", "brand"] as const) {
    // Each theme walks the morning world again: the first walk stages a pick.
    await resetStub(request);
    await page.goto("/");
    await expect(page.locator("[data-pick-card]")).toHaveCount(5);
    if (theme === "brand") await page.getByRole("button", { name: "Brand theme" }).click();
    await expectReadable(page, `${theme}: the queue`);

    await page.locator("[data-pick-card]", { hasText: "SO-1040" }).getByRole("button", { name: "Open" }).click();
    await page.getByRole("button", { name: "Start picking" }).click();
    await expect(page.locator("[data-task-status]")).toHaveText("Picking");
    await expectReadable(page, `${theme}: the pick started`);

    await page.locator("[data-line]", { hasText: "Granular A gravel" }).locator(".line-row").click();
    for (let i = 0; i < 4; i += 1) await page.getByRole("button", { name: "Add one on" }).click();
    await chooseReason(page, "line", "NOT_IN_YARD");
    await expectReadable(page, `${theme}: the line editor with the reason chosen`);

    await page.getByRole("button", { name: "Done" }).click();
    await page.getByRole("button", { name: "Save pick" }).click();
    await expect(page.locator("[data-notice]")).toContainText("on backorder");
    await expectReadable(page, `${theme}: the saved short pick`);

    // The door under a hand: the hover fill is part of the surface the words sit on.
    await page.getByRole("button", { name: "Report a shortage" }).hover();
    await expectReadable(page, `${theme}: the shortage door under a hand`);
    await page.getByRole("button", { name: "Report a shortage" }).click();
    await expect(page.locator("[data-shortage-sheet]")).toBeVisible();
    await page.getByLabel("How many are short").fill("3");
    await chooseReason(page, "sheet", "DAMAGED");
    await expectReadable(page, `${theme}: the shortage sheet open`);

    await page.locator("[data-shortage-sheet]").getByRole("button", { name: /^(Cancel|Never mind)$/ }).click();
    await page.getByRole("button", { name: "Back to the queue" }).click();
    await page.locator("[data-pick-card]", { hasText: "SO-1041" }).getByRole("button", { name: "Open" }).click();
    await expect(page.locator("[data-pick-order]")).toHaveText("SO-1041");
    await expectReadable(page, `${theme}: the pick that carries shortages`);
    await page.locator("[data-shortage]", { hasText: "22 SQFT" }).getByRole("button", { name: "Mark it settled" }).click();
    await expectReadable(page, `${theme}: the resolve form open`);

    await page.getByRole("button", { name: "Back to the queue" }).click();
    await page.locator("[data-pick-card]", { hasText: "SO-1040" }).getByRole("button", { name: "Open" }).click();
    await page.getByLabel("The stage location").fill("Bay 3");
    await page.getByRole("button", { name: "Stage", exact: true }).click();
    await expect(page.locator("[data-notice]")).toContainText("Staged at Bay 3");
    await expectReadable(page, `${theme}: staged`);
  }
});
