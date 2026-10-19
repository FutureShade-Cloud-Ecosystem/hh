// Manager Home's reporting screens: the four panes (Templates, Runs,
// Commission rules, Accruals) as pure template functions over plain model
// objects; app-root owns every call and every state change.
//
// Port provenance: the original reporting console and its commission surface
// (repository hardscapeos_dibbits, read only; see PROVENANCE.md), rebuilt on
// this app's sheet and HH's tokens. The parameter form is GENERATED from the
// server's param specs, never hand written per report: the server refuses
// unknown parameters, so a form built from the same specs it validates
// against cannot silently filter on a misnamed field.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, in the data-, aria-, role and class positions; controls ride
// data-action names and the host's listeners act on them; field values are
// mirrored from input events and written back through app-root's field seam,
// never bound.
import { html, nothing, type TemplateResult } from "lit";
import type {
  AccrualView,
  AttributionView,
  CategoryView,
  PeriodView,
  ReportResultView,
  RuleView,
  RunView,
  SavedViewView,
  TemplateView,
} from "./reporting-api.js";
import {
  BASIS_CHOICES,
  REPORTING_PANES,
  basisLabel,
  bps,
  csvName,
  formatCell,
  isNumericKind,
  lineIsUnpaid,
  missingRequired,
  printAtNeeded,
  qty,
  resultSummary,
  totalCell,
  unattributedWords,
  type ReportingPane,
} from "./reporting-logic.js";
import { dateWords, grouped } from "./manager-logic.js";

// -- the model -----------------------------------------------------------------------------------

/** The commission register's morning: nothing previewed, nothing expanded, nothing frozen. */
function emptyRegister(): CommissionRegister {
  return {
    phase: "idle",
    ran: false,
    periods: [],
    postsToGl: false,
    note: "",
    error: "",
    notice: "",
    expandedRepId: "",
    expandedPhase: "ready",
    expandedPeriod: null,
    expandedError: "",
    computeBusyRep: "",
    runsPhase: "ready",
    runsError: "",
    runsItems: [],
    expandedRunId: "",
    expandedRunPhase: "ready",
    expandedRun: null,
    expandedRunError: "",
    runBusyId: "",
    accrual: null,
    accrualError: "",
    attribution: null,
    attributionError: "",
  };
}

/** The reporting view's morning state, before anything loads. */
export function emptyReporting(today: Date = new Date()): ReportingModel {
  const iso = (d: Date) => d.toISOString().slice(0, 10);
  const monthStart = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), 1));
  return {
    pane: "templates",
    branchNames: [],
    catalogPhase: "loading",
    catalogError: "",
    categories: [],
    selectedCode: "",
    template: null,
    values: {},
    printAt: "",
    busy: false,
    formError: "",
    sheetError: "",
    sheetNotice: "",
    result: null,
    savedPhase: "ready",
    savedError: "",
    savedViews: [],
    saveName: "",
    saveShared: false,
    saveBusy: false,
    saveError: "",
    viewBusyId: "",
    runsPhase: "loading",
    runsError: "",
    runs: [],
    expandedDocId: "",
    expandedDocPhase: "ready",
    expandedDoc: null,
    expandedDocError: "",
    docNotice: "",
    rulesPhase: "loading",
    rulesError: "",
    rulesRefusal: "",
    rules: [],
    canWriteRules: false,
    ruleDraft: {
      repUserId: "",
      basis: BASIS_CHOICES[0].value,
      ratePercent: "",
      effectiveFrom: iso(today),
      effectiveTo: "",
      branchName: "",
      note: "",
      busy: false,
      error: "",
    },
    retireBusyId: "",
    commissionFrom: iso(monthStart),
    commissionTo: iso(today),
    commissionBranchName: "",
    register: emptyRegister(),
  };
}

export interface RuleDraft {
  readonly repUserId: string;
  readonly basis: string;
  readonly ratePercent: string;
  readonly effectiveFrom: string;
  readonly effectiveTo: string;
  readonly branchName: string;
  readonly note: string;
  readonly busy: boolean;
  readonly error: string;
}

export interface CommissionRegister {
  readonly phase: "idle" | "loading" | "ready" | "error";
  readonly ran: boolean;
  readonly periods: readonly PeriodView[];
  readonly postsToGl: boolean;
  readonly note: string;
  readonly error: string;
  readonly notice: string;
  /** The preview row drilled through: rep user id, the lines' load state, the lines' period. */
  readonly expandedRepId: string;
  readonly expandedPhase: "loading" | "ready" | "error";
  readonly expandedPeriod: PeriodView | null;
  readonly expandedError: string;
  readonly computeBusyRep: string;
  /** The stored commission runs in the window, and the one drilled through. */
  readonly runsPhase: "loading" | "ready" | "error";
  readonly runsError: string;
  readonly runsItems: readonly PeriodView[];
  readonly expandedRunId: string;
  readonly expandedRunPhase: "loading" | "ready" | "error";
  readonly expandedRun: PeriodView | null;
  readonly expandedRunError: string;
  readonly runBusyId: string;
  readonly accrual: AccrualView | null;
  readonly accrualError: string;
  readonly attribution: AttributionView | null;
  readonly attributionError: string;
}

