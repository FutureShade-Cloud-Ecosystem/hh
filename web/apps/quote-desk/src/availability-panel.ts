// Placeholder until the panel is ported; replaced whole by its coder.
import { html, nothing } from "lit";
import { customElement } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";

@customElement("qd-quote-availability")
export class QdQuoteAvailability extends PanelBase {
  reload(): void {}
  override render() {
    return this.quoteId === "" ? nothing : html``;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-availability": QdQuoteAvailability;
  }
}
