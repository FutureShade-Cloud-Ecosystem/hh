// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-product-grid.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { AddOnce } from './_pos-add-dedupe.js';
import { customElement, property, state } from 'lit/decorators.js';
import { BRANCHES, branchName } from './ports.js';
import {
  listProducts,
  counterSearch,
  counterHitToProduct,
  money,
  type PosProduct,
  type CounterHit,
} from './pos-api.js';
import { focusRing } from './styles/focus-ring.js';
import { drawIcon, iconForId } from './_icon-consumer.js';
import './product-thumb.js';

/**
 * SEARCH_DEBOUNCE_MS: under the ~200ms threshold where an input starts to feel laggy, and above the
 * inter-keystroke interval of a fast typist, so a five-letter term costs one query rather than five.
 */
const SEARCH_DEBOUNCE_MS = 180;

interface CardPrice {
  finalCents: number;
  priceUom: string;
}

/**
 * A-19 product grid (US-A19-04 / US-A19-08). Category strip + a card grid: thumbnail, chip, name,
 * SKU (mono), amber price (mono, per-UOM, from pricing.DeriveLinePrice: never typed), click-to-add.
 * Each card shows a glanceable multi-branch on-hand read (home branch highlighted): display only;
 * the gate POS never decrements or allocates stock (M2).
 *
 * the pos-search ticket: THE SEARCH BOX IS NO LONGER A CLIENT-SIDE FILTER.
 *
 * It used to be `products.filter(p => p.name.includes(q) || p.sku.includes(q))` over a full
 * catalogue download. That has three defects that get worse with every SKU added: the payload is
 * unbounded, the match has no notion of relevance (a substring buried in a long name outranks the
 * SKU you typed in full), and it can only see what the payload carries: so barcodes, regional
 * aliases and branch stock are invisible to it by construction.
 *
 * Typing now debounces and asks the server, which ranks (sku/barcode exact > sku prefix > name
 * prefix > name substring > description > alias > trigram) and returns per-branch on-hand with each
 * hit. The browse grid: the no-query state a cashier scrolls when they do not know what they want:
 * is still the catalogue list, because browsing is a different job from searching and paging a
 * ranked search with an empty term would be a worse way to do it.
 *
 * DEBOUNCE, AND WHY THE RESPONSES ARE SEQUENCED. 180ms is under the threshold where an input feels
 * laggy and above the rate at which a fast typist fires requests. But debouncing alone still allows
 * out-of-order responses: "pav" issued before "paver" can land after it, replacing the right results
 * with stale ones. Every request carries a monotonically increasing token and a response whose token
 * is not the latest is dropped.
 */
@customElement('hos-pos-product-grid')
export class HosPosProductGrid extends LitElement {
 /** The cashier's home branch: highlighted in the stock glance. */
  @property() homeBranchId = '';

  /**
   * DIB-488 / D-096: the dealer-level quick-chip toggle. When false the category chip row is ABSENT
 *: not present-but-empty, not an error: because some dealers with huge catalogues find it
   * useless. Any category filter is ignored while the row is off, so a previously-clicked chip can
   * never keep filtering invisibly. Defaults true: every existing mount is unchanged.
   */
  @property({ type: Boolean, attribute: 'show-chips' }) showChips = true;

  /**
   * DIB-488: the LINES workspace keeps the ticket front and center, so once the sale is underway
 * the browse grid folds away behind a "Browse products" affordance (search results always show:
   * a typed query is an explicit ask). The terminal sets this from sale state; the grid keeps only
   * the operator's own pin. Defaults false: every existing mount is unchanged.
   */
  @property({ type: Boolean, attribute: 'auto-collapse' }) autoCollapse = false;

  /**
 * DIB-488 rev 3 (0167): how the results paint: 'CARDS' (the existing product-card grid,
   * untouched) or 'ROWS' (one product per dense row: name · SKU · category tag · list price ·
 * per-branch stock inline). Same data, same behaviors: search, chips, scan feedback and the
   * click-to-price-&-add action are identical; ONLY the shape changes. Defaults 'CARDS', so every
   * existing mount is unchanged; anything that is not the literal 'ROWS' stays on cards.
   */
  @property() presentation = 'CARDS';

 /** rows(): the presentation gate: hardened so garbage off a wire can only mean cards. */
  private rowsMode(): boolean {
    return this.presentation === 'ROWS';
  }

  /**
   * rev 8 (item 1): the UNIFIED smart field. When the host mounts the unified scan box, the grid
 * stops rendering its OWN search input and takes the term from here instead: null (the
 * default) keeps the internal input, so every other mount is untouched. Every search behavior:
 * debounce, sequencing, chips interplay, the live region, the roving results: is byte-identical:
   * the property feeds the SAME onQuery() the internal input fed.
   */
  @property({ attribute: false }) externalTerm: string | null = null;

  /**
   * rev 8 (item 4): the dealer's CURATED single-row chip set (pos_policy.quick_chip_set, derived
   * from top sellers until curated). Empty = the full auto-derived chip row exactly as before.
   * When present: ★ Favorites leads, the curated categories follow on ONE row (no wrapping), and
   * the full category list stays reachable through the "All categories" browse affordance.
   */
  @property({ attribute: false }) curated: string[] = [];

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

