// Notification choices on the stub: a person's own mode for a room (what the core stores, what an
// escalation room refuses), the room's default shown as a default, and the caller's own payment
// notice rules. Every change is held to the core: a control moves only when the core says so.
import { test, expect, openAs, channel, CH, stubPost } from "./helpers.js";

const sheet = (page: import("@playwright/test").Page) => page.locator("[data-prefs]");
const mode = (page: import("@playwright/test").Page, name: string) => sheet(page).getByRole("button", { name, exact: true });

test("a room's notification sheet shows the three modes with the core's current one pressed, and says it is the default", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  await page.getByRole("button", { name: "Notifications" }).click();
  await expect(sheet(page)).toContainText("Notifications for Yard floor");
  await expect(mode(page, "All messages")).toHaveAttribute("aria-pressed", "true");
  await expect(mode(page, "Mentions only")).toHaveAttribute("aria-pressed", "false");
  await expect(mode(page, "Muted")).toHaveAttribute("aria-pressed", "false");
  await expect(sheet(page).locator("[data-inherited]")).toHaveText("You have not chosen. This is the room's default.");
});

test("choosing a mode is stored by the core and the sheet follows what the core answers", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  await page.getByRole("button", { name: "Notifications" }).click();
  await mode(page, "Mentions only").click();
  await expect(mode(page, "Mentions only")).toHaveAttribute("aria-pressed", "true");
  await expect(mode(page, "All messages")).toHaveAttribute("aria-pressed", "false");
  await expect(sheet(page).locator("[data-inherited]")).toHaveCount(0);
  await page.reload();
  await channel(page, CH.yard).click();
  await page.getByRole("button", { name: "Notifications" }).click();
  await expect(mode(page, "Mentions only")).toHaveAttribute("aria-pressed", "true");
});

test("an escalation room can not be muted: the mode is not offered and the sheet says why", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.esc).click();
  await page.getByRole("button", { name: "Notifications" }).click();
  await expect(mode(page, "All messages")).toBeVisible();
  await expect(mode(page, "Mentions only")).toBeVisible();
  await expect(mode(page, "Muted")).toHaveCount(0);
  await expect(sheet(page).locator("[data-mute-rule]")).toContainText("cannot be muted");
});

test("a room that arrives quiet reads as quiet by default, not as something the person did", async ({ page, request }) => {
  await openAs(page, request, "payables-receivables");
  await channel(page, CH.pay).click();
  await page.getByRole("button", { name: "Notifications" }).click();
  await expect(mode(page, "Muted")).toHaveAttribute("aria-pressed", "true");
  await expect(sheet(page).locator("[data-inherited]")).toContainText("default");
});

test("the on and off choices move only when the core answers, and a refusal says so", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  await page.getByRole("button", { name: "Notifications" }).click();
  const desktop = sheet(page).locator("[data-pref='desktop_enabled']");
  await expect(desktop).toHaveAttribute("aria-pressed", "true");
  await stubPost(request, "offline", { on: true });
  await desktop.click();
  await expect(sheet(page).locator("[data-prefs-error]")).toContainText("did not go through");
  await expect(desktop).toHaveAttribute("aria-pressed", "true");
  await stubPost(request, "offline", { on: false });
  await desktop.click();
  await expect(desktop).toHaveAttribute("aria-pressed", "false");
  await expect(sheet(page).locator("[data-prefs-error]")).toHaveCount(0);
});

test("payment notice rules: all three read off, a toggle is the caller's own and survives a reload", async ({ page, request }) => {
  await openAs(page, request, "payables-receivables");
  await page.getByRole("button", { name: "Payment notice rules" }).click();
  const rules = page.locator("[data-rules]");
  await expect(rules).toContainText("Payment notices");
  for (const rule of ["ONLINE", "CROSS_BRANCH", "OWN_ORDER"]) {
    await expect(rules.locator(`[data-rule='${rule}']`)).toHaveAttribute("aria-pressed", "false");
  }
  await rules.locator("[data-rule='OWN_ORDER']").click();
  await expect(rules.locator("[data-rule='OWN_ORDER']")).toHaveAttribute("aria-pressed", "true");
  await expect(rules.locator("[data-rule='ONLINE']")).toHaveAttribute("aria-pressed", "false");
  await page.reload();
  await page.getByRole("button", { name: "Payment notice rules" }).click();
  await expect(page.locator("[data-rules] [data-rule='OWN_ORDER']")).toHaveAttribute("aria-pressed", "true");
});

test("one person's payment rules never read as another's", async ({ browser, request }) => {
  const payables = await browser.newPage();
  const sales = await browser.newPage();
  await openAs(payables, request, "payables-receivables");
  await payables.getByRole("button", { name: "Payment notice rules" }).click();
  await payables.locator("[data-rules] [data-rule='ONLINE']").click();
  await expect(payables.locator("[data-rules] [data-rule='ONLINE']")).toHaveAttribute("aria-pressed", "true");
  await openAs(sales, request, "sales", false);
  await sales.getByRole("button", { name: "Payment notice rules" }).click();
  await expect(sales.locator("[data-rules] [data-rule='ONLINE']")).toHaveAttribute("aria-pressed", "false");
  await payables.close();
  await sales.close();
});

test("the sheet closes and the room is as it was", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  await page.getByRole("button", { name: "Notifications" }).click();
  await sheet(page).getByRole("button", { name: "Close" }).click();
  await expect(sheet(page)).toHaveCount(0);
  await expect(page.locator("[data-conversation] li[data-msg]")).toHaveCount(2);
});

