// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-parked.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { discardParked, listParked, money, resumeParked, type ParkedTicket } from './pos-api.js';
import { resumeDiscardMessage } from './_pos.js';
import type { ParkedInfo } from './_pos.js';
import { focusRing } from './styles/focus-ring.js';
import { ModalFocus } from './modal-focus.js';

/**
 * W7 parked-tickets modal (POS_COUNTER_V2 §5): the PARKED list for the branch (ticket no, customer,
 * age, notes) with Resume / Discard. Resume marks the ticket RESUMED server-side and hands the saved
 * lines back to the terminal, which re-adds them through the normal engine-priced addProduct path:
 * a parked ticket NEVER rings at a stale price (the payload carries no prices at all).
 */
@customElement('hos-pos-parked')
export class HosPosParked extends LitElement {
  @property({ type: String }) branchId = '';

  // DIB-247: the park-time display snapshots (customer + the priced lines'
  // subtotal) the terminal captured, keyed by ticket id. The server list
  // carries only customer_id and no prices, so this is where a row's
  // customer NAME and FIGURE come from. A cold entry (parked in a prior
  // session) falls back to the account/walk-in label and the "(not priced)"
  // marker.
  @property({ attribute: false }) info: ReadonlyMap<string, ParkedInfo> = new Map();
  /**
   * How many lines are on the ticket the resume is about to discard (D17, round 3).
   *
   * The terminal owns the cart; this rail owns the button that throws it away. Resuming calls
 * `newSale()` on the till, and until round 3 it did so with nothing asked and nothing said:
   * while Clear sale and New Sale, which do exactly the same thing, both confirm first. The
   * question has to be asked HERE, before POST /resume: once the server has moved the ticket to
   * RESUMED it is out of the parked list, so a confirm on the far side would have nothing to
   * decline back to.
   */
  @property({ type: Number, attribute: false }) openLines = 0;

 // E6: parent-mounted: pos-terminal renders this element only while the dialog is open, so
  // "connected" IS "open" and no isOpen predicate is needed. Escape mirrors the scrim click exactly:
  // closing this dialog discards nothing, so there is no unsafe step to gate.
  // Public because `noUnusedLocals` flags a private field that is only ever constructed; the
  // controller registers itself with the host, so nothing needs to read it back.
  readonly modalFocus = new ModalFocus(this, {
    container: () => this.renderRoot.querySelector<HTMLElement>('.hos-dialog'),
    onEscape: () => this.close(),
  });

