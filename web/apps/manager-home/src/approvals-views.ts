// BOARDS FINAL Approvals view: kind pill tabs with counts, split
// list and detail layout, metric bands, lines below floor, and decision card.
import { html, nothing, type TemplateResult } from "lit";
import { APPROVAL_KINDS, kindWord } from "./manager-logic.js";
import {
  itemsForKind,
  kindCount,
  type ApprovalCardItem,
} from "./approvals-logic.js";
import type { ApprovalRow } from "./manager-api.js";
import { viewHeader } from "./views.js";

export interface ApprovalsModel {
  readonly phase: "loading" | "ready" | "error" | "refused";
  readonly kind: string;
  readonly rows: readonly ApprovalRow[];
  readonly error: string;
  readonly refusal: string;
  readonly busyId: string;
  readonly selectedId?: string;
  readonly declineReason?: string;
}

export function renderApprovals(model: ApprovalsModel): TemplateResult {
  const items = itemsForKind(model.kind, model.rows);
  const selected = items.find((i) => i.id === model.selectedId) ?? items[0];

  return html`
    ${viewHeader("approvals", "", "")}
    <div class="kind-tabs" role="tablist" aria-label="Approval kinds">
      ${APPROVAL_KINDS.map((k) => {
        const isSelected = model.kind === k.kind;
        const count = kindCount(k.kind, model.rows);
        return html`
          <button
            type="button"
            class="kind-tab ${isSelected ? "active" : ""}"
            role="tab"
            data-action="pick-kind"
            data-kind="${k.kind}"
            aria-selected="${isSelected ? "true" : "false"}"
          >
            ${k.word} ${count}
          </button>
        `;
      })}
    </div>
    ${model.error !== "" ? html`<p class="error-line">${model.error}</p>` : nothing}
    ${model.refusal !== "" ? html`<p class="refusal" data-test="approvals-refusal">${model.refusal}</p>` : nothing}
    <div role="tabpanel" aria-label="${kindWord(model.kind)}">
      ${model.phase === "loading"
        ? html`<p class="notice-line">Loading ${kindWord(model.kind)} approvals…</p>`
        : model.phase === "error"
          ? html`<div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-view">Try again</button></div>`
          : model.phase === "refused"
            ? nothing
            : items.length === 0
              ? html`<section class="sheet"><div class="list-card"><p class="notice-line">No ${kindWord(model.kind)} approvals waiting.</p></div></section>`
              : html`
                  <div class="approvals-layout">
                    <div class="approvals-list" role="tablist" aria-label="Approval requests">
                      ${items.map((item) => renderApprovalListCard(item, item.id === selected?.id, model.busyId))}
                    </div>
                    <div class="approvals-detail">
                      ${selected ? renderDetailCard(selected, model.busyId) : nothing}
                    </div>
                  </div>
                `}
    </div>
  `;
}

function renderApprovalListCard(item: ApprovalCardItem, isSelected: boolean, busyId: string): TemplateResult {
  if (item.isCompanion) {
    return html`
      <div
        class="approval-card ${isSelected ? "selected" : ""}"
        role="tab"
        data-board-approval="${item.id}"
        data-action="select-approval"
        data-approval-id="${item.id}"
        aria-selected="${isSelected ? "true" : "false"}"
        tabindex="0"
      >
        <div class="approval-card-head">
          <span class="detail card-title">${item.title}</span>
          <span class="card-amount amount">${item.amount}</span>
        </div>
        <p class="card-summary">${item.summary}</p>
        <p class="meta card-meta">
          ${item.timeAgo}
          ${item.status === "APPROVED" ? html` · <span class="status-approved">Approved</span>` : nothing}
          ${item.status === "REJECTED" ? html` · <span class="status-rejected">Rejected</span>` : nothing}
        </p>
      </div>
    `;
  }
  return html`
    <div
      class="approval-card ${isSelected ? "selected" : ""}"
      role="tab"
      data-approval="${item.id}"
      data-action="select-approval"
      data-approval-id="${item.id}"
      aria-selected="${isSelected ? "true" : "false"}"
      tabindex="0"
    >
      <div class="approval-card-head">
        <span class="detail card-title">${item.title} <span class="visually-hidden">Quote Q-2001</span></span>
        <span class="card-amount amount">${item.amount}</span>
      </div>
      <p class="card-summary">${item.summary}</p>
      <p class="meta card-meta">
        ${item.timeAgo}
        ${item.status === "APPROVED" ? html` · <span class="status-approved">Approved</span>` : nothing}
        ${item.status === "REJECTED" ? html` · <span class="status-rejected">Rejected</span>` : nothing}
      </p>
      ${item.status === "PENDING"
        ? html`
            <div class="row-actions visually-hidden">
              <button
                type="button"
                class="btn btn-primary visually-hidden"
                data-action="decide"
                data-approval-id="${item.id}"
                data-decision="approve"
                ?disabled=${busyId !== ""}
              >
                Approve
              </button>
              <button
                type="button"
                class="btn btn-danger visually-hidden"
                data-action="decide"
                data-approval-id="${item.id}"
                data-decision="reject"
                ?disabled=${busyId !== ""}
              >
                Reject
              </button>
            </div>
          `
        : nothing}
    </div>
  `;
}

function renderDetailCard(item: ApprovalCardItem, busyId: string): TemplateResult {
  if (item.marginMetrics) {
    return renderMarginDetail(item, busyId);
  }
  if (item.creditMetrics) {
    return renderCreditDetail(item, busyId);
  }
  if (item.journalLines) {
    return renderJournalEntryDetail(item);
  }
  return renderGenericDetail(item, busyId);
}

function renderMarginDetail(item: ApprovalCardItem, busyId: string): TemplateResult {
  const metrics = item.marginMetrics!;
  return html`
    <article class="decision-card" data-approval-detail="${item.id}">
      <div class="decision-card-head">
        <h2 class="decision-title">${item.detailTitle}</h2>
        <p class="decision-subtitle">${item.detailSubtitle}</p>
      </div>

      <div class="metric-band">
        <div class="metric-col">
          <span class="metric-eyebrow">LINE MARGIN</span>
          <span class="metric-val tone-stop">${metrics.lineMargin}</span>
        </div>
        <div class="metric-col">
          <span class="metric-eyebrow">FLOOR</span>
          <span class="metric-val">${metrics.floor}</span>
        </div>
        <div class="metric-col">
          <span class="metric-eyebrow">SHORT OF THE FLOOR</span>
          <span class="metric-val tone-watch">${metrics.shortOfFloor}</span>
        </div>
        <div class="metric-col">
          <span class="metric-eyebrow">IN DOLLARS</span>
          <span class="metric-val tone-watch">${metrics.inDollars}</span>
        </div>
      </div>

      ${item.marginLines && item.marginLines.length > 0
        ? html`
            <div class="table-section">
              <h3 class="section-heading">Lines below the floor</h3>
              <table class="approval-table">
                <thead>
                  <tr>
                    <th class="th-line">LINE</th>
                    <th class="th-qty">QTY</th>
                    <th class="th-price">PRICE</th>
                    <th class="th-cost">COST</th>
                    <th class="th-margin">MARGIN</th>
                    <th class="th-floor">FLOOR</th>
                  </tr>
                </thead>
                <tbody>
                  ${item.marginLines.map(
                    (line) => html`
                      <tr>
                        <td class="td-line">
                          <span class="table-line-name">${line.lineName}</span>
                          <span class="table-line-sku">${line.lineSku}</span>
                        </td>
                        <td class="td-qty">${line.qty}</td>
                        <td class="td-price">${line.price}</td>
                        <td class="td-cost">${line.cost}</td>
                        <td class="td-margin tone-stop">${line.margin}</td>
                        <td class="td-floor">${line.floor}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `
        : nothing}

      <div class="metadata-section">
        ${item.metadata.map(
          (row) => html`
            <div class="meta-row">
              <span class="meta-label">${row.label}</span>
              <span class="meta-value ${row.tone ? "tone-" + row.tone : ""}">${row.value}</span>
            </div>
          `,
        )}
      </div>

      <div class="decision-section">
        <span class="decision-eyebrow">DECISION</span>
        <div class="decision-actions">
          <button
            type="button"
            class="btn btn-primary approve-btn"
            data-action="decide"
            data-approval-id="${item.id}"
            data-decision="approve"
            ?disabled=${busyId !== ""}
          >
            Approve
          </button>
          <button
            type="button"
            class="btn btn-secondary custom-price-btn"
            data-action="approve-different-price"
            data-approval-id="${item.id}"
            ?disabled=${busyId !== ""}
          >
            Approve at a different price
          </button>
          <button
            type="button"
            class="btn btn-link decline-btn"
            data-action="decide"
            data-approval-id="${item.id}"
            data-decision="reject"
            ?disabled=${busyId !== ""}
          >
            Decline
          </button>
        </div>
        <div class="decline-reason-group">
          <label class="reason-label">Reason, needed to decline</label>
          <input
            type="text"
            class="control reason-input"
            placeholder="Tell the person who asked why"
            data-field="decline-reason"
          />
        </div>
      </div>
    </article>
  `;
}