export interface ReportingModel {
  readonly pane: ReportingPane;
  /** The branches the caller reaches, by name: the selects show names, app-root resolves ids. */
  readonly branchNames: readonly string[];
  // Templates pane.
  readonly catalogPhase: "loading" | "ready" | "error";
  readonly catalogError: string;
  readonly categories: readonly CategoryView[];
  readonly selectedCode: string;
  readonly template: TemplateView | null;
  readonly values: Readonly<Record<string, string>>;
  readonly printAt: string;
  readonly busy: boolean;
  readonly formError: string;
  readonly sheetError: string;
  readonly sheetNotice: string;
  readonly result: ReportResultView | null;
  readonly savedPhase: "loading" | "ready" | "error";
  readonly savedError: string;
  readonly savedViews: readonly SavedViewView[];
  readonly saveName: string;
  readonly saveShared: boolean;
  readonly saveBusy: boolean;
  readonly saveError: string;
  readonly viewBusyId: string;
  // Runs pane.
  readonly runsPhase: "loading" | "ready" | "error";
  readonly runsError: string;
  readonly runs: readonly RunView[];
  readonly expandedDocId: string;
  readonly expandedDocPhase: "loading" | "ready" | "error";
  readonly expandedDoc: RunView | null;
  readonly expandedDocError: string;
  readonly docNotice: string;
  // Rules pane.
  readonly rulesPhase: "loading" | "ready" | "error";
  readonly rulesError: string;
  readonly rulesRefusal: string;
  readonly rules: readonly RuleView[];
  readonly canWriteRules: boolean;
  readonly ruleDraft: RuleDraft;
  readonly retireBusyId: string;
  // Accruals pane.
  readonly commissionFrom: string;
  readonly commissionTo: string;
  readonly commissionBranchName: string;
  readonly register: CommissionRegister;
}

// -- the view -------------------------------------------------------------------------------------

export function renderReporting(model: ReportingModel): TemplateResult {
  return html`
    <div class="view-head">
      <button type="button" class="btn btn-secondary back" data-action="back"><span class="chev" aria-hidden="true">‹</span>Back to Home</button>
      <h1>Reporting</h1>
      <p class="page-sub">The numbers the business ran on, and the commission register behind them.</p>
    </div>
    <div class="kind-tabs" role="tablist" aria-label="Reporting panes">
      ${REPORTING_PANES.map(
        (p) => html`
          <button
            type="button"
            class="kind-tab"
            role="tab"
            data-action="reporting-pane"
            data-pane=${p.pane}
            aria-selected=${model.pane === p.pane ? "true" : "false"}
          >
            ${p.word}
          </button>
        `,
      )}
    </div>
    <p class="def-note" data-test="reporting-pane-note">${REPORTING_PANES.find((p) => p.pane === model.pane)?.note ?? ""}</p>
    ${model.pane === "templates" ? renderTemplatesPane(model) : nothing}
    ${model.pane === "runs" ? renderRunsPane(model) : nothing}
    ${model.pane === "rules" ? renderRulesPane(model) : nothing}
    ${model.pane === "accruals" ? renderAccrualsPane(model) : nothing}
  `;
}

// -- Templates pane ---------------------------------------------------------------------------------

function renderTemplatesPane(model: ReportingModel): TemplateResult {
  if (model.catalogPhase === "loading") {
    return html`<p class="notice-line" data-test="reporting-loading">Loading the report catalogue…</p>`;
  }
  if (model.catalogPhase === "error") {
    return html`
      <p class="error-line" data-test="reporting-error">${model.catalogError}</p>
      <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reporting-reload" data-what="catalog">Try again</button></div>
    `;
  }
  if (model.categories.length === 0) {
    return html`<section class="sheet"><div class="list-card"><p class="notice-line" data-test="reporting-empty">No reports are published in this catalogue yet.</p></div></section>`;
  }
  return html`
    <div class="report-layout">
      <nav class="report-nav list-card" aria-label="Report catalogue">
        ${model.categories.map(
          (c) => html`
            <p class="nav-cat">${c.label}</p>
            ${c.templates.map(
              (t) => html`
                <button
                  type="button"
                  class="nav-item"
                  data-action="reporting-pick"
                  data-code=${t.code}
                  aria-current=${t.code === model.selectedCode ? "true" : "false"}
                >
                  ${t.name}
                </button>
              `,
            )}
          `,
        )}
      </nav>
      <section class="report-main">${model.template === null ? nothing : renderTemplateSheet(model, model.template)}</section>
    </div>
  `;
}

