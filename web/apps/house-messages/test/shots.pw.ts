// Screenshots of every House Messages screen, at 390 and 1440 px, light and brand, walked on the
// stub the way a person walks them. It runs only when MSG_SHOT_DIR names a folder (so the suite's own
// run skips it), and writes there and nowhere else:
//
//   MSG_SHOT_DIR=/some/folder npx playwright test shots
//
// The brand theme is the design system's dark half; light is this app's default, one tap on the
// header's theme control moves between them.
import type { Page } from "@playwright/test";
import { test, expect, openAs, channel, CH, says, stubPost } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.MSG_SHOT_DIR ?? "";
test.skip(dir === "", "set MSG_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `house-messages__${order}-${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the whole walk at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      // Back to the channel list on the one column width; on two panes the list is always there.
      const toList = async () => {
        const back = page.locator("[data-conversation] [data-action='back'], [data-rules] [data-action='close-panel'].back").first();
        if (await back.isVisible()) await back.click();
        await expect(page.locator("[data-channel-list]")).toBeVisible();
      };
      const brandOn = async () => {
        if (theme === "brand") await page.getByRole("button", { name: "Theme: Light" }).click();
      };

      await request.post("/_stub/reset");
      await page.goto("/");
      await expect(page.locator("[data-flow]")).toHaveCount(3);
      await brandOn();
      await take("01", "way-in");

      await page.goto("/");
      await page.evaluate(() => window.sessionStorage.clear());
      await openAs(page, request, "sales");
      await brandOn();
      await take("02", "channel-list");

      await channel(page, CH.yard).click();
      await expect(page.locator("[data-conversation] li[data-msg]").first()).toBeVisible();
      await take("03", "conversation");

      await page.locator("[data-action='toggle-thread']").first().click();
      await expect(page.locator("[data-thread]")).toBeVisible();
      await take("04", "thread-open");

      await says(request, "yard-manager", CH.yard, "Truck 03 is at the gate");
      await expect(page.locator("[data-conversation] li[data-msg]").last()).toContainText("Truck 03 is at the gate");
      await page.locator("textarea[data-field='draft']").fill("a".repeat(8300));
      await take("05", "over-the-size-limit");

      // The forced acknowledgement is on the list already: the bubble above the panes.
      await page.locator("textarea[data-field='draft']").fill("");
      await toList();
      await expect(page.locator("[data-bubble]")).toBeVisible();
      await take("06", "ack-bubble-on-the-list");
      await channel(page, CH.branch).click();
      await expect(page.locator("[data-ack-progress]").first()).toBeVisible();
      await take("07", "home-room-with-ack-count");
      await page.locator("[data-bubble]").getByRole("button", { name: "I got it" }).click();
      await expect(page.locator("[data-bubble]")).toHaveCount(0);
      await take("08", "acknowledged");

      await toList();
      await channel(page, CH.house).click();
      await expect(page.locator("[data-queue-row]").first()).toBeVisible();
      await take("09", "house-queue");
      await page.locator("[data-queue-row]").first().getByRole("button", { name: "Claim" }).click();
      await expect(page.locator("[data-queue-note]")).toBeVisible();
      await take("10", "claimed");

      await page.getByRole("button", { name: "Notifications" }).click();
      await expect(page.locator("[data-prefs]")).toBeVisible();
      await take("11", "notify-prefs");

      await toList();
      await page.getByRole("button", { name: "Payment notice rules" }).click();
      await expect(page.locator("[data-rules]")).toBeVisible();
      await take("12", "payment-notice-rules");

      await page.locator("[data-rules]").getByRole("button", { name: "Close" }).click();
      await toList();
      await stubPost(request, "offline", { on: true });
      await stubPost(request, "drop-streams");
      await expect(page.locator("[data-live]")).toContainText("Offline");
      await take("13", "offline-strip");
      await stubPost(request, "offline", { on: false });
    });
  }
}
