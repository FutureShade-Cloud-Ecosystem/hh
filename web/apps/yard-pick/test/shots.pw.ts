// Screenshots of every Yard Pick screen, at 390, 1024 (tablet) and 1440 px,
// light and brand, walked on the stub the way a picker walks them. It runs
// only when YARD_SHOT_DIR names a folder (so the suite's own run skips it),
// and writes there and nowhere else:
//
//   YARD_SHOT_DIR=/some/folder npx playwright test shots
//
// The brand theme is the design system's dark half; light is this app's
// default, one tap on the header's theme control moves between them.
import { test, expect, type Page } from "@playwright/test";
import { accountItem, chooseReason, resetStub, useScenario, yardHandoff } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.YARD_SHOT_DIR ?? "";
test.skip(dir === "", "set YARD_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1024, height: 768 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `yard-pick__${order}-${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the whole walk at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(150_000);
      await page.setViewportSize(size);
      await resetStub(request);
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      const brandOn = async () => {
        if (theme !== "brand") return;
        // Idempotent: the account menu's item names the theme a tap moves TO, so after one tap inside a load it
        // reads "Light theme" and nothing needs doing; a reload boots light again and the tap lands.
        if ((await page.locator("html").getAttribute("data-theme")) === "light") await accountItem(page, "Brand theme");
      };

      // The way in, with no handoff: the Yard Team's own card.
      await page.goto("/");
      await expect(page.locator("[data-flow]")).toHaveCount(1);
      await brandOn();
      await take("01", "way-in");

      // The landing: the Point Loma queue in its two halves.
      await page.addInitScript(yardHandoff);
      await page.goto("/");
      await expect(page.locator("[data-pick-card]")).toHaveCount(5);
      await brandOn();
      await take("02", "pick-queue");

      // The held order waits on the board with its words.
      await page.locator("[data-pick-card]", { hasText: "SO-1044" }).scrollIntoViewIfNeeded();
      await take("03", "held-order");

      // A claimed pick, opened and started.
      await page.locator("[data-pick-card]", { hasText: "SO-1040" }).click();
      await expect(page.locator("[data-pick-order]")).toHaveText("SO-1040");
      await expect(page.locator("[data-task-status]")).toHaveText("Picking");
      await brandOn();
      await take("04", "pick-started");

      // The quantity editor on a line, with the reason rows a short line needs.
      await page.locator("[data-line]", { hasText: "Class 2 base rock gravel" }).getByRole("button", { name: "Short" }).click();
      await page.getByRole("button", { name: "Add one on" }).click();
      await page.getByRole("button", { name: "Add one on" }).click();
      await page.getByRole("button", { name: "Add one on" }).click();
      await page.getByRole("button", { name: "Add one on" }).click();
      await page.getByRole("radio", { name: "Not enough on the yard" }).click();
      await take("05", "line-editor");

      // The skid stepper with its reason rows.
      await page.getByRole("button", { name: "One skid more" }).click();
      await take("06", "skid-stepper");
      await chooseReason(page, "skid", "CONDENSED_SKIDS");
      await page.getByRole("button", { name: "One skid more" }).click();
      await page.getByRole("button", { name: "One skid more" }).click();
      await page.getByRole("button", { name: "Save skid count" }).click();
      await expect(page.locator("[data-notice]")).toContainText("now holds");

      // The save: the server's words for the short pick and the backorder.
      await page.getByRole("button", { name: "Done" }).click();
      await page.getByRole("button", { name: "Save pick" }).click();
      await expect(page.locator("[data-notice]")).toContainText("on backorder");
      await take("07", "short-pick-saved");

      // The pick that already holds the shortages: SO-1041, in progress, one
      // open and one cleared.
      await page.getByRole("button", { name: "Back to the queue" }).click();
      await page.locator("[data-pick-card]", { hasText: "SO-1041" }).click();
      await expect(page.locator("[data-pick-order]")).toHaveText("SO-1041");
      await brandOn();

      // The shortage sheet, the board's own shape.
      await page.getByRole("button", { name: "Report a shortage" }).click();
      await expect(page.locator("[data-shortage-sheet]")).toBeVisible();
      await page.getByLabel("How many are short").fill("3");
      await page.getByRole("radio", { name: "Damaged stock" }).click();
      await take("08", "shortage-sheet");

      // The shortages on the pick, open and cleared.
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      await take("09", "shortages");
      await page.locator("[data-shortage]", { hasText: "22 SQFT" }).getByRole("button", { name: "Mark it settled" }).click();
      await page.getByLabel("How the shortage was settled").fill("Substituted mixed blue from Yard C");
      await take("10", "resolve");
      await page.getByRole("button", { name: "Send" }).click();
      await expect(page.locator("[data-notice]")).toContainText("Shortage cleared");

      // Staged, with the spot and the handle: back to the short picked order.
      await page.getByRole("button", { name: "Back to the queue" }).click();
      await page.locator("[data-pick-card]", { hasText: "SO-1040" }).click();
      await expect(page.locator("[data-pick-order]")).toHaveText("SO-1040");
      await brandOn();
      await page.getByLabel("The stage location").fill("Bay 3");
      await page.getByRole("button", { name: "Stage", exact: true }).click();
      await expect(page.locator("[data-notice]")).toContainText("Staged at Bay 3");
      await take("11", "staged");

      // The board after: one staged, the queue moved on.
      await page.getByRole("button", { name: "Back to the queue" }).click();
      await expect(page.locator("[data-pick-card]", { hasText: "SO-1040" }).locator("[data-task-status]")).toHaveText("Staged");
      await take("12", "queue-after");

      // The two honest empty states.
      await useScenario(request, "empty");
      await page.goto("/");
      await expect(page.locator("[data-empty]")).toBeVisible();
      await brandOn();
      await take("13", "empty-queue");
      await useScenario(request, "not-yard");
      await page.goto("/");
      await expect(page.locator("[data-error]")).toContainText("not set up on a yard");
      await brandOn();
      await take("14", "not-on-a-yard");
    });
  }
}
