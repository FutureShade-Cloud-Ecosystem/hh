// The Print jobs surface's screens: one document's print history, asked for
// by its paper and its reference. Pure template functions over plain model
// objects; AdminWork owns every call and every state change.
//
// There is no original screen for this surface (the original app never called
// the print queue's list), so the visual reference is this app's own
// established screens: the register's table and the audit trail's filter form
// on HH's tokens, with the gold-means-act rule on the one Find button.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings, bound values only on data- and aria-
// attributes. The select and the reference field take the same road as the
// audit filters: mirrored in code, answered on the host's input listener.
import { html, nothing, type TemplateResult } from "lit";
import type { PrintJobRow } from "./admin-api.js";
import {
  PRINT_DOC_TYPES,
  printDocLabel,
  printEmptyWords,
  printStatusWords,
  printTargetWords,
} from "./admin-logic.js";

export interface PrintModel {
  /** idle: nothing asked yet. loading: the surface's first paint. */
  readonly phase: "loading" | "idle" | "ready";
  /** A search in flight. */
  readonly busy: boolean;
  /** A refused or failed search's words, the server's own. */
  readonly error: string;
  /** The chosen paper, as the select answers it: the option's own words. */
  readonly docType: string;
  readonly ref: string;
  readonly rows: readonly PrintJobRow[];
}

export function renderPrint(model: PrintModel): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Print jobs</h1>
      <p class="page-sub">One document's print history: how many times it was printed, and by whom.</p>
    </header>
    ${model.error === "" ? nothing : html`<p class="error-line" data-test="print-error">${model.error}</p>`}
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Find a document's history</h2>
      </div>
      <div class="filters">
        <select class="select" data-field="print-doc-type" aria-label="The paper to look up">
          ${PRINT_DOC_TYPES.map((t) => html`<option>${t.label}</option>`)}
        </select>
        <input
          class="text-field"
          type="text"
          data-field="print-ref"
          placeholder="The document's reference"
          aria-label="The document's reference"
        />
        <button type="button" class="btn btn-primary" data-action="print-find" ?disabled=${model.busy}>
          ${model.busy ? "Looking…" : "Find"}
        </button>
      </div>
      ${model.phase === "loading"
        ? html`<p class="notice-line" data-test="print-loading">Loading…</p>`
        : nothing}
      ${model.phase === "idle"
        ? html`<p class="notice-line" data-test="print-idle">
            Choose the paper and the document's reference to see its print history.
          </p>`
        : nothing}
      ${model.phase === "ready" && model.rows.length === 0 && model.error === ""
        ? html`<p class="notice-line" data-test="print-empty">${printEmptyWords()}</p>`
        : nothing}
      ${model.phase === "ready" && model.rows.length > 0 ? renderJobs(model) : nothing}
    </section>
  `;
}

function renderJobs(model: PrintModel): TemplateResult {
  return html`
    <div class="card-head">
      <h2 class="card-title">${printDocLabel(model.docType)}</h2>
      <span class="count" data-test="print-count">${model.rows.length} jobs</span>
    </div>
    <div class="tablewrap">
      <table>
        <thead>
          <tr>
            <th scope="col">Status</th>
            <th scope="col">Rendered for</th>
            <th scope="col">Requested by</th>
            <th scope="col">Requested</th>
            <th scope="col">Printed</th>
            <th scope="col">Failure</th>
          </tr>
        </thead>
        <tbody>
          ${model.rows.map((row, i) => renderJobRow(row, i))}
        </tbody>
      </table>
    </div>
  `;
}

function renderJobRow(row: PrintJobRow, index: number): TemplateResult {
  return html`
    <tr data-test="print-row-${index}">
      <td><span class="chip ${row.status === "FAILED" ? "chip-sensitive" : ""}">${printStatusWords(row.status)}</span></td>
      <td><span class="chip">${printTargetWords(row.render_target)}</span></td>
      <td class="mono">${row.requested_by === "" ? "not recorded" : row.requested_by.slice(0, 8)}</td>
      <td class="mono dim">${row.requested_at}</td>
      <td class="mono dim">${row.printed_at === "" ? "not yet" : row.printed_at}</td>
      <td class="dim">${row.failure_reason}</td>
    </tr>
  `;
}
