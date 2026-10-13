// The receivables office walked in a real browser against the stub: the statement run read back with what went to
// whom, the AP contact, reminders and the paper path, an invoice's send, document, dispute and a payment reversal,
// and the settings. The stub is held to the core's shapes, sentences and gates by test/office-stub-contract.test.mjs.
import { test, expect, openDesk, receivablesHandoff } from "./helpers.js";
import type { APIRequestContext, Page } from "@playwright/test";

/** Signs in as Payables and Receivables (or the controller) on a stub world named by its scenario, and lands on the desk. */
async function openOffice(page: Page, request: APIRequestContext, scenario: "pro" | "empty", who?: "controller"): Promise<void> {
  const answer = await request.post("/_stub/scenario", { data: { name: scenario } });
  expect(answer.ok()).toBe(true);
  await page.addInitScript(receivablesHandoff, who);
  await page.goto("/");
  if (who === "controller") await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
}

async function openTab(page: Page, name: string): Promise<void> {
  await page.getByRole("button", { name, exact: true }).first().click();
}

async function openAccount(page: Page, name: string): Promise<void> {
  await page.locator("[data-register-row]", { hasText: name }).first().getByRole("button").click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
}

async function noSidewaysScroll(page: Page): Promise<void> {
  const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(wide).toBe(false);
}

// ---- statement runs and AP contacts --------------------------------------------------------------------------

test("a statement run is read back with what went to whom, and no address is drawn", async ({ page, request }) => {
  await openDesk(page, request);
  await page.getByRole("button", { name: "Statements", exact: true }).click();
  await expect(page.locator("[data-run-none]")).toBeVisible();
  await page.getByRole("button", { name: "Run for every account" }).first().click();
  await expect(page.locator("[data-run]")).toBeVisible();
  await expect(page.locator("[data-run-mode]")).toHaveText("Month end, every account");
  await expect(page.locator("[data-run-count]")).toHaveText("5");
  await expect(page.locator("[data-run-notices]")).toHaveText("4 statements went to the messaging sink; 1 had no AP contact or was held back.");
  await expect(page.locator("[data-run-row]")).toHaveCount(5);
  await expect(page.locator("[data-run-row]", { hasText: "Granite Edge" }).locator("[data-run-goes]")).toHaveText("To the AP contact (Accounts payable desk), by email");
  await expect(page.locator("[data-run-row]", { hasText: "Cedar Ridge" }).locator("[data-run-goes]")).toHaveText("Nowhere: no AP contact is tagged");
  await expect(page.locator("[data-run-row]", { hasText: "Cedar Ridge" })).toContainText("Not queued");
  expect(await page.locator("[data-run]").innerText()).not.toMatch(/@|gls-demo/);
});

