// UX-117 browser legs: Dealer Admin forms pick customers, products, categories,
// vendors, branches, profiles, batches, staff, channels and surface items;
// they never ask a dealer to type a raw id.
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type { Page } from "@playwright/test";
import { test, expect } from "@playwright/test";

async function signInAsDealerAdmin(page: Page): Promise<void> {
  await page.request.post("/_stub/reset");
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator('[data-test="staff-count"]')).toBeVisible();
}

async function collectRawIdFields(page: Page, stateName: string): Promise<string[]> {
  return page.evaluate((state) => {
    const root = document.querySelector("dealer-admin-app")?.shadowRoot;
    if (!root) return [`${state}: missing dealer-admin-app shadowRoot`];
    const violations: string[] = [];

    for (const input of root.querySelectorAll<HTMLElement>(".text-field")) {
      const field = input.getAttribute("data-field") ?? "(no data-field)";
      const aria = input.getAttribute("aria-label") ?? "";
      const placeholder = input.getAttribute("placeholder") ?? "";
      const labelEl = input.closest("label.field")?.querySelector(".field-label");
      const label = labelEl?.textContent?.trim() ?? "";

      const idPattern = /\bid\b/i;
      if (idPattern.test(label) || idPattern.test(aria) || idPattern.test(placeholder)) {
        violations.push(
          `${state} | field=${field} | label="${label}" | aria="${aria}" | placeholder="${placeholder}"`,
        );
      }
    }

    const workText = root.querySelector(".surface-work")?.textContent ?? "";
    const badPhrases = [
      "Type a customer id",
      "Type the product id",
      "so the id is typed whole",
      "by id.",
    ];
    for (const phrase of badPhrases) {
      if (workText.includes(phrase)) {
        violations.push(`${state} | copy="${phrase}"`);
      }
    }
    return violations;
  }, stateName);
}

