// The statements slice's screens, as plain render functions of a model. They hold no state and own no
// events: every control is a plain element carrying a data-action name and the slice's click listener acts
// on it (the design system's position rules allow no @ or . binding in an app, and the only bound values are
// class, role, aria- and data- attributes).
//
// Every figure on a screen is the server's, formatted: the app computes no money. References: the Money Desk
// boards (trial-balance for the frame, cards border first, no shadow), drawn from the original app's
// statements screen (finstmt-home, -income, -balance, -gl-detail, -aging) and its print document. A statement
// is a document: its title, period and scope are on the page and on the printed sheet, and the print
// stylesheet drops everything that is not the statement.
import { html, nothing, type TemplateResult } from "lit";
import type {
  Branch,
  FinStmtAgingSummary,
  FinStmtBalanceSheetReport,
  FinStmtComparativeRow,
  FinStmtGLDetailResponse,
  FinStmtIncomeStatementReport,
  FinStmtSchedule,
  FinStmtStatementRow,
  FinStmtTrialBalanceReport,
  GlPeriod,
} from "@hardscapehouse/api-client";
import { formatChange, formatMoney } from "./finstmt-money.js";
import {
  accountTypeWord,
  bucketWord,
  cadenceWord,
  CADENCES,
  entryLabel,
  periodLabel,
  periodStatusWord,
  periodsOf,
  periodYears,
  REPORTS,
  reportWord,
  scopeLabel,
  sourceWord,
  TABS,
  tabTitle,
  type StmtTab,
} from "./finstmt-logic.js";

export type TabStatus = "idle" | "loading" | "ready" | "error";

/** The ledger detail opened from a statement line: what the line said, so the detail can be held against it. */
export interface Drill {
  readonly from: "income" | "balance";
  readonly accountCode: string;
  readonly accountName: string;
  readonly accountType: string;
  readonly statementCents: number | null;
}

export interface PackFormState {
  readonly report: string;
  readonly recipients: string;
  readonly cadence: string;
  readonly branchId: string;
  readonly active: "yes" | "no";
}

export interface FinstmtModel {
  readonly phase: "idle" | "loading" | "ready" | "error";
  readonly loadError: string;
  readonly tab: StmtTab;
  readonly periods: readonly GlPeriod[];
  readonly branches: readonly Branch[];
  readonly year: number;
  readonly month: number;
  readonly branchId: string;
  readonly compare: boolean;
  readonly status: TabStatus;
  readonly tabError: string;
  readonly income: FinStmtIncomeStatementReport | null;
  readonly incomeCompare: readonly FinStmtComparativeRow[] | null;
  readonly balance: FinStmtBalanceSheetReport | null;
  readonly balanceCompare: readonly FinStmtComparativeRow[] | null;
  readonly trial: FinStmtTrialBalanceReport | null;
  readonly detail: FinStmtGLDetailResponse | null;
  readonly aging: FinStmtAgingSummary | null;
  readonly packs: readonly FinStmtSchedule[];
  readonly account: string;
  readonly drill: Drill | null;
  readonly shown: number;
  readonly form: PackFormState;
  readonly editing: string | null;
  readonly confirmDelete: string | null;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
}

/** A feature the core serves that this increment does not draw yet, said in words. */
function later(word: string): TemplateResult {
  return html`<span class="fs-later">${word}</span>`;
}

function pill(tone: string, word: string): TemplateResult {
  return html`<span class="fs-status fs-status-${tone}">${word}</span>`;
}

function messages(model: FinstmtModel): TemplateResult {
  return html`
    ${model.error !== "" ? html`<p class="fs-alert fs-alert-error" role="alert" data-error>${model.error}</p>` : nothing}
    ${model.notice !== "" ? html`<p class="fs-alert fs-alert-ok" role="status" data-notice>${model.notice}</p>` : nothing}
  `;
}

// ---- the way in ------------------------------------------------------------------------------------

function loading(): TemplateResult {
  return html`<p class="fs-dim" role="status" data-loading>Loading the books.</p>`;
}

function loadFailed(model: FinstmtModel): TemplateResult {
  return html`
    <p class="fs-alert fs-alert-error" role="alert" data-error>${model.loadError}</p>
    <button type="button" class="btn btn-secondary" data-action="fs-reload">Try again</button>
  `;
}

