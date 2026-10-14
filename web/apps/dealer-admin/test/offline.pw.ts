// CP-3, Dealer Admin in the Work shell: online only (manifest mode none).
// Every change, a role, a reset link, a setting, resolves online. With no
// signal the change is refused with the honest "no signal" state, nothing is
// sent and nothing is kept, and the register shows the role it had.
import { test, expect, type Page } from "@playwright/test";

const YARD_TEAM_ROW = "da110000-0000-4000-9000-0000000000yt";

function dialedWrites(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (r) => {
    if (["POST", "PUT", "PATCH", "DELETE"].includes(r.method()) && new URL(r.url()).pathname.startsWith("/api/")) seen.push(`${r.method()} ${new URL(r.url()).pathname}`);
  });
  return seen;
}

test("with no signal a role change is refused with no signal, sends nothing, and goes through once the signal is back", async ({ page, context }) => {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]")).toHaveText("5 staff");
  const select = page.locator(`select[data-user-id=${YARD_TEAM_ROW}]`);
  const before = await select.inputValue();
  const dialed = dialedWrites(page);

  await context.setOffline(true);
  await select.selectOption({ label: "Yard Manager" });
  await expect(page.getByText(/No signal/).first()).toBeVisible();
  expect(dialed).toEqual([]);
  await expect(select).toHaveValue(before);

  await context.setOffline(false);
  await select.selectOption({ label: "Yard Manager" });
  await expect(select).toHaveValue("Yard Manager");
  expect(dialed.length).toBeGreaterThan(0);
});
