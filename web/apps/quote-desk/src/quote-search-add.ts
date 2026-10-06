// The Quote Desk's search and add box (decision log row 991): the desk has no barcode scanner, so lines go in by
// searching. One box, "Search products by name, code or category"; results as you type (debounced), each row the
// product's name, code and unit, the price THIS quote's customer pays and the stock at the quote's branch; the
// arrow keys and Enter, or a click, add the line with quantity 1 in the product's sell unit; the box stays focused
// and empty for the next item, so several lines go in without leaving it. A code typed exactly (or pasted: it works
// like a scan without promising one) adds at once. An empty answer says so in words.
//
// THE READS are the counter till's own (pos-kit's counterSearch and counterPrices, the reads
// pos-search-drop.ts makes): nothing is duplicated, and NOTHING IS PRICED HERE. Every figure on a row is the
// server's, formatted; a pricing outage leaves the row priced "on add" (the server prices every line when it is
// added), never a number this element made up.
//
// THE EVENT. `quote-line-add`, a bubbling composed CustomEvent on the host, detail
// { productId, code, unit, quantity: 1 }. The work area subscribes to it and posts the line (the one line swap in
// the workarea's mount is the split list coder's); the quantity is editable on the line as today.
//
// THE HANDS. Properties: `branch-id` (the quote's branch, whose stock the rows show) and `customer-id` (the
// quote's customer, whose price they show; empty is the walk-in case, priced at retail), handed over by the host's
// wiring pass; `quote-id` rides along as the quote this box belongs to (a quote switch clears the box). The
// binding discipline is the app's own (the position gate holds this app's templates to no property or event
// bindings): the host listens for input, keydown, click and mousedown, and the template carries data- hooks only.
import { css, html, nothing, type PropertyValues } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { counterPrices, counterSearch, money, type CounterHit } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";
import { DROP_ROWS, pricePicture, shouldQuery, type CounterQuote } from "@hardscapehouse/pos-kit/src/ported/_pos-searchdrop.js";

/** The event this element fires for every line the person adds. */
export const QUOTE_LINE_ADD = "quote-line-add";

export interface QuoteLineAddDetail {
  productId: string;
  code: string;
  unit: string;
  quantity: 1;
}

/** The pause after the last keystroke before the answer is asked for. */
const DEBOUNCE_MS = 160;
/** How many rows the list shows: a search and add list is a glance, and the person narrows by typing. */
const ROWS = DROP_ROWS + 3;

/** The unit a line is added in: the product's sell unit, else its base unit. */
export function sellUnit(hit: Pick<CounterHit, "sold_by" | "base_uom">): string {
  return hit.sold_by !== undefined && hit.sold_by !== "" ? hit.sold_by : hit.base_uom;
}

/** The detail of the add event for one hit. */
export function addDetail(hit: CounterHit): QuoteLineAddDetail {
  return { productId: hit.product_id, code: hit.sku, unit: sellUnit(hit), quantity: 1 };
}

/** The hit whose code is exactly the term (case blind), when there is exactly one: the pasted or typed code. */
export function exactCodeHit(hits: readonly CounterHit[], term: string): CounterHit | null {
  const code = term.trim().toLowerCase();
  if (code === "") return null;
  const found = hits.filter((hit) => hit.sku.trim().toLowerCase() === code);
  return found.length === 1 ? found[0] : null;
}

export class QuoteSearchAdd extends HHElement {
  static override properties = {
    ...HHElement.properties,
    branchId: { attribute: "branch-id" },
    customerId: { attribute: "customer-id" },
    quoteId: { attribute: "quote-id" },
    term: { state: true },
    hits: { state: true },
    quotes: { state: true },
    pricing: { state: true },
    searching: { state: true },
    failed: { state: true },
    active: { state: true },
    added: { state: true },
  };

  declare branchId: string;
  declare customerId: string;
  declare quoteId: string;
  declare term: string;
  declare hits: CounterHit[];
  declare quotes: Record<string, CounterQuote>;
  declare pricing: boolean;
  declare searching: boolean;
  declare failed: boolean;
  declare active: number;
  declare added: string;

  private timer = 0;
  private seq = 0;
  /** Enter was pressed while the current word's answer was still to come: add its first row when it lands. */
  private enterWaiting = false;

  constructor() {
    super();
    this.branchId = "";
    this.customerId = "";
    this.quoteId = "";
    this.term = "";
    this.hits = [];
    this.quotes = {};
    this.pricing = false;
    this.searching = false;
    this.failed = false;
    this.active = 0;
    this.added = "";
  }

