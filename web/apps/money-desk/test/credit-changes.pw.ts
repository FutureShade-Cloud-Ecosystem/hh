// The credit limit's change screens (CC-2) walked in a real browser against the stub.
//
// The Receivables desk asks for a new limit on one account and reads its history in words; the
// approver's tab lists what waits and approves or rejects it. The tier, the roles and every
// refusal are the core's: the stub carries the core's sentences, and the desk shows them.
import { test, expect, openDesk } from "./helpers.js";
import type { Page } from "@playwright/test";

async function openCreditLimit(page: Page, name: string): Promise<void> {
  await page.locator("[data-register-row]", { hasText: name }).first().getByRole("button").click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  await page.locator("button[data-action='sub'][data-choice='credit']").click();
  await expect(page.locator("[data-credit-limit]")).toBeVisible();
}

test("a decrease takes effect at once and the history reads it in words", async ({ page, request }) => {
  await openDesk(page, request);
  await openCreditLimit(page, "Granite Edge Landscaping");
  await page.locator("[data-field='credit-limit-amount']").fill("1000.00");
  await page.getByRole("button", { name: "Request the change" }).click();
  // The reason is the core's own requirement, said in its words.
  await expect(page.locator("[data-error]")).toContainText("reason");
  await page.locator("[data-field='credit-limit-reason']").fill("Past due");
  await page.getByRole("button", { name: "Request the change" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The change is in place");
  await expect(page.locator("[data-credit-limit-figure]")).toHaveText("$1,000.00");
  const row = page.locator("[data-credit-history] [data-creditchange-row]").first();
  await expect(row).toContainText("Took effect the day it was asked");
  await expect(row).toContainText("Past due");
});

test("an increase waits, says what it waits for, and moves nothing", async ({ page, request }) => {
  await openDesk(page, request);
  await openCreditLimit(page, "Granite Edge Landscaping");
  const before = await page.locator("[data-credit-limit-figure]").innerText();
  await page.locator("[data-field='credit-limit-amount']").fill("90000.00");
  await page.locator("[data-field='credit-limit-reason']").fill("Two large jobs");
  await page.getByRole("button", { name: "Request the change" }).click();
  await expect(page.locator("[data-notice]")).toContainText("waiting for two approvals");
  await expect(page.locator("[data-notice]")).toContainText("Nothing has moved");
  await expect(page.locator("[data-credit-limit-figure]")).toHaveText(before);
  await expect(page.locator("[data-credit-pending]").first()).toContainText("Waiting for two approvals");
});

test("a requester who may not decide sees the waiting list without the controls", async ({ page, request }) => {
  await openDesk(page, request);
  // Payables and Receivables is admitted at the route, so it reads the list, but it may not decide.
  await page.getByRole("button", { name: "Credit changes" }).click();
  await expect(page.locator("[data-creditchanges]")).toBeVisible();
  await expect(page.getByRole("button", { name: "Approve" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^Reject/ })).toHaveCount(0);
  await expect(page.locator("[data-creditchanges]")).toContainText("decides");
});

test("the approver sees what waits, approves one, and the first of two approvals is said", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await page.getByRole("button", { name: "Credit changes" }).click();
  const rows = page.locator("[data-creditchanges] [data-creditchange-row]");
  await expect(rows).toHaveCount(2);
  // The one that waits for a single approval is approved and applies.
  const single = rows.filter({ hasText: "The winter work" });
  await single.getByRole("button", { name: "Approve" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Approved: the credit limit is now $32,000.00");
  await expect(rows).toHaveCount(1);
  // The one that already holds the first of two approvals shows it.
  await expect(rows.first()).toContainText("First of two approvals recorded");
});

test("Approve is disabled, and says why, on a request this viewer already approved", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await page.getByRole("button", { name: "Credit changes" }).click();
  const mine = page.locator("[data-creditchanges] [data-creditchange-row]").filter({ hasText: "First of two approvals recorded" });
  const approve = mine.getByRole("button", { name: "Approve" });
  await expect(approve).toBeDisabled();
  await expect(approve).toHaveClass(/held/);
  await expect(mine.locator("[data-credit-why]")).toContainText("You approved this one; it needs a second person");
  // Rejecting is still the viewer's to do: the core only turns away a second approval.
  await expect(mine.getByRole("button", { name: /^Reject/ })).toBeEnabled();
});

test("a request the viewer asked for can be neither approved nor rejected by them, and says so", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openCreditLimit(page, "Granite Edge Landscaping");
  await page.locator("[data-field='credit-limit-amount']").fill("55000.00");
  await page.locator("[data-field='credit-limit-reason']").fill("A new yard contract");
  await page.getByRole("button", { name: "Request the change" }).click();
  await expect(page.locator("[data-credit-pending]").first()).toBeVisible();
  await page.getByRole("button", { name: "‹ Receivables" }).first().click();
  await page.getByRole("button", { name: "Credit changes" }).click();
  const own = page.locator("[data-creditchanges] [data-creditchange-row]").filter({ hasText: "A new yard contract" });
  await expect(own.getByRole("button", { name: "Approve" })).toBeDisabled();
  await expect(own.getByRole("button", { name: /^Reject/ })).toBeDisabled();
  await expect(own.locator("[data-credit-why]")).toContainText("You asked for this one");
});

test("a second approver completes a two approval request and the limit moves", async ({ page, request }) => {
  await openDesk(page, request, "super-admin");
  await page.getByRole("button", { name: "Credit changes" }).click();
  const two = page.locator("[data-creditchanges] [data-creditchange-row]").filter({ hasText: "First of two approvals recorded" });
  await expect(two.getByRole("button", { name: "Approve" })).toBeEnabled();
  await two.getByRole("button", { name: "Approve" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Approved: the credit limit is now $85,000.00");
  await expect(page.locator("[data-creditchanges] [data-creditchange-row]").filter({ hasText: "First of two approvals recorded" })).toHaveCount(0);
});

test("the server's refusal still reads in its own words when a role it turns away presses Approve", async ({ page, request }) => {
  // A branch manager may give a first approval, not the second: the screen leaves the control live and the
  // core is the backstop.
  await openDesk(page, request, "branch-manager");
  await page.getByRole("button", { name: "Credit changes" }).click();
  const two = page.locator("[data-creditchanges] [data-creditchange-row]").filter({ hasText: "First of two approvals recorded" });
  await two.getByRole("button", { name: "Approve" }).click();
  await expect(page.locator("[data-error]")).toContainText("caller role is not permitted to approve this request");
});

test("a rejection asks why and settles the request", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await page.getByRole("button", { name: "Credit changes" }).click();
  const single = page.locator("[data-creditchanges] [data-creditchange-row]").filter({ hasText: "The winter work" });
  await single.getByRole("button", { name: /^Reject/ }).click();
  await page.getByRole("button", { name: "Reject the change" }).click();
  await expect(page.locator("[data-error]")).toContainText("reason");
  await page.locator("[data-field='credit-reject-reason']").fill("The balance is past due");
  await page.getByRole("button", { name: "Reject the change" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The request is rejected");
  await expect(page.locator("[data-creditchanges] [data-creditchange-row]").filter({ hasText: "The winter work" })).toHaveCount(0);
});

// A box the page drew, as numbers; the two never share a pixel when one ends before the other starts.
type Box = { x: number; y: number; width: number; height: number };
const apart = (a: Box, b: Box): boolean => a.x + a.width <= b.x + 0.5 || b.x + b.width <= a.x + 0.5 || a.y + a.height <= b.y + 0.5 || b.y + b.height <= a.y + 0.5;

test("at 390 the approver list reads the name, then From, then To, and no box overprints another", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openDesk(page, request, "controller");
  await page.getByRole("button", { name: "Credit changes" }).click();
  const rows = page.locator("[data-creditchanges] [data-creditchange-row]");
  await expect(rows).toHaveCount(2);
  // The longest seeded account name rides one of these rows.
  await expect(rows.filter({ hasText: "Granite Edge Landscaping (demo)" })).toHaveCount(1);
  for (let i = 0; i < 2; i += 1) {
    const row = rows.nth(i);
    const name = (await row.locator("button.link-row").boundingBox()) as Box;
    const from = (await row.locator("td[data-card-label='From']").boundingBox()) as Box;
    const to = (await row.locator("td[data-card-label='To']").boundingBox()) as Box;
    expect(apart(name, from), "name and From").toBe(true);
    expect(apart(name, to), "name and To").toBe(true);
    expect(apart(from, to), "From and To").toBe(true);
    expect(from.y, "From reads before To").toBeLessThan(to.y);
    expect(name.y, "the name reads before From").toBeLessThan(from.y);
  }
});
