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
import type { CustomerLine, CustomerOrder } from "../returns/lines.js";

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
  .returns .credit-count {
    font-weight: 600;
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
  return `${name}: the refund.`;
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
          A walk-in sale is not returned from here.
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
              <p class="customer-meta">
                ${customer?.account_no ?? ""} · ${customer === null ? "" : typeWords(customer.customer_type)} · Outstanding
                ${customer === null ? "" : formatMoney(customer.open_balance_cents)}
              </p>
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
                    .value=${flow.filter}
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
        <span>${order.orderNo}${order.jobLabel === "" ? "" : html` · job ${order.jobLabel}`}</span>
        <span class="open-count">${open} of ${order.lines.length} ${order.lines.length === 1 ? "line" : "lines"} open to return</span>
      </div>
      <ul class="line-list" aria-label="Lines of ${order.orderNo}">
        ${order.lines.map((line) => renderLine(flow, order, line))}
      </ul>
    </section>
  `;
}

function renderLine(flow: ReturnsFlow, order: CustomerOrder, line: CustomerLine): TemplateResult {
  const qty = flow.draft.qtyOf(line.orderLineId);
  const chosen = flow.draft.dispositionOf(line.orderLineId);
  const exhausted = line.qtyOpen <= 0;
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
            ? html`<p class="notice">Every unit has gone back already.</p>`
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
                    max=${line.qtyOpen}
                    step="any"
                    inputmode="decimal"
                    data-field="returns-qty"
                    data-line=${line.orderLineId}
                    aria-label="Return quantity: ${line.description} on ${order.orderNo}"
                    .value=${qty === 0 ? "" : String(qty)}
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
            ${qty > 0 && chosen === null
              ? html`<p class="notice" data-testid="returns-line-choose">Choose what happens below.</p>`
              : nothing}
          `}
    </li>
  `;
}

function shownQty(value: number): string {
  return String(Number(value.toFixed(4)));
}

function renderCredit(flow: ReturnsFlow): TemplateResult {
  const picked = flow.draft.pickedLines();
  const missing = flow.draft.missingDispositions;
  const lineById = new Map<string, CustomerLine>();
  for (const order of flow.orders) for (const line of order.lines) lineById.set(line.orderLineId, line);
  return html`
    <aside class="rt-card rt-credit" data-testid="returns-credit" aria-label="The credit so far">
      <h3>The credit so far</h3>
      ${picked.length === 0
        ? html`<p class="notice" data-testid="returns-credit-empty">Pick a line to start the credit note.</p>`
        : html`
            <p class="credit-count" data-testid="returns-credit-count">
              ${picked.length} ${picked.length === 1 ? "line" : "lines"} from ${flow.draft.orderCount}
              ${flow.draft.orderCount === 1 ? "order" : "orders"}
            </p>
            <ul class="credit-lines" aria-label="Lines on the credit note">
              ${picked.map((pick) => {
                const line = lineById.get(pick.orderLineId);
                return html`
                  <li data-testid="returns-credit-line">
                    ${shownQty(pick.qty)} ${pick.description} on ${pick.orderNo} ·
                    ${pick.disposition === null ? "choose what happens" : DISPOSITION_WORDS[pick.disposition]}
                    ${line === undefined
                      ? nothing
                      : html`<span class="server-figure">${formatMoney(line.unitPriceCents)} each on the order, line ${formatMoney(line.lineTotalCents)} plus ${formatMoney(line.taxCents)} tax</span>`}
                  </li>
                `;
              })}
            </ul>
            <p class="notice" data-testid="returns-credit-pending">
              The credit's own total is the server's figure, with tax: it is priced when the refund is drafted. The figures
              above are what the order says, not the credit.
            </p>
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

function renderRefundStep(flow: ReturnsFlow): TemplateResult {
  const picked = flow.draft.pickedLines();
  return html`
    <div class="customer-step">
      <div class="rt-card focus" data-testid="returns-refund-wait">
        <span class="eyebrow">Refund</span>
        <p>
          ${picked.length} ${picked.length === 1 ? "line" : "lines"} from ${flow.draft.orderCount}
          ${flow.draft.orderCount === 1 ? "order" : "orders"} are drafted for ${flow.customer?.name ?? "this customer"}.
        </p>
        <p class="hint">
          The refund rails (card, on the account where it can spend credit, cash) and the swap arrive with the next step of
          this build. Nothing has been returned.
        </p>
        <button type="button" class="btn-secondary" data-action="returns-back-to-lines">Back to the lines</button>
      </div>
    </div>
  `;
}
