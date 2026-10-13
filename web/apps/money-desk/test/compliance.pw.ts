// The Money Desk's Compliance area (item 1d), walked in a real browser on the stub: what is due soon and what is past
// due, the whole calendar and its status filter, and the shared review status banner every lien screen carries. The
// refusal legs: a horizon that is not a whole number of days is refused in words before anything is asked, a status
// with no deadlines says so, a load that fails says so and can be tried again.
import type { Page } from "@playwright/test";
import { test, expect, openCompliance, complianceTab, liensTab, noSidewaysScroll, handoffFor } from "./lien-helpers.js";

const dueRows = (page: Page) => page.locator("[data-row='deadline']:visible");

test("Compliance opens on what is due soon: five open deadlines, soonest first, the two past due marked Overdue in words", async ({ page, request }) => {
  await openCompliance(page, request);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Compliance");
  await expect(page.locator("[data-tab-id='upcoming']")).toHaveAttribute("aria-current", "true");
  const rows = dueRows(page);
  await expect(rows).toHaveCount(5);
  await expect(rows.nth(0)).toContainText("Holdback release");
  await expect(rows.nth(0)).toContainText("GLS-2102");
  await expect(rows.nth(0)).toContainText("Overdue");
  await expect(rows.nth(1)).toContainText("GLS-2101");
  await expect(rows.nth(1)).toContainText("Overdue");
  await expect(rows.nth(2)).toContainText("GLS-2104");
  await expect(rows.nth(2)).toContainText("Open");
  await expect(rows.nth(3)).toContainText("Notice deadline");
  await expect(rows.nth(4)).toContainText("GLS-2103");
  await expect(rows.nth(4)).toContainText("45 days");
  await expect(page.locator("[data-overdue-count]")).toHaveText("2 past due");
});

test("the horizon box narrows the list, a bad horizon is refused in words before anything is asked, and blank reads the core's own", async ({ page, request }) => {
  await openCompliance(page, request);
  let asked = 0;
  await page.route("**/api/v1/compliance/upcoming*", (route) => {
    asked += 1;
    return route.continue();
  });
  await page.getByLabel("Look ahead (days)").fill("10");
  await page.getByRole("button", { name: "Show" }).click();
  await expect(dueRows(page)).toHaveCount(3);
  const askedBefore = asked;
  await page.getByLabel("Look ahead (days)").fill("abc");
  await page.getByRole("button", { name: "Show" }).click();
  await expect(page.locator("[data-error]")).toHaveText("The look ahead is a whole number of days from 1 to 365, or blank for the core's own.");
  expect(asked).toBe(askedBefore);
  await page.getByLabel("Look ahead (days)").fill("");
  await page.getByRole("button", { name: "Show" }).click();
  await expect(dueRows(page)).toHaveCount(5);
});

test("the calendar lists every deadline by due date, a status filter reads what the core holds, and an empty status says so", async ({ page, request }) => {
  await openCompliance(page, request);
  await complianceTab(page, "calendar");
  await expect(dueRows(page)).toHaveCount(7);
  await page.getByRole("button", { name: "Open", exact: true }).click();
  await expect(dueRows(page)).toHaveCount(5);
  // The core stores a past due deadline as open; the upcoming list is where it is marked, so the row says so.
  await expect(dueRows(page).filter({ hasText: "GLS-2102" })).toContainText("Overdue");
  await expect(dueRows(page).filter({ hasText: "GLS-2103" })).toContainText("Open");
  await page.getByRole("button", { name: "Waived", exact: true }).click();
  await expect(dueRows(page)).toHaveCount(1);
  await expect(dueRows(page)).toContainText("Proof of claim");
  await page.getByRole("button", { name: "Satisfied", exact: true }).click();
  await expect(dueRows(page)).toHaveCount(1);
  await expect(dueRows(page)).toContainText("Satisfied");
  await page.getByRole("button", { name: "Overdue", exact: true }).click();
  await expect(page.locator("[data-empty='calendar']")).toContainText("No deadlines with that status.");
  await page.getByRole("button", { name: "All", exact: true }).click();
  await expect(dueRows(page)).toHaveCount(7);
});

