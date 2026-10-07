// The phone-width ruling (the train review's P3-7): the counter stays usable
// at 390x844. The review's shot caught two failures at exactly this width:
// the search drop's rows ran the price over the name, and the ticket's line
// table ran past its card's right edge with the money columns. This spec holds
// the fix at the width the review shot, and holds the desktop shape beside it,
// because a stack that never unstacks would be its own regression.
import { expect, test, type Page } from "@playwright/test";

/** Sign in through the entry card and land on the ported terminal. */
async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

/** Open the Items step and search; the drop row's click adds the hit. */
async function searchDropRow(page: Page, term: string, hitName: string) {
  const head = page.getByTestId("step-head-items");
  for (let attempt = 0; attempt < 5; attempt += 1) {
    if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
    if ((await head.getAttribute("aria-expanded")) === "true") break;
  }
  const field = page.getByTestId("pos-scan-input");
  await field.fill(term);
  const row = page.getByTestId("drop-row").filter({ hasText: hitName }).first();
  await expect(row).toBeVisible();
  return row;
}

/** A box is inside the 390x844 viewport: nothing of it hangs off any edge. */
function insideViewport(box: { x: number; y: number; width: number; height: number }): boolean {
  return box.x >= 0 && box.y >= 0 && box.x + box.width <= 390.01 && box.y + box.height <= 844.01;
}

test("at 390 the search drop stacks: the top row's price sits below the name, never over it", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  const row = await searchDropRow(page, "holland", "Holland Stone 60 mm");
  const name = row.locator(".name");
  const price = row.locator(".price");
  await expect(name).toBeVisible();
  await expect(price).toBeVisible();
  const nameBox = (await name.boundingBox()) as { x: number; y: number; width: number; height: number };
  const priceBox = (await price.boundingBox()) as { x: number; y: number; width: number; height: number };
  const intersects =
    nameBox.x < priceBox.x + priceBox.width &&
    priceBox.x < nameBox.x + nameBox.width &&
    nameBox.y < priceBox.y + priceBox.height &&
    priceBox.y < nameBox.y + nameBox.height;
  expect(intersects, "the row's price box intersects its name box at phone width").toBe(false);
  // The stack is the fix's shape: the price begins below the name's line.
  expect(priceBox.y, "the price did not stack below the name at phone width").toBeGreaterThanOrEqual(
    nameBox.y + nameBox.height - 0.5,
  );
});

test("at desktop width the row keeps its one-line shape: name and price share the band", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await signIn(page);
  const row = await searchDropRow(page, "holland", "Holland Stone 60 mm");
  const name = row.locator(".name");
  const price = row.locator(".price");
  await expect(name).toBeVisible();
  await expect(price).toBeVisible();
  const nameBox = (await name.boundingBox()) as { x: number; y: number; width: number; height: number };
  const priceBox = (await price.boundingBox()) as { x: number; y: number; width: number; height: number };
  // Beside, not below: the two boxes share a vertical band and the price sits
  // to the name's right.
  const sameBand =
    priceBox.y < nameBox.y + nameBox.height && priceBox.y + priceBox.height > nameBox.y;
  expect(sameBand, "the desktop row no longer carries the price beside the name").toBe(true);
  expect(priceBox.x, "the price is not to the name's right on the desktop row").toBeGreaterThan(
    nameBox.x,
  );
});

