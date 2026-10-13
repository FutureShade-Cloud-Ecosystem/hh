// The Exports surface's screens: the audit workbook and the house messages
// export. Each is one card with its own filters, one server call, and one
// file in the person's downloads; there is no queue and no second call.
//
// There is no original screen for this surface (the original app never called
// the export routes), so the visual reference is this app's own established
// screens: the audit trail's filter form and table on HH's tokens, with the
// gold-means-act rule on each card's one Download button.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings, bound values only on data- and aria-
// attributes. Every filter field is mirrored in code, the way the audit
// filters are.
import { html, nothing, type TemplateResult } from "lit";
import type { MessagesExportRow } from "./admin-api.js";
import {
  messageKindWords,
  messagesEmptyWords,
  messagesMaskWords,
} from "./admin-logic.js";

export interface WorkbookModel {
  readonly busy: boolean;
  readonly error: string;
  /** The downloaded words: the file's name and its size, after the save. */
  readonly notice: string;
  /** The filters' words, mirrored onto the fields. */
  readonly entityType: string;
  readonly action: string;
  readonly actorId: string;
  readonly since: string;
}

export interface MessagesModel {
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
  /** Whether an export has run: the empty sentence is an answer, not a greeting. */
  readonly asked: boolean;
  readonly channelId: string;
  readonly userId: string;
  readonly since: string;
  readonly until: string;
  readonly rows: readonly MessagesExportRow[];
}

export function renderExports(workbook: WorkbookModel, messages: MessagesModel): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Exports</h1>
      <p class="page-sub">Two files, each one call: the audit trail as a workbook, and the house's messages, masked.</p>
    </header>
    ${renderWorkbook(workbook)}
    ${renderMessages(messages)}
  `;
}

// -- the audit workbook ---------------------------------------------------------------------------

function renderWorkbook(model: WorkbookModel): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">The audit workbook</h2>
      </div>
      <p class="card-sub">The trail as a spreadsheet, the same filters the Audit surface reads. An empty set of filters takes the whole trail.</p>
      ${model.error === "" ? nothing : html`<p class="error-line" data-test="wb-error">${model.error}</p>`}
      ${model.notice === "" ? nothing : html`<p class="notice-line" data-test="wb-notice">${model.notice}</p>`}
      <div class="filters">
        <input class="text-field" type="text" placeholder="Entity type" aria-label="Entity type" data-field="wb-entity-type" />
        <input class="text-field" type="text" placeholder="Action" aria-label="Action" data-field="wb-action" />
        <input class="text-field" type="text" placeholder="Actor id" aria-label="Actor id" data-field="wb-actor" />
        <input class="text-field" type="datetime-local" aria-label="Since" data-field="wb-since" />
        <button type="button" class="btn btn-primary" data-action="wb-download" ?disabled=${model.busy}>
          ${model.busy ? "Packing…" : "Download the workbook"}
        </button>
      </div>
    </section>
  `;
}

// -- the messages export ---------------------------------------------------------------------------

const PREVIEW_ROWS = 50;

function renderMessages(model: MessagesModel): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">The house's messages</h2>
        ${model.rows.length === 0 ? nothing : html`<span class="count" data-test="msg-count">${model.rows.length} messages</span>`}
      </div>
      <p class="card-sub">${messagesMaskWords()}</p>
      ${model.error === "" ? nothing : html`<p class="error-line" data-test="msg-error">${model.error}</p>`}
      ${model.notice === "" ? nothing : html`<p class="notice-line" data-test="msg-notice">${model.notice}</p>`}
      <div class="filters">
        <input class="text-field" type="text" placeholder="Channel id" aria-label="Channel id" data-field="msg-channel" />
        <input class="text-field" type="text" placeholder="Person id" aria-label="Person id" data-field="msg-user" />
        <input class="text-field" type="datetime-local" aria-label="Since" data-field="msg-since" />
        <input class="text-field" type="datetime-local" aria-label="Until" data-field="msg-until" />
        <button type="button" class="btn btn-primary" data-action="msg-export" ?disabled=${model.busy}>
          ${model.busy ? "Packing…" : "Download the export"}
        </button>
      </div>
      ${model.rows.length === 0
        ? html`<p class="notice-line" data-test="msg-empty">
            ${model.asked ? messagesEmptyWords() : "Take the whole house, or name a channel, a person or a window first."}
          </p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Kind</th>
                    <th scope="col">Author</th>
                    <th scope="col">When</th>
                    <th scope="col">Message</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.rows.slice(0, PREVIEW_ROWS).map((row, i) => renderMessageRow(row, i))}
                </tbody>
              </table>
            </div>
            ${model.rows.length > PREVIEW_ROWS
              ? html`<p class="card-sub">The first ${PREVIEW_ROWS} rows are here; the file carries all ${model.rows.length}.</p>`
              : nothing}
          `}
    </section>
  `;
}

function renderMessageRow(row: MessagesExportRow, index: number): TemplateResult {
  return html`
    <tr data-test="msg-row-${index}">
      <td><span class="chip">${messageKindWords(row.kind)}</span></td>
      <td>${row.author_name === "" ? html`<span class="dim">not recorded</span>` : row.author_name}</td>
      <td class="mono dim">${row.created_at}</td>
      <td class="${row.deleted_at !== "" ? "dim" : ""}">
        ${row.deleted_at !== "" ? html`<span class="chip chip-wait">deleted</span>` : nothing}
        ${row.body}
      </td>
    </tr>
  `;
}
