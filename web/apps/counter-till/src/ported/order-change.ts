// Ported from hardscapeos_dibbits f8f4973db317b92296ae8ec7e62a3f54f5dc82ab app/src/order-change.ts; adapted for HH.

// order-change.ts: SCOPE:F10 post-pick change orders + the reservation panel, as the till's ONE
// "Change order" action.
//
//   <hos-order-change order-id="#SO-42">
//
// THE ONE ACTION. The brief's stage 1 ruling: the sold card and a looked-up order carry ONE
// "Change order" action, and which flow it opens is the order's own state:
//
//   before the pick starts  ->  the pre pick line edit (<hos-order-edit>, DIB-175) and its trail
//   once the pick has started  ->  a change request whose delta the SERVER prices (no money is
//                                  computed in the till, Q8), with the request's state shown and
//                                  the actions the server offers
//
// The mode is picked from the order read's status through the same ladder the core's raise gate
// falls back to (statusIsPostPick in _order-change.ts). The server stays the authority: a raise the
// core refuses with "the pick has not started" answers in the server's words and the surface flips
// to the line edit, which is the path the refusal names.
//
// EVERY CONTROL IS RENDERED FROM THE SERVER'S ANSWER. next_actions (core order/change.go) decides
// which buttons exist; nothing here evaluates the yard/finance ladder or the threshold. That is
// what makes it impossible to offer an Apply the server would refuse, and means a ratified rule
// change needs no client redeploy.
//
// REFUSALS. Every refusal lands in the server's own words, verbatim, because each 409 on this
// surface carries a different sentence ("the yard has not acknowledged" vs "over the finance
// threshold" vs "already closed") and replacing them with one generic line is exactly what makes a
// gate feel arbitrary. The one exception is the role gate's bare Forbidden word, which names
// nobody: refusalWords (ROLE-APP-OBLIGATIONS 1) replaces it with plain words naming who may act.
import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { api } from '@hardscapehouse/pos-kit/src/ported/ports.js';
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';
import { formatMoney } from "../money.js";
import {
  ChangeOrder,
  OrderHold,
  canDoChangeAction,
  changeActionLabel,
  offeredChangeActions,
  changeActionPath,
  changeStageSummary,
  changeStatusChip,
  describeDelta,
  formatQty,
  refusalWords,
  statusIsPostPick,
  thrownMessage,
  ACTION_APPLY,
  ACTION_REJECT,
} from './_order-change.js';
import { changeStatusWord, orderStatusPhrase } from './order-logic.js';
import { rowStripe } from './styles/row-stripe.js';
// The embedded panels register on import: the line edit and its trail are the pre pick half of
// this one action.
import './order-edit.js';
import './order-edits.js';

/** The raise is not a next_action on a change row (there is no row yet); it is the form's own act. */
const ACTION_RAISE = 'raise';

interface ReservationView {
  order_id: string;
  status: string;
  seam_wired: boolean;
  reservations: OrderHold[];
  held_base: number;
}

/** The one fact of the order read this surface needs: the status that picks the flow. */
interface OrderStatusView {
  id: string;
  status: string;
}

