// PR 121 round 4 findings: each test holds one answer back behind a delayed route, so the order of events is exact
// and does not depend on the machine's speed.
import { expect, test, type Page } from "@playwright/test";
import { hold, open, quiet, row, seed, settle, signIn, writes } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function openDetail(page: Page): Promise<void> {
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qe-box")).toBeVisible();
}

async function signOutAndInAs(page: Page, role: string): Promise<void> {
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

test("A P2-1 a quantity write still out at sign out sends nothing more under the next person's session", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  const patch = await hold(page, /\/quotes\/q-1001\/lines\/1$/, { method: "PATCH", snapshot: true });
  await page.getByRole("button", { name: /^Increase quantity/ }).first().click();
  await patch.asked;
  await signOutAndInAs(page, "Sales manager");
  const seen = writes(page);
  await patch.release();
  await settle(page);
  expect(seen).toEqual([]);
});

test("A P2-1 an edit panel save still out at sign out sends no discount write under the next person's session", async ({ page }) => {
  await signIn(page, "Sales manager");
  await row(page, "Q-1001").click();
  await openDetail(page);
  await expect(page.getByTestId("qe-title")).toContainText("Q-1001");
  const panel = page.locator("qd-quote-edit");
  const line = panel.getByTestId("qe-line").first();
  await line.locator('[data-field="qty"]').fill("3");
  await line.locator('[data-field="disc"]').fill("4");
  const patch = await hold(page, /\/quotes\/q-1001\/lines\/1$/, { method: "PATCH", times: 1, snapshot: true });
  await line.getByRole("button", { name: "Save" }).click();
  await patch.asked;
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  const seen = writes(page);
  await patch.release();
  await settle(page);
  expect(seen).toEqual([]);
});

test("A P2-1 an edit panel save refused after sign out reads nothing and opens no dialog under the next person's session", async ({ page }) => {
  await signIn(page, "Sales");
  await row(page, "Q-1001").click();
  await openDetail(page);
  await expect(page.getByTestId("qe-title")).toContainText("Q-1001");
  const line = page.locator("qd-quote-edit").getByTestId("qe-line").first();
  await line.locator('[data-field="disc"]').fill("10");
  // The server's own answer to this discount is the typed floor refusal; it is held until after the next person is in.
  const patch = await hold(page, /\/quotes\/q-1001\/lines\/1$/, { method: "PATCH", times: 1 });
  await line.getByRole("button", { name: "Save" }).click();
  await patch.asked;
  // The next person is another role: the same role would be resumed on the quote it had open.
  await signOutAndInAs(page, "Sales manager");
  const touched: string[] = [];
  page.on("request", (request) => {
    if (/\/api\/v1\/quotes\/q-1001/.test(request.url())) touched.push(`${request.method()} ${request.url()}`);
  });
  await patch.release();
  await settle(page);
  expect(touched).toEqual([]);
  await expect(page.getByTestId("fr-reason")).toHaveCount(0);
});

test("A P2-1 a panel that has left the page reads nothing when it is asked to reload", async ({ page }) => {
  await signIn(page, "Sales manager");
  await row(page, "Q-1001").click();
  await openDetail(page);
  const tags = ["qd-quote-edit", "qd-quote-history", "qd-quote-assist", "qd-quote-followups", "qd-quote-hold-expiry", "qd-quote-availability"];
  for (const tag of tags) await expect(page.locator(tag)).toHaveCount(1);
  await quiet(page);
  const requests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/api/")) requests.push(`${request.method()} ${request.url()}`);
  });
  // Each panel is taken off the page with its last quote id still set and asked to re-read, as the builder's fan-out would.
  for (const tag of tags) {
    await page.locator(tag).evaluate((el) => {
      el.remove();
      (el as HTMLElement & { reload: () => void }).reload();
    });
  }
  await settle(page);
  expect(requests).toEqual([]);
});

test("A P2-1 a work area that has left the page reads nothing when it is asked to", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  await quiet(page);
  const requests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/api/")) requests.push(`${request.method()} ${request.url()}`);
  });
  await page.locator("qd-quote-workarea").evaluate((el) => {
    el.remove();
    void (el as HTMLElement & { loadAll: () => Promise<void> }).loadAll();
  });
  await settle(page);
  expect(requests).toEqual([]);
});
