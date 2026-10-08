// The held order card (item 9.8, board 03): how a Cash On Demand order held from picking reads.
// One card for both places that show one: the Summary after a pay before release ring with a
// balance, and the Pay balance surface's order card. The card holds no money rule: the chips and
// the title are the server's words, the figures are the server's cents through the one formatter.
import { css, html, nothing, type TemplateResult } from "lit";
import { hhAddress } from "@hardscapehouse/design-system/src/address.js";
import { formatMoney } from "../money.js";
import { HOLD_PANEL_LINE, HOLD_TITLE_FALLBACK, type HeldOrderCard } from "../pay-balance/hold.js";

export const heldOrderStyles = css`
  .held-order {
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-card);
    padding: var(--hh-s-16, 16px);
    margin: var(--hh-s-16, 16px) 0 0;
    min-width: 0;
  }
  .held-order h3 {
    margin: 0 0 4px;
    font-size: 15px;
    overflow-wrap: anywhere;
  }
  .held-order .held-sub {
    margin: 0 0 var(--hh-s-12, 12px);
    font-size: 12px;
    color: var(--hos-text-dim);
    overflow-wrap: anywhere;
  }
  .held-order .held-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin: 0 0 var(--hh-s-12, 12px);
  }
  .held-order .held-chip {
    font-size: 12px;
    font-weight: 600;
    padding: 4px 12px;
    border: 1px solid var(--hos-line);
    border-radius: 999px;
    overflow-wrap: anywhere;
  }
  .held-order .held-chip.hold {
    border-color: var(--hos-safety-red);
    color: var(--hos-safety-red);
  }
  .held-order .held-panel {
    padding: var(--hh-s-12, 12px) var(--hh-s-16, 16px);
    margin: 0 0 var(--hh-s-12, 12px);
    border-radius: var(--hos-radius-control);
    background: color-mix(in srgb, var(--hos-stone-amber) 18%, transparent);
  }
  .held-order .held-title {
    margin: 0 0 4px;
    font-weight: 600;
    overflow-wrap: anywhere;
  }
  .held-order .held-line {
    margin: 0;
    font-size: 12px;
    color: var(--hos-text-dim);
    overflow-wrap: anywhere;
  }
  .held-order .held-fig {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding: 8px 12px;
    margin: 0 0 8px;
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-control);
  }
  .held-order .held-fig .amount {
    font-variant-numeric: tabular-nums;
    font-weight: 600;
    white-space: nowrap;
  }
  .held-order .held-fig.due {
    background: color-mix(in srgb, var(--hos-stone-amber) 18%, transparent);
    border-color: var(--hos-stone-amber);
    font-weight: 600;
  }
  .held-order .held-lines {
    list-style: none;
    margin: 4px 0 var(--hh-s-12, 12px);
    padding: 0;
  }
  .held-order .held-lines li {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    padding: 8px 12px;
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-control);
    margin: 0 0 6px;
    overflow-wrap: anywhere;
  }
  .held-order .held-eyebrow {
    margin: 12px 0 4px;
    font-size: 12px;
    font-weight: 600;
  }
  .held-order .held-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: var(--hh-s-12, 12px);
  }
  .held-order .held-actions a,
  .held-order .held-actions button {
    min-width: 0;
  }
  .held-order .held-actions a {
    display: inline-flex;
    align-items: center;
    text-decoration: none;
    font-family: inherit;
    font-size: 13px;
    font-weight: 600;
    padding: 10px 16px;
    color: #0b1725;
    background: var(--hos-stone-amber);
    border: 1px solid var(--hos-stone-amber);
    border-radius: var(--hos-radius-control);
  }
  .held-order .held-actions button {
    font-family: inherit;
    font-size: 13px;
    padding: 10px 16px;
    color: var(--hos-text);
    background: transparent;
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-control);
    cursor: pointer;
  }
`;

/**
 * The card. `payBalanceHref` is the Pay balance action (an anchor to the surface's own address:
 * no script writes the address); null on the Pay balance surface itself. `printButton` is the
 * caller's own Print hold slip button, because the two hosts bind their actions differently
 * (the terminal binds its own; the app root's surfaces use host delegation).
 */
export function renderHeldOrder(
  card: HeldOrderCard,
  actions: { payBalanceHref: string | null; printButton: TemplateResult; heading?: boolean; figures?: boolean },
): TemplateResult {
  return html`
    <section class="held-order" data-testid="held-order" aria-label="Held from picking">
      ${actions.heading === false
        ? nothing
        : html`<h3>Sales order ${card.orderNo}</h3>
            <p class="held-sub">${[card.customerName, card.accountWords].filter((p) => p !== null && p !== "").join(" · ")}</p>`}
      <div class="held-chips">
        ${card.inProcessWords === null
          ? nothing
          : html`<span class="held-chip" data-testid="held-chip-process">${card.inProcessWords}</span>`}
        <span class="held-chip hold" data-testid="held-chip-hold">Held from picking</span>
      </div>
      <div class="held-panel">
        <p class="held-title" data-testid="held-title">${card.holdWords === "" ? HOLD_TITLE_FALLBACK : card.holdWords}</p>
        <p class="held-line" data-testid="held-line">${HOLD_PANEL_LINE}</p>
      </div>
      ${actions.figures === false
        ? nothing
        : html`      <div class="held-fig"><span>Order total</span><span class="amount">${formatMoney(card.orderTotalCents)}</span></div>
      ${card.deposit === null
        ? nothing
        : html`<div class="held-fig">
            <span>Deposit paid, ${card.deposit.methodWord}</span><span class="amount">${formatMoney(card.deposit.cents)}</span>
          </div>`}
      <div class="held-fig due"><span>Balance due</span><span class="amount">${formatMoney(card.balanceDueCents)}</span></div>
      ${card.lines.length === 0
        ? nothing
        : html`<p class="held-eyebrow">On the order</p>
            <ul class="held-lines">
              ${card.lines.map(
                (l) => html`<li>
                  <span>${l.description} · ${l.qty}</span>
                  <span class="amount">${l.lineTotalCents === null ? "" : formatMoney(l.lineTotalCents)}</span>
                </li>`,
              )}
            </ul>`}
`}
      <div class="held-actions">
        ${actions.payBalanceHref === null
          ? nothing
          : html`<a
              class="btn-primary"
              href=${hhAddress(actions.payBalanceHref)}
              data-testid="held-pay-balance"
              >Pay balance</a
            >`}
        ${actions.printButton}
      </div>
    </section>
  `;
}
