// PR 121 round 4 findings: each test holds one answer back behind a delayed route, so the order of events is exact
// and does not depend on the machine's speed.
import { expect, test, type Page } from "@playwright/test";
import { gate, hold, open, quiet, row, seed, settle, signIn, stubBrowserChannels, writes } from "./switch-helpers.js";

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

test("A P2-2 a terms save slower than the wait bound shows its edit when it lands, and the next edit does not erase it", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const seen = writes(page);
  const put = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT", times: 1 });
  await page.getByTestId("qb-po").fill("PO-SLOW-FLUSH");
  await row(page, "Q-1001").click();
  await put.asked;
  await row(page, "Q-1004").click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1004");
  // Past the bound: the read went ahead without the save and says so.
  await expect(page.getByTestId("qb-error")).toContainText("still being saved", { timeout: 10000 });
  await put.release();
  // The save has landed: what was typed is what the box shows, and the line that promised it no longer stands.
  await expect(page.getByTestId("qb-po")).toHaveValue("PO-SLOW-FLUSH");
  await expect(page.getByTestId("qb-error")).toHaveText("");
  await page.getByTestId("qb-contact").fill("Site contact");
  await expect.poll(() => seen.filter((w) => w.startsWith("PUT") && w.includes("Site contact")).length).toBeGreaterThan(0);
  const last = seen.filter((w) => w.startsWith("PUT") && w.includes("Site contact")).pop() ?? "";
  expect(last).toContain('"customer_po":"PO-SLOW-FLUSH"');
});

test("A P2-2 a slow terms save that lands after the person has left the quote again reads nothing", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const put = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT", times: 1 });
  await page.getByTestId("qb-po").fill("PO-SLOW-FLUSH");
  await row(page, "Q-1001").click();
  await put.asked;
  await row(page, "Q-1004").click();
  await expect(page.getByTestId("qb-error")).toContainText("still being saved", { timeout: 10000 });
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await quiet(page);
  const reads: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "GET" && /\/api\/v1\/quotes\/q-100\d/.test(request.url())) reads.push(request.url());
  });
  await put.release();
  await settle(page);
  expect(reads).toEqual([]);
  await expect(page.getByTestId("qb-error")).toHaveText("");
});

const followups = (page: Page) => page.locator("qd-quote-followups");
const holdExpiry = (page: Page) => page.locator("qd-quote-hold-expiry");

async function openQuote(page: Page, quoteNo: string): Promise<void> {
  await row(page, quoteNo).click();
  await openDetail(page);
  await expect(page.getByTestId("qe-title")).toContainText(quoteNo);
}

test("B P2-1 a follow-ups stop that lands after leaving and returning shows on return", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1002");
  await expect(followups(page).getByTestId("fu-summary")).toContainText("Follow-ups are on");
  const disable = await hold(page, /\/quotes\/q-1002\/followups\/disable$/, { method: "POST", times: 1 });
  await followups(page).getByRole("button", { name: "Stop follow-ups" }).click();
  await disable.asked;
  await openQuote(page, "Q-1001");
  await openQuote(page, "Q-1002");
  await expect(followups(page).getByTestId("fu-summary")).toContainText("Follow-ups are on");
  await disable.release();
  await expect(followups(page).getByTestId("fu-summary")).toContainText("Follow-ups are OFF");
});

test("B P2-1 a follow-ups restart that lands after leaving and returning shows on return", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1002");
  await followups(page).getByRole("button", { name: "Stop follow-ups" }).click();
  await expect(followups(page).getByTestId("fu-summary")).toContainText("Follow-ups are OFF");
  const enable = await hold(page, /\/quotes\/q-1002\/followups\/enable$/, { method: "POST", times: 1 });
  await followups(page).getByRole("button", { name: "Restart follow-ups" }).click();
  await enable.asked;
  await openQuote(page, "Q-1001");
  await openQuote(page, "Q-1002");
  await expect(followups(page).getByTestId("fu-summary")).toContainText("Follow-ups are OFF");
  await enable.release();
  await expect(followups(page).getByTestId("fu-summary")).toContainText("Follow-ups are on");
});

test("B P2-1 a text share that lands after leaving and returning makes the quote re-read", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1002");
  const door = gate();
  await page.route(/\/api\/v1\/quotes\/q-1002\/sms-share$/, async (route) => {
    await door.opened;
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ queued: true }) });
  });
  const asked = page.waitForRequest((r) => r.url().endsWith("/quotes/q-1002/sms-share"));
  await followups(page).getByRole("button", { name: "Text the quote link" }).click();
  await asked;
  await openQuote(page, "Q-1001");
  await openQuote(page, "Q-1002");
  await quiet(page);
  const reads: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "GET" && /\/api\/v1\/quotes\/q-1002\/followups$/.test(request.url())) reads.push(request.url());
  });
  const answered = page.waitForResponse((r) => r.url().endsWith("/quotes/q-1002/sms-share"));
  door.open();
  await answered;
  await settle(page);
  expect(reads.length).toBeGreaterThan(0);
});

