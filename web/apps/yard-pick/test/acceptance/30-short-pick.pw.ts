// The yard team's story, part two: the short pick. A line recorded short
// with a reason backorders the line and the order's rep is notified (Phase 0
// item 6), proved through the rep's own API read (the Sales login's channel
// list and message read) until House Messages is live. Written against PR
// 274's notice wiring and proved on the demo image.
import { expect, test } from "./helpers.js";
import { TRENTON, arriveFromFrontDoor, mint, queue, shot, staff } from "./helpers.js";

test("a short pick with a reason backorders the line and the rep's read holds the notice", async ({ page }, testInfo) => {
  await arriveFromFrontDoor(page);
  const answer = await queue(page);
  expect(answer.status).toBe(200);
  const open = (answer.body.items as any[]).filter((e) => e.task.status === "QUEUED" || e.task.status === "PUSHED");
  expect(open.length).toBeGreaterThan(0);
  const id = open[0].task.id;

  await staff(page, "POST", `/api/v1/tasks/${id}/claim`);
  await staff(page, "POST", `/api/v1/tasks/${id}/accept`);
  const detail = await staff(page, "GET", `/api/v1/yard/picks/${id}`);
  const first = detail.body.ticket.lines[0];
  const ordered = Number(first.qty_ordered);
  expect(ordered).toBeGreaterThan(1);
  const picked = ordered - 1;
  const recorded = await staff(page, "PUT", `/api/v1/yard/picks/${id}/lines`, {
    lines: [
      {
        order_line_no: first.line_no,
        product_id: first.product_id ?? "",
        sku: first.sku ?? "",
        description: first.description,
        qty_ordered: first.qty_ordered,
        qty_picked: String(picked),
        uom: first.uom,
        short_reason: "NOT_IN_YARD",
      },
    ],
  });
  expect(recorded.status).toBe(200);
  expect(recorded.body.items[0].short_reason).toBe("NOT_IN_YARD");

  // The re-read ticket holds the backorder on the line.
  const after = await staff(page, "GET", `/api/v1/yard/picks/${id}`);
  const lineAfter = after.body.ticket.lines.find((l: any) => l.line_no === first.line_no);
  expect(Number(lineAfter.qty_backordered)).toBe(1);

  // The rep's API read: the Sales login lists its channels, the rep's DM
  // channel carries the notice. THE REP'S OWN READ, not the yard's.
  const rep = await mint("sales");
  const channels = await page.request.get("/api/v1/messaging/channels", { headers: { Authorization: `Bearer ${rep.token}`, "X-Org": "hh-demo" } });
  expect(channels.status()).toBe(200);
  const list = (await channels.json()) as { items?: any[] } | any[];
  const channelList = Array.isArray(list) ? list : (list.items ?? []);
  expect(channelList.length).toBeGreaterThan(0);
  let notice: any = null;
  for (const channel of channelList) {
    const messages = await page.request.get(`/api/v1/messaging/channels/${channel.id}/messages?limit=50`, {
      headers: { Authorization: `Bearer ${rep.token}`, "X-Org": "hh-demo" },
    });
    if (!messages.ok()) continue;
    const page0 = (await messages.json()) as any;
    for (const message of page0.messages ?? page0.items ?? []) {
      const ref = typeof message.ref === "string" ? message.ref : JSON.stringify(message.ref ?? {});
      if (ref.includes("yard.short_pick")) {
        notice = message;
        break;
      }
    }
    if (notice !== null) break;
  }
  expect(notice, "the rep's channels hold the short pick notice").not.toBeNull();
  expect(String(notice.body)).toContain("on backorder");
  expect(String(notice.body)).toContain("Not in the yard");
  await shot(page, "short-pick", testInfo);
});

test("the screen tells the yard the same story in the server's words", async ({ page }) => {
  await arriveFromFrontDoor(page);
  const answer = await queue(page);
  const open = (answer.body.items as any[]).filter((e) => e.task.status === "QUEUED" || e.task.status === "PUSHED");
  const id = open[0].task.id;
  await staff(page, "POST", `/api/v1/tasks/${id}/claim`);
  await staff(page, "POST", `/api/v1/tasks/${id}/accept`);
  const detail = await staff(page, "GET", `/api/v1/yard/picks/${id}`);
  const first = detail.body.ticket.lines[0];
  await page.getByRole("button", { name: "Start picking" }).waitFor({ state: "attached" }).catch(() => {});
  await page.reload();
  await page.locator("[data-pick-card]", { hasText: "Claimed by you" }).first().getByRole("button", { name: "Open" }).click();
  await page.getByRole("button", { name: "Start picking" }).click();
  await page.locator("[data-line]").first().locator(".line-row").click();
  await page.getByRole("button", { name: /^All \d+/ }).click();
  await page.getByRole("button", { name: "Take one off" }).click();
  await page.getByLabel(/Why line \d+ is short/).selectOption("NOT_IN_YARD");
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("on backorder and the order's rep has been told");
});
