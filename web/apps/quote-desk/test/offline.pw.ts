// CP-3, the Work shell's offline behaviour in the Quote Desk, in a real
// browser against the stubbed harness. After sign in the desk pulls its world
// into the device's copy; with the network gone the desk reads the catalogue
// and the customers from the copy, a draft is kept on the device and waits in
// the outbox, every money moving action resolves online with the honest "no
// signal" refusal and nothing is queued or dialed, and when the signal returns
// the draft lands. The money legs of the stub are the core's; the sync routes
// admit only what core/internal/quote/syncscope.go admits.
import { expect, test, type Page } from "@playwright/test";
import { goFieldSet } from "@hardscapehouse/work-offline/gosets";
import { signIn, showList } from "./switch-helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

type SyncLog = { pulls: { scope: string; since: number }[]; pushes: { scope: string; op: string; client_id: string; fields: string[] }[]; refusals: string[] };

async function syncLog(page: Page): Promise<SyncLog> {
  return (await (await page.request.get("/_stub/sync-log")).json()) as SyncLog;
}

async function waitForFirstPull(page: Page): Promise<void> {
  await expect
    .poll(async () => [...new Set((await syncLog(page)).pulls.map((p) => p.scope))].sort())
    .toEqual(expect.arrayContaining(["customers", "price_matrix", "products", "quotes"]));
}

/** Writes this page dialed, by path: a refused write never leaves the browser. */
function dialedWrites(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (r) => {
    if (["POST", "PUT", "PATCH", "DELETE"].includes(r.method()) && new URL(r.url()).pathname.startsWith("/api/")) seen.push(`${r.method()} ${new URL(r.url()).pathname}`);
  });
  return seen;
}

const banner = (page: Page) => page.getByTestId("qd-offline-banner");

test("a signed in desk says nothing about the connection while all is well", async ({ page }) => {
  await signIn(page);
  await waitForFirstPull(page);
  await expect(banner(page)).toHaveCount(0);
  await expect(page.getByTestId("qd-cache-note")).toHaveCount(0);
  await expect(page.getByTestId("qd-waiting")).toHaveCount(0);
});

test("with no signal: the cached catalogue and customers answer, a draft is kept, a payment is refused with no signal, and back online the draft lands", async ({ page, context }) => {
  await signIn(page);
  // A quote opened while the signal is good, so the money door is on screen when it goes.
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2101" }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.locator("qd-order-payment").getByTestId("op-box")).toBeVisible();
  await waitForFirstPull(page);
  const dialed = dialedWrites(page);

  // The signal goes.
  await context.setOffline(true);
  await expect(banner(page)).toBeVisible();
  await expect(banner(page)).toContainText("offline");

  // A money action: the payment door resolves online, says so, and sends nothing.
  const pay = page.locator("qd-order-payment");
  await pay.locator('[data-field="amount"]').fill("10.00");
  await pay.getByTestId("op-take").click();
  await expect(pay.getByTestId("op-error")).toContainText("No signal");
  await expect(pay.getByTestId("op-receipt")).toHaveCount(0);
  await expect(pay.getByTestId("op-unknown")).toHaveCount(0);
  expect(dialed.filter((w) => w.includes("/order-payment"))).toEqual([]);

  // The catalogue and the customers answer from the copy on the device.
  await showList(page);
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  await expect(page.getByTestId("qb-compose-customer")).toContainText("Maple Ridge Contracting");
  await expect(page.getByTestId("qd-cache-note")).toContainText("Showing the copy on this device");
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("Holland");
  const hit = page.getByRole("option", { name: /Holland Stone 60 mm/ });
  await expect(hit.first()).toBeVisible();

  // Adding the first line makes the quote; with no signal the draft is kept on the device instead.
  await hit.first().click();
  await expect(page.getByTestId("qb-error")).toContainText("saved on this device");
  const waiting = page.getByTestId("qd-waiting");
  await expect(waiting).toBeVisible();
  await expect(waiting.getByTestId("qd-waiting-item")).toHaveCount(1);
  await expect(waiting).toContainText("Maple Ridge Contracting");
  await expect(banner(page)).toContainText("1 changes in the outbox");
  expect((await syncLog(page)).pushes).toEqual([]);
  expect(dialed.filter((w) => w === "POST /api/v1/quotes")).toEqual([]);

  // The signal returns: the draft lands, once, with only the fields the core admits.
  await context.setOffline(false);
  await expect.poll(async () => (await syncLog(page)).pushes.length).toBe(1);
  const pushes = (await syncLog(page)).pushes;
  expect(pushes[0]?.op).toBe("create");
  expect(pushes[0]?.scope).toBe("quotes");
  // The desk names the customer, the branch and the default validity; every field is one the core's create admits.
  expect([...(pushes[0]?.fields ?? [])].sort()).toEqual(["branch_id", "customer_id", "id", "valid_until"]);
  const admitted = goFieldSet("quote/syncscope.go", "quoteCreateFields");
  for (const field of pushes[0]?.fields ?? []) expect(admitted.has(field), field).toBe(true);
  expect((await syncLog(page)).refusals).toEqual([]);
  await expect(waiting).toHaveCount(0);
  await expect(banner(page)).toHaveCount(0);
  // The strip is gone. What is still on screen was read from the copy, and the note says so until a read from the server replaces it.
  await expect(page.getByTestId("qd-cache-note")).toContainText("back online");
});

test("a withdrawn draft never reaches the server as a quote", async ({ page, context }) => {
  await signIn(page);
  await waitForFirstPull(page);
  await context.setOffline(true);
  await expect(banner(page)).toBeVisible();
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Fenwick");
  await picker.getByRole("option", { name: /Fenwick Builders/ }).click();
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scan.fill("Holland");
  await page.getByRole("option", { name: /Holland Stone 60 mm/ }).first().click();
  const waiting = page.getByTestId("qd-waiting");
  await expect(waiting.getByTestId("qd-waiting-item")).toHaveCount(1);
  await waiting.getByRole("button", { name: "Withdraw" }).click();
  await expect(waiting).toHaveCount(0);
  await context.setOffline(false);
  // The create and its withdrawal go in order; the server ends with no live quote for the draft.
  await expect.poll(async () => (await syncLog(page)).pushes.map((p) => p.op)).toEqual(["create", "delete"]);
});

test("pressing add again with no signal keeps one draft, not one per press, and one create reaches the server", async ({ page, context }) => {
  await signIn(page);
  await waitForFirstPull(page);
  await context.setOffline(true);
  await expect(banner(page)).toBeVisible();
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
  const waiting = page.getByTestId("qd-waiting");
  for (const query of ["Holland", "Holland", "Holland"]) {
    await scan.fill(query);
    await page.getByRole("option", { name: /Holland Stone 60 mm/ }).first().click();
    await expect(page.getByTestId("qb-error")).toContainText("saved on this device");
    await expect(waiting.getByTestId("qd-waiting-item").first()).toBeVisible();
  }
  await expect(waiting.getByTestId("qd-waiting-item")).toHaveCount(1);
  expect((await syncLog(page)).pushes).toEqual([]);
  await context.setOffline(false);
  await expect.poll(async () => (await syncLog(page)).pushes.length).toBeGreaterThan(0);
  await expect(waiting).toHaveCount(0);
  expect((await syncLog(page)).pushes.map((p) => p.op)).toEqual(["create"]);
});
