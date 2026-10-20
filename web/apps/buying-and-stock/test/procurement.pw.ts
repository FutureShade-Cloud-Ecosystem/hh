// The procurement slice walked in a real browser against the stub: the card
// lands on the Buying and Stock home and shows the K08 gap, a PO is drafted from
// the shortfall, placed, and seen incoming, and the walled moves are refused in
// words. The stub is held to the core's shapes and sentences by
// test/stub-contract.test.mjs.
import type { Page } from "@playwright/test";
import { test, expect, openHome, resetStub, lowContrast, showArea } from "./helpers.js";

const gapRow = (page: Page) => page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" });

async function draftTheGap(page: Page): Promise<void> {
  // The K08 row is already picked on landing; one press drafts it.
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  await expect(page.locator("[data-draft]")).toBeVisible();
}

test("the way in with no handoff offers this role's own card", async ({ page, request }) => {
  await resetStub(request);
  await page.goto("/");
  // The surface's four roles now hold cards here (the inventory and counts
  // areas brought the yard manager and Payables and Receivables in; the stock
  // lookup brings Inside sales in, the read only seat), and the procurement
  // manager's card is this app's landing role.
  await expect(page.locator("[data-flow]")).toHaveCount(4);
  await expect(page.locator('[data-flow="procurement-manager"]')).toContainText("Procurement manager");
  await expect(page.locator('[data-flow="yard-manager"]')).toContainText("Yard manager");
  await expect(page.locator('[data-flow="payables-receivables"]')).toContainText("Payables and Receivables");
  await expect(page.locator('[data-flow="inside-sales"]')).toContainText("Inside sales");
});

test("the band names the role as the reference does, and the theme control names the current theme", async ({ page, request }) => {
  await openHome(page, request);
  const band = page.locator("hh-shell-header");
  await expect(band).toContainText("Procurement manager");
  await expect(band).not.toContainText("(demo)");
  await band.locator("button.account").click();
  const theme = page.getByRole("button", { name: /^Theme: / });
  await expect(theme).toHaveText("Theme: Light");
});

test("Feedback opens the shared widget from the account menu", async ({ page, request }) => {
  await openHome(page, request);
  await page.locator("hh-shell-header button.account").click();
  await page.getByRole("button", { name: "Feedback" }).click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();
  await expect(page.locator("hh-feedback")).toHaveAttribute("screen", "Buying and Stock");
});

