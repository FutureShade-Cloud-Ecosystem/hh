// The receivables slice walked in a real browser against the stub, held to the
// Money Desk references (hh-sup/design-refs/money-desk): the register with its
// aging tiles and Needs you, an account's open invoices with the payment panel,
// the statement, and the credit holds. The stub is held to the core's shapes and
// sentences by test/stub-contract.test.mjs.
import { test, expect, openDesk, dayFromToday, receivablesHandoff } from "./helpers.js";
import type { Page } from "@playwright/test";

async function openAccount(page: Page, name: string): Promise<void> {
  await page.locator("[data-register-row]", { hasText: name }).first().getByRole("button").click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  // The row's invoice arrives prefilled once the account has loaded; typing before then would be overwritten.
  await expect(page.locator("[data-field='receive-amount']")).not.toHaveValue("");
}

test("the way in with no handoff offers the role cards", async ({ page, request }) => {
  await request.post("/_stub/reset");
  await page.goto("/");
  await expect(page.locator("[data-flow]")).toHaveCount(2);
  await expect(page.locator("[data-flow]").first()).toContainText("Payables and Receivables");
});

test("the way in builds the role code from the session's own code, never from the role's display name", async ({ page, request }) => {
  await request.post("/_stub/reset");
  // A renamed role display name must not move the landing: the session's own role code decides it.
  await page.route("**/api/v1/demo/session", async (route) => {
    const answer = await route.fetch();
    const body = await answer.json();
    body.user.role_name = "Chief Book Keeper";
    return route.fulfill({ response: answer, headers: { "content-type": "application/json" }, body: JSON.stringify(body) });
  });
  await page.goto("/");
  await page.locator("[data-flow='financial-controller']").click();
  await expect(page.getByRole("button", { name: "Approvals and ledger" })).toBeVisible();
});

test("the desk opens on the register: four tiles, the rows overdue first, Needs you, and later marked in words", async ({ page, request }) => {
  await openDesk(page, request);
  await expect(page.getByRole("heading", { name: "Receivables", exact: true })).toBeVisible();
  expect(await page.locator("[data-card] [data-card-title]").allTextContents()).toEqual(["90 days and over", "61 to 89 days", "1 to 60 days", "Current"]);
  await expect(page.locator("[data-card]").first()).toContainText("Past due");
  await expect(page.locator("[data-card-asof]")).toHaveCount(0);
  await expect(page.locator("[data-card-note]")).toHaveText("As of today · Grouped by your past due settings");
  await expect(page.locator("[data-register-row]")).toHaveCount(9);
  const first = page.locator("[data-register-row]").first();
  await expect(first).toContainText("INV-1001");
  await expect(first).toContainText("104d past due");
  await expect(first).toContainText("promise open");
  await expect(first).toContainText("104 days ago");
  await expect(page.locator("[data-register-row]", { hasText: "INV-1004" })).toContainText("disputed");
  await expect(page.locator("[data-register-row]", { hasText: "INV-1006" })).toContainText("in 9 days");
  await expect(page.locator("[data-register-foot]")).toContainText("9 invoices, $16,049.50");
  await expect(page.locator("[data-bounced-row]")).toHaveCount(1);
  await expect(page.locator("[data-need-hold]")).toHaveCount(2);
  await expect(page.locator("[data-need-paper]")).toHaveCount(1);
  await expect(page.locator(".tab-later", { hasText: "Payables" })).toContainText("later");
});

test("a tile filters the register to the accounts it counted, and clears", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-card='card.escalation']").getByRole("button").click();
  // The tile counted one account, which carries two open invoices.
  await expect(page.locator("[data-register-row]")).toHaveCount(2);
  await expect(page.locator("[data-register-row]").first()).toContainText("Granite Edge Landscaping");
  await page.locator("[data-card='card.escalation']").getByRole("button").click();
  await expect(page.locator("[data-register-row]")).toHaveCount(9);
});

test("the register's filters: overdue, promised, disputed and paid", async ({ page, request }) => {
  await openDesk(page, request);
  const pick = (word: string) => page.getByRole("group", { name: "Register filter" }).getByRole("button", { name: word, exact: true }).click();
  await pick("Past due");
  await expect(page.locator("[data-register-row]")).toHaveCount(7);
  await expect(page.locator("[data-testid='c8-summary']")).toContainText("7 of 9 past due · $11,499.50");
  await expect(page.locator("[data-register-row]").first().locator("[data-testid='overdue-line']")).toContainText("104 days past due");
  await expect(page.locator("[data-register-row]").first().locator("[data-testid='bucket-pill']")).toHaveText("Over 90 days");
  await pick("Current");
  await expect(page.locator("[data-register-row]")).toHaveCount(2);
  await expect(page.locator("[data-testid='c8-summary']")).toHaveCount(0);
  await pick("On hold");
  await expect(page.locator("[data-register-row]")).toHaveCount(6);
  await pick("All");
  await expect(page.locator("[data-register-row]")).toHaveCount(9);
});

test("aging to payment applied: Record payment on a row opens the account with the payment ready, and the server's split and balance show", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-register-row]", { hasText: "INV-1002" }).getByRole("button", { name: "Record payment" }).click();
  await expect(page.getByRole("heading", { name: "Granite Edge Landscaping (demo)" })).toBeVisible();
  await expect(page.locator("[data-hold-banner]")).toContainText("on hold: Returned payment");
  await expect(page.locator("[data-field='receive-amount']")).toHaveValue("$960.00");
  await expect(page.locator("[data-applied]")).toHaveText("$960.00 of $960.00");
  await expect(page.locator("[data-allocation]")).toContainText("INV-1002 · closes");
  const amount = page.locator("[data-field='receive-amount']");
  await amount.fill("100");
  await amount.blur();
  await expect(amount).toHaveValue("$100.00");
  await expect(page.locator("[data-allocation]")).toContainText("INV-1002 · part, $860.00 stays open");
  await expect(page.locator("[data-applied]")).toHaveText("$100.00 of $100.00");
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1002" }).locator("[data-applied-cell]")).toHaveText("$100.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 1042");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("PAY-0101 applied: $100.00 on INV-1002, balance now $860.00");
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1002" })).toContainText("$860.00");
  await expect(page.locator("[data-hold-stays]")).toContainText("The hold stays");
});

