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

test("switching rooms while a post is in flight keeps the new room's draft, and the sent words are gone from the old room's box when it is reopened", async ({ page, request }) => {
  await openAs(page, request, "sales");
  const box = page.locator("textarea[data-field='draft']");
  // The rail re-sorts when a room gets a newer message, so a click can land on the row that moved into
  // its place: click until the room asked for is the one on screen.
  const go = (id: string, title: string) =>
    expect(async () => {
      await channel(page, id).click();
      await expect(page.locator("[data-conversation]").getByRole("heading", { level: 2 })).toHaveText(title, { timeout: 1000 });
    }).toPass();
  await page.route("**/api/v1/messaging/channels/*/messages", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    await new Promise((done) => setTimeout(done, 1500));
    return route.continue();
  });
  await go(CH.deliveries, "Deliveries");
  await box.fill("half written about the trucks");
  await go(CH.yard, "Yard floor");
  await expect(box).toHaveValue("");
  await box.fill("Counted again, 13 on the pallet");
  const answered = page.waitForResponse((r) => r.request().method() === "POST" && r.url().includes(`/channels/${CH.yard}/messages`));
  await page.getByRole("button", { name: "Send" }).click();
  await go(CH.deliveries, "Deliveries");
  await expect(box).toHaveValue("half written about the trucks");
  await answered;
  await expect(page.locator("[data-conversation]").getByRole("heading", { level: 2 })).toHaveText("Deliveries");
  await expect(box).toHaveValue("half written about the trucks");
  await go(CH.yard, "Yard floor");
  await expect(page.locator("[data-conversation] li[data-msg]", { hasText: "Counted again, 13 on the pallet" })).toHaveCount(1);
  await expect(box).toHaveValue("");
  await go(CH.deliveries, "Deliveries");
  await expect(box).toHaveValue("half written about the trucks");
});

test("admin callers see New channel in + New menu", async ({ page, request }) => {
  await openAs(page, request, "admin");
  await page.locator("[data-action='toggle-new-menu']").click();
  await expect(page.locator("[data-action='new-message']")).toBeVisible();
  await expect(page.locator("[data-action='new-channel']")).toBeVisible();
});

test("non-admin callers do not see New channel in + New menu", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await page.locator("[data-action='toggle-new-menu']").click();
  await expect(page.locator("[data-action='new-message']")).toBeVisible();
  await expect(page.locator("[data-action='new-channel']")).toHaveCount(0);
});

test("system messages show source label, real SVG icon, deep-link pill, and zero System author labels", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();
  const conversation = page.locator("[data-conversation]");
  const eventMsg = conversation.locator("li[data-msg][data-kind='EVENT']").first();
  await expect(eventMsg).toBeVisible();
  // Zero System author labels
  await expect(conversation.locator(".who", { hasText: /^System/ })).toHaveCount(0);
  // Source label derived from topic or channel
  await expect(eventMsg.locator(".who")).toContainText("Credit");
  // Real SVG icon present
  await expect(eventMsg.locator("svg[data-icon='shield']")).toBeVisible();
  // Deep-link action pill
  await expect(eventMsg.locator("[data-action='open-entity']")).toContainText("Open SO-1042");
});

test("quick replies populate composer, avatar initials render, @mention chips highlight, and thread side panel opens", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();

  // Quick reply chips
  const quickChips = page.locator("[data-quick-reply]");
  await expect(quickChips).toHaveCount(4);
  await expect(quickChips.nth(0)).toHaveText("On it");
  await quickChips.nth(0).click();
  await expect(page.locator("textarea[data-field='draft']")).toHaveValue("On it");

  // Avatar initials
  const firstMsg = page.locator("[data-conversation] li[data-msg]").first();
  await expect(firstMsg.locator("[data-avatar]")).toHaveText("YM");

  // Post message with @mention chip
  await page.locator("textarea[data-field='draft']").fill("Hello @Elena please verify door 2");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const lastMsg = page.locator("[data-conversation] li[data-msg]").last();
  await expect(lastMsg.locator(".mention-chip")).toHaveText("@Elena");

  // Thread side panel
  await page.locator("[data-action='open-thread']").first().click();
  const threadPanel = page.locator("[data-thread-panel]");
  await expect(threadPanel).toBeVisible();
  await expect(threadPanel.getByRole("heading", { name: "Thread" })).toBeVisible();
  await expect(threadPanel.locator("textarea[data-field='thread-draft']")).toBeVisible();
});

test("at tablet width the left sidebar collapses when a thread is open", async ({ page, request }) => {
  await page.setViewportSize({ width: 1112, height: 868 });
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();

  const sidebar = page.locator("[data-channel-list]");
  await expect(sidebar).toBeVisible();

  await page.locator("[data-action='open-thread']").first().click();
  const threadPanel = page.locator("[data-thread-panel]");
  await expect(threadPanel).toBeVisible();
  await expect(sidebar).toBeHidden();

  await page.locator("[data-action='close-thread']").click();
  await expect(threadPanel).toBeHidden();
  await expect(sidebar).toBeVisible();
});

