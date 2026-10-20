// BOARDS FINAL Approvals view: kind pill tabs with counts, split
// list and detail layout, metric bands, lines below floor, and decision card.
import { html, nothing, type TemplateResult } from "lit";
import { APPROVAL_KINDS, kindWord } from "./manager-logic.js";
import {
  changeOrderItems,
  decisionSubject,
  itemsForKind,
  kindCount,
  type ApprovalCardItem,
} from "./approvals-logic.js";
import type { ApprovalRow, ChangeOrder, ChangeOrderQueueRow } from "./manager-api.js";
import { viewHeader } from "./views.js";

export interface ApprovalsModel {
  readonly phase: "loading" | "ready" | "error" | "refused";
  readonly kind: string;
  readonly rows: readonly ApprovalRow[];
  readonly changeOrders: readonly ChangeOrderQueueRow[];
  readonly changeOrderDetails: ReadonlyMap<string, ChangeOrder>;
  readonly error: string;
  readonly refusal: string;
  readonly busyId: string;
  readonly selectedId?: string;
  readonly declineReason?: string;
  /** The core's waiting count per kind (approvals-logic.ts pendingCounts); absent, a tab counts this kind's rows. */
  readonly counts?: Readonly<Record<string, number>>;
  /** A decision asked for and not yet confirmed: nothing is sent until the person says yes. */
  readonly confirming?: { readonly id: string; readonly decision: "approve" | "reject" };
  /** The last decision in words, or why it did not happen. */
  readonly notice?: string;
}

export function renderApprovals(model: ApprovalsModel): TemplateResult {
  const items = model.kind === "CHANGE_ORDER"
    ? changeOrderItems(model.changeOrders, model.changeOrderDetails)
    : itemsForKind(model.kind, model.rows);
  const selected = items.find((i) => i.id === model.selectedId) ?? items[0];
  const decided = items.filter((i) => i.status !== "PENDING");

  return html`
    ${viewHeader("approvals", "", "")}
    <div class="kind-tabs" role="tablist" aria-label="Approval kinds">
      ${APPROVAL_KINDS.map((k) => {
        const isSelected = model.kind === k.kind;
        const count = kindCount(k.kind, model.rows, model.changeOrders, model.counts);
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
    ${model.notice !== undefined && model.notice !== ""
      ? html`<p class="decision-notice" role="status" tabindex="-1" data-test="decision-notice">${model.notice}</p>`
      : nothing}
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
                      ${items.filter((i) => i.status === "PENDING").map((item) => renderApprovalListCard(item, item.id === selected?.id, model.busyId, model.confirming !== undefined))}
                      ${decided.length > 0
                        ? html`
                            <div class="decided-group" data-test="decided-group">
                              <h3 class="section-heading">Decided</h3>
                              ${decided.map((item) => renderApprovalListCard(item, item.id === selected?.id, model.busyId, false))}
                            </div>
                          `
                        : nothing}
                    </div>
                    <div class="approvals-detail">
                      ${selected ? renderDetailCard(selected, model) : nothing}
                    </div>
                  </div>
                `}
    </div>
  `;
}

function renderAmount(item: ApprovalCardItem): TemplateResult {
  return html`<span class="card-amount amount">${item.amount}${item.amount !== "" && item.amountLabel !== ""
    ? html` <span class="card-amount-label">${item.amountLabel}</span>`
    : nothing}</span>`;
}

function renderApprovalListCard(item: ApprovalCardItem, isSelected: boolean, busyId: string, asking: boolean): TemplateResult {
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
          ${renderAmount(item)}
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
        <span class="detail card-title">${item.title}</span>
        ${renderAmount(item)}
      </div>
      <p class="card-summary">${item.summary}</p>
      <p class="meta card-meta">
        ${item.timeAgo}
        ${item.status === "APPROVED" ? html` · <span class="status-approved">Approved</span>` : nothing}
        ${item.status === "REJECTED" ? html` · <span class="status-rejected">Rejected</span>` : nothing}
      </p>
      ${item.status === "PENDING" && item.kind !== "CHANGE_ORDER"
        ? html`
            <div class="row-actions card-row-actions">
              <button
                type="button"
                class="btn btn-primary"
                data-action="decide"
                data-approval-id="${item.id}"
                data-decision="approve"
                ?disabled=${busyId !== "" || asking}
              >
                Approve
              </button>
              <button
                type="button"
                class="btn btn-danger"
                data-action="decide"
                data-approval-id="${item.id}"
                data-decision="reject"
                ?disabled=${busyId !== "" || asking}
              >
                Reject
              </button>
            </div>
          `
        : nothing}
      ${item.status === "PENDING" && item.kind === "CHANGE_ORDER" && (item.canSignApply || item.canReject)
        ? html`
            <div class="row-actions card-row-actions">
              ${item.canSignApply
                ? html`
                    <button
                      type="button"
                      class="btn btn-primary"
                      data-action="sign-apply"
                      data-approval-id="${item.id}"
                      ?disabled=${busyId !== ""}
                    >
                      Sign and apply
                    </button>
                  `
                : nothing}
              ${item.canReject
                ? html`
                    <button
                      type="button"
                      class="btn btn-danger"
                      data-action="decide"
                      data-approval-id="${item.id}"
                      data-decision="reject"
                      ?disabled=${busyId !== ""}
                    >
                      Reject
                    </button>
                  `
                : nothing}
            </div>
          `
        : nothing}
    </div>
  `;
}

