// The tender and the receipt: how a sale settles and what the customer walks
// away with. The card tender sends the fixed demo token and nothing else: no
// card number field exists in this app and no tokenizing script is loaded, so
// the second source scan has nothing to find. The simulated notice is value
// driven: it renders exactly when the status route's simulated answered true,
// on the tender and again on the receipt, and never when it answered false or
// did not answer. The change due is the core's figure, read off the committed
// sale; this screen computes no money of its own.
import { css, html, nothing, type TemplateResult } from "lit";

export type TenderChoice = "CASH" | "CARD" | "ACCOUNT";

export interface TenderPanel {
  tender: TenderChoice;
  /** The status route's answer: true shows the notice, false and unknown show none. */
  simulated: boolean | null;
  lineCount: number;
  /** True when every line on the ticket carries the engine's price. */
  priced: boolean;
  /** True when an account is picked: the walk-in has no account to bill. */
  canBillAccount: boolean;
  busy: boolean;
  message: string | null;
}

export interface ReceiptTender {
  method: string;
  amount: string;
}

export interface ReceiptView {
  orderId: string;
  orderNo: string;
  invoiceNo: string;
  subtotal: string;
  tax: string;
  total: string;
  tenders: ReceiptTender[];
  /** The cash rows, present only on a cash ring. */
  tendered: string | null;
  changeDue: string | null;
  simulated: boolean | null;
  /** The account sale's after-state: the invoice and the fresh balance. */
  account: { name: string; accountNo: string; balance: string | null } | null;
}

export const checkoutStyles = css`
  .tender-choices {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-8);
    margin: 0 0 var(--hh-s-12);
  }
  .tender-choices button[aria-pressed="true"] {
    border-color: var(--hh-accent);
    font-weight: 700;
  }
  .tender-cash label {
    display: block;
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
    margin: 0 0 var(--hh-s-4);
  }
  .tender-cash input {
    min-height: 44px;
    width: 140px;
    background: var(--hh-page);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    padding: 0 var(--hh-s-12);
    font-family: inherit;
    font-size: var(--hh-s-16);
  }
  .tender-cash input:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .simulated-note {
    border: 1px solid var(--hh-state-watch);
    border-radius: var(--hh-radius-control);
    color: var(--hh-state-watch);
    padding: var(--hh-s-8) var(--hh-s-12);
    margin: 0 0 var(--hh-s-12);
  }
  .receipt-rows {
    list-style: none;
    margin: 0 0 var(--hh-s-12);
    padding: 0;
    max-width: 420px;
  }
  .receipt-rows li {
    display: flex;
    justify-content: space-between;
    gap: var(--hh-s-12);
    padding: var(--hh-s-4) 0;
    border-bottom: 1px solid var(--hh-line);
  }
  .receipt-rows li strong {
    font-weight: 700;
  }
  .receipt-total-row strong {
    font-size: var(--hh-s-16);
  }
`;

export function renderTender(panel: TenderPanel): TemplateResult {
  const choice = (value: TenderChoice, label: string, disabled: boolean) => html`
    <button
      type="button"
      class="btn-secondary"
      data-action="set-tender"
      data-tender=${value}
      aria-pressed=${panel.tender === value ? "true" : "false"}
      ?disabled=${disabled || panel.busy}
    >
      ${label}
    </button>
  `;
  return html`
    <section class="panel" aria-label="Tender">
      <h3>Tender</h3>
      <div class="tender-choices">
        ${choice("CASH", "Cash", false)}
        ${choice("CARD", "Card", false)}
        ${choice("ACCOUNT", "Account", !panel.canBillAccount)}
      </div>
      ${panel.simulated === true && panel.tender === "CARD"
        ? html`<p class="simulated-note" role="note" data-testid="simulated-notice">
            Simulated card: no card was charged
          </p>`
        : nothing}
      ${panel.tender === "CASH"
        ? html`
            <div class="tender-cash">
              <label for="cash-tendered-input">Cash tendered, in dollars</label>
              <input
                id="cash-tendered-input"
                data-input="cash-tendered"
                type="text"
                inputmode="decimal"
                autocomplete="off"
                placeholder="0.00"
              />
            </div>
          `
        : nothing}
      ${panel.tender === "CARD" && panel.simulated === false
        ? html`<p class="notice" role="note">
            The card rail reports itself live: this box is not the demo's simulator.
          </p>`
        : nothing}
      <div class="ticket-tools">
        <button
          type="button"
          class="btn-primary"
          data-action="commit-sale"
          ?disabled=${panel.lineCount === 0 || !panel.priced || panel.busy}
        >
          Ring the sale
        </button>
      </div>
      ${panel.message === null ? nothing : html`<p class="notice" role="alert" data-testid="tender-refusal">${panel.message}</p>`}
    </section>
  `;
}

export function renderReceipt(view: ReceiptView): TemplateResult {
  return html`
    <section class="panel" aria-label="Receipt" data-testid="receipt" data-order-id=${view.orderId}>
      <h3>Receipt</h3>
      <p>
        Order <strong data-testid="receipt-order-no">${view.orderNo}</strong>,
        invoice <strong data-testid="receipt-invoice-no">${view.invoiceNo}</strong>
      </p>
      ${view.simulated === true
        ? html`<p class="simulated-note" role="note" data-testid="receipt-simulated-notice">
            Simulated card: no card was charged
          </p>`
        : nothing}
      <ul class="receipt-rows">
        <li>
          <span>Subtotal</span>
          <span data-testid="receipt-subtotal">${view.subtotal}</span>
        </li>
        <li>
          <span>Tax</span>
          <span data-testid="receipt-tax">${view.tax}</span>
        </li>
        <li class="receipt-total-row">
          <span>Total</span>
          <strong data-testid="receipt-total">${view.total}</strong>
        </li>
        ${view.tenders.map(
          (tender) => html`
            <li data-testid="receipt-tender">
              <span>Tender: ${tender.method}</span>
              <span>${tender.amount}</span>
            </li>
          `,
        )}
        ${view.tendered === null
          ? nothing
          : html`
              <li>
                <span>Cash tendered</span>
                <span data-testid="receipt-tendered">${view.tendered}</span>
              </li>
            `}
        ${view.changeDue === null
          ? nothing
          : html`
              <li>
                <span>Change due</span>
                <strong data-testid="receipt-change-due">${view.changeDue}</strong>
              </li>
            `}
      </ul>
      ${view.account === null
        ? nothing
        : html`<p data-testid="receipt-account">
            Billed to ${view.account.name} (${view.account.accountNo}):
            the account's balance is now
            ${view.account.balance === null
              ? html`<span class="unpriced">(not answered)</span>`
              : html`<strong>${view.account.balance}</strong>`}
          </p>`}
      <button type="button" class="btn-secondary" data-action="clear-receipt">Clear the receipt</button>
    </section>
  `;
}
