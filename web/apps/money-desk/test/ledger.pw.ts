// The Financial Controller's legs on the Money Desk (roadmap 2.9, the first
// increment), walked in a real browser on the stub: the card lands on the
// approvals queue; the controller decides the seeded floor request and the
// approval is spent on that one quote; it approves the parked journal and the
// trial balance carries it; it cannot decide its own request, and the screen
// says so in words before a press; it reads the trial balance; and a refused
// decision names who may decide it.
import { test, expect, openApprovals, controllerHandoff } from "./gl-helpers.js";

const FR1 = "2f6a1c00-0000-4000-9000-0000000000f1";
const J1_REQUEST = "2f6a1c00-0000-4000-9000-0000000000a1";
const J2_REQUEST = "2f6a1c00-0000-4000-9000-0000000000a2";

test("the demo way in offers the controller's card and lands on the approvals queue", async ({ page, request }) => {
  await request.post("/_stub/reset");
  await page.goto("/");
  await expect(page.locator("[data-flow]")).toHaveCount(2);
  await page.locator("[data-flow='financial-controller']").click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Approvals");
  // The controller also holds the receivables desk: one tap, and the other slice is whole.
  await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await page.getByRole("button", { name: "Approvals and ledger" }).click();
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
  // Each journal is named by its number, so two parked journals are told apart.
  await expect(page.locator(`[data-approval-id="${J1_REQUEST}"]`)).toContainText("JE-0005");
  await expect(page.locator(`[data-approval-id="${J2_REQUEST}"]`)).toContainText("JE-0006");
  await expect(page.locator(`[data-approval-id="${J2_REQUEST}"]`)).toContainText("Filed by you");
  await expect(page.locator(`[data-approval-id="${J1_REQUEST}"]`)).not.toContainText("Filed by you");
});

test("it decides the floor request, and the answer says the approval is spent on that one quote", async ({ page, request }) => {
  await openApprovals(page, request);
  await page.locator(`[data-approval-id="${FR1}"]`).click();
  const panel = page.locator("[data-decide-panel]");
  await expect(panel).toContainText("Decide: Below floor quote request");
  // In words with the figures as tiles: no line id, no "bps".
  await expect(panel.locator("[data-detail]")).toContainText("a 12% discount asked");
  await expect(panel.locator("[data-floor-tiles]")).toContainText("16.3%");
  await expect(panel.locator("[data-floor-tiles]")).toContainText("20.0%");
  await expect(panel).not.toContainText("bps");
  await expect(panel).not.toContainText("62e4ad55");
  await expect(panel.getByRole("radio", { name: /Approve as asked/ })).toHaveAttribute("aria-checked", "true");
  await expect(panel.getByRole("radio", { name: /Decline/ })).toHaveAttribute("aria-checked", "false");
  await expect(panel.locator("[data-choice-later]")).toContainText("later");
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
  await expect(panel).toContainText("Decide: journal JE-0005");
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
  // The branch is chosen from the route's own branch_id, and what the core does not draw yet says so.
  await page.getByRole("button", { name: /^Julian/ }).click();
  await expect(page.locator("[data-trial]")).toContainText("No posted journals yet.");
  await page.getByRole("button", { name: "All branches" }).click();
  await expect(page.locator("[data-total-debit]")).toHaveText("$37,025.00");
  await expect(page.locator("[data-later-trial]")).toContainText("tie out");
  await expect(page.locator("[data-later-trial] .gl-later")).toHaveText("later");
});

