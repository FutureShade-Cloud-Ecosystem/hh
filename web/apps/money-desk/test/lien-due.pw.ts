// The Money Desk's Due to release screens (RT-3b), walked in a real browser on the stub: the list per customer job
// with its state in words, the Controller's release (asked first, the core's own sentence when it refuses), its
// reversal with a reason, the lien doors (a preserved lien, its discharge, the three dates), and the old job level
// press saying where the holdback is released now.
import type { APIRequestContext, Page } from "@playwright/test";
import { test, expect, openLiens, liensTab, noSidewaysScroll, CONTROLLER_ID, JOB5, dayFrom } from "./lien-helpers.js";

const due = (page: Page, lien: string) => page.locator(`[data-row="due"][data-lien-job="${lien}"]:visible`);

function token(sub: string): string {
  const part = (v: unknown) => Buffer.from(JSON.stringify(v), "utf8").toString("base64url");
  return `${part({ alg: "ES256" })}.${part({ sub, exp: 4102444800 })}.c3R1Yg`;
}

/** A call made behind the screen's back, as another person at another desk would make it. */
async function behind(request: APIRequestContext, method: "POST" | "PUT", path: string, data: unknown): Promise<void> {
  const res = await request.fetch(path, { method, data, headers: { Authorization: `Bearer ${token(CONTROLLER_ID)}` } });
  expect(res.ok(), `${method} ${path} answered ${res.status()}`).toBe(true);
}

async function openDue(page: Page, request: APIRequestContext, who: "controller" | "general_manager" = "controller"): Promise<void> {
  await openLiens(page, request, who);
  await liensTab(page, "due");
  await expect(page.locator("[data-row='due']:visible").first()).toBeVisible();
}

test("item 1: due to release lists each customer job with its invoices, its holdback, the governing lien job, the last day and the state in words", async ({ page, request }) => {
  await openDue(page, request);
  await expect(page.locator("[data-row='due']:visible")).toHaveCount(3);
  const ready = due(page, "GLS-2100");
  await expect(ready).toContainText("Due now");
  await expect(ready).toContainText("INV-4101");
  await expect(ready).toContainText("$8,400.00");
  await expect(ready).toContainText("INV-4102");
  await expect(ready).toContainText("$10,200.00");
  await expect(ready.locator("[data-last-day]")).not.toHaveText("");
  const waiting = due(page, "GLS-2103");
  await expect(waiting).toContainText(/Not due until/);
  await expect(waiting).toContainText("The holdback is not due: lien job GLS-2103 counts 45 days from");
  await expect(waiting.getByRole("button", { name: "Release" })).toHaveCount(0);
  const none = due(page, "");
  await expect(none).toContainText("No lien job linked");
  await expect(none).toContainText("No lien job is linked to this job, so there is no preservation period to count.");
  // The server's own total of what is due, by branch.
  await expect(page.locator("[data-due-total]")).toContainText("$18,600.00");
  await expect(page.locator("[data-tab-id='due'] .li-count")).toHaveText("1");
});

test("item 2: the Controller is asked first, naming the job, the invoices and each amount; the release names its rows; the General Manager is shown no button", async ({ page, request }) => {
  await openDue(page, request);
  await due(page, "GLS-2100").getByRole("button", { name: "Release" }).click();
  const panel = page.locator("[data-due-panel]");
  await expect(panel).toBeFocused();
  await expect(panel.locator("[data-confirm-words]")).toContainText("lien job GLS-2100");
  await expect(panel.locator("[data-confirm-words]")).toContainText("INV-4101 ($8,400.00)");
  await expect(panel.locator("[data-confirm-words]")).toContainText("INV-4102 ($10,200.00)");
  await panel.getByRole("button", { name: "Cancel" }).click();
  await expect(page.locator("[data-due-panel]")).toHaveCount(0);
  await due(page, "GLS-2100").getByRole("button", { name: "Release" }).click();
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Released $18,600.00 on lien job GLS-2100: 2 invoices.");
  await expect(due(page, "GLS-2100")).toHaveCount(0);
  const rows = page.locator("[data-row='released']:visible");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("INV-4101");
  await expect(rows.nth(0)).toContainText("$8,400.00");
  await noSidewaysScroll(page);
});

test("item 2: what the core refuses is said in the core's sentence, and the list is read again", async ({ page, request }) => {
  await openDue(page, request);
  await due(page, "GLS-2100").getByRole("button", { name: "Release" }).click();
  await behind(request, "POST", `/api/v1/lien/jobs/${JOB5}/preserved-lien`, { preserved_on: dayFrom(-1), note: "filed while the screen was open" });
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-error]")).toContainText(`A lien was preserved on lien job GLS-2100 on ${dayFrom(-1)} and has not been discharged, so the holdback stays held until it is discharged or settled.`);
  await expect(due(page, "GLS-2100")).toContainText("Blocked by a preserved lien on GLS-2100");
  await expect(due(page, "GLS-2100").getByRole("button", { name: "Release" })).toHaveCount(0);
});

test("item 2: the General Manager reads the list but is shown no Release button, and says who releases", async ({ page, request }) => {
  await openDue(page, request, "general_manager");
  await expect(due(page, "GLS-2100")).toContainText("Due now");
  await expect(due(page, "GLS-2100").getByRole("button", { name: "Release" })).toHaveCount(0);
  await expect(page.locator("[data-due-note]")).toContainText("Releasing a due holdback is for the Financial Controller.");
});

