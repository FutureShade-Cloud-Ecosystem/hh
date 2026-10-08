// The Pay balance surface (item 9.7, board 02): find the customer's order, see what was paid and
// what is owed, take the whole rest. This view holds no domain logic and no money of its own: it
// spells the flow's facts through the one money helper and asks the host's actions through data
// attributes, read off composedPath() by the app root (the design system's host delegation rule:
// never @click, never a content listener that a re-render would strand).
import { css, html, nothing, type TemplateResult } from "lit";
import { keyed } from "lit/directives/keyed.js";
import { formatMoney } from "../money.js";
import type { PayBalanceFlow } from "../pay-balance/flow.js";
import { heldCardFromOwing } from "../pay-balance/hold.js";
import { renderHeldOrder, heldOrderStyles } from "./held-order.js";
import {
  fulfilmentWord,
  moneyTrailWords,
  paidInFullWords,
  takeButtonWords,
  methodWord,
  type OwingOrder,
  MORE_ORDERS_WORDS,
} from "../pay-balance/words.js";

export const payBalanceStyles = [heldOrderStyles, css`
  .pay-balance .subtitle {
    color: var(--hos-text-dim);
    margin: 0 0 var(--hh-s-16);
  }
  .pay-balance .eyebrow {
    display: block;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--hos-text-dim);
    margin: 0 0 var(--hh-s-8);
  }
  .pay-balance .find-row {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-16);
  }
  .pay-balance .find-row input {
    flex: 1 1 220px;
    min-width: 0;
    font-family: inherit;
    font-size: 13px;
    color: var(--hos-text);
    background: var(--hos-field);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hos-radius-control);
    padding: var(--hh-s-8) var(--hh-s-12);
    min-height: 44px;
  }
  .pay-balance .find-row input:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .pay-balance .find-hint {
    flex: 1 1 100%;
    margin: 0;
    font-size: 12px;
    color: var(--hos-text-dim);
  }
  .pay-balance .matches {
    list-style: none;
    margin: 0 0 var(--hh-s-16);
    padding: 0;
    display: grid;
    gap: var(--hh-s-8);
  }
  .pay-balance .matches button {
    width: 100%;
    display: flex;
    justify-content: space-between;
    gap: var(--hh-s-12);
    text-align: start;
    min-width: 0;
  }
  .pay-balance .panes {
    display: grid;
    grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
    gap: var(--hh-s-16);
    align-items: start;
  }
  @media (max-width: 760px) {
    .pay-balance .panes {
      grid-template-columns: minmax(0, 1fr);
    }
  }
  .pay-balance .pane {
    min-width: 0;
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-card);
    padding: var(--hh-s-16);
  }
  .pay-balance .customer {
    margin: 0 0 var(--hh-s-8);
    font-size: 15px;
    font-weight: 600;
    overflow-wrap: anywhere;
  }
  .pay-balance .chip {
    display: inline-block;
    max-width: 100%;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: var(--hh-s-4) var(--hh-s-12);
    border: 1px solid var(--hos-stone-amber);
    border-radius: 999px;
    background: color-mix(in srgb, var(--hos-stone-amber) 14%, transparent);
    overflow-wrap: anywhere;
  }
  .pay-balance .jobline {
    margin: var(--hh-s-8) 0 var(--hh-s-12);
    color: var(--hos-text-dim);
    overflow-wrap: anywhere;
  }
  /* Item 9 visual review P3-2: the shared card carries no bottom margin of its own, so where it
     sits above the order's rows here the panel touched the Order total row. */
  .pay-balance .held-order {
    margin-bottom: var(--hh-s-16);
  }
  .pay-balance .figure {
    display: flex;
    justify-content: space-between;
    gap: var(--hh-s-12);
    padding: var(--hh-s-8) var(--hh-s-12);
    margin: 0 0 var(--hh-s-8);
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-control);
  }
  .pay-balance .figure .amount {
    font-variant-numeric: tabular-nums;
    font-weight: 600;
    white-space: nowrap;
  }
  .pay-balance .figure.owed {
    background: color-mix(in srgb, var(--hos-stone-amber) 18%, transparent);
    border-color: var(--hos-stone-amber);
    font-weight: 600;
  }
  .pay-balance .tenders {
    display: flex;
    gap: var(--hh-s-8);
    margin: 0 0 var(--hh-s-12);
  }
  .pay-balance .tenders button {
    flex: 1 1 0;
    min-width: 0;
  }
  .pay-balance .tenders button[aria-pressed="true"] {
    border-color: var(--hos-stone-amber);
    font-weight: 600;
  }
  .pay-balance .wholeamount {
    text-align: end;
    font-size: 18px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    padding: var(--hh-s-12);
    border: 1px solid var(--hos-line);
    border-radius: var(--hos-radius-control);
    background: var(--hos-field);
    margin: 0 0 var(--hh-s-8);
  }
  .pay-balance .wholeonly {
    margin: 0 0 var(--hh-s-12);
    font-size: 12px;
    color: var(--hos-text-dim);
  }
  .pay-balance .take {
    width: 100%;
  }
  .pay-balance .done {
    border: 2px solid var(--hh-success); /* board 02: the green success border (--hh-state-ok was never a token) */
    border-radius: var(--hos-radius-card);
    padding: var(--hh-s-16);
    margin: var(--hh-s-16) 0 0;
  }
  .pay-balance .done h3 {
    margin: 0 0 var(--hh-s-8);
    font-size: 15px;
    overflow-wrap: anywhere;
  }
  .pay-balance .done p {
    margin: 0 0 var(--hh-s-8);
    overflow-wrap: anywhere;
  }
`];

