// Pro Pay in a real browser against the shared tenant stub. Visual references: the sign in screen is
// HH: Pro shell-signin-phone-brand; the frame is shell-frame-phone-light-r2 and shell-selector-phone-light; Pay home
// (the balance, the aging tiles, select to pay, the sticky Pay bar) is the live capture app-phone-05-pay and
// app-desktop-04-pay; the pay sheet (methods with the fee on each before choosing, Add a card, the gold Pay button)
// is the board pay-hosted-phone-light. Phone first (390 wide), then desktop (1440).
//
// The money legs: a pay with a stored method and its receipt, the double press (one request), the unknown outcome (the
// response is lost after the stub settled it: the same key and body on Check payment, still one payment), a decline
// through the hosted frame, and the card sweep (no input in the app, the frame sandboxed with an opaque origin, the
// digits typed in the frame on no request the page makes).
import { test, expect, type Page } from "@playwright/test";

const PASSWORD = "stub-password";
const EMAIL = (who: string): string => `contractor.${who}@hh-demo.invalid`;
const CARD_DECLINE = "4000000000000002";
const CARD_OK = "4111111111111111";

test.beforeEach(async ({ request }) => {
  const response = await request.post("/_stub/reset");
  expect(response.ok()).toBe(true);
});

async function signIn(page: Page, who: string, password = PASSWORD): Promise<void> {
  await page.goto("/");
  await page.getByTestId("signin-email").fill(EMAIL(who));
  await page.getByTestId("signin-password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
}

/** Sign in again on the same page, with no reload: what a person does after a lapse, with the app's memory intact. */
async function signInAgain(page: Page, who: string): Promise<void> {
  await page.getByTestId("signin-email").fill(EMAIL(who));
  await page.getByTestId("signin-password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
}

/** The dealer's day (the house zone) a number of days from now, as YYYY-MM-DD: the day the app ages against. */
function houseDayOffset(days: number): string {
  const parts = new Intl.DateTimeFormat("en-US", { timeZone: "America/Toronto", year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(new Date());
  const of = (type: string): string => parts.find((p) => p.type === type)?.value ?? "";
  const when = new Date(`${of("year")}-${of("month")}-${of("day")}T00:00:00Z`);
  when.setUTCDate(when.getUTCDate() + days);
  return when.toISOString().slice(0, 10);
}

async function shot(page: Page, name: string): Promise<void> {
  const dir = process.env.PAY_SHOTS;
  await page.screenshot({ path: dir ? `${dir}/${name}.png` : test.info().outputPath(`${name}.png`) });
}

/** The payments the stub holds, read with a bearer of the same persona (the test's own view of the books). */
async function paymentsHeld(page: Page, who: string): Promise<{ payment_no: string; amount_cents: number }[]> {
  const session = await page.request.post("/_stub/session", { data: { email: EMAIL(who), password: PASSWORD } });
  const { token } = (await session.json()) as { token: string };
  const list = await page.request.get("/api/v1/tenant/payments", { headers: { Authorization: `Bearer ${token}` } });
  return (await list.json()) as { payment_no: string; amount_cents: number }[];
}

async function pickAllAndOpenSheet(page: Page): Promise<void> {
  await page.getByRole("button", { name: /Select all/ }).click();
  await page.getByTestId("pay-open").click();
  await expect(page.getByTestId("pay-sheet")).toBeVisible();
  await expect(page.getByTestId("method-option").first()).toBeVisible();
}

async function useCardInFrame(page: Page, digits: string): Promise<void> {
  const frame = page.frameLocator('[data-testid="card-frame"]');
  await frame.locator("#number").fill(digits);
  await frame.locator("#code").fill("123");
  await frame.getByRole("button", { name: "Use this card" }).click();
}

test.describe("phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the sign in screen reads like a contractor's, with the dealer's name and nothing technical", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "HH Pro" })).toBeVisible();
    await expect(page.locator("hh-pro-signin")).toContainText("Gable Landscape Supply");
    const text = (await page.locator("hh-pro-signin").innerText()).toLowerCase();
    for (const word of ["console", "core", "token", "session", "exchange", "stub", "demo"]) expect(text).not.toContain(word);
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
  });

  test("a wrong password is refused in plain words and the person stays on the sign in screen", async ({ page }) => {
    await signIn(page, "owner", "not-the-password");
    await expect(page.getByTestId("signin-message")).toHaveText("That email and password do not match.");
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
  });

  test("Pay home shows what is owed, the aging tiles and the invoices, in dollars", async ({ page }) => {
    await signIn(page, "ap");
    await expect(page.getByTestId("surface-title")).toHaveText("Invoices");
    await expect(page.getByTestId("frame-dealer")).toHaveText("Gable Landscape Supply");
    await expect(page.getByTestId("owed-total")).toContainText("$");
    await expect(page.getByTestId("aging").locator("li")).toHaveCount(4);
    await expect(page.getByTestId("invoice-row").first()).toContainText("INV-");
    const text = await page.locator("hh-pro-frame").innerText();
    expect(text).not.toMatch(/CAD|undefined|NaN|null/);
    await shot(page, "pay-home-phone");
  });

  test("an overdue invoice shows its days late and the overdue line, filed in the right tile", async ({ page }) => {
    await page.route("**/api/v1/tenant/invoices?*", async (route) => {
      const response = await route.fetch();
      const page_ = (await response.json()) as { items: { invoice_no: string; due_date: string }[] };
      for (const item of page_.items) if (item.invoice_no === "INV-20402") item.due_date = houseDayOffset(-12);
      await route.fulfill({ response, json: page_ });
    });
    await signIn(page, "ap");
    await expect(page.getByTestId("owed-overdue")).toContainText("overdue across 1 invoice");
    await expect(page.getByTestId("invoice-row").filter({ hasText: "INV-20402" })).toContainText("12 days late");
    await expect(page.getByTestId("aging").locator('[data-bucket="1-30"]')).not.toContainText("None");
    await shot(page, "pay-home-overdue-phone");
  });

  test("select to pay counts the pick, the bar shows the sum, and Clear takes it back", async ({ page }) => {
    await signIn(page, "ap");
    await expect(page.getByTestId("pay-open")).toBeDisabled();
    await expect(page.getByTestId("picked-count")).toHaveText("0 of 2 selected");
    await page.getByTestId("invoice-row").filter({ hasText: "INV-20402" }).getByRole("checkbox").click();
    await expect(page.getByTestId("picked-count")).toHaveText("1 of 2 selected");
    await expect(page.getByTestId("pay-open")).toContainText("Pay 1 invoice");
    await page.getByRole("button", { name: "Select all 2" }).click();
    await expect(page.getByTestId("pay-open")).toContainText("Pay 2 invoices");
    await page.getByRole("button", { name: "Clear" }).click();
    await expect(page.getByTestId("pay-open")).toBeDisabled();
    await shot(page, "pay-select-phone");
  });

  test("Select all and each payable invoice toggle are at least 44 high, and tapping the row text checks the invoice", async ({ page }) => {
    await signIn(page, "ap");
    const selectAll = page.getByRole("button", { name: "Select all 2" });
    await expect(selectAll).toBeVisible();
    await expect.poll(async () => (await selectAll.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(44);
    for (const no of ["INV-20403", "INV-20402"]) {
      const row = page.getByTestId("invoice-row").filter({ hasText: no });
      const box = row.getByRole("checkbox");
      await expect.poll(async () => (await box.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(44);
      await expect.poll(async () => (await box.boundingBox())?.width ?? 0).toBeGreaterThanOrEqual(44);
      await row.locator(".inv-sub").click();
      await expect(box).toHaveAttribute("aria-checked", "true");
    }
    await expect(page.getByTestId("picked-count")).toHaveText("2 of 2 selected");
  });

  test("keyboard focus shows a ring on the invoice checkbox and on the invoice number", async ({ page }) => {
    await signIn(page, "ap");
    await page.getByRole("button", { name: "Select all 2" }).focus();
    await page.keyboard.press("Tab");
    const hit = page.getByTestId("invoice-row").first().getByRole("checkbox");
    await expect(hit).toBeFocused();
    await expect.poll(() => hit.locator(".box").evaluate((el) => getComputedStyle(el).outlineStyle)).not.toBe("none");
    await page.keyboard.press("Tab");
    const open = page.getByTestId("invoice-row").first().locator(".inv-open");
    await expect(open).toBeFocused();
    await expect.poll(() => open.evaluate((el) => getComputedStyle(el).outlineStyle)).not.toBe("none");
  });

  test("the invoice number is underlined so it reads as the way into the statement", async ({ page }) => {
    await signIn(page, "ap");
    const open = page.getByTestId("invoice-row").first().locator(".inv-open");
    await expect.poll(() => open.evaluate((el) => getComputedStyle(el).textDecorationLine)).toContain("underline");
  });

  test("the statement button is named for what it does, and the selected count is a polite live region", async ({ page }) => {
    await signIn(page, "ap");
    await expect(page.getByRole("button", { name: "Open statement for INV-20403" })).toBeVisible();
    await expect(page.getByTestId("picked-count")).toHaveAttribute("aria-live", "polite");
  });

  test("the sheet shows the fee on every method before one is chosen, then a stored method pays and the receipt is the core's", async ({ page }) => {
    await signIn(page, "ap");
    await page.getByTestId("invoice-row").filter({ hasText: "INV-20402" }).getByRole("checkbox").click();
    await page.getByTestId("pay-open").click();
    const methods = page.getByTestId("method-option");
    await expect(methods).toHaveCount(2);
    for (let i = 0; i < 2; i += 1) await expect(methods.nth(i)).toContainText("No fee");
    await expect(page.getByTestId("method-new")).toContainText("No fee");
    await expect(page.getByTestId("sandbox-note")).toHaveText("Sandbox payments. No money moves.");
    await expect(methods.first()).toHaveAttribute("aria-checked", "true");
    await shot(page, "pay-sheet-phone");
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("receipt")).toBeVisible();
    await expect(page.getByTestId("receipt-no")).toHaveText(/^PMT-/);
    await expect(page.getByTestId("receipt-differs")).toHaveCount(0);
    await shot(page, "pay-receipt-phone");
    await page.getByTestId("receipt-done").click();
    await expect(page.getByTestId("pay-sheet")).toHaveCount(0);
    await expect(page.getByTestId("invoice-row").filter({ hasText: "INV-20402" })).toContainText("Paid");
    expect((await paymentsHeld(page, "ap")).length).toBe(3);
  });

  test("closing a paid sheet by the scrim is Done: the invoices reload and show the paid ones as paid", async ({ page }) => {
    await signIn(page, "ap");
    await page.getByTestId("invoice-row").filter({ hasText: "INV-20402" }).getByRole("checkbox").click();
    await page.getByTestId("pay-open").click();
    await expect(page.getByTestId("method-option").first()).toBeVisible();
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("receipt")).toBeVisible();
    await page.mouse.click(5, 5);
    await expect(page.getByTestId("pay-sheet")).toHaveCount(0);
    await expect(page.getByTestId("invoice-row").filter({ hasText: "INV-20402" })).toContainText("Paid");
  });

  test("a double press is one request and one payment", async ({ page, request }) => {
    const posts: string[] = [];
    page.on("request", (r) => {
      if (r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/tenant/payments") posts.push(r.postData() ?? "");
    });
    await signIn(page, "ap");
    await pickAllAndOpenSheet(page);
    await request.post("/_stub/control", { data: { delayMs: 700 } });
    await page.getByTestId("pay-confirm").dblclick();
    await expect(page.getByTestId("receipt")).toBeVisible();
    expect(posts.length).toBe(1);
    expect((await paymentsHeld(page, "ap")).length).toBe(3);
  });

  test("an answer lost after the charge is unknown: Check payment resends the same key and body and there is still one payment", async ({ page }) => {
    const posts: { idempotency_key: string; invoice_ids: string[]; payment_method_id?: string; nonce?: string }[] = [];
    await page.route("**/api/v1/tenant/payments", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      posts.push(JSON.parse(route.request().postData() ?? "{}"));
      if (posts.length === 1) {
        await route.fetch();
        return route.abort("connectionreset");
      }
      return route.continue();
    });
    await signIn(page, "ap");
    await page.getByTestId("invoice-row").filter({ hasText: "INV-20402" }).getByRole("checkbox").click();
    await page.getByTestId("pay-open").click();
    await expect(page.getByTestId("method-option").first()).toBeVisible();
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("sheet-words")).toContainText("It may have gone through");
    await expect(page.getByTestId("check-payment")).toBeVisible();
    await expect(page.getByTestId("pay-confirm")).toHaveCount(0);
    await expect(page.getByTestId("receipt")).toHaveCount(0);
    await shot(page, "pay-unknown-phone");
    // The choices are locked while the answer is unknown.
    await expect(page.getByTestId("method-option").first()).toBeDisabled();
    await page.getByTestId("check-payment").click();
    await expect(page.getByTestId("receipt")).toBeVisible();
    expect(posts.length).toBe(2);
    expect(posts[1]).toEqual(posts[0]);
    expect(posts[0].idempotency_key.length).toBeGreaterThanOrEqual(16);
    expect((await paymentsHeld(page, "ap")).length).toBe(3);
  });

  test("a 400 that reads balance changed after a charge is unknown: held, and Check payment is the same request", async ({ page }) => {
    const posts: string[] = [];
    await page.route("**/api/v1/tenant/payments", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      posts.push(route.request().postData() ?? "");
      if (posts.length === 1) {
        await route.fetch();
        return route.fulfill({ status: 400, json: { error: 'invoice "x" balance changed; refresh and retry' } });
      }
      return route.continue();
    });
    await signIn(page, "ap");
    await page.getByTestId("invoice-row").filter({ hasText: "INV-20402" }).getByRole("checkbox").click();
    await page.getByTestId("pay-open").click();
    await expect(page.getByTestId("method-option").first()).toBeVisible();
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("sheet-words")).toContainText("It may have gone through");
    await expect(page.getByTestId("sheet-words")).not.toContainText("No money moved");
    await expect(page.getByTestId("check-payment")).toBeVisible();
    await page.getByTestId("check-payment").click();
    await expect(page.getByTestId("receipt")).toBeVisible();
    expect(posts.length).toBe(2);
    expect(posts[1]).toBe(posts[0]);
    expect((await paymentsHeld(page, "ap")).length).toBe(3);
  });

  test("any other 400 or a 404 on a pay says to check Statements, never that no money moved, and reloads the invoices", async ({ page }) => {
    let listed = 0;
    await page.route("**/api/v1/tenant/invoices?*", (route) => {
      listed += 1;
      return route.continue();
    });
    await page.route("**/api/v1/tenant/payments", (route) => (route.request().method() === "POST" ? route.fulfill({ status: 404, json: { error: "not found" } }) : route.continue()));
    await signIn(page, "ap");
    await pickAllAndOpenSheet(page);
    const before = listed;
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("sheet-words")).toContainText("Check Statements");
    await expect(page.getByTestId("sheet-words")).not.toContainText("No money moved");
    await expect.poll(() => listed).toBeGreaterThan(before);
  });

  test("closing the sheet on an unknown outcome leaves a notice, and Check payment from it is the same request", async ({ page }) => {
    const posts: string[] = [];
    await page.route("**/api/v1/tenant/payments", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      posts.push(route.request().postData() ?? "");
      if (posts.length === 1) {
        await route.fetch();
        return route.abort("connectionreset");
      }
      return route.continue();
    });
    await signIn(page, "ap");
    await pickAllAndOpenSheet(page);
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("check-payment")).toBeVisible();
    await page.getByTestId("sheet-close").click();
    await expect(page.getByTestId("waiting-notice")).toBeVisible();
    // A different pick cannot start another payment while this one waits: the sheet comes back as the held one.
    await page.getByTestId("pay-open").click();
    await expect(page.getByTestId("check-payment")).toBeVisible();
    await page.getByTestId("check-payment").click();
    await expect(page.getByTestId("receipt")).toBeVisible();
    expect(posts[1]).toBe(posts[0]);
    expect((await paymentsHeld(page, "ap")).length).toBe(3);
  });

  test("a held attempt survives a sign out: the same person signs back in and Check payment is the same request", async ({ page }) => {
    const posts: string[] = [];
    await page.route("**/api/v1/tenant/payments", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      posts.push(route.request().postData() ?? "");
      if (posts.length === 1) {
        await route.fetch();
        return route.abort("connectionreset");
      }
      if (posts.length === 2) return route.fulfill({ status: 401, json: { error: "unauthorized" } });
      return route.continue();
    });
    await signIn(page, "ap");
    await pickAllAndOpenSheet(page);
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("check-payment")).toBeVisible();
    await page.getByTestId("check-payment").click();
    await expect(page.getByTestId("signin-message")).toHaveText("You are signed out. Sign in again.");
    await signInAgain(page, "ap");
    await expect(page.getByTestId("waiting-notice")).toBeVisible();
    await page.getByRole("button", { name: "Check payment" }).click();
    await expect(page.getByTestId("check-payment")).toBeVisible();
    await page.getByTestId("check-payment").click();
    await expect(page.getByTestId("receipt")).toBeVisible();
    expect(posts.length).toBe(3);
    expect(posts[2]).toBe(posts[0]);
    expect((await paymentsHeld(page, "ap")).length).toBe(3);
  });

  test("a different person signing in does not see the first person's waiting payment", async ({ page }) => {
    const posts: string[] = [];
    await page.route("**/api/v1/tenant/payments", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      posts.push(route.request().postData() ?? "");
      if (posts.length === 1) {
        await route.fetch();
        return route.abort("connectionreset");
      }
      if (posts.length === 2) return route.fulfill({ status: 401, json: { error: "unauthorized" } });
      return route.continue();
    });
    await signIn(page, "ap");
    await pickAllAndOpenSheet(page);
    await page.getByTestId("pay-confirm").click();
    await page.getByTestId("check-payment").click();
    await expect(page.getByTestId("signin-message")).toHaveText("You are signed out. Sign in again.");
    await signInAgain(page, "owner");
    await expect(page.getByTestId("invoice-row").first()).toBeVisible();
    await expect(page.getByTestId("waiting-notice")).toHaveCount(0);
  });

  test("a card added in the frame that is declined moves no money and says so", async ({ page }) => {
    await signIn(page, "ap");
    await pickAllAndOpenSheet(page);
    await page.getByTestId("method-new").click();
    await expect(page.getByTestId("pay-confirm")).toBeDisabled();
    await useCardInFrame(page, CARD_DECLINE);
    await expect(page.getByTestId("card-ready")).toBeVisible();
    await shot(page, "pay-add-card-phone");
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("sheet-words")).toHaveText("The payment was declined. No money moved.");
    await expect(page.getByTestId("receipt")).toHaveCount(0);
    expect((await paymentsHeld(page, "ap")).length).toBe(2);
  });

  test("the card digits never reach the app: no input in the app, a sandboxed frame, and only a token on the wire", async ({ page }) => {
    const sent: string[] = [];
    page.on("request", (r) => sent.push(`${r.method()} ${r.url()} ${r.postData() ?? ""}`));
    await signIn(page, "ap");
    await expect(page.getByTestId("invoice-row").first()).toBeVisible();
    await expect(page.locator("input, textarea, [contenteditable]")).toHaveCount(0);
    await pickAllAndOpenSheet(page);
    await page.getByTestId("method-new").click();
    const frame = page.getByTestId("card-frame");
    await expect(frame).toBeVisible();
    const flags = (await frame.getAttribute("sandbox")) ?? "";
    expect(flags).toContain("allow-scripts");
    expect(flags).not.toContain("allow-same-origin");
    await useCardInFrame(page, CARD_OK);
    await expect(page.getByTestId("card-ready")).toBeVisible();
    await expect(page.locator("input, textarea, [contenteditable]")).toHaveCount(0);
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("receipt")).toBeVisible();
    const wire = sent.join("\n");
    expect(wire).not.toContain(CARD_OK);
    expect(wire).not.toContain("4111");
    const paid = sent.find((line) => line.startsWith("POST") && line.includes("/tenant/payments"));
    expect(paid).toMatch(/"nonce":"stub-nonce-/);
    const dom = await page.locator("hh-pro-frame").innerHTML();
    expect(dom).not.toContain(CARD_OK);
  });

  test("a role that cannot pay reads the invoices with no way to pick or pay", async ({ page }) => {
    await signIn(page, "pm");
    await expect(page.getByTestId("invoice-row").first()).toBeVisible();
    await expect(page.getByTestId("read-only")).toBeVisible();
    await expect(page.getByRole("checkbox")).toHaveCount(0);
    await expect(page.getByTestId("pay-open")).toHaveCount(0);
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    await expect(panel.getByRole("button", { name: "Payment methods" })).toHaveCount(0);
  });

  test("a lapsed session on the landing signs the person out with a plain message, not a role refusal", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/invoices", status: 401 } } });
    await signIn(page, "ap");
    await expect(page.getByTestId("signin-message")).toHaveText("You are signed out. Sign in again.");
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
    await expect(page.getByTestId("landing-state")).toHaveCount(0);
  });

  test("a session that lapses while paying signs the person out and no payment is recorded", async ({ page, request }) => {
    await signIn(page, "ap");
    await pickAllAndOpenSheet(page);
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/payments", status: 401 } } });
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("signin-message")).toHaveText("You are signed out. Sign in again.");
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
    await request.post("/_stub/reset");
    expect((await paymentsHeld(page, "ap")).length).toBe(2);
  });

  test("a surface in another app says it opens that app and asks to sign in again, one in this app says nothing", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    const elsewhere = panel.locator("button[data-app]:not([data-app='pay'])");
    expect(await elsewhere.count()).toBeGreaterThan(0);
    await expect(panel.locator("button[data-app='pay'] [data-testid='opens-note']")).toHaveCount(0);
    await expect(elsewhere.first().getByTestId("opens-note")).toContainText("sign in again");
  });

  test("a role without billing sees the refusal state and no call is made for it", async ({ page }) => {
    const calls: string[] = [];
    page.on("request", (r) => calls.push(new URL(r.url()).pathname));
    await signIn(page, "field");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    await expect(page.getByTestId("landing-state")).toHaveAttribute("data-state", "refusal");
    expect(calls).not.toContain("/api/v1/tenant/invoices");
  });

  test("the landing shows a loading state, an empty state in this app's words, an error with Try again and a refusal with no retry", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { delayMs: 900 } });
    await signIn(page, "ap");
    await expect(page.getByTestId("landing-state")).toHaveAttribute("data-state", "loading");
    await expect(page.getByTestId("landing-rows")).toBeVisible();
    await request.post("/_stub/reset");
    await request.post("/_stub/control", { data: { empty: "invoices" } });
    await page.getByTestId("frame-sign-out").click();
    await signIn(page, "ap");
    await expect(page.getByTestId("landing-state")).toContainText("No invoices yet");
    await request.post("/_stub/reset");
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/invoices", status: 503 } } });
    await page.getByTestId("frame-sign-out").click();
    await signIn(page, "ap");
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "error");
    await expect(state).toContainText("Nothing was changed.");
    await request.post("/_stub/reset");
    await state.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByTestId("landing-rows")).toBeVisible();
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/invoices", status: 403 } } });
    await page.getByTestId("frame-sign-out").click();
    await signIn(page, "ap");
    await expect(page.getByTestId("landing-state")).toHaveAttribute("data-state", "refusal");
    await expect(page.getByTestId("landing-state").getByRole("button", { name: "Try again" })).toHaveCount(0);
  });

  test("a slow session check and slow data call keep the signed in frame with the loading pattern, never the sign in page", async ({ page }) => {
    await page.route("**/api/v1/tenant/**", async (route) => {
      if (route.request().url().endsWith("/tenant/config")) return route.continue();
      await new Promise((resolve) => setTimeout(resolve, 1800));
      return route.continue();
    });
    await signIn(page, "ap");
    await expect(page.locator("hh-pro-frame")).toBeVisible({ timeout: 600 });
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "loading");
    await expect(state.locator('.state-indicator[data-pattern="progress"]')).toBeVisible();
    await shot(page, "slow-390x844-light");
    await expect(page.getByTestId("invoice-row").first()).toContainText("INV-", { timeout: 6000 });
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
  });

  test("a network failure on the session check or data call keeps the person in the frame with not reachable words and Try again", async ({ page }) => {
    let failMe = true;
    await page.route("**/api/v1/tenant/me", async (route) => {
      if (failMe) return route.abort("failed");
      return route.continue();
    });
    await signIn(page, "ap");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "error");
    await expect(state).toContainText("Pro is not reachable right now. Try again in a moment.");
    await shot(page, "unreachable-390x844-light");
    failMe = false;
    await state.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByTestId("invoice-row").first()).toContainText("INV-");
  });

  test("an invoice opens to its statement: lines, the core's tax, the balance and the payments on it", async ({ page }) => {
    await signIn(page, "ap");
    await page.getByTestId("invoice-row").filter({ hasText: "INV-20403" }).getByRole("button", { name: /INV-20403/ }).click();
    await expect(page.getByTestId("detail-no")).toHaveText("INV-20403");
    await expect(page.getByTestId("detail-lines").locator("li").first()).toContainText("$");
    await expect(page.getByTestId("detail-sums")).toContainText("Tax");
    await expect(page.getByTestId("detail-balance")).toContainText("$");
    await shot(page, "pay-invoice-detail-phone");
    await expect(page.getByTestId("pay-this")).toContainText("Pay $");
    await page.getByTestId("invoice-back").click();
    await expect(page.getByTestId("invoice-row").first()).toBeVisible();
  });

  test("Payment methods lists the stored methods, adds one from the frame, makes it default and removes with a second press", async ({ page }) => {
    await signIn(page, "ap");
    await page.getByTestId("frame-selector").click();
    await page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" }).getByRole("button", { name: "Payment methods" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Payment methods");
    await expect(page.getByTestId("method-row")).toHaveCount(2);
    await expect(page.getByTestId("default-chip")).toHaveCount(1);
    await shot(page, "pay-methods-phone");
    await page.getByTestId("open-add").click();
    await expect(page.getByTestId("save-method")).toBeDisabled();
    await useCardInFrame(page, CARD_OK);
    await expect(page.getByTestId("card-ready")).toBeVisible();
    await page.getByTestId("save-method").click();
    await expect(page.getByTestId("method-row")).toHaveCount(3);
    await page.getByTestId("method-row").nth(1).getByRole("button", { name: "Make default" }).click();
    await expect(page.getByTestId("default-chip")).toHaveCount(1);
    await page.getByTestId("method-row").last().getByTestId("remove-method").click();
    await expect(page.getByTestId("confirm-remove")).toBeVisible();
    await page.getByRole("button", { name: "Keep" }).click();
    await expect(page.getByTestId("method-row")).toHaveCount(3);
    await page.getByTestId("method-row").last().getByTestId("remove-method").click();
    await page.getByTestId("confirm-remove").click();
    await expect(page.getByTestId("method-row")).toHaveCount(2);
  });

  test("Statements lists the payments with what each settled", async ({ page }) => {
    await signIn(page, "ap");
    await page.getByTestId("frame-selector").click();
    await page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" }).getByRole("button", { name: "Statements" }).click();
    await expect(page.getByTestId("paid-total")).toContainText("$");
    await expect(page.getByTestId("payment-row")).toHaveCount(2);
    await expect(page.getByTestId("payment-row").first()).toContainText("Applied to INV-");
    await shot(page, "pay-statements-phone");
  });

  test("a bounced or voided payment's line names each invoice once with no status word", async ({ page }) => {
    // The history (PR 668) carries the application row and the reversal beside it: the row names
    // each invoice once, from the application rows. The chip carries the state, so the line
    // carries the facts only, no status word, never "Applied to".
    let invoiceNos: string[] = [];
    await page.route("**/api/v1/tenant/payments", async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      const response = await route.fetch();
      const payments = (await response.json()) as { payment_no: string }[];
      const auth = route.request().headers()["authorization"] ?? "";
      const invoices = await page.request.get("/api/v1/tenant/invoices?limit=200", { headers: { authorization: auth } });
      const body = (await invoices.json()) as { items: { id: string; customer_id: string; invoice_no: string }[] };
      const [inv1, inv2, inv3] = body.items;
      invoiceNos = body.items.map((i) => i.invoice_no);
      await route.fulfill({
        json: [
          ...payments,
          {
            id: "pmt-9101",
            payment_no: "PMT-9101",
            customer_id: inv1!.customer_id,
            method: "CHEQUE",
            amount_cents: 80_000,
            status: "BOUNCED",
            gateway_ref: "",
            received_at: "2026-03-03T13:00:00.000Z",
            allocations: [
              { invoice_id: inv1!.id, amount_cents: 50_000, created_at: "2026-03-03T13:00:00.000Z" },
              { invoice_id: inv2!.id, amount_cents: 30_000, created_at: "2026-03-03T13:00:00.000Z" },
              { invoice_id: inv1!.id, amount_cents: -50_000, created_at: "2026-03-04T09:00:00.000Z" },
              { invoice_id: inv2!.id, amount_cents: -30_000, created_at: "2026-03-04T09:00:00.000Z" },
            ],
          },
          {
            id: "pmt-9102",
            payment_no: "PMT-9102",
            customer_id: inv1!.customer_id,
            method: "CHEQUE",
            amount_cents: 25_000,
            status: "VOID",
            gateway_ref: "",
            received_at: "2026-03-02T13:00:00.000Z",
            allocations: [
              { invoice_id: inv3!.id, amount_cents: 25_000, created_at: "2026-03-02T13:00:00.000Z" },
              { invoice_id: inv3!.id, amount_cents: -25_000, created_at: "2026-03-02T14:00:00.000Z" },
            ],
          },
        ],
      });
    });
    await signIn(page, "ap");
    await page.getByTestId("frame-selector").click();
    await page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" }).getByRole("button", { name: "Statements" }).click();
    await expect(page.getByTestId("payment-row")).toHaveCount(4);
    // The standing payment is unchanged.
    await expect(page.getByTestId("payment-row").filter({ hasText: "PMT-9001" })).toContainText("Applied to INV-");
    // The bounced cheque's line names each invoice once, with no status word: the chip says Bounced.
    const bounced = page.getByTestId("payment-row").filter({ hasText: "PMT-9101" });
    await expect(bounced).toContainText("8:00 AM · INV-20403, INV-20402");
    await expect(bounced).not.toContainText("Bounced ·");
    await expect(bounced).not.toContainText("Applied to");
    const bouncedText = await bounced.innerText();
    for (const no of [invoiceNos[0]!, invoiceNos[1]!]) {
      expect(bouncedText.split(no).length - 1, `${no} named once in ${bouncedText}`).toBe(1);
    }
    // The voided receipt's line names its invoice once, with no status word: the chip says Void.
    const voided = page.getByTestId("payment-row").filter({ hasText: "PMT-9102" });
    await expect(voided).toContainText(`8:00 AM · ${invoiceNos[2]}`);
    await expect(voided).not.toContainText("Void ·");
    await expect(voided).not.toContainText("Applied to");
    const voidedText = await voided.innerText();
    expect(voidedText.split(invoiceNos[2]!).length - 1, `${invoiceNos[2]} named once in ${voidedText}`).toBe(1);
  });

  test("invoices, statement detail, the pay receipt and statements show the year on every date and glossary words for unknown figures", async ({ page }) => {
    await signIn(page, "ap");
    await expect(page.getByTestId("invoice-row").filter({ hasText: "INV-20401" })).toContainText(/Issued [A-Z][a-z]{2} \d{1,2}, \d{4}/);
    await expect(page.getByTestId("invoice-row").filter({ hasText: "INV-20403" })).toContainText(/Due [A-Z][a-z]{2} \d{1,2}, \d{4}/);
    await page.getByTestId("invoice-row").filter({ hasText: "INV-20401" }).getByRole("button", { name: /INV-20401/ }).click();
    await expect(page.getByTestId("invoice-detail")).toContainText(/Issued [A-Z][a-z]{2} \d{1,2}, \d{4} · Due [A-Z][a-z]{2} \d{1,2}, \d{4}/);
    await expect(page.getByTestId("detail-history")).toContainText(/[A-Z][a-z]{2} \d{1,2}, \d{4}/);
    await page.getByTestId("invoice-back").click();
    await page.getByTestId("invoice-row").filter({ hasText: "INV-20402" }).getByRole("button", { name: /INV-20402/ }).click();
    await page.getByTestId("pay-this").click();
    await expect(page.getByTestId("method-option").first()).toBeVisible();
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("receipt")).toContainText(/[A-Z][a-z]{2} \d{1,2}, \d{4}/);
    await page.getByTestId("receipt-done").click();
    await page.getByTestId("frame-selector").click();
    await page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" }).getByRole("button", { name: "Statements" }).click();
    await expect(page.getByTestId("payment-row").first()).toContainText(/[A-Z][a-z]{2} \d{1,2}, \d{4}/);
  });

  test("unknown money, quantity and day values on the wire read Not priced, Not counted and Not set, never n/a or a zero", async ({ page }) => {
    await page.route("**/api/v1/tenant/billing/summary", (route) => route.fulfill({ status: 503, json: { error: "unavailable" } }));
    await page.route("**/api/v1/tenant/invoices/*", async (route) => {
      const response = await route.fetch();
      const detail = (await response.json()) as {
        issue_date: string;
        tax_cents: number | null;
        lines: { qty_entered: string; line_total_cents: number | null }[];
        payment_history: { received_at: string }[];
      };
      detail.issue_date = "";
      detail.tax_cents = null;
      if (detail.lines.length > 0) {
        detail.lines[0]!.qty_entered = "";
        detail.lines[0]!.line_total_cents = null;
      }
      if (detail.payment_history.length > 0) detail.payment_history[0]!.received_at = "";
      await route.fulfill({ response, json: detail });
    });
    await signIn(page, "ap");
    await expect(page.getByTestId("owed-total")).toHaveText("Not priced");
    await page.getByTestId("invoice-row").filter({ hasText: "INV-20401" }).getByRole("button", { name: /INV-20401/ }).click();
    await expect(page.getByTestId("invoice-detail")).toContainText("Issued Not set");
    await expect(page.getByTestId("detail-lines").locator("li").first()).toContainText("Not counted");
    await expect(page.getByTestId("detail-lines").locator("li").first()).toContainText("Not priced");
    await expect(page.getByTestId("detail-sums")).toContainText("Not priced");
    await expect(page.getByTestId("detail-history")).toContainText("Not set");
    const text = await page.locator("hh-pro-frame").innerText();
    expect(text).not.toMatch(/\bn\/a\b/i);
  });

  test("a moment on a Pro screen reads in the shared default zone when nothing is served, and in America/Los_Angeles when served on tenant config", async ({ page }) => {
    const boundary = new Date();
    boundary.setUTCHours(5, 30, 0, 0);
    const defaultDay = new Intl.DateTimeFormat("en-US", { timeZone: "America/Toronto", month: "short", day: "numeric", year: "numeric" }).format(boundary);
    const pacificDay = new Intl.DateTimeFormat("en-US", { timeZone: "America/Los_Angeles", month: "short", day: "numeric", year: "numeric" }).format(boundary);
    expect(pacificDay).not.toBe(defaultDay);
    await page.route("**/api/v1/tenant/invoices/*", async (route) => {
      const response = await route.fetch();
      const detail = (await response.json()) as { payment_history: { received_at: string }[] };
      if (detail.payment_history.length > 0) detail.payment_history[0]!.received_at = boundary.toISOString();
      await route.fulfill({ response, json: detail });
    });
    await signIn(page, "ap");
    await page.getByTestId("invoice-row").filter({ hasText: "INV-20401" }).getByRole("button", { name: /INV-20401/ }).click();
    await expect(page.getByTestId("detail-history")).toContainText(defaultDay);
    await page.route("**/api/v1/tenant/config", async (route) => {
      const response = await route.fetch();
      const cfg = (await response.json()) as Record<string, unknown>;
      await route.fulfill({ response, json: { ...cfg, business_zone: "America/Los_Angeles" } });
    });
    await signIn(page, "ap");
    await page.getByTestId("invoice-row").filter({ hasText: "INV-20401" }).getByRole("button", { name: /INV-20401/ }).click();
    await expect(page.getByTestId("detail-history")).toContainText(pacificDay);
    // A payment is a moment, so it reads the day and the clock time ("Mar 1, 2026, 8:15 AM"), not the day alone.
    await expect(page.getByTestId("detail-history")).toContainText(/\d{4}, \d{1,2}:\d{2} [AP]M/);
  });

  test("nothing a person typed, was given or paid with rests in web storage, the address bar or the console", async ({ page }) => {
    const logged: string[] = [];
    page.on("console", (message) => logged.push(message.text()));
    await signIn(page, "ap");
    await pickAllAndOpenSheet(page);
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("receipt")).toBeVisible();
    const stored = await page.evaluate(() => JSON.stringify({ local: { ...window.localStorage }, session: { ...window.sessionStorage }, cookie: document.cookie }));
    expect(stored).not.toMatch(/pro-stub|stub-password|Bearer|stub-nonce|idempotency/);
    expect(await page.evaluate(async () => (await indexedDB.databases()).length)).toBe(0);
    expect(page.url()).not.toMatch(/pro-stub|token|password|nonce/);
    expect(logged.join("\n")).not.toMatch(/pro-stub|stub-password|Bearer|stub-nonce/);
  });

  test("the selector lists the surfaces this role opens, and signing out forgets the screen", async ({ page }) => {
    await signIn(page, "ap");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    await expect(panel.getByRole("button", { name: "Invoices" })).toHaveAttribute("aria-current", "page");
    await page.keyboard.press("Escape");
    await page.getByTestId("frame-sign-out").click();
    await expect(page.locator("hh-pro-signin")).toBeVisible();
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
  });
});

