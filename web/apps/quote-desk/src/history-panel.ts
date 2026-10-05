// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-edits.ts;
// adapted for HH.
//
// The change history: who changed what, when, for the open quote. The trail
// arrives in causal order (the audit sequence), so nothing here re-sorts; it
// is only grouped so one action reads as one entry. A group leads with the
// action (a discount, a quantity, an added line) and shows the money that
// moved beneath it, so the consequence is visible without pretending it was a
// separate edit. The discount changes are rows like any other.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { quoteErrorMessage } from "./_quote-edit.js";
import {
  changeActor,
  fieldLabel,
  formatChangeValue,
  groupChanges,
  historyHeadline,
  summariseGroup,
  type QuoteFieldChange,
} from "./_quote-history.js";
import { spokenRefusal } from "./_quote-workarea.js";

@customElement("qd-quote-history")
export class QdQuoteHistory extends PanelBase {
  @state() private changes: QuoteFieldChange[] = [];
  @state() private loaded = false;

  static styles = [
    panelStyles,
    css`
      ol {
        list-style: none;
        margin: 0;
        padding: 0;
        display: flex;
        flex-direction: column;
        gap: 10px;
      }
      li {
        border-left: 2px solid var(--hos-line, var(--hh-line));
        padding-left: 10px;
      }
      .head {
        font-size: var(--qp-body);
        font-weight: 600;
      }
      .meta {
        font-size: var(--qp-caption);
        color: var(--hos-text-dim, var(--hh-text-dim));
        margin-top: 2px;
      }
      .detail {
        font-size: var(--qp-caption);
        color: var(--hos-text-dim, var(--hh-text-dim));
        margin-top: 4px;
        display: flex;
        flex-direction: column;
        gap: 1px;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("quoteId") || changed.has("client")) {
      // The last quote's changes never sit under the next quote's heading or error.
      this.changes = [];
      this.loaded = false;
      this.error = "";
      this.reload();
    }
  }

  reload(): void {
    void this.load();
  }

  /** An answer for the quote that was open before is dropped, never shown over the one open now, and an older read of this quote never lands over a newer one. */
  private async load(): Promise<void> {
    const client = this.client;
    const ctx = this.ctx;
    if (client === null || ctx.id === "") {
      this.changes = [];
      this.loaded = false;
      return;
    }
    // A cancelled context issues no more requests: the panel left the page (sign out) and a read of its is not this session's.
    if (ctx.stale) return;
    this.error = "";
    const seq = ctx.startRead();
    const result = await ctx.guardedRead(seq, client.editHistory(ctx.id));
    if (result === undefined) return;
    if (!result.ok) {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "read this quote's history"));
      return;
    }
    this.changes = result.value;
    this.loaded = true;
  }

  private whenLabel(iso: string): string {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? iso : d.toLocaleString("en-CA");
  }

  private renderGroup(group: QuoteFieldChange[]) {
    const lead = summariseGroup(group);
    if (lead === undefined) return nothing;
    // The lead names the action; the rest are its consequences.
    const rest = group.filter((c) => c !== lead);
    return html`
      <li data-testid="qh-group">
        <div class="head">${historyHeadline(lead)}</div>
        <div class="meta">${changeActor(lead)} · ${this.whenLabel(lead.occurred_at)}</div>
        ${rest.length > 0
          ? html`<div class="detail">
              ${rest.map((c) => html`<span>${fieldLabel(c.field)}: ${formatChangeValue(c.field, c.before)} → ${formatChangeValue(c.field, c.after)}</span>`)}
            </div>`
          : nothing}
      </li>
    `;
  }

  override render() {
    if (this.quoteId === "") return nothing;
    return html`
      <div class="box" data-testid="qh-box">
        <h3>Change history</h3>
        ${this.error !== "" ? html`<p class="err" role="alert">${this.error}</p>` : nothing}
        ${this.error === "" && this.loaded && this.changes.length === 0 ? html`<p class="dim">No changes yet.</p>` : nothing}
        ${this.changes.length > 0 ? html`<ol>${groupChanges(this.changes).map((g) => this.renderGroup(g))}</ol>` : nothing}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-history": QdQuoteHistory;
  }
}
