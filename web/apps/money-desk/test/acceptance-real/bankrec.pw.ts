// The Money Desk's bank reconciliation against the REAL core (W2-4 (d), wave 2, the join round).
//
// WHAT THIS SPEC IS: the Financial Controller's bank reconciliation area on a real core box, read the way the
// controller reads it. Every money figure the screen shows is held against the core's own answer for that figure.
//
// TWO WORLDS, ONE SWITCH (the join round). (d) was written against W2-1b's world, where a month was signed off at
// zero difference. Today's finance seed signs off NO month: world.ts's SIGNED_OFF_BANK_MONTH is the empty string,
// and the harness's own leg 3 ("a signed off bank month: what today's seed truly holds") shows what the box answers.
// So each leg here carries both branches, as (c)'s periods leg does:
//
//   - THE SEED'S BRANCH (SIGNED_OFF_BANK_MONTH === ""): what today's seed truly holds. No month is reconciled, so
//     no month is signed off and no month reads zero difference; the desk offers the starting point; the figures
//     the desk does show are the core's own figures.
//   - THE WORLD'S BRANCH: the world's signed off month at zero difference, its report's figures and its matched
//     lines, the assertions (d) was written with.
//
// The switch is one constant in world.ts, and the world's own anchors take over with it: while OPEN_MONTH_ANCHOR is
// empty the specs read the box's own month at run time, and when it carries a month they read that month.
//
// A BRANCH THAT CAN PASS ON A BLANK IS NOT A LEG (FROM-PRINCIPAL 386). Every leg here fails on an empty screen
// alone and on an empty core alone, each on its own:
//
//   - EMPTY SCREEN ALONE: the seed branch asserts the desk's own elements (the empty month's block, the starting
//     point form, the account card, the empty lines block). A screen that shows nothing has none of them.
//   - EMPTY CORE ALONE: the seed branch asserts the core's own answer (its bank and card accounts, by count and by
//     the bank account's name) before it reads the screen, so a core that answers with nothing fails on the count.
//
// THE RED: this spec runs under the stub and fails there, because the stub's world is not the core's. The stub holds
// three demo accounts where the seed holds nine, and seven statement lines where the seed holds none, so each leg
// fails on the core's own figures and never on a missing session.
import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import type {
  BankRecAccount,
  BankRecMatch,
  BankRecReconciliation,
  BankRecStatementLine,
  BankRecStatementLinePage,
} from "@hardscapehouse/api-client";
import { formatMoney } from "../../src/bankrec/bankrec-money.js";
import { matchMethodWord, targetWord } from "../../src/bankrec/bankrec-logic.js";
import { asStaff, coreURL } from "./session.js";
import { readBoxMonth, staffHeaders } from "./box-read.js";
import {
  BANKREC_ACCOUNT_COUNT,
  BANKREC_BANK_ACCOUNT_NAME,
  BANKREC_RECONCILIATION_COUNT,
  BANKREC_STATEMENT_LINE_COUNT,
  OPEN_MONTH_ANCHOR,
  SIGNED_OFF_BANK_MONTH,
  SIGNED_OFF_VARIANCE_CENTS,
} from "./world.js";

/** One answer the core sent: its route, the query it carried and its body. */
interface CoreAnswer {
  readonly path: string;
  readonly params: URLSearchParams;
  readonly body: unknown;
}

/** Every answer the core sent, in the order they arrived. */
interface CoreAnswers {
  readonly answers: readonly CoreAnswer[];
}

/** The value or a failure that says what was missing. */
function need<T>(value: T | undefined, what: string): T {
  if (value === undefined) throw new Error(what);
  return value;
}

/** The world's month, split the way the core's rows hold it. */
function monthOf(text: string): { year: number; month: number } {
  const [year, month] = text.split("-").map(Number);
  return { year: need(year, `the month's year in ${text}`), month: need(month, `the month's number in ${text}`) };
}

/** The core's own answers, as they arrive: the desk's calls are the ones the spec reads. */
function watchCore(page: Page): CoreAnswers {
  const answers: CoreAnswer[] = [];
  page.on("response", (response) => {
    const url = new URL(response.url());
    if (!url.pathname.startsWith("/api/v1/bankrec/")) return;
    if (!(response.headers()["content-type"] ?? "").includes("json")) return;
    void response
      .json()
      .then((body: unknown) => {
        answers.push({ path: url.pathname, params: url.searchParams, body });
      })
      .catch(() => undefined);
  });
  return { answers };
}

