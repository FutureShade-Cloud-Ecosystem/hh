// The bank reconciliation slice's screens, as plain render functions of a model. They hold no state and own no
// events: every control is a plain element carrying a data-action name and the slice's click listener acts on it
// (the design system's position rules allow no @ or . binding in an app, and the only bound values are class,
// role, aria- and data- attributes). Form fields carry data-field names; the desk reads them as they change.
//
// Every figure on a screen is the server's, formatted: the app computes no money. Visual reference: the Money Desk
// boards (trial-balance, controller-home) for the frame, the panels and the pills, and the original app's
// bank reconciliation screens (the bankrec files under app/src: the main screen, import, match and rules)
// for what each screen holds.
import { html, nothing, type TemplateResult } from "lit";
import type { BankRecAccount, BankRecCutoverPreview, BankRecMatch, BankRecMatchRule, BankRecReconciliation, BankRecStatementLine, GlJournal } from "@hardscapehouse/api-client";
import type { LineStatus, TargetType } from "./bankrec-api.js";
import {
  accountTypeWord,
  dayWords,
  isUuid,
  lineAction,
  lineStatusWord,
  lineTone,
  matchMethodWord,
  periodWords,
  reconStatusWord,
  reconTone,
  RULE_KINDS,
  ruleKindWord,
  ruleStatusWord,
  ruleTone,
  targetWord,
  varianceWords,
  type Tone,
} from "./bankrec-logic.js";
import { formatMoney, parseBalanceToCents } from "./bankrec-money.js";

export type BrTab = "accounts" | "import" | "match" | "rules";

export type ImportView =
  | { readonly kind: "ok"; readonly filename: string; readonly parsed: number; readonly inserted: number; readonly duplicates: number }
  | { readonly kind: "failed"; readonly words: string };

export interface BankrecModel {
  readonly phase: "loading" | "ready" | "error";
  readonly tab: BrTab;
  readonly accounts: readonly BankRecAccount[];
  readonly accountId: string;
  readonly recons: readonly BankRecReconciliation[];
  readonly confirmSignOff: string | null;
  readonly balanceText: string;
  readonly importView: ImportView | null;
  readonly lines: readonly BankRecStatementLine[];
  readonly linesNext: string | null;
  readonly linesBusy: boolean;
  readonly lineFilter: LineStatus | "";
  readonly selectedLine: string | null;
  readonly history: readonly BankRecMatch[] | null;
  readonly confirmUndo: boolean;
  readonly targetType: TargetType;
  readonly journals: readonly GlJournal[];
  readonly rules: readonly BankRecMatchRule[];
  readonly rulesLoaded: boolean;
  readonly linesLoaded: boolean;
  readonly ruleKind: string;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
  readonly userId?: string;
  readonly userName?: string;
  // The starting point form (the Controller's cut over door, round 3): what is typed, what is ticked, and the
  // core's preview read for the typed date (the ledger's entries and the book balance, the server's figures).
  readonly cutoverDateText: string;
  readonly cutoverBalanceText: string;
  readonly cutoverTicked: Readonly<Record<string, boolean>>;
  readonly cutoverPreview: BankRecCutoverPreview | null;
  readonly cutoverPreviewLoading: boolean;
}

const MONTH_NAMES = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

const TARGETS: ReadonlyArray<{ id: TargetType; title: string; line: string; idLabel: string }> = [
  { id: "PAYMENT", title: "Payment", line: "A card or received payment. Posts the clearing entry.", idLabel: "Payment id" },
  { id: "LODGEMENT", title: "Lodgement", line: "A banked cash or cheque lodgement. Posts the clearing entry.", idLabel: "Lodgement id" },
  { id: "JOURNAL", title: "Journal", line: "A journal already in the ledger. Posts nothing new.", idLabel: "Journal" },
  { id: "GROUP", title: "Group", line: "A hand grouping of several items. Posts nothing new.", idLabel: "Group id (optional)" },
];

const FILTERS: ReadonlyArray<{ id: LineStatus | ""; word: string }> = [
  { id: "UNMATCHED", word: "Unmatched" },
  { id: "MATCHED", word: "Matched" },
  { id: "EXCLUDED", word: "Excluded" },
  { id: "", word: "All" },
];

function pill(tone: Tone, word: string): TemplateResult {
  return html`<span class="br-status br-status-${tone}">${word}</span>`;
}

/** A feature the core serves that this increment does not draw yet, said in words. */
function later(word: string): TemplateResult {
  return html`<span class="br-later">${word}</span>`;
}

function accountOf(model: BankrecModel): BankRecAccount | undefined {
  return model.accounts.find((a) => a.id === model.accountId);
}

// ---- the starting point (the Controller's cut over door, round 3) ---------------------------------------

/**
 * The recorded starting point of the chosen account, or null when none is recorded. Read from the account
 * itself, so it is never a second copy of the core's fact.
 */
function recordedOf(account: BankRecAccount | undefined): { date: string; balanceCents: number; journalIds: readonly string[] } | null {
  if (account === undefined || account.cutover_date === undefined || account.cutover_date === null || account.cutover_date === "") return null;
  return {
    date: account.cutover_date,
    balanceCents: account.cutover_statement_balance_cents ?? 0,
    journalIds: account.cutover_outstanding_journal_ids ?? [],
  };
}

/**
 * The starting point can no longer change: the core refuses a re-record once the cut over month or any later
 * month has signed off, so the screen holds back the form exactly when the server would refuse the write.
 */
