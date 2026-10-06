// The split-list round (the CP1 feedback, decision log row 941): the quotes
// list and the quote flow live on separate screens. The desk's landing page
// is the list alone; the quote (and a new quote) is the work area's own page;
// three hash routes carry the person between them. These legs hold the split
// itself, each one red on the head that stacked the work area, the list and
// the new quote form on one screen:
//
//   A. the list page carries no work area (no stepper, no composer): the
//      work area's markup is not what the person sees on the landing page;
//   B. the quote page carries no list, and the work area's title row opens
//      with the All quotes link that walks back to the list;
//   C. the three routes: #/quotes lands on the list, #/quotes/new on the
//      compose card, #/quotes/<id> on that quote with the list never shown,
//      and the browser's back and forward move between them; a boot through
//      the front door's handoff lands on the list;
//   D. the list's rows render as cards at the tablet viewport (768x1024),
//      the design tokens' card face, not the desktop row;
//   E. the list's toolbar carries the design system's segmented control for
//      the status tabs and the search box, the primary New quote button at
//      the right of the title row, and the Sales manager's tabs also carry
//      Approvals (Sales sees neither Approvals nor Pricing).
import { expect, test, type Page } from "@playwright/test";
import { showList } from "./switch-helpers.js";

const SALES = { name: "Sales", exact: true };
const MANAGER = { name: "Sales manager", exact: true };

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signIn(page: Page, role = SALES): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", role).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

/** The first row's quote id, the route's own subject (the rows live in the builder's shadow root). */
async function firstQuoteId(page: Page): Promise<string> {
  await expect(page.getByTestId("qb-list-row").first()).toBeVisible();
  const id = await page.evaluate(() => {
    const root = document.querySelector("qd-app-root")?.shadowRoot?.querySelector("qd-quote-builder")?.shadowRoot;
    const row = root?.querySelector('[data-testid="qb-list-row"]');
    return row instanceof HTMLElement ? (row.getAttribute("data-quote") ?? "") : "";
  });
  expect(id, "the seeded list holds a row to open").not.toBe("");
  return id;
}

// ── A. the list page is the list alone ────────────────────────────────────────

