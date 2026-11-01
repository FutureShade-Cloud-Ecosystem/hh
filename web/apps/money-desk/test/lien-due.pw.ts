// The Money Desk's Due to release screens (RT-3b), walked in a real browser on the stub: the list per customer job
// with its state in words, the Controller's release (asked first, the core's own sentence when it refuses), its
// reversal with a reason, the lien doors (a preserved lien, its discharge, the three dates), and the old job level
// press saying where the holdback is released now.
import type { APIRequestContext, Page } from "@playwright/test";
import { test, expect, openLiens, liensTab, noSidewaysScroll, CONTROLLER_ID, JOB6, dayFrom, nb, shown } from "./lien-helpers.js";

const due = (page: Page, lien: string) => page.locator(`[data-row="due"][data-lien-job="${lien}"]:visible`);

function token(sub: string): string {
  const part = (v: unknown) => Buffer.from(JSON.stringify(v), "utf8").toString("base64url");
  return `${part({ alg: "ES256" })}.${part({ sub, exp: 4102444800 })}.c3R1Yg`;
}

/** A call made behind the screen's back, as another person at another desk would make it. */
async function behind(request: APIRequestContext, method: "POST" | "PUT", path: string, data: unknown): Promise<void> {
  const res = await request.fetch(path, { method, data, headers: { Authorization: `Bearer ${token(CONTROLLER_ID)}` } });
  expect(res.ok(), `${method} ${path} answered ${res.status()}`).toBe(true);
}

async function openDue(page: Page, request: APIRequestContext, who: "controller" | "general_manager" = "controller"): Promise<void> {
  await openLiens(page, request, who);
  await liensTab(page, "due");
  await expect(page.locator("[data-row='due']:visible").first()).toBeVisible();
}

test("item 1: due to release lists each customer job with its invoices, its holdback, the governing lien job, the last day and the state in words", async ({ page, request }) => {
  await openDue(page, request);
  await expect(page.locator("[data-row='due']:visible")).toHaveCount(3);
  const ready = due(page, "GLS-2105");
  await expect(ready).toContainText("Due now");
  await expect(ready).toContainText("INV-4101");
  await expect(ready).toContainText("$6,300.00");
  await expect(ready).toContainText("INV-4102");
  await expect(ready).toContainText("$7,500.00");
  await expect(ready.locator("[data-last-day]")).not.toHaveText("");
  const waiting = due(page, "GLS-2103");
  await expect(waiting).toContainText(/Not due until/);
  await expect(waiting).toContainText(nb("The holdback is not due: lien job GLS-2103 counts 45 days from"));
  await expect(waiting.getByRole("button", { name: "Release" })).toHaveCount(0);
  const none = due(page, "");
  await expect(none).toContainText("No lien job linked");
  await expect(none).toContainText("No lien job is linked to this job, so there is no preservation period to count.");
  // The server's own total of what is due, by branch.
  await expect(page.locator("[data-due-total]")).toContainText("$13,800.00");
  await expect(page.locator("[data-tab-id='due'] .li-count")).toHaveText("1");
});

test("item 2: the Controller is asked first, naming the job, the invoices and each amount; the release names its rows; the General Manager is shown no button", async ({ page, request }) => {
  await openDue(page, request);
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  const panel = page.locator("[data-due-panel]");
  await expect(panel).toBeFocused();
  await expect(panel.locator("[data-confirm-words]")).toContainText(nb("lien job GLS-2105"));
  // The total, the invoice count and the customer job, as well as each invoice (RT-3b fix round, finding 3).
  await expect(panel.locator("[data-confirm-words]")).toContainText("Release $13,800.00 held on 2 invoices for ");
  await expect(panel.locator("[data-confirm-words]")).toContainText("(demo)");
  await expect(panel.locator("[data-confirm-words]")).toContainText(nb("INV-4101 ($6,300.00)"));
  await expect(panel.locator("[data-confirm-words]")).toContainText(nb("INV-4102 ($7,500.00)"));
  await panel.getByRole("button", { name: "Cancel" }).click();
  await expect(page.locator("[data-due-panel]")).toHaveCount(0);
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Released $13,800.00 on lien job GLS-2105: 2 invoices."));
  await expect(due(page, "GLS-2105")).toHaveCount(0);
  // One press is one card: the job, each invoice with its amount, and the core's total.
  const rows = page.locator("[data-row='released']:visible");
  await expect(rows).toHaveCount(1);
  await expect(rows.nth(0)).toContainText("GLS-2105");
  await expect(rows.nth(0)).toContainText("INV-4101");
  await expect(rows.nth(0)).toContainText("$6,300.00");
  await expect(rows.nth(0)).toContainText("INV-4102");
  await expect(rows.nth(0).locator("[data-press-total]")).toContainText("$13,800.00");
  await noSidewaysScroll(page);
});

