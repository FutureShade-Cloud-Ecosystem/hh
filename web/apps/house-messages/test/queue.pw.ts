// The house queue on the stub: customers with no assigned rep wait in the branch's house room; a rep
// claims one, and the claim is sticky. The row order, the claim held to the core, a lost race in the
// core's words, and who can see the room at all.
import { test, expect, openAs, channel, CH, resetStub, stubPost } from "./helpers.js";

const rows = (page: import("@playwright/test").Page) => page.locator("[data-queue] [data-queue-row]");

test("the house room shows who is waiting: unclaimed first and the longest waiting first, claimed last", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.house).click();
  await expect(page.locator("[data-queue]")).toContainText("Customers waiting");
  await expect(rows(page)).toHaveCount(3);
  await expect(rows(page).nth(0)).toContainText("Ridgeview Outdoor Living");
  await expect(rows(page).nth(0)).toContainText("3 messages");
  await expect(rows(page).nth(0)).toContainText("waiting 2 h");
  await expect(rows(page).nth(1)).toContainText("Fieldstone Pools and Patios");
  await expect(rows(page).nth(1)).toContainText("1 message");
  await expect(rows(page).nth(1)).toContainText("waiting 40 min");
  await expect(rows(page).nth(2)).toContainText("Summit Ridge Builders");
  await expect(rows(page).nth(2)).toContainText("Claimed by Yard manager (demo)");
  await expect(rows(page).nth(2).getByRole("button", { name: "Claim" })).toHaveCount(0);
});

test("only the house room carries the queue", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();
  await expect(page.locator("[data-conversation]")).toBeVisible();
  await expect(page.locator("[data-queue]")).toHaveCount(0);
});

test("a claim is recorded by the core, the row shows who owns it, and the words say the claim is sticky", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.house).click();
  await rows(page).nth(0).getByRole("button", { name: "Claim" }).click();
  await expect(page.locator("[data-queue-note]")).toHaveText("You now own Ridgeview Outdoor Living. Their next message goes straight to your DM.");
  await expect(rows(page).filter({ hasText: "Ridgeview Outdoor Living" })).toContainText("Claimed by Inside Sales (demo)");
  await expect(rows(page).filter({ hasText: "Ridgeview Outdoor Living" }).getByRole("button", { name: "Claim" })).toHaveCount(0);
  await expect(rows(page).first()).toContainText("Fieldstone Pools and Patios");
  await expect(page.locator("[data-conversation] li[data-msg]").last()).toContainText("claimed the conversation with Ridgeview Outdoor Living");
});

test("a lost race is said in the core's words and the queue shows who got there first", async ({ browser, request }) => {
  await resetStub(request);
  // No stream: the screen reads every ten seconds, so a claim made a moment ago is not on the other
  // screen yet, which is the race.
  await stubPost(request, "tickets", { enabled: false });
  const sales = await browser.newPage();
  const yard = await browser.newPage();
  await openAs(sales, request, "sales", false);
  await openAs(yard, request, "yard-manager", false);
  await channel(sales, CH.house).click();
  await channel(yard, CH.house).click();
  await expect(rows(sales)).toHaveCount(3);
  await rows(yard).nth(0).getByRole("button", { name: "Claim" }).click();
  await expect(yard.locator("[data-queue-note]")).toBeVisible();
  await rows(sales).nth(0).getByRole("button", { name: "Claim" }).click();
  await expect(sales.locator("[data-queue-error]")).toContainText("conversation already claimed");
  await expect(rows(sales).filter({ hasText: "Ridgeview Outdoor Living" })).toContainText("Claimed by Yard manager (demo)");
  await sales.close();
  await yard.close();
});

test("a person who is not in the house room never sees it or its queue", async ({ page, request }) => {
  await openAs(page, request, "payables-receivables");
  await expect(channel(page, CH.house)).toHaveCount(0);
  await expect(page.locator("[data-queue]")).toHaveCount(0);
});