 /** rev 8: the ★ Favorites chip's state: filters browse to the catalogue's featured products. */
  @state() private favoritesOn = false;

  /** rev 8: the browse affordance opened the FULL category list over the curated row. */
  @state() private allChipsOpen = false;

  protected willUpdate(changed: Map<string, unknown>): void {
    // The unified field's term drives the same door the internal input drove. Guarded on real
    // change so unrelated renders never re-issue a search.
    if (changed.has('externalTerm') && this.externalTerm !== null && this.externalTerm !== this.query) {
      this.onQuery(this.externalTerm);
    }
    // DIB-553 (bounce 2) / D6 (round 3): the ticket moved under us, so the press this guard is
 // holding may be protecting a line that is gone. Unconditional: AddOnce.ticketAt owns the
    // comparison, because `if (false && changed.has('ticketEpoch'))` here left 3311 tests green.
    this.adder.ticketAt(this.ticketEpoch);
  }

  /** The operator explicitly opened (pinned) the browse grid while autoCollapse is on. */
  @state() private browsePinned = false;

  @state() private products: PosProduct[] = [];
  @state() private query = '';
  @state() private category = '';
  @state() private loading = true;
  @state() private error = '';
  @state() private prices: Record<string, CardPrice> = {};
  @state() private stock: Record<string, Record<string, number>> = {};

 // ── the pos-search ticket server-side search state ──
  @state() private hits: CounterHit[] = [];
  @state() private searching = false;
  @state() private searchError = '';
  /** Debounce handle, and the sequence token that makes late responses droppable. */
  private searchTimer: ReturnType<typeof setTimeout> | null = null;
  private searchSeq = 0;

