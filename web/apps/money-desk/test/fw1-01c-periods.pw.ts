// FW1-01c: the Controller's two Periods tab controls, walked in a real browser on the stub.
// The early open of the next fiscal year is a review that names what it changes and asks for a
// reason, and the late posted list says each journal's document date, the month it landed in and
// the way to the journal itself.
import { test, expect, openApprovals, openReader, openTab } from "./gl-helpers.js";

test("the next fiscal year opens with a reason, and the year joins the periods", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "periods");
  const periods = page.getByRole("table", { name: "Periods" });
  await expect(periods).toContainText("August 2026");
  await expect(periods).not.toContainText("January 2028");

  // The control opens a review that names what it changes; nothing is sent until the review's own press.
  await page.getByRole("button", { name: "Open fiscal year 2028" }).first().click();
  const confirm = page.locator("[data-confirm]");
  await expect(confirm).toContainText("Open fiscal year 2028?");
  await expect(confirm).toContainText("The twelve monthly periods of fiscal year 2028 open for posting");
  await expect(confirm).toContainText("Your reason is kept with the open");
  await expect(confirm).toBeFocused();
  await confirm.getByRole("button", { name: "Keep it as it is" }).click();
  await expect(page.locator("[data-confirm]")).toHaveCount(0);
  await expect(periods).not.toContainText("January 2028");

  // A reason is required: empty is refused in words without a request.
  await page.getByRole("button", { name: "Open fiscal year 2028" }).first().click();
  await page.locator("[data-confirm]").getByRole("button", { name: "Open fiscal year 2028" }).click();
  await expect(page.locator("[data-confirm-error]")).toContainText("A reason is required");
  await page.locator('[data-field="reason"]').fill("The year is set up early");
  await page.locator("[data-confirm]").getByRole("button", { name: "Open fiscal year 2028" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Fiscal year 2028 is open");
  await expect(page.locator("[data-notice]")).toContainText("Your reason is on record");
  await expect(periods).toContainText("January 2028");
  await expect(page.locator('[data-action="gl-open-year"]')).toHaveCount(0);
});

test("an open year refused at the gate names who may, never the bare gate word", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "periods");
  await page.route("**/api/v1/gl/periods/open-year", (route) =>
    route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) }),
  );
  await page.getByRole("button", { name: "Open fiscal year 2028" }).first().click();
  await page.locator('[data-field="reason"]').fill("The year is set up early");
  await page.locator("[data-confirm]").getByRole("button", { name: "Open fiscal year 2028" }).click();
  const error = page.locator("[data-confirm-error]");
  await expect(error).toContainText("To open the next fiscal year is for the Financial Controller");
  await expect(error).not.toContainText("Forbidden");
  await expect(page.getByRole("table", { name: "Periods" })).not.toContainText("January 2028");
});

test("the late posted list says the document date, the month it landed in, and opens the journal", async ({ page, request }) => {
  await openApprovals(page, request);
  await openTab(page, "periods");
  const late = page.getByRole("table", { name: "Late posted journals" });
  await expect(late).toContainText("JE-0002");
  await expect(late).toContainText("Jul 18, 2026");
  await expect(late).toContainText("August 2026");
  await expect(late).toContainText("July invoice booked in August");
  // The way to the journal takes the Controller to it in the register.
  await late.getByRole("button", { name: "Open JE-0002" }).click();
  await expect(page.locator("[data-tab-id='journals']")).toHaveAttribute("aria-current", "true");
  await expect(page.locator("[data-journal-panel]")).toContainText("JE-0002");
});

test("a reader's Periods tab has neither control, and says who may", async ({ page, request }) => {
  await openReader(page, request);
  await openTab(page, "periods");
  await expect(page.getByRole("table", { name: "Periods" })).toContainText("August 2026");
  await expect(page.getByRole("button", { name: "Open fiscal year 2028" })).toHaveCount(0);
  await expect(page.locator("[data-late-posted]")).toHaveCount(0);
  await expect(page.locator("[data-held]").first()).toContainText("for the Financial Controller");
});
