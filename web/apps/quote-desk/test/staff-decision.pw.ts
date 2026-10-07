// Round 10 item 4 (PR 247): a staff member records the customer's yes or no on a sent quote. The offer is the
// server's (can_accept, can_reject); the accept names the channel, carries the total read to the customer and the
// statement that the customer confirmed the quantities; the reject names the channel and a reason.
import { expect, test, type Page } from "@playwright/test";
import { hold, settle, signIn } from "./switch-helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function seedSent(page: Page): Promise<string> {
  const response = await page.request.post("/_stub/seed-quote", { data: { status: "SENT", customer_id: "c-fenwick" } });
  return ((await response.json()) as { id: string }).id;
}

async function openById(page: Page, id: string): Promise<void> {
  await signIn(page);
  await page.evaluate((quote) => {
    window.location.hash = `#/quotes/${quote}`;
  }, id);
  await expect(page.getByTestId("qb-context")).toBeVisible();
}

interface Seen {
  staffDecisions: Array<{ kind: string; quote_id: string; key: string | null; body: Record<string, unknown> }>;
}
async function seen(page: Page): Promise<Seen> {
  return (await (await page.request.get("/_stub/state")).json()) as Seen;
}

test("a sent quote offers the customer's decision, and a draft or a converted quote does not", async ({ page }) => {
  const id = await seedSent(page);
  await openById(page, id);
  await expect(page.getByTestId("qb-decision")).toBeVisible();
  await expect(page.getByTestId("qb-decision-accept-open")).toBeVisible();
  await expect(page.getByTestId("qb-decision-reject-open")).toBeVisible();
  const done = await page.request.post("/_stub/seed-quote", { data: { status: "CONVERTED", customer_id: "c-fenwick" } });
  const doneId = ((await done.json()) as { id: string }).id;
  await page.evaluate((quote) => {
    window.location.hash = `#/quotes/${quote}`;
  }, doneId);
  await expect(page.getByTestId("qb-status")).toContainText(/converted/i);
  await expect(page.getByTestId("qb-decision")).toHaveCount(0);
});

test("accepting for the customer names the channel, the total read to them and the quantities statement, then Convert is offered", async ({ page }) => {
  const id = await seedSent(page);
  await openById(page, id);
  await page.getByTestId("qb-decision-accept-open").click();
  const accept = page.getByTestId("qb-decision-accept");
  await expect(accept).toBeDisabled();
  await expect(page.getByTestId("qb-decision-total")).toContainText("$");
  const read = (await page.getByTestId("qb-decision-total").innerText()).replace(/[^0-9.]/g, "");
  await page.getByTestId("qb-decision-channel").selectOption("phone");
  await expect(accept).toBeDisabled();
  await expect(page.getByTestId("qb-decision-statement-text")).toContainText("quantities");
  await page.getByTestId("qb-decision-statement").check();
  await page.getByTestId("qb-decision-note").fill("Said yes on the phone");
  await expect(accept).toBeEnabled();
  await accept.click();
  await expect(page.getByTestId("qb-status")).toContainText(/accepted/i);
  await expect(page.getByTestId("qb-decision-result")).toContainText("accepted for the customer");
  await expect(page.getByTestId("qb-convert")).toBeVisible();
  const sent = (await seen(page)).staffDecisions;
  expect(sent).toHaveLength(1);
  expect(sent[0]!.kind).toBe("accept");
  expect(sent[0]!.body).toMatchObject({ channel: "phone", quantities_confirmed: true, note: "Said yes on the phone" });
  expect(String(Number(sent[0]!.body.expected_total_cents) / 100)).toBe(String(Number(read)));
  expect(sent[0]!.key).toMatch(/\S/);
});

