// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-search-drop.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { focusRing } from './styles/focus-ring.js';
import './product-thumb.js';
import { counterSearch, counterPrices, counterHitToProduct, money, type CounterHit } from './pos-api.js';
import {
  DROP_ROWS,
  dropView,
  shouldQuery,
  rowStep,
  pricePicture,
  basisWord,
  type CounterQuote,
} from './_pos-searchdrop.js';
import { AddOnce } from './_pos-add-dedupe.js';

/**
 * hos-pos-search-drop: the counter's search RESULTS, as five rows under the field.
 *
 * THE CLIENT'S ASK, VERBATIM: "the name of the game is getting people in and out really quickly."
 * The lookup this replaces was a grid: a browsing surface, given the default. But browsing is not
 * what happens at a counter: somebody says "black mulch", the clerk types four letters and takes
 * the first answer. So the default surface is a compact list of about five, Enter commits the top
 * one, and the full grid moved behind a Browse disclosure as the exception path it always was.
 *
 * A SCAN NEVER COMES THROUGH HERE. The scan box resolves a barcode (or an exact SKU) against the
 * server and adds in the same gesture; a successful resolve clears the term, which closes this list
 * on the way past. That separation is deliberate and it is a safety property, not a nicety: a list
 * a 200-char-per-second burst could pick from is a list that could pick the wrong product.
 *
 * PRICES HERE ARE THE CUSTOMER'S, AND THE SERVER'S. Each answer is followed by one
 * POST /pos/counter-prices for the visible rows, so a contract account sees its own price, the
 * saving against retail, and any bulk break: all decided by the pricing waterfall the ring itself
 * prices through. Nothing on this surface subtracts one number from another.
 *
 * ONE TAB STOP. The list is a listbox with a roving tabindex (the house grid/rail rule): Tab passes
 * over it, ArrowDown walks into it from the field, ArrowUp at the top walks back out. A results
 * list that swallows Tab is a list a keyboard user has to escape from mid-sale.
 */
@customElement('hos-pos-search-drop')
export class HosPosSearchDrop extends LitElement {
  /** Branch whose stock and prices the rows show. */
  @property({ attribute: 'branch-id' }) branchId = '';
 /** The account on the sale: '' is the ordinary walk-in case, priced at retail. */
  @property({ attribute: 'customer-id' }) customerId = '';
  /** The live term, forwarded from the one scan/search field. */
  @property() term = '';
  /** How many rows to show. Dealer-tunable from the terminal; the client asked for "about five". */
  @property({ type: Number }) rows = DROP_ROWS;

  /**
 * DIB-553 (bounce 2): the ticket's change counter, handed down by the terminal.
   *
   * The echo guard is surface state; the ticket is the terminal's. Without this, a click followed
   * by a cart the terminal emptied underneath the surface left the guard still holding a press
   * whose line was gone, and the next click on the same product added NOTHING. The terminal bumps
   * this on every cart change that is not the add itself; this surface forgets its last activation
   * when it moves.
   */
  @property({ type: Number, attribute: false }) ticketEpoch = 0;


  @state() private hits: CounterHit[] = [];
  @state() private quotes: Record<string, CounterQuote> = {};
  @state() private searching = false;
  @state() private failed = false;
  @state() private roveIdx = 0;

  private timer = 0;
  private seq = 0;

 /** The hit Enter commits: read by the terminal when the field's Enter falls through. */
  topHit(): CounterHit | null {
    return dropView(this.hits, this.rows).top;
  }

  /** Add the top hit, if there is one. Returns whether anything was added. */
  commitTop(): boolean {
    const top = this.topHit();
    if (!top) return false;
    this.add(top);
    return true;
  }

  /** Walk into the list from the field (the terminal forwards ArrowDown out of the scan box). */
  focusFirstRow(): boolean {
    if (this.hits.length === 0) return false;
    this.roveIdx = 0;
    this.focusRow(0);
    return true;
  }

