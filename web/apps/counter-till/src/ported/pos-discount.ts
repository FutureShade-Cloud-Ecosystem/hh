import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import {
  listDiscountApprovers,
  type CartDiscount,
  type DiscountApprover,
} from '@hardscapehouse/pos-kit/src/ported/pos-api.js';
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';
import { ModalFocus } from '@hardscapehouse/pos-kit/src/ported/modal-focus.js';
import { parseDiscountEntry, percentText, type DiscountMode } from './_pos-discount.js';

/**
 * The counter's Discount dialog (row 1047 item 3), for one ticket line or for the whole ticket.
 *
 * At the counter a salesperson gives a discount only after a manager says yes in person. The till
 * keeps that verbal, no approval queue and no second login, and records the yes: the dialog will
 * not apply a discount until the person names the manager who approved it, picked from the list
 * the ring itself judges approved_by against (GET /pos/discount-approvers). The value is a percent
 * or dollars; the cents it takes off are the server's, shown on the ticket once the preview
 * answers, never worked out here.
 *
 * Parent-mounted, so connected IS open (the relief dialog's shape): Escape, Cancel and the scrim
 * abandon it, Apply hands the discount to the terminal, Remove (only when one stands) clears it.
 */
@customElement('hos-pos-discount')
export class HosPosDiscount extends LitElement {
  @property() branchId = '';
  /** What the discount is on, in words: "Holland paver" for a line, "the whole ticket". */
  @property() subject = '';
  /** The discount already on the line or ticket, to edit or remove. */
  @property({ attribute: false }) existing: CartDiscount | null = null;

  readonly modalFocus = new ModalFocus(this, {
    container: () => this.renderRoot.querySelector<HTMLElement>('.hos-dialog'),
    onEscape: () => this.close(),
  });

  @state() private mode: DiscountMode = 'percent';
  @state() private text = '';
  @state() private approverId = '';
  @state() private approvers: DiscountApprover[] = [];
  @state() private loaded: 'loading' | 'ready' | 'failed' = 'loading';
  @state() private touched = false;

