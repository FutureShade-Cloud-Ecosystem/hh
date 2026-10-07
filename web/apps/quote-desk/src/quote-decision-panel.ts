// The customer's decision, recorded for them (PR 247): a sent quote's customer said yes or no by phone, text,
// email or in person, and the staff member writes it down here. The offer is the server's (can_accept and
// can_reject ride the quote read); the accept names the channel, carries the total that was read to the customer
// (the server refuses it when the quote has moved) and the statement that the customer confirmed the quantities,
// which is the notice the quote carries. The server's refusals are shown in its own words; a retry carries the
// same idempotency key, so a lost answer never records the decision twice. Accepted, the quote offers Convert.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { moneyWord, quoteErrorMessage, type QuoteView } from "./_quote-edit.js";
import {
  acceptReady,
  acceptedNotice,
  declinedNotice,
  keyFor,
  rejectReady,
  type DecisionChannel,
  type DeclineReason,
} from "./_quote-decision.js";

type Mode = "none" | "accept" | "reject";

@customElement("qd-quote-decision")
export class QdQuoteDecision extends PanelBase {
  @state() private view: QuoteView | null = null;
  @state() private mode: Mode = "none";
  @state() private channel = "";
  @state() private reason = "";
  @state() private note = "";
  @state() private confirmed = false;
  @state() private result = "";
  private keyed: { signature: string; key: string } | null = null;

