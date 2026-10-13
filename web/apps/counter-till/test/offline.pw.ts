// CP-3, the Work shell's offline behaviour at the Counter Till, in a real
// browser against the stubbed harness. The till is READ CACHE ONLY (plan D5,
// manifest mode read_cache_only): after sign in it pulls the catalogue, the
// customers, the price band and the parked tickets into the copy on the
// device. With the network gone the till reads them from the copy with the
// copy's age shown; it has NO outbox, so it queues nothing; and every money
// action (the tender, a park, a resume) resolves online with the honest "no
// signal" refusal, sends nothing and leaves the sale on the glass for the
// signal's return. Never a queued tender.
import { expect, test, type Page } from "@playwright/test";

test.use({ viewport: { width: 1440, height: 900 } });

type SyncLog = { pulls: { scope: string; since: number }[]; pushes: { scope: string; status: number }[] };

async function syncLog(page: Page): Promise<SyncLog> {
  return (await (await page.request.get("/_stub/sync-log")).json()) as SyncLog;
}

async function waitForFirstPull(page: Page): Promise<void> {
  await expect
    .poll(async () => [...new Set((await syncLog(page)).pulls.map((p) => p.scope))].sort())
    .toEqual(["customers", "invoices", "parked_tickets", "price_matrix", "products", "warranty_claims"]);
}

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

async function openStep(page: Page, step: "customer" | "items" | "payment" | "summary"): Promise<void> {
  const head = page.getByTestId(`step-head-${step}`);
  for (let attempt = 0; attempt < 5; attempt += 1) {
    if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
    try {
      await expect(head).toHaveAttribute("aria-expanded", "true", { timeout: 1_000 });
      await page.waitForTimeout(250);
      if ((await head.getAttribute("aria-expanded")) === "true") return;
    } catch {
      // closed again by the advance: press once more
    }
  }
  await expect(head).toHaveAttribute("aria-expanded", "true");
}

async function ensureSession(page: Page): Promise<void> {
  const dot = page.getByTestId("session-dot");
  const openButton = page.getByRole("button", { name: "Open Session", exact: true });
  await expect(dot.or(openButton).first()).toBeVisible({ timeout: 10_000 });
  if (await dot.isVisible()) return;
  await openButton.click({ timeout: 3_000 }).catch(() => undefined);
  await expect(dot).toContainText("Open");
}

/** Writes this page dialed, by path: a refused write never leaves the browser. */
function dialedWrites(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (r) => {
    if (["POST", "PUT", "PATCH", "DELETE"].includes(r.method()) && new URL(r.url()).pathname.startsWith("/api/")) seen.push(`${r.method()} ${new URL(r.url()).pathname}`);
  });
  return seen;
}

const banner = (page: Page) => page.getByTestId("ct-offline-banner");

async function addLine(page: Page, query: string, name: string): Promise<void> {
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill(query);
  const row = page.getByTestId("drop-row").filter({ hasText: name }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: name })).toHaveCount(1);
}

test("a signed in till says nothing about the connection while all is well, and pulls only", async ({ page }) => {
  await signIn(page);
  await waitForFirstPull(page);
  await expect(banner(page)).toHaveCount(0);
  await expect(page.getByTestId("ct-cache-note")).toHaveCount(0);
  await expect(page.getByTestId("ct-money-line")).toHaveCount(0);
  expect((await syncLog(page)).pushes).toEqual([]);
});

