// The procurement slice walked in a real browser against the stub: the card
// lands on the Buying and Stock home and shows the K08 gap, a PO is drafted from
// the shortfall, placed, and seen incoming, and the walled moves are refused in
// words. The stub is held to the core's shapes and sentences by
// test/stub-contract.test.mjs.
import type { Page } from "@playwright/test";
import { test, expect, openHome, resetStub } from "./helpers.js";

const gapRow = (page: Page) => page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" });

async function draftTheGap(page: Page): Promise<void> {
  // The K08 row is already picked on landing; one press drafts it.
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  await expect(page.locator("[data-draft]")).toBeVisible();
}

test("the way in with no handoff offers this role's own card", async ({ page, request }) => {
  await resetStub(request);
  await page.goto("/");
  await expect(page.locator("[data-flow]")).toHaveCount(1);
  await expect(page.locator("[data-flow]")).toContainText("Procurement manager");
});

test("the header names the role as the reference does, and the theme control is a small button", async ({ page, request }) => {
  await openHome(page, request);
  const header = page.locator("hos-app-header");
  await expect(header).toContainText("Procurement manager (demo)");
  const theme = page.getByRole("button", { name: /^Theme: / });
  await expect(theme).toHaveText("Theme: Light");
  const box = await theme.boundingBox();
  expect(box?.height ?? 99).toBeLessThanOrEqual(40);
});

test("the card lands on the home and the K08 gap is the hot, picked row of the worklist", async ({ page, request }) => {
  await openHome(page, request);
  await expect(page.getByRole("heading", { name: "Buying and Stock", level: 1 })).toBeVisible();
  await expect(page.locator("[data-shortfall-row]")).toHaveCount(3);
  const gap = page.locator("[data-shortfall-row][data-hot]");
  await expect(gap).toHaveCount(1);
  await expect(gap).toContainText("DBS-BRUS-DIM");
  await expect(gap).toContainText("-40");
  await expect(gap).toContainText("240");
  await expect(gap.getByRole("checkbox")).toHaveAttribute("aria-checked", "true");
  await expect(page.locator("[data-k08-note]")).toContainText("K08");
  await expect(page.getByRole("button", { name: "Draft PO (1)" })).toBeEnabled();
  await expect(page.locator("[data-tile='below-point']")).toContainText("3");
  await expect(page.locator("[data-po-row]")).toHaveCount(4);
});

test("the worklist shows every column the read carries, with the vendor the draft will go to", async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHome(page, request);
  const heads = (await page.locator("[data-worklist] th").allTextContents()).map((t) => t.trim());
  for (const name of ["SKU", "Product", "On hand", "Reserved", "In transit", "On order", "Projected", "Point", "Short by", "Suggest", "Vendor"]) {
    expect(heads).toContain(name);
  }
  const row = gapRow(page);
  await expect(row).toContainText("Alcli (demo)");
  await expect(row).toContainText("200");
  // The worklist fits its card at the reference width: no column is cut off.
  const clipped = await page.evaluate(() => {
    const root = document.querySelector("buying-and-stock-app")!.shadowRoot!;
    const box = root.querySelector("[data-worklist] .scroll") as HTMLElement;
    return box.scrollWidth - box.clientWidth;
  });
  expect(clipped).toBeLessThanOrEqual(0);
});

test("Draft PO goes grey when the pick is cleared", async ({ page, request }) => {
  await openHome(page, request);
  await gapRow(page).getByRole("checkbox").click();
  await expect(page.getByRole("button", { name: "Draft PO (0)" })).toBeDisabled();
  await gapRow(page).getByRole("checkbox").click();
  await expect(page.getByRole("button", { name: "Draft PO (1)" })).toBeEnabled();
});

test("shortfall to a draft: the vendor, its code, the line at the suggested quantity and the server's total", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  const panel = page.locator("[data-draft]");
  await expect(panel).toContainText("New purchase order from the shortfall");
  await expect(panel).toContainText("Alcli (demo)");
  await expect(panel).toContainText("Trenton yard");
  const line = panel.locator("[data-draft-line]");
  await expect(line).toHaveCount(1);
  await expect(line).toContainText("DBS-BRUS-DIM");
  await expect(line).toContainText("ALC-BRU-DIM-60");
  await expect(line).toContainText("240");
  await expect(line).toContainText("$8.40");
  await expect(line).toContainText("$2,016.00");
  await expect(panel.locator("[data-draft-total]")).toContainText("$2,016.00");
  await expect(page.locator("[data-po-row]", { hasText: "PO-TRT-142" })).toContainText("Draft");
  await expect(panel.locator("[data-callout]")).toContainText("Place moves the order to Ordered.");
});

