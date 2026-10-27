// The ledger slice's books screens: the chart of accounts, the new journal form and its review, the recurring
// templates, the fiscal periods with their close and reopen, the year end close, the checks (the receivables
// tie out and the legacy import check) and the policy. Plain render functions of a model, like gl-views.ts: no
// state, no events, every control a plain element carrying a data-action name.
//
// Every figure is the server's, formatted: nothing on these screens adds two amounts. The new journal form
// checks each line's shape and leaves whether the two sides match to the core, which refuses an unbalanced
// journal in words. A destructive step (posting, closing a period, closing the year, reversing, voiding,
// deactivating an account) is never one press: it opens a review that names what it will change.
import { html, nothing, type TemplateResult } from "lit";
import type {
  Branch,
  GlAccount,
  GlBisTrackTieoutReport,
  GlJournal,
  GlPeriod,
  GlPolicy,
  GlRecurringTemplate,
  GlTieoutState,
} from "@hardscapehouse/api-client";
import {
  accountTypeWord,
  bisSourceWord,
  costBasisWord,
  monthWord,
  periodName,
  periodStatusWord,
  periodTone,
  sideWord,
  tieWords,
  type ReadyLine,
} from "./gl-logic.js";
import { formatMoney } from "./gl-money.js";

/** One lazily loaded part of the books: not asked yet, being read, read, or refused in words. */
export interface Section<T> {
  readonly phase: "idle" | "loading" | "ready" | "error";
  readonly value: T;
  readonly error: string;
}

/** What waits for an explicit yes, named so the review can say what it will change. */
export type Confirm =
  | { readonly kind: "close-period"; readonly id: string }
  | { readonly kind: "reopen"; readonly id: string }
  | { readonly kind: "close-year"; readonly year: number }
  | { readonly kind: "reverse"; readonly id: string }
  | { readonly kind: "void"; readonly id: string }
  | { readonly kind: "deactivate"; readonly code: string };

/** The new journal as checked, held while the review is open. */
export interface Review {
  readonly memo: string;
  readonly branchId: string;
  readonly lines: readonly ReadyLine[];
}

export interface BooksModel {
  /** The controller holds every control; a reader (Payables and Receivables) reads and holds the rest. */
  readonly mode: "controller" | "reader";
  readonly chart: Section<readonly GlAccount[]>;
  readonly recurring: Section<readonly GlRecurringTemplate[]>;
  readonly periods: Section<readonly GlPeriod[]>;
  readonly policy: Section<GlPolicy | null>;
  readonly tie: Section<readonly GlTieoutState[]>;
  readonly bis: Section<GlBisTrackTieoutReport | null>;
  /** The dealer's legacy import source word; "" is none, and the legacy import section is not drawn. */
  readonly legacyImport: string;
  readonly confirm: Confirm | null;
  readonly composing: boolean;
  readonly lineCount: number;
  readonly review: Review | null;
  readonly composeError: string;
  readonly trialMonth: number;
  readonly exporting: boolean;
}

interface Frame {
  readonly error: string;
  readonly branches: readonly Branch[];
  readonly journals: readonly GlJournal[];
  readonly busy: boolean;
  readonly books: BooksModel;
}

function pill(tone: string, word: string): TemplateResult {
  return html`<span class="gl-status gl-status-${tone}">${word}</span>`;
}

function later(word: string): TemplateResult {
  return html`<span class="gl-later">${word}</span>`;
}

function held(words: string): TemplateResult {
  return html`<p class="gl-note" data-held>${words}</p>`;
}

function sectionState<T>(section: Section<T>, what: string): TemplateResult | null {
  if (section.phase === "idle" || section.phase === "loading") return html`<p class="gl-dim" role="status">Reading ${what}.</p>`;
  if (section.phase === "error") {
    return html`
      <p class="gl-alert gl-alert-error" role="alert" data-section-error>${section.error}</p>
      <button type="button" class="btn btn-secondary" data-action="gl-reload-section">Try again</button>
    `;
  }
  return null;
}