test.describe("desktop", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("Pay home and the sheet fit a wide page, the sheet centred", async ({ page }) => {
    await signIn(page, "ap");
    await expect(page.getByTestId("frame-role")).toBeVisible();
    await expect(page.getByTestId("aging").locator("li")).toHaveCount(4);
    await shot(page, "pay-home-desktop");
    await pickAllAndOpenSheet(page);
    const box = await page.getByTestId("pay-sheet").boundingBox();
    expect(box).not.toBeNull();
    expect(Math.abs((box?.x ?? 0) + (box?.width ?? 0) / 2 - 720)).toBeLessThan(2);
    await shot(page, "pay-sheet-desktop");
  });

  test("the unknown outcome and the receipt read the same on a wide page", async ({ page }) => {
    let first = true;
    await page.route("**/api/v1/tenant/payments", async (route) => {
      if (route.request().method() !== "POST") return route.continue();
      if (first) {
        first = false;
        await route.fetch();
        return route.abort("connectionreset");
      }
      return route.continue();
    });
    await signIn(page, "ap");
    await pickAllAndOpenSheet(page);
    await page.getByTestId("pay-confirm").click();
    await expect(page.getByTestId("check-payment")).toBeVisible();
    await shot(page, "pay-unknown-desktop");
    await page.getByTestId("check-payment").click();
    await expect(page.getByTestId("receipt-amount")).toContainText("$");
    await shot(page, "pay-receipt-desktop");
  });

  test("a slow session check and network failure keep the signed in frame on a wide page", async ({ page }) => {
    let mode: "slow" | "fail" | "ok" = "slow";
    await page.route("**/api/v1/tenant/**", async (route) => {
      if (route.request().url().endsWith("/tenant/config")) return route.continue();
      if (mode === "slow") {
        await new Promise((resolve) => setTimeout(resolve, 1800));
        return route.continue();
      }
      if (mode === "fail" && route.request().url().endsWith("/tenant/me")) {
        return route.abort("failed");
      }
      return route.continue();
    });
    await signIn(page, "ap");
    await expect(page.locator("hh-pro-frame")).toBeVisible({ timeout: 600 });
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "loading");
    await expect(state.locator('.state-indicator[data-pattern="progress"]')).toBeVisible();
    await shot(page, "slow-1440x900-light");
    await expect(page.getByTestId("invoice-row").first()).toContainText("INV-", { timeout: 6000 });

    await page.getByTestId("frame-sign-out").click();
    await expect(page.locator("hh-pro-signin")).toBeVisible();
    mode = "fail";
    await signInAgain(page, "ap");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
    await expect(state).toHaveAttribute("data-state", "error");
    await expect(state).toContainText("Pro is not reachable right now. Try again in a moment.");
    await shot(page, "unreachable-1440x900-light");
    mode = "ok";
    await state.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByTestId("invoice-row").first()).toContainText("INV-");
  });
});