function renderDetailCard(item: ApprovalCardItem, model: ApprovalsModel): TemplateResult {
  const busyId = model.busyId;
  if (item.changeMetrics) {
    return renderChangeOrderDetail(item, busyId);
  }
  if (item.marginMetrics) {
    return renderMarginDetail(item, model);
  }
  if (item.creditMetrics) {
    return renderCreditDetail(item, model);
  }
  if (item.journalLines) {
    return renderJournalEntryDetail(item);
  }
  return renderGenericDetail(item, model);
}

// The decision block shared by the margin, credit and plain cards. A decided request shows the outcome and no
// buttons. A press on Approve or Reject only asks: the confirm names the request and the amount, and nothing is sent
// until the person says yes (the core has no way to take a decision back).
function renderDecision(item: ApprovalCardItem, model: ApprovalsModel, middle: TemplateResult | typeof nothing): TemplateResult {
  const busyId = model.busyId;
  if (item.status !== "PENDING") {
    return html`
      <div class="decision-section">
        <span class="decision-eyebrow">Decision</span>
        <p class="decision-outcome">${item.status === "APPROVED" ? "Approved" : "Rejected"}. Nothing more to do here.</p>
      </div>
    `;
  }
  const confirming = model.confirming?.id === item.id ? model.confirming.decision : null;
  const reasonMissing = confirming === "reject" && (model.declineReason ?? "").trim() === "";
  return html`
    <div class="decision-section">
      <span class="decision-eyebrow">Decision</span>
      ${confirming === null
        ? html`
            <div class="decision-actions">
              <button type="button" class="btn btn-primary approve-btn" data-action="decide" data-approval-id="${item.id}" data-decision="approve" ?disabled=${busyId !== ""}>Approve</button>
              ${middle}
              <button type="button" class="btn btn-link decline-btn" data-action="decide" data-approval-id="${item.id}" data-decision="reject" ?disabled=${busyId !== ""}>Reject</button>
            </div>
          `
        : html`
            <div class="decision-confirm" data-test="decision-confirm" role="group" aria-label="Confirm the decision">
              <p class="decision-confirm-line">${confirming === "approve" ? "Approve" : "Reject"} ${decisionSubject(item)}? ${confirming === "reject" ? "The person who asked is told why." : "This cannot be undone."}</p>
              ${reasonMissing ? html`<p class="meta">Say why in the box below first.</p>` : nothing}
              <div class="decision-actions">
                <button type="button" class="btn ${confirming === "approve" ? "btn-primary" : "btn-danger"}" data-action="decide-confirm" data-approval-id="${item.id}" data-decision="${confirming}" ?disabled=${busyId !== "" || reasonMissing}>${confirming === "approve" ? "Yes, approve" : "Yes, reject"}</button>
                <button type="button" class="btn btn-secondary" data-action="decide-cancel" data-approval-id="${item.id}">Cancel</button>
              </div>
            </div>
          `}
      ${confirming === "approve"
        ? nothing
        : html`
            <div class="decline-reason-group">
              <label class="reason-label">Reason, needed to reject</label>
              <input type="text" class="control reason-input" placeholder="Tell the person who asked why" data-field="decline-reason" />
            </div>
          `}
    </div>
  `;
}