function entryLabel(journal: GlJournal): string {
  return `JE-${String(journal.entry_no).padStart(4, "0")}`;
}

function branchName(branches: readonly Branch[], id: string | undefined): string {
  if (id === undefined || id === "") return "";
  return branches.find((b) => b.id === id)?.name ?? "";
}

/** The default branch first, so the form's first choice is the dealer's own yard. */
export function branchChoices(branches: readonly Branch[]): readonly Branch[] {
  return [...branches].sort((a, b) => Number(b.is_default) - Number(a.is_default) || a.name.localeCompare(b.name));
}

// ---- confirm panels -------------------------------------------------------------------------------

function confirmShell(frame: Frame, title: string, tone: "stop" | "watch", body: TemplateResult, go: TemplateResult): TemplateResult {
  return html`
    <section class="gl-panel gl-confirm gl-confirm-${tone}" aria-label="Confirm" role="alertdialog" aria-labelledby="gl-confirm-title" tabindex="-1" data-confirm>
      <h2 id="gl-confirm-title">${title}</h2>
      ${frame.error === "" ? nothing : html`<p class="gl-alert gl-alert-error" role="alert" data-error data-confirm-error>${frame.error}</p>`}
      ${body}
      <div class="gl-actions">${go}<button type="button" class="btn btn-secondary" data-action="gl-confirm-cancel">Keep it as it is</button></div>
    </section>
  `;
}

/** What closing a period will change, said before the press. */
function closePeriodConfirm(frame: Frame, period: GlPeriod): TemplateResult {
  const parked = frame.journals.filter((j) => j.period_id === period.id && j.derived_status === "PENDING_APPROVAL").length;
  return confirmShell(
    frame,
    `Close ${periodName(period)}?`,
    "watch",
    html`
      <ul class="gl-changes" aria-label="What changes">
        <li>The period moves from Open to Closed.</li>
        <li>No journal can post into ${periodName(period)} until the Financial Controller reopens it with a reason.</li>
        <li data-parked>${parked === 0 ? "No parked journal waits in this period." : `${parked} parked ${parked === 1 ? "journal waits" : "journals wait"} in this period for a second approver.`}</li>
      </ul>
      <label class="gl-field">Reason (optional)
        <textarea data-field="reason" rows="2" aria-label="Reason for closing"></textarea>
      </label>
    `,
    html`<button type="button" class="btn btn-primary" data-action="gl-confirm-go" ?disabled=${frame.busy}>Close ${periodName(period)}</button>`,
  );
}

function reopenConfirm(frame: Frame, period: GlPeriod): TemplateResult {
  return confirmShell(
    frame,
    `Reopen ${periodName(period)}?`,
    "stop",
    html`
      <ul class="gl-changes" aria-label="What changes">
        <li>The period moves from Closed to Open, and journals can post into ${periodName(period)} again.</li>
        <li>Your reason is kept with the reopen and the reopen is recorded as a sensitive act.</li>
      </ul>
      <label class="gl-field">Reason (required)
        <textarea data-field="reason" rows="2" aria-label="Reason for reopening"></textarea>
      </label>
    `,
    html`<button type="button" class="btn btn-primary" data-action="gl-confirm-go" ?disabled=${frame.busy}>Reopen ${periodName(period)}</button>`,
  );
}

function closeYearConfirm(frame: Frame, year: number): TemplateResult {
  return confirmShell(
    frame,
    `Close fiscal year ${year}?`,
    "stop",
    html`
      <ul class="gl-changes" aria-label="What changes">
        <li>One closing journal posts: every revenue and expense account of ${year} is flattened and the net income, or loss, moves to account 3900 Retained Earnings.</li>
        <li>Every open period of ${year} is closed.</li>
        <li>The net income is the server's figure and shows after the close. Reopening a period does not undo the closing journal, and a closing journal has no undo from the register.</li>
      </ul>
      <label class="gl-field">Reason (optional)
        <textarea data-field="reason" rows="2" aria-label="Reason for closing the year"></textarea>
      </label>
    `,
    html`<button type="button" class="btn btn-primary" data-action="gl-confirm-go" ?disabled=${frame.busy}>Close fiscal year ${year}</button>`,
  );
}

