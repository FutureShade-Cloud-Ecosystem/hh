// The role wall's own surface (decision log row 957): a route the role cannot see lands here, a clean page that
// names the wall and offers the way back to Quotes, never an empty shell and never an uncaught error. The surface
// is chosen over a 404 because the route exists; the person's role is what keeps them out.
import { LitElement, css, html } from "lit";
import { customElement, property } from "lit/decorators.js";

@customElement("qd-no-access")
export class QdNoAccess extends LitElement {
  @property() surface = "This page";

  static styles = css`
    :host {
      display: block;
      font-family: var(--hh-font-ui);
      color: var(--hh-text);
    }
    .card {
      max-width: 36rem;
      margin: var(--hh-s-24, 24px) auto;
      padding: var(--hh-s-16, 16px) var(--hh-s-20, 20px);
      border: 1px solid var(--hh-line);
      border-radius: var(--hh-radius-card, 10px);
      background: var(--hh-card);
    }
    h2 {
      margin: 0 0 var(--hh-s-8, 8px);
      font-size: 1.1rem;
    }
    p {
      margin: 0 0 var(--hh-s-12, 12px);
      color: var(--hh-text-dim);
    }
    a {
      color: var(--hh-accent-text);
    }
  `;

  override render() {
    return html`<div class="card" role="alert" data-testid="no-access">
      <h2>No access</h2>
      <p>${this.surface} is not available to your role.</p>
      <a href="#/quotes" data-testid="no-access-back">Back to Quotes</a>
    </div>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-no-access": QdNoAccess;
  }
}