function lockedOf(account: BankRecAccount | undefined, recons: readonly BankRecReconciliation[]): boolean {
  const recorded = recordedOf(account);
  if (recorded === null) return false;
  const year = Number(recorded.date.slice(0, 4));
  const month = Number(recorded.date.slice(5, 7));
  return recons.some((r) => r.status === "COMPLETED" && (r.year > year || (r.year === year && r.month >= month)));
}

/** The posted ledger entries on the account's GL code on or before the typed cut over date, oldest first: the core's preview read, never a desk computation. */
function cutoverEntriesOf(model: BankrecModel): readonly { journal_id: string; posted_at: string; memo: string; amount_cents: number }[] {
  return model.cutoverPreview?.entries ?? [];
}

function cutoverRecordedView(model: BankrecModel): TemplateResult {
  const account = accountOf(model);
  const recorded = recordedOf(account);
  if (recorded === null) return html`${nothing}`;
  const named = model.journals.filter((j) => recorded.journalIds.includes(j.id));
  return html`
    <div class="br-card" data-cutover-recorded>
      <div class="br-card-top"><strong>Starting point</strong>${pill("good", "Recorded")}</div>
      <dl class="br-facts">
        <dt>Cut over date</dt><dd data-cutover-recorded-date>${dayWords(recorded.date)}</dd>
        <dt>Statement balance that day</dt><dd data-cutover-recorded-balance>${formatMoney(recorded.balanceCents)}</dd>
        <dt>Still outstanding at cut over</dt><dd data-cutover-recorded-count>${recorded.journalIds.length === 0 ? "None" : `${recorded.journalIds.length} ${recorded.journalIds.length === 1 ? "entry" : "entries"}`}</dd>
      </dl>
      ${named.length > 0
        ? html`<ul class="br-history" aria-label="Entries still outstanding at cut over" data-cutover-recorded-list>
            ${named.map((j) => html`<li>${j.memo}</li>`)}
          </ul>`
        : nothing}
      <p class="br-note">The months after this point reconcile from here. A month has signed off, so this starting point no longer changes.</p>
    </div>
  `;
}

function cutoverPanel(model: BankrecModel): TemplateResult {
  const account = accountOf(model);
  if (account === undefined) return html`${nothing}`;
  const locked = lockedOf(account, model.recons);
  const recorded = recordedOf(account);
  return html`
    <section class="br-panel" aria-label="Set the starting point" data-cutover>
      <div class="br-panel-head"><h3>Set the starting point</h3></div>
      ${recorded === null
        ? html`<p class="br-dim">The point reconciliation starts from: the day the dealer's books moved onto this ledger, and the statement balance on that day. Entries on or before it are not outstanding items unless they are ticked.</p>`
        : nothing}
      ${cutoverRecordedView(model)}
      ${locked
        ? nothing
        : html`
            <div class="br-form" role="group" aria-label="Set the starting point" data-cutover-form>
              <label class="br-field">Cut over date
                <input type="date" data-field="br-cutover-date" autocomplete="off" aria-label="Cut over date" />
              </label>
              <label class="br-field">Statement balance on that date
                <input type="text" inputmode="decimal" data-field="br-cutover-balance" autocomplete="off" placeholder="Such as 25,255.75" aria-label="Statement balance on the cut over date" />
                <span class="br-hint">Dollars and cents, from the statement of the day the books moved onto this ledger.</span>
              </label>
            </div>
            ${model.cutoverDateText === ""
              ? html`<p class="br-hint" data-cutover-await-date>Type the cut over date to list the ledger's entries on or before it.</p>`
              : model.cutoverPreviewLoading
                ? html`<p class="br-dim" role="status" data-loading-cutover-entries>Reading the ledger's entries.</p>`
                : cutoverEntriesList(model)}
            ${cutoverResultView(model)}
            <div class="br-actions">
              <button type="button" class="btn btn-primary" data-action="br-cutover-save" ?disabled=${model.busy}>Save the starting point</button>
            </div>
            <p class="br-note">Saving records the starting point for this account. Only the Financial Controller can record it, and once a month has signed off it can no longer change.</p>
          `}
    </section>
  `;
}

function cutoverEntriesList(model: BankrecModel): TemplateResult {
  const entries = cutoverEntriesOf(model);
  if (entries.length === 0) {
    return html`<p class="br-dim" data-cutover-no-entries>No ledger entries on or before that date.</p>`;
  }
  return html`
    <ul class="br-history" aria-label="The ledger's entries on or before the cut over date" data-cutover-entries>
      ${entries.map(
        (e) => html`<li>
          <label>
            <input type="checkbox" data-field="br-cutover-tick" data-id=${e.journal_id} data-cutover-tick ?checked=${model.cutoverTicked[e.journal_id] === true} />
            <span>${dayWords(e.posted_at)} · ${e.memo}</span>
          </label>
          <span class="br-card-amount">${formatMoney(e.amount_cents)}</span>
        </li>`,
      )}
    </ul>
    <p class="br-hint">Tick an entry only if it was still outstanding at cut over: unticked entries on or before this date are never listed as outstanding items later.</p>
  `;
}

