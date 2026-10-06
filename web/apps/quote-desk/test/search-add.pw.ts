// The Quote Desk's search and add box (decision log row 991): one box, results as you type, the line goes in by a
// click or by arrows and Enter, and the box stays open and focused for the next item. The component is mounted here
// by the test (the workarea's swap is the split list coder's one line), the way the workarea will hand it its
// branch and customer, and the events it fires are collected on the page.
import { expect, test, type Locator, type Page } from "@playwright/test";
import { shot } from "./shot";

const TRENTON_ID = "6f1c2b3a-0000-4000-8000-000000000001";
const MAPLE_RIDGE = "c-maple-ridge";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

type Added = { productId: string; code: string; unit: string; quantity: number };

async function mountBox(page: Page): Promise<{ box: Locator; input: Locator }> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.evaluate(
    ({ branch, customer }) => {
      const host = document.createElement("quote-search-add");
      host.setAttribute("branch-id", branch);
      host.setAttribute("customer-id", customer);
      host.setAttribute("data-testid", "search-add-host");
      host.style.cssText = "position:fixed;top:96px;left:48px;width:560px;z-index:50";
      const w = window as unknown as { __adds: unknown[] };
      w.__adds = [];
      host.addEventListener("quote-line-add", (event) => w.__adds.push((event as CustomEvent).detail));
      document.body.append(host);
    },
    { branch: TRENTON_ID, customer: MAPLE_RIDGE },
  );
  const box = page.getByTestId("search-add-host");
  return { box, input: box.getByRole("combobox", { name: "Search products by name, code or category" }) };
}

async function adds(page: Page): Promise<Added[]> {
  return page.evaluate(() => (window as unknown as { __adds: Added[] }).__adds);
}

test("search by name shows products and adds the row on click", async ({ page }) => {
  const { box, input } = await mountBox(page);
  await expect(input).toBeVisible();
  await input.fill("polymeric");
  const rows = box.getByTestId("search-add-row");
  await expect(rows).toHaveCount(1);
  // Name, unit, the price this customer pays (the server's, 18 percent under retail), stock at the branch.
  await expect(rows.first()).toContainText("Polymeric Locking Sand");
  await expect(rows.first()).toContainText("PSN-100");
  await expect(rows.first()).toContainText("bag");
  await expect(box.getByTestId("search-add-price").first()).toContainText("11.48");
  await expect(rows.first()).toContainText("400 on hand");
  await shot(page, "search-add-name");
  await rows.first().click();
  expect(await adds(page)).toEqual([{ productId: "p-polymer-sand", code: "PSN-100", unit: "bag", quantity: 1 }]);
});

test("search by code adds at once", async ({ page }) => {
  const { input } = await mountBox(page);
  await input.fill("PSN-100");
  await expect.poll(async () => (await adds(page)).length).toBe(1);
  expect(await adds(page)).toEqual([{ productId: "p-polymer-sand", code: "PSN-100", unit: "bag", quantity: 1 }]);
  // The box is ready for the next item.
  await expect(input).toBeFocused();
  await expect(input).toHaveValue("");
});

test("keyboard arrow + Enter adds the highlighted row", async ({ page }) => {
  const { box, input } = await mountBox(page);
  await input.fill("stone");
  await expect(box.getByTestId("search-add-row")).toHaveCount(3);
  // The first row is highlighted as the results arrive; two arrows walk to the third.
  await expect(box.getByTestId("search-add-row").first()).toHaveAttribute("aria-selected", "true");
  await input.press("ArrowDown");
  await input.press("ArrowDown");
  await expect(box.getByTestId("search-add-row").nth(2)).toHaveAttribute("aria-selected", "true");
  await input.press("ArrowUp");
  await expect(box.getByTestId("search-add-row").nth(1)).toHaveAttribute("aria-selected", "true");
  const name = (await box.getByTestId("search-add-row").nth(1).getAttribute("data-code")) ?? "";
  await input.press("Enter");
  const got = await adds(page);
  expect(got).toHaveLength(1);
  expect(got[0].code).toBe(name);
  expect(got[0].quantity).toBe(1);
  await expect(input).toBeFocused();
});

test("three lines in a row without leaving the box", async ({ page }) => {
  const { box, input } = await mountBox(page);
  await input.click();
  await input.fill("stone");
  await expect(box.getByTestId("search-add-row")).toHaveCount(3);
  await input.press("Enter");
  await expect(input).toBeFocused();
  await input.fill("edge");
  await expect(box.getByTestId("search-add-row")).toHaveCount(1);
  await box.getByTestId("search-add-row").first().click();
  await expect(input).toBeFocused();
  await input.fill("polymeric");
  await expect(box.getByTestId("search-add-row")).toHaveCount(1);
  await input.press("Enter");
  await expect(input).toBeFocused();
  const got = await adds(page);
  expect(got.map((line) => line.code)).toEqual(["HPV-60", "EDG-08", "PSN-100"]);
  expect(got.every((line) => line.quantity === 1)).toBe(true);
  // Typing continues in the same box with no click between lines.
  await page.keyboard.type("clear");
  await expect(input).toHaveValue("clear");
  await shot(page, "search-add-three");
});

