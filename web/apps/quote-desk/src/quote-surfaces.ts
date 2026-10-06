// The quote's own surfaces below its title row (decision log row 955): Activity, Stock and Admin, each a section
// shown only on its tab. The "Quote detail & admin" disclosure these panels lived in is gone; each panel moved to
// the surface its job belongs to.
//
//   Activity   history, follow ups, assist         Sales and Sales manager
//   Stock      availability, hold expiry           Sales and Sales manager
//   Admin      per line admin, Check Quote         Sales manager only
//
// The Admin panels are mounted for the manager only: for Sales they are not in the page at all. The panels of the
// other tabs stay mounted while hidden, as the disclosure kept them: a panel taken off the page loses the late
// answers of writes still out for its quote, so a tab switch only changes what is shown.
import { LitElement, css, html, nothing } from "lit";
import { customElement, property } from "lit/decorators.js";
import "./edit-panel.js";
import "./history-panel.js";
import "./assist-panel.js";
import "./followups-panel.js";
import "./hold-expiry-panel.js";
import "./availability-panel.js";
import "./guard-panel.js";
import { SEAM_ASSIST, SEAM_AVAILABILITY, SEAM_EDIT_PANEL, SEAM_FOLLOWUPS, SEAM_HISTORY, SEAM_HOLD_EXPIRY } from "./panel-seams.js";
import type { QdClient } from "./qd-client.js";
import type { DeskRole, QuoteTab } from "./surface-route.js";

const PANELS = "qd-quote-edit, qd-quote-assist, qd-quote-history, qd-quote-followups, qd-quote-hold-expiry, qd-quote-availability, qd-quote-guard";

@customElement("qd-quote-surfaces")
export class QdQuoteSurfaces extends LitElement {
  @property({ attribute: false }) client: QdClient | null = null;
  @property({ attribute: "quote-id" }) quoteId = "";
  @property({ attribute: "branch-id" }) branchId = "";
  @property() tab: QuoteTab = "build";
  @property({ attribute: "desk-role" }) deskRole: DeskRole = "sales";

  static styles = css`
    :host {
      display: block;
    }
    :host([data-compose]) {
      display: none;
    }
    [hidden] {
      display: none;
    }
    section {
      display: flex;
      flex-direction: column;
      gap: var(--hh-s-12, 12px);
    }
  `;

  protected override updated(): void {
    for (const panel of this.renderRoot.querySelectorAll<HTMLElement & Record<string, unknown>>(PANELS)) {
      panel.quoteId = this.quoteId;
      panel.branchId = this.branchId;
      panel.client = this.client;
    }
  }

  /** The quote-changed fan-out reaches every mounted panel. */
  reload(): void {
    this.renderRoot.querySelectorAll<HTMLElement & { reload?: () => void }>(PANELS).forEach((el) => el.reload?.());
  }

  override render() {
    const admin = this.deskRole === "manager";
    return html`
      <section data-testid="qs-activity" ?hidden=${this.tab !== "activity"}>
        <qd-quote-history data-seam=${SEAM_HISTORY}></qd-quote-history>
        <qd-quote-followups data-seam=${SEAM_FOLLOWUPS}></qd-quote-followups>
        <qd-quote-assist data-seam=${SEAM_ASSIST}></qd-quote-assist>
      </section>
      <section data-testid="qs-stock" ?hidden=${this.tab !== "stock"}>
        <qd-quote-availability data-seam=${SEAM_AVAILABILITY}></qd-quote-availability>
        <qd-quote-hold-expiry data-seam=${SEAM_HOLD_EXPIRY}></qd-quote-hold-expiry>
      </section>
      ${admin
        ? html`<section data-testid="qs-admin" ?hidden=${this.tab !== "admin"}>
            <qd-quote-edit data-seam=${SEAM_EDIT_PANEL}></qd-quote-edit>
            <qd-quote-guard></qd-quote-guard>
          </section>`
        : nothing}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-surfaces": QdQuoteSurfaces;
  }
}