test("the journal register keeps a journal number on one line and offers void and reverse where they apply", async ({ page, request }) => {
  await openApprovals(page, request);
  await page.locator("[data-tab-id='journals']").click();
  await expect(page.getByRole("button", { name: /^Pending 2$/ })).toBeVisible();
  const link = page.getByRole("table", { name: "Journal register" }).getByRole("button", { name: "JE-0006" });
  const box = await link.boundingBox();
  expect(box === null ? 99 : box.height).toBeLessThan(40);
  await link.click();
  // A parked draft can be voided; a journal a person posted can only be reversed; a journal the till
  // posted is not reversed here, and the panel names the till.
  await expect(page.getByRole("button", { name: "Void", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Reverse", exact: true })).toHaveCount(0);
  await page.getByRole("table", { name: "Journal register" }).getByRole("button", { name: "JE-0004" }).click();
  await expect(page.getByRole("button", { name: "Reverse", exact: true })).toHaveCount(0);
  await expect(page.locator("[data-system-note]")).toContainText("the till");
  await page.getByRole("table", { name: "Journal register" }).getByRole("button", { name: "JE-0001" }).click();
  await expect(page.getByRole("button", { name: "Reverse", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Void", exact: true })).toHaveCount(0);
});

test("on a phone a chosen request brings its decision into view", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openApprovals(page, request);
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
  await page.locator(`[data-approval-id="${FR1}"]`).click();
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(300);
  await expect(page.locator("[data-decide-panel]")).toBeInViewport();
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
  await expect(alert).toContainText("Sign out and in again");
  await expect(alert).not.toContainText("Forbidden");
  // The request is still waiting.
  await expect(page.locator(`[data-approval-id="${FR1}"]`)).toHaveCount(1);
});

test("a bare array where the contract says a page is refused, never half read", async ({ page, request }) => {
  await request.post("/_stub/reset");
  await page.addInitScript(controllerHandoff);
  await page.route("**/api/v1/staff/approvals?*", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([]) }),
  );
  await page.goto("/");
  await expect(page.locator("[data-error]")).toContainText("The approvals list was not readable.");
  await expect(page.locator("[data-approval-id]")).toHaveCount(0);
});

test("on a phone the two area buttons share a row and the ledger tabs share a row", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openApprovals(page, request);
  const tops = async (selector: string): Promise<number[]> =>
    page.locator(selector).evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().top)));
  expect(new Set(await tops("nav.areas button")).size).toBe(1);
  expect(new Set(await tops("[data-tab-id]")).size).toBe(1);
});

test("the frame does not jump when the controller moves between the ledger and receivables", async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openApprovals(page, request);
  const left = async (selector: string): Promise<number> => Math.round((await page.locator(selector).first().boundingBox())?.x ?? -1);
  const ledger = { switch: await left("nav.areas button"), content: await left("main h1") };
  await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
  const receivables = { switch: await left("nav.areas button"), content: await left("main h1") };
  expect(receivables).toEqual(ledger);
});

test("one dead lettered journal planted through the stub shows the posting queue banner with its words, and none shows no banner", async ({ page, request }) => {
  await openApprovals(page, request);
  await expect(page.locator("[data-posting-queue]")).toHaveCount(0);
  await request.post("/_stub/posting-queue", {
    data: {
      items: [
        {
          event_id: "d0560000-0000-4000-9000-0000000000f3",
          topic: "ar.invoice.finalized",
          document: "d0560000-0000-4000-9000-0000000000f4",
          status: "DEAD",
          attempts: 5,
          last_error: "gl: account \"2200\" is inactive; cannot post",
          occurred_at: "2026-03-12T11:00:00Z",
        },
        {
          event_id: "d0560000-0000-4000-9000-0000000000f5",
          topic: "payment.received",
          document: "d0560000-0000-4000-9000-0000000000f6",
          status: "PENDING",
          attempts: 0,
          last_error: "",
          occurred_at: new Date().toISOString(),
        },
      ],
    },
  });
  await page.reload();
  await expect(page.locator("[data-posting-queue]")).toHaveText(
    "1 journal failed to post and is set aside. The posting queue lists them for the Financial Controller.",
  );
  await request.post("/_stub/posting-queue", { data: { items: [] } });
  await page.reload();
  await expect(page.locator("[data-posting-queue]")).toHaveCount(0);
});

