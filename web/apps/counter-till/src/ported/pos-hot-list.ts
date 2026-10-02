// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-hot-list.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { focusRing } from './styles/focus-ring.js';
import './product-thumb.js';
import { quickPicks, money, type HotPick } from './pos-api.js';
import { pricePicture } from './_pos-searchdrop.js';
import { AddOnce } from './_pos-add-dedupe.js';
import {
  hotTiles,
  drillInto,
  drillUp,
  crumbs,
  pathExists,
  HOT_ROOT,
  HOT_LEVEL_MAX,
  type HotPath,
  type HotTile,
} from './_pos-hotlist.js';

/**
 * hos-pos-hot-list: the counter's high-frequency picking surface: photo tiles, drilled by
 * category → subcategory → item.
 *
 * WHY THIS REPLACED A FLAT GRID. The flat quick-pick grid could hold about a dozen tiles before it
 * stopped being faster than typing, which meant it could only ever cover the top of the top. This
 * yard's volume is bulk material, confirmed on the call to dwarf contracts and projects: and
 * bulk is exactly the shape a drill fits: a handful of categories, a handful of materials in each,
 * a handful of colours in that. Three taps with a picture at every step, and no reading.
 *
 * THE SET IS STILL DEALER CURATION. Same settings-kit surface, same user-over-dealer whole-or-
 * absent override, same derived-from-what-you-sell fallback. Only the arrangement changed, and it
 * changed in the frontend, which is why it needed no migration.
 *
 * NO PHOTO IS A NORMAL STATE. Most of the catalogue has no image yet. hos-product-thumb draws a
 * real "no photo" mark on the panel colour rather than an empty square (indistinguishable from
 * still-loading) or the browser's torn-page glyph (reads as "this app is broken"). A group tile
 * borrows a photo from a member that has one, so a whole category is not blank because its first
 * product is.
 *
 * ONE TAP BACK. Every level carries a breadcrumb whose entries are all tappable and a Back tile at
 * the head of the grid: the thumb is already in the grid, so the way back should be too, not
 * only in a trail at the top.
 */
@customElement('hos-pos-hot-list')
export class HosPosHotList extends LitElement {
  @property({ attribute: 'branch-id' }) branchId = '';
 /** The account on the sale: the tiles wear THEIR price. '' is the walk-in case. */
  @property({ attribute: 'customer-id' }) customerId = '';

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


  @state() private items: HotPick[] = [];
  @state() private path: HotPath = HOT_ROOT;
  @state() private loading = true;
  @state() private failed = false;
  @state() private roveIdx = 0;

  private seq = 0;

  static styles = [
    focusRing,
    css`
      :host {
        display: block;
        font-family: var(--hos-font-ui);
        color: var(--hos-text);
      }
      .crumbs {
        display: flex;
        align-items: center;
        flex-wrap: wrap;
        gap: 4px;
        margin-bottom: 8px;
        font-size: 12px;
        color: var(--hos-text-dim);
      }
      .crumbs button {
        background: transparent;
        border: 0;
        padding: 2px 4px;
        color: var(--hos-text-dim);
        font-family: inherit;
        font-size: 12px;
        cursor: pointer;
      }
      .crumbs button:hover {
        color: var(--hos-text);
      }
      .crumbs [aria-current='true'] {
        color: var(--hos-text);
        font-weight: 600;
      }
      .crumbs .sep {
        opacity: 0.6;
      }
      /* Big targets. A tile is meant for a gloved thumb on a shop-floor panel, so the floor is
         generous and the grid reflows rather than shrinking the tiles. */
      .grid {
        display: grid;
        grid-template-columns: repeat(auto-fill, minmax(132px, 1fr));
        gap: 8px;
      }
      .tile {
        display: flex;
        flex-direction: column;
        align-items: flex-start;
        gap: 4px;
        min-height: 116px;
        padding: 8px;
        background: var(--hos-slate-warm);
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-card);
        color: var(--hos-text);
        font-family: inherit;
        font-size: 13px;
        text-align: left;
        cursor: pointer;
      }
      .tile:hover {
        border-color: var(--hos-stone-amber);
      }
      .tile .name {
        font-weight: 600;
        line-height: 1.25;
      }
      .tile .sub {
        font-size: 11px;
        color: var(--hos-text-dim);
      }
      .tile .now {
        margin-top: auto;
        font-family: var(--hos-font-mono);
        font-weight: 700;
      }
      .tile .was {
        font-family: var(--hos-font-mono);
        font-size: 11px;
        color: var(--hos-text-dim);
        text-decoration: line-through;
      }
      .tile .save {
        padding: 1px 6px;
        border-radius: var(--hos-radius-pill);
        border: 1px solid var(--hos-success);
        color: var(--hos-success);
        font-size: 10px;
        font-weight: 600;
      }
      /* The Back tile is a way out, not a product: quiet clothes, and it never wears a photo. */
      .tile.back {
        justify-content: center;
        align-items: center;
        background: transparent;
        color: var(--hos-text-dim);
      }
      .empty {
        padding: 10px 0;
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
      @media (pointer: coarse) {
        .grid {
          grid-template-columns: repeat(auto-fill, minmax(152px, 1fr));
        }
        .tile {
          min-height: 140px;
        }
        .crumbs button {
          min-height: 44px;
          padding: 0 10px;
        }
      }
    `,
  ];

