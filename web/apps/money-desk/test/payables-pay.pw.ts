// The Payables board paying a vendor bill, walked in a real browser against the stub.
//
// The stub answers the route the core answers (POST /api/v1/purchasing/bills/{id}/payments): the
// key rides the Idempotency-Key header, one key per bill while its outcome is unknown, a key spent
// on this bill is a replay that writes nothing, a key spent on another bill is a conflict, a press
// with no key is refused, and the role gate is the core's own (a manage level route on the money
// desk, so the Payables and Receivables role is turned away by it).
//
// Every leg reads the figures on the screen, so a screen that renders nothing, or a route that
// answers nothing, fails the leg instead of passing it.
import { test, expect, openDesk, resetStub, dayFromToday, dayWords, receivablesHandoff } from "./helpers.js";
import type { Page } from "@playwright/test";

/** The cents a figure reads: "$13,482.50" is 1348250. */
function centsOf(text: string): number {
  const dollars = text.replace(/[^0-9.]/g, "");
  const [whole = "0", fraction = ""] = dollars.split(".");
  return Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
}

/** The demo's own token for a role, minted the way the front door's handoff mints it. */
function demoToken(who: "controller" | "clerk"): string {
  const part = (value: unknown): string => Buffer.from(JSON.stringify(value)).toString("base64url");
  const id = who === "controller" ? "de110000-0000-4000-9000-000000000008" : "de110000-0000-4000-9000-000000000007";
  const exp = Math.floor(Date.now() / 1000) + 3600;
  return `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: id, exp })}.c2ln`;
}

/** The bill the world's B-4107 is: the one the part paid bill belongs to. */
const B4107 = "b0000000-0000-4000-8000-000000000006";
/** The bill the world's B-4108 is. */
const B4108 = "b0000000-0000-4000-8000-000000000007";

/** The board's open AP, read from the screen. */
async function openBoard(page: Page): Promise<void> {
  await page.locator("nav[aria-label='Money Desk areas']").getByRole("button", { name: "Payables", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Payables", exact: true })).toBeVisible();
  await expect(page.locator("[data-payables-aging], [data-payables-empty]").first()).toBeVisible();
}

/** A payee's open items, read from the screen. */
async function openPayee(page: Page, payee: string): Promise<void> {
  await page.locator("[data-payee-row]", { hasText: payee }).getByRole("button").click();
  await expect(page.locator("[data-payables-lines]")).toBeVisible();
}

/** The Pay form on one bill, opened from the row's own Pay control. */
async function openPayForm(page: Page, billNo: string): Promise<void> {
  await page.locator("[data-line-row]", { hasText: billNo }).getByRole("button", { name: "Pay" }).click();
  await expect(page.locator("[data-pay-form]")).toBeVisible();
}

test("paying part of a bill lowers its open amount, and the board's total moves by the same cents", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  const bookBefore = centsOf(await page.locator("[data-payables-total]").innerText());

  await openPayee(page, "Saltmarsh Fasteners (demo)");
  // B-4107 is a part paid bill: $400.00 has been paid against it and $560.00 is open.
  await expect(page.locator("[data-line-row]", { hasText: "B-4107" })).toContainText("$560.00");
  const payeeBefore = centsOf(await page.locator("[data-payee-lines-total]").innerText());

  await openPayForm(page, "B-4107");
  // The form opens on the bill it names, with the amount it has open and the house day.
  await expect(page.locator("[data-pay-target]")).toContainText("B-4107");
  await expect(page.locator("[data-pay-target]")).toContainText("Saltmarsh Fasteners (demo)");
  await expect(page.locator("[data-field='pay-amount']")).toHaveValue("560.00");
  await expect(page.locator("[data-field='pay-date']")).toHaveValue(dayFromToday(0));

  // Pay a hundred dollars of it.
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-action='payables-pay-send']").click();

  // The board and the payee read the books again: the bill's open amount is lower by what was
  // paid, the payee's total with it, and the whole book's total moves by the same cents.
  await expect(page.locator("[data-pay-form]")).toHaveCount(0);
  await expect(page.locator("[data-line-row]", { hasText: "B-4107" })).toContainText("$460.00");
  expect(centsOf(await page.locator("[data-payee-lines-total]").innerText())).toBe(payeeBefore - 10_000);
  await expect(page.locator("[data-payee-lines-total]")).toContainText("$775.00");
  await page.getByRole("button", { name: "‹ Payables" }).click();
  await expect(page.locator("[data-payables-total]")).toBeVisible();
  expect(centsOf(await page.locator("[data-payables-total]").innerText())).toBe(bookBefore - 10_000);
});

