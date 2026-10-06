// The settings panel: the three settings screens as the sections of the Settings
// surface (#/settings). The screen settings (the typed quote policy and
// the per role curation) open first; the acceptance switch and the scope
// rules sit behind their own disclosures, as the reference has them. The work
// area's Screen settings button asks the builder to open this region, which
// calls openScreenSettings(). Each screen reads what the server offers and
// shows the server's refusal; none holds a role rule.
import { css, html } from "lit";
import { customElement, property } from "lit/decorators.js";
import "./settings-surface.js";
import "./settings-accept.js";
import "./settings-scope.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";

@customElement("qd-quote-settings")
export class QdQuoteSettings extends PanelBase {
  /** The settings page shows every section open (the page's content, not a fold under another page). */
  @property({ type: Boolean, attribute: "open-sections" }) openSections = false;

  static styles = [
    panelStyles,
    css`
      details.box + details.box {
        margin-top: 10px;
      }
      summary {
        cursor: pointer;
        font-size: var(--qp-section);
        font-weight: 600;
      }
      details > :not(summary) {
        margin-top: 10px;
      }
      .inner {
        display: block;
      }
    `,
  ];

  /** The children receive the same facts the host was given. */
  protected override updated(): void {
    for (const child of this.renderRoot.querySelectorAll<HTMLElement & Record<string, unknown>>("qd-surface-settings, qd-accept-admin, qd-scope-rules")) {
      child.client = this.client;
      child.branchId = this.branchId;
    }
  }

  reload(): void {
    this.renderRoot
      .querySelectorAll<HTMLElement & { reload?: () => void }>("qd-surface-settings, qd-accept-admin, qd-scope-rules")
      .forEach((el) => el.reload?.());
  }

  /** Open the screen settings and bring them into view (the work area's button lands here). */
  openScreenSettings(): void {
    const screen = this.renderRoot.querySelector<HTMLDetailsElement>('[data-testid="qs-screen"]');
    if (screen === null) return;
    screen.open = true;
    screen.scrollIntoView({ block: "start" });
  }

  override render() {
    return html`
      <details class="box" data-testid="qs-screen" ?open=${this.openSections}>
        <summary>Screen settings</summary>
        <qd-surface-settings></qd-surface-settings>
      </details>
      <details class="box" data-testid="qa-details" ?open=${this.openSections}>
        <summary>Acceptance settings</summary>
        <qd-accept-admin></qd-accept-admin>
      </details>
      <details class="box" data-testid="qr-details" ?open=${this.openSections}>
        <summary>Scope suggestion rules</summary>
        <qd-scope-rules></qd-scope-rules>
      </details>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-settings": QdQuoteSettings;
  }
}
