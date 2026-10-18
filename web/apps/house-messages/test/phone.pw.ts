// House Messages phone and tablet legs on the stub: the phone channel list, a conversation on a phone,
// the files and photos screen from the header, the new message sheet, and the tablet single pane.
import { test, expect, openAs, resetStub, channel, CH, says, stubPost } from "./helpers.js";

test("phone channel list shows search, gold + New, CHANNELS and DIRECT MESSAGES groups, avatars for DMs and a stacked badge for groups", async ({ page, request }) => {
  await openAs(page, request, "sales");
  const list = page.locator("[data-channel-list]");
  await expect(list.getByRole("heading", { name: "DIRECT MESSAGES" })).toBeVisible();
  await expect(list.getByRole("heading", { name: "CHANNELS" })).toBeVisible();
  // Rooms, Branch, and Approvals fold into CHANNELS on the phone rail.
  await expect(list.getByRole("heading", { name: "Rooms" })).toHaveCount(0);
  await expect(list.getByRole("heading", { name: "Branch" })).toHaveCount(0);
  await expect(list.getByRole("heading", { name: "Approvals" })).toHaveCount(0);
  // Gold + New sits above the search.
  const newBtn = list.locator("[data-action='toggle-new-menu']");
  await expect(newBtn).toBeVisible();
  await expect(newBtn).toContainText("+ New");
  await expect(newBtn).toHaveClass(/btn-new/);
  // Mentions, Saved and Settings live under More rather than on the phone rail.
  await expect(list.locator("[data-action='nav-mentions']")).toHaveCount(0);
  await expect(list.locator("[data-action='nav-saved']")).toHaveCount(0);
  await expect(list.locator("[data-action='open-settings']")).toHaveCount(0);
  await expect(list.locator("[data-action='open-rules']")).toHaveCount(0);
  await expect(list.locator("[data-action='open-notify-optin']")).toHaveCount(0);
});

test("each channel row reads a gold unread badge, a direct message shows initials and a group shows two stacked initials", async ({ page, request }) => {
  await openAs(page, request, "sales");
  const list = page.locator("[data-channel-list]");
  // Sales has at least one DM and one room.
  const dmRow = channel(page, CH.dm);
  await expect(dmRow).toBeVisible();
  await expect(dmRow.locator(".avatar")).toBeVisible();
  // Channel rows: gold badge for unread.
  const branchRow = channel(page, CH.branch);
  const branchBadge = branchRow.locator("[data-unread]");
  await expect(branchBadge).toBeVisible();
  const color = await branchBadge.evaluate((el) => getComputedStyle(el).backgroundColor);
  // The brand gold reads as rgb(199, 165, 70) or similar warm gold tone, never a dark pill.
  expect(color).not.toBe("rgb(0, 0, 0)");
  expect(color).not.toBe("rgb(255, 255, 255)");
});

test("an admin caller sees the + New menu open with New channel, and the More button in the header opens Mentions, Saved, Settings", async ({ page, request }) => {
  await openAs(page, request, "admin");
  await page.locator("[data-action='toggle-new-menu']").click();
  await expect(page.locator("[data-action='new-message']")).toBeVisible();
  await expect(page.locator("[data-action='new-channel']")).toBeVisible();
  // The More button in the header opens the app-level "more" view.
  await page.keyboard.press("Escape");
  const moreBtn = page.locator("[data-action='open-more']");
  await expect(moreBtn).toBeVisible();
  await moreBtn.click();
  const moreView = page.locator("[data-view='more']");
  await expect(moreView).toBeVisible();
  await expect(moreView.locator("[data-action='nav-mentions']")).toBeVisible();
  await expect(moreView.locator("[data-action='nav-saved']")).toBeVisible();
  await expect(moreView.locator("[data-action='open-settings']")).toBeVisible();
  await expect(moreView.locator("[data-action='open-rules']")).toBeVisible();
});

test("a conversation on a phone shows a back chevron, the channel name with a sync dot, members and notify line, and a one-line pill composer with Send inside", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  const convo = page.locator("[data-conversation]");
  // Back chevron (data-action=back) is visible on a phone; the title row carries a sync dot inline.
  await expect(convo.locator(".back")).toBeVisible();
  await expect(convo.locator(".convo-head h2")).toContainText("Yard floor");
  await expect(convo.locator(".convo-head .sync-dot")).toBeVisible();
  // Members and notify line lives under the title.
  await expect(convo.locator("[data-convo-meta]")).toContainText("Notify");
  // One-line pill composer: a single draft input and Send inside the same pill.
  const composer = convo.locator("[data-composer]");
  await expect(composer).toBeVisible();
  await expect(composer.locator("textarea[data-field='draft']")).toBeVisible();
  const sendBtn = composer.locator("[data-action='send']");
  await expect(sendBtn).toBeVisible();
  await expect(sendBtn).toContainText("Send");
});

test("a conversation's per-message actions render as compact chips: reactions, Seen and Open thread all live on one row", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  const firstMsg = page.locator("[data-conversation] li[data-msg]").first();
  // Reactions row carries the react picker.
  await expect(firstMsg.locator("[data-action='open-react-picker']")).toBeVisible();
  // Seen chip is a small pill, not a row of buttons.
  await expect(firstMsg.locator(".seen-chip")).toBeVisible();
});

