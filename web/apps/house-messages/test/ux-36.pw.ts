// UX-36 Playwright legs:
// - B12 + B15: the strip leads with an h1 ("Nothing unread" or "<n> unread") and the polling line reads "Updates while this page is open".
// - B13: slug-named rooms from the core render dealer labels ("House, Julian", "Portal inbox", "All alerts", "Unassigned", "Drivers, Point Loma").
// - B14: deleting a message asks for confirmation naming the message first, and a deleted message renders zero action buttons.
// - B15: a message with no replies renders a single Reply button.
import { test, expect, accountItem, openAs, channel, CH, stubPost } from "./helpers.js";
import { join } from "node:path";

test("B12 and B15: the page has an h1 leading with the unread summary, and polling says Updates while this page is open", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await stubPost(request, "tickets", { enabled: false });
  await page.reload();

  const heading = page.locator("[data-live] h1[data-total-unread]");
  await expect(heading).toBeVisible();
  await expect(heading).toHaveText("9 unread");
  await expect(page.locator("[data-live-words]")).toHaveText("Updates while this page is open");

  // Once every unread channel is opened and marked read, the h1 leads with Nothing unread.
  for (const id of [CH.branch, CH.yard, CH.deliveries, CH.esc, CH.house, CH.dm]) {
    await channel(page, id).click();
    await expect(channel(page, id).locator("[data-unread]")).toHaveCount(0);
  }
  await expect(heading).toHaveText("Nothing unread");
});

