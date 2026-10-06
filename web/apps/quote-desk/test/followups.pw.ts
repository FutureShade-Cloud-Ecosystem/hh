// The follow-ups panel in a real browser, against the stubbed harness: a draft
// quote has no cadence and says so, a sent quote has a plan with its upcoming
// steps, a step that went out reads "Queued" (never "Sent") with the heard
// from us line, the text leg refuses in the server's own words, and stopping
// then resuming the cadence round trips through the server.
import { expect, test, type Page } from "@playwright/test";
import { openTab } from "./surface-tabs";
import { shot } from "./shot";

async function signIn(page: Page, role = "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openQuote(page: Page, text: string): Promise<void> {
  await page.getByTestId("qb-list-row").filter({ hasText: text }).first().click();
  await openTab(page, "activity");
  await expect(page.locator("qd-quote-followups").getByTestId("fu-box")).toBeVisible();
}

const panel = (page: Page) => page.locator("qd-quote-followups");

test.beforeAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a draft quote has no cadence and says so, and offers the text leg", async ({ page }) => {
  await signIn(page, "Sales");
  await openQuote(page, "Draft");
  await expect(panel(page).getByTestId("fu-summary")).toContainText("No follow-up cadence on this quote yet");
  await expect(panel(page).getByTestId("fu-empty")).toContainText("Nothing has been sent yet, and nothing is scheduled.");
  await expect(panel(page).getByRole("button", { name: "Text the quote link" })).toBeEnabled();
  await expect(panel(page).getByRole("button", { name: /follow-ups/i })).toHaveCount(0);
  await shot(panel(page), "followups-empty-draft");
});

test("a sent quote shows its cadence and the steps to come; the empty look is the seed's", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1002");
  await expect(panel(page).getByTestId("fu-summary")).toContainText("Follow-ups are on: day 3, day 7, day 14 after the quote was sent, by email.");
  await expect(panel(page).getByTestId("fu-row")).toHaveCount(3);
  await expect(panel(page).getByTestId("fu-row").first()).toContainText("Scheduled");
  await expect(panel(page).getByTestId("fu-contact")).toContainText("Not heard from us yet; next: day 3 email");
  await expect(panel(page).getByRole("button", { name: "Stop follow-ups" })).toBeEnabled();
  await shot(panel(page), "followups-empty-seed-sent");
});

test("a step that went out is a heard from us line and reads Queued, never Sent", async ({ page, request }) => {
  await request.post("/_stub/followup-event", { data: { quote_id: "q-1002", step_day: 3, channel: "EMAIL", status: "SENT" } });
  await signIn(page);
  await openQuote(page, "Q-1002");
  await expect(panel(page).getByTestId("fu-contact")).toContainText("Heard from us: day 3 email, queued");
  await expect(panel(page).getByTestId("fu-contact")).toContainText("next: day 7 email");
  const first = panel(page).getByTestId("fu-row").first();
  await expect(first).toContainText("Day 3");
  await expect(first).toContainText("Queued");
  await expect(panel(page).getByTestId("fu-box")).not.toContainText(/\bSent\b/);
  await expect(panel(page).getByTestId("fu-row")).toHaveCount(3);
  await shot(panel(page), "followups-heard");
});

test("a skipped step is not hearing from us", async ({ page, request }) => {
  await request.post("/_stub/followup-event", { data: { quote_id: "q-1002", step_day: 3, channel: "SMS", status: "SKIPPED" } });
  await signIn(page);
  await openQuote(page, "Q-1002");
  await expect(panel(page).getByTestId("fu-contact")).toContainText("Not heard from us yet");
  await expect(panel(page).getByTestId("fu-row").first()).toContainText("Not sent");
});

test("the text leg's refusal shows the server's words and changes nothing", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1002");
  await panel(page).getByRole("button", { name: "Text the quote link" }).click();
  await expect(panel(page).getByTestId("fu-error")).toHaveText("The customer has not opted in to text messages");
  await expect(panel(page).getByTestId("fu-notice")).toHaveCount(0);
  await expect(panel(page).getByTestId("fu-summary")).toContainText("Follow-ups are on");
  await shot(panel(page), "followups-refused");
});

test("stop then resume the automatic follow-ups", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1002");
  await panel(page).getByRole("button", { name: "Stop follow-ups" }).click();
  await expect(panel(page).getByTestId("fu-notice")).toHaveText("Follow-ups turned off for this quote.");
  await expect(panel(page).getByTestId("fu-summary")).toContainText("Follow-ups are OFF for this quote");
  await expect(panel(page).getByTestId("fu-summary")).toContainText("Nothing further will be sent.");
  await expect(panel(page).getByTestId("fu-contact")).toContainText("nothing further is scheduled");
  await expect(panel(page).getByRole("button", { name: "Stop follow-ups" })).toHaveCount(0);
  await shot(panel(page), "followups-stopped");
  await panel(page).getByRole("button", { name: "Restart follow-ups" }).click();
  await expect(panel(page).getByTestId("fu-notice")).toHaveText("Follow-ups turned on.");
  await expect(panel(page).getByTestId("fu-summary")).toContainText("Follow-ups are on: day 3, day 7, day 14");
  await expect(panel(page).getByRole("button", { name: "Stop follow-ups" })).toBeVisible();
});

test("an accepted quote cannot be texted, and the panel says nothing of it", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Accepted");
  await expect(panel(page).getByRole("button", { name: "Text the quote link" })).toHaveCount(0);
});