test("a double press with the same key pays once", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  await openPayForm(page, "B-4107");
  const before = centsOf(await page.locator("[data-payee-lines-total]").innerText());

  // The key the press carries, read off the request the desk made.
  let key = "";
  page.on("request", (req) => {
    if (req.url().includes("/api/v1/purchasing/bills/") && req.url().endsWith("/payments")) {
      key = req.headers()["idempotency-key"] ?? "";
    }
  });
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-action='payables-pay-send']").click();
  await expect(page.locator("[data-pay-form]")).toHaveCount(0);
  expect(key).not.toBe("");
  // The money moved once. The board and the payee read the books again, so the leg waits for the
  // re-read before it reads the total: the form closes before the books come back.
  await expect(page.locator("[data-line-row]", { hasText: "B-4107" })).toContainText("$460.00");
  expect(centsOf(await page.locator("[data-payee-lines-total]").innerText())).toBe(before - 10_000);

  // The same key again, the way a retry after a lost answer sends it: the server answers the first
  // payment and writes nothing, so the books do not move again.
  const replay = await request.post(`/api/v1/purchasing/bills/${B4107}/payments`, {
    headers: { authorization: `Bearer ${demoToken("controller")}`, "Idempotency-Key": key },
    data: { amount_cents: 10_000, paid_at: `${dayFromToday(0)}T12:00:00Z`, note: "" },
  });
  expect(replay.ok()).toBe(true);
  await page.reload();
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  expect(centsOf(await page.locator("[data-payee-lines-total]").innerText())).toBe(before - 10_000);
});

test("a clerk sees no Pay control", async ({ page, request }) => {
  // The Payables and Receivables role reads the open AP but is at work on the money desk, so the
  // route that pays a vendor bill turns it away: the control is not on the row.
  await openDesk(page, request);
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  await expect(page.locator("[data-action='payables-pay']")).toHaveCount(0);

  // The Financial Controller holds the money desk at manage, so the control is there.
  const second = await page.context().newPage();
  await second.addInitScript(receivablesHandoff as (who: string) => void, "controller");
  await second.goto("/");
  await second.locator("nav[aria-label='Money Desk areas']").getByRole("button", { name: "Payables", exact: true }).click();
  await expect(second.locator("[data-payables-aging]")).toBeVisible();
  await second.locator("[data-payee-row]", { hasText: "Saltmarsh Fasteners (demo)" }).getByRole("button").click();
  await expect(second.locator("[data-payables-lines]")).toBeVisible();
  // The vendor's two bills each carry the control; the clerk's carry none.
  await expect(second.locator("[data-action='payables-pay']")).toHaveCount(2);
  await second.close();
});

test("a landed cost line has no Pay control", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  // The carrier holds a bill of its own and a landed cost line. The bill is a vendor bill; the
  // landed cost is owed to the carrier, and this round pays no landed cost.
  await openPayee(page, "Northwind Freight Lines (demo)");
  await expect(page.locator("[data-line-row][data-doc-kind='VENDOR_BILL']")).toHaveCount(1);
  await expect(page.locator("[data-line-row][data-doc-kind='LANDED_COST']")).toHaveCount(1);
  await expect(page.locator("[data-line-row][data-doc-kind='LANDED_COST'] [data-action='payables-pay']")).toHaveCount(0);
  await expect(page.locator("[data-line-row][data-doc-kind='VENDOR_BILL'] [data-action='payables-pay']")).toHaveCount(1);
});

test("a refusal shows the server's words", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  await openPayForm(page, "B-4107");

  // Someone else pays the bill while this form is open: the form's own open amount is stale, and
  // the press asks for more than the bill has left.
  const other = await request.post(`/api/v1/purchasing/bills/${B4107}/payments`, {
    headers: { authorization: `Bearer ${demoToken("controller")}`, "Idempotency-Key": "another-voice-key" },
    data: { amount_cents: 20_000, paid_at: `${dayFromToday(0)}T12:00:00Z`, note: "" },
  });
  expect(other.ok()).toBe(true);

  // The press is answered with the server's own sentence, not the desk's guess at one.
  await page.locator("[data-action='payables-pay-send']").click();
  await expect(page.locator("[data-pay-error]")).toBeVisible();
  await expect(page.locator("[data-pay-error]")).toContainText("Payment of $560.00 exceeds open balance $360.00 on bill B-4107");
  // The form is kept, so the person can take a smaller amount.
  await expect(page.locator("[data-pay-form]")).toBeVisible();
});

