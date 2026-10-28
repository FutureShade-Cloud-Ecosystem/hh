// The ledger slice's screens, as plain render functions of a model. They hold
// no state and own no events: every control is a plain element carrying a
// data-action name and the slice's click listener acts on it (the design
// system's position rules allow no @ or . binding in an app, and the only
// bound values are class, role, aria- and data- attributes).
//
// Every figure on a screen is the server's, formatted: the app computes no
// money. References: the Money Desk boards (controller-home, trial-balance),
// drawn from the original app's pending approvals, home bento, GL workbench
// and trial balance screens.
import { html, nothing, type TemplateResult } from "lit";
import type { Branch, GlJournal, GlTrialBalanceResult, IdentityApproval } from "@hardscapehouse/api-client";
import { approvalKind, approvalTitle, canDecide, detailWords, entryLabel, floorFigures, floorWords, journalTone, journalWord, monthWord, systemJournalNote } from "./gl-logic.js";
import { formatMoney } from "./gl-money.js";
import { chartView, checksView, composerView, confirmFor, linesTable, periodsView, policyView, recurringView, type BooksModel } from "./gl-books.js";

export type GlTab = "approvals" | "journals" | "chart" | "trial" | "periods" | "checks" | "policy";
export type JournalFilter = "all" | "pending" | "posted" | "draft" | "void";

export interface GlModel {
  readonly phase: "loading" | "ready" | "error";
  readonly tab: GlTab;
  readonly userId: string;
  readonly approvals: readonly IdentityApproval[];
  readonly selectedApproval: string | null;
  readonly choice: "approve" | "decline";
  readonly approvalJournal: GlJournal | null;
  readonly journals: readonly GlJournal[];
  readonly journalFilter: JournalFilter;
  readonly selectedJournal: string | null;
  readonly trial: GlTrialBalanceResult | null;
  readonly trialBranch: string;
  readonly branches: readonly Branch[];
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
  readonly postingQueueWords: string;
  readonly books: BooksModel;
}

const FILTERS: ReadonlyArray<{ id: JournalFilter; word: string }> = [
  { id: "all", word: "All" },
  { id: "pending", word: "Pending" },
  { id: "posted", word: "Posted" },
  { id: "draft", word: "Draft" },
  { id: "void", word: "Void" },
];

function filterMatches(filter: JournalFilter, journal: GlJournal): boolean {
  if (filter === "all") return true;
  if (filter === "pending") return journal.derived_status === "PENDING_APPROVAL";
  if (filter === "posted") return journal.derived_status === "POSTED";
  if (filter === "draft") return journal.derived_status === "DRAFT";
  return journal.derived_status === "VOID";
}

function branchName(branches: readonly Branch[], id: string | undefined): string {
  if (id === undefined || id === "") return "";
  return branches.find((b) => b.id === id)?.name ?? "";
}

/** The journal a JE request is about, when the register holds it. */
function journalOf(model: GlModel, row: IdentityApproval): GlJournal | undefined {
  if (row.policy_type !== "JE_OVER_THRESHOLD" || row.subject_ref === undefined) return undefined;
  return model.journals.find((j) => j.id === row.subject_ref);
}

/** What a request says it is about, in words: the floor request read out, a journal named by its number. */
function aboutWords(model: GlModel, row: IdentityApproval): string {
  const floor = floorWords(row.detail);
  if (floor !== null) return floor;
  const journal = journalOf(model, row);
  if (journal !== undefined) return `${entryLabel(journal)} · ${journal.memo}`;
  return detailWords(row.detail);
}

/** A feature the core serves that this increment does not draw yet, said in words. */
function later(word: string): TemplateResult {
  return html`<span class="gl-later">${word}</span>`;
}

function messages(model: GlModel): TemplateResult {
  return html`
    ${model.error !== "" && model.books.confirm === null ? html`<p class="gl-alert gl-alert-error" role="alert" data-error>${model.error}</p>` : nothing}
    ${model.notice !== "" ? html`<p class="gl-alert gl-alert-ok" role="status" data-notice>${model.notice}</p>` : nothing}
  `;
}

function pill(tone: string, word: string): TemplateResult {
  return html`<span class="gl-status gl-status-${tone}">${word}</span>`;
}

