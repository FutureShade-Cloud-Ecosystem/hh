// The Approvals surface's queue (decision log rows 954, 955, 956): the margin approvals page, manager only. It
// hosts the pending approvals panel full page; each request row opens its own detail at #/approvals/<id>. The
// route's wall is the host's: this element is never mounted for Sales.
import { LitElement, css, html } from "lit";
import { customElement, property } from "lit/decorators.js";
import "./approvals-panel.js";
import type { QdClient } from "./qd-client.js";

@customElement("qd-approvals-queue")
export class QdApprovalsQueue extends LitElement {
  @property({ attribute: false }) client: QdClient | null = null;

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
    const panel = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("qd-approvals");
    if (panel !== null) panel.client = this.client;
  }

  /** The fan-out's hook: the queue re-reads. */
  reload(): void {
    this.renderRoot.querySelector<HTMLElement & { reload?: () => void }>("qd-approvals")?.reload?.();
  }

  override render() {
    return html`<h1 data-testid="ap-title">Approvals</h1><qd-approvals></qd-approvals>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-approvals-queue": QdApprovalsQueue;
  }
}
