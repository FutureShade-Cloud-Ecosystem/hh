// The Returns surface: a return built like a credit, the customer first. This view holds no domain
// logic and no money of its own: it spells the flow's facts through the one money helper and asks
// the host's actions through data attributes, read off composedPath() by the app root (the design
// system's host delegation rule: never @click, never a content listener that a re-render would
// strand). Layout follows the proposed boards (customer step, lines step); every figure is a number
// the server answered.
import { css, html, nothing, type TemplateResult } from "lit";
import { keyed } from "lit/directives/keyed.js";
import { formatMoney } from "../money.js";
import { DISPOSITIONS, DISPOSITION_WORDS, type Disposition } from "../returns/draft.js";
import { STEP_ORDER, type ReturnStep, type ReturnsFlow } from "../returns/flow.js";
import {
  defectCostCents,
  defectThresholdSentence,
  type DefectSentence,
} from "../returns/defect.js";
import type { CustomerLine, CustomerOrder } from "../returns/lines.js";
import { approverLabels } from "../returns/approval.js";
import type { RefundMethod } from "../returns/note.js";
import type { PosRefundApproval } from "@hardscapehouse/api-client";

export const returnsStyles = css`
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
    font-size: 13px;
  }
  .returns .head-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--hh-s-16);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-16);
  }
  .returns .eyebrow {
    display: block;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--hos-text-dim);
    margin: 0 0 var(--hh-s-4);
  }
  .returns h2 {
    font-size: 20px;
    font-weight: 600;
    margin: 0 0 var(--hh-s-4);
  }
  .returns .subtitle {
    color: var(--hos-text-dim);
    margin: 0;
  }
  .returns .stepper {
    display: flex;
    gap: var(--hh-s-8);
    flex-wrap: wrap;
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .returns .stepper li {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    padding: var(--hh-s-4) var(--hh-s-16);
    border: 1px solid var(--hh-control-line);
    border-radius: 999px;
    color: var(--hos-text-dim);
    background: transparent;
  }
  .returns .stepper li[aria-current="step"] {
    border-color: var(--hh-accent);
    color: var(--hos-text);
    font-weight: 600;
  }
  .returns .stepper li[data-state="done"] {
    border-color: var(--hos-line);
    color: var(--hos-text);
  }
  .returns .rt-card {
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-card);
    background: var(--hos-slate-warm);
    box-shadow: var(--hos-elev-1);
    padding: var(--hh-s-16);
    min-width: 0;
  }
  .returns .rt-card.focus {
    border-color: var(--hh-accent);
  }
  .returns .customer-step {
    width: min(100%, 520px);
    margin: var(--hh-s-24) auto 0;
    display: grid;
    gap: var(--hh-s-16);
  }
  .returns input[type="text"],
  .returns input[type="search"],
  .returns input[type="number"] {
    font-family: inherit;
    font-size: 13px;
    color: var(--hos-text);
    background: var(--hos-field);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hos-radius-control);
    padding: var(--hh-s-8) var(--hh-s-12);
    min-height: 44px;
    box-sizing: border-box;
  }
  .returns input:focus-visible,
  .returns button:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .returns .search-input {
    width: 100%;
  }
  .returns .hint {
    color: var(--hos-text-dim);
    margin: var(--hh-s-8) 0 0;
  }
  .returns .notice {
    color: var(--hos-text-dim);
    margin: var(--hh-s-8) 0 0;
  }
  .returns .note-stop {
    color: var(--hh-state-stop);
    margin: 0 0 var(--hh-s-12);
  }
  .returns .matches {
    list-style: none;
    margin: var(--hh-s-12) 0 0;
    padding: 0;
    display: grid;
    gap: var(--hh-s-8);
  }
  .returns .matches button {
    padding: var(--hh-s-8) var(--hh-s-12);
    align-items: center;
    width: 100%;
    display: flex;
    justify-content: space-between;
    gap: var(--hh-s-12);
    text-align: start;
    min-height: 44px;
    min-width: 0;
  }
  .returns .why {
    border-radius: var(--hos-radius-card);
    background: color-mix(in srgb, var(--hh-accent) 22%, transparent);
    padding: var(--hh-s-12) var(--hh-s-16);
  }
  .returns .why h3 {
    font-size: 13px;
    margin: 0 0 var(--hh-s-4);
  }
  .returns .why p {
    margin: 0;
    color: var(--hos-text-dim);
  }
  .returns .lines-step {
    display: grid;
    grid-template-columns: minmax(0, 3fr) minmax(0, 1fr);
    gap: var(--hh-s-16);
    align-items: start;
  }
  @media (max-width: 760px) {
    .returns .lines-step {
      grid-template-columns: minmax(0, 1fr);
    }
  }
  .returns .stack {
    display: grid;
    gap: var(--hh-s-12);
    min-width: 0;
  }
  .returns .customer-card {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    justify-content: space-between;
  }
  .returns .customer-name {
    font-size: 15px;
    font-weight: 600;
    margin: 0;
    overflow-wrap: anywhere;
  }
  .returns .customer-meta {
    color: var(--hos-text-dim);
    margin: var(--hh-s-4) 0 0;
  }
  .returns .filter-row {
    display: flex;
    gap: var(--hh-s-12);
    align-items: center;
    flex-wrap: wrap;
  }
  .returns .filter-row input {
    flex: 1 1 220px;
    min-width: 0;
  }
  .returns .order-head {
    display: flex;
    justify-content: space-between;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    font-weight: 600;
    margin: 0 0 var(--hh-s-12);
  }
  .returns .order-head .open-count {
    font-weight: 400;
    color: var(--hos-text-dim);
  }
  .returns .line-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--hh-s-12);
  }
  .returns .line {
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-card);
    padding: var(--hh-s-12);
    display: grid;
    gap: var(--hh-s-12);
  }
  .returns .line[data-picked="true"] {
    border-color: var(--hh-accent);
  }
  .returns .line-top {
    display: flex;
    justify-content: space-between;
    gap: var(--hh-s-16);
    flex-wrap: wrap;
  }
  .returns .line-desc {
    font-weight: 600;
    overflow-wrap: anywhere;
    margin: 0;
  }
  .returns .line-sku {
    color: var(--hos-text-dim);
    font-size: 11px;
    margin: var(--hh-s-4) 0 0;
  }
  .returns .line-bought {
    color: var(--hos-text-dim);
    margin: 0;
    font-variant-numeric: tabular-nums;
  }
  .returns .qty {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    margin-top: var(--hh-s-8);
  }
  .returns .qty input {
    width: 5em;
    text-align: center;
  }
  .returns .qty button {
    min-width: 44px;
    min-height: 44px;
  }
  .returns .dispositions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-8);
  }
  .returns .dispositions button {
    min-height: 44px;
    font-size: 12px;
  }
  .returns .dispositions button[aria-pressed="true"] {
    border-color: var(--hh-accent);
    background: color-mix(in srgb, var(--hh-accent) 22%, transparent);
    font-weight: 600;
  }
  /*
   * The manufacturer defect threshold sentence (walk change 6 item 4, boards
   * ct-return-defect-over and ct-return-defect-under). One bullet at the line's left, a short word
   * after. The sentence is one piece of prose; the bullet is the only visual difference between
   * the over and under thresholds, kept identical in size so the two boards read identically.
   */
  .returns .defect-sentence {
    display: flex;
    align-items: flex-start;
    gap: var(--hh-s-8);
    margin: var(--hh-s-12) 0 0;
    font-size: 13px;
    font-weight: 600;
  }
  .returns .defect-sentence::before {
    content: "";
    flex: none;
    width: 10px;
    height: 10px;
    border-radius: 999px;
    background: var(--hh-accent);
    margin-top: 6px;
  }
  .returns .defect-sentence[data-kind="under"]::before {
    background: var(--hos-text-dim);
  }
  .returns .defect-sentence[data-testid="returns-defect-unreadable"] {
    font-weight: 400;
    color: var(--hos-text-dim);
  }
  .returns .defect-sentence[data-testid="returns-defect-unreadable"]::before {
    display: none;
  }
  .returns .rt-credit h3 {
    font-size: 15px;
    margin: 0 0 var(--hh-s-12);
  }
  .returns .credit-lines {
    list-style: none;
    margin: 0 0 var(--hh-s-12);
    padding: 0;
    display: grid;
    gap: var(--hh-s-8);
  }
  .returns .credit-lines li {
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-control);
    padding: var(--hh-s-8) var(--hh-s-12);
    background: var(--hos-field);
    overflow-wrap: anywhere;
  }
  .returns .credit-lines .server-figure {
    display: block;
    color: var(--hos-text-dim);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
  }
  .returns .credit-total {
    font-size: 28px;
    font-weight: 700;
    margin: 0 0 var(--hh-s-4);
    font-variant-numeric: tabular-nums;
  }
  .returns .credit-count {
    font-weight: 600;
    margin: 0 0 var(--hh-s-12);
  }
  .returns .order-blocked {
    color: var(--hh-state-stop);
    margin: 0 0 var(--hh-s-12);
  }
  .returns .btn-wide {
    margin-top: var(--hh-s-12);
    width: 100%;
    min-height: 48px;
  }
  /* The one disabled rule the till uses (Complete sale): no fill, grey ink and line, no fade. */
  .returns button:disabled,
  .returns button:disabled:hover {
    background: transparent;
    border-color: var(--hos-line);
    color: var(--hos-text-dim);
    opacity: 1;
    cursor: not-allowed;
  }
  .returns .methods {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-12);
    margin: var(--hh-s-16) 0 var(--hh-s-8);
  }
  .returns .methods button {
    min-height: 44px;
  }
  .returns .methods button[aria-pressed="true"] {
    border-color: var(--hh-accent);
    background: color-mix(in srgb, var(--hh-accent) 22%, transparent);
    font-weight: 600;
  }
  .returns .refund-step {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) minmax(0, 1.2fr);
    gap: var(--hh-s-16);
    align-items: start;
  }
  .returns .refund-step[data-walkin="true"] {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  }
  @media (max-width: 1100px) {
    .returns .refund-step {
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    }
  }
  .returns .rt-card.swap {
    border-color: var(--hh-accent);
    background: color-mix(in srgb, var(--hh-accent) 8%, var(--hos-slate-warm));
  }
  .returns .rt-card.swap .hint {
    margin: 0 0 var(--hh-s-12);
  }
  .returns .rt-card.swap .matches button {
    align-items: center;
  }
  .returns .swap-lines {
    list-style: none;
    margin: var(--hh-s-12) 0;
    padding: 0;
    display: grid;
    gap: var(--hh-s-8);
  }
  .returns .swap-lines li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--hh-s-8);
    flex-wrap: wrap;
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-control);
    padding: var(--hh-s-8) var(--hh-s-12);
    background: var(--hos-field);
  }
  .returns .swap-figures {
    display: grid;
    gap: var(--hh-s-4);
    margin: var(--hh-s-12) 0;
    font-variant-numeric: tabular-nums;
  }
  .returns .swap-figures div {
    display: flex;
    justify-content: space-between;
    gap: var(--hh-s-12);
  }
  .returns .swap-figures .strong {
    font-weight: 700;
  }
  @media (max-width: 760px) {
    .returns .refund-step {
      grid-template-columns: minmax(0, 1fr);
    }
  }
  .returns .rt-card h3 {
    font-size: 15px;
    margin: 0 0 var(--hh-s-12);
  }
  .returns .refund-actions,
  .returns .rt-card > button.btn-secondary {
    margin-top: var(--hh-s-16);
  }
  .returns .refund-wide {
    grid-column: 1 / -1;
  }
  .returns .refund-orders {
    list-style: none;
    margin: var(--hh-s-12) 0;
    padding: 0;
    display: grid;
    gap: var(--hh-s-8);
  }
  .returns .refund-orders li,
  .returns .done-return {
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-control);
    padding: var(--hh-s-8) var(--hh-s-12);
    background: var(--hos-field);
    overflow-wrap: anywhere;
  }
  .returns .figures {
    font-variant-numeric: tabular-nums;
    font-weight: 600;
  }
  .returns .done-return p {
    margin: 0 0 var(--hh-s-4);
  }
  .returns .receipt-line {
    font-family: ui-monospace, monospace;
    font-size: 12px;
    color: var(--hos-text-dim);
  }
  .returns .refusal {
    border: 1px solid var(--hh-state-stop);
    border-radius: var(--hos-radius-control);
    padding: var(--hh-s-16);
    margin: var(--hh-s-16) 0 0;
  }
  .returns .refusal h3,
  .returns .dialog h3 {
    font-size: 15px;
    margin: 0 0 var(--hh-s-8);
  }
  .returns .refusal p,
  .returns .dialog p {
    margin: 0 0 var(--hh-s-8);
  }
  .returns .dialog {
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hos-radius-control);
    background: var(--hh-card);
    padding: var(--hh-s-16);
    margin: var(--hh-s-16) 0 0;
  }
  .returns .dialog .cards {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
    margin: 0 0 var(--hh-s-16);
  }
  .returns .dialog .approver-card {
    min-height: 64px;
    font-size: var(--hh-s-16);
    font-weight: 600;
    text-align: start;
    padding: var(--hh-s-16) var(--hh-s-20);
  }
`;