// ---- the way in ----------------------------------------------------------------------------------

function loading(): TemplateResult {
  return html`<p class="gl-dim" role="status">Loading the approvals and the ledger.</p>`;
}

function loadFailed(model: GlModel): TemplateResult {
  return html`
    <p class="gl-alert gl-alert-error" role="alert" data-error>${model.error}</p>
    <button type="button" class="btn btn-secondary" data-action="gl-reload">Try again</button>
  `;
}

/** The tabs a mode holds, in bar order. A reader (Payables and Receivables) has no approvals to decide. */
export function tabsFor(mode: "controller" | "reader"): ReadonlyArray<{ id: GlTab; label: string }> {
  const all: ReadonlyArray<{ id: GlTab; label: string }> = [
    { id: "approvals", label: "Approvals" },
    { id: "journals", label: "Journals" },
    { id: "chart", label: "Chart" },
    { id: "trial", label: "Trial balance" },
    { id: "periods", label: "Periods" },
    { id: "checks", label: "Checks" },
    { id: "policy", label: "Policy" },
  ];
  return mode === "controller" ? all : all.filter((t) => t.id !== "approvals");
}

function tabs(model: GlModel): TemplateResult {
  const pending = model.journals.filter((j) => j.derived_status === "PENDING_APPROVAL").length;
  const count = (id: GlTab): number | null => (id === "approvals" ? model.approvals.length : id === "journals" && pending > 0 ? pending : null);
  return html`
    <nav class="gl-tabs" aria-label="Ledger views">
      ${tabsFor(model.books.mode).map((t) => {
        const n = count(t.id);
        return html`<button type="button" class="gl-tab" data-action="gl-tab" data-tab=${t.id} data-tab-id=${t.id} aria-current="${model.tab === t.id ? "true" : "false"}">${t.label}${n === null ? nothing : html`<span class="gl-count">${n}</span>`}</button>`;
      })}
    </nav>
  `;
}

// ---- the approvals queue and the decision ---------------------------------------------------------

function queueRow(model: GlModel, row: IdentityApproval): TemplateResult {
  const own = row.requested_by === model.userId;
  const selected = model.selectedApproval === row.id;
  const kind = approvalKind(row.policy_type);
  return html`
    <li>
      <button type="button" class="gl-row gl-kind-${kind.toLowerCase()}" data-action="gl-select-approval" data-id=${row.id} data-approval-id=${row.id} aria-current="${selected ? "true" : "false"}">
        <span class="gl-row-main">
          <span class="gl-row-title">${approvalTitle(row.policy_type)}</span>
          <span class="gl-row-detail">${aboutWords(model, row)}</span>
          ${row.amount_cents !== undefined ? html`<span class="gl-row-detail">Amount <strong>${formatMoney(row.amount_cents)}</strong></span>` : nothing}
          ${own ? html`<span class="gl-row-own" data-own>Filed by you</span>` : nothing}
        </span>
        <span class="gl-pill">${kind}</span>
      </button>
    </li>
  `;
}

function journalLines(journal: GlJournal | null): TemplateResult {
  return linesTable(journal);
}

function choiceCard(model: GlModel, id: "approve" | "decline", title: string, line: string, held: boolean): TemplateResult {
  if (held) {
    return html`
      <div class="gl-choice gl-choice-held" data-choice-held=${id}>
        <span class="gl-choice-title">${title}</span>
        <span class="gl-choice-line">Held: not yours to decide.</span>
      </div>
    `;
  }
  return html`
    <button type="button" class="gl-choice" role="radio" data-action="gl-choice" data-choice=${id} aria-checked="${model.choice === id ? "true" : "false"}">
      <span class="gl-choice-title">${title}</span>
      <span class="gl-choice-line">${line}</span>
    </button>
  `;
}

function oneDecisionWords(row: IdentityApproval): TemplateResult {
  if (row.policy_type === "MARGIN_BELOW_MINIMUM") {
    return html`<strong>One decision, one quote.</strong> The approval is spent on that one quote. The rep sees the answer and the quote is repriced once: a re-price asks again.`;
  }
  if (row.policy_type === "JE_OVER_THRESHOLD") {
    return html`<strong>Approving posts the journal.</strong> The post and your decision commit together, so the queue and the ledger never disagree.`;
  }
  return html`<strong>One decision.</strong> Your answer is final for this request.`;
}

