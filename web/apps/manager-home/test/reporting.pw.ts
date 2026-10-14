// Manager Home, sub item 2B: the reporting view. Four panes over the core's
// twenty two reporting operations: the catalogue with its generated parameter
// form and its run, print and CSV hooks, the saved views, the frozen runs and
// their documents, the commission rules behind the admin wall, and the
// commission register with its accrual caveat in the server's own words.
import { test, expect } from "@playwright/test";
import { frontLineHandoff, managerHandoff, resetStub, useScenario } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

async function openReporting(page: import("@playwright/test").Page): Promise<void> {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator(".view-link", { hasText: "Reporting" }).click();
  await expect(page.getByRole("heading", { name: "Reporting" })).toBeVisible();
}

test("the landing draws the reporting link for a manager role, and the catalogue opens on the first template", async ({ page }) => {
  await openReporting(page);
  const tabs = page.locator(".kind-tab");
  await expect(tabs).toHaveCount(4);
  await expect(tabs.first()).toHaveAttribute("aria-selected", "true");
  await expect(page.locator(".nav-item").first()).toContainText("Sales by day");
  // The generated form: dates default to the month to date, the branch is a
  // select of the caller's own branches, never a uuid box.
  await expect(page.locator("input[data-param='from_date']")).not.toHaveValue("");
  await expect(page.locator("select[data-param='branch_id'] option")).toHaveText(["All branches", "Gable Landscape Supply · Point Loma"]);
});

test("a front-line role's landing draws no reporting link", async ({ page }) => {
  await page.addInitScript(frontLineHandoff);
  await page.goto("/");
  await expect(page.locator("[data-widget='needs.you']")).toBeVisible();
  await expect(page.locator(".view-link", { hasText: "Reporting" })).toHaveCount(0);
});

test("run fills the grid with the server's rows and its totals, and the summary names both", async ({ page }) => {
  await openReporting(page);
  await page.getByRole("button", { name: "Run", exact: true }).click();
  const grid = page.locator("[data-test='reporting-result']");
  await expect(grid).toBeVisible();
  await expect(grid.locator("tbody tr")).toHaveCount(2);
  await expect(grid.locator("tbody tr").first().locator("td").nth(4)).toContainText("$983.10");
  await expect(grid.locator("tfoot")).toContainText("$2,480.35");
  await expect(page.locator(".count-line").first()).toContainText("2 rows · Subtotal $2,195.00");
});

test("export takes the CSV as a download under the report's own name", async ({ page }) => {
  await openReporting(page);
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export CSV" }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toMatch(/^sales_by_day_\d{4}-\d{2}-\d{2}_\d{4}-\d{2}-\d{2}\.csv$/);
  await expect(page.locator("[data-test='reporting-notice']")).toContainText(`Exported ${download.suggestedFilename()}`);
});

test("print queues the job, opens the document at the paper, and the run lands in Runs", async ({ page }) => {
  await openReporting(page);
  // A report with no branch parameter set is a consolidated report: the
  // server refuses to queue its paper until the form names where it prints.
  await page.getByRole("button", { name: "Print" }).click();
  await expect(page.locator("[data-test='reporting-error']")).toHaveText(
    "this report covers every branch, name the branch to print it at",
  );
  await page.locator("[data-field='print-at']").selectOption({ label: "Gable Landscape Supply · Point Loma" });
  const popupPromise = page.waitForEvent("popup");
  await page.getByRole("button", { name: "Print" }).click();
  const popup = await popupPromise;
  // The document rides a blob into the window; the navigation lands a tick
  // after the window itself opens.
  await expect.poll(() => popup.url()).toMatch(/^blob:/);
  await expect(page.locator("[data-test='reporting-notice']")).toContainText("Print job queued");
  // The frozen run is on the runs pane, with its document one tap away.
  await page.locator(".kind-tab", { hasText: "Runs" }).click();
  await expect(page.locator(".run-row").first()).toContainText("Sales by day");
  await page.locator(".run-expand").first().click();
  await page.getByRole("button", { name: "Open the document" }).click();
  await expect(page.locator("[data-test='reporting-doc-notice']")).toContainText("Print job queued");
});

test("a saved view is stored under a name, applies, and deletes", async ({ page }) => {
  await openReporting(page);
  await page.locator("[data-field='save-name']").fill("The fortnight");
  await page.getByRole("button", { name: "Save view" }).click();
  const row = page.locator("[data-saved-view]", { hasText: "The fortnight" });
  await expect(row).toBeVisible();
  // Applying writes the stored parameters onto the form's fields: the view
  // holds the values it was saved under, and the overwritten field comes back.
  const savedTo = await page.locator("input[data-param='to_date']").inputValue();
  await page.locator("input[data-param='to_date']").fill("2026-10-31");
  await row.getByRole("button", { name: "Apply" }).click();
  await expect(page.locator("input[data-param='to_date']")).toHaveValue(savedTo);
  // A blank name is refused before anything is stored.
  await page.getByRole("button", { name: "Save view" }).click();
  await expect(page.locator("[data-test='reporting-error']").last()).toContainText("A saved view needs a name");
  await row.getByRole("button", { name: "Delete" }).click();
  await expect(page.locator("[data-saved-view]", { hasText: "The fortnight" })).toHaveCount(0);
});

