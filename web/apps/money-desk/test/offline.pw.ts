// CP-3, the Money Desk in the Work shell: online only (manifest mode none).
// Recording a payment is the money moving action: with no signal the press is
// refused with the honest "no signal" state, nothing is sent and nothing is
// kept, the form stays as it was, and the payment is recorded once when the
// signal is back. Never a queued payment.
import type { Page } from "@playwright/test";
import { test, expect, openDesk } from "./helpers.js";

function dialedWrites(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (r) => {
    if (["POST", "PUT", "PATCH", "DELETE"].includes(r.method()) && new URL(r.url()).pathname.startsWith("/api/")) seen.push(`${r.method()} ${new URL(r.url()).pathname}`);
  });
  return seen;
}

test("with no signal recording a payment is refused with no signal, sends nothing, and records once the signal is back", async ({ page, request, context }) => {
  await openDesk(page, request);
  await page.locator("[data-register-row]", { hasText: "INV-1002" }).getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-field='receive-amount']")).toHaveValue("$960.00");
  const amount = page.locator("[data-field='receive-amount']");
  await amount.fill("100");
  await amount.blur();
  await page.locator("[data-field='receive-reference']").fill("Cheque 1042");
  const dialed = dialedWrites(page);

  await context.setOffline(true);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.getByText(/The payment was not recorded\. No signal/).first()).toBeVisible();
  expect(dialed.filter((w) => w.startsWith("POST") && !w.includes("preview"))).toEqual([]);
  await expect(page.locator("[data-notice]")).toHaveCount(0);

  await context.setOffline(false);
  await page.getByRole("button", { name: "Record payment" }).click();
  await expect(page.locator("[data-notice]")).toContainText("PAY-0101 applied: $100.00 on INV-1002");
});