function renderTemplateSheet(model: ReportingModel, tmpl: TemplateView): TemplateResult {
  const missing = missingRequired(tmpl.params, model.values);
  return html`
    <header class="report-head">
      <h2>${tmpl.name}</h2>
      <p class="page-sub">${tmpl.description}</p>
      ${tmpl.legacyName !== "" ? html`<p class="legacy-name">Replaces the legacy report &ldquo;${tmpl.legacyName}&rdquo;</p>` : nothing}
    </header>
    <div class="report-form">
      ${tmpl.params.map((p) => renderParam(p, model))}
      ${printAtNeeded(model.values) ? renderPrintAt(model) : nothing}
      <div class="row-actions">
        <button type="button" class="btn btn-primary" data-action="reporting-run" ?disabled=${model.busy}>Run</button>
        <button type="button" class="btn btn-secondary" data-action="reporting-export" ?disabled=${model.busy}>Export CSV</button>
        <button type="button" class="btn btn-secondary" data-action="reporting-print" ?disabled=${model.busy}>Print</button>
      </div>
      ${missing.length > 0 ? html`<p class="notice-line" data-test="reporting-missing">Fill in: ${missing.join(", ")}</p>` : nothing}
    </div>
    ${model.sheetError !== "" ? html`<p class="error-line" data-test="reporting-error">${model.sheetError}</p>` : nothing}
    ${model.sheetNotice !== "" ? html`<p class="notice-line" data-test="reporting-notice">${model.sheetNotice}</p>` : nothing}
    ${renderSavedViews(model)}
    ${model.result === null
      ? html`<p class="notice-line">Choose parameters and run.</p>`
      : renderResultGrid(model.result)}
  `;
}

function renderParam(p: { name: string; kind: string; required: boolean; label: string; values: readonly string[] }, model: ReportingModel): TemplateResult {
  if (p.kind === "date") {
    return html`
      <label class="field">
        <span class="field-label">${p.label}</span>
        <input type="date" class="text-field" data-param=${p.name} aria-label=${p.label} />
      </label>
    `;
  }
  if (p.kind === "enum") {
    return html`
      <label class="field">
        <span class="field-label">${p.label}</span>
        <select class="text-field" data-param=${p.name} aria-label=${p.label}>
          <option>All</option>
          ${p.values.map((v) => html`<option>${v}</option>`)}
        </select>
      </label>
    `;
  }
  // A branch is a uuid a person does not type: the select shows the names of
  // the branches this caller reaches, and app-root resolves the pick back to
  // the id. Every other uuid stays a text field this wave, named as a gap.
  if (p.name === "branch_id") {
    return html`
      <label class="field">
        <span class="field-label">${p.label}</span>
        <select class="text-field" data-param=${p.name} aria-label=${p.label}>
          <option>All branches</option>
          ${model.branchNames.map((n) => html`<option>${n}</option>`)}
        </select>
      </label>
    `;
  }
  // The placeholder is static in both branches: every interpolation in an
  // attribute outside the gate's allow list is refused, whatever it carries.
  if (p.kind === "int") {
    return html`
      <label class="field">
        <span class="field-label">${p.label}${p.required ? "" : " (optional)"}</span>
        <input type="text" class="text-field" data-param=${p.name} aria-label=${p.label} placeholder="0" autocomplete="off" />
      </label>
    `;
  }
  return html`
    <label class="field">
      <span class="field-label">${p.label}${p.required ? "" : " (optional)"}</span>
      <input type="text" class="text-field" data-param=${p.name} aria-label=${p.label} placeholder="id" autocomplete="off" />
    </label>
  `;
}

function renderPrintAt(model: ReportingModel): TemplateResult {
  return html`
    <label class="field">
      <span class="field-label">Print at</span>
      <select class="text-field" data-field="print-at" aria-label="Print at">
        <option>Not picked</option>
        ${model.branchNames.map((n) => html`<option>${n}</option>`)}
      </select>
    </label>
  `;
}