  static styles = [
    panelStyles,
    css`
      :host {
        margin-top: 12px;
      }
      .decision {
        border: 1px solid var(--hos-line, var(--hh-line));
        border-radius: var(--hos-radius-card, var(--hh-radius-card));
        padding: 12px 16px;
        background: var(--hos-slate-warm, var(--hh-card));
      }
      .row {
        display: flex;
        flex-wrap: wrap;
        gap: 8px;
        align-items: center;
        margin-top: 8px;
      }
      label.f {
        display: grid;
        gap: 4px;
        font-size: var(--qp-caption);
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      textarea,
      select {
        font: inherit;
        font-size: var(--qp-body);
        max-width: 100%;
      }
      .statement {
        display: flex;
        gap: 8px;
        align-items: flex-start;
        margin-top: 8px;
        font-size: var(--qp-body);
      }
      .total {
        font-size: var(--qp-body);
        margin: 8px 0 0;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("quoteId") || changed.has("client")) {
      this.view = null;
      this.mode = "none";
      this.channel = "";
      this.reason = "";
      this.note = "";
      this.confirmed = false;
      this.result = "";
      this.keyed = null;
      this.reload();
    }
  }

  reload(): void {
    void this.load();
  }

  private async load(): Promise<void> {
    const client = this.client;
    const ctx = this.ctx;
    if (client === null || ctx.id === "" || ctx.stale) return;
    const seq = ctx.startRead();
    const read = await ctx.guardedRead(seq, client.readQuote(ctx.id));
    if (read === undefined || !read.ok) return;
    this.view = read.value;
    if (!read.value.can_accept && !read.value.can_reject) this.mode = "none";
  }

  protected override onClickAction(action: string): void {
    if (action === "accept-open") this.openMode("accept");
    else if (action === "reject-open") this.openMode("reject");
    else if (action === "decision-cancel") this.openMode("none");
    else if (action === "accept") void this.decide("accept");
    else if (action === "reject") void this.decide("reject");
  }

  /** Opening a form starts it clean: what was picked in an earlier opening is not carried into this one. */
  private openMode(mode: Mode): void {
    this.mode = mode;
    this.channel = "";
    this.reason = "";
    this.note = "";
    this.confirmed = false;
    this.keyed = null;
    this.error = "";
    this.result = "";
  }

  /** The fields are uncontrolled: after each render they are set to what the state holds, so the screen and the state never differ. */
  protected override updated(): void {
    this.syncValue('[data-field="decision-channel"]', this.channel);
    this.syncValue('[data-field="decision-reason"]', this.reason);
    this.syncValue('[data-field="decision-note"]', this.note);
    const statement = this.renderRoot.querySelector<HTMLInputElement>('[data-field="decision-statement"]');
    if (statement !== null && statement.checked !== this.confirmed) statement.checked = this.confirmed;
  }

  protected override onFieldInput(field: string, control: HTMLElement): void {
    const value = (control as HTMLTextAreaElement).value;
    if (field === "decision-note") this.note = value;
  }

  protected override onFieldChange(field: string, control: HTMLElement): void {
    if (field === "decision-channel") this.channel = (control as HTMLSelectElement).value;
    if (field === "decision-reason") this.reason = (control as HTMLSelectElement).value;
    if (field === "decision-statement") this.confirmed = (control as HTMLInputElement).checked;
    this.error = "";
  }

  private async decide(kind: "accept" | "reject"): Promise<void> {
    const client = this.client;
    const view = this.view;
    if (client === null || view === null || this.busy) return;
    const ctx = this.ctx;
    const channel = this.channel as DecisionChannel;
    const note = this.note.trim();
    if (kind === "accept" && !acceptReady(channel, this.confirmed)) return;
    if (kind === "reject" && !rejectReady(channel, this.reason, this.note)) return;
    this.busy = true;
    this.error = "";
    const quoteNo = view.quote_no;
    const total = view.total_cents;
    this.keyed = keyFor(this.keyed, [kind, channel, this.reason, note, total].join("|"), () => crypto.randomUUID());
    const key = this.keyed.key;
    const call =
      kind === "accept"
        ? client.acceptOnBehalf(ctx.id, { channel, note, expectedTotalCents: total }, key)
        : client.rejectOnBehalf(ctx.id, { channel, reason: this.reason as DeclineReason, note }, key);
    const done = await ctx.guarded(call, this.lateWrite(ctx, (late) => spokenRefusal(quoteErrorMessage(late.status, late.error, "record the decision"))));
    if (done === undefined) return;
    this.busy = false;
    if (!done.ok) {
      this.error = spokenRefusal(quoteErrorMessage(done.status, done.error, "record the decision"));
      return;
    }
    this.keyed = null;
    this.mode = "none";
    this.channel = "";
    this.reason = "";
    this.note = "";
    this.confirmed = false;
    this.result = kind === "accept" ? acceptedNotice(quoteNo, channel, total) : declinedNotice(quoteNo, channel);
    await this.load();
    if (ctx.stale) return;
    this.announceChanged();
  }

  private renderChannel() {
    return html`<label class="f"
      >How the customer said so
      <select data-field="decision-channel" data-testid="qb-decision-channel" aria-label="How the customer said so">
        <option value="">Choose one</option>
        <option value="phone">On the phone</option>
        <option value="text">By text</option>
        <option value="email">By email</option>
        <option value="in_person">In person</option>
      </select></label
    >`;
  }

  private renderAccept(view: QuoteView) {
    const ready = acceptReady(this.channel, this.confirmed);
    return html`
      <p class="total" data-testid="qb-decision-total">Total to read to the customer: <strong>${moneyWord(view.total_cents)}</strong></p>
      <div class="row">${this.renderChannel()}</div>
      <div class="statement">
        <input type="checkbox" id="qd-statement" data-field="decision-statement" data-testid="qb-decision-statement" />
        <label for="qd-statement" data-testid="qb-decision-statement-text">The customer confirmed the quantities. ${view.quantity_notice}</label>
      </div>
      <div class="row">
        <label class="f" style="flex: 1 1 18rem"
          >Note (optional)
          <textarea rows="2" maxlength="500" data-field="decision-note" data-testid="qb-decision-note" aria-label="Note"></textarea></label
        >
      </div>
      <div class="row">
        <button type="button" class="primary" data-action="accept" data-testid="qb-decision-accept" ?disabled=${!ready || this.busy}>Record acceptance</button>
        <button type="button" data-action="decision-cancel" data-testid="qb-decision-cancel">Cancel</button>
      </div>
    `;
  }

  private renderReject() {
    const ready = rejectReady(this.channel, this.reason, this.note);
    return html`
      <div class="row">
        ${this.renderChannel()}
        <label class="f"
          >Why
          <select data-field="decision-reason" data-testid="qb-decision-reason" aria-label="Why">
            <option value="">Choose one</option>
            <option value="price">The price</option>
            <option value="competitor">Went with a competitor</option>
            <option value="project_cancelled">The project was cancelled</option>
            <option value="timing">The timing</option>
            <option value="no_response">No response</option>
            <option value="other">Something else (say what)</option>
          </select></label
        >
      </div>
      <div class="row">
        <label class="f" style="flex: 1 1 18rem"
          >Note${this.reason === "other" ? " (required)" : " (optional)"}
          <textarea rows="2" maxlength="500" data-field="decision-note" data-testid="qb-decision-note" aria-label="Note"></textarea></label
        >
      </div>
      <div class="row">
        <button type="button" class="primary" data-action="reject" data-testid="qb-decision-reject" ?disabled=${!ready || this.busy}>Record decline</button>
        <button type="button" data-action="decision-cancel" data-testid="qb-decision-cancel">Cancel</button>
      </div>
    `;
  }

  override render() {
    const view = this.view;
    const offered = view !== null && (view.can_accept || view.can_reject);
    if (!offered && this.result === "" && this.error === "") return nothing;
    return html`
      ${offered && view !== null
        ? html`<section class="decision" data-testid="qb-decision" aria-label="Record the customer's decision">
            <h3>The customer's decision</h3>
            <p class="sub">Heard from the customer another way? Write it down here. Nothing is sent to them.</p>
            ${this.mode === "none"
              ? html`<div class="row">
                  ${view.can_accept ? html`<button type="button" data-action="accept-open" data-testid="qb-decision-accept-open">The customer accepted</button>` : nothing}
                  ${view.can_reject ? html`<button type="button" data-action="reject-open" data-testid="qb-decision-reject-open">The customer declined</button>` : nothing}
                </div>`
              : this.mode === "accept"
                ? this.renderAccept(view)
                : this.renderReject()}
            ${this.error !== "" ? html`<p class="err" role="alert" data-testid="qb-decision-error">${this.error}</p>` : nothing}
          </section>`
        : nothing}
      ${this.result !== "" ? html`<p class="did" role="status" data-testid="qb-decision-result">${this.result}</p>` : nothing}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-decision": QdQuoteDecision;
  }
}