function tabs(model: FinstmtModel): TemplateResult {
  return html`
    <nav class="fs-tabs fs-noprint" aria-label="Statements">
      ${TABS.map(([id, label]) => html`<button type="button" class="fs-tab" data-action="fs-tab" data-tab=${id} data-tab-id=${id} aria-current="${model.tab === id ? "true" : "false"}">${label}</button>`)}
    </nav>
  `;
}

// ---- the choice of books ----------------------------------------------------------------------------

const BY_PERIOD: ReadonlySet<StmtTab> = new Set<StmtTab>(["income", "gl"]);
const BY_BRANCH: ReadonlySet<StmtTab> = new Set<StmtTab>(["income", "balance", "gl", "aging"]);
const COMPARES: ReadonlySet<StmtTab> = new Set<StmtTab>(["income", "balance"]);
const EXPORTS: ReadonlySet<StmtTab> = new Set<StmtTab>(["income", "balance", "trial", "gl", "aging"]);

function controls(model: FinstmtModel): TemplateResult {
  const years = periodYears(model.periods);
  const months = periodsOf(model.periods, model.year);
  const asOf = model.tab === "aging";
  return html`
    <section class="fs-panel fs-controls fs-noprint" aria-label="Choose the books">
      <div class="fs-form">
        ${asOf
          ? nothing
          : html`<label class="fs-field">Fiscal year
              <select data-field="fs-year" aria-label="Fiscal year">${years.map((y) => html`<option data-id=${String(y)}>${periodLabel(y, 0)}</option>`)}</select>
            </label>`}
        ${BY_PERIOD.has(model.tab)
          ? html`<label class="fs-field">Period
              <select data-field="fs-month" aria-label="Period">
                <option data-id="0">Whole fiscal year</option>
                ${months.map((p) => html`<option data-id=${String(p.month)}>Period ${p.month} · ${p.status === "CLOSED" ? "closed" : "open"}</option>`)}
              </select>
            </label>`
          : nothing}
        ${BY_BRANCH.has(model.tab)
          ? html`<label class="fs-field">Branch
              <select data-field="fs-branch" aria-label="Branch">
                <option data-id="">All branches</option>
                ${model.branches.map((b) => html`<option data-id=${b.id}>${b.name}</option>`)}
              </select>
            </label>`
          : nothing}
        ${COMPARES.has(model.tab)
          ? html`<label class="fs-field">Compare
              <select data-field="fs-compare" aria-label="Compare with the year before">
                <option data-id="no">This year alone</option>
                <option data-id="yes">With the year before</option>
              </select>
            </label>`
          : nothing}
      </div>
      <div class="fs-actions">
        ${EXPORTS.has(model.tab) ? html`<button type="button" class="btn btn-secondary" data-action="fs-csv" data-csv>Download CSV</button>` : nothing}
        ${EXPORTS.has(model.tab) ? html`<button type="button" class="btn btn-secondary" data-action="fs-print" data-print>Print</button>` : nothing}
      </div>
    </section>
  `;
}

// ---- a statement's heading -------------------------------------------------------------------------

/** The title, the period and the scope of a statement: on the screen and, with the print stylesheet, on the sheet. */
function heading(model: FinstmtModel, subtitle?: TemplateResult): TemplateResult {
  const byPeriod = BY_PERIOD.has(model.tab);
  const month = byPeriod ? model.month : 0;
  const period = model.tab === "aging" ? "As of now" : periodLabel(model.year, month);
  const scope = model.tab === "trial" ? "All branches" : BY_BRANCH.has(model.tab) ? scopeLabel(model.branchId, model.branches) : "";
  const status = periodStatusWord(model.periods, model.year, month);
  return html`
    <div class="fs-panel-head">
      <div>
        <h2 data-statement-title>${tabTitle(model.tab)}</h2>
        <p class="fs-dim fs-sub" data-statement-sub>${period}${scope === "" ? "" : ` · ${scope}`}${model.compare && COMPARES.has(model.tab) ? ` · with fiscal year ${model.year - 1}` : ""}</p>
      </div>
      <div class="fs-heading-marks">
        ${status === "" ? nothing : pill(status === "Closed" ? "good" : "watch", status === "Closed" ? "Period closed" : "Period open: figures can still move")}
        ${subtitle ?? nothing}
      </div>
    </div>
    <p class="fs-print-only fs-dim">Printed from Money Desk. Every figure is the books' own.</p>
  `;
}

