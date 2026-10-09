// The Financial Controller's legs on the Money Desk (roadmap 2.9, the first
// increment), walked in a real browser on the stub: the card lands on the
// approvals queue; the controller decides the seeded floor request and the
// approval is spent on that one quote; it approves the parked journal and the
// trial balance carries it; it cannot decide its own request, and the screen
// says so in words before a press; it reads the trial balance; and a refused
// decision names who may decide it.
import { test, expect, openApprovals } from "./gl-helpers.js";

const FR1 = "2f6a1c00-0000-4000-9000-0000000000f1";
const J1_REQUEST = "2f6a1c00-0000-4000-9000-0000000000a1";
const J2_REQUEST = "2f6a1c00-0000-4000-9000-0000000000a2";

test("the demo way in offers the controller's card and lands on the approvals queue", async ({ page, request }) => {
  await request.post("/_stub/reset");
  await page.goto("/");
  await expect(page.locator("hos-app-header")).toBeVisible();
  await expect(page.locator("[data-flow]")).toHaveCount(3);
  await page.locator("[data-flow='financial-controller']").click();
  await expect(page.locator("hos-app-header")).toBeVisible();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Approvals");
  await expect(page.locator("[data-approval-id]")).toHaveCount(5);
});

test("the card lands on the approvals queue: the floor request, both parked journals and the freight raises", async ({ page, request }) => {
  await openApprovals(page, request);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Approvals");
  await expect(page.locator("[data-waiting]")).toHaveText("5 waiting");
  const rows = page.locator("[data-approval-id]");
  await expect(rows.filter({ hasText: "Below floor quote request" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "Journal parked over the threshold" })).toHaveCount(2);
  await expect(rows.filter({ hasText: "Freight above the quote" })).toHaveCount(2);
  // The journals show the server's amounts; the controller's own request says so.
  await expect(page.locator(`[data-approval-id="${J1_REQUEST}"]`)).toContainText("$7,500.00");
  await expect(page.locator(`[data-approval-id="${J2_REQUEST}"]`)).toContainText("Filed by you");
  await expect(page.locator(`[data-approval-id="${J1_REQUEST}"]`)).not.toContainText("Filed by you");
});

test("it decides the floor request, and the answer says the approval is spent on that one quote", async ({ page, request }) => {
  await openApprovals(page, request);
  await page.locator(`[data-approval-id="${FR1}"]`).click();
  const panel = page.locator("[data-decide-panel]");
  await expect(panel).toContainText("Decide: Below floor quote request");
  await expect(panel.locator("[data-detail]")).toContainText("12 percent discount");
  await expect(panel).toContainText("One decision, one quote.");
  await panel.getByLabel("Note to the requester").fill("Approved once for this job.");
  await panel.getByRole("button", { name: "Decide" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The approval is spent on that one quote");
  await expect(page.locator(`[data-approval-id="${FR1}"]`)).toHaveCount(0);
  await expect(page.locator("[data-waiting]")).toHaveText("4 waiting");
});

test("it approves the parked journal, the journal posts, and the trial balance carries it", async ({ page, request }) => {
  await openApprovals(page, request);
  await page.locator("[data-tab-id='trial']").click();
  const total = page.locator("[data-total-debit]");
  const before = await total.innerText();
  await expect(page.locator("[data-trial]")).toContainText("In balance");
  await page.locator("[data-tab-id='approvals']").click();
  await page.locator(`[data-approval-id="${J1_REQUEST}"]`).click();
  const panel = page.locator("[data-decide-panel]");
  await expect(panel).toContainText("Amount $7,500.00");
  await expect(panel.getByRole("table", { name: "Journal lines" })).toContainText("1300");
  await expect(panel.getByRole("table", { name: "Journal lines" })).toContainText("Prepaid Expenses");
  await panel.getByRole("button", { name: "Decide" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The journal is posted to the ledger");
  await page.locator("[data-tab-id='journals']").click();
  const register = page.getByRole("table", { name: "Journal register" });
  await expect(register.getByRole("row", { name: /JE-0005/ })).toContainText("Posted");
  await page.locator("[data-tab-id='trial']").click();
  await expect(page.locator("[data-trial]")).toContainText("In balance");
  await expect(page.locator("[data-trial]")).toContainText("1300");
  expect(await total.innerText()).not.toBe(before);
});

test("it cannot decide its own request, and the screen says so before a press", async ({ page, request }) => {
  await openApprovals(page, request);
  await page.locator(`[data-approval-id="${J2_REQUEST}"]`).click();
  const panel = page.locator("[data-decide-panel]");
  await expect(panel.locator("[data-own-notice]")).toContainText("You filed this request.");
  await expect(panel.locator("[data-own-notice]")).toContainText("General Manager");
  await expect(panel.locator("[data-choice-held='approve']")).toContainText("Held: not yours to decide.");
  await expect(panel.locator("[data-choice-held='decline']")).toContainText("Held: not yours to decide.");
  await expect(panel.getByRole("button", { name: "Decide" })).toBeDisabled();
  // Nothing was sent: the request is still in the queue.
  await expect(page.locator("[data-waiting]")).toHaveText("5 waiting");
});

test("it reads the trial balance in balance, with the server's totals", async ({ page, request }) => {
  await openApprovals(page, request);
  await page.locator("[data-tab-id='trial']").click();
  const trial = page.locator("[data-trial]");
  await expect(trial).toContainText("Trial balance");
  await expect(trial).toContainText("In balance");
  await expect(trial.getByRole("table", { name: "Trial balance rows" })).toContainText("Cash");
  await expect(page.locator("[data-total-debit]")).toHaveText("$37,025.00");
  await expect(page.locator("[data-total-credit]")).toHaveText("$37,025.00");
});

test("a refused decision names who may decide it, never the bare gate word", async ({ page, request }) => {
  await openApprovals(page, request);
  await page.route("**/api/v1/staff/approvals/*/decide", (route) =>
    route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) }),
  );
  await page.locator(`[data-approval-id="${FR1}"]`).click();
  await page.locator("[data-decide-panel]").getByRole("button", { name: "Decide" }).click();
  const alert = page.locator("[data-error]");
  await expect(alert).toContainText("Financial Controller");
  await expect(alert).toContainText("General Manager");
  await expect(alert).not.toContainText("Forbidden");
  // The request is still waiting.
  await expect(page.locator(`[data-approval-id="${FR1}"]`)).toHaveCount(1);
});
