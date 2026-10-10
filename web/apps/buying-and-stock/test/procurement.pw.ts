// The procurement slice walked in a real browser against the stub: the card
// lands on the Buying and Stock home and shows the K08 gap, a PO is drafted from
// the shortfall, placed, and seen incoming, and the walled moves are refused in
// words. The stub is held to the core's shapes and sentences by
// test/stub-contract.test.mjs.
import type { Page } from "@playwright/test";
import { test, expect, openHome, resetStub } from "./helpers.js";

/**
 * Every visible text node's contrast against the colour it really sits on (backgrounds composited up the
 * tree, shadow roots crossed). Returns "text: ratio" for each under 4.5 (3 for large text). Disabled controls
 * are exempt, as the standard has it.
 */
async function lowContrast(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const canvas = document.createElement("canvas");
    canvas.width = canvas.height = 1;
    const ctx = canvas.getContext("2d", { willReadFrequently: true })!;
    const rgba = (css: string): [number, number, number, number] => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = "#000";
      ctx.fillStyle = css;
      ctx.fillRect(0, 0, 1, 1);
      const d = ctx.getImageData(0, 0, 1, 1).data;
      return [d[0] ?? 0, d[1] ?? 0, d[2] ?? 0, (d[3] ?? 0) / 255];
    };
    const over = (top: number[], under: number[]): number[] => {
      const a = top[3] ?? 1;
      return [0, 1, 2].map((i) => (top[i] ?? 0) * a + (under[i] ?? 0) * (1 - a)).concat([1]);
    };
    const lum = (c: number[]): number => {
      const f = (v: number): number => {
        const x = v / 255;
        return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * f(c[0] ?? 0) + 0.7152 * f(c[1] ?? 0) + 0.0722 * f(c[2] ?? 0);
    };
    const parentOf = (e: Element): Element | null => e.parentElement ?? ((e.getRootNode() as ShadowRoot).host ?? null);
    const backdrop = (e: Element): number[] => {
      const chain: number[][] = [];
      for (let n: Element | null = e; n !== null; n = parentOf(n)) chain.push(rgba(getComputedStyle(n).backgroundColor));
      let base = [255, 255, 255, 1];
      for (let i = chain.length - 1; i >= 0; i -= 1) base = over(chain[i] as number[], base);
      return base;
    };
    const out: string[] = [];
    const walk = (root: Document | ShadowRoot | Element): void => {
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      for (let node = walker.nextNode(); node !== null; node = walker.nextNode()) {
        const text = (node.textContent ?? "").trim();
        const el = node.parentElement;
        if (text === "" || el === null || el.closest("[disabled]") !== null) continue;
        if (el.closest("style, script") !== null) continue;
        const cs = getComputedStyle(el);
        if (cs.visibility === "hidden" || cs.display === "none") continue;
        const bg = backdrop(el);
        const fg = over(rgba(cs.color), bg);
        const l1 = lum(fg);
        const l2 = lum(bg);
        const ratio = (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
        const size = parseFloat(cs.fontSize);
        const large = size >= 24 || (size >= 18.66 && Number(cs.fontWeight) >= 700);
        if (ratio < (large ? 3 : 4.5)) out.push(`${text.slice(0, 40)}: ${ratio.toFixed(2)}`);
      }
      root.querySelectorAll("*").forEach((n) => {
        if (n.shadowRoot !== null) walk(n.shadowRoot);
      });
    };
    walk(document);
    return out;
  });
}

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

test("the header reads its sub line as the reference does, and Feedback opens the shared widget", async ({ page, request }) => {
  await openHome(page, request);
  await expect(page.locator("hos-app-header")).toContainText("a Hardscape House product");
  await expect(page.locator("hos-app-header")).not.toContainText("Work shell");
  await page.getByRole("button", { name: "Feedback" }).click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();
  await expect(page.locator("hh-feedback")).toHaveAttribute("screen", "Buying and Stock");
});

test("every text is 4.5 to 1 or better in both themes: home, the drafted K08 row, the draft, the placed notice", async ({ page, request }) => {
  test.setTimeout(60_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openHome(page, request);
  for (const theme of ["Theme: Light", "Theme: Brand"]) {
    if (theme === "Theme: Brand") await page.getByRole("button", { name: "Theme: Light" }).click();
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
  await expect(methods).toHaveCount(2);
  // Nothing is lit until a way is chosen: the core's "not set" is no option to pick.
  await expect(page.locator("[data-method][aria-pressed='true']")).toHaveCount(0);
  await page.locator("[data-method='PICKUP']").click();
  await expect(page.locator("[data-method='PICKUP']")).toHaveAttribute("aria-pressed", "true");
  await page.locator("[data-field='expected']").fill("2031-03-04");
  // The date reads as words under the field as it is typed, before anything is saved.
  await expect(page.locator("[data-date-words]")).toContainText("Tuesday 4 March 2031");
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Draft PO-TRT-142 saved");
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
  await expect(row.getByRole("checkbox")).toHaveAccessibleName("Drafted, PO-TRT-142");
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
  await expect(notice).toContainText("Moved to Ordered, promised 2031-03-04. It shows under Incoming. The yard team receives it in Yard Pick and Receive.");
  await expect(notice).not.toContainText("the core reads");
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
  // The tabs read in the reference's order, the ones not yet built dashed between them.
  await expect(page.locator("nav.tabs")).toHaveText(/Purchase orders\s*Receiving later\s*Incoming\s*Vendors later\s*Vendor bills later/);
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
  await expect(error).toContainText("Drafting, placing, editing and cancelling a purchase order is for the Procurement Manager, the General Manager and the super admin only.");
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