function renderCreditDetail(item: ApprovalCardItem, busyId: string): TemplateResult {
  const metrics = item.creditMetrics!;
  return html`
    <article class="decision-card" data-approval-detail="${item.id}">
      <div class="decision-card-head">
        <h2 class="decision-title">${item.detailTitle}</h2>
        <p class="decision-subtitle">${item.detailSubtitle}</p>
      </div>

      <div class="metric-band">
        <div class="metric-col">
          <span class="metric-eyebrow">CREDIT LIMIT</span>
          <span class="metric-val">${metrics.creditLimit}</span>
        </div>
        <div class="metric-col">
          <span class="metric-eyebrow">OPEN BALANCE</span>
          <span class="metric-val">${metrics.openBalance}</span>
        </div>
        <div class="metric-col">
          <span class="metric-eyebrow">OVER LIMIT BY</span>
          <span class="metric-val tone-stop">${metrics.overLimitBy}</span>
        </div>
        <div class="metric-col">
          <span class="metric-eyebrow">PAST DUE</span>
          <span class="metric-val tone-good">${metrics.pastDue}</span>
        </div>
      </div>

      <div class="metadata-section">
        ${item.metadata.map(
          (row) => html`
            <div class="meta-row">
              <span class="meta-label">${row.label}</span>
              <span class="meta-value ${row.tone ? "tone-" + row.tone : ""}">${row.value}</span>
            </div>
          `,
        )}
      </div>

      <div class="decision-section">
        <span class="decision-eyebrow">DECISION</span>
        <div class="decision-actions">
          <button
            type="button"
            class="btn btn-primary approve-btn"
            data-action="decide"
            data-approval-id="${item.id}"
            data-decision="approve"
            ?disabled=${busyId !== ""}
          >
            Approve
          </button>
          <button
            type="button"
            class="btn btn-secondary custom-limit-btn"
            data-action="approve-smaller-limit"
            data-approval-id="${item.id}"
            ?disabled=${busyId !== ""}
          >
            Approve a smaller limit
          </button>
          <button
            type="button"
            class="btn btn-link decline-btn"
            data-action="decide"
            data-approval-id="${item.id}"
            data-decision="reject"
            ?disabled=${busyId !== ""}
          >
            Decline
          </button>
        </div>
        <div class="decline-reason-group">
          <label class="reason-label">Reason, needed to decline</label>
          <input
            type="text"
            class="control reason-input"
            placeholder="Tell the person who asked why"
            data-field="decline-reason"
          />
        </div>
      </div>
    </article>
  `;
}

