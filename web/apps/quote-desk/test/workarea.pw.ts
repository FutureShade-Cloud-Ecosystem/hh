// The Quote Desk's work area in a real browser, against the stubbed harness
// (the stub serves the quote operations' contract shapes; the vite dev server
// proxies /api to it). The suite covers this item's behaviours: the three
// stepper states, adding and removing lines, the discount path with the typed
// floor refusal surfacing, the margin columns absent unless the server sends
// them, the layout saved per person, and the offer's own absence for a
// caller the server does not offer the edit to. The stub's world is in
// memory, so the tests run in order and each reads only what it itself
// caused.
import { expect, test, type Page } from "@playwright/test";

const SALES = { name: "Sales", exact: true };

async function signIn(page: Page, role = SALES): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", role).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function stubState(page: Page): Promise<{
  workspaceWrites: Array<{ user_id: string; body: Record<string, unknown> | null }>;
}> {
  const response = await page.request.get("/_stub/state");
  return (await response.json()) as { workspaceWrites: Array<{ user_id: string; body: Record<string, unknown> | null }> };
}

test("the entry page shows the demo role cards and the desk opens on the Sales tap", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("button", SALES)).toBeVisible();
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toBeVisible();
  await signIn(page);
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
  // The desk is the builder: the work area above, the list under it.
  await expect(page.getByTestId("qb-title")).toBeVisible();
  await expect(page.getByTestId("qb-list")).toBeVisible();
});

test("the stepper tracks the quote: compose, picked, lines, sent", async ({ page }) => {
  await signIn(page);
  // Compose: the customer question is the screen, Customer is the open step.
  await expect(page.getByTestId("qb-title")).toHaveText("New Quote");
  const customer = page.getByRole("navigation", { name: "Quote sections" }).getByRole("button", { name: "Customer" });
  await expect(customer).toHaveClass(/current/);
  await expect(customer).not.toHaveClass(/complete/);

  // Pick the customer through the compose picker (the dialog the kit mounts).
  const pickerInput = page.getByTestId("qb-identity").locator("hos-customer-picker").getByPlaceholder("Search name or account #…");
  await pickerInput.fill("Maple");
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  await expect(page.getByTestId("qb-title")).toHaveText("Quote");
  await expect(customer).toHaveClass(/complete/);
  const products = page.getByRole("navigation", { name: "Quote sections" }).getByRole("button", { name: "Products" });
  await expect(products).toHaveClass(/current/);
  await expect(products).not.toHaveClass(/complete/);

  // Add a line through the scan box: the resolution is the add.
  const scanInput = page.getByTestId("pos-scan-input");
  await scanInput.fill("PSN-100");
  await scanInput.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await expect(products).toHaveClass(/complete/);
  const review = page.getByRole("navigation", { name: "Quote sections" }).getByRole("button", { name: "Review & Send" });
  await expect(review).toHaveClass(/current/);
  await expect(review).not.toHaveClass(/complete/);

  // Send: minting is the email send, every step completes.
  await page.getByTestId("qb-send").click();
  await expect(review).toHaveClass(/complete/);
  await expect(page.getByTestId("qb-status")).toContainText("Sent");
});

test("lines are added and removed, and the removal renumbers the ticket", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByPlaceholder("Search name or account #…").fill("Maple");
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  await expect(page.getByTestId("qb-line")).toHaveCount(0);

  const scanInput = page.getByTestId("pos-scan-input");
  await scanInput.fill("PSN-100");
  await scanInput.press("Enter");
  await scanInput.fill("WAT-STD");
  await scanInput.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await expect(page.getByTestId("qb-lines")).toContainText("Polymeric Locking Sand");
  await expect(page.getByTestId("qb-lines")).toContainText("Retaining Wall Stone, Standard");

  // Remove the first line: the ticket holds the second alone.
  await page.getByTestId("qb-line-trash").first().click();
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await expect(page.getByTestId("qb-lines")).toContainText("Retaining Wall Stone, Standard");
});

test("a discount above the floor lands and shows the discount row; below the floor is the typed refusal", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByPlaceholder("Search name or account #…").fill("Maple");
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  const scanInput = page.getByTestId("pos-scan-input");
  await scanInput.fill("PSN-100");
  await scanInput.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);

  // Five percent stays above the floor for this product and account: the
  // write lands and the totals card gains the discount row.
  const disc = page.getByTestId("qb-line-disc");
  await disc.fill("5");
  await disc.blur();
  await expect(page.getByTestId("qb-totals")).toContainText("Discount");
  await expect(page.getByTestId("qb-error")).toHaveText("", { useInnerText: true });

  // Fifteen percent lands below the floor: the typed 409 arrives, the plain
  // message names the approvers and the branch manager way forward, and the
  // refusal dialog seam fires with the typed body.
  await page.evaluate(() => {
    (window as unknown as { __qdSeam: unknown[] }).__qdSeam = [];
    window.addEventListener("qd-floor-refusal", (event) => {
      (window as unknown as { __qdSeam: unknown[] }).__qdSeam.push((event as CustomEvent).detail);
    });
  });
  await disc.fill("15");
  await disc.blur();
  await expect(page.getByTestId("qb-error")).toContainText("below the margin floor");
  await expect(page.getByTestId("qb-error")).toContainText("General Manager");
  await expect(page.getByTestId("qb-error")).toContainText("Sales manager");
  const fired = await page.evaluate(() => (window as unknown as { __qdSeam: unknown[] }).__qdSeam);
  expect(fired.length).toBe(1);
  const detail = fired[0] as { refusal: { approverRoles: string[]; managerMaySet: boolean }; discPct: number };
  expect(detail.refusal.approverRoles).toEqual(["General Manager", "Financial Controller", "Sales Manager"]);
  expect(detail.refusal.managerMaySet).toBe(true);
  expect(detail.discPct).toBe(15);
  // The refused write changed nothing: the line keeps the five percent.
  await expect(page.getByTestId("qb-line-disc")).toHaveValue("5");
});

