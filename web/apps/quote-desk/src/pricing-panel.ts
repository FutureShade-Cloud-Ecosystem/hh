// The Sales manager's pricing panel (QD-4): the ERP's pricing console ported as five tabs,
// the category margin policy, the rule editor, customer prices, quantity breaks and the
// pricing log with its link to the record. It is mounted by the Quote Desk for the roles
// that edit prices until Dealer Admin has a screen of its own.
//
// THE APP NEVER DECIDES A ROLE RULE. The panel is offered wherever the server answers: the
// Pricing button is on every signed in desk, and the server's gate (the dealer admin
// surface at manage with the pricing editors list on the writes, the cost and margin list
// on the log) is what answers. A role the server refuses is shown the server's own words
// and no controls, and when a write or the floor check is refused the controls go and the
// prices stay readable. Nothing here guesses a role from a name.
//
// The templates carry no property or event bindings (the app's discipline): the host wires
// each tab by property after it renders, the tabs report refusals and record links as
// events, and the host answers them.
import { css, html, nothing } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { pricingStyles } from "./pricing-styles.js";
import type { PricingClient } from "./pricing-client.js";
import { PRICING_REFUSED_EVENT, type PricingRefusalDetail } from "./pricing-tab-base.js";
import { PRICING_DRILL_EVENT } from "./pricing-audit.js";
import type { PricingTabId } from "./_pricing-audit.js";
import "./pricing-margin.js";
import "./pricing-rules.js";
import "./pricing-customers.js";
import "./pricing-breaks.js";
import "./pricing-audit.js";

const TABS: ReadonlyArray<{ id: PricingTabId; label: string }> = [
  { id: "margin", label: "Margin policy" },
  { id: "rules", label: "Rules" },
  { id: "customers", label: "Customer prices" },
  { id: "breaks", label: "Quantity breaks" },
  { id: "audit", label: "Pricing log" },
];

const TAG: Record<PricingTabId, string> = {
  margin: "qd-pricing-margin",
  rules: "qd-pricing-rules",
  customers: "qd-pricing-customers",
  breaks: "qd-pricing-breaks",
  audit: "qd-pricing-audit",
};

@customElement("qd-pricing-panel")
export class QdPricingPanel extends PanelBase {
  /** The pricing client; the app root wires it beside the quote client. */
  @property({ attribute: false }) pricing: PricingClient | null = null;

  @state() private tab: PricingTabId = "margin";
  @state() private focusCustomerId = "";
  @state() private focusCategoryId = "";
  /** The server's words when it refused the price screens to this login. */
  @state() private denied = "";
  /** The server's words when it refused a write or the floor check: the prices stay readable. */
  @state() private readOnly = "";

  static styles = [
    pricingStyles,
    css`
      .title {
        display: flex;
        align-items: baseline;
        gap: 12px;
        flex-wrap: wrap;
        margin: 0 0 12px;
      }
      .title h2 {
        margin: 0;
        font-size: 1.05rem;
        font-weight: 600;
        text-transform: none;
        letter-spacing: 0;
        color: var(--hos-text, var(--hh-text));
      }
      .shell {
        padding: var(--hos-s-16, 16px) var(--hos-s-20, 20px);
      }
    `,
  ];

  reload(): void {
    const child = this.renderRoot.querySelector<HTMLElement & { reload?: () => void }>(TAG[this.tab]);
    child?.reload?.();
  }

  override connectedCallback(): void {
    super.connectedCallback();
    this.renderRoot.addEventListener(PRICING_REFUSED_EVENT, this.onRefused as EventListener);
    this.renderRoot.addEventListener(PRICING_DRILL_EVENT, this.onDrill as EventListener);
  }

  override disconnectedCallback(): void {
    this.renderRoot.removeEventListener(PRICING_REFUSED_EVENT, this.onRefused as EventListener);
    this.renderRoot.removeEventListener(PRICING_DRILL_EVENT, this.onDrill as EventListener);
    super.disconnectedCallback();
  }

  private readonly onRefused = (event: CustomEvent<PricingRefusalDetail>): void => {
    const detail = event.detail;
    if (detail.write) {
      this.readOnly = detail.message;
      return;
    }
    // The log is behind the cost and margin list, a different wall from the price screens:
    // a refusal there is the log tab's own to show, not the whole panel's.
    if (this.tab !== "audit") this.denied = detail.message;
  };

  private readonly onDrill = (event: CustomEvent<{ tab: PricingTabId; id: string; kind: string }>): void => {
    const { tab, id, kind } = event.detail;
    if (kind === "customer") this.focusCustomerId = id;
    if (kind === "category") this.focusCategoryId = id;
    this.tab = tab;
  };

  protected override onClickAction(action: string, control: HTMLElement): void {
    if (action !== "tab") return;
    const next = TABS.find((t) => t.id === control.getAttribute("data-tab"));
    if (next !== undefined) this.tab = next.id;
  }

  /** The wiring pass: the active tab receives what a binding would have given it. */
  protected override updated(): void {
    const child = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>(TAG[this.tab]);
    if (child === null) return;
    if (child["pricing"] !== this.pricing) child["pricing"] = this.pricing;
    const canEdit = this.readOnly === "";
    if (child["canEdit"] !== canEdit) child["canEdit"] = canEdit;
    if (this.tab === "margin" && child["focusCategoryId"] !== this.focusCategoryId) child["focusCategoryId"] = this.focusCategoryId;
    if (this.tab === "customers") {
      if (child["focusCustomerId"] !== this.focusCustomerId) child["focusCustomerId"] = this.focusCustomerId;
      if (child["branchScope"] !== this.branchId) child["branchScope"] = this.branchId;
    }
  }

  private renderTab() {
    switch (this.tab) {
      case "margin":
        return html`<qd-pricing-margin></qd-pricing-margin>`;
      case "rules":
        return html`<qd-pricing-rules></qd-pricing-rules>`;
      case "customers":
        return html`<qd-pricing-customers></qd-pricing-customers>`;
      case "breaks":
        return html`<qd-pricing-breaks></qd-pricing-breaks>`;
      case "audit":
        return html`<qd-pricing-audit></qd-pricing-audit>`;
    }
  }

  override render() {
    if (this.denied !== "") {
      return html`
        <div class="shell" data-testid="pricing-panel">
          <div class="refusal" role="alert" data-testid="pricing-denied">
            <h2>Pricing is not open to this login</h2>
            <p data-testid="pricing-denied-words">The server answered: ${this.denied}</p>
            <p class="dim">Prices are set by the roles the dealer has named as pricing editors. Nothing here can be read or changed from this login.</p>
          </div>
        </div>
      `;
    }
    return html`
      <div class="shell" data-testid="pricing-panel">
        <div class="title">
          <h2>Pricing</h2>
          <span class="dim small">Margin policy, rules, customer prices and quantity breaks. Every change is logged.</span>
        </div>
        <nav class="subnav" aria-label="Pricing sections">
          ${TABS.map(
            (t) => html`<button type="button" class="${t.id === this.tab ? "active" : ""}" aria-current=${t.id === this.tab ? "true" : "false"} data-action="tab" data-tab="${t.id}" data-testid="pricing-tab-${t.id}">${t.label}</button>`,
          )}
        </nav>
        ${this.readOnly !== ""
          ? html`<p class="readonly-note" role="alert" data-testid="pricing-readonly">
              The server refused a change from this login: ${this.readOnly}. The prices stay readable and the controls are off.
            </p>`
          : nothing}
        ${this.renderTab()}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-pricing-panel": QdPricingPanel;
  }
}
