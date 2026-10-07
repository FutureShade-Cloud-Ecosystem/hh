// The returns desk: the till's returns story, rendered from the returns
// flow's state. This view holds no domain logic and no money of its own: it
// spells the flow's facts through the one money helper and asks the host's
// actions through data attributes, read off composedPath() by the app root
// (the design system's host delegation rule: never @click, never a content
// listener that a re-render would strand).
import { css, html, nothing, type TemplateResult } from "lit";
import { keyed } from "lit/directives/keyed.js";
import { formatMoney } from "../money.js";
import { approverLabels } from "../returns/approval.js";
import type { RefundMethod, RefundReceipt, ReturnsFlow } from "../returns/flow.js";
import type { PosRefundApproval } from "@hardscapehouse/api-client";

export const returnsStyles = css`
  /* UI-3 (visual GAP 2): the desk wears the till's own card: the same column
     (the terminal's 1180px cap, 20px gutter, full bleed like the surface above
     it), the same card face, hairline, radius and shadow as an accordion card,
     a 15px semibold head like the step heads, 13px body copy, 11px caption
     labels and the till's input face. The roles read the --hos-* tokens the
     till reads, so both themes follow. */
  .returns {
    /* The bottom lane keeps the last control clear of the pinned hotkey legend. */
    margin: var(--hh-s-24) calc(-1 * var(--hh-s-20)) 0;
    padding-bottom: 72px;
  }
  .returns-col {
    box-sizing: border-box;
    width: min(100%, 1180px);
    margin: 0 auto;
    padding: 0 var(--hh-s-20);
  }
  .returns-card {
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-card);
    background: var(--hos-slate-warm);
    box-shadow: var(--hos-elev-1);
    overflow: hidden;
  }
  .returns h2 {
    font-size: 15px;
    font-weight: 600;
    margin: 0;
    padding: var(--hh-s-12) var(--hh-s-16);
    border-bottom: 1px solid var(--hos-line);
  }
  .returns-body {
    padding: var(--hh-s-16);
    font-size: 13px;
  }
  .returns .hint {
    color: var(--hos-text-dim);
    margin: 0 0 var(--hh-s-16);
  }
  .returns .btn-primary,
  .returns .btn-secondary {
    font-size: 13px;
  }
  /* UI-3 (visual NOTE 4): the one disabled rule the till uses (Complete sale): no fill, grey ink
     and line, no fade, in both themes; not the design system's pale fill. */
  .returns button:disabled,
  .returns button:disabled:hover {
    background: transparent;
    border-color: var(--hos-line);
    color: var(--hos-text-dim);
    opacity: 1;
    cursor: not-allowed;
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
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--hos-text-dim);
    margin: 0 0 var(--hh-s-4);
  }
  .find-row input {
    font-family: inherit;
    font-size: 13px;
    color: var(--hos-text);
    background: var(--hos-field);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hos-radius-control);
    padding: var(--hh-s-8) var(--hh-s-12);
    min-height: 44px;
  }
  .find-row input:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .sale-heading {
    font-size: 15px;
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
    font-size: 13px;
    color: var(--hos-text);
    background: var(--hos-field);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hos-radius-control);
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
    font-size: 13px;
    color: var(--hos-text);
    background: var(--hos-field);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hos-radius-control);
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
    font-size: 13px;
    color: var(--hh-text);
  }
  .submit-row {
    margin: var(--hh-s-16) 0 0;
  }
  .receipt {
    border: 1px solid var(--hos-line);
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
    font-size: 15px;
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
    font-size: 15px;
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
    font-size: 15px;
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
      <div class="returns-col">
      <div class="returns-card">
      <h2>Returns</h2>
      <div class="returns-body">
      <p class="hint">Find the original sale by its number, then choose the lines to take back.</p>
      <div class="find-row">
        <div>
          <label for="ct-returns-order-no">Sale number</label>
          <input
            id="ct-returns-order-no"
            type="text"
            data-field="order-no"
            ?disabled=${flow.busy || flow.dialogOpen}
          />
        </div>
        <button
          type="button"
          class="btn-secondary"
          data-action="returns-find"
          ?disabled=${!flow.canFind || flow.dialogOpen}
        >
          Find sale
        </button>
      </div>
      ${flow.notice === null ? nothing : html`<p class="notice" role="status" data-testid="returns-notice">${flow.notice}</p>`}
      ${flow.lookup === null ? nothing : renderSale(flow)}
      ${flow.refusal === null || flow.receipt !== null ? nothing : renderRefusal(flow.refusal)}
      ${flow.dialogOpen ? renderApprovalDialog(flow) : nothing}
      ${flow.receipt === null ? nothing : renderReceipt(flow.receipt)}
      </div>
      </div>
      </div>
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

// The keyed block below is a directive result rather than a bare
// TemplateResult; a child part takes either, so this helper's return type is
// the union Lit's templates accept from it.
function renderSale(flow: ReturnsFlow): unknown {
  const lookup = flow.lookup;
  if (lookup === null) return html``;
  if (flow.receipt !== null) {
    // The receipt tells this return's story now; the sale's lines wait until
    // the desk starts another one.
    return html``;
  }
  const nothingReturnable = lookup.lines.every((line) => line.qtySold <= line.qtyReturned);
  // The whole sale's block is keyed on the draft's epoch: the desk's fields
  // are uncontrolled by design, so a reset the model performs must rebuild
  // the nodes too, or a new find would inherit the last draft's typed words
  // (the review round's P1).
  return keyed(
    flow.draftEpoch,
    html`
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
    `,
  );
}

function renderLine(
  flow: ReturnsFlow,
  line: { orderLineId: string; description: string; qtySold: number; qtyReturned: number; unitPriceCents: number; lineTotalCents: number },
): TemplateResult {
  // The desk's controls stand down while anything is in flight or the
  // approval dialog holds the till: nothing drafts under a send.
  const frozen = flow.submitting || flow.dialogOpen || flow.approving;
  // What is still returnable is the line's two returned-and-sold counts read
  // side by side; the commit re-guards the quantity on the server, so this
  // read-off is for the clerk's eye, never an authority.
  return html`
    <li class="row">
      <span class="line-desc">${line.description}</span>
      <span class="line-figures">each ${formatMoney(line.unitPriceCents)}, line ${formatMoney(line.lineTotalCents)}</span>
      <span class="line-returnable">sold ${line.qtySold}, returned ${line.qtyReturned}</span>
      <span class="line-qty">
        <span class="line-qty-label">Return</span>
        <input
          type="number"
          min="0"
          step="1"
          inputmode="numeric"
          data-field="qty"
          data-line=${line.orderLineId}
          aria-label="Quantity to return: ${line.description}"
          ?disabled=${frozen}
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
  // The chosen rail's words: the card rail's only when the status route's own
  // flag says simulated, so the screen never claims it on its own.
  let chosenNote: TemplateResult | typeof nothing = nothing;
  if (flow.method === "CARD" && flow.cardSimulated === true) {
    chosenNote = html`<p class="notice" role="status" data-testid="simulated-notice">
      Simulated refund: nothing reached a card.
    </p>`;
  } else if (flow.method !== null) {
    chosenNote = html`<p class="notice" role="status">${methodWords(flow.method)}: ${methodNote(flow)}</p>`;
  }
  const frozen = flow.submitting || flow.dialogOpen || flow.approving;
  return html`
    <div class="methods" role="group" aria-label="Refund method">
      ${(["CASH", "CARD", "ACCOUNT_CREDIT"] as const).map((method) => html`
        <button
          type="button"
          class="btn-secondary"
          data-action="returns-method"
          data-method=${method}
          aria-pressed=${flow.method === method ? "true" : "false"}
          ?disabled=${!flow.methodAvailable(method) || frozen}
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
      <!-- The five codes the contract spells, written static because the union
           is closed and Other leads because a fresh select shows its first
           option: the flow's default reason (REASON_CODES in returns/flow.ts)
           is the same Other, so the screen and the draft never disagree. -->
      <select id="ct-returns-reason" data-field="reason-code" ?disabled=${frozen}>
        <option value="OTHER">Other</option>
        <option value="DAMAGED">Damaged</option>
        <option value="DEFECT">Defect</option>
        <option value="WRONG_ITEM">Wrong item</option>
        <option value="NO_LONGER_NEEDED">No longer needed</option>
      </select>
      <label for="ct-returns-note">Note</label>
      <input id="ct-returns-note" type="text" data-field="note" ?disabled=${frozen} />
      <label class="restock">
        <input type="checkbox" data-field="restock" checked ?disabled=${frozen} />
        Restock the goods
      </label>
    </div>
    <div class="submit-row">
      <button type="button" class="btn-primary" data-action="returns-submit" ?disabled=${!flow.canSubmit || frozen}>
        ${flow.method === null ? "Record refund" : submitLabel(flow.method)}
      </button>
    </div>
  `;
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
