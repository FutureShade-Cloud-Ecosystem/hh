// The quote page's tab strip (decision log row 955): Build, Activity, Stock and Admin in the quote's title row,
// each its own route. Admin is the Sales manager's alone, so for Sales it is not rendered. The builder slots the
// strip into the work area's title row and feeds it through attributes; the tabs are plain links.
import { LitElement, html, nothing } from "lit";
import { hhAddress } from "@hardscapehouse/design-system/src/address.js";
import { customElement, property } from "lit/decorators.js";
import { QUOTE_TAB_LABEL, quoteTabHash, quoteTabsFor, type DeskRole, type QuoteTab } from "./surface-route.js";
import { tabStyles } from "./_tab-styles.js";

@customElement("qd-quote-tabs")
export class QdQuoteTabs extends LitElement {
  @property({ attribute: "quote-id" }) quoteId = "";
  @property({ attribute: "desk-role" }) deskRole: DeskRole = "sales";
  @property() active: QuoteTab = "build";

  static styles = [tabStyles];

  override render() {
    if (this.quoteId === "") return nothing;
    return html`<nav class="tabs" data-testid="qt-strip" aria-label="Quote surfaces">
      ${quoteTabsFor(this.deskRole).map((tab) => {
        const href = quoteTabHash(this.quoteId, tab);
        return html`<a
          class="tab"
          href="${hhAddress(href)}"
          data-testid=${`qt-tab-${tab}`}
          aria-current=${tab === this.active ? "page" : nothing}
          >${QUOTE_TAB_LABEL[tab]}</a
        >`;
      })}
    </nav>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-tabs": QdQuoteTabs;
  }
}