test("a press with no key is refused by the server", async ({ request }) => {
  await resetStub(request);
  // The route judges the key before it judges anything else: a press with no key is refused in
  // the core's own words.
  const noKey = await request.post(`/api/v1/purchasing/bills/${B4107}/payments`, {
    headers: { authorization: `Bearer ${demoToken("controller")}` },
    data: { amount_cents: 10_000, paid_at: `${dayFromToday(0)}T12:00:00Z`, note: "" },
  });
  expect(noKey.status()).toBe(400);
  expect((await noKey.json()).error).toBe("purchasing: idempotency_key is required on a bill payment (one key per press)");
});

test("a key spent on another bill is a conflict", async ({ request }) => {
  await resetStub(request);
  const first = await request.post(`/api/v1/purchasing/bills/${B4107}/payments`, {
    headers: { authorization: `Bearer ${demoToken("controller")}`, "Idempotency-Key": "one-key-two-bills" },
    data: { amount_cents: 10_000, paid_at: `${dayFromToday(0)}T12:00:00Z`, note: "" },
  });
  expect(first.ok()).toBe(true);
  const second = await request.post(`/api/v1/purchasing/bills/${B4108}/payments`, {
    headers: { authorization: `Bearer ${demoToken("controller")}`, "Idempotency-Key": "one-key-two-bills" },
    data: { amount_cents: 10_000, paid_at: `${dayFromToday(0)}T12:00:00Z`, note: "" },
  });
  expect(second.status()).toBe(409);
  expect((await second.json()).error).toBe("purchasing: idempotency key already used for another bill's payment");
});

test("the route turns the Payables and Receivables role away", async ({ request }) => {
  await resetStub(request);
  // The core's own gate: the route is a manage level route on the money desk and that role is at
  // work on the surface, so the call is refused with the gate's own 403.
  const refused = await request.post(`/api/v1/purchasing/bills/${B4107}/payments`, {
    headers: { authorization: `Bearer ${demoToken("clerk")}`, "Idempotency-Key": "a-clerk-key" },
    data: { amount_cents: 10_000, paid_at: `${dayFromToday(0)}T12:00:00Z`, note: "" },
  });
  expect(refused.status()).toBe(403);
});

test("a lost answer, then Cancel and Pay again, pays once", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  const before = centsOf(await page.locator("[data-payee-lines-total]").innerText());

  await openPayForm(page, "B-4107");
  // The stub records the payment and the answer never comes back: the money moved and the desk
  // never heard so.
  let drop = true;
  await page.route("**/api/v1/purchasing/bills/*/payments", async (route) => {
    if (drop) {
      drop = false;
      await route.fetch();
      await route.abort("connectionreset");
      return;
    }
    await route.continue();
  });
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-action='payables-pay-send']").click();

  // The books are read again and show the payment landed: the bill has that much less open. Once
  // the books show the earlier payment, its key ends and the form says it is on the bill until the
  // person closes it (S6 on 701, P2-2 and P2-3), so the money has moved exactly once.
  await expect(page.locator("[data-payee-lines-total]")).toContainText("$775.00");
  await expect(page.locator("[data-pay-landed]")).toBeVisible();
  await page.locator("[data-action='payables-pay-cancel']").click();
  await expect(page.locator("[data-pay-form]")).toHaveCount(0);
  const after = centsOf(await page.locator("[data-payee-lines-total]").innerText());
  expect(before - after).toBe(10_000);
});

