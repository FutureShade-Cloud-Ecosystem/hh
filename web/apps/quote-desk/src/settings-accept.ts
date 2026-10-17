// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-accept-admin.ts;
// adapted for HH.
//
// The dealer's acceptance settings: ONE switch, may a quote be accepted more
// than once. It ships off, and the refusal a rep sees at the counter points
// here. The switch is dealer wide, so the server gives the write to the
// General Manager alone (CP0-5 answer A) and answers everyone else with a
// sentence naming the role. The app holds no such rule: a read the server
// refuses shows the server's sentence in place of the switch, and a write the
// server refuses leaves the switch where it was, drops its fill and shows the
// sentence beside it.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import type { AcceptanceView } from "./_panel-wire.js";
import { acceptanceNotice, writeRefusalLine } from "./_quote-settings.js";

@customElement("qd-accept-admin")
export class QdAcceptAdmin extends PanelBase {
  @state() private settings: AcceptanceView | null = null;
  /** The server refused the read: its own sentence stands in place of the switch. */
  @state() private readRefusal = "";
  /** The server refused the write: the switch is locked with its sentence. */
  @state() private writeRefusal = "";

  static styles = [
    panelStyles,
    css`
      :host {
        margin-top: 8px;
      }
      .why {
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
        margin: 6px 0 0;
      }
      label.sw {
        display: inline-flex;
        gap: 6px;
        align-items: center;
        font-size: var(--qp-body);
        color: var(--hos-text, var(--hh-text));
        margin: 0;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("client")) this.reload();
  }

  reload(): void {
    void this.load();
  }

  /**
   * Numbers the reads and the flip of this switch: a read's answer applies only while nothing newer has begun, and a flip
   * counts as newer when it answers, so a read begun before it never lays the old value over the flip.
   */
  private generation = 0;

  private async load(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const mine = ++this.generation;
    const result = await client.getAcceptance();
    if (mine !== this.generation) return;
    if (!result.ok) {
      this.settings = null;
      this.readRefusal = writeRefusalLine(result.status, result.error);
      return;
    }
    this.readRefusal = "";
    this.settings = result.value;
  }

  private async flip(next: boolean): Promise<void> {
    const client = this.client;
    if (client === null || this.busy) return;
    this.busy = true;
    this.error = "";
    this.notice = "";
    const result = await client.setAcceptance(next);
    this.generation += 1; // a read begun before this answer must not land over it
    this.busy = false;
    if (!result.ok) {
      const line = writeRefusalLine(result.status, result.error);
      if (result.status === 403) this.writeRefusal = line;
      else this.error = line;
      await this.load();
      return;
    }
    this.settings = result.value;
    this.notice = acceptanceNotice(result.value.allowMultiAccept);
  }

  protected override onFieldChange(field: string, control: HTMLElement): void {
    if (field === "multi") void this.flip((control as HTMLInputElement).checked);
  }

  protected override updated(): void {
    // While the write is in flight the box keeps the position the person gave
    // it; the server's answer (or its refusal) decides where it settles.
    if (this.busy) return;
    const box = this.renderRoot.querySelector<HTMLInputElement>('[data-field="multi"]');
    if (box !== null && this.settings !== null && box.checked !== this.settings.allowMultiAccept) box.checked = this.settings.allowMultiAccept;
  }

  override render() {
    if (this.readRefusal !== "") {
      return html`<p class="lock" role="status" data-testid="qa-refused">${this.readRefusal}</p>`;
    }
    if (this.settings === null) return html`<p class="dim">Loading acceptance settings...</p>`;
    const locked = this.writeRefusal !== "" || !this.settings.switchWired;
    return html`
      <div class="row flush-top">
        <label class="sw">
          <input type="checkbox" data-field="multi" data-testid="qa-switch" ?disabled=${this.busy || locked} />
          Allow accepting a quote more than once
        </label>
      </div>
      <p class="why">
        Off (the default): once a quote has been accepted as an order, a second acceptance is refused and points at that order. On: a
        repeat acceptance creates another order, and the Related documents list on each quote and order shows where every document came
        from.
      </p>
      ${this.writeRefusal !== "" ? html`<p class="lock" role="status" data-testid="qa-lock">${this.writeRefusal}</p>` : nothing}
      ${this.error !== "" ? html`<p class="err" role="alert">${this.error}</p>` : nothing}
      ${this.notice !== "" ? html`<p class="ok" role="status" data-testid="qa-notice">${this.notice}</p>` : nothing}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-accept-admin": QdAcceptAdmin;
  }
}
