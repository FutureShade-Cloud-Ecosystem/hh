// HH Drive offline: the item's own acceptance leg, in the field cycle's port
// range, inline (never timeout). The story is the exit test's drive half: the
// run is pulled, the device goes offline, a stop moves and a proof of
// delivery is captured, the device comes back, the outbox drains in order,
// and the server wins card shows when the test moved a stop first.
//
// The proof is the typed name attestation, queued on the pod scope; the photo
// is a direct door and says no signal offline.
//
// The banner is the chrome package's, fed through its properties; the cards
// are the chrome package's conflict cards; the reads answer from the store.
// This leg runs on the wall clock, like the yard's own offline leg: the
// offline proof wants the real connectivity emulation, and the day question
// (the pinned fixture's job) is held by the run sheet legs beside this one.
import { test, expect } from "@playwright/test";
import { TINY_PNG, driverHandoff, resetStub, type LoggedCall } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });
// The boot opens the store's worker, migrates and pulls every scope; a leg
// that starts from a cold page gives that an honest berth, never a silent
// pass (the counts and words below are still asserted exactly).
test.setTimeout(90_000);

test.beforeEach(async ({ request, page }) => {
  // Pin the stub to NOTHING, explicitly: the reset helper remembers the last
  // pin across the suite's files in one worker, and this leg's page runs on
  // the wall clock, so the world must be built on the wall clock too.
  await resetStub(request, null);
  await page.addInitScript(driverHandoff);
});

const STOP_1 = "5a000000-0000-4000-9000-000000000001";
const STOP_2 = "5a000000-0000-4000-9000-000000000002";

interface PushCall extends LoggedCall {
  body: { ops: Array<Record<string, unknown>> } | null;
}

/** The platform's word: the emulator cuts the network and the harness says
 * the event, then repeats it if the glass has not answered, the way a real
 * radio re-announces (the layer dispatches only on a change). */
