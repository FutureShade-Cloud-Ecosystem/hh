// Item 9.7 (row 1047 item 6, board 02): Pay balance is its own surface at #/pay-balance, beside Returns
// in the bar. Find the customer's order, see what was paid and what is owed, take the whole rest.
import { expect, test, type Page } from "@playwright/test";

async function reseed(page: Page): Promise<void> {
  const answered = await page.request.post("/_stub/reseed-owing");
  expect(answered.ok()).toBe(true);
}

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

async function openDrawer(page: Page): Promise<void> {
  const dot = page.getByTestId("session-dot");
  const openButton = page.getByRole("button", { name: "Open Session", exact: true });
  await expect(dot.or(openButton).first()).toBeVisible({ timeout: 10_000 });
  if (!(await dot.isVisible())) await openButton.click({ timeout: 3_000 }).catch(() => undefined);
  await expect(dot).toContainText("Open");
}

test.beforeEach(async ({ page }) => {
  await reseed(page);
});

test("Pay balance is its own surface beside Returns, at #/pay-balance", async ({ page }) => {
  await signIn(page);
  await expect(page.getByTestId("till-pay-balance-entry")).toHaveText("Pay balance");
  await expect(page.getByTestId("till-returns-entry")).toBeVisible();
  await page.getByTestId("till-pay-balance-entry").click();
  expect(new URL(page.url()).hash).toBe("#/pay-balance");
  const surface = page.getByTestId("pay-balance");
  await expect(surface).toBeVisible();
  await expect(surface.getByRole("heading", { name: "Pay balance", level: 2 })).toBeVisible();
  await expect(surface).toContainText("Find the customer's order, see what was paid and what is owed, take the rest.");
  await expect(surface).toContainText(/Find the customer's order/i);
  await expect(surface).toContainText("Order number, customer name, or phone.");
  await expect(page.getByTestId("pos-page-title")).toBeHidden();
  await page.getByTestId("till-sale-entry").click();
  await expect(page.getByTestId("pay-balance")).toHaveCount(0);
});

test("find the order, see the money, take the whole rest by cash, and read Paid in full", async ({ page }) => {
  await signIn(page);
  await openDrawer(page);
  await page.getByTestId("till-pay-balance-entry").click();
  await page.getByTestId("pay-balance-term").fill("DEMO-TRT-D02");
  await page.getByTestId("pay-balance-find").click();

  const card = page.getByTestId("pay-balance-order");
  await expect(card).toContainText("Cash On Demand Landscaping");
  await expect(card).toContainText(/Order · DEMO-TRT-D02/i);
  await expect(card).toContainText("Order total");
  await expect(card).toContainText("$61.02");
  await expect(card).toContainText("Paid so far: Cash");
  await expect(card).toContainText("$20.00");
  await expect(page.getByTestId("pay-balance-owed")).toContainText("Still owed");
  await expect(page.getByTestId("pay-balance-owed")).toContainText("$41.02");

  const take = page.getByTestId("pay-balance-take");
  await expect(take).toHaveText("Take $41.02");
  await expect(page.getByTestId("pay-balance-amount")).toHaveText("$41.02");
  await expect(page.getByTestId("pay-balance-whole-only")).toHaveText("The whole balance: this door takes no part payments.");
  await page.getByTestId("pay-balance-method-CASH").click();

  const post = page.waitForRequest((r) => r.url().endsWith("/api/v1/pos/pay-balance") && r.method() === "POST");
  await take.click();
  const body = (await (await post).postDataJSON()) as Record<string, unknown>;
  expect(body.method).toBe("CASH");
  expect(body.amount_cents).toBe(4102);
  expect(typeof body.idempotency_key).toBe("string");

  const done = page.getByTestId("pay-balance-done");
  await expect(done).toContainText("DEMO-TRT-D02 reads Paid in full and is released to picking.");
  await expect(done).toContainText("Money trail: $20.00 by Cash, then $41.02 by Cash.");
});

test("a card take rides a card token, and a credit order that was not held has no release words", async ({ page }) => {
  await signIn(page);
  await openDrawer(page);
  await page.getByTestId("till-pay-balance-entry").click();
  await page.getByTestId("pay-balance-term").fill("Fenwick");
  await page.getByTestId("pay-balance-find").click();
  await expect(page.getByTestId("pay-balance-order")).toContainText("Job: Marina Road patio");
  await expect(page.getByTestId("pay-balance-order")).toContainText("Will-call");
  await expect(page.getByTestId("pay-balance-take")).toHaveText("Take $1,413.59");
  const post = page.waitForRequest((r) => r.url().endsWith("/api/v1/pos/pay-balance") && r.method() === "POST");
  await page.getByTestId("pay-balance-take").click();
  const body = (await (await post).postDataJSON()) as Record<string, unknown>;
  expect(body.method).toBe("CARD");
  expect(typeof body.payment_token).toBe("string");
  const done = page.getByTestId("pay-balance-done");
  await expect(done).toContainText("DEMO-TRT-D01 reads Paid in full.");
  await expect(done).not.toContainText("released to picking");
});

test("several matches list as rows to pick, and a miss says so in words", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("till-pay-balance-entry").click();
  await page.getByTestId("pay-balance-term").fill("DEMO-TRT-D0");
  await page.getByTestId("pay-balance-find").click();
  const rows = page.getByTestId("pay-balance-match");
  await expect(rows).toHaveCount(2);
  await rows.filter({ hasText: "DEMO-TRT-D02" }).click();
  await expect(page.getByTestId("pay-balance-order")).toContainText(/Order · DEMO-TRT-D02/i);
  await page.getByTestId("pay-balance-term").fill("NOPE-0");
  await page.getByTestId("pay-balance-find").click();
  await expect(page.getByTestId("pay-balance-notice")).toContainText("No order that still owes matches");
});