  connectedCallback(): void {
    super.connectedCallback();
    void this.load();
  }

  protected updated(changed: Map<string, unknown>): void {
 // A customer arriving re-prices the tiles: showing an account's price is only worth doing if
    // it follows the account.
    if (changed.has('customerId') || changed.has('branchId')) void this.load();
  }

  private async load(): Promise<void> {
    const seq = ++this.seq;
    this.loading = true;
    try {
      const { items } = await quickPicks(this.branchId, this.customerId);
      if (seq !== this.seq) return;
      this.items = items;
      this.failed = false;
      // A curated set that changed under a drilled-in operator must not strand them on a level
      // that no longer has anything in it.
      if (!pathExists(items, this.path)) this.path = HOT_ROOT;
    } catch {
      if (seq !== this.seq) return;
      this.items = [];
      this.failed = true;
    } finally {
      if (seq === this.seq) this.loading = false;
    }
  }

  /**
   * ONE TAB STOP for the whole grid (the house roving rule for grids and rails). Arrows move
   * within it; Tab passes over it to the ticket. A grid of thirteen tap targets that each took a
   * Tab stop would put the ticket thirteen presses away from the search field, which is precisely
   * the "sixty Tab stops to the Cash tile" defect this codebase already fixed once.
   *
   * The tiles reflow with the viewport, so the grid is walked as a RAIL rather than by row and
   * column: forward is Right or Down, back is Left or Up. Guessing a column count from CSS would
   * be a lie at some width, and a keyboard user who cannot predict where Down goes has no
   * navigation at all.
   */
  private onGridKey(e: KeyboardEvent): void {
    const tiles = this.renderRoot.querySelectorAll<HTMLElement>('.tile');
    if (tiles.length === 0) return;
    let next: number | null = null;
    switch (e.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        next = Math.min(tiles.length - 1, this.roveIdx + 1);
        break;
      case 'ArrowLeft':
      case 'ArrowUp':
        next = Math.max(0, this.roveIdx - 1);
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = tiles.length - 1;
        break;
      default:
        return;
    }
    e.preventDefault();
    this.roveIdx = next;
    tiles[next]?.focus({ preventScroll: true });
  }

  /** A mouse click or a Tab into the grid moves the roving stop to whatever was reached. */
  private onGridFocus(e: FocusEvent): void {
    const tiles = [...this.renderRoot.querySelectorAll<HTMLElement>('.tile')];
    const i = tiles.indexOf(e.target as HTMLElement);
    if (i >= 0) this.roveIdx = i;
  }

  private go(path: HotPath): void {
    this.path = path;
    this.roveIdx = 0;
    void this.updateComplete.then(() => this.renderRoot.querySelector<HTMLElement>('.tile')?.focus({ preventScroll: true }));
  }

  /**
   * DIB-553: the second half of a double-click is not a second sale. The rule, the state, the
   * clock and the dispatch all live in _pos-add-dedupe.ts, so this component has no decision of
 * its own to get wrong: and no expression of its own for a `false &&` to switch off with the
   * source text left intact, which is how the first cut of this guard survived being disabled.
   */
  private readonly adder = new AddOnce();