test("every text is 4.5 to 1 or better in both themes: home, the drafted K08 row, the draft, the placed notice", async ({ page, request }) => {
  test.setTimeout(60_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHome(page, request);
  for (const theme of ["Theme: Light", "Theme: Brand"]) {
    if (theme === "Theme: Brand") {
      await page.locator("hh-shell-header button.account").click();
      await page.getByRole("button", { name: "Theme: Light" }).click();
    }
    expect(await lowContrast(page), `${theme}, home`).toEqual([]);
    await page.getByRole("button", { name: "Draft PO (1)" }).click();
    await expect(page.locator("[data-draft]")).toBeVisible();
    expect(await lowContrast(page), `${theme}, draft`).toEqual([]);
    await page.getByRole("button", { name: "Save draft" }).click();
    await expect(page.locator("[data-open-draft]")).toBeVisible();
    expect(await lowContrast(page), `${theme}, drafted row`).toEqual([]);
    await page.locator("[data-open-draft]").click();
    await page.getByRole("button", { name: "Place order" }).click();
    await expect(page.locator("[data-notice]")).toBeVisible();
    expect(await lowContrast(page), `${theme}, placed`).toEqual([]);
    await resetStub(request);
    await page.reload();
    await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  }
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
  // The design notes are gone from the screen: the row and the button carry the whole story.
  await expect(page.locator("[data-k08-note]")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Draft PO (1)" })).toBeEnabled();
  await expect(page.locator("[data-tile='below-point']")).toContainText("3");
  await expect(page.locator("[data-po-row]")).toHaveCount(4);
});

test("the worklist shows one product column, the vendor under the name, as the board draws it", async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHome(page, request);
  const heads = (await page.locator("[data-worklist] th").allTextContents()).map((t) => t.trim());
  for (const name of ["Product", "On hand", "Reserved", "In transit", "On order", "Projected", "Point", "Short by", "Suggest"]) {
    expect(heads).toContain(name);
  }
  // One product column: the SKU and Vendor columns the old table carried are gone from the head.
  // The SKU lives on the draft's lines; the vendor rides under the name in the product cell.
  expect(heads).not.toContain("SKU");
  expect(heads).not.toContain("Vendor");
  const row = gapRow(page);
  const cell = row.locator("td[data-label='Product']");
  await expect(cell).toContainText("Brussels Dimensional");
  await expect(cell).toContainText("Alcli (demo)");
  await expect(row).toContainText("200");
  await expect(row.locator("td[data-label='SKU']")).toHaveCount(0);
  await expect(row.locator("td[data-label='Vendor']")).toHaveCount(0);
  // The worklist fits its card at the reference width: no column is cut off.
  const clipped = await page.evaluate(() => {
    const root = document.querySelector("buying-and-stock-app")!.shadowRoot!;
    const box = root.querySelector("[data-worklist] .scroll") as HTMLElement;
    return box.scrollWidth - box.clientWidth;
  });
  expect(clipped).toBeLessThanOrEqual(0);
});

test("the shortfall row names the sales orders it holds up, oldest first with a count", async ({ page, request }) => {
  await openHome(page, request);
  // The turf row holds up five orders: the row names the oldest three and counts the two more,
  // the core's own window (change 5, R7). The dimensional row holds up one. Nothing waits on the
  // stone row, and its row draws no tag at all.
  const turf = page.locator("[data-shortfall-row]", { hasText: "DBS-RYMAR-TURF" });
  await expect(turf.locator("[data-short-order]")).toHaveText("Short for SO-3305, SO-3311, SO-3312 and 2 more");
  await expect(gapRow(page).locator("[data-short-order]")).toHaveText("Short for SO-3314");
  await expect(page.locator("[data-shortfall-row]", { hasText: "DBS-AGG-34CLEAR" }).locator("[data-short-order]")).toHaveCount(0);
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
  await expect(panel).toContainText("Loma yard");
  const line = panel.locator("[data-draft-line]");
  await expect(line).toHaveCount(1);
  await expect(line).toContainText("DBS-BRUS-DIM");
  await expect(line).toContainText("ALC-BRU-DIM-60");
  await expect(line).toContainText("240");
  await expect(line).toContainText("$8.40");
  await expect(line).toContainText("$2,016.00");
  await expect(panel.locator("[data-draft-total]")).toContainText("$2,016.00");
  await expect(page.locator("[data-po-row]", { hasText: "PO-PLM-142" })).toContainText("Draft");
  await expect(panel.locator("[data-callout]")).toContainText("Place moves the order to Ordered.");
});

test("delivery is a three way control, and the choice is saved with the draft", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  const methods = page.locator("[data-method]");
  await expect(methods).toHaveCount(2);
  // Nothing is lit until a way is chosen: the core's "not set" is no option to pick.
  await expect(page.locator("[data-method][aria-pressed='true']")).toHaveCount(0);
  await page.locator("[data-method='PICKUP']").click();
  await expect(page.locator("[data-method='PICKUP']")).toHaveAttribute("aria-pressed", "true");
  await page.locator("[data-field='expected']").fill("2031-03-04");
  // The date reads as words under the field as it is typed, before anything is saved.
  await expect(page.locator("[data-date-words]")).toContainText("Tuesday 4 March 2031");
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Draft PO-PLM-142 saved");
  await expect(page.locator("[data-draft]")).toHaveCount(0);
  await page.locator("[data-open-draft]").click();
  await expect(page.locator("[data-method='PICKUP']")).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("[data-field='expected']")).toHaveValue("2031-03-04");
});

test("a draft's line can be corrected: the quantity and the cost, sent to the core and read back", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  const panel = page.locator("[data-draft]");
  const line = panel.locator("[data-draft-line]");
  await expect(line).toContainText("240");
  await expect(line).toContainText("$8.40");
  // Change opens the line's editor: the two figures a vendor's confirmation corrects.
  await line.getByRole("button", { name: "Change" }).click();
  await page.locator("[data-line-qty]").fill("480");
  await page.locator("[data-line-cost]").fill("9.00");
  await line.getByRole("button", { name: "Save" }).click();
  await expect(line).toContainText("480");
  await expect(line).toContainText("$9.00");
  await expect(line).toContainText("$4,320.00");
  await expect(panel.locator("[data-draft-total]")).toContainText("$4,320.00");
  // The editor closed; the line reads as words again.
  await expect(page.locator("[data-line-editing]")).toHaveCount(0);
  await expect(line.getByRole("button", { name: "Change" })).toBeVisible();
});

