// Ported from hardscapeos_dibbits master 87d7941b app/src/price-breakdown.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { centsAmount } from './_money-format.js';
import { customElement, property, state } from 'lit/decorators.js';
import { api } from './ports.js';
import { priceSourceWord } from './pricing-words-logic.js';
import { focusRing } from './styles/focus-ring.js';

/**
 * A-07 price breakdown chip + read-only resolve inspector.
 *
 * Renders the pre-tax `Breakdown` returned by `POST /api/v1/pricing/resolve`
 * (original → final, discount %, source, detail, price UOM, and the below-floor
 * "manager approval required" state). A read-only inspector (the counter lead's ask) can drive
 * the engine here without `go test`. NEVER renders tax: A-07 is pre-tax only (M5
 * owns tax).
 *
 * Money formatting: the reference flagged its local stopgap here for a mandatory
 * swap onto the app's one shared formatter once that landed. HH's shared
 * formatter exists (the app's src/money.ts), but the port keeps this module's
 * own display helper under its own name, because the app's format-scan binds
 * the shared formatter's name to its own import path and this file sits deeper
 * than that path can spell. The polish work item converges the two (CLOSURE.md
 * names it).
 */
export interface PriceBreakdown {
  original_cents: number;
  final_cents: number;
  discount_bps: number;
  source: string;
  detail: string;
  price_uom: string;
  qty_base: number;
  line_total_cents: number;
  // Optional so a policy-ruled host can withhold the figures from the data it
  // passes: absent figures render the below-floor words without the margin
  // pair, the same words marginFiguresVisible=false paints.
  margin_bps?: number;
  floor_bps?: number;
  blocked: boolean;
  block_policy?: string;
}

// HH note: the reference's flagged local stopgap, renamed for this tree: the app's
// format-scan reserves the shared formatter's name for files importing it by its
// own path (src/money.ts). Same shape, same swap intent, now aimed at the polish
// work item's convergence pass.
function priceMoney(cents: number): string {
  return `CA$${centsAmount(cents)}`;
}

@customElement('hos-price-breakdown')
export class HosPriceBreakdown extends LitElement {
  /** A resolved breakdown to render directly (e.g. from a quote line). */
  @property({ attribute: false }) breakdown: PriceBreakdown | null = null;

 /** Inspector inputs: set these and call resolve() (or click "Resolve"). */
  @property() productId = '';
  @property() customerId = '';
  @property() customerType = 'CONTRACTOR';
  @property() categoryId = '';
  @property() branchId = '';
  @property({ type: Number }) qtyEntered = 1;
  @property() uomEntered = 'SF';
  @property({ type: Number }) costCents = 0;

  /**
   * P3-10 (the comprehensive train review): whether the below-floor row may
   * spell its margin figures. The renderer holds no policy of its own. A host
   * can refuse the figures two ways, and the counter till uses the second:
   * set this property false, or withhold margin_bps and floor_bps from the
   * breakdown it passes (the positions gate admits no new property binding in
   * the till's templates, so the till strips the figures from a copy instead).
   * Default true: a host that mounts this chip outside a policy-ruled surface
   * keeps the renderer's own behavior.
   */
  @property({ type: Boolean }) marginFiguresVisible = true;

  @state() private loading = false;
  @state() private error = '';

  static styles = [
    focusRing,
    css`
    :host {
      display: block;
      font-family: var(--hos-font-ui);
      color: var(--hos-text);
    }
    .chip {
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: 12px;
      padding: 14px 16px;
      max-width: 420px;
    }
    .row {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      padding: 3px 0;
      font-size: 13px;
    }
    .label {
      color: var(--hos-text-dim);
      text-transform: uppercase;
      letter-spacing: 0.1em;
      font-size: 10px;
    }
    .num {
      font-family: var(--hos-font-mono);
    }
    .final {
      color: var(--hos-stone-amber-text);
      font-weight: 600;
    }
    .strike {
      color: var(--hos-text-dim);
      text-decoration: line-through;
    }
    .source {
      color: var(--hos-info-blue);
      font-size: 11px;
      font-family: var(--hos-font-mono);
    }
    .blocked {
      margin-top: 8px;
      padding: 8px 10px;
      border-radius: 8px;
      background: rgba(244, 63, 94, 0.12);
      border: 1px solid var(--hos-safety-red);
      color: var(--hos-safety-red);
      font-size: 12px;
    }
    button {
      margin-top: 10px;
      background: var(--hos-stone-amber);
      color: #0b1725;
      border: 0;
      border-radius: 8px;
      padding: 7px 14px;
      font-weight: 600;
      cursor: pointer;
    }
    .err {
      color: var(--hos-safety-red);
      font-size: 12px;
      margin-top: 8px;
    }
  `,
  ];

  /** Read-only resolve against the live engine. */
  async resolve(): Promise<void> {
    this.loading = true;
    this.error = '';
    try {
      const res = await api('/api/v1/pricing/resolve', {
        method: 'POST',
        body: JSON.stringify({
          customer_id: this.customerId,
          customer_type: this.customerType,
          product_id: this.productId,
          category_id: this.categoryId,
          branch_id: this.branchId,
          qty_entered: this.qtyEntered,
          uom_entered: this.uomEntered,
          cost_cents: this.costCents,
        }),
      });
      if (!res.ok) {
        this.error = `resolve failed (${res.status})`;
        return;
      }
      this.breakdown = (await res.json()) as PriceBreakdown;
    } catch (e) {
      this.error = e instanceof Error ? e.message : 'resolve error';
    } finally {
      this.loading = false;
    }
  }

  render() {
    const b = this.breakdown;
    return html`
      <div class="chip">
        ${b
          ? html`
              <div class="row">
                <span class="label">Source</span>
                <span class="source">${priceSourceWord(b.source)}</span>
              </div>
              <div class="row">
                <span class="label">List</span>
                <span class="num strike">${priceMoney(b.original_cents)}</span>
              </div>
              <div class="row">
                <span class="label">Price / ${b.price_uom}</span>
                <span class="num final">${priceMoney(b.final_cents)}</span>
              </div>
              <div class="row">
                <span class="label">Discount</span>
                <span class="num">${centsAmount(b.discount_bps)}%</span>
              </div>
              <div class="row">
                <span class="label">Line total</span>
                <span class="num">${priceMoney(b.line_total_cents)}</span>
              </div>
              <div class="row">
                <span class="label">Detail</span>
                <span>${b.detail}</span>
              </div>
              ${b.blocked
                ? html`<div class="blocked">
                    ${b.detail || 'Below the configured margin floor: manager approval required'}${this
                      .marginFiguresVisible && b.margin_bps !== undefined && b.floor_bps !== undefined
                      ? html` (margin ${centsAmount(b.margin_bps)}% &lt; floor
                          ${centsAmount(b.floor_bps)}%)`
                      : nothing}
                  </div>`
                : nothing}
            `
          : html`<div class="label">no breakdown: set inputs and resolve</div>`}
        ${this.productId
          ? html`<button ?disabled=${this.loading} @click=${() => this.resolve()}>
              ${this.loading ? 'Resolving…' : 'Resolve'}
            </button>`
          : nothing}
        ${this.error ? html`<div class="err" role="alert">${this.error}</div>` : nothing}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-price-breakdown': HosPriceBreakdown;
  }
}
