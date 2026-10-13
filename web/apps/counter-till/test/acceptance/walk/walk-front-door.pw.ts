// The front door walk: the box's own story, walked in a real browser
// against a running box of the assembled demo image (the front door at /,
// the till at /till/ and the Quote Desk at /quote/): pick a role, open each
// module signed in, Switch role, Sign out. The walk lives beside the till's
// acceptance suite and runs only through its own config
// (test/walk-front-door.config.ts): the stub harness's web servers serve the
// lanes' suites only, and the walk's box is the assembled image's, which
// this machine's default legs never build or boot. Nothing here is stubbed:
// the mints are the box's own, paced to the demo mint guard through the
// acceptance suite's shared clock (the helpers' paceMint, keyed on the box's
// address like the till's own belt), and every landing is asserted on the
// screen it names.
//
// What the walk holds, in order:
//
//   the picker renders: the eight live role cards with their calls to act,
//     the demo badge, and the five coming roles in their disclosure;
//   a Sales sign in lands on the Sales home: the role's name as the title,
//     the two live module tiles, the header word;
//   the Counter Till tile opens the till signed in as Sales, no second
//     picker: the adopted handoff lands on the loaded counter;
//   back at the front door the picker is up again: the handoff is one
//     shot, the till took the key it carried (the seam's own rule);
//   the Quote Desk tile opens the desk signed in as Sales, no second
//     picker either: the desk's adopt (PR 202) takes the handoff's key
//     the same one shot way, and back at the front door the picker is up
//     again for the same reason;
//   a Sales manager sign in shows the manager's five tiles, the not yet
//     modules receding under Coming next;
//   a not yet module opens its coming soon page and returns;
//   Switch role returns to the picker and clears the session keys;
//   Sign out returns to the picker and clears them again.
//
// The two landings read different chrome on purpose (round 7): the desk
// wears the design system's shared header, whose word carries
// data-testid="header-word"; the till keeps its own session bar's
// data-testid="signed-in-role".
import { expect, test, type Page } from "@playwright/test";
import { paceMint } from "../helpers.js";

const FRONT_DOOR_SESSION_KEY = "hh-demo.front-door.session.v1";
const MIRROR_KEY = "hh-auth.session.v2";

type Role = "Sales" | "Sales manager";

// ── the belt (the acceptance suite's pace, this walk's own watch) ──────────

const WINDOW_PASS_MS = 31_000;
const SIGN_ANSWER_MS = 15_000;
const REFUSAL_RETRIES = 2;
const REFUSAL_WORDS = "too many sign-in attempts";

// ── the walk's own moves ────────────────────────────────────────────────────

function storedKeys(page: Page): Promise<string[]> {
  return page.evaluate(
    (keys) => keys.filter((key) => window.sessionStorage.getItem(key) !== null),
    [FRONT_DOOR_SESSION_KEY, MIRROR_KEY],
  );
}

async function expectPicker(page: Page): Promise<void> {
  await expect(page.getByTestId("header-word")).toHaveText("Pick a role");
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Sales manager", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Yard manager", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Payables and Receivables", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Procurement manager", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Driver", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Yard Team", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Financial Controller", exact: true })).toBeVisible();
}

/**
 * The refusal watch: the box's own words for a spent window, verbatim, in
 * the card that earned them (the front door puts the mint's answer in the
 * card and only the pointer sentence in the polite region, so the watch
 * reads the page's text, not the status role).
 */
function refusalWatch(page: Page) {
  return page.getByText(REFUSAL_WORDS).first();
}

/**
 * Sign in from the front door's picker: the shared pace, the tap, and the
 * belt (the box's own words for a spent window are the one refusal a wait
 * cures; the belt waits and taps again, at most twice, then the run fails
 * honestly). Every ending holds the role home: the role's name as the
 * title and the header word carrying it.
 */
async function signInFrontDoor(page: Page, role: Role): Promise<void> {
  const card = page.getByRole("button", { name: role, exact: true });
  const home = page.getByTestId("home-title");
  const refusal = refusalWatch(page);
  for (let attempt = 0; ; attempt += 1) {
    await paceMint(page);
    await card.click();
    const answer = await Promise.race([
      home
        .filter({ hasText: role })
        .waitFor({ state: "visible", timeout: SIGN_ANSWER_MS })
        .then(() => "in" as const, () => "stalled" as const),
      refusal
        .waitFor({ state: "visible", timeout: SIGN_ANSWER_MS })
        .then(() => "refused" as const, () => "stalled" as const),
    ]);
    if (answer === "in") break;
    if (answer !== "refused" || attempt >= REFUSAL_RETRIES) break;
    await page.waitForTimeout(WINDOW_PASS_MS);
  }
  await expect(home).toHaveText(role);
  await expect(page.getByTestId("header-word")).toHaveText(role);
}

