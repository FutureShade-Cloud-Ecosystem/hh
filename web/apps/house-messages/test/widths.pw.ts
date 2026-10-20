// One codebase, two widths: the Work shell's two panes at 1440 and the Field shell's one column at
// 390, with a Back control, 44 pixel targets, no sideways scroll, and the brand theme one tap away.
import { test, expect, accountItem, openAs, channel, CH, says } from "./helpers.js";
import type { Page } from "@playwright/test";

async function boxes(page: Page, selector: string): Promise<{ x: number; y: number; width: number; height: number }> {
  const box = await page.locator(selector).first().boundingBox();
  if (box === null) throw new Error(`${selector} is not on screen`);
  return box;
}

test("at 1440 the channel rail sits left of the conversation, both on screen", async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  const rail = await boxes(page, "[data-channel-list]");
  const convo = await boxes(page, "[data-conversation]");
  expect(rail.x + rail.width).toBeLessThanOrEqual(convo.x);
  expect(Math.abs(rail.y - convo.y)).toBeLessThan(2);
  await expect(page.locator("[data-action='back']")).toBeHidden();
});

test("at 390 the screen is one column: the list, then a channel with Back to the list", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openAs(page, request, "sales");
  await expect(page.locator("[data-channel-list]")).toBeVisible();
  await expect(page.locator("[data-conversation-empty]")).toBeHidden();
  await channel(page, CH.yard).click();
  await expect(page.locator("[data-conversation]")).toBeVisible();
  await expect(page.locator("[data-channel-list]")).toBeHidden();
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await expect(page.locator("[data-channel-list]")).toBeVisible();
  await expect(page.locator("[data-conversation]")).toBeHidden();
});

test("at 390 every control in the desk is at least 44 pixels tall, in the list, a room, the sheet and the bubble", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openAs(page, request, "sales");
  const small = async (): Promise<string[]> =>
    page.evaluate(() => {
      const host = document.querySelector("house-messages-app");
      const root = host?.shadowRoot;
      const out: string[] = [];
      for (const el of Array.from(root?.querySelectorAll("main button, main textarea") ?? [])) {
        const r = (el as HTMLElement).getBoundingClientRect();
        if (r.width === 0 || r.height === 0) continue;
        if (r.height < 43.5) out.push(`${el.getAttribute("data-action") ?? el.tagName}: ${Math.round(r.height)}px`);
      }
      return out;
    });
  expect(await small(), "the list and the bubble").toEqual([]);
  await channel(page, CH.house).click();
  await expect(page.locator("[data-queue]")).toBeVisible();
  expect(await small(), "a house room with its queue").toEqual([]);
  await page.getByRole("button", { name: "Notifications" }).click();
  await expect(page.locator("[data-prefs]")).toBeVisible();
  expect(await small(), "the notification sheet").toEqual([]);
});

test("at 390 a long unbroken word does not make the page scroll sideways", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();
  await says(request, "yard-manager", CH.branch, `Order${"SO1042".repeat(40)}`);
  await expect(page.locator("[data-conversation] li[data-msg]").last()).toContainText("OrderSO1042");
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});

test("the brand theme is one tap away at both widths and the desk follows it", async ({ page, request }) => {
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await openAs(page, request, "sales");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await accountItem(page, "Brand theme");
    await expect(page.locator("html")).not.toHaveAttribute("data-theme", "light");
    await page.getByRole("button", { name: /^Account/ }).click();
    await expect(page.getByRole("menuitem", { name: "Light theme" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.locator("[data-channel-list]")).toBeVisible();
  }
});
