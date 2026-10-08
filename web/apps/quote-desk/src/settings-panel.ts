// The settings seam's panel: the three settings screens as one region under
// the quote detail disclosure. The screen settings (the typed quote policy and
// the per role curation) open first; the acceptance switch and the scope
// rules sit behind their own disclosures, as the reference has them. The work
// area's Screen settings button asks the builder to open this region, which
// calls openScreenSettings(). Each screen reads what the server offers and
// shows the server's refusal; none holds a role rule.
//
// GRANT GATING: the three disclosures are hidden when the settings grant
// answers false, so the panels never mount and never read. The grant is read
// once when the client arrives; the child panels still do their own reads and
// show the server's refusal (defence in depth).
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import type { SettingsGrant } from "./_panel-wire.js";
import "./settings-surface.js";
import "./settings-accept.js";
import "./settings-scope.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";

@customElement("qd-quote-settings")
export class QdQuoteSettings extends PanelBase {
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

  /** The settings grant: one question the Quote Desk asks before mounting its panels.
   *  Cached per session (one caller); null until the first client arrives. */
  @state() private grant: SettingsGrant | null = null;

  /** True from the first client arrival until the grant read answers or fails. */
  @state() private grantLoading = false;

  /** The children receive the same facts the host was given. */
  protected override updated(): void {
    if (this.client !== null && this.grant === null && !this.grantLoading) {
      void this.#loadGrant();
    }
    for (const child of this.renderRoot.querySelectorAll<HTMLElement & Record<string, unknown>>("qd-surface-settings, qd-accept-admin, qd-scope-rules")) {
      child.client = this.client;
      child.branchId = this.branchId;
    }
  }

  async #loadGrant(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    this.grantLoading = true;
    const result = await client.settingsGrant();
    this.grantLoading = false;
    if (result.ok) this.grant = result.value;
  }

  reload(): void {
    void this.#loadGrant();
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
    const g = this.grant;
    // Nothing is rendered when no grant has resolved yet; the region stays absent
    // from the DOM so no child ever mounts without a grant.
    if (g === null) return nothing;
    return html`
      ${g.canEditPolicy ? html`
      <details class="box" data-testid="qs-screen">
        <summary>Screen settings</summary>
        <qd-surface-settings></qd-surface-settings>
      </details>` : nothing}
      ${g.canReadAcceptance ? html`
      <details class="box" data-testid="qa-details">
        <summary>Acceptance settings</summary>
        <qd-accept-admin></qd-accept-admin>
      </details>` : nothing}
      ${g.canReadScopeRules ? html`
      <details class="box" data-testid="qr-details">
        <summary>Scope suggestion rules</summary>
        <qd-scope-rules></qd-scope-rules>
      </details>` : nothing}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-settings": QdQuoteSettings;
  }
}
