// The Audit surface's screens: the trail's list with its filters, the one
// entry's masked detail drawer, and one entity's whole trail. Pure template
// functions over plain model objects; app-root owns every call and every
// state change.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings; unquoted whole values in the data-
// and aria- namespaces; ?disabled is the one boolean. Controls ride
// data-action names and the host's listeners act on them; field values are
// mirrored from input events, never bound.
import { html, nothing, type TemplateResult } from "lit";
import type { AuditRow, StaffMemberRow } from "./dealer-api.js";
import {
  ACTION_OPTIONS,
  actorWords,
  auditEmptyWords,
  entityWords,
  filtersActive,
  isSensitive,
  trailEmptyWords,
  whenWords,
  type AuditFilters,
} from "./audit-logic.js";

export interface AuditDetail {
  /** The id of the row the person clicked, never rewritten by a response. */
  readonly forId: string;
  /** What the drawer shows, in one word: the word anything reading it keys on. */
  readonly state: "loading" | "ready" | "error";
  /** The row on the glass: the list's projection first, Get's answer when it lands. */
  readonly entry: AuditRow | null;
}

export interface AuditTrail {
  readonly entityType: string;
  readonly entityId: string;
  readonly phase: "loading" | "ready" | "error";
  readonly rows: readonly AuditRow[];
  readonly error: string;
}

export interface AuditModel {
  readonly phase: "loading" | "ready" | "error" | "refused";
  readonly rows: readonly AuditRow[];
  readonly filters: AuditFilters;
  readonly error: string;
  readonly detail: AuditDetail | null;
  readonly trail: AuditTrail | null;
}

export function renderAudit(model: AuditModel, staff: readonly StaffMemberRow[] = []): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Audit</h1>
      <p class="page-sub">Who changed what, when, where and why.</p>
    </header>
    ${model.phase === "loading" ? html`<p class="notice-line" data-test="audit-loading">Loading the trail…</p>` : nothing}
    ${model.phase === "error" || model.phase === "refused"
      ? html`
          <p class="error-line" data-test="audit-error">${model.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-audit">Try again</button></div>
        `
      : nothing}
    ${model.phase === "error" ? html`<section class="card">${renderFilters(model.filters, staff)}</section>` : nothing}
    ${model.phase === "ready" ? renderTrail(model, staff) : nothing}
  `;
}

function renderTrail(model: AuditModel, staff: readonly StaffMemberRow[]): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">The trail</h2>
        <span class="count" data-test="audit-count">${model.rows.length} entries</span>
      </div>
      ${renderFilters(model.filters, staff)}
      ${model.rows.length === 0
        ? html`<p class="notice-line" data-test="audit-empty">${auditEmptyWords(filtersActive(model.filters))}</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">When</th>
                    <th scope="col">Action</th>
                    <th scope="col">Entity</th>
                    <th scope="col">Actor</th>
                    <th scope="col">Request</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.rows.map((row) => renderAuditRowView(row, model))}
                </tbody>
              </table>
            </div>
          `}
    </section>
    ${model.detail === null ? nothing : renderDetail(model)}
    ${model.trail === null ? nothing : renderTrailCard(model.trail)}
  `;
}

// -- the filters ------------------------------------------------------------------------------------

function renderFilters(_filters: AuditFilters, staff: readonly StaffMemberRow[]): TemplateResult {
  return html`
    <div class="filters" role="group" aria-label="Trail filters">
      <input
        class="text-field"
        placeholder="Entity type (e.g. pos.sale)"
        aria-label="Entity type"
        data-field="audit-entity-type"
      />
      <select class="select" aria-label="Action" data-field="audit-action">
        <option value="">Action: all</option>
        ${ACTION_OPTIONS.map((a) => html`<option>${a}</option>`)}
      </select>
      <select class="select" aria-label="Staff member" data-field="audit-actor-id">
        <option value="">Any staff member</option>
        ${staff.map((s) => html`<option data-option-id=${s.user_id}>${s.display_name || s.email}</option>`)}
      </select>
      <input class="text-field" type="datetime-local" aria-label="Since" data-field="audit-since" />
      <button type="button" class="btn btn-primary" data-action="apply-audit-filters">Apply</button>
      <button type="button" class="btn btn-secondary" data-action="clear-audit-filters">Clear</button>
    </div>
  `;
}

// -- the list's rows -----------------------------------------------------------------------------------

