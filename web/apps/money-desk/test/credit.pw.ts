// The credit memo door (FW1-12) walked in a real browser against the stub.
//
// The desk's part of a credit is small and is walked here the way it is used: open the account, press
// Issue a credit, say how much and why, press it once. The money is the core's business; this walk
// proves the desk asks the two questions the core insists on (the amount and the reason), that the
// credit against ONE INVOICE names that invoice, and that a second press on the same form raises one
// credit and not two.
import { test, expect, openDesk } from "./helpers.js";
import type { Page } from "@playwright/test";

async function openAccount(page: Page, name: string): Promise<void> {
  await page.locator("[data-register-row]", { hasText: name }).first().getByRole("button").click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
}

test("the credit form asks for the amount and the reason, and the credit reads out in words", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping");

  // The account's own door.
  await page.getByRole("button", { name: "Issue a credit" }).click();
  await expect(page.locator("[data-credit-form]")).toBeVisible();

  // The reason comes first: a credit with no stated reason cannot be explained to the customer, to
  // the Controller, or to whoever reads the file in a year.
  await page.locator("[data-field='credit-amount']").fill("100.00");
  await page.getByRole("button", { name: "Issue the credit" }).click();
  await expect(page.locator("[data-error]")).toContainText("why the customer is being credited");

  await page.locator("[data-field='credit-reason']").fill("The goods came back");
  await page.getByRole("button", { name: "Issue the credit" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Credit memo #CM-9001");
  await expect(page.locator("[data-notice]")).toContainText("$113.00");
  // The form closes when the credit is a document: the credit is issued, not still being typed.
  await expect(page.locator("[data-credit-form]")).toHaveCount(0);
});

test("a credit against one invoice names that invoice, and the desk says which", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping");

  await page.locator("[data-invoice-row] button[data-action='credit-start']").first().click();
  await expect(page.locator("[data-credit-form]")).toBeVisible();
  // The form says what the credit is against, in words: that one invoice, not the account.
  await expect(page.locator("[data-credit-target]")).toContainText("Against invoice");
  await expect(page.locator("[data-credit-target]")).toContainText("INV-");
});

test("a second press on the same credit raises one credit and not two", async ({ page, request }) => {
  await openDesk(page, request);
  await openAccount(page, "Granite Edge Landscaping");

  await page.getByRole("button", { name: "Issue a credit" }).click();
  await page.locator("[data-field='credit-amount']").fill("25.00");
  await page.locator("[data-field='credit-reason']").fill("The goods came back");
  await page.getByRole("button", { name: "Issue the credit" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Credit memo #CM-9001");

  // The stub answers the held key with the credit it already named, and says so.
  const calls: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/api/v1/ar/credit-memos")) calls.push(r.postData() ?? "");
  });
  await page.getByRole("button", { name: "Issue a credit" }).click();
  await page.locator("[data-field='credit-amount']").fill("25.00");
  await page.locator("[data-field='credit-reason']").fill("The goods came back");
  await page.getByRole("button", { name: "Issue the credit" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Credit memo #CM-9001");
  expect(calls.length).toBe(1);
});