  static styles = [
    focusRing,
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
        z-index: 50;
      }
      .card {
        background: var(--hos-slate-warm);
        border: 1px solid var(--hos-line);
        border-radius: 14px;
        width: min(460px, 92vw);
        max-height: 90vh;
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
      .subject {
        font-size: 13px;
        color: var(--hos-text-dim);
        margin-bottom: 4px;
      }
      .notice {
        font-size: 12px;
        color: var(--hos-text-dim);
        background: var(--hos-deep-earth);
        border: 1px solid var(--hos-line);
        border-radius: 8px;
        padding: 10px 12px;
        margin: 10px 0 14px;
      }
      label,
      .lab {
        display: block;
        font-size: 11px;
        text-transform: uppercase;
        letter-spacing: 0.08em;
        color: var(--hos-text-dim);
        margin: 14px 0 4px;
      }
      .modes {
        display: flex;
        gap: 0;
        border: 1px solid var(--hos-line);
        border-radius: 8px;
        overflow: hidden;
        width: max-content;
      }
      .modes button {
        background: transparent;
        color: var(--hos-text-dim);
        border: 0;
        border-radius: 0;
        padding: 8px 16px;
        font-weight: 600;
        font-family: inherit;
        cursor: pointer;
      }
      .modes button[aria-checked='true'] {
        background: var(--hos-stone-amber);
        color: #0b1725;
      }
      input,
      select {
        width: 100%;
        box-sizing: border-box;
        background: var(--hos-field);
        color: var(--hos-text);
        border: 1px solid var(--hos-line);
        border-radius: 7px;
        padding: 9px 11px;
        font-size: 14px;
        font-family: inherit;
      }
      input {
        font-family: var(--hos-font-mono);
      }
      .actions {
        display: flex;
        justify-content: space-between;
        gap: 10px;
        margin-top: 18px;
      }
      .right {
        display: flex;
        gap: 10px;
        margin-left: auto;
      }
      .actions button {
        background: var(--hos-stone-amber);
        color: #0b1725;
        border: 0;
        border-radius: 8px;
        padding: 9px 16px;
        font-weight: 600;
        font-family: inherit;
        cursor: pointer;
      }
      .actions button.ghost {
        background: transparent;
        color: var(--hos-text-dim);
        border: 1px solid var(--hos-line);
      }
      .actions button.danger {
        background: transparent;
        color: var(--hos-safety-red);
        border: 1px solid var(--hos-line);
      }
      .actions button:disabled {
        background: transparent;
        border: 1px solid var(--hos-line);
        color: var(--hos-text-dim);
        opacity: 1;
        cursor: not-allowed;
      }
      .err {
        color: var(--hos-safety-red);
        font-size: 12px;
        margin-top: 6px;
      }
      .hint {
        color: var(--hos-text-dim);
        font-size: 12px;
        margin-top: 6px;
      }
    `,
  ];

  connectedCallback(): void {
    super.connectedCallback();
    if (this.existing) {
      this.mode = this.existing.percentBps !== undefined ? 'percent' : 'amount';
      this.text =
        this.existing.percentBps !== undefined
          ? percentText(this.existing.percentBps)
          : (Math.floor((this.existing.amountCents ?? 0) / 100) + '.' + String((this.existing.amountCents ?? 0) % 100).padStart(2, '0'));
      this.approverId = this.existing.approvedBy;
    }
    void this.loadApprovers();
  }

  private async loadApprovers(): Promise<void> {
    try {
      this.approvers = await listDiscountApprovers(this.branchId);
      this.loaded = 'ready';
    } catch {
      this.loaded = 'failed';
    }
  }

  private close(): void {
    this.dispatchEvent(new CustomEvent('pos-discount-close', { bubbles: true, composed: true }));
  }

  private entry() {
    return parseDiscountEntry(this.mode, this.text);
  }

  private chosen(): DiscountApprover | undefined {
    return this.approvers.find((a) => a.id === this.approverId);
  }

  private canApply(): boolean {
    return this.entry().ok && this.chosen() !== undefined;
  }

  private apply(): void {
    this.touched = true;
    const entry = this.entry();
    const who = this.chosen();
    if (!entry.ok || !who) return;
    const discount = { ...entry.discount, approvedBy: who.id, approvedByName: who.name } as CartDiscount;
    this.dispatchEvent(new CustomEvent('pos-discount-apply', { detail: discount, bubbles: true, composed: true }));
  }

  private removeDiscount(): void {
    this.dispatchEvent(new CustomEvent('pos-discount-remove', { bubbles: true, composed: true }));
  }

  private setMode(mode: DiscountMode): void {
    if (mode === this.mode) return;
    this.mode = mode;
    this.text = '';
    this.touched = false;
  }

  render() {
    const entry = this.entry();
    const showEntryError = this.touched && !entry.ok;
    return html`<div class="hos-dialog" data-testid="discount-dialog" @click=${(e: Event) => e.target === e.currentTarget && this.close()}>
      <div class="card" role="dialog" aria-modal="true" aria-label="Discount">
        <h2>Discount</h2>
        <div class="subject" data-testid="discount-subject">${this.subject}</div>
        <div class="notice">
          A discount is given only after a manager says yes. Name the manager who approved it: it is recorded on the sale.
        </div>

        <div class="lab" id="discount-mode-label">Take off</div>
        <div class="modes" role="radiogroup" aria-labelledby="discount-mode-label">
          <button
            type="button"
            role="radio"
            aria-checked=${this.mode === 'percent' ? 'true' : 'false'}
            data-testid="discount-mode-percent"
            @click=${() => this.setMode('percent')}
          >
            Percent
          </button>
          <button
            type="button"
            role="radio"
            aria-checked=${this.mode === 'amount' ? 'true' : 'false'}
            data-testid="discount-mode-amount"
            @click=${() => this.setMode('amount')}
          >
            Dollars
          </button>
        </div>

        <label for="discount-value">${this.mode === 'percent' ? 'Percent off' : 'Dollars off'}</label>
        <input
          id="discount-value"
          data-testid="discount-value"
          inputmode="decimal"
          autocomplete="off"
          placeholder=${this.mode === 'percent' ? 'for example 10' : 'for example 25.00'}
          .value=${this.text}
          @input=${(e: Event) => {
            this.text = (e.target as HTMLInputElement).value;
            this.touched = false;
          }}
          @keydown=${(e: KeyboardEvent) => e.key === 'Enter' && this.canApply() && this.apply()}
        />
        ${showEntryError && !entry.ok ? html`<div class="err" data-testid="discount-entry-error">${entry.words}</div>` : nothing}

        <label for="discount-approver">Approved by</label>
        ${this.loaded === 'failed'
          ? html`<div class="err" data-testid="discount-approvers-failed">
              The list of managers could not be read. Close this and try again; a discount needs the manager who approved it.
            </div>`
          : this.loaded === 'ready' && this.approvers.length === 0
            ? html`<div class="err" data-testid="discount-approvers-none">
                No manager is set up to approve a discount at this branch.
              </div>`
            : html`<select
                id="discount-approver"
                data-testid="discount-approver"
                required
                aria-required="true"
                ?disabled=${this.loaded !== 'ready'}
                @change=${(e: Event) => (this.approverId = (e.target as HTMLSelectElement).value)}
              >
                <option value="" ?selected=${this.approverId === ''}>
                  ${this.loaded === 'loading' ? 'Loading managers…' : 'Choose the manager who said yes'}
                </option>
                ${this.approvers.map(
                  (a) => html`<option value=${a.id} ?selected=${a.id === this.approverId}>${a.name} · ${a.role_label}</option>`,
                )}
              </select>`}
        ${this.chosen() === undefined && this.loaded === 'ready' && this.approvers.length > 0
          ? html`<div class="hint">Required.</div>`
          : nothing}

        <div class="actions">
          ${this.existing
            ? html`<button type="button" class="danger" data-testid="discount-remove" @click=${() => this.removeDiscount()}>Remove discount</button>`
            : nothing}
          <div class="right">
            <button type="button" class="ghost" data-testid="discount-cancel" @click=${() => this.close()}>Cancel</button>
            <button type="button" data-testid="discount-apply" ?disabled=${!this.canApply()} @click=${() => this.apply()}>
              Apply discount
            </button>
          </div>
        </div>
      </div>
    </div>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-discount': HosPosDiscount;
  }
}