function reverseConfirm(frame: Frame, journal: GlJournal): TemplateResult {
  return confirmShell(
    frame,
    `Reverse ${entryLabel(journal)}?`,
    "stop",
    html`
      <p class="gl-fact">${journal.memo}</p>
      <ul class="gl-changes" aria-label="What changes">
        <li>A new posted journal is added with every line's side swapped, so the two entries net to nothing on every account.</li>
        <li>${entryLabel(journal)} stays posted and keeps its place in the register. Nothing is erased.</li>
      </ul>
      ${linesTable(journal, "Lines of the journal being reversed")}
    `,
    html`<button type="button" class="btn btn-primary" data-action="gl-confirm-go" ?disabled=${frame.busy}>Reverse ${entryLabel(journal)}</button>`,
  );
}

function voidConfirm(frame: Frame, journal: GlJournal): TemplateResult {
  return confirmShell(
    frame,
    `Void ${entryLabel(journal)}?`,
    "watch",
    html`
      <p class="gl-fact">${journal.memo}</p>
      <ul class="gl-changes" aria-label="What changes">
        <li>The draft is voided and can never post. It has not touched the ledger, so nothing is reversed.</li>
        <li>Its approval request, if one is waiting, is not removed by this: decline it in the approvals queue.</li>
      </ul>
    `,
    html`<button type="button" class="btn btn-primary" data-action="gl-confirm-go" ?disabled=${frame.busy}>Void ${entryLabel(journal)}</button>`,
  );
}

function deactivateConfirm(frame: Frame, account: GlAccount): TemplateResult {
  return confirmShell(
    frame,
    `Deactivate ${account.code} ${account.name}?`,
    "watch",
    html`
      <ul class="gl-changes" aria-label="What changes">
        <li>The account stays on every posted journal and on the trial balance, but no new journal can use it.</li>
        <li>The core has no route to turn an account back on, so this is final from here.</li>
      </ul>
    `,
    html`<button type="button" class="btn btn-primary" data-action="gl-confirm-go" ?disabled=${frame.busy}>Deactivate ${account.code}</button>`,
  );
}

/** The open confirm step, or nothing. The caller places it where the thing it names is. */
export function confirmFor(model: Frame, kinds: ReadonlyArray<Confirm["kind"]>): TemplateResult | typeof nothing {
  const confirm = model.books.confirm;
  if (confirm === null || !kinds.includes(confirm.kind)) return nothing;
  if (confirm.kind === "close-period" || confirm.kind === "reopen") {
    const period = model.books.periods.value.find((p) => p.id === confirm.id);
    if (period === undefined) return nothing;
    return confirm.kind === "close-period" ? closePeriodConfirm(model, period) : reopenConfirm(model, period);
  }
  if (confirm.kind === "close-year") return closeYearConfirm(model, confirm.year);
  if (confirm.kind === "reverse" || confirm.kind === "void") {
    const journal = model.journals.find((j) => j.id === confirm.id);
    if (journal === undefined) return nothing;
    return confirm.kind === "reverse" ? reverseConfirm(model, journal) : voidConfirm(model, journal);
  }
  const account = model.books.chart.value.find((a) => a.code === confirm.code);
  return account === undefined ? nothing : deactivateConfirm(model, account);
}

// ---- the lines of one journal ---------------------------------------------------------------------

export function linesTable(journal: GlJournal | null, label = "Journal lines"): TemplateResult {
  if (journal === null) return html`<p class="gl-dim" role="status">Reading the journal.</p>`;
  const memos = journal.lines.some((l) => (l.memo ?? "") !== "");
  return html`
    <div class="gl-scroll">
      <table aria-label=${label}>
        <thead><tr><th>Account</th>${memos ? html`<th>Memo</th>` : nothing}<th class="num">Debit</th><th class="num">Credit</th></tr></thead>
        <tbody>
          ${journal.lines.map(
            (l) => html`<tr>
              <td><strong>${l.account_code}</strong> ${l.account_name ?? ""}</td>
              ${memos ? html`<td>${l.memo ?? ""}</td>` : nothing}
              <td class="num">${l.debit_cents > 0 ? formatMoney(l.debit_cents) : ""}</td>
              <td class="num">${l.credit_cents > 0 ? formatMoney(l.credit_cents) : ""}</td>
            </tr>`,
          )}
        </tbody>
      </table>
    </div>
  `;
}