test("B P2-1 a re-hold that lands after leaving and returning shows on return", async ({ page, request }) => {
  await request.post("/_stub/hold-lapse", { data: { quote_id: "q-1001", line_no: 2 } });
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1001");
  await expect(holdExpiry(page).getByTestId("he-headline")).toBeVisible();
  const resync = await hold(page, /\/quotes\/q-1001\/holds\/resync$/, { method: "POST", times: 1 });
  await holdExpiry(page).getByRole("button", { name: "Re-hold these lines" }).click();
  await resync.asked;
  await openQuote(page, "Q-1003");
  await openQuote(page, "Q-1001");
  await expect(holdExpiry(page).getByTestId("he-headline")).toBeVisible();
  await resync.release();
  await expect(holdExpiry(page).getByTestId("he-headline")).toHaveCount(0);
});

test("B P2-1 a follow-ups stop refused after leaving and returning is said on return", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1002");
  const disable = await hold(page, /\/quotes\/q-1002\/followups\/disable$/, { method: "POST", times: 1, refuse: { status: 409, error: "quote: the cadence is locked" } });
  await followups(page).getByRole("button", { name: "Stop follow-ups" }).click();
  await disable.asked;
  await openQuote(page, "Q-1001");
  await openQuote(page, "Q-1002");
  await expect(followups(page).getByTestId("fu-error")).toHaveCount(0);
  await disable.release();
  await expect(followups(page).getByTestId("fu-error")).toBeVisible();
});

test("B P2-1 a follow-ups stop refused while the person is on another quote says nothing there", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1002");
  const disable = await hold(page, /\/quotes\/q-1002\/followups\/disable$/, { method: "POST", times: 1, refuse: { status: 409, error: "quote: the cadence is locked" } });
  await followups(page).getByRole("button", { name: "Stop follow-ups" }).click();
  await disable.asked;
  await openQuote(page, "Q-1001");
  await disable.release();
  await settle(page);
  await expect(followups(page).getByTestId("fu-error")).toHaveCount(0);
});

// The work area's own writes: each, answered after the person left the quote, still tells the screens that quote changed.
interface LateCase {
  name: string;
  seed?: Record<string, unknown>;
  url: RegExp;
  method: string;
  fulfilOk?: Record<string, unknown>;
  trigger: (page: Page) => Promise<void>;
}

const LATE_CASES: LateCase[] = [
  {
    name: "an add",
    url: /\/quotes\/q-1004\/lines$/,
    method: "POST",
    trigger: async (page) => {
      const scan = page.getByTestId("pos-scan-input");
      await scan.fill("PSN-100");
      await scan.press("Enter");
    },
  },
  {
    name: "a quantity change",
    url: /\/quotes\/q-1004\/lines\/1$/,
    method: "PATCH",
    trigger: async (page) => {
      await page.getByRole("button", { name: /^Increase quantity/ }).first().click();
    },
  },
  {
    name: "a discount change",
    url: /\/quotes\/q-1004\/lines\/1$/,
    method: "PATCH",
    trigger: async (page) => {
      const disc = page.getByTestId("qb-line-disc");
      await disc.fill("5");
      await disc.blur();
    },
  },
  {
    name: "a line removal",
    url: /\/quotes\/q-1004\/lines\/1$/,
    method: "DELETE",
    trigger: async (page) => {
      await page.getByTestId("qb-line-trash").first().click();
    },
  },
  {
    name: "an expiry change",
    url: /\/quotes\/q-1004$/,
    method: "PATCH",
    trigger: async (page) => {
      await page.getByTestId("qb-terms-validity").selectOption("60 days");
    },
  },
  {
    name: "a terms save",
    url: /\/quotes\/q-1004\/terms$/,
    method: "PUT",
    trigger: async (page) => {
      await page.getByTestId("qb-po").fill("PO-LATE-ANSWER");
    },
  },
  {
    name: "a conversion",
    seed: { customer_id: "c-fenwick", status: "ACCEPTED" },
    url: /\/quotes\/q-1004\/convert$/,
    method: "POST",
    trigger: async (page) => {
      await page.getByTestId("qb-convert").click();
    },
  },
  {
    name: "a link made to copy",
    url: /\/quotes\/q-1004\/share-links$/,
    method: "POST",
    trigger: async (page) => {
      await page.getByTestId("qb-copy-link").click();
    },
  },
  {
    name: "a text share made by a send",
    url: /\/quotes\/q-1004\/sms-share$/,
    method: "POST",
    fulfilOk: { queued: true },
    trigger: async (page) => {
      await page.getByTestId("qb-send").click();
    },
  },
];