function renderAuditRowView(row: AuditRow, model: AuditModel): TemplateResult {
  const open = model.detail !== null && model.detail.forId === row.id;
  return html`
    <tr
      tabindex="0"
      data-action="open-audit-entry"
      data-entry-id=${row.id}
      aria-current=${open ? "true" : "false"}
      data-test=${`audit-row-${row.id}`}
    >
      <td class="mono dim">${whenWords(row.created_at)}</td>
      <td><span class="chip ${isSensitive(row.action) ? "chip-sensitive" : ""}">${row.action}</span></td>
      <td class="mono">${entityWords(row)}</td>
      <td>${actorWords(row)}</td>
      <td class="mono dim">${row.request_id === "" ? "-" : row.request_id}</td>
    </tr>
  `;
}

// -- the one entry's drawer ------------------------------------------------------------------------------

function renderDetail(model: AuditModel): TemplateResult | typeof nothing {
  const detail = model.detail;
  if (detail === null || detail.entry === null) return nothing;
  const e = detail.entry;
  const stale = detail.state === "ready" && detail.forId !== e.id;
  return html`
    <section
      class="card detail"
      role="group"
      aria-label="Details of the selected entry"
      data-detail-state=${stale ? "error" : detail.state}
      data-detail-for=${detail.forId}
    >
      <div class="card-head">
        <h2 class="card-title">Audit entry</h2>
        <button type="button" class="btn btn-secondary btn-small" data-action="close-audit-detail">Close</button>
      </div>
      ${detail.state === "error" || stale
        ? html`<p class="error-line" data-test="audit-detail-error">
            The full entry could not be loaded. What follows is the summary from the list: the masked change set and the
            actor's id may be incomplete. Close and reopen to try again.
          </p>`
        : nothing}
      <div class="kv">
        <span class="k">ID</span><span class="v mono">${e.id}</span>
        <span class="k">Action</span><span class="v"><span class="chip ${isSensitive(e.action) ? "chip-sensitive" : ""}">${e.action}</span></span>
        <span class="k">Entity</span><span class="v mono">${entityWords(e) === "" ? "-" : entityWords(e)}</span>
        <span class="k">Actor</span><span class="v">${actorWords(e)}</span>
        <span class="k">Branch</span><span class="v mono">${e.branch_id === "" ? "-" : e.branch_id}</span>
        <span class="k">Request</span><span class="v mono">${e.request_id === "" ? "-" : e.request_id}</span>
        <span class="k">When</span><span class="v">${whenWords(e.created_at)}</span>
      </div>
      <pre class="changes" data-test="audit-changes">${JSON.stringify(e.changes ?? {}, null, 2)}</pre>
      ${e.entity_type !== "" && e.entity_id !== ""
        ? html`<div class="row-actions">
            <button
              type="button"
              class="btn btn-secondary"
              data-action="open-audit-trail"
              data-entity-type=${e.entity_type}
              data-entity-id=${e.entity_id}
            >
              Open this entity's whole trail
            </button>
          </div>`
        : nothing}
    </section>
  `;
}

// -- one entity's whole trail -----------------------------------------------------------------------------

function renderTrailCard(trail: AuditTrail): TemplateResult {
  return html`
    <section class="card" data-test="audit-trail">
      <div class="card-head">
        <h2 class="card-title">The trail of one entity</h2>
        <span class="mono dim">${trail.entityType} / ${trail.entityId}</span>
      </div>
      ${trail.phase === "loading" ? html`<p class="notice-line">Loading the entity's trail…</p>` : nothing}
      ${trail.phase === "error" ? html`<p class="error-line">${trail.error}</p>` : nothing}
      ${trail.phase === "ready" && trail.rows.length === 0
        ? html`<p class="notice-line" data-test="audit-trail-empty">${trailEmptyWords()}</p>`
        : nothing}
      ${trail.phase === "ready" && trail.rows.length > 0
        ? html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">When</th>
                    <th scope="col">Action</th>
                    <th scope="col">Actor</th>
                  </tr>
                </thead>
                <tbody>
                  ${trail.rows.map((row) => renderTrailRow(row))}
                </tbody>
              </table>
            </div>
          `
        : nothing}
    </section>
  `;
}

function renderTrailRow(row: AuditRow): TemplateResult {
  return html`
    <tr tabindex="0" data-action="open-audit-entry" data-entry-id=${row.id} data-test=${`audit-trail-row-${row.id}`}>
      <td class="mono dim">${whenWords(row.created_at)}</td>
      <td><span class="chip ${isSensitive(row.action) ? "chip-sensitive" : ""}">${row.action}</span></td>
      <td>${actorWords(row)}</td>
    </tr>
  `;
}