function floorTiles(row: IdentityApproval): TemplateResult {
  const f = floorFigures(row.detail);
  if (f === null) return html`${nothing}`;
  return html`
    <dl class="gl-tiles" aria-label="The request's figures" data-floor-tiles>
      <div><dt>Quote</dt><dd>${f.quote}</dd></div>
      <div><dt>Line</dt><dd>${f.line}</dd></div>
      <div><dt>Discount asked</dt><dd>${f.discount}</dd></div>
      <div><dt>Margin asked</dt><dd class="gl-stop">${f.marginAsked}</dd></div>
      <div><dt>Floor margin</dt><dd>${f.floorMargin}</dd></div>
    </dl>
    <p class="gl-dim">The prices behind them (asked, floor, list) are not on the request yet: ${later("later")}</p>
  `;
}

function decidePanel(model: GlModel): TemplateResult {
  const row = model.approvals.find((a) => a.id === model.selectedApproval);
  if (row === undefined) {
    return html`
      <section class="gl-panel" aria-label="Decision">
        <p class="gl-dim">Pick a request to decide it.</p>
      </section>
    `;
  }
  const own = !canDecide(row, model.userId);
  const isJournal = row.policy_type === "JE_OVER_THRESHOLD";
  const where = branchName(model.branches, row.branch_id);
  const approveWord = isJournal ? "Approve and post" : "Approve as asked";
  const approveLine = isJournal
    ? "The journal posts to the ledger as filed."
    : row.policy_type === "MARGIN_BELOW_MINIMUM"
      ? "Spent on this one quote: a re-price asks again."
      : "The request goes ahead as filed.";
  const declineLine = isJournal ? "The journal stays parked as a draft." : "The requester gets your note.";
  return html`
    <section class="gl-panel" aria-label="Decision" data-decide-panel>
      <div class="gl-panel-head">
        <h2>Decide: ${journalOf(model, row) !== undefined ? html`journal ${entryLabel(journalOf(model, row) as GlJournal)}` : approvalTitle(row.policy_type)}</h2>
        <span class="gl-pill">${approvalKind(row.policy_type)}</span>
      </div>
      ${own
        ? html`<p class="gl-alert gl-alert-error" role="alert" data-own-notice><strong>You filed this request.</strong> Four eyes: the person who files a request never decides it. Another approver on the list decides it: the General Manager, or a Financial Controller who did not file it.</p>`
        : nothing}
      <p class="gl-fact" data-detail>${aboutWords(model, row)}</p>
      ${floorTiles(row)}
      ${where !== "" ? html`<p class="gl-dim">Branch: ${where}</p>` : nothing}
      ${row.amount_cents !== undefined ? html`<p class="gl-figure">Amount <strong>${formatMoney(row.amount_cents)}</strong></p>` : nothing}
      ${isJournal ? journalLines(model.approvalJournal) : nothing}
      <div class="gl-choices" role="radiogroup" aria-label="Your decision">
        ${choiceCard(model, "approve", approveWord, approveLine, own)} ${choiceCard(model, "decline", "Decline", declineLine, own)}
        ${row.policy_type === "MARGIN_BELOW_MINIMUM"
          ? html`<div class="gl-choice gl-choice-held" data-choice-later><span class="gl-choice-title">Approve at a different price ${later("later")}</span><span class="gl-choice-line">Only approve or decline is here today.</span></div>`
          : nothing}
      </div>
      ${own
        ? nothing
        : html`
            <label class="gl-field">Note to the requester
              <textarea data-field="note" rows="3" aria-label="Note to the requester"></textarea>
            </label>
          `}
      <div class="gl-actions">
        <button type="button" class="btn btn-primary" data-action="gl-decide" data-id=${row.id} ?disabled=${own || model.busy}>Decide</button>
        <button type="button" class="btn btn-secondary" data-action="gl-cancel">Cancel</button>
      </div>
      ${own ? nothing : html`<p class="gl-note">${oneDecisionWords(row)}</p>`}
    </section>
  `;
}