const STEP_WORDS: Record<ReturnStep, string> = {
  customer: "Customer",
  lines: "Lines",
  refund: "Refund",
  done: "Done",
};

export function renderReturns(flow: ReturnsFlow): TemplateResult {
  return html`
    <section class="returns" data-testid="returns" data-step=${flow.step}>
      <div class="returns-col">
        <div class="head-row">
          <div>
            <span class="eyebrow">Point of sale</span>
            <h2>New Return</h2>
            <p class="subtitle">${subtitle(flow)}</p>
          </div>
          ${renderStepper(flow.step)}
        </div>
        ${flow.step === "customer" ? renderCustomerStep(flow) : nothing}
        ${flow.step === "lines" ? renderLinesStep(flow) : nothing}
        ${flow.step === "refund" ? renderRefundStep(flow) : nothing}
        ${flow.step === "done" ? renderDoneStep(flow) : nothing}
      </div>
    </section>
  `;
}

function subtitle(flow: ReturnsFlow): string {
  if (flow.step === "customer") {
    return "A credit note built like a new sales order: the customer first, then their lines across every order.";
  }
  const name = flow.customer?.name ?? "The customer";
  if (flow.step === "lines") return `${name}: pick the lines coming back, from any of their orders.`;
  if (flow.step === "refund") return `${name}: choose how the credit goes back.`;
  return `${name}: the credit note is recorded.`;
}