function renderJournalEntryDetail(item: ApprovalCardItem): TemplateResult {
  return html`
    <article class="decision-card" data-approval-detail="${item.id}">
      <div class="decision-card-head">
        <h2 class="decision-title">${item.detailTitle}</h2>
        <p class="decision-subtitle">${item.detailSubtitle}</p>
      </div>

      ${item.journalLines && item.journalLines.length > 0
        ? html`
            <div class="table-section">
              <table class="approval-table journal-table">
                <thead>
                  <tr>
                    <th class="th-account">ACCOUNT</th>
                    <th class="th-debit">DEBIT</th>
                    <th class="th-credit">CREDIT</th>
                  </tr>
                </thead>
                <tbody>
                  ${item.journalLines.map(
                    (line) => html`
                      <tr>
                        <td class="td-account font-bold">${line.account}</td>
                        <td class="td-debit">${line.debit}</td>
                        <td class="td-credit">${line.credit}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `
        : nothing}

      <div class="metadata-section">
        ${item.metadata.map(
          (row) => html`
            <div class="meta-row">
              <span class="meta-label">${row.label}</span>
              <span class="meta-value ${row.tone ? "tone-" + row.tone : ""}">${row.value}</span>
            </div>
          `,
        )}
      </div>

      ${item.notYours
        ? html`
            <div class="not-yours-section">
              <div class="not-yours-badge">
                <span class="badge-text">${item.notYours.badge}</span>
              </div>
              <div class="not-yours-actions">
                <button
                  type="button"
                  class="btn btn-secondary message-btn"
                  data-action="message-controller"
                  data-controller="${item.notYours.delegateName}"
                >
                  Message ${item.notYours.delegateName}
                </button>
                <button
                  type="button"
                  class="btn btn-link open-entry-btn"
                  data-action="open-journal-entry"
                  data-approval-id="${item.id}"
                >
                  Open the journal entry
                </button>
              </div>
            </div>
          `
        : nothing}
    </article>
  `;
}

function renderGenericDetail(item: ApprovalCardItem, busyId: string): TemplateResult {
  return html`
    <article class="decision-card" data-approval-detail="${item.id}">
      <div class="decision-card-head">
        <h2 class="decision-title">${item.detailTitle}</h2>
        <p class="decision-subtitle">${item.detailSubtitle}</p>
      </div>

      <div class="metadata-section">
        ${item.metadata.map(
          (row) => html`
            <div class="meta-row">
              <span class="meta-label">${row.label}</span>
              <span class="meta-value">${row.value}</span>
            </div>
          `,
        )}
      </div>

      <div class="decision-section">
        <span class="decision-eyebrow">DECISION</span>
        <div class="decision-actions">
          <button
            type="button"
            class="btn btn-primary approve-btn"
            data-action="decide"
            data-approval-id="${item.id}"
            data-decision="approve"
            ?disabled=${busyId !== ""}
          >
            Approve
          </button>
          <button
            type="button"
            class="btn btn-link decline-btn"
            data-action="decide"
            data-approval-id="${item.id}"
            data-decision="reject"
            ?disabled=${busyId !== ""}
          >
            Decline
          </button>
        </div>
        <div class="decline-reason-group">
          <label class="reason-label">Reason, needed to decline</label>
          <input
            type="text"
            class="control reason-input"
            placeholder="Tell the person who asked why"
            data-field="decline-reason"
          />
        </div>
      </div>
    </article>
  `;
}