// ---- the chart of accounts ------------------------------------------------------------------------

function newAccountForm(frame: Frame): TemplateResult {
  if (frame.books.mode !== "controller") {
    return held("Adding and deactivating accounts is for the Financial Controller or the General Manager.");
  }
  return html`
    <section class="gl-panel" aria-label="New account" data-new-account>
      <div class="gl-panel-head"><h3>New account</h3></div>
      <div class="gl-form-grid">
        <label class="gl-field">Code
          <input data-field="account-code" type="text" inputmode="numeric" autocomplete="off" aria-label="Account code" />
        </label>
        <label class="gl-field">Name
          <input data-field="account-name" type="text" autocomplete="off" aria-label="Account name" />
        </label>
        <label class="gl-field">Type
          <select data-field="account-type" aria-label="Account type">
            <option value="ASSET">Asset</option>
            <option value="LIABILITY">Liability</option>
            <option value="EQUITY">Equity</option>
            <option value="REVENUE">Revenue</option>
            <option value="EXPENSE">Expense</option>
          </select>
        </label>
        <label class="gl-field">Normal side
          <select data-field="account-side" aria-label="Normal side">
            <option value="DEBIT">Debit</option>
            <option value="CREDIT">Credit</option>
          </select>
        </label>
      </div>
      <p class="gl-dim">Assets and expenses normally sit on the debit side, the rest on the credit side.</p>
      <div class="gl-actions"><button type="button" class="btn btn-primary" data-action="gl-add-account" ?disabled=${frame.busy}>Add account</button></div>
    </section>
  `;
}

export function chartView(frame: Frame): TemplateResult {
  const chart = frame.books.chart;
  const state = sectionState(chart, "the chart of accounts");
  return html`
    <div class="gl-stack">
      ${confirmFor(frame, ["deactivate"])}
      <section class="gl-panel" aria-label="Chart of accounts" data-chart>
        <div class="gl-panel-head">
          <h3>Chart of accounts</h3>
          ${chart.phase === "ready" ? html`<span class="gl-dim">${chart.value.length} accounts</span>` : nothing}
        </div>
        ${state ??
        (chart.value.length === 0
          ? html`<div class="gl-empty"><strong>No accounts yet.</strong><span class="gl-dim">Add the first account below.</span></div>`
          : html`
              <div class="gl-scroll">
                <table aria-label="Accounts">
                  <thead><tr><th>Code</th><th>Name</th><th class="gl-col-type">Type</th><th class="gl-col-type">Normal side</th><th>Status</th>${frame.books.mode === "controller" ? html`<th><span class="gl-visually">Action</span></th>` : nothing}</tr></thead>
                  <tbody>
                    ${chart.value.map(
                      (a) => html`<tr data-account=${a.code} class="${a.is_active ? "" : "gl-inactive"}">
                        <td><strong>${a.code}</strong></td>
                        <td>${a.name}<span class="gl-name-narrow">${accountTypeWord(a.account_type)} · ${sideWord(a.normal_balance)}</span></td>
                        <td class="gl-col-type">${accountTypeWord(a.account_type)}</td>
                        <td class="gl-col-type">${sideWord(a.normal_balance)}</td>
                        <td>${a.is_active ? pill("good", "Active") : pill("plain", "Inactive")}</td>
                        ${frame.books.mode === "controller"
                          ? html`<td>${a.is_active ? html`<button type="button" class="gl-link" data-action="gl-deactivate" data-code=${a.code} aria-label="Deactivate ${a.code} ${a.name}">Deactivate</button>` : nothing}</td>`
                          : nothing}
                      </tr>`,
                    )}
                  </tbody>
                </table>
              </div>
            `)}
      </section>
      ${newAccountForm(frame)}
    </div>
  `;
}