  static override styles = [
    baseStyles,
    css`
      :host {
        display: block;
        color: var(--hh-text);
      }
      .box {
        position: relative;
      }
      .field {
        display: flex;
        align-items: center;
        gap: var(--hh-s-8);
      }
      input {
        flex: 1 1 auto;
        min-width: 0;
        min-height: var(--hh-h-input);
        padding: 0 var(--hh-s-16);
        font: inherit;
        font-size: var(--hh-s-16);
        color: var(--hh-text);
        background: var(--hh-card);
        border: 1px solid var(--hh-control-line);
        border-radius: var(--hh-radius-control);
      }
      input::placeholder {
        color: var(--hh-text-dim);
      }
      input:focus-visible {
        outline: 2px solid var(--hh-focus-ring);
        outline-offset: 2px;
      }
      .list {
        margin-top: var(--hh-s-4);
        background: var(--hh-card);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-card);
        box-shadow: var(--hh-elev-2);
        overflow: hidden;
      }
      .row {
        display: grid;
        grid-template-columns: minmax(0, 1fr) auto auto;
        align-items: center;
        gap: var(--hh-s-16);
        padding: var(--hh-s-8) var(--hh-s-16);
        border-top: 1px solid var(--hh-line);
        cursor: pointer;
      }
      .row:first-child {
        border-top: 0;
      }
      .row:hover,
      .row[aria-selected="true"] {
        background: var(--hh-row-alt);
      }
      .row[aria-selected="true"] {
        box-shadow: inset 3px 0 0 var(--hh-accent);
      }
      .name {
        font-weight: 600;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
      .meta,
      .stock,
      .per {
        font-size: 12px;
        color: var(--hh-text-dim);
      }
      .code {
        font-family: var(--hh-font-mono);
      }
      .price {
        text-align: right;
        min-width: 11ch;
        font-family: var(--hh-font-mono);
        white-space: nowrap;
      }
      .price .now {
        display: block;
        font-weight: 700;
      }
      .price .none {
        font-family: var(--hh-font-ui);
        font-size: 12px;
        color: var(--hh-text-dim);
      }
      .stock {
        min-width: 11ch;
        text-align: right;
        white-space: nowrap;
      }
      .stock.out {
        color: var(--hh-danger);
      }
      .empty,
      .busy {
        margin-top: var(--hh-s-4);
        padding: var(--hh-s-12) var(--hh-s-16);
        background: var(--hh-card);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-card);
      }
      .empty b {
        display: block;
      }
      .empty span,
      .busy {
        color: var(--hh-text-dim);
        font-size: 13px;
      }
      .added {
        margin-top: var(--hh-s-4);
        font-size: 13px;
        line-height: 1.4;
        /* The line keeps its height when it is empty, so the rows under the box never move when it fills or clears
           (a second press of a button under the pointer must land on the same button). */
        min-height: 1.4em;
        color: var(--hh-success);
      }
    `,
  ];

