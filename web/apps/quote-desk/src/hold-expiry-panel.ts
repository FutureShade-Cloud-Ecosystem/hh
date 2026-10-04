// Placeholder until the panel is ported; replaced whole by its coder.
import { html, nothing } from "lit";
import { customElement } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";

@customElement("qd-quote-hold-expiry")
export class QdQuoteHoldExpiry extends PanelBase {
  reload(): void {}
  override render() {
    return this.quoteId === "" ? nothing : html``;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-hold-expiry": QdQuoteHoldExpiry;
  }
}
