// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-return.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import {
  createReturn,
  lookupReturn,
  money,
  newIdempotencyKey,
  type ReturnLookup,
  type ReturnResult,
} from '@hardscapehouse/pos-kit/src/ported/pos-api.js';
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';
import { ModalFocus } from '@hardscapehouse/pos-kit/src/ported/modal-focus.js';
import { rowStripe } from './styles/row-stripe.js';
import { refundEstimateCents } from '@hardscapehouse/pos-kit/src/ported/ports.js';

/**
 * W7 POS return modal (POS_COUNTER_V2 §4): enter the original order no → the returnable lines (qty
 * remaining per line) → per-line qty + reason → refund method (CASH needs the open till; CARD only when
 * the original capture was card; ACCOUNT_CREDIT when the customer has an account) → optional logistics
 * pickup (site + requested date) → commit. One modal, count-entry styling, --hos-* tokens only.
 */
@customElement('hos-pos-return')
export class HosPosReturn extends LitElement {
  @property({ type: String }) tillSessionId = '';

 // E6: parent-mounted, so connected IS open. Escape is safe at every step of this dialog:
  // nothing is written until the refund is submitted, and the terminal re-opens it clean.
  readonly modalFocus = new ModalFocus(this, {
    container: () => this.renderRoot.querySelector<HTMLElement>('.hos-dialog'),
    onEscape: () => this.close(),
  });

  @state() private orderNo = '';
  @state() private lookup: ReturnLookup | null = null;
  @state() private qty: Record<string, number> = {};
  @state() private reasons: Record<string, string> = {};
  @state() private refundMethod: 'CASH' | 'CARD' | 'ACCOUNT_CREDIT' = 'CASH';
  @state() private reason = '';
  @state() private restock = true;
  @state() private pickupRequired = false;
  @state() private pickupSite = '';
  @state() private pickupDate = '';
  @state() private busy = false;
  @state() private error = '';
  @state() private done: ReturnResult | null = null;
  // The idempotency key is minted once per attempted return and retried VERBATIM (replay-safe).
  private idemKey = newIdempotencyKey();

  static styles = [
    focusRing,
    rowStripe,
    css`
    :host {
      display: block;
      font-family: var(--hos-font-ui);
      color: var(--hos-text);
    }
    .hos-dialog {
      position: fixed;
      inset: 0;
      background: var(--hos-scrim);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: var(--hos-z-modal);
    }
    .card {
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: 14px;
      width: min(640px, 94vw);
      max-height: 92vh;
      overflow: auto;
      padding: 22px 24px;
    }
    h2 {
      margin: 0 0 4px;
      font-size: 14px;
      text-transform: uppercase;
      letter-spacing: 0.12em;
      color: var(--hos-stone-amber-text);
    }
    .sub {
      font-size: 12px;
      color: var(--hos-text-dim);
      margin: 0 0 14px;
    }
    label {
      display: block;
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      margin: 12px 0 4px;
    }
    input,
    select,
    textarea {
      width: 100%;
      box-sizing: border-box;
      background: var(--hos-field);
      color: var(--hos-text);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      padding: 8px 10px;
      font-size: 13px;
      font-family: inherit;
    }
    .lookup-row {
      display: flex;
      gap: 8px;
      align-items: flex-end;
    }
    .lookup-row > div {
      flex: 1;
    }
    .lookup-row input {
      font-family: var(--hos-font-mono);
    }
    button {
      border: 1px solid var(--hos-line);
      border-radius: 8px;
      background: var(--hos-deep-earth);
      color: var(--hos-text);
      padding: 8px 14px;
      font-size: 13px;
      font-family: inherit;
      cursor: pointer;
    }
    button.primary {
      background: var(--hos-stone-amber);
      border-color: var(--hos-stone-amber);
      color: #0b1725;
      font-weight: 600;
    }
    button:disabled {
      background: var(--hh-disabled-fill);
      border-color: var(--hh-disabled-line);
      color: var(--hh-disabled-ink);
      opacity: 1;
      cursor: not-allowed;
    }
    /* HH note: disabled drops its fill and goes grey; fade is not a state. */
    button.primary:disabled {
      background: var(--hh-disabled-fill);
      border-color: var(--hh-disabled-line);
      color: var(--hh-disabled-ink);
      opacity: 1;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      margin-top: 12px;
      font-size: 13px;
    }
    th {
      text-align: left;
      font-size: 10px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      border-bottom: 1px solid var(--hos-line);
      padding: 6px 6px;
    }
    td {
      border-bottom: 1px solid var(--hos-line);
      padding: 7px 6px;
      vertical-align: middle;
    }
    td.num,
    th.num {
      text-align: right;
      font-family: var(--hos-font-mono);
    }
    td input {
      width: 76px;
      font-family: var(--hos-font-mono);
      text-align: right;
    }
    td select {
      width: 120px;
    }
    .checkline {
      display: flex;
      align-items: center;
      gap: 8px;
      margin-top: 12px;
      font-size: 13px;
    }
    .checkline input {
      width: auto;
    }
    .pickup {
      border: 1px solid var(--hos-line);
      border-radius: 10px;
      padding: 4px 14px 14px;
      margin-top: 10px;
      background: var(--hos-deep-earth);
    }
    .totals {
      display: flex;
      justify-content: space-between;
      border-top: 1px solid var(--hos-line);
      margin-top: 14px;
      padding-top: 10px;
      font-size: 14px;
    }
    .totals .num {
      font-family: var(--hos-font-mono);
      color: var(--hos-stone-amber-text);
      font-weight: 600;
    }
    .actions {
      display: flex;
      justify-content: flex-end;
      gap: 8px;
      margin-top: 16px;
    }
    .banner {
      margin-top: 12px;
      border-radius: 8px;
      padding: 10px 12px;
      font-size: 13px;
    }
    .banner.ok {
      background: rgba(74, 222, 128, 0.12);
      border: 1px solid var(--hos-success);
      color: var(--hos-success);
    }
    .banner.bad {
      background: rgba(244, 63, 94, 0.12);
      border: 1px solid var(--hos-safety-red);
      color: var(--hos-safety-red);
    }
    .num {
      font-family: var(--hos-font-mono);
    }
    .hint {
      font-size: 11px;
      color: var(--hos-text-dim);
      margin-top: 4px;
    }
  `,
  ];