function renderSavedViews(model: ReportingModel): TemplateResult {
  if (model.selectedCode === "") return html``;
  return html`
    <div class="saved-views">
      <p class="nav-cat">Saved views for this report</p>
      ${model.savedPhase === "loading"
        ? html`<p class="notice-line">Loading saved views…</p>`
        : model.savedPhase === "error"
          ? html`<p class="error-line">${model.savedError}</p>`
          : model.savedViews.length === 0
            ? html`<p class="notice-line">No saved views yet: set the parameters above and save them under a name.</p>`
            : html`
                <ul class="saved-list">
                  ${model.savedViews.map(
                    (v) => html`
                      <li class="saved-row" data-saved-view=${v.id}>
                        <span class="saved-name">${v.name}${v.isShared ? " · shared" : ""}</span>
                        <span class="row-actions">
                          <button type="button" class="btn btn-secondary" data-action="reporting-apply-view" data-view-id=${v.id} ?disabled=${model.busy}>Apply</button>
                          <button type="button" class="btn btn-secondary" data-action="reporting-delete-view" data-view-id=${v.id} ?disabled=${model.viewBusyId !== ""}>Delete</button>
                        </span>
                      </li>
                    `,
                  )}
                </ul>
              `}
      ${model.saveError !== "" ? html`<p class="error-line" data-test="reporting-error">${model.saveError}</p>` : nothing}
      <div class="saved-save">
        <label class="field">
          <span class="field-label">Save these parameters as</span>
          <input type="text" class="text-field" data-field="save-name" aria-label="Saved view name" placeholder="Such as This month, Point Loma" autocomplete="off" />
        </label>
        <label class="field share-field">
          <span class="field-label">Shared</span>
          <input type="checkbox" data-field="save-shared" aria-label="Share this saved view" />
        </label>
        <button type="button" class="btn btn-secondary" data-action="reporting-save-view" ?disabled=${model.saveBusy}>Save view</button>
      </div>
    </div>
  `;
}

function renderResultGrid(res: ReportResultView): TemplateResult {
  if (res.rows.length === 0) {
    return html`<section class="sheet"><div class="list-card"><p class="notice-line" data-test="reporting-empty">No rows for these parameters.</p></div></section>`;
  }
  const hasTotals = res.columns.some((c) => c.total);
  return html`
    <section class="sheet">
      <div class="grid-wrap" data-test="reporting-result">
        <table>
          <thead>
            <tr>
              ${res.columns.map((c) => html`<th class="cell${numericCellClass(c.kind)}">${c.label}</th>`)}
            </tr>
          </thead>
          <tbody>
            ${res.rows.map(
              (row) => html`
                <tr>
                  ${res.columns.map((c, i) => html`<td class="cell${numericCellClass(c.kind)}">${formatCell(c.kind, row[i])}</td>`)}
                </tr>
              `,
            )}
          </tbody>
          ${hasTotals
            ? html`
                <tfoot>
                  <tr>
                    ${res.columns.map(
                      (c, i) => html`<td class="cell${numericCellClass(c.kind)}">${i === 0 ? "Total" : totalCell(c, res.totals)}</td>`,
                    )}
                  </tr>
                </tfoot>
              `
            : nothing}
        </table>
      </div>
      <p class="count-line">${resultSummary(res)}</p>
      <p class="count-line">Export saves ${csvName(res.code, res.params)}.</p>
    </section>
  `;
}

function numericCellClass(kind: string): string {
  return isNumericKind(kind) ? " num" : "";
}

// -- Runs pane --------------------------------------------------------------------------------------

function runWord(run: RunView): string {
  if (run.kind === "COMMISSION") return "Commission statement";
  return "Report";
}

function renderRunsPane(model: ReportingModel): TemplateResult {
  if (model.runsPhase === "loading") {
    return html`<p class="notice-line" data-test="reporting-loading">Loading the frozen runs…</p>`;
  }
  if (model.runsPhase === "error") {
    return html`
      <p class="error-line" data-test="reporting-error">${model.runsError}</p>
      <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reporting-reload" data-what="runs">Try again</button></div>
    `;
  }
  if (model.runs.length === 0) {
    return html`<section class="sheet"><div class="list-card"><p class="notice-line" data-test="reporting-empty">Nothing has been printed or frozen yet: run a report and print it, and the run lands here.</p></div></section>`;
  }
  return html`
    <section class="sheet">
      <div class="list-card">
        ${model.runs.map((run) => renderRunRow(model, run))}
      </div>
    </section>
  `;
}