  static styles = [
    focusRing,
    css`
    :host {
      display: block;
      font-family: var(--hos-font-ui);
      color: var(--hos-text);
      height: 100%;
    }
    .bar {
      display: flex;
      gap: 10px;
      margin-bottom: 12px;
    }
    input,
    select {
      background: var(--hos-deep-earth);
      color: var(--hos-text);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      padding: 9px 11px;
      font-size: 14px;
      font-family: inherit;
    }
    input {
      flex: 1;
    }
    input::placeholder {
      color: var(--hos-text-dim); /* rev 5: the UA default gray measured under 4.5:1 */
      opacity: 1;
    }
    .chips {
      display: flex;
      flex-wrap: wrap;
      gap: 6px;
      margin-bottom: 12px;
    }
 /* rev 8 (item 4): the CURATED row is a single line: never wraps; a hard-narrow panel scrolls
       the row inside itself instead (the responsive house rule: wide content scrolls its own box). */
    .chips.onerow {
      flex-wrap: nowrap;
      overflow-x: auto;
      scrollbar-width: none;
    }
    .chips.onerow .chip {
      flex: none;
      white-space: nowrap;
    }
    .chip.fav.active {
      color: var(--hos-stone-amber-text);
      border-color: var(--hos-stone-amber);
    }
 /* rev 5: an author display declaration beats the UA's [hidden] rule: the coded
       ?hidden=searchMode() intent never actually hid the chips during a search. */
    .chips[hidden] {
      display: none;
    }
    /* an inline-flex centred row keeps the button the exact height the words
      line box always gave it, the drawn glyph beside them centred (PR 55 review round; the
      quick-action-bar precedent) */
    .chip {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      background: transparent;
      border: 1px solid var(--hos-line);
      color: var(--hos-text-dim);
      border-radius: var(--hos-radius-pill);
      padding: 5px 12px;
      font-size: 12px;
      cursor: pointer;
      font-family: inherit;
    }
    .chip.active {
      color: var(--hos-stone-amber-text);
      border-color: var(--hos-stone-amber);
    }
    /* DIB-258: was a fixed 2-up grid, which wasted horizontal space on the wide counter displays
       and buried commonly-sold products below the fold. auto-fill + minmax lets the grid pack as
       many ~180px tiles as the viewport allows: typically 4-5 across on a wide screen: while
       still collapsing to 2 (and to 1 on the narrowest) so nothing overflows on a small terminal.
       Tiles have no fixed width, so they simply flex to the computed track. */
    .grid {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
      gap: 12px;
    }
    .card {
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-card);
      padding: 14px;
      cursor: pointer;
      transition: border-color 0.15s ease;
    }
    .card:hover {
      border-color: var(--hos-stone-amber);
    }
    /* Ship-review a11y: visible keyboard focus for the now-focusable product tile. */
    .card:focus-visible {
      outline: 2px solid var(--hos-stone-amber-text);
      outline-offset: 2px;
    }
    .hitrow {
      display: flex;
      gap: 10px;
      align-items: center;
    }
    .hitmain {
      min-width: 0;
    }
    .hitmain .name {
      margin: 0 0 2px;
    }
    .why {
      margin-top: 8px;
      font-size: 10px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-info-blue);
    }
    .searching {
      align-self: center;
      font-size: 12px;
      color: var(--hos-text-dim);
      white-space: nowrap;
    }
    .cat {
      font-size: 10px;
      text-transform: uppercase;
      letter-spacing: 0.1em;
      color: var(--hos-info-blue);
    }
    .name {
      font-size: 14px;
      font-weight: 600;
      margin: 4px 0 2px;
    }
    .sku {
      font-family: var(--hos-font-mono);
      font-size: 11px;
      color: var(--hos-text-dim);
    }
    .price {
      font-family: var(--hos-font-mono);
      font-size: 16px;
      color: var(--hos-stone-amber-text);
      font-weight: 600;
      margin-top: 8px;
    }
    .price .uom {
      font-size: 11px;
      color: var(--hos-text-dim);
    }
    .stock {
      display: flex;
      gap: 10px;
      margin-top: 8px;
      font-size: 11px;
    }
    .stock .b {
      font-family: var(--hos-font-mono);
      color: var(--hos-text-dim);
    }
    .stock .b.home {
      color: var(--hos-text);
    }
    .stock .b .qty {
      color: var(--hos-success);
    }
 /* ── rev 6 (item I): in ROW view the branch-stock cluster is COLUMN-ALIGNED: every row
       renders every branch in the same order on equal-width tracks (an absent read prints an
       honest:), so the numbers line up down the list like a table. Card views keep the inline
       cluster (cards do not align across a wrapped grid). ── */
    .stock.cols {
      display: grid;
      grid-auto-flow: column;
      grid-auto-columns: minmax(11ch, auto);
      gap: 0 12px;
    }
    .stock.cols .b {
      text-align: right;
      white-space: nowrap;
    }
    .stock.cols .b .none {
      color: var(--hos-text-dim);
    }
    .muted {
      color: var(--hos-text-dim);
      font-size: 13px;
    }
 /* rev 5: the browse pane's honest empty state: centered like the ticket's, never an error. */
    .browse-empty {
      text-align: center;
      padding: 18px 0;
    }
 /* rev 6 (item I): the no-results state gets the same centered treatment PLUS a next step:
       a flat one-liner at the top-left read as the pane failing, not as an answer. */
    .noresults {
      text-align: center;
      padding: 22px 0;
    }
    .noresults .nowhat {
      font-size: 14px;
      color: var(--hos-text);
      margin-bottom: 4px;
    }
    .noresults .nextstep {
      font-size: 12px;
      color: var(--hos-text-dim);
    }
 /* ── DIB-488 rev 3: the ROWS presentation: one product per dense row. Same tokens and the
       same interaction affordances as .card (hover border, visible keyboard focus); the name takes
       the free track and truncates honestly, stock + price sit flush right. ── */
    .rows {
      display: flex;
      flex-direction: column;
      gap: 6px;
      /* DIB-512: the rows list is a size container so the wrap rule below can key off the PANEL
         width instead of the viewport. */
      container-type: inline-size;
    }
    .rowitem {
      display: flex;
      align-items: center;
 /* DIB-512: at working widths rows do NOT wrap: with flex-wrap, a long name/category moved
         the whole money cluster to a second line BEFORE any text shrank (wrap happens on base
         sizes), so the price edge went ragged row to row. Nowrap lets the name/tags truncate
         first and every price shares one flush-right edge. The rev-5 hard-narrow concern keeps
         its answer in the container query below. */
      flex-wrap: nowrap;
      gap: 6px 12px;
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      padding: 7px 12px;
      cursor: pointer;
      transition: border-color 0.15s ease;
      min-width: 0;
    }
    .rowitem:hover {
      border-color: var(--hos-stone-amber);
    }
    .rowitem:focus-visible {
      outline: 2px solid var(--hos-stone-amber-text);
      outline-offset: 2px;
    }
 /* DIB-488 rev 7: the row's thumbnail: immediately left of the name, never shrinking, never a
       tab stop. Decorative (alt="") because the row button's accessible name is the product name
       beside it; the component's own glyph fallback keeps a no-photo product honest. */
    .rowitem hos-product-thumb {
      flex: none;
    }
    .rowitem .name {
      margin: 0;
 /* rev 5: the name: the one field that identifies the product: was the ONLY shrinkable
         item and collapsed to 0px before any sibling yielded. It now takes the free space with a
         floor so the ellipsis always shows something; the SKU is what truncates instead. */
      flex: 1 1 auto;
      min-width: 72px;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .rowitem .sku {
      flex: 0 1 auto;
      margin: 0;
      min-width: 4ch;
      max-width: 22ch;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    /* DIB-512 · the tag columns yield BEFORE the price wraps: a long name/category used to push
       the money cluster to a second line, so the price edge went ragged row to row. Name and tags
       truncate with an ellipsis; the price column below is fixed-width and flush right. */
    .rowitem .cat,
    .rowitem .why {
      flex: 0 1 auto;
      min-width: 0;
      margin: 0;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
 /* rev 5: category tag and match-reason were the SAME 10px caps info-blue side by side:
       "PAVER NAME MATCH" read as one garbled label. The reason drops to the dim ink in rows;
       the card view keeps it on its own line where the two never collide. */
    .rowitem .why {
      color: var(--hos-text-dim);
    }
    .rowitem .stock {
      margin: 0 0 0 auto; /* pushes stock + price to the right edge */
      flex: none;
      flex-wrap: wrap;
      justify-content: flex-end;
    }
    .rowitem .price {
      margin: 0 0 0 auto; /* when there is no stock read, the price alone takes the right edge */
      flex: none;
      font-size: 14px;
      white-space: nowrap;
 /* DIB-512 · a fixed-floor, right-aligned money column: every row's price shares one edge
         instead of landing wherever the name let it. */
      min-width: 12ch;
      text-align: right;
    }
    .rowitem .stock + .price {
      margin-left: 0; /* stock already claimed the free space: the price sits beside it */
    }
 /* DIB-512: the rev-5 hard-narrow protection, scoped to where it applies: a truly narrow
       panel wraps the money/stock cluster to a second line instead of clipping at the edge. */
    @container (max-width: 560px) {
      .rowitem {
        flex-wrap: wrap;
      }
    }
 /* DIB-488: the fold/unfold affordance for the browse grid in the LINES workspace. */
    .browsetoggle {
      display: block;
      width: 100%;
      background: transparent;
      border: 1px dashed var(--hos-line);
      border-radius: var(--hos-radius-control);
      color: var(--hos-text-dim);
      font-family: inherit;
      font-size: 12px;
      padding: 8px;
      margin-bottom: 12px;
      cursor: pointer;
    }
    .browsetoggle:hover {
      border-color: var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
    }
    .err {
      color: var(--hos-safety-red);
      font-size: 13px;
    }
    /* rev 5: visually hidden, still in the accessibility tree (the classic clip idiom). */
    .visclip {
      position: absolute;
      width: 1px;
      height: 1px;
      padding: 0;
      margin: -1px;
      overflow: hidden;
      clip: rect(0 0 0 0);
      white-space: nowrap;
      border: 0;
    }
  `,
  ];

