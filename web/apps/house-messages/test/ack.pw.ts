// The forced acknowledgement on the stub: the bubble that persists until the person acknowledges, the
// ack arithmetic for everyone in the room, a write that is held to the core, and a superseded notice
// that stops asking.
import { test, expect, openAs, channel, CH, FORCED, stubPost, resetStub } from "./helpers.js";

const bubble = (page: import("@playwright/test").Page) => page.locator("[data-bubble]");

test("a forced acknowledgement sits above everything with the notice, its room and how many have answered", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await expect(bubble(page)).toBeVisible();
  await expect(bubble(page)).toContainText("Needs your acknowledgement");
  await expect(bubble(page)).toContainText("Order SO-1042 is on credit hold. Do not release it to the yard.");
  await expect(bubble(page)).toContainText("Escalations");
  await expect(bubble(page).locator("[data-ack-progress]")).toHaveText("0 of 3 acknowledged · 3 to go");
  await expect(bubble(page).getByRole("button", { name: "I got it" })).toBeVisible();
});

test("the bubble has no way to dismiss it other than acknowledging, and it follows the person across channels", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await expect(bubble(page).getByRole("button")).toHaveCount(1);
  await channel(page, CH.branch).click();
  await expect(bubble(page)).toBeVisible();
  await channel(page, CH.yard).click();
  await expect(bubble(page)).toBeVisible();
});

test("acknowledging records it with the core, then the bubble goes and the room shows the count", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await bubble(page).getByRole("button", { name: "I got it" }).click();
  await expect(bubble(page)).toHaveCount(0);
  await channel(page, CH.esc).click();
  const row = page.locator(`li[data-msg='${FORCED}']`);
  await expect(row).toContainText("You acknowledged");
  await expect(row.locator("[data-ack-progress]")).toHaveText("1 of 3 acknowledged · 2 to go");
  await expect(row.getByRole("button", { name: "I got it" })).toHaveCount(0);
});

test("a second person still sees the bubble until their own acknowledgement, and sees the first one counted", async ({ browser, request }) => {
  await resetStub(request);
  const sales = await browser.newPage();
  const yard = await browser.newPage();
  await openAs(sales, request, "sales", false);
  await bubble(sales).getByRole("button", { name: "I got it" }).click();
  await expect(bubble(sales)).toHaveCount(0);
  await openAs(yard, request, "yard-manager", false);
  await expect(bubble(yard)).toBeVisible();
  await expect(bubble(yard).locator("[data-ack-progress]")).toHaveText("1 of 3 acknowledged · 2 to go");
  await bubble(yard).getByRole("button", { name: "I got it" }).click();
  await expect(bubble(yard)).toHaveCount(0);
  await channel(yard, CH.esc).click();
  await expect(yard.locator(`li[data-msg='${FORCED}'] [data-ack-progress]`)).toHaveText("2 of 3 acknowledged · 1 to go");
  await sales.close();
  await yard.close();
});

test("the last acknowledgement completes it in words", async ({ browser, request }) => {
  await resetStub(request);
  const pages = [];
  for (const who of ["sales", "yard-manager", "payables-receivables"] as const) {
    const p = await browser.newPage();
    await openAs(p, request, who, false);
    await bubble(p).getByRole("button", { name: "I got it" }).click();
    await expect(bubble(p)).toHaveCount(0);
    pages.push(p);
  }
  await channel(pages[2]!, CH.esc).click();
  await expect(pages[2]!.locator(`li[data-msg='${FORCED}'] [data-ack-progress]`)).toHaveText("Acknowledged by everyone · 3 of 3");
  for (const p of pages) await p.close();
});

test("a failed acknowledgement keeps the bubble and says so; nothing is shown as acknowledged", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await stubPost(request, "offline", { on: true });
  await bubble(page).getByRole("button", { name: "I got it" }).click();
  await expect(bubble(page).locator("[data-bubble-error]")).toContainText("did not go through");
  await expect(bubble(page)).toBeVisible();
  await stubPost(request, "offline", { on: false });
  await bubble(page).getByRole("button", { name: "I got it" }).click();
  await expect(bubble(page)).toHaveCount(0);
});

test("a notice a newer fact replaced stops asking: no bubble, and the room says it was replaced", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await expect(bubble(page)).toBeVisible();
  await stubPost(request, "supersede", { id: FORCED });
  await channel(page, CH.esc).click();
  await expect(bubble(page)).toHaveCount(0);
  const row = page.locator(`li[data-msg='${FORCED}']`);
  await expect(row).toContainText("Replaced by a newer notice");
  await expect(row.getByRole("button", { name: "I got it" })).toHaveCount(0);
});

test("in the escalation room a notice carries its ack count for a person who has not acknowledged, with the button in place", async ({ page, request }) => {
  await openAs(page, request, "yard-manager");
  await channel(page, CH.esc).click();
  const row = page.locator(`li[data-msg='${FORCED}']`);
  await expect(row).toContainText("Needs your acknowledgement");
  await expect(row.locator("[data-ack-progress]")).toHaveText("0 of 3 acknowledged · 3 to go");
  await row.getByRole("button", { name: "I got it" }).click();
  await expect(row).toContainText("You acknowledged");
  await expect(bubble(page)).toHaveCount(0);
});