function renderRunRow(model: ReportingModel, run: RunView): TemplateResult {
  const open = model.expandedDocId === run.id;
  return html`
    <div class="run-row" data-run=${run.id}>
      <button type="button" class="run-expand" data-action="reporting-expand-run" data-run-id=${run.id} aria-expanded=${open ? "true" : "false"}>
        <span class="run-title">${runWord(run)}: ${run.name !== "" ? run.name : run.templateCode}</span>
        <span class="meta">${grouped(run.rowCount)} rows · ${dateWords(run.createdAt)}</span>
      </button>
      ${open
        ? model.expandedDocPhase === "loading"
          ? html`<p class="notice-line">Loading the run…</p>`
          : model.expandedDocPhase === "error"
            ? html`<p class="error-line" data-test="reporting-error">${model.expandedDocError}</p>`
            : renderRunDetail(model, model.expandedDoc)
        : nothing}
    </div>
  `;
}

function renderRunDetail(model: ReportingModel, run: RunView | null): TemplateResult {
  if (run === null) return html``;
  const paramEntries = Object.entries(run.params);
  return html`
    <div class="run-detail">
      ${paramEntries.length === 0
        ? nothing
        : html`<p class="detail">${paramEntries.map(([k, v]) => `${k}: ${v}`).join(" · ")}</p>`}
      <div class="row-actions">
        <button type="button" class="btn btn-secondary" data-action="reporting-open-doc" data-run-id=${run.id} ?disabled=${model.expandedDocId !== "" && model.expandedDocPhase === "loading"}>Open the document</button>
      </div>
      ${model.docNotice !== "" ? html`<p class="notice-line" data-test="reporting-doc-notice">${model.docNotice}</p>` : nothing}
      <p class="notice-line">The document opens at the paper: the frozen copy the server stored, not a rerun.</p>
    </div>
  `;
}

// -- Commission rules pane -----------------------------------------------------------------------------

function renderRulesPane(model: ReportingModel): TemplateResult {
  if (model.rulesPhase === "loading") {
    return html`<p class="notice-line" data-test="reporting-loading">Loading the commission rules…</p>`;
  }
  if (model.rulesPhase === "error") {
    return html`
      ${model.rulesRefusal !== "" ? html`<p class="refusal" data-test="reporting-refusal">${model.rulesRefusal}</p>` : nothing}
      ${model.rulesRefusal === "" ? html`<p class="error-line" data-test="reporting-error">${model.rulesError}</p>` : nothing}
      <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reporting-reload" data-what="rules">Try again</button></div>
    `;
  }
  return html`
    ${model.rulesRefusal !== "" ? html`<p class="refusal" data-test="reporting-refusal">${model.rulesRefusal}</p>` : nothing}
    ${model.rules.length === 0 && model.rulesRefusal === ""
      ? html`<section class="sheet"><div class="list-card"><p class="notice-line" data-test="reporting-empty">No commission rules stand yet: commission preview will report every sale as having no rule.</p></div></section>`
      : html`
          <section class="sheet">
            <div class="grid-wrap" data-test="reporting-rules">
              <table>
                <thead>
                  <tr>
                    <th>Rep</th>
                    <th>Rate</th>
                    <th>Applies to</th>
                    <th>Effective</th>
                    <th>State</th>
                    ${model.canWriteRules ? html`<th></th>` : nothing}
                  </tr>
                </thead>
                <tbody>
                  ${model.rules.map((r) => renderRuleRow(model, r))}
                </tbody>
              </table>
            </div>
          </section>
        `}
    ${model.canWriteRules ? renderRuleForm(model) : nothing}
  `;
}

function renderRuleRow(model: ReportingModel, r: RuleView): TemplateResult {
  return html`
    <tr data-rule=${r.id}>
      <td>${r.repName !== "" ? r.repName : r.repUserId}</td>
      <td class="num">${bps(r.rateBps)}</td>
      <td>${basisLabel(r.basis)}${r.branchId !== "" ? " · at one branch" : ""}</td>
      <td>${dateWords(r.effectiveFrom)}${r.effectiveTo !== "" ? html` to ${dateWords(r.effectiveTo)}` : nothing}</td>
      <td>${r.isActive ? "Standing" : "Retired"}</td>
      ${model.canWriteRules
        ? html`<td class="num">
            ${r.isActive
              ? html`<button type="button" class="btn btn-secondary" data-action="reporting-rule-retire" data-rule-id=${r.id} ?disabled=${model.retireBusyId !== ""}>Retire</button>`
              : nothing}
          </td>`
        : nothing}
    </tr>
  `;
}