/** The answers the core sent for a route, newest last. A query narrows the answers to the ones that carried it. */
function answersFor(core: CoreAnswers, path: string, query?: Record<string, string>): unknown[] {
  return core.answers
    .filter((answer) => answer.path === path)
    .filter((answer) => query === undefined || Object.entries(query).every(([key, value]) => answer.params.get(key) === value))
    .map((answer) => answer.body);
}

/** The last answer the core sent for a route, waiting for it to arrive. The route and its query ride together: the
 *  desk asks the lines with and without a status, and only the answer that carried the query is this one. */
async function coreAnswer(core: CoreAnswers, path: string, query?: Record<string, string>): Promise<unknown> {
  await expect.poll(() => answersFor(core, path, query).length, { timeout: 30_000 }).toBeGreaterThan(0);
  const list = answersFor(core, path, query);
  return list[list.length - 1];
}

/** The query the core's own last answer for a route carried, so a spec can hold the desk's own call to the core's
 *  own account: the desk asked for the lines of one account, and the answer names that account. */
function answerParams(core: CoreAnswers, path: string, query?: Record<string, string>): URLSearchParams {
  const list = core.answers
    .filter((answer) => answer.path === path)
    .filter((answer) => query === undefined || Object.entries(query).every(([key, value]) => answer.params.get(key) === value));
  return need(list[list.length - 1], `the core answered ${path}`).params;
}

/** Where the desk's app may be served: under the base URL, or at the box's own path. */
function appPaths(baseURL: string | undefined): string[] {
  const path = baseURL === undefined ? "" : new URL(baseURL).pathname.replace(/\/+$/, "");
  if (path.endsWith("/money-desk")) return ["./"];
  if (path === "") {
    // The box's own path, when the config's base URL is the box's root.
    const box = coreURL().replace(/\/+$/, "");
    return ["./", `${box}/money-desk/`];
  }
  return [`${path}/`, "/money-desk/"];
}

/** Signs in as the Financial Controller and opens the bank reconciliation area. */
async function openDesk(page: Page, context: BrowserContext, baseURL: string | undefined): Promise<void> {
  await asStaff(context, "financial_controller");
  const area = page.getByRole("button", { name: "Bank reconciliation", exact: true });
  for (const path of appPaths(baseURL)) {
    await page.goto(path);
    try {
      await area.waitFor({ state: "visible", timeout: 15_000 });
      await area.click();
      // The area's own accounts panel, not an account card: a core that answers with no accounts at all still
      // opens the area, and the leg then fails on the core's own figure rather than in this helper.
      await page.locator("[data-br-accounts]").waitFor({ state: "visible", timeout: 15_000 });
      return;
    } catch {
      // The desk is not at this path; the next one is where the box serves it.
    }
  }
  throw new Error(`the Money Desk did not open at ${appPaths(baseURL).join(" or ")}`);
}

/** The desk's own tab. */
async function tab(page: Page, id: "accounts" | "import" | "match" | "rules"): Promise<void> {
  await page.locator(`[data-tab-id='${id}']`).click();
}

/** The account the desk works on: the first bank account the core lists, the same one the desk picks. */
function bankAccount(accounts: BankRecAccount[]): BankRecAccount {
  return need(
    accounts.find((a) => a.account_type === "BANK"),
    "the core lists a bank account to reconcile",
  );
}

/**
 * THE WORLD'S BANK ACCOUNT, as the core answers it.
 *
 * The name is the world's own, so a core that answers with another world's accounts fails here. The count of the
 * accounts is the seed's branch's own figure, read before anything else (see the legs).
 */
function worldBank(accounts: BankRecAccount[]): BankRecAccount {
  const bank = bankAccount(accounts);
  expect(bank.name, "the world's bank account").toBe(BANKREC_BANK_ACCOUNT_NAME);
  return bank;
}

/** The core's own reconciliation for a month of the account. */
function reconOf(recons: BankRecReconciliation[], month: string): BankRecReconciliation {
  const { year, month: number } = monthOf(month);
  return need(
    recons.find((r) => r.year === year && r.month === number),
    `the core holds the month's reconciliation for ${month}`,
  );
}

