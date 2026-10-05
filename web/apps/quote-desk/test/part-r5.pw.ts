// Round 5: the principal's two P2s and the folded P3s (decision row 783). Each test holds an answer back behind a
// gate, so the order of events is exact and never depends on the machine's speed.
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
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail &amp; admin").click();
  await expect(page.getByTestId("qe-box")).toBeVisible();
}

async function newQuote(page: Page): Promise<void> {
  await page.getByRole("button", { name: "New Quote" }).click();
  await expect(page.getByTestId("qb-title")).toHaveText("New Quote");
}

async function stubState(page: Page): Promise<{ workspaceWrites: { user_id: string; body: unknown }[] }> {
  const response = await page.request.get("/_stub/state");
  return (await response.json()) as { workspaceWrites: { user_id: string; body: unknown }[] };
}

test("P2-1 a queued layout save is never sent under the next person's session, and their desk resumes their own quote", async ({ page }) => {
  await signIn(page, "Sales");
  await open(page, "Q-1001");
  await quiet(page);
  // The resume reference save of the quote just opened is held on its way out; the next open queues another behind it.
  const first = await hold(page, /\/surfaces\/quotes\.builder\/my-workspace$/, { method: "PUT", times: 1 });
  await open(page, "Q-1003");
  await first.asked;
  await open(page, "Q-1001");
  // Sign out cannot wait for the held save: the bounded wait gives up and the session ends with the save still out.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible({ timeout: 10000 });
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await quiet(page);
  const before = (await stubState(page)).workspaceWrites.filter((w) => w.user_id === "demo-account-sales-manager");
  // The held save answers under the person it was sent for: the queued one behind it must never go out as the manager.
  await first.release();
  await settle(page);
  const after = (await stubState(page)).workspaceWrites.filter((w) => w.user_id === "demo-account-sales-manager");
  expect(after).toEqual(before);
  expect(after).toHaveLength(0);
  // The manager's own desk is what their own row says: Sales' last quote is not resumed for them.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible({ timeout: 10000 });
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await expect(page.getByTestId("qb-context")).not.toContainText("Q-10");
});