export function renderPayBalance(flow: PayBalanceFlow): TemplateResult {
  const frozen = flow.finding || flow.taking;
  return html`
    <section class="returns pay-balance" data-testid="pay-balance">
      <div class="returns-col">
        <div class="returns-card">
          <h2>Pay balance</h2>
          <div class="returns-body">
            <p class="subtitle">Find the customer's order, see what was paid and what is owed, take the rest.</p>
            ${keyed(
              flow.epoch,
              html`<span class="eyebrow" id="ct-pay-balance-eyebrow">Find the customer's order</span>
                <div class="find-row">
                  <input
                    type="text"
                    data-field="pay-balance-term"
                    data-testid="pay-balance-term"
                    aria-labelledby="ct-pay-balance-eyebrow"
                    aria-describedby="ct-pay-balance-hint"
                    autocomplete="off"
                    ?disabled=${frozen}
                  />
                  <button
                    type="button"
                    class="btn-secondary"
                    data-action="pay-balance-find"
                    data-testid="pay-balance-find"
                    ?disabled=${!flow.canFind}
                  >
                    Find
                  </button>
                  <p class="find-hint" id="ct-pay-balance-hint">Order number, customer name, or phone.</p>
                </div>`,
            )}
            ${flow.notice === null
              ? nothing
              : html`<p class="notice" role="status" data-testid="pay-balance-notice">${flow.notice}</p>`}
            ${flow.more
              ? html`<p class="notice" role="status" data-testid="pay-balance-more">${MORE_ORDERS_WORDS}</p>`
              : nothing}
            ${flow.order === null && flow.matches.length > 1 ? renderMatches(flow) : nothing}
            ${flow.order === null ? nothing : renderOrder(flow, flow.order)}
          </div>
        </div>
      </div>
    </section>
  `;
}

function renderMatches(flow: PayBalanceFlow): TemplateResult {
  return html`
    <ul class="matches" aria-label="Orders that match">
      ${flow.matches.map(
        (o) => html`<li>
          <button
            type="button"
            class="btn-secondary"
            data-action="pay-balance-pick"
            data-order=${o.orderId}
            data-testid="pay-balance-match"
          >
            <span>${o.orderNo} · ${o.customerName}</span>
            <span>${formatMoney(o.stillOwedCents)} owed</span>
          </button>
        </li>`,
      )}
    </ul>
  `;
}

