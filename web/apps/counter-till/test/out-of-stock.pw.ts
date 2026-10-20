// Out of stock display (UX-51, audit a P1-2): a product at or below zero on
// hand reads "Out of stock here" (with no negative figure) in the search drop,
// on the ticket line, and in the browse product grid, while a positive count
// still shows its on-hand figure as today.
import { expect, test, type Page } from "@playwright/test";

async function stubStock(page: Page): Promise<void> {
  await page.route("**/api/v1/products/counter-search**", async (route) => {
    const res = await route.fetch();
    const body = (await res.json()) as { items?: Array<Record<string, unknown>> };
    const items = (body.items ?? []).map((item) => {
      if (item.product_id === "p-charcoal-60") {
        const byBranch = (item.on_hand_by_branch ?? {}) as Record<string, number>;
        const updated: Record<string, number> = {};
        for (const k of Object.keys(byBranch)) updated[k] = -3;
        return { ...item, on_hand_branch: -3, on_hand_by_branch: updated };
      }
      if (item.product_id === "p-clear-stone") {
        const byBranch = (item.on_hand_by_branch ?? {}) as Record<string, number>;
        const updated: Record<string, number> = {};
        for (const k of Object.keys(byBranch)) updated[k] = 12;
        return { ...item, on_hand_branch: 12, on_hand_by_branch: updated };
      }
      return item;
    });
    await route.fulfill({ response: res, json: { ...body, items } });
  });

  await page.route(
    (url) => url.pathname === "/api/v1/products",
    async (route) => {
      const res = await route.fetch();
      const rows = (await res.json()) as Array<Record<string, unknown>>;
      const patched = rows.map((row) => {
        if (row.id === "p-charcoal-60") {
          const byBranch = (row.on_hand_by_branch ?? {}) as Record<string, number>;
          const updated: Record<string, number> = {};
          for (const k of Object.keys(byBranch)) updated[k] = -3;
          return { ...row, on_hand_by_branch: updated };
        }
        if (row.id === "p-clear-stone") {
          const byBranch = (row.on_hand_by_branch ?? {}) as Record<string, number>;
          const updated: Record<string, number> = {};
          for (const k of Object.keys(byBranch)) updated[k] = 12;
          return { ...row, on_hand_by_branch: updated };
        }
        return row;
      });
      await route.fulfill({ response: res, json: patched });
    },
  );

  await page.route("**/api/v1/pos/stock-glance**", async (route) => {
    const res = await route.fetch();
    const body = (await res.json()) as Record<string, unknown>;
    if (body.product_id === "p-charcoal-60") {
      const byBranch = (body.on_hand ?? {}) as Record<string, number>;
      const updated: Record<string, number> = {};
      for (const k of Object.keys(byBranch)) updated[k] = -3;
      await route.fulfill({ response: res, json: { ...body, on_hand: updated } });
      return;
    }
    if (body.product_id === "p-clear-stone") {
      const byBranch = (body.on_hand ?? {}) as Record<string, number>;
      const updated: Record<string, number> = {};
      for (const k of Object.keys(byBranch)) updated[k] = 12;
      await route.fulfill({ response: res, json: { ...body, on_hand: updated } });
      return;
    }
    await route.fulfill({ response: res, json: body });
  });
}

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  const dot = page.getByTestId("session-dot");
  const openButton = page.getByRole("button", { name: "Open Session", exact: true });
  await expect(dot.or(openButton).first()).toBeVisible({ timeout: 10_000 });
  if (!(await dot.isVisible())) await openButton.click({ timeout: 3_000 }).catch(() => undefined);
  await expect(dot).toContainText("Open");
}

