// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-customer-picker.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { money, searchCustomers } from './pos-api.js';
import { api, availableCreditCents } from './ports.js';
import type { PosCustomer } from './pos-customer-strip.js';
import { customerTypeWord } from './_customer-picker.js';
import { focusRing } from './styles/focus-ring.js';
import { ModalFocus } from './modal-focus.js';

/**
 * POS customer picker (account-tender UX fix). A modal over GET /api/v1/customers/search:
 * a debounced (~250ms) search on name / account_no, a results list (name · account_no ·
 * Available credit), and a "Walk-in / no account" clear option. Emits `hos-customer-selected`
 * with the chosen PosCustomer: or null for walk-in. The terminal opens this when the cashier
 * taps the customer strip, or when Charge Account is pressed with no customer selected (the
 * `hint` shows "Pick the account to charge."). Styling mirrors the other POS modals (design
 * tokens; the pos-parked card/scrim pattern).
 */
@customElement('hos-pos-customer-picker')
export class HosPosCustomerPicker extends LitElement {
 // hint is an optional prompt (e.g. the Charge-Account nudge). Empty = plain picker.
  @property({ type: String }) hint = '';

 /**
 * THE BRANCH THIS TILL IS STANDING IN: DIB-506, and the reason this picker returned nothing.
 *
 * The account search is branch-fenced on the server: the U-park E4 fence 403s a branch-SCOPED
 * caller that names no branch, so `searchCustomers` grew an optional `branchId` and its own
 * docstring says "callers that know their branch must say it". Every other read on this counter
 * says it: the scan box, the search drop, the hot list, the browse grid and the past-sales
 * lookup all take `.branchId` from the terminal: and quote-workarea.ts has passed it to THIS
 * function since DIB-506 landed. The picker was the one caller that did not, which meant an
 * INSIDE_SALES clerk (branch-scoped, and the exact role this screen is for) got a 403 for every
 * search and therefore could not put an account on a sale at all. Empty is still honest: a
 * GLOBAL-scoped caller names no branch and gets the unfiltered sweep, byte for byte as before.
 */
  @property({ type: String }) branchId = '';

 // E6: parent-mounted, so connected IS open. No data-autofocus needed: the search box is already
 // first in DOM order, which is where a cashier wants to be typing the moment this opens.
  readonly modalFocus = new ModalFocus(this, {
    container: () => this.renderRoot.querySelector<HTMLElement>('.hos-dialog'),
    onEscape: () => this.close(),
  });

  @state() private term = '';
  @state() private results: PosCustomer[] = [];
  @state() private busy = false;
  @state() private error = '';
  private debounce = 0;