test("no dealer-admin form carries a raw id text input or asks the dealer to type an id", async ({ page }) => {
  await signInAsDealerAdmin(page);
  const allViolations: string[] = [];

  await page.locator('.surface-link[data-surface="pricing"]').click();
  await expect(page.locator('[data-test="pr-margin-count"]')).toBeVisible();

  // 1. Rules editor
  await page.locator('.pr-tab[data-tab="rules"]').click();
  await expect(page.locator('[data-test="pr-rules-count"]')).toBeVisible();
  await page.locator('[data-action="pr-rule-edit"]').first().click();
  await expect(page.locator('[data-test="pr-rule-editor"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "pricing:rules")));

  // 2. Customer prices
  await page.locator('.pr-tab[data-tab="customers"]').click();
  await expect(page.locator('[data-test="pr-cust-empty"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "pricing:customers")));

  // 3. Quantity breaks editor
  await page.locator('.pr-tab[data-tab="breaks"]').click();
  await expect(page.locator('[data-test="pr-breaks-count"]')).toBeVisible();
  await page.locator('[data-action="pr-break-edit"]').first().click();
  await expect(page.locator('[data-test="pr-break-editor"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "pricing:breaks")));

  // 4. Promotions and clearance editors
  await page.locator('.pr-tab[data-tab="promos"]').click();
  await expect(page.locator('[data-test="pr-promo-count"]')).toBeVisible();
  await page.locator('[data-action="pr-promo-edit"]').first().click();
  await page.locator('[data-action="pr-clear-edit"]').first().click();
  await expect(page.locator('[data-test="pr-promo-editor"]')).toBeVisible();
  await expect(page.locator('[data-test="pr-clear-editor"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "pricing:promos")));

  // 5. Rebates editor and accrual form
  await page.locator('.pr-tab[data-tab="rebates"]').click();
  await expect(page.locator('[data-test="pr-rebate-count"]')).toBeVisible();
  await page.locator('[data-action="pr-rebate-edit"]').first().click();
  await expect(page.locator('[data-test="pr-rebate-editor"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "pricing:rebates")));

  // 6. Units of measure probe and conversion editor
  await page.locator('.pr-tab[data-tab="uom"]').click();
  await expect(page.locator('[data-test="pr-uom-count"]')).toBeVisible();
  await page.locator('[data-action="pr-uom-edit"]').first().click();
  await expect(page.locator('[data-test="pr-uom-editor"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "pricing:uom")));

  // 7. Batches create form
  await page.locator('.pr-tab[data-tab="batches"]').click();
  await expect(page.locator('[data-test="pr-batches-count"]')).toBeVisible();
  await page.locator('[data-action="pr-batch-new"]').click();
  await expect(page.locator('[data-test="pr-batch-create"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "pricing:batches")));

  // 8. Low margin probe
  await page.locator('.pr-tab[data-tab="lowmargin"]').click();
  await expect(page.locator('[data-test="pr-lm-count"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "pricing:lowmargin")));

  // 9. Change log filters
  await page.locator('.pr-tab[data-tab="changelog"]').click();
  await expect(page.locator('[data-test="pr-log-count"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "pricing:changelog")));

  // 10. Audit filters
  await page.locator('.surface-link[data-surface="audit"]').click();
  await expect(page.locator('[data-test="audit-count"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "audit")));

  // 11. Exports filters
  await page.locator('.surface-link[data-surface="exports"]').click();
  await expect(page.locator('[data-action="wb-download"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "exports")));

  // 12. Role configuration editor
  await page.locator('.surface-link[data-surface="role-config"]').click();
  await expect(page.locator('[data-test="config-roles-count"]')).toBeVisible();
  await openShape(page, "yard");
  await page.locator('[data-test="config-cell-counter-till-yard"]').click();
  await expect(page.locator('[data-test="config-editor"]')).toBeVisible();
  allViolations.push(...(await collectRawIdFields(page, "role-config")));

  expect(allViolations, `raw id inputs or copy found:\n${allViolations.join("\n")}`).toEqual([]);
});

test("customer picker: search by name, choose the account, see its name, and send its id", async ({ page }) => {
  await signInAsDealerAdmin(page);
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="customers"]').click();

  await expect(page.locator('[data-test="pr-cust-empty"]')).toContainText("Choose a customer and load their prices");

  const search = page.locator('[data-picker="pr-cust-id"] input[type="search"]');
  await search.fill("Point Loma");
  const option = page.locator('[data-action="picker-choose"][data-picker-field="pr-cust-id"]').first();
  await expect(option).toContainText("Point Loma Hardscape & Masonry");
  await option.click();

  await expect(page.locator('[data-picker="pr-cust-id"]')).toContainText("Point Loma Hardscape & Masonry");
  await page.locator('[data-action="pr-cust-load"]').click();
  await expect(page.locator('[data-test="pr-cust-count"]')).toHaveText("1 price");

  const log = await page.request.get("/_stub/log").then((r) => r.json());
  const readCall = log.find(
    (e: { method: string; path: string }) =>
      e.method === "GET" &&
      e.path === "/api/v1/pricing/customers/cu110000-0000-4000-9000-0000000000a1/overrides",
  );
  expect(readCall).toBeTruthy();

  // Optional customer picker in the rules editor keeps its explicit empty meaning.
  await page.locator('.pr-tab[data-tab="rules"]').click();
  await page.locator('[data-action="pr-rule-edit"]').first().click();
  await expect(page.locator('[data-picker="pr-rule-customer"]')).toContainText("Any customer");
});

test("product picker: search by name or SKU, choose the product, see its name, and send its id", async ({ page }) => {
  await signInAsDealerAdmin(page);
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await page.locator('.pr-tab[data-tab="uom"]').click();

  const search = page.locator('[data-picker="pr-uom-probe-product"] input[type="search"]');
  await search.fill("Paver 60");
  const option = page.locator('[data-action="picker-choose"][data-picker-field="pr-uom-probe-product"]').first();
  await expect(option).toContainText("Paver 60 Charcoal");
  await option.click();

  await expect(page.locator('[data-picker="pr-uom-probe-product"]')).toContainText("Paver 60 Charcoal");
  await page.locator('[data-field="pr-uom-probe-qty"]').fill("77");
  await page.locator('[data-field="pr-uom-probe-uom"]').fill("SF");
  await page.locator('[data-action="pr-uom-plan"]').click();
  await expect(page.locator('[data-test="pr-uom-plan"]')).toContainText("77 SF = 6.16 PLT");

  const log = await page.request.get("/_stub/log").then((r) => r.json());
  const planCall = log.find(
    (e: { method: string; path: string; body?: { product_id?: string } }) =>
      e.method === "POST" && e.path === "/api/v1/pricing/uom/plan",
  );
  expect(planCall?.body?.product_id).toBe("fa110000-0000-4000-9000-0000000000a1");

  // Optional product picker in the UoM conversion editor keeps its explicit empty meaning.
  await page.locator('[data-action="pr-uom-edit"]').first().click();
  await expect(page.locator('[data-picker="pr-uom-product"]')).toContainText("Every product");
});

test("category, vendor, branch, profile and batch pickers: choose by name and send the right id", async ({ page }) => {
  await signInAsDealerAdmin(page);
  await page.locator('.surface-link[data-surface="pricing"]').click();

  // Branch picker in the rules editor keeps its empty meaning ("Every yard").
  await page.locator('.pr-tab[data-tab="rules"]').click();
  await page.locator('[data-action="pr-rule-edit"]').first().click();
  const branchSelect = page.locator('select[data-field="pr-rule-branch"]');
  await expect(branchSelect.locator('option[value=""]')).toHaveText("Every yard");
  await branchSelect.selectOption("5d1b3f6e-0000-4000-9000-0000000000pl");
  await expect(branchSelect).toHaveValue("5d1b3f6e-0000-4000-9000-0000000000pl");

  // Vendor picker and category picker on the rebates tab.
  await page.locator('.pr-tab[data-tab="rebates"]').click();
  await page.locator('[data-action="pr-rebate-edit"]').first().click();
  const vendorSelect = page.locator('select[data-field="pr-reb-vendor"]');
  await vendorSelect.selectOption({ label: "Northline Supply" });
  await expect(vendorSelect).toHaveValue("vd110000-0000-4000-9000-0000000000a1");

  // Accrual form: pick product by search and category by select, then accrue.
  await page.locator('[data-field="pr-acc-source-id"]').fill("0f110000-0000-4000-9000-0000000000a2");
  await page.locator('[data-picker="pr-acc-product"] input[type="search"]').fill("Paver 60");
  await page.locator('[data-action="picker-choose"][data-picker-field="pr-acc-product"]').first().click();
  await page.locator('select[data-field="pr-acc-category"]').selectOption({ label: "Pavers" });
  await page.locator('[data-field="pr-acc-qty"]').fill("6");
  await page.locator('[data-field="pr-acc-sale"]').fill("3500.00");
  await page.locator('[data-field="pr-acc-cost"]').fill("2500.00");
  await page.locator('[data-action="pr-accrue"]').click();
  await expect(page.locator('[data-test="pr-accrue-result"]')).toContainText("1 accrual written");

  // Batches tab: vendor and import profile selects.
  await page.locator('.pr-tab[data-tab="batches"]').click();
  await page.locator('[data-action="pr-batch-new"]').click();
  await expect(page.locator('select[data-field="pr-batch-vendor"]')).toBeVisible();
  await expect(page.locator('select[data-field="pr-batch-profile"]')).toBeVisible();

  // Change log tab: batch select with "Any batch".
  await page.locator('.pr-tab[data-tab="changelog"]').click();
  const batchSelect = page.locator('select[data-field="pr-log-batch"]');
  await expect(batchSelect.locator('option[value=""]')).toHaveText("Any batch");
  await batchSelect.selectOption("3a110000-0000-4000-9000-0000000000a1");
  await page.locator('[data-action="pr-log-apply"]').click();
  await expect(page.locator('[data-test="pr-log-count"]')).toHaveText("1 change");
});

test("staff and channel pickers on Audit and Exports: choose by name and send the right id", async ({ page }) => {
  await signInAsDealerAdmin(page);
  await page.locator('.surface-link[data-surface="audit"]').click();
  await expect(page.locator('[data-test="audit-count"]')).toHaveText("3 entries");

  const actorSelect = page.locator('select[data-field="audit-actor-id"]');
  await expect(actorSelect.locator('option[value=""]')).toHaveText("Any staff member");
  await actorSelect.selectOption({ label: "Dealer admin (demo)" });
  await page.locator('[data-action="apply-audit-filters"]').click();
  await expect(page.locator('[data-test="audit-count"]')).toHaveText("1 entries");

  const log = await page.request.get("/_stub/log").then((r) => r.json());
  const auditCall = [...log]
    .reverse()
    .find((e: { method: string; path: string; query?: { actor_id?: string } }) => e.method === "GET" && e.path === "/api/v1/audit");
  expect(auditCall?.query?.actor_id).toBe("da110000-0000-4000-9000-000000000001");

  await page.locator('.surface-link[data-surface="exports"]').click();
  await expect(page.locator('select[data-field="wb-actor"] option[value=""]')).toHaveText("Any staff member");
  await expect(page.locator('select[data-field="msg-user"] option[value=""]')).toHaveText("Any staff member");
  await expect(page.locator('select[data-field="msg-channel"] option[value=""]')).toHaveText("Every channel");
});

test("role curation item picker: pick from the surface's registered items by kind and name", async ({ page }) => {
  await signInAsDealerAdmin(page);
  await page.locator('.surface-link[data-surface="role-config"]').click();
  await openShape(page, "yard");
  await page.locator('[data-test="config-cell-counter-till-yard"]').click();

  await page.locator('[data-action="config-add-kind"]').selectOption("column");
  const itemSelect = page.locator('select[data-field="config-add-id"]');
  await expect(itemSelect).toBeVisible();
  await itemSelect.selectOption({ label: "Unit cost" });
  await page.locator('[data-action="config-add-row"]').click();
  await page.locator('[data-action="config-save"]').click();
  await expect(page.locator('[data-test="config-notice"]')).toContainText("Saved");

  const log = await page.request.get("/_stub/log").then((r) => r.json());
  const putCall = log.find(
    (e: { method: string; path: string }) => e.method === "PUT" && e.path === "/api/v1/surfaces/counter-till/items",
  );
  expect(putCall?.body?.items).toEqual([{ kind: "column", id: "cost", position: 0, visible: true }]);
});

test("390 px phone layout: pickers fit within the card and a picker inside a card-row cell sits in grid-column 2", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signInAsDealerAdmin(page);
  await page.locator('[data-action="toggle-surface-list"]').click();
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await expect(page.locator('[data-test="pr-tab-select"]')).toBeVisible();

  await page.locator('[data-test="pr-tab-select"]').selectOption({ label: "Customer prices" });
  await expect(page.locator('[data-picker="pr-cust-id"]')).toBeVisible();
  await page.locator('[data-picker-search="pr-cust-id"]').fill("Point Loma");
  await page.locator('[data-action="picker-choose"][data-picker-field="pr-cust-id"]').first().click();
  await page.locator('[data-action="pr-cust-load"]').click();
  await expect(page.locator('[data-test="pr-cust-count"]')).toHaveText("1 price");

  await page.locator('[data-test="pr-tab-select"]').selectOption({ label: "Rules" });
  await page.locator('[data-action="pr-rule-edit"]').first().click();
  await expect(page.locator('[data-test="pr-rule-editor"]')).toBeVisible();

  const check = await page.evaluate(() => {
    const root = document.querySelector("dealer-admin-app")?.shadowRoot;
    if (!root) return { ok: false, overflow: ["no shadowRoot"], gridCol: "" };
    const overflow: string[] = [];
    for (const picker of root.querySelectorAll<HTMLElement>(".hh-entity-picker")) {
      const rect = picker.getBoundingClientRect();
      if (rect.left < -0.5 || rect.right > 390.5) {
        overflow.push(`${picker.getAttribute("data-picker")}: left=${rect.left} right=${rect.right}`);
      }
    }
    const td = root.querySelector<HTMLElement>(".cardrow-table tbody td:not(.cardrow-act)");
    let gridCol = "";
    if (td) {
      const probe = document.createElement("div");
      probe.className = "hh-entity-picker";
      td.appendChild(probe);
      gridCol = getComputedStyle(probe).gridColumnStart;
      probe.remove();
    }
    return { ok: overflow.length === 0, overflow, gridCol };
  });

  expect(check.overflow).toEqual([]);
  expect(check.gridCol).toBe("2");
});

async function openPricingTabAt390(page: Page, title: string, settle: string): Promise<void> {
  await page.locator('[data-test="pr-tab-select"]').selectOption({ label: title });
  await expect(page.locator(settle).first()).toBeVisible();
}

async function openSurfaceAt390(page: Page, surface: string, settle: string): Promise<void> {
  const list = page.locator(".surface-list");
  if (!(await list.isVisible())) {
    await page.locator('[data-action="toggle-surface-list"]').click();
    await expect(list).toBeVisible();
  }
  await page.locator(`.surface-link[data-surface="${surface}"]`).click();
  await expect(page.locator(settle).first()).toBeVisible();
}

async function collectClippedPickerHints(page: Page, stateName: string): Promise<string[]> {
  return page.evaluate((state) => {
    const root = document.querySelector("dealer-admin-app")?.shadowRoot;
    if (!root) return [`${state}: missing dealer-admin-app shadowRoot`];
    const canvas = document.createElement("canvas");
    const ctx = canvas.getContext("2d");
    if (!ctx) return [`${state}: missing 2d canvas context`];

    const measureText = (text: string, cs: CSSStyleDeclaration): number => {
      ctx.font = `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
      ctx.letterSpacing = cs.letterSpacing === "normal" ? "0px" : cs.letterSpacing;
      return ctx.measureText(text).width;
    };

    const clipped: string[] = [];

    for (const input of root.querySelectorAll<HTMLInputElement>("input[placeholder]")) {
      const cs = getComputedStyle(input);
      if (cs.display === "none" || cs.visibility === "hidden") continue;
      if (input.getBoundingClientRect().width === 0) continue;
      const placeholder = input.getAttribute("placeholder") ?? "";
      if (placeholder === "") continue;

      const field =
        input.getAttribute("data-picker-search") ??
        input.getAttribute("data-field") ??
        input.getAttribute("aria-label") ??
        "input";
      const padL = Number.parseFloat(cs.paddingLeft) || 0;
      const padR = Number.parseFloat(cs.paddingRight) || 0;
      const boxWidth = input.clientWidth - padL - padR;
      const measured = measureText(placeholder, cs);

      const prevValue = input.value;
      input.value = placeholder;
      const scrollWithPlaceholder = input.scrollWidth;
      input.value = prevValue;

      if (
        input.scrollWidth > input.clientWidth + 0.5 ||
        scrollWithPlaceholder > input.clientWidth + 0.5 ||
        measured > boxWidth + 0.5
      ) {
        clipped.push(
          `${state} | field=${field} | placeholder="${placeholder}" | measured=${Math.round(measured)}px | box=${Math.round(boxWidth)}px | scroll=${scrollWithPlaceholder}px | client=${input.clientWidth}px`,
        );
      }
    }

    for (const current of root.querySelectorAll<HTMLElement>(".hh-entity-picker .picker-current")) {
      const cs = getComputedStyle(current);
      if (cs.display === "none" || cs.visibility === "hidden") continue;
      if (current.getBoundingClientRect().width === 0) continue;
      if (current.scrollWidth > current.clientWidth + 0.5) {
        const picker = current.closest(".hh-entity-picker")?.getAttribute("data-picker") ?? "picker";
        clipped.push(
          `${state} | picker-current=${picker} | text="${(current.textContent ?? "").trim()}" | scroll=${current.scrollWidth}px | client=${current.clientWidth}px`,
        );
      }
    }

    for (const sel of root.querySelectorAll<HTMLSelectElement>("select.select")) {
      const cs = getComputedStyle(sel);
      if (cs.display === "none" || cs.visibility === "hidden") continue;
      if (sel.getBoundingClientRect().width === 0) continue;
      const optText = (sel.selectedOptions[0]?.textContent ?? "").trim();
      if (optText === "") continue;
      const padL = Number.parseFloat(cs.paddingLeft) || 0;
      const padR = Number.parseFloat(cs.paddingRight) || 0;
      const boxWidth = sel.clientWidth - padL - padR;
      const measured = measureText(optText, cs);
      if (sel.scrollWidth > sel.clientWidth + 0.5 || measured > boxWidth + 0.5) {
        const field = sel.getAttribute("data-field") ?? sel.getAttribute("aria-label") ?? "select";
        clipped.push(
          `${state} | select=${field} | option="${optText}" | measured=${Math.round(measured)}px | box=${Math.round(boxWidth)}px`,
        );
      }
    }

    return clipped;
  }, stateName);
}

test("390 px phone layout: every picker input placeholder and hint fits its box across all views", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signInAsDealerAdmin(page);
  const clipped: string[] = [];

  await openSurfaceAt390(page, "pricing", '[data-test="pr-margin-count"]');

  // 1. Rules editor
  await openPricingTabAt390(page, "Rules", '[data-test="pr-rules-count"]');
  await page.locator('[data-action="pr-rule-edit"]').first().click();
  await expect(page.locator('[data-test="pr-rule-editor"]')).toBeVisible();
  clipped.push(...(await collectClippedPickerHints(page, "pricing:rules")));

  // 2. Customer prices
  await openPricingTabAt390(page, "Customer prices", '[data-test="pr-cust-empty"]');
  clipped.push(...(await collectClippedPickerHints(page, "pricing:customers")));

  // 3. Quantity breaks editor
  await openPricingTabAt390(page, "Quantity breaks", '[data-test="pr-breaks-count"]');
  await page.locator('[data-action="pr-break-edit"]').first().click();
  await expect(page.locator('[data-test="pr-break-editor"]')).toBeVisible();
  clipped.push(...(await collectClippedPickerHints(page, "pricing:breaks")));

  // 4. Promotions and clearance editors
  await openPricingTabAt390(page, "Promotions & clearance", '[data-test="pr-clear-count"]');
  await page.locator('[data-action="pr-promo-edit"]').first().click();
  await page.locator('[data-action="pr-clear-edit"]').first().click();
  await expect(page.locator('[data-test="pr-promo-editor"]')).toBeVisible();
  await expect(page.locator('[data-test="pr-clear-editor"]')).toBeVisible();
  clipped.push(...(await collectClippedPickerHints(page, "pricing:promos")));

  // 5. Rebates editor and accrual form
  await openPricingTabAt390(page, "Rebates", '[data-test="pr-ledger-total"]');
  await page.locator('[data-action="pr-rebate-edit"]').first().click();
  await expect(page.locator('[data-test="pr-rebate-editor"]')).toBeVisible();
  clipped.push(...(await collectClippedPickerHints(page, "pricing:rebates")));

  // 6. Units of measure probe and conversion editor
  await openPricingTabAt390(page, "Units of measure", '[data-test="pr-uom-count"]');
  await page.locator('[data-action="pr-uom-edit"]').first().click();
  await expect(page.locator('[data-test="pr-uom-editor"]')).toBeVisible();
  clipped.push(...(await collectClippedPickerHints(page, "pricing:uom")));

  // 7. Batches create form
  await openPricingTabAt390(page, "Batches & imports", '[data-test="pr-profiles-count"]');
  await page.locator('[data-action="pr-batch-new"]').click();
  await expect(page.locator('[data-test="pr-batch-create"]')).toBeVisible();
  clipped.push(...(await collectClippedPickerHints(page, "pricing:batches")));

  // 8. Low margin probe
  await openPricingTabAt390(page, "Low margin", '[data-test="pr-lm-count"]');
  clipped.push(...(await collectClippedPickerHints(page, "pricing:lowmargin")));

  // 9. Change log filters
  await openPricingTabAt390(page, "Change log", '[data-test="pr-log-count"]');
  clipped.push(...(await collectClippedPickerHints(page, "pricing:changelog")));

  // 10. Audit filters
  await openSurfaceAt390(page, "audit", '[data-test="audit-count"]');
  clipped.push(...(await collectClippedPickerHints(page, "audit")));

  // 11. Exports filters
  await openSurfaceAt390(page, "exports", '[data-action="wb-download"]');
  clipped.push(...(await collectClippedPickerHints(page, "exports")));

  // 12. Role configuration editor
  await openSurfaceAt390(page, "role-config", '[data-test="config-roles-count"]');
  await openShape(page, "yard");
  await page.locator('[data-test="config-cell-counter-till-yard"]').click();
  await expect(page.locator('[data-test="config-editor"]')).toBeVisible();
  clipped.push(...(await collectClippedPickerHints(page, "role-config")));

  expect(clipped, `picker placeholders or hints clipped at 390 px:\n${clipped.join("\n")}`).toEqual([]);
});

test("vendor picker lists only loaded vendors, says 'No vendors yet' when empty, and src carries no seed uuids or planted names", async ({
  page,
}) => {
  const srcDir = join(dirname(fileURLToPath(import.meta.url)), "..", "src");
  const seedViolations: string[] = [];
  const seedUuidRx = /[0-9a-f]{4,8}-[0-9a-f]{4}-4000-9000-[0-9a-f]{12}/i;
  const plantedRx = /\bnorthline\b/i;

  for (const entry of readdirSync(srcDir)) {
    if (!entry.endsWith(".ts")) continue;
    const lines = readFileSync(join(srcDir, entry), "utf8").split("\n");
    lines.forEach((line, idx) => {
      if (seedUuidRx.test(line) || plantedRx.test(line)) {
        seedViolations.push(`${entry}:${idx + 1}: ${line.trim()}`);
      }
    });
  }

  await page.route("**/api/v1/pricing/rebates/programs*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [], has_more: false, next_cursor: "" }),
    });
  });
  await page.route("**/api/v1/pricing/batches*", async (route) => {
    if (
      route.request().method() === "GET" &&
      new URL(route.request().url()).pathname === "/api/v1/pricing/batches"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ items: [], has_more: false, next_cursor: "" }),
      });
      return;
    }
    await route.continue();
  });
  await page.route("**/api/v1/pricing/import-profiles*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [], has_more: false, next_cursor: "" }),
    });
  });
  await page.route("**/api/v1/pricing/price-file/vendors*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ items: [], has_more: false, next_cursor: "" }),
    });
  });

  await signInAsDealerAdmin(page);
  await page.locator('.surface-link[data-surface="pricing"]').click();
  await expect(page.locator('[data-test="pr-margin-count"]')).toBeVisible();

  await page.locator('.pr-tab[data-tab="rebates"]').click();
  await expect(page.locator('[data-test="pr-rebates-empty"]')).toBeVisible();
  await page.locator('[data-action="pr-rebate-edit"]').first().click();
  await expect(page.locator('[data-test="pr-rebate-editor"]')).toBeVisible();
  const rebVendorOptions = await page.locator('select[data-field="pr-reb-vendor"] option').allTextContents();

  await page.locator('.pr-tab[data-tab="batches"]').click();
  await expect(page.locator('[data-test="pr-batches-empty"]')).toBeVisible();
  await page.locator('[data-action="pr-batch-new"]').click();
  await expect(page.locator('[data-test="pr-batch-create"]')).toBeVisible();
  const batchVendorOptions = await page.locator('select[data-field="pr-batch-vendor"] option').allTextContents();
  await page.locator('[data-field="pr-batch-no"]').fill("B-EMPTY-VENDOR-1");
  await page.locator('[data-field="pr-batch-source"]').fill("sheet.csv");
  await page.locator('[data-action="pr-batch-create"]').click();
  await expect(page.locator('[data-test="pr-batches-notice"]')).toContainText("open in draft");

  await page.locator('.pr-tab[data-tab="pricefile"]').click();
  await expect(page.locator('[data-test="pr-pf-empty"]')).toBeVisible();
  const pfVendorOptions = await page.locator('select[data-field="pr-pf-vendor"] option').allTextContents();

  expect({
    seedViolations,
    rebVendorOptions,
    batchVendorOptions,
    pfVendorOptions,
  }).toEqual({
    seedViolations: [],
    rebVendorOptions: ["No vendors yet"],
    batchVendorOptions: ["No vendors yet"],
    pfVendorOptions: ["No vendors yet"],
  });
});




// UX-119c: a job shape with nothing curated is one line until asked; open it before clicking one of its tiles.
async function openShape(page: import("@playwright/test").Page, profile: string): Promise<void> {
  const toggle = page.locator(`[data-test="config-shape-toggle-${profile}"]`);
  if ((await toggle.getAttribute("aria-expanded")) !== "true") await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
}