test("at 390 the line table drops its weight column and keeps qty, price and remove reachable", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  const row = await searchDropRow(page, "holland", "Holland Stone 60 mm");
  await row.click();
  const lineRow = page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" });
  await expect(lineRow).toHaveCount(1);
  // The weight column dropped at phone width (the cell renders, the column
  // does not), and the table itself scrolls inside its card: that scroll is
  // what keeps the right-hand columns usable.
  await expect(lineRow.getByTestId("pos-line-weight")).toBeHidden();
  await expect(page.getByTestId("pos-lines")).toHaveCSS("overflow-x", "auto");
  // The qty control acts: scrolled into view by the table's own scroll, the
  // increase press lands and the box reads 2.
  const plus = lineRow.getByRole("button", { name: "Increase quantity: Holland Stone 60 mm" });
  await plus.scrollIntoViewIfNeeded();
  await expect(plus).toBeVisible();
  await plus.click();
  const qty = lineRow.getByRole("textbox", { name: "Quantity: Holland Stone 60 mm" });
  await expect(qty).toHaveValue("2");
  // The line's price is inside the viewport once the table scrolls it in.
  const price = lineRow.locator(".lprice");
  await price.scrollIntoViewIfNeeded();
  await expect(price).toBeVisible();
  const priceBox = (await price.boundingBox()) as { x: number; y: number; width: number; height: number };
  expect(insideViewport(priceBox), "the line's price hangs outside the viewport at 390").toBe(true);
  // The remove control is reachable and acts: the press takes the line off.
  const remove = lineRow.getByRole("button", { name: "Remove Holland Stone 60 mm" });
  await remove.scrollIntoViewIfNeeded();
  const removeBox = (await remove.boundingBox()) as { x: number; y: number; width: number; height: number };
  expect(insideViewport(removeBox), "the remove control hangs outside the viewport at 390").toBe(true);
  await remove.click();
  await expect(lineRow).toHaveCount(0);
});

/**
 * The page itself must hold the 390 viewport: the visual review caught the
 * DOCUMENT scrolling sideways at 390 (scroll width 551 against the viewport)
 * even after the rows and the table were fixed, so the pin is on the
 * document's scroll width, not on any one row or table.
 */
async function expectDocumentHolds(page: Page): Promise<void> {
  const widths = await page.evaluate(() => ({
    doc: document.documentElement.scrollWidth,
    view: window.innerWidth,
  }));
  expect(
    widths.doc,
    `the document's scroll width ${widths.doc} runs past the ${widths.view} viewport at 390`,
  ).toBeLessThanOrEqual(widths.view);
}

test("at 390 the page itself holds the viewport with an empty ticket: no sideways document scroll", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await expectDocumentHolds(page);
  // The scroll allowance is the boxes' own: the step bar scrolls inside its
  // box at phone width, the same idiom the line table takes in its card.
  await expect(page.locator("hos-pos-flow-steps")).toHaveCSS("overflow-x", "auto");
});

test("at 390 the page itself holds the viewport with a priced line: no sideways document scroll", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  const row = await searchDropRow(page, "holland", "Holland Stone 60 mm");
  await row.click();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" })).toHaveCount(1);
  await expectDocumentHolds(page);
});

test("at 390 the bar's controls, Feedback included, wrap and never widen the page", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await expect(page.getByTestId("feedback-button")).toBeVisible();
  await expectDocumentHolds(page);
});

test("at desktop width the line table keeps its weight column and needs no sideways scroll", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await signIn(page);
  const row = await searchDropRow(page, "holland", "Holland Stone 60 mm");
  await row.click();
  const lineRow = page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" });
  await expect(lineRow).toHaveCount(1);
  await expect(lineRow.getByTestId("pos-line-weight")).toBeVisible();
  // The scroll allowance is the narrow layout's alone: at desktop width the
  // table does not scroll inside its card.
  await expect(page.getByTestId("pos-lines")).toHaveCSS("overflow-x", "visible");
});

