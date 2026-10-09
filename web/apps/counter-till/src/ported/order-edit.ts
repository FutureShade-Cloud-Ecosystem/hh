// Ported from hardscapeos_dibbits f8f4973db317b92296ae8ec7e62a3f54f5dc82ab app/src/order-edit.ts; adapted for HH.

// DIB-175 staff order-edit UI. <hos-order-edit order-id=…> renders an order's lines with the edit
// controls the SERVER says this caller may use right now, plus the requested-date field.
//
// Nothing here evaluates the edit-state matrix. `editable_fields` on GET /api/v1/orders/{id} is
// computed server-side from editRules (core order/edit.go), the same map the write path enforces,
// so a control can never be offered for an edit the server would refuse, and a ratified matrix
// change needs no client redeploy. This is the same discipline <hos-order-lifecycle> uses for
// legal_actions.
//
// It sits in the same panel family as <hos-order-lifecycle> and <hos-order-history>: same api()
// helper (pos-kit's port seam), same tokens, same self-contained bordered box.
//
// HH adaptations: the reference's requested-date control rendered whatever the read carried; the
// HH read (OrderView) serves no requested_date value, so the date row renders only the control and
// starts empty (an unset date is honest, a guessed one is not). The money figures go through the
// app's one money door (src/money.ts), whose unknown figure is the explicit placeholder, never a
// confident zero.
import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { api } from '@hardscapehouse/pos-kit/src/ported/ports.js';
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';
import { formatMoney } from "../money.js";
import {
  canEdit,
  hasAnyEdit,
  validateQty,
  validateDate,
  EDIT_LINE_QTY,
  EDIT_LINE_ADD,
  EDIT_LINE_REMOVE,
  EDIT_REQUESTED_DATE,
  type OrderView,
} from './_order-edit.js';
import { orderStatusWord } from './order-logic.js';
import { rowStripe } from './styles/row-stripe.js';

/** One line as the staff surface renders it (core order.OrderViewLine). */
interface OrderLine {
  line_no: number;
  product_id: string;
  description: string;
  qty_entered: string;
  uom_entered: string;
  qty_base: string;
  allocated_base: string;
  fulfilled_base: string;
  unit_price_cents: number;
  line_total_cents: number;
}

type FullView = OrderView & {
  lines?: OrderLine[];
  subtotal_cents?: number;
  tax_cents?: number;
  total_cents?: number;
};

/** The server's own sentence for a refused call, or a plain default when the body says nothing. */
async function errMessage(res: Response, what: string): Promise<string> {
  let message = '';
  try {
    const body = (await res.json()) as { error?: string };
    if (typeof body?.error === 'string' && body.error !== '') message = body.error;
  } catch {
    // a body that is not JSON keeps the default sentence
  }
  return message !== '' ? message : `the server refused to ${what} (${res.status})`;
}