test("a product at -3 on hand reads 'Out of stock here' on the search row and the ticket line, while 12 still reads '12 on hand'", async ({
  page,
}) => {
  await stubStock(page);
  await signIn(page);

  const field = page.getByTestId("pos-scan-input");
  await page.keyboard.press("F2");
  await expect(field).toBeFocused();
  await field.fill("stone");

  const outRow = page.getByTestId("drop-row").filter({ hasText: "Charcoal Stone 60 mm" }).first();
  const inRow = page.getByTestId("drop-row").filter({ hasText: "Clear Stone" }).first();
  await expect(outRow).toBeVisible();
  await expect(inRow).toBeVisible();

  await expect(outRow).toContainText("Out of stock here");
  await expect(outRow).not.toContainText("-3");
  await expect(inRow).toContainText("12 on hand");

  await outRow.click();
  const line = page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" }).first();
  await expect(line).toBeVisible();
  await expect(line).toContainText("Out of stock here");
  await expect(line).not.toContainText("-3");
  await expect(line).not.toContainText("Out of stock at this branch");

  const browse = page.getByTestId("pos-browse");
  await browse.locator("summary").click();
  const grid = browse.locator("hos-pos-product-grid");
  await expect(grid.locator(".card").filter({ hasText: "Charcoal Stone 60 mm" }).first()).toContainText(
    "Out of stock here",
  );
  await expect(grid.locator(".card").filter({ hasText: "Charcoal Stone 60 mm" }).first()).not.toContainText("-3");
  await expect(grid.locator(".card").filter({ hasText: "Clear Stone" }).first()).toContainText("12");

  await page.getByTestId("lookup-view-rows").click();
  await expect(grid.locator(".rowitem").filter({ hasText: "Charcoal Stone 60 mm" }).first()).toContainText(
    "Out of stock here",
  );
  await expect(grid.locator(".rowitem").filter({ hasText: "Charcoal Stone 60 mm" }).first()).not.toContainText("-3");
  await expect(grid.locator(".rowitem").filter({ hasText: "Clear Stone" }).first()).toContainText("12");
});

test("at 390x844 a long product name at 0 on hand keeps 'Out of stock here' whole on the search row", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route("**/api/v1/products/counter-search**", async (route) => {
    const res = await route.fetch();
    const body = (await res.json()) as { items?: Array<Record<string, unknown>> };
    const items = (body.items ?? []).map((item) => {
      if (item.product_id !== "p-charcoal-60") return item;
      const byBranch = (item.on_hand_by_branch ?? {}) as Record<string, number>;
      const updated: Record<string, number> = {};
      for (const k of Object.keys(byBranch)) updated[k] = 0;
      return {
        ...item,
        name: "Charcoal Stone 60 mm Permeable Interlocking Paver Heavy Duty Driveway Grade Pallet",
        sku: "CHAR-STN-60-PERM-HD",
        on_hand_branch: 0,
        on_hand_by_branch: updated,
      };
    });
    await route.fulfill({ response: res, json: { ...body, items } });
  });
  await signIn(page);

  const head = page.getByTestId("step-head-items");
  for (let attempt = 0; attempt < 5; attempt += 1) {
    if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
    if ((await head.getAttribute("aria-expanded")) === "true") break;
  }
  const field = page.getByTestId("pos-scan-input");
  await field.fill("stone");
  const row = page.getByTestId("drop-row").filter({ hasText: "Permeable Interlocking" }).first();
  await expect(row).toBeVisible();
  const words = row.locator(".out");
  await expect(words).toHaveText("Out of stock here");
  await expect(words).toBeVisible();
  // The words are an inline span (its own scrollWidth is always 0), so the clip is read on the
  // line that holds them: nothing of it overflows, and the words end inside its box.
  const clip = await words.evaluate((el) => {
    const meta = el.closest(".meta") as HTMLElement;
    const r = el.getBoundingClientRect();
    const m = meta.getBoundingClientRect();
    return {
      overflow: meta.scrollWidth - meta.clientWidth,
      past: r.right - m.right,
      ellipsis: getComputedStyle(el).textOverflow === "ellipsis" || getComputedStyle(meta).textOverflow === "ellipsis",
      clipped: getComputedStyle(meta).overflow !== "visible" && meta.scrollWidth > meta.clientWidth,
    };
  });
  expect(clip.overflow, "the stock words overflow their line").toBeLessThanOrEqual(0);
  expect(clip.past, "the stock words run past their line").toBeLessThanOrEqual(0.5);
  expect(clip.clipped, "the stock words are cut").toBe(false);
});