function renderRuleForm(model: ReportingModel): TemplateResult {
  const d = model.ruleDraft;
  return html`
    <section class="list-card rule-form" data-test="reporting-rule-form">
      <p class="nav-cat">Stand a new rule</p>
      <div class="report-form">
        <label class="field">
          <span class="field-label">Rep user id</span>
          <input type="text" class="text-field" data-field="rule-rep" aria-label="Rep user id" placeholder="The rep's user id, as a report line shows it" autocomplete="off" />
        </label>
        <label class="field">
          <span class="field-label">Rate (percent)</span>
          <input type="text" inputmode="decimal" class="text-field" data-field="rule-rate" aria-label="Rate in percent" placeholder="Such as 2.5" autocomplete="off" />
        </label>
        <label class="field">
          <span class="field-label">Applies to</span>
          <select class="text-field" data-field="rule-basis" aria-label="What the rate applies to">
            ${BASIS_CHOICES.map((b) => html`<option>${b.word}</option>`)}
          </select>
        </label>
        <label class="field">
          <span class="field-label">Effective from</span>
          <input type="date" class="text-field" data-field="rule-from" aria-label="Effective from" />
        </label>
        <label class="field">
          <span class="field-label">Effective to (optional)</span>
          <input type="date" class="text-field" data-field="rule-to" aria-label="Effective to" />
        </label>
        <label class="field">
          <span class="field-label">Branch</span>
          <select class="text-field" data-field="rule-branch" aria-label="Branch the rule covers">
            <option>Every branch</option>
            ${model.branchNames.map((n) => html`<option>${n}</option>`)}
          </select>
        </label>
        <label class="field">
          <span class="field-label">Note (optional)</span>
          <input type="text" class="text-field" data-field="rule-note" aria-label="Note" autocomplete="off" />
        </label>
        <div class="row-actions">
          <button type="button" class="btn btn-primary" data-action="reporting-rule-create" ?disabled=${d.busy}>Stand the rule</button>
        </div>
        ${d.error !== "" ? html`<p class="error-line" data-test="reporting-error">${d.error}</p>` : nothing}
      </div>
    </section>
  `;
}

// -- Accruals pane (the commission register) ---------------------------------------------------------------

function renderAccrualsPane(model: ReportingModel): TemplateResult {
  const reg = model.register;
  return html`
    <section class="list-card">
      <div class="report-form">
        <label class="field">
          <span class="field-label">Period from</span>
          <input type="date" class="text-field" data-field="commission-from" aria-label="Period from" />
        </label>
        <label class="field">
          <span class="field-label">to</span>
          <input type="date" class="text-field" data-field="commission-to" aria-label="Period to" />
        </label>
        <label class="field">
          <span class="field-label">Branch</span>
          <select class="text-field" data-field="commission-branch" aria-label="Branch">
            <option>All branches</option>
            ${model.branchNames.map((n) => html`<option>${n}</option>`)}
          </select>
        </label>
        <div class="row-actions">
          <button type="button" class="btn btn-primary" data-action="reporting-preview" ?disabled=${reg.phase === "loading"}>Preview period</button>
        </div>
      </div>
      ${reg.error !== "" ? html`<p class="error-line" data-test="reporting-error">${reg.error}</p>` : nothing}
      ${reg.notice !== "" ? html`<p class="notice-line" data-test="reporting-notice">${reg.notice}</p>` : nothing}
      ${reg.ran ? renderRegisterBody(model, reg) : html`<p class="notice-line">Choose a period and preview.</p>`}
    </section>
    ${reg.runsItems.length > 0 || reg.runsPhase !== "ready" ? renderStoredRuns(reg) : renderStoredRunsEmpty()}
    ${reg.accrual !== null ? renderAccrualRegister(reg.accrual) : reg.accrualError !== "" ? html`<p class="error-line" data-test="reporting-error">${reg.accrualError}</p>` : nothing}
    ${reg.attribution !== null ? renderAttribution(reg.attribution) : reg.attributionError !== "" ? html`<p class="error-line" data-test="reporting-error">${reg.attributionError}</p>` : nothing}
  `;
}

function renderRegisterBody(model: ReportingModel, reg: CommissionRegister): TemplateResult {
  if (reg.phase === "loading") {
    return html`<p class="notice-line" data-test="reporting-loading">Computing the preview…</p>`;
  }
  const warning = unattributedWords(reg.periods);
  return html`
    ${reg.note !== "" ? html`<p class="who-may" data-test="reporting-accrual-note">${reg.note}</p>` : nothing}
    ${warning !== "" ? html`<p class="warn-line" data-test="reporting-unattributed">${warning}</p>` : nothing}
    ${reg.periods.length === 0
      ? html`<p class="notice-line" data-test="reporting-empty">No reps with sales or rules in this period.</p>`
      : html`
          <div class="grid-wrap" data-test="reporting-periods">
            <table>
              <thead>
                <tr>
                  <th>Rep</th>
                  <th class="num">Sales</th>
                  <th class="num">Revenue</th>
                  <th class="num">Margin</th>
                  <th class="num">Commission</th>
                  <th>State</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                ${reg.periods.map((p) => renderPeriodRow(model, reg, p))}
              </tbody>
            </table>
          </div>
        `}
  `;
}