test("one payment across two invoices: the server splits it oldest first and posts it as one payment", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Pick all open" }).click();
  const amount = page.locator("[data-field='receive-amount']");
  await amount.fill("$5,000.00");
  await amount.blur();
  await expect(page.locator("[data-allocation]")).toContainText("INV-1001 · closes");
  await expect(page.locator("[data-allocation]")).toContainText("INV-1002 · part, $785.00 stays open");
  await expect(page.locator("[data-applied]")).toHaveText("$5,000.00 of $5,000.00");
  await expect(page.locator("[data-unapplied]")).toContainText("unapplied $0.00");
  await expect(page.locator("[data-hold-stays]")).toContainText("$785.00 left on INV-1002");
  await page.locator("[data-field='receive-reference']").fill("Cheque 4471");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 applied: $4,825.00 on INV-1001, balance now $0.00; $175.00 on INV-1002, balance now $785.00");
  await expect(page.locator("[data-notice]")).not.toContainText("PAY-0102");
});

test("a preview answers while a payments writer is mid flight (the final route's preview draws no counter)", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Pick all open" }).click();
  const amount = page.locator("[data-field='receive-amount']");
  await amount.fill("$5,000.00");
  await amount.blur();
  await expect(page.locator("[data-applied]")).toHaveText("$5,000.00 of $5,000.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 4488");
  // Park the next real apply, the shape of a payments writer (a till payment) settling under
  // the payments counter while the desk keeps typing.
  await request.post("/_stub/park-apply", { data: { ms: 4000 } });
  await page.getByRole("button", { name: "Record payment" }).click();
  // The writer is mid flight: the button is held and no outcome has shown.
  await expect(page.getByRole("button", { name: "Record payment" })).toBeDisabled();
  await expect(page.locator("[data-notice]")).toHaveCount(0);
  await expect(page.locator("[data-error]")).toHaveCount(0);
  // Typing asks the server for a new split inside the writer's window; the final route's
  // preview takes no counter and no row lock, so its answer must land ahead of the writer.
  await amount.fill("$3,000.00");
  await expect(page.locator("[data-applied]")).toHaveText("$3,000.00 of $3,000.00", { timeout: 2500 });
  await expect(page.getByRole("button", { name: "Record payment" })).toBeDisabled();
  // The parked writer then answers on its own clock, and the pressed payment lands once.
  await expect(page.locator("[data-notice]")).toContainText("PAY-0101 applied: $4,825.00 on INV-1001, balance now $0.00; $175.00 on INV-1002", { timeout: 15000 });
});

type ApplyBody = { preview: boolean; idempotency_key: string; amount_cents: number; reference: string; invoice_ids: string[] } & Record<string, unknown>;

async function readyToPay(page: Page, request: Parameters<typeof openDesk>[1], reference: string): Promise<void> {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Pick all open" }).click();
  const amount = page.locator("[data-field='receive-amount']");
  await amount.fill("$5,000.00");
  await amount.blur();
  await expect(page.locator("[data-applied]")).toHaveText("$5,000.00 of $5,000.00");
  await page.locator("[data-field='receive-reference']").fill(reference);
}

test("the panel sends the route's body and nothing it computed, and every preview carries its own key", async ({ page, request }) => {
  const sent: ApplyBody[] = [];
  await page.route("**/api/v1/ar/payments/apply", (route) => {
    sent.push(route.request().postDataJSON() as ApplyBody);
    return route.continue();
  });
  await readyToPay(page, request, "Cheque 4471");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 applied");
  const previews = sent.filter((b) => b.preview);
  const posts = sent.filter((b) => !b.preview);
  expect(previews.length).toBeGreaterThan(0);
  expect(posts).toHaveLength(1);
  for (const b of sent) {
    expect(Object.keys(b).sort()).toEqual(["amount_cents", "customer_id", "idempotency_key", "invoice_ids", "method", "preview", "reference"]);
    expect(b.idempotency_key).not.toBe("");
  }
  expect(posts[0]?.amount_cents).toBe(500000);
  expect(new Set(sent.map((b) => b.idempotency_key)).size).toBe(sent.length);
});

test("an answer lost after the server applied the payment says the outcome is unknown, and pressing again reuses the key and posts once", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  let lose = true;
  await page.route("**/api/v1/ar/payments/apply", async (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    posts.push(body);
    if (lose) {
      lose = false;
      await route.fetch();
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await readyToPay(page, request, "Cheque 4471");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  await expect(page.locator("[data-error]")).toContainText("Check the account");
  await expect(page.locator("[data-error]")).not.toContainText("Nothing was changed");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 was already applied");
  expect(posts).toHaveLength(2);
  expect(posts[1]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  await expect(page.locator("[data-notice]")).not.toContainText("PAY-0102");
});

test("a 5xx answer says the outcome is unknown, and every retry carries the same key whatever the form now says", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  let failures = 2;
  await page.route("**/api/v1/ar/payments/apply", (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    posts.push(body);
    if (failures > 0) {
      failures -= 1;
      return route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "the receivables register is not answering right now" }) });
    }
    return route.continue();
  });
  await readyToPay(page, request, "Cheque 4471");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  expect(posts).toHaveLength(2);
  expect(posts[1]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  // The cheque number changes after the unknown outcome: the press still carries this payment's key,
  // so the money can only land once. Nothing landed here (the failures were synthetic), so the changed
  // body posts as the payment's first landing under the same key.
  await page.locator("[data-field='receive-reference']").fill("Cheque 4472");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 applied");
  expect(posts).toHaveLength(3);
  expect(posts[2]?.idempotency_key).toBe(posts[0]?.idempotency_key);
});