async function lateCase(page: Page, c: LateCase): Promise<void> {
  await stubBrowserChannels(page);
  await seed(page, c.seed ?? { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await quiet(page);
  const door = gate();
  let askedNow: () => void = () => undefined;
  const asked = new Promise<void>((resolve) => {
    askedNow = resolve;
  });
  await page.route(c.url, async (route) => {
    if (route.request().method() !== c.method || c.fulfilOk === undefined) {
      if (route.request().method() === c.method) {
        askedNow();
        await door.opened;
      }
      await route.continue();
      return;
    }
    askedNow();
    await door.opened;
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(c.fulfilOk) });
  });
  await c.trigger(page);
  await asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await page.evaluate(() => {
    const w = window as unknown as { __changedFor: string[] };
    w.__changedFor = [];
    window.addEventListener("quote-changed", (event) => w.__changedFor.push((event as CustomEvent<{ quoteId: string }>).detail.quoteId), true);
  });
  door.open();
  await expect.poll(() => page.evaluate(() => (window as unknown as { __changedFor: string[] }).__changedFor)).toContain("q-1004");
}

test("B P2-1 work area: an add answered after leaving the quote tells the screens that quote changed", async ({ page }) => {
  await lateCase(page, LATE_CASES[0] as LateCase);
});

test("B P2-1 work area: a quantity change answered after leaving the quote tells the screens that quote changed", async ({ page }) => {
  await lateCase(page, LATE_CASES[1] as LateCase);
});

test("B P2-1 work area: a discount change answered after leaving the quote tells the screens that quote changed", async ({ page }) => {
  await lateCase(page, LATE_CASES[2] as LateCase);
});

test("B P2-1 work area: a line removal answered after leaving the quote tells the screens that quote changed", async ({ page }) => {
  await lateCase(page, LATE_CASES[3] as LateCase);
});

test("B P2-1 work area: an expiry change answered after leaving the quote tells the screens that quote changed", async ({ page }) => {
  await lateCase(page, LATE_CASES[4] as LateCase);
});

test("B P2-1 work area: a terms save answered after leaving the quote tells the screens that quote changed", async ({ page }) => {
  await lateCase(page, LATE_CASES[5] as LateCase);
});

test("B P2-1 work area: a conversion answered after leaving the quote tells the screens that quote changed", async ({ page }) => {
  await lateCase(page, LATE_CASES[6] as LateCase);
});

test("B P2-1 work area: a link made to copy answered after leaving the quote tells the screens that quote changed", async ({ page }) => {
  await lateCase(page, LATE_CASES[7] as LateCase);
});

test("B P2-1 work area: a text share made by a send answered after leaving the quote tells the screens that quote changed", async ({ page }) => {
  await lateCase(page, LATE_CASES[8] as LateCase);
});

test("B P2-1 work area: a quantity change refused after leaving and returning is said on return", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  const held = await hold(page, /\/quotes\/q-1004\/lines\/1$/, { method: "PATCH", times: 1, refuse: { status: 500, error: "quote: the line store is down" } });
  await page.getByRole("button", { name: /^Increase quantity/ }).first().click();
  await held.asked;
  await open(page, "Q-1001");
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-error")).toHaveText("");
  await held.release();
  await expect(page.getByTestId("qb-error")).not.toHaveText("");
});

test("P3 a draft typed on a line that is gone is dropped, so it does not land on the new line that takes its number", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1001");
  const lines = page.locator("qd-quote-edit").getByTestId("qe-line");
  await expect(lines).toHaveCount(2);
  await lines.nth(1).locator('[data-field="qty"]').fill("9");
  await page.getByTestId("qb-line-trash").nth(1).click();
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await expect(lines).toHaveCount(1);
  const scan = page.getByTestId("pos-scan-input");
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await expect(lines).toHaveCount(2);
  await expect(lines.nth(1).locator('[data-field="qty"]')).toHaveValue("1");
});

test("P3 an unsaved expiry survives a re-read caused by another save", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1001");
  const panel = page.locator("qd-quote-edit");
  await panel.locator('[data-field="expiry"]').fill("2031-06-15");
  const first = panel.getByTestId("qe-line").first();
  await first.locator('[data-field="qty"]').fill("6");
  await first.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-notice")).toHaveText("Saved.");
  await expect(panel.locator('[data-field="expiry"]')).toHaveValue("2031-06-15");
});

test("P3 a refused expiry save returns the box to the server's date", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1001");
  const panel = page.locator("qd-quote-edit");
  const box = panel.locator('[data-field="expiry"]');
  const served = await box.inputValue();
  await hold(page, /\/quotes\/q-1001$/, { method: "PATCH", refuse: { status: 500, error: "quote: the expiry store is down" } }).then((held) => held.open());
  await box.fill("2031-06-15");
  await panel.getByRole("button", { name: "Save expiry" }).click();
  await expect(page.getByTestId("qe-error")).not.toHaveText("");
  await expect(box).toHaveValue(served);
});

