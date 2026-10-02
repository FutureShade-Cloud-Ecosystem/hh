// Shared words and moves for the W9 acceptance run, on the ported POS surface
// (the stepper cards, the session bar, the sold panel): signing in as a role,
// the counter's search and add rhythm, the money strings the one helper renders
// (mirrored here so the test never imports the app's own code), the staff
// reads the assertions compare the screen against, and the screenshot helper
// that names every screen of the story.
import { expect, test as base, type Page, type TestInfo } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

export type Role = "Sales" | "Sales manager";

/** The demo branch the till works: Trenton's id, as the seed planted it. */
export const TRENTON_ID = "5d1b3f6e-0000-4000-9000-000000000001";
/** The branch the demo login never reaches, as the seed planted it. */
export const KINGSTON_ID = "5d1b3f6e-0000-4000-9000-000000000002";

/** The seeded sale numbers the run's screens act on, as the seed writes them. */
export const SEEDED = {
  /** The Kingston card sale: refused at the Trenton counter, by the branch wall. */
  kingstonSale: "DEMO-KGN-S01",
  /** Kingston's parked ticket: never on Trenton's parked list. */
  kingstonTicket: "#PARK-DEMO-01",
  /** Trenton's seeded walk-in ticket: on the list, resumable by anyone. */
  trentonTicket: "#PARK-DEMO-02",
  /** Trenton's seeded card sale: paid by card, carrying no card reference. */
  trentonCardSale: "DEMO-TRT-S03",
};

/**
 * Two of the seed's stable ids, fixed by the seed's own id scheme: the
 * Trenton contractor whose bulk rule puts Granular A below the floor, and
 * that product. The below floor leg reads the glance for exactly this line
 * through the manager's bearer (the policy word's named reader, row 651)
 * and proves the floor fact; the clerk's screen states no column at all.
 */
export const SEEDED_IDS = {
  quinteC03: "02969883-fda1-5281-b306-014ad3a2b655",
  granularA: "b7fd983f-04d3-5d01-9fec-9deaee0714e6",
};

/**
 * The screen-side rendering of integer cents, the same words the app's one
 * helper renders: 1968 becomes "$19.68". Mirrored here so the test compares
 * like with like without importing the app's own module.
 */
export function dollars(cents: number): string {
  const negative = cents < 0;
  const magnitude = Math.abs(cents);
  const whole = Math.floor(magnitude / 100);
  const remainder = magnitude - whole * 100;
  return `${negative ? "-" : ""}$${whole.toLocaleString("en-CA")}.${String(remainder).padStart(2, "0")}`;
}

/**
 * The ported surface's own money word on a line, the total card and the sold
 * panel: "CA$5.95". The returns desk (HH's own view) keeps the plain "$"
 * words, so both renderings are mirrored here.
 */
export function cad(cents: number): string {
  return `CA${dollars(cents)}`;
}

/** The column's percent from basis points, as the app's helper rounds it. */
export function marginPercent(bps: number): string {
  const tenths = Math.round(Math.abs(bps) / 10);
  const whole = Math.floor(tenths / 10);
  const tenth = tenths - whole * 10;
  return `${bps < 0 ? "-" : ""}${whole}.${tenth}%`;
}

/**
 * The dealer's day in the house zone, computed here the way the till computes
 * it, so the world's newest rows can be held against the run's own clock.
 */
export function houseDay(): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "America/Toronto",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date());
}

/** The dealer's day the given count of house zone days before this one. */
export function houseDaysAgo(days: number): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "America/Toronto",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date(Date.now() - days * 24 * 60 * 60 * 1000));
}

/**
 * The demo mint's per-address allowance is the till's own guard: a bounded
 * number of mints from one address inside one window, one bucket behind the
 * proxy. The run paces itself to the human pace the guard assumes before every
 * mint, wherever it happens: a person taps a card, reads the screen, and taps
 * again far slower than this guard's window. The clock lives on the worker's
 * global object, not in module state: the run's spec files each get their own
 * module instance, and the pace is one pace for the whole run.
 */
const MINT_SPACING_MS = 7500;
type MintClock = { lastMintAt: number };

function mintClock(): MintClock {
  const carrier = globalThis as { __ctMintClock?: MintClock };
  if (carrier.__ctMintClock === undefined) carrier.__ctMintClock = { lastMintAt: 0 };
  return carrier.__ctMintClock;
}