function empty(title: string, line: string): TemplateResult {
  return html`<div class="fs-empty" data-empty><strong>${title}</strong><span class="fs-dim">${line}</span></div>`;
}

function waiting(model: FinstmtModel): TemplateResult | null {
  if (model.status === "loading" || model.status === "idle") return html`<section class="fs-panel"><p class="fs-dim" role="status" data-loading>Reading ${tabTitle(model.tab).toLowerCase()}.</p></section>`;
  if (model.status === "error") {
    return html`<section class="fs-panel">
      <p class="fs-alert fs-alert-error" role="alert" data-error>${model.tabError}</p>
      <button type="button" class="btn btn-secondary" data-action="fs-retry">Try again</button>
    </section>`;
  }
  return null;
}

function tiles(items: ReadonlyArray<readonly [string, number, string]>): TemplateResult {
  return html`<dl class="fs-tiles">${items.map(([label, cents, id]) => html`<div><dt>${label}</dt><dd data-tile=${id}>${formatMoney(cents)}</dd></div>`)}</dl>`;
}

// ---- statement lines -------------------------------------------------------------------------------

function line(row: FinStmtStatementRow, drillable: boolean): TemplateResult {
  const open = drillable
    ? html`<button type="button" class="fs-link" data-action="fs-drill" data-code=${row.account_code} data-name=${row.account_name} data-type=${row.account_type} data-amount=${String(row.amount_cents)} aria-label="Open the ledger detail behind ${row.account_code} ${row.account_name}">${row.account_code}</button>`
    : html`${row.account_code}`;
  return html`<tr data-account=${row.account_code}>
    <td class="fs-col-code fs-nowrap">${open}</td>
    <td><span class="fs-name">${row.account_name}</span><span class="fs-code-narrow">${open}</span></td>
    <td class="num" data-amount>${formatMoney(row.amount_cents)}</td>
  </tr>`;
}

function sectionRows(title: string, rows: readonly FinStmtStatementRow[], totalLabel: string, totalCents: number, id: string): TemplateResult {
  return html`
    <tr class="fs-section"><th colspan="3" scope="colgroup">${title}</th></tr>
    ${rows.length === 0 ? html`<tr><td colspan="3" class="fs-dim" data-none>Nothing posted.</td></tr>` : rows.map((r) => line(r, true))}
    <tr class="fs-total"><td class="fs-col-code"></td><td><strong>${totalLabel}</strong></td><td class="num" data-total=${id}><strong>${formatMoney(totalCents)}</strong></td></tr>
  `;
}

function comparisonTable(model: FinstmtModel, rows: readonly FinStmtComparativeRow[]): TemplateResult {
  if (rows.length === 0) return empty("Nothing posted in either year.", "The comparison fills as journals post.");
  return html`
    <div class="fs-scroll">
      <table aria-label="${tabTitle(model.tab)} compared with the year before">
        <thead><tr><th class="fs-col-code">Account</th><th>Name</th><th class="num">${periodLabel(model.year, 0)}</th><th class="num">${periodLabel(model.year - 1, 0)}</th><th class="num">Change</th></tr></thead>
        <tbody>
          ${rows.map((r) => html`<tr data-account=${r.account_code}>
            <td class="fs-col-code fs-nowrap"><strong>${r.account_code}</strong></td>
            <td><span class="fs-name">${r.account_name}</span><span class="fs-code-narrow">${r.account_code}</span></td>
            <td class="num">${formatMoney(r.current_cents)}</td>
            <td class="num">${formatMoney(r.prior_cents)}</td>
            <td class="num" data-change>${formatChange(r.delta_cents)}</td>
          </tr>`)}
        </tbody>
      </table>
    </div>
    <p class="fs-note">The change is the books' own figure. It is not worked out on this page.</p>
  `;
}

// ---- the income statement --------------------------------------------------------------------------