test("a lost answer then a changed cheque number sends the same key; the refusal keeps the form and the next press records the cheque", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  let lose = true;
  await page.route("**/api/v1/ar/payments/apply", async (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    posts.push(body);
    if (lose) {
      lose = false;
      await route.fetch();
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Pick all open" }).click();
  await page.locator("[data-field='receive-amount']").fill("500.00");
  await expect(page.locator("[data-applied]")).toHaveText("$500.00 of $500.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 447");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  // The cheque number is corrected and the payment pressed again: the same key goes out, and because
  // the first press landed, the route refuses the changed body in words. The earlier press IS recorded
  // and this cheque is NOT, so both are said in the error words, never a green notice; the form is
  // kept for the next press, and the account reloads so the check the words ask for reads the balances.
  await page.locator("[data-field='receive-reference']").fill("Cheque 4471");
  await page.getByRole("button", { name: "Record payment" }).click();
  expect(posts).toHaveLength(2);
  expect(posts[1]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  await expect(page.locator("[data-error]")).toContainText("This payment was not recorded. The earlier press was recorded as a different payment.");
  await expect(page.locator("[data-error]")).toContainText("press Record payment to record this one");
  await expect(page.locator("[data-notice]")).toHaveCount(0);
  await expect(page.locator("[data-field='receive-amount']")).toHaveValue(/500\.00/);
  await expect(page.locator("[data-field='receive-reference']")).toHaveValue("Cheque 4471");
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1001" })).toContainText("$4,325.00");
  // The next press mints a fresh key and records this cheque: one payment for each press that landed.
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0102 applied: $500.00 on INV-1001");
  expect(posts).toHaveLength(3);
  expect(posts[2]?.idempotency_key).not.toBe(posts[0]?.idempotency_key);
  await expect(page.locator("[data-notice]")).not.toContainText("PAY-0103");
});

test("a held key re-used for a different payment: the error words say both, the form is kept, and the next press records the new cheque", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  let lose = true;
  await page.route("**/api/v1/ar/payments/apply", async (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    posts.push(body);
    if (lose) {
      lose = false;
      await route.fetch();
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  // The first cheque lands and its answer is lost: the outcome is unknown and the key is held.
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  // No press. The next cheque on the same account is a DIFFERENT payment: the route answers for the
  // held key by refusing it, and the screen must never read that as this cheque being recorded.
  await page.getByRole("button", { name: "Clear" }).first().click();
  await page.getByRole("checkbox", { name: "Pay INV-1002" }).click();
  await page.locator("[data-field='receive-amount']").fill("50.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 5000");
  await page.getByRole("button", { name: "Record payment" }).click();
  expect(posts).toHaveLength(2);
  expect(posts[1]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  await expect(page.locator("[data-error]")).toContainText("This payment was not recorded. The earlier press was recorded as a different payment.");
  await expect(page.locator("[data-notice]")).toHaveCount(0);
  await expect(page.locator("[data-field='receive-reference']")).toHaveValue("Cheque 5000");
  await expect(page.locator("[data-field='receive-amount']")).toHaveValue(/50\.00/);
  // The next press mints a fresh key and records the new cheque: one payment for each cheque.
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0102 applied: $50.00 on INV-1002");
  expect(posts).toHaveLength(3);
  expect(posts[2]?.idempotency_key).not.toBe(posts[0]?.idempotency_key);
  await page.locator("[data-invoice-row]", { hasText: "INV-1002" }).getByRole("button", { name: "INV-1002" }).click();
  await expect(page.locator("[data-payment-row]", { hasText: "PAY-0101" })).toHaveCount(1);
  await expect(page.locator("[data-payment-row]", { hasText: "PAY-0102" })).toHaveCount(1);
  await expect(page.locator("[data-payment-row]", { hasText: "PAY-0103" })).toHaveCount(0);
});

test("the same re-use from a duplicated tab: the copied key refuses the new cheque in the error words, and the next press records it", async ({ page, request, context }) => {
  const posts: ApplyBody[] = [];
  let lose = true;
  await page.route("**/api/v1/ar/payments/apply", async (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    posts.push(body);
    if (lose) {
      lose = false;
      await route.fetch();
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  // A duplicated tab copies the session storage as it stands, held key included.
  const held = await page.evaluate((): [string, string] | null => {
    const name = Object.keys(window.sessionStorage).find((k) => k.startsWith("hh-money-desk.held-payment-keys.v1."));
    return name === undefined ? null : [name, window.sessionStorage.getItem(name) ?? ""];
  });
  if (held === null) throw new Error("the held key record was not in the tab's session storage");
  // Tab A resolves its own press: the replay answer says the payment was already applied.
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 was already applied");
  // Tab B starts from the copied record and takes a NEW cheque: the copied key goes out, the route
  // refuses it in words, and the screen says both things in the error style with the form kept.
  const postsB: ApplyBody[] = [];
  const tabB = await context.newPage();
  await tabB.route("**/api/v1/ar/payments/apply", (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (!body.preview) postsB.push(body);
    return route.continue();
  });
  await tabB.addInitScript(([name, record]) => {
    if (window.sessionStorage.getItem(name) === null) window.sessionStorage.setItem(name, record);
  }, held);
  await tabB.addInitScript(receivablesHandoff);
  await tabB.goto("/");
  await expect(tabB.locator("[data-card]")).toHaveCount(4);
  await tabB.locator("[data-register-row]", { hasText: "Granite Edge Landscaping" }).first().getByRole("button").click();
  await expect(tabB.getByRole("heading", { name: "Granite Edge Landscaping (demo)" })).toBeVisible();
  await tabB.getByRole("button", { name: "Clear" }).first().click();
  await tabB.getByRole("checkbox", { name: "Pay INV-1002" }).click();
  await tabB.locator("[data-field='receive-amount']").fill("50.00");
  await tabB.locator("[data-field='receive-reference']").fill("Cheque 5000");
  await tabB.getByRole("button", { name: "Record payment" }).click();
  expect(postsB).toHaveLength(1);
  expect(postsB[0]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  await expect(tabB.locator("[data-error]")).toContainText("This payment was not recorded. The earlier press was recorded as a different payment.");
  await expect(tabB.locator("[data-notice]")).toHaveCount(0);
  await expect(tabB.locator("[data-field='receive-reference']")).toHaveValue("Cheque 5000");
  // The next press mints a fresh key and records the new cheque.
  await tabB.getByRole("button", { name: "Record payment" }).click();
  await expect(tabB.locator("[data-notice]")).toContainText("Payment PAY-0102 applied: $50.00 on INV-1002");
  expect(postsB).toHaveLength(2);
  expect(postsB[1]?.idempotency_key).not.toBe(posts[0]?.idempotency_key);
  await tabB.close();
});

test("a payment recorded on another account leaves this account's pending payment key alone", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  let firstCustomer = "";
  let lose = true;
  await page.route("**/api/v1/ar/payments/apply", async (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    if (firstCustomer === "") firstCustomer = String(body.customer_id);
    posts.push(body);
    if (lose && String(body.customer_id) === firstCustomer) {
      lose = false;
      await route.fetch();
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  const readyFirstAccount = async (): Promise<void> => {
    await openAccount(page, "Granite Edge Landscaping (demo)");
    await page.getByRole("button", { name: "Clear" }).first().click();
    await page.getByRole("checkbox", { name: "Pay INV-1002" }).click();
    await page.locator("[data-field='receive-amount']").fill("100.00");
    await page.locator("[data-field='receive-reference']").fill("Cheque 4471");
  };
  await openDesk(page, request);
  await readyFirstAccount();
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  // A payment on another account succeeds; it must not end the first account's pending key.
  await page.getByRole("button", { name: "‹ Receivables" }).click();
  await openAccount(page, "Oakline Builders (demo)");
  await page.locator("[data-field='receive-amount']").fill("100.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 9001");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("PAY-0102 applied");
  // Back on the first account, the same body presses under the first payment's key: the route answers
  // the replay, so the payment posts once and nothing doubles.
  await page.getByRole("button", { name: "‹ Receivables" }).click();
  await readyFirstAccount();
  await page.getByRole("button", { name: "Record payment" }).click();
  const mine = posts.filter((p) => String(p.customer_id) === firstCustomer);
  expect(mine).toHaveLength(2);
  expect(mine[1]?.idempotency_key).toBe(mine[0]?.idempotency_key);
  await expect(page.locator("[data-notice]")).toContainText("PAY-0101 was already applied");
  await expect(page.locator("[data-notice]")).not.toContainText("PAY-0103");
});

test("opening the invoice's payments after a lost answer never ends the held key: the next press carries it and reads already applied, with one payment", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  let sent = 0;
  await page.route("**/api/v1/ar/payments/apply", async (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    posts.push(body);
    sent += 1;
    if (sent === 2) {
      await route.fetch();
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  const tenByCheque = async (reference: string): Promise<void> => {
    await page.getByRole("button", { name: "Clear" }).first().click();
    await page.getByRole("checkbox", { name: "Pay INV-1002" }).click();
    await page.locator("[data-field='receive-amount']").fill("10.00");
    await page.locator("[data-field='receive-reference']").fill(reference);
  };
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await tenByCheque("Cheque 1");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 applied: $10.00 on INV-1002");
  // The second cheque of the same method and amount on the same invoice: its answer is lost after the stub wrote it.
  await tenByCheque("Cheque 2");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  // The check the words ask for: the invoice's payments are opened. The list carries no key and no
  // reference, so it cannot say which press wrote what; it never ends the held key.
  await page.locator("[data-invoice-row]", { hasText: "INV-1002" }).getByRole("button", { name: "INV-1002" }).click();
  await expect(page.locator("[data-payment-row]", { hasText: "PAY-0102" })).toContainText("Received");
  await page.getByRole("button", { name: "Back to payment" }).click();
  await tenByCheque("Cheque 2");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0102 was already applied");
  expect(posts).toHaveLength(3);
  expect(posts[2]?.idempotency_key).toBe(posts[1]?.idempotency_key);
  expect(posts[1]?.idempotency_key).not.toBe(posts[0]?.idempotency_key);
  await page.locator("[data-invoice-row]", { hasText: "INV-1002" }).getByRole("button", { name: "INV-1002" }).click();
  await expect(page.locator("[data-payment-row]", { hasText: "PAY-0101" })).toHaveCount(1);
  await expect(page.locator("[data-payment-row]", { hasText: "PAY-0102" })).toHaveCount(1);
  await expect(page.locator("[data-payment-row]", { hasText: "PAY-0103" })).toHaveCount(0);
});

/** One account, INV-1002 only, $100.00 by cheque; the first press's answer is lost after the stub wrote the payment. */
async function lostFirstPress(page: Page, posts: ApplyBody[], gate?: { statuses: number[] }): Promise<void> {
  const statuses = gate === undefined ? [] : [...gate.statuses];
  await page.route("**/api/v1/ar/payments/apply", async (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    posts.push(body);
    if (posts.length === 1) {
      await route.fetch();
      return route.abort("connectionreset");
    }
    const status = statuses.shift();
    if (status !== undefined) {
      return route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error: status === 401 ? "unauthorized" : "forbidden" }) });
    }
    return route.continue();
  });
}

async function fillCheque(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Clear" }).first().click();
  await page.getByRole("checkbox", { name: "Pay INV-1002" }).click();
  await page.locator("[data-field='receive-amount']").fill("100.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 4471");
}

async function gateAnswersTheRetry(status: 401 | 403, page: Page, request: Parameters<typeof openDesk>[1]): Promise<void> {
  const posts: ApplyBody[] = [];
  await lostFirstPress(page, posts, { statuses: [status] });
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("The earlier press may or may not have been recorded");
  await expect(page.locator("[data-error]")).not.toContainText("was not recorded");
  await expect(page.locator("[data-error]")).toContainText(status === 401 ? "Your sign in has ended. Sign in again." : "Your role may not do that.");
  // The press instruction waits for what the gate asked: signing in for a 401, a role that may record
  // the payment for a 403 (the same role pressing again would only draw the same 403).
  await expect(page.locator("[data-error]")).toContainText(
    status === 401 ? "Sign in again. Then press Record payment" : "Press Record payment again only once you are signed in with a role that can record it",
  );
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 was already applied");
  expect(posts).toHaveLength(3);
  expect(posts[1]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  expect(posts[2]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  await expect(page.locator("[data-notice]")).not.toContainText("PAY-0102");
}

test("a 401 on the retry after a lost answer is the gate's answer: the key is kept, the words stay unknown, and the next press carries it", async ({ page, request }) => {
  await gateAnswersTheRetry(401, page, request);
});

test("a 403 on the retry after a lost answer is the gate's answer: the key is kept, the words stay unknown, and the next press carries it", async ({ page, request }) => {
  await gateAnswersTheRetry(403, page, request);
});

test("a first press answered 401 wrote nothing: it is refused in words and the next press is a new payment", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  await page.route("**/api/v1/ar/payments/apply", (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    posts.push(body);
    if (posts.length === 1) return route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ error: "unauthorized" }) });
    return route.continue();
  });
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("The payment was not recorded. Your sign in has ended.");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 applied");
  expect(posts[1]?.idempotency_key).not.toBe(posts[0]?.idempotency_key);
});

test("a 4xx that is not the payment route's own refusal never ends the held key: a 404 on the retry keeps it and the next press replays once", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  let answered404 = false;
  await page.route("**/api/v1/ar/payments/apply", async (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    posts.push(body);
    if (posts.length === 1) {
      await route.fetch();
      return route.abort("connectionreset");
    }
    if (!answered404) {
      answered404 = true;
      return route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: "not found" }) });
    }
    return route.continue();
  });
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  // The retry is answered 404 by something that is not the payment route's own answer for this key
  // (a proxy, or the route's no existence leak answer, which its replay walk can also give for a
  // payment that landed): the outcome stays unknown and the key is kept.
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("The earlier press may or may not have been recorded");
  await expect(page.locator("[data-error]")).not.toContainText("The payment was not recorded");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 was already applied");
  expect(posts).toHaveLength(3);
  expect(posts[2]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  await expect(page.locator("[data-notice]")).not.toContainText("PAY-0102");
});

test("the held key survives a page reload: a lost answer, a reload, and the next press carries the same key", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  await lostFirstPress(page, posts);
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  await page.reload();
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 was already applied");
  expect(posts).toHaveLength(2);
  expect(posts[1]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  await expect(page.locator("[data-notice]")).not.toContainText("PAY-0102");
});

test("the held key survives a sign out and a sign in of the same user", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  await lostFirstPress(page, posts);
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  await page.locator("[data-action='sign-out']").click();
  await page.locator("[data-flow='payables-receivables']").click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 was already applied");
  expect(posts[1]?.idempotency_key).toBe(posts[0]?.idempotency_key);
});

test("held keys are never shared across users: another user starts with none, and the first user's key is left untouched", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  await lostFirstPress(page, posts);
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  await page.locator("[data-action='sign-out']").click();
  await page.locator("[data-flow='financial-controller']").click();
  await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0102 applied");
  expect(posts).toHaveLength(2);
  expect(posts[1]?.idempotency_key).not.toBe(posts[0]?.idempotency_key);
  // The first user signs in again and still holds the first key: the route answers its replay.
  await page.locator("[data-action='sign-out']").click();
  await page.locator("[data-flow='payables-receivables']").click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 was already applied");
  expect(posts).toHaveLength(3);
  expect(posts[2]?.idempotency_key).toBe(posts[0]?.idempotency_key);
});

test("a worded 4xx says the payment was not recorded in the route's own words, and the next press is a new payment with a new key", async ({ page, request }) => {
  const posts: ApplyBody[] = [];
  let refuse = true;
  await page.route("**/api/v1/ar/payments/apply", (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    posts.push(body);
    if (refuse) {
      refuse = false;
      return route.fulfill({ status: 422, contentType: "application/json", body: JSON.stringify({ error: "the selected invoices can take at most 96000 cents right now; refresh and retry" }) });
    }
    return route.continue();
  });
  await readyToPay(page, request, "Cheque 4471");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toHaveText("The payment was not recorded. The selected invoices can take at most $960.00 right now; refresh and retry.");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 applied");
  expect(posts[1]?.idempotency_key).not.toBe(posts[0]?.idempotency_key);
});

test("a pick of more than 100 invoices is refused in the browser with the route's words: no body is sent and the held key is kept", async ({ page, request }) => {
  // The wide world: Granite Edge carries 101 open invoices, the one pick the app can build that the
  // core's validateApply refuses ahead of its replay judgement, so the desk refuses it before the wire.
  await request.post("/_stub/scenario", { data: { name: "wide" } });
  const posts: ApplyBody[] = [];
  const overCeiling: ApplyBody[] = [];
  let lose = true;
  await page.route("**/api/v1/ar/payments/apply", async (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    // The route's ceiling is 100 invoices: once the desk guards it, no body naming more is ever
    // sent, not a preview's and not a post's.
    if (body.invoice_ids.length > 100) overCeiling.push(body);
    if (body.preview) return route.continue();
    posts.push(body);
    if (lose) {
      lose = false;
      await route.fetch();
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await page.addInitScript(receivablesHandoff);
  await page.goto("/");
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  // Pick all open: 101 invoices. The split area and the press read the route's own ceiling words, and
  // the browser is their source: no apply body naming over 100 invoices goes out, not even a preview's.
  await page.getByRole("button", { name: "Pick all open" }).click();
  await expect(page.locator("[data-preview-error]")).toContainText("A received payment may name at most 100 invoices; record it as more than one payment");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("A received payment may name at most 100 invoices; record it as more than one payment");
  // The guard is the browser's own stop, so the route refusal's frame is not used: nothing was sent.
  await expect(page.locator("[data-error]")).not.toContainText("The payment was not recorded");
  await expect(page.getByRole("button", { name: "Record payment" })).toBeEnabled();
  expect(overCeiling).toHaveLength(0);
  expect(posts).toHaveLength(1);
  // The held key is untouched by the guard: the next press under a lawful pick carries it, and the
  // route answers the replay.
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 was already applied");
  expect(posts).toHaveLength(2);
  expect(posts[1]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  await request.post("/_stub/reset");
});

test("on a phone the already applied notice after a replay is brought into view", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const posts: ApplyBody[] = [];
  let lose = true;
  await page.route("**/api/v1/ar/payments/apply", async (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    posts.push(body);
    if (lose) {
      lose = false;
      await route.fetch();
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await fillCheque(page);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  // The usual way out of the unknown state: the next press replays and the notice carries the only
  // words saying the money is recorded, so it is brought into view as the error branches are.
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Payment PAY-0101 was already applied");
  expect(posts[1]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  await expect(page.locator("[data-notice]")).toBeInViewport({ ratio: 0.5 });
});

test("on a phone the unknown outcome message is in view and focused when it appears", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route("**/api/v1/ar/payments/apply", (route) => {
    const body = route.request().postDataJSON() as ApplyBody;
    if (body.preview) return route.continue();
    return route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "the receivables register is not answering right now" }) });
  });
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Pick all open" }).click();
  await page.locator("[data-field='receive-amount']").fill("100.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 447");
  await page.getByRole("button", { name: "Record payment" }).click();
  const banner = page.locator("[data-error]");
  await expect(banner).toContainText("may or may not have been recorded");
  await expect(banner).toBeInViewport({ ratio: 0.5 });
  const focused = await page.evaluate(() => {
    let el: Element | null = document.activeElement;
    while (el instanceof Element && el.shadowRoot !== null) el = el.shadowRoot.activeElement;
    return el === null ? "" : el.getAttribute("data-error") ?? "other";
  });
  expect(focused).toBe("");
});

test("a payment that cannot be posted is stopped in words, and the server's refusal shows beside the split", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Clear" }).first().click();
  await page.locator("[data-field='receive-reference']").fill("x");
  await page.locator("[data-field='receive-amount']").fill("100.00");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("Pick the invoices the payment is for");
  await page.getByRole("checkbox", { name: "Pay INV-1002" }).click();
  await page.locator("[data-field='receive-amount']").fill("960.01");
  await expect(page.locator("[data-preview-error]")).toHaveText("The selected invoices can take at most $960.00 right now; refresh and retry.");
  await expect(page.locator("[data-applied]")).toHaveText("Nothing to apply yet");
  await page.locator("[data-field='receive-amount']").fill("abc");
  await expect(page.locator("[data-amount-hint]")).toHaveText("Enter dollars and cents, such as $100.00.");
});

test("a paid invoice stays for context, dimmed, and takes nothing", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping (demo)");
  const paid = page.locator("[data-invoice-row]", { hasText: "INV-1010" });
  await expect(paid).toBeVisible();
  await expect(page.locator("[data-invoice-row]").last()).toContainText("INV-1010");
  await expect(paid).toContainText("paid");
  await expect(paid.getByRole("checkbox")).toHaveCount(0);
});

test("a promise to pay is recorded, and a second is refused in the core's words", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Oakline Builders (demo)");
  await page.getByRole("button", { name: "Record a promise" }).click();
  const fill = async () => {
    await page.locator("[data-field='promise-invoice']").selectOption("INV-1003");
    await page.locator("[data-field='promise-amount']").fill("500.00");
    await page.locator("[data-field='promise-date']").fill(dayFromToday(7));
    await page.locator("[data-field='promise-by']").fill("Accounts payable");
    await page.locator("[data-field='promise-note']").fill("Cheque goes out Friday");
    await page.getByRole("button", { name: "Record promise", exact: true }).click();
  };
  await fill();
  await expect(page.locator("[data-notice]")).toContainText("Promise recorded on INV-1003: $500.00");
  await expect(page.locator("[data-promise]")).toContainText(dayFromToday(7));
  await page.getByRole("button", { name: "Back to payment" }).click();
  await page.getByRole("button", { name: "Record a promise" }).click();
  await fill();
  await expect(page.locator("[data-error]")).toHaveText("This invoice already carries a live promise; settle it before recording another.");
});