test("a refused find speaks in words, never the envelope's raw string", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("till-pay-balance-entry").click();
  // Item 9 visual review P2-1: the find's refusal (a signed out or fenced login, the server down)
  // reaches the person as words: the server's message when the body has one, else one plain
  // sentence; the raw error string or code never shows.
  let mode: "message" | "bare" | "dead" = "message";
  await page.route("**/api/v1/pos/pay-balance*", async (route) => {
    if (route.request().method() !== "GET") return route.continue();
    if (mode === "dead") return route.abort("failed");
    const body =
      mode === "message"
        ? { error: "staff_auth_required", message: "Sign in again to search." }
        : { error: "staff_auth_required" };
    return route.fulfill({ status: 422, contentType: "application/json", body: JSON.stringify(body) });
  });
  const notice = page.getByTestId("pay-balance-notice");
  await page.getByTestId("pay-balance-term").fill("DEMO-TRT-D02");
  await page.getByTestId("pay-balance-find").click();
  await expect(notice).toHaveText("Sign in again to search.");
  mode = "bare";
  await page.getByTestId("pay-balance-find").click();
  await expect(notice).toHaveText("The search was refused: try again, or ask a manager.");
  await expect(notice).not.toContainText("staff_auth_required");
  mode = "dead";
  await page.getByTestId("pay-balance-find").click();
  await expect(notice).toHaveText("No answer from the server. Try again.");
});

test("a replay shows the same result and the server charges nothing twice", async ({ page }) => {
  await signIn(page);
  await openDrawer(page);
  await page.getByTestId("till-pay-balance-entry").click();
  await page.getByTestId("pay-balance-term").fill("DEMO-TRT-D02");
  await page.getByTestId("pay-balance-find").click();
  await page.getByTestId("pay-balance-method-CASH").click();
  const first = page.waitForRequest((r) => r.url().endsWith("/api/v1/pos/pay-balance") && r.method() === "POST");
  await page.getByTestId("pay-balance-take").click();
  const key = ((await (await first).postDataJSON()) as { idempotency_key: string }).idempotency_key;
  await expect(page.getByTestId("pay-balance-done")).toBeVisible();
  // The same key again, straight at the door: the answer is the same take, flagged as a replay.
  const bearer = await (await page.request.post("/api/v1/demo/session", { data: { flow: "sales" } })).json();
  const again = await page.request.post("/api/v1/pos/pay-balance", {
    headers: { Authorization: `Bearer ${bearer.token}`, "X-Org": "hh-demo" },
    data: { order_id: "x", method: "CASH", amount_cents: 1, till_session_id: "x", idempotency_key: key },
  });
  expect(again.status()).toBe(200);
  expect(((await again.json()) as { replayed?: boolean }).replayed).toBe(true);
});

test("a take that got no answer is retried on the same key, so it can never charge twice", async ({ page }) => {
  await signIn(page);
  await openDrawer(page);
  await page.getByTestId("till-pay-balance-entry").click();
  await page.getByTestId("pay-balance-term").fill("DEMO-TRT-D02");
  await page.getByTestId("pay-balance-find").click();
  await page.getByTestId("pay-balance-method-CASH").click();
  const keys: string[] = [];
  let dropped = false;
  await page.route("**/api/v1/pos/pay-balance", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    keys.push((route.request().postDataJSON() as { idempotency_key: string }).idempotency_key);
    if (!dropped) {
      dropped = true;
      return route.abort("failed");
    }
    return route.continue();
  });
  await page.getByTestId("pay-balance-take").click();
  await expect(page.getByTestId("pay-balance-notice")).toBeVisible();
  await page.getByTestId("pay-balance-take").click();
  await expect(page.getByTestId("pay-balance-done")).toContainText("DEMO-TRT-D02 reads Paid in full");
  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
});

test("at 390 the Pay balance surface does not scroll sideways", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await page.getByTestId("till-pay-balance-entry").click();
  await page.getByTestId("pay-balance-term").fill("DEMO-TRT-D02");
  await page.getByTestId("pay-balance-find").click();
  await expect(page.getByTestId("pay-balance-order")).toBeVisible();
  const read = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, view: window.innerWidth }));
  expect(read.doc).toBeLessThanOrEqual(read.view);
  await expect(page.getByTestId("pay-balance-take")).toBeVisible();
});