  connectedCallback(): void {
    super.connectedCallback();
    void this.load();
  }

 // ── rev 6 (item K): ROVING TABINDEX over the result set: the commander-approved opinion #8.
  // The lookup results were 30+ sequential tab stops standing between the two panels; the result
  // set is now ONE stop (Arrow keys move within, Home/End jump, Tab leaves). Managed imperatively:
 // the templates keep their static tabindex="0" (each item IS a focusable button: the SC 2.1.1
  // floor), and this pass parks every item but the roving one at -1 after each render. Enter/Space
  // activation stays on the items themselves, untouched.

 /** The roving index over the visible result items. Plain field: never paints on its own. */
  private roveIdx = 0;

  /** Every visible result item (hit/browse × card/row share the two classes), in DOM order. */
  private resultItems(): HTMLElement[] {
    return [...this.renderRoot.querySelectorAll<HTMLElement>('.card[role="button"], .rowitem[role="button"]')];
  }

  /** Re-park the tab stops: exactly one item (the roving one, clamped) keeps tabindex 0. */
  private applyRoving(): void {
    const items = this.resultItems();
    if (items.length === 0) return;
    if (this.roveIdx >= items.length) this.roveIdx = items.length - 1;
    if (this.roveIdx < 0) this.roveIdx = 0;
    items.forEach((el, i) => {
      el.tabIndex = i === this.roveIdx ? 0 : -1;
    });
  }

  protected updated(): void {
    this.applyRoving(); // every render may add/replace items: re-park the stops
  }

  /** A click / programmatic focus landing on an item adopts it as the roving stop. */
  private onResultsFocusin = (e: Event): void => {
    const items = this.resultItems();
    const i = items.indexOf(e.target as HTMLElement);
    if (i >= 0 && i !== this.roveIdx) {
      this.roveIdx = i;
      this.applyRoving();
    }
  };

  /** Arrows move within the set (Right/Down next, Left/Up previous), Home/End jump; Tab leaves. */
  private onResultsKeydown = (e: KeyboardEvent): void => {
    let delta = 0;
    let absolute = -1;
    switch (e.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        delta = 1;
        break;
      case 'ArrowLeft':
      case 'ArrowUp':
        delta = -1;
        break;
      case 'Home':
        absolute = 0;
        break;
      case 'End':
        absolute = Number.MAX_SAFE_INTEGER;
        break;
      default:
        return;
    }
    const items = this.resultItems();
    if (items.length === 0) return;
    e.preventDefault(); // the panel must not scroll under the gesture
    const cur = Math.max(0, items.indexOf(e.target as HTMLElement));
    const next =
      absolute >= 0 ? Math.min(absolute, items.length - 1) : Math.min(items.length - 1, Math.max(0, cur + delta));
    this.roveIdx = next;
    this.applyRoving();
    items[next].focus();
  };

  private async load(): Promise<void> {
    this.loading = true;
    this.error = '';
    try {
      this.products = await listProducts();
      // rev 5: the LIST response already carries the retail anchor and per-branch on-hand for
 // EVERY row (product.Service.List enrichment: the same source the counter-search hits
      // show). The old path ignored the price entirely ("click to price & add" on every browse
      // tile, with `this.prices` dead state) and burned one stock-glance round trip per product
 // capped at an arbitrary 24: beyond which tiles silently showed no stock at all. Rows
      // with no anchor are simply absent from the map: no price claim (never a fake zero).
      const prices: Record<string, CardPrice> = {};
      const stock: Record<string, Record<string, number>> = {};
      for (const p of this.products) {
        if (typeof p.list_price_cents === 'number') {
          prices[p.id] = { finalCents: p.list_price_cents, priceUom: p.list_price_uom ?? '' };
        }
        if (p.on_hand_by_branch && Object.keys(p.on_hand_by_branch).length > 0) {
          stock[p.id] = p.on_hand_by_branch;
        }
      }
      this.prices = prices;
      this.stock = stock;
    } catch {
      this.error = 'Could not reach the API: is the server running?';
    } finally {
      this.loading = false;
    }
  }

