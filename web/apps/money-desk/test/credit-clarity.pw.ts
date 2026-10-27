// The credit memo form, read the way the visual read read it (FW1-12, round 2).
//
// The visual read of PR 617 found three things the form did not do: it hid its two field labels
// where only a screen reader could find them, it said nothing about what the credit would do to a
// balance or about the approval rail, and it sat on the bare page between the head and the tabs
// instead of in the row or card it belongs to. The invoice row's Credit link was under the
// counter's PC target height.
//
// This file walks the form the way a manager walks it, and proves each of those is fixed.
import { test, expect, openDesk } from "./helpers.js";
import type { Page } from "@playwright/test";

async function openAccount(page: Page, name: string): Promise<void> {
  await page.locator("[data-register-row]", { hasText: name }).first().getByRole("button").click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
}

/** The width of a piece of on-screen text, so a label hidden from the eye is caught. */
async function textWidth(locator: import("@playwright/test").Locator): Promise<number> {
  const box = await locator.boundingBox();
  return box === null ? 0 : box.width;
}

test("the form shows its labels, and says what the credit does, before the press", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping");

  await page.getByRole("button", { name: "Issue a credit" }).click();
  const form = page.locator("[data-credit-form]");
  await expect(form).toBeVisible();

  // The two labels are on screen, not only for a screen reader: a hidden label is one pixel wide.
  const amountLabel = form.locator("[data-credit-amount-label]");
  const reasonLabel = form.locator("[data-credit-reason-label]");
  await expect(amountLabel).toBeVisible();
  await expect(reasonLabel).toBeVisible();
  expect(await textWidth(amountLabel)).toBeGreaterThan(40);
  expect(await textWidth(reasonLabel)).toBeGreaterThan(40);

  // The form says what the credit does: on the account, not on an invoice.
  await expect(form.locator("[data-credit-target]")).toContainText("On the account");
  await expect(form.locator("[data-credit-target]")).toContainText("lowers what this customer owes");
});

test("a credit against one invoice names the invoice and the cap it has left", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping");

  await page.locator("[data-invoice-row] button[data-action='credit-start']").first().click();
  const form = page.locator("[data-credit-form]");
  await expect(form).toBeVisible();
  await expect(form.locator("[data-credit-target]")).toContainText("Against invoice INV-1001");
  await expect(form.locator("[data-credit-target]")).toContainText("up to $4,825.00");
  // The cap is on the amount label too, where the amount is typed.
  await expect(form.locator("[data-credit-amount-label]")).toContainText("up to $4,825.00");
});

test("a credit at or over the approval threshold says it waits and moves no money", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping");

  await page.getByRole("button", { name: "Issue a credit" }).click();
  const form = page.locator("[data-credit-form]");
  await expect(form).toBeVisible();

  // The threshold is the branch's own, read from the server: the form says what it is.
  await expect(form.locator("[data-credit-approval]")).toContainText("$5,000.00");
  await expect(form.locator("[data-credit-approval]")).toContainText("tax included");

  // Type a credit over it: the form says, before the press, that this one waits.
  await page.locator("[data-field='credit-amount']").fill("6000.00");
  await expect(form.locator("[data-credit-approval]")).toContainText("at or over $5,000.00");
  await expect(form.locator("[data-credit-approval]")).toContainText("moves no money");
  await expect(form.locator("[data-credit-approval]")).toContainText("second pair of eyes");

  // And the server agrees: the credit waits, and the desk says no money has moved.
  await page.locator("[data-field='credit-reason']").fill("The goods came back");
  await page.getByRole("button", { name: "Issue the credit" }).click();
  await expect(page.locator("[data-notice]")).toContainText("second pair of eyes");
  await expect(page.locator("[data-notice]")).toContainText("No money has moved yet");
});

test("the account's credit opens in a card, and the invoice's credit opens in its own row", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping");

  // The account's credit: a card of its own, below the tabs, not on the bare page.
  await page.getByRole("button", { name: "Issue a credit" }).click();
  await expect(page.locator(".panel [data-credit-form]")).toHaveCount(1);
  const head = page.locator(".head").first();
  const tabs = page.locator("nav.tabs").first();
  const card = page.locator(".panel [data-credit-form]").first();
  // The card sits BELOW the tabs: the form is no longer between the head and the tabs.
  expect(await tabs.evaluate((el, other) => el.compareDocumentPosition(other) & Node.DOCUMENT_POSITION_FOLLOWING, await card.elementHandle())).toBeTruthy();
  await expect(card).toBeVisible();

  // The invoice's credit: in the row of the invoice it is about.
  await page.locator("[data-credit-form]").getByRole("button", { name: "Cancel" }).click();
  const row = page.locator("[data-invoice-row]").first();
  await row.locator("button[data-action='credit-start']").click();
  const creditRow = page.locator("tr.credit-form-row");
  await expect(creditRow).toHaveCount(1);
  await expect(creditRow.locator("[data-credit-form]")).toHaveCount(1);
  // The row it sits in is the row of the invoice that was pressed, right under it.
  expect(await creditRow.evaluate((el) => el.previousElementSibling?.hasAttribute("data-invoice-row") === true)).toBe(true);
  await expect(creditRow.locator("[data-credit-target]")).toContainText("INV-1001");
  // The form is as wide as the table it sits in, not squeezed into the invoice column.
  const formWidth = await creditRow.locator("[data-credit-form]").evaluate((el) => el.getBoundingClientRect().width);
  const tableWidth = await page.locator("table.inv").evaluate((el) => el.getBoundingClientRect().width);
  expect(formWidth).toBeGreaterThan(tableWidth * 0.8);
});

test("the form closes when the desk changes tab", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping");

  await page.getByRole("button", { name: "Issue a credit" }).click();
  await expect(page.locator("[data-credit-form]")).toBeVisible();
  await page.getByRole("button", { name: "Statement", exact: true }).click();
  await expect(page.locator("[data-credit-form]")).toHaveCount(0);
});

test("the invoice row's Credit link is a target a counter can press", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping");

  const credit = page.locator("[data-invoice-row] button[data-action='credit-start']").first();
  const box = await credit.boundingBox();
  // The counter's PC floor is 24 px.
  expect(box?.height ?? 0).toBeGreaterThanOrEqual(24);

  // On a phone the bar is 44 px.
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(credit).toBeVisible();
  const phone = await credit.boundingBox();
  expect(phone?.height ?? 0).toBeGreaterThanOrEqual(44);
});