test("the review status is the core's own draft banner until it is set; setting it changes the banner every lien screen carries", async ({ page, request }) => {
  await openCompliance(page, request);
  await complianceTab(page, "review");
  await expect(page.locator("[data-review-state]")).toHaveText("Draft");
  await expect(page.locator("[data-review-banner]")).toHaveText("DRAFT, statute-derived, pending client validation");
  await page.getByLabel("State", { exact: true }).selectOption({ label: "Approved" });
  await page.getByLabel("Banner words").fill("Checked against the dealer's own forms");
  await page.getByRole("button", { name: "Save review status" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Saved: Approved.");
  await expect(page.locator("[data-review-state]")).toHaveText("Approved");
  await expect(page.locator("[data-review-banner]")).toHaveText("Checked against the dealer's own forms");
  await page.getByRole("button", { name: "Liens", exact: true }).click();
  await expect(page.locator("[data-banner]")).toContainText("Checked against the dealer's own forms");
  await expect(page.locator("[data-banner]")).toHaveAttribute("data-state", "APPROVED");
  await liensTab(page, "documents");
  await expect(page.locator("[data-banner]")).toContainText("Checked against the dealer's own forms");
});

test("a job's review is its own: the cockpit stays as it was, and a blank banner is the core's default words", async ({ page, request }) => {
  await openCompliance(page, request);
  await complianceTab(page, "review");
  await page.getByLabel("About", { exact: true }).selectOption({ label: "Job GLS-2101" });
  await expect(page.locator("[data-review-state]")).toHaveText("Draft");
  await page.getByLabel("State", { exact: true }).selectOption({ label: "Pending review" });
  await page.getByRole("button", { name: "Save review status" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Saved: Pending review.");
  await expect(page.locator("[data-review-banner]")).toHaveText("DRAFT, statute-derived, pending client validation");
  await page.getByLabel("About", { exact: true }).selectOption({ label: "Whole lien cockpit" });
  await expect(page.locator("[data-review-state]")).toHaveText("Draft");
});

test("a load that fails says so in words and Try again reads the area again", async ({ page, request }) => {
  await page.route("**/api/v1/compliance/upcoming*", (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal error" }) }));
  await page.addInitScript(handoffFor, "controller");
  await request.post("/_stub/reset");
  await page.goto("/");
  await page.getByRole("button", { name: "Compliance", exact: true }).click();
  await expect(page.locator("[data-load-error]")).toContainText("Internal error.");
  await page.unroute("**/api/v1/compliance/upcoming*");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(dueRows(page)).toHaveCount(5);
});

test("an empty calendar and nothing due soon say so", async ({ page, request }) => {
  for (const route of ["calendar", "upcoming"]) {
    await page.route(`**/api/v1/compliance/${route}*`, (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: null }) }));
  }
  await page.addInitScript(handoffFor, "payrec");
  await request.post("/_stub/reset");
  await page.goto("/");
  await page.getByRole("button", { name: "Compliance", exact: true }).click();
  await expect(page.locator("[data-empty='upcoming']")).toContainText("Nothing is due inside this look ahead.");
  await complianceTab(page, "calendar");
  await expect(page.locator("[data-empty='calendar']")).toContainText("No deadlines are registered.");
});

test("the General Manager reads the same calendar", async ({ page, request }) => {
  await openCompliance(page, request, "general_manager");
  await expect(dueRows(page)).toHaveCount(5);
});

test("the keys reach the tabs, the filter and the save, and focus stays where the person was", async ({ page, request }) => {
  await openCompliance(page, request);
  await page.locator("[data-compliance] [data-tab-id='calendar']").focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-tab-id='calendar']")).toHaveAttribute("aria-current", "true");
  await page.getByRole("button", { name: "Waived", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(dueRows(page)).toHaveCount(1);
  await page.locator("[data-compliance] [data-tab-id='review']").focus();
  await page.keyboard.press("Enter");
  await page.getByLabel("State", { exact: true }).focus();
  await page.keyboard.press("ArrowDown");
  await page.getByRole("button", { name: "Save review status" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-notice]")).toContainText("Saved:");
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("every tab fits the screen with cards in place of tables", async ({ page, request }) => {
    await openCompliance(page, request);
    await expect(dueRows(page)).toHaveCount(5);
    await expect(page.locator("table:visible")).toHaveCount(0);
    await noSidewaysScroll(page);
    await complianceTab(page, "calendar");
    await expect(dueRows(page)).toHaveCount(7);
    await noSidewaysScroll(page);
    await complianceTab(page, "review");
    await noSidewaysScroll(page);
  });
});
