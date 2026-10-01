// The returns desk: the till's returns story, rendered from the returns
// flow's state. This view holds no domain logic and no money of its own: it
// spells the flow's facts through the one money helper and asks the host's
// actions through data attributes, read off composedPath() by the app root
// (the design system's host delegation rule: never @click, never a content
// listener that a re-render would strand).
import { css, html, nothing, type TemplateResult } from "lit";
import { formatMoney } from "../money.js";
import { approverLabels } from "../returns/approval.js";
import type { RefundMethod, RefundReceipt, ReasonCode, ReturnsFlow } from "../returns/flow.js";
import type { PosRefundApproval } from "@hardscapehouse/api-client";

export const returnsStyles = css`
  .returns {
    max-width: 720px;
    margin: var(--hh-s-24) 0 0;
    border-top: 1px solid var(--hh-line);
    padding-top: var(--hh-s-16);
  }
  .returns h2 {
    font-size: var(--hh-s-16);
    margin: 0 0 var(--hh-s-8);
  }
  .returns .hint {
    color: var(--hh-text-dim);
    margin: 0 0 var(--hh-s-16);
  }
  .find-row {
    display: flex;
    align-items: end;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-16);
  }
  .find-row label {
    display: block;
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin: 0 0 var(--hh-s-4);
  }
  .find-row input {
    font-family: inherit;
    font-size: var(--hh-s-16);
    color: var(--hh-text);
    background: var(--hh-card);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    padding: var(--hh-s-8) var(--hh-s-12);
    min-height: 44px;
  }
  .find-row input:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .sale-heading {
    font-size: var(--hh-s-16);
    margin: 0 0 var(--hh-s-8);
  }
  .sale-meta {
    color: var(--hh-text-dim);
    margin: 0 0 var(--hh-s-8);
  }
  .line-desc {
    font-weight: 600;
    min-width: 0;
  }
  .line-figures {
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
    white-space: nowrap;
  }
  .line-returnable {
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .notice {
    color: var(--hh-text-dim);
  }
  .line-qty {
    display: flex;
    align-items: center;
    gap: var(--hh-s-8);
    white-space: nowrap;
  }
  .line-qty label {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }
  .line-qty input {
    font-family: inherit;
    font-size: var(--hh-s-16);
    color: var(--hh-text);
    background: var(--hh-card);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    padding: var(--hh-s-8) var(--hh-s-12);
    width: 5em;
    min-height: 44px;
  }
  .line-qty input:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .methods {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-12);
    margin: var(--hh-s-16) 0 var(--hh-s-8);
  }
  .methods button[aria-pressed="true"] {
    border-color: var(--hh-accent);
    font-weight: 600;
  }
  .draft-row {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-12);
  }
  .draft-row label {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }
  .draft-row select,
  .draft-row input[type="text"] {
    font-family: inherit;
    font-size: var(--hh-s-16);
    color: var(--hh-text);
    background: var(--hh-card);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    padding: var(--hh-s-8) var(--hh-s-12);
    min-height: 44px;
  }
  .draft-row select:focus-visible,
  .draft-row input:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .restock {
    display: flex;
    align-items: center;
    gap: var(--hh-s-8);
    font-size: var(--hh-s-16);
    color: var(--hh-text);
  }
  .submit-row {
    margin: var(--hh-s-16) 0 0;
  }
  .receipt {
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-control);
    padding: var(--hh-s-16);
    margin: var(--hh-s-16) 0 0;
    max-width: 480px;
  }
  .refusal {
    border: 1px solid var(--hh-state-stop);
    border-radius: var(--hh-radius-control);
    padding: var(--hh-s-16);
    margin: var(--hh-s-16) 0 0;
    max-width: 480px;
  }
  .refusal h3 {
    font-size: var(--hh-s-16);
    margin: 0 0 var(--hh-s-8);
  }
  .refusal p {
    margin: 0 0 var(--hh-s-8);
  }
  .refusal button {
    margin-top: var(--hh-s-8);
  }
  .dialog {
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    background: var(--hh-card);
    padding: var(--hh-s-16);
    margin: var(--hh-s-16) 0 0;
    max-width: 480px;
  }
  .dialog h3 {
    font-size: var(--hh-s-16);
    margin: 0 0 var(--hh-s-8);
  }
  .dialog p {
    margin: 0 0 var(--hh-s-12);
  }
  .dialog .cards {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
    margin: 0 0 var(--hh-s-16);
  }
  .dialog .card {
    min-height: 64px;
    font-size: var(--hh-s-16);
    font-weight: 600;
    background: var(--hh-card);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    text-align: start;
    padding: var(--hh-s-16) var(--hh-s-20);
  }
  .dialog .card:hover:not(:disabled) {
    border-color: var(--hh-accent);
  }
  .receipt h3 {
    font-size: var(--hh-s-16);
    margin: 0 0 var(--hh-s-8);
  }
  .receipt p {
    margin: 0 0 var(--hh-s-8);
  }
  .receipt .figures {
    font-variant-numeric: tabular-nums;
    font-weight: 600;
  }
`;