 // DIB-553 (bounce 2) / D6 (round 3): unconditional by design: AddOnce.ticketAt owns the
  // "has the ticket moved?" comparison, so there is no expression here to short-circuit.
  protected willUpdate(): void {
    this.adder.ticketAt(this.ticketEpoch);
  }

  private add(p: HotPick): void {
    this.adder.add(this, p.product_id, {
      id: p.product_id,
      sku: p.sku,
      name: p.name,
      color_family: '',
      application: p.category ?? '',
      sold_by: p.uom,
      base_uom: p.uom,
      is_active: true,
    });
  }

  render() {
    if (this.loading && this.items.length === 0) return nothing;
    const tiles = hotTiles(this.items, this.path, HOT_LEVEL_MAX);
    if (tiles.length === 0) {
 // Empty is never an error: and a yard that has not curated and has not rung anything yet is
      // an ordinary first day, not a fault. role="status", one calm line, and the way forward.
      return html`<div class="empty" role="status" data-testid="hot-empty">
        <b>${this.failed ? 'Hot list paused' : 'No hot list yet'}</b>
        <span
          >${this.failed
            ? 'The hot list is not answering right now: search or scan above to add items.'
            : 'Nothing is pinned here yet: search or scan above, and your busiest lines appear here.'}</span
        >
      </div>`;
    }
    const trail = crumbs(this.path);
    return html`<div data-testid="pos-hot-list">
      ${this.path.category
        ? html`<nav class="crumbs" aria-label="Hot list position" data-testid="hot-crumbs">
            ${trail.map(
              (c, i) => html`${i > 0 ? html`<span class="sep" aria-hidden="true">›</span>` : nothing}
                <button
                  aria-current=${c.current ? 'true' : 'false'}
                  @click=${() => this.go(c.path)}
                >
                  ${c.label}
                </button>`,
            )}
          </nav>`
        : nothing}
      <div
        class="grid"
        role="group"
        aria-label="Hot list"
        @keydown=${(e: KeyboardEvent) => this.onGridKey(e)}
        @focusin=${(e: FocusEvent) => this.onGridFocus(e)}
      >
        ${this.path.category
          ? html`<button
              class="tile back"
              data-testid="hot-back"
              tabindex=${this.roveIdx === 0 ? '0' : '-1'}
              @click=${() => this.go(drillUp(this.path))}
            >
              <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M15 18l-6-6 6-6"></path>
              </svg>
              Back
            </button>`
          : nothing}
        ${tiles.map((t, i) => this.renderTile(t, i + (this.path.category ? 1 : 0)))}
      </div>
    </div>`;
  }

  private renderTile(t: HotTile, i: number) {
    if (t.kind === 'group') {
      const thumb = t.thumbFor;
      return html`<button
        class="tile"
        data-testid="hot-group"
        title=${t.label}
        tabindex=${this.roveIdx === i ? '0' : '-1'}
        @click=${() => this.go(drillInto(this.path, t.label))}
      >
        <hos-product-thumb
          .productId=${thumb?.product_id ?? ''}
          .hasImage=${thumb?.has_image ?? false}
          alt=""
          .size=${52}
        ></hos-product-thumb>
        <span class="name">${t.label}</span>
        <span class="sub">${t.count} ${t.count === 1 ? 'item' : 'items'}</span>
      </button>`;
    }
    const p = t.pick as HotPick;
    const pic = pricePicture(p.price);
    return html`<button
      class="tile"
      data-testid="hot-item"
      title=${p.sku}
      tabindex=${this.roveIdx === i ? '0' : '-1'}
      @click=${() => this.add(p)}
    >
      <hos-product-thumb .productId=${p.product_id} .hasImage=${p.has_image} alt="" .size=${52}></hos-product-thumb>
      <span class="name">${p.name}</span>
      ${p.size_line ? html`<span class="sub">${p.size_line}</span>` : nothing}
      ${pic.unpriceable
        ? nothing
        : html`<span class="now" data-testid="hot-price">${money(pic.customerCents)}</span>
            ${pic.showRetail ? html`<span class="was">${money(pic.retailCents)}</span>` : nothing}
            ${pic.showSaving
              ? html`<span class="save" data-testid="hot-saving">Save ${money(pic.savingCents)}</span>`
              : nothing}`}
    </button>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-hot-list': HosPosHotList;
  }
}
