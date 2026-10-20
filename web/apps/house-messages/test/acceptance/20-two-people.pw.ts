// The two people story on the demo image: one person posts to a room, the other sees it unread, reads
// it, replies in its thread and sets how loud the room is; the first sees the reply; a customer message
// lands in the house queue and the other person claims it. Every step is read back from the core.
//
// Not run here: the forced acknowledgement. The demo image runs the core's serve role alone, so the
// outbox consumer that turns a credit hold or a blocked order into a forced acknowledgement never runs,
// and a person's own post can never require one. The acknowledgement legs run on the stub
// (test/ack.pw.ts). This file asserts the honest state: nothing waiting, so no bubble.
import { test, expect, arriveFromFrontDoor, channelBySlug, channelsOf, secondPerson, shot, staff } from "./helpers.js";
import { channelTitle } from "../../src/messages-logic.js";

const stamp = `acc-${Date.now()}`;

test.describe.configure({ mode: "serial" });

test("one person posts to a room and sees their own message", async ({ page }, testInfo) => {
  await arriveFromFrontDoor(page, "sales");
  const office = await channelBySlug(page, "sales", "office");
  await page.locator(`[data-action='open-channel'][data-id='${office.id}']`).click();
  await expect(page.locator("[data-conversation] h2")).toHaveText(channelTitle(office as any));
  await page.locator("textarea[data-field='draft']").fill(`Pallets staged at door 2 ${stamp}`);
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const last = page.locator("[data-conversation] li[data-msg]").last();
  await expect(last).toContainText(`Pallets staged at door 2 ${stamp}`);
  await expect(last).toContainText("You");
  const core = await staff(page, "sales", "GET", `/api/v1/messaging/channels/${office.id}/messages?limit=5`);
  expect(core.body.messages.some((m: { body: string }) => m.body === `Pallets staged at door 2 ${stamp}`)).toBe(true);
  await shot(page, "03-posted", testInfo);
});

test("the other person sees it unread, reads it, and the core's count falls to nothing", async ({ browser, page }, testInfo) => {
  const yard = await secondPerson(browser, "yard-manager");
  const office = await channelBySlug(page, "yard-manager", "office");
  const row = yard.locator(`[data-action='open-channel'][data-id='${office.id}']`);
  await expect(row.locator("[data-unread]")).toHaveText("1");
  const before = await staff(page, "yard-manager", "GET", "/api/v1/messaging/unread");
  expect(before.body.channels.find((c: { channel_id: string }) => c.channel_id === office.id)?.unread_count).toBe(1);
  await row.click();
  await expect(yard.locator("[data-conversation] li[data-msg]").last()).toContainText(`Pallets staged at door 2 ${stamp}`);
  await expect(row.locator("[data-unread]")).toHaveCount(0);
  const after = await staff(page, "yard-manager", "GET", "/api/v1/messaging/unread");
  expect(after.body.channels.some((c: { channel_id: string }) => c.channel_id === office.id)).toBe(false);
  await shot(yard, "04-read", testInfo);
  // A reply in the message's thread, and the room's loudness set to mentions only.
  await yard.locator("[data-action='toggle-thread']").last().click();
  await yard.locator("[data-action='reply']").last().click();
  await yard.locator("textarea[data-field='draft']").fill(`Copy, door 2 ${stamp}`);
  await yard.getByRole("button", { name: "Send", exact: true }).click();
  await expect(yard.locator("[data-thread]")).toContainText(`Copy, door 2 ${stamp}`);
  await yard.getByRole("button", { name: "Notifications" }).click();
  await yard.locator("[data-prefs]").getByRole("button", { name: "Mentions only", exact: true }).click();
  await expect(yard.locator("[data-prefs] [data-action='set-mode'][aria-pressed='true']")).toHaveText("Mentions only");
  const prefs = await staff(page, "yard-manager", "GET", `/api/v1/messaging/channels/${office.id}/notify-prefs`);
  expect(prefs.body.mode).toBe("MENTIONS");
  expect(prefs.body.inherited ?? false).toBe(false);
  await yard.context().close();
});