  private categories(): string[] {
    const set = new Set<string>();
    for (const p of this.products) if (p.application) set.add(p.application);
    return [...set].sort();
  }

  // browseList is the NO-QUERY grid: the catalogue, optionally narrowed by the category chips. This
  // is the browse path, and it stays client-side because it is a fixed, already-downloaded set.
 // DIB-488: with the chip row configured OFF the category filter is ignored outright: a filter
  // with no visible control is a trap, not a feature.
  private browseList(): PosProduct[] {
 // rev 8 (item 4): the ★ Favorites chip narrows browse to the catalogue's featured products:
    // a filter exactly like the category chips (and exclusive with them; picking either clears
    // the other). Only meaningful while the chip row is on.
    if (this.showChips && this.favoritesOn) {
      return this.products.filter((p) => p.is_featured === true);
    }
    const cat = this.showChips ? this.category : '';
    return this.products.filter((p) => !cat || p.application === cat);
  }

  /** DIB-488: the browse grid is folded away (LINES workspace, sale underway, no explicit ask). */
  private browseCollapsed(): boolean {
    return this.autoCollapse && !this.browsePinned && !this.searchMode() && (!this.showChips || this.category === '');
  }

  disconnectedCallback(): void {
    super.disconnectedCallback();
    if (this.searchTimer !== null) clearTimeout(this.searchTimer);
  }

  // onQuery debounces the server search. A term under two characters is not sent at all: one
 // keystroke matches almost everything, so the result is noise the cashier reads past: and it is
  // the most expensive query of the lot.
  private onQuery(value: string): void {
    this.query = value;
    if (this.searchTimer !== null) clearTimeout(this.searchTimer);
    const term = value.trim();
    if (term.length < 2) {
      // Cancel in flight AND clear: bumping the token means any response still on the wire for the
      // longer term is dropped when it lands, so an emptied box never repopulates itself.
      this.searchSeq++;
      this.hits = [];
      this.searching = false;
      this.searchError = '';
      return;
    }
    this.searching = true;
    this.searchTimer = setTimeout(() => void this.runSearch(term), SEARCH_DEBOUNCE_MS);
  }

  private async runSearch(term: string): Promise<void> {
    const seq = ++this.searchSeq;
    try {
      const hits = await counterSearch(term, this.homeBranchId, 24);
      if (seq !== this.searchSeq) return; // a newer query has been issued: this answer is stale
      this.hits = hits;
      this.searchError = '';
    } catch (err) {
      if (seq !== this.searchSeq) return;
      this.hits = [];
 // rev 5: err.message here is the transport's internal string ('search 500'): a cashier
      // needs a sentence with a next step, not a status code.
      void err;
      this.searchError = 'Search isn’t responding: try again, or scan the barcode.';
    } finally {
      if (seq === this.searchSeq) this.searching = false;
    }
  }

  /**
 * DIB-553: the second half of a double-click is not a second sale: the same rule every other add
   * surface uses, from the same module, because all three dispatch the same event from a real
   * <button> and a habit of double-clicking rang up two units on any of them. Measured before the
   * guard: one double-click put the product on the ticket at quantity 2.
   *
   * ONE instance for BOTH of this component's add paths (a server search hit and a browse tile),
   * deliberately: they are two renderings of the same catalogue, the same product can be on both at
   * once, and a gesture that crosses between them is still one gesture.
   */
  private readonly adder = new AddOnce();

  private addHit(h: CounterHit): void {
    this.adder.add(this, h.product_id, counterHitToProduct(h));
  }

  private add(p: PosProduct): void {
    this.adder.add(this, p.id, p);
  }

  private renderStock(productId: string) {
    const onHand = this.stock[productId];
    if (!onHand) return nothing;
    return html`<div class="stock">
      ${BRANCHES.map((b) => {
        const qty = onHand[b.id];
        if (qty === undefined) return nothing;
        return html`<span class="b ${b.id === this.homeBranchId ? 'home' : ''}">
          ${branchName(b.id)} <span class="qty">${qty}</span>
        </span>`;
      })}
    </div>`;
  }

  /**
 * rev 6 (item I): the ROW view's column-aligned branch stock: every row prints every branch in
 * the same order on equal-width tracks, with the explicit "(no read)" mark where that branch has no read, so the
 * numbers align down the list. A product with NO stock read at all still renders nothing:
   * absent data is not an error and not a stock claim (the card views keep the old inline cluster).
   */
  private renderStockCols(onHand: Record<string, number> | undefined) {
    if (!onHand || Object.keys(onHand).length === 0) return nothing;
    return html`<div class="stock cols">
      ${BRANCHES.map((b) => {
        const qty = onHand[b.id];
        return html`<span class="b ${b.id === this.homeBranchId ? 'home' : ''}">
          ${branchName(b.id)}
          ${qty === undefined ? html`<span class="none">(no read)</span>` : html`<span class="qty">${qty}</span>`}
        </span>`;
      })}
    </div>`;
  }