export async function paceMint(page: Page): Promise<void> {
  const clock = mintClock();
  const wait = clock.lastMintAt + MINT_SPACING_MS - Date.now();
  if (wait > 0) await page.waitForTimeout(wait);
  clock.lastMintAt = Date.now();
}

function paceMintOffPage(): Promise<void> {
  const clock = mintClock();
  const wait = clock.lastMintAt + MINT_SPACING_MS - Date.now();
  clock.lastMintAt = Date.now() + Math.max(wait, 0);
  return wait > 0 ? new Promise((resolve) => setTimeout(resolve, wait)) : Promise.resolve();
}

/**
 * Mint a demo session for one flow through the gated public route. One token
 * per flow per process: the mint is a credential-shaped effect, and a run that
 * minted per test would spend the till's own sign-in guard on itself. The
 * token lives far longer than the run, so a memoized mint is the honest pace,
 * the same one pace a person tapping cards holds.
 */
const minted = new Map<string, Promise<string>>();

export function mint(page: Page, flow: string): Promise<string> {
  // The page argument keeps the helpers' call sites uniform, but the mint
  // never rides a test's request context: the memo outlives any one test.
  let inFlight = minted.get(flow);
  if (inFlight === undefined) {
    inFlight = (async () => {
      await paceMintOffPage();
      const phrase = process.env.CT_GATE_PHRASE ?? "";
      const base = process.env.CT_IMAGE_URL ?? "http://127.0.0.1:5801";
      const answer = await fetch(new URL("/api/v1/demo/session", base), {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Basic ${Buffer.from(`demo:${phrase}`).toString("base64")}`,
        },
        body: JSON.stringify({ flow }),
      });
      expect(answer.ok).toBe(true);
      const body = (await answer.json()) as { token?: string };
      expect(typeof body.token).toBe("string");
      return body.token as string;
    })();
    minted.set(flow, inFlight);
  }
  return inFlight;
}

/** A staff GET through the same origin the app uses, with a bearer and org. */
export async function staffGet(
  page: Page,
  bearer: string,
  path: string,
): Promise<{ status: number; body: Record<string, unknown> }> {
  const answer = await page.request.get(path, {
    headers: { Authorization: `Bearer ${bearer}`, "X-Org": "hh-demo" },
  });
  return { status: answer.status(), body: (await answer.json()) as Record<string, unknown> };
}

/** A staff POST with a JSON body, the same headers. */
export async function staffPost(
  page: Page,
  bearer: string,
  path: string,
  data: unknown,
): Promise<{ status: number; body: Record<string, unknown> }> {
  const answer = await page.request.post(path, {
    headers: { Authorization: `Bearer ${bearer}`, "X-Org": "hh-demo" },
    data,
  });
  return { status: answer.status(), body: (await answer.json()) as Record<string, unknown> };
}

/**
 * The drawer the counter shares: found or opened on first use. The opening
 * float is the reference's own: hardscapeos_dibbits app/src/pos-terminal.ts
 * line 2650 (master at 87d7941b) sends float_cents 30000 on its Open Session
 * press, and the ported surface keeps it (src/ported/pos-terminal.ts,
 * openSession), so the API's open carries the same 30000 and the suite
 * asserts one truth.
 */
export async function openDrawer(page: Page, bearer: string): Promise<string> {
  const lookup = await staffGet(
    page,
    bearer,
    `/api/v1/till/sessions/open?branch_id=${TRENTON_ID}&station=MAIN`,
  );
  if (lookup.body.open === true) return lookup.body.id as string;
  const opened = await staffPost(page, bearer, "/api/v1/till/sessions", {
    branch_id: TRENTON_ID,
    station: "MAIN",
    float_cents: 30000,
    opening_denominations: [],
  });
  return opened.body.id as string;
}

/** The drawer Z report's expected cash, the figure a cash refund moves. */
export async function expectedCash(page: Page, bearer: string, drawerId: string): Promise<number> {
  const report = await staffGet(page, bearer, `/api/v1/till/sessions/${drawerId}/zreport`);
  const cash = report.body.cash as { expected_cents?: number } | undefined;
  expect(typeof cash?.expected_cents).toBe("number");
  return cash?.expected_cents as number;
}

/**
 * The invented trading name the demo populate writes onto the branch rows
 * (the review round's P2-1): the till's identity line under the page title
 * carries it, and the first dealer's real trading name appears nowhere on a
 * till screen.
 */
export const DEMO_DEALER = "Hardscape House (demo)";

/** The steps of the linear flow, by the id their head carries. */
export type Step = "customer" | "items" | "payment" | "summary";

/** Open one step of the accordion by its head (a no-op when it is already open). */
export async function openStep(page: Page, step: Step): Promise<void> {
  const head = page.getByTestId(`step-head-${step}`);
  // A pick or an add can advance the flow a moment after it lands, closing
  // the head this call just opened: press again until it holds open.
  for (let attempt = 0; attempt < 5; attempt += 1) {
    if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
    try {
      await expect(head).toHaveAttribute("aria-expanded", "true", { timeout: 1_000 });
      await page.waitForTimeout(250);
      if ((await head.getAttribute("aria-expanded")) === "true") return;
    } catch {
      // closed again by the advance: press once more
    }
  }
  await expect(head).toHaveAttribute("aria-expanded", "true");
}

/**
 * Tap a role card and wait until the till answers under that role's name and
 * has loaded: the session bar's open dot (the drawer the till finds open)
 * use), the page's context line naming the invented dealer and the branch,
 * and the parked button's count (its first read has landed).
 */
export async function signIn(page: Page, role: Role): Promise<void> {
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await paceMint(page);
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("signed-in-role")).toHaveText(role);
  await expect(page.getByTestId("session-dot")).toContainText("Open", { timeout: 15_000 });
  await expect(page.getByTestId("pagectx")).toContainText(DEMO_DEALER);
  await expect(page.getByTestId("pagectx")).toContainText("Trenton");
  // The parked read has landed: the button states 0 until it does, and the
  // seeded world always holds Trenton's walk-in ticket (a pickup parks a
  // ticket back), so a count of one or more is the loaded state.
  await expect(page.getByRole("button", { name: /^Parked \([1-9]\d*\)/ })).toBeVisible();
}

/** The drawer Z report's opening float, the figure the drawer was opened with. */
export async function openingFloat(page: Page, bearer: string, drawerId: string): Promise<number> {
  const report = await staffGet(page, bearer, `/api/v1/till/sessions/${drawerId}/zreport`);
  const cash = report.body.cash as { opening_float_cents?: number } | undefined;
  expect(typeof cash?.opening_float_cents).toBe("number");
  return cash?.opening_float_cents as number;
}

/** Search from the scan box and add the named hit: the drop row's click adds. */
export async function searchAndAdd(page: Page, query: string, hitName: string): Promise<void> {
  // The accordion opens on Customer; the scan box lives in Items.
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill(query);
  const row = page.getByTestId("drop-row").filter({ hasText: hitName }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(lineRow(page, hitName)).toHaveCount(1);
  // The line is priced when its price lookup answers: the unit price slot
  // reads a figure, never the pending marker.
  await expect(lineRow(page, hitName).locator(".lprice")).toContainText("$");
}

/** The ticket's line for one product, found by its name. */
export function lineRow(page: Page, hitName: string) {
  return page.getByTestId("pos-line-row").filter({ hasText: hitName });
}

/** Type a quantity into a line's stepper input and let the engine reprice it. */
export async function setQuantity(page: Page, hitName: string, qty: string): Promise<void> {
  const field = page.getByRole("textbox", { name: `Quantity: ${hitName}` });
  await field.fill(qty);
  await field.blur();
}

/**
 * Find an account by a word of its number or name and pick the row that
 * names it. The pick advances the flow, so the Customer card is opened again
 * (retried until it is readable) and held to the expected words: the strong
 * carries the display name and the account number sits beside it, so the
 * caller may name either.
 */
export async function pickCustomer(page: Page, query: string, expected: string): Promise<void> {
  await openStep(page, "customer");
  // A picked account is changed through its card's own control; a walk-in
  // opens the picker from the select button.
  const change = page.getByTestId("customer-change");
  if (await change.isVisible()) await change.click();
  else await page.getByRole("button", { name: /Select customer/ }).click();
  await page.getByPlaceholder("Search name or account #…").fill(query);
  const row = page.locator("button.row").filter({ hasText: expected });
  await expect(row).toHaveCount(1);
  await row.click();
  const card = page.getByTestId("customer-card");
  for (let attempt = 0; attempt < 6; attempt += 1) {
    await openStep(page, "customer");
    try {
      await expect(card).toContainText(expected, { timeout: 700 });
      return;
    } catch {
      // the advance closed the step again: open it once more
    }
  }
  await expect(card).toContainText(expected);
}

/** What the ring's POST answered: the identifiers and figures of the committed sale. */
export interface Rung {
  order_id: string;
  order_no: string;
  invoice_no: string;
  total_cents: number;
  /** The cash over-tender's change, present on a cash ring with a quick entry. */
  change_due_cents?: number;
  /** The deposit taken at ring, present on an unpaid-at-ring sale taken with one. */
  deposit_cents?: number;
}

/**
 * Press Complete sale on the Payment step and return the core's own answer to
 * the ring. The caller has chosen the tender and typed any cash tendered.
 */
export async function ring(page: Page): Promise<Rung> {
  await openStep(page, "payment");
  const posted = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  const answer = await posted;
  expect(answer.ok()).toBe(true);
  const sale = (await answer.json()) as Rung;
  await expect(page.getByTestId("sold-panel")).toBeVisible();
  return sale;
}

/** A cash ring: the quick entry typed, then the ring. */
export async function ringCash(page: Page, tendered: string): Promise<Rung> {
  await openStep(page, "payment");
  await page.getByLabel(/Cash tendered in dollars/).fill(tendered);
  return ring(page);
}

/**
 * The parked tickets dialog, opened from the button that carries the count,
 * and read once its list has landed: the dialog's rows equal the count the
 * button states (the dialog shows its empty word until the first read lands).
 */
export async function openParked(page: Page) {
  const button = page.getByRole("button", { name: /^Parked \(\d+\)/ });
  const stated = async (): Promise<number> =>
    Number(/\((\d+)\)/.exec((await button.textContent()) ?? "")?.[1] ?? "-1");
  await button.click();
  const dialog = page.locator("hos-pos-parked .hos-dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Parked tickets");
  // The button's count refreshes a beat after a park, the dialog's list reads
  // on open: both settle on the same figure.
  await expect
    .poll(async () => (await dialog.locator(".ticket").count()) - (await stated()))
    .toBe(0);
  return dialog;
}

/**
 * The returns desk's per line quantity inputs. The desk labels each input
 * with the line's description as the core answered it, and a line a rung sale
 * carries no description word, so the tests find the inputs by their role in
 * the desk rather than by the line's name.
 */
export function returnLineInputs(page: Page) {
  return page.getByTestId("returns").getByRole("spinbutton");
}

function shotDir(testInfo: TestInfo): string {
  // Outside Playwright's own output directory: a later leg's run start would
  // sweep the earlier leg's screenshots away with the rest of its artifacts.
  const dir =
    process.env.CT_SHOT_DIR ?? join(testInfo.config.rootDir, "..", "..", "acceptance-screens");
  mkdirSync(dir, { recursive: true });
  return dir;
}

/**
 * The page's own requests, counted from the page's making: every request the
 * page fires raises the count, and only its finish or its failure lowers it,
 * so a read still in flight is a count above zero however many times the
 * page has already gone idle. The harness's page fixture installs the
 * counter, so every page a spec is handed is counted from birth, and settle
 * waits on the count rather than on a load state (a load state of the
 * document is recorded once and answers every later call from that record,
 * which is no wait at all in the path every till shot takes: the cards page
 * goes idle while the mint paces the tap, so the till's own reads fire after
 * the only idle the document will ever report).
 */
type InFlight = { count: number };
const inFlight = new WeakMap<Page, InFlight>();

function trackInFlight(page: Page): void {
  if (inFlight.has(page)) return;
  const state: InFlight = { count: 0 };
  inFlight.set(page, state);
  page.on("request", () => {
    state.count += 1;
  });
  page.on("requestfinished", () => {
    state.count = Math.max(0, state.count - 1);
  });
  page.on("requestfailed", () => {
    state.count = Math.max(0, state.count - 1);
  });
}

/** The harness's own test: the pages it hands a spec carry the counter. */
export const test = base.extend<{ page: Page }>({
  page: async ({ page }, use) => {
    trackInFlight(page);
    await use(page);
  },
});

export { expect };

/**
 * The page is at rest: the fonts are in, the ported surface's own in-flight
 * words are off the screen (the demo roles word while the cards load, the
 * products word while the grid loads, the receipt word while a print read
 * runs; the surface shows no word of its own while its other reads are in
 * flight), the reads are quiet (the page's own request count has drained to
 * zero), and a mounted till shows its loaded chrome: the flow stepper, and
 * the session bar's own answer, the open dot or the Open Session button. A
 * shot of a half loaded screen shows nothing of its claim.
 */
export async function settle(page: Page): Promise<void> {
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
  await expect(page.getByText(/\bLoading\b/)).toHaveCount(0);
  await expect
    .poll(() => inFlight.get(page)?.count ?? 0, { intervals: [100], timeout: 15_000 })
    .toBe(0);
  const till = page.locator("hos-pos-flow-steps");
  if ((await till.count()) > 0) {
    await expect(till).toBeVisible();
    await expect(
      page.getByTestId("session-dot").or(page.getByRole("button", { name: "Open Session" })),
    ).toBeVisible();
  }
}

/**
 * One screenshot per screen per role, into the folder the run was given:
 * clearly named, produced on demand, carried by no commit. The page settles
 * first. A clip narrows the picture to the part of the screen that carries
 * the claim (the session bar and the identity line, for the drawer).
 */
export async function shot(
  page: Page,
  name: string,
  testInfo: TestInfo,
  options: { clip?: { x: number; y: number; width: number; height: number } } = {},
): Promise<void> {
  await settle(page);
  await page.screenshot({
    path: join(shotDir(testInfo), `${name}.png`),
    fullPage: options.clip === undefined,
    clip: options.clip,
  });
}

/** The top of the screen: the demo banner, the role, the dealer line and the session bar. */
export const TOP_BAR_CLIP = { x: 0, y: 0, width: 1280, height: 280 };

/**
 * A staff read shown as the browser shows it, for the claims whose evidence
 * the screen does not carry (the drawer's Z report, the dispatch read): a
 * fresh page under the same gate, the bearer attached to the one navigation,
 * the picture returned as bytes for a composed shot. The bearer is a demo
 * session token; it never appears in the picture, only the core's answer does.
 */
export async function apiPicture(page: Page, bearer: string, path: string): Promise<Buffer> {
  const evidence = await page.context().newPage();
  // A short viewport, so the full page picture is the answer's own height.
  await evidence.setViewportSize({ width: 1280, height: 100 });
  await evidence.route("**/api/v1/**", (route) =>
    route.continue({
      headers: { ...route.request().headers(), Authorization: `Bearer ${bearer}`, "X-Org": "hh-demo" },
    }),
  );
  const answer = await evidence.goto(path);
  expect(answer).not.toBeNull();
  await expect(evidence.locator("body")).not.toBeEmpty();
  const picture = await evidence.screenshot({ fullPage: true });
  await evidence.close();
  return picture;
}

/**
 * One screenshot made of several pictures stacked top to bottom, each a real
 * capture of the product (a region of the till, a staff read as the browser
 * shows it): the shot for a claim whose proof lives in more than one place,
 * so the one named file shows all of it. The page settles first, the same
 * rule the single shot holds.
 */
export async function shotParts(
  page: Page,
  name: string,
  testInfo: TestInfo,
  parts: Buffer[],
): Promise<void> {
  await settle(page);
  const composer = await page.context().newPage();
  await composer.setViewportSize({ width: 1280, height: 800 });
  const images = parts
    .map(
      (part) =>
        `<img style="display:block;max-width:1240px;margin:0 0 16px 0;border:1px solid #c9ced6" src="data:image/png;base64,${part.toString("base64")}">`,
    )
    .join("");
  await composer.setContent(`<body style="margin:20px;background:#fff">${images}</body>`);
  await composer.waitForFunction(() => Array.from(document.images).every((image) => image.complete));
  await composer.screenshot({ path: join(shotDir(testInfo), `${name}.png`), fullPage: true });
  await composer.close();
}
