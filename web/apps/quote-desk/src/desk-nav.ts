// The desk's navigation bar (decision log rows 955 and 957): the four desk surfaces as tabs under the shared
// header, each its own route. The role wall is the surface table's: a tab the role cannot see is not rendered at
// all (not hidden by style), so Sales never has the Approvals, Pricing or Settings tabs in the page. The host feeds
// the role, the active tab and the pending count through its wiring pass; the tabs are plain links, so the hash
// carries the walk and the browser's history carries back and forward.
import { LitElement, html, nothing } from "lit";
import { customElement, property } from "lit/decorators.js";
import { DESK_TAB_LABEL, deskTabHash, deskTabsFor, type DeskRole, type DeskTab } from "./surface-route.js";
import { tabStyles } from "./_tab-styles.js";

@customElement("qd-desk-nav")
export class QdDeskNav extends LitElement {
  @property({ attribute: "desk-role" }) deskRole: DeskRole = "sales";
  @property() active: DeskTab = "quotes";
  /** Pending margin requests; shown on the Approvals tab only while above zero. */
  @property({ type: Number }) pending = 0;

  static styles = [tabStyles];

  override render() {
    return html`<nav class="tabs" data-testid="desk-nav" aria-label="Desk surfaces">
      ${deskTabsFor(this.deskRole).map(
        (tab) => html`<a
          class="tab"
          href=${deskTabHash(tab)}
          data-testid=${`desk-tab-${tab}`}
          aria-current=${tab === this.active ? "page" : nothing}
          >${DESK_TAB_LABEL[tab]}${tab === "approvals" && this.pending > 0
            ? html` <span class="count" data-testid="desk-pending">${this.pending}</span>`
            : nothing}</a
        >`,
      )}
    </nav>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-desk-nav": QdDeskNav;
  }
}
