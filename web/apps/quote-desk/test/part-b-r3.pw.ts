// PR 121 round 3, Part B findings: the panels. Every test holds an answer back behind a delayed route, so the order
// of events is exact on any machine.
import { expect, test, type Browser, type Page } from "@playwright/test";
import { hold, row, seed, settle, signIn as signInAs, writes } from "./switch-helpers.js";

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

test("P2-3 a quantity save that lands after leaving and returning to the quote shows on it, and the list follows", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const box = edit(page).getByTestId("qe-line").first().locator('[data-field="qty"]');
  await expect(box).toHaveValue("120");
  const listBefore = await row(page, "Q-1001").innerText();
  const patch = await hold(page, /\/api\/v1\/quotes\/q-1001\/lines\/1$/, { method: "PATCH", times: 1 });
  await box.fill("6");
  await edit(page).getByTestId("qe-line").first().getByRole("button", { name: "Save" }).click();
  await patch.asked;
  await row(page, "Q-1002").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1002");
  await row(page, "Q-1001").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1001");
  await expect(box).toHaveValue("120");
  const answered = page.waitForResponse((r) => r.request().method() === "PATCH" && /\/quotes\/q-1001\/lines\/1$/.test(r.url()));
  patch.open();
  await answered;
  await expect(box).toHaveValue("6");
  await expect.poll(() => row(page, "Q-1001").innerText()).not.toBe(listBefore);
});

test("P2-3 an add that lands after leaving and returning to the quote shows on it", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await edit(page).locator('[data-field="add-search"]').fill("edge");
  await edit(page).getByRole("option").first().click();
  await edit(page).locator('[data-field="add-qty"]').fill("2");
  const post = await hold(page, /\/api\/v1\/quotes\/q-1001\/lines$/, { method: "POST" });
  await edit(page).getByRole("button", { name: /^Add/ }).click();
  await post.asked;
  await row(page, "Q-1002").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1002");
  await row(page, "Q-1001").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1001");
  await expect(edit(page).getByTestId("qe-line")).toHaveCount(2);
  post.open();
  await expect(edit(page).getByTestId("qe-line")).toHaveCount(3);
  await expect(page.getByTestId("qb-line")).toHaveCount(3);
});

test("P2-3 a link minted after leaving and returning to the quote shows in its link list", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await expect(edit(page).getByTestId("qe-link")).toHaveCount(0);
  const post = await hold(page, /\/api\/v1\/quotes\/q-1001\/share-links$/, { method: "POST" });
  await edit(page).getByRole("button", { name: "Create link to copy" }).click();
  await post.asked;
  await row(page, "Q-1002").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1002");
  await row(page, "Q-1001").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1001");
  post.open();
  await expect(edit(page).getByTestId("qe-link")).toHaveCount(1);
});

test("P2-3 a conversion that lands after leaving and returning to the quote shows the quote converted", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1003");
  const post = await hold(page, /\/api\/v1\/quotes\/q-1003\/convert$/, { method: "POST" });
  await edit(page).getByTestId("qe-convert").click();
  await post.asked;
  await row(page, "Q-1002").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1002");
  await row(page, "Q-1003").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1003");
  post.open();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Converted");
});

test("P3 a floor refusal answered after the person returned to the quote is shown on it", async ({ page }) => {
  await signIn(page, "Sales");
  await openQuote(page, "Q-1001");
  const patch = await hold(page, /\/api\/v1\/quotes\/q-1001\/lines\/1$/, { method: "PATCH", times: 1 });
  const paver = edit(page).getByTestId("qe-line").first();
  await paver.locator('[data-field="disc"]').fill("10");
  await paver.getByRole("button", { name: "Save" }).click();
  await patch.asked;
  await row(page, "Q-1003").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1003");
  await row(page, "Q-1001").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1001");
  patch.open();
  await expect(page.getByTestId("qe-error")).toContainText("puts the line below the margin floor");
});

test("P3 a refusal answered while the person is on another quote is not shown there", async ({ page }) => {
  await signIn(page, "Sales");
  await openQuote(page, "Q-1001");
  const patch = await hold(page, /\/api\/v1\/quotes\/q-1001\/lines\/1$/, { method: "PATCH", times: 1 });
  const paver = edit(page).getByTestId("qe-line").first();
  await paver.locator('[data-field="disc"]').fill("10");
  await paver.getByRole("button", { name: "Save" }).click();
  await patch.asked;
  await row(page, "Q-1003").click();
  await expect(edit(page).getByTestId("qe-title")).toContainText("Q-1003");
  patch.open();
  await settle(page);
  await expect(page.getByTestId("qe-error")).toHaveText("");
});