function cutoverResultView(model: BankrecModel): TemplateResult {
  if (model.cutoverDateText === "") return html`${nothing}`;
  const entries = cutoverEntriesOf(model);
  const ticked = entries.filter((e) => model.cutoverTicked[e.journal_id] === true);
  const typed = model.cutoverBalanceText.trim();
  const statement = typed === "" ? null : parseBalanceToCents(typed);
  return html`
    <div class="br-card" data-cutover-result>
      <div class="br-card-top"><strong>What saving records</strong></div>
      <dl class="br-facts">
        <dt>Book balance at cut over</dt>
        <dd data-cutover-book>${model.cutoverPreview === null ? "Reading the ledger." : formatMoney(model.cutoverPreview.book_balance_cents)}</dd>
        <dt>Statement balance that day</dt>
        <dd data-cutover-statement>${statement === null ? "Type the statement balance." : formatMoney(statement)}</dd>
      </dl>
      ${ticked.length === 0
        ? html`<p class="br-hint" data-cutover-none>Nothing is ticked as still outstanding.</p>`
        : html`<ul class="br-history" aria-label="Entries ticked as still outstanding" data-cutover-ticked>
            ${ticked.map((e) => html`<li>${e.memo} · ${formatMoney(e.amount_cents)}</li>`)}
          </ul>`}
    </div>
  `;
}

function messages(model: BankrecModel): TemplateResult {
  return html`
    ${model.error !== "" ? html`<p class="br-alert br-alert-error" role="alert" data-error>${model.error}</p>` : nothing}
    ${model.notice !== "" ? html`<p class="br-alert br-alert-ok" role="status" data-notice>${model.notice}</p>` : nothing}
  `;
}

function accountSelect(model: BankrecModel): TemplateResult {
  return html`
    <label class="br-field">Account
      <select data-field="br-account" aria-label="Account">${model.accounts.map((a) => html`<option data-id=${a.id}>${a.name}</option>`)}</select>
    </label>
  `;
}

// ---- the way in ---------------------------------------------------------------------------------------

function loading(): TemplateResult {
  return html`<p class="br-dim" role="status" data-loading>Loading the bank accounts.</p>`;
}

function loadFailed(model: BankrecModel): TemplateResult {
  return html`
    <p class="br-alert br-alert-error" role="alert" data-error>${model.error}</p>
    <button type="button" class="btn btn-secondary" data-action="br-reload">Try again</button>
  `;
}

function tabs(model: BankrecModel): TemplateResult {
  const tab = (id: BrTab, label: string): TemplateResult => html`
    <button type="button" class="br-tab" data-action="br-tab" data-tab=${id} data-tab-id=${id} aria-current="${model.tab === id ? "true" : "false"}">${label}</button>
  `;
  return html`
    <nav class="br-tabs" aria-label="Bank reconciliation views">
      ${tab("accounts", "Accounts and month end")} ${tab("import", "Import")} ${tab("match", "Match")} ${tab("rules", "Rules")}
    </nav>
  `;
}

// ---- accounts and month end ---------------------------------------------------------------------------

function accountCard(model: BankrecModel, a: BankRecAccount): TemplateResult {
  const clear = a.unmatched_lines === 0;
  const unmatched = a.unmatched_lines === 1 ? "1 line unmatched" : `${a.unmatched_lines} lines unmatched`;
  return html`
    <li>
      <button type="button" class="br-account ${clear ? "br-account-clear" : "br-account-open"}" data-action="br-pick-account" data-id=${a.id} data-account-id=${a.id} aria-current="${model.accountId === a.id ? "true" : "false"}">
        <span class="br-account-main">
          <span class="br-account-name">${a.name}</span>
          <span class="br-account-meta">${accountTypeWord(a.account_type)} · GL ${a.gl_account_code} · ending ${a.last4}</span>
          <span class="br-account-meta">${a.institution}</span>
          ${a.tracking_only ? html`<span><span class="br-tag" data-tracking-only>Tracking only</span></span>` : nothing}
        </span>
        ${pill(clear ? "good" : "watch", clear ? "All matched" : unmatched)}
      </button>
    </li>
  `;
}

function monthForm(model: BankrecModel): TemplateResult {
  const typed = model.balanceText.trim();
  const bad = typed !== "" && parseBalanceToCents(typed) === null;
  return html`
    <div class="br-form" role="group" aria-label="Open a month">
      <label class="br-field">Month
        <select data-field="br-month" aria-label="Month">${MONTH_NAMES.map((name, i) => html`<option data-id=${String(i + 1)}>${name}</option>`)}</select>
      </label>
      <label class="br-field">Year
        <input type="text" inputmode="numeric" data-field="br-year" autocomplete="off" aria-label="Year" />
      </label>
      <label class="br-field br-field-wide">Closing balance on the statement
        <input type="text" inputmode="decimal" data-field="br-balance" autocomplete="off" placeholder="Such as 25,255.75" aria-label="Closing balance on the statement" />
        ${bad
          ? html`<span class="br-hint br-hint-bad" role="alert" data-balance-hint>Enter dollars and cents, such as 25,255.75. A negative balance has a minus sign or brackets.</span>`
          : html`<span class="br-hint">Dollars and cents, from the last line of the statement. The ledger side comes from the core.</span>`}
      </label>
    </div>
  `;
}