test("item 2: what the core refuses is said in the core's sentence, and the list is read again", async ({ page, request }) => {
  await openDue(page, request);
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  await behind(request, "POST", `/api/v1/lien/jobs/${JOB6}/preserved-lien`, { preserved_on: dayFrom(-1), note: "filed while the screen was open" });
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-error]")).toContainText(nb(`A lien was preserved on lien job GLS-2105 on ${shown(dayFrom(-1))} and has not been discharged, so the holdback stays held until it is discharged or settled.`));
  await expect(due(page, "GLS-2105")).toContainText(nb("Blocked by a preserved lien on GLS-2105"));
  await expect(due(page, "GLS-2105").getByRole("button", { name: "Release" })).toHaveCount(0);
});

test("item 2: the General Manager reads the list but is shown no Release button, and says who releases", async ({ page, request }) => {
  await openDue(page, request, "general_manager");
  await expect(due(page, "GLS-2105")).toContainText("Due now");
  await expect(due(page, "GLS-2105").getByRole("button", { name: "Release" })).toHaveCount(0);
  await expect(page.locator("[data-due-note]")).toContainText("Releasing a due holdback is for the Financial Controller.");
});

test("item 3: a release is reversed whole, with one reason, asked first; a release reversed elsewhere is said to be reversed already", async ({ page, request }) => {
  await openDue(page, request);
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  const press = page.locator("[data-row='released']:visible");
  await expect(press).toHaveCount(1);
  await press.getByRole("button", { name: "Reverse" }).click();
  const panel = page.locator("[data-reverse-panel]");
  await expect(panel).toContainText(nb("lien job GLS-2105"));
  await expect(panel).toContainText(nb("INV-4101 ($6,300.00)"));
  await expect(panel).toContainText(nb("INV-4102 ($7,500.00)"));
  // The error sits beside the reason, and the reason is marked invalid.
  await panel.getByRole("button", { name: "Reverse release" }).click();
  await expect(panel.locator("[data-error]")).toContainText("Say why the release is reversed.");
  await expect(panel.getByLabel("Reason")).toHaveAttribute("aria-invalid", "true");
  await expect(page.locator("[data-liens] > [data-error], [data-liens] > p[data-error]")).toHaveCount(0);
  // One reversal is already made at another desk; the press still ends with both back on hold.
  const first = (await press.locator("li[data-invoice-no='INV-4101']").getAttribute("data-release-id")) ?? "";
  await behind(request, "POST", `/api/v1/ar/holdback/releases/${first}/reversal`, { reason: "reversed at the next desk" });
  await panel.getByLabel("Reason").fill("Released against the wrong job");
  await panel.getByRole("button", { name: "Reverse release" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Reversed the release on lien job GLS-2105: INV-4101 ($6,300.00) and INV-4102 ($7,500.00) are held back again."));
  await expect(press).toContainText("Reversed");
  await expect(press.getByRole("button", { name: "Reverse" })).toHaveCount(0);
  // Both invoices are held again, so the job is due once more with both.
  await expect(due(page, "GLS-2105")).toContainText("INV-4101");
  await expect(due(page, "GLS-2105")).toContainText("INV-4102");
});

test("item 3: a release made again after one invoice was reversed elsewhere moves only what is held", async ({ page, request }) => {
  await openDue(page, request);
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  const first = (await page.locator("[data-row='released']:visible li[data-invoice-no='INV-4101']").getAttribute("data-release-id")) ?? "";
  await behind(request, "POST", `/api/v1/ar/holdback/releases/${first}/reversal`, { reason: "reversed at the next desk" });
  // The list is read again when it is opened: one invoice is held and the other is not.
  await liensTab(page, "jobs");
  await liensTab(page, "due");
  const job = due(page, "GLS-2105");
  await expect(job).toContainText("INV-4101");
  await expect(job).not.toContainText("INV-4102");
  await job.getByRole("button", { name: "Release" }).click();
  await expect(page.locator("[data-due-panel] [data-confirm-words]")).toContainText("held on 1 invoice for");
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Released $6,300.00 on lien job GLS-2105: 1 invoice."));
});

test("fix 2 and 3 at 390: the confirm, the success, the error and the opened job are brought into view, and the targets are 44 high", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openDue(page, request);
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  await expect(page.locator("[data-due-panel]")).toBeInViewport();
  await expect(page.locator("[data-due-panel]")).toBeFocused();
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-notice]")).toBeInViewport();
  await page.locator("[data-row='released']:visible").getByRole("button", { name: "Reverse" }).click();
  await expect(page.locator("[data-reverse-panel]")).toBeInViewport();
  await page.locator("[data-reverse-panel]").getByRole("button", { name: "Reverse release" }).click();
  await expect(page.locator("[data-reverse-panel] [data-error]")).toBeInViewport();
  await liensTab(page, "jobs");
  const open = page.locator("[data-row='job'][data-job-no='GLS-2105']:visible").getByRole("button", { name: "Open" });
  expect((await open.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(44);
  await open.click();
  await expect(page.locator("[data-job-panel]")).toBeInViewport();
  // Date boxes sit in one column, one width.
  const boxes = page.locator("[data-doors] input[type='date']");
  const widths = new Set<number>();
  const lefts = new Set<number>();
  for (let i = 0; i < (await boxes.count()); i += 1) {
    const box = await boxes.nth(i).boundingBox();
    widths.add(Math.round(box?.width ?? 0));
    lefts.add(Math.round(box?.x ?? 0));
  }
  expect([...widths].length, "the date boxes have one width").toBe(1);
  expect([...lefts].length, "the date boxes share one left edge").toBe(1);
  await liensTab(page, "holdbacks");
  const release = page.locator("[data-row='aging']:visible").first().getByRole("button", { name: "Release" });
  expect((await release.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(44);
});

test("fix 10 and 12 at 1280: Reverse is an auto width button on the right, and the confirm is one box like the reverse panel", async ({ page, request }) => {
  await openDue(page, request);
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  const statement = page.locator("[data-due-panel] .li-statement");
  await expect(statement).toBeVisible();
  expect(await page.locator("[data-due-panel] .li-confirm").count()).toBe(0);
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  const reverse = page.locator("[data-row='released']:visible").getByRole("button", { name: "Reverse" });
  const card = await page.locator("[data-row='released']:visible").boundingBox();
  const button = await reverse.boundingBox();
  expect(button?.width ?? 9999).toBeLessThan(260);
  expect((button?.x ?? 0) + (button?.width ?? 0)).toBeGreaterThan((card?.x ?? 0) + (card?.width ?? 0) - 40);
});

test("item 4: a preserved lien is recorded and discharged on a lien job, and the list follows", async ({ page, request }) => {
  await openLiens(page, request);
  await liensTab(page, "jobs");
  await page.locator("[data-row='job'][data-job-no='GLS-2105']:visible").getByRole("button", { name: "Open" }).click();
  const doors = page.locator("[data-doors]");
  await doors.getByRole("button", { name: "Record preserved lien" }).click();
  await expect(page.locator("[data-error]")).toContainText("Give the day the lien was preserved, written yyyy-mm-dd.");
  await doors.getByLabel("Lien preserved on").fill(dayFrom(-2));
  await doors.getByLabel("Note", { exact: true }).fill("Filed at the registry");
  await doors.getByRole("button", { name: "Record preserved lien" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Recorded: a lien was preserved on lien job GLS-2105."));
  await expect(doors.locator("[data-lien-state]")).toContainText("A lien was preserved on");
  await expect(doors.locator("[data-lien-state]")).toContainText("Filed at the registry");
  await liensTab(page, "due");
  await expect(due(page, "GLS-2105")).toContainText(nb("Blocked by a preserved lien on GLS-2105"));
  await liensTab(page, "jobs");
  await doors.getByLabel("Discharged on").fill(dayFrom(-5));
  await doors.getByRole("button", { name: "Record discharge" }).click();
  await expect(page.locator("[data-error]")).toContainText("cannot be discharged on");
  await doors.getByLabel("Discharged on").fill(dayFrom(0));
  await doors.getByRole("button", { name: "Record discharge" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Recorded: the lien on lien job GLS-2105 was discharged."));
  await liensTab(page, "due");
  await expect(due(page, "GLS-2105")).toContainText("Due now");
});

test("item 4: the three dates are set, corrected and cleared, and a job with none says it has no date to count from", async ({ page, request }) => {
  await openLiens(page, request);
  await liensTab(page, "jobs");
  await page.locator("[data-row='job'][data-job-no='GLS-2103']:visible").getByRole("button", { name: "Open" }).click();
  const doors = page.locator("[data-doors]");
  await doors.getByRole("button", { name: "Save dates" }).click();
  await expect(page.locator("[data-error]")).toContainText("Change at least one date.");
  await doors.getByLabel("Certificate published on").fill(dayFrom(-3));
  await doors.getByLabel("Substantial performance date").fill("");
  await doors.getByLabel("Last supply date").fill("");
  await doors.getByRole("button", { name: "Save dates" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Saved the dates of lien job GLS-2103."));
  await expect(page.locator("[data-job-panel]")).toContainText("Certificate published");
  await liensTab(page, "due");
  await expect(due(page, "GLS-2103")).toContainText("Not due until");
  await liensTab(page, "jobs");
  await doors.getByLabel("Certificate published on").fill("");
  await doors.getByRole("button", { name: "Save dates" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Saved the dates of lien job GLS-2103."));
  await liensTab(page, "due");
  await expect(due(page, "GLS-2103")).toContainText("No date to count from");
});

test("item 5: the old job level press on a job whose holdback is held on invoices offers only Go to Due to release, with no Confirm", async ({ page, request }) => {
  await openLiens(page, request, "general_manager");
  await liensTab(page, "holdbacks");
  await page.locator("[data-row='aging'][data-job-no='GLS-2105']:visible").getByRole("button", { name: "Release" }).click();
  const panel = page.locator("[data-release-panel]");
  await expect(panel.locator("[data-job-level-refused]")).toContainText("held on its invoices");
  await expect(panel.getByRole("button", { name: "Confirm release" })).toHaveCount(0);
  await expect(panel.getByLabel("Co-signer")).toHaveCount(0);
  await panel.locator("[data-job-level-refused]").getByRole("button", { name: "Go to Due to release" }).click();
  await expect(page.locator("[data-tab-id='due']")).toHaveAttribute("aria-current", "true");
});

test("fix 6 and 5: one date style and no jargon on any lien screen", async ({ page, request }) => {
  await openDue(page, request);
  const text = async () => page.locator("[data-liens]").innerText();
  for (const tab of ["due", "triage", "jobs", "holdbacks"] as const) {
    await liensTab(page, tab);
    const words = await text();
    expect(words, `${tab} shows a day written yyyy-mm-dd`).not.toMatch(/\d{4}-\d{2}-\d{2}/);
    expect(words, `${tab} says the core`).not.toMatch(/the core/i);
  }
  await liensTab(page, "due");
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-released]")).toBeVisible();
  const words = await text();
  expect(words).toContain("Released in this session");
  expect(words).not.toMatch(/the core/i);
});

test("fix 9: a job with a preserved lien is not called Released and not called releasable", async ({ page, request }) => {
  await openDue(page, request);
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-notice]")).toBeVisible();
  await liensTab(page, "jobs");
  await page.locator("[data-row='job'][data-job-no='GLS-2105']:visible").getByRole("button", { name: "Open" }).click();
  await page.locator("[data-doors]").getByLabel("Lien preserved on").fill(dayFrom(-2));
  await page.locator("[data-doors]").getByRole("button", { name: "Record preserved lien" }).click();
  await expect(page.locator("[data-lien-state]")).toContainText("has not been discharged");
  const panel = page.locator("[data-job-panel]");
  await expect(panel.locator(".li-status").first()).toHaveText("Lien preserved");
  await expect(panel).not.toContainText("can be released");
  await expect(page.locator("[data-row='job'][data-job-no='GLS-2105']:visible")).not.toContainText("Released");
});

/** Nothing is wider than the window, no card runs past its right edge, and a job label sits on one line. */
async function fitsNarrow(page: Page, where: string): Promise<void> {
  // The screen sits in a shadow root: locators pierce it, document.querySelector does not.
  const win = await page.evaluate(() => window.innerWidth);
  const doc = await page.evaluate(() => document.documentElement.scrollWidth);
  const over = await page.locator("[data-liens] *").evaluateAll(
    (els, w) =>
      els
        .filter((el) => {
          const box = el.getBoundingClientRect();
          return box.width > 0 && box.right > (w as number) + 0.5 && el.closest(".li-scroll") === null;
        })
        .map((el) => `${el.tagName.toLowerCase()}.${String(el.className)}`),
    win,
  );
  const wrapped = await page.locator("[data-liens] .li-card .li-card-top .li-mono").evaluateAll((els) =>
    els.filter((el) => el.getBoundingClientRect().height > 2 * parseFloat(getComputedStyle(el).fontSize)).map((el) => el.textContent ?? ""),
  );
  expect(doc, `${where}: the page scrolls sideways`).toBeLessThanOrEqual(win);
  expect(over, `${where}: something runs past the right edge`).toEqual([]);
  expect(wrapped, `${where}: a job label wraps`).toEqual([]);
}

test("fix 1: at 390 wide every state fits, the longest pill wraps under the job label and the label stays on one line", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openDue(page, request);
  await fitsNarrow(page, "list");
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  await fitsNarrow(page, "release asks");
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-notice]")).toBeVisible();
  await fitsNarrow(page, "released");
  await page.locator("[data-row='released']:visible").first().getByRole("button", { name: "Reverse" }).click();
  await fitsNarrow(page, "reversing");
  await page.locator("[data-reverse-panel]").getByRole("button", { name: "Reverse release" }).click();
  await expect(page.locator("[data-error]")).toBeVisible();
  await fitsNarrow(page, "reverse needs a reason");
});

test("fix 1: at 390 wide the blocked row, with the longest pill, fits", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openDue(page, request);
  await behind(request, "POST", `/api/v1/lien/jobs/${JOB6}/preserved-lien`, { preserved_on: dayFrom(-1), note: "filed" });
  await liensTab(page, "jobs");
  await liensTab(page, "due");
  await expect(due(page, "GLS-2105").first()).toContainText("Blocked by a preserved lien");
  await fitsNarrow(page, "blocked row");
});

/** The contrast of a text colour against what it sits on, from the browser's own computed colours. */
async function contrasts(page: Page, selector: string): Promise<number[]> {
  return page.locator(selector).evaluateAll((els) => {
    const parse = (value: string): [number, number, number, number] | null => {
      const rgb = /^rgba?\(([^)]+)\)$/.exec(value);
      if (rgb !== null) {
        const [r = 0, g = 0, b = 0, a = 1] = (rgb[1] ?? "").split(/[ ,/]+/).filter(Boolean).map(Number);
        return [r, g, b, a];
      }
      const srgb = /^color\(srgb ([^)]+)\)$/.exec(value);
      if (srgb !== null) {
        const [r = 0, g = 0, b = 0, a = 1] = (srgb[1] ?? "").split(/[ /]+/).filter(Boolean).map(Number);
        return [r * 255, g * 255, b * 255, a];
      }
      return null;
    };
    const lum = ([r, g, b]: number[]): number => {
      const f = (v: number): number => ((v /= 255) <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
      return 0.2126 * f(r ?? 0) + 0.7152 * f(g ?? 0) + 0.0722 * f(b ?? 0);
    };
    const under = (el: Element): [number, number, number] => {
      for (let n: Element | null = el; n !== null; n = n.parentElement ?? ((n.getRootNode() as ShadowRoot).host ?? null)) {
        const c = parse(getComputedStyle(n).backgroundColor);
        if (c !== null && c[3] > 0.99) return [c[0], c[1], c[2]];
      }
      return [255, 255, 255];
    };
    return els.map((el) => {
      const fg = parse(getComputedStyle(el).color) ?? [0, 0, 0, 1];
      const a = lum(fg);
      const b = lum(under(el));
      return Math.round(((Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)) * 100) / 100;
    });
  });
}

test("fix 13: in charcoal the pills and the small helper text read at 4.5 to 1", async ({ page, request }) => {
  await openDue(page, request);
  await page.locator("hh-shell-header button.account").click();
  await page.getByRole("button", { name: "Theme: Light" }).click();
  await liensTab(page, "due");
  for (const selector of ["[data-liens] .li-status:visible", "[data-liens] .li-sub:visible", "[data-liens] .li-dim:visible", "[data-liens] .li-hint:visible", "[data-liens] .li-summary:visible"]) {
    const ratios = await contrasts(page, selector);
    for (const ratio of ratios) expect(ratio, `${selector} reads ${ratio} to 1`).toBeGreaterThanOrEqual(4.5);
  }
});

// ---- the second visual walk's polish ---------------------------------------------------------------------------------

async function pressAndOpenReverse(page: Page): Promise<void> {
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await page.locator("[data-row='released']:visible").getByRole("button", { name: "Reverse" }).click();
}

test("polish: the confirm statement is in the body weight with only the amount and the job in bold", async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await openDue(page, request);
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  const words = page.locator("[data-due-panel] [data-confirm-words]");
  expect(await words.evaluate((el) => getComputedStyle(el).fontWeight)).toBe("400");
  const bold = await words.locator("strong").allTextContents();
  expect(bold.map((t) => t.trim())).toContain(nb("lien job GLS-2105"));
  expect(bold.length).toBeLessThanOrEqual(2);
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await page.locator("[data-row='released']:visible").getByRole("button", { name: "Reverse" }).click();
  const rev = page.locator("[data-reverse-panel] [data-confirm-words]");
  expect(await rev.evaluate((el) => getComputedStyle(el).fontWeight)).toBe("400");
  expect(await rev.locator("strong").allTextContents()).toEqual([nb("lien job GLS-2105")]);
});

test("polish: Reverse release is the rejecting colour, and the reason box shows red when it is invalid", async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await openDue(page, request);
  await pressAndOpenReverse(page);
  const panel = page.locator("[data-reverse-panel]");
  await expect(panel.getByRole("button", { name: "Reverse release" })).toHaveClass(/btn-danger/);
  const reason = panel.getByLabel("Reason");
  const calm = await reason.evaluate((el) => getComputedStyle(el).borderTopColor);
  await panel.getByRole("button", { name: "Reverse release" }).click();
  await expect(reason).toHaveAttribute("aria-invalid", "true");
  const red = await reason.evaluate((el) => getComputedStyle(el).borderTopColor);
  expect(red).not.toBe(calm);
  const stop = await page.evaluate(() => {
    const probe = document.createElement("span");
    probe.style.color = "var(--hh-state-stop)";
    document.body.append(probe);
    const c = getComputedStyle(probe).color;
    probe.remove();
    return c;
  });
  expect(red).toBe(stop);
});

test("polish: a reversed card says reversed in all, and the released card runs the width of the card at 1280", async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await openDue(page, request);
  await due(page, "GLS-2105").getByRole("button", { name: "Release" }).click();
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  const press = page.locator("[data-row='released']:visible");
  await expect(press.locator("[data-press-total]")).toContainText("released in all");
  const card = await press.boundingBox();
  const amount = await press.locator("li[data-invoice-no='INV-4102'] .li-amt").boundingBox();
  const total = await press.locator("[data-press-total]").boundingBox();
  expect(card && amount && total).toBeTruthy();
  expect(card!.x + card!.width - (amount!.x + amount!.width), "the invoice amount sits at the card's right edge").toBeLessThan(40);
  expect(card!.x + card!.width - (total!.x + total!.width), "the total sits at the card's right edge").toBeLessThan(40);
  await press.getByRole("button", { name: "Reverse" }).click();
  await page.locator("[data-reverse-panel]").getByLabel("Reason").fill("Released against the wrong job");
  await page.locator("[data-reverse-panel]").getByRole("button", { name: "Reverse release" }).click();
  await expect(press).toContainText("Reversed");
  await expect(press.locator("[data-press-total]")).toContainText("reversed in all");
  await expect(press.locator("[data-press-total]")).not.toContainText("released in all");
});

test("polish: the Holdbacks tab offers no Release on a row that is not yet due", async ({ page, request }) => {
  await openLiens(page, request, "controller");
  await liensTab(page, "holdbacks");
  const rows = (job: string) => page.locator(`[data-row='aging'][data-job-no='${job}']:visible`);
  await expect(rows("GLS-2103")).toContainText("Not yet");
  await expect(rows("GLS-2103").getByRole("button", { name: "Release" })).toHaveCount(0);
  await expect(rows("GLS-2101")).toContainText("Release due");
  await expect(rows("GLS-2101").getByRole("button", { name: "Release" })).toHaveCount(1);
});

// ---- RT-3b fix round 3: the key on a retry, the key across a reload, the partial reversal, a job over two branches ----

const RELEASES = "**/api/v1/ar/holdback/releases";
const REVERSAL = /\/api\/v1\/ar\/holdback\/releases\/[^/]+\/reversal$/;

/** The idempotency key of every release POST the page makes, in order; `first` decides the answer of the first one. */
async function watchReleases(page: Page, first: "pass" | "abort" | "five-hundred" | "refuse"): Promise<string[]> {
  const keys: string[] = [];
  await page.route(RELEASES, async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    keys.push((route.request().postDataJSON() as { idempotency_key: string }).idempotency_key);
    if (keys.length === 1 && first === "abort") return route.abort();
    if (keys.length === 1 && first === "five-hundred") return route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal error" }) });
    if (keys.length === 1 && first === "refuse") return route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: "the holdback of this job has already been released, and no invoice on it withholds any more" }) });
    return route.continue();
  });
  return keys;
}