test("item 3: a release is reversed with a reason, asked first, and a release reversed elsewhere is said to be reversed already", async ({ page, request }) => {
  await openDue(page, request);
  await due(page, "GLS-2100").getByRole("button", { name: "Release" }).click();
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-row='released']:visible")).toHaveCount(2);
  const first = page.locator("[data-row='released'][data-invoice-no='INV-4101']:visible");
  await first.getByRole("button", { name: "Reverse" }).click();
  const panel = page.locator("[data-reverse-panel]");
  await expect(panel).toContainText("INV-4101");
  await expect(panel).toContainText("$8,400.00");
  await panel.getByRole("button", { name: "Reverse release" }).click();
  await expect(page.locator("[data-error]")).toContainText("Say why the release is reversed.");
  await panel.getByLabel("Reason").fill("Released against the wrong job");
  await panel.getByRole("button", { name: "Reverse release" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Reversed the release of INV-4101: $8,400.00 is held back again.");
  await expect(first).toContainText("Reversed");
  await expect(first.getByRole("button", { name: "Reverse" })).toHaveCount(0);
  await expect(due(page, "GLS-2100")).toContainText("INV-4101");
  // The other invoice is reversed at another desk; this screen's press hears the core's sentence.
  const second = page.locator("[data-row='released'][data-invoice-no='INV-4102']:visible");
  const id = (await second.getAttribute("data-release-id")) ?? "";
  await second.getByRole("button", { name: "Reverse" }).click();
  await behind(request, "POST", `/api/v1/ar/holdback/releases/${id}/reversal`, { reason: "reversed at the next desk" });
  await page.locator("[data-reverse-panel]").getByLabel("Reason").fill("Same mistake");
  await page.locator("[data-reverse-panel]").getByRole("button", { name: "Reverse release" }).click();
  await expect(page.locator("[data-error]")).toContainText("That release has already been reversed.");
  await expect(second).toContainText("Reversed");
});

test("item 4: a preserved lien is recorded and discharged on a lien job, and the list follows", async ({ page, request }) => {
  await openLiens(page, request);
  await liensTab(page, "jobs");
  await page.locator("[data-row='job'][data-job-no='GLS-2100']:visible").getByRole("button", { name: "Open" }).click();
  const doors = page.locator("[data-doors]");
  await doors.getByRole("button", { name: "Record preserved lien" }).click();
  await expect(page.locator("[data-error]")).toContainText("Give the day the lien was preserved, written yyyy-mm-dd.");
  await doors.getByLabel("Lien preserved on").fill(dayFrom(-2));
  await doors.getByLabel("Note", { exact: true }).fill("Filed at the registry");
  await doors.getByRole("button", { name: "Record preserved lien" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Recorded: a lien was preserved on lien job GLS-2100.");
  await expect(doors.locator("[data-lien-state]")).toContainText("A lien was preserved on");
  await expect(doors.locator("[data-lien-state]")).toContainText("Filed at the registry");
  await liensTab(page, "due");
  await expect(due(page, "GLS-2100")).toContainText("Blocked by a preserved lien on GLS-2100");
  await liensTab(page, "jobs");
  await doors.getByLabel("Discharged on").fill(dayFrom(-5));
  await doors.getByRole("button", { name: "Record discharge" }).click();
  await expect(page.locator("[data-error]")).toContainText("cannot be discharged on");
  await doors.getByLabel("Discharged on").fill(dayFrom(0));
  await doors.getByRole("button", { name: "Record discharge" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Recorded: the lien on lien job GLS-2100 was discharged.");
  await liensTab(page, "due");
  await expect(due(page, "GLS-2100")).toContainText("Due now");
});

test("item 4: the three dates are set, corrected and cleared, and a job with none says it has no date to count from", async ({ page, request }) => {
  await openLiens(page, request);
  await liensTab(page, "jobs");
  await page.locator("[data-row='job'][data-job-no='GLS-2103']:visible").getByRole("button", { name: "Open" }).click();
  const doors = page.locator("[data-doors]");
  await doors.getByRole("button", { name: "Save dates" }).click();
  await expect(page.locator("[data-error]")).toContainText("Change at least one date.");
  await doors.getByLabel("Certificate published on").fill(dayFrom(-3));
  await doors.getByLabel("Substantial performance date").fill("");
  await doors.getByLabel("Last supply date").fill("");
  await doors.getByRole("button", { name: "Save dates" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Saved the dates of lien job GLS-2103.");
  await expect(page.locator("[data-job-panel]")).toContainText("Certificate published");
  await liensTab(page, "due");
  await expect(due(page, "GLS-2103")).toContainText("Not due until");
  await liensTab(page, "jobs");
  await doors.getByLabel("Certificate published on").fill("");
  await doors.getByRole("button", { name: "Save dates" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Saved the dates of lien job GLS-2103.");
  await liensTab(page, "due");
  await expect(due(page, "GLS-2103")).toContainText("No date to count from");
});

test("item 5: the old job level press on a job whose holdback is held on invoices says so in words and points to Due to release", async ({ page, request }) => {
  await openLiens(page, request, "general_manager");
  await liensTab(page, "holdbacks");
  await page.locator("[data-row='aging'][data-job-no='GLS-2103']:visible").getByRole("button", { name: "Release" }).click();
  await page.getByLabel("Co-signer").selectOption({ label: "Financial Controller (demo)" });
  await page.getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-error]")).toContainText("Job GLS-2103 is linked to a customer job, so its holdback is released from Due to release by the Financial Controller, once its preservation period has run out, and not here.");
  await page.locator("[data-job-level-refused]").getByRole("button", { name: "Go to Due to release" }).click();
  await expect(page.locator("[data-tab-id='due']")).toHaveAttribute("aria-current", "true");
});