test("a payment is marked bounced from its invoice, and worked from the queue", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Riverstone Paving (demo)");
  await page.locator("[data-field='receive-amount']").fill("250.00");
  await expect(page.locator("[data-applied]")).toHaveText("$250.00 of $250.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 7781");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("PAY-0101 applied: $250.00 on INV-1005");
  await page.locator("[data-invoice-row]", { hasText: "INV-1005" }).getByRole("button", { name: "INV-1005" }).click();
  await page.locator("[data-payment-row]").getByRole("button", { name: "Mark bounced" }).click();
  await page.locator("[data-field='bounce-reason']").fill("Cheque returned NSF");
  await page.getByRole("button", { name: "Confirm bounced" }).click();
  await expect(page.locator("[data-notice]")).toContainText("marked bounced");
  await expect(page.locator("[data-notice]")).toContainText("$250.00");
  await expect(page.locator("[data-payment-row]")).toContainText("Bounced");

  await page.getByRole("button", { name: "‹ Receivables" }).click();
  await expect(page.locator("[data-bounced-row]")).toHaveCount(2);
  await page.locator("[data-bounced-row]", { hasText: "Riverstone" }).getByRole("button").click();
  const work = page.locator("[data-bounced-work]");
  await expect(work).toContainText("INV-1005");
  await expect(work).toContainText("$250.00");
  await work.getByRole("radio", { name: /Promise to pay/ }).click();
  await work.getByRole("button", { name: "Record promise" }).click();
  await expect(page.getByRole("heading", { name: "Riverstone Paving (demo)" })).toBeVisible();
  await expect(page.locator("[data-promise-form]")).toBeVisible();
});