test("a line can be added to a draft from the catalogue, and removed again", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  const panel = page.locator("[data-draft]");
  await panel.getByRole("button", { name: "Add a line" }).click();
  await page.locator("[data-add-product]").selectOption({ label: "TB-PAVER-60 Techo Bloc Paver 60mm" });
  await page.locator("[data-add-qty]").fill("12");
  await panel.getByRole("button", { name: "Add line" }).click();
  const lines = panel.locator("[data-draft-line]");
  await expect(lines).toHaveCount(2);
  const added = panel.locator("[data-draft-line]", { hasText: "TB-PAVER-60" });
  await expect(added).toContainText("12");
  // The cost left empty: the server keeps the branch's own figure for the product.
  await expect(added).toContainText("$820.00");
  await added.getByRole("button", { name: "Remove" }).click();
  await expect(panel.locator("[data-draft-line]")).toHaveCount(1);
  await expect(panel.locator("[data-add-line]")).toHaveCount(0);
});

test("typed dollars become cents exactly: 0.1 and 19.99 land as the server's own totals, a third decimal is refused in words", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  const panel = page.locator("[data-draft]");
  const line = panel.locator("[data-draft-line]");
  await line.getByRole("button", { name: "Change" }).click();
  // 1.005 is the float edge a multiply and a round turns into 100 cents, a price nobody typed:
  // the boundary refuses a third decimal in words and the line keeps the server's figure.
  await page.locator("[data-line-cost]").fill("1.005");
  await line.getByRole("button", { name: "Save" }).click();
  await expect(panel.locator("[data-error]")).toHaveText("Write the unit cost as dollars and cents, two decimals at most.");
  await expect(page.locator("[data-line-editing]")).toHaveCount(1);
  await expect(line).toContainText("$2,016.00");
  // 0.1 and 19.99 become exactly 10 and 1999 cents, and the server's line total proves it (240 pieces).
  await page.locator("[data-line-cost]").fill("0.1");
  await line.getByRole("button", { name: "Save" }).click();
  await expect(line).toContainText("$0.10");
  await expect(line).toContainText("$24.00");
  await line.getByRole("button", { name: "Change" }).click();
  await page.locator("[data-line-cost]").fill("19.99");
  await line.getByRole("button", { name: "Save" }).click();
  await expect(line).toContainText("$19.99");
  await expect(line).toContainText("$4,797.60");
  await expect(panel.locator("[data-draft-total]")).toContainText("$4,797.60");
});

test("a role the gate walls out of editing is refused in the ruled words, beside the line's Save", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  await request.post("/_stub/wall");
  const line = page.locator("[data-draft-line]");
  await line.getByRole("button", { name: "Change" }).click();
  await page.locator("[data-line-qty]").fill("480");
  await line.getByRole("button", { name: "Save" }).click();
  const error = page.locator("[data-draft] [data-error]");
  await expect(error).toContainText("Drafting, placing, editing and cancelling a purchase order is for the Procurement Manager, the General Manager and the super admin only.");
  await expect(error).toContainText("The Yard Manager receives goods only; other roles can read.");
  await expect(page.locator("[data-line-editing]")).toHaveCount(1);
});

test("a branch outside the caller's reach is refused in the server's own words, beside the Draft button", async ({ page, request }) => {
  await openHome(page, request);
  // The buyer's grants were taken away after the worklist rendered: the branch the row names is
  // now outside the reach, and the fence's own sentence reads in red, not the role wall's words.
  await request.post("/_stub/fence");
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  const error = page.locator("[data-worklist] [data-error]");
  await expect(error).toHaveText("You do not have access to that branch.");
  await expect(page.locator("[data-draft]")).toHaveCount(0);
});

test("the existing draft note keeps space under it before the order's facts", async ({ page, request }) => {
  await openHome(page, request);
  // Another tab drafted the K08 gap after this page rendered, so this press meets the open draft.
  const rows = await request.get("/api/v1/inventory/shortfall", { headers: { authorization: "Bearer another-tab" } });
  const page1 = (await rows.json()) as { items: { sku: string; product_id: string; branch_id: string }[] };
  const gap = page1.items.find((r) => r.sku === "DBS-BRUS-DIM");
  await request.post("/api/v1/inventory/reorder-po", {
    headers: { authorization: "Bearer another-tab" },
    data: { product_id: gap?.product_id, branch_id: gap?.branch_id, qty_base: 0 },
  });
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  const existing = page.locator("[data-existing-draft]");
  await expect(existing).toContainText("A draft was already open: PO-PLM-142.");
  // The facts list (Order, Vendor, Ship to) sits below the note with air between, not flush.
  const gapPx = await page.evaluate(() => {
    const root = document.querySelector("buying-and-stock-app")!.shadowRoot!;
    const note = root.querySelector("[data-existing-draft]") as HTMLElement;
    const facts = root.querySelector(".facts") as HTMLElement;
    return facts.getBoundingClientRect().top - note.getBoundingClientRect().bottom;
  });
  expect(gapPx).toBeGreaterThanOrEqual(12);
});

