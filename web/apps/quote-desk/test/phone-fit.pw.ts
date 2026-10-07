// Round 10 item 5 (row 1092): on a phone, from 320 to 480 px, nothing scrolls the page sideways. The Quotes list's
// status tabs, the New quote stepper and Convert's payment fields all fit; a wide table may scroll inside its own box.
import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const WIDTHS = [320, 360, 390, 480];

async function overflow(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
}

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

/** The names of the elements, deepest first, whose right edge passes the viewport: what to fix when a leg is red. */
async function wide(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out: string[] = [];
    const walk = (root: ParentNode): void => {
      for (const el of Array.from(root.querySelectorAll("*"))) {
        const box = el.getBoundingClientRect();
        if (box.width > 0 && box.right > document.documentElement.clientWidth + 1) out.push(`${el.tagName.toLowerCase()}${el.getAttribute("data-testid") ? `[${el.getAttribute("data-testid")}]` : ""}`);
        if (el.shadowRoot !== null) walk(el.shadowRoot);
      }
    };
    walk(document);
    return out.slice(0, 12);
  });
}

/** The page itself never scrolls sideways; when it does, the message names what reaches past the edge. */
async function fits(page: Page, what = ""): Promise<void> {
  const sideways = await overflow(page);
  expect(sideways, sideways > 0 ? `${what} past the right edge: ${(await wide(page)).join(", ")}` : "").toBeLessThanOrEqual(0);
}

for (const width of WIDTHS) {
  test.describe(`at ${width} px`, () => {
    test.use({ viewport: { width, height: 800 } });

    test("the Quotes list does not scroll the page sideways, and its status tabs are reachable", async ({ page }) => {
      await signIn(page);
      await expect(page.getByTestId("qd-list-tabs")).toBeVisible();
      await fits(page);
      const tabs = page.getByTestId("qd-list-tabs").getByRole("button");
      for (const name of await tabs.allInnerTexts()) await expect(page.getByTestId("qd-list-tabs").getByRole("button", { name: name.trim().split("\n")[0]!, exact: false }).first()).toBeVisible();
    });

    test("the New quote screen and its stepper fit", async ({ page }) => {
      await signIn(page);
      await page.locator('button.btn-primary[data-action="new-quote"]').first().click();
      await expect(page.getByTestId("qb-topbar").or(page.getByTestId("qb-context")).first()).toBeVisible();
      await fits(page);
    });

    test("an open quote fits", async ({ page }) => {
      await signIn(page);
      await page.getByTestId("qb-list-row").first().click();
      await expect(page.getByTestId("qb-context")).toBeVisible();
      await fits(page);
    });

    test("Convert's payment step fits, for an account and for a Cash On Demand customer", async ({ page }) => {
      for (const customer of ["c-fenwick", "c-walkin"]) {
        const seeded = await page.request.post("/_stub/seed-quote", { data: { status: "ACCEPTED", customer_id: customer, lines: [{ p: "p-wall-std", q: 4 }] } });
        const id = ((await seeded.json()) as { id: string }).id;
        await signIn(page);
        await page.evaluate((quote) => {
          window.location.hash = `#/quotes/${quote}/convert`;
        }, id);
        for (let step = 0; step < 3; step += 1) await page.getByTestId("qc-next").click();
        await expect(page.getByTestId("qc-payment")).toBeVisible();
        await fits(page, customer);
        // Each field shows its whole face: nothing is cut inside its own box ("Cas" for Cash).
        for (const field of ["qc-pay-method", "qc-pay-reference"]) {
          const box = await page.getByTestId(field).boundingBox();
          expect(box === null ? 0 : box.x + box.width).toBeLessThanOrEqual(width);
          expect(box === null ? 0 : box.width, `${field} is wide enough to read`).toBeGreaterThanOrEqual(Math.min(200, width - 80));
        }
      }
    });
  });
}
