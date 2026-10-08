// The settings seam's panel: the three settings screens as one region under
// the quote detail disclosure. The screen settings (the typed quote policy and
// the per role curation) open first; the acceptance switch and the scope
// rules sit behind their own disclosures, as the reference has them. The work
// area's Screen settings button asks the builder to open this region, which
// calls openScreenSettings(). Each screen reads what the server offers and
// shows the server's refusal; none holds a role rule.
//
// GRANT GATING: the acceptance and scope rule disclosures mount only when the
// settings grant says the signed-in role may read them, so their reads never
// fire for a role the server refuses. The grant is asked once per person (the
// client remembers it); the screen settings stay for every role. The child
// panels still show the server's refusal if one comes (defence in depth).
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

  /** The settings grant of the signed-in role; null while it is unknown, and when its read failed, so the
   *  two gated sections stay absent. The panel is built per sign-in, and the client remembers the grant
   *  per person, so nothing here outlives the person it was read for. */
  @state() private grant: SettingsGrant | null = null;

  /** Whether the grant was asked for: one ask per panel, so a failed read does not loop. */
  #grantAsked = false;

  /** The children receive the same facts the host was given. */
  protected override updated(): void {
    if (this.client !== null && !this.#grantAsked) void this.#loadGrant();
    for (const child of this.renderRoot.querySelectorAll<HTMLElement & Record<string, unknown>>("qd-surface-settings, qd-accept-admin, qd-scope-rules")) {
      child.client = this.client;
      child.branchId = this.branchId;
    }
  }

  async #loadGrant(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    this.#grantAsked = true;
    const result = await client.settingsGrant();
    if (result.ok) this.grant = result.value;
  }

  reload(): void {
    // A failed grant read is tried again; a known grant stays.
    if (this.grant === null) void this.#loadGrant();
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
    // The policy section stays for every role (its read is open, and its write lock says why a role cannot
    // save). The two sections whose reads the server refuses some roles mount only on a grant, so the
    // refusing reads never fire and a person who cannot use them sees nothing in their place.
    return html`
      <details class="box" data-testid="qs-screen">
        <summary>Screen settings</summary>
        <qd-surface-settings></qd-surface-settings>
      </details>
      ${g?.canReadAcceptance ? html`
      <details class="box" data-testid="qa-details">
        <summary>Acceptance settings</summary>
        <qd-accept-admin></qd-accept-admin>
      </details>` : nothing}
      ${g?.canReadScopeRules ? html`
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
