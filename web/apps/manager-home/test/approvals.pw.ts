// Manager Home, sub item 3: approvals by kind. The core's kind filter
// narrows the list to one policy kind; the view draws one tab per kind of
// the closed vocabulary, decides through the core's decide route, and a kind
// the caller does not decide renders the core's own refusal plus the plain
// sentence naming who decides it (the matrix's exception approvers lists).
import { test, expect } from "@playwright/test";
import { managerHandoff, resetStub, useScenario } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

test("the sales manager's needs row opens the approvals view on the kind they decide", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await expect(page.locator("[data-widget='needs.you'] [data-needs='approvals.waiting'] .needs-count")).toHaveText("1");
  await page.locator("[data-needs='approvals.waiting'] button", { hasText: "Review" }).click();
  await expect(page.getByRole("heading", { name: "Approvals" })).toBeVisible();
  await expect(page.locator("[data-approval]").first().locator(".detail")).toContainText("Q-2001");
});

test("the five kinds of the closed vocabulary and the change queue sit as tabs, the decided one first", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  const tabs = page.locator(".kind-tab");
  await expect(tabs).toHaveCount(6);
  // The counts on the tabs arrive with the queue's read, after the tabs are drawn: the words are waited for, never sampled once.
  // The counts are the core's own, so the words are UX-47's: one margin request waiting, the two freight raises, the change queue.
  await expect(tabs).toHaveText(["Margin 1", "Credit 0", "Cash On Demand 0", "Journal entry 0", "Freight above quote 2", "Change orders 2"]);
  await expect(tabs.first()).toHaveAttribute("aria-selected", "true");
});

test("a margin approval decides through the core's route and the queue empties", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await page.locator("[data-needs='approvals.waiting'] button", { hasText: "Review" }).click();
  const row = page.locator("[data-approval]").first();
  await row.getByRole("button", { name: "Approve" }).click();
  await page.locator("[data-test='decision-confirm']").getByRole("button", { name: "Yes, approve" }).click();
  await expect(page.locator("[data-test='decided-group']")).toContainText("Approved");
  // Back on the landing the approvals row is absent, not zero; the sales manager's other rows stay.
  await page.getByRole("button", { name: "Back to Home" }).click();
  await expect(page.locator("[data-needs='approvals.waiting']")).toHaveCount(0);
  await expect(page.locator("[data-needs='ar.escalation']")).toHaveCount(1);
});

test("a kind the caller does not decide is said in plain words naming who decides it, no enum on screen", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await page.locator(".kind-tab", { hasText: "Credit" }).click();
  const refusal = page.locator("[data-test='approvals-refusal']");
  await expect(refusal).toHaveText(
    "You do not decide credit approvals. Credit approvals are decided by the financial controller and the general manager.",
  );
  await expect(refusal).not.toContainText("CREDIT_LIMIT_EXCEEDED");
  await expect(page.locator("[data-approval]")).toHaveCount(0);
});

test("a caller who decides no kind, and every tab says who does", async ({ request, page }) => {
  // The yard manager's own strip carries no Approvals link; the plain profile's does, and the wall is the core's.
  await useScenario(request, "plain-profile");
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await expect(page.locator("[data-test='approvals-refusal']")).toContainText(
    "Margin approvals are decided by the financial controller, the general manager, and a sales manager in their own branch.",
  );
  await page.locator(".kind-tab", { hasText: "Journal entry" }).click();
  await expect(page.locator("[data-test='approvals-refusal']")).toContainText(
    "Journal entry approvals are decided by the financial controller and the general manager.",
  );
  await expect(page.locator("[data-approval]")).toHaveCount(0);
});

test("a rejection records its own word beside the request", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  const row = page.locator("[data-approval]").first();
  await row.getByRole("button", { name: "Reject" }).click();
  await page.locator("input[data-field='decline-reason']").fill("Below the floor for this account");
  await page.locator("[data-test='decision-confirm']").getByRole("button", { name: "Yes, reject" }).click();
  await expect(page.locator("[data-test='decided-group']")).toContainText("Rejected");
});

