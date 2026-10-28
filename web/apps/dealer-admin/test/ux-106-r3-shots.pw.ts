// UX-106 round 3's shot: the Staff register with a branch the catalog does
// not know. It runs only when DA_SHOT_DIR names a folder (so the suite's own
// run skips it), and writes there and nowhere else:
//
//   DA_SHOT_DIR=/some/folder npx playwright test ux-106-r3-shots
//
// The name is the item's: <section>__<state>__<theme>-<width>.png. The
// scenario is the stub's own "unknown-branch": the yard persona's grants
// reach a branch the branches route does not know, so the register shows
// the house placeholder word where the id's own first block used to be.
//
// The shot waits for the register's own content, never for a control, and
// the scenario is put back afterwards so the suite's other shots are
// unaffected.
import type { Page } from "@playwright/test";
import { test, expect } from "@playwright/test";
import { join } from "node:path";

const dir = process.env.DA_SHOT_DIR ?? "";
test.skip(dir === "", "set DA_SHOT_DIR to take the screenshots");

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]").first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

test("the register's unknown branch, at 390, light", async ({ page }) => {
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.request.post("/_stub/reset");
  await page.request.post("/_stub/scenario", { data: { name: "unknown-branch" } });
  await signIn(page);
  await page.screenshot({
    path: join(dir, "staff__unknown-branch__light-390.png"),
    fullPage: true,
    animations: "disabled",
  });
  await page.request.post("/_stub/reset");
});