function approvalsView(model: GlModel): TemplateResult {
  return html`
    <div class="gl-split">
      <section class="gl-panel" aria-label="Needs you">
        <div class="gl-panel-head">
          <h3>Needs you</h3>
          ${model.approvals.length === 0 ? nothing : html`<span class="gl-waiting" data-waiting>${model.approvals.length} waiting</span>`}
        </div>
        ${model.approvals.length === 0
          ? html`<div class="gl-empty"><strong>Nothing waits on you.</strong><span class="gl-dim">Requests to decide show here the moment they are filed.</span></div>`
          : html`<ul class="gl-list" aria-label="Approvals waiting">${model.approvals.map((row) => queueRow(model, row))}</ul>`}
      </section>
      ${decidePanel(model)}
    </div>
  `;
}

// ---- the journal register -------------------------------------------------------------------------

function journalActions(model: GlModel, journal: GlJournal): TemplateResult {
  if (model.books.mode !== "controller") {
    return html`<p class="gl-note" data-held>Posting, approving, voiding and reversing a journal are for the Financial Controller or the General Manager.</p>`;
  }
  const request = model.approvals.find((a) => a.subject_ref === journal.id);
  const draft = journal.status === "DRAFT";
  const own = request !== undefined && !canDecide(request, model.userId);
  // A journal a module posted is refused by the server (FW1-09a), so the panel says who owns it instead
  // of offering a press that cannot go through.
  const systemNote = systemJournalNote(journal);
  return html`
    <div class="gl-actions gl-actions-spaced">
      ${draft && request !== undefined
        ? html`<button type="button" class="btn btn-primary" data-action="gl-approve-journal" data-id=${journal.id} ?disabled=${own || model.busy}>Approve</button>`
        : nothing}
      ${draft && request === undefined
        ? html`<button type="button" class="btn btn-primary" data-action="gl-approve-route" data-id=${journal.id} ?disabled=${model.busy}>Approve here</button>`
        : nothing}
      ${draft ? html`<button type="button" class="btn btn-secondary" data-action="gl-void" data-id=${journal.id} ?disabled=${model.busy}>Void</button>` : nothing}
      ${journal.status === "POSTED" && systemNote === "" ? html`<button type="button" class="btn btn-secondary" data-action="gl-reverse" data-id=${journal.id} ?disabled=${model.busy}>Reverse</button>` : nothing}
    </div>
    ${draft && request !== undefined
      ? html`<p class="gl-note">${own ? "You made this journal, so you cannot approve it. Another approver posts it." : "Approving posts it as filed. The maker cannot approve their own."}</p>`
      : nothing}
    ${draft && request === undefined
      ? html`<p class="gl-note">This draft has no approval request in the queue. The server says whether it can post from here.</p>`
      : nothing}
    ${systemNote !== "" ? html`<p class="gl-note" data-system-note>${systemNote}</p>` : nothing}
    ${journal.status === "POSTED" && systemNote === "" ? html`<p class="gl-note">A posted journal is corrected with a reversing journal. Reverse asks you to confirm first.</p>` : nothing}
  `;
}

function journalDetail(model: GlModel): TemplateResult {
  const journal = model.journals.find((j) => j.id === model.selectedJournal);
  if (journal === undefined) {
    return html`<section class="gl-panel" aria-label="Journal"><p class="gl-dim">Pick a journal to read it.</p></section>`;
  }
  const pending = journal.derived_status === "PENDING_APPROVAL";
  const where = branchName(model.branches, journal.branch_id);
  const reverses = journal.reverses_journal_id === undefined ? undefined : model.journals.find((j) => j.id === journal.reverses_journal_id);
  return html`
    ${confirmFor(model, ["reverse", "void"])}
    <section class="gl-panel" aria-label="Journal" data-journal-panel>
      <div class="gl-panel-head">
        <h2>${entryLabel(journal)} · ${journal.memo}</h2>
        ${pill(journalTone(journal.derived_status), journalWord(journal.derived_status))}
      </div>
      <dl class="gl-facts">
        <dt>Source</dt><dd>${journal.source === "MANUAL" ? "Manual" : journal.source}</dd>
        ${where !== "" ? html`<dt>Branch</dt><dd>${where}</dd>` : nothing}
        ${pending ? html`<dt>Why it waits</dt><dd>At or over the approval threshold (GL policy).</dd>` : nothing}
        ${reverses !== undefined ? html`<dt>Reverses</dt><dd data-reverses>${entryLabel(reverses)}</dd>` : nothing}
      </dl>
      ${journalLines(journal)}
      ${journalActions(model, journal)}
    </section>
  `;
}