  @state() private tickets: ParkedTicket[] = [];
  @state() private busyId = '';
  @state() private error = '';
  // Ordering guard for the reads below. Two reads of one open dialog are
  // routinely in flight at once (the open read and a host triggered reload
  // that lands under it), so whichever read resolves last may be the stale
  // one: only the read holding the newest reserved token may write, and a
  // superseded read discards its answer instead of putting the rail back on
  // the old list.
  private readSeq = 0;

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
      width: min(560px, 94vw);
      max-height: 88vh;
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
    .ticket {
      border: 1px solid var(--hos-line);
      border-radius: 10px;
      padding: 12px 14px;
      margin-bottom: 10px;
      background: var(--hos-deep-earth);
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 10px;
      align-items: center;
    }
    .tno {
      font-family: var(--hos-font-mono);
      color: var(--hos-stone-amber-text);
      font-size: 13px;
    }
    .who {
      font-size: 13px;
      font-weight: 600;
      margin-top: 2px;
    }
    .meta {
      font-size: 12px;
      color: var(--hos-text-dim);
      margin-top: 3px;
    }
    .mtot {
      font-family: var(--hos-font-mono);
      color: var(--hos-stone-amber-text);
    }
    .notes {
      font-size: 12px;
      margin-top: 4px;
    }
    .btns {
      display: flex;
      gap: 6px;
    }
    button {
      border: 1px solid var(--hos-line);
      border-radius: 8px;
      background: transparent;
      color: var(--hos-text);
      padding: 7px 12px;
      font-size: 12px;
      font-family: inherit;
      cursor: pointer;
    }
    button.primary {
      background: var(--hos-stone-amber);
      border-color: var(--hos-stone-amber);
      color: #0b1725;
      font-weight: 600;
    }
    button.danger {
      color: var(--hos-safety-red);
      border-color: var(--hos-safety-red);
    }
    button:disabled {
      background: transparent;
      border-color: var(--hos-line);
      color: var(--hos-text-dim);
      opacity: 1;
      cursor: not-allowed;
    }
    .empty {
      color: var(--hos-text-dim);
      font-size: 13px;
      text-align: center;
      padding: 22px 0;
    }
    .banner.bad {
      background: rgba(244, 63, 94, 0.12);
      border: 1px solid var(--hos-safety-red);
      color: var(--hos-safety-red);
      border-radius: 8px;
      padding: 10px 12px;
      font-size: 13px;
      margin-bottom: 10px;
    }
    .actions {
      display: flex;
      justify-content: flex-end;
      margin-top: 8px;
    }
  `,
  ];

  connectedCallback(): void {
    super.connectedCallback();
    void this.reload();
  }

  /**
   * The branch's parked list, read again on demand. The dialog reads exactly
   * once on open (the reference's own shape, hardscapeos_dibbits master
   * 87d7941b app/src/pos-parked.ts), so a list change landing under the open
   * dialog left it frozen at the stale rows until close and reopen. The host
   * calls this when its own parked-list read lands (pos-terminal's
   * refreshParkedCount, which fires when the branch's list changes: park,
   * resume and dialog close through the terminal, and this rail's own
   * discard, announced), so a change landing while the dialog is open
   * converges it within one read: no polling loop, no contract change.
   */
  async reload(): Promise<void> {
    const token = ++this.readSeq;
    try {
      const tickets = await listParked(this.branchId);
      if (token === this.readSeq) this.tickets = tickets;
    } catch (e) {
      if (token === this.readSeq) {
        this.error = e instanceof Error ? e.message : 'failed to load parked tickets';
      }
    }
  }

  private close(): void {
    this.dispatchEvent(new CustomEvent('pos-parked-close', { bubbles: true, composed: true }));
  }

  private age(createdAt: string): string {
    const ms = Date.now() - new Date(createdAt).getTime();
    if (!Number.isFinite(ms) || ms < 0) return '';
    const mins = Math.floor(ms / 60000);
    if (mins < 60) return `${mins}m ago`;
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return `${hrs}h ${mins % 60}m ago`;
    return `${Math.floor(hrs / 24)}d ago`;
  }

  private async resume(t: ParkedTicket): Promise<void> {
 // Ask BEFORE the ticket is consumed: see `openLines`. Nothing is lost when they decline: no
    // request has gone out, the rail stays open, and the ticket stays parked.
    const ask = resumeDiscardMessage(this.openLines, t.ticket_no);
    if (ask && !window.confirm(ask)) return;
    this.busyId = t.id;
    this.error = '';
    try {
      const resumed = await resumeParked(t.id);
      this.dispatchEvent(
        new CustomEvent('pos-parked-resume', { detail: { ticket: resumed }, bubbles: true, composed: true }),
      );
    } catch (e) {
      this.error = e instanceof Error ? e.message : 'resume failed';
      void this.reload(); // a concurrent resume/discard: refresh the list
    } finally {
      this.busyId = '';
    }
  }

  private async discard(t: ParkedTicket): Promise<void> {
    this.busyId = t.id;
    this.error = '';
    try {
      await discardParked(t.id);
      await this.reload();
      // The list changed without the terminal acting: announce it the way
      // close and resume are announced, so the host's count read moves the
      // button with the list while this rail stays open.
      this.dispatchEvent(new CustomEvent('pos-parked-discard', { bubbles: true, composed: true }));
    } catch (e) {
      this.error = e instanceof Error ? e.message : 'discard failed';
    } finally {
      this.busyId = '';
    }
  }

  render() {
    return html`<div class="hos-dialog" @click=${(e: Event) => e.target === e.currentTarget && this.close()}>
      <div class="card">
        <h2>Parked tickets</h2>
        <p class="sub">Saved carts for this branch: resumed lines re-price live through the engine.</p>
        ${this.error ? html`<div class="banner bad" role="alert">${this.error}</div>` : nothing}
        ${this.tickets.length === 0
          ? html`<div class="empty">No parked tickets.</div>`
          : this.tickets.map((t) => {
              // DIB-247: identify each ticket by WHO it's for and HOW MUCH: both come from
              // the terminal's park-time snapshot (the server row carries neither the name
              // nor a price). The snapshot figure is the park-time SUBTOTAL of the priced
              // lines (row 661: the till computed no total to snapshot), so the row NAMES
              // it a subtotal and carries no estimate mark; a cold snapshot degrades to the
              // account/walk-in label and the "(not priced)" marker.
              const snap = this.info.get(t.id);
              const who = snap?.customer?.name || (t.customer_id ? 'Account customer' : 'Walk-in');
              const figure = snap && snap.subtotalCents !== null ? `subtotal ${money(snap.subtotalCents)}` : '(not priced)';
              return html`<div class="ticket">
                <div>
                  <div class="tno">${t.ticket_no}</div>
                  <div class="who">${who}</div>
                  <div class="meta">
                    ${t.lines.length} line${t.lines.length === 1 ? '' : 's'}${figure
                      ? html` · <span class="mtot">${figure}</span>`
                      : nothing}
                    · ${this.age(t.created_at)}
                  </div>
                  ${t.notes ? html`<div class="notes">${t.notes}</div>` : nothing}
                </div>
                <div class="btns">
                  <button class="primary" ?disabled=${this.busyId === t.id} @click=${() => this.resume(t)}>
                    Resume
                  </button>
                  <button class="danger" ?disabled=${this.busyId === t.id} @click=${() => this.discard(t)}>
                    Discard
                  </button>
                </div>
              </div>`;
            })}
        <div class="actions"><button @click=${this.close}>Close</button></div>
      </div>
    </div>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-parked': HosPosParked;
  }
}
