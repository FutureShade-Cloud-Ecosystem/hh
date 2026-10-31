// UX-120 round 2: the demo seed's pricing audit ids resolve to names.
//
// The round 1 read (REVIEW-PR734-sonnet.md, P2-1) left point 4 proven only on
// the test stub's own convenience ids. What the real demo seed writes, traced:
//
//   - user_id is the persona's users.id as resolvePersonas answers it
//     (core/cmd/core/demoseed_resolve.go:95): the demo populate's pinned
//     logins, Sales manager de110000-...0002 on the bypass row and Sales
//     de110000-...0001 on the update rows (core/internal/demo/populate.go:70-78).
//     The staff roster the Dealer Admin loads carries the same users.id
//     (core/internal/identity/staffroster.go), so the actor reads as a name.
//   - entity_type is margin_enforcement and entity_id is the category's id,
//     or the product's own seeded id when the line names no category
//     (core/cmd/core/demoseed_quotes.go:365-391). The product id is
//     demoID("product:P02"), a UUIDv5 over the seed's namespace
//     (core/cmd/core/demoseed_ids.go:62).
//
// The stub fixture mirrors those ids exactly (the populate's literals, the
// seed's uuidv5), and this leg walks the pricing audit and asserts both rows
// read as names, never "Unknown actor" or "Unknown entity".
//
// Principal item 386 blank check: set UX120_BLANK_MODE to "screen" or "core"
// to verify that the leg fails on an empty screen and on an empty core.
import { expect, test, type Page } from "@playwright/test";

const UX120_BLANK_MODE = "" as "" | "screen" | "core";

// The demo populate's own literals (populate.go salesUserID, managerUserID).
const SALES_USER_ROW = "0e110000-0000-4000-9000-0000000000a4";
const MANAGER_USER_ROW = "0e110000-0000-4000-9000-0000000000a3";

// demoID("product:P02"), the seed's id for the Brussels paver, verified
// against uuid.NewSHA1 on the seed's namespace.
const DEMO_PRODUCT_ID = "62e4ad55-5a14-5945-80ee-866864a6c774";

async function signIn(page: Page): Promise<void> {
  await page.request.post("/_stub/reset");
  if (UX120_BLANK_MODE === "core") {
    await page.request.post("/_stub/scenario", { data: { name: "empty-pricing" } });
  }
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count], [data-test=staff-empty]").first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

async function expectVisible(page: Page, settle: string): Promise<void> {
  const visible = page.locator(settle.split(",").map((s) => `${s.trim()}:visible`).join(", ")).first();
  await expect(visible).toBeVisible({ timeout: 30_000 });
}

async function openSurface(page: Page, surface: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    const list = page.locator(".surface-list");
    if (!(await list.isVisible())) {
      await page.locator('[data-action="toggle-surface-list"]').click();
    }
    await expect(list).toBeVisible();
  }
  await page.locator(`.surface-link[data-surface="${surface}"]`).click();
  await expectVisible(page, settle);
  await page.waitForLoadState("networkidle");
}

async function openPricingView(page: Page, title: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator("[data-test=pr-tab-select]").selectOption({ label: title });
  } else {
    await page.locator(".pr-tab").filter({ hasText: title }).first().click();
  }
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

test.afterEach(async ({ page }) => {
  if (UX120_BLANK_MODE !== "") {
    await page.request.post("/_stub/reset");
  }
});

test("UX-120 round 2: the pricing audit reads the demo seed's actor and entity ids as names", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await openSurface(page, "pricing", '[data-test="pr-tab-select"], .pr-tabs');
  await openPricingView(page, "Pricing audit", '[data-test="pr-audit-count"], [data-test="pr-audit-empty"]');
  if (UX120_BLANK_MODE === "screen") await page.setContent("");

  // The seed-shaped rows are on the log: the bypass against the category, and
  // the rule price row whose entity is the product's seeded id.
  const managerRow = page.locator(`tr[data-audit="${MANAGER_USER_ROW}"]`);
  await expect(managerRow).toHaveCount(1, { timeout: 5_000 });
  const productRow = page.locator(`tr[data-audit="${SALES_USER_ROW}"]`);
  await expect(productRow).toHaveCount(1);

  // The seeded actors read as the staff register's names, exactly as the
  // roster carries them: the manager on the bypass row, the owner on the
  // update row. Never "Unknown actor".
  await expect(managerRow.locator('td[data-label="Actor"]')).toHaveText("Sales manager (demo)");
  await expect(productRow.locator('td[data-label="Actor"]')).toHaveText("Sales (demo)");

  // A margin enforcement entity reads as its category's name, or as the
  // product's own name through the catalog read. Never "Unknown entity".
  await expect(managerRow.locator('td[data-label="Entity"]')).toHaveText("Margin enforcement / Pavers");
  const productEntity = productRow.locator('td[data-label="Entity"]');
  await expect(productEntity).toContainText("Brussels Dimensional");
  await expect(productEntity).not.toContainText(DEMO_PRODUCT_ID.slice(0, 8));

  // Neither placeholder word appears anywhere on the pricing audit.
  const work = page.locator(".surface-work");
  await expect(work).not.toContainText("Unknown actor");
  await expect(work).not.toContainText("Unknown entity");
});
