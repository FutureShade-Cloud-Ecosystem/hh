// House Messages browser legs on the stub: the way in, the channel list and its unread counts, a
// channel's messages with threads, posting (the size rule), reading, and the refusals in the core's
// words. The ack, queue, prefs, live and phone legs follow in their own sub items.
import { test, expect, openAs, resetStub, channel, CH, says, stubPost } from "./helpers.js";

test("the way in offers a card per persona and a card signs in", async ({ page, request }) => {
  await resetStub(request);
  await page.goto("/");
  await expect(page.locator("[data-flow]")).toHaveCount(3);
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.locator("[data-channel-list]")).toBeVisible();
});

test("the channel list groups rooms and shows each unread count; the payments room is not in it for Sales", async ({ page, request }) => {
  await openAs(page, request, "sales");
  const list = page.locator("[data-channel-list]");
  await expect(list.getByRole("heading", { name: "Direct messages" })).toBeVisible();
  await expect(list.getByRole("heading", { name: "Rooms" })).toBeVisible();
  await expect(list.getByRole("heading", { name: "Branch" })).toBeVisible();
  await expect(channel(page, CH.branch)).toContainText("Point Loma");
  await expect(channel(page, CH.branch).locator("[data-unread]")).toHaveText("2");
  await expect(channel(page, CH.yard).locator("[data-unread]")).toHaveText("2");
  await expect(channel(page, CH.dm)).toContainText("Yard manager (demo)");
  await expect(channel(page, CH.pay)).toHaveCount(0);
  await expect(page.locator("[data-total-unread]")).toContainText("unread");
});

test("a channel opens with its messages oldest first, a thread counted, and the unread count clears once the core marks it read", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  const conversation = page.locator("[data-conversation]");
  await expect(conversation.getByRole("heading", { level: 2 })).toHaveText("Yard floor");
  const rows = conversation.locator("li[data-msg]");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("Pallet of Copperline coping is short");
  await expect(rows.nth(1)).toContainText("Gate code for the second yard");
  await expect(rows.nth(0).locator("[data-action='toggle-thread']")).toHaveText("2 replies");
  await expect(channel(page, CH.yard).locator("[data-unread]")).toHaveCount(0);
});

test("a thread opens under its message and reads oldest first", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  await page.locator("[data-action='toggle-thread']").first().click();
  const thread = page.locator("[data-thread]");
  await expect(thread.locator("li[data-msg]")).toHaveCount(2);
  await expect(thread.locator("li[data-msg]").nth(0)).toContainText("Counted twice");
  await expect(thread.locator("li[data-msg]").nth(1)).toContainText("note the shortage");
});

test("a reply is posted into the thread and the count follows", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  await page.locator("[data-action='toggle-thread']").first().click();
  await page.locator("[data-action='reply']").first().click();
  await expect(page.locator("[data-replying]")).toContainText("Replying in thread");
  await page.locator("textarea[data-field='draft']").fill("Recount done, 13 confirmed.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await expect(page.locator("[data-thread] li[data-msg]")).toHaveCount(3);
  await expect(page.locator("[data-thread]")).toContainText("Recount done, 13 confirmed.");
  await expect(page.locator("[data-action='toggle-thread']").first()).toHaveText("3 replies");
  await expect(page.locator("[data-replying]")).toHaveCount(0);
});

test("posting: the message appears in the stream, the box clears, and a blank body can not be sent", async ({ page, request }) => {
  await openAs(page, request, "yard-manager");
  await channel(page, CH.deliveries).click();
  const send = page.getByRole("button", { name: "Send", exact: true });
  await expect(send).toBeDisabled();
  await page.locator("textarea[data-field='draft']").fill("   ");
  await expect(send).toBeDisabled();
  await page.locator("textarea[data-field='draft']").fill("Truck 02 is back, unloading at door 3");
  await expect(send).toBeEnabled();
  await send.click();
  const rows = page.locator("[data-conversation] li[data-msg]");
  await expect(rows.last()).toContainText("Truck 02 is back, unloading at door 3");
  await expect(rows.last()).toContainText("You");
  await expect(page.locator("textarea[data-field='draft']")).toHaveValue("");
});

test("the size rule: a body over 8 KB can not be sent and the screen says why; at the limit it can", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();
  const draft = page.locator("textarea[data-field='draft']");
  const send = page.getByRole("button", { name: "Send", exact: true });
  await draft.fill("a".repeat(8193));
  await expect(send).toBeDisabled();
  await expect(page.locator("[data-composer-words]")).toContainText("1 byte over the 8 KB limit");
  await expect(page.locator("[data-composer-words]")).toContainText("cut");
  await draft.fill("a".repeat(8192));
  await expect(send).toBeEnabled();
});

test("a post the core refuses keeps the words in the box and says why in the core's sentence", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();
  await stubPost(request, "offline", { on: true });
  await page.locator("textarea[data-field='draft']").fill("Do not lose this");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await expect(page.locator("[data-error]")).toContainText("The post did not go through");
  await expect(page.locator("textarea[data-field='draft']")).toHaveValue("Do not lose this");
  await stubPost(request, "offline", { on: false });
});

test("earlier messages load a page at a time without a repeat", async ({ page, request }) => {
  await openAs(page, request, "sales");
  for (let i = 1; i <= 33; i += 1) await says(request, "yard-manager", CH.deliveries, `Delivery note ${String(i).padStart(2, "0")}`);
  await channel(page, CH.deliveries).click();
  const rows = page.locator("[data-conversation] li[data-msg]");
  await expect(rows).toHaveCount(30);
  await expect(rows.last()).toContainText("Delivery note 33");
  await page.getByRole("button", { name: "Earlier messages" }).click();
  await expect(rows).toHaveCount(34);
  await expect(rows.first()).toContainText("Truck 04 to Julian leaves at 10:40");
  await expect(page.getByRole("button", { name: "Earlier messages" })).toHaveCount(0);
});

test("a message from another person arrives in the open channel without a reload", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();
  await expect(page.locator("[data-live]")).toContainText("Live");
  await says(request, "yard-manager", CH.branch, "Truck 03 is at the gate");
  await expect(page.locator("[data-conversation] li[data-msg]").last()).toContainText("Truck 03 is at the gate");
});