test("the month the world signed off reads zero difference, and the desk says so", async ({
  page,
  context,
  request,
}, testInfo) => {
  const headers = staffHeaders(page);
  const core = watchCore(page);
  await openDesk(page, context, testInfo.project.use.baseURL);
  const boxMonth = await readBoxMonth(request, await headers);
  // The world's open month, or the box's own month while the world names none (no month literal, FROM-PRINCIPAL 389).
  const openMonth = OPEN_MONTH_ANCHOR === "" ? boxMonth : OPEN_MONTH_ANCHOR;

  const accounts = (await coreAnswer(core, "/api/v1/bankrec/accounts")) as BankRecAccount[];
  if (SIGNED_OFF_BANK_MONTH === "") {
    // THE CORE'S OWN ACCOUNTS, FIRST, BEFORE ANY OTHER ANSWER IS READ. A core that answers with nothing at all
    // fails on this figure, here, and not on a later wait for an answer the desk never asks for: with no account
    // the desk never asks for the account's months at all.
    expect(accounts.length, "the core lists the world's bank and card accounts").toBe(BANKREC_ACCOUNT_COUNT);
  }
  const recons = (await coreAnswer(core, "/api/v1/bankrec/reconciliations")) as BankRecReconciliation[];

  if (SIGNED_OFF_BANK_MONTH === "") {
    // TODAY'S SEED: THE BOX SIGNS OFF NO MONTH AT ALL.
    //
    // THE CORE'S OWN ANSWER: it holds no reconciliation, for any month, for any account. The finance seed opens no
    // month and signs none off, so there is no month that could read zero difference.
    expect(recons.length, `the core holds no reconciliation at all (the box's own month is ${boxMonth})`).toBe(
      BANKREC_RECONCILIATION_COUNT,
    );
    for (const recon of recons) {
      expect(recon.status, "the seed signs no month off").not.toBe("COMPLETED");
    }

    // THE CORE'S OWN ACCOUNTS: the world's bank account among them.
    const bank = worldBank(accounts);

    // THE SCREEN SAYS THE SAME: no month is open for the account, and the desk offers the starting point because
    // no month has signed off. The starting point form is the desk's own answer to a box with nothing reconciled.
    await expect(page.locator("[data-empty-recons]"), "no month is open for the account").toBeVisible();
    await expect(page.locator("[data-recon-id]"), "no month row is shown").toHaveCount(0);
    await expect(page.locator("[data-cutover-form]"), "the desk offers the starting point").toBeVisible();
    await expect(page.locator("[data-cutover-closed]"), "the starting point is not closed off").toHaveCount(0);

    // The desk's account is the core's account: the card carries the core's own id, so the empty state above is
    // about this account's month and not about a box with no accounts at all.
    await expect(page.locator(`[data-account-id='${bank.id}']`), "the desk shows the core's bank account").toBeVisible();
    return;
  }

  // THE WORLD'S OWN MONTH.
  const bank = worldBank(accounts);
  const signedOff = reconOf(recons, SIGNED_OFF_BANK_MONTH);
  expect(signedOff.status, "the world signed the month off").toBe("COMPLETED");
  expect(signedOff.variance_cents, "a signed off month reads zero difference").toBe(SIGNED_OFF_VARIANCE_CENTS);
  expect(signedOff.completed_by ?? "", "the sign off names the person who signed").not.toBe("");
  expect(signedOff.completed_at ?? "", "the sign off carries the day it was signed").not.toBe("");

  // The world's open month is never signed off.
  const open = monthOf(openMonth);
  for (const recon of recons.filter((r) => r.year === open.year && r.month === open.month)) {
    expect(recon.status, `the world's open month ${openMonth} is never signed off`).toBe("OPEN");
  }

  // The screen says the same: signed off, balanced, and closed to changes.
  const row = page.locator(`[data-recon-id='${signedOff.id}']`);
  await expect(row).toContainText("Signed off");
  await expect(row).toContainText("Balanced");
  await expect(row.getByRole("button"), "a signed off month carries no buttons").toHaveCount(0);
  // The desk works on the core's bank account, so the signed off month is on the account the core named.
  await expect(page.locator(`[data-account-id='${bank.id}']`), "the desk shows the core's bank account").toBeVisible();
});

