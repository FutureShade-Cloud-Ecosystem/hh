// The admin-rest surfaces' Playwright legs, on the same harness as the app's
// own file: the stub serves the demo routes and each surface's world; the
// scenarios move the world (the stub's own controls) between legs. One file
// per commit's surface, so the registry's next coder lands beside, not into,
// these legs.
import { test, expect } from "@playwright/test";

test.describe("the assist admin surface", () => {
  test("the month's spend lists every active person, zeros included", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await expect(page.locator("[data-test=staff-count]")).toBeVisible();
    await page.locator('.surface-link[data-surface="assist"]').click();

    // The report's month and its rows: five staff, four of them at zero, the
    // zero rows rendered as facts and not collapsed away.
    await expect(page.locator("[data-test=assist-month]")).toHaveText("2026-10");
    const rows = page.locator("tr[data-user]");
    await expect(rows).toHaveCount(5);
    const zeroRow = page.locator('tr[data-user="da110000-0000-4000-9000-0000000000yt"]');
    await expect(zeroRow).toContainText("No questions yet");
    // The spend row carries the questions first, tokens second. The dealer
    // admin's own budget resolves from their role's seeded default.
    const adminRow = page.locator('tr[data-user="da110000-0000-4000-9000-000000000001"]');
    await expect(adminRow).toContainText("4 questions");
    await expect(adminRow).toContainText("500 tokens");
    await expect(adminRow).toContainText("500,000, from GENERAL_MANAGER");
    await expect(page.locator('tr[data-user="da110000-0000-4000-9000-000000000002"]')).toContainText("0, their own");
  });

  test("a role budget changes, and the report re-reads with the new number", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await page.locator('.surface-link[data-surface="assist"]').click();
    await expect(page.locator("[data-test=assist-month]")).toBeVisible();

    // The general manager role's row: change 500,000 to 120,000.
    const roleRow = page.locator("tr[data-role=GENERAL_MANAGER]");
    await expect(roleRow).toContainText("500,000");
    await roleRow.getByRole("button", { name: "Change" }).click();
    await page.locator('[data-field="assist-budget"]').fill("120000");
    await page.locator('[data-action="assist-save"][data-budget-kind="role"]').click();

    await expect(page.locator("[data-test=assist-notice]")).toContainText("Saved");
    await expect(page.locator("tr[data-role=GENERAL_MANAGER]")).toContainText("120,000");
  });

  test("a person's override is set and reset to their role, whole numbers only", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await page.locator('.surface-link[data-surface="assist"]').click();
    await expect(page.locator("[data-test=assist-month]")).toBeVisible();

    // A budget that is not a whole number of tokens is refused before any call.
    const yardRow = page.locator('tr[data-user="da110000-0000-4000-9000-0000000000yt"]');
    await yardRow.getByRole("button", { name: "Change" }).click();
    await page.locator('[data-field="assist-budget"]').fill("12.5");
    await page.locator('[data-action="assist-save"][data-budget-kind="user"]').click();
    await expect(page.locator("[data-test=assist-edit-error]")).toContainText("whole number of tokens");
    // The refusal keeps the person's typed number on the glass.
    await expect(page.locator('[data-field="assist-budget"]')).toHaveValue("12.5");

    // A whole number lands as the person's own, and Reset to role appears.
    await page.locator('[data-field="assist-budget"]').fill("250000");
    await page.locator('[data-action="assist-save"][data-budget-kind="user"]').click();
    await expect(page.locator("[data-test=assist-notice]")).toContainText("Saved");
    await expect(yardRow).toContainText("250,000, their own");

    // Reset to role: the override is gone, the code default applies again.
    await yardRow.getByRole("button", { name: "Reset to role" }).click();
    await expect(yardRow).toContainText("60,000, the default");
    await expect(yardRow.getByRole("button", { name: "Reset to role" })).toHaveCount(0);
  });

  test("the settings carry the four keys and the key's name, and a toggle saves", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await page.locator('.surface-link[data-surface="assist"]').click();
    await expect(page.locator("[data-test=assist-month]")).toBeVisible();

    // The settings block: the provider, the model, the key variable's NAME,
    // and a no for the key itself, which is never sent.
    await expect(page.locator("[data-test=assist-settings-provider]")).toHaveText("stub");
    await expect(page.locator("[data-test=assist-settings-keyvar]")).toHaveText("HH_ASSIST_API_KEY");
    await expect(page.locator("[data-test=assist-settings-keypresent]")).toHaveText("no");

    // The spend visibility toggle moves, the notice says so, and the
    // checkbox lands on the state the server now answers.
    const toggle = page.locator('#assist-toggle-spend_visible_to_non_admin');
    await expect(toggle).not.toBeChecked();
    await toggle.click();
    await expect(page.locator("[data-test=assist-notice]")).toContainText("Saved");
    await expect(toggle).toBeChecked();
  });

  test("a frontline caller is refused the assist admin read", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").nth(1).click();
    await page.locator('.surface-link[data-surface="assist"]').click();

    // The assistant's month is leadership's read; the refusal names who may
    // act, and no row is asserted.
    await expect(page.locator("[data-test=assist-error]")).toContainText("leadership");
    await expect(page.locator("[data-test=assist-error]")).toContainText("assistant");
  });
});