test("the front door walk: pick a role, open each module signed in, Switch role, Sign out", async ({
  page,
}) => {
  // ── the picker, before any sign in ──────────────────────────────────────
  await page.goto("/");
  await expectPicker(page);
  const badge = page.getByRole("note");
  await expect(badge).toContainText("Demo");
  const sales = page.getByRole("button", { name: "Sales", exact: true });
  await expect(sales).toContainText("Sign in as Sales");
  const disclosure = page.getByTestId("coming-disclosure");
  await expect(disclosure).toContainText("Five more roles are coming");
  await disclosure.getByText("Five more roles are coming").click();
  await expect(page.getByRole("list", { name: "Coming roles" }).getByRole("listitem")).toHaveCount(5);

  // ── Sales signs in, and the home shows the two live tiles ───────────────
  await signInFrontDoor(page, "Sales");
  await expect(page.getByRole("button", { name: "Counter Till", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Quote Desk", exact: true })).toBeVisible();

  // ── the Counter Till opens signed in, no second picker ──────────────────
  // The landing is held by the till's loaded chrome: the adopted role's
  // name in the session bar (signed-in-role, the till's own testid, kept
  // by round 7), the flow stepper, and the session bar's answer, the open
  // dot or the Open Session button (a fresh world's drawer is closed; the
  // dot reads Open only once a session is opened, which is world state,
  // not the handoff's claim).
  await page.getByRole("button", { name: "Counter Till", exact: true }).click();
  await expect(page).toHaveURL(/\/till\/$/);
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales", { timeout: SIGN_ANSWER_MS });
  await expect(page.locator("hos-pos-flow-steps")).toBeVisible({ timeout: 15_000 });
  await expect(
    page.getByTestId("session-dot").or(page.getByRole("button", { name: "Open Session" })),
  ).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);

  // ── back at the front door the picker is up again: the handoff was
  // one shot, the till took the key it carried (the custody mirror the
  // till rewrote may remain; the front door's own key is the spent one) ──
  await page.goto("/");
  await expectPicker(page);
  expect(await storedKeys(page)).not.toContain(FRONT_DOOR_SESSION_KEY);

  // ── the Quote Desk opens signed in, no second picker ────────────────────
  // The desk's adopt (PR 202) took the handoff's key the same one shot
  // way the till does: the landing is held by the desk's own chrome, the
  // adopted role's name in the design system's shared header (header-word,
  // round 7's edit: the desk has no session bar of its own) and the quote
  // list, and the desk's entry cards never render.
  await signInFrontDoor(page, "Sales");
  await page.getByRole("button", { name: "Quote Desk", exact: true }).click();
  await expect(page).toHaveURL(/\/quote\/$/);
  await expect(page.getByTestId("header-word")).toHaveText("Sales", { timeout: SIGN_ANSWER_MS });
  await expect(page.getByTestId("qb-list")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);

  // ── back at the front door the picker is up again: the handoff was
  // one shot, the desk took the key it carried ────────────────────────────
  await page.goto("/");
  await expectPicker(page);
  expect(await storedKeys(page)).not.toContain(FRONT_DOOR_SESSION_KEY);

  // ── the Sales manager's home shows the five tiles, the not yet modules
  // under Coming next ──────────────────────────────────────────────────────
  await signInFrontDoor(page, "Sales manager");
  for (const name of ["Counter Till", "Quote Desk", "Manager Home", "Dealer Admin", "House Messages"]) {
    await expect(page.getByRole("button", { name, exact: true })).toBeVisible();
  }
  await expect(page.getByRole("heading", { name: "Coming next" })).toBeVisible();

  // ── a not yet module opens its coming soon page and returns ─────────────
  await page.getByRole("button", { name: "House Messages", exact: true }).click();
  await expect(page.getByRole("heading", { name: /House Messages/ })).toBeVisible();
  await expect(page.getByText("not on this demo box yet")).toBeVisible();
  await expect(page.getByTestId("header-word")).toHaveText("Sales manager");
  await page.getByRole("button", { name: "Back to Sales manager" }).click();
  await expect(page.getByTestId("home-title")).toHaveText("Sales manager");

  // ── Switch role returns to the picker and clears the keys ───────────────
  await page.getByRole("button", { name: "Switch role", exact: true }).click();
  await expectPicker(page);
  expect(await storedKeys(page)).toEqual([]);

  // ── Sign out returns to the picker and clears them too ──────────────────
  await signInFrontDoor(page, "Sales manager");
  expect(await storedKeys(page)).toEqual([FRONT_DOOR_SESSION_KEY, MIRROR_KEY]);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expectPicker(page);
  expect(await storedKeys(page)).toEqual([]);
});