test("the month's figures on the screen are the core's own figures", async ({
  page,
  context,
  request,
}, testInfo) => {
  const headers = staffHeaders(page);
  const core = watchCore(page);
  await openDesk(page, context, testInfo.project.use.baseURL);
  const boxMonth = await readBoxMonth(request, await headers);

  const accounts = (await coreAnswer(core, "/api/v1/bankrec/accounts")) as BankRecAccount[];
  if (SIGNED_OFF_BANK_MONTH === "") {
    // THE CORE'S OWN ACCOUNTS, FIRST: a core that answers with nothing at all fails on this figure, here, and not
    // on a later wait for an answer the desk never asks for.
    expect(accounts.length, "the core lists the world's bank and card accounts").toBe(BANKREC_ACCOUNT_COUNT);
  }
  const recons = (await coreAnswer(core, "/api/v1/bankrec/reconciliations")) as BankRecReconciliation[];

  if (SIGNED_OFF_BANK_MONTH === "") {
    // TODAY'S SEED: THERE IS NO MONTH, SO THERE ARE NO MONTH'S FIGURES.
    //
    // The core holds no reconciliation, so the desk shows no report and no month row: a report for a month the
    // core does not hold would be a figure of the desk's own making.
    expect(recons.length, `the core holds no reconciliation at all (the box's own month is ${boxMonth})`).toBe(
      BANKREC_RECONCILIATION_COUNT,
    );
    await expect(page.locator("[data-recon-report]"), "no month's report is shown").toHaveCount(0);
    await expect(page.locator("[data-recon-id]"), "no month row is shown").toHaveCount(0);
    await expect(page.locator("[data-recon-card]"), "no month card is shown").toHaveCount(0);

    // THE FIGURES THE DESK DOES SHOW ARE THE CORE'S OWN: the account card's unmatched count and the account's
    // own name, both read from the core's own answer.
    const bank = worldBank(accounts);
    const card = page.locator(`[data-account-id='${bank.id}']`);
    await expect(card, "the desk shows the core's bank account").toBeVisible();
    await expect(card, "the desk shows the core's account name").toContainText(bank.name);
    await expect(card).toContainText(bank.unmatched_lines === 0 ? "All matched" : `${bank.unmatched_lines} lines unmatched`);
    return;
  }

  // THE WORLD'S OWN MONTH.
  const bank = worldBank(accounts);
  const signedOff = reconOf(recons, SIGNED_OFF_BANK_MONTH);

  // The reconciliation report: every figure the core answered with, the way the screen reads it.
  const report = page.locator(`[data-recon-report='${signedOff.id}']`);
  const adjusted = signedOff.adjusted_bank_balance_cents ?? signedOff.statement_balance_cents;
  await expect(report.locator("[data-report-statement]")).toHaveText(formatMoney(signedOff.statement_balance_cents));
  await expect(report.locator("[data-report-adjusted]")).toHaveText(formatMoney(adjusted));
  await expect(report.locator("[data-report-gl]")).toHaveText(formatMoney(signedOff.gl_balance_cents));
  await expect(report.locator("[data-report-matched]")).toHaveText(String(signedOff.matched_lines_count ?? 0));
  await expect(report.locator("[data-report-completed-at]"), "the report carries the day it was signed").toBeVisible();

  // The month's own row carries the same adjusted bank balance.
  const row = page.locator(`[data-recon-id='${signedOff.id}']`);
  await expect(row.locator("[data-row-adjusted]")).toHaveText(formatMoney(adjusted));

  // The account card's unmatched count is the core's own count.
  const card = page.locator(`[data-account-id='${bank.id}']`);
  await expect(card).toContainText(bank.unmatched_lines === 0 ? "All matched" : `${bank.unmatched_lines} lines unmatched`);
});