test("an account's AP contact is tagged, replaced and removed, and checked before it is sent", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Cedar Ridge");
  await page.getByRole("button", { name: "Contacts", exact: true }).click();
  await expect(page.locator("[data-contact-line]")).toHaveText("No AP contact is tagged on this account.");

  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "PUT" && r.url().includes("/ap-contact")) sent.push(r.url());
  });
  await page.locator("[data-field='contact-name']").fill("Accounts payable desk");
  await page.getByRole("button", { name: "Tag the contact" }).click();
  await expect(page.locator("[data-error]")).toContainText("An AP contact needs an email or a phone");
  await page.locator("[data-field='contact-email']").fill("no at sign");
  await page.getByRole("button", { name: "Tag the contact" }).click();
  await expect(page.locator("[data-error]")).toContainText("That AP email address is not an address.");
  expect(sent).toEqual([]);

  await page.locator("[data-field='contact-email']").fill("ap@gls-demo.invalid");
  await page.getByRole("button", { name: "Tag the contact" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The AP contact is saved.");
  await expect(page.locator("[data-contact-line]")).toHaveText("AP contact on file, Accounts payable desk: email on file, no phone.");
  expect(await page.locator("[data-contact-panel]").innerText()).not.toMatch(/@|gls-demo/);
  expect(await page.locator("[data-field='contact-email']").inputValue()).toBe("");

  await page.locator("[data-field='contact-phone']").fill("5550100");
  await page.getByRole("button", { name: "Replace the contact" }).click();
  await expect(page.locator("[data-contact-line]")).toHaveText("AP contact on file, Accounts payable desk: no email, phone on file.");

  await page.getByRole("button", { name: "Remove the contact" }).click();
  await page.getByRole("button", { name: "Keep it" }).click();
  await expect(page.locator("[data-contact-line]")).toContainText("phone on file");
  await page.getByRole("button", { name: "Remove the contact" }).click();
  await page.getByRole("button", { name: "Remove it now" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The AP contact is removed.");
  await expect(page.locator("[data-contact-line]")).toHaveText("No AP contact is tagged on this account.");
});

test("on a phone the run and the contact fit the screen and work from the keyboard", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openDesk(page, request);
  await page.getByRole("button", { name: "Statements", exact: true }).click();
  await page.getByRole("button", { name: "Run for every account" }).first().click();
  await expect(page.locator("[data-run-row]")).toHaveCount(5);
  await noSidewaysScroll(page);
  await page.getByRole("button", { name: "Receivables", exact: true }).first().click();
  await openAccount(page, "Cedar Ridge");
  await page.getByRole("button", { name: "Contacts", exact: true }).click();
  await expect(page.locator("[data-contact-panel]")).toBeVisible();
  await noSidewaysScroll(page);
  await page.locator("[data-field='contact-name']").focus();
  await page.keyboard.press("Tab");
  await expect(page.locator("[data-field='contact-email']")).toBeFocused();
});

// ---- reminders and the paper path ----------------------------------------------------------------------------

async function fillReminder(page: Page, account: string): Promise<void> {
  await page.locator("[data-field='remind-account']").selectOption({ label: account });
  await expect(page.locator("[data-field='remind-invoice']")).toBeEnabled();
}

test("on the base plan a reminder says why nothing goes, and the server's own sentence is what a press shows", async ({ page, request }) => {
  await openDesk(page, request);
  await openTab(page, "Reminders");
  await expect(page.locator("[data-plan]")).toHaveText("Base plan");
  await expect(page.locator("[data-history-empty]")).toHaveText("No reminder has been sent yet.");
  await fillReminder(page, "Granite Edge Landscaping (demo)");
  await expect(page.locator("[data-remind-blocked]")).toHaveText("Reminders are part of accelerated AR, and this dealer is on the base plan.");
  await expect(page.getByRole("button", { name: "Send reminder" })).toHaveAttribute("aria-disabled", "true");
  await page.getByRole("button", { name: "Send reminder" }).click({ force: true });
  await expect(page.locator("[data-error]")).toHaveText("Reminders are part of accelerated AR, this account is on the base plan.");
  await expect(page.locator("[data-history-row]")).toHaveCount(0);
});

test("a reminder goes to the AP contact by its role, is stamped on a resend, and lists without an address", async ({ page, request }) => {
  await openOffice(page, request, "pro");
  await openTab(page, "Reminders");
  await expect(page.locator("[data-plan]")).toHaveText("Accelerated AR");
  await fillReminder(page, "Granite Edge Landscaping (demo)");
  await expect(page.locator("[data-remind-line]")).toHaveText("Goes to the AP contact (Accounts payable desk), by email, through the messaging sink.");
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("Reminder for INV-1001 went to the AP contact by email, through the messaging sink.");
  await expect(page.locator("[data-history-row]")).toHaveCount(1);
  await expect(page.locator("[data-history-row]").first()).toContainText("the AP contact, by email");
  await expect(page.locator("[data-history-row]").first()).toContainText("Escalation");
  await expect(page.locator("[data-history-row]").first()).toContainText("Not opened");

  await page.getByRole("checkbox", { name: "Send it again" }).click();
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-notice]")).toContainText("It was stamped PAST DUE");
  await expect(page.locator("[data-history-row]")).toHaveCount(2);
  await expect(page.locator("[data-history-row]").first()).toContainText("(resend)");

  await page.locator("[data-field='history-account']").selectOption({ label: "Oakline Builders (demo)" });
  await expect(page.locator("[data-history-empty]")).toHaveText("No reminder has been sent to this account yet.");
  expect(await page.locator("main").innerText()).not.toMatch(/@|gls-demo|\.invalid/);
});

test("the channel and the account's contact decide what a reminder would do, and the server's refusal is said as written", async ({ page, request }) => {
  await openOffice(page, request, "pro");
  await openTab(page, "Reminders");
  await fillReminder(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Text", exact: true }).click();
  await expect(page.locator("[data-remind-blocked]")).toHaveText("Text reminders are switched off for this dealer.");
  await page.getByRole("button", { name: "Email", exact: true }).click();

  await fillReminder(page, "Cedar Ridge Contractors (demo)");
  await expect(page.locator("[data-remind-line]")).toContainText("Nobody is tagged for AP on this account. The account's own address is used only if you confirm it below.");
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Nobody is tagged for AP on this account, confirm the fallback recipient or tag a contact.");
  await expect(page.locator("[data-history-row]")).toHaveCount(0);
  await page.getByRole("checkbox", { name: "Confirm the account's own address" }).click();
  await expect(page.locator("[data-remind-line]")).toContainText("The account's own address will be used, as you confirmed.");
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-notice]")).toContainText("went to the account's own address, which you confirmed");
  await expect(page.locator("[data-history-row]").first()).toContainText("the account's own address, which you confirmed");
});