test("P3 compare answers within one quote land in the order they were asked", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1001");
  const panel = page.locator("qd-quote-edit");
  const first = await hold(page, /\/quotes\/q-1001\/lines\/1\/compare$/, { method: "POST", times: 1 });
  await panel.getByTestId("qe-line").nth(0).getByRole("button", { name: "Compare" }).click();
  await first.asked;
  await panel.getByTestId("qe-line").nth(1).getByRole("button", { name: "Compare" }).click();
  const box = panel.getByTestId("qe-compare");
  await expect(box.locator("legend")).toHaveText("Compare line 2");
  await expect(box.locator("tbody tr")).not.toHaveCount(0);
  const answered = await box.locator("tbody").innerText();
  await first.release();
  await settle(page);
  await expect(box.locator("legend")).toHaveText("Compare line 2");
  expect(await box.locator("tbody").innerText()).toBe(answered);
});

/** Ask the builder to re-read every panel, as any quote edit does. */
async function fanOut(page: Page): Promise<void> {
  await page.locator("qd-quote-settings").evaluate((el) => el.dispatchEvent(new CustomEvent("quote-changed", { bubbles: true, composed: true })));
}

test("P3 a read of the acceptance switch begun before a flip does not override the flip", async ({ page }) => {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  try {
    await signIn(page, "Sales manager");
    await page.getByText("Quote detail & admin").click();
    const settings = page.locator("qd-quote-settings");
    await settings.getByTestId("qa-details").locator("summary").click();
    await expect(page.getByTestId("qa-switch")).toBeVisible();
    await expect(page.getByTestId("qa-switch")).not.toBeChecked();
    const read = await hold(page, /\/quotes\/settings\/acceptance$/, { method: "GET", times: 1, snapshot: true });
    await fanOut(page);
    await read.asked;
    await page.getByTestId("qa-switch").check();
    await expect(page.getByTestId("qa-notice")).toContainText("Multiple acceptance is on");
    await read.release();
    await settle(page);
    await expect(page.getByTestId("qa-switch")).toBeChecked();
  } finally {
    await page.request.put("/_stub/general-manager", { data: { member: false } });
  }
});

test("P3 a read of the scope rules begun before a switch does not override the switch", async ({ page }) => {
  await signIn(page, "Sales manager");
  await page.getByText("Quote detail & admin").click();
  await page.locator("qd-quote-settings").getByTestId("qr-details").locator("summary").click();
  await expect(page.getByTestId("qr-row")).toHaveCount(7);
  const row = page.getByTestId("qr-row").filter({ hasText: "WALL_NEEDS_BASE" });
  const read = await hold(page, /\/quote-assist\/scope-rules$/, { method: "GET", times: 1, snapshot: true });
  await fanOut(page);
  await read.asked;
  await row.getByRole("button").click();
  await expect(row).toHaveAttribute("data-enabled", "false");
  await read.release();
  await settle(page);
  await expect(row).toHaveAttribute("data-enabled", "false");
});

test("P3 no empty lock box shows while a policy save is out", async ({ page }) => {
  await page.request.put("/_stub/general-manager", { data: { member: true } });
  try {
    await signIn(page, "Sales manager");
    await page.getByText("Quote detail & admin").click();
    await page.locator("qd-quote-settings").getByTestId("qs-screen").locator("summary").click();
    await expect(page.getByTestId("qs-policy")).toBeVisible();
    await page.getByTestId("qs-validity-days").fill("55");
    const put = await hold(page, /\/api\/v1\/quotes\/policy/, { method: "PUT", times: 1 });
    await page.getByTestId("qs-policy-save").click();
    await put.asked;
    await expect(page.getByTestId("qs-items-save")).toBeDisabled();
    await expect(page.getByTestId("qs-items-lock")).toHaveCount(0);
    await put.release();
  } finally {
    await page.request.put("/_stub/general-manager", { data: { member: false } });
  }
});

test("P3 layout saves reach the server one at a time, so the resume reference is the last quote opened", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  await quiet(page);
  const sent: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "PUT" && request.url().endsWith("/my-workspace")) sent.push(request.postData() ?? "");
  });
  const first = await hold(page, /\/surfaces\/quotes\.builder\/my-workspace$/, { method: "PUT", times: 1 });
  await open(page, "Q-1003");
  await first.asked;
  await open(page, "Q-1001");
  await open(page, "Q-1003");
  // The first save is still out: no second one is sent beside it.
  await settle(page);
  expect(sent).toHaveLength(1);
  await first.release();
  await expect.poll(() => sent.length).toBeGreaterThan(1);
  await quiet(page);
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1003");
});