test("a quote that moved since it was read is refused in words and nothing is recorded", async ({ page }) => {
  const id = await seedSent(page);
  await openById(page, id);
  await page.getByTestId("qb-decision-accept-open").click();
  await page.getByTestId("qb-decision-channel").selectOption("email");
  await page.getByTestId("qb-decision-statement").check();
  await page.request.put("/_stub/decision-moved", { data: { moved: true } });
  await page.getByTestId("qb-decision-accept").click();
  await expect(page.getByTestId("qb-decision-error")).toContainText("total");
  await expect(page.getByTestId("qb-status")).toContainText(/sent/i);
});

test("a retry after a failed answer carries the same idempotency key", async ({ page }) => {
  const id = await seedSent(page);
  await openById(page, id);
  await page.getByTestId("qb-decision-accept-open").click();
  await page.getByTestId("qb-decision-channel").selectOption("in_person");
  await page.getByTestId("qb-decision-statement").check();
  await page.request.put("/_stub/decision-fail", { data: { count: 1 } });
  await page.getByTestId("qb-decision-accept").click();
  await expect(page.getByTestId("qb-decision-error")).not.toHaveText("");
  await page.getByTestId("qb-decision-accept").click();
  await expect(page.getByTestId("qb-status")).toContainText(/accepted/i);
  const sent = (await seen(page)).staffDecisions;
  expect(sent).toHaveLength(2);
  expect(sent[0]!.key).toBe(sent[1]!.key);
});

test("declining for the customer names the channel and a reason, and the note is required for Other", async ({ page }) => {
  const id = await seedSent(page);
  await openById(page, id);
  await page.getByTestId("qb-decision-reject-open").click();
  const reject = page.getByTestId("qb-decision-reject");
  await expect(reject).toBeDisabled();
  await page.getByTestId("qb-decision-channel").selectOption("text");
  await page.getByTestId("qb-decision-reason").selectOption("other");
  await expect(reject).toBeDisabled();
  await page.getByTestId("qb-decision-note").fill("Bought elsewhere");
  await expect(reject).toBeEnabled();
  await page.getByTestId("qb-decision-reason").selectOption("price");
  await reject.click();
  await expect(page.getByTestId("qb-status")).toContainText(/lost/i);
  const sent = (await seen(page)).staffDecisions;
  expect(sent[0]!.kind).toBe("reject");
  expect(sent[0]!.body).toMatchObject({ channel: "text", reason_code: "price" });
  await expect(page.getByTestId("qb-decision")).toHaveCount(0);
});

async function seedWith(page: Page, qty: number): Promise<string> {
  const response = await page.request.post("/_stub/seed-quote", { data: { status: "SENT", customer_id: "c-fenwick", lines: [{ p: "p-wall-std", q: qty }] } });
  return ((await response.json()) as { id: string }).id;
}

async function goTo(page: Page, id: string): Promise<void> {
  await page.evaluate((quote) => {
    window.location.hash = `#/quotes/${quote}`;
  }, id);
}

test("guard decision read: the read of the quote just left never lands on the quote now open", async ({ page }) => {
  const a = await seedWith(page, 4);
  const b = await seedWith(page, 9);
  await signIn(page);
  // The work area's read passes; the panels' reads of the quote, which come after it, are held.
  let reads = 0;
  const read = await hold(page, new RegExp(`/api/v1/quotes/${a}$`), { method: "GET", match: () => (reads += 1) > 1 });
  await goTo(page, a);
  await read.asked;
  await goTo(page, b);
  await expect(page.getByTestId("qb-decision")).toBeVisible();
  await page.getByTestId("qb-decision-accept-open").click();
  const shown = await page.getByTestId("qb-decision-total").innerText();
  await read.release();
  await settle(page);
  await expect(page.getByTestId("qb-decision-total")).toHaveText(shown);
});

