// Screenshots of Manager Home's screens, at 390 and 1440 px, light and
// brand, walked on the stub the way a manager walks them. It runs only when
// MH_SHOT_DIR names a folder (so the suite's own run skips it), and writes
// there and nowhere else:
//
//   MH_SHOT_DIR=/some/folder npx playwright test shots
//
// The brand theme is the design system's dark half; light is this app's
// default, one tap on the header's theme control moves between them.
import { test, expect, type Page } from "@playwright/test";
import { managerHandoff, resetStub, useScenario } from "./helpers.js";
import { join } from "node:path";

const dir = process.env.MH_SHOT_DIR ?? "";
test.skip(dir === "", "set MH_SHOT_DIR to take the screenshots");

const SIZES = [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
];
const THEMES = ["light", "brand"] as const;

async function shot(page: Page, theme: string, width: number, order: string, name: string): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `manager-home__${order}-${name}__${theme}-${width}.png`), fullPage: true });
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the whole walk at ${size.width} px, ${theme}`, async ({ page, request }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await resetStub(request);
      await useScenario(request, "plain-profile");
      const take = (order: string, name: string) => shot(page, theme, size.width, order, name);
      const brandOn = async () => {
        if (theme === "brand") {
          await page.locator("hh-shell-header button.account").click();
          await page.getByRole("button", { name: "Brand theme" }).click();
        }
      };

      // The way in, with no handoff: the manager's own card among the live roles.
      await page.goto("/");
      await expect(page.locator("[data-flow='yard-manager']")).toBeVisible();
      await brandOn();
      await take("01", "way-in");

      // The landing: the profile's widgets in the profile's order, the needs queue inside.
      await page.addInitScript(managerHandoff);
      await page.goto("/");
      await expect(page.locator("[data-widget]")).toHaveCount(8);
      await brandOn();
      await take("02", "landing");

      // Shortages · open, with the resolve sheet open on the first shortage.
      await page.locator("[data-needs='tasks.open_shortages'] button", { hasText: "Resolve" }).click();
      await expect(page.getByRole("heading", { name: "Shortages · open" })).toBeVisible();
      await take("03", "shortages-open");
      await page.locator(".shortage[data-shortage]:has([data-action='open-resolve'])").first().getByRole("button", { name: "Resolve" }).click();
      await expect(page.locator("[data-resolving='true']")).toHaveCount(1);
      await page.getByRole("button", { name: "Backorder the shortfall" }).click();
      await take("04", "resolve-sheet");

      // Receiving · short, with the definition words and the open value.
      await page.getByRole("button", { name: "Cancel" }).click();
      await page.getByRole("button", { name: "Back to Home" }).click();
      await page.locator(".view-link", { hasText: "Receiving · short" }).click();
      await expect(page.locator("[data-po]")).toHaveCount(1);
      await take("05", "receiving-short");

      // Approvals, on the kind the yard manager decides none of: the words that name who does.
      await page.getByRole("button", { name: "Back to Home" }).click();
      await page.locator(".view-link", { hasText: "Approvals" }).click();
      await expect(page.locator("[data-test='approvals-refusal']")).toBeVisible();
      await take("06", "approvals");

      // Warranty: the board, the settled claim's detail with the settle sheet,
      // the recovery worklist with its chase log, the service levels, the
      // registry, the reports.
      await page.getByRole("button", { name: "Back to Home" }).click();
      await page.locator(".view-link", { hasText: "Warranty" }).click();
      await expect(page.locator("[data-test='warranty-board']")).toBeVisible();
      await take("07", "warranty-board");
      await page.locator(".claim-card", { hasText: "WTY-1039" }).click();
      await expect(page.locator("[data-test='warranty-claim-no']")).toHaveText("WTY-1039");
      await page.getByRole("button", { name: "Record settlement" }).click();
      await take("08", "warranty-settle-sheet");
      await page.getByRole("button", { name: "Cancel" }).click();
      await page.getByRole("button", { name: "Back to the board" }).click();
      await page.getByRole("tab", { name: "Recovery" }).click();
      await expect(page.locator("[data-test='warranty-recovery']")).toBeVisible();
      await page.getByRole("button", { name: "Chase" }).first().click();
      await expect(page.locator(".chase")).toBeVisible();
      await take("09", "warranty-recovery");
      await page.getByRole("tab", { name: "Service levels" }).click();
      await expect(page.locator("[data-test='warranty-sla']")).toBeVisible();
      await take("10", "warranty-sla");
      await page.getByRole("tab", { name: "Vendor registry" }).click();
      await expect(page.locator("[data-test='warranty-vendors']")).toBeVisible();
      await page.getByRole("button", { name: "Northland Stone Supply (demo)" }).click();
      await expect(page.locator(".vendor-panel")).toBeVisible();
      await take("11", "warranty-vendors");
      await page.getByRole("tab", { name: "Reports" }).click();
      await expect(page.locator("[data-test='warranty-reports']")).toBeVisible();
      await take("12", "warranty-reports");
    });
  }
}
