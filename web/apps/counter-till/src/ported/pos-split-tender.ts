// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-split-tender.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';
import { drawIcon, iconForId } from '@hardscapehouse/pos-kit/src/ported/_icon-consumer.js';
import { plainDollars } from '@hardscapehouse/pos-kit/src/ported/_money-format.js'; // HH note: the display spelling lives in the named helper (CLOSURE.md)
import {
  evaluateSplit,
  autoFillRemainder,
  parseAmountCents,
  splitDraftRead,
  fmtCents,
  toPostBody,
  SPLIT_METHODS,
  MAX_SPLIT_LEGS,
  type DraftLeg,
} from './_pos-split.js';

/**
 * hos-pos-split-tender: DIB-165. Settle ONE sale across several tenders.
 *
 * THE COUNTER PROBLEM. A contractor pays $400 cash out of the truck float, puts $600 on the company
 * card and leaves the rest on account. Before this, the cashier rang three sales: three orders,
 * three invoices, three receipts for one pile of stone: and a return against any of them had no
 * single sale to reverse.
 *
 * THE TOTAL IS THE SERVER'S (rows 661 and 985). The till computes no total of its own: the terminal
 * passes the total the sale-preview stated, and only a current one. With it the panel states Total to
 * split and Left to assign, fills the last leg with the remainder, and arms Ring only when the legs
 * equal it. With none (the preview pending, unreadable, or stale after the ticket changed) the panel
 * says the total is pending and Ring stays disabled: it never compares legs against nothing. The
 * server still refuses a split whose legs miss its total, and migration 0100's deferred trigger
 * refuses it again at COMMIT. The arithmetic lives in pos-kit's evaluateSplit and autoFillRemainder.
 *
 * The panel emits `pos-split-ring` with the wire-shaped legs; the terminal owns the actual POST, so
 * there is exactly one place in the app that rings a sale.
 */
/** The tender names a person reads: Account, Card, Cash (the wire words stay upper case). */
const METHOD_LABELS: Record<string, string> = { ACCOUNT: 'Account', CARD: 'Card', CASH: 'Cash', CHEQUE: 'Cheque' };
const METHOD_ORDER: DraftLeg['method'][] = SPLIT_METHODS.slice().sort((a, b) => (METHOD_LABELS[a] ?? a).localeCompare(METHOD_LABELS[b] ?? b));

@customElement('hos-pos-split-tender')
export class HosPosSplitTender extends LitElement {
  /**
   * The sale total the legs must settle, in cents. The terminal passes null
   * before the ring (the only state it has): no remaining is computed and no
   * leg is auto-filled against a browser figure. A server-stated total (the
   * rate-serving lane's return) restores the live remainder.
   */
  @property({ type: Number, attribute: 'total-cents' }) totalCents: number | null = 0;
  /** Whether an account is selected: an ACCOUNT leg needs one. */
  @property({ type: Boolean, attribute: 'has-customer' }) hasCustomer = false;
  /** Whether the ticket holds any line: an empty ticket cannot be split. */
  @property({ type: Boolean, attribute: 'has-lines' }) hasLines = true;
  /** Name of the selected account, for the on-screen confirmation of what gets charged. */
  @property({ attribute: 'customer-name' }) customerName = '';
  /** Disables the controls while the parent's ring is in flight. */
  @property({ type: Boolean }) busy = false;

  /**
   * Row 985: once the server has stated the total, the LAST leg follows the remainder until the
   * person types into it themselves (it stays an ordinary editable box). Typing in any other leg,
   * changing a method or the total, moves the last leg to what is left.
   */
  private lastLegTouched = false;

  // Two legs is the smallest legal split, so the panel opens ready to be typed into rather than
  // making the cashier click "add tender" twice before anything can happen.
  @state() private legs: { method: DraftLeg['method']; raw: string }[] = [
    { method: 'CASH', raw: '' },
    { method: 'CARD', raw: '' },
  ];

