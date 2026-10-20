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

test("a pick the pull already shows as taken disables its line rows and says so", async ({ page, request }) => {
  await page.goto("/");
  await rivalClaim(request, SO_1039);
  await page.reload();
  await openPickCard(page, "SO-1039");
  await expect(page.locator("[data-claimed-by]")).toHaveText("Claimed by a teammate");
  await expect(page.locator("[data-line]").first().locator(".line-row")).toBeDisabled();
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

async function expectNothingToAct(page: Page): Promise<void> {
  await expect(page.locator("[data-pick-taken]")).toHaveText("Someone else is on this pick.");
  await expect(page.getByRole("button", { name: /^Short/ })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Save pick" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Report a shortage" })).toHaveCount(0);
  await expect(page.locator("[data-stage-form]")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Stage", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "One skid more" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "One skid fewer" })).toHaveCount(0);
  await expect(page.locator("[data-skid-count]")).toBeVisible();
  await expect(page.getByRole("button", { name: "Back to the queue" })).toBeVisible();
}

test("after a lost race the second person acts on nothing and a standing line stays", async ({ page, request }) => {
  await openPickCard(page, "SO-1039");
  await rivalClaim(request, SO_1039);
  await page.locator("[data-line]").first().locator(".line-row").click();
  await expect(page.locator("[data-error]")).toHaveText(TAKEN);
  await expectNothingToAct(page);
  await page.getByRole("button", { name: "Back to the queue" }).click();
  await expect(page.locator("[data-pick-card]")).toHaveCount(5);
  await page.locator("[data-pick-card]", { hasText: "SO-1039" }).click();
  await expectNothingToAct(page);
});

test("opening a pick a teammate holds shows the same standing line and no controls", async ({ page, request }) => {
  await page.goto("/");
  await rivalClaim(request, SO_1039);
  await page.reload();
  await openPickCard(page, "SO-1039");
  await expectNothingToAct(page);
});

test("a pick I hold keeps its controls and has no standing line", async ({ page }) => {
  await openPickCard(page, "SO-1041");
  await expect(page.locator("[data-pick-taken]")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Save pick" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Report a shortage" })).toBeVisible();
});

test("green is for Claimed by you only; a teammate's claim is dim with a neutral stripe", async ({ page, request }) => {
  await page.goto("/");
  await rivalClaim(request, SO_1039);
  await page.reload();
  await expect(page.locator("[data-pick-card]")).toHaveCount(5);
  const colour = async (order: string) =>
    page.locator("[data-pick-card]", { hasText: order }).evaluate((card) => {
      const tag = card.querySelector("[data-claimed-by]") as HTMLElement;
      return { tag: getComputedStyle(tag).color, stripe: getComputedStyle(card).borderLeftColor, text: tag.textContent };
    });
  const mine = await colour("SO-1040");
  const theirs = await colour("SO-1039");
  expect(mine.text).toBe("Claimed by you");
  expect(theirs.text).toBe("Claimed by a teammate");
  const probe = async (token: string) =>
    page.evaluate((t) => {
      const el = document.createElement("span");
      el.style.color = `var(${t})`;
      document.body.appendChild(el);
      const c = getComputedStyle(el).color;
      el.remove();
      return c;
    }, token);
  const good = await probe("--hh-state-good");
  const dim = await probe("--hh-text-dim");
  const neutral = await probe("--hh-control-line");
  expect(mine.tag).toBe(good);
  expect(theirs.tag).toBe(dim);
  expect(theirs.tag).not.toBe(good);
  expect(mine.stripe).toBe(good);
  expect(theirs.stripe).toBe(neutral);
});
