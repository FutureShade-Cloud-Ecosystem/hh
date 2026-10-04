// The panel seams: the named, empty slots the builder hosts for the second
// and third coders of this app (QD-3's split). Each seam is a custom element
// that renders nothing today, carries the quote id and the branch id as
// plain attributes a panel reads, and answers reload() like every mounted
// child the quote-changed fan-out reaches. The second coder fills the edit
// panel, the change history and the settings seams (a port of quote-edit.ts,
// quote-edits.ts, quote-surface-settings.ts and the two admin disclosures);
// the third coder fills the assist, follow-ups, hold expiry, availability
// and approvals seams and replaces the floor refusal dialog seam in
// floor-refusal.ts. A seam is replaced by mounting the real panel in its
// place; the names below are the contract.
export const SEAM_EDIT_PANEL = "edit-panel";
export const SEAM_HISTORY = "history";
export const SEAM_SETTINGS = "settings";
export const SEAM_ASSIST = "assist";
export const SEAM_FOLLOWUPS = "followups";
export const SEAM_HOLD_EXPIRY = "hold-expiry";
export const SEAM_AVAILABILITY = "availability";
export const SEAM_APPROVALS = "approvals";

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

  static properties = { quoteId: { attribute: "quote-id" }, branchId: { attribute: "branch-id" } };

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