test("with no signal: the catalogue and customers read from the copy, a tender is refused with no signal and nothing is queued, and back online the same sale rings once", async ({ page, context }) => {
  await signIn(page);
  await ensureSession(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await waitForFirstPull(page);
  const dialed = dialedWrites(page);

  // The signal goes: the strip says so, with no count of changes waiting (the till has no outbox), and says money waits for the signal.
  await context.setOffline(true);
  await expect(banner(page)).toBeVisible();
  await expect(banner(page)).toContainText("offline");
  await expect(banner(page)).not.toContainText("outbox");
  await expect(page.getByTestId("ct-money-line")).toContainText("Nothing is kept for later");

  // The catalogue reads from the copy: the hit is offered, and its price is said to come on add, never guessed.
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill("polymeric");
  const hit = page.getByTestId("drop-row").filter({ hasText: "Polymeric Locking Sand" }).first();
  await expect(hit).toBeVisible();
  await expect(hit).toContainText("prices on add");
  await expect(page.getByTestId("ct-cache-note")).toContainText("Showing the copy on this device");
  await page.getByTestId("pos-scan-input").fill("");

  // The customer search reads from the copy.
  await openStep(page, "customer");
  await page.getByRole("button", { name: /Select customer/ }).click();
  await page.getByPlaceholder("Search name or account #…").fill("Maple");
  await expect(page.locator("button.row").filter({ hasText: "Maple Ridge Contracting" }).first()).toBeVisible();
  await page.keyboard.press("Escape");

  // The tender: refused with no signal, sends nothing, and the sale stays on the glass.
  await openStep(page, "payment");
  await page.getByTestId("pos-complete-sale").click();
  await expect(page.getByRole("alert").filter({ hasText: "No signal" })).toBeVisible();
  await expect(page.getByTestId("sold-panel")).toHaveCount(0);
  expect(dialed.filter((w) => w === "POST /api/v1/pos/sales")).toEqual([]);
  await openStep(page, "items");
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
  expect((await syncLog(page)).pushes).toEqual([]);

  // Park is a write too: refused with no signal, the ticket stays where it is.
  await page.getByRole("button", { name: "Park ticket" }).click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
  expect(dialed.filter((w) => w === "POST /api/v1/pos/parked")).toEqual([]);

  // The signal returns: nothing was queued, so nothing is sent by itself.
  await context.setOffline(false);
  await expect(banner(page)).toHaveCount(0);
  expect(dialed.filter((w) => w.startsWith("POST /api/v1/pos/"))).toEqual([]);
  expect((await syncLog(page)).pushes).toEqual([]);

  // The same sale is rung by the person, once.
  await openStep(page, "payment");
  await page.getByTestId("pos-complete-sale").click();
  await expect(page.getByTestId("sold-panel")).toBeVisible();
  expect(dialed.filter((w) => w === "POST /api/v1/pos/sales")).toHaveLength(1);
  expect((await syncLog(page)).pushes).toEqual([]);
});

test("the parked tickets read from the copy, and a resume is refused with no signal", async ({ page, context }) => {
  // A ticket parked at this counter before the till is opened, so the first pull holds it.
  const minted = await page.request.post("/api/v1/demo/session", { data: { flow: "sales" } });
  const bearer = ((await minted.json()) as { token: string }).token;
  const headers = { Authorization: `Bearer ${bearer}`, "X-Org": "hh-demo" };
  const branches = (await (await page.request.get("/api/v1/branches", { headers })).json()) as Array<{ id: string }>;
  const parkedMade = await page.request.post("/api/v1/pos/parked", {
    headers,
    data: { branch_id: branches[0]!.id, customer_id: "c-maple-ridge", sale_type: "COLLECT_NOW", notes: "", lines: [{ product_id: "p-holland-60", qty_entered: 10, uom_entered: "sf" }] },
  });
  expect(parkedMade.ok()).toBe(true);
  const made = (await parkedMade.json()) as { id: string; ticket_no: string };
  try {
    await signIn(page);
    await waitForFirstPull(page);
    const dialed = dialedWrites(page);
    await context.setOffline(true);
    await expect(banner(page)).toBeVisible();

    await page.getByRole("button", { name: /^Parked \(/ }).click();
    const dialog = page.locator("hos-pos-parked .hos-dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(made.ticket_no);
    await expect(dialog.getByText("PARK-9001")).toHaveCount(0);
    await dialog.getByRole("button", { name: "Resume" }).first().click();
    await expect(page.locator("hos-pos-parked")).toContainText("No signal");
    expect(dialed.filter((w) => w.includes("/resume"))).toEqual([]);
    expect((await syncLog(page)).pushes).toEqual([]);
  } finally {
    // The stub is one server for the whole suite: the ticket this leg parked goes, so the next leg's list is its own.
    await context.setOffline(false);
    await page.request.delete(`/api/v1/pos/parked/${made.id}`, { headers });
  }
});
