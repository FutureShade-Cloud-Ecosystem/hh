// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-hold-expiry.ts;
// adapted for HH.
//
// The staff quote view tells a person when its stock holds lapsed. A quote's
// soft holds expire on the quote's own validity date, swept by the nightly
// job; the sweep works but is silent. A rep re-opens a three week old quote,
// sees the confidence it had the day it was sent, converts it, and finds out
// at the yard. This is the sentence that was missing.
//
// It is a status and not an alert: nothing broke, the hold lapsed on schedule
// and the stock went back to available. And when there is nothing to say it
// says nothing: no holds, every hold standing, or an unwired seam render
// nothing at all (no tick, no reassurance).
//
// HH change: the app holds no rule about when a re-hold may be offered. The
// offer rides with the lapse, the server decides, and a refusal (a 409 on an
// expired quote) is shown in the server's own words. After a re-hold the
// panel announces quote-changed and re-reads rather than assume.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { spokenRefusal } from "./_quote-workarea.js";
import {
  REHOLD_LABEL,
  expiryBanner,
  reholdRefusal,
  reholdResultNote,
  type SoftHoldExpiry,
} from "./_quote-hold-expiry.js";

@customElement("qd-quote-hold-expiry")
export class QdQuoteHoldExpiry extends PanelBase {
  @state() private st: SoftHoldExpiry | null = null;
  @state() private seamWired = true;
  /** A failed read, in words: it is not the same thing as "no lapse" and never renders as it. */
  @state() private readError = "";

  static styles = [
    panelStyles,
    css`
      :host {
        margin-top: 8px;
      }
      /* The gold EDGE, not a gold fill: a filled banner would shout. */
      .status {
        border: 1px solid var(--hos-line, var(--hh-line));
        border-left: 3px solid var(--hos-stone-amber, var(--hh-accent));
        border-radius: var(--hos-radius-card, var(--hh-radius-card));
        padding: 10px 12px;
        background: var(--hos-slate-warm, var(--hh-card));
      }
      .head {
        font-weight: 600;
        margin: 0 0 6px;
      }
      .kicker {
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
        font-size: var(--qp-caption);
        letter-spacing: 0.04em;
        text-transform: uppercase;
        margin: 0 0 4px;
      }
      ul {
        margin: 0;
        padding-left: 18px;
      }
      li {
        margin: 2px 0;
        font-family: var(--hos-font-mono, var(--hh-font-mono, monospace));
      }
      .note,
      .did {
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
        margin: 6px 0 0;
      }
      button {
        margin-top: 8px;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("quoteId") || changed.has("client")) {
      // A re-hold notice or a lapse read belongs to the quote it was made for.
      this.error = "";
      this.notice = "";
      this.st = null;
      this.readError = "";
      this.reload();
    }
  }

  reload(): void {
    void this.load();
  }

  private whenLabel(iso: string): string {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? iso : d.toLocaleString("en-CA");
  }

  private async load(): Promise<void> {
    const client = this.client;
    const ctx = this.ctx;
    if (client === null || ctx.id === "") {
      this.st = null;
      return;
    }
    const result = await ctx.guarded(client.holdExpiry(ctx.id));
    if (result === undefined) return;
    if (!result.ok) {
      // A lapse notice is not invented from a failed read, and silence would read as "nothing lapsed":
      // the panel says the status could not be read.
      this.st = null;
      this.readError = "The stock hold status could not be read, so it is not known whether any hold has lapsed.";
      return;
    }
    this.readError = "";
    this.st = result.value.expiry;
    this.seamWired = result.value.seam_wired;
  }

  protected override onClickAction(action: string): void {
    if (action === "rehold") void this.rehold();
  }

  private async rehold(): Promise<void> {
    const client = this.client;
    if (client === null || this.busy) return;
    const ctx = this.ctx;
    this.busy = true;
    this.error = "";
    this.notice = "";
    const result = await ctx.guarded(client.resyncHolds(ctx.id));
    if (result === undefined) return;
    this.busy = false;
    if (!result.ok) {
      this.error = spokenRefusal(reholdRefusal(result.status, result.error));
      return;
    }
    this.notice = reholdResultNote(result.value);
    // Re-read rather than assume: the status clears because the holds are real again, a fact only the server has.
    await this.load();
    if (ctx.stale) return;
    this.announceChanged();
  }

  override render() {
    const banner = expiryBanner(this.st, { seamWired: this.seamWired, whenLabel: (iso) => this.whenLabel(iso) });
    // After a re-hold the lapse is gone but its outcome is still worth saying, so a notice or a refusal keeps the region.
    if (banner === null && this.notice === "" && this.error === "" && this.readError === "") return nothing;
    return html`
      <div class="status" role="status" data-testid="he-box">
        <p class="kicker">Stock holds</p>
        ${banner !== null
          ? html`
              <p class="head" data-testid="he-headline">${banner.headline}</p>
              <ul>
                ${banner.lines.map((l) => html`<li data-testid="he-line">${l}</li>`)}
              </ul>
              <p class="note">Soft holds lapse when the quote's validity date passes.</p>
              ${banner.offerRehold
                ? html`<button type="button" data-action="rehold" ?disabled=${this.busy}>${this.busy ? "Re-holding..." : REHOLD_LABEL}</button>`
                : nothing}
            `
          : nothing}
        ${this.readError !== "" ? html`<p class="err" role="status" data-testid="he-unread">${this.readError}</p>` : nothing}
        ${this.error !== "" ? html`<p class="err" role="alert" data-testid="he-error">${this.error}</p>` : nothing}
        ${this.notice !== "" ? html`<p class="did" data-testid="he-notice">${this.notice}</p>` : nothing}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-hold-expiry": QdQuoteHoldExpiry;
  }
}