function journalsView(model: GlModel): TemplateResult {
  const rows = model.journals.filter((j) => filterMatches(model.journalFilter, j));
  return html`
    ${composerView(model)}
    <div class="gl-split">
      <section class="gl-panel" aria-label="Journals">
        <div class="gl-panel-head">
          <h3>Journals</h3>
          <div class="gl-filters" role="group" aria-label="Filter journals">
            ${FILTERS.map((f) => html`<button type="button" class="gl-filter" data-action="gl-filter" data-filter=${f.id} aria-pressed="${model.journalFilter === f.id ? "true" : "false"}">${f.word}${f.id === "all" ? nothing : html` ${model.journals.filter((j) => filterMatches(f.id, j)).length}`}</button>`)}
          </div>
        </div>
        ${rows.length === 0
          ? html`<div class="gl-empty"><strong>No journals here.</strong><span class="gl-dim">Nothing matches this filter.</span></div>`
          : html`
              <div class="gl-scroll">
                <table aria-label="Journal register">
                  <thead><tr><th>#</th><th>Memo</th><th>Status</th></tr></thead>
                  <tbody>
                    ${rows.map(
                      (j) => html`<tr aria-current="${model.selectedJournal === j.id ? "true" : "false"}">
                        <td class="gl-nowrap"><button type="button" class="gl-link" data-action="gl-select-journal" data-id=${j.id} data-journal-id=${j.id}>${entryLabel(j)}</button></td>
                        <td>${j.memo}<span class="gl-row-detail">${j.source === "MANUAL" ? "Manual" : j.source}</span></td>
                        <td>${pill(journalTone(j.derived_status), journalWord(j.derived_status))}</td>
                      </tr>`,
                    )}
                  </tbody>
                </table>
              </div>
            `}
        <p class="gl-note">Pending approval is a derived label: a manual draft at or over the threshold. Nothing writes it to the row.</p>
      </section>
      <div class="gl-stack">${journalDetail(model)}</div>
    </div>
    ${recurringView(model)}
  `;
}

// ---- the trial balance ----------------------------------------------------------------------------

function exportRow(model: GlModel, year: number): TemplateResult {
  if (model.books.mode !== "controller") return html`<p class="gl-note" data-held>The ledger export is for the Financial Controller.</p>`;
  return html`
    <div class="gl-actions gl-actions-spaced">
      <button type="button" class="btn btn-secondary" data-action="gl-export" data-year=${String(year)} ?disabled=${model.busy || model.books.exporting}>Download the ${model.books.trialMonth === 0 ? "year" : monthWord(model.books.trialMonth)} export</button>
    </div>
    <p class="gl-dim">A draft in the parity format, one row per posted line, for the branch chosen above. It is not a filing.</p>
  `;
}