function renderOrder(flow: PayBalanceFlow, order: OwingOrder): TemplateResult {
  const fulfilment = fulfilmentWord(order.fulfilment);
  const jobLine = [order.job === null ? null : `Job: ${order.job}`, fulfilment === "" ? null : fulfilment]
    .filter((part): part is string => part !== null)
    .join(" · ");
  if (flow.taken !== null) {
    const taken = flow.taken;
    return html`
      <section class="done" role="status" data-testid="pay-balance-done">
        <h3>${paidInFullWords(taken.orderNo, taken.paidWords, taken.released)}</h3>
        <p>${moneyTrailWords(taken.payments, formatMoney)}</p>
        ${taken.replayed ? html`<p>This is the same payment answered again: nothing was charged twice.</p>` : nothing}
        <button type="button" class="btn-secondary" data-action="pay-balance-another">Find another order</button>
      </section>
    `;
  }
  return html`
    <div class="panes">
      <div class="pane" data-testid="pay-balance-order">
        <p class="customer">${order.customerName}</p>
        <span class="chip">Order · ${order.orderNo}</span>
        ${jobLine === "" ? nothing : html`<p class="jobline">${jobLine}</p>`}
        ${order.held
          ? renderHeldOrder(heldCardFromOwing(order, flow.hold, methodWord), {
              payBalanceHref: null,
              heading: false,
              figures: false,
              printButton: html`<button type="button" data-action="held-print-slip" data-testid="held-print-slip">Print hold slip</button>`,
            })
          : nothing}
        <div class="figure"><span>Order total</span><span class="amount">${formatMoney(order.orderTotalCents)}</span></div>
        ${order.payments.length === 0
          ? html`<div class="figure"><span>Paid so far: nothing yet</span></div>`
          : order.payments.map(
              (p) => html`<div class="figure">
                <span>Paid so far: ${methodWord(p.method)}</span>
                <span class="amount">${formatMoney(p.amountCents)}</span>
              </div>`,
            )}
        <div class="figure owed" data-testid="pay-balance-owed">
          <span>Still owed</span><span class="amount">${formatMoney(order.stillOwedCents)}</span>
        </div>
      </div>
      <div class="pane" data-testid="pay-balance-take-pane">
        <h3 class="customer">Take the rest</h3>
        <span class="eyebrow" id="ct-pay-balance-tender">Tender</span>
        <div class="tenders" role="group" aria-labelledby="ct-pay-balance-tender">
          ${(["CARD", "CASH"] as const).map(
            (m) => html`<button
              type="button"
              class="btn-secondary"
              data-action="pay-balance-method"
              data-method=${m}
              data-testid=${`pay-balance-method-${m}`}
              aria-pressed=${flow.method === m ? "true" : "false"}
              ?disabled=${flow.taking}
            >
              ${m === "CARD" ? "Card" : "Cash"}
            </button>`,
          )}
        </div>
        <div class="wholeamount" data-testid="pay-balance-amount">${formatMoney(order.stillOwedCents)}</div>
        <p class="wholeonly" data-testid="pay-balance-whole-only">The whole balance: this door takes no part payments.</p>
        ${flow.drawer.kind === "open"
          ? nothing
          : html`<p class="notice" data-testid="pay-balance-drawer">
              ${flow.drawer.kind === "closed"
                ? "No till session is open at this branch: open one on the sale screen, then take the payment."
                : "The drawer could not be asked just now."}
            </p>`}
        <button
          type="button"
          class="btn-primary take"
          data-action="pay-balance-take"
          data-testid="pay-balance-take"
          ?disabled=${!flow.canTake}
        >
          ${flow.taking ? "Taking…" : takeButtonWords(order.stillOwedCents, formatMoney)}
        </button>
      </div>
    </div>
  `;
}
