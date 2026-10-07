// Round 7 folds: the compose view and the panels, the refused create's reason, the hold expiry panel at New Quote and
// the sign out flush's store close.
import { expect, test, type Page } from "@playwright/test";
import { hold, open, quiet, settle, signIn, showList } from "./switch-helpers.js";

/** The compose page under the split-list navigation: the New quote button walks the desk to #/quotes/new. */
async function newQuote(page: Page): Promise<void> {
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
}

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const QUOTE_PANELS = ["qd-quote-edit", "qd-quote-assist", "qd-quote-history", "qd-quote-followups", "qd-quote-hold-expiry", "qd-quote-availability", "qd-quote-guard"];

test("compose draws none of the quote panels and leaves no blank gap in the detail", async ({ page }) => {
  await signIn(page, "Sales");
  await newQuote(page);
  await expect(page.getByTestId("qb-title")).toHaveText("New Quote");
  await page.getByText("Quote detail & admin").click();
  const seen = await page.locator("qd-quote-builder").evaluate((builder, tags) => {
    const root = builder.shadowRoot as ShadowRoot;
    const panels = tags.map((tag) => {
      const el = root.querySelector(tag) as HTMLElement | null;
      const drawn = el === null ? [] : [...(el.shadowRoot?.children ?? [])].filter((c) => c.tagName !== "STYLE").map((c) => c.tagName);
      return { tag, mounted: el !== null, height: el?.getBoundingClientRect().height ?? -1, drawn };
    });
    const seams = root.querySelector(".seams") as HTMLElement;
    const boxes = [...seams.children].map((c) => c.getBoundingClientRect()).filter((r) => r.height > 0);
    const gaps = boxes.slice(1).map((r, i) => r.top - (boxes[i] as DOMRect).bottom);
    return { panels, gaps };
  }, QUOTE_PANELS);
  for (const panel of seen.panels) {
    expect(panel.mounted, `${panel.tag} stays mounted so late answers have somewhere to land`).toBe(true);
    expect(panel.height, `${panel.tag} takes no room in compose`).toBe(0);
    expect(panel.drawn, `${panel.tag} draws nothing without a quote`).toEqual([]);
  }
  expect(seen.gaps.length).toBeGreaterThan(0);
  for (const gap of seen.gaps) expect(gap, "no blank space between the boxes that do show").toBeLessThanOrEqual(14);
  await expect(page.getByTestId("qg-box")).toHaveCount(0);
});

test("an open quote draws its panels in the detail", async ({ page }) => {
  await signIn(page, "Sales");
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qg-box")).toBeVisible();
  await expect(page.getByTestId("qb-quote-panels")).toBeVisible();
});

test("P2-2 leaving through New Quote: a re-hold that lands while the hold expiry panel is gone still announces its quote", async ({ page, request }) => {
  await request.post("/_stub/hold-lapse", { data: { quote_id: "q-1001", line_no: 2 } });
  await signIn(page, "Sales manager");
  await open(page, "Q-1001");
  await page.getByText("Quote detail & admin").click();
  const panel = page.locator("qd-quote-hold-expiry");
  await expect(panel.getByRole("button", { name: "Re-hold these lines" })).toBeEnabled();
  const resync = await hold(page, /\/quotes\/q-1001\/holds\/resync$/, { method: "POST", times: 1 });
  await panel.getByRole("button", { name: "Re-hold these lines" }).click();
  await resync.asked;
  await page.getByRole("button", { name: "New Quote" }).click();
  await expect(page.getByTestId("qb-title")).toHaveText("New Quote");
  // The panel stays mounted at New Quote: the announcement of the late success is made by the panel itself.
  await expect(page.locator("qd-quote-hold-expiry")).toHaveCount(1);
  await page.evaluate(() => {
    const seen: string[] = [];
    (window as unknown as { __changed: string[] }).__changed = seen;
    document.addEventListener("quote-changed", (e) => seen.push((e as CustomEvent).detail.quoteId));
  });
  await resync.release();
  await settle(page);
  const changed = await page.evaluate(() => (window as unknown as { __changed: string[] }).__changed);
  expect(changed).toContain("q-1001");
});

test("sign out flush: the layout store is stopped when the flush has run, whatever the page does next", async ({ page }) => {
  await signIn(page, "Sales");
  await open(page, "Q-1001");
  await quiet(page);
  const writes: string[] = [];
  page.on("request", (request) => {
    if (request.method() !== "GET" && new URL(request.url()).pathname.endsWith("/my-workspace")) writes.push(request.method());
  });
  // The sign out signal alone, with nothing leaving the page: the flush runs and the host's own wait ends.
  await page.evaluate(async () => {
    const detail = { waits: [] as Promise<void>[] };
    window.dispatchEvent(new CustomEvent("qd-sign-out", { detail }));
    await Promise.allSettled(detail.waits);
  });
  // A layout change after the flush: its debounce fires, and nothing of it may be sent.
  await page.getByRole("button", { name: "Swap panels" }).click();
  await page.waitForTimeout(1200);
  await settle(page);
  expect(writes).toEqual([]);
});
