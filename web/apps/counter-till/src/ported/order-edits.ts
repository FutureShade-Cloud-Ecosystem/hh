// Ported from hardscapeos_dibbits f8f4973db317b92296ae8ec7e62a3f54f5dc82ab app/src/order-edits.ts; adapted for HH.

// DIB-175 order change-history viewer. <hos-order-edits order-id=…> renders WHO changed WHAT on an
// order, from what to what, and when: the write-side companion to <hos-order-history>'s status
// timeline, backed by the staff-gated read that reconstructs the trail from the append-only audit
// log:
//   GET /api/v1/orders/{id}/edits
//
// One operator action emits several audit rows (a qty edit moves qty_entered, qty_base,
// line_total_cents and the order totals), correlated by the audit request id. They are grouped here
// so an edit reads as ONE entry with its consequences nested, rather than five unrelated history
// lines.
//
// Same panel family as <hos-order-history>: same api() helper (pos-kit's port seam), same tokens,
// same table shape.
import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { api } from '@hardscapehouse/pos-kit/src/ported/ports.js';
import {
  groupChanges,
  summariseGroup,
  fieldLabel,
  formatChangeValue,
  changeActor,
  type OrderEditHistory,
  type OrderFieldChange,
} from './_order-edit.js';
import { whenWord } from './field-refusal.js';
import { rowStripe } from './styles/row-stripe.js';

@customElement('hos-order-edits')
export class HosOrderEdits extends LitElement {
  static styles = [
    rowStripe,
    css`
    :host {
      display: block;
      font-family: var(--hos-font-ui);
      color: var(--hos-text);
      font-size: 13px;
    }
    .box {
      background: var(--hos-deep-earth, transparent);
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
    }
    .mono {
      font-family: var(--hos-font-mono);
    }
    .dim {
      color: var(--hos-text-dim);
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
      vertical-align: baseline;
    }
    tr:last-child td {
      border-bottom: none;
    }
    .flow {
      font-family: var(--hos-font-mono);
      white-space: nowrap;
    }
    .arrow {
      color: var(--hos-text-dim);
      padding: 0 4px;
    }
    .also {
      display: block;
      font-size: 11px;
      color: var(--hos-text-dim);
      margin-top: 2px;
    }
    .msg {
      color: var(--hos-text-dim);
      padding: 6px 0;
    }
    .err {
      color: var(--hos-safety-red);
    }
  `,
  ];

  @property({ attribute: 'order-id' }) orderId = '';

  @state() private history: OrderEditHistory | null = null;
  @state() private error = '';
  @state() private loading = false;
  private lastKey = '';

  willUpdate(changed: Map<string, unknown>) {
    if (changed.has('orderId')) void this.load();
  }

  /**
   * Re-reads the trail. load() short-circuits when the order id is unchanged, so clearing lastKey
   * is what makes an explicit refresh actually re-fetch: the same reload() contract
   * <hos-order-history> exposes, and what lets an edit update this panel with no page reload.
   */
  reload(): void {
    this.lastKey = '';
    void this.load();
  }

  private async load(): Promise<void> {
    if (this.orderId === this.lastKey) return;
    this.lastKey = this.orderId;
    this.history = null;
    this.error = '';
    if (!this.orderId) return;
    this.loading = true;
    try {
      const res = await api(`/api/v1/orders/${encodeURIComponent(this.orderId)}/edits`);
      if (!res.ok) {
        let msg = `request failed (${res.status})`;
        try {
          const body = (await res.json()) as { error?: string };
          if (body.error) msg = body.error;
        } catch {
          // non-JSON error body: keep the default
        }
        throw new Error(msg);
      }
      this.history = (await res.json()) as OrderEditHistory;
    } catch (e) {
      this.error = e instanceof Error ? e.message : 'Could not load the change history.';
    } finally {
      this.loading = false;
    }
  }

  render() {
    if (this.loading) return html`<div class="box msg">Loading change history…</div>`;
    if (this.error) return html`<div class="box msg err" role="alert">${this.error}</div>`;
    if (!this.history) return nothing;
    return this.renderHistory(this.history);
  }

  private renderHistory(h: OrderEditHistory) {
    const groups = groupChanges(h.changes ?? []);
    return html`
      <div class="box" data-testid="order-edits">
        <div class="hdr">
          <span>Change history</span>
          <span class="dim mono">${h.order_id.slice(0, 8)}</span>
        </div>
        ${groups.length === 0
          ? html`<div class="msg">No edits recorded yet.</div>`
          : html`
              <div class="scroll">
                <table>
                  <thead>
                    <tr>
                      <th>Change</th>
                      <th>Line</th>
                      <th>By</th>
                      <th>When</th>
                    </tr>
                  </thead>
                  <tbody>
                    ${groups.map((g) => this.renderGroup(g))}
                  </tbody>
                </table>
              </div>
            `}
      </div>
    `;
  }

  /** One operator action: the headline change, with the columns it also moved noted beneath. */
  private renderGroup(group: OrderFieldChange[]) {
    const lead = summariseGroup(group);
    if (!lead) return nothing;
    const rest = group.filter((c) => c !== lead);
    return html`
      <tr>
        <td>
          <span class="flow"
            >${fieldLabel(lead.field)} ${formatChangeValue(lead.field, lead.before)}<span class="arrow"
              >to</span
            >${formatChangeValue(lead.field, lead.after)}</span
          >
          ${rest.length > 0
            ? html`<span class="also"
                >also ${rest.map(
                  (c, i) =>
                    html`${i > 0 ? ', ' : ''}${fieldLabel(c.field)}
                    ${formatChangeValue(c.field, c.before)}to${formatChangeValue(c.field, c.after)}`,
                )}</span
              >`
            : nothing}
        </td>
        <td class="mono dim">${lead.line_no > 0 ? lead.line_no : '(none)'}</td>
        <td>${changeActor(lead)}</td>
        <td>${whenWord(lead.occurred_at)}</td>
      </tr>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-order-edits': HosOrderEdits;
  }
}