 // ── rev 8 (item 5): the add-new-customer flow: REUSED, not rebuilt. The create door is the
 // EXISTING manager-gated POST /api/v1/customers (M5 Canadian-address validation and all); this
 // panel is only the counter's way to knock on it. No second create path exists.
  @state() private creating = false;
  @state() private newName = '';
  @state() private newAccountNo = '';
  @state() private newType = 'CONTRACTOR';
  @state() private createBusy = false;
 /**
 * DIB-636. Set when the create form is left while a POST is still in flight. The counter carries
 * the same defect as the shared picker and gets the same two layers; see customer-picker.ts for
 * the full account, including which gesture actually reaches this and which two do not. The
 * request cannot be recalled: this only stops the answer selecting itself into a form the user
 * has walked away from, and clears the fields so a reopen cannot ask for the account twice.
 */
  private createAbandoned = false;
 /** Set when an abandoned create nevertheless succeeded; shown once on the next open. */
  @state() private abandonedNotice = '';
  @state() private createError = '';

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
      border-radius: var(--hos-radius-card);
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
    .hint {
      background: rgba(216, 164, 65, 0.12);
      border: 1px solid var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
      border-radius: var(--hos-radius-control);
      padding: 8px 12px;
      font-size: 12px;
      margin-bottom: 12px;
    }
    input.search::placeholder {
      color: var(--hos-text-dim); /* rev 5: the UA default gray measured under 4.5:1 */
      opacity: 1;
    }
    input.search {
      width: 100%;
      box-sizing: border-box;
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      background: var(--hos-deep-earth);
      color: var(--hos-text);
      padding: 10px 12px;
      font-family: inherit;
      font-size: 14px;
      margin-bottom: 12px;
    }
    .row {
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      padding: 11px 14px;
      margin-bottom: 8px;
      background: var(--hos-deep-earth);
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 10px;
      align-items: center;
      cursor: pointer;
      text-align: left;
      width: 100%;
      font: inherit;
      color: inherit;
    }
    .row:hover {
      border-color: var(--hos-stone-amber);
    }
    .name {
      font-size: 14px;
      font-weight: 600;
    }
    .acct {
      font-family: var(--hos-font-mono);
      font-size: 12px;
      color: var(--hos-text-dim);
      margin-top: 2px;
    }
    .credit {
      text-align: right;
      font-size: 11px;
      color: var(--hos-text-dim);
      text-transform: uppercase;
      letter-spacing: 0.06em;
    }
    .credit .num {
      display: block;
      font-family: var(--hos-font-mono);
      font-size: 13px;
      text-transform: none;
      letter-spacing: 0;
      color: var(--hos-success);
    }
    .walkin {
      color: var(--hos-text-dim);
      font-style: italic;
    }
    .empty {
      color: var(--hos-text-dim);
      font-size: 13px;
      text-align: center;
      padding: 18px 0;
    }
    .banner.bad {
      background: rgba(244, 63, 94, 0.12);
      border: 1px solid var(--hos-safety-red);
      color: var(--hos-safety-red);
      border-radius: var(--hos-radius-control);
      padding: 10px 12px;
      font-size: 13px;
      margin-bottom: 10px;
    }
    .actions {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-top: 12px;
      gap: 8px;
    }
    button.link {
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      background: transparent;
      color: var(--hos-text);
      padding: 8px 12px;
      font-size: 12px;
      font-family: inherit;
      cursor: pointer;
    }
 /* rev 8 (item 5): the search row: the box + the add-new-customer icon button beside it. */
    .searchrow {
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 8px;
      align-items: start;
      margin-bottom: 12px;
    }
    .searchrow input.search {
      margin-bottom: 0;
    }
    button.addcust {
      display: inline-grid;
      place-items: center;
      width: 40px;
      height: 40px;
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      background: var(--hos-deep-earth);
      color: var(--hos-text-dim);
      cursor: pointer;
      padding: 0;
    }
    button.addcust:hover,
    button.addcust[aria-expanded='true'] {
      border-color: var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
    }
    .createform {
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      background: var(--hos-deep-earth);
      padding: 12px 14px;
      margin-bottom: 12px;
    }
    .createform label {
      display: block;
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      margin: 8px 0 4px;
    }
    .createform label:first-child {
      margin-top: 0;
    }
    .createform input,
    .createform select {
      width: 100%;
      box-sizing: border-box;
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      background: var(--hos-slate-warm);
      color: var(--hos-text);
      padding: 8px 10px;
      font-family: inherit;
      font-size: 13px;
    }
    .createform .createactions {
      display: flex;
      justify-content: flex-end;
      gap: 8px;
      margin-top: 12px;
    }
    .createform button.primary {
      border: 1px solid var(--hos-stone-amber);
      border-radius: var(--hos-radius-control);
      background: var(--hos-stone-amber);
      color: var(--hos-stone-amber-ink);
      padding: 8px 14px;
      font-size: 12px;
      font-weight: 600;
      font-family: inherit;
      cursor: pointer;
    }
    .createform button.primary:hover:not(:disabled) {
      background: var(--hos-stone-amber-hover);
      border-color: var(--hos-stone-amber-hover);
      color: var(--hos-stone-amber-ink);
    }
    .createform button.primary:disabled {
      opacity: 0.45;
      cursor: not-allowed;
    }
    .createnote {
      font-size: 11px;
      color: var(--hos-text-dim);
      margin-top: 8px;
    }
  `,
  ];

  connectedCallback(): void {
    super.connectedCallback();
 // Autofocus the search input once rendered.
    this.updateComplete.then(() => {
      const el = this.renderRoot.querySelector<HTMLInputElement>('input.search');
      el?.focus();
    });
  }

  disconnectedCallback(): void {
    super.disconnectedCallback();
    if (this.debounce) clearTimeout(this.debounce);
  }

  private onInput(e: Event): void {
    this.term = (e.target as HTMLInputElement).value;
    if (this.debounce) clearTimeout(this.debounce);
    const q = this.term.trim();
    if (q === '') {
      this.results = [];
      this.busy = false;
      this.error = '';
      return;
    }
    this.debounce = window.setTimeout(() => void this.run(q), 250);
  }

  private async run(q: string): Promise<void> {
    this.busy = true;
    this.error = '';
    try {
 // DIB-506: name the branch. See the branchId property for why the omission was the whole bug.
      this.results = await searchCustomers(q, 20, this.branchId);
    } catch (err) {
      this.error = this.searchRefusal(err);
      this.results = [];
    } finally {
      this.busy = false;
    }
  }

 /**
 * A REFUSAL IN COUNTER WORDS, never a wire status.
 *
 * `searchCustomers` throws `customer search 403`: the literal HTTP number: and this panel used
 * to hand that straight to a role="alert" for a person standing at a till with a customer in
 * front of them to read aloud. Every line here is the D-107 shape: what is missing, what that
 * means for THIS sale, and the way forward. The way forward is always real on this screen,
 * because walk-in is one row above the message.
 */
  private searchRefusal(err: unknown): string {
    const raw = err instanceof Error ? err.message : '';
    if (/\b40[13]\b/.test(raw)) {
      return 'This register cannot search accounts for its branch, so no account can be charged from here. Ring the sale as walk-in, or ask a manager to give this register the branch.';
    }
    if (/\b5\d\d\b/.test(raw)) {
      return 'The account list did not answer, so no accounts can be shown right now. Search again in a moment, or ring the sale as walk-in.';
    }
    return 'The account search could not run, so no accounts can be shown. Try the search again, or ring the sale as walk-in.';
  }

 /** rev 5: Enter selects the top match; ArrowDown steps from the box into the result rows. */
  private onSearchKeydown(e: KeyboardEvent): void {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (!this.busy && this.term.trim() !== '' && this.results.length > 0) this.pick(this.results[0]);
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      this.rowAt(0)?.focus();
    }
  }

 /** rev 5: Arrow keys walk the result rows (walk-in row included); ArrowUp from the top returns to the box. */
  private onRowsKeydown(e: KeyboardEvent): void {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    const rows = [...this.renderRoot.querySelectorAll<HTMLButtonElement>('button.row')];
    const i = rows.indexOf(e.target as HTMLButtonElement);
    if (i < 0) return;
    e.preventDefault();
    if (e.key === 'ArrowDown' && i < rows.length - 1) rows[i + 1].focus();
    else if (e.key === 'ArrowUp') {
      if (i > 0) rows[i - 1].focus();
      else this.renderRoot.querySelector<HTMLInputElement>('input.search')?.focus();
    }
  }

  private rowAt(i: number): HTMLButtonElement | null {
    return this.renderRoot.querySelectorAll<HTMLButtonElement>('button.row')[i] ?? null;
  }

  private pick(c: PosCustomer | null): void {
    this.dispatchEvent(
      new CustomEvent<PosCustomer | null>('hos-customer-selected', {
        detail: c,
        bubbles: true,
        composed: true,
      }),
    );
  }

  private close(): void {
    this.dispatchEvent(new CustomEvent('hos-customer-picker-close', { bubbles: true, composed: true }));
  }

 // ── rev 8 (item 5): drive the EXISTING create endpoint, then select the new account ───────────

  private toggleCreate(): void {
 // DIB-636 · leaving a create in flight abandons it. The create toggle is the one control
 // still enabled while the POST is in the air; see the field's own comment for why the other
 // two gestures an earlier note claimed do not reach here.
    if (this.createBusy && this.creating) this.createAbandoned = true;
    this.creating = !this.creating;
 // A notice from a create the person walked away from outlives this reset, and is shown
 // once on the next open. Everything else about the previous attempt is cleared.
    this.createError = this.abandonedNotice;
    this.abandonedNotice = '';
    if (this.creating) {
      void this.updateComplete.then(() =>
        this.renderRoot.querySelector<HTMLInputElement>('[data-testid="customer-new-name"]')?.focus(),
      );
    }
  }

 /**
 * The account number the counter offers by default: derived from the name (a recognisable
 * "CUST-" slug) but fully editable: the SERVER is the authority (409 on a duplicate, in words).
 */
  private suggestAccountNo(): void {
    if (this.newAccountNo.trim() !== '') return;
    const slug = this.newName
      .toUpperCase()
      .replace(/[^A-Z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 12);
    if (slug) this.newAccountNo = `CUST-${slug}`;
  }

  private async createCustomer(): Promise<void> {
    const name = this.newName.trim();
    const acct = this.newAccountNo.trim();
    if (name === '' || acct === '') {
      this.createError = 'Name and account # are both needed.';
      return;
    }
    this.createBusy = true;
    this.createError = '';
    try {
      const res = await api('/api/v1/customers', {
        method: 'POST',
        body: JSON.stringify({ account_no: acct, name, customer_type: this.newType }),
      });
      if (res.status === 403) {
 // The write guard is the manager gate: say so in counter words, change nothing else.
        this.createError = 'Creating accounts needs a manager sign-in.';
        return;
      }
      if (res.status === 409) {
        this.createError = `Account # ${acct} already exists: pick another.`;
        return;
      }
      if (!res.ok) {
        let msg = 'Could not create the account.';
        try {
          const body = (await res.json()) as { error?: string; detail?: string };
          msg = body.detail || body.error || msg;
        } catch {
 /* keep the fallback wording */
        }
        this.createError = msg;
        return;
      }
      const acctBody = (await res.json()) as {
        customer_id: string;
        account_no: string;
        name: string;
        customer_type: string;
        payment_terms_days?: number;
        credit_limit_cents?: number;
        balance_cents?: number;
      };
 // The new account is the answer to the picker's own question: select it,
 // unless nobody is waiting for the answer any more (DIB-636).
      if (this.createAbandoned) {
 // The account EXISTS now. Leaving the form did not stop the request and nothing here
 // can, so the only question is what the person finds when they come back. Before this,
 // the fields kept their values: reopening and pressing Create again asked the server to
 // make an account that had just been made, and the counter person was told to "pick
 // another account number" for an account they were never told about. The form is
 // cleared and the next open says what happened, once.
        this.abandonedNotice = `${acctBody.name} was created while you were away. Search for it by name.`;
        this.newName = '';
        this.newAccountNo = '';
        return;
      }
      this.pick({
        id: acctBody.customer_id,
        name: acctBody.name,
        account_no: acctBody.account_no,
        customer_type: acctBody.customer_type,
        payment_terms_days: acctBody.payment_terms_days ?? 0,
        credit_limit_cents: acctBody.credit_limit_cents ?? 0,
        open_balance_cents: acctBody.balance_cents ?? 0,
      });
    } catch {
      this.createError = 'Could not reach the server: try again.';
    } finally {
      this.createBusy = false;
      this.createAbandoned = false;
    }
  }

 /** rev 8 (item 5): the minimal create form over the existing endpoint (name, account #, type). */
  private renderCreateForm() {
    return html`<div class="createform" data-testid="customer-create-form" role="group" aria-label="New customer account">
      ${this.createError ? html`<div class="banner bad" role="alert">${this.createError}</div>` : nothing}
      <label>Customer name</label>
      <input
        data-testid="customer-new-name"
        .value=${this.newName}
        autocomplete="off"
        placeholder="e.g. Greenridge Landscaping"
        @input=${(e: Event) => (this.newName = (e.target as HTMLInputElement).value)}
        @change=${() => this.suggestAccountNo()}
      />
      <label>Account #</label>
      <input
        data-testid="customer-new-account"
        .value=${this.newAccountNo}
        autocomplete="off"
        placeholder="e.g. CUST-1042"
        @input=${(e: Event) => (this.newAccountNo = (e.target as HTMLInputElement).value)}
      />
      <label>Account type</label>
      <select data-testid="customer-new-type" .value=${this.newType} @change=${(e: Event) => (this.newType = (e.target as HTMLSelectElement).value)}>
        <option value="CONTRACTOR">Contractor</option>
        <option value="RETAIL">Retail</option>
        <option value="BUILDER">Builder</option>
        <option value="GOVERNMENT">Government</option>
      </select>
      <div class="createactions">
        ${/* DIB-636: disabled mid-flight, exactly as Save is. A request already on the wire
             cannot be recalled, and a Cancel that stays pressable through it promises an undo
             that does not exist. */ ''}
        <button
          class="link"
          data-testid="customer-create-cancel"
          ?disabled=${this.createBusy}
          @click=${() => this.toggleCreate()}
        >
          Cancel
        </button>
        <button
          class="primary"
          data-testid="customer-create-save"
          ?disabled=${this.createBusy}
          @click=${() => void this.createCustomer()}
        >
          ${this.createBusy ? 'Creating…' : 'Create & select'}
        </button>
      </div>
      <div class="createnote">
        Creates the account through the standard customer-create flow (manager-gated; billing
        address and credit can be completed later in the account's own screens).
      </div>
    </div>`;
  }

  render() {
    return html`<div class="hos-dialog" @click=${(e: Event) => e.target === e.currentTarget && this.close()}>
      <div class="card">
        <h2>Select customer</h2>
        <p class="sub">Search an account by name or account number to charge, or ring walk-in.</p>
        ${this.hint ? html`<div class="hint">${this.hint}</div>` : nothing}
        ${this.error ? html`<div class="banner bad" role="alert">${this.error}</div>` : nothing}
        <div class="searchrow">
          <input
            class="search"
            type="text"
            placeholder="Search name or account #…"
            .value=${this.term}
            @input=${this.onInput}
            @keydown=${this.onSearchKeydown}
          />
          ${/* rev 8 (item 5): the add-new-customer affordance beside the search: opens the
             EXISTING create flow (the manager-gated POST /api/v1/customers), never a new one. */ ''}
          <button
            class="addcust"
            data-testid="customer-add-new"
            aria-expanded=${this.creating ? 'true' : 'false'}
            title="Add a new customer account"
            aria-label="Add a new customer account"
            @click=${() => this.toggleCreate()}
          >
            <svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <circle cx="10" cy="8" r="3.4"></circle>
              <path d="M4 19.5c1.2-3 3.4-4.5 6-4.5 1.1 0 2.1.25 3 .74"></path>
              <path d="M18 13.5v6M15 16.5h6"></path>
            </svg>
          </button>
        </div>
        ${this.creating ? this.renderCreateForm() : nothing}

        <button class="row walkin" @keydown=${this.onRowsKeydown} @click=${() => this.pick(null)}>
          <div><span class="name">Walk-in / no account</span><div class="acct">Cash customer: no account charge</div></div>
        </button>

        ${/* A REFUSAL IS NOT AN EMPTY RESULT. While `error` is set the banner above already says
             what happened and what to do; the empty line was gated on results.length alone, so a
             403 painted "customer search 403" and "No accounts match “Quinte”." in the same frame:
             two answers to one question, one of them a lie (there are five). */ ''}
        ${this.busy
          ? html`<div class="empty">Searching…</div>`
          : this.error !== '' || this.term.trim() === ''
            ? nothing
            : this.results.length === 0
              ? html`<div class="empty">No accounts match “${this.term.trim()}”.</div>`
              : this.results.map((c) => {
                  // HH note: the reference subtracted the balance from the limit here; the core
                  // computes credit in HH, so the port asks the host seam (CLOSURE.md names it),
                  // already clamped for display.
                  const available = availableCreditCents(c);
                  return html`<button class="row" @keydown=${this.onRowsKeydown} @click=${() => this.pick(c)}>
                    <div>
                      <div class="name">${c.name}</div>
                      ${/* ONE map for customers.customer_type, shared with the quote builder's
                           picker: the till and the quote desk cannot call an account two things. */ ''}
                      <div class="acct">${c.account_no} · ${customerTypeWord(c.customer_type)}</div>
                    </div>
                    <div class="credit">
                      Available<span class="num">${money(available)}</span>
                    </div>
                  </button>`;
                })}

        <div class="actions">
          <span></span>
          <button class="link" @click=${this.close}>Cancel</button>
        </div>
      </div>
    </div>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-customer-picker': HosPosCustomerPicker;
  }
}
