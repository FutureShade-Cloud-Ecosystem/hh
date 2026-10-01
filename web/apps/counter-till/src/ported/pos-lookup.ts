// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-lookup.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { focusRing } from './styles/focus-ring.js';
import { lookupSales, getSale, money, type SaleSummary, type SaleView } from './pos-api.js';
import { printReceipt } from './receipt-print.js';
import { orderStatusWord } from './order-logic.js';
import { tenderMethodWord } from './tender-logic.js';
import { rowStripe } from './styles/row-stripe.js';

/**
 * hos-pos-lookup: DIB-165. Find a past sale and reprint it.
 *
 * WHY THIS EXISTS WHEN REPRINT ALREADY DID. The receipt route
 * (GET /api/v1/pos/sales/{id}/receipt) has worked since DIB-410: provided you already knew the sale
 * id. At a counter nobody does. The customer says "I was in the other day", or hands over a creased
 * receipt with an order number, or the cashier needs the sale they rang twenty minutes ago on this
 * shift. This is the screen that turns those into an id.
 *
 * TWO SCOPES, ONE LIST. "This till session" is the shift-recall case (what did I ring today, on this
 * drawer) and "This branch" is the customer-recall case. They differ only by whether the till session
 * narrows the query, so they are one screen with a toggle rather than two screens that will drift.
 *
 * THE FENCE IS THE SERVER'S. This component always sends its branch id and never offers a branch
 * picker: the endpoint refuses a scoped caller who names a branch they cannot reach, and a UI
 * control that lets someone ask for a 403 is a control that teaches them the fence exists. A 403 is
 * surfaced plainly if one is ever returned.
 *
 * REPRINT GOES THROUGH THE EXISTING FLOW, deliberately, and through printReceipt() rather than a
 * link. Two separate reasons, and the Chromium walkthrough found the second one the hard way:
 *
 * 1. The receipt document carries a reprint counter, and that counter is only honest if every
 * reprint increments it: so this asks the server for the document rather than re-rendering the
 * sale from the reopen payload it already holds.
 * 2. The receipt route is STAFF-GATED and the staff session is a Bearer token in the session store. A
 * `window.open('/api/v1/pos/sales/…/receipt')` is a plain navigation with no Authorization
 * header: it renders `{"error":"unauthorized"}` in a window facing the customer. printReceipt()
 * (DIB-410) fetches with the token and writes the document into a blank window, which is why it
 * exists and why nothing here should reinvent it.
 */
@customElement('hos-pos-lookup')
export class HosPosLookup extends LitElement {
 /** The branch this terminal is acting for: always sent, never chosen here. */
  @property({ attribute: 'branch-id' }) branchId = '';
 /** The open drawer session, for the "this shift" scope. '' disables that toggle. */
  @property({ attribute: 'till-session-id' }) tillSessionId = '';

  @state() private scope: 'session' | 'branch' = 'session';
  @state() private term = '';
  @state() private from = '';
  @state() private to = '';
  @state() private rows: SaleSummary[] = [];
  @state() private loading = false;
  @state() private error = '';
  @state() private searched = false;
  @state() private detail: SaleView | null = null;
  @state() private detailError = '';

