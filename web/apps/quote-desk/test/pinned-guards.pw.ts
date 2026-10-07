// Round 10 item 14 (row 1066): guards no test killed until now, each pinned by a leg that goes red when the guard
// is taken away (test/mutants.json names the exact edit): the Copy link busy guard, the release in the finally of
// Copy link and Preview, the walk a create makes (focus goes to the search box, not the heading), and the exact code
// row marked at a position that is not the first.
import { expect, test, type Page } from "@playwright/test";
import { hold, open, seed, settle, signIn, stubBrowserChannels, writes } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("pinned copy link busy: two presses in one turn make one link", async ({ page }) => {
  await stubBrowserChannels(page);
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const seen = writes(page);
  await page.getByTestId("qb-copy-link").evaluate((el) => {
    (el as HTMLButtonElement).click();
    (el as HTMLButtonElement).click();
  });
  await settle(page);
  expect(seen.filter((w) => /POST .*\/share-links /.test(w))).toHaveLength(1);
});

test("pinned copy link release: a copy that failed lets the next press through", async ({ page }) => {
  await stubBrowserChannels(page);
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const mint = await hold(page, /\/api\/v1\/quotes\/q-1004\/share-links$/, { method: "POST", times: 1, refuse: { status: 500, error: "quote: links are down" } });
  await page.getByTestId("qb-copy-link").click();
  await mint.asked;
  await mint.release();
  await expect(page.getByTestId("qb-error")).not.toHaveText("");
  // The press that failed has let go: Send is offered again, and a second copy is sent.
  await expect(page.getByTestId("qb-send")).toBeEnabled();
  const seen = writes(page);
  await page.getByTestId("qb-copy-link").click();
  await expect.poll(() => seen.filter((w) => /POST .*\/share-links /.test(w)).length).toBe(1);
});

test("pinned preview release: a preview that failed lets the next press through", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const read = await hold(page, /\/api\/v1\/quotes\/q-1004\/customer-view$/, { method: "GET", times: 1, refuse: { status: 500, error: "quote: the preview is down" } });
  await page.getByTestId("qb-preview").click();
  await read.asked;
  await read.release();
  await expect(page.getByTestId("qb-error")).not.toHaveText("");
  await expect(page.getByTestId("qb-send")).toBeEnabled();
  await page.getByTestId("qb-preview").click();
  await expect(page.getByTestId("qb-preview-pane")).toBeVisible();
});

test("pinned create walk: after the customer is picked the caret is in the search box, not on the heading", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  // Every place focus lands from here on is written down, so a heading that takes it for a moment is caught too.
  await page.evaluate(() => {
    const landed: string[] = [];
    (window as unknown as { __landed: string[] }).__landed = landed;
    document.addEventListener("focusin", (event) => {
      const target = event.composedPath()[0] as Element;
      landed.push(target.getAttribute("data-testid") ?? target.tagName.toLowerCase());
    });
  });
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  const box = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await expect(box).toBeFocused();
  await box.fill("PSN-100");
  await box.press("Enter");
  await expect(page.getByTestId("qb-context")).toContainText("Q-");
  await expect(box).toBeFocused();
  await settle(page);
  expect(await page.evaluate(() => (window as unknown as { __landed: string[] }).__landed)).not.toContain("qb-title");
});

async function holdSearchWith(page: Page, hits: Array<Record<string, unknown>>): Promise<void> {
  await page.route(/\/api\/v1\/products\/counter-search/, (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: hits }) }));
}

test("pinned exact row: the exact code row is marked where it stands, not only at the top", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const base = { base_uom: "ea", sold_by: "ea", has_image: false, match_reason: "NAME", on_hand_branch: 5, on_hand_by_branch: {}, list_price_cents: 1000, description: "" };
  await holdSearchWith(page, [
    { ...base, product_id: "p-edge-8", sku: "EDG-08", name: "Edge Restraint 8 ft" },
    { ...base, product_id: "p-polymer-sand", sku: "PSN-100", name: "Polymeric Locking Sand" },
  ]);
  const box = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await box.fill("PSN-100");
  const rows = page.locator("quote-search-add").getByTestId("search-add-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(1)).toHaveAttribute("aria-selected", "true");
  await expect(rows.nth(0)).not.toHaveAttribute("aria-selected", "true");
});