function renderPeriodRow(model: ReportingModel, reg: CommissionRegister, p: PeriodView): TemplateResult {
  const open = reg.expandedRepId === p.repUserId;
  const stored = p.runId !== "";
  return html`
    <tr data-period=${p.repUserId}>
      <td>
        <button type="button" class="run-expand" data-action="reporting-expand-period" data-rep-id=${p.repUserId} aria-expanded=${open ? "true" : "false"}>
          ${p.repName !== "" ? p.repName : p.repUserId}
        </button>
      </td>
      <td class="num">${grouped(p.saleCount)}</td>
      <td class="num">${moneyCell(p.revenueCents)}</td>
      <td class="num">${moneyCell(p.marginCents)}</td>
      <td class="num">${moneyCell(p.commissionCents)}</td>
      <td>${stored ? "Accrued (stored)" : "Preview"}</td>
      <td class="num">
        <span class="row-actions">
          ${stored
            ? html`
                <button type="button" class="btn btn-secondary" data-action="reporting-crun-csv" data-run-id=${p.runId} ?disabled=${reg.runBusyId !== ""}>CSV</button>
                <button type="button" class="btn btn-secondary" data-action="reporting-crun-print" data-run-id=${p.runId} ?disabled=${reg.runBusyId !== ""}>Print</button>
              `
            : html`<button type="button" class="btn btn-secondary" data-action="reporting-compute" data-rep-id=${p.repUserId} ?disabled=${reg.computeBusyRep !== ""}>Compute</button>`}
        </span>
      </td>
    </tr>
    ${open
      ? html`
          <tr class="lines-row">
            <td colspan="7">
              ${reg.expandedPhase === "loading"
                ? html`<p class="notice-line">Loading the sales…</p>`
                : reg.expandedPhase === "error"
                  ? html`<p class="error-line" data-test="reporting-error">${reg.expandedError}</p>`
                  : renderLines(reg.expandedPeriod)}
            </td>
          </tr>
        `
      : nothing}
  `;
}

function renderLines(period: PeriodView | null): TemplateResult {
  if (period === null) return html``;
  const lines = period.lines;
  if (lines.length === 0) {
    return html`<p class="notice-line" data-test="reporting-empty">No attributed sales in this period.</p>`;
  }
  return html`
    <div class="grid-wrap">
      <table class="lines-table">
        <thead>
          <tr>
            <th>Invoice</th>
            <th>Date</th>
            <th>Customer</th>
            <th>SKU</th>
            <th class="num">Qty</th>
            <th class="num">Revenue</th>
            <th class="num">Cost</th>
            <th class="num">Margin</th>
            <th>Basis</th>
            <th class="num">Rate</th>
            <th class="num">Commission</th>
          </tr>
        </thead>
        <tbody>
          ${lines.map((l) => {
            const unpaid = lineIsUnpaid(l);
            return html`
              <tr class="line${unpaid ? " unpaid" : ""}" data-line=${l.invoiceNo}>
                <td>${l.invoiceNo}</td>
                <td>${dateWords(l.invoiceDate)}</td>
                <td>${l.customerName}</td>
                <td>${l.productSku}</td>
                <td class="num">${l.qtyBase === 0 ? "0" : qty(l.qtyBase)}</td>
                <td class="num">${moneyCell(l.revenueCents)}</td>
                <td class="num">${moneyCell(l.costCents)}</td>
                <td class="num">${moneyCell(l.marginCents)}</td>
                <td>${basisLabel(l.basis)}</td>
                <td class="num">${bps(l.rateBps)}</td>
                <td class="num">${moneyCell(l.commissionCents)}</td>
              </tr>
              ${unpaid && l.noRuleReason !== "" ? html`<tr class="unpaid"><td colspan="11">${l.noRuleReason}</td></tr>` : nothing}
            `;
          })}
        </tbody>
      </table>
    </div>
  `;
}

function renderStoredRuns(reg: CommissionRegister): TemplateResult {
  return html`
    <section class="list-card">
      <p class="nav-cat">Stored commission runs in this window</p>
      ${reg.runsPhase === "loading"
        ? html`<p class="notice-line">Loading the stored runs…</p>`
        : reg.runsPhase === "error"
          ? html`<p class="error-line" data-test="reporting-error">${reg.runsError}</p>`
          : reg.runsItems.length === 0
            ? html`<p class="notice-line" data-test="reporting-empty">No stored runs in this window: compute a period to freeze one.</p>`
            : html`
                <div class="grid-wrap" data-test="reporting-cruns">
                  <table>
                    <thead>
                      <tr>
                        <th>Rep</th>
                        <th>Period</th>
                        <th class="num">Sales</th>
                        <th class="num">Commission</th>
                        <th></th>
                      </tr>
                    </thead>
                    <tbody>
                      ${reg.runsItems.map((p) => renderStoredRunRow(reg, p))}
                    </tbody>
                  </table>
                </div>
              `}
    </section>
  `;
}