test("a server that answers 5xx leaves the outcome unknown too, and the key is held across a Cancel", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  const before = centsOf(await page.locator("[data-payee-lines-total]").innerText());
  await openPayForm(page, "B-4107");

  const keys: string[] = [];
  page.on("request", (req) => {
    if (req.url().includes("/api/v1/purchasing/bills/") && req.url().endsWith("/payments")) {
      keys.push(req.headers()["idempotency-key"] ?? "");
    }
  });
  // The server faults: the answer is not lost, it is a fault, and the money may have moved either
  // way.
  await page.route("**/api/v1/purchasing/bills/*/payments", async (route) => {
    await route.fulfill({ status: 500, contentType: "text/plain", body: "the server fell over" });
  });
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-action='payables-pay-send']").click();
  await expect(page.locator("[data-pay-error]")).toContainText("may or may not have been recorded");
  await expect(page.locator("[data-pay-error]")).not.toContainText("Nothing was changed");
  expect(keys.length).toBe(1);

  // The person cancels and pays again: the same key goes out, so the money moves once.
  await page.locator("[data-action='payables-pay-cancel']").click();
  await expect(page.locator("[data-pay-form]")).toHaveCount(0);
  await page.unroute("**/api/v1/purchasing/bills/*/payments");
  await openPayForm(page, "B-4107");
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-action='payables-pay-send']").click();
  await expect(page.locator("[data-pay-form]")).toHaveCount(0);
  expect(keys.length).toBe(2);
  expect(keys[1]).toBe(keys[0]);
  await expect(page.locator("[data-line-row]", { hasText: "B-4107" })).toContainText("$460.00");
  expect(centsOf(await page.locator("[data-payee-lines-total]").innerText())).toBe(before - 10_000);
});

test("the desk refuses an amount above the open amount, and asks the server nothing", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  await openPayForm(page, "B-4107");

  // B-4107 has $560.00 open.
  let asked = 0;
  page.on("request", (req) => {
    if (req.url().includes("/api/v1/purchasing/bills/") && req.url().endsWith("/payments")) asked += 1;
  });
  await page.locator("[data-field='pay-amount']").fill("600.00");
  await page.locator("[data-action='payables-pay-send']").click();

  // The desk stops it in its own words, and the server is never asked, so a stale open amount
  // cannot turn a mistyped amount into a refused payment.
  await expect(page.locator("[data-pay-error]")).toContainText("The amount cannot be more than the open amount, $560.00");
  expect(asked).toBe(0);
});

test("a lost answer on a payment that landed says so once the books show it, and closes on Close", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  const before = centsOf(await page.locator("[data-payee-lines-total]").innerText());
  await openPayForm(page, "B-4107");

  // The answer is lost on the way back, and the payment landed.
  await page.route("**/api/v1/purchasing/bills/*/payments", async (route) => {
    await route.fetch();
    await route.abort("connectionreset");
  });
  await page.locator("[data-field='pay-amount']").fill("460.00");
  await page.locator("[data-action='payables-pay-send']").click();

  // The books are read again and show the $460.00 landed: the bill has $100.00 open. The key ends
  // and the form says the payment is on the bill, with nothing to send (S6 on 701, P2-2 and P2-3).
  await expect(page.locator("[data-line-row]", { hasText: "B-4107" })).toContainText("$100.00");
  await expect(page.locator("[data-pay-landed]")).toBeVisible();
  await page.locator("[data-action='payables-pay-cancel']").click();
  await expect(page.locator("[data-pay-form]")).toHaveCount(0);
  expect(centsOf(await page.locator("[data-payee-lines-total]").innerText())).toBe(before - 46_000);
});

test("a retry after a lost answer carries the same key, so the money moves once", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  await openPayForm(page, "B-4107");
  const before = centsOf(await page.locator("[data-payee-lines-total]").innerText());

  // The keys the presses carried, read off the requests the desk made.
  const keys: string[] = [];
  page.on("request", (req) => {
    if (req.url().includes("/api/v1/purchasing/bills/") && req.url().endsWith("/payments")) {
      keys.push(req.headers()["idempotency-key"] ?? "");
    }
  });
  // The first press never reaches the stub: the answer is lost before the money is asked for.
  await page.route("**/api/v1/purchasing/bills/*/payments", async (route) => {
    await route.abort("connectionreset");
  });
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-action='payables-pay-send']").click();
  await expect(page.locator("[data-pay-error]")).toBeVisible();
  expect(keys.length).toBe(1);
  expect(keys[0]).not.toBe("");

  // The person presses again: the desk sends the same key, so the core judges the second press
  // against the first one, and the money moves once.
  await page.unroute("**/api/v1/purchasing/bills/*/payments");
  await page.locator("[data-action='payables-pay-send']").click();
  await expect(page.locator("[data-pay-form]")).toHaveCount(0);
  expect(keys.length).toBe(2);
  expect(keys[1]).toBe(keys[0]);
  // The books are read again before the leg reads the total: the form closes before they come back.
  await expect(page.locator("[data-line-row]", { hasText: "B-4107" })).toContainText("$460.00");
  expect(centsOf(await page.locator("[data-payee-lines-total]").innerText())).toBe(before - 10_000);
});

