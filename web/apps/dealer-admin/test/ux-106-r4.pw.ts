// UX-106 round 4: the audit trail's identifiers are labelled.
//
// The defects. The drawer's Branch row printed a branch id where the branch's
// name belongs, the Entity cell printed "type / uuid" as one string, and the
// trail card's header printed "type / id" with no caption at all. A handle is
// evidence and it stays, but a handle with no caption reads as a code token
// that fell out of a formatter.
//
// The legs:
//
//   1. the drawer names the branch, and every handle it shows sits under a
//      caption that says it is an id;
//   2. the list's entity cell reads as a type in words and a captioned id;
//   3. the trail card's header captions its id;
//   4. the neutral pill wears the design system's own line token (K3 P3).
//
// What an id looks like here. The stub's handles are dashed tokens of eight,
// four, four, four and twelve characters, and they are not all hex ("s", "o",
// "pl"), so the shape is the test and the alphabet is not.
import { expect, test, type Locator, type Page } from "@playwright/test";

/** A dashed handle: eight, four, four, four, twelve. */
const HANDLE = /^\w{8}-\w{4}-\w{4}-\w{4}-\w{12}$/;

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.locator("[data-flow]").first().click();
  await expect(page.locator("[data-test=staff-count]").first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

/** The charcoal half is the design system's brand theme, one press away. */
async function goCharcoal(page: Page): Promise<void> {
  await page.locator("hh-shell-header .account").click();
  await page.locator('[data-testid="theme-control"]').click();
  await page.locator("hh-shell-header .account").click();
}

/** The audit surface opens through the app's own list; on a phone the list
 *  waits folded behind the Settings control (UX-104). */
async function openAudit(page: Page): Promise<void> {
  if (page.viewportSize()!.width <= 900) {
    await page.locator('[data-action="toggle-surface-list"]').click();
    await expect(page.locator(".surface-list")).toBeVisible();
  }
  await page.locator('.surface-link[data-surface="audit"]').click();
  await expect(page.locator("[data-test=audit-count]").first()).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
}

/** One entry's drawer, once its own read has landed. */
async function openFirstEntry(page: Page): Promise<Locator> {
  await page.locator("tr[data-action='open-audit-entry']").first().click();
  const drawer = page.locator("[data-detail-state='ready']").first();
  await expect(drawer).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");
  return drawer;
}

/** Every key and value row a region shows, caption and value together. */
async function keyValueRows(region: Locator): Promise<{ caption: string; value: string; classes: string }[]> {
  return region.locator(".k").evaluateAll((keys) =>
    keys.map((key) => {
      const value = key.nextElementSibling;
      return {
        caption: (key.textContent ?? "").trim(),
        value: (value?.textContent ?? "").trim(),
        classes: value?.className ?? "",
      };
    }),
  );
}

/** The words a region's leaves carry, one by one. A region's whole text glues
 *  two neighbours together, and the glued string hides what the leaves say. */
async function leafWords(region: Locator): Promise<string[]> {
  return region.evaluate((el) => {
    const leaves = [el, ...Array.from(el.querySelectorAll("*"))].filter((node) => node.children.length === 0);
    return leaves.map((node) => (node.textContent ?? "").trim()).filter((text) => text !== "");
  });
}

// -- 1: the drawer -------------------------------------------------------------------------------------

for (const theme of ["light", "charcoal"] as const) {
  test(`the drawer names the branch and captions every handle, in ${theme}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await signIn(page);
    if (theme === "charcoal") await goCharcoal(page);
    await openAudit(page);
    const drawer = await openFirstEntry(page);

    const rows = await keyValueRows(drawer);
    const row = (caption: string) => rows.find((r) => r.caption === caption);

    // The branch row names the yard. A handle there is the defect itself.
    const branch = row("Branch");
    expect(branch?.value ?? "", "the drawer prints no branch name").not.toMatch(HANDLE);
    expect(branch?.value ?? "", "the drawer prints a branch handle where the name belongs").toBe("Point Loma");
    expect(branch?.classes ?? "", "the branch name wears the monospace a handle wears").not.toMatch(/(^|\s)mono(\s|$)/);

    // The entity row reads the type as words.
    const entity = row("Entity");
    expect(entity?.value ?? "", "the drawer does not read the entity type as words").toBe("pos sale");
    expect(entity?.classes ?? "", "the entity type wears the monospace a handle wears").not.toMatch(/(^|\s)mono(\s|$)/);

    // Every handle the drawer shows sits under a caption that says it is an id.
    const handles = rows.filter((r) => HANDLE.test(r.value));
    expect(handles.length, "the drawer shows no handle at all").toBeGreaterThan(0);
    for (const handle of handles) {
      expect(handle.caption, `the handle ${handle.value} sits under no caption`).toMatch(/ID$/);
    }

    // The branch's own handle keeps its own caption.
    expect(row("Branch ID")?.value ?? "", "the drawer's branch handle is not captioned").toMatch(HANDLE);
  });
}

// -- 2: the list's entity cell -------------------------------------------------------------------------

test("the list's entity cell reads as a type in words and a captioned id", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await openAudit(page);

  const cell = page.locator("tr[data-action='open-audit-entry']").first().locator("td").nth(2);
  const leaves = await leafWords(cell);

  expect(leaves, "the entity cell does not read its type as words").toContain("pos sale");
  expect(leaves, "the entity cell captions no id").toContain("ID");
  expect(leaves.join(" "), "the entity cell glues the type and the id into one string").not.toMatch(/\S+\s*\/\s*\S/);
});

// -- 3: the trail card's header -------------------------------------------------------------------------

test("the trail card's header captions its id", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await openAudit(page);
  await openFirstEntry(page);

  await page.locator("[data-action='open-audit-trail']").first().click();
  const trail = page.locator("[data-test=audit-trail]").first();
  await expect(trail).toBeVisible({ timeout: 30_000 });
  await page.waitForLoadState("networkidle");

  const leaves = await leafWords(trail.locator(".card-head").first());
  expect(leaves, "the trail header does not read its type as words").toContain("pos sale");
  expect(leaves, "the trail header captions no id").toContain("ID");
});

// -- 4: the neutral pill's token ------------------------------------------------------------------------

for (const theme of ["light", "charcoal"] as const) {
  test(`the neutral pill wears the design system's own line token, in ${theme}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.request.post("/_stub/reset");
    const answer = await page.request.post("/_stub/scenario", { data: { name: "unknown-branch" } });
    expect(answer.ok(), "the stub's scenario control did not answer").toBe(true);
    await signIn(page);
    if (theme === "charcoal") await goCharcoal(page);

    // The yard persona's grants reach a branch the catalog does not know, and
    // that unknown branch wears the neutral pill.
    const pill = page.locator(".staff-branches .chip-neutral").first();
    await expect(pill).toHaveCount(1);
    const border = await pill.evaluate((el) => getComputedStyle(el).borderTopColor);

    // The token the design system's own chip-neutral wears.
    const token = await page.evaluate(() => {
      const probe = document.createElement("span");
      probe.style.color = "var(--hh-line)";
      document.body.appendChild(probe);
      const value = getComputedStyle(probe).color;
      probe.remove();
      return value;
    });

    expect(border, "the neutral pill does not wear the design system's line token").toBe(token);

    await page.request.post("/_stub/reset");
  });
}