function renderStoredRunsEmpty(): TemplateResult {
  return html`
    <section class="list-card">
      <p class="nav-cat">Stored commission runs in this window</p>
      <p class="notice-line" data-test="reporting-empty">No stored runs in this window: compute a period to freeze one.</p>
    </section>
  `;
}

function renderStoredRunRow(reg: CommissionRegister, p: PeriodView): TemplateResult {
  const open = reg.expandedRunId === p.runId;
  return html`
    <tr data-crun=${p.runId}>
      <td>${p.repName !== "" ? p.repName : p.repUserId}</td>
      <td>${p.periodStart} to ${p.periodEnd}</td>
      <td class="num">${grouped(p.saleCount)}</td>
      <td class="num">${moneyCell(p.commissionCents)}</td>
      <td class="num">
        <span class="row-actions">
          <button type="button" class="btn btn-secondary" data-action="reporting-expand-crun" data-run-id=${p.runId} aria-expanded=${open ? "true" : "false"}>Lines</button>
          <button type="button" class="btn btn-secondary" data-action="reporting-crun-csv" data-run-id=${p.runId} ?disabled=${reg.runBusyId !== ""}>CSV</button>
          <button type="button" class="btn btn-secondary" data-action="reporting-crun-print" data-run-id=${p.runId} ?disabled=${reg.runBusyId !== ""}>Print</button>
        </span>
      </td>
    </tr>
    ${open
      ? html`
          <tr class="lines-row">
            <td colspan="5">
              ${reg.expandedRunPhase === "loading"
                ? html`<p class="notice-line">Loading the lines…</p>`
                : reg.expandedRunPhase === "error"
                  ? html`<p class="error-line" data-test="reporting-error">${reg.expandedRunError}</p>`
                  : renderLines(reg.expandedRun)}
            </td>
          </tr>
        `
      : nothing}
  `;
}

function renderAccrualRegister(acc: AccrualView): TemplateResult {
  return html`
    <section class="list-card" data-test="reporting-accruals">
      <p class="nav-cat">Accrual register</p>
      ${acc.lines.length === 0
        ? html`<p class="notice-line" data-test="reporting-empty">No computed periods in this window.</p>`
        : html`
            <div class="grid-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Rep</th>
                    <th class="num">Periods</th>
                    <th class="num">Accrued</th>
                  </tr>
                </thead>
                <tbody>
                  ${acc.lines.map(
                    (l) => html`
                      <tr data-accrual=${l.repUserId}>
                        <td>${l.repName !== "" ? l.repName : l.repUserId}</td>
                        <td class="num">${grouped(l.periodCount)}</td>
                        <td class="num">${moneyCell(l.commissionCents)}</td>
                      </tr>
                    `,
                  )}
                </tbody>
                <tfoot>
                  <tr>
                    <td>Total</td>
                    <td class="num"></td>
                    <td class="num">${moneyCell(acc.totalCents)}</td>
                  </tr>
                </tfoot>
              </table>
            </div>
          `}
      <p class="who-may">${acc.note}</p>
    </section>
  `;
}

function renderAttribution(check: AttributionView): TemplateResult {
  return html`
    <section class="list-card" data-test="reporting-attribution">
      <p class="nav-cat">Rep attribution check</p>
      <p class="detail">
        ${check.bound ? "Every invoice line is attributed." : `${grouped(check.mismatches.length)} of ${grouped(check.checked)} invoice lines disagree between the report and the sales line.`}
      </p>
      ${check.explanation !== "" ? html`<p class="who-may">${check.explanation}</p>` : nothing}
      ${check.mismatches.length === 0
        ? nothing
        : html`
            <div class="grid-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Invoice</th>
                    <th>Where the lines disagree</th>
                  </tr>
                </thead>
                <tbody>
                  ${check.mismatches.map(
                    (m) => html`
                      <tr data-mismatch=${m.invoiceNo}>
                        <td>${m.invoiceNo}</td>
                        <td>at the ${m.seamLevel}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function moneyCell(cents: number): string {
  // The one money rendering in this pane set: the core's integer cents,
  // formatted for a person. Computed nowhere, summed nowhere.
  return formatCell("cents", cents);
}