  static styles = [
    focusRing,
    css`
      :host {
        display: block;
        font-family: var(--hos-font-ui);
        color: var(--hos-text);
      }
      .drop {
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-card);
        background: var(--hos-slate-warm);
        overflow: hidden;
      }
      .row {
        display: grid;
        grid-template-columns: 34px minmax(0, 1fr) auto;
        align-items: center;
        gap: 10px;
        width: 100%;
        padding: 8px 12px;
        background: transparent;
        border: 0;
        border-top: 1px solid var(--hos-line);
        color: var(--hos-text);
        font-family: inherit;
        font-size: 13px;
        text-align: left;
        cursor: pointer;
      }
      .row:first-child {
        border-top: 0;
      }
      .row:hover,
      .row[aria-selected='true'] {
        background: var(--hos-deep-earth);
      }
      .id {
        min-width: 0;
      }
      .name {
        font-weight: 600;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
 /* SKU and on-hand ride one dim line: the identifier plus ONE status line, per the row budget. */
      .meta {
        font-size: 11px;
        color: var(--hos-text-dim);
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
      .meta .sku {
        font-family: var(--hos-font-mono);
      }
      /* The money column has a fixed floor and is right-aligned, so five prices form one edge
         instead of five (the DIB-512 finding, held here from the start). */
      .price {
        text-align: right;
        min-width: 13ch;
        font-family: var(--hos-font-mono);
        white-space: nowrap;
      }
      .price .now {
        font-weight: 700;
      }
      .price .was {
        display: block;
        font-size: 11px;
        color: var(--hos-text-dim);
        text-decoration: line-through;
      }
      .price .none {
        font-family: var(--hos-font-ui);
        font-size: 11px;
        color: var(--hos-text-dim);
      }
      .save {
        display: inline-block;
        margin-top: 2px;
        padding: 1px 6px;
        border-radius: var(--hos-radius-pill);
        border: 1px solid var(--hos-success);
        color: var(--hos-success);
        font-family: var(--hos-font-ui);
        font-size: 10px;
        font-weight: 600;
        letter-spacing: 0.02em;
      }
 /* The unreached break is an OFFER, so it stays QUIET: it is not a state of this sale and
         gold never decorates. The words carry it: a quantity and the price at that quantity. */
      .brk {
        display: block;
        margin-top: 2px;
        font-family: var(--hos-font-ui);
        font-size: 10px;
        color: var(--hos-text-dim);
      }
      .foot {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 8px;
        padding: 6px 12px;
        border-top: 1px solid var(--hos-line);
        font-size: 11px;
        color: var(--hos-text-dim);
      }
      .empty {
        padding: 12px;
      }
      .empty b {
        display: block;
        font-size: 13px;
      }
      .empty span {
        display: block;
        margin-top: 2px;
        font-size: 12px;
        color: var(--hos-text-dim);
      }
      .busy {
        padding: 10px 12px;
        font-size: 12px;
        color: var(--hos-text-dim);
      }
    `,
  ];

  protected updated(changed: Map<string, unknown>): void {
    if (changed.has('term') || changed.has('branchId')) this.schedule();
 // A customer arriving (or leaving) re-prices what is already on screen: the whole point of
    // showing the account's price is that it changes when the account does.
    if (changed.has('customerId') && this.hits.length > 0) void this.loadPrices(this.hits);
  }

  disconnectedCallback(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = 0;
    super.disconnectedCallback();
  }

  private schedule(): void {
    if (this.timer) clearTimeout(this.timer);
    const term = this.term;
    if (!shouldQuery(term)) {
      this.seq++; // any answer in flight is now stale
      this.hits = [];
      this.quotes = {};
      this.searching = false;
      this.failed = false;
      return;
    }
    this.searching = true;
    this.timer = window.setTimeout(() => void this.run(term), 140);
  }

  private async run(term: string): Promise<void> {
    const seq = ++this.seq;
    try {
      const hits = await counterSearch(term, this.branchId, this.rows * 4);
      if (seq !== this.seq) return; // a newer keystroke owns the screen
      this.hits = hits;
      this.failed = false;
      this.roveIdx = 0;
      void this.loadPrices(hits);
    } catch {
      if (seq !== this.seq) return;
      this.hits = [];
      this.failed = true;
    } finally {
      if (seq === this.seq) this.searching = false;
    }
  }

  /**
   * The price picture for the rows on screen. Best-effort by design: a pricing outage leaves the
   * rows priceless rather than blank, because a clerk can still add a product and see the ticket
 * price it: an empty result list would have hidden products that exist.
   */
  private async loadPrices(hits: CounterHit[]): Promise<void> {
    const shown = dropView(hits, this.rows).rows;
    if (shown.length === 0) {
      this.quotes = {};
      return;
    }
    const seq = this.seq;
    try {
      const { prices } = await counterPrices(
        this.branchId,
        this.customerId,
        shown.map((h) => ({ product_id: h.product_id, qty_entered: 1, uom_entered: h.sold_by || h.base_uom })),
      );
      if (seq !== this.seq) return;
      const map: Record<string, CounterQuote> = {};
      for (const q of prices) if (q && typeof q.product_id === 'string') map[q.product_id] = q;
      this.quotes = map;
    } catch {
      if (seq === this.seq) this.quotes = {};
    }
  }

  /**
 * DIB-553 (bounce 2): THE THIRD ADD SURFACE, and the one that takes the money.
   *
   * These rows are the counter's DEFAULT way in: the terminal's own comment calls the dropdown
   * "the default surface … a glance" and puts the browse grid behind a <details>. The first cut of
   * the echo guard fixed the hot list and the browse grid and left this one alone, so one
 * double-click on a search row still sold TWO units: measured on the live till at 1440:
   * { row: "Red Mulch DBS-MUL-RED", lines: 1, qty: [2] }. Same rule, same module, same state
   * object, so there is nowhere for a third copy to drift.
   */
  private readonly adder = new AddOnce();