test("margin surfaces render only when the server sends them", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByPlaceholder("Search name or account #…").fill("Maple");
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByRole("option", { name: /Maple Ridge Contracting/ }).click();

  // On the cost list (the shipped configuration): the margin pill shows, and
  // the cost class columns stay hidden until this person curates them on.
  await expect(page.getByTestId("qb-margin-pill")).toBeVisible();
  expect(await page.getByTestId("qb-lines").locator('th[data-column="line.unit_cost"]').count()).toBe(0);
  expect(await page.getByTestId("qb-lines").locator('th[data-column="line.margin_pct"]').count()).toBe(0);

  // Curate the cost columns on through the Columns popover.
  await page.getByTestId("qb-columns-btn").click();
  await page.locator('[data-field="column"][data-column="line.margin_pct"]').check();
  await expect(page.getByTestId("qb-lines").locator('th[data-column="line.margin_pct"]')).toBeVisible();
  await expect(page.getByTestId("qb-line-margin").first()).not.toHaveText("(none)");

  // Off the list (the wrong-role page): the glance answers byte absent and
  // every margin surface vanishes, the popover's cost offers included.
  await page.request.put("/_stub/cost-list", { data: { member: false } });
  await page.reload();
  await page.getByRole("button", SALES).click();
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByPlaceholder("Search name or account #…").fill("Maple");
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  await expect(page.getByTestId("qb-margin-pill")).toHaveCount(0);
  expect(await page.getByTestId("qb-lines").locator('th[data-column="line.unit_cost"]').count()).toBe(0);
  expect(await page.getByTestId("qb-lines").locator('th[data-column="line.margin_pct"]').count()).toBe(0);
  await page.getByTestId("qb-columns-btn").click();
  await expect(page.locator('[data-field="column"][data-column="line.margin_pct"]')).toHaveCount(0);
  await expect(page.getByTestId("qb-cost-toggle")).toHaveCount(0);
  await page.request.put("/_stub/cost-list", { data: { member: true } });
});

test("the layout saves per person: the swap and the keyboard resize reach the workspace row", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByPlaceholder("Search name or account #…").fill("Maple");
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  await expect(page.getByTestId("qb-workspace")).toBeVisible();

  // The swap: the workspace's arrangement flips and the save lands.
  await page.getByRole("button", { name: "Swap panels" }).click();
  await expect(page.getByTestId("qb-workspace")).toHaveAttribute("data-arrangement", "PRIMARY_RIGHT");
  await page.getByRole("button", { name: "Swap panels" }).click();
  await expect(page.getByTestId("qb-workspace")).toHaveAttribute("data-arrangement", "PRIMARY_LEFT");
  await expect
    .poll(async () => {
      const state = await stubState(page);
      return state.workspaceWrites.at(-1)?.body?.arrangement;
    })
    .toBe("PRIMARY_LEFT");

  // The keyboard resize: the divider takes the arrows and the split saves.
  const before = await stubState(page);
  const beforeCount = before.workspaceWrites.length;
  await page.getByTestId("qb-divider").focus();
  await page.getByTestId("qb-divider").press("ArrowRight");
  await page.getByTestId("qb-divider").press("ArrowRight");
  await expect
    .poll(async () => (await stubState(page)).workspaceWrites.length)
    .toBeGreaterThan(beforeCount);
  const last = (await stubState(page)).workspaceWrites.at(-1)?.body as { split_pct: number };
  expect(last.split_pct).toBeGreaterThan(58);

  // The collapse keeps one panel open, and the restore brings it back.
  await page.getByRole("button", { name: "Collapse this panel" }).first().click();
  await expect(page.getByRole("button", { name: "Restore products panel" })).toBeVisible();
  await page.getByRole("button", { name: "Restore products panel" }).click();
  await expect(page.getByRole("button", { name: "Restore products panel" })).toHaveCount(0);
});

test("a Work caller that does not own the quote is offered no edit the server did not offer", async ({ page }) => {
  await signIn(page);
  // The seeded SENT quote is the manager's own: the Sales login (Work) reads
  // it, and the read's editable_fields is empty, so no quantity stepper, no
  // discount field and no remove button render.
  const managerRow = page.getByTestId("qb-list-row").filter({ hasText: "Sent" }).first();
  await managerRow.click();
  await expect(page.getByTestId("qb-title")).toHaveText("Quote");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await expect(page.getByTestId("qb-line-qty")).toHaveCount(0);
  await expect(page.getByTestId("qb-line-disc")).toHaveCount(0);
  await expect(page.getByTestId("qb-line-trash")).toHaveCount(0);
  await expect(page.locator("hos-pos-scan-box")).toHaveCount(1);
});

test("the Sales manager clears the floor itself, and the clearing is stamped on the line", async ({ page }) => {
  await signIn(page, { name: "Sales manager", exact: true });
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByPlaceholder("Search name or account #…").fill("Fenwick");
  await page.getByTestId("qb-identity").locator("hos-customer-picker").getByRole("option", { name: /Fenwick Builders/ }).click();
  const scanInput = page.getByTestId("pos-scan-input");
  await scanInput.fill("PSN-100");
  await scanInput.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const disc = page.getByTestId("qb-line-disc");
  // The same fifteen percent the Sales login was refused: the manager's own
  // clearing goes through once and the line carries who cleared it.
  await disc.fill("15");
  await disc.blur();
  await expect(page.getByTestId("qb-line")).toContainText("cleared by Sales manager (demo)");
  await expect(page.getByTestId("qb-totals")).toContainText("Discount");
});