export function renderReturns(flow: ReturnsFlow): TemplateResult {
  return html`
    <section class="returns" data-testid="returns">
      <h2>Returns</h2>
      <p class="hint">Find the original sale by its number, then choose the lines to take back.</p>
      <div class="find-row">
        <div>
          <label for="ct-returns-order-no">Sale number</label>
          <input id="ct-returns-order-no" type="text" data-field="order-no" ?disabled=${flow.busy} />
        </div>
        <button type="button" class="btn-secondary" data-action="returns-find" ?disabled=${!flow.canFind}>
          Find sale
        </button>
      </div>
      ${flow.notice === null ? nothing : html`<p class="notice" role="status" data-testid="returns-notice">${flow.notice}</p>`}
      ${flow.lookup === null ? nothing : renderSale(flow)}
      ${flow.refusal === null || flow.receipt !== null ? nothing : renderRefusal(flow.refusal)}
      ${flow.dialogOpen ? renderApprovalDialog(flow) : nothing}
      ${flow.receipt === null ? nothing : renderReceipt(flow.receipt)}
    </section>
  `;
}

function renderRefusal(approval: PosRefundApproval): TemplateResult {
  // The roles named the way the refusal speaks them: the Sales manager, or
  // the Branch manager. A role key with no label shows as the key itself.
  const named = approverLabels(approval.approvers).map((role) => `the ${role}`);
  const roles =
    named.length === 1 ? named[0] : `${named.slice(0, -1).join(", ")}, or ${named[named.length - 1]}`;
  return html`
    <section class="refusal" data-testid="refund-refusal" role="alert">
      <h3>Refused</h3>
      <p>
        This refund is over the dealer's approval amount of ${formatMoney(approval.amount_cents)}, so
        this desk cannot commit it as rung.
      </p>
      <p>It can be committed by ${roles}: a manager whose role holds the approval.</p>
      <button type="button" class="btn-primary" data-action="returns-approve">Manager approval</button>
    </section>
  `;
}

function renderApprovalDialog(flow: ReturnsFlow): TemplateResult {
  return html`
    <section class="dialog" role="dialog" aria-modal="true" aria-label="Manager approval" data-testid="approval-dialog">
      <h3>Manager approval</h3>
      <p>The manager taps their own card here: the till stays the clerk's, and the refund is recorded as the manager's decision with the clerk named beside.</p>
      ${flow.approverFlows === null
        ? html`<p class="notice" role="status">Asking for the approver cards.</p>`
        : flow.approverFlows.length === 0
          ? html`<p class="notice">No approver card is available in this demo.</p>`
          : html`
              <div class="cards" role="group" aria-label="Approver roles">
                ${flow.approverFlows.map(
                  (row) => html`
                    <button
                      type="button"
                      class="card"
                      data-action="returns-approver-flow"
                      data-flow=${row.flow}
                      ?disabled=${flow.approving}
                    >
                      ${row.label ?? row.flow}
                    </button>
                  `,
                )}
              </div>
            `}
      <button type="button" class="btn-secondary" data-action="returns-approve-cancel" ?disabled=${flow.approving}>
        Cancel
      </button>
    </section>
  `;
}

function renderSale(flow: ReturnsFlow): TemplateResult {
  const lookup = flow.lookup;
  if (lookup === null) return html``;
  if (flow.receipt !== null) {
    // The receipt tells this return's story now; the sale's lines wait until
    // the desk starts another one.
    return html``;
  }
  const nothingReturnable = lookup.lines.every((line) => line.qtySold <= line.qtyReturned);
  return html`
    <h3 class="sale-heading" data-testid="returns-sale">Sale ${lookup.orderNo}</h3>
    <p class="sale-meta">Paid by ${tenderWords(lookup.saleTender)}.</p>
    ${nothingReturnable
      ? html`<p class="notice" role="status">Nothing on this sale is still returnable: every line has gone back already.</p>`
      : html`
          <ul class="rows" aria-label="Sale lines">
            ${lookup.lines.map((line) => renderLine(flow, line))}
          </ul>
          ${renderDraft(flow)}
        `}
  `;
}

function renderLine(
  flow: ReturnsFlow,
  line: { orderLineId: string; description: string; qtySold: number; qtyReturned: number; unitPriceCents: number; lineTotalCents: number },
): TemplateResult {
  // What is still returnable is the line's two returned-and-sold counts read
  // side by side; the commit re-guards the quantity on the server, so this
  // read-off is for the clerk's eye, never an authority.
  return html`
    <li class="row">
      <span class="line-desc">${line.description}</span>
      <span class="line-figures">each ${formatMoney(line.unitPriceCents)}, line ${formatMoney(line.lineTotalCents)}</span>
      <span class="line-returnable">sold ${line.qtySold}, returned ${line.qtyReturned}</span>
      <span class="line-qty">
        <label for="ct-qty-${line.orderLineId}">Return</label>
        <input
          id="ct-qty-${line.orderLineId}"
          type="number"
          min="0"
          step="1"
          inputmode="numeric"
          data-field="qty"
          data-line=${line.orderLineId}
          aria-label="Quantity to return: ${line.description}"
          ?disabled=${flow.submitting}
        />
      </span>
    </li>
  `;
}