  // DIB-553 (bounce 2) / D6 (round 3): the ticket moved under us, so the press this guard is
 // holding may be protecting a line that is gone. The COMPARISON lives in AddOnce.ticketAt: this
  // component deliberately holds no condition of its own for a `false &&` to switch off.
  protected willUpdate(): void {
    this.adder.ticketAt(this.ticketEpoch);
  }

  private add(h: CounterHit): void {
    this.adder.add(this, h.product_id, counterHitToProduct(h));
  }

  private focusRow(i: number): void {
    void this.updateComplete.then(() =>
      this.renderRoot.querySelectorAll<HTMLElement>('.row')[i]?.focus({ preventScroll: true }),
    );
  }

  private onKeydown(e: KeyboardEvent, len: number): void {
    const next = rowStep(this.roveIdx, e.key, len);
    if (next === null) return;
    e.preventDefault();
    if (next < 0) {
 // Out of the top of the list and back to the field: the list never traps the caret.
      this.dispatchEvent(new CustomEvent('pos-drop-exit', { bubbles: true, composed: true }));
      return;
    }
    this.roveIdx = next;
    this.focusRow(next);
  }

  render() {
    if (!shouldQuery(this.term)) return nothing;
    const v = dropView(this.hits, this.rows);
    if (this.searching && v.rows.length === 0) {
      return html`<div class="drop"><div class="busy" data-testid="drop-busy">Searching…</div></div>`;
    }
    if (v.rows.length === 0) {
 // Empty is never an error, and a search that found nothing is not a fault: role="status".
      // D-107 shape: what is missing, what that means, the way forward, in one calm line.
      return html`<div class="drop">
        <div class="empty" role="status" data-testid="drop-empty">
          <b>${this.failed ? 'Search paused' : 'No match'}</b>
          <span
            >${this.failed
              ? 'The search is not answering right now: scan the barcode, or browse products below.'
              : 'Nothing matches that yet: try fewer letters, scan the label, or browse below.'}</span
          >
        </div>
      </div>`;
    }
    return html`<div class="drop">
      <div
        class="list"
        role="listbox"
        aria-label="Search results"
        data-testid="pos-search-drop"
        @keydown=${(e: KeyboardEvent) => this.onKeydown(e, v.rows.length)}
      >
        ${v.rows.map((h, i) => this.renderRow(h, i))}
      </div>
      ${v.overflow > 0
        ? html`<div class="foot" data-testid="drop-overflow"><span>+${v.overflow} more</span><span>Browse below</span></div>`
        : nothing}
    </div>`;
  }

  private renderRow(h: CounterHit, i: number) {
    const onHand = h.on_hand_branch;
    return html`<button
      class="row"
      role="option"
      aria-selected=${i === this.roveIdx ? 'true' : 'false'}
      tabindex=${i === this.roveIdx ? '0' : '-1'}
      data-sku=${h.sku}
      data-testid="drop-row"
      @focus=${() => (this.roveIdx = i)}
      @click=${() => this.add(h)}
    >
      <hos-product-thumb .productId=${h.product_id} .hasImage=${h.has_image} alt="" .size=${34}></hos-product-thumb>
      <span class="id">
        <span class="name">${h.name}</span>
        <span class="meta"
          ><span class="sku">${h.sku}</span>${onHand === undefined ? nothing : html` · ${onHand} on hand`}</span
        >
      </span>
      ${this.renderPrice(h)}
    </button>`;
  }

  /**
   * The row's price picture: what this customer pays, retail struck beside it when they are paying
   * less, the saving, and any bulk break they have not reached. Every figure comes off the server's
 * quote: see _pos-searchdrop.pricePicture for which parts may be claimed when.
   */
  private renderPrice(h: CounterHit) {
    const p = pricePicture(this.quotes[h.product_id]);
    if (p.unpriceable) {
      // Never CA$0.00, and never a blank cell that reads as a missing column: the row says it will
 // price on the ticket, which is true: addProduct resolves the line server-side.
      return html`<span class="price"><span class="none">prices on add</span></span>`;
    }
    const word = basisWord(p.basis);
    return html`<span class="price">
      <span class="now" data-testid="drop-price">${money(p.customerCents)}</span>
      ${p.showRetail ? html`<span class="was" data-testid="drop-retail">${money(p.retailCents)}</span>` : nothing}
      ${p.showSaving
        ? html`<span class="save" data-testid="drop-saving"
            >Save ${money(p.savingCents)}${word ? html` · ${word}` : nothing}</span
          >`
        : nothing}
      ${p.showBreak
        ? html`<span class="brk" data-testid="drop-break"
            >at ${p.breakQty}${p.breakUom ? ` ${p.breakUom}` : ''} · ${money(p.breakUnitCents)}</span
          >`
        : nothing}
    </span>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-search-drop': HosPosSearchDrop;
  }
}