test("the bounced payment the demo world starts with: what happened, and a reminder sent from the queue", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-bounced-row]").getByRole("button").click();
  const work = page.locator("[data-bounced-work]");
  await expect(work).toContainText("PAY-0001");
  await expect(work).toContainText("INV-1002");
  await expect(work).toContainText("fee $35.00");
  await work.getByRole("radio", { name: /Send a reminder/ }).click();
  await work.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Reminder sent for 2 invoices");
});

test("Remind on an account and the paper path item in Needs you chase what is overdue", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-need-paper]").getByRole("button").click();
  await expect(page.locator("[data-notice]")).toContainText("Reminder sent for 1 invoice");
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Remind", exact: true }).click();
  await expect(page.locator("[data-notice]")).toContainText("Reminder sent for 2 invoices");
});

test("Record a payment on the home asks which account", async ({ page, request }) => {
  await openDesk(page, request);
  await page.getByRole("button", { name: "Record a payment", exact: true }).click();
  await page.locator("[data-field='picker-account']").selectOption("Summit Patio Co (demo)");
  await page.getByRole("button", { name: "Open account" }).click();
  await expect(page.getByRole("heading", { name: "Summit Patio Co (demo)" })).toBeVisible();
  await expect(page.locator("[data-receive]")).toBeVisible();
});

