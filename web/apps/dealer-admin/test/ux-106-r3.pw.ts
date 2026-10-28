// UX-106 round 3: no id fragment on a dealer screen.
//
// The defect: branchChips rendered a branch the branches route does not know
// as the first block of its id ("9a11e111"), in a monospace chip. That is a
// raw id on a dealer screen: the same class of thing round 1 removed from the
// margin policy table. Round 2 left it alone and flagged it.
//
// The legs:
//
//   1. the Staff register's unknown branch reads as a neutral pill, never as
//      an id, in the light theme and in the charcoal theme alike;
//   2. the general one: no dealer screen prints a bare id fragment. The
//      register, the pricing log, the change log, the audit trail, the print
//      queue and the hotkey rosters each say it.
//
// What the detector sees. An id fragment is eight characters that carry both
// a letter and a digit, standing alone. A whole uuid is not a fragment: the
// audit trail's entity id IS the thing the trail is about, and it stays, so
// the parts of a uuid are not counted.
import { expect, test, type Page } from "@playwright/test";

/** Eight characters standing alone, with a letter and a digit among them. */
const ID_FRAGMENT = /(?<![0-9a-z-])([0-9a-z]{8})(?![0-9a-z-])/gi;

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

/** The phone folds the surface list behind the Settings control (UX-104). */
async function openSurface(page: Page, surface: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator('[data-action="toggle-surface-list"]').click();
    await expect(page.locator(".surface-list")).toBeVisible();
  }
  await page.locator(`.surface-link[data-surface="${surface}"]`).click();
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

/** The pricing view a walk stands on: the strip's pill at the desk widths, the phone's select below them. */
async function openPricingView(page: Page, title: string, settle: string): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator("[data-test=pr-tab-select]").selectOption({ label: title });
  } else {
    await page.locator(`.pr-tab`).filter({ hasText: title }).first().click();
  }
  await expect(page.locator(settle).first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

/**
 * The bare id fragments in a region's words, if any carry one.
 *
 * The leaves are read one by one: a chip's own text is its own, while a
 * region's whole text glues two neighbouring pills together ("Julian" and
 * "9a11e111" would read as one word and hide the fragment).
 */
async function idFragmentsIn(page: Page, selector: string): Promise<string[]> {
  return page.locator(selector).first().evaluate((el, source) => {
    const pattern = new RegExp(source, "gi");
    const found: string[] = [];
    const leaves = [el, ...Array.from(el.querySelectorAll("*"))].filter((node) => node.children.length === 0);
    for (const leaf of leaves) {
      const text = (leaf.textContent ?? "").trim();
      for (const hit of text.matchAll(pattern)) {
        const word = hit[1] ?? "";
        if (!/[0-9]/.test(word) || !/[a-z]/i.test(word)) continue;
        found.push(word);
      }
    }
    return found;
  }, ID_FRAGMENT.source);
}

/** Asserts that a region carries no bare id fragment, and says which one it saw. */
async function expectNoIdFragment(page: Page, where: string, selector: string): Promise<void> {
  const found = await idFragmentsIn(page, selector);
  expect(found, `${where} prints an id fragment`).toEqual([]);
}

// -- 1: the register's unknown branch -------------------------------------------------------------------

/**
 * Puts the stub in the unknown branch scenario, and says so when it does
 * not. The world is reset first: a leg elsewhere in the suite leaves the
 * yard persona on another role, and this leg must read the world the stub
 * starts with.
 */
async function useUnknownBranchScenario(page: Page): Promise<void> {
  await page.request.post("/_stub/reset");
  const answer = await page.request.post("/_stub/scenario", { data: { name: "unknown-branch" } });
  expect(answer.ok(), "the stub's scenario control did not answer").toBe(true);
  expect((await answer.text()).includes("unknown-branch"), "the stub did not take the scenario").toBe(true);
}

for (const theme of ["light", "charcoal"] as const) {
  test(`the register's unknown branch reads as a neutral pill, never as an id, in ${theme}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await useUnknownBranchScenario(page);
    await signIn(page);
    if (theme === "charcoal") await goCharcoal(page);

    // The yard persona's grants reach a branch the catalog does not know.
    const cell = page.locator(".staff-branches").filter({ hasText: "Julian" });
    await expect(cell).toHaveCount(1);
    const chips = cell.locator(".chip");
    await expect(chips).toHaveCount(3);

    // The third chip is the unknown one: a word, a pill, and not the monospace
    // chip an id used to wear.
    const unknown = chips.nth(2);
    await expect(unknown).toHaveText("Unknown branch");
    const cls = (await unknown.getAttribute("class")) ?? "";
    expect(cls, "the unknown branch is not a pill").toMatch(/(^|\s)chip(\s|$)/);
    expect(cls, "the unknown branch wears the monospace chip an id wore").not.toMatch(/(^|\s)mono(\s|$)/);
    expect(cls, "the unknown branch is not a neutral pill").toMatch(/(^|\s)chip-neutral(\s|$)/);

    // And the register prints no id fragment at all.
    await expectNoIdFragment(page, "the staff register", ".staff-table");

    await page.request.post("/_stub/reset");
  });
}

// -- 2: the general one ----------------------------------------------------------------------------------

test("no dealer screen prints a bare id fragment", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await useUnknownBranchScenario(page);
  await signIn(page);

  // The register.
  await expectNoIdFragment(page, "the staff register", ".staff-table");

  // The pricing log: the entity and the actor.
  await openSurface(page, "pricing", "[data-test=pr-margin-count]");
  await openPricingView(page, "Pricing audit", "[data-test=pr-audit-count]");
  await expectNoIdFragment(page, "the pricing log", "tr[data-audit]");

  // The change log: the product, the batch and the by.
  await openPricingView(page, "Change log", "[data-test=pr-log-count]");
  await expectNoIdFragment(page, "the change log", "tr[data-log]");

  // The audit trail: the actor.
  await openSurface(page, "audit", "[data-test=audit-count]");
  await expectNoIdFragment(page, "the audit trail", "tr[data-action='open-audit-entry']");

  // The print queue: who asked for the print.
  await openSurface(page, "printing", "[data-field='print-doc-type']");
  await page.locator('[data-field="print-doc-type"]').selectOption({ label: "Receipt" });
  await page.locator('[data-field="print-ref"]').fill("0f000000-0000-4000-9000-000000000001");
  await page.locator('[data-action="print-find"]').click();
  await expect(page.locator("[data-test=print-count]")).toBeVisible({ timeout: 30_000 });
  await expectNoIdFragment(page, "the print queue", "tr[data-test^='print-row-']");

  // The hotkey rosters: the action labels.
  await openSurface(page, "hotkeys", '[data-test="hotkeys-roster-app.shell"]');
  await expectNoIdFragment(page, "the hotkey rosters", ".klabel");

  await page.request.post("/_stub/reset");
});