function reconActions(model: BankrecModel, r: BankRecReconciliation): TemplateResult {
  if (r.status !== "OPEN") return html`${nothing}`;
  if (model.confirmSignOff === r.id) {
    return html`
      <span class="br-actions">
        <button type="button" class="btn btn-primary" data-action="br-confirm-sign-off" data-id=${r.id} ?disabled=${model.busy}>Confirm sign off</button>
        <button type="button" class="btn btn-secondary" data-action="br-cancel-sign-off">Keep open</button>
      </span>
    `;
  }
  const held = r.variance_cents !== 0;
  return html`
    <span class="br-actions">
      <button type="button" class="btn btn-secondary" data-action="br-refresh-month" data-id=${r.id} ?disabled=${model.busy}>Refresh figures</button>
      <button type="button" class="btn btn-primary" data-action="br-sign-off" data-id=${r.id} ?disabled=${held || model.busy} data-sign-off=${r.id}>Sign off</button>
    </span>
    ${held ? html`<span class="br-hint" data-held>Held: the statement and the ledger must agree first.</span>` : nothing}
  `;
}

function signedOffByWords(model: BankrecModel, completedBy: string | null | undefined): string {
  if (completedBy === undefined || completedBy === null || completedBy === "") return "";
  if (!isUuid(completedBy)) return completedBy;
  if (model.userId !== undefined && model.userId !== "" && completedBy === model.userId && model.userName !== undefined && model.userName !== "") {
    return model.userName;
  }
  return "";
}

function reconReport(model: BankrecModel, r: BankRecReconciliation): TemplateResult {
  const dit = r.deposits_in_transit ?? [];
  const oc = r.outstanding_cheques ?? [];
  const ditCents = r.deposits_in_transit_cents ?? 0;
  const ocCents = r.outstanding_cheques_cents ?? 0;
  const adjusted = r.adjusted_bank_balance_cents ?? r.statement_balance_cents;
  const signedBy = signedOffByWords(model, r.completed_by);
  return html`
    <div class="br-card" data-recon-report=${r.id}>
      <div class="br-card-top">
        <strong>Reconciliation report · ${periodWords(r.year, r.month)}</strong>
        ${pill(reconTone(r.status), reconStatusWord(r.status))}
      </div>
      <dl class="br-facts">
        ${r.opening_balance_cents !== undefined && r.opening_balance_cents !== null
          ? html`<dt>Opening statement</dt><dd data-report-opening>${formatMoney(r.opening_balance_cents)}</dd>`
          : nothing}
        <dt>Closing statement</dt><dd data-report-statement>${formatMoney(r.statement_balance_cents)}</dd>
        <dt>Deposits in transit</dt><dd data-report-dit>${formatMoney(ditCents)}</dd>
        <dt>Outstanding cheques</dt><dd data-report-oc>${formatMoney(ocCents)}</dd>
        <dt>Adjusted bank balance</dt><dd data-report-adjusted>${formatMoney(adjusted)}</dd>
        <dt>Closing ledger</dt><dd data-report-gl>${formatMoney(r.gl_balance_cents)}</dd>
        <dt>Matched lines</dt><dd data-report-matched>${r.matched_lines_count ?? 0}</dd>
        ${signedBy !== ""
          ? html`<dt>Signed off by</dt><dd data-report-completed-by>${signedBy}</dd>`
          : nothing}
        ${r.completed_at !== undefined && r.completed_at !== null
          ? html`<dt>Signed off</dt><dd data-report-completed-at>${dayWords(r.completed_at)}</dd>`
          : nothing}
      </dl>
      ${dit.length > 0
        ? html`<ul class="br-history" aria-label="Deposits in transit" data-dit-list>
            ${dit.map((item) => html`<li>Deposit in transit · ${dayWords(item.posted_at)} · ${item.memo} · ${formatMoney(item.amount_cents)}</li>`)}
          </ul>`
        : nothing}
      ${oc.length > 0
        ? html`<ul class="br-history" aria-label="Outstanding cheques" data-oc-list>
            ${oc.map((item) => html`<li>Outstanding cheque · ${dayWords(item.posted_at)} · ${item.memo} · ${formatMoney(item.amount_cents)}</li>`)}
          </ul>`
        : nothing}
    </div>
  `;
}

function reconRows(model: BankrecModel): TemplateResult {
  if (model.recons.length === 0) {
    return html`<div class="br-empty" data-empty-recons><strong>No month is open for this account.</strong><span class="br-dim">Open a month with the closing balance on its statement.</span></div>`;
  }
  return html`
    <div class="br-scroll br-hide-narrow">
      <table aria-label="Months for this account">
        <thead><tr><th>Month</th><th class="num">Statement</th><th class="num">Adjusted bank</th><th class="num">Ledger</th><th>Difference</th><th></th></tr></thead>
        <tbody>
          ${model.recons.map((r) => {
            const v = varianceWords(r.variance_cents);
            const adjusted = r.adjusted_bank_balance_cents ?? r.statement_balance_cents;
            return html`<tr data-recon-id=${r.id}>
              <td><strong>${periodWords(r.year, r.month)}</strong><span class="br-sub">${pill(reconTone(r.status), reconStatusWord(r.status))}</span></td>
              <td class="num">${formatMoney(r.statement_balance_cents)}</td>
              <td class="num" data-row-adjusted>${formatMoney(adjusted)}</td>
              <td class="num">${formatMoney(r.gl_balance_cents)}</td>
              <td>${pill(v.tone, v.word)}</td>
              <td>${reconActions(model, r)}</td>
            </tr>`;
          })}
        </tbody>
      </table>
    </div>
    <ul class="br-cards" aria-label="Months for this account, as cards">
      ${model.recons.map((r) => {
        const v = varianceWords(r.variance_cents);
        const adjusted = r.adjusted_bank_balance_cents ?? r.statement_balance_cents;
        return html`<li class="br-card" data-recon-card=${r.id}>
          <div class="br-card-top"><strong>${periodWords(r.year, r.month)}</strong>${pill(reconTone(r.status), reconStatusWord(r.status))}</div>
          <dl class="br-facts">
            <dt>Statement</dt><dd>${formatMoney(r.statement_balance_cents)}</dd>
            <dt>Adjusted bank</dt><dd data-card-adjusted>${formatMoney(adjusted)}</dd>
            <dt>Ledger</dt><dd>${formatMoney(r.gl_balance_cents)}</dd>
            <dt>Difference</dt><dd>${pill(v.tone, v.word)}</dd>
          </dl>
          ${reconActions(model, r)}
        </li>`;
      })}
    </ul>
    ${model.recons.map((r) => reconReport(model, r))}
  `;
}

