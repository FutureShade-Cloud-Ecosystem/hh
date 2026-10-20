// The settings tab on the stub: the yard's dispatch rules in words with their units, a change saved and
// recorded with who and when, the core's own words on a refusal with the typing kept, a login off the
// setters list meeting the wall in words, the trail kept from a login that may not read it, both widths.
import { test, expect, openBoard, stubPost } from "./helpers.js";
import type { Page } from "@playwright/test";

const rule = (page: Page, key: string) => page.locator(`[data-rule='${key}'] [data-rule-word]`);
const field = (page: Page, name: string) => page.locator(`[data-sheet] [data-field='${name}']`);

async function openSettings(page: Page, request: Parameters<typeof openBoard>[1]): Promise<void> {
  await openBoard(page, request);
  await page.locator("[data-tab-id='settings']").first().click();
  await expect(page.locator("[data-rules]")).toBeVisible();
}

test("the rules read in words with their units, and the two reaches are told apart", async ({ page, request }) => {
  await openSettings(page, request);
  await expect(page.locator("[data-tab-id='settings']").first()).toHaveAttribute("aria-current", "true");
  await expect(rule(page, "commit_prompt_hours")).toHaveText("48 hours");
  await expect(rule(page, "reopen_policy")).toHaveText("A dispatch manager");
  await expect(rule(page, "load_value_visibility")).toHaveText("Managers and above");
  await expect(rule(page, "commit_horizon_weeks")).toHaveText("4 weeks");
  await expect(rule(page, "working_view_days")).toHaveText("14 days");
  await expect(rule(page, "freight_split_mode")).toHaveText("Evenly across the loads");
  await expect(rule(page, "driver_session_hours")).toHaveText("12 hours");
  await expect(page.locator("[data-rule='working_view_days']")).toContainText("It is not how far ahead a delivery may be promised.");
  await expect(page.locator("[data-history-empty]")).toHaveText("No rule has been changed at this yard.");
  await expect(page.locator("main")).not.toContainText("commit_prompt_hours");
});

test("changing the rules opens on what they are, saves, shows the new words, and the trail records each change", async ({ page, request }) => {
  await openSettings(page, request);
  await page.getByRole("button", { name: "Change the rules" }).click();
  await expect(field(page, "prompt")).toHaveValue("48");
  await expect(field(page, "reopen")).toHaveValue("A dispatch manager");
  await expect(field(page, "horizon")).toHaveValue("4");
  await field(page, "prompt").fill("24");
  await field(page, "reopen").selectOption({ label: "Anyone who could have committed it" });
  await page.locator("[data-sheet]").getByRole("button", { name: "Save the rules" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Saved. The board reads the new rules on its next read.");
  await expect(rule(page, "commit_prompt_hours")).toHaveText("24 hours");
  await expect(rule(page, "reopen_policy")).toHaveText("Anyone who could have committed it");
  await expect(page.locator("[data-history-row]")).toHaveCount(2);
  const first = page.locator("[data-history-row]", { hasText: "Prompt to commit, hours ahead" });
  await expect(first).toContainText("48");
  await expect(first).toContainText("24");
  await expect(first).toContainText("Logistics Manager");
  const reopened = page.locator("[data-history-row]", { hasText: "Who may reopen a committed load" });
  await expect(reopened).toContainText("A dispatch manager");
  await expect(reopened).toContainText("Anyone who could have committed it");
  // The load board reads the rule the screen just saved.
  await page.locator("[data-tab-id='load']").first().click();
  await expect(page.locator("[data-card]").first()).toBeVisible();
});

test("a rule the core refuses is answered in its words with the rule's name, and the typing stays", async ({ page, request }) => {
  await openSettings(page, request);
  await page.getByRole("button", { name: "Change the rules" }).click();
  await field(page, "prompt").fill("999");
  await field(page, "view").fill("30");
  await page.locator("[data-sheet]").getByRole("button", { name: "Save the rules" }).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("Prompt to commit, hours ahead must be between 0 and 336");
  await expect(field(page, "prompt")).toHaveValue("999");
  await expect(field(page, "view")).toHaveValue("30");
  await expect(rule(page, "working_view_days")).toHaveText("14 days");
  await field(page, "prompt").fill("12");
  await field(page, "horizon").fill("40");
  await page.locator("[data-sheet]").getByRole("button", { name: "Save the rules" }).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("How far the board plans, in weeks must be between 1 and 26");
});

test("a number that is not a whole number is answered before the core is asked", async ({ page, request }) => {
  await openSettings(page, request);
  await page.getByRole("button", { name: "Change the rules" }).click();
  await field(page, "prompt").fill("soon");
  await page.locator("[data-sheet]").getByRole("button", { name: "Save the rules" }).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("Hours ahead is a whole number, like 48.");
});

test("a login off the setters list reads the rules and meets the wall in words on save", async ({ page, request }) => {
  await openSettings(page, request);
  await stubPost(request, "caller", { can_set_policy: false });
  await page.getByRole("button", { name: "Change the rules" }).click();
  await field(page, "prompt").fill("12");
  await page.locator("[data-sheet]").getByRole("button", { name: "Save the rules" }).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("You can read the dispatch rules, but your dealer names who may change them.");
  await expect(rule(page, "commit_prompt_hours")).toHaveText("48 hours");
});

test("a login that may not read the trail still reads the rules, and the trail says why it is not shown", async ({ page, request }) => {
  await openBoard(page, request);
  await stubPost(request, "caller", { can_manage: false });
  await page.locator("[data-tab-id='settings']").first().click();
  await expect(rule(page, "commit_horizon_weeks")).toHaveText("4 weeks");
  await expect(page.locator("[data-history-fault]")).toHaveText("Managers read the change history. It is not shown to this sign-in.");
  await expect(page.locator("[data-history-row]")).toHaveCount(0);
});

for (const size of [{ width: 390, height: 844 }, { width: 1440, height: 900 }]) {
  test(`at ${size.width} px the settings never scroll sideways and every control is at least 44 tall`, async ({ page, request }) => {
    await page.setViewportSize(size);
    await openSettings(page, request);
    await page.getByRole("button", { name: "Change the rules" }).click();
    await field(page, "prompt").fill("24");
    await page.locator("[data-sheet]").getByRole("button", { name: "Save the rules" }).click();
    await expect(page.locator("[data-history-row]").first()).toBeVisible();
    await page.getByRole("button", { name: "Change the rules" }).click();
    const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    expect(wide).toBe(false);
    const small = await page.evaluate(() => {
      const root = document.querySelector("dispatch-board-app")?.shadowRoot;
      return [...(root?.querySelectorAll("[data-rules] button, [data-rules] input, [data-rules] select") ?? [])]
        .filter((el) => el.getBoundingClientRect().height < 44)
        .map((el) => el.textContent?.trim() || el.getAttribute("data-field") || "?");
    });
    expect(small).toEqual([]);
  });
}
