// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-customer-strip.ts; adapted for HH.

import { LitElement, css, html } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { money } from './pos-api.js';
import { availableCreditCents } from './ports.js';

export interface PosCustomer {
  id: string;
  name: string;
  account_no: string;
  customer_type: string;
  payment_terms_days: number;
  credit_limit_cents: number;
  open_balance_cents: number;
}

/**
 * A-19 customer strip (US-A19-10). Shows name, account #, Terms, and the credit posture: Available
 * (green) vs Outstanding (red). Read-only display; the credit decision is enforced server-side by the
 * identity gate when an ACCOUNT tender is rung.
 */
@customElement('hos-pos-customer-strip')
export class HosPosCustomerStrip extends LitElement {
  @property({ attribute: false }) customer: PosCustomer | null = null;

  static styles = css`
    :host {
      display: block;
      font-family: var(--hos-font-ui);
      color: var(--hos-text);
    }
    .strip {
      display: flex;
      flex-wrap: wrap;
      gap: 16px;
      align-items: baseline;
      background: var(--hos-deep-earth);
      border: 1px solid var(--hos-line);
      border-radius: 10px;
      padding: 12px 14px;
    }
    .name {
      font-size: 15px;
      font-weight: 600;
    }
    .acct {
      font-family: var(--hos-font-mono);
      font-size: 12px;
      color: var(--hos-text-dim);
    }
    .pill {
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
    }
    .num {
      font-family: var(--hos-font-mono);
    }
    .avail {
      color: var(--hos-success);
    }
    .out {
      color: var(--hos-safety-red);
    }
    .walkin {
      color: var(--hos-text-dim);
      font-size: 13px;
    }
  `;

  render() {
    const c = this.customer;
    if (!c) {
      return html`<div class="strip"><span class="walkin">Walk-In / Cash Customer: no account</span></div>`;
    }
    // HH note: the reference subtracted the balance from the limit here; the core computes
    // credit in HH, so the port asks the host seam (CLOSURE.md names it).
    const available = availableCreditCents(c);
    return html`
      <div class="strip">
        <span class="name">${c.name}</span>
        <span class="acct">${c.account_no}</span>
        <span class="pill">Terms <span class="num">${c.payment_terms_days}d</span></span>
        <span class="pill">
          Available <span class="num avail">${money(available)}</span>
        </span>
        ${c.open_balance_cents > 0
          ? html`<span class="pill">
              Outstanding <span class="num out">${money(c.open_balance_cents)}</span>
            </span>`
          : ''}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-customer-strip': HosPosCustomerStrip;
  }
}