test("at 390 the editor's refusal sits beside the Save it answers, not a screen below it", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openHome(page, request);
  await draftTheGap(page);
  const line = page.locator("[data-draft-line]");
  await line.getByRole("button", { name: "Change" }).click();
  await page.locator("[data-line-qty]").fill("0");
  await line.getByRole("button", { name: "Save" }).click();
  // The refusal belongs to the editor's own card: inside the row it answers, under its buttons.
  const error = page.locator("[data-line-editing] [data-error]");
  await expect(error).toHaveText("Line quantity must be positive.");
  const save = await line.getByRole("button", { name: "Save" }).boundingBox();
  const refusal = await error.boundingBox();
  expect(save).not.toBeNull();
  expect(refusal).not.toBeNull();
  expect((refusal?.y ?? 1e9) - ((save?.y ?? 0) + (save?.height ?? 0))).toBeLessThanOrEqual(60);
});

test("a draft made behind the screen's back is named when the press drafts again", async ({ page, request }) => {
  await openHome(page, request);
  // Another tab drafted the K08 gap after this page rendered: the worklist here is stale, so the
  // press drafts the full quantity again, and the core's answer names the draft already open.
  const rows = await request.get("/api/v1/inventory/shortfall", { headers: { authorization: "Bearer another-tab" } });
  const page1 = (await rows.json()) as { items: { sku: string; product_id: string; branch_id: string }[] };
  const gap = page1.items.find((r) => r.sku === "DBS-BRUS-DIM");
  await request.post("/api/v1/inventory/reorder-po", {
    headers: { authorization: "Bearer another-tab" },
    data: { product_id: gap?.product_id, branch_id: gap?.branch_id, qty_base: 0 },
  });
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  const panel = page.locator("[data-draft]");
  await expect(panel).toBeVisible();
  await expect(panel.locator("[data-po-no]")).toHaveText("PO-PLM-143");
  const existing = panel.locator("[data-existing-draft]");
  await expect(existing).toContainText("A draft was already open: PO-PLM-142.");
  await expect(existing).toContainText("Drafted, not placed");
  await existing.locator("[data-open-draft]").click();
  await expect(panel.locator("[data-po-no]")).toHaveText("PO-PLM-142");
});

test("a draft does not cover the shortfall: the K08 row stays, reading drafted not placed, with the draft's link", async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHome(page, request);
  await draftTheGap(page);
  await page.getByRole("button", { name: "Save draft" }).click();
  const row = gapRow(page);
  await expect(row).toHaveCount(1);
  await expect(row).toContainText("Drafted, not placed");
  await expect(row.locator("[data-open-draft]")).toHaveText("PO-PLM-142");
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
  await expect(row.getByRole("checkbox")).toHaveAccessibleName("Drafted, PO-PLM-142");
  // The link opens the draft again.
  await row.locator("[data-open-draft]").click();
  await expect(page.locator("[data-draft]")).toContainText("PO-PLM-142");
});

test("a draft is placed and seen incoming, and only the placed order closes the gap", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  await page.locator("[data-field='expected']").fill("2031-03-04");
  await page.getByRole("button", { name: "Place order" }).click();
  const notice = page.locator("[data-notice]");
  await expect(notice.locator("strong")).toContainText("Placed: PO-PLM-142 to Alcli (demo), 240 Brussels Dimensional, $2,016.00.");
  await expect(notice).toContainText("Moved to Ordered, promised 2031-03-04. It shows under Incoming. The yard team receives it in Yard Pick and Receive.");
  await expect(notice).not.toContainText("the core reads");
  await expect(page.locator("[data-draft]")).toHaveCount(0);
  const register = page.locator("[data-po-row]", { hasText: "PO-PLM-142" });
  await expect(register).toContainText("Ordered");
  await expect(register).toContainText("$2,016.00");
  await expect(gapRow(page)).toHaveCount(0);
  await expect(page.locator("[data-tile='below-point']")).toContainText("2");
  await showArea(page, "incoming");
  const incoming = page.locator("[data-incoming-row]", { hasText: "PO-PLM-142" });
  await expect(incoming).toContainText("2031-03-04");
  await expect(incoming).toContainText("Alcli (demo)");
  await expect(incoming).toContainText("Brussels Dimensional");
});