function renderChangeOrderDetail(item: ApprovalCardItem, busyId: string): TemplateResult {
  const metrics = item.changeMetrics!;
  return html`
    <article class="decision-card" data-approval-detail="${item.id}">
      <div class="decision-card-head">
        <h2 class="decision-title">${item.detailTitle}</h2>
        ${item.detailSubtitle !== "" ? html`<p class="decision-subtitle">${item.detailSubtitle}</p>` : nothing}
      </div>

      <div class="metric-band">
        <div class="metric-col">
          <span class="metric-eyebrow">CHANGE DELTA</span>
          <span class="metric-val ${item.amount.startsWith("-") ? "tone-watch" : ""}">${metrics.delta}</span>
        </div>
        <div class="metric-col">
          <span class="metric-eyebrow">FINANCE THRESHOLD</span>
          <span class="metric-val">${metrics.threshold}</span>
        </div>
        <div class="metric-col">
          <span class="metric-eyebrow">FINANCE REQUIRED</span>
          <span class="metric-val">${metrics.financeRequired}</span>
        </div>
      </div>

      ${item.changeLines && item.changeLines.length > 0
        ? html`
            <div class="table-section">
              <h3 class="section-heading">Lines changing</h3>
              <table class="approval-table">
                <thead>
                  <tr>
                    <th class="th-line">OP</th>
                    <th class="th-line">PRODUCT</th>
                    <th class="th-qty">BEFORE</th>
                    <th class="th-qty">AFTER</th>
                    <th class="th-price">LINE DELTA</th>
                  </tr>
                </thead>
                <tbody>
                  ${item.changeLines.map(
                    (line) => html`
                      <tr>
                        <td class="td-line">${line.op}</td>
                        <td class="td-line">
                          <span class="table-line-name">${line.product}</span>
                        </td>
                        <td class="td-qty">${line.before}</td>
                        <td class="td-qty">${line.after}</td>
                        <td class="td-price">${line.lineDelta}</td>
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
        <span class="decision-eyebrow">Decision</span>
        <div class="decision-actions">
          ${item.canSignApply
            ? html`
                <button
                  type="button"
                  class="btn btn-primary sign-apply-btn"
                  data-action="sign-apply"
                  data-approval-id="${item.id}"
                  ?disabled=${busyId !== ""}
                >
                  Sign and apply
                </button>
              `
            : nothing}
          ${item.canReject
            ? html`
                <button
                  type="button"
                  class="btn btn-link decline-btn"
                  data-action="decide"
                  data-approval-id="${item.id}"
                  data-decision="reject"
                  ?disabled=${busyId !== ""}
                >
                  Reject
                </button>
              `
            : nothing}
        </div>
        ${item.canReject
          ? html`
              <div class="decline-reason-group">
                <label class="reason-label">Reason, needed to reject</label>
                <input
                  type="text"
                  class="control reason-input"
                  placeholder="Tell the person who asked why"
                  data-field="decline-reason"
                />
              </div>
            `
          : nothing}
      </div>
    </article>
  `;
}

function renderMarginDetail(item: ApprovalCardItem, model: ApprovalsModel): TemplateResult {
  const metrics = item.marginMetrics!;
  return html`
    <article class="decision-card" data-approval-detail="${item.id}">
      <div class="decision-card-head">
        <h2 class="decision-title">${item.detailTitle}</h2>
        ${item.detailSubtitle !== "" ? html`<p class="decision-subtitle">${item.detailSubtitle}</p>` : nothing}
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

      ${renderDecision(item, model, html`<button type="button" class="btn btn-secondary custom-price-btn" data-action="approve-different-price" data-approval-id="${item.id}" ?disabled=${model.busyId !== ""}>Approve at a different price</button>`)}
    </article>
  `;
}

function renderCreditDetail(item: ApprovalCardItem, model: ApprovalsModel): TemplateResult {
  const metrics = item.creditMetrics!;
  return html`
    <article class="decision-card" data-approval-detail="${item.id}">
      <div class="decision-card-head">
        <h2 class="decision-title">${item.detailTitle}</h2>
        ${item.detailSubtitle !== "" ? html`<p class="decision-subtitle">${item.detailSubtitle}</p>` : nothing}
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

      ${renderDecision(item, model, html`<button type="button" class="btn btn-secondary custom-limit-btn" data-action="approve-smaller-limit" data-approval-id="${item.id}" ?disabled=${model.busyId !== ""}>Approve a smaller limit</button>`)}
    </article>
  `;
}

function renderJournalEntryDetail(item: ApprovalCardItem): TemplateResult {
  return html`
    <article class="decision-card" data-approval-detail="${item.id}">
      <div class="decision-card-head">
        <h2 class="decision-title">${item.detailTitle}</h2>
        ${item.detailSubtitle !== "" ? html`<p class="decision-subtitle">${item.detailSubtitle}</p>` : nothing}
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

function renderGenericDetail(item: ApprovalCardItem, model: ApprovalsModel): TemplateResult {
  return html`
    <article class="decision-card" data-approval-detail="${item.id}">
      <div class="decision-card-head">
        <h2 class="decision-title">${item.detailTitle}</h2>
        ${item.detailSubtitle !== "" ? html`<p class="decision-subtitle">${item.detailSubtitle}</p>` : nothing}
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

      ${renderDecision(item, model, nothing)}
    </article>
  `;
}