  static styles = [
    focusRing,
    css`
      :host {
        display: block;
        font-family: var(--hos-font-ui);
        color: var(--hos-text);
      }
      .panel {
        background: var(--hos-slate-warm);
        border: 1px solid var(--hos-line);
        border-radius: 12px;
        padding: 14px;
      }
      h3 {
        margin: 0 0 4px;
        font-size: 13px;
        text-transform: uppercase;
        letter-spacing: 0.09em;
        color: var(--hos-text-dim);
        font-weight: 600;
      }
 /* THE SALE TOTAL IS NOT THIS PANEL'S FACT, AND NEITHER IS THE REMAINDER. A.total rule used to
         paint the total here at 18px in the amber text role, ~200px under the totals card's own
         26px amber Total; the remainder took its place and now it is gone too (the ring states
         the settlement, so the browser compares the legs against nothing). What this panel ADDS
         is the typed sum, which is arithmetic over the operator's own entries, and the words. */
      .leg {
        display: flex;
        gap: 8px;
        align-items: center;
        margin-bottom: 8px;
      }
      select,
      input {
        background: var(--hos-deep-earth);
        color: var(--hos-text);
        border: 1px solid var(--hos-line);
        border-radius: 8px;
        padding: 8px 10px;
        font-size: 14px;
        font-family: inherit;
      }
      input.amt {
        flex: 1;
        min-width: 0;
        font-family: var(--hos-font-mono);
        text-align: right;
      }
      button {
        background: transparent;
        border: 1px solid var(--hos-line);
        color: var(--hos-text-dim);
        border-radius: 8px;
        padding: 7px 12px;
        font-size: 13px;
        font-family: inherit;
        cursor: pointer;
      }
      /* vertical-align: middle on the drawn glyph keeps the icon on the
      text middle and the line box the character left behind, so the button keeps its exact
      height (PR 55 review round; the quick-action-bar precedent centers, this keeps the box) */
      button svg {
        vertical-align: middle;
      }
      button:hover:not(:disabled) {
        border-color: var(--hos-stone-amber);
        color: var(--hos-stone-amber-text);
      }
      button:disabled {
        background: transparent;
        border-color: var(--hos-line);
        color: var(--hos-text-dim);
        opacity: 1;
        cursor: not-allowed;
      }
      button.ring {
        border-color: var(--hos-stone-amber);
        color: var(--hos-stone-amber-text);
        font-weight: 600;
        padding: 10px 18px;
      }
      /* HH note: disabled drops its fill and goes grey; fade is not a state. */
      button.ring:disabled {
        background: transparent;
        border-color: var(--hos-line);
        color: var(--hos-text-dim);
        opacity: 1;
        cursor: not-allowed;
      }
      button.ring:not(:disabled) {
        background: color-mix(in srgb, var(--hos-stone-amber) 14%, transparent);
      }
      .drop {
        padding: 7px 10px;
      }
      .foot {
        display: flex;
        justify-content: space-between;
        align-items: center;
        gap: 12px;
        margin-top: 12px;
        padding-top: 12px;
        border-top: 1px solid var(--hos-line);
      }
      .total {
        font-family: var(--hos-font-mono);
        font-size: 15px;
        margin-bottom: 2px;
      }
      .remaining {
        font-family: var(--hos-font-mono);
        font-size: 15px;
      }
      .remaining.settled {
        color: var(--hos-success);
      }
      .remaining.owing {
        color: var(--hos-stone-amber-text);
      }
      .remaining.over {
        color: var(--hos-safety-red);
      }
      .problem {
        font-size: 12px;
        color: var(--hos-text-dim);
        margin-top: 6px;
        min-height: 16px;
      }
      .acct {
        font-size: 11px;
        color: var(--hos-text-dim);
        margin-top: 2px;
      }
    `,
  ];

  /** draft maps the typed rows onto the pure arithmetic's input shape. */
  private draft(): DraftLeg[] {
    return this.legs.map((l) => ({ method: l.method, amountCents: parseAmountCents(l.raw) }));
  }