  render() {
    if (this.loading) return html`<p class="muted">Loading products…</p>`;
    if (this.error) return html`<p class="err" role="alert">${this.error}</p>`;
    const cats = this.categories();
    return html`
      ${this.externalTerm === null
        ? html`<div class="bar">
            <input
              data-testid="pos-search-input"
              placeholder="Search products, SKU or what the customer calls it…"
              aria-label="Search products"
              aria-describedby="search-status"
              .value=${this.query}
              @input=${(e: Event) => this.onQuery((e.target as HTMLInputElement).value)}
            />
            ${this.searching ? html`<span class="searching" data-testid="pos-search-busy">searching…</span>` : nothing}
          </div>`
        : this.searching
          ? html`<div class="bar unifiedbusy"><span class="searching" data-testid="pos-search-busy">searching…</span></div>`
          : nothing}
      ${/* rev 5: the polite status line the scan box already has: searching, the result count,
         or the no-match outcome, announced without moving focus off the box. Visually clipped;
         the visible grid is the sighted answer. */ ''}
      <div id="search-status" class="visclip" role="status" aria-live="polite">${this.searchStatusText()}</div>
      ${this.showChips ? this.renderChips(cats) : nothing}
      ${this.searchMode() ? this.renderHits() : this.renderBrowse()}
    `;
  }

 /** rev 8 (item 4): pick a category chip (clears favorites: the two filters are exclusive). */
  private pickCategory(c: string): void {
    this.category = c;
    this.favoritesOn = false;
  }

 /** rev 8 (item 4): the ★ Favorites chip: toggles the featured filter, clears any category. */
  private toggleFavorites(): void {
    this.favoritesOn = !this.favoritesOn;
    this.category = '';
  }

  /**
   * rev 8 (item 4): the chip row. With a CURATED set: ★ Favorites + the short curated categories
 * on ONE row (nowrap: the curated row never wraps), and an "All categories" affordance that
   * opens the FULL auto-derived list (the pre-rev-8 row, wrapping as before) so nothing became
   * unreachable. Without a curated set: the full row exactly as before.
   */
  private renderChips(cats: string[]) {
    const hasFavs = this.products.some((p) => p.is_featured === true);
    const curated = this.curated.filter((c) => cats.includes(c));
    const full = html`<button class="chip ${this.category === '' && !this.favoritesOn ? 'active' : ''}" @click=${() => this.pickCategory('')}>
        All
      </button>
      ${cats.map(
        (c) => html`<button class="chip ${this.category === c ? 'active' : ''}" @click=${() => this.pickCategory(c)}>
          ${c}
        </button>`,
      )}`;
    if (curated.length === 0) {
      return html`<div class="chips" ?hidden=${this.searchMode()}>${full}</div>`;
    }
    if (this.allChipsOpen) {
      return html`<div class="chips" ?hidden=${this.searchMode()}>
        ${hasFavs ? this.renderFavChip() : nothing}${full}
        <button class="chip" data-testid="pos-chips-less" title="Back to the short curated row" @click=${() => (this.allChipsOpen = false)}>
          ${drawIcon(iconForId('pencil:left'), 12)} Fewer
        </button>
      </div>`;
    }
    return html`<div class="chips onerow" ?hidden=${this.searchMode()}>
      ${hasFavs ? this.renderFavChip() : nothing}
      ${curated.map(
        (c) => html`<button class="chip ${this.category === c ? 'active' : ''}" @click=${() => this.pickCategory(c)}>
          ${c}
        </button>`,
      )}
      <button
        class="chip"
        data-testid="pos-chips-all"
        title="Show every category"
        aria-expanded="false"
        @click=${() => (this.allChipsOpen = true)}
      >
        All categories ${drawIcon(iconForId('pencil:right'), 12)}
      </button>
    </div>`;
  }

  /** rev 8: the ★ Favorites chip (only offered when the catalogue actually flags favorites). */
  private renderFavChip() {
    return html`<button
      class="chip fav ${this.favoritesOn ? 'active' : ''}"
      data-testid="pos-chip-favorites"
      aria-pressed=${this.favoritesOn ? 'true' : 'false'}
      title="The dealer's featured products"
      @click=${() => this.toggleFavorites()}
    >
      ${drawIcon(iconForId('lucide:star'), 12)} Favorites
    </button>`;
  }

  /** searchMode: a term of two or more characters puts the pane in server-search mode. */
  private searchMode(): boolean {
    return this.query.trim().length >= 2;
  }

 /** rev 5: what the polite live region speaks: busy, the ranked count, or the no-match state. */
  private searchStatusText(): string {
    if (!this.searchMode()) return '';
    if (this.searching) return 'Searching…';
    if (this.searchError) return ''; // the role=alert error line already announces
    const n = this.hits.length;
    if (n === 0) return `Nothing matches “${this.query.trim()}”.`;
    return `${n} result${n === 1 ? '' : 's'} for “${this.query.trim()}”.`;
  }

