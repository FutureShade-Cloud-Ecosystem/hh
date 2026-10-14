// A retry of an OPEN payment attempt (its outcome not known, or its key held) made with NO SIGNAL
// sends nothing and so changes nothing: the attempt goes back to the phase it was in, the "not
// known" note and the locked fields stay, and the attempt is still carried across a sign out, so
// the next session's retry carries the SAME idempotency key and the payment is taken once. The
// stub records every post it receives (replays and refusals included), so the keys asserted here
// are what the server saw, across both sessions. Money never queues: the offline retry reaches no
// server at all.
import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { signIn, showList } from "./switch-helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

const pay = (page: Page) => page.locator("qd-order-payment");

async function openWithPayment(page: Page, quoteNo: string): Promise<void> {
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: quoteNo }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(pay(page).getByTestId("op-box")).toBeVisible();
}

async function take(page: Page, amount: string, reference = "", tender = "CASH"): Promise<void> {
  await pay(page).locator('[data-field="amount"]').fill(amount);
  if (reference !== "") await pay(page).locator('[data-field="reference"]').fill(reference);
  if (tender !== "CASH") await pay(page).getByTestId("op-tenders").getByRole("button", { name: tender, exact: true }).click();
  await pay(page).getByTestId("op-take").click();
}

/** Every payment post the stub received, in order, and the payments it took. */
async function serverSaw(page: Page): Promise<{ keys: string[]; taken: number }> {
  const state = (await (await page.request.get("/_stub/state")).json()) as { posts: { kind: string; key: string }[]; payments: unknown[] };
  return { keys: state.posts.filter((p) => p.kind === "order-payment").map((p) => p.key), taken: state.payments.length };
}

/** Payment posts this page dialed: a refused write never leaves the browser. */
function dialedPayments(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && new URL(r.url()).pathname.endsWith("/order-payment")) seen.push(new URL(r.url()).pathname);
  });
  return seen;
}