function accountsView(model: BankrecModel): TemplateResult {
  const account = accountOf(model);
  return html`
    <div class="br-split br-split-month" data-br-accounts>
      <section class="br-panel" aria-label="Accounts">
        <div class="br-panel-head"><h3>Accounts</h3></div>
        ${model.accounts.length === 0
          ? html`<div class="br-empty" data-empty-accounts><strong>No bank or card accounts are set up.</strong><span class="br-dim">Reconciliation runs per account. The core lists them here once an administrator adds them.</span></div>`
          : html`<ul class="br-list" aria-label="Bank and card accounts">${model.accounts.map((a) => accountCard(model, a))}</ul>`}
      </section>
      <section class="br-panel" aria-label="Month end" data-month-end>
        <div class="br-panel-head"><h3>Month end${account === undefined ? "" : html` · ${account.name}`}</h3></div>
        ${account === undefined
          ? html`<p class="br-dim">Pick an account to open a month.</p>`
          : html`
              ${monthForm(model)}
              <div class="br-actions">
                <button type="button" class="btn btn-primary" data-action="br-open-month" ?disabled=${model.busy}>Open month</button>
              </div>
              <p class="br-note" data-month-note>The difference is the adjusted bank balance less the ledger. It is worked out when you open or refresh the month, so refresh the figures after you match. A month signs off only when no line is unmatched and the difference is zero.</p>
              ${reconRows(model)}
              ${account.tracking_only ? html`<p class="br-dim br-later-line">Card spend is tracked here and not yet booked to the ledger: ${later("later")}</p>` : nothing}
            `}
      </section>
    </div>
    ${cutoverPanel(model)}
  `;
}

// ---- import -------------------------------------------------------------------------------------------

function importResult(view: ImportView | null): TemplateResult {
  if (view === null) return html`${nothing}`;
  if (view.kind === "failed") {
    return html`<p class="br-alert br-alert-error" role="alert" data-import-failed>${view.words}</p>`;
  }
  const nothingNew = view.inserted === 0;
  return html`
    <div class="br-alert ${nothingNew ? "br-alert-watch" : "br-alert-ok"}" role="status" data-import-result>
      <strong>${nothingNew ? "Nothing new: every line was already here." : "Imported."}</strong>
      <dl class="br-facts" aria-label="Import result">
        <dt>File</dt><dd>${view.filename}</dd>
        <dt>Lines read</dt><dd data-parsed>${view.parsed}</dd>
        <dt>New lines</dt><dd data-inserted>${view.inserted}</dd>
        <dt>Already here</dt><dd data-duplicates>${view.duplicates}</dd>
      </dl>
      <button type="button" class="btn btn-secondary" data-action="br-go-match">Go to matching</button>
    </div>
  `;
}

function importView(model: BankrecModel): TemplateResult {
  const account = accountOf(model);
  const card = account?.account_type === "CREDIT_CARD";
  return html`
    <section class="br-panel" aria-label="Import a statement" data-br-import>
      <div class="br-panel-head"><h3>Import a statement</h3></div>
      <div class="br-toolbar">${accountSelect(model)}</div>
      <label class="br-field">Choose the statement file
        <input type="file" accept=".csv,text/csv,text/plain" data-field="br-file" aria-label="Choose the statement file" />
      </label>
      <label class="br-field">Or paste the rows
        <textarea data-field="br-csv" rows="8" spellcheck="false" aria-label="Statement rows" placeholder="Date,Description,Amount,Reference"></textarea>
        <span class="br-hint">The first row names the columns: ${card ? "Transaction Date" : "Date"}, Description, Amount, Reference. Dates are year, month, day. A credit is positive; a debit has a minus sign or brackets. Importing the same lines again adds nothing.</span>
      </label>
      <div class="br-actions">
        <button type="button" class="btn btn-primary" data-action="br-import" ?disabled=${model.busy || account === undefined}>Import statement</button>
        <button type="button" class="btn btn-secondary" data-action="br-import-clear">Clear</button>
      </div>
      ${importResult(model.importView)}
      <p class="br-dim br-later-line" data-later-import>Uploading the file to FB Console Storage, and the bank feed, are not in this increment: ${later("later")}</p>
    </section>
  `;
}

// ---- matching -----------------------------------------------------------------------------------------

function lineAmount(line: BankRecStatementLine): TemplateResult {
  return html`${formatMoney(line.amount_cents)}`;
}

