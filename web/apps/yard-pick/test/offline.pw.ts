// Yard Pick offline: the item's own acceptance leg, in the field cycle's port
// range, inline (never timeout). The story is the exit test's yard half: the
// world is pulled, the device goes offline, a ticket is claimed and picked
// with a short line, the device comes back, the outbox drains in order, and
// the server wins card shows when the test reassigned the ticket first.
//
// The banner is the chrome package's, fed through its properties; the cards
// are the chrome package's conflict cards; the reads answer from the store.
import { test, expect } from "@playwright/test";
import { deskResolve, resetStub, reassignTicket, stubLog, yardHandoff, type LoggedCall } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

const TASK = "0a000000-0000-4000-9000-000000000039"; // SO-1039, queued, four lines

interface PushCall extends LoggedCall {
  body: { ops: Array<Record<string, unknown>> } | null;
}

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

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

function payloadOf(op: unknown): Record<string, unknown> {
  return ((op as { payload?: unknown })?.payload ?? {}) as Record<string, unknown>;
}

/** Claims SO-1039 offline and records two full lines and one short line. */
async function claimAndPickOffline(page: import("@playwright/test").Page): Promise<void> {
  await page.context().setOffline(true);
  await expect(page.locator("hh-shell-offline-banner")).toContainText("You're offline");
  // The claim goes through the door: queued, not lost, and the pick opens.
  await page.locator("[data-pick-card]", { hasText: "SO-1039" }).getByRole("button", { name: "Claim" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1039");
  await expect(page.locator("hh-shell-offline-banner")).toContainText("1 changes in the outbox");
  // Starting the pick queues its own operation; the record doors open on it.
  await page.getByRole("button", { name: "Start picking" }).click();
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
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("on backorder");
  await expect(page.locator("hh-shell-offline-banner")).toContainText("3 changes in the outbox");
  // The line's backorder figure reads from the local store's own projection.
  const line = page.locator("[data-line]", { hasText: "Paver edge restraint" });
  await expect(line).toContainText("on backorder");
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
  await page.context().setOffline(false);
  await expect(page.locator("hh-shell-offline-banner")).toHaveCount(0);
  const ops = await pushedOps(request, "pick_tasks");
  expect(ops.length).toBe(3);
  expect(String(payloadOf(ops[0]).status)).toBe("ACCEPTED");
  expect(String(payloadOf(ops[1]).status)).toBe("IN_PROGRESS");
  const picked = payloadOf(ops[2]).picked as Array<Record<string, unknown>>;
  expect(picked).toHaveLength(3);
  const shortLine = picked.find((l) => l.order_line_no === 3);
  expect(String(shortLine?.qty_picked)).toBe("1");
  expect(String(shortLine?.short_reason)).toBe("DAMAGED");
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

test("offline, a shortage reported and settled waits as a review card for the person", async ({ page, request }) => {
  await loadBoard(page);
  // Online first: the shortage is reported and drains, so the row has a seq.
  await page.locator("[data-pick-card]", { hasText: "SO-1041" }).getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1041");
  await page.getByRole("button", { name: "Report a shortage" }).click();
  await page.getByLabel("How many are short").fill("4");
  await page.getByRole("radio", { name: "Not enough on the yard" }).click();
  await page.getByRole("button", { name: "Report short pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Shortage sent");
  await expect(page.locator("hh-shell-offline-banner")).toHaveCount(0);
  const createOps = await pushedOps(request, "shortages");
  expect(createOps.length).toBe(1);
  const shortageId = String(payloadOf(createOps[0]).id);
  // Offline: the picker settles it; the desk answers the same shortage first.
  await page.context().setOffline(true);
  const shortage = page.locator("[data-shortage]", { hasText: "4 SQFT" });
  await shortage.getByRole("button", { name: "Mark it settled" }).click();
  await page.getByLabel("How the shortage was settled").fill("Settled from the bulk pile");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Shortage cleared");
  await expect(page.locator("hh-shell-offline-banner")).toContainText("1 changes in the outbox");
  await deskResolve(request, shortageId);
  await page.context().setOffline(false);
  // The settle drain lands second, stale against the desk's answer.
  await page.getByRole("button", { name: "Back to the queue" }).click();
  await expect(page.getByRole("button", { name: /Review 1/ })).toBeVisible();
  await page.getByRole("button", { name: /Review 1/ }).click();
  const list = page.locator("[data-conflict-list]");
  await expect(list).toContainText("Needs a person's review");
  await expect(list).toContainText("Manual · shortages");
  await expect(list).toContainText("never merges on its own");
});
