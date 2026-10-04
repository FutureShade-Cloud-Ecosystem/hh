// The panel seams: the names the builder hosts its panels under, and the one
// slot still open. Every seam below is filled by its own panel (the edit panel,
// the change history and the settings; the assist, follow-ups, hold expiry,
// availability and approvals panels; the floor refusal dialog), mounted by the
// builder in place of the seam element and given the open quote's id and
// branch id as the other panels are. Their names stay here so the seam
// contract is one list. The only seam still empty is the QD-5 payment slot
// below, which is the one custom element in this file: it renders nothing and
// carries the quote id, the branch id and, once the quote is converted, the
// order id.
export const SEAM_EDIT_PANEL = "edit-panel";
export const SEAM_HISTORY = "history";
export const SEAM_SETTINGS = "settings";
export const SEAM_ASSIST = "assist";
export const SEAM_FOLLOWUPS = "followups";
export const SEAM_HOLD_EXPIRY = "hold-expiry";
export const SEAM_AVAILABILITY = "availability";
export const SEAM_APPROVALS = "approvals";
// The QD-5 seam: the quote's related documents and the converted order's
// payment. The edit panel hosts it under the convert control; QD-5 replaces
// the body of this slot. It renders nothing today; it is handed the quote id,
// the branch id and the converted order's id (empty until the conversion).
export const SEAM_PAYMENT = "payment";

/**
 * The window event the work area's Screen settings button fires. The builder
 * answers it by opening the detail disclosure and the settings region: the
 * work area owns the button, the builder owns the region, and neither imports
 * the other's panels.
 */
export const OPEN_SETTINGS_EVENT = "qd-open-settings";

import { LitElement, html } from "lit";
import { customElement } from "lit/decorators.js";

/**
 * One seam: renders nothing. The quote id and branch id ride as attributes
 * so the panel that replaces it reads them the way every host passes them,
 * and reload() answers so the builder's fan-out reaches whatever lands here
 * unchanged. The reload the seam itself answers is deliberately empty: there
 * is nothing behind the seam yet, and an honest empty seam never fakes a
 * panel.
 */
@customElement("qd-panel-seam")
export class QdPanelSeam extends LitElement {
  quoteId = "";
  branchId = "";
  /** The converted order's id, "" until the quote has been converted. */
  orderId = "";

  static properties = {
    quoteId: { attribute: "quote-id" },
    branchId: { attribute: "branch-id" },
    orderId: { attribute: "order-id" },
  };

  /** The fan-out's hook: a real panel re-reads its quote here. */
  reload(): void {
    /* empty by design: the seam holds no panel yet */
  }

  override render() {
    return html``;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-panel-seam": QdPanelSeam;
  }
}
