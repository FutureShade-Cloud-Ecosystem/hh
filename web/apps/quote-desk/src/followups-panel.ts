// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-followups.ts;
// adapted for HH.
//
// The follow-up panel on the quote detail. It answers the question a
// salesperson has in front of a quiet quote, "has this customer heard from us,
// and when will they hear from us next?", and gives the two controls that
// question leads to: send the quote by text now, and stop (or resume) the
// automatic follow-ups on this one quote.
//
// Everything it says is true with the provider switched off: a step that went
// out reads "Queued", never "Sent" (see stateLabel). No decision is made here:
// the cadence, the consent refusal and the schedule all come from the server,
// and a refusal is shown in the server's own words.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { isShareable } from "./_quote-edit.js";
import { spokenRefusal } from "./_quote-workarea.js";
import {
  contactLine,
  queuedNotice,
  shareErrorMessage,
  stateLabel,
  summaryLine,
  timeline,
  type FollowUpHistory,
} from "./_quote-followups.js";

@customElement("qd-quote-followups")
export class QdQuoteFollowups extends PanelBase {
  @state() private history: FollowUpHistory | null = null;
  @state() private shareable = false;
  /** Set when the server says the automation is not configured (503). */
  @state() private unavailable = "";
  @state() private loaded = false;

  static styles = [
    panelStyles,
    css`
      :host {
        margin-top: 10px;
      }
      .summary {
        margin: 0 0 6px;
      }
      .contact {
        margin: 0 0 8px;
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
      }
      .row {
        margin: 0 0 8px;
      }
      tr.future td {
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      .why {
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("quoteId") || changed.has("client")) {
      // Everything on screen belonged to the last quote: its refusal, its notice, its plan.
      this.error = "";
      this.notice = "";
      this.history = null;
      this.unavailable = "";
      this.loaded = false;
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
      this.history = null;
      this.loaded = false;
      return;
    }
    // A cancelled context issues no more requests: the panel left the page (sign out) and a read of its is not this session's.
    if (ctx.stale) return;
    // A newer read of this quote supersedes an older one still out: the older answer never lands over it.
    const seq = ctx.startRead();
    const answers = await ctx.guardedRead(seq, Promise.all([client.followUps(ctx.id), client.readQuote(ctx.id)]));
    if (answers === undefined) return;
    const [result, quote] = answers;
    // Whether the quote can still be shared is the quote's own status; a read that fails offers no send.
    this.shareable = quote.ok && isShareable(quote.value);
    if (!result.ok) {
      this.history = null;
      this.loaded = true;
      if (result.status === 503) {
        this.unavailable = "Text messaging and follow-up automation are not configured on this server.";
        return;
      }
      this.unavailable = "";
      this.error = "The follow-ups did not load. Check the connection and press Refresh.";
      return;
    }
    this.unavailable = "";
    this.history = result.value;
    this.loaded = true;
  }

  protected override onClickAction(action: string): void {
    if (action === "share") void this.share();
    else if (action === "stop") void this.change(true);
    else if (action === "resume") void this.change(false);
  }

  private async share(): Promise<void> {
    const client = this.client;
    if (client === null || this.busy) return;
    const ctx = this.ctx;
    this.busy = true;
    this.error = "";
    this.notice = "";
    const result = await ctx.guarded(client.shareBySMS(ctx.id), this.lateWrite(ctx, (late) => spokenRefusal(shareErrorMessage(late.status, late.error))));
    // The person opened another quote while this was out: its answer says nothing there.
    if (result === undefined) return;
    this.busy = false;
    if (!result.ok) {
      this.error = spokenRefusal(shareErrorMessage(result.status, result.error));
      return;
    }
    this.notice = queuedNotice();
    await this.load();
    if (ctx.stale) return;
    // The surface shows the status, which a first share moves from draft to sent.
    this.announceChanged();
  }

  private async change(stop: boolean): Promise<void> {
    const client = this.client;
    if (client === null || this.busy) return;
    const ctx = this.ctx;
    this.busy = true;
    this.error = "";
    this.notice = "";
    const result = await ctx.guarded(
      stop ? client.stopFollowUps(ctx.id) : client.resumeFollowUps(ctx.id),
      this.lateWrite(ctx, (late) => spokenRefusal(shareErrorMessage(late.status, late.error))),
    );
    if (result === undefined) return;
    this.busy = false;
    if (!result.ok) {
      this.error = spokenRefusal(shareErrorMessage(result.status, result.error));
      return;
    }
    this.history = result.value;
    this.notice = stop ? "Follow-ups turned off for this quote." : "Follow-ups turned on.";
    this.announceChanged();
  }

  private renderTimeline() {
    const rows = timeline(this.history);
    if (rows.length === 0) return html`<p class="dim" data-testid="fu-empty">Nothing has been sent yet, and nothing is scheduled.</p>`;
    return html`
      <div class="tscroll"><table data-testid="fu-table">
        <thead>
          <tr>
            <th>Step</th>
            <th>Channel</th>
            <th>State</th>
            <th>When</th>
            <th>Note</th>
          </tr>
        </thead>
        <tbody>
          ${rows.map(
            (r) => html`<tr class="${r.future ? "future" : ""}" data-testid="fu-row">
              <td>Day ${r.day}</td>
              <td>${r.channel === "SMS" ? "Text" : "Email"}</td>
              <td>${stateLabel(r.state)}</td>
              <td>${this.whenLabel(r.when)}</td>
              <td class="why">${r.detail}</td>
            </tr>`,
          )}
        </tbody>
      </table></div>
    `;
  }

  override render() {
    if (this.quoteId === "") return nothing;
    if (this.unavailable !== "") {
      // Stated, not hidden: an operator who cannot find the button needs to know the server has the
      // feature switched off rather than assume the page is broken.
      return html`<fieldset data-testid="fu-box">
        <legend>Follow-ups</legend>
        <p class="dim" data-testid="fu-unavailable">${this.unavailable}</p>
      </fieldset>`;
    }
    if (!this.loaded) return nothing;
    if (this.history === null && this.error !== "") {
      return html`<fieldset data-testid="fu-box">
        <legend>Follow-ups</legend>
        <p class="err" role="alert" data-testid="fu-error">${this.error}</p>
      </fieldset>`;
    }
    const plan = this.history?.plan;
    const contact = contactLine(this.history, (iso) => this.whenLabel(iso));
    return html`
      <fieldset data-testid="fu-box">
        <legend>Follow-ups</legend>
        <p class="summary" data-testid="fu-summary">${summaryLine(this.history)}</p>
        ${contact !== "" ? html`<p class="contact" data-testid="fu-contact">${contact}</p>` : nothing}
        <div class="row">
          ${this.shareable
            ? html`<button
                type="button"
                data-action="share"
                ?disabled=${this.busy}
                title="Texts this customer their quote link. Nothing is sent if they have not opted in."
              >
                Text the quote link
              </button>`
            : nothing}
          ${plan !== undefined
            ? plan.enabled
              ? html`<button type="button" data-action="stop" ?disabled=${this.busy}>Stop follow-ups</button>`
              : html`<button type="button" data-action="resume" ?disabled=${this.busy}>Restart follow-ups</button>`
            : nothing}
        </div>
        ${this.renderTimeline()}
        ${this.error !== "" ? html`<p class="err" role="alert" data-testid="fu-error">${this.error}</p>` : nothing}
        ${this.notice !== "" ? html`<p class="ok" role="status" data-testid="fu-notice">${this.notice}</p>` : nothing}
      </fieldset>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-followups": QdQuoteFollowups;
  }
}