  private setMethod(i: number, method: DraftLeg['method']): void {
    const next = [...this.legs];
    next[i] = { ...next[i], method };
    this.legs = this.withLastFilled(next);
  }

  private setAmount(i: number, raw: string): void {
    const next = [...this.legs];
    next[i] = { ...next[i], raw };
    if (i === next.length - 1) this.lastLegTouched = true;
    this.legs = this.withLastFilled(next);
  }

  private addLeg(): void {
    if (this.legs.length >= MAX_SPLIT_LEGS) return;
    this.lastLegTouched = false;
    this.legs = this.withLastFilled([...this.legs, { method: 'CASH', raw: '' }]);
  }

  private removeLeg(i: number): void {
    // Two is the floor: dropping to one would silently turn a split into an ordinary sale with the
    // panel still open, which is a confusing state to be in when you press Ring.
    if (this.legs.length <= 2) return;
    this.lastLegTouched = false;
    this.legs = this.withLastFilled(this.legs.filter((_, n) => n !== i));
  }

  /** The legs with the last one set to the remainder, while a server total is stated and it is untouched. */
  private withLastFilled(legs: { method: DraftLeg['method']; raw: string }[]): { method: DraftLeg['method']; raw: string }[] {
    if (this.totalCents === null || this.lastLegTouched || legs.length === 0) return legs;
    const draft = legs.map((l) => ({ method: l.method, amountCents: parseAmountCents(l.raw) }));
    const last = legs.length - 1;
    const rest = autoFillRemainder(draft, this.totalCents, last);
    const out = [...legs];
    out[last] = { ...out[last], raw: rest > 0 ? plainDollars(rest) : '' };
    return out;
  }

  protected willUpdate(changed: Map<PropertyKey, unknown>): void {
    // A new total from the server (the lines, the customer or the fulfilment moved) re-fills the last leg.
    if (changed.has('totalCents')) this.legs = this.withLastFilled(this.legs);
  }

  /** fill puts exactly what is left into one leg: the counter's whole reason to trust this panel. */
  private fill(i: number): void {
    // A server-stated total is the only thing a leg fills against; pre-ring the
    // panel is handed none, and the Rest affordance does not render at all.
    if (this.totalCents === null) return;
    const cents = autoFillRemainder(this.draft(), this.totalCents ?? 0, i);
    this.setAmount(i, plainDollars(cents));
  }

  private ring(): void {
    const legs = this.draft();
    if (!this.reads(legs).armed) return;
    this.dispatchEvent(
      new CustomEvent('pos-split-ring', {
        detail: { tenders: toPostBody(legs) },
        bubbles: true,
        composed: true,
      }),
    );
  }

  private cancel(): void {
    this.dispatchEvent(new CustomEvent('pos-split-cancel', { bubbles: true, composed: true }));
  }

  /**
   * The panel's read of the draft, in one shape for both postures: pre-ring
   * (no total from the terminal) it is splitDraftRead's typed sum and shape
   * words, and the ring arms on the shape alone; with a server-stated total it
   * is evaluateSplit's live verdict, the remainder included.
   */
  private reads(legs: DraftLeg[]): { typedCents: number; remainingCents: number; armed: boolean; problem: string } {
    if (this.totalCents === null) {
      // No server total in hand (the preview is pending, unreadable, or stale after the ticket
      // changed): the legs are compared with nothing and Ring split sale stays disabled.
      const shape = splitDraftRead(legs, this.hasCustomer, this.hasLines);
      return {
        typedCents: shape.typedCents,
        remainingCents: 0,
        armed: false,
        problem: shape.problem === '' ? 'Ring split sale arms when the total arrives.' : shape.problem,
      };
    }
    const live = evaluateSplit(legs, this.totalCents, this.hasCustomer);
    return { typedCents: live.tenderedCents, remainingCents: live.remainingCents, armed: live.ready, problem: live.problem };
  }

