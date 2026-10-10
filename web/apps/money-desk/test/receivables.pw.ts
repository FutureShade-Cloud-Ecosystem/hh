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
  expect(await page.locator("[data-card] [data-card-title]").allTextContents()).toEqual(["Escalation", "Late", "Overdue", "Current"]);
  await expect(page.locator("[data-register-row]")).toHaveCount(9);
  const first = page.locator("[data-register-row]").first();
  await expect(first).toContainText("INV-1001");
  await expect(first).toContainText("104d overdue");
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
  await pick("Overdue");
  await expect(page.locator("[data-register-row]")).toHaveCount(7);
  await pick("Promised");
  await expect(page.locator("[data-register-row]")).toHaveCount(1);
  await pick("Disputed");
  await expect(page.locator("[data-register-row]")).toHaveCount(1);
  await expect(page.locator("[data-register-row]")).toContainText("Open dispute");
  await pick("Paid");
  await expect(page.locator("[data-register-row]")).toHaveCount(1);
  await expect(page.locator("[data-register-row]")).toContainText("INV-1010");
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

test("a lost answer then a changed cheque number sends the same key; the route's refusal reads already recorded and the account reloads", async ({ page, request }) => {
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
  // the first press landed, the route refuses the changed body in words; the desk reads that refusal
  // as the payment already being recorded, says so, and reloads the account's balances.
  await page.locator("[data-field='receive-reference']").fill("Cheque 4471");
  await page.getByRole("button", { name: "Record payment" }).click();
  expect(posts).toHaveLength(2);
  expect(posts[1]?.idempotency_key).toBe(posts[0]?.idempotency_key);
  await expect(page.locator("[data-notice]")).toContainText("already recorded by the earlier press");
  await expect(page.locator("[data-error]")).toHaveCount(0);
  await expect(page.locator("[data-invoice-row]", { hasText: "INV-1001" })).toContainText("$4,325.00");
  await expect(page.locator("[data-notice]")).not.toContainText("PAY-0102");
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

test("a reload that shows the payment ends the held key, so pressing again with the same body is a new payment", async ({ page, request }) => {
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
  const sameBody = async (): Promise<void> => {
    await openAccount(page, "Granite Edge Landscaping (demo)");
    await page.getByRole("button", { name: "Clear" }).first().click();
    await page.getByRole("checkbox", { name: "Pay INV-1002" }).click();
    await page.locator("[data-field='receive-amount']").fill("100.00");
    await page.locator("[data-field='receive-reference']").fill("Cheque 4471");
  };
  await openDesk(page, request);
  await sameBody();
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-error]")).toContainText("may or may not have been recorded");
  // The check the words ask for: the account is reopened and the invoice's payments show the payment,
  // so the outcome is known and the held key ends.
  await page.getByRole("button", { name: "‹ Receivables" }).click();
  await openAccount(page, "Granite Edge Landscaping (demo)");
  await page.locator("[data-invoice-row]", { hasText: "INV-1002" }).getByRole("button", { name: "INV-1002" }).click();
  await expect(page.locator("[data-payment-row]", { hasText: "PAY-0101" })).toContainText("Received");
  // The same body pressed again is now a new payment with its own key, and it posts as its own.
  await page.getByRole("button", { name: "Back to payment" }).click();
  await page.getByRole("button", { name: "Clear" }).click();
  await page.getByRole("checkbox", { name: "Pay INV-1002" }).click();
  await page.locator("[data-field='receive-amount']").fill("100.00");
  await page.locator("[data-field='receive-reference']").fill("Cheque 4471");
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("PAY-0102 applied: $100.00 on INV-1002");
  expect(posts).toHaveLength(2);
  expect(posts[1]?.idempotency_key).not.toBe(posts[0]?.idempotency_key);
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
  await expect(page.locator("[data-hold-panel]")).toContainText("no route for it yet");
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
  await expect(page.locator("[data-statement]")).toContainText("1 to 30");
  await page.getByRole("button", { name: "Send to the AP contact" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Statement queued to the AP contact");
  await page.getByRole("button", { name: /Download PDF/ }).click({ force: true });
  await expect(page.locator("[data-error]")).toContainText("later");
  await expect(page.locator(".tab-later", { hasText: "Activity" })).toContainText("later");
  await expect(page.locator(".tab-later", { hasText: "Contacts" })).toContainText("later");
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
  await page.getByRole("button", { name: "Receivables", exact: true }).click();
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