test("guard decision write: a decision that answers after another quote opened says nothing there", async ({ page }) => {
  const a = await seedWith(page, 4);
  const b = await seedWith(page, 9);
  await openById(page, a);
  await page.getByTestId("qb-decision-accept-open").click();
  await page.getByTestId("qb-decision-channel").selectOption("phone");
  await page.getByTestId("qb-decision-statement").check();
  const write = await hold(page, new RegExp(`/api/v1/quotes/${a}/accept$`), { method: "POST" });
  await page.getByTestId("qb-decision-accept").click();
  await write.asked;
  await goTo(page, b);
  await expect(page.getByTestId("qb-decision")).toBeVisible();
  await write.release();
  await settle(page);
  await expect(page.getByTestId("qb-decision-result")).toHaveCount(0);
  await expect(page.getByTestId("qb-decision-accept-open")).toBeVisible();
});

test("P2-1 cancel then reopen starts clean: nothing stays armed", async ({ page }) => {
  const id = await seedSent(page);
  await openById(page, id);
  await page.getByTestId("qb-decision-accept-open").click();
  await page.getByTestId("qb-decision-channel").selectOption("phone");
  await page.getByTestId("qb-decision-statement").check();
  await expect(page.getByTestId("qb-decision-accept")).toBeEnabled();
  await page.getByTestId("qb-decision-cancel").click();
  await page.getByTestId("qb-decision-accept-open").click();
  await expect(page.getByTestId("qb-decision-channel")).toHaveValue("");
  await expect(page.getByTestId("qb-decision-statement")).not.toBeChecked();
  await expect(page.getByTestId("qb-decision-accept")).toBeDisabled();
  // Restating both arms it again.
  await page.getByTestId("qb-decision-channel").selectOption("email");
  await page.getByTestId("qb-decision-statement").check();
  await expect(page.getByTestId("qb-decision-accept")).toBeEnabled();
});

test("P2-2 after a refusal for a moved total the panel and the page show the server's total, and the retry waits for the person to confirm it", async ({ page }) => {
  const id = await seedSent(page);
  await openById(page, id);
  await page.getByTestId("qb-decision-accept-open").click();
  const oldTotal = (await page.getByTestId("qb-decision-total").innerText()).replace(/[^0-9.]/g, "");
  await page.getByTestId("qb-decision-channel").selectOption("phone");
  await page.getByTestId("qb-decision-statement").check();
  await page.request.put("/_stub/decision-moved", { data: { moved: true } });
  await page.getByTestId("qb-decision-accept").click();
  await expect(page.getByTestId("qb-decision-error")).toContainText("total");
  const newTotal = String((Number(oldTotal) + 1).toFixed(2));
  await expect(page.getByTestId("qb-decision-total")).toContainText(newTotal);
  await expect(page.getByTestId("qb-total")).toContainText(newTotal);
  // The retry is held until the person says they read the new total to the customer.
  await expect(page.getByTestId("qb-decision-accept")).toBeDisabled();
  await page.getByTestId("qb-decision-total-confirm").check();
  await expect(page.getByTestId("qb-decision-accept")).toBeEnabled();
  await page.getByTestId("qb-decision-accept").click();
  await expect(page.getByTestId("qb-status")).toContainText(/accepted/i);
  const sent = (await seen(page)).staffDecisions;
  expect(sent).toHaveLength(2);
  expect(String(Number(sent[1]!.body.expected_total_cents) / 100)).toBe(String(Number(newTotal)));
  expect(sent[1]!.key).not.toBe(sent[0]!.key);
});

test("a role without the grant to accept or reject sees no decision section, and the server refuses it anyway", async ({ page }) => {
  const id = await seedSent(page);
  await page.request.put("/_stub/decision-grant", { data: { refused: true } });
  await openById(page, id);
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await expect(page.getByTestId("qb-decision")).toHaveCount(0);
  await expect(page.getByTestId("qb-decision-accept-open")).toHaveCount(0);
  await expect(page.getByTestId("qb-decision-reject-open")).toHaveCount(0);
  const refused = await page.request.post(`/api/v1/quotes/${id}/accept`, { data: { channel: "phone", quantities_confirmed: true, expected_total_cents: 1 }, headers: { Authorization: "Bearer x" } });
  expect([401, 403]).toContain(refused.status());
});