test("promised dates read as days from today, and a part received order reads how much landed", async ({ page, request }) => {
  await openHome(page, request);
  // The PO screen has no second row of tabs: Incoming is its own area on the switcher above.
  await expect(page.locator("nav.tabs")).toHaveCount(0);
  await expect(page.locator("nav.areas").getByRole("button", { name: "Incoming" })).toBeVisible();
  await expect(page.locator("[data-po-row]", { hasText: "PO-PLM-67" })).toContainText("6 of 10 pc landed");
  await expect(page.locator("[data-po-row]", { hasText: "PO-PLM-67" })).toContainText("Partial");
  await expect(page.locator("[data-tile='incoming']")).toContainText("arrivals with a promised date");
  await showArea(page, "incoming");
  await expect(page.locator("[data-incoming-row]", { hasText: "PO-PLM-141" })).toContainText("in 2 days");
  await expect(page.locator("[data-incoming-row]", { hasText: "PO-PLM-136" })).toContainText("in 4 days");
});

test("Incoming is its own area: the tile leads to it and its table lists every line still to arrive", async ({ page, request }) => {
  await openHome(page, request);
  // The tile's link is the area's own press, not the desk's old tab.
  await page.locator("[data-tile='incoming']").getByRole("button", { name: "Open incoming" }).click();
  await expect(page.locator("nav.areas [data-area-id='incoming'][aria-current='true']")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Incoming", level: 1 })).toBeVisible();
  const heads = (await page.locator("[data-incoming] th").allTextContents()).map((t) => t.trim());
  for (const name of ["PO", "Vendor", "Product", "Quantity", "Expected", "How"]) {
    expect(heads).toContain(name);
  }
  // One row per line still to arrive, soonest promised first: 141 in two days, 136 in four, and
  // the part received order's four pieces still coming, undated, at the foot.
  const rows = page.locator("[data-incoming-row]");
  await expect(rows).toHaveCount(3);
  const first = rows.nth(0);
  await expect(first).toContainText("PO-PLM-141");
  await expect(first).toContainText("Techo Bloc (demo)");
  await expect(first).toContainText("Techo Bloc Paver 60mm");
  // Pieces draw bare, every other unit beside the figure (the app's own quantity words).
  await expect(first.locator("td[data-label='Quantity']")).toHaveText("12");
  await expect(first).toContainText("in 2 days");
  await expect(first).toContainText("Vendor truck");
  const last = rows.nth(2);
  await expect(last).toContainText("PO-PLM-67");
  await expect(last.locator("td[data-label='Quantity']")).toHaveText("4");
  await expect(last).toContainText("no date");
  await expect(last).toContainText("Vendor truck");
});

test("an order placed without a promised date shows no date, never an invented one", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  await page.getByRole("button", { name: "Place order" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Placed: PO-PLM-142");
  await showArea(page, "incoming");
  await expect(page.locator("[data-incoming-row]", { hasText: "PO-PLM-142" })).toContainText("no date");
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
  await expect(page.locator("[data-po-row]", { hasText: "PO-PLM-142" })).toContainText("Cancelled");
  await expect(gapRow(page)).not.toContainText("Drafted, not placed");
  await expect(page.locator("[data-tile='below-point']")).toContainText("3");
});

test("a role the gate walls out of ordering is refused in the ruled words, beside the Draft PO button", async ({ page, request }) => {
  await openHome(page, request);
  await request.post("/_stub/wall");
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  const error = page.locator("[data-worklist] [data-error]");
  await expect(error).toContainText("Drafting, placing, editing and cancelling a purchase order is for the Procurement Manager, the General Manager and the super admin only.");
  await expect(error).toContainText("The Yard Manager receives goods only; other roles can read.");
  await expect(page.locator("[data-draft]")).toHaveCount(0);
});

test("the orders screen has no vendor bill control of its own, the bills live in their area, and the words say who posts a bill", async ({ page, request }) => {
  await openHome(page, request);
  // The worklist, the register and incoming carry no bill control; the Vendor bills area is a tab beside them.
  await expect(page.locator(".home").getByRole("button", { name: /bill/i })).toHaveCount(0);
  await expect(page.locator("nav.areas").getByRole("button", { name: "Vendor bills" })).toBeVisible();
  await expect(page.locator("[data-bills-note]")).toContainText("Posting a vendor bill is for Payables and Receivables");
});

test("an order the yard has received reads Received, as an outline word and never a button", async ({ page, request }) => {
  await openHome(page, request);
  await request.post("/_stub/receive", { data: { po_no: "PO-PLM-141" } });
  await page.getByRole("button", { name: "Refresh" }).first().click();
  const row = page.locator("[data-po-row]", { hasText: "PO-PLM-141" });
  await expect(row).toContainText("Received");
  await expect(row.getByRole("button")).toHaveCount(0);
  await showArea(page, "incoming");
  await expect(page.locator("[data-incoming-row]", { hasText: "PO-PLM-141" })).toHaveCount(0);
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
  for (const label of ["Short by", "Suggest", "Projected", "Product", "Reserved", "In transit", "Point"]) {
    await expect(row.locator(`td[data-label="${label}"]`)).toBeVisible();
  }
  // The vendor rides under the name in the product cell, so it is on screen too.
  await expect(row.locator("td[data-label='Product']")).toContainText("Alcli (demo)");
  const box = await row.locator("[data-field-short]").boundingBox();
  expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(390);
});

test("on a phone the incoming table stacks: every cell keeps its label, and the page does not scroll sideways", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openHome(page, request);
  await showArea(page, "incoming");
  const row = page.locator("[data-incoming-row]", { hasText: "PO-PLM-141" });
  await expect(row).toBeVisible();
  for (const label of ["PO", "Vendor", "Product", "Quantity", "Expected", "How"]) {
    await expect(row.locator(`td[data-label="${label}"]`)).toBeVisible();
  }
  await expect(row).toContainText("Techo Bloc (demo)");
  await expect(row.locator("[data-expected]")).toBeVisible();
  const sideways = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(await sideways()).toBeLessThanOrEqual(0);
});

test("when the vendor has an open warranty credit, the draft renders the credit strip and can apply it without changing line costs", async ({ page, request }) => {
  await openHome(page, request);
  await draftTheGap(page);
  const panel = page.locator("[data-draft]");
  await expect(panel).toBeVisible();

  // Credit strip names the credit, amount, claim and description
  const strip = panel.locator("[data-warranty-credit-strip]");
  await expect(strip).toBeVisible();
  await expect(strip.locator("[data-credit-headline]")).toContainText("Open warranty credit · $264.00 from WCL-104 (8 pieces of Paver 60mm Slate)");

  // Apply button is present
  const applyBtn = strip.getByRole("button", { name: "Apply $264.00 to this PO" });
  await expect(applyBtn).toBeVisible();

  // Line unit cost and subtotal before applying credit
  const line = panel.locator("[data-draft-line]");
  await expect(line).toContainText("$8.40");
  await expect(line).toContainText("$2,016.00");
  await expect(panel.locator("[data-draft-total]")).toContainText("$2,016.00");

  // Apply the credit
  await applyBtn.click();

  // Strip now displays the Applied badge
  await expect(strip.locator("[data-credit-applied-badge]")).toBeVisible();
  await expect(strip.locator("[data-credit-applied-badge]")).toHaveText("Applied");
  await expect(applyBtn).toHaveCount(0);

  // Line unit cost remains unchanged (Ruling R3)
  await expect(line).toContainText("$8.40");
  await expect(line).toContainText("$2,016.00");

  // Draft totals reflect the 3-row breakdown
  const subtotalRow = panel.locator("[data-row-lines-subtotal]");
  await expect(subtotalRow).toContainText("Lines subtotal");
  await expect(subtotalRow.locator("[data-lines-subtotal]")).toHaveText("$2,016.00");

  const creditRow = panel.locator("[data-row-credit-applied]");
  await expect(creditRow).toContainText("Warranty credit applied · WCL-104");
  await expect(creditRow.locator("[data-warranty-credit-applied]")).toHaveText("-$264.00");

  const dueRow = panel.locator("[data-row-total-due]");
  await expect(dueRow).toContainText("Total due to Alcli (demo)");
  await expect(dueRow.locator("[data-total-due-vendor]")).toHaveText("$1,752.00");
});