test("a notification sheet whose read failed draws no controls, only the core's refusal and Try again; Try again draws the real values", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  const refuse = (route: import("@playwright/test").Route) =>
    route.request().method() === "GET" ? route.fulfill({ status: 503, json: { error: "database unavailable" } }) : route.continue();
  await page.route("**/api/v1/messaging/channels/*/notify-prefs", refuse);
  await page.getByRole("button", { name: "Notifications" }).click();
  await expect(sheet(page).locator("[data-prefs-error]")).toContainText("Your notification choices");
  await expect(sheet(page).locator("[data-action='set-mode']")).toHaveCount(0);
  await expect(sheet(page).locator("[data-action='toggle-pref']")).toHaveCount(0);
  await expect(sheet(page).getByRole("button", { name: "Try again" })).toBeVisible();
  await page.unroute("**/api/v1/messaging/channels/*/notify-prefs", refuse);
  await sheet(page).getByRole("button", { name: "Try again" }).click();
  await expect(sheet(page).locator("[data-prefs-error]")).toHaveCount(0);
  await expect(mode(page, "All messages")).toHaveAttribute("aria-pressed", "true");
  await expect(sheet(page).locator("[data-action='toggle-pref']")).toHaveCount(2);
});

test("a payment rules sheet whose read failed draws no rule, writes nothing, and never shows the last read's values; Try again reads them", async ({ page, request }) => {
  await openAs(page, request, "sales");
  const rules = page.locator("[data-rules]");
  await page.getByRole("button", { name: "Payment notice rules" }).click();
  await rules.locator("[data-rule='OWN_ORDER']").click();
  await expect(rules.locator("[data-rule='OWN_ORDER']")).toHaveAttribute("aria-pressed", "true");
  await rules.getByRole("button", { name: "Close" }).click();
  const refuse = (route: import("@playwright/test").Route) =>
    route.request().method() === "GET" ? route.fulfill({ status: 503, json: { error: "database unavailable" } }) : route.continue();
  await page.route("**/api/v1/messaging/payment-rules", refuse);
  await page.getByRole("button", { name: "Payment notice rules" }).click();
  await expect(rules.locator("[data-rules-error]")).toContainText("Your payment notice rules");
  await expect(rules.locator("[data-action='toggle-rule']")).toHaveCount(0);
  await expect(rules.getByRole("button", { name: "Try again" })).toBeVisible();
  await page.unroute("**/api/v1/messaging/payment-rules", refuse);
  await rules.getByRole("button", { name: "Try again" }).click();
  await expect(rules.locator("[data-rules-error]")).toHaveCount(0);
  await expect(rules.locator("[data-action='toggle-rule']")).toHaveCount(3);
  await expect(rules.locator("[data-rule='OWN_ORDER']")).toHaveAttribute("aria-pressed", "true");
});

test("while a notification choice is out, the chosen segment and chosen switch carry a tick and non-brand ring, and On clears 3:1 against its pill in charcoal", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  await page.getByRole("button", { name: "Notifications" }).click();
  const s = sheet(page);
  await expect(s).toBeVisible();
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v1/messaging/channels/*/notify-prefs", async (route) => {
    if (route.request().method() === "PUT") await gate;
    await route.continue();
  });
  await mode(page, "Mentions only").click();
  const chosenSeg = s.locator('.seg[aria-pressed="true"]');
  const unchosenSeg = s.locator('.seg[aria-pressed="false"]').first();
  await expect(chosenSeg).toBeDisabled();
  await expect(unchosenSeg).toBeDisabled();
  await expect(chosenSeg.locator(".picked")).toHaveText("\u2713");
  await expect(unchosenSeg.locator(".picked")).toHaveCount(0);
  const segRing = await chosenSeg.evaluate((el) => getComputedStyle(el).boxShadow);
  expect(segRing).not.toBe("none");

  const chosenSw = s.locator('.switch[aria-pressed="true"]').first();
  const unchosenSw = s.locator('.switch[aria-pressed="false"]').first();
  await expect(chosenSw).toBeDisabled();
  await expect(unchosenSw).toBeDisabled();
  await expect(chosenSw.locator(".picked")).toHaveText("\u2713");
  await expect(unchosenSw.locator(".picked")).toHaveCount(0);
  const swRing = await chosenSw.evaluate((el) => getComputedStyle(el).boxShadow);
  expect(swRing).not.toBe("none");

  // Compute WCAG contrast of "On" against its pill in charcoal (default) and light.
  for (const theme of ["charcoal", "light"] as const) {
    await page.evaluate((t) => {
      if (t === "light") document.documentElement.setAttribute("data-theme", "light");
      else document.documentElement.removeAttribute("data-theme");
    }, theme);
    const { contrast, fg, bg } = await chosenSw.evaluate((el) => {
      const style = getComputedStyle(el);
      const lin = (c: number) => {
        const v = c / 255;
        return v <= 0.04045 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
      };
      const lum = (rgb: string) => {
        const [r = 0, g = 0, b = 0] = rgb.match(/\d+/g)?.map(Number) ?? [];
        return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
      };
      const l1 = lum(style.color);
      const l2 = lum(style.backgroundColor);
      return {
        contrast: (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05),
        fg: style.color,
        bg: style.backgroundColor,
      };
    });
    expect(contrast, `${theme}: ${fg} on ${bg}`).toBeGreaterThanOrEqual(3);
  }
  await page.evaluate(() => document.documentElement.removeAttribute("data-theme"));
  release();
});