function trialView(model: GlModel): TemplateResult {
  const trial = model.trial;
  if (trial === null) {
    return html`<section class="gl-panel" aria-label="Trial balance"><p class="gl-dim">The trial balance did not load. Reload to try again.</p></section>`;
  }
  const inBalance = trial.balance_cents === 0;
  return html`
    <section class="gl-panel" aria-label="Trial balance" data-trial>
      <div class="gl-panel-head">
        <h3>Trial balance · ${trial.month === undefined ? `fiscal year ${trial.fiscal_year}` : `${monthWord(trial.month)} ${trial.fiscal_year}`}</h3>
        ${inBalance ? pill("good", "In balance") : pill("stop", `Out of balance by ${formatMoney(Math.abs(trial.balance_cents))}`)}
      </div>
      <div class="gl-filters" role="group" aria-label="Branch">
        <button type="button" class="gl-filter" data-action="gl-trial-branch" data-branch="" aria-pressed="${model.trialBranch === "" ? "true" : "false"}">All branches</button>
        ${model.branches.map((b) => html`<button type="button" class="gl-filter" data-action="gl-trial-branch" data-branch=${b.id} aria-pressed="${model.trialBranch === b.id ? "true" : "false"}">${b.name}</button>`)}
      </div>
      <div class="gl-filters" role="group" aria-label="Period">
        <button type="button" class="gl-filter" data-action="gl-trial-month" data-month="0" aria-pressed="${model.books.trialMonth === 0 ? "true" : "false"}">Whole year</button>
        ${model.books.periods.value
          .filter((p) => p.year === trial.fiscal_year)
          .map((p) => html`<button type="button" class="gl-filter" data-action="gl-trial-month" data-month=${String(p.month)} aria-pressed="${model.books.trialMonth === p.month ? "true" : "false"}">${monthWord(p.month).slice(0, 3)}</button>`)}
      </div>
      ${trial.rows.length === 0
        ? html`<div class="gl-empty"><strong>No posted journals yet.</strong><span class="gl-dim">The trial balance fills as journals post.</span></div>`
        : html`
            <div class="gl-scroll">
              <table aria-label="Trial balance rows">
                <thead><tr><th>Account</th><th class="gl-col-name">Name</th><th class="gl-col-type">Type</th><th class="num">Debit</th><th class="num">Credit</th></tr></thead>
                <tbody>
                  ${trial.rows.map(
                    (r) => html`<tr>
                      <td><strong>${r.account_code}</strong><span class="gl-name-narrow">${r.account_name}</span></td>
                      <td class="gl-col-name">${r.account_name}</td>
                      <td class="gl-col-type">${r.account_type.charAt(0) + r.account_type.slice(1).toLowerCase()}</td>
                      <td class="num">${formatMoney(r.debit_cents)}</td>
                      <td class="num">${formatMoney(r.credit_cents)}</td>
                    </tr>`,
                  )}
                  <tr class="gl-total"><td><strong>Total</strong></td><td class="gl-col-name"></td><td class="gl-col-type"></td><td class="num" data-total-debit>${formatMoney(trial.total_debit_cents)}</td><td class="num" data-total-credit>${formatMoney(trial.total_credit_cents)}</td></tr>
                </tbody>
              </table>
            </div>
          `}
      ${exportRow(model, trial.fiscal_year)}
      <p class="gl-dim gl-later-line" data-later-trial>The receivables tie out is on the Checks tab. A typed Excel workbook of the trial balance is ${later("later")}: the export is a CSV that opens in Excel.</p>
    </section>
  `;
}

// ---- the whole slice ------------------------------------------------------------------------------

const HEADINGS: Record<GlTab, { title: string; line: string }> = {
  approvals: { title: "Approvals", line: "What needs your decision: floor requests, parked journals and the exceptions." },
  journals: { title: "Ledger", line: "Post a journal, decide the parked ones, void a draft or reverse a posted entry." },
  chart: { title: "Ledger", line: "The chart of accounts: what a journal can post to." },
  trial: { title: "Ledger", line: "The trial balance for the year or one month, and the export." },
  periods: { title: "Ledger", line: "Which months are open to posting, the year end close, opening the next fiscal year, and the late posted journals." },
  checks: { title: "Ledger", line: "Does the ledger agree with the receivables ledger." },
  policy: { title: "Ledger", line: "The approval threshold and the year the books keep." },
};

export function renderGl(model: GlModel): TemplateResult {
  if (model.phase === "loading") return loading();
  if (model.phase === "error") return loadFailed(model);
  const head = HEADINGS[model.tab];
  return html`
    <div class="gl-head">
      <h1>${head.title}</h1>
      <p class="gl-dim">${head.line}</p>
    </div>
    ${model.postingQueueWords !== "" ? html`<p class="gl-alert gl-alert-error" role="alert" data-posting-queue>${model.postingQueueWords}</p>` : nothing}
    ${tabs(model)} ${messages(model)}
    ${model.tab === "approvals"
      ? approvalsView(model)
      : model.tab === "journals"
        ? journalsView(model)
        : model.tab === "chart"
          ? chartView(model)
          : model.tab === "trial"
            ? trialView(model)
            : model.tab === "periods"
              ? periodsView(model)
              : model.tab === "checks"
                ? checksView(model)
                : policyView(model)}
  `;
}