test("a branch-scope refusal is the server's own sentence, verbatim", async ({ page, request }) => {
  await useScenario(request, "reporting-out-of-scope");
  await openReporting(page);
  await page.getByRole("button", { name: "Run", exact: true }).click();
  const error = page.locator("[data-test='reporting-error']");
  await expect(error).toHaveText("this report is outside your branch scope");
});

test("the rules pane reads for a manager and draws no write controls", async ({ page }) => {
  await openReporting(page);
  await page.locator(".kind-tab", { hasText: "Commission rules" }).click();
  const grid = page.locator("[data-test='reporting-rules']");
  await expect(grid).toBeVisible();
  await expect(grid.locator("tbody tr")).toHaveCount(1);
  await expect(grid.locator("tbody tr").first()).toContainText("Outside sales (demo)");
  await expect(grid.locator("tbody tr").first()).toContainText("2.50%");
  await expect(page.locator("[data-test='reporting-rule-form']")).toHaveCount(0);
});

test("the register previews the period, marks the line that earned nothing, and carries the server's own caveat", async ({ page }) => {
  await openReporting(page);
  await page.locator(".kind-tab", { hasText: "Accruals" }).click();
  await page.getByRole("button", { name: "Preview period" }).click();
  const periods = page.locator("[data-test='reporting-periods']");
  await expect(periods).toBeVisible();
  await expect(periods.locator("tbody tr").first()).toContainText("Outside sales (demo)");
  await expect(periods.locator("tbody tr").first()).toContainText("$57.75");
  // The caveat is the server's sentence, never this app's words.
  await expect(page.locator("[data-test='reporting-accrual-note']")).toContainText("Accrued, not posted.");
  await expect(page.locator("[data-test='reporting-unattributed']")).toContainText("$120.00 of revenue over 1 line");
  // Drill through: two paid lines and one marked, with its reason in words.
  await page.locator("[data-action='reporting-expand-period']").click();
  const line = page.locator("[data-line='GT-2118']");
  await expect(line).toHaveClass(/unpaid/);
  await expect(page.locator(".lines-table")).toContainText("no rule covers the EDGING category");
  // The register and the attribution check answer beside it.
  await expect(page.locator("[data-test='reporting-accruals'] tfoot")).toContainText("$57.75");
  await expect(page.locator("[data-test='reporting-attribution']")).toContainText("GT-2118");
});

test("compute freezes a rep's period and the stored run takes the CSV and the print", async ({ page }) => {
  await openReporting(page);
  await page.locator(".kind-tab", { hasText: "Accruals" }).click();
  await page.getByRole("button", { name: "Preview period" }).click();
  await expect(page.locator("[data-test='reporting-periods']")).toBeVisible();
  await page.locator("[data-action='reporting-compute']").click();
  await expect(page.locator("[data-test='reporting-notice']")).toContainText("Computed Outside sales (demo)");
  await expect(page.locator("[data-test='reporting-periods'] tbody tr").first()).toContainText("Accrued (stored)");
  // The stored run appears with its own hooks.
  await expect(page.locator("[data-test='reporting-cruns'] tbody tr").first()).toContainText("Lines");
  await expect(page.locator("[data-test='reporting-periods'] [data-action='reporting-crun-csv']").first()).toBeVisible();
});

test("a stored commission run prints at the commission pane's own branch, never the Templates print-at pick", async ({ page }) => {
  await openReporting(page);
  // The Templates pane has a print-at branch picked.
  await page.getByRole("button", { name: "Print" }).click();
  await page.locator("[data-field='print-at']").selectOption({ label: "Gable Landscape Supply · Point Loma" });
  await page.locator(".kind-tab", { hasText: "Accruals" }).click();
  await page.getByRole("button", { name: "Preview period" }).click();
  await expect(page.locator("[data-test='reporting-periods']")).toBeVisible();
  await page.locator("[data-action='reporting-compute']").click();
  await expect(page.locator("[data-test='reporting-notice']")).toContainText("Computed");
  // The period covers every branch and this pane names none, so the server's
  // own refusal answers; the other pane's pick sends the paper nowhere.
  await page.locator("[data-test='reporting-periods'] [data-action='reporting-crun-print']").first().click();
  await expect(page.locator("[data-test='reporting-error']")).toHaveText(
    "this commission period covers every branch, name the branch to print it at",
  );
});
