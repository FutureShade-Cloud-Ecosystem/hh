// Yard Pick offline: the item's own acceptance leg, in the field cycle's port
// range, inline (never timeout). The story is the exit test's yard half: the
// world is pulled, the device goes offline, a ticket is claimed and picked
// with a short line, the device comes back, the outbox drains in order, and
// the server wins card shows when the test reassigned the ticket first.
//
// The banner is the chrome package's, fed through its properties; the cards
// are the chrome package's conflict cards; the reads answer from the store.
import { test, expect } from "@playwright/test";
import { repNotices, resetStub, reassignTicket, stubLog, yardHandoff, type LoggedCall } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

const TASK = "0a000000-0000-4000-9000-000000000039"; // SO-1039, queued, four lines

interface PushCall extends LoggedCall {
  body: { ops: Array<Record<string, unknown>> } | null;
}

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

/** The platform's word, delivered by the harness beside the emulator's own
 * events, then repeated if the glass has not answered, the way a real radio
 * re-announces (the layer dispatches only on a change). The machine never
 * sends while the word is offline, so the outbox behaviour the leg asserts
 * does not depend on the network cut itself. */
async function platformWord(page: import("@playwright/test").Page, online: boolean, expectWords: string | null = null): Promise<void> {
  // The emulator cuts with the word, so the app's short watcher and the event
  // carry one truth, never two.
  await page.context().setOffline(!online);
  await page.evaluate((on) => window.dispatchEvent(new Event(on ? "online" : "offline")), online);
  if (expectWords !== null) {
    const banner = page.locator("hh-shell-offline-banner");
    try {
      await expect(banner).toContainText(expectWords, { timeout: 4_000 });
    } catch {
      await page.evaluate((on) => window.dispatchEvent(new Event(on ? "online" : "offline")), online);
      await expect(banner).toContainText(expectWords, { timeout: 6_000 });
    }
  }
}

async function loadBoard(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await expect(page.locator("[data-pick-card]")).toHaveCount(5);
  await expect(page.locator("[data-staleness]")).toContainText("Pulled");
}

/** The pushed operations on one sync scope, once the push landed. */
async function pushedOps(request: import("@playwright/test").APIRequestContext, scope: string): Promise<Array<Record<string, unknown>>> {
  let ops: Array<Record<string, unknown>> = [];
  await expect
    .poll(async () => {
      const pushes = (await stubLog(request)).filter(
        (c): c is PushCall => c.path === `/api/v1/sync/${scope}` && c.method === "POST",
      );
      ops = pushes.flatMap((p) => p.body?.ops ?? []);
      return ops.length;
    })
    .toBeGreaterThan(0);
  return ops;
}

/** The rep's short pick notice for one order, once the drain applied the record. The offline strip says
 * nothing as soon as the platform is back (bannerMode: only "offline" and the like speak) and the pending
 * line leaves with the first local line, so neither says the outbox drained: the push is the fact, and
 * the stub raises the notice while it applies the record op. Poll for it instead of reading once. */
async function shortPickNotice(request: import("@playwright/test").APIRequestContext, orderNo: string): Promise<string> {
  let body = "";
  await expect
    .poll(async () => {
      const n = (await repNotices(request)).find((x) => x.topic === "yard.short_pick" && x.order_no === orderNo);
      body = String((n as { body?: unknown } | undefined)?.body ?? "");
      return body;
    }, { message: `the rep's short pick notice for ${orderNo} (the record op was applied)` })
    .not.toBe("");
  return body;
}

function payloadOf(op: unknown): Record<string, unknown> {
  return ((op as { payload?: unknown })?.payload ?? {}) as Record<string, unknown>;
}