test("empty results says so in words", async ({ page }) => {
  const { box, input } = await mountBox(page);
  await input.fill("zzzzqq");
  const empty = box.getByTestId("search-add-empty");
  await expect(empty).toBeVisible();
  await expect(empty).toContainText("No products match");
  await expect(empty).toContainText("zzzzqq");
  await expect(box.getByTestId("search-add-row")).toHaveCount(0);
  expect(await adds(page)).toEqual([]);
  // Enter on an empty answer adds nothing and leaves the box where it is.
  await input.press("Enter");
  expect(await adds(page)).toEqual([]);
  await expect(input).toBeFocused();
});

// ── the late answers (decision log row 991, the review's P2s) ──────────────────────────────────────────────────
// The legs below hold an answer of the stub on the wire, change what the box was asked about, and release the
// old answer; the box must show only what belongs to its current customer and its current query.

type Held = { url: URL; body: { customer_id?: string } | null; release: (mode?: "pass" | "fail") => Promise<void> };

/** Hold every request to `pattern`; each is parked until the test releases it (pass it on, or answer 500). */
async function holdRequests(page: Page, pattern: RegExp, onlyWhen: (url: URL, body: { customer_id?: string } | null) => boolean = () => true) {
  const parked: Held[] = [];
  await page.route(pattern, async (route) => {
    const url = new URL(route.request().url());
    const raw = route.request().postData();
    const body = raw === null ? null : (JSON.parse(raw) as { customer_id?: string });
    if (!onlyWhen(url, body)) {
      await route.continue();
      return;
    }
    await new Promise<void>((resolve) => {
      parked.push({
        url,
        body,
        release: async (mode = "pass") => {
          if (mode === "fail") await route.fulfill({ status: 500, body: "{}" });
          else await route.continue();
          resolve();
        },
      });
    });
  });
  return {
    parked,
    async waitFor(count: number): Promise<void> {
      await expect.poll(() => parked.length).toBeGreaterThanOrEqual(count);
    },
  };
}

/** Let the page run every continuation of an answer that has just come back. */
async function settle(page: Page): Promise<void> {
  await page.evaluate(() => new Promise<void>((done) => requestAnimationFrame(() => requestAnimationFrame(() => done()))));
}

const SEARCH = /\/api\/v1\/products\/counter-search/;
const PRICES = /\/api\/v1\/pos\/counter-prices/;

test("a price answer for the customer who was switched away from is dropped", async ({ page }) => {
  const { box, input } = await mountBox(page);
  const prices = await holdRequests(page, PRICES, (_url, body) => body?.customer_id === MAPLE_RIDGE);
  await input.fill("polymeric");
  await expect(box.getByTestId("search-add-row")).toHaveCount(1);
  await prices.waitFor(1);
  // The first customer's answer is out; the quote moves to the walk-in, whose read is answered at once.
  await box.evaluate((host) => host.setAttribute("customer-id", ""));
  await expect(box.getByTestId("search-add-price").first()).toContainText("14.00");
  // The first customer's answer lands last and must not be shown.
  await prices.parked[0].release();
  await settle(page);
  await expect(box.getByTestId("search-add-price").first()).toContainText("14.00");
  await expect(box.getByTestId("search-add-price").first()).not.toContainText("11.48");
});

test("a price answer for an older query is dropped", async ({ page }) => {
  const { box, input } = await mountBox(page);
  const prices = await holdRequests(page, PRICES, (_url, body) => JSON.stringify(body).includes("p-polymer-sand"));
  await input.fill("polymeric");
  await expect(box.getByTestId("search-add-row")).toHaveCount(1);
  await prices.waitFor(1);
  // A new word answers and prices at once; then the old word's price answer lands.
  await input.fill("edge");
  await expect(box.getByTestId("search-add-row")).toHaveCount(1);
  await expect(box.getByTestId("search-add-price").first()).toContainText("26.24");
  await prices.parked[0].release();
  await settle(page);
  await expect(box.getByTestId("search-add-price").first()).toContainText("26.24");
});

test("a search answer that lands after the box moved on adds nothing and shows nothing", async ({ page }) => {
  const { box, input } = await mountBox(page);
  const search = await holdRequests(page, SEARCH);
  await input.fill("PSN-100");
  await search.waitFor(1);
  // The person clears down to one letter while the answer for a whole code is out.
  await input.fill("z");
  await search.parked[0].release();
  await settle(page);
  expect(await adds(page)).toEqual([]);
  await expect(box.getByTestId("search-add-row")).toHaveCount(0);
});

