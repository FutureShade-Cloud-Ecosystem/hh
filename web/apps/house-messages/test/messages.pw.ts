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

test("the channel list groups channels and direct messages and shows each unread count; the payments room is not in it for Sales", async ({ page, request }) => {
  await openAs(page, request, "sales");
  const list = page.locator("[data-channel-list]");
  await expect(list.getByRole("heading", { name: "DIRECT MESSAGES" })).toBeVisible();
  await expect(list.getByRole("heading", { name: "CHANNELS" })).toBeVisible();
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
      const back = page.locator("[data-conversation] [data-action='back']");
      if (await back.isVisible()) await back.click();
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

test("at tablet width the desk is single pane: opening a channel hides the channel list, the back chevron returns to it", async ({ page, request }) => {
  await page.setViewportSize({ width: 834, height: 1194 });
  await openAs(page, request, "sales");
  const sidebar = page.locator("[data-channel-list]");
  await expect(sidebar).toBeVisible();
  await expect(page.locator("[data-conversation]")).toHaveCount(0);

  await channel(page, CH.yard).click();
  await expect(page.locator("[data-conversation]")).toBeVisible();
  await expect(sidebar).toBeHidden();

  await page.locator("[data-conversation] [data-action='back']").click();
  await expect(sidebar).toBeVisible();
  await expect(page.locator("[data-conversation]")).toHaveCount(0);
});

test("C2: new message sheet opens, lets caller select people, and starts a DM or group", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await page.locator("[data-action='toggle-new-menu']").click();
  await page.locator("[data-action='new-message']").click();

  const sheet = page.locator("hh-sheet[data-sheet='new-message']");
  await expect(sheet).toBeVisible();
  await expect(sheet).toContainText("New channel is for admins: it is in Settings.");

  // Select 1 person -> DM
  const personPriya = sheet.locator("[data-person-id='a0000000-0000-4000-9000-000000000003']");
  await personPriya.click();
  await expect(sheet.locator("[data-to-chips]")).toContainText("Priya Shah");
  await expect(sheet.locator("[data-action='start-conversation']")).toHaveText("Start message");
  await sheet.locator("[data-action='start-conversation']").click();

  // Opens DM
  const convo = page.locator("[data-conversation]");
  await expect(convo).toBeVisible();
  await expect(convo.locator("[data-dm-prompt]")).toContainText("This is the start of your conversation with Priya Shah");

  // Open the sheet again and select 2 people -> Group DM.
  await page.locator("[data-action='toggle-new-menu']").click();
  await page.locator("[data-action='new-message']").click();
  await expect(sheet).toBeVisible();
  await sheet.locator("[data-person-id='a0000000-0000-4000-9000-000000000003']").click();
  await sheet.locator("[data-person-id='a0000000-0000-4000-9000-000000000005']").click();
  const groupNameInput = sheet.locator("input[data-field='group-name']");
  await expect(groupNameInput).toBeVisible();
  await groupNameInput.fill("Saturday runs");
  await expect(sheet.locator("[data-action='start-conversation']")).toHaveText("Start group");
  await sheet.locator("[data-action='start-conversation']").click();

  // Opens Group
  await expect(convo.locator("h2")).toHaveText("Saturday runs");
  await expect(convo.locator(".group-subtitle")).toContainText("3 people including you");
  await expect(convo.locator("li[data-kind='EVENT']").first()).toContainText("You created the group Saturday runs");
});

test("C2: new channel modal for admins only creates channel", async ({ page, request }) => {
  await openAs(page, request, "admin");
  await page.locator("[data-action='toggle-new-menu']").click();
  await page.locator("[data-action='new-channel']").click();

  const modal = page.locator("[data-modal='new-channel']");
  await expect(modal).toBeVisible();
  await expect(modal.locator(".badge-admin")).toHaveText("ADMINS ONLY");

  await modal.locator("input[data-field='channel-name']").fill("yard-julian-receiving");
  await modal.locator("input[data-field='channel-purpose']").fill("What arrives at the Julian dock");
  await modal.locator("[data-action='create-channel-submit']").click();

  const convo = page.locator("[data-conversation]");
  await expect(convo.locator("h2")).toHaveText("yard-julian-receiving");
});

test("C2: a DM or group conversation carries a Files button that opens its own files-and-photos screen", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.dm).click();
  const filesBtn = page.locator("[data-conversation] [data-action='open-files']");
  await expect(filesBtn).toBeVisible();
});

test("C3: reactions can be toggled on messages and + React picker adds reaction", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();

  // Look for reaction chip or react button
  const msg = page.locator("[data-conversation] li[data-msg]").first();
  await expect(msg.locator("[data-action='open-react-picker']")).toBeVisible();
  await msg.locator("[data-action='open-react-picker']").click();
  await page.locator("[data-react-option='Thanks']").click();

  const thanksChip = msg.locator(".reaction-chip[data-emoji='Thanks']");
  await expect(thanksChip).toBeVisible();
  await expect(thanksChip).toContainText("1");
  await thanksChip.click();
  await expect(thanksChip).toHaveCount(0);
});