test("the poster sees the reply count and the reply, as the core holds them", async ({ page }, testInfo) => {
  await arriveFromFrontDoor(page, "sales");
  const office = await channelBySlug(page, "sales", "office");
  await page.locator(`[data-action='open-channel'][data-id='${office.id}']`).click();
  const root = page.locator("[data-conversation] li[data-msg]", { hasText: `Pallets staged at door 2 ${stamp}` });
  await expect(root.locator("[data-action='toggle-thread']")).toHaveText("1 reply");
  await root.locator("[data-action='toggle-thread']").click();
  await expect(root.locator("[data-thread]")).toContainText(`Copy, door 2 ${stamp}`);
  await shot(page, "05-poster-sees-the-reply", testInfo);
});

test("a customer message lands in the house queue and the other person claims it, sticky", async ({ browser, page }, testInfo) => {
  // The branch is the yard's own: the room that carries the yard's name names its branch.
  const yardRoom = ((await channelsOf(page, "sales")) as Array<{ slug?: string; branch_id?: string }>).find((c) => c.slug === "yard-point-loma");
  expect(yardRoom?.branch_id, "the yard room names its branch").toBeTruthy();
  const branch = { id: yardRoom!.branch_id as string };
  // A customer the world holds: the first row of the receivables register, read as Payables and Receivables.
  const register = await staff(page, "payables-receivables", "GET", "/api/v1/ar/register?limit=1");
  expect(register.status).toBe(200);
  const first = register.body.items[0] as { customer_id: string; customer_name?: string };
  const customer = { id: first.customer_id, name: first.customer_name };
  const inbound = await staff(page, "sales", "POST", "/api/v1/messaging/inbound/customer", {
    customer_id: customer.id,
    customer_name: customer.name ?? "A customer",
    branch_id: branch.id,
    body: `Can I get a delivery window for Friday ${stamp}`,
    event_key: `accept-${stamp}`,
    channel: "PORTAL",
  });
  expect(inbound.status).toBe(200);
  testInfo.annotations.push({ type: "routed", description: `${inbound.body.kind} ${inbound.body.level} ${inbound.body.channel_slug} inserted=${inbound.body.inserted}` });
  expect(inbound.body.kind).toBe("HOUSE");
  // The branch's own house room is not the one the core picks on this image: it maps the branch's code
  // (PLM) to a room name (yard-plm, house-plm) that the seeded rooms (yard-point-loma) do not carry, so an
  // inbound customer message lands on the global floor. The screen shows the queue where the core files it.
  const house = await channelBySlug(page, "yard-manager", testInfo.annotations.find((a) => a.type === "routed")?.description?.split(" ")[2] ?? "house-unassigned");
  const yard = await secondPerson(browser, "yard-manager");
  await yard.locator(`[data-action='open-channel'][data-id='${house.id}']`).click();
  const queue = yard.locator("[data-queue]");
  await expect(queue).toContainText("Customers waiting");
  const row = queue.locator("[data-queue-row]").first();
  await expect(row).toContainText("waiting");
  await shot(yard, "06-house-queue", testInfo);
  await row.getByRole("button", { name: "Claim" }).click();
  await expect(yard.locator("[data-queue-note]")).toContainText("Their next message goes straight to your DM.");
  const core = await staff(page, "yard-manager", "GET", `/api/v1/messaging/channels/${house.id}/house-queue`);
  const claimed = core.body.conversations.find((c: { claimed_by?: string }) => c.claimed_by);
  expect(claimed, "the core reports the claim").toBeDefined();
  await expect(queue.locator("[data-queue-row]").first()).toContainText("Claimed by");
  await yard.context().close();
});

test("nothing waits for an acknowledgement on this image, so there is no bubble to dismiss", async ({ page }) => {
  await arriveFromFrontDoor(page, "sales");
  const waiting = await staff(page, "sales", "GET", "/api/v1/messaging/unacked");
  expect(waiting.status).toBe(200);
  expect(waiting.body.messages).toEqual([]);
  await expect(page.locator("[data-bubble]")).toHaveCount(0);
});