@customElement('hos-order-change')
export class HosOrderChange extends LitElement {
  static styles = [
    focusRing,
    rowStripe,
    css`
      :host {
        display: block;
        font-family: var(--hos-font-ui);
        color: var(--hos-text);
        font-size: 13px;
      }
      details.change {
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-control, 8px);
        padding: 8px 12px;
        margin-top: 8px;
      }
      details.change summary {
        cursor: pointer;
        font-weight: 600;
        list-style: revert;
      }
      .box {
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-control, 8px);
        padding: 10px 12px;
        margin-top: 8px;
      }
      h3 {
        font-size: 13px;
        margin: 0 0 8px;
        font-weight: 600;
      }
      /* The reservations heading pairs with an info disclosure rather than a resting sentence: a
         reader who wonders why the list is shorter than the order gets the answer on demand;
         nobody else pays for a sentence they were not confused by. */
      .titlerow {
        display: flex;
        align-items: baseline;
        gap: 8px;
        margin-bottom: 8px;
      }
      .titlerow h3 {
        margin-bottom: 0;
      }
      details.info {
        display: inline-block;
        font-size: 11px;
        color: var(--hos-text-dim);
      }
      details.info summary {
        cursor: pointer;
        list-style: none;
        border: 1px solid var(--hos-line);
        border-radius: 99px;
        padding: 0 7px;
        font-size: 11px;
        user-select: none;
      }
      details.info summary::-webkit-details-marker {
        display: none;
      }
      details.info[open] summary {
        border-color: var(--hos-stone-amber);
      }
      details.info p {
        margin: 6px 0 0;
        max-width: 46rem;
        font-size: 12px;
      }
      .row {
        display: flex;
        align-items: center;
        gap: 10px;
        flex-wrap: wrap;
      }
      .label {
        color: var(--hos-text-dim);
        font-size: 12px;
      }
      .chip {
        display: inline-block;
        font-size: 11px;
        font-family: var(--hos-font-mono);
        padding: 2px 8px;
        border-radius: 99px;
        border: 1px solid var(--hos-line);
        color: var(--hos-text-dim);
      }
      .chip.red {
        color: var(--hos-safety-red);
        border-color: var(--hos-safety-red);
      }
      .chip.amber {
        color: var(--hos-stone-amber-text);
        border-color: var(--hos-stone-amber);
      }
      .chip.blue {
        color: var(--hos-info-blue);
        border-color: var(--hos-info-blue);
      }
      .chip.green {
        color: var(--hos-success);
        border-color: var(--hos-success);
      }
      .spacer {
        flex: 1;
      }
      button {
        font: inherit;
        padding: 4px 10px;
        border-radius: var(--hos-radius-control, 8px);
        border: 1px solid var(--hos-line);
        background: transparent;
        color: var(--hos-text);
        cursor: pointer;
      }
      button:disabled {
        opacity: 0.5;
        cursor: default;
      }
      button.primary {
        border-color: var(--hos-info-blue);
        color: var(--hos-info-blue);
      }
      button.danger {
        color: var(--hos-safety-red);
        border-color: var(--hos-safety-red);
      }
      table {
        width: 100%;
        border-collapse: collapse;
        margin-top: 6px;
      }
      th,
      td {
        text-align: left;
        padding: 4px 6px;
        border-bottom: 1px solid var(--hos-line);
        font-size: 12px;
      }
      th {
        color: var(--hos-text-dim);
        font-weight: 500;
      }
      td.num {
        text-align: right;
        font-family: var(--hos-font-mono);
      }
      .stage {
        color: var(--hos-text-dim);
        margin: 6px 0;
      }
      .err {
        color: var(--hos-safety-red);
        margin-top: 6px;
      }
      .empty {
        color: var(--hos-text-dim);
        font-style: italic;
      }
      .warn {
        color: var(--hos-stone-amber-text);
        margin-top: 6px;
      }
      input,
      textarea {
        font: inherit;
        padding: 4px 6px;
        border-radius: var(--hos-radius-control, 8px);
        border: 1px solid var(--hos-line);
        background: transparent;
        color: var(--hos-text);
      }
      .form {
        display: flex;
        gap: 8px;
        flex-wrap: wrap;
        align-items: flex-end;
        margin-top: 8px;
      }
      .field {
        display: flex;
        flex-direction: column;
        gap: 3px;
      }
    `,
  ];

  /** The order id or its visible order_no. The server resolves both. */
  @property({ attribute: 'order-id' }) orderId = '';

  @state() private status: OrderStatusView | null = null;
  @state() private changes: ChangeOrder[] = [];
  @state() private holds: ReservationView | null = null;
  @state() private err = '';
  @state() private busy = false;
  @state() private draftLineNo = '';
  @state() private draftQty = '';
  @state() private draftReason = '';
  @state() private note = '';

  connectedCallback() {
    super.connectedCallback();
    void this.load();
  }

  updated(changed: Map<string, unknown>) {
    if (changed.has('orderId')) void this.load();
  }

  /** Re-reads the whole surface: this element's own reads and the embedded panels' (the terminal's
   *  refresh reaches this element, and the trail beneath an edit lives inside this shadow root where
   *  the terminal's query cannot see it). */
  reload(): void {
    void this.load();
    this.renderRoot
      ?.querySelectorAll('hos-order-edit, hos-order-edits')
      .forEach((el) => (el as HTMLElement & { reload?: () => void }).reload?.());
  }

  private async load() {
    if (!this.orderId) return;
    this.err = '';
    try {
      const [oRes, cRes, rRes] = await Promise.all([
        api(`/api/v1/orders/${encodeURIComponent(this.orderId)}`),
        api(`/api/v1/orders/${encodeURIComponent(this.orderId)}/changes`),
        api(`/api/v1/orders/${encodeURIComponent(this.orderId)}/reservations`),
      ]);
      if (oRes.ok) this.status = (await oRes.json()) as OrderStatusView;
      if (cRes.ok) this.changes = ((await cRes.json()) as { changes: ChangeOrder[] }).changes ?? [];
      if (rRes.ok) this.holds = (await rRes.json()) as ReservationView;
    } catch (e) {
      this.err = thrownMessage(e);
    }
  }