function incomeView(model: FinstmtModel): TemplateResult {
  const wait = waiting(model);
  if (wait !== null) return wait;
  if (model.compare) {
    const rows = model.incomeCompare ?? [];
    return html`<section class="fs-panel fs-statement" aria-label="Income statement" data-statement>${heading(model)}${comparisonTable(model, rows)}</section>`;
  }
  const r = model.income;
  if (r === null) return html`<section class="fs-panel"><p class="fs-dim">The income statement did not load. Try again.</p></section>`;
  const none = r.revenue.length === 0 && r.expenses.length === 0;
  return html`
    <section class="fs-panel fs-statement" aria-label="Income statement" data-statement>
      ${heading(model)}
      ${tiles([["Revenue", r.revenue_cents, "revenue"], ["Expenses", r.expense_cents, "expenses"], [r.net_income_cents < 0 ? "Net loss" : "Net income", r.net_income_cents, "net"]])}
      ${none
        ? empty("No posted journals in this period.", "The statement fills as journals post.")
        : html`<div class="fs-scroll">
            <table aria-label="Income statement lines">
              <thead><tr><th class="fs-col-code">Account</th><th>Name</th><th class="num">Amount</th></tr></thead>
              <tbody>
                ${sectionRows("Revenue", r.revenue, "Total revenue", r.revenue_cents, "revenue")}
                ${sectionRows("Expenses", r.expenses, "Total expenses", r.expense_cents, "expenses")}
                <tr class="fs-total fs-grand"><td class="fs-col-code"></td><td><strong>${r.net_income_cents < 0 ? "Net loss" : "Net income"}</strong></td><td class="num" data-total="net"><strong>${formatMoney(r.net_income_cents)}</strong></td></tr>
              </tbody>
            </table>
          </div>
          <p class="fs-note fs-noprint">An account number opens the ledger detail behind that line.</p>`}
    </section>
  `;
}

// ---- the balance sheet -----------------------------------------------------------------------------

function balanceView(model: FinstmtModel): TemplateResult {
  const wait = waiting(model);
  if (wait !== null) return wait;
  if (model.compare) {
    const rows = model.balanceCompare ?? [];
    return html`<section class="fs-panel fs-statement" aria-label="Balance sheet" data-statement>${heading(model)}${comparisonTable(model, rows)}</section>`;
  }
  const r = model.balance;
  if (r === null) return html`<section class="fs-panel"><p class="fs-dim">The balance sheet did not load. Try again.</p></section>`;
  const none = r.assets.length === 0 && r.liabilities.length === 0 && r.equity.length === 0;
  const mark = r.balanced ? pill("good", "Balanced") : pill("stop", "Out of balance: assets do not equal liabilities plus equity plus net income");
  return html`
    <section class="fs-panel fs-statement" aria-label="Balance sheet" data-statement>
      ${heading(model, mark)}
      ${tiles([["Assets", r.total_assets_cents, "assets"], ["Liabilities", r.total_liabilities_cents, "liabilities"], ["Equity", r.total_equity_cents, "equity"], ["Net income for the year", r.net_income_cents, "net"]])}
      ${none
        ? empty("No posted journals in this year.", "The balance sheet fills as journals post.")
        : html`<div class="fs-scroll">
            <table aria-label="Balance sheet lines">
              <thead><tr><th class="fs-col-code">Account</th><th>Name</th><th class="num">Amount</th></tr></thead>
              <tbody>
                ${sectionRows("Assets", r.assets, "Total assets", r.total_assets_cents, "assets")}
                ${sectionRows("Liabilities", r.liabilities, "Total liabilities", r.total_liabilities_cents, "liabilities")}
                ${sectionRows("Equity", r.equity, "Total equity", r.total_equity_cents, "equity")}
                <tr class="fs-total fs-grand"><td class="fs-col-code"></td><td><strong>Net income for the year</strong></td><td class="num" data-total="net"><strong>${formatMoney(r.net_income_cents)}</strong></td></tr>
              </tbody>
            </table>
          </div>
          <p class="fs-note fs-noprint">A cash flow statement is ${later("later")}: the books do not serve one yet.</p>`}
    </section>
  `;
}

// ---- the trial balance -----------------------------------------------------------------------------