test("P3 saving one line keeps the unsaved figures typed on the other lines", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  const lines = edit(page).getByTestId("qe-line");
  await lines.nth(1).locator('[data-field="qty"]').fill("9");
  await lines.first().locator('[data-field="qty"]').fill("6");
  await lines.first().getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-notice")).toHaveText("Saved.");
  await expect(lines.first().locator('[data-field="qty"]')).toHaveValue("6");
  await expect(lines.nth(1).locator('[data-field="qty"]')).toHaveValue("9");
});

test("P3 the fulfilment chosen for one conversion is not preselected on the next quote", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick", status: "ACCEPTED" });
  await signIn(page);
  await openQuote(page, "Q-1003");
  await edit(page).getByTestId("qe-fulfilment").selectOption("WILL_CALL");
  await openQuote(page, "Q-1004");
  await expect(edit(page).getByTestId("qe-fulfilment")).toHaveValue("");
});

async function openScreenSettings(page: Page): Promise<void> {
  await page.getByText("Quote detail & admin").click();
  await page.locator("qd-quote-settings").getByTestId("qs-screen").locator("summary").click();
  await expect(page.getByTestId("qs-policy")).toBeVisible();
}

async function signInAsGeneralManager(page: Page): Promise<void> {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  await signIn(page);
  await openScreenSettings(page);
}

test("P3 the policy form is held while its save is out, so nothing typed meanwhile can be lost", async ({ page }) => {
  await signInAsGeneralManager(page);
  await page.getByTestId("qs-validity-days").fill("55");
  const put = await hold(page, /\/api\/v1\/quotes\/policy/, { method: "PUT" });
  await page.getByTestId("qs-policy-save").click();
  await put.asked;
  await expect(page.getByTestId("qs-validity-days")).toBeDisabled();
  put.open();
  await expect(page.getByTestId("qs-notice")).toContainText("Quote settings saved");
  await expect(page.getByTestId("qs-validity-days")).toBeEnabled();
  await expect(page.getByTestId("qs-validity-days")).toHaveValue("55");
  await page.request.put("/_stub/general-manager", { data: { member: false } });
});

test("P3 the screen layout is held while its save is out, so the answer's re-read cannot wipe a change made meanwhile", async ({ page }) => {
  await signInAsGeneralManager(page);
  const put = await hold(page, /\/api\/v1\/surfaces\/[^/]+\/items$/, { method: "PUT" });
  await page.getByTestId("qs-items-save").click();
  await put.asked;
  await expect(page.getByTestId("qs-items").getByLabel("Show Photo")).toBeDisabled();
  put.open();
  await expect(page.getByTestId("qs-notice")).toContainText("Screen layout saved");
  await expect(page.getByTestId("qs-items").getByLabel("Show Photo")).toBeEnabled();
  await page.request.put("/_stub/general-manager", { data: { member: false } });
});

test("P3 a policy save that succeeds after the scope moved on tells the other screens", async ({ page }) => {
  await signInAsGeneralManager(page);
  await page.getByTestId("qs-validity-days").fill("55");
  const put = await hold(page, /\/api\/v1\/quotes\/policy/, { method: "PUT" });
  await page.getByTestId("qs-policy-save").click();
  await put.asked;
  await page.getByTestId("qs-scope").selectOption("dealer");
  await page.evaluate(() => {
    (window as unknown as { __changed: number }).__changed = 0;
    window.addEventListener("quote-changed", () => ((window as unknown as { __changed: number }).__changed += 1), true);
  });
  put.open();
  await settle(page);
  expect(await page.evaluate(() => (window as unknown as { __changed: number }).__changed)).toBeGreaterThan(0);
  await page.request.put("/_stub/general-manager", { data: { member: false } });
});

test("P3 a policy save refused after the scope moved on says which settings were not saved", async ({ page }) => {
  await signInAsGeneralManager(page);
  await page.getByTestId("qs-validity-days").fill("55");
  const put = await hold(page, /\/api\/v1\/quotes\/policy/, { method: "PUT", refuse: { status: 500, error: "quote: the policy store is down" } });
  await page.getByTestId("qs-policy-save").click();
  await put.asked;
  await page.getByTestId("qs-scope").selectOption("dealer");
  put.open();
  await expect(page.getByTestId("qs-error")).toContainText("were not saved");
  await expect(page.getByTestId("qs-error")).toContainText("branch you left");
  await page.request.put("/_stub/general-manager", { data: { member: false } });
});