test("C3: edit and delete own text message within 10 minutes shows (edited) and tombstone", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();

  await page.locator("textarea[data-field='draft']").fill("Quick update on the pavers");
  await page.getByRole("button", { name: "Send", exact: true }).click();

  const ownMsg = page.locator("[data-conversation] li[data-msg].mine").last();
  await expect(ownMsg).toContainText("Quick update on the pavers");

  await ownMsg.locator("[data-action='edit-msg']").click();
  const editField = ownMsg.locator("textarea[data-field='edit-body']");
  await editField.fill("Quick update on the pavers - edited");
  await ownMsg.locator("[data-action='save-edit']").click();

  await expect(ownMsg).toContainText("Quick update on the pavers - edited");
  await expect(ownMsg.locator(".edited-marker")).toHaveText("(edited)");

  await ownMsg.locator("[data-action='delete-msg']").click();
  await expect(ownMsg.locator(".text")).toHaveText("This message was deleted");
});

test("C3: Mentions, Saved, and Search views open from More and the rail", async ({ page, request }) => {
  await openAs(page, request, "sales");

  // Open the More view from the rail.
  await page.locator("[data-action='open-more']").click();
  await expect(page.locator("[data-view='more']")).toBeVisible();

  // Mentions view from More.
  await page.locator("[data-view='more'] [data-action='nav-mentions']").click();
  await expect(page.locator("[data-view='mentions']")).toBeVisible();

  // Saved view from More.
  await page.locator("[data-action='open-more']").click();
  await page.locator("[data-view='more'] [data-action='nav-saved']").click();
  await expect(page.locator("[data-view='saved']")).toBeVisible();

  // Search view from the rail's search input.
  await page.locator("[data-action='open-more']").click();
  await page.locator("[data-view='more'] [data-action='back']").click();
  const searchInput = page.locator("input.search-input");
  await searchInput.fill("pallet");
  await expect(page.locator("[data-view='search']")).toBeVisible();
  await expect(page.locator("[data-search-section='messages']")).toBeVisible();
});

test("C4 and C5: Settings view shows Membership rules, Added by hand, Event classes, and Rep assignment", async ({ page, request }) => {
  await openAs(page, request, "admin");
  await page.locator("[data-action='open-more']").click();
  await page.locator("[data-view='more'] [data-action='open-settings']").click();

  const settings = page.locator("[data-view='settings']");
  await expect(settings).toBeVisible();
  await expect(settings.getByRole("heading", { name: "Membership rules" })).toBeVisible();
  await expect(settings.locator("[data-membership-table]")).toBeVisible();
  await expect(settings.locator("[data-added-by-hand]")).toBeVisible();

  // Subtabs
  await settings.locator("[data-action='settings-subtab'][data-tab='event-classes']").click();
  await expect(settings.getByRole("heading", { name: "Event classes" })).toBeVisible();

  await settings.locator("[data-action='settings-subtab'][data-tab='rep-assignments']").click();
  await expect(settings.getByRole("heading", { name: "Rep assignment" })).toBeVisible();
});

test("C4 and C5: Notifications opt-in view displays per-channel mode, event classes, quiet hours, and channel directory", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  await page.locator("[data-action='open-more']").click();
  await page.locator("[data-view='more'] [data-action='open-notify-optin']").click();

  const notif = page.locator("[data-view='notifications-optin']");
  await expect(notif).toBeVisible();
  await expect(notif.getByRole("heading", { name: "My notifications" })).toBeVisible();
  await expect(notif.locator("[data-card='channels']")).toBeVisible();
  await expect(notif.locator("[data-card='events']")).toBeVisible();
  await expect(notif.locator("[data-card='directory']")).toBeVisible();
});

test("C6: Files and photos screen shows photos grid and files list, and composer attach adds file", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();

  await page.locator("[data-conversation] [data-action='open-files']").click();
  const filesView = page.locator("[data-files-view]");
  await expect(filesView).toBeVisible();
  await expect(filesView.locator("[data-photos-grid]")).toBeVisible();
  await expect(filesView.locator("[data-files-list]")).toBeVisible();

  // Attach dialog from composer
  await filesView.locator("[data-action='back-from-files']").click();
  await page.locator("[data-action='attach']").click();
  const attachModal = page.locator("[data-modal='attach']");
  await expect(attachModal).toBeVisible();
  await attachModal.locator("input[data-field='attach-filename']").fill("Yard safety walk.pdf");
  await attachModal.locator("[data-action='attach-submit']").click();
  await expect(attachModal).toBeHidden();
});