test.describe("the print jobs surface", () => {
  const findDocument = async (page: import("@playwright/test").Page, ref: string): Promise<void> => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await expect(page.locator("[data-test=staff-count]")).toBeVisible();
    await page.locator('.surface-link[data-surface="printing"]').click();
    await page.locator('[data-field="print-doc-type"]').selectOption({ label: "Receipt" });
    await page.locator('[data-field="print-ref"]').fill(ref);
    await page.locator('[data-action="print-find"]').click();
  };

  test("one sale's receipt history: a print and a reprint", async ({ page }) => {
    await findDocument(page, "0f000000-0000-4000-9000-000000000001");
    await expect(page.locator("[data-test=print-count]")).toHaveText("2 jobs");
    // The queued reprint and the printed first copy, each in its own words.
    await expect(page.locator("[data-test^=print-row-]").filter({ hasText: "Queued" })).toHaveCount(1);
    await expect(page.locator("[data-test^=print-row-]").filter({ hasText: "Printed" })).toHaveCount(1);
    await expect(page.locator("[data-test^=print-row-]").filter({ hasText: "Printed" })).toContainText("Browser");
  });

  test("a document that has printed nothing says so about itself", async ({ page }) => {
    await findDocument(page, "0f000000-0000-4000-9000-0000000000e9");
    await expect(page.locator("[data-test=print-empty]")).toContainText("No print jobs for this document");
    await expect(page.locator("[data-test=print-count]")).toHaveCount(0);
  });

  test("a search in flight says it is looking, never that nothing printed", async ({ page }) => {
    // Hold the core's answer so the in-flight frame stays on the glass.
    await page.route("**/api/v1/printing/jobs*", async (route) => {
      await new Promise((r) => setTimeout(r, 800));
      await route.continue();
    });
    await findDocument(page, "0f000000-0000-4000-9000-0000000000e9");
    await expect(page.locator("[data-test=print-loading]")).toBeVisible();
    await expect(page.locator("[data-test=print-empty]")).toHaveCount(0);
    await expect(page.locator("[data-test=print-empty]")).toContainText("No print jobs for this document");
  });

  test("a reference that is no document id is refused in the server's own words", async ({ page }) => {
    await findDocument(page, "not-a-document");
    // The core answers the bare not found; the surface does not dress it up.
    await expect(page.locator("[data-test=print-error]")).toHaveText("not found");
    // The person's words stay on the glass, so the question is still there.
    await expect(page.locator('[data-field="print-ref"]')).toHaveValue("not-a-document");
  });

  test("an empty reference asks for the question, it does not search", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await page.locator('.surface-link[data-surface="printing"]').click();
    await expect(page.locator("[data-test=print-idle]")).toBeVisible();
    await page.locator('[data-action="print-find"]').click();
    await expect(page.locator("[data-test=print-error]")).toContainText("Give the document's reference");
    // No search ran, so the empty state keeps its own words.
    await expect(page.locator("[data-test=print-idle]")).toContainText("Choose the paper");
  });
});