  render() {
    const read = this.reads(this.draft());
    const remainingClass =
      read.remainingCents === 0 ? 'settled' : read.remainingCents > 0 ? 'owing' : 'over';
    return html`
      <div class="panel" data-testid="pos-split-panel">
        <h3>Split tender</h3>

        ${this.legs.map(
          (leg, i) => html`
            <div class="leg">
              <select
                aria-label="Tender ${i + 1} method"
                .value=${leg.method}
                ?disabled=${this.busy}
                @change=${(e: Event) => this.setMethod(i, (e.target as HTMLSelectElement).value as DraftLeg['method'])}
              >
                ${METHOD_ORDER.map((m) => html`<option value=${m} ?selected=${m === leg.method}>${METHOD_LABELS[m] ?? m}</option>`)}
              </select>
              <input
                class="amt"
                data-testid="split-amount-${i}"
                aria-label="Tender ${i + 1} amount"
                inputmode="decimal"
                placeholder="0.00"
                .value=${leg.raw}
                ?disabled=${this.busy}
                @input=${(e: Event) => this.setAmount(i, (e.target as HTMLInputElement).value)}
              />
              ${/* The Rest fill needs a total to fill toward, and the total is
                 the server's, at the ring: with none in hand the affordance
                 does not render, rather than a button that fills from a guess. */ ''}
              ${this.totalCents !== null
                ? html`<button
                    class="drop"
                    type="button"
                    title="Fill this tender with the remaining balance"
                    ?disabled=${this.busy}
                    @click=${() => this.fill(i)}
                  >
                    Rest
                  </button>`
                : nothing}
              ${/* The glyph is not a name. With no aria-label the button's accessible name was
                 "✕": a screen reader announced "✕ button" three times down a three-leg split with
                 nothing to tell them apart. The ticket row two panels away already does this
                 right (aria-label="Remove ${'$'}{l.product.name}"); the legs are numbered by their
                 own select and input labels, so the number is what names this one too. */ ''}
              <button
                class="drop"
                type="button"
                aria-label="Remove tender ${i + 1}"
                title="Remove this tender"
                ?disabled=${this.busy || this.legs.length <= 2}
                @click=${() => this.removeLeg(i)}
              >
                ${drawIcon(iconForId('pencil:close-m'), 14)}
              </button>
            </div>
            ${leg.method === 'ACCOUNT' && this.hasCustomer
              ? html`<div class="acct">on account: ${this.customerName || 'selected account'}</div>`
              : nothing}
          `,
        )}

        <button type="button" ?disabled=${this.busy || this.legs.length >= MAX_SPLIT_LEGS} @click=${this.addLeg}>
          ${drawIcon(iconForId('pencil:add'), 14)} Add tender
        </button>

        <div class="foot">
          <div>
            ${this.totalCents === null
              ? html`<div class="remaining" data-testid="split-total-pending">Total to split: pending</div>`
              : html`<div class="total" data-testid="split-total">Total to split ${fmtCents(this.totalCents)}</div>
                <div class="remaining ${remainingClass}" data-testid="split-remaining">
                  ${read.remainingCents < 0
                    ? `Over by ${fmtCents(-read.remainingCents)}`
                    : `Left to assign ${fmtCents(read.remainingCents)}`}
                </div>`}
            ${/* aria-live: the line changes on every keystroke and is the one thing a
               screen-reader user needs to hear without leaving the amount box. */ ''}
            <div class="problem" role="status" aria-live="polite" data-testid="split-problem">
              ${read.problem || nothing}
            </div>
          </div>
          <div>
            <button type="button" ?disabled=${this.busy} @click=${this.cancel}>Cancel</button>
            <button
              class="ring"
              type="button"
              data-testid="split-ring"
              ?disabled=${this.busy || !read.armed}
              @click=${this.ring}
            >
              ${this.busy ? 'Ringing…' : 'Ring split sale'}
            </button>
          </div>
        </div>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-split-tender': HosPosSplitTender;
  }
}
