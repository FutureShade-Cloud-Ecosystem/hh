// Reconnect and offline honesty on the stub: the stream's state is always on screen, a drop is a
// visible state with a retry, a message that arrived while the stream was down is read when it
// comes back, a core that issues no stream is read on a timer and says so, a read that fails says
// what the screen is showing, and nothing is queued: a post that can not go keeps its words.
import { test, expect, openAs, channel, CH, says, stubPost } from "./helpers.js";

const live = (page: import("@playwright/test").Page) => page.locator("[data-live]");

test("the strip says Live once the stream is open", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await expect(live(page)).toContainText("Live");
});

test("a dropped stream says so, reconnects, and reads what arrived while it was down", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();
  await expect(live(page)).toContainText("Live");
  await stubPost(request, "offline", { on: true });
  await stubPost(request, "drop-streams");
  await expect(live(page)).toContainText("Offline");
  await expect(live(page)).toContainText("nothing you send is saved");
  await expect(live(page).getByRole("button", { name: "Retry now" })).toBeVisible();
  await stubPost(request, "offline", { on: false });
  await says(request, "yard-manager", CH.branch, "Said while you were away");
  await live(page).getByRole("button", { name: "Retry now" }).click();
  await expect(live(page)).toContainText("Live");
  await expect(page.locator("[data-conversation] li[data-msg]").last()).toContainText("Said while you were away");
});

test("a stream that drops while the server is still answering reconnects by itself", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();
  await expect(live(page)).toContainText("Live");
  await stubPost(request, "drop-streams");
  await expect(live(page)).toContainText("Reconnecting");
  await says(request, "yard-manager", CH.branch, "Said during the gap");
  await expect(live(page)).toContainText("Live", { timeout: 10_000 });
  await expect(page.locator("[data-conversation] li[data-msg]").last()).toContainText("Said during the gap");
});

test("a core that issues no stream is read every ten seconds, says so, and still brings the message", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await stubPost(request, "tickets", { enabled: false });
  await page.reload();
  await expect(live(page)).toContainText("Messages are read every 10 seconds");
  await channel(page, CH.branch).click();
  await says(request, "yard-manager", CH.branch, "Brought by the timer");
  await expect(page.locator("[data-conversation] li[data-msg]").last()).toContainText("Brought by the timer", { timeout: 20_000 });
});

test("a read that fails says what the screen is showing, and clears when a read works again", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();
  await expect(live(page)).toContainText("Live");
  await stubPost(request, "offline", { on: true });
  await says(request, "yard-manager", CH.branch, "Never seen while down");
  await expect(page.locator("[data-stale]")).toContainText("You are looking at what was read before");
  await stubPost(request, "offline", { on: false });
  await says(request, "yard-manager", CH.branch, "Seen once it is back");
  await expect(page.locator("[data-stale]")).toHaveCount(0);
  await expect(page.locator("[data-conversation] li[data-msg]").last()).toContainText("Seen once it is back");
  await expect(page.locator("[data-conversation]")).toContainText("Never seen while down");
});

test("nothing is queued: the composer says so, and a post that can not go keeps its words and is not sent later", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();
  await expect(page.locator("[data-composer]")).toContainText("Messages send while you are online. Nothing is saved to send later.");
  await stubPost(request, "offline", { on: true });
  await page.locator("textarea[data-field='draft']").fill("Offline words");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await expect(page.locator("[data-error]")).toBeVisible();
  await stubPost(request, "offline", { on: false });
  await page.waitForTimeout(1500);
  await expect(page.locator("[data-conversation]")).not.toContainText("Offline words\n");
  await expect(page.locator("[data-conversation] li[data-msg]", { hasText: "Offline words" })).toHaveCount(0);
  await expect(page.locator("textarea[data-field='draft']")).toHaveValue("Offline words");
});
