// The ONE band's account chip menu carries Theme, Feedback, Switch role and
// Sign out (PR 503 round 1). A leg that presses one of them opens the menu
// first; the helper is idempotent, and a leg that is done presses Escape.
import type { Page } from "@playwright/test";

export async function openAccountMenu(page: Page, how: "click" | "dispatch" = "click"): Promise<void> {
  const menu = page.locator("hh-shell-header .account-menu");
  if (await menu.isVisible()) return;
  const chip = page.locator("hh-shell-header button.account");
  // A modal over the band takes a real pointer click: the dispatch form is for those legs.
  if (how === "dispatch") await chip.dispatchEvent("click");
  else await chip.click();
}

export async function closeAccountMenu(page: Page): Promise<void> {
  if (await page.locator("hh-shell-header .account-menu").isVisible()) await page.keyboard.press("Escape");
}
