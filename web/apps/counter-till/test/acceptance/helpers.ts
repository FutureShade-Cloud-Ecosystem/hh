// Shared words and moves for the W9 acceptance run: signing in as a role, the
// counter's search and add rhythm, the money strings the one helper renders
// (mirrored here so the test never imports the app's own code), the staff
// reads the assertions compare the screen against, and the screenshot helper
// that names every screen of the story.
import { expect, type Page, type TestInfo } from "@playwright/test";
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
 * and holds the screen's column to its answer.
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

/** The drawer the counter shares: found or opened on first use. */
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
    float_cents: 20000,
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
 * (the review round's P2-1): every till screen's drawer strip and parked list
 * header carries it, and the first dealer's real trading name appears
 * nowhere.
 */
export const DEMO_DEALER = "Hardscape House (demo)";

/** Tap a role card and wait until the till answers under that role's name. */
export async function signIn(page: Page, role: Role): Promise<void> {
  await page.goto("/");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await paceMint(page);
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("signed-in-role")).toHaveText(role);
  // The till's own start: the branch resolved, the drawer found or opened,
  // named for the invented dealer the populate wrote, not the migrated name.
  await expect(page.getByTestId("drawer-status")).toContainText(DEMO_DEALER);
  await expect(page.getByTestId("drawer-status")).toContainText("MAIN: open with a float of $200.00");
}

/** Search the catalogue and add the named hit to the ticket. */
export async function searchAndAdd(page: Page, query: string, hitName: string): Promise<void> {
  await page.getByLabel("Search the catalogue by sku or name").fill(query);
  await page.getByRole("button", { name: "Search", exact: true }).click();
  const hit = page.getByRole("button", { name: `Add ${hitName} to the ticket` });
  await expect(hit).toBeVisible();
  await hit.click();
}

/**
 * Find an account by a word of its number or name and pick the row that
 * names it. The picked panel is held to the expected words on the whole
 * Customer section: the strong carries the display name and the account
 * number sits beside it, so the caller may name either.
 */
export async function pickCustomer(page: Page, query: string, expected: string): Promise<void> {
  await page.getByLabel("Find an account by name or account number").fill(query);
  await page.getByRole("button", { name: "Find", exact: true }).click();
  const row = page
    .getByTestId("customer-hits")
    .getByRole("listitem")
    .filter({ hasText: expected });
  await expect(row).toHaveCount(1);
  await row.getByRole("button", { name: "Use" }).click();
  await expect(page.getByLabel("Customer")).toContainText(expected);
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

/**
 * One screenshot per screen per role, into the folder the run was given:
 * clearly named, produced on demand, carried by no commit.
 */
export async function shot(page: Page, name: string, testInfo: TestInfo): Promise<void> {
  // Outside Playwright's own output directory: a later leg's run start would
  // sweep the earlier leg's screenshots away with the rest of its artifacts.
  const dir =
    process.env.CT_SHOT_DIR ?? join(testInfo.config.rootDir, "..", "..", "acceptance-screens");
  mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: join(dir, `${name}.png`), fullPage: true });
}