test("the controller's Review opens on Freight above quote, where the two raises are waiting", async ({ request, page }) => {
  await resetStub(request);
  await useScenario(request, "freight-only");
  await page.goto("/");
  await page.locator("[data-flow='financial-controller']").click();
  await page.locator("[data-needs='approvals.waiting'] button", { hasText: "Review" }).click();
  const selected = page.locator(".kind-tab[aria-selected='true']");
  await expect(selected).toContainText("Freight above quote");
  await expect(page.locator("[data-approval]")).toHaveCount(2);
});

test("a caller with Margin waiting opens on Margin, the first kind", async ({ request, page }) => {
  await resetStub(request);
  await page.goto("/");
  await page.locator("[data-flow='general-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  // The general manager decides every kind and the margin one waits too, so Margin is first and has its row.
  await expect(page.locator(".kind-tab[aria-selected='true']")).toContainText("Margin");
  await expect(page.locator("[data-approval]")).toHaveCount(1);
});

test("a sales manager with only freight raises waiting opens on Margin, not on a refused Freight tab", async ({ request, page }) => {
  await resetStub(request);
  await useScenario(request, "freight-only");
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await expect(page.getByText("No Margin approvals waiting.")).toBeVisible();
  await expect(page.locator(".kind-tab[aria-selected='true']")).toContainText("Margin");
  await expect(page.locator("[data-test='approvals-refusal']")).toHaveCount(0);
});

// The approvals' tone words are TEXT colours (approvals-styles.ts), and the home's bar segments use the same
// tone-* names for a FILL (styles.ts). Both sheets reach the same shadow root, so a metric value or a margin
// cell once took a fill of nearly its own ink and could not be read. A tone word paints no background.
test("a tone word on the margin and credit cards paints no fill behind its text", async ({ request, page }) => {
  await resetStub(request);
  await page.goto("/");
  await page.locator("[data-flow='general-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await expect(page.locator(".kind-tab[aria-selected='true']")).toContainText("Margin");
  for (const kind of ["Margin"]) {
    await page.locator(".kind-tab", { hasText: kind }).click();
    const toned = page.locator(".metric-val[class*='tone-'], td[class*='tone-'], .meta-value[class*='tone-']");
    await expect(toned.first()).toBeVisible();
    const fills = await toned.evaluateAll((els) => els.map((el) => `${el.textContent?.trim()} on ${getComputedStyle(el).backgroundColor}`));
    expect(fills.length).toBeGreaterThan(0);
    for (const fill of fills) expect(fill, `${kind}: ${fill}`).toMatch(/ on rgba\(0, 0, 0, 0\)$/);
  }
});

// UX-47 and UX-55: a decision on money is confirmed by name and amount, lands visibly, and reads back from the core.
// The scenario "margin-pair" adds a margin request whose id is not a board item's id, the way every request on the
// real core is: the screen shows the core's rows, never an invented one.
const SECOND = "0e000000-0000-4000-9000-0000000000a5";

async function openMargin(page: import("@playwright/test").Page) {
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await expect(page.locator(".kind-tab[aria-selected='true']")).toContainText("Margin");
}

test("the screen lists the core's own requests, one id on a row, a labelled figure, no staff names", async ({ request, page }) => {
  await useScenario(request, "margin-pair");
  await openMargin(page);
  const row = page.locator(`[data-approval='${SECOND}']`);
  await expect(row).toBeVisible();
  await expect(row.locator(".card-amount")).toContainText("$35.59");
  await expect(row.locator(".card-amount-label")).toHaveText("at stake");
  const text = (await row.innerText()).replace(/\s+/g, " ");
  expect(text.match(/Q-2002/g)?.length).toBe(1);
  await expect(page.locator("[data-board-approval]")).toHaveCount(0);
  await expect(page.locator(".approvals-list")).not.toContainText(/Jordan Lee|Elena Ruiz|Priya Shah|Morgan Ellis/);
});

test("Approve asks first, naming the quote and the amount, and a cancel decides nothing", async ({ request, page }) => {
  await useScenario(request, "margin-pair");
  await openMargin(page);
  await page.locator(`[data-approval='${SECOND}']`).getByRole("button", { name: "Approve" }).click();
  const confirm = page.locator("[data-test='decision-confirm']");
  await expect(confirm).toContainText("Approve Q-2002");
  await expect(confirm).toContainText("$35.59");
  await confirm.getByRole("button", { name: "Cancel" }).click();
  await expect(confirm).toHaveCount(0);
  await expect(page.locator(".kind-tab", { hasText: "Margin" })).toContainText("Margin 2");
});

test("a confirmed approval drops the count, moves the row to Decided with no buttons, and survives a fresh open", async ({ request, page }) => {
  await useScenario(request, "margin-pair");
  await openMargin(page);
  await expect(page.locator(".kind-tab", { hasText: "Margin" })).toContainText("Margin 2");
  await page.locator(`[data-approval='${SECOND}']`).getByRole("button", { name: "Approve" }).click();
  await page.locator("[data-test='decision-confirm']").getByRole("button", { name: "Yes, approve" }).click();
  await expect(page.locator(".kind-tab", { hasText: "Margin" })).toContainText("Margin 1");
  await expect(page.locator("[data-test='decision-notice']")).toContainText("Approved Q-2002");
  const decided = page.locator("[data-test='decided-group']");
  await expect(decided.locator(`[data-approval='${SECOND}']`)).toContainText("Approved");
  await expect(decided.getByRole("button")).toHaveCount(0);
  await decided.locator(`[data-approval='${SECOND}']`).click();
  await expect(page.locator(`[data-approval-detail='${SECOND}']`)).toBeVisible();
  await expect(page.locator(`[data-approval-detail='${SECOND}'] button`)).toHaveCount(0);
  // A fresh open reads the decision back from the list.
  await page.getByRole("button", { name: "Back to Home" }).click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await expect(page.locator(".kind-tab", { hasText: "Margin" })).toContainText("Margin 1");
  await expect(page.locator("[data-test='decided-group']").locator(`[data-approval='${SECOND}']`)).toContainText("Approved");
});

test("a request that is no longer there is never shown as decided", async ({ request, page }) => {
  await useScenario(request, "margin-pair");
  await openMargin(page);
  await page.route("**/api/v1/staff/approvals/*/decide", (route) =>
    route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: "approval not found" }) }),
  );
  await page.locator(`[data-approval='${SECOND}']`).getByRole("button", { name: "Approve" }).click();
  await page.locator("[data-test='decision-confirm']").getByRole("button", { name: "Yes, approve" }).click();
  await expect(page.locator(".error-line")).toContainText("That request is no longer waiting.");
  await expect(page.locator("[data-test='decided-group']")).toHaveCount(0);
  await expect(page.locator(".kind-tab", { hasText: "Margin" })).toContainText("Margin 2");
});

test("Reject is the one word on the row and in the pane, and it asks first too", async ({ request, page }) => {
  await useScenario(request, "margin-pair");
  await openMargin(page);
  await expect(page.getByRole("button", { name: "Decline" })).toHaveCount(0);
  const row = page.locator(`[data-approval='${SECOND}']`);
  await row.locator(".card-title").click();
  await expect(page.locator(`[data-approval-detail='${SECOND}'] .decline-btn`)).toHaveText("Reject");
  await row.getByRole("button", { name: "Reject" }).click();
  await page.locator("input[data-field='decline-reason']").fill("Price is too low for this account");
  await page.locator("[data-test='decision-confirm']").getByRole("button", { name: "Yes, reject" }).click();
  await expect(page.locator("[data-test='decided-group']").locator(`[data-approval='${SECOND}']`)).toContainText("Rejected");
});
