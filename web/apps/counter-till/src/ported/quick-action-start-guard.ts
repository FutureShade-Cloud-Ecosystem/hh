// Ported from hardscapeos_dibbits master 87d7941b app/src/quick-action-start-guard.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { focusRing } from './styles/focus-ring.js';
import { ModalFocus } from './modal-focus.js';
import { canPark, guardMessage, quickStartMode, type LiveTicket } from './quick-action-guard.js';

/**
 * <hos-quick-start-guard>: DIB-434. The dialog the in-progress guard raises.
 *
 * It is a SEPARATE ELEMENT from the terminal on purpose, and the purpose is not tidiness: the
 * terminal is 1,700 lines with several agents' regions in it, and the guard is the part of this
 * ticket that must be reviewable on its own. Everything it knows arrives as one property (the live
 * ticket, as three primitives) and everything it decides leaves as one event. The rules themselves
 * live in quick-action-guard.ts, where they are tested without a DOM.
 *
 * THERE IS NO DISCARD BUTTON ON THE LINES PATH. When the ticket has lines, the two answers are
 * Continue and Park it: parking is a real server ticket on the shipped rail (POS Counter V2 /
 * Q-042), so there is no reason to offer destruction of something that can be kept. The only path
 * that clears anything is the one where there is nothing the rail can hold (no lines), and that
 * button says exactly what it clears.
 *
 * Emits `quick-start-continue` (leave the ticket alone), `quick-start-park` (park, then fresh) and
 * `quick-start-clear` (no lines to park: clear and start fresh).
 */
@customElement('hos-quick-start-guard')
export class HosQuickStartGuard extends LitElement {
  /** The live ticket the guard is protecting. */
  @property({ attribute: false }) ticket: LiveTicket = {
    lineCount: 0,
    hasCustomer: false,
    hasOtherWork: false,
  };

  /** True while the park request is in flight (the button is doing something on the server). */
  @property({ type: Boolean }) busy = false;

 // E6: parent-mounted: the terminal renders this element only while the guard is up, so
  // "connected" IS "open". Escape means CONTINUE (the safe answer): dismissing this dialog must
  // leave the till exactly as it was, which is the entire point of the guard.
  readonly modalFocus = new ModalFocus(this, {
    container: () => this.renderRoot.querySelector<HTMLElement>('.hos-dialog'),
    onEscape: () => this.emit('quick-start-continue'),
  });

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
        z-index: var(--hos-z-modal);
      }
      .card {
        background: var(--hos-slate-warm);
        border: 1px solid var(--hos-line);
        border-radius: 14px;
        width: min(520px, 94vw);
        padding: 22px 24px;
      }
      h2 {
        margin: 0 0 6px;
        font-size: 14px;
        text-transform: uppercase;
        letter-spacing: 0.12em;
        color: var(--hos-stone-amber-text);
      }
      p {
        margin: 0 0 16px;
        font-size: 13.5px;
        line-height: 1.5;
        color: var(--hos-text);
      }
      .note {
        font-size: 12px;
        color: var(--hos-text-dim);
        margin: -8px 0 16px;
      }
      .actions {
        display: flex;
        gap: 10px;
        flex-wrap: wrap;
        justify-content: flex-end;
      }
      button {
        border-radius: var(--hos-radius-control, 8px);
        padding: 9px 15px;
        font-size: 13px;
        font-family: inherit;
        font-weight: 600;
        cursor: pointer;
        border: 1px solid var(--hos-line);
        background: transparent;
        color: var(--hos-text);
      }
      button:hover {
        border-color: var(--hos-stone-amber);
      }
      /* The primary is CONTINUE, not the destructive-adjacent one. The guard exists because the
         click that got here may have been a mistake made from a screen where this till was not
         even visible, so the default answer is "leave it alone". */
      button.primary {
        background: var(--hos-stone-amber);
        border-color: var(--hos-stone-amber);
        color: #0b1725;
      }
      button.primary:hover:not(:disabled) {
        background: color-mix(in srgb, var(--hos-stone-amber) 86%, #000);
      }
      button:disabled,
      button.primary:disabled {
        background: transparent;
        border-color: var(--hos-line);
        color: var(--hos-text-dim);
        opacity: 1;
        cursor: not-allowed;
      }
    `,
  ];

  private emit(type: string): void {
    this.dispatchEvent(new CustomEvent(type, { bubbles: true, composed: true }));
  }

  render() {
    const mode = quickStartMode(this.ticket);
    if (mode === 'fresh') return nothing; // nothing to guard: the terminal never mounts this
    return html`
      <div
        class="hos-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="A ticket is already open on this till"
        tabindex="-1"
        @click=${(e: Event) => {
          // Scrim click = Continue, matching Escape. A stray click outside the card must never be
          // read as consent to touch the ticket.
          if (e.target === e.currentTarget) this.emit('quick-start-continue');
        }}
      >
        <div class="card">
          <h2>Ticket already open</h2>
          <p>${guardMessage(this.ticket)}</p>
          ${canPark(this.ticket)
            ? html`<p class="note">
                Parking keeps it on the parked-tickets rail; resuming re-prices every line live.
              </p>`
            : nothing}
          <div class="actions">
            <button
              type="button"
              class="primary"
              ?disabled=${this.busy}
              @click=${() => this.emit('quick-start-continue')}
            >
              Continue current ticket
            </button>
            ${canPark(this.ticket)
              ? html`<button
                  type="button"
                  ?disabled=${this.busy}
                  @click=${() => this.emit('quick-start-park')}
                >
                  ${this.busy ? 'Parking…' : 'Park it & start new'}
                </button>`
              : html`<button
                  type="button"
                  ?disabled=${this.busy}
                  @click=${() => this.emit('quick-start-clear')}
                >
                  Clear it & start new
                </button>`}
          </div>
        </div>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-quick-start-guard': HosQuickStartGuard;
  }
}