test("the statement lines and their matches are the core's, and the desk shows each match", async ({
  page,
  context,
  request,
}, testInfo) => {
  const headers = staffHeaders(page);
  const core = watchCore(page);
  await openDesk(page, context, testInfo.project.use.baseURL);
  const boxMonth = await readBoxMonth(request, await headers);

  // THE CORE'S OWN ACCOUNTS, FIRST: a core that answers with nothing at all fails on this figure, here, and not
  // on a later wait for an answer the desk never asks for (with no account the desk asks for no statement).
  const accounts = (await coreAnswer(core, "/api/v1/bankrec/accounts")) as BankRecAccount[];
  if (SIGNED_OFF_BANK_MONTH === "") {
    expect(accounts.length, "the core lists the world's bank and card accounts").toBe(BANKREC_ACCOUNT_COUNT);
  }

  await tab(page, "match");

  // The world's matched lines, the filter the desk offers for them.
  await page.locator("[data-action='br-filter'][data-filter-id='MATCHED']").click();
  // The core's answer to the filtered call: the query rides with the answer, so the lines the desk asked for with
  // the filter are the ones read here, never the unfiltered page that came before it.
  const lines = (await coreAnswer(core, "/api/v1/bankrec/lines", { status: "MATCHED" })) as BankRecStatementLinePage;

  // THE CORE'S OWN ANSWER, FIRST. The desk asked for the lines of one account, and the answer names that account:
  // the empty state below is about this account's statement and not about a box with no accounts at all.
  const asked = answerParams(core, "/api/v1/bankrec/lines", { status: "MATCHED" });
  const bank = worldBank(accounts);
  expect(asked.get("bank_account_id"), "the desk asked the core for the bank account's own statement").toBe(bank.id);

  if (SIGNED_OFF_BANK_MONTH === "") {
    // TODAY'S SEED: THE FINANCE SEED IMPORTS NO STATEMENT.
    //
    // The core's own answer: it holds no statement line for the account, so there is no line to match, no match
    // history and nothing unmatched. The answer to the filtered call is the one that says so.
    expect(lines.items.length, `the core holds no statement line (the box's own month is ${boxMonth})`).toBe(
      BANKREC_STATEMENT_LINE_COUNT,
    );
    expect(lines.next_cursor ?? null, "no more lines wait behind the page").toBeNull();

    // THE SCREEN SAYS THE SAME, in the desk's own words for the state that was asked for: the filter is on, so
    // the empty block is the filtered one, and it is not the unfiltered "import a statement first".
    const empty = page.locator("[data-empty-lines]");
    await expect(empty, "the desk holds no statement line").toBeVisible();
    await expect(empty, "the desk's own words for the filtered state").toContainText("Nothing is in this state");
    await expect(page.locator("[data-line-id]"), "no line row is shown").toHaveCount(0);
    await expect(page.locator("[data-action='br-more-lines']"), "no more lines wait behind the page").toHaveCount(0);
    return;
  }

  const rows = page.locator("[data-line-id]");
  await expect(rows).toHaveCount(lines.items.length);

  for (const line of lines.items) {
    const row = page.locator(`[data-line-id='${line.id}']`);
    await expect(row, "the line's own description").toContainText(line.description);
    await expect(row, "the line's own amount").toContainText(formatMoney(line.amount_cents));
    await expect(row, "the line's own state").toContainText("Matched");
  }

  // More lines wait behind the page the core answered with.
  await expect(page.locator("[data-action='br-more-lines']")).toHaveCount(lines.next_cursor === null ? 0 : 1);

  // One line's matches: the core's own match rows, the way the desk reads them.
  const matched = need(
    lines.items.find((l) => l.status === "MATCHED"),
    "the world's statement holds a matched line",
  );
  await page.locator(`[data-line-id='${matched.id}']`).getByRole("button").click();
  // The answer for this line's own matches, never another line's: the query rides with the answer.
  const matches = (await coreAnswer(core, "/api/v1/bankrec/matches", { statement_line_id: matched.id })) as BankRecMatch[];
  // A matched line carries at least one match, so a blank history and a blank answer cannot agree their way to a pass.
  expect(matches.length, "the core holds the matched line's own match").toBeGreaterThan(0);
  await expect(page.locator("[data-history] li")).toHaveCount(matches.length);
  for (const match of matches) {
    await expect(page.locator("[data-history]"), "the match's own amount").toContainText(formatMoney(match.amount_cents));
    await expect(page.locator("[data-history]"), "the match's own method").toContainText(matchMethodWord(match.method));
    await expect(page.locator("[data-history]"), "the match's own target").toContainText(targetWord(match.target_type));
    await expect(page.locator("[data-history]"), "the match's own state").toContainText(
      match.unmatched_at === null || match.unmatched_at === undefined ? "in force" : "undone",
    );
  }
});