  // renderHits is the ranked server result list. Each row shows WHY it matched, because a search
 // that surfaces a row for a reason the cashier cannot see is a search they stop trusting: and the
  // alias arm in particular ("patio stone" finding a Slab) looks like a bug until it is labelled.
 // DIB-488 rev 3: the ROWS presentation reshapes the same hits: same add action, same match
 // label, same stock read: while the card grid below stays exactly as it was.
  private renderHits() {
    if (this.searchError) return html`<p class="err" role="alert">${this.searchError}</p>`;
    if (this.hits.length === 0) {
 // rev 6 (item I): the centered no-results state with a NEXT STEP: a flat one-liner at the
      // top-left read as the pane breaking; this reads as an answer with somewhere to go.
      return html`<div class="muted noresults" data-testid="pos-search-empty">
        ${this.searching
          ? 'Searching…'
          : html`<div class="nowhat">Nothing matches “${this.query.trim()}”.</div>
              <div class="nextstep">Try the exact SKU or another word for it: or scan the barcode.</div>`}
      </div>`;
    }
    if (this.rowsMode()) return this.renderHitRows();
    return html`<div
      class="grid"
      data-testid="pos-search-results"
      @keydown=${this.onResultsKeydown}
      @focusin=${this.onResultsFocusin}
    >
      ${this.hits.map((h) => {
        const tip = h.sku ? `${h.name} · ${h.sku}` : h.name;
        return html`<div
          class="card hit"
          role="button"
          tabindex="0"
          title=${tip}
          data-sku=${h.sku}
          @click=${() => this.addHit(h)}
          @keydown=${(e: KeyboardEvent) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault();
              this.addHit(h);
            }
          }}
        >
          <div class="hitrow">
            <hos-product-thumb
              .productId=${h.product_id}
              .hasImage=${h.has_image}
              .alt=${h.name}
              .category=${h.application ?? ''}
              .size=${44}
            ></hos-product-thumb>
            <div class="hitmain">
              <div class="name">${h.name}</div>
              <div class="sku">${h.sku}</div>
            </div>
          </div>
          <div class="why" data-testid="match-reason">${matchLabel(h.match_reason)}</div>
          ${h.list_price_cents !== undefined
            ? html`<div class="price">${money(h.list_price_cents)}<span class="uom"> list</span></div>`
            : html`<div class="price"><span class="uom">click to price &amp; add</span></div>`}
          ${this.renderHitStock(h)}
        </div>`;
      })}
    </div>`;
  }

  /**
 * DIB-488 rev 3: the search hits as dense listed rows: name · SKU · category tag · why-it-
   * matched · list price · per-branch stock inline. The SAME data and the SAME click-to-price-&-add
   * action as the hit cards (Enter/Space included); only the presentation differs, so every
   * data-testid the cards carry rides the rows too.
   */
  private renderHitRows() {
    return html`<div
      class="rows"
      data-testid="pos-search-results"
      @keydown=${this.onResultsKeydown}
      @focusin=${this.onResultsFocusin}
    >
      ${this.hits.map((h) => {
        const tip = h.sku ? `${h.name} · ${h.sku}` : h.name;
        return html`<div
          class="rowitem hit"
          role="button"
          tabindex="0"
          title=${tip}
          data-sku=${h.sku}
          @click=${() => this.addHit(h)}
          @keydown=${(e: KeyboardEvent) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault();
              this.addHit(h);
            }
          }}
        >
          <hos-product-thumb
            .productId=${h.product_id}
            .hasImage=${h.has_image}
            alt=""
            .category=${h.application ?? ''}
            .size=${30}
          ></hos-product-thumb>
          <div class="name">${h.name}</div>
          <div class="sku">${h.sku}</div>
          <div class="cat">${h.application || h.color_family || 'Product'}</div>
          <div class="why" data-testid="match-reason">${matchLabel(h.match_reason)}</div>
          ${this.renderStockCols(h.on_hand_by_branch)}
          ${h.list_price_cents !== undefined
            ? html`<div class="price">${money(h.list_price_cents)}<span class="uom"> list</span></div>`
            : html`<div class="price"><span class="uom">click to price &amp; add</span></div>`}
        </div>`;
      })}
    </div>`;
  }

 // renderHitStock uses the on-hand the search already returned: no per-tile glance round trip.
  private renderHitStock(h: CounterHit) {
    const onHand = h.on_hand_by_branch;
    if (!onHand) return nothing;
    return html`<div class="stock">
      ${BRANCHES.map((b) => {
        const qty = onHand[b.id];
        if (qty === undefined) return nothing;
        return html`<span class="b ${b.id === this.homeBranchId ? 'home' : ''}">
          ${branchName(b.id)} <span class="qty">${qty}</span>
        </span>`;
      })}
    </div>`;
  }

  private renderBrowse() {
 // DIB-488: folded away: one quiet affordance back in. Not an empty state and not an error;
    // the ticket owns the space and the operator asked for it to.
    if (this.browseCollapsed()) {
      return html`<button class="browsetoggle" data-testid="pos-browse-open" @click=${() => (this.browsePinned = true)}>
        Browse products
      </button>`;
    }
    return html`
      ${this.autoCollapse && this.browsePinned
        ? html`<button class="browsetoggle" data-testid="pos-browse-close" @click=${() => (this.browsePinned = false)}>
            Hide browse
          </button>`
        : nothing}
      ${this.browseList().length === 0
        ? html`<p class="muted browse-empty" data-testid="pos-browse-empty">
            ${this.products.length === 0
              ? 'No products on file yet: items appear here as soon as they are added.'
              : 'Nothing in this category: pick another chip, or search above.'}
          </p>`
        : this.rowsMode()
          ? this.renderBrowseRows()
          : this.renderBrowseGrid()}
    `;
  }

  /**
 * WAVE 2 · finding 20d: THE BROWSE TILES DO NOT SPECULATE ABOUT PICTURES.
   *
   * Measured: eleven `404 /api/v1/products/<id>/image` every single time Browse opens, at 1920 and
   * at 1024, one per visible tile. hos-product-thumb catches the error and paints its stone-stack
 * placeholder, so nothing LOOKS broken: but the browser logs every failed image load as a
   * console error regardless, and the walkthrough harness's expected-refusal filter only exempts a
   * 401/403, which meant this surface could not produce a clean console ledger for any run that
   * opened Browse. The counter's own backend states the rule: counter_search.go's HasImage exists
   * "so the UI skips a request that would 404".
   *
   * THE SEARCH SIDE ALREADY OBEYS IT (`.hasImage=${h.has_image}` on both hit renderers) because
   * the counter-search wire carries the flag. The product LIST does not carry it yet, so the two
 * browse renderers below pass `p.has_image ?? false`: "ask only when the wire has said yes".
   * That is a real trade and it is the honest side of it: on this dealer's catalogue no product
   * has a thumbnail, so the pixels are identical (the placeholder either way) and the eleven
   * console errors are gone; a product that DOES get a picture still shows it everywhere the wire
   * reports it, including the search results a clerk uses to find one specific product. The `??`
 * is the self-healing half: the day `/api/v1/products` emits has_image (backend, and outside
   * this lane) the browse grid starts painting thumbnails with no further change here.
   */
 /** DIB-488 rev 3: the browse catalogue as dense rows (same list, same add path as the cards). */
  private renderBrowseRows() {
    return html`<div class="rows" @keydown=${this.onResultsKeydown} @focusin=${this.onResultsFocusin}>
      ${this.browseList().map((p) => {
        const price = this.prices[p.id];
        const tip = p.sku ? `${p.name} · ${p.sku}` : p.name;
        return html`<div
          class="rowitem"
          role="button"
          tabindex="0"
          title=${tip}
          @click=${() => this.add(p)}
          @keydown=${(e: KeyboardEvent) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault();
              this.add(p);
            }
          }}
        >
          <hos-product-thumb .productId=${p.id} .hasImage=${p.has_image ?? false} alt="" .category=${p.application} .size=${30}></hos-product-thumb>
          <div class="name">${p.name}</div>
          <div class="sku">${p.sku}</div>
          <div class="cat">${p.application || p.color_family || 'Product'}</div>
          ${this.renderStockCols(this.stock[p.id])}
          ${price
            ? html`<div class="price">${money(price.finalCents)}<span class="uom"> list${price.priceUom ? ` / ${price.priceUom}` : ''}</span></div>`
            : html`<div class="price"><span class="uom">click to price &amp; add</span></div>`}
        </div>`;
      })}
    </div>`;
  }

  /**
   * The CARDS-presentation browse grid. rev 6 (item H, the commander-approved opinion #3): the
 * browse tiles now wear the SEARCH-HIT anatomy: thumbnail + name/SKU in the hitrow, then the
   * small caps line (the CATEGORY, standing where a hit's match-reason stands; the match slot is
 * empty on browse because nothing was matched), then price, then stock: so the two result
   * surfaces read as one tile system instead of two. Same add path, same tooltip, same SC 2.1.1
   * real-button posture as before.
   */
  private renderBrowseGrid() {
    return html`
      <div class="grid" @keydown=${this.onResultsKeydown} @focusin=${this.onResultsFocusin}>
        ${this.browseList().map((p) => {
          const price = this.prices[p.id];
          const tip = p.sku ? `${p.name} · ${p.sku}` : p.name;
          return html`<div
            class="card"
            role="button"
            tabindex="0"
            title=${tip}
            @click=${() => this.add(p)}
            @keydown=${(e: KeyboardEvent) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                this.add(p);
              }
            }}
          >
            <div class="hitrow">
              <hos-product-thumb .productId=${p.id} .hasImage=${p.has_image ?? false} .alt=${p.name} .category=${p.application} .size=${44}></hos-product-thumb>
              <div class="hitmain">
                <div class="name">${p.name}</div>
                <div class="sku">${p.sku}</div>
              </div>
            </div>
            <div class="why">${p.application || p.color_family || 'Product'}</div>
            ${price
              ? html`<div class="price">
                  ${money(price.finalCents)}<span class="uom"> list${price.priceUom ? ` / ${price.priceUom}` : ''}</span>
                </div>`
              : html`<div class="price">
                  <span class="uom">click to price &amp; add</span>
                </div>`}
            ${this.renderStock(p.id)}
          </div>`;
        })}
      </div>
    `;
  }
}

// matchLabel turns the server's rank arm into words at the counter. Kept beside the component
// because it is presentation, not protocol: the wire values stay stable regardless of wording.
function matchLabel(reason: string): string {
  switch (reason) {
    case 'EXACT':
      return 'exact SKU / barcode';
    case 'SKU_PREFIX':
      return 'SKU match';
    case 'NAME_PREFIX':
    case 'NAME':
      return 'name match';
    case 'DESCRIPTION':
      return 'description match';
    case 'ALIAS':
      return 'matched an alias';
    case 'FUZZY':
      return 'close match';
    default:
      return reason.toLowerCase();
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-product-grid': HosPosProductGrid;
  }
}