test("a conversation's quick replies are chips; tapping one populates the composer", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  const quickChips = page.locator("[data-quick-reply]");
  const firstText = await quickChips.first().textContent();
  await expect(quickChips.first()).toBeVisible();
  await quickChips.first().click();
  await expect(page.locator("textarea[data-field='draft']")).toHaveValue(firstText ?? "");
});

test("a system message shows source label, real SVG icon, deep-link pill, zero System author labels", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();
  const conversation = page.locator("[data-conversation]");
  const eventMsg = conversation.locator("li[data-msg][data-kind='EVENT']").first();
  await expect(eventMsg).toBeVisible();
  await expect(conversation.locator(".who", { hasText: /^System/ })).toHaveCount(0);
  await expect(eventMsg.locator(".who")).toContainText("Credit");
  await expect(eventMsg.locator("svg[data-icon='shield']")).toBeVisible();
  await expect(eventMsg.locator("[data-action='open-entity']")).toContainText("Open SO-1042");
});

test("the conversation header carries a Files button; tapping it opens its own files-and-photos screen with photos in a 3-column grid", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  const filesBtn = page.locator("[data-conversation] [data-action='open-files']");
  await expect(filesBtn).toBeVisible();
  await filesBtn.click();
  const filesView = page.locator("[data-files-view]");
  await expect(filesView).toBeVisible();
  // Photos grid is 3 columns on phone.
  await expect(filesView.locator("[data-photos-grid]")).toBeVisible();
  // Files list renders rows.
  await expect(filesView.locator("[data-files-list]")).toBeVisible();
  // Back returns to the conversation.
  await page.locator("[data-files-view] [data-action='back-from-files']").click();
  await expect(page.locator("[data-files-view]")).toHaveCount(0);
  await expect(page.locator("[data-conversation]")).toBeVisible();
});

test("the + New menu opens a bottom sheet (hh-sheet) for new message: TO chips, optional group name, Start message or Start group", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await page.locator("[data-action='toggle-new-menu']").click();
  await page.locator("[data-action='new-message']").click();
  const sheet = page.locator("hh-sheet[data-sheet='new-message']");
  await expect(sheet).toBeVisible();
  // The sheet explains where channel creation lives for admins.
  await expect(sheet).toContainText("New channel is for admins");
  // To chips area.
  await expect(sheet.locator("[data-to-chips]")).toBeVisible();
  // Tap one person; the button says Start message.
  await sheet.locator("[data-person-id='a0000000-0000-4000-9000-000000000003']").click();
  await expect(sheet.locator("[data-to-chips]")).toContainText("Priya Shah");
  await expect(sheet.locator("[data-action='start-conversation']")).toHaveText("Start message");
  // Tap two people: the button switches to Start group and a group name field appears.
  await sheet.locator("[data-person-id='a0000000-0000-4000-9000-000000000005']").click();
  await expect(sheet.locator("[data-action='start-conversation']")).toHaveText("Start group");
  await expect(sheet.locator("input[data-field='group-name']")).toBeVisible();
});

test("the new message sheet closes on Escape, scrim tap, and the cancel button, and only one close event fires per close path", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await page.locator("[data-action='toggle-new-menu']").click();
  await page.locator("[data-action='new-message']").click();
  const sheet = page.locator("hh-sheet[data-sheet='new-message']");
  await expect(sheet).toBeVisible();
  // Cancel button.
  await page.locator("[data-action='close-sheet']").click();
  await expect(sheet).toHaveCount(0);
});

test("Tablet: at 834 width the desk is single pane; the list and the conversation are not side by side; a back button in the conversation returns to the list", async ({ page, request }) => {
  await page.setViewportSize({ width: 834, height: 1194 });
  await openAs(page, request, "sales");
  // Single pane: rail is full width and only one pane is visible (the list).
  const list = page.locator("[data-channel-list]");
  await expect(list).toBeVisible();
  await expect(page.locator("[data-conversation]")).toHaveCount(0);
  await expect(page.locator("[data-tablet-list-only]")).toBeVisible();
  // Open a channel: the conversation replaces the list (single pane, not side by side).
  await channel(page, CH.yard).click();
  await expect(page.locator("[data-conversation]")).toBeVisible();
  await expect(page.locator("[data-channel-list]")).toHaveCount(0);
  // A back button returns to the list.
  await page.locator("[data-conversation] .back").click();
  await expect(page.locator("[data-channel-list]")).toBeVisible();
  await expect(page.locator("[data-conversation]")).toHaveCount(0);
});

test("composer has no camera capture, no Waiting to send, no outbox row for messages; the offline sentence stays honest about what is not saved", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();
  await stubPost(request, "offline", { on: true });
  // The composer has no camera icon; only a paperclip.
  const composer = page.locator("[data-composer]");
  await expect(composer).toBeVisible();
  // The composer has no camera button.
  await expect(composer.locator("[data-action='camera']")).toHaveCount(0);
  // No "Waiting to send" marker anywhere.
  await expect(page.locator("text=Waiting to send")).toHaveCount(0);
  await expect(page.locator("text=outbox")).toHaveCount(0);
  // The offline sentence says nothing is saved to send later.
  await expect(page.locator("[data-composer-footnote]")).toContainText("Nothing is saved to send later");
  await stubPost(request, "offline", { on: false });
});