// ---- the new journal ------------------------------------------------------------------------------

function lineRow(frame: Frame, index: number): TemplateResult {
  const active = frame.books.chart.value.filter((a) => a.is_active);
  const key = String(index);
  const n = index + 1;
  return html`
    <div class="gl-line" data-line=${key}>
      <label class="gl-field gl-line-account">Account
        <select data-field="line-account" data-key=${key} aria-label="Line ${n} account">
          <option value="">Choose an account</option>
          ${active.map((a) => html`<option>${a.code} · ${a.name}</option>`)}
        </select>
      </label>
      <label class="gl-field">Debit
        <input data-field="line-debit" data-key=${key} type="text" inputmode="decimal" autocomplete="off" placeholder="0.00" aria-label="Line ${n} debit" />
      </label>
      <label class="gl-field">Credit
        <input data-field="line-credit" data-key=${key} type="text" inputmode="decimal" autocomplete="off" placeholder="0.00" aria-label="Line ${n} credit" />
      </label>
      <label class="gl-field gl-line-memo">Line memo
        <input data-field="line-memo" data-key=${key} type="text" autocomplete="off" aria-label="Line ${n} memo" />
      </label>
    </div>
  `;
}

function thresholdWords(policy: GlPolicy | null): TemplateResult {
  if (policy === null) return html`<p class="gl-dim">The approval threshold is not on screen yet.</p>`;
  return html`<p class="gl-note" data-threshold>Below ${formatMoney(policy.je_approval_threshold_cents)} a journal posts to the ledger at once. At or over it, the journal parks as a draft until a second approver posts it. Whether yours is over is the server's call.</p>`;
}

function reviewPanel(frame: Frame, review: Review): TemplateResult {
  const where = branchName(frame.branches, review.branchId);
  return html`
    <section class="gl-panel gl-confirm gl-confirm-watch" aria-label="Review the journal" role="alertdialog" aria-labelledby="gl-review-title" tabindex="-1" data-review>
      <h2 id="gl-review-title">Post this journal?</h2>
      <p class="gl-fact">${review.memo}${where === "" ? "" : ` · ${where}`}</p>
      <div class="gl-scroll">
        <table aria-label="Lines to post">
          <thead><tr><th>Account</th><th>Memo</th><th class="num">Debit</th><th class="num">Credit</th></tr></thead>
          <tbody>
            ${review.lines.map(
              (l) => html`<tr>
                <td><strong>${l.account_code}</strong> ${frame.books.chart.value.find((a) => a.code === l.account_code)?.name ?? ""}</td>
                <td>${l.memo}</td>
                <td class="num">${l.debit_cents > 0 ? formatMoney(l.debit_cents) : ""}</td>
                <td class="num">${l.credit_cents > 0 ? formatMoney(l.credit_cents) : ""}</td>
              </tr>`,
            )}
          </tbody>
        </table>
      </div>
      ${thresholdWords(frame.books.policy.value)}
      <p class="gl-dim">A posted journal is corrected with a reversing journal, never edited.</p>
      <div class="gl-actions">
        <button type="button" class="btn btn-primary" data-action="gl-post-go" ?disabled=${frame.busy}>Post journal</button>
        <button type="button" class="btn btn-secondary" data-action="gl-review-back">Back to editing</button>
      </div>
    </section>
  `;
}