  /** raise posts a QTY change against one line. The delta is priced SERVER-side (the pricing
   *  waterfall and its margin gate): this form never sends a price. */
  private async raise() {
    const lineNo = Number(this.draftLineNo);
    if (!Number.isInteger(lineNo) || lineNo <= 0) {
      this.err = 'Enter the line number you are changing.';
      return;
    }
    if (!this.draftQty.trim()) {
      this.err = 'Enter the new quantity for that line.';
      return;
    }
    await this.post(
      ACTION_RAISE,
      `/api/v1/orders/${encodeURIComponent(this.orderId)}/changes`,
      {
        reason: this.draftReason,
        lines: [{ op: 'QTY', line_no: lineNo, qty_entered: this.draftQty.trim() }],
      },
      () => {
        this.draftLineNo = '';
        this.draftQty = '';
        this.draftReason = '';
      },
    );
  }

  private async act(co: ChangeOrder, action: string) {
    const path = changeActionPath(action);
    if (!path) return; // an action this client does not know must not POST to a guessed endpoint
    await this.post(
      action,
      `/api/v1/order-changes/${encodeURIComponent(co.id)}/${path}`,
      this.note.trim() === '' ? {} : { note: this.note.trim() },
      () => {
        this.note = '';
      },
    );
    // An applied change rewrote the order's lines and totals: the sold card and every sibling
    // order panel must hear it, exactly as a line edit's order-changed does.
    if (action === ACTION_APPLY) {
      this.dispatchEvent(
        new CustomEvent('order-changed', {
          detail: { orderId: this.orderId },
          bubbles: true,
          composed: true,
        }),
      );
    }
  }

  private async post(action: string, path: string, body: unknown, onDone: () => void) {
    this.busy = true;
    this.err = '';
    try {
      const res = await api(path, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        // The server's own sentence, verbatim, except the role gate's bare Forbidden word, which
        // refusalWords replaces with plain words naming who may act.
        const payload = (await res.json().catch(() => ({}))) as { error?: string };
        this.err = refusalWords(action, payload.error || `request failed (${res.status})`);
        // A raise the core refused because the pick has not started names the line-edit path: the
        // surface follows the server's word and flips to that path.
        if (action === ACTION_RAISE && this.status !== null && this.status.status !== '') {
          await this.load();
        }
        return;
      }
      onDone();
      await this.load();
    } catch (e) {
      this.err = thrownMessage(e);
    } finally {
      this.busy = false;
    }
  }

  render() {
    const postPick = this.status !== null && statusIsPostPick(this.status.status);
    return html`
      <details
        class="change"
        data-testid="order-change"
        @toggle=${(e: Event) => {
          // Reopening re-reads: the yard and finance move a change out of band
          // (their apps, or a colleague at another counter), and a surface that
          // kept its first read would answer "With yard" to a decided question.
          if ((e.target as HTMLDetailsElement).open) void this.load();
        }}
      >
        <summary>Change order</summary>
        ${this.status === null
          ? nothing
          : postPick
            ? this.renderChangeFlow()
            : this.renderEditFlow()}
        ${this.err ? html`<div class="err" role="alert" data-testid="change-error">${this.err}</div>` : nothing}
      </details>
      ${/* The reservation read is the change surface's own (what the change will rewrite); it
             rides beside the flow the order is in, and needs no press of its own. */ ''}
      ${postPick ? this.renderReservations() : nothing}
    `;
  }

  /** Before the pick starts: the one action opens the line edit and its trail. */
  private renderEditFlow() {
    return html`
      <div class="stage" data-testid="change-mode-note">
        This order is ${this.status ? orderStatusPhrase(this.status.status) : ''}: the pick has not
        started, so a line edit applies straight away. Once the yard has started picking, changes
        travel as a request the yard and, over the threshold, finance sign.
      </div>
      <hos-order-edit order-id=${this.orderId} @order-changed=${this.forwardOrderChanged}>
      </hos-order-edit>
      <hos-order-edits order-id=${this.orderId}></hos-order-edits>
    `;
  }

