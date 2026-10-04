// PR 121 round 3, Part B findings: the panels. Every test holds an answer back behind a delayed route, so the order
// of events is exact on any machine.
import { expect, test, type Browser, type Page } from "@playwright/test";
import { hold, row, settle, signIn as signInAs, writes } from "./switch-helpers.js";

// The Sales manager sees every quote in the list, so the probes can move between quotes of either owner.
const signIn = (page: Page, role = "Sales manager") => signInAs(page, role);

async function openDetail(page: Page): Promise<void> {
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qe-box")).toBeVisible();
}

async function openQuote(page: Page, quoteNo: string): Promise<void> {
  await row(page, quoteNo).click();
  await openDetail(page);
  await expect(page.getByTestId("qe-title")).toContainText(quoteNo);
}

const edit = (page: Page) => page.locator("qd-quote-edit");
const assist = (page: Page) => page.locator("qd-quote-assist");

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("P2-1 an add that was out when the person left lands, and the note counts it and the line is there on return", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await assist(page).locator("#jobtext").fill("3 yd clear stone, 20 sf holland stone");
  await assist(page).getByRole("button", { name: "Suggest lines from this text" }).click();
  const proposals = assist(page).getByTestId("qa-proposal");
  await expect(proposals).toHaveCount(2);
  for (let i = 0; i < 2; i += 1) await proposals.nth(i).locator('[data-field="tick"]').check();
  const posts = await hold(page, /\/api\/v1\/quotes\/q-1001\/lines$/, { method: "POST", times: 1 });
  const seen = writes(page);
  await assist(page).getByTestId("qa-confirm").click();
  await posts.asked;
  await row(page, "Q-1003").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1003");
  let listReads = 0;
  page.on("request", (r) => {
    if (r.method() === "GET" && /\/api\/v1\/quotes(\?.*)?$/.test(r.url())) listReads += 1;
  });
  const answered = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/quotes/q-1001/lines"));
  posts.open();
  await answered;
  await settle(page);
  // The one add that was out landed: it is counted, and the second line was never sent.
  await expect(assist(page).getByTestId("qa-add-note")).toContainText("1 of the 2 selected lines were added to the quote you left");
  expect(seen.filter((w) => w.startsWith("POST") && w.includes("/lines"))).toHaveLength(1);
  expect(seen.filter((w) => w.includes("/quotes/q-1003/"))).toEqual([]);
  // The list is told, so its figures follow the line that landed.
  expect(listReads).toBeGreaterThan(0);
  await row(page, "Q-1001").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1001");
  await expect(edit(page).getByTestId("qe-line")).toHaveCount(3);
});

async function plantAndOpenAsManager(browser: Browser, page: Page): Promise<Page> {
  for (const lineNo of [1, 2]) await page.request.post("/_stub/plant-approval", { data: { quote_id: "q-1001", line_no: lineNo, disc_pct: 10, requested_by_flow: "sales" } });
  const manager = await (await browser.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
  await signIn(manager, "Sales manager");
  await openQuote(manager, "Q-1001");
  await expect(manager.locator("qd-approvals").getByTestId("ap-row")).toHaveCount(2);
  return manager;
}

test("P2-2 a reason typed for a row whose decision clashed (409) is not filed with the next row's decision", async ({ page, browser }) => {
  const manager = await plantAndOpenAsManager(browser, page);
  const panel = manager.locator("qd-approvals");
  const bodies: string[] = [];
  let first = true;
  await manager.route(/\/api\/v1\/staff\/approvals\/[^/]+\/decide$/, async (route) => {
    if (first) {
      first = false;
      await route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: "identity: another approver decided this request" }) });
      return;
    }
    bodies.push(route.request().postData() ?? "");
    await route.continue();
  });
  await panel.getByTestId("ap-reason").fill("Repeat buyer on row one");
  await panel.getByTestId("ap-approve").first().click();
  await expect(panel.getByTestId("ap-row-error")).toContainText("another approver");
  await panel.getByTestId("ap-approve").nth(1).click();
  await expect.poll(() => bodies.length).toBe(1);
  expect(bodies[0]).not.toContain("Repeat buyer on row one");
  await manager.context().close();
});