function trialView(model: FinstmtModel): TemplateResult {
  const wait = waiting(model);
  if (wait !== null) return wait;
  const r = model.trial;
  if (r === null) return html`<section class="fs-panel"><p class="fs-dim">The trial balance did not load. Try again.</p></section>`;
  const mark = r.in_balance ? pill("good", "In balance") : pill("stop", "Out of balance: debits and credits differ");
  return html`
    <section class="fs-panel fs-statement" aria-label="Trial balance" data-statement>
      ${heading(model, mark)}
      ${r.rows.length === 0
        ? empty("No posted journals in this year.", "The trial balance fills as journals post.")
        : html`<div class="fs-scroll">
            <table aria-label="Trial balance lines">
              <thead><tr><th class="fs-col-code">Account</th><th>Name</th><th class="fs-col-type">Type</th><th class="num">Debit</th><th class="num">Credit</th><th class="num fs-col-balance">Balance</th></tr></thead>
              <tbody>
                ${r.rows.map((x) => html`<tr data-account=${x.account_code}>
                  <td class="fs-col-code fs-nowrap"><strong>${x.account_code}</strong></td>
                  <td><span class="fs-name">${x.account_name}</span><span class="fs-code-narrow">${x.account_code}</span></td>
                  <td class="fs-col-type">${accountTypeWord(x.account_type)}</td>
                  <td class="num">${formatMoney(x.total_debit_cents)}</td>
                  <td class="num">${formatMoney(x.total_credit_cents)}</td>
                  <td class="num fs-col-balance">${formatMoney(x.balance_cents)}</td>
                </tr>`)}
                <tr class="fs-total"><td class="fs-col-code"></td><td><strong>Total</strong></td><td class="fs-col-type"></td><td class="num" data-total="debit"><strong>${formatMoney(r.total_debit_cents)}</strong></td><td class="num" data-total="credit"><strong>${formatMoney(r.total_credit_cents)}</strong></td><td class="fs-col-balance"></td></tr>
              </tbody>
            </table>
          </div>
          <p class="fs-note fs-noprint">The trial balance is the whole company's: it takes no branch.</p>`}
    </section>
  `;
}

// ---- the ledger detail -----------------------------------------------------------------------------

function tieWords(model: FinstmtModel, d: FinStmtGLDetailResponse): TemplateResult {
  const drill = model.drill;
  if (drill === null) return html`${nothing}`;
  if (drill.statementCents === null) return html`${nothing}`;
  return drill.statementCents === d.tie_cents
    ? html`<p class="fs-alert fs-alert-ok" role="status" data-tie>Ties to the statement line: the detail adds to ${formatMoney(d.tie_cents)}, the figure on ${drill.accountCode} ${drill.accountName}.</p>`
    : html`<p class="fs-alert fs-alert-error" role="alert" data-tie>Does not tie: the statement line says ${formatMoney(drill.statementCents)} and the detail adds to ${formatMoney(d.tie_cents)}.</p>`;
}

function detailView(model: FinstmtModel): TemplateResult {
  const wait = waiting(model);
  if (wait !== null) return wait;
  const d = model.detail;
  if (d === null) return html`<section class="fs-panel"><p class="fs-dim">The ledger detail did not load. Try again.</p></section>`;
  const rows = d.rows.slice(0, model.shown);
  return html`
    <section class="fs-panel fs-statement" aria-label="General ledger detail" data-statement data-detail-panel>
      ${heading(model)}
      <div class="fs-form fs-noprint" role="group" aria-label="Narrow the detail">
        <label class="fs-field">Account number
          <input type="text" inputmode="numeric" data-field="fs-account" autocomplete="off" aria-label="Account number" placeholder="All accounts" />
        </label>
        <div class="fs-actions">
          <button type="button" class="btn btn-secondary" data-action="fs-gl-apply">Show detail</button>
          ${model.account !== "" || model.drill !== null ? html`<button type="button" class="btn btn-secondary" data-action="fs-gl-clear">All accounts</button>` : nothing}
          ${model.drill !== null ? html`<button type="button" class="btn btn-secondary" data-action="fs-back" data-back>Back to ${tabTitle(model.drill.from).toLowerCase()}</button>` : nothing}
        </div>
      </div>
      ${tieWords(model, d)}
      ${d.rows.length === 0
        ? empty("No posted lines for these books.", "Choose another period or account.")
        : html`<div class="fs-scroll">
            <table class="fs-cards" aria-label="General ledger detail lines">
              <thead><tr><th>Entry</th><th class="fs-col-period">Period</th><th>Account</th><th class="fs-col-source">Source</th><th>Memo</th><th class="num">Debit</th><th class="num">Credit</th></tr></thead>
              <tbody>
                ${rows.map((x) => html`<tr data-line=${x.line_id}>
                  <td data-label="Entry" class="fs-nowrap"><strong>${entryLabel(x.entry_no)}</strong></td>
                  <td data-label="Period" class="fs-col-period">${periodLabel(x.period_year, x.period_month)}</td>
                  <td data-label="Account"><strong>${x.account_code}</strong> ${x.account_name}</td>
                  <td data-label="Source" class="fs-col-source">${sourceWord(x.source)}</td>
                  <td data-label="Memo">${x.memo}</td>
                  <td data-label="Debit" class="num">${x.debit_cents === 0 ? "" : formatMoney(x.debit_cents)}</td>
                  <td data-label="Credit" class="num">${x.credit_cents === 0 ? "" : formatMoney(x.credit_cents)}</td>
                </tr>`)}
              </tbody>
            </table>
          </div>
          <p class="fs-note" data-count>Showing ${rows.length} of ${d.rows.length} lines.</p>
          ${rows.length < d.rows.length ? html`<button type="button" class="btn btn-secondary fs-noprint" data-action="fs-more">Show more</button>` : nothing}`}
    </section>
  `;
}

// ---- the receivables aging -------------------------------------------------------------------------

function agingView(model: FinstmtModel): TemplateResult {
  const wait = waiting(model);
  if (wait !== null) return wait;
  const a = model.aging;
  if (a === null) return html`<section class="fs-panel"><p class="fs-dim">The receivables aging did not load. Try again.</p></section>`;
  return html`
    <section class="fs-panel fs-statement" aria-label="Receivables aging" data-statement>
      ${heading(model)}
      <dl class="fs-tiles fs-tiles-aging">
        ${a.buckets.map((b) => html`<div><dt>${bucketWord(b.bucket)}</dt><dd data-bucket=${b.bucket}>${formatMoney(b.cents)}</dd></div>`)}
        <div class="fs-tile-total"><dt>Receivables</dt><dd data-total="grand">${formatMoney(a.grand_total_cents)}</dd></div>
      </dl>
      ${a.rows.length === 0
        ? empty("Nothing is owed.", "Open invoices appear here as they are issued.")
        : html`<div class="fs-scroll">
            <table class="fs-cards" aria-label="Receivables aging lines">
              <thead><tr><th>Customer</th><th>Branch</th><th class="fs-col-source">Source</th><th class="num">Days</th><th>Band</th><th class="num">Amount</th></tr></thead>
              <tbody>
                ${a.rows.map((x) => html`<tr data-aging=${x.source_ref_id}>
                  <td data-label="Customer"><strong>${x.customer_name}</strong></td>
                  <td data-label="Branch">${scopeLabel(x.branch_id, model.branches)}</td>
                  <td data-label="Source" class="fs-col-source">${sourceWord(x.source_type)}</td>
                  <td data-label="Days" class="num">${x.age_days}</td>
                  <td data-label="Band">${bucketWord(x.bucket)}</td>
                  <td data-label="Amount" class="num">${formatMoney(x.amount_cents)}</td>
                </tr>`)}
              </tbody>
            </table>
          </div>`}
    </section>
  `;
}

// ---- the saved packs -------------------------------------------------------------------------------

function packRow(model: FinstmtModel, p: FinStmtSchedule): TemplateResult {
  const confirming = model.confirmDelete === p.id;
  return html`<tr data-pack=${p.id} aria-current="${model.editing === p.id ? "true" : "false"}">
    <td data-label="Statement"><strong>${reportWord(p.report)}</strong></td>
    <td data-label="Branch">${scopeLabel(p.branch_id, model.branches)}</td>
    <td data-label="How often">${cadenceWord(p.cadence)}</td>
    <td data-label="Recipients" class="fs-wrap">${p.recipients.split(",").join(", ")}</td>
    <td data-label="State">${p.is_active ? pill("good", "On") : pill("plain", "Paused")}</td>
    <td data-label="Actions">
      ${confirming
        ? html`<span class="fs-confirm" role="group" aria-label="Delete this pack"><span>Delete this pack?</span>
            <button type="button" class="btn btn-secondary" data-action="fs-pack-delete-yes" data-id=${p.id}>Yes, delete</button>
            <button type="button" class="btn btn-secondary" data-action="fs-pack-delete-no">Keep it</button></span>`
        : html`<button type="button" class="fs-link" data-action="fs-pack-edit" data-id=${p.id} aria-label="Change the ${reportWord(p.report)} pack">Change</button>
            <button type="button" class="fs-link" data-action="fs-pack-delete" data-id=${p.id} aria-label="Delete the ${reportWord(p.report)} pack">Delete</button>`}
    </td>
  </tr>`;
}

function packForm(model: FinstmtModel): TemplateResult {
  return html`
    <section class="fs-panel" aria-label="${model.editing === null ? "Save a pack" : "Change a pack"}" data-pack-form>
      <div class="fs-panel-head"><h3>${model.editing === null ? "Save a pack" : "Change a pack"}</h3></div>
      <div class="fs-form">
        <label class="fs-field">Statement
          <select data-field="fs-pack-report" aria-label="Statement">
            <option data-id="">Choose a statement</option>
            ${REPORTS.map((r) => html`<option data-id=${r}>${reportWord(r)}</option>`)}
          </select>
        </label>
        <label class="fs-field">How often
          <select data-field="fs-pack-cadence" aria-label="How often">
            <option data-id="">Choose how often</option>
            ${CADENCES.map((c) => html`<option data-id=${c}>${cadenceWord(c)}</option>`)}
          </select>
        </label>
        <label class="fs-field">Branch
          <select data-field="fs-pack-branch" aria-label="Branch for the pack">
            <option data-id="">All branches</option>
            ${model.branches.map((b) => html`<option data-id=${b.id}>${b.name}</option>`)}
          </select>
        </label>
        <label class="fs-field">State
          <select data-field="fs-pack-active" aria-label="State of the pack">
            <option data-id="yes">On</option>
            <option data-id="no">Paused</option>
          </select>
        </label>
        <label class="fs-field fs-field-wide">Recipients, separated by commas
          <input type="text" inputmode="email" data-field="fs-recipients" autocomplete="off" aria-label="Recipients" placeholder="name@example.com, other@example.com" />
        </label>
      </div>
      <div class="fs-actions">
        <button type="button" class="btn btn-primary" data-action="fs-pack-save" ?disabled=${model.busy}>${model.editing === null ? "Save pack" : "Save change"}</button>
        ${model.editing !== null ? html`<button type="button" class="btn btn-secondary" data-action="fs-pack-cancel">Cancel</button>` : nothing}
      </div>
    </section>
  `;
}

function packsView(model: FinstmtModel): TemplateResult {
  const wait = waiting(model);
  const list =
    wait !== null
      ? wait
      : html`<section class="fs-panel" aria-label="Saved packs" data-packs>
          <div class="fs-panel-head"><h2>Saved packs</h2><span class="fs-dim" data-count>${model.packs.length === 1 ? "1 pack" : `${model.packs.length} packs`}</span></div>
          <p class="fs-note">A saved pack records which statement goes to whom and how often. Sending packs by e-mail is ${later("later")}: nothing is sent yet.</p>
          ${model.packs.length === 0
            ? empty("No packs saved.", "Save one below to record who should receive a statement.")
            : html`<div class="fs-scroll">
                <table class="fs-cards" aria-label="Saved packs">
                  <thead><tr><th>Statement</th><th>Branch</th><th>How often</th><th>Recipients</th><th>State</th><th></th></tr></thead>
                  <tbody>${model.packs.map((p) => packRow(model, p))}</tbody>
                </table>
              </div>`}
        </section>`;
  return html`${list}${packForm(model)}`;
}

// ---- the whole slice -------------------------------------------------------------------------------

function body(model: FinstmtModel): TemplateResult {
  if (model.periods.length === 0 && model.tab !== "packs" && model.tab !== "aging") {
    return html`<section class="fs-panel">${empty("The books hold no fiscal periods yet.", "Statements appear once the ledger has a period.")}</section>`;
  }
  switch (model.tab) {
    case "income":
      return incomeView(model);
    case "balance":
      return balanceView(model);
    case "trial":
      return trialView(model);
    case "gl":
      return detailView(model);
    case "aging":
      return agingView(model);
    case "packs":
      return packsView(model);
  }
}

export function renderFinstmt(model: FinstmtModel): TemplateResult {
  if (model.phase === "idle" || model.phase === "loading") return loading();
  if (model.phase === "error") return loadFailed(model);
  return html`
    <div class="fs-head fs-noprint">
      <h1>Statements</h1>
      <p class="fs-dim">The books as statements: income, balance sheet, trial balance, the ledger behind any line, and receivables aging.</p>
    </div>
    ${tabs(model)} ${model.tab === "packs" ? nothing : controls(model)} ${messages(model)} ${body(model)}
  `;
}