function lineAction_(model: BankrecModel, line: BankRecStatementLine): TemplateResult {
  const action = lineAction(line.status);
  const selected = model.selectedLine === line.id;
  if (action === "match") {
    return html`<button type="button" class="br-link" data-action="br-select-line" data-id=${line.id} aria-pressed="${selected ? "true" : "false"}">Match by hand</button>`;
  }
  if (action === "unmatch") {
    return html`<button type="button" class="br-link" data-action="br-select-line" data-id=${line.id} aria-pressed="${selected ? "true" : "false"}">Open the match</button>`;
  }
  return html`<span class="br-dim">Nothing to do</span>`;
}

function linesList(model: BankrecModel): TemplateResult {
  if (!model.linesLoaded) return html`<p class="br-dim" role="status" data-loading-lines>Reading the statement lines.</p>`;
  if (model.lines.length === 0) {
    return html`<div class="br-empty" data-empty-lines><strong>No lines here.</strong><span class="br-dim">${model.lineFilter === "" ? "Import a statement for this account first." : "Nothing is in this state. Change the filter, or import a statement."}</span></div>`;
  }
  return html`
    <div class="br-scroll br-hide-narrow">
      <table aria-label="Statement lines">
        <thead><tr><th>Date</th><th>Description</th><th class="num">Amount</th><th>State</th><th></th></tr></thead>
        <tbody>
          ${model.lines.map(
            (l) => html`<tr data-line-id=${l.id} aria-current="${model.selectedLine === l.id ? "true" : "false"}">
              <td class="br-nowrap">${dayWords(l.txn_date)}</td>
              <td>${l.description}${l.reference !== "" ? html`<span class="br-sub br-mono">${l.reference}</span>` : nothing}</td>
              <td class="num">${lineAmount(l)}</td>
              <td>${pill(lineTone(l.status), lineStatusWord(l.status))}</td>
              <td>${lineAction_(model, l)}</td>
            </tr>`,
          )}
        </tbody>
      </table>
    </div>
    <ul class="br-cards" aria-label="Statement lines, as cards">
      ${model.lines.map(
        (l) => html`<li class="br-card" data-line-card=${l.id} aria-current="${model.selectedLine === l.id ? "true" : "false"}">
          <div class="br-card-top"><span>${dayWords(l.txn_date)}</span><span class="br-card-amount">${lineAmount(l)}</span></div>
          <div>${l.description}${l.reference !== "" ? html`<span class="br-sub br-mono">${l.reference}</span>` : nothing}</div>
          <div class="br-actions">${pill(lineTone(l.status), lineStatusWord(l.status))} ${lineAction_(model, l)}</div>
        </li>`,
      )}
    </ul>
    ${model.linesNext === null
      ? html`<p class="br-dim" data-lines-end>${model.lines.length === 1 ? "1 line shown." : `${model.lines.length} lines shown.`}</p>`
      : html`<div class="br-actions"><button type="button" class="btn btn-secondary" data-action="br-more-lines" ?disabled=${model.linesBusy}>Show more lines</button></div>`}
  `;
}

function historyList(model: BankrecModel): TemplateResult {
  if (model.history === null) return html`<p class="br-dim" role="status">Reading the match history.</p>`;
  if (model.history.length === 0) return html`<p class="br-dim" data-history-empty>This line has never been matched.</p>`;
  return html`
    <ul class="br-history" aria-label="Match history" data-history>
      ${model.history.map(
        (m) => html`<li>${matchMethodWord(m.method)} · ${targetWord(m.target_type)} · ${formatMoney(m.amount_cents)} · ${m.unmatched_at === undefined || m.unmatched_at === null ? html`<strong>in force</strong>` : html`undone ${dayWords(m.unmatched_at)}`}</li>`,
      )}
    </ul>
  `;
}

function manualForm(model: BankrecModel, line: BankRecStatementLine): TemplateResult {
  const target = TARGETS.find((t) => t.id === model.targetType) ?? TARGETS[0];
  return html`
    <p class="br-fact"><strong>Match this line by hand.</strong> Pick what it clears against.</p>
    <div class="br-choices" role="radiogroup" aria-label="What the line clears against">
      ${TARGETS.map(
        (t) => html`<button type="button" class="br-choice" role="radio" data-action="br-target" data-target=${t.id} aria-checked="${model.targetType === t.id ? "true" : "false"}">
          <span class="br-choice-title">${t.title}</span><span class="br-choice-line">${t.line}</span>
        </button>`,
      )}
    </div>
    ${model.targetType === "JOURNAL"
      ? html`<label class="br-field">Journal
          <select data-field="br-journal" aria-label="Journal">
            <option data-id="">Pick a posted journal</option>
            ${model.journals.map((j) => html`<option data-id=${j.id}>JE-${String(j.entry_no).padStart(4, "0")} · ${j.memo}</option>`)}
          </select>
          ${model.journals.length === 0 ? html`<span class="br-hint">No posted journals were found. Post one in the ledger first.</span>` : nothing}
        </label>`
      : html`<label class="br-field">${target?.idLabel ?? "Id"}
          <input type="text" data-field="br-target" autocomplete="off" aria-label="${target?.idLabel ?? "Id"}" placeholder="Paste the id" />
          <span class="br-hint">The core takes the id as it is shown on the payment, lodgement or group.</span>
        </label>`}
    <div class="br-actions">
      <button type="button" class="btn btn-primary" data-action="br-match-line" data-id=${line.id} ?disabled=${model.busy}>Match line</button>
      <button type="button" class="btn btn-secondary" data-action="br-close-line">Cancel</button>
    </div>
  `;
}

