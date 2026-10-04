// Placeholder until the panel is ported; replaced whole by its coder.
import { html } from "lit";
import { customElement } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";

@customElement("qd-approvals")
export class QdApprovals extends PanelBase {
  reload(): void {}
  override render() {
    return html``;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-approvals": QdApprovals;
  }
}