test("Download CSV hands over the register", async ({ page, request }) => {
  await openDesk(page, request);
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download CSV" }).click();
  expect((await download).suggestedFilename()).toBe("receivables.csv");
});

test("credit holds: the sweep's holds are listed, lifting is held for this role and says who may, place a hold reads as later", async ({ page, request }) => {
  await openDesk(page, request);
  await page.getByRole("button", { name: "Credit holds", exact: true }).click();
  await expect(page.locator("[data-hold-row]")).toHaveCount(3);
  await expect(page.locator("[data-hold-row]", { hasText: "Cedar Ridge" })).toContainText("override in force");
  await expect(page.locator("[data-hold-row]", { hasText: "Oakline" })).toContainText("auto");
  const lift = page.locator("[data-hold-row]", { hasText: "Oakline" }).getByRole("button", { name: "Lift..." });
  await expect(lift).toHaveAttribute("aria-disabled", "true");
  await lift.click({ force: true });
  const words = page.locator("[data-error]");
  await expect(words).toContainText("Financial Controller");
  await expect(words).toContainText("General Manager");
  await expect(words).not.toContainText("Forbidden");
  await expect(page.locator("[data-held-words]")).toContainText("held for this role");
  await expect(page.locator("[data-hold-row]")).toHaveCount(3);
  const place = page.getByRole("button", { name: /Place a hold/ });
  await expect(place).toHaveAttribute("aria-disabled", "true");
  await place.click({ force: true });
  await expect(page.locator("[data-hold-panel]")).toContainText("there is no path for it yet");
  await expect(page.locator("[data-hold-panel] fieldset[disabled]")).toBeVisible();
  await page.getByRole("button", { name: "Run the sweep" }).click({ force: true });
  await expect(page.locator("[data-error]")).toContainText("Running the sweep is the Financial Controller's");
});

