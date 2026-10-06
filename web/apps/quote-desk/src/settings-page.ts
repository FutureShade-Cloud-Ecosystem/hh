// The Settings surface (decision log rows 955 and 957): its own route, #/settings, manager only. The three
// settings screens that were disclosures under the quote's detail region (screen layout, acceptance settings, scope
// suggestion rules) are its sections, open by default, on one page. The sections are the settings panel's own
// (the server says who may write); this page only gives them a home of their own.
import { LitElement, css, html } from "lit";
import { customElement, property } from "lit/decorators.js";
import "./settings-panel.js";
import type { QdClient } from "./qd-client.js";

@customElement("qd-settings-page")
export class QdSettingsPage extends LitElement {
  @property({ attribute: false }) client: QdClient | null = null;
  @property({ attribute: "branch-id" }) branchId = "";

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
  `;

  protected override updated(): void {
    const panel = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("qd-quote-settings");
    if (panel === null) return;
    panel.client = this.client;
    panel.branchId = this.branchId;
  }

  reload(): void {
    this.renderRoot.querySelector<HTMLElement & { reload?: () => void }>("qd-quote-settings")?.reload?.();
  }

  override render() {
    return html`<h1 data-testid="st-title">Settings</h1><qd-quote-settings open-sections></qd-quote-settings>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-settings-page": QdSettingsPage;
  }
}
