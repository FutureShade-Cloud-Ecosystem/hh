// The counts area's screens, as plain render functions of a model, on the same
// rules as the inventory area's: no state, no events, every control a
// data-action name, every field read on press. References: the original app's
// count-sheets, count-entry and count-variance screens (repository
// hardscapeos_dibbits, commit 87d7941b, read only), drawn to HH's design
// system at 1440 and 390.
import { html, nothing, type TemplateResult } from "lit";
import type { InvCountLine, InvCountReasonCode, InvCountReconRun, InvCountSheet, InvCountSheetDetail, InvCountNegativeStockRow } from "@hardscapehouse/api-client";
import { confirmCancelWords, confirmPostLines, confirmPostWords, entryState, reconStatusTone, reconStatusWord, reconcilePeriodWord, sheetStatusTone, sheetStatusWord, sheetTypeWord, signTone } from "./counts-logic.js";
import { formatMoney, formatQty } from "../po/po-money.js";
import { whenWords } from "../po/po-logic.js";

export type CountsTab = "sheets" | "recon";

export const COUNTS_TABS: readonly { readonly id: CountsTab; readonly label: string }[] = [
  { id: "sheets", label: "Count sheets" },
  { id: "recon", label: "Reconciliation" },
];

export interface CountsModel {
  readonly phase: "loading" | "ready" | "error";
  readonly tab: CountsTab;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
  readonly branchNames: ReadonlyMap<string, string>;
  /** Today, YYYY-MM-DD in the reader's own day: the stamps on screen read against it. */
  readonly today: string;
  // The sheets list.
  readonly sheets: readonly InvCountSheet[];
  // The open sheet, with its lines, its summary and the reason picker's rows.
  readonly detail: InvCountSheetDetail | null;
  readonly reasons: readonly InvCountReasonCode[];
  readonly blind: boolean;
  readonly counts: ReadonlyMap<string, string>;
  readonly lineReasons: ReadonlyMap<string, string>;
  // The post confirm.
  readonly confirmingPost: boolean;
  readonly confirmingCancel: boolean;
  // The recon.
  readonly reconBranch: string;
  readonly recon: { readonly current: InvCountReconRun; readonly history: readonly InvCountReconRun[] } | null;
  readonly negatives: readonly InvCountNegativeStockRow[];
  readonly reconBusy: boolean;
}

function notice(model: CountsModel): TemplateResult {
  if (model.notice === "") return html`${nothing}`;
  return html`<div class="alert alert-ok" role="status" data-notice><strong>${model.notice.split("\n")[0]}</strong></div>`;
}

function errorNote(model: CountsModel): TemplateResult {
  return model.error !== "" ? html`<p class="alert alert-error" role="alert" data-error>${model.error}</p>` : html`${nothing}`;
}

function statusPill(status: string): TemplateResult {
  return html`<span class="status status-${sheetStatusTone(status)}" data-status>${sheetStatusWord(status)}</span>`;
}

function loading(words: string): TemplateResult {
  return html`<p class="dim" role="status">${words}</p>`;
}

export function renderCountsLoading(): TemplateResult {
  return html`<p class="dim" role="status">Loading the count sheets.</p>`;
}

export function renderCountsLoadFailed(error: string): TemplateResult {
  return html`
    <p class="alert alert-error" role="alert" data-error>${error}</p>
    <button type="button" class="btn btn-secondary" data-action="counts-reload">Try again</button>
  `;
}

function countsHead(): TemplateResult {
  return html`
    <div class="head">
      <div>
        <h1>Cycle counts</h1>
        <p class="dim">Freeze what the yard should hold, count what it does, and post the difference with a reason. The checker is never the maker.</p>
      </div>
    </div>
  `;
}

function countsTabs(model: CountsModel): TemplateResult {
  return html`
    <nav class="tabs" aria-label="Cycle counts views">
      ${COUNTS_TABS.map(
        (t) => html`<button type="button" class="tab" data-action="counts-tab" data-counts-tab=${t.id} aria-current="${model.tab === t.id ? "true" : "false"}">${t.label}</button>`,
      )}
    </nav>
  `;
}

