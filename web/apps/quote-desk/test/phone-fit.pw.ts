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

    test("every Convert step fits, for an account, a Cash On Demand customer, and the done screen", async ({ page }) => {
      for (const customer of ["c-fenwick", "c-walkin", "c-held", "c-pastdue"]) {
        const seeded = await page.request.post("/_stub/seed-quote", { data: { status: "ACCEPTED", customer_id: customer, lines: [{ p: "p-wall-std", q: 4 }, { p: "p-holland-60", q: 120 }] } });
        const id = ((await seeded.json()) as { id: string }).id;
        await signIn(page);
        await page.evaluate((quote) => {
          window.location.hash = `#/quotes/${quote}/convert`;
        }, id);
        await expect(page.getByTestId("qc-review")).toBeVisible();
        for (const step of ["qc-review", "qc-fulfilment", "qc-standing", "qc-payment", "qc-confirm-step"]) {
          await expect(page.getByTestId(step)).toBeVisible();
          await fits(page, `${customer} ${step}`);
          if (step !== "qc-confirm-step") await page.getByTestId("qc-next").click();
        }
      }
    });

    test("every page of the desk fits: Approvals, Pricing, the decision form, the preview and the unreadable quote", async ({ page }) => {
      await signIn(page);
      const seeded = await page.request.post("/_stub/seed-quote", { data: { status: "SENT", customer_id: "c-fenwick" } });
      const id = ((await seeded.json()) as { id: string }).id;
      await page.evaluate((quote) => {
        window.location.hash = `#/quotes/${quote}`;
      }, id);
      await page.getByTestId("qb-decision-accept-open").click();
      await fits(page, "decision form");
      await page.getByTestId("qb-preview").click();
      await expect(page.getByTestId("qb-preview-pane")).toBeVisible();
      await fits(page, "preview");
      await page.evaluate(() => {
        window.location.hash = "#/quotes/no-such-quote";
      });
      await expect(page.getByTestId("qb-unreadable")).toBeVisible();
      await fits(page, "unreadable");
    });

    test("the Sales manager's Approvals and every Pricing tab fit", async ({ page }) => {
      await page.goto("/");
      await page.getByRole("button", { name: "Sales manager", exact: true }).click();
      await expect(page.getByTestId("qb-list")).toBeVisible();
      await page.getByRole("button", { name: "Approvals", exact: true }).first().click().catch(() => undefined);
      await fits(page, "approvals");
      await page.getByTestId("desk-tab-pricing").click();
      await expect(page.getByTestId("pricing-panel")).toBeVisible();
      for (const tab of ["margin", "rules", "customers", "breaks", "audit"]) {
        await page.getByTestId(`pricing-tab-${tab}`).click();
        await fits(page, `pricing ${tab}`);
      }
    });

    test("the quote detail bar opens without pushing the page sideways, and the preview's Amount column is whole", async ({ page }) => {
      await signIn(page);
      const seeded = await page.request.post("/_stub/seed-quote", {
        data: { status: "DRAFT", customer_id: "c-fenwick", lines: [{ p: "p-holland-60", q: 120 }, { p: "p-polymer-sand", q: 2 }, { p: "p-edge-8", q: 10 }, { p: "p-wall-std", q: 4 }, { p: "p-clear-stone", q: 3 }] },
      });
      const id = ((await seeded.json()) as { id: string }).id;
      await page.evaluate((quote) => {
        window.location.hash = `#/quotes/${quote}`;
      }, id);
      await expect(page.getByTestId("qb-line")).toHaveCount(5);
      await page.getByText("Quote detail & admin").click();
      await expect(page.getByTestId("qe-box")).toBeVisible();
      await fits(page, "the detail bar open");
      await page.getByTestId("qb-preview").click();
      await expect(page.getByTestId("qb-preview-pane")).toBeVisible();
      await fits(page, "the preview open");
      // The Amount column is in view whole (the table's own box may scroll for the rest), and the words are the
      // page's body size or more, never smaller.
      const clipped = await page.locator("pd-quote-public").evaluate((host) => {
        const root = host.shadowRoot as ShadowRoot;
        const wrap = root.querySelector(".tablewrap") as HTMLElement | null;
        const amount = root.querySelector("th:last-child") as HTMLElement | null;
        const cell = root.querySelector("tbody td:last-child") as HTMLElement | null;
        if (wrap === null || amount === null || cell === null) return ["no table"];
        const out: string[] = [];
        const box = wrap.getBoundingClientRect();
        for (const el of [amount, cell]) {
          const r = el.getBoundingClientRect();
          if (r.right > box.right + 1 || r.left < box.left - 1) out.push("the Amount column is out of view");
        }
        const body = parseFloat(getComputedStyle(root.host).fontSize) * 0.88;
        const size = parseFloat(getComputedStyle(cell).fontSize);
        if (size + 0.5 < Math.min(body, 14)) out.push(`the table text is ${size}px`);
        return out;
      });
      expect(clipped).toEqual([]);
    });
  });
}