export function composerView(frame: Frame): TemplateResult {
  const books = frame.books;
  if (books.mode !== "controller") return html`${held("Posting a journal is for the Financial Controller or the General Manager.")}`;
  if (!books.composing) {
    return html`<div class="gl-actions"><button type="button" class="btn btn-primary" data-action="gl-compose">New journal</button></div>`;
  }
  const state = sectionState(books.chart, "the chart of accounts");
  return html`
    <section class="gl-panel" aria-label="New journal" data-composer>
      <div class="gl-panel-head"><h2>New journal</h2></div>
      ${state ??
      html`
        ${books.composeError !== "" ? html`<p class="gl-alert gl-alert-error" role="alert" data-compose-error>${books.composeError}</p>` : nothing}
        <div class="gl-form-grid">
          <label class="gl-field gl-grow">Memo
            <input data-field="memo" type="text" autocomplete="off" aria-label="Journal memo" />
          </label>
          <label class="gl-field">Branch
            <select data-field="branch" aria-label="Branch">${branchChoices(frame.branches).map((b) => html`<option>${b.name}</option>`)}</select>
          </label>
        </div>
        ${Array.from({ length: books.lineCount }, (_, i) => lineRow(frame, i))}
        <div class="gl-actions">
          <button type="button" class="btn btn-secondary" data-action="gl-add-line">Add a line</button>
          ${books.lineCount > 2 ? html`<button type="button" class="btn btn-secondary" data-action="gl-remove-line">Remove the last line</button>` : nothing}
        </div>
        <p class="gl-dim">Fill exactly one of debit or credit on each line. The two sides must be equal: the server refuses a journal that is not, and saves nothing.</p>
        <div class="gl-actions">
          <button type="button" class="btn btn-primary" data-action="gl-review" ?disabled=${frame.busy}>Review</button>
          <button type="button" class="btn btn-secondary" data-action="gl-compose-cancel">Cancel</button>
        </div>
      `}
    </section>
    ${books.review === null ? nothing : reviewPanel(frame, books.review)}
  `;
}

// ---- recurring templates --------------------------------------------------------------------------

export function recurringView(frame: Frame): TemplateResult {
  const rec = frame.books.recurring;
  const state = sectionState(rec, "the recurring templates");
  return html`
    <section class="gl-panel" aria-label="Recurring templates" data-recurring>
      <div class="gl-panel-head"><h3>Recurring templates</h3></div>
      ${state ??
      (rec.value.length === 0
        ? html`<div class="gl-empty"><strong>No recurring templates.</strong><span class="gl-dim">Templates show here once the dealer sets them up.</span></div>`
        : html`
            <ul class="gl-list" aria-label="Templates">
              ${rec.value.map(
                (t) => html`<li class="gl-card-row" data-template=${t.id}>
                  <span class="gl-row-main">
                    <span class="gl-row-title">${t.name}</span>
                    <span class="gl-row-detail">${t.schedule === "MONTHLY" ? "Monthly" : t.schedule} · ${t.lines.length} lines${t.is_active ? "" : " · Inactive"}</span>
                  </span>
                  ${frame.books.mode === "controller"
                    ? html`<button type="button" class="btn btn-secondary" data-action="gl-run-template" data-id=${t.id} aria-label="Run ${t.name}" ?disabled=${frame.busy || !t.is_active}>Run</button>`
                    : nothing}
                </li>`,
              )}
            </ul>
            <p class="gl-dim">Running a template adds a draft journal for review. A second approver posts it; it never posts by itself.</p>
          `)}
    </section>
  `;
}

// ---- periods --------------------------------------------------------------------------------------