// ---- the sheets list ----------------------------------------------------------------------------------

function sheetsPane(model: CountsModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Count sheets" data-sheets>
      <div class="panel-head">
        <h3>Count sheets</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="sheets-reload">Refresh</button>
      </div>
      ${model.sheets.length === 0
        ? html`<div class="empty"><strong>No count sheets yet.</strong><span class="dim">Start one to freeze what the yard should hold, then count against it.</span></div>`
        : html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Sheet</th>
                    <th>Branch</th>
                    <th>Type</th>
                    <th>Status</th>
                    <th>Frozen</th>
                    <th><span class="visually-hidden">Open</span></th>
                  </tr>
                </thead>
                <tbody>
                  ${model.sheets.map(
                    (s) => html`
                      <tr data-sheet-row=${s.sheet_no}>
                        <td class="code" data-label="Sheet">${s.sheet_no}</td>
                        <td data-label="Branch">${model.branchNames.get(s.branch_id) ?? s.branch_id}</td>
                        <td data-label="Type">${sheetTypeWord(s.scope)}</td>
                        <td data-label="Status">${statusPill(s.status)}</td>
                        <td data-label="Frozen">${s.freeze_at === undefined ? html`<span class="dim">not yet</span>` : whenWords(s.freeze_at, model.today)}</td>
                        <td class="pick" data-label="Open">
                          <button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-id=${s.id} ?disabled=${model.busy}>Open</button>
                        </td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
      ${errorNote(model)}
      <h3 class="sub">Start a sheet</h3>
      <div class="forms">
        <label>Branch
          <select data-field="sheet-branch">
            ${[...model.branchNames.entries()].map(([id, name]) => html`<option data-value=${id}>${name}</option>`)}
          </select>
        </label>
        <label>Type
          <select data-field="sheet-scope">
            <option value="FULL">Full count</option>
            <option value="AD_HOC">Ad-hoc</option>
          </select>
        </label>
        <label>Notes
          <input type="text" data-field="sheet-notes" placeholder="What this count is for" />
        </label>
        <button type="button" class="btn btn-primary" data-action="new-sheet" ?disabled=${model.busy}>New sheet</button>
      </div>
    </section>
  `;
}

// ---- the sheet detail ----------------------------------------------------------------------------------

function lineRow(model: CountsModel, line: InvCountLine): TemplateResult {
  const editable = entryState(model.detail?.sheet ?? { status: "OPEN" }) === "entry";
  const typed = model.counts.get(line.id) ?? "";
  const variance = typed === "" ? null : Number(typed) - line.snapshot_qty_base;
  const needsReason = variance !== null && variance !== 0;
  const reason = model.lineReasons.get(line.id) ?? line.reason_code ?? "";
  return html`
    <tr data-count-line>
      <td data-label="Product"><span class="sku">${line.sku ?? ""}</span> ${line.product_name ?? line.product_id}</td>
      ${model.blind || !editable ? nothing : html`<td class="num" data-label="Book on hand"><span data-book-qty>${formatQty(line.snapshot_qty_base, "")}</span></td>`}
      <td class="num" data-label="Counted">
        ${editable
          ? html`<input type="number" min="0" step="any" inputmode="decimal" data-field="counted" data-key=${line.id} />`
          : html`<span>${line.counted_qty_base === null ? html`<span class="dim">not counted</span>` : formatQty(line.counted_qty_base, "")}</span>`}
      </td>
      <td class="num ${variance === null || variance === 0 ? "" : variance > 0 ? "good" : "stop"}" data-label="Variance">
        ${variance === null ? html`<span class="dim">.</span>` : formatQty(variance, "")}
      </td>
      <td data-label="Reason">
        ${editable && needsReason
          ? html`<select data-field="reason" data-key=${line.id}>
              <option value="">Pick a reason</option>
              ${model.reasons.map((r) => html`<option>${r.code}</option>`)}
            </select>`
          : html`<span class="code">${line.reason_code ?? ""}</span>`}
      </td>
    </tr>
  `;
}

function entryPane(model: CountsModel): TemplateResult {
  const detail = model.detail;
  if (detail === null) return html`${nothing}`;
  const state = entryState(detail.sheet);
  return html`
    <section class="panel" aria-label="Count entry" data-count-entry>
      <div class="panel-head">
        <h3>Count entry</h3>
      ${state === "freeze"
        ? html`<span class="dim">Freeze the snapshot on-hand basis to begin counting.</span>`
        : html`<label class="check">${model.blind ? html`<input type="checkbox" data-action="toggle-blind" checked />` : html`<input type="checkbox" data-action="toggle-blind" />`} Blind count (hide book on-hand)</label>`}
      </div>
      ${state === "freeze"
        ? html`<div class="actions">
            <button type="button" class="btn btn-primary" data-action="freeze-sheet" ?disabled=${model.busy}>Freeze snapshot</button>
          </div>`
        : nothing}
      ${state !== "freeze" && detail.lines.length === 0
        ? html`<div class="empty"><strong>The freeze found nothing to count.</strong><span class="dim">The branch holds no stock rows, so the sheet has no lines.</span></div>`
        : nothing}
      ${state !== "freeze" && detail.lines.length > 0
        ? html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Product</th>
                    ${model.blind || state === "read" ? nothing : html`<th class="num">Book on hand</th>`}
                    <th class="num">Counted</th>
                    <th class="num">Variance</th>
                    <th>Reason</th>
                  </tr>
                </thead>
                <tbody>
                  ${detail.lines.map((line) => lineRow(model, line))}
                </tbody>
              </table>
            </div>
            ${state === "entry"
              ? html`
                  <div class="actions">
                    <button type="button" class="btn btn-primary" data-action="submit-counts" ?disabled=${model.busy}>Submit counts</button>
                    <span class="dim">You are the counter (the maker). A different manager approves before anything posts.</span>
                  </div>
                `
              : nothing}
          `
        : nothing}
      ${errorNote(model)}
    </section>
  `;
}

function variancePane(model: CountsModel): TemplateResult {
  const detail = model.detail;
  if (detail === null) return html`${nothing}`;
  if (detail.sheet.status === "OPEN" || detail.sheet.status === "FROZEN") return html`${nothing}`;
  const summary = detail.summary;
  const branchName = model.branchNames.get(detail.sheet.branch_id) ?? detail.sheet.branch_id;
  const sheetFacts = { sheet_no: detail.sheet.sheet_no, branch_name: branchName };
  const confirmLines = detail.lines.map((l) => ({
    product_label: `${l.sku ?? ""} ${l.product_name ?? l.product_id}`.trim(),
    snapshot_qty_base: l.snapshot_qty_base,
    counted_qty_base: l.counted_qty_base,
    variance_base: l.variance_base,
    variance_value_cents: l.variance_value_cents,
  }));
  const confirmWords = confirmPostWords(sheetFacts, summary, confirmLines);
  return html`
    <section class="panel" aria-label="Variance and post" data-count-variance>
      <div class="panel-head">
        <h3>Variance and post</h3>
      </div>
      <div class="chips" data-variance-summary>
        <span class="chip"><span class="dim">Variance lines</span><span class="figure">${summary.variance_lines}</span></span>
        <span class="chip"><span class="dim">Net</span><span class="figure ${signTone(summary.net_variance_value_cents) === "plain" ? "" : signTone(summary.net_variance_value_cents)}">${formatMoney(summary.net_variance_value_cents)}</span></span>
        <span class="chip"><span class="dim">Gross</span><span class="figure">${formatMoney(summary.abs_variance_value_cents)}</span></span>
      </div>
      ${detail.lines.filter((l) => l.counted_qty_base !== null).length === 0
        ? html`<p class="dim">No counted lines yet.</p>`
        : html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Product</th>
                    <th class="num">Book</th>
                    <th class="num">Counted</th>
                    <th class="num">Variance</th>
                    <th class="num">Value</th>
                    <th>Reason</th>
                  </tr>
                </thead>
                <tbody>
                  ${detail.lines
                    .filter((l) => l.counted_qty_base !== null)
                    .map(
                      (l) => html`
                        <tr data-variance-line>
                          <td data-label="Product"><span class="sku">${l.sku ?? ""}</span> ${l.product_name ?? l.product_id}</td>
                          <td class="num" data-label="Book">${formatQty(l.snapshot_qty_base, "")}</td>
                          <td class="num" data-label="Counted">${formatQty(l.counted_qty_base, "")}</td>
                          <td class="num ${signTone(l.variance_base) === "plain" ? "" : signTone(l.variance_base)}" data-label="Variance">${formatQty(l.variance_base, "")}</td>
                          <td class="num ${signTone(l.variance_value_cents) === "plain" ? "" : signTone(l.variance_value_cents)}" data-label="Value">${formatMoney(l.variance_value_cents)}</td>
                          <td class="code" data-label="Reason">${l.reason_code ?? ""}</td>
                        </tr>
                      `,
                    )}
                </tbody>
              </table>
            </div>
          `}
      ${detail.sheet.status === "COUNTED"
        ? html`
            <div class="actions">
              <button type="button" class="btn btn-secondary" data-action="approve-sheet" ?disabled=${model.busy}>Approve (checker)</button>
              <button type="button" class="btn btn-primary" data-action="post-sheet" ?disabled=${model.busy}>Post adjustment</button>
              <button type="button" class="btn btn-secondary" data-action="cancel-sheet" ?disabled=${model.busy}>Cancel sheet</button>
            </div>
            <p class="foot dim">Approving and posting are manager seats' moves. The maker never approves their own count, and posting writes the stock moves and the reason-coded journal.</p>
          `
        : nothing}
      ${notice(model)}
    </section>
    ${model.confirmingPost
      ? html`
          <div class="scrim">
            <div class="modal" role="dialog" aria-label="Confirm the post" data-post-confirm>
              <h3>Post the count adjustment</h3>
              <p>${confirmWords}</p>
              <ul data-post-lines>
                ${confirmPostLines(branchName, confirmLines).map((row) => html`<li>${row}</li>`)}
              </ul>
              <div class="actions end">
                <button type="button" class="btn btn-secondary" data-action="post-cancel">Back</button>
                <button type="button" class="btn btn-primary" data-action="post-confirm" ?disabled=${model.busy}>Confirm post</button>
              </div>
            </div>
          </div>
        `
      : nothing}
    ${model.confirmingCancel
      ? html`
          <div class="scrim">
            <div class="modal" role="dialog" aria-label="Confirm the cancel" data-cancel-confirm>
              <h3>Cancel the count sheet</h3>
              <p>${confirmCancelWords(sheetFacts)}</p>
              <div class="actions end">
                <button type="button" class="btn btn-secondary" data-action="cancel-back">Back</button>
                <button type="button" class="btn btn-primary" data-action="cancel-confirm" ?disabled=${model.busy}>Confirm cancel</button>
              </div>
            </div>
          </div>
        `
      : nothing}
  `;
}

function detailPane(model: CountsModel): TemplateResult {
  const detail = model.detail;
  if (detail === null) return html`${nothing}`;
  return html`
    <section class="panel" data-sheet-detail>
      <div class="panel-head">
        <div class="forms">
          <button type="button" class="btn btn-secondary small-btn" data-action="close-sheet">All sheets</button>
          <span class="who">${detail.sheet.sheet_no}</span>
          <span class="dim">${model.branchNames.get(detail.sheet.branch_id) ?? detail.sheet.branch_id}</span>
          <span class="status status-${sheetStatusTone(detail.sheet.status)}" data-sheet-status>${sheetStatusWord(detail.sheet.status)}</span>
        </div>
      </div>
    </section>
    ${entryPane(model)}
    ${variancePane(model)}
  `;
}

// ---- the recon ---------------------------------------------------------------------------------------------

function reconPane(model: CountsModel): TemplateResult {
  const run = model.recon?.current;
  return html`
    <section class="panel" aria-label="Inventory to GL reconciliation" data-recon>
      <div class="panel-head">
        <h3>Inventory to GL</h3>
        <div class="forms">
          <label>Branch
            <select data-field="recon-branch">
              ${[...model.branchNames.entries()].map(([id, name]) => html`<option data-value=${id}>${name}</option>`)}
            </select>
          </label>
          <button type="button" class="btn btn-secondary small-btn" data-action="recon-run" ?disabled=${model.busy || model.reconBusy}>Run the tie-out</button>
        </div>
      </div>
      ${model.reconBusy || (run === undefined && model.reconBranch !== "") ? loading("Running the tie-out.") : nothing}
      ${run !== undefined
        ? html`
            <div class="facts" data-recon-current>
              <dt>Period</dt><dd>${reconcilePeriodWord(run.fiscal_year, run.month)}</dd>
              <dt>Verdict</dt><dd><span class="status status-${reconStatusTone(run.status)}" data-status>${reconStatusWord(run.status)}</span></dd>
              <dt>Subledger</dt><dd class="num">${formatMoney(run.subledger_cents)}</dd>
              <dt>GL 1200</dt><dd class="num">${formatMoney(run.gl_1200_cents)}</dd>
              <dt>Delta</dt><dd class="num ${signTone(run.delta_cents) === "plain" ? "" : signTone(run.delta_cents)}"><strong>${formatMoney(run.delta_cents)}</strong></dd>
            </div>
            <p class="foot dim">The run is made once per period and branch, on the first look; the next look reads the same run.</p>
          `
        : html`<p class="dim">Pick a branch and run the tie-out: the stock the yards hold, valued, against the GL's inventory account.</p>`}
      ${model.recon !== null && model.recon.history.length > 1
        ? html`
            <h3 class="sub">History</h3>
            <div class="scroll" data-recon-history>
              <table class="stack">
                <thead>
                  <tr>
                    <th>Period</th>
                    <th class="num">Subledger</th>
                    <th class="num">GL 1200</th>
                    <th class="num">Delta</th>
                    <th>Verdict</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.recon.history.map(
                    (r) => html`
                      <tr data-recon-run>
                        <td data-label="Period">${reconcilePeriodWord(r.fiscal_year, r.month)}</td>
                        <td class="num" data-label="Subledger">${formatMoney(r.subledger_cents)}</td>
                        <td class="num" data-label="GL 1200">${formatMoney(r.gl_1200_cents)}</td>
                        <td class="num ${signTone(r.delta_cents) === "plain" ? "" : signTone(r.delta_cents)}" data-label="Delta">${formatMoney(r.delta_cents)}</td>
                        <td data-label="Verdict"><span class="status status-${reconStatusTone(r.status)}" data-status>${reconStatusWord(r.status)}</span></td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `
        : nothing}
      ${errorNote(model)}
    </section>
    <section class="panel" aria-label="Negative stock" data-negative>
      <div class="panel-head">
        <h3>Negative stock</h3>
        <span class="dim">${model.negatives.length === 0 ? "every product received before sale" : `${model.negatives.length} ${model.negatives.length === 1 ? "row" : "rows"} below zero`}</span>
      </div>
      ${model.negatives.length === 0
        ? html`<div class="empty"><strong>No negative on hand.</strong><span class="dim">A count is what clears a sold-before-receipt line.</span></div>`
        : html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Product</th>
                    <th>Location</th>
                    <th class="num">On hand</th>
                    <th class="num">Average cost</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.negatives.map(
                    (n) => html`
                      <tr data-negative-row>
                        <td class="sku" data-label="Product">${n.product_id}</td>
                        <td class="code" data-label="Location">${n.location_id}</td>
                        <td class="num stop" data-label="On hand">${formatQty(n.on_hand_base, "")}</td>
                        <td class="num" data-label="Average cost">${formatMoney(n.avg_cost_cents)}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
            <p class="foot dim">These rows are sold before their receipt. A count that brings them back to what the yard holds is what clears them.</p>
          `}
    </section>
  `;
}

// ---- the area -------------------------------------------------------------------------------------------------

export function renderCounts(model: CountsModel): TemplateResult {
  if (model.phase === "loading") return renderCountsLoading();
  if (model.phase === "error") return renderCountsLoadFailed(model.error);
  return html`
    ${countsHead()}
    ${countsTabs(model)}
    ${model.tab === "recon" ? reconPane(model) : model.detail !== null ? detailPane(model) : sheetsPane(model)}
  `;
}
