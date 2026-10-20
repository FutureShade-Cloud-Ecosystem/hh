// The contrast gate: every visible text node on every screen, in both themes
// at 390 and 1440, sits at 4.5 to 1 or more against the background it actually
// renders on. The brand theme once shipped black ink on navy (the round 1
// review's P1-1, 21 text styles under the bar, worst 1.42); this leg is the
// one that keeps it from coming back: it walks the light and shadow trees,
// resolves the effective background up the tree (compositing any translucent
// layers), and computes the WCAG ratio the way the reviewer's script did.
import { test, expect, type Page } from "@playwright/test";
import { resetStub, useScenario } from "./helpers.js";

const FLOOR = 4.5;

type Offender = { readonly text: string; readonly ratio: number; readonly ink: string; readonly on: string };

async function measure(page: Page): Promise<readonly Offender[]> {
  return page.evaluate((floor: number) => {
    const offenders: { text: string; ratio: number; ink: string; on: string }[] = [];

    const parse = (value: string): [number, number, number, number] | null => {
      const m = value.match(/rgba?\(([^)]+)\)/);
      if (m === null) return null;
      const parts = m[1].split(",").map((s) => Number.parseFloat(s));
      if (parts.length < 3 || parts.slice(0, 3).some((n) => Number.isNaN(n))) return null;
      return [parts[0], parts[1], parts[2], parts.length > 3 && !Number.isNaN(parts[3]) ? parts[3] : 1];
    };

    // top over bottom, both premultiplied onto an opaque result.
    const over = (top: [number, number, number, number], bottom: [number, number, number, number]): [number, number, number, number] => {
      const a = top[3] + bottom[3] * (1 - top[3]);
      if (a === 0) return [0, 0, 0, 1];
      const mix = (t: number, b: number): number => (top[3] * t + bottom[3] * (1 - top[3]) * b) / a;
      return [mix(top[0], bottom[0]), mix(top[1], bottom[1]), mix(top[2], bottom[2]), 1];
    };

    const parentOf = (node: Element): Element | null => {
      const parent = node.parentElement;
      if (parent !== null) return parent;
      const root = node.getRootNode();
      return root instanceof ShadowRoot ? root.host : null;
    };

    // The first opaque background up the tree, with any translucent layers
    // composited over it. Transparent layers are skipped, never composited
    // (two transparents over one another stay transparent, and treating them
    // as ink would paint every unstyled page black); a tree that never sets
    // one falls back to white, the browser's own canvas, never to a silent
    // pass.
    const backdrop = (el: Element): [number, number, number, number] => {
      let acc: [number, number, number, number] | null = null;
      let node: Element | null = el;
      while (node !== null) {
        const layer = parse(getComputedStyle(node).backgroundColor);
        if (layer !== null && layer[3] > 0) {
          acc = acc === null ? layer : over(acc, layer);
          if (acc[3] >= 1) return acc;
        }
        node = parentOf(node);
      }
      return acc === null ? [255, 255, 255, 1] : over(acc, [255, 255, 255, 1]);
    };

    const channel = (v: number): number => {
      const s = v / 255;
      return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
    };
    const luminance = (c: [number, number, number, number]): number =>
      0.2126 * channel(c[0]) + 0.7152 * channel(c[1]) + 0.0722 * channel(c[2]);
    const ratio = (a: [number, number, number, number], b: [number, number, number, number]): number => {
      const la = luminance(a);
      const lb = luminance(b);
      return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
    };

    const hidden = (el: Element): boolean => {
      if (el.classList.contains("visually-hidden")) return true;
      const style = getComputedStyle(el);
      if (style.display === "none" || style.visibility === "hidden") return true;
      const rect = el.getBoundingClientRect();
      return rect.width < 1 || rect.height < 1;
    };

    const walk = (root: Element | ShadowRoot | Document): void => {
      const all = root.querySelectorAll("*");
      for (const el of Array.from(all)) {
        if (el.shadowRoot !== null) walk(el.shadowRoot);
        if (hidden(el)) continue;
        const hasDirectText = Array.from(el.childNodes).some(
          (n) => n.nodeType === Node.TEXT_NODE && (n.textContent ?? "").trim() !== "",
        );
        if (!hasDirectText) continue;
        const ink = parse(getComputedStyle(el).color);
        if (ink === null) continue;
        const bg = backdrop(el);
        const r = ratio(ink, bg);
        if (r < floor) {
          const text = (el.textContent ?? "").trim().replace(/\s+/g, " ").slice(0, 40);
          offenders.push({ text, ratio: Math.round(r * 100) / 100, ink: ink.join(" "), on: bg.join(" ") });
        }
      }
    };

    walk(document);
    return offenders;
  }, FLOOR);
}

async function check(page: Page, where: string): Promise<void> {
  const offenders = await measure(page);
  const shown = offenders.map((o) => `${where}: "${o.text}" at ${o.ratio} to 1 (ink ${o.ink} on ${o.on})`).join("; ");
  expect(offenders, shown).toEqual([]);
}

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];

for (const size of SIZES) {
  for (const theme of ["light", "brand"] as const) {
    test(`every visible text holds 4.5 to 1 at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await resetStub(request);
      await useScenario(request, "plain-profile");

      // The way in, unsigned.
      await page.goto("/");
      if (theme === "brand") {
        await page.locator("hh-shell-header button.account").click();
        await page.getByRole("button", { name: "Brand theme" }).click();
      }
      await expect(page.locator("[data-flow='yard-manager']")).toBeVisible();
      await check(page, "the way in");

      // The landing, signed in as the yard manager through the card.
      await page.locator("[data-flow='yard-manager']").click();
      await expect(page.locator("[data-widget='needs.you']")).toBeVisible();
      await check(page, "the landing");

      // Shortages, with the resolve panel open on the first shortage.
      await page.locator("[data-needs='tasks.open_shortages'] button", { hasText: "Resolve" }).click();
      await expect(page.getByRole("heading", { name: "Shortages · open" })).toBeVisible();
      await check(page, "shortages");
      await page.locator(".shortage[data-shortage]:has([data-action='open-resolve'])").first().getByRole("button", { name: "Resolve" }).click();
      await expect(page.locator("[data-resolving='true']")).toHaveCount(1);
      await check(page, "the resolve panel");
      await page.getByRole("button", { name: "Backorder the shortfall" }).click();
      await check(page, "the resolve panel with a choice picked");

      // Receiving, with its definition note and the open value line.
      await page.getByRole("button", { name: "Back to Home" }).click();
      await page.locator(".view-link", { hasText: "Receiving · short" }).click();
      await expect(page.locator("[data-po]").first()).toBeVisible();
      await check(page, "receiving");

      // Approvals, on the kind the yard manager decides none of.
      await page.getByRole("button", { name: "Back to Home" }).click();
      await page.locator(".view-link", { hasText: "Approvals" }).click();
      await expect(page.locator("[data-test='approvals-refusal']")).toBeVisible();
      await check(page, "approvals");

      // Change orders, the new HH-681 tab, in both themes.
      await page.locator(".kind-tab", { hasText: "Change orders" }).click();
      await expect(page.locator(".kind-tab[aria-selected='true']")).toContainText("Change orders");
      await check(page, "change orders");
    });
  }
}
