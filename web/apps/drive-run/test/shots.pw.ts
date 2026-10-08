// Screenshots of every HH Drive screen, at 390 and 1440 px, light and brand,
// walked on the stub the way a driver walks them. It runs only when
// DRIVE_SHOT_DIR names a folder (so the suite's own run skips it), and writes
// there and nowhere else:
//
//   DRIVE_SHOT_DIR=/some/folder npx playwright test shots
//
// The brand theme is the design system's dark half; light is this app's
// default, one tap on the header's theme control moves between them.
import { test, expect, type Page } from "@playwright/test";
import { TINY_PNG, driverHandoff, resetStub, useScenario } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.DRIVE_SHOT_DIR ?? "";
test.skip(dir === "", "set DRIVE_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `hh-drive__${order}-${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the whole walk at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await resetStub(request);
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      const brandOn = async () => {
        if (theme === "brand") await page.getByRole("button", { name: "Brand theme" }).click();
      };

      // The way in, with no handoff: the driver's own card.
      await page.goto("/");
      await expect(page.locator("[data-flow]")).toHaveCount(1);
      await brandOn();
      await take("01", "way-in");

      // The landing: today's run.
      await page.addInitScript(driverHandoff);
      await page.goto("/");
      await expect(page.locator("[data-stop]")).toHaveCount(3);
      await brandOn();
      await take("02", "my-run");

      // A stop, ready, then loaded and en route with its two doors.
      await page.locator("[data-stop]").first().click();
      await take("03", "stop-ready");
      await page.getByRole("button", { name: "Mark loaded" }).click();
      await expect(page.locator("[data-stop-status]")).toHaveText("Loaded");
      await page.getByRole("button", { name: "Mark en route" }).click();
      await expect(page.getByRole("button", { name: "Delivered", exact: true })).toBeVisible();
      await take("04", "stop-en-route");

      // Proof of delivery: empty, then ready with a photo, then signed.
      await page.getByRole("button", { name: "Delivered", exact: true }).click();
      await take("05", "proof-of-delivery");
      await page.locator("input[type=file]").setInputFiles({ name: "drop.png", mimeType: "image/png", buffer: TINY_PNG });
      await expect(page.locator("[data-photo-sent]")).toHaveCount(1);
      await page.getByRole("checkbox").check();
      await take("06", "proof-ready");
      await page.getByRole("button", { name: "Sign for this delivery" }).click();
      await expect(page.locator("[data-stop-status]")).toHaveText("Delivered");
      await take("07", "stop-delivered");
      await page.getByRole("button", { name: "Back to my run" }).click();

      // Could not deliver, and the handback.
      await page.locator("[data-stop]").nth(1).click();
      await page.getByRole("button", { name: "Mark loaded" }).click();
      await page.getByRole("button", { name: "Mark en route" }).click();
      await page.getByRole("button", { name: "Could not deliver" }).click();
      await page.getByLabel("What stopped this delivery?").fill("Nobody on site and the gate is locked");
      await take("08", "could-not-deliver");
      await page.getByRole("button", { name: "Could not deliver" }).click();
      await expect(page.locator("[data-stop-status]")).toHaveText("Not delivered");
      await take("09", "handback");
      await page.getByRole("button", { name: "Back to my run" }).click();
      await take("10", "my-run-midway");

      // The end of the run: blocked by the last stop, then closable, then closed.
      await page.getByRole("button", { name: "End of run" }).click();
      await take("11", "end-of-run-blocked");
      await page.getByRole("button", { name: "Back to my run" }).click();
      await page.locator("[data-stop]").nth(2).click();
      await page.getByRole("button", { name: "Mark loaded" }).click();
      await page.getByRole("button", { name: "Mark en route" }).click();
      await page.getByRole("button", { name: "Delivered", exact: true }).click();
      await page.getByLabel("Full name of the person receiving").fill("Receiving clerk");
      await page.getByRole("checkbox").check();
      await page.getByRole("button", { name: "Sign for this delivery" }).click();
      await expect(page.locator("[data-stop-status]")).toHaveText("Delivered");
      await page.getByRole("button", { name: "Back to my run" }).click();
      await page.getByRole("button", { name: "End of run" }).click();
      await take("12", "end-of-run-ready");
      await page.getByRole("button", { name: "Complete run" }).click();
      await expect(page.getByRole("status")).toContainText("is closed");
      await take("13", "run-closed");

      // The two honest empty states.
      await useScenario(request, "empty");
      await page.goto("/");
      await expect(page.locator("[data-empty]")).toBeVisible();
      await brandOn();
      await take("14", "no-run");
      await useScenario(request, "not-driver");
      await page.goto("/");
      await expect(page.getByRole("alert")).toContainText("You are not set up as a driver");
      await brandOn();
      await take("15", "not-a-driver");
    });
  }
}