const METHOD_LABELS: Record<RefundMethod, string> = {
  CASH: "Cash",
  CARD: "Card",
  ACCOUNT_CREDIT: "Account credit",
};

function submitLabel(method: RefundMethod): string {
  if (method === "CASH") return "Refund cash";
  if (method === "CARD") return "Refund card";
  return "Refund to account";
}

export function methodWords(method: RefundMethod): string {
  if (method === "CASH") return "cash, from the drawer";
  if (method === "CARD") return "card";
  return "account credit";
}

function renderDraft(flow: ReturnsFlow): TemplateResult {
  const lookup = flow.lookup;
  if (lookup === null) return html``;
  const chosenNote =
    flow.method === null
      ? nothing
      : html`<p class="notice" role="status">
          ${methodWords(flow.method)}: ${flow.method === "CARD" && flow.cardSimulated === true
            ? "Simulated refund: nothing reached a card."
            : methodNote(flow)}
        </p>`;
  return html`
    <div class="methods" role="group" aria-label="Refund method">
      ${(["CASH", "CARD", "ACCOUNT_CREDIT"] as const).map((method) => html`
        <button
          type="button"
          class="btn-secondary"
          data-action="returns-method"
          data-method=${method}
          aria-pressed=${flow.method === method ? "true" : "false"}
          ?disabled=${!flow.methodAvailable(method) || flow.submitting}
        >
          ${METHOD_LABELS[method]}
        </button>
      `)}
    </div>
    ${flow.methodAvailable("CASH")
      ? nothing
      : html`<p class="notice">Cash waits on the drawer: ${drawerWords(flow.drawer)}</p>`}
    ${flow.methodAvailable("CARD")
      ? nothing
      : html`<p class="notice">No card refund: this sale was not paid by card, so there is no capture to reverse.</p>`}
    ${chosenNote}
    <div class="draft-row">
      <label for="ct-returns-reason">Reason</label>
      <select id="ct-returns-reason" data-field="reason-code" ?disabled=${flow.submitting}>
        ${REASON_OPTIONS.map((code) => html`<option value=${code}>${reasonWords(code)}</option>`)}
      </select>
      <label for="ct-returns-note">Note</label>
      <input id="ct-returns-note" type="text" data-field="note" ?disabled=${flow.submitting} />
      <label class="restock">
        <input type="checkbox" data-field="restock" checked ?disabled=${flow.submitting} />
        Restock the goods
      </label>
    </div>
    <div class="submit-row">
      <button type="button" class="btn-primary" data-action="returns-submit" ?disabled=${!flow.canSubmit}>
        ${flow.method === null ? "Record refund" : submitLabel(flow.method)}
      </button>
    </div>
  `;
}

// Other leads the list because it is the desk's default: a fresh select
// shows its first option, and the flow's default reason must be the one the
// screen already shows, never a silent mismatch between the two.
const REASON_OPTIONS: readonly ReasonCode[] = ["OTHER", "DAMAGED", "DEFECT", "WRONG_ITEM", "NO_LONGER_NEEDED"];

export function reasonWords(code: ReasonCode): string {
  if (code === "DAMAGED") return "Damaged";
  if (code === "DEFECT") return "Defect";
  if (code === "WRONG_ITEM") return "Wrong item";
  if (code === "NO_LONGER_NEEDED") return "No longer needed";
  return "Other";
}

function drawerWords(drawer: ReturnsFlow["drawer"]): string {
  if (drawer.kind === "closed") return "no drawer is open at the sale's branch.";
  return "the drawer could not be asked just now.";
}

function methodNote(flow: ReturnsFlow): string {
  if (flow.method === "CASH") return "paid out of the open drawer.";
  if (flow.method === "CARD") return "the original card capture is reversed.";
  return "the customer's account is credited.";
}

function renderReceipt(receipt: RefundReceipt): TemplateResult {
  return html`
    <section class="receipt" data-testid="refund-receipt" role="status">
      <h3>Refund recorded</h3>
      <p>${receipt.returnNo}${receipt.replayed ? " (the same return, answered again)" : ""}</p>
      <p class="figures">Total refunded ${formatMoney(receipt.totalCents)} by ${methodWords(receipt.method)}</p>
      ${receipt.method === "CARD" && receipt.cardSimulated === true
        ? html`<p data-testid="simulated-notice">Simulated refund: nothing reached a card.</p>`
        : nothing}
      ${receipt.approverName === null ? nothing : html`<p>Approved by ${receipt.approverName}.</p>`}
      ${receipt.clerkName === null ? nothing : html`<p>Rung by ${receipt.clerkName}.</p>`}
      <button type="button" class="btn-secondary" data-action="returns-another">Start another return</button>
    </section>
  `;
}

function tenderWords(tender: string): string {
  if (tender === "ACCOUNT") return "account";
  if (tender === "CASH") return "cash";
  if (tender === "CARD") return "card";
  return "cheque";
}