test("split A: the list page carries no work area", async ({ page }) => {
  await signIn(page);
  await expect(page).toHaveURL(/#\/quotes$/);
  await expect(page.getByTestId("qb-list")).toBeVisible();
  // The work area's own chrome: the stepper's title and context, the flow
  // steps, the workspace. None of it is what the list page shows.
  await expect(page.getByTestId("qb-title")).toBeHidden();
  await expect(page.getByTestId("qb-context")).toBeHidden();
  await expect(page.locator("hos-pos-flow-steps")).toBeHidden();
  await expect(page.getByTestId("qb-workspace")).toBeHidden();
  // The old new quote form is gone from under the list: no picker, no
  // branch select, no expiry, no Create.
  await expect(page.getByRole("group", { name: "New quote" })).toHaveCount(0);
  await expect(page.locator('[data-field="new-branch"]')).toHaveCount(0);
  await expect(page.locator('[data-field="new-expiry"]')).toHaveCount(0);
});

// ── B. the quote page is the work area alone ─────────────────────────────────

test("split B: the quote page carries no list, and All quotes walks back", async ({ page }) => {
  await signIn(page);
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page).toHaveURL(/#\/quotes\/[^/]+$/);
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  // The list is not on the quote's page.
  await expect(page.getByTestId("qb-list")).toBeHidden();
  await expect(page.getByTestId("qb-list-row").first()).toBeHidden();
  // The work area's title row opens with the way back.
  const back = page.getByTestId("qb-all-quotes");
  await expect(back).toBeVisible();
  await expect(back).toContainText("All quotes");
  await back.click();
  await expect(page).toHaveURL(/#\/quotes$/);
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await expect(page.getByTestId("qb-title")).toBeHidden();
});

// ── C. the routes ─────────────────────────────────────────────────────────────

test("split C: the quote route lands on the work area with the list never shown", async ({ page }) => {
  await signIn(page);
  const id = await firstQuoteId(page);
  // A fresh boot straight at the quote's route: the list page never shows.
  await page.goto(`/#/quotes/${id}`);
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await expect(page.getByTestId("qb-title")).toHaveText("Quote");
  await expect(page.getByTestId("qb-list")).toBeHidden();
});

test("split C: New quote is the compose page, and back and forward move between the routes", async ({ page }) => {
  await signIn(page);
  // The primary New quote button at the right of the title row.
  const newQuote = page.locator('button.btn-primary[data-action="new-quote"]');
  await expect(newQuote).toBeVisible();
  await expect(newQuote).toHaveText("New quote");
  await newQuote.click();
  await expect(page).toHaveURL(/#\/quotes\/new$/);
  await expect(page.getByTestId("qb-title")).toHaveText("New Quote");
  await expect(page.getByTestId("qb-identity")).toBeVisible();
  await expect(page.getByTestId("qb-list")).toBeHidden();
  // The browser's back returns to the list, forward to the compose page.
  await page.goBack();
  await expect(page).toHaveURL(/#\/quotes$/);
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.goForward();
  await expect(page).toHaveURL(/#\/quotes\/new$/);
  await expect(page.getByTestId("qb-identity")).toBeVisible();
});

test("split C: a boot through the front door's handoff lands on the list", async ({ page }) => {
  // The handoff record the front door leaves, the till's own shape (the
  // handoff suite seeds it the same way).
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const segment = (value: unknown) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const token = `${segment({ alg: "ES256", typ: "JWT" })}.${segment({
    iss: "hh-demo-stub",
    sub: "demo-account-sales",
    aud: "staff",
    iat: issuedAtSeconds,
    exp: issuedAtSeconds + 60 * 60,
  })}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
  await page.addInitScript(
    ({ key, value }) => window.sessionStorage.setItem(key, value),
    {
      key: "hh-demo.front-door.session.v1",
      value: JSON.stringify({
        token,
        tokenExpiresAtMs: (issuedAtSeconds + 60 * 60) * 1000,
        flow: "sales",
        roleLabel: "Sales",
        user: { userId: "demo-account-sales", email: "sales@demo.invalid", name: "Sales (demo)" },
        roleCode: "inside_sales",
        roleName: "Inside Sales",
        tier: 3,
        scope: "BRANCH",
      }),
    },
  );
  // Even a boot whose address bar names a quote lands on the list: a sign
  // in starts at the desk's landing page.
  await page.goto("/#/quotes/quote-1001");
  await expect(page.getByTestId("qb-list")).toBeVisible({ timeout: 15_000 });
  await expect(page).toHaveURL(/#\/quotes$/);
  await expect(page.getByTestId("qb-title")).toBeHidden();
});

// ── D. the tablet viewport ────────────────────────────────────────────────────

test("split D: the list renders as cards at the tablet viewport", async ({ page }) => {
  await page.setViewportSize({ width: 768, height: 1024 });
  await signIn(page);
  const row = page.getByTestId("qb-list-row").first();
  await expect(row).toBeVisible();
  // The card face: the design tokens' card radius, and a two track grid
  // (the facts stack in card columns) where the desktop row runs one wide
  // line of six.
  const card = await row.evaluate((el) => {
    const style = getComputedStyle(el);
    return { radius: style.borderRadius, tracks: style.gridTemplateColumns.split(" ").length, display: style.display };
  });
  expect(card.radius, "the tablet card carries the card radius token").toBe("16px");
  expect(card.tracks, "the tablet card stacks its facts in two tracks").toBe(2);
  // And the desktop row is the wide row, not the card.
  await page.setViewportSize({ width: 1440, height: 900 });
  const wide = await row.evaluate((el) => {
    const style = getComputedStyle(el);
    return { radius: style.borderRadius, tracks: style.gridTemplateColumns.split(" ").length };
  });
  expect(wide.tracks, "the desktop row runs the wide six track line").toBe(6);
  expect(wide.radius).not.toBe("16px");
});

// ── E. the toolbar: the segmented control and the search ─────────────────────

test("split E: the toolbar carries the segmented control and the search, and both reach the rows", async ({ page }) => {
  await signIn(page);
  const toolbar = page.getByTestId("qd-list-toolbar");
  await expect(toolbar).toBeVisible();
  const tabs = toolbar.locator('[data-control="segmented"][data-field="status-filter"]');
  await expect(tabs).toBeVisible();
  // The six tabs, All pressed, the rest not.
  for (const label of ["All", "Draft", "Sent", "Accepted", "Expired", "Converted"]) {
    await expect(tabs.getByRole("button", { name: label, exact: true })).toBeVisible();
  }
  await expect(tabs.getByRole("button", { name: "All", exact: true })).toHaveAttribute("aria-pressed", "true");
  await expect(tabs.getByRole("button", { name: "Draft", exact: true })).toHaveAttribute("aria-pressed", "false");
  // The search box is the design system's control face.
  const search = toolbar.locator('[data-control="text"][data-field="list-search"]');
  await expect(search).toBeVisible();
  // The rows the seeded world gives the Sales login: two (Draft and
  // Accepted), both Maple Ridge.
  await expect(page.getByTestId("qb-list-row")).toHaveCount(2);
  // A status tab routes the rows: Draft alone answers one.
  await tabs.getByRole("button", { name: "Draft", exact: true }).click();
  await expect(page.getByTestId("qb-list-row")).toHaveCount(1);
  await expect(page.getByTestId("qb-list-row").first()).toContainText("Q-1001");
  await tabs.getByRole("button", { name: "All", exact: true }).click();
  await expect(page.getByTestId("qb-list-row")).toHaveCount(2);
  // The search filters by customer and by number, and the two combine with
  // the tab that is pressed.
  await search.fill("Maple");
  await expect(page.getByTestId("qb-list-row")).toHaveCount(2);
  await search.fill("Q-1001");
  await expect(page.getByTestId("qb-list-row")).toHaveCount(1);
  await search.fill("Fenwick");
  await expect(page.getByTestId("qb-list")).toContainText("No quotes.");
  await search.fill("");
  await expect(page.getByTestId("qb-list-row")).toHaveCount(2);
});

test("split E: the Sales manager's tabs also carry Approvals, and Sales sees neither", async ({ page }) => {
  await signIn(page, MANAGER);
  const tabs = page.getByTestId("qd-list-toolbar").locator('[data-control="segmented"][data-field="status-filter"]');
  await expect(tabs.getByRole("button", { name: "Approvals", exact: true })).toBeVisible();
  // The Approvals tab is the manager's own view: the approvals panel stands
  // in the page's content place, the list stands down.
  await tabs.getByRole("button", { name: "Approvals", exact: true }).click();
  await expect(page.locator("qd-approvals")).toBeVisible();
  await expect(page.getByTestId("qb-list")).toBeHidden();
  // A status tab walks back to the list.
  await tabs.getByRole("button", { name: "All", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await expect(page.locator("qd-approvals")).toBeHidden();
  // Sales sees no Approvals tab at all (the role wall is the tab's own).
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await signIn(page);
  const salesTabs = page.getByTestId("qd-list-toolbar").locator('[data-control="segmented"][data-field="status-filter"]');
  await expect(salesTabs.getByRole("button", { name: "Approvals", exact: true })).toHaveCount(0);
});

// ── The New quote button's icon (decision log row 990) ───────────────────────

for (const theme of ["light", "dark"] as const) {
  for (const [name, size] of [["desktop", { width: 1440, height: 900 }], ["tablet", { width: 768, height: 1024 }]] as const) {
    test(`split button: the New quote plus is the label's size and centred on its line (${theme}, ${name})`, async ({ page }) => {
      await page.setViewportSize(size);
      await signIn(page);
      await page.evaluate((t) => document.documentElement.setAttribute("data-theme", t), theme);
      const button = page.getByTestId("qd-list-page").getByRole("button", { name: "New quote", exact: true });
      await expect(button).toBeVisible();
      const m = await button.evaluate((el) => {
        const mark = el.querySelector("svg");
        const text = [...el.childNodes].find((n) => n.nodeType === Node.TEXT_NODE && (n.textContent ?? "").includes("New quote"));
        if (mark === null || text === undefined) return null;
        const range = document.createRange();
        range.selectNodeContents(text);
        const t = range.getBoundingClientRect();
        const i = mark.getBoundingClientRect();
        const b = el.getBoundingClientRect();
        return {
          font: parseFloat(getComputedStyle(el).fontSize),
          iconW: i.width,
          iconH: i.height,
          iconMid: i.top + i.height / 2,
          textMid: t.top + t.height / 2,
          boxMid: b.top + b.height / 2,
          gap: t.left - i.right,
        };
      });
      expect(m, "the button carries a plus icon beside its label").not.toBeNull();
      const r = m!;
      expect(Math.abs(r.iconH - r.font), "the icon is the label's height").toBeLessThanOrEqual(0.5);
      expect(Math.abs(r.iconW - r.font), "the icon is the label's width").toBeLessThanOrEqual(0.5);
      expect(Math.abs(r.iconMid - r.textMid), "the icon and the label share one centre line").toBeLessThanOrEqual(1);
      expect(Math.abs(r.iconMid - r.boxMid), "both sit on the button's own centre").toBeLessThanOrEqual(1);
      expect(Math.abs(r.gap - 8), "the design system's icon gap").toBeLessThanOrEqual(0.5);
    });
  }
}

for (const theme of ["light", "dark"] as const) {
  for (const [name, size] of [["desktop", { width: 1440, height: 900 }], ["tablet", { width: 768, height: 1024 }]] as const) {
    test(`split button: the quote page's New Quote plus is the label's size and centred on its line (${theme}, ${name})`, async ({ page }) => {
      await page.setViewportSize(size);
      await signIn(page);
      await page.evaluate((t) => document.documentElement.setAttribute("data-theme", t), theme);
      await page.getByTestId("qb-list-row").first().click();
      const button = page.getByTestId("qb-new-quote");
      await expect(button).toBeVisible();
      const m = await button.evaluate((el) => {
        const mark = el.querySelector("svg");
        const text = [...el.childNodes].find((n) => n.nodeType === Node.TEXT_NODE && (n.textContent ?? "").includes("New Quote"));
        if (mark === null || text === undefined) return null;
        const range = document.createRange();
        range.selectNodeContents(text);
        const t = range.getBoundingClientRect();
        const i = mark.getBoundingClientRect();
        return { font: parseFloat(getComputedStyle(el).fontSize), iconH: i.height, iconMid: i.top + i.height / 2, textMid: t.top + t.height / 2, gap: t.left - i.right };
      });
      expect(m, "the button carries a plus icon beside its label").not.toBeNull();
      const r = m!;
      expect(Math.abs(r.iconH - r.font), "the icon is the label's height").toBeLessThanOrEqual(0.5);
      expect(Math.abs(r.iconMid - r.textMid), "the icon and the label share one centre line").toBeLessThanOrEqual(1);
      expect(Math.abs(r.gap - 8), "the design system's icon gap").toBeLessThanOrEqual(0.5);
    });
  }
}