test("the controller lifts a hold with a reason, and overrides another", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await page.getByRole("button", { name: "Credit holds", exact: true }).click();
  await page.locator("[data-hold-row]", { hasText: "Oakline" }).getByRole("button", { name: "Lift..." }).click();
  await page.locator("[data-field='release-reason']").fill("Payment applied; promise open");
  await page.getByRole("button", { name: "Lift hold" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The credit hold is lifted.");
  await expect(page.locator("[data-hold-row]")).toHaveCount(2);
  await page.locator("[data-hold-row]", { hasText: "Granite" }).getByRole("button", { name: "Override..." }).click();
  await page.getByRole("radio", { name: /Override instead/ }).click();
  await page.locator("[data-field='release-reason']").fill("Deposit is coming");
  await page.getByRole("button", { name: "Override hold" }).click();
  await expect(page.locator("[data-notice]")).toContainText("until the override expires");
  await expect(page.locator("[data-hold-row]", { hasText: "Granite" })).toContainText("override in force");
});

test("the statement: none yet, run it, read the aging strip, send it; PDF reads as later", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Summit Patio Co (demo)");
  await page.getByRole("button", { name: "Statement", exact: true }).click();
  await expect(page.locator("[data-statement]")).toContainText("No statement has been run for this account yet.");
  await page.getByRole("button", { name: "Run a statement" }).click();
  await expect(page.locator("[data-closing]")).toContainText("$623.00");
  await expect(page.locator("[data-statement]")).toContainText("As of today");
  expect(await page.locator("[data-statement] dl.strip dt").allTextContents()).toEqual(["Current", "1 to 30 days", "31 to 60 days", "Over 60 days", "Total due"]);
  await page.getByRole("button", { name: "Send to the AP contact" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Statement queued to the AP contact");
  await page.getByRole("button", { name: /Download PDF/ }).click({ force: true });
  await expect(page.locator("[data-error]")).toContainText("later");
  await expect(page.locator(".tab-later", { hasText: "Activity" })).toContainText("later");
  await expect(page.getByRole("button", { name: "Contacts", exact: true })).toBeVisible();
});

test("the Statements tab lists every account and runs them all", async ({ page, request }) => {
  await openDesk(page, request);
  await page.getByRole("button", { name: "Statements", exact: true }).click();
  await expect(page.locator("[data-statement-row]")).toHaveCount(5);
  await expect(page.locator("[data-statement-row]").first()).toContainText("None yet");
  await page.getByRole("button", { name: "Run for every account" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Statements run: 5 accounts");
  await expect(page.locator("[data-statement-row]").first()).toContainText("As of");
});

test("the controller's holds screen has no Place a hold button", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await page.getByRole("button", { name: "Credit holds", exact: true }).click();
  await expect(page.getByRole("button", { name: /Place a hold/ })).toHaveCount(0);
});

test("on a phone no screen scrolls the page sideways", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openDesk(page, request);
  const wide = () => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(await wide()).toBeLessThanOrEqual(0);
  await page.getByRole("button", { name: "Statements", exact: true }).click();
  await expect(page.locator("[data-statement-row]")).toHaveCount(5);
  expect(await wide()).toBeLessThanOrEqual(0);
  await page.getByRole("button", { name: "Credit holds", exact: true }).click();
  await expect(page.locator("[data-hold-row]")).toHaveCount(3);
  expect(await wide()).toBeLessThanOrEqual(0);
  await page.getByRole("navigation", { name: "Money Desk views" }).getByRole("button", { name: "Receivables", exact: true }).click();
  await openAccount(page, "Granite Edge Landscaping (demo)");
  expect(await wide()).toBeLessThanOrEqual(0);
  await page.getByRole("button", { name: "Statement", exact: true }).click();
  expect(await wide()).toBeLessThanOrEqual(0);
});

test("the testers' page is served beside the desk and ends with the one contact line", async ({ page }) => {
  const answer = await page.goto("/testers.html");
  expect(answer?.status()).toBe(200);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Money Desk: the testers' brief");
  await expect(page.locator("main")).toContainText("send everything to Colton");
  await expect(page.locator("[data-claim]").first()).toBeVisible();
});

test("the empty world: each screen says so in words", async ({ page, request }) => {
  await request.post("/_stub/scenario", { data: { name: "empty" } });
  await page.addInitScript(receivablesHandoff);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Receivables", exact: true })).toBeVisible();
  await expect(page.locator("[data-register-foot]")).toBeVisible();
  await expect(page.locator(".empty", { hasText: "No invoices in this view." })).toBeVisible();
  await expect(page.getByText("Nothing needs you right now.")).toBeVisible();
  await page.getByRole("button", { name: "Credit holds", exact: true }).click();
  await expect(page.locator(".empty", { hasText: "No account is on hold." })).toBeVisible();
  await request.post("/_stub/reset");
});

// ---- the account reads every invoice, open and paid (audit B1, B2) ---------------------------------------------------

test("an account arrived at from a past due row lists its overdue invoice, prefills the payment, picks it with Pick all open, and offers Remind", async ({ page, request }) => {
  await openDesk(page, request);
  const reads: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/api/v1/ar/register?") && r.url().includes("customer_id=")) reads.push(r.url());
  });
  await page.locator("[data-register-row]", { hasText: "INV-1002" }).getByRole("button", { name: "Record payment" }).click();
  await expect(page.getByRole("heading", { name: "Granite Edge Landscaping (demo)" })).toBeVisible();
  // The account holds one paid and two open invoices: the open ones are listed, the paid one stays for context.
  const overdue = page.locator("[data-invoice-row]", { hasText: "INV-1002" });
  await expect(overdue).toBeVisible();
  await expect(overdue.getByRole("checkbox")).toBeVisible();
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1010" })).toContainText("paid");
  await expect(page.locator("[data-field='receive-amount']")).toHaveValue("$960.00");
  await expect(overdue.getByRole("checkbox")).toHaveAttribute("aria-checked", "true");
  await page.getByRole("button", { name: "Clear" }).click();
  await page.getByRole("button", { name: "Pick all open" }).click();
  await expect(overdue.getByRole("checkbox")).toHaveAttribute("aria-checked", "true");
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1001" }).getByRole("checkbox")).toHaveAttribute("aria-checked", "true");
  await expect(page.getByRole("button", { name: "Remind", exact: true })).toBeEnabled();
  await expect(page.locator("[data-open-balance]")).not.toContainText("no figure");
  await expect(page.getByText("Open balance")).toHaveCount(0);
  // No read of the account used the chip that means paid invoices only.
  expect(reads.length).toBeGreaterThan(0);
  expect(reads.filter((u) => /chip=PAID/i.test(u))).toEqual([]);
});