async function platformWord(page: import("@playwright/test").Page, online: boolean, expectWords: string | null = null): Promise<void> {
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

async function loadRunSheet(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await expect(page.locator("[data-stop]")).toHaveCount(3, { timeout: 20_000 });
  await expect(page.locator("[data-staleness]")).toContainText("Pulled", { timeout: 20_000 });
}

/** The pushed operations on one sync scope, once the push landed. */
async function pushedOps(request: import("@playwright/test").APIRequestContext, scope: string): Promise<Array<Record<string, unknown>>> {
  let ops: Array<Record<string, unknown>> = [];
  await expect
    .poll(async () => {
      const response = await request.get("/_stub/log");
      const calls = (await response.json()) as PushCall[];
      ops = calls.filter((c) => c.path === `/api/v1/sync/${scope}` && c.method === "POST").flatMap((p) => p.body?.ops ?? []);
      return ops.length;
    })
    .toBeGreaterThan(0);
  return ops;
}

function payloadOf(op: unknown): Record<string, unknown> {
  return ((op as { payload?: unknown })?.payload ?? {}) as Record<string, unknown>;
}

/** The stop one drive offline: loaded, en route, then the proof screen open. */
async function toTheProofScreenOffline(page: import("@playwright/test").Page): Promise<void> {
  await platformWord(page, false, "You're offline");
  // Stop one: loaded, en route. Each move queues.
  await page.locator("[data-stop]", { hasText: "Bluffside Builders" }).click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await expect(page.locator("hh-shell-offline-banner")).toContainText("1 changes in the outbox");
  await page.getByRole("button", { name: "Mark en route" }).click();
  await expect(page.locator("hh-shell-offline-banner")).toContainText("2 changes in the outbox");
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await expect(page.locator("[data-pod]")).toBeVisible();
}

async function signOffline(page: import("@playwright/test").Page): Promise<void> {
  await page.getByLabel("Full name of the person receiving").fill("  Receiver (demo)  ");
  await page.getByLabel(/confirm/i).check();
  await page.getByRole("button", { name: "Delivered" }).click();
}

test("offline, the stop moves and the proof wait in the outbox, then all drain in order and the stop reads delivered", async ({ page, request }) => {
  // The world is pulled and the runner rests before the word goes offline: a
  // boot still in flight would hear the word and then hear its own boot's
  // online, and the glass would show online while the radio was cut.
  await loadRunSheet(page);
  // The banner says the truth about the connection, with the outbox's depth.
  await toTheProofScreenOffline(page);
  // The proof of delivery is the attestation: it queues on pod create, the
  // stop shows delivered and waiting, and nobody has been told it is done.
  await signOffline(page);
  await expect(page.locator("[data-notice]").or(page.getByRole("status").first())).toContainText("Signature saved on this device. It sends when you have signal.");
  await expect(page.locator("[data-stop-status]")).toHaveText("Delivered");
  await expect(page.locator("[data-proof-waiting]")).toContainText("Waiting to send");
  await expect(page.locator(".signed")).toContainText("Signed by Receiver (demo)");
  await expect(page.locator("hh-shell-offline-banner")).toContainText("3 changes in the outbox");
  // A late photo is not offered while the signature waits.
  await expect(page.locator("input[type=file]")).toHaveCount(0);
  // Back online: the outbox drains, in the order the writes were made.
  await platformWord(page, true);
  await expect
    .poll(async () => page.locator("hh-shell-offline-banner").count(), { timeout: 10_000 })
    .toBe(0);
  const stopOps = await pushedOps(request, "stops");
  expect(stopOps.length).toBe(2);
  expect(String(payloadOf(stopOps[0]).status)).toBe("LOADED");
  expect(String(payloadOf(stopOps[1]).status)).toBe("EN_ROUTE");
  const podOps = await pushedOps(request, "pod");
  expect(podOps.length).toBe(1);
  expect((podOps[0] as Record<string, unknown>).op).toBe("create");
  const proof = payloadOf(podOps[0]);
  expect(Object.keys(proof).sort()).toEqual(["affirmed", "id", "received_by", "stop_id"]);
  expect(proof.received_by).toBe("Receiver (demo)");
  expect(proof.affirmed).toBe(true);
  expect(proof.stop_id).toBe(STOP_1);
  // The order the writes were made is the order they reached the server.
  const log = (await (await request.get("/_stub/log")).json()) as PushCall[];
  const scopes = log.filter((c) => c.path.startsWith("/api/v1/sync/") && c.method === "POST").map((c) => c.path.slice("/api/v1/sync/".length));
  expect(scopes.indexOf("stops")).toBeLessThan(scopes.indexOf("pod"));
  // The direct route was never used for the signature.
  expect(log.filter((c) => c.path.endsWith("/pod") && !c.path.includes("/sync/"))).toHaveLength(0);
  // The stop is delivered on the server, and the pulled row says so.
  const state = (await (await request.get("/_stub/state")).json()) as { stops: Array<{ id: string; status: string; pod_received_by: string }> };
  expect(state.stops.find((s) => s.id === STOP_1)).toMatchObject({ status: "DELIVERED", pod_received_by: "Receiver (demo)" });
  await expect(page.locator("[data-proof-waiting]")).toHaveCount(0);
  await page.getByRole("button", { name: "Back to my run" }).click();
  await expect(page.locator("[data-stop]", { hasText: "Bluffside Builders" }).locator("[data-stop-status]")).toHaveText("Delivered");
  // No card: the whole chain applied clean.
  await expect(page.getByRole("button", { name: /^Review [1-9]/ })).toHaveCount(0);
});

test("a stop the test moved first: the moves come back superseded and refused, the signature is refused in the core's words, and the server row stands", async ({ page, request }) => {
  await loadRunSheet(page);
  await toTheProofScreenOffline(page);
  await signOffline(page);
  await expect(page.locator("hh-shell-offline-banner")).toContainText("3 changes in the outbox");
  // Dispatch cancels the stop while the device cannot hear it.
  const move = await request.post("/_stub/move-stop", { data: { stop_id: STOP_1, status: "CANCELLED" } });
  expect(move.ok()).toBeTruthy();
  await platformWord(page, true);
  await expect
    .poll(async () => page.locator("hh-shell-offline-banner").count(), { timeout: 10_000 })
    .toBe(0);
  await pushedOps(request, "pod");
  await page.getByRole("button", { name: "Back to my run" }).click();
  await expect(page.getByRole("button", { name: "Review 3" })).toBeVisible();
  await page.getByRole("button", { name: "Review 3" }).click();
  const list = page.locator("[data-conflict-list]");
  await expect(list).toContainText("Superseded on the server");
  await expect(list).toContainText("Refused · proof of delivery");
  await expect(list).toContainText("illegal status transition");
  // The board shows the server's row, not the device's delivery.
  await page.getByRole("button", { name: "Back to the run" }).click();
  await expect(page.locator("[data-stop]", { hasText: "Bluffside Builders" }).locator("[data-stop-status]")).toHaveText("Cancelled");
});

test("the proof photo stays a direct door: with no signal it says so and queues nothing", async ({ page }) => {
  await loadRunSheet(page);
  await toTheProofScreenOffline(page);
  await page.locator("input[type=file]").setInputFiles({ name: "drop.png", mimeType: "image/png", buffer: TINY_PNG });
  await expect(page.getByRole("alert")).toContainText("Check your signal");
  await expect(page.locator("hh-shell-offline-banner")).toContainText("2 changes in the outbox");
});

test("a stop the test moved first comes back superseded, and the server row is what reads show", async ({ page, request }) => {
  await loadRunSheet(page);
  await platformWord(page, false);
  await page.locator("[data-stop]", { hasText: "Harbourview Builders" }).click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await expect(page.locator("hh-shell-offline-banner")).toContainText("1 changes in the outbox");
  // Dispatch moves the stop while the device cannot hear it.
  const move = await request.post("/_stub/move-stop", { data: { stop_id: STOP_2, status: "CANCELLED" } });
  expect(move.ok()).toBeTruthy();
  // Back online: the queued move lands stale, and the server row is shown.
  await platformWord(page, true);
  await expect
    .poll(async () => page.locator("hh-shell-offline-banner").count(), { timeout: 10_000 })
    .toBe(0);
  await pushedOps(request, "stops");
  await page.getByRole("button", { name: "Back to my run" }).click();
  await expect(page.getByRole("button", { name: "Review 1" })).toBeVisible();
  await page.getByRole("button", { name: "Review 1" }).click();
  const list = page.locator("[data-conflict-list]");
  await expect(list).toContainText("Superseded on the server");
  await expect(list).toContainText("Server wins · stops");
  await expect(list).toContainText("moved on the server while you were offline");
  // The board itself shows the server's row now, not the device's move.
  await page.getByRole("button", { name: "Back to the run" }).click();
  await expect(page.locator("[data-stop]", { hasText: "Harbourview Builders" }).locator("[data-stop-status]")).toHaveText("Cancelled");
  // Dismiss is the exit, and the list says when nothing waits.
  await page.getByRole("button", { name: "Review 1" }).click();
  await page.getByRole("button", { name: /Dismiss: Superseded on the server/ }).click();
  await expect(list).toContainText("Nothing waits for review");
  await page.getByRole("button", { name: "Back to the run" }).click();
  await expect(page.getByRole("button", { name: "Review", exact: true })).toBeVisible();
});