  /** Once the pick has started: the raise form, the request's state, and the offered actions. */
  private renderChangeFlow() {
    return html`
      <div class="box">
        <h3>Change request</h3>
        <div class="stage" data-testid="change-mode-note">
          The yard has this order (${this.status ? orderStatusPhrase(this.status.status) : ''}): a
          change travels as a request, priced by the server, that the yard acknowledges and, over
          the threshold, finance signs before it applies.
        </div>
        <div class="form">
          <label class="field">
            <span class="label">Line no.</span>
            <input
              size="4"
              data-testid="change-raise-line"
              .value=${this.draftLineNo}
              @input=${(e: Event) => (this.draftLineNo = (e.target as HTMLInputElement).value)}
            />
          </label>
          <label class="field">
            <span class="label">New quantity</span>
            <input
              size="8"
              data-testid="change-raise-qty"
              .value=${this.draftQty}
              @input=${(e: Event) => (this.draftQty = (e.target as HTMLInputElement).value)}
            />
          </label>
          <label class="field">
            <span class="label">Reason</span>
            <input
              size="30"
              data-testid="change-raise-reason"
              .value=${this.draftReason}
              @input=${(e: Event) => (this.draftReason = (e.target as HTMLInputElement).value)}
            />
          </label>
          <button data-testid="change-raise" ?disabled=${this.busy} @click=${() => void this.raise()}>
            Raise change
          </button>
        </div>
        ${this.changes.length === 0
          ? html`<div class="empty">No change orders on this order.</div>`
          : this.changes.map((co) => this.renderChange(co))}
      </div>
    `;
  }

  private renderChange(co: ChangeOrder) {
    return html`
      <div class="box" data-change-id=${co.id}>
        <div class="row">
          <strong>${co.change_no}</strong>
          <span class="${changeStatusChip(co.status)}" data-change-status>${changeStatusWord(co.status)}</span>
          <span class="label">delta</span>
          <span data-change-delta>${formatMoney(co.delta_cents)}</span>
          ${co.finance_required
            ? html`<span class="chip amber" data-finance-required>FINANCE REQUIRED</span>`
            : nothing}
          <span class="spacer"></span>
          ${offeredChangeActions(co).map(
            (a) => html`
              <button
                class="${a === ACTION_APPLY ? 'primary' : a === ACTION_REJECT ? 'danger' : ''}"
                data-change-action=${a}
                ?disabled=${this.busy || !canDoChangeAction(co, a)}
                @click=${() => void this.act(co, a)}
              >
                ${changeActionLabel(a)}
              </button>
            `,
          )}
        </div>
        <div class="stage" data-change-stage>${changeStageSummary(co)}</div>
        ${co.reason ? html`<div class="label">"${co.reason}"</div>` : nothing}
        ${offeredChangeActions(co).length > 0
          ? html`
              <div class="form">
                <label class="field">
                  <span class="label">Note (optional)</span>
                  <input
                    size="30"
                    data-testid="change-note"
                    .value=${this.note}
                    @input=${(e: Event) => (this.note = (e.target as HTMLInputElement).value)}
                  />
                </label>
              </div>
            `
          : nothing}
        <table>
          <thead>
            <tr>
              <th>Change</th>
              <th class="num">Money</th>
            </tr>
          </thead>
          <tbody>
            ${co.lines.map(
              (l) => html`
                <tr>
                  <td>${describeDelta(l)}</td>
                  <td class="num">${formatMoney(l.line_delta_cents)}</td>
                </tr>
              `,
            )}
          </tbody>
        </table>
      </div>
    `;
  }

  private renderReservations() {
    const h = this.holds;
    return html`
      <div class="box" data-testid="change-reservations">
        <div class="titlerow">
          <h3>Stock held by this order</h3>
          <details class="info">
            <summary aria-label="What this reservation list shows">what this shows</summary>
            <p>
              Only the lines this order is actively holding stock against, not the whole order.
              Lines that do not track inventory, and lines already shipped, never appear here.
            </p>
          </details>
        </div>
        ${h && !h.seam_wired
          ? html`<div class="warn">
              Reservations are not being maintained on this deployment: this order holds nothing.
            </div>`
          : nothing}
        ${!h || h.reservations.length === 0
          ? html`<div class="empty">No stock is currently held for this order.</div>`
          : html`
              <table>
                <thead>
                  <tr>
                    <th>Product</th>
                    <th>SKU</th>
                    <th>Hold</th>
                    <th class="num">Qty</th>
                  </tr>
                </thead>
                <tbody>
                  ${h.reservations.map(
                    (r) => html`
                      <tr>
                        <td>${r.product_name || r.product_id}</td>
                        <td>${r.sku}</td>
                        <td><span class="${r.hold_mode === 'HARD' ? 'chip green' : 'chip'}">${r.hold_mode}</span></td>
                        <td class="num" data-hold-qty>${formatQty(r.qty_base)}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            `}
      </div>
    `;
  }

  /** The embedded line edit changed the order: carry the word out of this shadow root so the sold
   *  card and the sibling order panels refresh exactly as they did when the edit was a sibling. */
  private forwardOrderChanged = (e: Event): void => {
    e.stopPropagation();
    this.dispatchEvent(
      new CustomEvent('order-changed', {
        detail: { orderId: this.orderId },
        bubbles: true,
        composed: true,
      }),
    );
  };
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-order-change': HosOrderChange;
  }
}