async function pressRelease(page: Page): Promise<void> {
  const open = due(page, "GLS-2105").getByRole("button", { name: "Release" });
  if ((await page.locator("[data-due-panel]").count()) === 0) await open.click();
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
}

for (const first of ["abort", "five-hundred"] as const) {
  test(`fix r3: a release with no answer (${first}) is retried with the same key`, async ({ page, request }) => {
    await openDue(page, request);
    const keys = await watchReleases(page, first);
    await pressRelease(page);
    await expect(page.locator("[data-error]").first()).toBeVisible();
    await pressRelease(page);
    await expect(page.locator("[data-notice]")).toContainText(nb("Released $13,800.00 on lien job GLS-2105: 2 invoices."));
    expect(keys).toHaveLength(2);
    expect(keys[1]).toBe(keys[0]);
  });
}

test("fix r3: a refused release (a 4xx) drops its key, so the next press is a new release with a new key", async ({ page, request }) => {
  await openDue(page, request);
  const keys = await watchReleases(page, "refuse");
  await pressRelease(page);
  await expect(page.locator("[data-error]").first()).toContainText("already been released");
  await pressRelease(page);
  await expect(page.locator("[data-notice]")).toContainText(nb("Released $13,800.00 on lien job GLS-2105: 2 invoices."));
  expect(keys).toHaveLength(2);
  expect(keys[1]).not.toBe(keys[0]);
});