test("delivery is a three way control, and the choice is saved with the draft", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  const methods = page.locator("[data-method]");
  await expect(methods).toHaveCount(3);
  await expect(page.locator("[data-method='UNKNOWN']")).toHaveAttribute("aria-pressed", "true");
  await page.locator("[data-method='PICKUP']").click();
  await expect(page.locator("[data-method='PICKUP']")).toHaveAttribute("aria-pressed", "true");
  await page.locator("[data-field='expected']").fill("2031-03-04");
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Draft PO-TRT-142 saved");
  await expect(page.locator("[data-draft]")).toHaveCount(0);
  await page.locator("[data-open-draft]").click();
  await expect(page.locator("[data-method='PICKUP']")).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("[data-field='expected']")).toHaveValue("2031-03-04");
});

test("a draft does not cover the shortfall: the K08 row stays, reading drafted not placed, with the draft's link", async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHome(page, request);
  await draftTheGap(page);
  await page.getByRole("button", { name: "Save draft" }).click();
  const row = gapRow(page);
  await expect(row).toHaveCount(1);
  await expect(row).toContainText("Drafted, not placed");
  await expect(row.locator("[data-open-draft]")).toHaveText("PO-TRT-142");
  await expect(row).toHaveAttribute("data-hot", "true");
  await expect(page.locator("[data-tile='below-point']")).toContainText("3");
  // With the draft's words in the row, the worklist and the register still show every column and every pill whole.
  const clipped = await page.evaluate(() => {
    const root = document.querySelector("buying-and-stock-app")!.shadowRoot!;
    return [...root.querySelectorAll<HTMLElement>("[data-worklist] .scroll, [data-register] .scroll")].map((b) => b.scrollWidth - b.clientWidth);
  });
  expect(clipped, "worklist then register: pixels cut off").toEqual([0, 0]);
  // The row's own pick is closed: a second draft for the same gap is not offered.
  await expect(row.getByRole("checkbox")).toBeDisabled();
  // The link opens the draft again.
  await row.locator("[data-open-draft]").click();
  await expect(page.locator("[data-draft]")).toContainText("PO-TRT-142");
});

test("a draft is placed and seen incoming, and only the placed order closes the gap", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  await page.locator("[data-field='expected']").fill("2031-03-04");
  await page.getByRole("button", { name: "Place order" }).click();
  const notice = page.locator("[data-notice]");
  await expect(notice.locator("strong")).toContainText("Placed: PO-TRT-142 to Alcli (demo), 240 Brussels Dimensional, $2,016.00.");
  await expect(notice).toContainText("shows under Incoming");
  await expect(notice).toContainText("2031-03-04");
  await expect(page.locator("[data-draft]")).toHaveCount(0);
  const register = page.locator("[data-po-row]", { hasText: "PO-TRT-142" });
  await expect(register).toContainText("Ordered");
  await expect(register).toContainText("$2,016.00");
  const incoming = page.locator("[data-incoming-row]", { hasText: "PO-TRT-142" });
  await expect(incoming).toContainText("2031-03-04");
  await expect(incoming).toContainText("Alcli (demo)");
  await expect(gapRow(page)).toHaveCount(0);
  await expect(page.locator("[data-tile='below-point']")).toContainText("2");
});

test("promised dates read as days from today, and a part received order reads how much landed", async ({ page, request }) => {
  await openHome(page, request);
  await expect(page.locator("[data-incoming-row]", { hasText: "PO-TRT-141" })).toContainText("in 2 days");
  await expect(page.locator("[data-incoming-row]", { hasText: "PO-TRT-136" })).toContainText("in 4 days");
  await expect(page.locator("[data-po-row]", { hasText: "PO-TRT-67" })).toContainText("6 of 10 pc landed");
  await expect(page.locator("[data-po-row]", { hasText: "PO-TRT-67" })).toContainText("Partial");
  await expect(page.locator("[data-tile='incoming']")).toContainText("arrivals with a promised date");
});