async function signOutAndIn(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible({ timeout: 15000 });
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

/** The first take lands on the server and its answer is lost: the outcome is not known. Returns the key it carried. */
async function unknownAttempt(page: Page): Promise<string> {
  await page.request.put("/_stub/payment-ghost", { data: { ghost: true } });
  await take(page, "10.00", "AUTH 9", "CARD");
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  await page.request.put("/_stub/payment-ghost", { data: { ghost: false } });
  expect(await serverSaw(page)).toMatchObject({ taken: 1 });
  return (await serverSaw(page)).keys[0];
}

/** The server has already said this key took a different payment: the attempt is held. Returns the key it carried. */
async function heldAttempt(page: Page): Promise<string> {
  await page.request.put("/_stub/payment-key-crossed", { data: { crossed: true } });
  await take(page, "10.00", "AUTH 1", "CARD");
  await expect(pay(page).getByTestId("op-error")).toContainText("took a different payment");
  await expect(pay(page).getByTestId("op-start-new")).toBeVisible();
  return (await serverSaw(page)).keys[0];
}

/** The signal goes and the cashier retries. */
async function retryOffline(page: Page, context: BrowserContext): Promise<void> {
  await context.setOffline(true);
  // The app reads navigator.onLine per call: the press waits until the page itself says it is offline.
  await page.waitForFunction(() => navigator.onLine === false);
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-take")).toBeEnabled();
  await expect(pay(page).getByTestId("op-error")).toContainText("No signal");
}

test("a no signal retry of a payment whose outcome is not known keeps the note and the locked fields, and sends nothing", async ({ page, context }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const dialed = dialedPayments(page);
  await unknownAttempt(page);
  expect(dialed).toHaveLength(1);
  await retryOffline(page, context);
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await expect(pay(page).locator('[data-field="amount"]')).toHaveValue("10.00");
  await expect(pay(page).locator('[data-field="reference"]')).toBeDisabled();
  expect(dialed).toHaveLength(1);
  // Online the retry is still the same attempt and the server replays its first outcome.
  await context.setOffline(false);
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$10.00");
  const seen = await serverSaw(page);
  expect(seen.keys).toHaveLength(2);
  expect(seen.keys[1]).toBe(seen.keys[0]);
  expect(seen.taken).toBe(1);
});

test("a payment whose outcome was not known, retried with no signal, is still carried across a sign out and the next session's retry carries the same key", async ({ page, context }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const firstKey = await unknownAttempt(page);
  await retryOffline(page, context);
  await context.setOffline(false);
  await signOutAndIn(page);
  await openWithPayment(page, "Q-2101");
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await expect(pay(page).locator('[data-field="amount"]')).toHaveValue("10.00");
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$10.00");
  await expect(pay(page).getByTestId("op-replayed-note")).toContainText("nothing was posted a second time");
  const seen = await serverSaw(page);
  expect(seen.keys).toEqual([firstKey, firstKey]);
  expect(seen.taken).toBe(1);
});

test("a no signal retry of a held key keeps it held and locked, and sends nothing", async ({ page, context }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const dialed = dialedPayments(page);
  await heldAttempt(page);
  expect(dialed).toHaveLength(1);
  await retryOffline(page, context);
  await expect(pay(page).getByTestId("op-start-new")).toBeVisible();
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await expect(pay(page).locator('[data-field="amount"]')).toHaveValue("10.00");
  expect(dialed).toHaveLength(1);
});

test("a held key, retried with no signal, is still carried across a sign out on the same key", async ({ page, context }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const firstKey = await heldAttempt(page);
  await retryOffline(page, context);
  await context.setOffline(false);
  await signOutAndIn(page);
  await openWithPayment(page, "Q-2101");
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
  await expect(pay(page).locator('[data-field="amount"]')).toHaveValue("10.00");
  // The held key is still the one it was: a retry says the same words on the same key, and no
  // payment is taken beside the phantom the crossed key stored (the stub's payments list holds only payments it took on a post).
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-error")).toContainText("took a different payment");
  const seen = await serverSaw(page);
  expect(seen.keys).toEqual([firstKey, firstKey]);
  expect(seen.taken).toBe(0);
});

test("a no signal take of a fresh attempt sends nothing, leaves the form editable, and online the same payment is taken once", async ({ page, context }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  const dialed = dialedPayments(page);
  await context.setOffline(true);
  await take(page, "10.00", "AUTH 9", "CARD");
  await expect(pay(page).getByTestId("op-error")).toContainText("No signal");
  await expect(pay(page).getByTestId("op-unknown")).toHaveCount(0);
  await expect(pay(page).locator('[data-field="amount"]')).toBeEnabled();
  await expect(pay(page).locator('[data-field="amount"]')).toHaveValue("10.00");
  expect(dialed).toEqual([]);
  expect((await serverSaw(page)).keys).toEqual([]);
  await context.setOffline(false);
  await pay(page).getByTestId("op-take").click();
  await expect(pay(page).getByTestId("op-receipt-amount")).toHaveText("$10.00");
  const seen = await serverSaw(page);
  expect(seen.keys).toHaveLength(1);
  expect(seen.taken).toBe(1);
});

// The two copies of the rule a no signal answer cannot reach through the real transport today (it is
// refused in microtasks, before any click can land): the leg forces the interleaving by handing the
// panel's own answer sites the answer, as the platform would if it ever came late.
const NO_SIGNAL = { ok: false, status: 0, error: "No signal. This needs the server and does not wait in a queue: try again when you are back online." };

test("a no signal answer that lands late for a quote the person has left changes that quote's record only, never the screen of the quote now open", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await unknownAttempt(page);
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1001");
  const seen = await pay(page).evaluate((el, noSignal) => {
    const panel = el as unknown as Record<string, any>;
    panel.reopenedFrom.set("q-2101", "not-known");
    panel.book.mark("q-2101", "in-flight");
    panel.lateAnswer({ id: "q-2101" }, noSignal);
    return { attempt: panel.attempt, unknown: panel.unknown, error: panel.error, phaseOfLeft: panel.book.attempt("q-2101")?.phase };
  }, NO_SIGNAL);
  expect(seen.attempt).toBeNull();
  expect(seen.unknown).toBe("");
  expect(seen.error).toBe("");
  expect(seen.phaseOfLeft).toBe("not-known");
  // Back on the quote it belongs to: the note, the locked fields and the figures are there.
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  await expect(pay(page).getByTestId("op-unknown")).toContainText("not known");
  await expect(pay(page).locator('[data-field="amount"]')).toBeDisabled();
});

test("the sign out wait and a late answer both meeting one no signal answer, in either order, keep the open attempt unsettled", async ({ page }) => {
  await signIn(page);
  await openWithPayment(page, "Q-2101");
  await unknownAttempt(page);
  const phases = await pay(page).evaluate(async (el, noSignal) => {
    const panel = el as unknown as Record<string, any>;
    const id = "q-2101";
    const out: string[] = [];
    const arm = () => {
      panel.reopenedFrom.set(id, "not-known");
      panel.book.mark(id, "in-flight");
      panel.posting.set(id, Promise.resolve(noSignal));
      return panel.book.attempt(id);
    };
    const seenPhase = () => panel.book.unsettled().map((row: { quoteId: string; attempt: { phase: string } }) => `${row.quoteId}:${row.attempt.phase}`).join(",");
    // The answer site runs first, the sign out wait second.
    let attempt = arm();
    panel.lateAnswer({ id }, noSignal);
    await panel.waitOneAtSignOut({ notes: [] }, id, attempt);
    out.push(seenPhase());
    // The sign out wait runs first, the answer site second.
    attempt = arm();
    await panel.waitOneAtSignOut({ notes: [] }, id, attempt);
    panel.lateAnswer({ id }, noSignal);
    out.push(seenPhase());
    return out;
  }, NO_SIGNAL);
  expect(phases).toEqual(["q-2101:not-known", "q-2101:not-known"]);
});