test("B13: core slug-named channels render dealer labels instead of raw slugs", async ({ page, request }) => {
  await page.route("**/api/v1/messaging/channels", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    await route.fulfill({
      status: 200,
      json: {
        channels: [
          { id: "c1000000-0000-4000-9000-000000000001", kind: "TEAM", slug: "house-julian", name: "#house-julian", topic: "", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
          { id: "c1000000-0000-4000-9000-000000000002", kind: "TOPIC", slug: "portal-inbox", name: "#portal-inbox", topic: "", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
          { id: "c1000000-0000-4000-9000-000000000003", kind: "TEAM", slug: "alerts-all", name: "#alerts-all", topic: "", tier: "FIREHOSE", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
          { id: "c1000000-0000-4000-9000-000000000004", kind: "TEAM", slug: "house-unassigned", name: "#house-unassigned", topic: "", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
          { id: "c1000000-0000-4000-9000-000000000005", kind: "TEAM", slug: "drivers-point-loma", name: "#drivers-point-loma", topic: "", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
        ],
      },
    });
  });

  await openAs(page, request, "sales");
  const list = page.locator("[data-channel-list]");
  await expect(list.locator(".ch-name", { hasText: "House, Julian" })).toBeVisible();
  await expect(list.locator(".ch-name", { hasText: "Portal inbox" })).toBeVisible();
  await expect(list.locator(".ch-name", { hasText: "All alerts" })).toBeVisible();
  await expect(list.locator(".ch-name", { hasText: "Unassigned" })).toBeVisible();
  await expect(list.locator(".ch-name", { hasText: "Drivers, Point Loma" })).toBeVisible();
  await expect(list.locator(".ch-name", { hasText: /^#/ })).toHaveCount(0);
});

test("B14: deleting a message requires confirmation naming the message, and a deleted message shows no actions", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.branch).click();

  await page.locator("textarea[data-field='draft']").fill("Check pallet 9 before loading");
  await page.getByRole("button", { name: "Send", exact: true }).click();

  const ownMsg = page.locator("[data-conversation] li[data-msg].mine").last();
  await expect(ownMsg).toContainText("Check pallet 9 before loading");

  // Pressing Delete opens the confirmation naming the message instead of deleting at once.
  await ownMsg.locator("[data-action='delete-msg']").click();
  const confirmBox = ownMsg.locator("[data-delete-confirm]");
  await expect(confirmBox).toBeVisible();
  await expect(confirmBox).toContainText("Check pallet 9 before loading");
  await expect(ownMsg.locator("[data-body]")).toHaveText("Check pallet 9 before loading");

  // Cancel keeps the message intact.
  await confirmBox.locator("[data-action='cancel-delete-msg']").click();
  await expect(confirmBox).toHaveCount(0);
  await expect(ownMsg.locator("[data-body]")).toHaveText("Check pallet 9 before loading");

  // Confirming deletes the message and removes every action button on the tombstone.
  await ownMsg.locator("[data-action='delete-msg']").click();
  await ownMsg.locator("[data-action='confirm-delete-msg']").click();
  await expect(ownMsg.locator("[data-body]")).toHaveText("This message was deleted");
  await expect(ownMsg.locator("button")).toHaveCount(0);
});

for (const size of [
  { width: 390, height: 844 },
  { width: 1440, height: 900 },
]) {
  for (const theme of ["light", "brand"] as const) {
    test(`r3 P2: the delete confirm is keyboard complete at ${size.width}x${size.height} (${theme})`, async ({ page, request }) => {
      await page.setViewportSize(size);
      await openAs(page, request, "sales");
      if (theme === "brand") await accountItem(page, "Brand theme");
      await channel(page, CH.branch).click();
      await page.locator("textarea[data-field='draft']").fill("Check pallet 9 before loading");
      await page.getByRole("button", { name: "Send", exact: true }).click();
      const ownMsg = page.locator("[data-conversation] li[data-msg].mine").last();
      await expect(ownMsg).toContainText("Check pallet 9 before loading");
      const del = ownMsg.locator("[data-action='delete-msg']");
      const cancel = ownMsg.locator("[data-action='cancel-delete-msg']");
      const confirm = ownMsg.locator("[data-action='confirm-delete-msg']");

      // Open by keyboard: focus lands on Cancel, the safe default.
      await del.focus();
      await page.keyboard.press("Enter");
      await expect(ownMsg.locator("[data-delete-confirm]")).toBeVisible();
      await expect(cancel).toBeFocused();

      // Tab stays inside the confirm, both ways.
      await page.keyboard.press("Tab");
      await expect(confirm).toBeFocused();
      await page.keyboard.press("Tab");
      await expect(cancel).toBeFocused();
      await page.keyboard.press("Shift+Tab");
      await expect(confirm).toBeFocused();

      // Escape closes it as Cancel and returns focus to the message's Delete control.
      await page.keyboard.press("Escape");
      await expect(ownMsg.locator("[data-delete-confirm]")).toHaveCount(0);
      await expect(del).toBeFocused();
      await expect(ownMsg.locator("[data-body]")).toHaveText("Check pallet 9 before loading");

      // Cancel by keyboard returns focus to Delete too.
      await page.keyboard.press("Enter");
      await expect(cancel).toBeFocused();
      await page.keyboard.press("Enter");
      await expect(ownMsg.locator("[data-delete-confirm]")).toHaveCount(0);
      await expect(del).toBeFocused();

      // Delete message: the tombstone has no controls, so focus moves on to a control or the list, never body.
      await page.keyboard.press("Enter");
      await page.keyboard.press("Tab");
      await expect(confirm).toBeFocused();
      await page.keyboard.press("Enter");
      await expect(ownMsg.locator("[data-body]")).toHaveText("This message was deleted");
      const focusedInside = await page.evaluate(() => {
        let node: Element | null = document.activeElement;
        while (node?.shadowRoot?.activeElement) node = node.shadowRoot.activeElement;
        const conv = node?.getRootNode() instanceof ShadowRoot ? (node.getRootNode() as ShadowRoot).querySelector("[data-conversation]") : null;
        return node !== null && node !== document.body && conv !== null && conv.contains(node);
      });
      expect(focusedInside).toBe(true);
    });
  }
}

test("r3 P3: the h1 sits outside the status region, which stays announced", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await expect(page.locator("[data-live] h1[data-total-unread]")).toBeVisible();
  expect(await page.locator("[data-live] h1[data-total-unread]").evaluate((el) => el.closest("[role='status']"))).toBeNull();
  await expect(page.locator("[data-live] [role='status'] [data-live-words]")).toBeVisible();
});

test("r3 P3: the room list carries one heading, Channels, and no Rooms heading", async ({ page, request }) => {
  await openAs(page, request, "sales");
  const list = page.locator("[data-channel-list]");
  await expect(list.getByRole("heading", { name: "Channels" })).toHaveCount(1);
  await expect(list.getByRole("heading", { name: "Rooms" })).toHaveCount(0);
});

test("B15: a message with zero replies shows a single Reply action", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await channel(page, CH.yard).click();

  const noReplyMsg = page.locator("[data-conversation] li[data-msg]").nth(1);
  await expect(noReplyMsg).toContainText("Gate code for the second yard");
  await expect(noReplyMsg.getByRole("button", { name: "Reply", exact: true })).toHaveCount(1);
});

const YARD_DEMO_CHANNELS = [
  { id: "a5000000-0000-4000-9000-000000000001", kind: "BRANCH", branch_id: "5d1b3f6e-0000-4000-9000-000000000001", slug: "yard-plm", name: "#yard-point-loma", topic: "Point Loma yard, releases, receiving, delivery scheduling", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
  { id: "a5000000-0000-4000-9000-000000000271", kind: "TEAM", branch_id: "5d1b3f6e-0000-4000-9000-000000000001", slug: "house-plm", name: "#house-point-loma", topic: "Unassigned customer messages, Point Loma. Claim one to make it yours.", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
  { id: "a5000000-0000-4000-9000-000000000003", kind: "TEAM", slug: "office", name: "#office", topic: "Company-wide office channel, announcements and cross-branch coordination", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
  { id: "a5000000-0000-4000-9000-000000000006", kind: "TOPIC", slug: "portal-inbox", name: "#portal-inbox", topic: "Contractor portal inbound, new portal contacts land here", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
  { id: "a5000000-0000-4000-9000-000000000269", kind: "TEAM", slug: "escalations", name: "#escalations", topic: "Escalations, events that must never be missed. This channel cannot be muted.", tier: "ESCALATION", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
  { id: "a5000000-0000-4000-9000-000000000005", kind: "TEAM", slug: "alerts-all", name: "#alerts-all", topic: "Company-wide critical alerts, escalations reach everyone", tier: "FIREHOSE", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
  { id: "a5000000-0000-4000-9000-000000000270", kind: "TEAM", slug: "house-unassigned", name: "#house-unassigned", topic: "Unassigned customer messages with no branch, the last-resort house queue.", tier: "STANDARD", visibility: "PUBLIC", is_archived: false, unread_count: 0, mentioned: false },
];

test("item 33 (r2): yard.demo sees only its home branch rooms in House Messages", async ({ page, request }) => {
  await page.route("**/api/v1/messaging/channels", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    await route.fulfill({ status: 200, json: { channels: YARD_DEMO_CHANNELS } });
  });
  await openAs(page, request, "yard-team");
  const list = page.locator("[data-channel-list]");
  await expect(list.locator(".ch-name", { hasText: "Yard, Point Loma" })).toBeVisible();
  await expect(list.locator(".ch-name", { hasText: "House, Point Loma" })).toBeVisible();
  await expect(list.locator(".ch-name", { hasText: "Julian" })).toHaveCount(0);
});

const shotDir = process.env.UX36_SHOT_DIR ?? "";
for (const size of [
  { width: 390, height: 844 },
  { width: 1024, height: 768 },
]) {
  for (const theme of ["light", "brand"] as const) {
    test(`UX-36 screenshots at ${size.width}x${size.height} (${theme})`, async ({ page, request }) => {
      test.skip(shotDir === "", "set UX36_SHOT_DIR to capture screenshots");
      await page.setViewportSize(size);
      await openAs(page, request, "sales");
      await stubPost(request, "tickets", { enabled: false });
      await page.reload();
      await expect(page.locator("[data-live-words]")).toHaveText("Updates while this page is open");
      if (theme === "brand") await accountItem(page, "Brand theme");
      await page.screenshot({ path: join(shotDir, `ux36-list-${theme}-${size.width}x${size.height}.png`), fullPage: true });

      await channel(page, CH.branch).click();
      await page.locator("textarea[data-field='draft']").fill("Check pallet 9 before loading");
      await page.getByRole("button", { name: "Send", exact: true }).click();
      const ownMsg = page.locator("[data-conversation] li[data-msg].mine").last();
      await ownMsg.locator("[data-action='delete-msg']").click();
      await expect(ownMsg.locator("[data-delete-confirm]")).toBeVisible();
      await page.screenshot({ path: join(shotDir, `ux36-delete-confirm-${theme}-${size.width}x${size.height}.png`), fullPage: true });
    });

    test(`UX-36 r2 yard.demo screenshots at ${size.width}x${size.height} (${theme})`, async ({ page, request }) => {
      test.skip(shotDir === "", "set UX36_SHOT_DIR to capture screenshots");
      await page.setViewportSize(size);
      await page.route("**/api/v1/messaging/channels", async (route) => {
        if (route.request().method() !== "GET") return route.continue();
        await route.fulfill({ status: 200, json: { channels: YARD_DEMO_CHANNELS } });
      });
      await openAs(page, request, "yard-team");
      await stubPost(request, "tickets", { enabled: false });
      await page.reload();
      await expect(page.locator("[data-live-words]")).toHaveText("Updates while this page is open");
      if (theme === "brand") await accountItem(page, "Brand theme");
      const modeName = theme === "brand" ? "charcoal" : "light";
      await page.screenshot({ path: join(shotDir, `r2-yard-demo-${modeName}-${size.width}x${size.height}.png`), fullPage: true });
    });
  }
}