/** Claims SO-1039 offline, starts it and records the pick with a short line: all three wait in the outbox. */
async function claimAndPickOffline(page: import("@playwright/test").Page): Promise<void> {
  await platformWord(page, false, "You're offline");
  // Opening the card is read-only; taking the pick claims and starts it in the outbox.
  await page.locator("[data-pick-card]", { hasText: "SO-1039" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1039");
  await expect(page.locator("[data-task-status]")).toHaveText("Queued");
  await page.getByRole("button", { name: "Take this pick" }).click();
  await expect(page.locator("[data-task-status]")).toHaveText("Picking");
  await expect(page.locator("hh-shell-offline-banner")).toContainText("2 changes in the outbox");
  for (const what of ["Retaining wall block", "HPB bedding sand"]) {
    await page.locator("[data-line]", { hasText: what }).locator(".line-row").click();
    await page.getByRole("button", { name: /^All \d+/ }).click();
    await page.getByRole("button", { name: "Done" }).click();
  }
  // The short line: one of twelve off the shelf, with its reason.
  await page.locator("[data-line]", { hasText: "Paver edge restraint" }).locator(".line-row").click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("radio", { name: "Damaged stock" }).click();
  await page.getByRole("button", { name: "Done" }).click();
  // The record queues as its own operation on the pick_tasks door: saved on
  // the device, shown as waiting, counted in the outbox, nobody told yet.
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Pick saved on this device. It sends when you have signal.");
  await expect(page.locator("[data-pick-pending]")).toContainText("Waiting to send");
  await expect(page.locator("hh-shell-offline-banner")).toContainText("3 changes in the outbox");
}

test("offline, the claim and the pick wait in the outbox, and the banner carries the count", async ({ page }) => {
  await loadBoard(page);
  await claimAndPickOffline(page);
});

test("back online, the outbox drains in order, and a ticket the test reassigned first comes back superseded", async ({ page, request }) => {
  await loadBoard(page);
  await claimAndPickOffline(page);
  // The server reassigns the ticket while the device cannot hear it.
  await reassignTicket(request, TASK);
  // Back online: the outbox drains, in the order the writes were made.
  await platformWord(page, true);
  await expect(page.locator("hh-shell-offline-banner")).toHaveCount(0);
  const ops = await pushedOps(request, "pick_tasks");
  expect(ops.length).toBe(3);
  expect(String(payloadOf(ops[0]).status)).toBe("ACCEPTED");
  expect(String(payloadOf(ops[1]).status)).toBe("IN_PROGRESS");
  expect(Object.keys(payloadOf(ops[2])).sort()).toEqual(["id", "picked"]);
  const picked = payloadOf(ops[2]).picked as Array<Record<string, unknown>>;
  expect(picked).toHaveLength(3);
  for (const line of picked) {
    expect(Object.keys(line).sort()).toEqual(["description", "order_line_no", "product_id", "qty_ordered", "qty_picked", "short_note", "short_reason", "sku", "uom"]);
  }
  // Only fields the core's sync set admits ride the outbox.
  for (const op of ops.slice(0, 2)) expect(Object.keys(payloadOf(op)).filter((k) => !["id", "status"].includes(k))).toEqual([]);
  // The chained writes on one row go blind: the only stale base the device
  // could name is its own first write.
  expect((ops[0] as Record<string, unknown>).base_seq).toBeDefined();
  expect((ops[1] as Record<string, unknown>).base_seq).toBeUndefined();
  expect((ops[2] as Record<string, unknown>).base_seq).toBeUndefined();
  // The server wins: the claim was superseded, and the start and the record
  // landed on a cancelled pick, so each answer is a card, in the board's words.
  await page.getByRole("button", { name: "Back to the queue" }).click();
  await expect(page.getByRole("button", { name: "Review 3" })).toBeVisible();
  await page.getByRole("button", { name: "Review 3" }).click();
  const list = page.locator("[data-conflict-list]");
  await expect(list).toContainText("Superseded on the server");
  await expect(list).toContainText("Server wins · pick tasks");
  await expect(list).toContainText("moved on the server while you were offline");
  await expect(list).toContainText("Not accepted");
  // The record, blind behind the start, reached a cancelled pick: the core refused it, and the card says what the picker
  // must tell the counter (FROM-PRINCIPAL 49), in the yard's words, not the core's.
  await expect(list).toContainText("This pick was cancelled, so your record was not saved. Tell the counter.");
  // The board itself shows the server's row now, not the device's claim.
  await page.getByRole("button", { name: "Back to the queue" }).click();
  await expect(page.locator("[data-pick-card]", { hasText: "SO-1039" }).locator("[data-task-status]")).toHaveText("Cancelled");
  // Dismiss is the exit: one card goes, the others wait.
  await page.getByRole("button", { name: "Review 3" }).click();
  await page.getByRole("button", { name: /Dismiss: Superseded on the server/ }).click();
  await expect(list).toContainText("2 to review");
  // The list is reachable from the board, and the board names what is left.
  await page.getByRole("button", { name: "Back to the queue" }).click();
  await expect(page.getByRole("button", { name: "Review 2" })).toBeVisible();
});

test("offline, a shortage report waits in the outbox and drains clean, and the settle says no signal", async ({ page, request }) => {
  await loadBoard(page);
  await page.locator("[data-pick-card]", { hasText: "SO-1041" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1041");
  await platformWord(page, false, "You're offline");
  await page.getByRole("button", { name: "Report a shortage" }).click();
  await page.getByLabel("How many are short").fill("4");
  await page.getByRole("radio", { name: "Not enough on the yard" }).click();
  await page.getByRole("button", { name: "Report short pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Shortage sent");
  await expect(page.locator("hh-shell-offline-banner")).toContainText("1 changes in the outbox");
  // The settle is the counter's act: its direct door, so offline it says no
  // signal and queues nothing.
  const shortage = page.locator("[data-shortage]", { hasText: "22 SQFT" });
  await shortage.getByRole("button", { name: "Mark it settled" }).click();
  await page.getByLabel("How the shortage was settled").fill("Settled from the bulk pile");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator("[data-error]")).toContainText("Check your signal");
  await expect(page.locator("hh-shell-offline-banner")).toContainText("1 changes in the outbox");
  await platformWord(page, true);
  await expect(page.locator("hh-shell-offline-banner")).toHaveCount(0);
  // The report drained with only the fields the core's shortage set admits.
  const createOps = await pushedOps(request, "shortages");
  expect(createOps.length).toBe(1);
  expect(Object.keys(payloadOf(createOps[0])).sort()).toEqual(["description", "id", "order_line_no", "qty_short", "task_id", "uom"]);
  // No card: the report applied clean.
  await page.getByRole("button", { name: "Back to the queue" }).click();
  await expect(page.getByRole("button", { name: /^Review [1-9]/ })).toHaveCount(0);
});

test("offline, the pick record and a shortage report drain in order and land clean, and the rep is told when they send", async ({ page, request }) => {
  await loadBoard(page);
  await page.locator("[data-pick-card]", { hasText: "SO-1041" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1041");
  await platformWord(page, false, "You're offline");
  // The short line: two of forty six flagstone, with its reason.
  await page.locator("[data-line]", { hasText: "Flagstone" }).locator(".line-row").click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("radio", { name: "Damaged stock" }).click();
  await page.getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Pick saved on this device. It sends when you have signal.");
  await expect(page.locator("[data-notice]")).toContainText("short line goes on backorder and the order's rep is told when it sends");
  await expect(page.locator("[data-pick-pending]")).toBeVisible();
  await expect(page.locator("hh-shell-offline-banner")).toContainText("1 changes in the outbox");
  // The rep has not heard: the record has not left the device.
  expect((await repNotices(request)).filter((n) => n.topic === "yard.short_pick")).toHaveLength(0);
  // The deliberate shortage report queues behind it.
  await page.getByRole("button", { name: "Report a shortage" }).click();
  await page.getByLabel("How many are short").fill("4");
  await page.getByRole("radio", { name: "Not enough on the yard" }).click();
  await page.getByRole("button", { name: "Report short pick" }).click();
  await expect(page.locator("hh-shell-offline-banner")).toContainText("2 changes in the outbox");
  // The skid count is a direct door (it moves the order's skid deposit): with
  // no signal it says so in words and queues nothing.
  await page.getByRole("button", { name: "One skid more" }).click();
  await page.getByRole("radio", { name: "Count corrected" }).click();
  await page.getByRole("button", { name: "Save skid count" }).click();
  await expect(page.locator("[data-error]")).toContainText("Check your signal");
  await expect(page.locator("hh-shell-offline-banner")).toContainText("2 changes in the outbox");
  // Back online: the outbox drains in the order the writes were made.
  await platformWord(page, true);
  await expect(page.locator("hh-shell-offline-banner")).toHaveCount(0);
  await expect(page.locator("[data-pick-pending]")).toHaveCount(0);
  // The record landed: the rep was told, and the server's backorder figure shows.
  expect(await shortPickNotice(request, "SO-1041")).toContain("44.0000 SQFT on backorder");
  const pushes = (await stubLog(request)).filter((c) => c.path.startsWith("/api/v1/sync/") && c.method === "POST");
  const scopes = pushes.map((c) => c.path.slice("/api/v1/sync/".length));
  expect(scopes.indexOf("pick_tasks")).toBeGreaterThanOrEqual(0);
  expect(scopes.indexOf("pick_tasks")).toBeLessThan(scopes.indexOf("shortages"));
  await expect(page.locator("[data-line]", { hasText: "Flagstone" })).toContainText("44 SQFT on backorder");
  // Only the fields the core's doors admit rode the outbox, and no card came back.
  const ops = pushes.flatMap((c) => ((c.body as { ops?: Array<{ payload: Record<string, unknown> }> } | null)?.ops ?? []));
  const record = ops.find((op) => "picked" in op.payload)!;
  expect(Object.keys(record.payload).sort()).toEqual(["id", "picked"]);
  const report = ops.find((op) => "qty_short" in op.payload)!;
  for (const stamped of ["reported_at", "resolved_at", "resolved_by", "resolution"]) expect(Object.keys(report.payload)).not.toContain(stamped);
  await page.getByRole("button", { name: "Back to the queue" }).click();
  await expect(page.getByRole("button", { name: /^Review [1-9]/ })).toHaveCount(0);
});

test("offline, a shortage reported before the record does not make the record come back superseded", async ({ page, request }) => {
  await loadBoard(page);
  await page.locator("[data-pick-card]", { hasText: "SO-1041" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1041");
  await platformWord(page, false, "You're offline");
  // The picker reports the shortage first: on the server it moves the task's seq.
  await page.getByRole("button", { name: "Report a shortage" }).click();
  await page.getByLabel("How many are short").fill("4");
  await page.getByRole("radio", { name: "Not enough on the yard" }).click();
  await page.getByRole("button", { name: "Report short pick" }).click();
  await expect(page.locator("hh-shell-offline-banner")).toContainText("1 changes in the outbox");
  // Then records the pick.
  await page.locator("[data-line]", { hasText: "Flagstone" }).locator(".line-row").click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("radio", { name: "Damaged stock" }).click();
  await page.getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("hh-shell-offline-banner")).toContainText("2 changes in the outbox");
  await platformWord(page, true);
  await expect(page.locator("hh-shell-offline-banner")).toHaveCount(0);
  await expect(page.locator("[data-pick-pending]")).toHaveCount(0);
  // The record applied: the rep was told, and no card came back for the device's own report.
  expect(await shortPickNotice(request, "SO-1041")).toContain("44.0000 SQFT on backorder");
  await page.getByRole("button", { name: "Back to the queue" }).click();
  await expect(page.getByRole("button", { name: /^Review [1-9]/ })).toHaveCount(0);
});
