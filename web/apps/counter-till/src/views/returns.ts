// The returns desk: the till's returns story, rendered from the returns
// flow's state. This view holds no domain logic and no money of its own: it
// spells the flow's facts through the one money helper and asks the host's
// actions through data attributes, read off composedPath() by the app root
// (the design system's host delegation rule: never @click, never a content
// listener that a re-render would strand).
import { css, html, nothing, type TemplateResult } from "lit";
import { formatMoney } from "../money.js";
import type { ReturnsFlow } from "../returns/flow.js";

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
`;

export function renderReturns(flow: ReturnsFlow): TemplateResult {
  return html`
    <section class="returns" data-testid="returns">
      <h2>Returns</h2>
      <p class="hint">Find the original sale by its number, then choose the lines to take back.</p>
      <div class="find-row">
        <div>
          <label for="ct-returns-order-no">Sale number</label>
          <input
            id="ct-returns-order-no"
            type="text"
            data-field="order-no"
            .value=${flow.orderNo}
            ?disabled=${flow.busy}
          />
        </div>
        <button type="button" class="btn-secondary" data-action="returns-find" ?disabled=${!flow.canFind}>
          Find sale
        </button>
      </div>
      ${flow.notice === null ? nothing : html`<p class="notice" role="status" data-testid="returns-notice">${flow.notice}</p>`}
      ${flow.lookup === null ? nothing : renderSale(flow)}
    </section>
  `;
}

function renderSale(flow: ReturnsFlow): TemplateResult {
  const lookup = flow.lookup;
  if (lookup === null) return html``;
  return html`
    <h3 class="sale-heading" data-testid="returns-sale">Sale ${lookup.orderNo}</h3>
    <p class="sale-meta">Paid by ${tenderWords(lookup.saleTender)}.</p>
    <ul class="rows" aria-label="Sale lines">
      ${lookup.lines.map((line) => renderLine(line))}
    </ul>
  `;
}

function renderLine(line: {
  description: string;
  qtySold: number;
  qtyReturned: number;
  unitPriceCents: number;
  lineTotalCents: number;
}): TemplateResult {
  // What is still returnable is the line's two returned-and-sold counts read
  // side by side; the commit re-guards the quantity on the server, so this
  // read-off is for the clerk's eye, never an authority.
  return html`
    <li class="row">
      <span class="line-desc">${line.description}</span>
      <span class="line-figures">each ${formatMoney(line.unitPriceCents)}, line ${formatMoney(line.lineTotalCents)}</span>
      <span class="line-returnable">sold ${line.qtySold}, returned ${line.qtyReturned}</span>
    </li>
  `;
}

function tenderWords(tender: string): string {
  if (tender === "ACCOUNT") return "account";
  if (tender === "CASH") return "cash";
  if (tender === "CARD") return "card";
  return "cheque";
}