test("delta: a release whose answer was lost but whose refresh shows it made drops its key, so a later release is a new press", async ({ page, request }) => {
  await openDue(page, request);
  const keys: string[] = [];
  await page.route(RELEASES, async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    keys.push((route.request().postDataJSON() as { idempotency_key: string }).idempotency_key);
    // The core makes the release; the answer is lost on the way back.
    await route.fetch();
    return route.abort();
  });
  await pressRelease(page);
  await expect(page.locator("[data-error]").first()).toBeVisible();
  // The refresh shows the job no longer due: the outcome is known, so no lien-due key is held any more.
  await expect(due(page, "GLS-2105").getByRole("button", { name: "Release" })).toHaveCount(0);
  const held = await page.evaluate(() =>
    Object.keys(window.sessionStorage)
      .filter((k) => k.includes("lien-due."))
      .map((k) => window.sessionStorage.getItem(k) ?? "")
      .join(""),
  );
  expect(keys).toHaveLength(1);
  expect(held).not.toContain(keys[0]);
});

test("fix r3: the key of a release with no answer survives a reload, held beside the other money presses' keys", async ({ page, request }) => {
  await openDue(page, request);
  const keys = await watchReleases(page, "abort");
  await pressRelease(page);
  await expect(page.locator("[data-error]").first()).toBeVisible();
  await page.reload();
  await page.getByRole("button", { name: "Liens", exact: true }).click();
  await liensTab(page, "due");
  await expect(page.locator("[data-row='due']:visible").first()).toBeVisible();
  await pressRelease(page);
  await expect(page.locator("[data-notice]")).toContainText(nb("Released $13,800.00 on lien job GLS-2105: 2 invoices."));
  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
});