test.describe("the exports surface", () => {
  const openExports = async (page: import("@playwright/test").Page): Promise<void> => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await expect(page.locator("[data-test=staff-count]")).toBeVisible();
    await page.locator('.surface-link[data-surface="exports"]').click();
    await expect(page.locator('[data-action="wb-download"]')).toBeVisible();
  };

  test("the audit workbook downloads, one call, and the notice names the file", async ({ page }) => {
    await openExports(page);
    // The workbook's filters ride the trail's own four: an entity type in,
    // the file out.
    await page.locator('[data-field="wb-entity-type"]').fill("pos.sale");
    const downloadPromise = page.waitForEvent("download");
    await page.locator('[data-action="wb-download"]').click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe("audit.xlsx");
    await expect(page.locator("[data-test=wb-notice]")).toContainText("audit.xlsx");
    await expect(page.locator("[data-test=wb-notice]")).toContainText("1024 bytes");
  });

  test("the messages export downloads, and the masked rows render under it", async ({ page }) => {
    await openExports(page);
    await expect(page.locator("[data-test=msg-empty]")).toContainText("Take the whole house");
    const downloadPromise = page.waitForEvent("download");
    await page.locator('[data-action="msg-export"]').click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe("house-messages-export.json");
    await expect(page.locator("[data-test=msg-count]")).toHaveText("3 messages");
    // A text row with an author, and the deleted row tombstoned.
    await expect(page.locator("[data-test=msg-row-0]")).toContainText("Dealer admin (demo)");
    await expect(page.locator("[data-test=msg-row-2]")).toContainText("deleted");
  });

  test("a frontline caller is refused the messages export, and the workbook still downloads", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").nth(1).click();
    await page.locator('.surface-link[data-surface="exports"]').click();
    await expect(page.locator('[data-action="wb-download"]')).toBeVisible();

    // The messages export is leadership's; the workbook read is staff wide.
    await page.locator('[data-action="msg-export"]').click();
    await expect(page.locator("[data-test=msg-error]")).toContainText("leadership");
    const downloadPromise = page.waitForEvent("download");
    await page.locator('[data-action="wb-download"]').click();
    expect((await downloadPromise).suggestedFilename()).toBe("audit.xlsx");
  });
});