  override connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("input", this.onInput);
    this.addEventListener("keydown", this.onKeydown);
    this.addEventListener("mousedown", this.onMousedown);
    this.addEventListener("click", this.onClick);
  }

  override disconnectedCallback(): void {
    this.removeEventListener("input", this.onInput);
    this.removeEventListener("keydown", this.onKeydown);
    this.removeEventListener("mousedown", this.onMousedown);
    this.removeEventListener("click", this.onClick);
    if (this.timer !== 0) clearTimeout(this.timer);
    this.timer = 0;
    this.seq += 1;
    super.disconnectedCallback();
  }

  /** Put the caret in the box (the workarea hands focus here when the Products step opens). */
  focusInput(): void {
    this.input()?.focus();
  }

  private input(): HTMLInputElement | null {
    return this.renderRoot.querySelector<HTMLInputElement>('[data-testid="search-add-input"]');
  }

  private rowAt(event: Event): number {
    for (const node of event.composedPath()) {
      if (node instanceof HTMLElement && node.dataset.index !== undefined) return Number(node.dataset.index);
    }
    return -1;
  }

  // ── the delegated hands ────────────────────────────────────────────────

  private readonly onInput = (event: Event): void => {
    const field = event.composedPath()[0];
    if (!(field instanceof HTMLInputElement)) return;
    this.term = field.value;
    this.added = "";
    this.schedule();
  };

  private readonly onKeydown = (event: KeyboardEvent): void => {
    const shown = this.shown();
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      if (shown.length === 0) return;
      event.preventDefault();
      const step = event.key === "ArrowDown" ? 1 : -1;
      this.active = Math.min(shown.length - 1, Math.max(0, this.active + step));
    } else if (event.key === "Enter") {
      event.preventDefault();
      if (this.timer !== 0 || this.searching) {
        // The answer for this word is still to come, and the rows on screen are an older word's: never add one of
        // them. Ask now (when the pause is still running) and add this word's first row once its answer lands.
        this.enterWaiting = true;
        if (this.timer !== 0) void this.run(this.term, false);
        return;
      }
      const hit = shown[this.active];
      if (hit !== undefined) this.add(hit);
    } else if (event.key === "Escape") {
      this.reset();
    }
  };

  // The box never loses the caret to a row: pressing on a row must not blur the input.
  private readonly onMousedown = (event: MouseEvent): void => {
    if (this.rowAt(event) >= 0) event.preventDefault();
  };

  private readonly onClick = (event: MouseEvent): void => {
    const index = this.rowAt(event);
    const hit = index >= 0 ? this.shown()[index] : undefined;
    if (hit !== undefined) this.add(hit);
  };

  // ── the reads ──────────────────────────────────────────────────────────

  private shown(): CounterHit[] {
    return this.hits.slice(0, ROWS);
  }

  private schedule(): void {
    if (this.timer !== 0) clearTimeout(this.timer);
    this.timer = 0;
    this.seq += 1; // every keystroke makes any answer in flight stale, so a code that is only the start of this one never adds
    this.enterWaiting = false;
    const term = this.term;
    if (!shouldQuery(term)) {
      this.hits = [];
      this.quotes = {};
      this.pricing = false;
      this.searching = false;
      this.failed = false;
      return;
    }
    this.searching = true;
    this.timer = window.setTimeout(() => void this.run(term, false), DEBOUNCE_MS);
  }

  /** `refresh` re-reads the rows already shown (a branch change): same word, nothing added, the highlight kept. */
  private async run(term: string, refresh: boolean): Promise<void> {
    if (this.timer !== 0) clearTimeout(this.timer);
    this.timer = 0;
    if (!shouldQuery(term)) return;
    const seq = ++this.seq;
    let hits: CounterHit[];
    try {
      hits = await counterSearch(term, this.branchId, ROWS * 2);
    } catch {
      if (seq !== this.seq) return;
      if (refresh) return; // the rows on screen stay as they were when only their stock could not be read again
      this.hits = [];
      this.quotes = {};
      this.failed = true;
      this.searching = false;
      return;
    }
    if (seq !== this.seq) return; // a newer keystroke owns the box
    this.hits = hits;
    this.failed = false;
    this.searching = false;
    this.active = refresh ? Math.min(this.active, Math.max(0, hits.length - 1)) : 0;
    const enter = this.enterWaiting;
    this.enterWaiting = false;
    if (!refresh) {
      // A code typed exactly adds at once, the way a scan does (this answer is for the word as it stands: every
      // keystroke since would have dropped it above).
      const exact = exactCodeHit(hits, term);
      if (exact !== null) {
        this.add(exact);
        return;
      }
    }
    void this.loadPrices(hits);
    if (enter && hits.length > 0) this.add(hits[0]);
  }

  /** The customer's price picture for the rows on screen: the server's figures, best effort. */
  private async loadPrices(hits: CounterHit[]): Promise<void> {
    const rows = hits.slice(0, ROWS);
    if (rows.length === 0) {
      this.quotes = {};
      return;
    }
    // The answer belongs to the customer and the rows it was asked for; either moving on makes it stale.
    const customer = this.customerId;
    const asked = (): boolean => customer === this.customerId && hits === this.hits;
    this.pricing = true;
    try {
      const { prices } = await counterPrices(
        this.branchId,
        this.customerId,
        rows.map((hit) => ({ product_id: hit.product_id, qty_entered: 1, uom_entered: sellUnit(hit) })),
      );
      if (customer !== this.customerId) return;
      if (hits !== this.hits) return;
      const map: Record<string, CounterQuote> = {};
      for (const quote of prices) if (quote && typeof quote.product_id === "string") map[quote.product_id] = quote;
      this.quotes = map;
    } catch {
      if (asked()) this.quotes = {};
    } finally {
      if (asked()) this.pricing = false;
    }
  }

  // ── the add ────────────────────────────────────────────────────────────

  private add(hit: CounterHit): void {
    this.dispatchEvent(new CustomEvent<QuoteLineAddDetail>(QUOTE_LINE_ADD, { detail: addDetail(hit), bubbles: true, composed: true }));
    this.added = `Added ${hit.name}`;
    this.clear();
  }

  /** The box is ready for the next item: emptied, no list, and the caret stays in it. */
  private clear(): void {
    if (this.timer !== 0) clearTimeout(this.timer);
    this.timer = 0;
    this.seq += 1;
    this.term = "";
    this.enterWaiting = false;
    this.hits = [];
    this.quotes = {};
    this.pricing = false;
    this.searching = false;
    this.failed = false;
    this.active = 0;
    const field = this.input();
    if (field !== null) {
      field.value = "";
      field.focus();
    }
  }

  private reset(): void {
    this.added = "";
    this.clear();
  }

  protected override willUpdate(changed: PropertyValues): void {
    // A quote switch leaves nothing of the old quote's search behind.
    if (changed.has("quoteId") && changed.get("quoteId") !== undefined) this.reset();
  }

  protected override updated(changed: PropertyValues): void {
    // A customer arriving (or leaving) re-prices what is on screen.
    if (changed.has("customerId") && changed.get("customerId") !== undefined && this.hits.length > 0) void this.loadPrices(this.hits);
    // A branch change reads the rows again: their stock is the new branch's.
    if (changed.has("branchId") && changed.get("branchId") !== undefined && this.hits.length > 0 && shouldQuery(this.term)) void this.run(this.term, true);
    // The highlighted row is the input's active descendant (ids are set here: the template binds none).
    const field = this.input();
    const rows = this.renderRoot.querySelectorAll<HTMLElement>('[data-testid="search-add-row"]');
    rows.forEach((row, index) => {
      row.id = `search-add-row-${index}`;
    });
    if (field !== null) {
      if (rows.length > 0 && rows[this.active] !== undefined) field.setAttribute("aria-activedescendant", `search-add-row-${this.active}`);
      else field.removeAttribute("aria-activedescendant");
    }
    rows[this.active]?.scrollIntoView({ block: "nearest" });
  }

  // ── the render ─────────────────────────────────────────────────────────

  protected override render() {
    const shown = this.shown();
    const open = shown.length > 0;
    return html`<div class="box">
      <div class="field">
        <!-- future: <quote-search-add-mic data-testid="search-mic"></quote-search-add-mic> (HH-221, HH-222: the microphone button sits here, after the input) -->
        <input
          type="text"
          role="combobox"
          autocomplete="off"
          spellcheck="false"
          data-testid="search-add-input"
          placeholder="Search products by name, code or category"
          aria-label="Search products by name, code or category"
          aria-autocomplete="list"
          aria-controls="search-add-list"
          aria-expanded=${open ? "true" : "false"}
        />
      </div>
      ${open
        ? html`<div class="list" id="search-add-list" role="listbox" aria-label="Matching products" data-testid="search-add-list">
            ${shown.map((hit, index) => this.renderRow(hit, index))}
          </div>`
        : nothing}
      ${this.renderEmpty(open)}
      <div class="added" role="status" data-testid="search-add-status">${this.added}</div>
    </div>`;
  }

  private renderEmpty(open: boolean) {
    if (open || !shouldQuery(this.term)) return nothing;
    if (this.searching) return html`<div class="busy" data-testid="search-add-busy">Searching…</div>`;
    return html`<div class="empty" role="status" data-testid="search-add-empty">
      <b>${this.failed ? "The search is not answering right now" : `No products match "${this.term.trim()}"`}</b>
      <span
        >${this.failed
          ? "Try again in a moment."
          : "Try fewer letters, another word, or the product code."}</span
      >
    </div>`;
  }

  private renderRow(hit: CounterHit, index: number) {
    const unit = sellUnit(hit);
    const onHand = hit.on_hand_branch;
    return html`<div
      class="row"
      role="option"
      data-index=${String(index)}
      data-code=${hit.sku}
      data-testid="search-add-row"
      aria-selected=${index === this.active ? "true" : "false"}
    >
      <span class="id">
        <span class="name">${hit.name}</span>
        <span class="meta"><span class="code">${hit.sku}</span> · ${unit}</span>
      </span>
      ${this.renderPrice(hit, unit)}
      <span class="${onHand !== undefined && onHand <= 0 ? "stock out" : "stock"}" data-testid="search-add-stock"
        >${onHand === undefined ? "Stock not shown" : onHand <= 0 ? "Out of stock here" : `${onHand} on hand`}</span
      >
    </div>`;
  }

  /** What this customer pays, as the server decided it (formatted here, never derived). */
  private renderPrice(hit: CounterHit, unit: string) {
    const quote = this.quotes[hit.product_id];
    if (quote === undefined && this.pricing) return html`<span class="price"><span class="none">Pricing…</span></span>`;
    const picture = pricePicture(quote);
    if (picture.unpriceable) return html`<span class="price"><span class="none">Priced on add</span></span>`;
    return html`<span class="price"
      ><span class="now" data-testid="search-add-price">${money(picture.customerCents)}</span
      ><span class="per">per ${quote?.price_uom !== undefined && quote.price_uom !== "" ? quote.price_uom : unit}</span></span
    >`;
  }
}

defineElement("quote-search-add", QuoteSearchAdd);

declare global {
  interface HTMLElementTagNameMap {
    "quote-search-add": QuoteSearchAdd;
  }
}