  static styles = [
    focusRing,
    rowStripe,
    css`
      :host {
        display: block;
        font-family: var(--hos-font-ui);
        color: var(--hos-text);
      }
      .bar {
        display: flex;
        flex-wrap: wrap;
        gap: 8px;
        align-items: center;
        margin-bottom: 12px;
      }
      input,
      select {
        background: var(--hos-deep-earth);
        color: var(--hos-text);
        border: 1px solid var(--hos-line);
        border-radius: 8px;
        padding: 8px 10px;
        font-size: 14px;
        font-family: inherit;
      }
      input.term {
        flex: 1;
        min-width: 180px;
      }
      button {
        background: transparent;
        border: 1px solid var(--hos-line);
        color: var(--hos-text-dim);
        border-radius: 8px;
        padding: 8px 14px;
        font-size: 13px;
        font-family: inherit;
        cursor: pointer;
      }
      button:hover:not(:disabled) {
        border-color: var(--hos-stone-amber);
        color: var(--hos-stone-amber-text);
      }
      button:disabled {
        opacity: 0.5;
        cursor: default;
      }
      .seg {
        display: inline-flex;
        border: 1px solid var(--hos-line);
        border-radius: 99px;
        overflow: hidden;
      }
      .seg button {
        border: 0;
        border-radius: 0;
        padding: 7px 14px;
      }
      .seg button.active {
        color: var(--hos-stone-amber-text);
        background: color-mix(in srgb, var(--hos-stone-amber) 12%, transparent);
      }
      table {
        width: 100%;
        border-collapse: collapse;
        font-size: 13px;
      }
      th {
        text-align: left;
        font-size: 10px;
        text-transform: uppercase;
        letter-spacing: 0.09em;
        color: var(--hos-text-dim);
        font-weight: 600;
        padding: 6px 8px;
        border-bottom: 1px solid var(--hos-line);
      }
      td {
        padding: 8px;
        border-bottom: 1px solid var(--hos-line);
        vertical-align: middle;
      }
      td.mono,
      th.mono {
        font-family: var(--hos-font-mono);
      }
      td.amt {
        text-align: right;
        font-family: var(--hos-font-mono);
      }
      .badge {
        display: inline-block;
        font-size: 10px;
        text-transform: uppercase;
        letter-spacing: 0.07em;
        border: 1px solid var(--hos-line);
        border-radius: 99px;
        padding: 2px 8px;
        color: var(--hos-text-dim);
      }
      .badge.split {
        color: var(--hos-info-blue);
        border-color: var(--hos-info-blue);
      }
      .muted {
        color: var(--hos-text-dim);
        font-size: 13px;
      }
      .err {
        color: var(--hos-safety-red);
        font-size: 13px;
      }
      .detail {
        margin-top: 14px;
        background: var(--hos-slate-warm);
        border: 1px solid var(--hos-line);
        border-radius: 12px;
        padding: 14px;
      }
      .detail h4 {
        margin: 0 0 8px;
        font-size: 13px;
        text-transform: uppercase;
        letter-spacing: 0.09em;
        color: var(--hos-text-dim);
      }
      .legs {
        display: flex;
        flex-wrap: wrap;
        gap: 8px;
        margin: 8px 0;
      }
      .leg {
        border: 1px solid var(--hos-line);
        border-radius: 8px;
        padding: 6px 10px;
        font-family: var(--hos-font-mono);
        font-size: 12px;
      }
      .leg .m {
        color: var(--hos-info-blue);
        font-family: var(--hos-font-ui);
        font-size: 10px;
        text-transform: uppercase;
        letter-spacing: 0.07em;
        margin-right: 6px;
      }
    `,
  ];

  private async search(): Promise<void> {
    if (!this.branchId) {
      this.error = 'No branch selected on this terminal.';
      return;
    }
    this.loading = true;
    this.error = '';
    this.detail = null;
    try {
      this.rows = await lookupSales({
        branch_id: this.branchId,
 // The only difference between the two scopes: a session filter, or none.
        till_session_id: this.scope === 'session' ? this.tillSessionId : undefined,
        q: this.term.trim() || undefined,
        from: this.from || undefined,
        to: this.to || undefined,
        limit: 50,
      });
    } catch (err) {
      this.rows = [];
      this.error = err instanceof Error ? err.message : 'Lookup failed.';
    } finally {
      this.loading = false;
      this.searched = true;
    }
  }

  private async open(row: SaleSummary): Promise<void> {
    this.detail = null;
    this.detailError = '';
    try {
      this.detail = await getSale(row.order_id);
    } catch (err) {
      this.detailError = err instanceof Error ? err.message : 'Could not reopen that sale.';
    }
  }

 // reprint runs the DIB-410 flow: fetch the document with the session token, write it into a blank
 // window, print. Never a navigation to the API URL: see the class comment.
  private async reprint(row: SaleSummary): Promise<void> {
    this.detailError = '';
    try {
      const outcome = await printReceipt(row.sale_id || row.order_id);
      if (!outcome.ok) {
 // The popup-blocker case is the cashier's to fix: say so plainly rather than leaving them
 // looking at a window that never appeared.
        this.detailError = outcome.message;
      }
    } catch (err) {
      this.detailError = err instanceof Error ? err.message : 'Reprint failed.';
    }
  }

  private setScope(scope: 'session' | 'branch'): void {
    this.scope = scope;
    if (this.searched) void this.search();
  }