function undoForm(model: BankrecModel, line: BankRecStatementLine): TemplateResult {
  const live = model.history?.find((m) => m.unmatched_at === undefined || m.unmatched_at === null);
  if (model.history === null) return html`${nothing}`;
  if (live === undefined) {
    return html`<p class="br-alert br-alert-watch" data-nothing-to-undo>This line is not matched to anything now, so there is nothing to undo. Reload to see where it stands.</p>`;
  }
  return html`
    <p class="br-fact" data-live-match>${live.method === "AUTO" ? "Matched by a rule" : "Matched by hand"} to a ${targetWord(live.target_type).toLowerCase()}.</p>
    ${model.confirmUndo
      ? html`<p class="br-note" data-undo-note>Undoing returns the line to unmatched. For a payment or lodgement the core also posts the reversing entry.</p>
          <div class="br-actions">
            <button type="button" class="btn btn-primary" data-action="br-confirm-undo" data-id=${live.id} ?disabled=${model.busy}>Confirm undo</button>
            <button type="button" class="btn btn-secondary" data-action="br-cancel-undo">Keep the match</button>
          </div>`
      : html`<div class="br-actions">
          <button type="button" class="btn btn-secondary" data-action="br-undo" data-id=${line.id}>Undo match</button>
          <button type="button" class="btn btn-secondary" data-action="br-close-line">Close</button>
        </div>`}
  `;
}

function linePanel(model: BankrecModel): TemplateResult {
  const line = model.lines.find((l) => l.id === model.selectedLine);
  if (line === undefined) {
    return html`<section class="br-panel" aria-label="Line"><p class="br-dim">Pick a line to match it, or to open its match.</p></section>`;
  }
  const action = lineAction(line.status);
  return html`
    <section class="br-panel" aria-label="Line" data-line-panel>
      <div class="br-panel-head">
        <h2>${line.description}</h2>
        ${pill(lineTone(line.status), lineStatusWord(line.status))}
      </div>
      <dl class="br-facts">
        <dt>Date</dt><dd>${dayWords(line.txn_date)}</dd>
        <dt>Amount</dt><dd data-line-amount>${lineAmount(line)}</dd>
        ${line.reference !== "" ? html`<dt>Reference</dt><dd class="br-mono">${line.reference}</dd>` : nothing}
      </dl>
      <h3>History</h3>
      ${historyList(model)}
      ${action === "match" ? manualForm(model, line) : action === "unmatch" ? undoForm(model, line) : nothing}
    </section>
  `;
}

function matchView(model: BankrecModel): TemplateResult {
  const account = accountOf(model);
  return html`
    <div data-br-match>
      <section class="br-panel" aria-label="Auto match">
        <div class="br-panel-head"><h3>Auto match</h3></div>
        <div class="br-toolbar">
          ${accountSelect(model)}
          <label class="br-field">From
            <input type="date" data-field="br-from" aria-label="From" />
          </label>
          <label class="br-field">Before
            <input type="date" data-field="br-to" aria-label="Before" />
          </label>
          <button type="button" class="btn btn-primary" data-action="br-auto" ?disabled=${model.busy || account === undefined}>Run auto match</button>
        </div>
        <p class="br-hint">Credits only: the rules pair card settlements, lodgements and batch settlements to what the till received. Leave the dates empty to match every unmatched credit. Debits, such as fees, are matched by hand.</p>
      </section>
      <div class="br-split">
        <section class="br-panel" aria-label="Statement lines">
          <div class="br-panel-head">
            <h3>Statement lines${account === undefined ? "" : html` · ${account.name}`}</h3>
            <div class="br-filters" role="group" aria-label="Filter lines">
              ${FILTERS.map((f) => html`<button type="button" class="br-filter" data-action="br-filter" data-filter=${f.id} data-filter-id=${f.id === "" ? "ALL" : f.id} aria-pressed="${model.lineFilter === f.id ? "true" : "false"}">${f.word}</button>`)}
            </div>
          </div>
          ${linesList(model)}
          <p class="br-dim br-later-line" data-later-adjust>Fees and interest the statement shows are booked as a journal in the ledger, then the line is matched to that journal. The core has no adjusting entry on these routes: ${later("later")}</p>
        </section>
        ${linePanel(model)}
      </div>
    </div>
  `;
}

// ---- rules --------------------------------------------------------------------------------------------

function specLine(rule: BankRecMatchRule): string {
  const s = rule.spec;
  if (s.kind === "descriptor_fuzzy") {
    const days = s.date_window_days ?? 0;
    const split = s.allow_fee_split === true ? ", fee split allowed" : "";
    return `Description ${s.descriptor_ilike ?? ""}, within ${days} days${split}`;
  }
  const tolerance = s.tolerance_cents ?? 0;
  return tolerance === 0 ? "Exact amount" : `Within ${formatMoney(tolerance)}`;
}