  private close(): void {
    this.dispatchEvent(new CustomEvent('pos-return-close', { bubbles: true, composed: true }));
  }

  private async doLookup(): Promise<void> {
    if (!this.orderNo.trim()) return;
    this.error = '';
    this.lookup = null;
    this.done = null;
    try {
      const lk = await lookupReturn(this.orderNo.trim());
      this.lookup = lk;
      this.qty = {};
      this.reasons = {};
      // Card refunds are only possible against the original CARD capture.
      if (this.refundMethod === 'CARD' && !lk.card_refundable) this.refundMethod = 'CASH';
    } catch (e) {
      this.error = e instanceof Error ? e.message : 'lookup failed';
    }
  }

  private setQty(lineId: string, remaining: number, v: string): void {
    let q = Number(v);
    if (!Number.isFinite(q) || q < 0) q = 0;
    if (q > remaining) q = remaining;
    this.qty = { ...this.qty, [lineId]: q };
  }

 // Display estimate only: the SERVER derives the binding refund (capped per line) inside the tx.
 // HH note: the reference prorated each line's price and tax here; the money invariant puts that
 // arithmetic in the core, so the port asks the host seam with the reference's shape
 // (ports.ts, named in CLOSURE.md).
  private estimate(): { subtotal: number; tax: number; total: number } {
    return refundEstimateCents(this.lookup?.lines ?? [], this.qty);
  }

  private selectedLines(): { order_line_id: string; qty_base: number; reason_code?: string }[] {
    return (this.lookup?.lines ?? [])
      .filter((ln) => (this.qty[ln.order_line_id] ?? 0) > 0)
      .map((ln) => ({
        order_line_id: ln.order_line_id,
        qty_base: this.qty[ln.order_line_id],
        reason_code: this.reasons[ln.order_line_id] || undefined,
      }));
  }

  private canCommit(): boolean {
    if (!this.lookup || this.busy || this.done) return false;
    if (this.selectedLines().length === 0) return false;
    if (this.refundMethod === 'CASH' && !this.tillSessionId) return false;
    if (this.refundMethod === 'CARD' && !this.lookup.card_refundable) return false;
    return true;
  }

  private async commit(): Promise<void> {
    if (!this.lookup || !this.canCommit()) return;
    this.busy = true;
    this.error = '';
    try {
      const res = await createReturn({
        idempotency_key: this.idemKey,
        order_id: this.lookup.order_id,
        lines: this.selectedLines(),
        refund_method: this.refundMethod,
        reason: this.reason || undefined,
        restock: this.restock,
        till_session_id: this.refundMethod === 'CASH' ? this.tillSessionId : undefined,
        pickup: this.pickupRequired
          ? { required: true, site: this.pickupSite, requested_date: this.pickupDate }
          : undefined,
      });
      this.done = res;
      this.dispatchEvent(new CustomEvent('pos-return-done', { detail: { result: res }, bubbles: true, composed: true }));
    } catch (e) {
      this.error = e instanceof Error ? e.message : 'return failed';
      this.idemKey = newIdempotencyKey(); // a rejected return re-mints; a network retry keeps the key
    } finally {
      this.busy = false;
    }
  }

