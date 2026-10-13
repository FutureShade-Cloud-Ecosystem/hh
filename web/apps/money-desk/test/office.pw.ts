// The receivables office walked in a real browser against the stub: the statement run read back with what went to
// whom, the AP contact, reminders and the paper path, an invoice's send, document, dispute and a payment reversal,
// and the settings. The stub is held to the core's shapes, sentences and gates by test/office-stub-contract.test.mjs.
import { test, expect, openDesk } from "./helpers.js";
import type { Page } from "@playwright/test";

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
