// Ported from hardscapeos_dibbits master 87d7941b app/src/product-thumb.ts; adapted for HH.

import { LitElement, css, html } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { productImageUrl } from './pos-api.js';

/**
 * hos-product-thumb: the product-images-ui ticket. The one product thumbnail in the app.
 *
 * WHY A COMPONENT AND NOT AN <img> IN EACH LIST. Three surfaces want the same picture (POS search
 * results, order lines, inventory rows) and every one of them has the same two problems: most
 * products have no image yet, and an <img> whose src 404s renders the browser's broken-image glyph:
 * a torn page icon that reads as "this application is broken", not as "no photo on file". Putting
 * the fallback in one component means the broken state cannot be reintroduced by the next list that
 * needs a picture.
 *
 * THE FALLBACK IS A REAL GLYPH, NOT AN EMPTY BOX. An empty box of the right size is indistinguishable
 * from an image that has not loaded yet, so a slow connection looks like missing data. The placeholder
 * is a drawn stone-stack mark on the panel colour: unmistakably "no photo", never "loading".
 *
 * hasImage lets a caller that already knows (the counter search returns has_image per hit) skip the
 * request entirely. When it is undefined the component simply tries, and falls back on error: so a
 * caller with no such flag still gets the right behaviour with no extra plumbing.
 *
 * No interactive controls, so no focusRing import is required (DIB-414 applies to components with
 * focusable elements); the thumbnail is decorative-with-alt-text and is never a tab stop.
 */
@customElement('hos-product-thumb')
export class HosProductThumb extends LitElement {
  /** The product whose thumbnail to show. */
  @property({ attribute: 'product-id' }) productId = '';
 /** Alt text: the product name. Required for a meaningful image; '' marks it decorative. */
  @property() alt = '';
  /** Edge length in px. 40 suits a table row, 96 a search tile. */
  @property({ type: Number }) size = 40;
  /**
   * Known-ahead-of-time image presence. undefined = "try and see"; false skips the request; true is
 * a hint only: the error fallback still applies if the byte route disagrees.
   */
  @property({ type: Boolean, attribute: 'has-image' }) hasImage?: boolean;

  @state() private failed = false;

  static styles = css`
    :host {
      display: inline-block;
      line-height: 0;
    }
    .box {
      display: grid;
      place-items: center;
      overflow: hidden;
      border-radius: 8px;
      border: 1px solid var(--hos-line);
      background: var(--hos-deep-earth);
    }
    img {
      width: 100%;
      height: 100%;
      object-fit: cover;
      display: block;
    }
    /* The placeholder mark: a stacked-stone silhouette in the dim text colour. Deliberately low
       contrast: it is an absence, and should not compete with the product name beside it. */
    svg {
      width: 60%;
      height: 60%;
      color: var(--hos-text-dim);
      opacity: 0.65;
    }
  `;

  private onError(): void {
    // One-way latch: once the byte route has said no, stop asking on every re-render.
    this.failed = true;
  }

  protected willUpdate(changed: Map<string, unknown>): void {
 // A new product gets a fresh chance: otherwise a list that reuses this element for row 2 would
    // inherit row 1's failure and show a placeholder for a product that does have a picture.
    if (changed.has('productId')) this.failed = false;
  }

  render() {
    const px = `${this.size}px`;
    const show = this.productId !== '' && this.hasImage !== false && !this.failed;
    return html`<div class="box" style="width:${px};height:${px}">
      ${show
        ? html`<img
            src=${productImageUrl(this.productId)}
            alt=${this.alt}
            loading="lazy"
            decoding="async"
            @error=${this.onError}
          />`
        : this.placeholder()}
    </div>`;
  }

  private placeholder() {
    // aria-hidden + role=img on the wrapper would double-announce; the box carries the product name
    // in the row beside it, so the absence itself needs no announcement.
    return html`<svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <rect x="2" y="14" width="20" height="5" rx="1" stroke="currentColor" stroke-width="1.5" />
      <rect x="4" y="9" width="16" height="5" rx="1" stroke="currentColor" stroke-width="1.5" />
      <rect x="7" y="4" width="10" height="5" rx="1" stroke="currentColor" stroke-width="1.5" />
    </svg>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-product-thumb': HosProductThumb;
  }
}