function renderStepper(current: ReturnStep): TemplateResult {
  const at = STEP_ORDER.indexOf(current);
  return html`
    <ol class="stepper" aria-label="Return steps" data-testid="returns-stepper">
      ${STEP_ORDER.map((step, index) => html`
        <li
          data-step=${step}
          data-state=${index < at ? "done" : index === at ? "current" : "next"}
          aria-current=${index === at ? "step" : nothing}
        >
          <span>${index + 1}</span> ${STEP_WORDS[step]}
        </li>
      `)}
    </ol>
  `;
}

function renderCustomerStep(flow: ReturnsFlow): TemplateResult {
  return html`
    <div class="customer-step">
      <div class="rt-card focus">
        <span class="eyebrow">Customer</span>
        ${keyed(
          flow.epoch,
          html`<input
            type="search"
            class="search-input"
            id="ct-returns-customer"
            data-field="returns-customer"
            aria-label="Search name or account number"
            placeholder="Search name or account #..."
            autocomplete="off"
            ?disabled=${flow.loading}
          />`,
        )}
        <p class="hint">
          Only this customer's orders come up on the next step: every sales order they ever bought, grouped by order or job.
          The house walk-in account is not a returns customer.
        </p>
        ${flow.notice === null ? nothing : html`<p class="notice" role="status" data-testid="returns-notice">${flow.notice}</p>`}
        ${flow.searching ? html`<p class="notice" role="status">Searching.</p>` : nothing}
        ${flow.matches.length > 0
          ? html`<ul class="matches" aria-label="Customers" data-testid="returns-customers">
              ${flow.matches.map((row) => html`
                <li>
                  <button
                    type="button"
                    class="btn-secondary"
                    data-action="returns-customer"
                    data-customer=${row.id}
                    ?disabled=${flow.loading}
                  >
                    <span>${row.name}</span>
                    <span>${row.account_no}</span>
                  </button>
                </li>
              `)}
            </ul>`
          : flow.searched && !flow.searching && flow.notice === null
            ? html`<p class="notice" role="status" data-testid="returns-no-customer">No customer answers to that: check the name or account number.</p>`
            : nothing}
        ${flow.loading ? html`<p class="notice" role="status">Reading this customer's orders.</p>` : nothing}
      </div>
      <div class="rt-card" data-testid="returns-walkin">
        <span class="eyebrow">No account</span>
        <button type="button" class="btn-secondary" data-action="returns-walkin" aria-expanded=${flow.walkInOpen ? "true" : "false"} ?disabled=${flow.loading}>
          Walk in sale
        </button>
        ${flow.walkInOpen
          ? html`
              <p class="hint">A walk in sale is found only by its receipt or order number, and goes back to cash or card.</p>
              ${keyed(
                flow.epoch,
                html`<input
                  type="search"
                  class="search-input"
                  data-field="returns-walkin-number"
                  aria-label="Receipt or order number"
                  placeholder="Receipt or order number..."
                  autocomplete="off"
                  ?disabled=${flow.loading}
                />`,
              )}
              <button type="button" class="btn-primary" data-action="returns-walkin-find" ?disabled=${flow.loading}>Find the sale</button>
            `
          : nothing}
      </div>
      <div class="why">
        <h3>Why customer first</h3>
        <p>
          A return answers what did they buy here, never what is on the shelf. Stock and the general ledger follow the
          customer's paper.
        </p>
      </div>
    </div>
  `;
}

function renderLinesStep(flow: ReturnsFlow): unknown {
  const customer = flow.customer;
  const shown = flow.visibleOrders;
  return keyed(
    flow.epoch,
    html`
      <div class="lines-step" data-testid="returns-lines">
        <div class="stack">
          <div class="rt-card customer-card">
            <div>
              <p class="customer-name" data-testid="returns-customer-name">${customer?.name ?? ""}</p>
              ${flow.isWalkIn
                ? html`<p class="customer-meta" data-testid="returns-walkin-meta">Found by its receipt or order number. Goes back to cash or card only.</p>`
                : html`<p class="customer-meta">
                    ${customer?.account_no ?? ""} · ${customer === null ? "" : typeWords(customer.customer_type)}${customer?.open_balance_cents === undefined
                      ? nothing
                      : html` · Outstanding ${formatMoney(customer.open_balance_cents)}`}
                  </p>`}
            </div>
            <button type="button" class="btn-secondary" data-action="returns-another">Change customer</button>
          </div>
          ${flow.draftNotice === null
            ? nothing
            : html`<p class="note-stop" role="alert" data-testid="returns-draft-notice">${flow.draftNotice}</p>`}
          ${flow.orders.length === 0
            ? html`<p class="notice" role="status" data-testid="returns-no-orders">
                This customer holds no sales order here: there is nothing to return.
              </p>`
            : html`
                <div class="filter-row">
                  <input
                    type="search"
                    data-field="returns-filter"
                    aria-label="Filter by sales order number"
                    placeholder="Filter by sales order number"
                    data-value=${flow.filter}
                  />
                </div>
                ${flow.nothingOpen
                  ? html`<p class="notice" role="status" data-testid="returns-nothing-open">
                      Nothing is still returnable for this customer: every line has gone back already.
                    </p>`
                  : nothing}
                ${shown.length === 0
                  ? html`<p class="notice" role="status">No order matches that number.</p>`
                  : shown.map((order) => renderOrder(flow, order))}
              `}
        </div>
        ${renderCredit(flow)}
      </div>
    `,
  );
}

function typeWords(type: string): string {
  return type.charAt(0) + type.slice(1).toLowerCase();
}

function renderOrder(flow: ReturnsFlow, order: CustomerOrder): TemplateResult {
  const open = order.lines.filter((line) => line.qtyOpen > 0).length;
  return html`
    <section class="rt-card" data-testid="returns-order" data-order=${order.orderNo}>
      <div class="order-head">
        <span>${order.orderNo}${order.jobLabel === "" ? "" : html` · job ${order.jobLabel}`}${order.branchName === "" ? "" : html` · ${order.branchName}`}</span>
        <span class="open-count">${open} of ${order.lines.length} ${order.lines.length === 1 ? "line" : "lines"} open to return</span>
      </div>
      ${order.returnable
        ? nothing
        : html`<p class="order-blocked" role="status" data-testid="returns-order-blocked">${order.notReturnableReason}</p>`}
      <ul class="line-list" aria-label="Lines of ${order.orderNo}">
        ${order.lines.map((line) => renderLine(flow, order, line))}
      </ul>
    </section>
  `;
}

function renderLine(flow: ReturnsFlow, order: CustomerOrder, line: CustomerLine): TemplateResult {
  const qty = flow.draft.qtyOf(line.orderLineId);
  const chosen = flow.draft.dispositionOf(line.orderLineId);
  const exhausted = line.qtyOpen <= 0 || !order.returnable;
  return html`
    <li class="line" data-testid="returns-line" data-line=${line.orderLineId} data-picked=${qty > 0 ? "true" : "false"}>
      <div class="line-top">
        <div>
          <p class="line-desc">${line.description}</p>
          <p class="line-sku">${line.sku}</p>
        </div>
        <div>
          <p class="line-bought">
            bought ${shownQty(line.qtySold)} at ${formatMoney(line.unitPriceCents)} · ${shownQty(line.qtyOpen)} open
          </p>
          ${exhausted
            ? order.returnable
            ? html`<p class="notice">Every unit has gone back already.</p>`
            : nothing
            : html`
                <span class="qty" role="group" aria-label="Quantity to return: ${line.description} on ${order.orderNo}">
                  <button
                    type="button"
                    class="btn-secondary"
                    data-action="returns-qty-down"
                    data-line=${line.orderLineId}
                    aria-label="One fewer: ${line.description} on ${order.orderNo}"
                    ?disabled=${qty === 0}
                  >
                    -
                  </button>
                  <input
                    type="number"
                    min="0"
                    step="any"
                    inputmode="decimal"
                    data-field="returns-qty"
                    data-line=${line.orderLineId}
                    aria-label="Return quantity: ${line.description} on ${order.orderNo}"
                    data-value=${qty === 0 ? "" : String(qty)}
                  />
                  <button
                    type="button"
                    class="btn-secondary"
                    data-action="returns-qty-up"
                    data-line=${line.orderLineId}
                    aria-label="One more: ${line.description} on ${order.orderNo}"
                    ?disabled=${qty >= line.qtyOpen}
                  >
                    +
                  </button>
                </span>
              `}
        </div>
      </div>
      ${exhausted
        ? nothing
        : html`
            <div class="dispositions" role="group" aria-label="What happens to ${line.description} on ${order.orderNo}">
              ${DISPOSITIONS.map((disposition: Disposition) => html`
                <button
                  type="button"
                  class="btn-secondary"
                  data-action="returns-disposition"
                  data-line=${line.orderLineId}
                  data-disposition=${disposition}
                  aria-pressed=${chosen === disposition ? "true" : "false"}
                >
                  ${DISPOSITION_WORDS[disposition]}
                </button>
              `)}
            </div>
            ${chosen === "MANUFACTURER_DEFECT" ? renderDefectSentence(flow, line, qty) : nothing}
            ${qty > 0 && chosen === null
              ? html`<p class="notice" data-testid="returns-line-choose">Choose what happens below.</p>`
              : nothing}
          `}
    </li>
  `;
}

/**
 * The one line that says what the picked manufacturer defect will do (walk change 6 item 4, boards
 * ct-return-defect-over and ct-return-defect-under): "Over the $50 claim threshold ($56.80 at cost):
 * this goes to a warranty claim." or "Under the $50 claim threshold ($12.30 at cost): logged as
 * shrinkage." The cost is the line's picked quantity x unit cost, in integer cents; the threshold
 * is the dealer's live warranty claim threshold, the figure Manager Home and the till agree on by
 * reading the same row. A line whose unit cost has not yet arrived on the wire answers in quiet
 * words instead of guessing at zero.
 */
function renderDefectSentence(flow: ReturnsFlow, line: CustomerLine, qty: number): TemplateResult {
  const threshold = flow.claimThresholdCents;
  if (threshold === null || threshold <= 0) {
    return html`<p
      class="defect-sentence"
      data-testid="returns-defect-unreadable"
      data-line=${line.orderLineId}
    >
      Manufacturer defect: the dealer's claim threshold has not loaded yet; sign out and back in to read it.
    </p>`;
  }
  if (line.unitCostCents === null) {
    return html`<p
      class="defect-sentence"
      data-testid="returns-defect-unreadable"
      data-line=${line.orderLineId}
    >
      Manufacturer defect: this line has no unit cost on it yet, so the threshold cannot be sized; pick another disposition.
    </p>`;
  }
  const cost = defectCostCents(qty, line.unitCostCents);
  const sentence: DefectSentence | null = defectThresholdSentence(cost, threshold);
  if (sentence === null) {
    return html`<p
      class="defect-sentence"
      data-testid="returns-defect-unreadable"
      data-line=${line.orderLineId}
    >
      Manufacturer defect: the claim threshold could not be compared; check the dealer's warranty setting.
    </p>`;
  }
  return html`<p
    class="defect-sentence"
    data-testid="returns-defect-sentence"
    data-line=${line.orderLineId}
    data-kind=${sentence.kind}
    data-cost-cents=${sentence.costCents}
    data-threshold-cents=${sentence.thresholdCents}
  >
    ${sentence.words}
  </p>`;
}

function shownQty(value: number): string {
  return String(Number(value.toFixed(4)));
}

function creditFigure(flow: ReturnsFlow): TemplateResult {
  const preview = flow.preview;
  if (preview !== null) {
    return html`
      <p class="credit-total" data-testid="returns-credit-total">${formatMoney(preview.totalCents)}</p>
      <p class="notice" data-testid="returns-credit-tax">
        with tax: ${formatMoney(preview.subtotalCents)} before tax, ${formatMoney(preview.taxCents)} tax
      </p>
      ${preview.orders
        .filter((order) => order.railNote !== "")
        .map((order) => html`<p class="notice" role="status" data-testid="returns-split-note"><strong>${order.orderNo}:</strong> ${order.railNote}.</p>`)}
      ${preview.invoiceOffsetCents !== null && preview.invoiceOffsetCents > 0
        ? html`<p class="figures" data-testid="returns-split-figures">
            Goes back ${flow.method === "CARD" ? "to the card" : "in cash"}: ${formatMoney(preview.railCents ?? 0)} ·
            comes off the open invoice: ${formatMoney(preview.invoiceOffsetCents)}
          </p>`
        : nothing}
    `;
  }
  if (flow.previewing) return html`<p class="notice" role="status" data-testid="returns-credit-pricing">Pricing the credit.</p>`;
  if (flow.previewNotice !== null) return html`<p class="note-stop" role="alert" data-testid="returns-preview-notice">${flow.previewNotice}</p>`;
  return html``;
}

function renderCredit(flow: ReturnsFlow): TemplateResult {
  const picked = flow.draft.pickedLines();
  const missing = flow.draft.missingDispositions;
  return html`
    <aside class="rt-card rt-credit" data-testid="returns-credit" aria-label="The credit so far">
      <h3>The credit so far</h3>
      ${picked.length === 0
        ? html`<p class="notice" data-testid="returns-credit-empty">Pick a line to start the credit note.</p>`
        : html`
            ${creditFigure(flow)}
            <p class="credit-count" data-testid="returns-credit-count">
              ${picked.length} ${picked.length === 1 ? "line" : "lines"} from ${flow.draft.orderCount}
              ${flow.draft.orderCount === 1 ? "order" : "orders"}
            </p>
            <ul class="credit-lines" aria-label="Lines on the credit note">
              ${picked.map((pick) => {
                const priced = flow.preview?.lines.get(pick.orderLineId);
                return html`
                  <li data-testid="returns-credit-line">
                    ${shownQty(pick.qty)} ${pick.description} on ${pick.orderNo} ·
                    ${pick.disposition === null ? "choose what happens" : DISPOSITION_WORDS[pick.disposition]}
                    ${priced === undefined
                      ? nothing
                      : html`<span class="server-figure" data-testid="returns-credit-line-figure">${formatMoney(priced.totalCents)} with ${formatMoney(priced.taxCents)} tax</span>`}
                  </li>
                `;
              })}
            </ul>
          `}
      ${missing > 0
        ? html`<p class="notice" data-testid="returns-missing">
            ${missing === 1 ? "One line still needs" : `${missing} lines still need`} a choice: return to stock, damaged, or nothing returned.
          </p>`
        : nothing}
      <button
        type="button"
        class="btn-primary btn-wide"
        data-action="returns-continue"
        ?disabled=${!flow.canContinue}
      >
        Continue to the refund
      </button>
    </aside>
  `;
}

const RAIL_LABELS: Record<RefundMethod, string> = {
  CARD: "Card",
  CASH: "Cash",
  ACCOUNT_CREDIT: "On the account",
};

function railWords(method: RefundMethod): string {
  if (method === "CASH") return "cash, from the drawer";
  if (method === "CARD") return "card";
  return "on the customer's account";
}

function drawerWords(flow: ReturnsFlow): string {
  if (flow.drawer.kind === "closed") return "no drawer is open at this branch.";
  return "the drawer could not be asked just now.";
}

function renderRefundStep(flow: ReturnsFlow): TemplateResult {
  const picked = flow.draft.pickedLines();
  const frozen = flow.submitting || flow.dialogOpen || flow.approving;
  const orders = flow.draft.pickedOrders();
  return html`
    <div class="refund-step" data-testid="returns-refund" data-walkin=${flow.isWalkIn ? "true" : "false"}>
      <div class="rt-card">
        <h3>The credit</h3>
        <p>
          ${flow.customer?.name ?? "This customer"}: ${picked.length} ${picked.length === 1 ? "line" : "lines"} from
          ${flow.draft.orderCount} ${flow.draft.orderCount === 1 ? "order" : "orders"} on one credit note.
        </p>
        <ul class="refund-orders" aria-label="Orders on the credit note">
          ${orders.map(
            (order) => html`
              <li data-testid="returns-refund-order">
                ${order.orderNo}${order.jobLabel === "" ? "" : html` · job ${order.jobLabel}`}:
                ${picked
                  .filter((pick) => pick.orderId === order.orderId)
                  .map((pick, index) => html`${index > 0 ? "; " : ""}<span>${shownQty(pick.qty)} ${pick.description} · ${pick.disposition === null ? "" : DISPOSITION_WORDS[pick.disposition]}</span>`)}
              </li>
            `,
          )}
        </ul>
        ${creditFigure(flow)}
        <p class="hint">
          Damaged lines never touch the shelf; nothing returned is money back against the paper alone.
        </p>
        <button type="button" class="btn-secondary" data-action="returns-back-to-lines" ?disabled=${frozen}>Back to the lines</button>
      </div>
      <div class="rt-card focus">
        <h3>Refund it</h3>
        <div class="methods" role="group" aria-label="Refund rail">
          ${(["CARD", "CASH", "ACCOUNT_CREDIT"] as const).map(
            (method) => html`
              <button
                type="button"
                class="btn-secondary"
                data-action="returns-method"
                data-method=${method}
                aria-pressed=${flow.method === method ? "true" : "false"}
                ?disabled=${!flow.railAvailable(method) || frozen}
              >
                ${RAIL_LABELS[method]}
              </button>
            `,
          )}
        </div>
        ${flow.railAvailable("CARD")
          ? nothing
          : html`<p class="notice">No card refund: an order on this credit note was not paid by card, so there is no capture to reverse.</p>`}
        ${flow.railAvailable("CASH") ? nothing : html`<p class="notice">Cash waits on the drawer: ${drawerWords(flow)}</p>`}
        ${flow.isWalkIn
          ? html`<p class="notice" data-testid="returns-walkin-rails">A walk in sale has no account to credit: it goes back to cash or card.</p>`
          : nothing}
        ${flow.method === "CARD" && flow.cardSimulated === true
          ? html`<p class="notice" role="status" data-testid="simulated-notice">Simulated refund: nothing reached a card.</p>`
          : flow.method !== null
            ? html`<p class="notice" role="status">This credit goes back ${railWords(flow.method)}.</p>`
            : nothing}
        <p class="hint">No restocking fee is taken on a credit note.</p>
        ${flow.notice === null ? nothing : html`<p class="note-stop" role="alert" data-testid="returns-notice">${flow.notice}</p>`}
        <div class="refund-actions">
          <button type="button" class="btn-primary" data-action="returns-submit" ?disabled=${!flow.canSubmit}>
            Record the credit note
          </button>
        </div>
      </div>
      ${flow.isWalkIn ? nothing : renderSwapPanel(flow)}
      <div class="refund-wide">
        ${flow.refusal === null ? nothing : renderRefusal(flow.refusal)}
        ${flow.dialogOpen ? renderApprovalDialog(flow) : nothing}
      </div>
    </div>
  `;
}

function renderSwapPanel(flow: ReturnsFlow): TemplateResult {
  const frozen = flow.submitting || flow.swapping || flow.dialogOpen || flow.approving;
  const preview = flow.swapPreview;
  const lines = flow.basket.lines;
  return html`
    <div class="rt-card swap" data-testid="returns-swap">
      <h3>The swap: the credit pays a new order</h3>
      <p class="hint">Replacement goods, rung now as a sales order. The credit pays for them; what is left goes back, and what is short is paid.</p>
      <input
        type="search"
        class="search-input"
        data-field="returns-product"
        aria-label="Search replacement goods"
        placeholder="Search replacement goods..."
        autocomplete="off"
        ?disabled=${frozen}
        data-value=${flow.productTerm}
      />
      ${flow.productMatches.length > 0
        ? html`<ul class="matches" aria-label="Products" data-testid="returns-products">
            ${flow.productMatches.map(
              (row) => html`
                <li>
                  <button type="button" class="btn-secondary" data-action="returns-add-replacement" data-product=${row.productId} ?disabled=${frozen}>
                    <span>${row.name}</span><span>per ${row.soldBy}</span>
                  </button>
                </li>
              `,
            )}
          </ul>`
        : flow.productSearched && !flow.productSearching
          ? html`<p class="notice" role="status">No product answers to that.</p>`
          : nothing}
      ${lines.length === 0
        ? html`<p class="notice" data-testid="returns-swap-empty">Add the goods going out to see what the credit covers.</p>`
        : html`<ul class="swap-lines" aria-label="Replacement goods" data-testid="returns-replacements">
            ${lines.map(
              (line) => html`
                <li data-testid="returns-replacement">
                  <span>${line.name} (${line.soldBy})</span>
                  <span class="qty" role="group" aria-label="Replacement quantity: ${line.name}">
                    <button type="button" class="btn-secondary" data-action="returns-replacement-down" data-product=${line.productId} aria-label="One fewer: ${line.name}" ?disabled=${frozen}>-</button>
                    <input type="number" min="0" step="any" inputmode="decimal" data-field="returns-replacement-qty" data-product=${line.productId} aria-label="Replacement quantity: ${line.name}" data-value=${String(line.qty)} ?disabled=${frozen} />
                    <button type="button" class="btn-secondary" data-action="returns-replacement-up" data-product=${line.productId} aria-label="One more: ${line.name}" ?disabled=${frozen}>+</button>
                  </span>
                </li>
              `,
            )}
          </ul>`}
      ${preview === null
        ? flow.swapPreviewing
          ? html`<p class="notice" role="status" data-testid="returns-swap-pricing">Pricing the swap.</p>`
          : flow.swapPreviewNotice !== null
            ? html`<p class="note-stop" role="alert" data-testid="returns-swap-preview-notice">${flow.swapPreviewNotice}</p>`
            : nothing
        : html`
            <div class="swap-figures" data-testid="returns-swap-figures">
              <div><span>The credit</span><span data-testid="swap-credit">${formatMoney(preview.creditCents)}</span></div>
              <div class="strong"><span>New order total, with tax</span><span data-testid="swap-new-total">${formatMoney(preview.newOrderCents)}</span></div>
              <div><span>The credit pays</span><span data-testid="swap-applied">${formatMoney(preview.appliedCents)}</span></div>
              ${preview.leftoverCents > 0
                ? html`<div><span>Left over, back ${railWords(preview.leftoverMethod)}</span><span data-testid="swap-leftover">${formatMoney(preview.leftoverCents)}</span></div>`
                : nothing}
              ${preview.shortfallCents > 0
                ? html`<div><span>Still to pay</span><span data-testid="swap-shortfall">${formatMoney(preview.shortfallCents)}</span></div>`
                : nothing}
            </div>
            <p class="notice" data-testid="swap-words">${swapWords(preview.leftoverCents, preview.shortfallCents, preview.leftoverMethod)}</p>
          `}
      ${preview !== null && preview.shortfallCents > 0
        ? html`<div class="methods" role="group" aria-label="Shortfall tender">
            ${(["CASH", "ACCOUNT", "CARD"] as const)
              .filter((tender) => tender !== "CARD" || flow.cardShortfallAvailable)
              .map(
                (tender) => html`
                  <button type="button" class="btn-secondary" data-action="returns-swap-tender" data-tender=${tender} aria-pressed=${flow.swapTender === tender ? "true" : "false"} ?disabled=${frozen}>
                    ${tender === "CASH" ? "Pay the rest in cash" : tender === "ACCOUNT" ? "Put the rest on the account" : "Pay the rest by card"}
                  </button>
                `,
              )}
          </div>`
        : nothing}
      ${flow.swapNotice === null ? nothing : html`<p class="note-stop" role="alert" data-testid="returns-swap-notice">${flow.swapNotice}</p>`}
      <button type="button" class="btn-primary btn-wide" data-action="returns-ring-swap" ?disabled=${!flow.canRing}>Ring the swap</button>
      <p class="hint">After: the credit note and the new order point at each other, the restocked lines are back on the shelf, and the damaged lines are written off.</p>
    </div>
  `;
}

function swapWords(leftover: number, shortfall: number, method: RefundMethod): string {
  if (leftover > 0) return `The credit covers it whole; the ${formatMoney(leftover)} left over goes back ${railWords(method)}.`;
  if (shortfall > 0) return "The credit pays part of it; the rest is paid by the tender chosen below.";
  return "The credit covers the new order exactly.";
}

function renderRefusal(approval: PosRefundApproval): TemplateResult {
  // The roles named the way the refusal speaks them: the Sales manager, or the Branch manager.
  const named = approverLabels(approval.approvers).map((role) => `the ${role}`);
  const roles = named.length === 1 ? named[0] : `${named.slice(0, -1).join(", ")}, or ${named[named.length - 1]}`;
  return html`
    <section class="refusal" data-testid="refund-refusal" role="alert">
      <h3>Refused</h3>
      <p>This credit note is over the dealer's approval amount of ${formatMoney(approval.amount_cents)}, so this till cannot commit it as rung.</p>
      <p>It can be committed by ${roles}: a manager whose role holds the approval.</p>
      <button type="button" class="btn-primary" data-action="returns-approve">Manager approval</button>
    </section>
  `;
}

function renderApprovalDialog(flow: ReturnsFlow): TemplateResult {
  return html`
    <section class="dialog" role="dialog" aria-modal="true" aria-label="Manager approval" data-testid="approval-dialog">
      <h3>Manager approval</h3>
      <p>The manager taps their own card here: the till stays the clerk's, and the credit note is recorded as the manager's decision with the clerk named beside.</p>
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
                      class="btn-secondary approver-card"
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
      <button type="button" class="btn-secondary" data-action="returns-approve-cancel" ?disabled=${flow.approving}>Cancel</button>
    </section>
  `;
}

function renderDoneStep(flow: ReturnsFlow): TemplateResult {
  const done = flow.done;
  if (done === null) return html``;
  const note = done.note;
  const swap = flow.doneSwap;
  return html`
    <div class="customer-step">
      <section class="rt-card focus" data-testid="returns-done" role="status">
        <span class="eyebrow">Done</span>
        <h3>${swap === null ? "Credit note recorded" : "Swap rung: credit note recorded"}</h3>
        <p data-testid="returns-note-no">${note.noteNo}</p>
        ${swap === null
          ? nothing
          : html`
              <p class="figures" data-testid="returns-swap-sale">
                New order ${swap.sale.orderNo} · invoice ${swap.sale.invoiceNo} · ${formatMoney(swap.sale.totalCents)} with tax
              </p>
              <p data-testid="returns-swap-link">
                The credit note ${note.noteNo} and the new order ${swap.sale.orderNo}, one swap, point at each other.
              </p>
              <p class="notice" data-testid="returns-swap-split">
                The credit ${formatMoney(swap.creditCents)} paid ${formatMoney(swap.appliedCents)}${swap.leftoverCents > 0 ? `; ${formatMoney(swap.leftoverCents)} went back ${railWords(swap.leftoverMethod)}` : ""}${swap.shortfallCents > 0 ? `; ${formatMoney(swap.shortfallCents)} was paid by ${swap.sale.tender.toLowerCase()}` : ""}.
              </p>
              <p class="receipt-line" data-testid="returns-receipt-line-sale">
                ${swap.sale.orderNo} · ${swap.sale.invoiceNo} · ${formatMoney(swap.sale.totalCents)} · new order, paid by credit note ${note.noteNo}
              </p>
            `}
        ${note.replayed ? html`<p class="notice">The same credit note, answered again: nothing was posted twice.</p>` : nothing}
        <p class="figures" data-testid="returns-note-total">
          Total credited ${formatMoney(note.totalCents)} ${railWords(note.method)}
        </p>
        <p class="notice">${formatMoney(note.subtotalCents)} before tax, ${formatMoney(note.taxCents)} tax.</p>
        ${note.method === "CARD" && done.cardSimulated === true
          ? html`<p data-testid="simulated-notice">Simulated refund: nothing reached a card.</p>`
          : nothing}
        <ul class="refund-orders" aria-label="Returns on the credit note">
          ${note.returns.map((child) => {
            const orderNo = done.orderNos[child.orderId] ?? "";
            return html`
              <li class="done-return" data-testid="returns-done-return">
                <p><strong>${orderNo}</strong> · return ${child.returnNo}</p>
                <p class="figures">
                  ${formatMoney(child.totalCents)} (${formatMoney(child.subtotalCents)} before tax, ${formatMoney(child.taxCents)} tax)
                </p>
                ${child.paymentNote === ""
                  ? nothing
                  : html`<p class="notice" data-testid="returns-done-payment-note">${child.paymentNote}. ${formatMoney(child.invoiceOffsetCents)} came off the open invoice.</p>`}
                <p class="receipt-line" data-testid="returns-receipt-line">
                  ${child.returnNo} · ${orderNo} · ${formatMoney(child.totalCents)} · ${railWords(child.method)}
                </p>
              </li>
            `;
          })}
        </ul>
        ${done.approverName === null ? nothing : html`<p>Approved by ${done.approverName}.</p>`}
        ${done.clerkName === null ? nothing : html`<p>Rung by ${done.clerkName}.</p>`}
        <button type="button" class="btn-secondary" data-action="returns-another">Start another return</button>
      </section>
    </div>
  `;
}