export function periodsView(frame: Frame): TemplateResult {
  const periods = frame.books.periods;
  const state = sectionState(periods, "the fiscal periods");
  const controller = frame.books.mode === "controller";
  const years = [...new Set(periods.value.map((p) => p.year))].sort((a, b) => b - a);
  const open = (year: number): boolean => periods.value.some((p) => p.year === year && p.status === "OPEN");
  return html`
    <div class="gl-stack">
      ${confirmFor(frame, ["close-period", "reopen", "close-year"])}
      <section class="gl-panel" aria-label="Fiscal periods" data-periods>
        <div class="gl-panel-head">
          <h3>Fiscal periods</h3>
          ${controller && years.length > 0
            ? html`<span class="gl-actions">${years.filter(open).map((y) => html`<button type="button" class="btn btn-secondary" data-action="gl-close-year" data-year=${String(y)}>Close fiscal year ${y}</button>`)}</span>`
            : nothing}
        </div>
        ${state ??
        (periods.value.length === 0
          ? html`<div class="gl-empty"><strong>No fiscal periods.</strong><span class="gl-dim">Periods show here once the dealer's year is set up.</span></div>`
          : html`
              <div class="gl-scroll">
                <table aria-label="Periods">
                  <thead><tr><th>Period</th><th>Status</th><th><span class="gl-visually">Action</span></th></tr></thead>
                  <tbody>
                    ${periods.value.map(
                      (p) => html`<tr data-period=${p.id}>
                        <td><strong>${periodName(p)}</strong></td>
                        <td>${pill(periodTone(p.status), periodStatusWord(p.status))}</td>
                        <td>${!controller
                          ? nothing
                          : p.status === "OPEN"
                            ? html`<button type="button" class="gl-link" data-action="gl-close-period" data-id=${p.id} aria-label="Close ${periodName(p)}">Close</button>`
                            : html`<button type="button" class="gl-link" data-action="gl-reopen-period" data-id=${p.id} aria-label="Reopen ${periodName(p)}">Reopen</button>`}</td>
                      </tr>`,
                    )}
                  </tbody>
                </table>
              </div>
            `)}
        ${controller ? nothing : held("Closing a period is for the Financial Controller or the General Manager, and reopening one and closing the year are for the Financial Controller.")}
      </section>
    </div>
  `;
}

// ---- the checks -----------------------------------------------------------------------------------

function tieCard(frame: Frame): TemplateResult {
  const tie = frame.books.tie;
  const state = sectionState(tie, "the tie out");
  return html`
    <section class="gl-panel" aria-label="Receivables tie out" data-tie>
      <div class="gl-panel-head"><h3>Receivables tie out</h3></div>
      <p class="gl-dim">The receivables ledger against account 1100 in the general ledger. The two should match to the cent.</p>
      ${state ??
      (tie.value.length === 0
        ? html`<div class="gl-empty"><strong>Nothing to compare yet.</strong><span class="gl-dim">The tie out fills once receivables are on the books.</span></div>`
        : html`
            ${tie.value.map(
              (t) => html`<dl class="gl-tiles gl-tiles-three" aria-label="Tie out figures" data-tie-row>
                <div><dt>Receivables ledger</dt><dd>${formatMoney(t.subledger_cents)}</dd></div>
                <div><dt>General ledger ${t.account_code}</dt><dd>${formatMoney(t.gl_cents)}</dd></div>
                <div><dt>Difference</dt><dd class="${t.in_tie ? "" : "gl-stop"}">${formatMoney(t.delta_cents)}</dd></div>
              </dl>
              <p>${pill(t.in_tie ? "good" : "stop", tieWords(t.in_tie, t.age_days))}</p>`,
            )}
          `)}
    </section>
  `;
}

/** The one name the legacy import section wears on screen, whichever legacy system the dealer names. */
const LEGACY_IMPORT_LABEL = "Legacy import check";

function bisCard(frame: Frame): TemplateResult {
  const bis = frame.books.bis;
  if (frame.books.mode !== "controller") {
    return html`<section class="gl-panel" aria-label=${LEGACY_IMPORT_LABEL} data-bis><div class="gl-panel-head"><h3>${LEGACY_IMPORT_LABEL}</h3></div>${held(`The ${LEGACY_IMPORT_LABEL.toLowerCase()} is for the Financial Controller.`)}</section>`;
  }
  const report = bis.value;
  return html`
    <section class="gl-panel" aria-label=${LEGACY_IMPORT_LABEL} data-bis>
      <div class="gl-panel-head"><h3>${LEGACY_IMPORT_LABEL}</h3>${later("static snapshot")}</div>
      <p class="gl-dim">Compares this ledger's closing balances for one month against the trial balance of the legacy system the dealer is moving from. It reads a fixed snapshot, not a live feed.</p>
      <div class="gl-form-grid">
        <label class="gl-field">Fiscal year
          <input data-field="bis-year" type="text" inputmode="numeric" autocomplete="off" aria-label="Fiscal year" />
        </label>
        <label class="gl-field">Month (1 to 12)
          <input data-field="bis-month" type="text" inputmode="numeric" autocomplete="off" aria-label="Month" />
        </label>
        <label class="gl-field">Tolerance in dollars
          <input data-field="bis-tolerance" type="text" inputmode="decimal" autocomplete="off" placeholder="0.00" aria-label="Tolerance in dollars" />
        </label>
      </div>
      <div class="gl-actions"><button type="button" class="btn btn-primary" data-action="gl-run-bis" ?disabled=${frame.busy}>Run the check</button></div>
      ${bis.phase === "loading" ? html`<p class="gl-dim" role="status">Running the check.</p>` : nothing}
      ${bis.phase === "error" ? html`<p class="gl-alert gl-alert-error" role="alert" data-section-error>${bis.error}</p>` : nothing}
      ${report === null ? nothing : bisReport(report)}
    </section>
  `;
}

function bisReport(report: GlBisTrackTieoutReport): TemplateResult {
  return html`
    <div data-bis-report>
      <p>${pill(report.ties_within_tolerance ? "good" : "stop", report.ties_within_tolerance ? "Ties within tolerance" : "Does not tie")}
        ${report.is_synthetic ? html` <span class="gl-dim">Synthetic test data, not a real legacy export.</span>` : nothing}</p>
      <div class="gl-scroll">
        <table aria-label="Legacy import lines">
          <thead><tr><th>Account</th><th class="num">This ledger</th><th class="num">Legacy import</th><th class="num">Difference</th><th class="gl-col-type">Found</th></tr></thead>
          <tbody>
            ${report.lines.map(
              (l) => html`<tr>
                <td><strong>${l.account_code}</strong> ${l.account_name}</td>
                <td class="num">${formatMoney(l.erp_cents)}</td>
                <td class="num">${formatMoney(l.bistrack_cents)}</td>
                <td class="num">${l.delta_cents === 0 ? "None" : formatMoney(l.delta_cents)}</td>
                <td class="gl-col-type">${bisSourceWord(l.source)}</td>
              </tr>`,
            )}
          </tbody>
        </table>
      </div>
      <p class="gl-dim" data-bis-count>${report.discrepancies.length === 0 ? "No account differs beyond the tolerance." : `${report.discrepancies.length} ${report.discrepancies.length === 1 ? "account differs" : "accounts differ"} beyond the tolerance.`}</p>
    </div>
  `;
}

export function checksView(frame: Frame): TemplateResult {
  return html`<div class="gl-stack">${tieCard(frame)}${frame.books.legacyImport === "" ? nothing : bisCard(frame)}</div>`;
}

// ---- the policy -----------------------------------------------------------------------------------

export function policyView(frame: Frame): TemplateResult {
  const policy = frame.books.policy;
  const state = sectionState(policy, "the policy");
  const value = policy.value;
  return html`
    <section class="gl-panel" aria-label="Ledger policy" data-policy>
      <div class="gl-panel-head"><h3>Ledger policy</h3></div>
      ${state ??
      (value === null
        ? html`<div class="gl-empty"><strong>No policy on file.</strong></div>`
        : html`
            <dl class="gl-facts">
              <dt>Approval threshold</dt><dd data-policy-threshold>${formatMoney(value.je_approval_threshold_cents)}</dd>
              <dt>Fiscal year starts</dt><dd>${monthWord(value.fiscal_year_start_month)}</dd>
              <dt>Cost basis</dt><dd>${costBasisWord(value.cost_basis_strategy)}</dd>
              <dt>Special buys</dt><dd>${value.exclude_special_buys ? "Left out of cost" : "Included in cost"}</dd>
              ${value.notes === "" ? nothing : html`<dt>Notes</dt><dd>${value.notes}</dd>`}
            </dl>
            <p class="gl-dim">A journal at or over the approval threshold needs a second approver. Changing the policy is a dealer setup step and is not on this screen.</p>
          `)}
    </section>
  `;
}
