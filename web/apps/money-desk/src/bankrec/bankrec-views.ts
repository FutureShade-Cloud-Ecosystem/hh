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
import type { BankRecAccount, BankRecMatch, BankRecMatchRule, BankRecReconciliation, BankRecStatementLine, GlJournal } from "@hardscapehouse/api-client";
import type { LineStatus, TargetType } from "./bankrec-api.js";
import {
  accountTypeWord,
  dayWords,
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

function reconRows(model: BankrecModel): TemplateResult {
  if (model.recons.length === 0) {
    return html`<div class="br-empty" data-empty-recons><strong>No month is open for this account.</strong><span class="br-dim">Open a month with the closing balance on its statement.</span></div>`;
  }
  return html`
    <div class="br-scroll br-hide-narrow">
      <table aria-label="Months for this account">
        <thead><tr><th>Month</th><th class="num">Statement</th><th class="num">Ledger</th><th>Difference</th><th></th></tr></thead>
        <tbody>
          ${model.recons.map((r) => {
            const v = varianceWords(r.variance_cents);
            return html`<tr data-recon-id=${r.id}>
              <td><strong>${periodWords(r.year, r.month)}</strong><span class="br-sub">${pill(reconTone(r.status), reconStatusWord(r.status))}</span></td>
              <td class="num">${formatMoney(r.statement_balance_cents)}</td>
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
        return html`<li class="br-card" data-recon-card=${r.id}>
          <div class="br-card-top"><strong>${periodWords(r.year, r.month)}</strong>${pill(reconTone(r.status), reconStatusWord(r.status))}</div>
          <dl class="br-facts">
            <dt>Statement</dt><dd>${formatMoney(r.statement_balance_cents)}</dd>
            <dt>Ledger</dt><dd>${formatMoney(r.gl_balance_cents)}</dd>
            <dt>Difference</dt><dd>${pill(v.tone, v.word)}</dd>
          </dl>
          ${reconActions(model, r)}
        </li>`;
      })}
    </ul>
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
              <p class="br-note" data-month-note>The difference is the statement less the ledger. It is worked out when you open or refresh the month, so refresh the figures after you match. A month signs off only when no line is unmatched and the difference is zero.</p>
              ${reconRows(model)}
              ${account.tracking_only ? html`<p class="br-dim br-later-line">Card spend is tracked here and not yet booked to the ledger: ${later("later")}</p>` : nothing}
            `}
      </section>
    </div>
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