@customElement('hos-order-edit')
export class HosOrderEdit extends LitElement {
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
    .box {
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control, 8px);
      padding: 10px 12px;
      margin-top: 8px;
    }
    .hdr {
      display: flex;
      justify-content: space-between;
      gap: 8px;
      flex-wrap: wrap;
      align-items: center;
    }
    .dim {
      color: var(--hos-text-dim);
    }
    .mono {
      font-family: var(--hos-font-mono);
    }
    .scroll {
      overflow-x: auto;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      margin-top: 8px;
    }
    th {
      text-align: left;
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      padding: 4px 8px 4px 0;
      border-bottom: 1px solid var(--hos-line);
      font-weight: 500;
      white-space: nowrap;
    }
    td {
      padding: 5px 8px 5px 0;
      border-bottom: 1px solid var(--hos-line);
      vertical-align: middle;
    }
    tr:last-child td {
      border-bottom: none;
    }
    input {
      font: inherit;
      background: transparent;
      color: var(--hos-text);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control, 8px);
      padding: 3px 6px;
      box-sizing: border-box;
    }
    input.qty {
      width: 7rem;
      font-family: var(--hos-font-mono);
    }
    button {
      font: inherit;
      padding: 3px 9px;
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
    button.danger {
      color: var(--hos-safety-red);
      border-color: var(--hos-safety-red);
    }
    .err {
      color: var(--hos-safety-red);
      margin-top: 6px;
    }
    .ok {
      color: var(--hos-success);
      margin-top: 6px;
    }
    .row {
      display: flex;
      align-items: center;
      gap: 8px;
      flex-wrap: wrap;
      margin-top: 10px;
    }
    .totals {
      margin-top: 8px;
      font-family: var(--hos-font-mono);
    }
    .addrow {
      display: flex;
      gap: 6px;
      flex-wrap: wrap;
      align-items: center;
      margin-top: 10px;
      padding-top: 10px;
      border-top: 1px dashed var(--hos-line);
    }
  `,
  ];

  /** The order to render. Changing it reloads. */
  @property({ attribute: 'order-id' }) orderId = '';

  @state() private view: FullView | null = null;
  @state() private error = '';
  @state() private notice = '';
  @state() private busy = false;
  /** Per-line pending quantity, keyed by line_no, so typing in one row never disturbs another. */
  @state() private qtyDraft: Record<number, string> = {};
  @state() private dateDraft = '';
  @state() private addProduct = '';
  @state() private addQty = '';
  @state() private addUom = '';

  willUpdate(changed: Map<string, unknown>): void {
    if (changed.has('orderId')) void this.load();
  }

  /** Re-reads the order. Public so a parent can refresh the panel after an external change. */
  reload(): void {
    void this.load();
  }

  private async load(): Promise<void> {
    if (!this.orderId) {
      this.view = null;
      return;
    }
    this.error = '';
    try {
      const res = await api(`/api/v1/orders/${encodeURIComponent(this.orderId)}`);
      if (!res.ok) {
        this.view = null;
        // 404 is the ordinary "no such order" of the lookup box, not a fault worth shouting about.
        this.error = res.status === 404 ? '' : await errMessage(res, 'load this order');
        return;
      }
      const v = (await res.json()) as FullView;
      this.view = v;
      // Reset the drafts to the server's truth, so a reload after an edit shows what actually
      // landed rather than what the operator typed.
      this.qtyDraft = {};
      this.dateDraft = '';
    } catch {
      this.view = null;
      this.error = 'Could not reach the server.';
    }
  }

  /**
   * Sends one edit and re-reads. Every write re-loads on BOTH success and failure: a 409 usually
   * means the order moved underneath this panel (someone marked it READY), and the controls must
   * reflect the server's new truth rather than keep offering an edit that will now always fail.
   */
  private async send(path: string, init: RequestInit, what: string): Promise<boolean> {
    if (this.busy) return false;
    this.busy = true;
    this.error = '';
    this.notice = '';
    try {
      const res = await api(path, init);
      if (!res.ok) {
        this.error = await errMessage(res, what);
        await this.load();
        return false;
      }
      this.notice = 'Saved.';
      await this.load();
      // Tell the surface an edit landed, so the sibling change-history and status panels refresh
      // WITHOUT a page reload.
      this.dispatchEvent(
        new CustomEvent('order-changed', {
          detail: { orderId: this.orderId },
          bubbles: true,
          composed: true,
        }),
      );
      return true;
    } catch {
      this.error = 'Could not reach the server.';
      return false;
    } finally {
      this.busy = false;
    }
  }

  private async saveQty(line: OrderLine): Promise<void> {
    const raw = this.qtyDraft[line.line_no];
    if (raw === undefined || raw.trim() === line.qty_entered.trim()) return; // nothing to save
    const invalid = validateQty(raw);
    if (invalid) {
      this.error = invalid;
      return;
    }
    await this.send(
      `/api/v1/orders/${encodeURIComponent(this.orderId)}/lines/${line.line_no}`,
      { method: 'PATCH', body: JSON.stringify({ qty_entered: raw.trim() }) },
      'change this quantity',
    );
  }

  private async removeLine(line: OrderLine): Promise<void> {
    await this.send(
      `/api/v1/orders/${encodeURIComponent(this.orderId)}/lines/${line.line_no}`,
      { method: 'DELETE' },
      'remove this line',
    );
  }

  private async addLine(): Promise<void> {
    const invalid = validateQty(this.addQty);
    if (invalid) {
      this.error = invalid;
      return;
    }
    if (this.addProduct.trim() === '' || this.addUom.trim() === '') {
      this.error = 'A product id and a unit of measure are required.';
      return;
    }
    const ok = await this.send(
      `/api/v1/orders/${encodeURIComponent(this.orderId)}/lines`,
      {
        method: 'POST',
        body: JSON.stringify({
          product_id: this.addProduct.trim(),
          qty_entered: this.addQty.trim(),
          uom_entered: this.addUom.trim(),
        }),
      },
      'add this line',
    );
    if (ok) {
      this.addProduct = '';
      this.addQty = '';
      this.addUom = '';
    }
  }

  private async saveDate(): Promise<void> {
    const invalid = validateDate(this.dateDraft);
    if (invalid) {
      this.error = invalid;
      return;
    }
    await this.send(
      `/api/v1/orders/${encodeURIComponent(this.orderId)}`,
      { method: 'PATCH', body: JSON.stringify({ requested_date: this.dateDraft.trim() }) },
      'change the requested date',
    );
  }

  render() {
    if (!this.orderId) return nothing;
    if (!this.view) {
      return this.error ? html`<div class="box err" role="alert">${this.error}</div>` : nothing;
    }
    // Nothing editable: render nothing at all rather than an empty panel. A READY order shows only
    // the date row; a FULFILLED one shows no edit panel, which is the ratified matrix made visible.
    if (!hasAnyEdit(this.view)) return nothing;

    const lines = this.view.lines ?? [];
    const mayEditQty = canEdit(this.view, EDIT_LINE_QTY);
    const mayRemove = canEdit(this.view, EDIT_LINE_REMOVE);
    const mayAdd = canEdit(this.view, EDIT_LINE_ADD);
    const mayDate = canEdit(this.view, EDIT_REQUESTED_DATE);

    return html`
      <div class="box" data-testid="order-edit">
        <div class="hdr">
          <span>Edit lines</span>
          <span class="dim mono">${orderStatusWord(this.view.status)}</span>
        </div>

        ${lines.length > 0 && (mayEditQty || mayRemove)
          ? html`
              <div class="scroll">
                <table>
                  <thead>
                    <tr>
                      <th>#</th>
                      <th>Description</th>
                      <th>Qty</th>
                      <th>UOM</th>
                      <th>Unit</th>
                      <th>Line total</th>
                      <th></th>
                    </tr>
                  </thead>
                  <tbody>
                    ${lines.map(
                      (l) => html`
                        <tr>
                          <td class="mono">${l.line_no}</td>
                          <td>${l.description || html`<span class="dim">(no description)</span>`}</td>
                          <td>
                            ${mayEditQty
                              ? html`<input
                                  class="qty"
                                  data-testid="order-edit-qty"
                                  .value=${this.qtyDraft[l.line_no] ?? l.qty_entered}
                                  ?disabled=${this.busy}
                                  @input=${(e: Event) => {
                                    this.qtyDraft = {
                                      ...this.qtyDraft,
                                      [l.line_no]: (e.target as HTMLInputElement).value,
                                    };
                                  }}
                                  @keydown=${(e: KeyboardEvent) => {
                                    if (e.key === 'Enter') void this.saveQty(l);
                                  }}
                                  @blur=${() => void this.saveQty(l)}
                                />`
                              : html`<span class="mono">${l.qty_entered}</span>`}
                          </td>
                          <td class="mono dim">${l.uom_entered}</td>
                          <td class="mono">${formatMoney(l.unit_price_cents)}</td>
                          <td class="mono">${formatMoney(l.line_total_cents)}</td>
                          <td>
                            ${mayRemove
                              ? html`<button
                                  class="danger"
                                  data-testid="order-edit-remove"
                                  ?disabled=${this.busy}
                                  @click=${() => void this.removeLine(l)}
                                >
                                  Remove
                                </button>`
                              : nothing}
                          </td>
                        </tr>
                      `,
                    )}
                  </tbody>
                </table>
              </div>
              <div class="totals dim">
                Subtotal ${formatMoney(this.view.subtotal_cents)} · Tax ${formatMoney(this.view.tax_cents)} ·
                Total ${formatMoney(this.view.total_cents)}
              </div>
            `
          : nothing}

        ${mayAdd
          ? html`
              <div class="addrow">
                <input
                  style="min-width: 20rem"
                  placeholder="Add line: product id (uuid)"
                  data-testid="order-edit-add-product"
                  .value=${this.addProduct}
                  ?disabled=${this.busy}
                  @input=${(e: Event) => (this.addProduct = (e.target as HTMLInputElement).value)}
                />
                <input
                  class="qty"
                  placeholder="Qty"
                  data-testid="order-edit-add-qty"
                  .value=${this.addQty}
                  ?disabled=${this.busy}
                  @input=${(e: Event) => (this.addQty = (e.target as HTMLInputElement).value)}
                />
                <input
                  style="width: 6rem"
                  placeholder="UOM"
                  data-testid="order-edit-add-uom"
                  .value=${this.addUom}
                  ?disabled=${this.busy}
                  @input=${(e: Event) => (this.addUom = (e.target as HTMLInputElement).value)}
                />
                <button data-testid="order-edit-add" ?disabled=${this.busy} @click=${() => void this.addLine()}>
                  Add line
                </button>
              </div>
            `
          : nothing}

        ${mayDate
          ? html`
              <div class="row">
                <span class="dim">Requested date</span>
                <input
                  type="date"
                  data-testid="order-edit-date"
                  .value=${this.dateDraft}
                  ?disabled=${this.busy}
                  @input=${(e: Event) => (this.dateDraft = (e.target as HTMLInputElement).value)}
                />
                <button data-testid="order-edit-date-save" ?disabled=${this.busy} @click=${() => void this.saveDate()}>
                  Save date
                </button>
              </div>
            `
          : nothing}

        ${this.error ? html`<div class="err" role="alert" data-testid="order-edit-error">${this.error}</div>` : nothing}
        ${this.notice && !this.error ? html`<div class="ok" role="status" data-testid="order-edit-saved">${this.notice}</div>` : nothing}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-order-edit': HosOrderEdit;
  }
}
