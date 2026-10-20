// CP-3, Manager Home in the Work shell: an approval decides online. With no
// signal the decision is refused with the honest "no signal" state, nothing is
// sent, nothing is queued, and the request stays open for the person to decide
// when the signal returns. Money never queues, and an approval is a decision
// about money.
import { test, expect, type Page } from "@playwright/test";
import { resetStub } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

function dialedWrites(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (r) => {
    if (["POST", "PUT", "PATCH", "DELETE"].includes(r.method()) && new URL(r.url()).pathname.startsWith("/api/")) seen.push(`${r.method()} ${new URL(r.url()).pathname}`);
  });
  return seen;
}

test("with no signal an approval is refused with no signal, sends nothing, and decides once the signal is back", async ({ page, context }) => {
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await page.locator("[data-needs='approvals.waiting'] button", { hasText: "Review" }).click();
  const row = page.locator("[data-approval]").first();
  await expect(row).toBeVisible();
  const dialed = dialedWrites(page);

  await context.setOffline(true);
  await row.getByRole("button", { name: "Approve" }).click();
  await page.locator("[data-test='decision-confirm']").getByRole("button", { name: "Yes, approve" }).click();
  await expect(page.getByText(/No signal/).first()).toBeVisible();
  expect(dialed.filter((w) => w.includes("/decide"))).toEqual([]);
  await expect(page.locator("[data-test='decided-group']")).toHaveCount(0);

  await context.setOffline(false);
  await page.locator("[data-test='decision-confirm']").getByRole("button", { name: "Yes, approve" }).click();
  await expect(page.locator("[data-test='decided-group']")).toContainText("Approved");
  expect(dialed.filter((w) => w.includes("/decide"))).toHaveLength(1);
});