  render() {
    const noSession = this.tillSessionId === '';
    return html`
      <div class="bar">
        <div class="seg" role="group" aria-label="Search scope">
          <button
            class=${this.scope === 'session' ? 'active' : ''}
            ?disabled=${noSession}
            title=${noSession ? 'Open a till session to search this shift' : 'Sales rung on this drawer session'}
            @click=${() => this.setScope('session')}
          >
            This shift
          </button>
          <button
            class=${this.scope === 'branch' ? 'active' : ''}
            title="Every sale this branch has rung"
            @click=${() => this.setScope('branch')}
          >
            Branch history
          </button>
        </div>
        <input
          class="term"
          data-testid="lookup-term"
          aria-label="Order number, invoice number or customer"
          placeholder="Order no, invoice no, or customer…"
          .value=${this.term}
          @input=${(e: Event) => (this.term = (e.target as HTMLInputElement).value)}
          @keydown=${(e: KeyboardEvent) => {
 // Enter searches: the counter's hands are on the keyboard, not the mouse.
            if (e.key === 'Enter') void this.search();
          }}
        />
        <input
          type="date"
          aria-label="From date"
          .value=${this.from}
          @change=${(e: Event) => (this.from = (e.target as HTMLInputElement).value)}
        />
        <input
          type="date"
          aria-label="To date"
          .value=${this.to}
          @change=${(e: Event) => (this.to = (e.target as HTMLInputElement).value)}
        />
        <button data-testid="lookup-search" ?disabled=${this.loading} @click=${() => void this.search()}>
          ${this.loading ? 'Searching…' : 'Find sales'}
        </button>
      </div>

      ${this.error ? html`<p class="err" role="alert" data-testid="lookup-error">${this.error}</p>` : nothing}
      ${this.renderRows()} ${this.renderDetail()}
    `;
  }

  private renderRows() {
    if (this.error) return nothing;
    if (!this.searched) {
      return html`<p class="muted">
        Search this shift's drawer or the branch's history: by order number, invoice number or customer name.
      </p>`;
    }
    if (this.rows.length === 0) {
      return html`<p class="muted" data-testid="lookup-empty">No sales match that search.</p>`;
    }
    return html`
      <table data-testid="lookup-results">
        <thead>
          <tr>
            <th class="mono">Order</th>
            <th>Customer</th>
            <th>Tender</th>
            <th class="mono">When</th>
            <th style="text-align:right">Total</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          ${this.rows.map(
            (r) => html`
              <tr data-order-no=${r.order_no}>
                <td class="mono">${r.order_no}</td>
                <td>${r.customer_name}</td>
                <td>
                  ${tenderMethodWord(r.tender)}
                  ${r.split_tender
                    ? html`<span class="badge split" title="Settled across several tenders">split</span>`
                    : nothing}
                </td>
                <td class="mono">${r.created_at.slice(0, 16).replace('T', ' ')}</td>
                <td class="amt">${money(r.total_cents)}</td>
                <td>
                  <button @click=${() => void this.open(r)}>Open</button>
                  <button data-testid="reprint-${r.order_no}" @click=${() => void this.reprint(r)}>Reprint</button>
                </td>
              </tr>
            `,
          )}
        </tbody>
      </table>
    `;
  }

  private renderDetail() {
    if (this.detailError) return html`<p class="err" role="alert">${this.detailError}</p>`;
    const d = this.detail;
    if (!d) return nothing;
    return html`
      <div class="detail" data-testid="lookup-detail">
        <h4>${d.order_no} · ${orderStatusWord(d.status)}</h4>
        ${d.split_tender && d.tenders?.length
          ? html`<div class="legs" data-testid="detail-legs">
              ${d.tenders.map(
                (t) =>
                  html`<span class="leg"><span class="m">${tenderMethodWord(t.method)}</span>${money(
                    t.amount_cents,
                  )}</span>`,
              )}
            </div>`
          : html`<div class="legs">
              <span class="leg"><span class="m">${tenderMethodWord(d.tender)}</span>${money(
                d.total_cents,
              )}</span>
            </div>`}
        <table>
          <thead>
            <tr>
              <th>Line</th>
              <th class="mono">Qty</th>
              <th style="text-align:right">Total</th>
            </tr>
          </thead>
          <tbody>
            ${d.lines.map(
              (l) => html`<tr>
                <td>${l.description}</td>
                <td class="mono">${l.qty_entered} ${l.uom_entered}</td>
                <td class="amt">${money(l.line_total_cents)}</td>
              </tr>`,
            )}
          </tbody>
        </table>
        <p class="muted">
          Subtotal ${money(d.subtotal_cents)} · Tax ${money(d.tax_cents)} · <strong>${money(d.total_cents)}</strong>
        </p>
        ${/* Same gated flow as the row button; a bare <a href> to the API would render a 401. */ ''}
        <button data-testid="detail-reprint" @click=${() => void this.reprint({ sale_id: d.order_id } as SaleSummary)}>
          Reprint receipt
        </button>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-lookup': HosPosLookup;
  }
}