function rulesTable(model: BankrecModel): TemplateResult {
  if (!model.rulesLoaded) return html`<p class="br-dim" role="status" data-loading-rules>Reading the match rules.</p>`;
  if (model.rules.length === 0) {
    return html`<div class="br-empty" data-empty-rules><strong>No match rules yet.</strong><span class="br-dim">Approve a first rule with the form beside this list.</span></div>`;
  }
  return html`
    <div class="br-scroll br-hide-narrow">
      <table aria-label="Match rules">
        <thead><tr><th>Rule</th><th>Kind</th><th class="num">Priority</th><th class="num">Version</th><th>State</th><th></th></tr></thead>
        <tbody>
          ${model.rules.map(
            (r) => html`<tr data-rule-id=${r.id}>
              <td><strong>${r.name}</strong><span class="br-sub br-mono">${r.code}</span></td>
              <td>${ruleKindWord(r.spec.kind ?? "")}<span class="br-sub">${specLine(r)}</span></td>
              <td class="num">${r.priority}</td>
              <td class="num">v${r.version}</td>
              <td>${pill(ruleTone(r.status), ruleStatusWord(r.status))}</td>
              <td><button type="button" class="br-link" data-action="br-rule-clone" data-id=${r.id}>Start a new version from this</button></td>
            </tr>`,
          )}
        </tbody>
      </table>
    </div>
    <ul class="br-cards" aria-label="Match rules, as cards">
      ${model.rules.map(
        (r) => html`<li class="br-card" data-rule-card=${r.id}>
          <div class="br-card-top"><strong>${r.name}</strong>${pill(ruleTone(r.status), ruleStatusWord(r.status))}</div>
          <span class="br-sub br-mono">${r.code} · v${r.version} · priority ${r.priority}</span>
          <span>${ruleKindWord(r.spec.kind ?? "")}</span><span class="br-sub">${specLine(r)}</span>
          <div class="br-actions"><button type="button" class="br-link" data-action="br-rule-clone" data-id=${r.id}>Start a new version from this</button></div>
        </li>`,
      )}
    </ul>
  `;
}

function ruleForm(model: BankrecModel): TemplateResult {
  const fuzzy = model.ruleKind === "descriptor_fuzzy";
  return html`
    <section class="br-panel" aria-label="Approve a rule version" data-rule-form>
      <div class="br-panel-head"><h3>Approve a version</h3></div>
      <div class="br-form">
        <label class="br-field">Code
          <input type="text" data-field="br-rule-code" autocomplete="off" aria-label="Code" placeholder="gateway-ref-exact" />
        </label>
        <label class="br-field">Priority
          <input type="text" inputmode="numeric" data-field="br-rule-priority" autocomplete="off" aria-label="Priority" placeholder="100" />
        </label>
        <label class="br-field br-field-wide">Name
          <input type="text" data-field="br-rule-name" autocomplete="off" aria-label="Name" />
        </label>
        <label class="br-field br-field-wide">Kind
          <select data-field="br-rule-kind" aria-label="Kind">${RULE_KINDS.map((k) => html`<option data-id=${k}>${ruleKindWord(k)}</option>`)}</select>
        </label>
        ${fuzzy
          ? html`
              <label class="br-field">Description contains
                <input type="text" data-field="br-rule-descriptor" autocomplete="off" aria-label="Description contains" placeholder="RUN" />
              </label>
              <label class="br-field">Date window (days either side)
                <input type="text" inputmode="numeric" data-field="br-rule-window" autocomplete="off" aria-label="Date window" placeholder="3" />
              </label>
              <label class="br-field">Allow a fee split
                <select data-field="br-rule-fee-split" aria-label="Allow a fee split"><option data-id="no">No</option><option data-id="yes">Yes</option></select>
              </label>
              <label class="br-field">Fee account
                <input type="text" data-field="br-rule-fee-account" autocomplete="off" aria-label="Fee account" placeholder="6300" />
              </label>`
          : html`
              <label class="br-field br-field-wide">Amount tolerance (dollars)
                <input type="text" inputmode="decimal" data-field="br-rule-tolerance" autocomplete="off" aria-label="Amount tolerance" placeholder="0.00" />
                <span class="br-hint">Zero means the amount must be exact.</span>
              </label>`}
      </div>
      <div class="br-actions">
        <button type="button" class="btn btn-primary" data-action="br-rule-save" ?disabled=${model.busy}>Approve version</button>
      </div>
      <p class="br-note">Approving a version puts it in use and retires the version now in use for the same code. Earlier versions stay in the list.</p>
    </section>
  `;
}

function rulesView(model: BankrecModel): TemplateResult {
  return html`
    <div class="br-split" data-br-rules>
      <section class="br-panel" aria-label="Match rules">
        <div class="br-panel-head"><h3>Match rules</h3></div>
        ${rulesTable(model)}
      </section>
      ${ruleForm(model)}
    </div>
  `;
}

// ---- the whole slice ----------------------------------------------------------------------------------

const HEADINGS: Record<BrTab, string> = {
  accounts: "Open a month, see where each account stands, and sign it off when the statement and the ledger agree.",
  import: "Bring a bank or card statement in, as a file or pasted rows.",
  match: "Pair each statement line to what the till and the ledger hold.",
  rules: "The rules the auto match follows. Versioned: a change is a new version.",
};

export function renderBankrec(model: BankrecModel): TemplateResult {
  if (model.phase === "loading") return loading();
  if (model.phase === "error") return loadFailed(model);
  return html`
    <div class="br-head">
      <h1>Bank reconciliation</h1>
      <p class="br-dim">${HEADINGS[model.tab]}</p>
    </div>
    ${tabs(model)} ${messages(model)}
    ${model.tab === "accounts" ? accountsView(model) : model.tab === "import" ? importView(model) : model.tab === "match" ? matchView(model) : rulesView(model)}
  `;
}