test("a failed search answer that lands after the box moved on does not speak for the new word", async ({ page }) => {
  const { box, input } = await mountBox(page);
  const search = await holdRequests(page, SEARCH);
  await input.fill("polymeric");
  await search.waitFor(1);
  await input.fill("zzzzqq");
  await search.waitFor(2);
  await search.parked[0].release("fail");
  await settle(page);
  await expect(box.getByTestId("search-add-busy")).toBeVisible();
  await expect(box.getByTestId("search-add-empty")).toHaveCount(0);
  await search.parked[1].release();
  await expect(box.getByTestId("search-add-empty")).toContainText("No products match");
});

test("Enter while a search is in flight never adds the previous word's row", async ({ page }) => {
  const { box, input } = await mountBox(page);
  await input.fill("polymeric");
  await expect(box.getByTestId("search-add-row")).toHaveCount(1);
  const search = await holdRequests(page, SEARCH);
  await input.fill("edge");
  await search.waitFor(1);
  // The word changed and its answer is not back: the row on screen is still the old word's.
  await expect(box.getByTestId("search-add-row").first()).toHaveAttribute("data-code", "PSN-100");
  await input.press("Enter");
  expect(await adds(page)).toEqual([]);
  // When the answer lands the Enter is honoured, on the current word's row.
  await search.parked[0].release();
  await expect.poll(async () => (await adds(page)).length).toBe(1);
  expect((await adds(page))[0].code).toBe("EDG-08");
});

test("an Enter typed before the word's answer is a plain word is not carried to the next word", async ({ page }) => {
  const { box, input } = await mountBox(page);
  const search = await holdRequests(page, SEARCH);
  await input.fill("edge");
  await search.waitFor(1);
  await input.press("Enter");
  // More typing: the Enter was for "edge", not for what is typed now.
  await input.fill("polymeric");
  await search.waitFor(2);
  await search.parked[0].release();
  await search.parked[1].release();
  await expect(box.getByTestId("search-add-row")).toHaveCount(1);
  await settle(page);
  expect(await adds(page)).toEqual([]);
});

test("a code that is only the start of what is being typed is not added", async ({ page }) => {
  const { box, input } = await mountBox(page);
  // Two products: the code AB-1 is the start of the code AB-12.
  const hit = (id: string, sku: string) => ({
    product_id: id,
    sku,
    name: `Product ${sku}`,
    description: "",
    base_uom: "pc",
    sold_by: "pc",
    has_image: false,
    match_reason: "SKU_PREFIX",
    on_hand_branch: 4,
    on_hand_by_branch: {},
    list_price_cents: 100,
  });
  const gate: { release: () => void } = { release: () => undefined };
  const held = new Promise<void>((resolve) => (gate.release = resolve));
  let asked = 0;
  await page.route(SEARCH, async (route) => {
    const q = new URL(route.request().url()).searchParams.get("q");
    if (q === "AB-1") {
      asked += 1;
      await held;
      await route.fulfill({ json: { items: [hit("p-ab-1", "AB-1"), hit("p-ab-12", "AB-12")] } });
    } else {
      await route.fulfill({ json: { items: [hit("p-ab-12", "AB-12")] } });
    }
  });
  await input.fill("AB-1");
  await expect.poll(() => asked).toBe(1);
  // The person keeps typing while the answer for the start of the code is out.
  await input.press("End");
  await input.pressSequentially("2");
  gate.release();
  // The whole code, once the box has settled on it, adds that product and only that one.
  await expect.poll(async () => (await adds(page)).length).toBeGreaterThan(0);
  await settle(page);
  expect((await adds(page)).map((line) => line.code)).toEqual(["AB-12"]);
  expect(await box.getByTestId("search-add-row").count()).toBe(0);
});

test("a branch change reads the rows' stock again", async ({ page }) => {
  const { box, input } = await mountBox(page);
  await page.route(SEARCH, async (route) => {
    const branch = new URL(route.request().url()).searchParams.get("branch_id");
    const real = await route.fetch();
    const body = (await real.json()) as { items: Array<{ on_hand_branch: number }> };
    for (const item of body.items) item.on_hand_branch = branch === TRENTON_ID ? 400 : 7;
    await route.fulfill({ json: body });
  });
  await input.fill("polymeric");
  await expect(box.getByTestId("search-add-stock").first()).toContainText("400 on hand");
  await box.evaluate((host) => host.setAttribute("branch-id", "6f1c2b3a-0000-4000-8000-000000000002"));
  await expect(box.getByTestId("search-add-stock").first()).toContainText("7 on hand");
  expect(await adds(page)).toEqual([]);
});

test("a category word finds the products of that category", async ({ page }) => {
  const { box, input } = await mountBox(page);
  await input.fill("pavers");
  const rows = box.getByTestId("search-add-row");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("HPV-60");
});