test("when the account's invoices cannot be read the screen says so with a way to try again, never an empty table", async ({ page, request }) => {
  await openDesk(page, request);
  let failing = true;
  await page.route("**/api/v1/ar/register?*customer_id=*", (route) => (failing ? route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "boom" }) }) : route.continue()));
  await page.locator("[data-register-row]", { hasText: "INV-1002" }).getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-invoices-failed]")).toHaveText("We could not load this account's invoices. Try again.");
  await expect(page.locator("[data-invoice-row]")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Remind", exact: true })).toBeDisabled();
  failing = false;
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1002" })).toBeVisible();
  await expect(page.locator("[data-invoices-failed]")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Remind", exact: true })).toBeEnabled();
});

test("Remind is offered only while the account has an overdue invoice", async ({ page, request }) => {
  await openDesk(page, request);
  await page.route("**/api/v1/ar/register?*customer_id=*", async (route) => {
    const answer = await route.fetch();
    const body = await answer.json();
    body.items = body.items.map((r: { days_overdue: number }) => ({ ...r, days_overdue: 0 }));
    await route.fulfill({ response: answer, body: JSON.stringify(body) });
  });
  await page.locator("[data-register-row]", { hasText: "INV-1002" }).getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1002" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Remind", exact: true })).toBeDisabled();
});

test("a draft or a void invoice is neither listed as open nor picked, and never reaches the allocation", async ({ page, request }) => {
  await openDesk(page, request);
  await page.locator("[data-register-row]", { hasText: "INV-1002" }).getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1002" })).toBeVisible();
  // The account's read carries one draft (INV-1201) and one void (INV-1202), as the core's no chip read does.
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1201" })).toHaveCount(0);
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1202" })).toHaveCount(0);
  await page.getByRole("button", { name: "Pick all open" }).click();
  await expect(page.locator("[data-invoice-row] [role='checkbox'][aria-checked='true']")).toHaveCount(2);
  await page.locator("[data-field='receive-amount']").fill("$5,000.00");
  await page.locator("[data-field='receive-amount']").blur();
  await expect(page.locator("[data-allocation]")).not.toContainText("INV-1201");
  await expect(page.locator("[data-allocation]")).not.toContainText("INV-1202");
  // The All view shows the paid history but still no draft or void.
  await page.getByRole("button", { name: "All", exact: true }).click();
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1010" })).toBeVisible();
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1201" })).toHaveCount(0);
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1202" })).toHaveCount(0);
});

test("while the invoices are loading or have failed, Remind gives no reason that reads as nothing owed", async ({ page, request }) => {
  await openDesk(page, request);
  await page.route("**/api/v1/ar/register?*customer_id=*", (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "boom" }) }));
  await page.locator("[data-register-row]", { hasText: "INV-1002" }).getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-invoices-failed]")).toBeVisible();
  await expect(page.getByRole("button", { name: "Remind", exact: true })).toBeDisabled();
  await expect(page.getByText("Nothing on this account is overdue.")).toHaveCount(0);
});
