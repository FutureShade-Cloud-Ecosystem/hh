// The Money Desk's bank reconciliation against the REAL core (W2-4 (d), wave 2).
//
// WHAT THIS SPEC IS: the Financial Controller's bank reconciliation area on a real core box, read the way the
// controller reads it. The month the world signed off reads zero difference, the statement lines and their matches
// are the core's own, and the month's sign off state is closed. Every money figure the screen shows is held
// against the core's own answer for that figure.
//
// THE ASSUMPTIONS ABOUT (a) (the harness, W2-4-HARNESS-INTERFACE-r1.md). Each one names what the fold must hold,
// so a mismatch shows up there and not in the run:
//  1. ./session exports asStaff(context, who) and coreURL(), as the interface says. asStaff puts the world's named
//     staff user of that role on the browser context, so the next page.goto lands signed in on the real core.
//     This spec signs in as the Financial Controller, the role the bank area is for.
//  2. ./world exports the world's known figures as named constants, each with a one line comment. This spec reads
//     the world's signed off month, its open month and its zero difference from there, and hard codes no figure of
//     its own.
//  3. The box serves the Money Desk at /money-desk/ (deploy/demo/Caddyfile.template:139, deploy/finance/
//     Caddyfile.template:67), and the config's base URL names the app's own path, so this spec goes to "./" and
//     stays under it. If (a)'s config names the box's root instead, the spec tries the box's own path too.
//  4. The core's API answers this spec needs are the ones the desk itself asks for: GET /api/v1/bankrec/accounts,
//     GET /api/v1/bankrec/reconciliations, GET /api/v1/bankrec/lines and GET /api/v1/bankrec/matches. This spec
//     reads them from the answers the desk received, so it needs no bearer of its own and holds to whichever
//     session (a)'s helper makes. If (a)'s helper hands the spec a token, the same figures can be read straight
//     from coreURL().
//  5. The world's anchor month is 2026-09 and the world closes every month before it (the wave 2 round 2 notes:
//     seed 1, anchor 2026-09), so the month signed off at zero difference is the one before the anchor.
//
// THE RED: this spec runs under the stub and fails there, because the stub's world is not the core's. The stub
// holds no reconciliation at all, so the signed off month is not there to read.
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
import { OPEN_MONTH, SIGNED_OFF_BANK_MONTH, SIGNED_OFF_VARIANCE_CENTS } from "./world.js";

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
      await expect(page.locator("[data-account-id]").first()).toBeVisible();
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

/** The core's own reconciliation for a month of the account. */
function reconOf(recons: BankRecReconciliation[], month: string): BankRecReconciliation {
  const { year, month: number } = monthOf(month);
  return need(
    recons.find((r) => r.year === year && r.month === number),
    `the core holds the month's reconciliation for ${month}`,
  );
}

test("the month the world signed off reads zero difference, and the desk says so", async ({ page, context }, testInfo) => {
  const core = watchCore(page);
  await openDesk(page, context, testInfo.project.use.baseURL);

  const accounts = (await coreAnswer(core, "/api/v1/bankrec/accounts")) as BankRecAccount[];
  const bank = bankAccount(accounts);
  const recons = (await coreAnswer(core, "/api/v1/bankrec/reconciliations")) as BankRecReconciliation[];

  // The core's own answer: the month is signed off, and it reads zero difference.
  const signedOff = reconOf(recons, SIGNED_OFF_BANK_MONTH);
  expect(signedOff.status, "the world signed the month off").toBe("COMPLETED");
  expect(signedOff.variance_cents, "a signed off month reads zero difference").toBe(SIGNED_OFF_VARIANCE_CENTS);
  expect(signedOff.completed_by ?? "", "the sign off names the person who signed").not.toBe("");
  expect(signedOff.completed_at ?? "", "the sign off carries the day it was signed").not.toBe("");

  // The world's open month is never signed off.
  const open = monthOf(OPEN_MONTH);
  for (const recon of recons.filter((r) => r.year === open.year && r.month === open.month)) {
    expect(recon.status, `the world's open month ${OPEN_MONTH} is never signed off`).toBe("OPEN");
  }

  // The screen says the same: signed off, balanced, and closed to changes.
  const row = page.locator(`[data-recon-id='${signedOff.id}']`);
  await expect(row).toContainText("Signed off");
  await expect(row).toContainText("Balanced");
  await expect(row.getByRole("button"), "a signed off month carries no buttons").toHaveCount(0);
});

test("the month's figures on the screen are the core's own figures", async ({ page, context }, testInfo) => {
  const core = watchCore(page);
  await openDesk(page, context, testInfo.project.use.baseURL);

  const accounts = (await coreAnswer(core, "/api/v1/bankrec/accounts")) as BankRecAccount[];
  const bank = bankAccount(accounts);
  const recons = (await coreAnswer(core, "/api/v1/bankrec/reconciliations")) as BankRecReconciliation[];
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

test("the statement lines and their matches are the core's, and the desk shows each match", async ({ page, context }, testInfo) => {
  const core = watchCore(page);
  await openDesk(page, context, testInfo.project.use.baseURL);
  await tab(page, "match");

  // The world's matched lines, the filter the desk offers for them.
  await page.locator("[data-action='br-filter'][data-filter-id='MATCHED']").click();
  // The core's answer to the filtered call: the query rides with the answer, so the lines the desk asked for with
  // the filter are the ones read here, never the unfiltered page that came before it.
  const lines = (await coreAnswer(core, "/api/v1/bankrec/lines", { status: "MATCHED" })) as BankRecStatementLinePage;
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