test("a payment dated ahead of today still takes the bill's room, the way the core counts it", async ({ request }) => {
  await resetStub(request);
  // B-4108 is $315.00 with nothing paid against it. A payment dated tomorrow is a PAID instalment
  // and the core counts every one of them, whatever day it carries.
  const future = await request.post(`/api/v1/purchasing/bills/${B4108}/payments`, {
    headers: { authorization: `Bearer ${demoToken("controller")}`, "Idempotency-Key": "a-payment-dated-tomorrow" },
    data: { amount_cents: 20_000, paid_at: `${dayFromToday(1)}T12:00:00Z`, note: "" },
  });
  expect(future.ok()).toBe(true);

  // The next press asks for more than the bill has left, and the core refuses it in its own words.
  const over = await request.post(`/api/v1/purchasing/bills/${B4108}/payments`, {
    headers: { authorization: `Bearer ${demoToken("controller")}`, "Idempotency-Key": "a-second-payment-dated-tomorrow" },
    data: { amount_cents: 20_000, paid_at: `${dayFromToday(1)}T12:00:00Z`, note: "" },
  });
  expect(over.status()).toBe(409);
  expect((await over.json()).error).toBe("purchasing: payment of 20000 cents exceeds open balance 11500 cents on bill B-4108");
});

test("the landed cost footnote is on a carrier's page and on no other", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);

  // A carrier's page: the money on it is a landed cost, so the footnote belongs there.
  await openPayee(page, "Northwind Freight Lines (demo)");
  await expect(page.locator("section[aria-label='Open items'] p.foot")).toContainText(
    "Landed costs are owed to the carrier or broker, not to the supplier.",
  );

  // A plain vendor's page: there is no landed cost there, so the footnote would contradict the page.
  await page.locator("[data-action='payables-back']").click();
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  await expect(page.locator("section[aria-label='Open items'] p.foot")).toHaveCount(0);
});

test("the board and the payee page write a day in the desk's one words, never as digits", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);

  // The board's own day, in the desk's words.
  const foot = page.locator("[data-payables-foot]");
  await expect(foot).toContainText(`As of ${dayWords(dayFromToday(0))}`);
  await expect(foot).not.toContainText(/\d{1,2}\/\d{1,2}\/\d{4}/);

  // The payee page's own day, in the same words.
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  const lines = page.locator("table.paylines tfoot");
  await expect(lines).toContainText(`as of ${dayWords(dayFromToday(0))}`);
  await expect(lines).not.toContainText(/\d{1,2}\/\d{1,2}\/\d{4}/);
});

test("the row being paid is marked as the one being paid, in the desk's own selected row style", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");

  const row = page.locator("[data-line-row]", { hasText: "B-4107" });
  const other = page.locator("[data-line-row]", { hasText: "B-4108" });
  await expect(row).toHaveAttribute("aria-current", "false");

  // The Pay form is open on that bill, so its row wears the desk's selected row ground and the other
  // rows do not.
  await openPayForm(page, "B-4107");
  await expect(row).toHaveAttribute("aria-current", "true");
  const marked = await row.locator("td").first().evaluate((el) => getComputedStyle(el).backgroundColor);
  const unmarked = await other.locator("td").first().evaluate((el) => getComputedStyle(el).backgroundColor);
  expect(marked).not.toBe(unmarked);
});

test("the Pay control keeps the desk's compact size on a wide screen and meets the 44 px bar on a phone", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");

  const pay = page.locator("[data-action='payables-pay']").first();
  await expect(pay).toBeVisible();
  const wide = await pay.boundingBox();
  await page.setViewportSize({ width: 390, height: 844 });
  const phone = await pay.boundingBox();
  expect(wide).not.toBeNull();
  expect(phone).not.toBeNull();

  // A finger needs 44 px on a phone (UX-29); a wide screen keeps the compact control.
  expect(Math.round(wide!.height)).toBeLessThan(44);
  expect(Math.round(phone!.height)).toBeGreaterThanOrEqual(44);
});