test.describe("the catalog surface", () => {
  const openCatalog = async (page: import("@playwright/test").Page): Promise<void> => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await expect(page.locator("[data-test=staff-count]")).toBeVisible();
    await page.locator('.surface-link[data-surface="catalog"]').click();
    // The surface's own staff reads land with the open: the unit catalogue
    // and the categories.
    await expect(page.locator("[data-test=units-count]")).toHaveText("4 units");
    await expect(page.locator("[data-test=categories-count]")).toHaveText("3 categories");
    await expect(page.locator('[data-field="catalog-q"]')).toBeVisible();
  };

  test("find a product and open it: the editor carries its words", async ({ page }) => {
    await openCatalog(page);
    await page.locator('[data-field="catalog-q"]').fill("paver");
    await page.locator('[data-action="catalog-find"]').click();
    await expect(page.locator("[data-test=catalog-count]")).toHaveText("1 found");
    await expect(page.locator("[data-test=catalog-row-0]")).toContainText("PAV-60-CH");
    // The list price reads through the shared formatter, per its unit.
    await expect(page.locator("[data-test=catalog-row-0]")).toContainText("$4.88 per SF");

    await page.locator('[data-action="catalog-open"]').click();
    await expect(page.locator('[data-field="pf-name"]')).toHaveValue("Charcoal 60 paver");
    await expect(page.locator('[data-field="pf-description"]')).toHaveValue("A 60 mm paver in charcoal.");
  });

  test("a save sends only the changed field, and the list re-reads", async ({ page }) => {
    await openCatalog(page);
    await page.locator('[data-field="catalog-q"]').fill("paver");
    await page.locator('[data-action="catalog-find"]').click();
    await page.locator('[data-action="catalog-open"]').click();
    await expect(page.locator('[data-field="pf-name"]')).toHaveValue("Charcoal 60 paver");

    // Nothing changed: the save says so and no call leaves.
    await page.locator('[data-action="catalog-save"]').click();
    await expect(page.locator("[data-test=catalog-notice]").first()).toContainText("Nothing changed yet");

    await page.locator('[data-field="pf-description"]').fill("A 60 mm paver in charcoal, for patios and walkways.");
    await page.locator('[data-action="catalog-save"]').click();
    await expect(page.locator("[data-test=catalog-notice]").first()).toContainText("Saved");

    // The wire carried the one field, the contract's partial map, and the
    // row's decimal minimum order never left its string.
    const log = await page.request.get("/_stub/log").then((r) => r.json());
    const put = log.find((e: { method: string; path: string }) => e.method === "PUT" && e.path === "/api/v1/products/3a000000-0000-4000-9000-000000000001");
    expect(put).toBeTruthy();
    expect(put.body).toEqual({ description: "A 60 mm paver in charcoal, for patios and walkways." });
  });

  test("a picture is chosen, set and removed, and the notice names the bytes", async ({ page }) => {
    await openCatalog(page);
    await page.locator('[data-field="catalog-q"]').fill("wall");
    await page.locator('[data-action="catalog-find"]').click();
    await page.locator('[data-action="catalog-open"]').click();
    await expect(page.locator('[data-field="pf-name"]')).toHaveValue("Seminole wall block");

    // A one pixel PNG, the smallest picture the contract declares.
    const png = Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
      "base64",
    );
    await page.locator('input[data-action="catalog-image-file"]').setInputFiles({
      name: "wall-block.png",
      mimeType: "image/png",
      buffer: png,
    });
    await expect(page.locator("[data-test=catalog-picked]")).toContainText("wall-block.png");
    await page.locator('[data-field="catalog-alt"]').fill("The Seminole wall block, split face");
    await page.locator('[data-action="catalog-image-set"]').click();
    await expect(page.locator("[data-test=catalog-notice]").first()).toContainText(`${png.byteLength} bytes`);

    await page.locator('[data-action="catalog-image-remove"]').click();
    await expect(page.locator("[data-test=catalog-notice]").first()).toContainText("removed");
  });

  test("a frontline caller may search, and is refused the save in the shared words", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").nth(1).click();
    await page.locator('.surface-link[data-surface="catalog"]').click();
    await expect(page.locator("[data-test=units-count]")).toHaveText("4 units");

    // The reads are staff wide: the search answers, the editor opens.
    await page.locator('[data-field="catalog-q"]').fill("paver");
    await page.locator('[data-action="catalog-find"]').click();
    await expect(page.locator("[data-test=catalog-count]")).toHaveText("1 found");
    await page.locator('[data-action="catalog-open"]').click();
    await expect(page.locator('[data-field="pf-name"]')).toHaveValue("Charcoal 60 paver");

    // The write is the manager gate's; the refusal names who may write, and
    // never a raw status word.
    await page.locator('[data-field="pf-description"]').fill("new words");
    await page.locator('[data-action="catalog-save"]').click();
    await expect(page.locator("[data-test=catalog-error]").first()).toContainText("managers and above");
  });
});

test.describe("the platform keys surface", () => {
  test("the roster renders the keys, the floors and the dealer's overrides, read only", async ({ page }) => {
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await expect(page.locator("[data-test=staff-count]")).toBeVisible();
    await page.locator('.surface-link[data-surface="keys"]').click();

    // The roster: two keys, each with the platform's floor and this dealer's
    // override as the server sent them, and no write control anywhere.
    await expect(page.locator("[data-test=keys-count]")).toHaveText("2 keys");
    await expect(page.locator("[data-test=keys-row-0]")).toContainText("launcher.tiles");
    await expect(page.locator("[data-test=keys-row-0]")).toContainText('{"launcher":true');
    await expect(page.locator("[data-test=keys-row-1]")).toContainText("not set");
    await expect(page.locator("[data-test=keys-row-1]")).toContainText("configured");
    await expect(page.locator('[data-action^="keys-"]')).toHaveCount(0);
    await expect(page.locator('[data-action="reload-keys"]')).toHaveCount(0);
  });

  test("a system that does not serve the roster lands on the waits state, not a raw 404", async ({ page }) => {
    await page.request.post("/_stub/scenario", { data: { name: "unregistered-keys" } });
    await page.goto("/");
    await page.locator("[data-flow]").first().click();
    await page.locator('.surface-link[data-surface="keys"]').click();

    await expect(page.locator("[data-test=keys-waits]")).toContainText("not served here yet");
    await expect(page.locator("[data-test=keys-waits]")).toContainText("operator tools");
    await expect(page.locator("body")).not.toContainText("404");
    await page.request.post("/_stub/reset");
  });
});
