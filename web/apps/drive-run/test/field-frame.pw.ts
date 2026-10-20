// GAPS-r2 G2: the Field frame on HH Drive. The tenant's lockup, a small DEMO chip, the surface chip with the sync
// state, the bell and the account chip; on a phone a bottom tab bar of the tabs the role holds; Switch role, Sign out and
// Theme reached from the account menu by keyboard. Every assertion waits.
import { test, expect, driverHandoff, resetStub } from "./helpers.js";

const PHONE = { width: 390, height: 844 };
const TABLET = { width: 1024, height: 768 };

for (const size of [PHONE, TABLET]) {
  test(`the frame shows the tenant's lockup, a small DEMO chip and the old header words are gone at ${size.width} px`, async ({ page, request }) => {
    await page.setViewportSize(size);
    await resetStub(request);
    await page.addInitScript(driverHandoff);
    await page.goto("/");
    await expect(page.locator("[data-stop]")).toHaveCount(3);
    await expect(page.locator("hh-field-frame [data-lockup]")).toBeVisible();
    await expect(page.locator("hh-field-frame")).toContainText("DEMO");
    await expect(page.locator("hh-field-frame .demo")).toBeVisible();
    // The Demo state is a chip, never a full width block.
    await expect.poll(async () => (await page.locator("hh-field-frame .demo").boundingBox())?.width ?? 9999).toBeLessThan(120);
    await expect(page.locator("hos-app-header")).toHaveCount(0);
    await expect(page.getByText("HH Drive", { exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Brand theme", exact: true })).toHaveCount(0);
  });
}

test("the dealer lockup names the tenant from its config, on a tablet", async ({ page, request }) => {
  await page.setViewportSize(TABLET);
  await resetStub(request);
    await page.addInitScript(driverHandoff);
    await page.goto("/");
    await expect(page.locator("[data-stop]")).toHaveCount(3);
  await expect(page.locator("hh-field-frame .lockup-name")).toHaveText("Gable Landscape Supply");
  await expect(page.locator("hh-field-frame [data-selector]")).toContainText("My run");
  await expect(page.locator("hh-field-frame [data-sync]")).toBeVisible();
  await expect(page.getByRole("button", { name: /^Account/ })).toBeVisible();
});

test("the bottom tab bar shows on a phone with the tabs the role holds, and not on a tablet", async ({ page, request }) => {
  await page.setViewportSize(PHONE);
  await resetStub(request);
    await page.addInitScript(driverHandoff);
    await page.goto("/");
    await expect(page.locator("[data-stop]")).toHaveCount(3);
  const bar = page.getByRole("navigation", { name: "Field apps" });
  await expect(bar).toBeVisible();
  for (const name of ["Drive", "Messages"]) await expect(bar.getByText(name, { exact: true })).toBeVisible();
  for (const name of ["Pick"]) await expect(bar.getByText(name, { exact: true })).toHaveCount(0);
  await expect(bar.getByText("More", { exact: true })).toBeVisible();
  await expect(bar.locator("[aria-current='page']")).toHaveCount(1);
  await expect(bar.locator("[aria-current='page']")).toHaveText("Drive");
  for (const tab of await bar.locator(".tab").all()) {
    await expect.poll(async () => (await tab.boundingBox())?.width ?? 0).toBeGreaterThanOrEqual(44);
    await expect.poll(async () => (await tab.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(44);
  }
  await page.setViewportSize(TABLET);
  await expect(bar).toBeHidden();
});

test("the account menu reaches Switch role, Sign out and Theme by keyboard, Escape closes and the ring returns", async ({ page, request }) => {
  await page.setViewportSize(TABLET);
  await resetStub(request);
    await page.addInitScript(driverHandoff);
    await page.goto("/");
    await expect(page.locator("[data-stop]")).toHaveCount(3);
  const account = page.getByRole("button", { name: /^Account/ });
  await account.focus();
  await page.keyboard.press("Enter");
  const switchRole = page.getByRole("menuitem", { name: "Switch role" });
  await expect(switchRole).toBeVisible();
  await expect(switchRole).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(page.getByRole("menuitem", { name: "Sign out" })).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(page.getByRole("menuitem", { name: /theme/i }).first()).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(switchRole).toBeHidden();
  await expect(account).toBeFocused();
});

test("the More tab on a phone opens the same menu, and Sign out works from it", async ({ page, request }) => {
  await page.setViewportSize(PHONE);
  await resetStub(request);
    await page.addInitScript(driverHandoff);
    await page.goto("/");
    await expect(page.locator("[data-stop]")).toHaveCount(3);
  await page.getByRole("button", { name: "More" }).click();
  await expect(page.getByRole("menuitem", { name: "Switch role" })).toBeVisible();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect(page.locator("[data-flow]").first()).toBeVisible();
});

test("the theme item swaps light and charcoal from the account menu", async ({ page, request }) => {
  await page.setViewportSize(TABLET);
  await resetStub(request);
    await page.addInitScript(driverHandoff);
    await page.goto("/");
    await expect(page.locator("[data-stop]")).toHaveCount(3);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: /^Account/ }).click();
  await page.getByRole("menuitem", { name: "Brand theme" }).click();
  await expect(page.locator("html")).not.toHaveAttribute("data-theme", "light");
});

test("the bell opens the review of anything the sync set aside", async ({ page, request }) => {
  await page.setViewportSize(TABLET);
  await resetStub(request);
  await page.addInitScript(driverHandoff);
  await page.goto("/");
  await expect(page.locator("[data-stop]")).toHaveCount(3);
  await page.getByRole("button", { name: /^Alerts/ }).click();
  await expect(page.locator("hh-field-frame [data-selector]")).toContainText("Review");
});