/** Round 9: the stepper fits its box at phone width; where it cannot, it scrolls inside itself, never clips. */
for (const width of [390, 360, 320]) {
  test(`at ${width} every step of the stepper is whole inside its box, or the box scrolls`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await signIn(page);
    const read = await page.evaluate(() => {
      const host = document
        .querySelector("ct-app-root")
        ?.shadowRoot?.querySelector("hos-pos-terminal")
        ?.shadowRoot?.querySelector("hos-pos-flow-steps") as HTMLElement | null;
      if (host === null || host === undefined) return null;
      const box = host.getBoundingClientRect();
      const buttons = Array.from(host.shadowRoot?.querySelectorAll("button") ?? []);
      return {
        scrolls: host.scrollWidth > host.clientWidth + 1,
        overflowX: getComputedStyle(host).overflowX,
        count: buttons.length,
        outside: buttons
          .map((b) => b.getBoundingClientRect())
          .filter((r) => r.right > box.right + 0.5 || r.left < box.left - 0.5).length,
        // A pill's own content (its numeral chip and its label) stays inside the pill: no step
        // paints over its neighbour.
        spilling: buttons.filter(
          (b) => b.scrollWidth > b.clientWidth + 1 || Array.from(b.querySelectorAll("span")).some((span) => span.scrollWidth > span.clientWidth + 1 && !span.classList.contains("sr")),
        ).length,
        overlapping: buttons.filter((b, i) => i > 0 && b.getBoundingClientRect().left < (buttons[i - 1] as HTMLElement).getBoundingClientRect().right - 0.5).length,
      };
    });
    expect(read).not.toBeNull();
    expect(read!.count).toBe(4);
    // Whole inside the box, or the box is one that scrolls (and says so by overflow-x).
    if (read!.scrolls) expect(["auto", "scroll"]).toContain(read!.overflowX);
    else expect(read!.outside).toBe(0);
    // At the common phone widths it simply fits: no scrolling needed to reach Summary.
    if (width >= 360) expect(read!.scrolls).toBe(false);
    if (!read!.scrolls) {
      expect(read!.spilling).toBe(0);
      expect(read!.overlapping).toBe(0);
    }
  });
}

/**
 * PR 253 review P2-2: no width from 320 to 480 cuts a step's label short. Sampled every 5 px: at
 * each width every label shows whole (nothing ellipsised, nothing spilling out of its pill, no pill
 * over its neighbour), either because the four fit or because the row scrolls inside itself.
 */
test("no width from 320 to 480 cuts a step label short, sampled every 5 px", async ({ page }) => {
  await page.setViewportSize({ width: 480, height: 844 });
  await signIn(page);
  const cut: string[] = [];
  for (let width = 320; width <= 480; width += 5) {
    await page.setViewportSize({ width, height: 844 });
    const read = await page.evaluate(() => {
      const host = document
        .querySelector("ct-app-root")
        ?.shadowRoot?.querySelector("hos-pos-terminal")
        ?.shadowRoot?.querySelector("hos-pos-flow-steps") as HTMLElement | null;
      if (host === null || host === undefined) return null;
      const buttons = Array.from(host.shadowRoot?.querySelectorAll("button") ?? []) as HTMLElement[];
      const labels = buttons.map((b) => Array.from(b.querySelectorAll("span")).filter((s) => !s.classList.contains("sr") && !s.classList.contains("num"))[0] as HTMLElement);
      return {
        cut: labels.filter((l) => l.scrollWidth > l.clientWidth + 0.5 || getComputedStyle(l).textOverflow === "ellipsis" && l.scrollWidth > l.clientWidth).map((l) => l.textContent?.trim() ?? ""),
        overlap: buttons.filter((b, i) => i > 0 && b.getBoundingClientRect().left < (buttons[i - 1] as HTMLElement).getBoundingClientRect().right - 0.5).length,
        scrolls: host.scrollWidth > host.clientWidth + 1,
        overflowX: getComputedStyle(host).overflowX,
      };
    });
    if (read === null) throw new Error("the stepper did not render");
    if (read.cut.length > 0 || read.overlap > 0) cut.push(`${width}px: cut ${read.cut.join("/")} overlap ${read.overlap}`);
    if (read.scrolls && !["auto", "scroll"].includes(read.overflowX)) cut.push(`${width}px: overflows without a scroll box`);
  }
  expect(cut, "widths where a label is cut short").toEqual([]);
});