  render() {
    const est = this.estimate();
    return html`<div class="hos-dialog" @click=${(e: Event) => e.target === e.currentTarget && this.close()}>
      <div class="card">
        <h2>Return</h2>
        <p class="sub">Against one original counter sale: refund by cash, card reversal or account credit.</p>

        <div class="lookup-row">
          <div>
            <label>Original order no</label>
            <input
              placeholder="#SO-1042"
              .value=${this.orderNo}
              @keydown=${(e: KeyboardEvent) => e.key === 'Enter' && this.doLookup()}
              @change=${(e: Event) => (this.orderNo = (e.target as HTMLInputElement).value)}
            />
          </div>
          <button @click=${this.doLookup}>Look up</button>
        </div>

        ${this.lookup
          ? html`
              <table>
                <thead>
                  <tr>
                    <th>Line</th>
                    <th class="num">Sold</th>
                    <th class="num">Returnable</th>
                    <th class="num">Return qty</th>
                    <th>Reason</th>
                  </tr>
                </thead>
                <tbody>
                  ${this.lookup.lines.map((ln) => {
                    const remaining = Math.max(0, ln.qty_sold - ln.qty_returned);
                    return html`<tr>
                      <td>${ln.description || ln.product_id}</td>
                      <td class="num">${ln.qty_sold}</td>
                      <td class="num">${remaining}</td>
                      <td class="num">
                        <input
                          type="number"
                          min="0"
                          max=${remaining}
                          step="any"
                          ?disabled=${remaining <= 0}
                          .value=${String(this.qty[ln.order_line_id] ?? 0)}
                          @change=${(e: Event) =>
                            this.setQty(ln.order_line_id, remaining, (e.target as HTMLInputElement).value)}
                        />
                      </td>
                      <td>
                        <select
                          @change=${(e: Event) =>
                            (this.reasons = {
                              ...this.reasons,
                              [ln.order_line_id]: (e.target as HTMLSelectElement).value,
                            })}
                        >
                          <option value="">(not set)</option>
                          <option value="DAMAGED">Damaged</option>
                          <option value="WRONG_ITEM">Wrong item</option>
                          <option value="SURPLUS">Surplus / over-ordered</option>
                          <option value="QUALITY">Quality</option>
                          <option value="OTHER">Other</option>
                        </select>
                      </td>
                    </tr>`;
                  })}
                </tbody>
              </table>

              <label>Refund method</label>
              <select
                .value=${this.refundMethod}
                @change=${(e: Event) =>
                  (this.refundMethod = (e.target as HTMLSelectElement).value as 'CASH' | 'CARD' | 'ACCOUNT_CREDIT')}
              >
                <option value="CASH" ?disabled=${!this.tillSessionId}>
                  Cash from drawer${this.tillSessionId ? '' : ' (open a till session first)'}
                </option>
                <option value="CARD" ?disabled=${!this.lookup.card_refundable}>
                  Card reversal${this.lookup.card_refundable ? '' : ' (original sale was not card)'}
                </option>
                <option value="ACCOUNT_CREDIT">Account credit</option>
              </select>

              <label>Return reason (ticket note)</label>
              <textarea
                rows="2"
                .value=${this.reason}
                @change=${(e: Event) => (this.reason = (e.target as HTMLTextAreaElement).value)}
              ></textarea>

              <div class="checkline">
                <input
                  type="checkbox"
                  id="restock"
                  .checked=${this.restock}
                  @change=${(e: Event) => (this.restock = (e.target as HTMLInputElement).checked)}
                />
                <label for="restock" style="margin:0">Restock (RETURN move at current weighted average)</label>
              </div>

              <div class="checkline">
                <input
                  type="checkbox"
                  id="pickup"
                  .checked=${this.pickupRequired}
                  @change=${(e: Event) => (this.pickupRequired = (e.target as HTMLInputElement).checked)}
                />
                <label for="pickup" style="margin:0">Logistics pickup required (schedule collection)</label>
              </div>
              ${this.pickupRequired
                ? html`<div class="pickup">
                    <label>Pickup site</label>
                    <input
                      placeholder="Site / address the material is collected from"
                      .value=${this.pickupSite}
                      @change=${(e: Event) => (this.pickupSite = (e.target as HTMLInputElement).value)}
                    />
                    <label>Requested date</label>
                    <input
                      type="date"
                      .value=${this.pickupDate}
                      @change=${(e: Event) => (this.pickupDate = (e.target as HTMLInputElement).value)}
                    />
                    <div class="hint">Queued REQUESTED for logistics: scheduled by dispatch, not the counter.</div>
                  </div>`
                : nothing}

              <div class="totals">
                <span>Refund (estimated: server derives the binding amount)</span>
                <span class="num">${money(est.total)}</span>
              </div>
            `
          : nothing}
        ${this.error ? html`<div class="banner bad" role="alert">${this.error}</div>` : nothing}
        ${this.done
          ? html`<div class="banner ok">
              Returned · <span class="num">${this.done.return_no}</span> · refunded
              <span class="num">${money(this.done.total_cents)}</span> (${this.done.refund_method})
              ${this.done.pickup_status !== 'NONE' ? html` · pickup ${this.done.pickup_status}` : nothing}
            </div>`
          : nothing}

        <div class="actions">
          <button @click=${this.close}>${this.done ? 'Close' : 'Cancel'}</button>
          <button class="primary" ?disabled=${!this.canCommit()} @click=${this.commit}>
            ${this.busy ? 'Committing…' : 'Commit return'}
          </button>
        </div>
      </div>
    </div>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-return': HosPosReturn;
  }
}
