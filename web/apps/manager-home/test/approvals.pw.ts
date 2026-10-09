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
  await expect(page.locator("[data-approval]").first().locator(".detail")).toContainText("Quote Q-2001");
});

test("the five kinds of the closed vocabulary sit as tabs, the decided one first", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  const tabs = page.locator(".kind-tab");
  await expect(tabs).toHaveCount(5);
  const words = await tabs.evaluateAll((nodes) => nodes.map((n) => n.textContent?.trim()));
  expect(words).toEqual(["Margin", "Credit", "Cash on delivery", "Journal entry", "Freight above quote"]);
  await expect(tabs.first()).toHaveAttribute("aria-selected", "true");
});

test("a margin approval decides through the core's route and the queue empties", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await page.locator("[data-needs='approvals.waiting'] button", { hasText: "Review" }).click();
  const row = page.locator("[data-approval]").first();
  await row.getByRole("button", { name: "Approve" }).click();
  await expect(row.locator(".meta")).toContainText("Approved");
  // Back on the landing the queue is empty: the row is absent, not zero.
  await page.getByRole("button", { name: "Back to Home" }).click();
  await expect(page.locator("[data-needs='approvals.waiting']")).toHaveCount(0);
  await expect(page.locator("[data-widget='needs.you'] .needs-empty")).toContainText("Nothing is waiting on you right now.");
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

test("the yard manager decides no kind, and every tab says who does", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await expect(page.locator("[data-test='approvals-refusal']")).toContainText(
    "Margin approvals are decided by the financial controller, the general manager, and a sales manager or branch manager in their own branch.",
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
  await expect(row.locator(".meta")).toContainText("Rejected");
});
