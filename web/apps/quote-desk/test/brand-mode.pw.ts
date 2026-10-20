// Brand mode A (decision log rows 1119 and 1123): the theme control reads Auto, Light, Brand, and the Quote Desk's four
// main screens (the list, a quote, New quote, Convert) read at AA in brand mode, at the desk's two sizes. The pairs are
// measured on what is drawn: every visible text run against the first opaque background above it, 4.5:1 for body text and
// 3:1 for large text. The screenshots are the evidence under the tracker's qd-brand folder when QD_BRAND_SHOTS names it.
import { expect, test, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { showList, signIn } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const SIZES = [
  { name: "1440", width: 1440, height: 900 },
  { name: "390", width: 390, height: 844 },
] as const;

async function openAccountMenu(page: Page): Promise<void> {
  const menu = page.locator("hh-shell-header .account-menu");
  if (!(await menu.isVisible())) await page.locator("hh-shell-header button.account").click();
}

async function brand(page: Page): Promise<void> {
  const control = page.getByTestId("theme-control");
  await openAccountMenu(page);
  for (let i = 0; i < 3; i += 1) {
    if ((await control.textContent())?.includes("Brand")) {
      await page.keyboard.press("Escape");
      return;
    }
    await control.click();
  }
  await expect(control).toContainText("Brand");
  await page.keyboard.press("Escape");
}

/** Every visible text run below the bar's measure: [text, ratio, needed], worst first, only the ones short of the bar. */
async function short(page: Page): Promise<{ bad: string[]; measured: number }> {
  return page.evaluate(() => {
    const parse = (c: string): [number, number, number, number] => {
      const m = c.match(/rgba?\(([^)]+)\)/);
      if (!m) return [0, 0, 0, 0];
      const p = m[1]!.split(/[ ,/]+/).filter(Boolean).map(Number);
      return [p[0]!, p[1]!, p[2]!, p[3] ?? 1];
    };
    const lum = (r: number, g: number, b: number): number => {
      const f = (v: number): number => {
        const s = v / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const parentOf = (el: Element): Element | null => el.parentElement ?? ((el.getRootNode() as ShadowRoot).host ?? null);
    const backdrop = (el: Element): [number, number, number] | null => {
      let layers: [number, number, number, number][] = [];
      for (let n: Element | null = el; n; n = parentOf(n)) {
        const cs = getComputedStyle(n);
        if (cs.backgroundImage !== "none") return null;
        const c = parse(cs.backgroundColor);
        if (c[3] > 0) layers.push(c);
        if (c[3] === 1) break;
      }
      let [r, g, b] = [255, 255, 255];
      const root = parse(getComputedStyle(document.documentElement).backgroundColor);
      if (layers.length === 0 || layers[layers.length - 1]![3] < 1) [r, g, b] = [root[0], root[1], root[2]];
      for (const l of layers.reverse()) {
        r = l[0] * l[3] + r * (1 - l[3]);
        g = l[1] * l[3] + g * (1 - l[3]);
        b = l[2] * l[3] + b * (1 - l[3]);
      }
      return [r, g, b];
    };
    const out: [string, number][] = [];
    let measured = 0;
    const walk = (root: ParentNode): void => {
      for (const el of Array.from(root.querySelectorAll("*"))) {
        if (el.shadowRoot) walk(el.shadowRoot);
        const own = [...el.childNodes].filter((n) => n.nodeType === Node.TEXT_NODE && (n.textContent ?? "").trim() !== "");
        if (own.length === 0) continue;
        const cs = getComputedStyle(el);
        const box = el.getBoundingClientRect();
        if (cs.visibility === "hidden" || cs.display === "none" || box.width === 0 || box.height === 0 || Number(cs.opacity) < 1) continue;
        const back = backdrop(el);
        if (!back) continue;
        const fg = parse(cs.color);
        const mix = (i: number): number => fg[i]! * fg[3] + back[i]! * (1 - fg[3]);
        const a = lum(mix(0), mix(1), mix(2));
        const b = lum(back[0], back[1], back[2]);
        const ratio = (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
        const px = parseFloat(cs.fontSize);
        const large = px >= 24 || (px >= 18.66 && Number(cs.fontWeight) >= 700);
        const need = large ? 3 : 4.5;
        measured += 1;
        const disabled = (el as HTMLButtonElement).disabled === true || el.closest("[disabled]") !== null;
        if (ratio < need && !disabled) out.push([`${(own[0]!.textContent ?? "").trim().slice(0, 40)} ${ratio.toFixed(2)} < ${need}`, ratio]);
      }
    };
    walk(document);
    return { bad: out.sort((x, y) => x[1] - y[1]).map((x) => x[0]), measured };
  });
}

async function look(page: Page, name: string, size: string): Promise<void> {
  const got = await short(page);
  expect(got.measured, `${name} at ${size}: the audit saw the screen's text`).toBeGreaterThan(15);
  expect(got.bad, `${name} at ${size}: text pairs short of AA in brand mode`).toEqual([]);
  const dir = process.env.QD_BRAND_SHOTS;
  if (dir) {
    mkdirSync(dir, { recursive: true });
    await page.screenshot({ path: `${dir}/${name}-${size}.png`, fullPage: false });
  }
}

for (const size of SIZES) {
  test(`brand mode at ${size.name}: the list, a quote, New quote and Convert read at AA`, async ({ page }) => {
    await page.setViewportSize({ width: size.width, height: size.height });
    await signIn(page);
    await brand(page);
    await openAccountMenu(page);
    await expect(page.getByTestId("theme-control")).toContainText("Brand");
    await page.keyboard.press("Escape");
    await showList(page);
    await look(page, "list", size.name);
    await page.getByTestId("qb-list-row").filter({ hasText: "Q-1003" }).first().click();
    await expect(page.locator("qd-order-payment")).toBeVisible();
    await look(page, "quote", size.name);
    await page.locator("qd-order-payment").getByTestId("op-convert").click();
    await expect(page.getByTestId("qc-title")).toBeVisible();
    await look(page, "convert", size.name);
    await page.evaluate(() => { location.hash = "#/quotes"; });
    await expect(page.getByTestId("qd-list-toolbar")).toBeVisible();
    await page.getByRole("button", { name: "New quote", exact: true }).click();
    await expect(page.getByTestId("qb-identity")).toBeVisible();
    await look(page, "new-quote", size.name);
  });
}

test("no theme word a person sees says Dark", async ({ page }) => {
  await signIn(page);
  await openAccountMenu(page);
  const control = page.getByTestId("theme-control");
  const seen: string[] = [];
  for (let i = 0; i < 3; i += 1) {
    seen.push((await control.textContent()) ?? "", (await control.getAttribute("title")) ?? "", (await control.getAttribute("aria-label")) ?? "");
    await control.click();
  }
  expect(seen.join("|")).not.toMatch(/dark/i);
});