test("an order placed without a promised date shows no date, never an invented one", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  await page.getByRole("button", { name: "Place order" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Placed: PO-TRT-142");
  await expect(page.locator("[data-incoming-row]", { hasText: "PO-TRT-142" })).toContainText("no date");
});

test("a draft with its line removed is refused by the server in its own words, beside the button", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  await page.locator("[data-draft-line]").getByRole("button", { name: "Remove" }).click();
  await expect(page.locator("[data-draft-line]")).toHaveCount(0);
  await page.getByRole("button", { name: "Place order" }).click();
  await expect(page.locator("[data-draft] [data-error]")).toHaveText("Cannot order an empty purchase order, add at least one line.");
});

test("Cancel draft cancels the order it drafted and the row is plain again", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  await page.getByRole("button", { name: "Cancel draft" }).click();
  await expect(page.locator("[data-draft]")).toHaveCount(0);
  await expect(page.locator("[data-po-row]", { hasText: "PO-TRT-142" })).toContainText("Cancelled");
  await expect(gapRow(page)).not.toContainText("Drafted, not placed");
  await expect(page.locator("[data-tile='below-point']")).toContainText("3");
});

test("a role the gate walls out of ordering is refused in the ruled words, beside the Draft PO button", async ({ page, request }) => {
  await openHome(page, request);
  await request.post("/_stub/wall");
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  const error = page.locator("[data-worklist] [data-error]");
  await expect(error).toContainText("Placing, editing and cancelling a purchase order is for the Procurement Manager, the General Manager and the super admin only.");
  await expect(error).toContainText("The Yard Manager receives goods only; other roles can read.");
  await expect(page.locator("[data-draft]")).toHaveCount(0);
});

test("there is no vendor bill control, and the words say who posts a bill", async ({ page, request }) => {
  await openHome(page, request);
  await expect(page.getByRole("button", { name: /bill/i })).toHaveCount(0);
  await expect(page.getByRole("link", { name: /bill/i })).toHaveCount(0);
  await expect(page.locator("[data-bills-note]")).toContainText("Posting a vendor bill is for Payables and Receivables");
});

test("an order the yard has received reads Received, as an outline word and never a button", async ({ page, request }) => {
  await openHome(page, request);
  await request.post("/_stub/receive", { data: { po_no: "PO-TRT-141" } });
  await page.getByRole("button", { name: "Refresh" }).first().click();
  const row = page.locator("[data-po-row]", { hasText: "PO-TRT-141" });
  await expect(row).toContainText("Received");
  await expect(row.getByRole("button")).toHaveCount(0);
  await expect(page.locator("[data-incoming-row]", { hasText: "PO-TRT-141" })).toHaveCount(0);
});

test.skip("PENDING: the yard team receives the placed order in Yard Pick and Receive (waits for Yard receiving)", async () => {
  // The cross role receive leg of roadmap 2.7. It needs the Yard receiving app and the yard roles'
  // cards on one running core; it is named here so it cannot be forgotten.
});

test("on a phone the draft fits: the line, its amount and the total are on screen, and the page does not scroll sideways", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openHome(page, request);
  const sideways = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(await sideways()).toBeLessThanOrEqual(0);
  await draftTheGap(page);
  expect(await sideways()).toBeLessThanOrEqual(0);
  for (const sel of ["[data-draft-line] [data-amount]", "[data-draft-total]"]) {
    const box = await page.locator(sel).first().boundingBox();
    expect(box).not.toBeNull();
    expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(390);
  }
  await expect(page.locator("[data-draft-total]")).toContainText("$2,016.00");
});

test("on a phone every worklist figure is on screen, none hidden behind a sideways scroll", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openHome(page, request);
  const row = gapRow(page);
  for (const label of ["Short by", "Suggest", "Projected", "Vendor", "Reserved", "In transit", "Point"]) {
    await expect(row.locator(`td[data-label="${label}"]`)).toBeVisible();
  }
  const box = await row.locator("[data-field-short]").boundingBox();
  expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(390);
});