test("fix r3: a reversal refused part way reads Partly reversed, and the next press sends only the rest", async ({ page, request }) => {
  await openDue(page, request);
  await pressRelease(page);
  const press = page.locator("[data-row='released']:visible");
  await expect(press).toHaveCount(1);
  let posts = 0;
  let refuseSecond = true;
  await page.route(REVERSAL, async (route) => {
    posts += 1;
    // The first press: the first invoice goes through, the second is refused once.
    if (refuseSecond && posts === 2) {
      refuseSecond = false;
      return route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: "an invoice on this release has been paid since, so it cannot be put back on hold" }) });
    }
    return route.continue();
  });
  await press.getByRole("button", { name: "Reverse" }).click();
  const panel = page.locator("[data-reverse-panel]");
  await panel.getByLabel("Reason").fill("Released against the wrong job");
  await panel.getByRole("button", { name: "Reverse release" }).click();
  await expect(panel.locator("[data-error]")).toContainText("has been paid since");
  await expect(press).toContainText("Partly reversed");
  await expect(press.locator("[data-partly]")).toBeVisible();
  expect(posts).toBe(2);
  // Reverse again: the one still released is the only one sent.
  await press.getByRole("button", { name: "Reverse" }).click();
  await page.locator("[data-reverse-panel]").getByLabel("Reason").fill("Released against the wrong job");
  await page.locator("[data-reverse-panel]").getByRole("button", { name: "Reverse release" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Reversed the release on lien job GLS-2105: INV-4102 ($7,500.00) is held back again."));
  expect(posts).toBe(3);
  await expect(press).toContainText("Reversed");
  await expect(press).not.toContainText("Partly reversed");
});

