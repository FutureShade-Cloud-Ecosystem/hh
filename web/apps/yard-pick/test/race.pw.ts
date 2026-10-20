// Yard Pick, PR 545 round 2: the second person in a race is told so, undo is a
// word on the line, the claimer is never a guessed role, the offline pull time
// is in the house zone's 12 hour form, each Short names its line, and a saved
// skid count shows one message.
import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import { resetStub, rivalClaim, yardHandoff } from "./helpers.js";

async function openPickCard(page: Page, order: string): Promise<void> {
  await page.goto("/");
  await expect(page.locator("[data-pick-card]")).toHaveCount(5);
  await page.locator("[data-pick-card]", { hasText: order }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText(order);
}

const SO_1039 = "0a000000-0000-4000-9000-000000000039";
const TAKEN = "Someone else already took this pick. Nothing was recorded from you.";

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

for (const vp of [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
]) {
  test(`the second person in a race is told the pick was taken, and nothing of theirs shows at ${vp.width}x${vp.height}`, async ({ page, request }) => {
    await page.setViewportSize(vp);
    await openPickCard(page, "SO-1039");
    await expect(page.locator("[data-task-status]")).toHaveText("Queued");
    await rivalClaim(request, SO_1039);
    await page.locator("[data-line]").first().locator(".line-row").click();
    await expect(page.locator("[data-error]")).toHaveText(TAKEN);
    await expect(page.locator("[data-notice]")).toHaveCount(0);
    await expect(page.locator("[data-line]").first().locator(".line-check")).toHaveText("");
    await expect(page.locator("body")).not.toContainText("Claimed by you");
    await expect(page.locator("body")).not.toContainText("Claimed. It is yours to pull.");
    await expect(page.locator("body")).not.toContainText("Task already in that status");
    await expect(page.locator("body")).not.toContainText("Illegal task transition");
    await expect(page.locator("[data-claimed-by]")).toHaveText("Claimed by a teammate");
    await page.screenshot({ path: `${process.env.YARD_SHOTS ?? "/tmp"}/race-${vp.width}.png` });
  });
}

test("a pick the pull already shows as taken refuses a line tap in the same words", async ({ page, request }) => {
  await page.goto("/");
  await rivalClaim(request, SO_1039);
  await page.reload();
  await openPickCard(page, "SO-1039");
  await expect(page.locator("[data-claimed-by]")).toHaveText("Claimed by a teammate");
  await page.locator("[data-line]").first().locator(".line-row").click();
  await expect(page.locator("[data-error]")).toHaveText(TAKEN);
  await expect(page.locator("[data-line]").first().locator(".line-check")).toHaveText("");
});

test("a full line shows an Undo word beside the quantity, and tapping it undoes", async ({ page }) => {
  await openPickCard(page, "SO-1040");
  const line = page.locator("[data-line]").first();
  await expect(line.locator("[data-undo-word]")).toHaveCount(0);
  await line.locator(".line-row").click();
  await expect(line.locator("[data-undo-word]")).toHaveText("Undo");
  await line.locator(".line-row").click();
  await expect(line.locator("[data-undo-word]")).toHaveCount(0);
  await expect(line.locator(".line-check")).toHaveText("");
});

test("the claimer is never a guessed role or a raw id", async ({ page, browser }) => {
  await openPickCard(page, "SO-1040");
  await expect(page.locator("[data-claimed-by]")).toHaveText("Claimed by you");
  const other = await browser.newContext();
  const otherPage = await other.newPage();
  await otherPage.addInitScript(() => {
    const part = (value: unknown): string => btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
    const exp = Math.floor(Date.now() / 1000) + 3600;
    const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: "demo-account-yard-manager", exp })}.c2ln`;
    window.sessionStorage.setItem(
      "hh-demo.front-door.session.v1",
      JSON.stringify({
        token,
        tokenExpiresAtMs: Date.now() + 3_600_000,
        flow: "yard-manager",
        roleLabel: "Yard Manager",
        user: { userId: "demo-account-yard-manager", email: "yard-manager@demo.invalid", name: "Yard manager (demo)" },
        roleCode: "yard_manager",
        roleName: "Yard Manager",
        tier: 2,
        scope: "BRANCH",
      }),
    );
  });
  await otherPage.goto("/");
  const card = otherPage.locator("[data-pick-card]", { hasText: "SO-1040" });
  await expect(card).toContainText("Claimed by a teammate");
  await expect(card).not.toContainText("Yard Team");
  await expect(card).not.toContainText("d0200000");
  await other.close();
});

test("each Short button names its line to a screen reader", async ({ page }) => {
  await openPickCard(page, "SO-1039");
  const shorts = page.locator("[data-line] .line-short");
  const count = await shorts.count();
  expect(count).toBeGreaterThan(1);
  const labels = new Set<string>();
  for (let i = 0; i < count; i += 1) {
    const label = (await shorts.nth(i).getAttribute("aria-label")) ?? "";
    expect(label).toMatch(/^Short on line \d+, .+/);
    labels.add(label);
  }
  expect(labels.size).toBe(count);
});

test("a saved skid count shows one message, not two", async ({ page }) => {
  await openPickCard(page, "SO-1040");
  await page.getByRole("button", { name: "One skid more" }).click();
  await page.getByRole("radio", { name: "Skids condensed" }).click();
  await page.getByRole("button", { name: "Save skid count" }).click();
  await expect(page.locator("[data-notice]")).toContainText("now holds");
  await expect(page.locator("[data-skid-saved]")).toHaveCount(0);
});
