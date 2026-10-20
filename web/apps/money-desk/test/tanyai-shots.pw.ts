// Screenshots of the TanyAI panel's main states at 1440x900, 1024x768 and 390x844, light and charcoal, on the
// stub. It runs only when MD_SHOT_DIR names a folder (the suite's own run skips it) and writes there alone:
//
//   MD_SHOT_DIR=/some/folder npx playwright test tanyai-shots
//
// MD_SHOT_PHASE=before takes the desk alone (the page without the panel open), the "before" half of the pair.
import type { Page } from "@playwright/test";
import { join } from "node:path";
import { test, expect, openDesk } from "./helpers.js";

const dir = process.env.MD_SHOT_DIR ?? "";
test.skip(dir === "", "set MD_SHOT_DIR to take the screenshots");
const before = process.env.MD_SHOT_PHASE === "before";

const SIZES = [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 390, height: 844 },
];
const THEMES = ["light", "charcoal"] as const;

const panel = (page: Page) => page.getByRole("dialog", { name: "TanyAI" });

async function ask(page: Page, question: string): Promise<void> {
  const box = panel(page).getByRole("textbox", { name: "Ask TanyAI" });
  await box.fill(question);
  await box.press("Enter");
}

for (const size of SIZES) {
  for (const theme of THEMES) {
    test(`the panel at ${size.width} px, ${theme}${before ? ", before" : ""}`, async ({ page, request, context }) => {
      test.setTimeout(120_000);
      await page.setViewportSize(size);
      await openDesk(page, request);
      if (theme === "charcoal") await page.getByRole("button", { name: "Theme: Light" }).click();
      const take = async (state: string): Promise<void> => {
        await page.waitForTimeout(250);
        await page.screenshot({ path: join(dir, `${before ? "before" : "after"}__${state}-${theme}-${size.width}.png`) });
      };
      if (before) {
        await take("desk");
        return;
      }
      await take("closed");
      await page.getByRole("button", { name: "Ask TanyAI" }).click();
      await expect(panel(page).getByRole("button", { name: /Who owes us/ })).toBeVisible();
      await take("first-run");

      await ask(page, "who owes us");
      await expect(panel(page).getByRole("table", { name: "Who owes us" })).toBeVisible();
      await take("answered");

      await ask(page, "figures");
      await expect(panel(page).getByText("Point Loma, this month, as of 8:15 AM")).toBeVisible();
      await take("figures-scope-citations");

      await ask(page, "refuse");
      await expect(panel(page).getByText("Costs are not shown for your role.")).toBeVisible();
      await take("refused");

      await panel(page).getByRole("button", { name: "Start a new conversation" }).click();
      await ask(page, "stock of blu");
      await expect(panel(page).getByRole("button", { name: /Techo-Bloc Blu 60/ })).toBeVisible();
      await take("clarify");

      await panel(page).getByRole("button", { name: "Start a new conversation" }).click();
      await ask(page, "slow, who owes us");
      await expect(panel(page).getByRole("status")).toBeVisible();
      await take("working");
      await expect(panel(page).getByRole("table", { name: "Who owes us" })).toBeVisible({ timeout: 10_000 });

      await panel(page).getByRole("button", { name: "Start a new conversation" }).click();
      await ask(page, "break it");
      await expect(panel(page).getByRole("alert")).toBeVisible();
      await take("error");
      await panel(page).getByRole("alert").getByRole("button", { name: "Try again" }).click();
      await expect(panel(page).getByText("I looked, and there is nothing unusual today.")).toBeVisible();

      await panel(page).getByRole("button", { name: "Start a new conversation" }).click();
      await ask(page, "long");
      await expect(panel(page).getByRole("table", { name: "Open orders" })).toBeVisible();
      await take("long-answer");

      await context.setOffline(true);
      await expect(panel(page).getByText("TanyAI needs a connection.")).toBeVisible();
      await take("offline");
      await context.setOffline(false);

      await request.post("/_stub/assist", { data: { over_budget: true } });
      await page.keyboard.press("Escape");
      await page.reload();
      await openDesk(page, request);
      await request.post("/_stub/assist", { data: { over_budget: true } });
      if (theme === "charcoal") await page.getByRole("button", { name: "Theme: Light" }).click();
      await page.getByRole("button", { name: "Ask TanyAI" }).click();
      await expect(panel(page).getByText("You've used this month's AI allowance")).toBeVisible();
      await take("over-budget");
    });
  }
}
