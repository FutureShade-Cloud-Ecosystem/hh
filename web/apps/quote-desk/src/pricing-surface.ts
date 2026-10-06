// The Pricing surface (decision log rows 955 and 957): its own route, #/pricing, manager only. The pricing console
// is built on the pricing lane's branch (quote/qd-4) and is not on this tree; this element is the surface's
// home and its seam. The console mounts here when that branch merges: it keeps the quote-desk's console element
// under this tag's shadow root and is handed the client the way every surface is. Until then the page says so
// plainly and offers the way back; it never fakes the page.
import { LitElement, css, html } from "lit";
import { customElement, property } from "lit/decorators.js";
import type { QdClient } from "./qd-client.js";

@customElement("qd-pricing-surface")
export class QdPricingSurface extends LitElement {
  @property({ attribute: false }) client: QdClient | null = null;

  static styles = css`
    :host {
      display: block;
      padding: var(--hh-s-16, 16px);
      font-family: var(--hh-font-ui);
      color: var(--hh-text);
    }
    h1 {
      margin: 0 0 var(--hh-s-12, 12px);
      font-size: 1.3rem;
    }
    .card {
      border: 1px solid var(--hh-line);
      border-radius: var(--hh-radius-card, 10px);
      background: var(--hh-card);
      padding: var(--hh-s-12, 12px) var(--hh-s-16, 16px);
    }
    p {
      margin: 0;
      color: var(--hh-text-dim);
    }
  `;

  /** The fan-out's hook: nothing is mounted behind the seam yet. */
  reload(): void {
    /* the console lands behind this seam */
  }

  override render() {
    return html`<h1 data-testid="pr-title">Pricing</h1>
      <div class="card" data-testid="pr-seam"><p>The pricing console opens here.</p></div>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-pricing-surface": QdPricingSurface;
  }
}