test("a reminder whose answer was lost is sent again with the same key and is recorded once", async ({ page, request }) => {
  await openOffice(page, request, "pro");
  await openTab(page, "Reminders");
  await fillReminder(page, "Granite Edge Landscaping (demo)");
  const keys: string[] = [];
  let lost = true;
  await page.route("**/api/v1/ar/reminders", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    keys.push(JSON.parse(route.request().postData() ?? "{}").idempotency_key);
    if (lost) {
      lost = false;
      await route.fetch();
      return route.abort("connectionreset");
    }
    return route.continue();
  });
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-error]")).toContainText("could not be reached");
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-notice]")).toContainText("went to the AP contact");
  expect(keys.length).toBe(2);
  expect(keys[0]).toBe(keys[1]);
  await expect(page.locator("[data-history-row]")).toHaveCount(1);
});

test("the paper path closes a flag with a note, and the Needs you list follows", async ({ page, request }) => {
  await openDesk(page, request);
  await expect(page.locator("[data-need-paper]")).toHaveCount(1);
  await openTab(page, "Reminders");
  await expect(page.locator("[data-paper-row]")).toHaveCount(1);
  await expect(page.locator("[data-paper-row]")).toContainText("Summit Patio Co (demo)");
  await expect(page.locator("[data-paper-row]")).toContainText("3 reminders in 45 days, none opened");
  await page.getByRole("button", { name: "Moved to paper" }).click();
  await page.getByRole("button", { name: "Move to paper" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Say why in a few words: the next person to open this account reads it, not the flag.");
  await expect(page.locator("[data-paper-row]")).toHaveCount(1);
  await page.locator("[data-field='paper-note']").fill("Moved to paper statements by phone");
  await page.getByRole("button", { name: "Move to paper" }).click();
  await expect(page.locator("[data-notice]")).toContainText("marked as moved to paper");
  await expect(page.locator("[data-paper-empty]")).toHaveText("No account is flagged.");
  await openTab(page, "Receivables");
  await expect(page.locator("[data-need-paper]")).toHaveCount(0);
});

test("the sweep raises the account that ignored three reminders", async ({ page, request }) => {
  await openOffice(page, request, "pro");
  await openTab(page, "Reminders");
  await fillReminder(page, "Riverstone Paving (demo)");
  for (let i = 1; i <= 3; i += 1) {
    if (i > 1) await page.getByRole("checkbox", { name: "Send it again" }).click();
    await page.getByRole("button", { name: "Send reminder" }).click();
    await expect(page.locator("[data-history-row]")).toHaveCount(i);
  }
  await page.getByRole("button", { name: "Run the sweep" }).click();
  await expect(page.locator("[data-notice]")).toContainText("raised 1 new flag.");
  await expect(page.locator("[data-paper-row]")).toHaveCount(2);
  await expect(page.locator("[data-paper-row]").first()).toContainText("Riverstone Paving (demo)");
  await page.getByRole("button", { name: "Run the sweep" }).click();
  await expect(page.locator("[data-notice]")).toContainText("raised 0 new flags.");
  await page.getByRole("button", { name: "Dismiss", exact: true }).first().click();
  await page.locator("[data-field='paper-note']").fill("Phoned the desk");
  await page.getByRole("button", { name: "Dismiss the flag" }).click();
  await expect(page.locator("[data-paper-row]")).toHaveCount(1);
});

test("on a phone the reminders screen fits, the history becomes cards, and the form works from the keyboard", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openOffice(page, request, "pro");
  await openTab(page, "Reminders");
  await fillReminder(page, "Granite Edge Landscaping (demo)");
  await page.getByRole("button", { name: "Send reminder" }).click();
  await expect(page.locator("[data-history-row]")).toHaveCount(1);
  await expect(page.locator("[data-history] thead")).toBeHidden();
  await noSidewaysScroll(page);
  await page.locator("[data-field='remind-account']").focus();
  await page.keyboard.press("Tab");
  await expect(page.locator("[data-field='remind-invoice']")).toBeFocused();
});
