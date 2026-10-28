// UX-106 round 2: the Staff register's branch values at a phone's width.
//
// The audit's after shot showed two defects in one cell. The yard persona's
// second branch, "Julian", sat at the card's left edge under the BRANCHES
// label, so it read as a label and not as a value. And the branch values were
// two styles: "All branches" was a pill, "Point Loma" was plain text.
//
// The leg is the two branch persona at 390:
//   1. the second branch's left edge sits at or right of the first value's
//      left edge, inside the value column;
//   2. every branch value carries the pill class.
//
// The second leg is the general one: no Staff value sits under its label, in
// any labelled cell of the card (branches, access, status, last sign in).
import { expect, test, type Locator, type Page } from "@playwright/test";

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]")).toBeVisible();
}

/** The charcoal half is the design system's brand theme, one press away. */
async function goCharcoal(page: Page): Promise<void> {
  await page.locator("hh-shell-header .account").click();
  await page.locator('[data-testid="theme-control"]').click();
  await page.locator("hh-shell-header .account").click();
}

/** One value of a cell: its words, where its left edge sits, and its classes. */
interface CellValue {
  readonly text: string;
  readonly left: number;
  readonly cls: string;
}

/** A cell read: the label, the left edge of the value column, and the values. */
interface CellReading {
  readonly label: string;
  readonly valueLeft: number;
  readonly values: readonly CellValue[];
}

/**
 * Reads a labelled cell the way the card lays it out at a phone's width: the
 * label is the cell's first grid column, the values sit in the second, and
 * every value is a leaf (the deepest element that carries words).
 */
async function readCell(cell: Locator): Promise<CellReading> {
  return cell.evaluate((el) => {
    const style = getComputedStyle(el);
    const tracks = style.gridTemplateColumns
      .split(" ")
      .map((track) => parseFloat(track))
      .filter((track) => !Number.isNaN(track));
    const gap = parseFloat(style.columnGap) || 0;
    const padLeft = parseFloat(style.paddingLeft) || 0;
    const box = el.getBoundingClientRect();
    const twoColumns = tracks.length > 1;
    const valueLeft = box.left + padLeft + (twoColumns ? tracks[0] + gap : 0);
    const values = Array.from(el.querySelectorAll("*"))
      .filter((node) => node.children.length === 0 && (node.textContent ?? "").trim() !== "")
      .map((node) => ({
        text: (node.textContent ?? "").trim(),
        left: node.getBoundingClientRect().left,
        cls: node.getAttribute("class") ?? "",
      }));
    return { label: el.getAttribute("data-label") ?? "", valueLeft, values };
  });
}

for (const theme of ["light", "charcoal"] as const) {
  test(`the two branch values sit in the value column and every one is a pill, in ${theme}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await signIn(page);
    if (theme === "charcoal") await goCharcoal(page);

    // The yard persona reaches two branches: the cell under test.
    const cell = page.locator(".staff-branches").filter({ hasText: "Julian" });
    await expect(cell).toHaveCount(1);
    const read = await readCell(cell);
    expect(read.values.map((v) => v.text)).toEqual(["Point Loma", "Julian"]);

    // The value checks are soft, so one run names every defect the cell
    // carries: the pill class and the left edge.

    // 1. Every branch value carries the pill class, the same class "All
    //    branches" carries.
    for (const value of read.values) {
      expect.soft(value.cls, `the branch value ${value.text} is not a pill`).toMatch(/(^|\s)chip(\s|$)/);
    }

    // 2. The second branch's left edge sits at or right of the first value's
    //    left edge, and both sit inside the value column, never under the
    //    label.
    expect.soft(read.values[1].left).toBeGreaterThanOrEqual(read.values[0].left - 0.5);
    for (const value of read.values) {
      expect.soft(value.left, `the branch value ${value.text} sits under the label`).toBeGreaterThanOrEqual(
        read.valueLeft - 0.5,
      );
    }
  });
}

test("no Staff value sits under its label at 390", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);

  // The labelled cells of the card row: the branches, the access, the status
  // and the last sign in.
  const cells = page.locator(
    ".staff-table tbody td.staff-branches, .staff-table tbody td.staff-access, .staff-table tbody td.staff-status, .staff-table tbody td.staff-seen",
  );
  const count = await cells.count();
  expect(count).toBeGreaterThan(0);
  for (let i = 0; i < count; i += 1) {
    const read = await readCell(cells.nth(i));
    for (const value of read.values) {
      expect.soft(
        value.left,
        `the ${read.label} value ${value.text} sits under its label`,
      ).toBeGreaterThanOrEqual(read.valueLeft - 0.5);
    }
  }
});