test("fix r3: a job over two branches names both, and the confirm names no branch total that is not the job's", async ({ page, request }) => {
  await openDue(page, request);
  // The Fieldstone patio job (INV-4201 at one yard, INV-4202 at the other) comes due while the screen is open.
  await behind(request, "PUT", "/api/v1/lien/jobs/10b00000-0000-4000-8000-000000000003/preservation-dates", { substantial_perf_date: dayFrom(-120), last_supply_date: dayFrom(-120), csp_published_on: dayFrom(-120) });
  await liensTab(page, "jobs");
  await liensTab(page, "due");
  const split = due(page, "GLS-2103");
  await expect(split).toContainText("Due now");
  await expect(split).toContainText("INV-4201");
  await expect(split).toContainText("INV-4202");
  await expect(split).toContainText("Point Loma (demo) and Julian (demo)");
  await split.getByRole("button", { name: "Release" }).click();
  const words = page.locator("[data-due-panel] [data-confirm-words]");
  await expect(words).toContainText("Release the holdback held on 2 invoices for ");
  await expect(words).not.toContainText("Release $");
  await expect(words).toContainText("Point Loma (demo) and Julian (demo)");
  await expect(words).toContainText(nb("INV-4201 ($3,360.00)"));
  await expect(words).toContainText(nb("INV-4202 ($2,100.00)"));
  await page.locator("[data-due-panel]").getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Released $5,460.00 on lien job GLS-2103: 2 invoices."));
});
