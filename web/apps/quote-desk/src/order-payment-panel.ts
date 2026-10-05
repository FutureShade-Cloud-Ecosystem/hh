// The counter payment panel (QD-5): take payment at the counter on a
// converted quote's order, through the server's own door
// (POST /api/v1/quotes/{id}/order-payment, quoteTakeOrderPayment).
//
// WHAT THE PANEL DOES, and what it never does:
//
// - The four tenders exactly as the contract names them (CASH, CARD, CHEQUE,
//   EFT), the amount typed in DOLLARS and sent as INTEGER CENTS parsed
//   through the digits (never multiplied), a free reference (a cheque number,
//   an EFT trace, a terminal reference; may be empty for cash).
// - The idempotency key is minted ONCE per payment attempt and kept across
//   retries of the same attempt: a refusal, a dropped answer or a 5xx keeps
//   the key, so a retry replays the first result on the server and posts
//   nothing a second time. Only a payment the server took ends an attempt;
//   the next deliberate payment mints a fresh key. The ring is
//   _order-payment.ts's PaymentKeyRing, the whole rule in one place.
// - The answer is shown as the receipt, every figure the SERVER answered:
//   the invoice number, the payment number, the tender, the amount, the
//   balance, who received it and the reference as stored. Never an invented
//   figure: the panel computes nothing.
// - A refusal is shown in the server's words (403 role or branch, 400
//   amounts, 409 including an order already invoiced another route, whose
//   wording names who can record the payment).
// - An UNKNOWN outcome (no status, a 5xx, an unreadable body) says so
//   plainly and tells the person that trying again with the same amount and
//   method is safe: the same key replays the first result.
// - The cash customer's one action: the same door converts, invoices and
//   takes the payment in full. The contract gives this desk no signal in the
//   quote view or the family read that says a conversion is pending, so the
//   panel offers the action on an ACCEPTED quote (and the plain payment on a
//   CONVERTED one) and lets the SERVER refuse, showing its words: the app
//   decides nothing by customer type or by role.
//
// Every read and write goes through the quote context: an answer that lands
// after the person left the quote is never applied to the quote now open (a
// late success is announced for ITS quote, so every view of it re-reads; a
// late refusal is said only on the quote it belongs to).
import { css, html, nothing, type TemplateResult } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { moneyWord, quoteErrorMessage } from "./_quote-edit.js";
import { docStatusWord, kindLabel, type RelatedDoc } from "./_related-docs.js";
import {
  PaymentKeyRing,
  TENDER_METHODS,
  convertedNote,
  isUnknownOutcome,
  paymentRefusalWords,
  receiptRows,
  replayedNote,
  typedCents,
  unknownOutcomeNote,
  type TenderMethod,
} from "./_order-payment.js";
import type { OrderPaymentAnswer } from "./qd-client.js";
import type { QuoteContext } from "./quote-context.js";
import type { Result } from "@hardscapehouse/api-client";
import { spokenRefusal } from "./_quote-workarea.js";

/** Said on return when a payment was taken on this quote while the person was away: its receipt went with the answer. */
const LATE_PAYMENT_NOTE = "A payment was taken on this quote while you were away. Its receipt was shown only once; the documents panel now holds what the server recorded.";

@customElement("qd-order-payment")
export class QdOrderPayment extends PanelBase {
  /** The converted order's id, "" until the quote has been converted. The family read is the truth this panel follows. */
  @property({ attribute: "order-id" }) orderId = "";

  @state() private status = "";
  @state() private family: RelatedDoc[] = [];
  @state() private loaded = false;
  /** A failed read, in words: the payment controls never render over a guess. */
  @state() private readError = "";
  @state() private tender: TenderMethod = "CASH";
  @state() private amountRaw = "";
  @state() private reference = "";
  /** The receipt of the payment the server took (or replayed) on this quote. */
  @state() private receipt: OrderPaymentAnswer | null = null;
  /** The unknown outcome note: set when no answer the desk can read came back; a retry keeps the same key. */
  @state() private unknown = "";

  /** The idempotency keys of this panel: one key per attempt, kept across retries. */
  private readonly keys = new PaymentKeyRing();

  /** What a late write changed that the screen cannot show, said to the person who comes back, by quote. */
  private readonly lateNotes = new Map<string, string>();

  static styles = [
    panelStyles,
    css`
      :host {
        display: block;
        margin-top: 8px;
      }
      legend {
        font-weight: 600;
      }
      .sub {
        margin: 0 0 6px;
      }
      .tenders {
        display: grid;
        grid-template-columns: repeat(4, 1fr);
        gap: 8px;
        margin-top: 8px;
      }
      /* The till's own tile language: an unselected tile is the card itself with a grey
         border; the selected tile takes the warm cream fill and the amber edge and keeps
         the normal ink (colour is the state, never a fade and never a second accent). */
      .tenders button {
        border: 1px solid var(--hos-line, var(--hh-line));
        border-radius: var(--hos-radius-control, var(--hh-radius-control));
        padding: 10px 6px;
        font-size: var(--qp-body);
        font-weight: 600;
        background: var(--hos-slate-warm, var(--hh-card));
        color: var(--hos-text, var(--hh-text));
      }
      .tenders button.active {
        background: color-mix(in srgb, var(--hos-stone-amber, var(--hh-accent)) 16%, transparent);
        border-color: var(--hos-stone-amber, var(--hh-accent));
        color: var(--hos-text, var(--hh-text));
      }
      .tenders button:disabled {
        background: transparent;
        border-color: var(--hh-disabled-line);
        color: var(--hh-disabled-ink);
        cursor: not-allowed;
      }
      input.amt {
        width: 7rem;
        text-align: right;
        font-family: var(--hos-font-mono, var(--hh-font-mono, monospace));
      }
      input.ref {
        flex: 1 1 14rem;
        min-width: 0;
      }
      .figures {
        margin: 8px 0 0;
        padding: 0;
        font-size: var(--qp-caption);
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      .receipt {
        margin-top: 10px;
        border: 1px solid var(--hos-line, var(--hh-line));
        border-left: 3px solid var(--hos-success, var(--hh-success));
        border-radius: var(--hos-radius-control, var(--hh-radius-control));
        padding: 8px 10px;
      }
      .receipt h4 {
        margin: 0 0 6px;
        font-size: var(--qp-body);
        text-transform: uppercase;
        letter-spacing: 0.09em;
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      .receipt dl {
        margin: 0;
        display: grid;
        grid-template-columns: auto 1fr;
        gap: 3px 16px;
        font-size: var(--qp-body);
        /* A receipt reads down a column, not across the whole panel: the rows keep a
           measure, so a value sits beside its label instead of 1100px away from it. */
        max-width: 24rem;
      }
      .receipt dt {
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      .receipt dd {
        margin: 0;
        font-family: var(--hos-font-mono, var(--hh-font-mono, monospace));
        text-align: right;
      }
      .note {
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
        margin: 6px 0 0;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("quoteId") || changed.has("client")) {
      // Everything a person left on the last quote stays with that quote: the
      // figures typed, the receipt, the unknown outcome, and the key (a
      // different quote is a different payment).
      this.error = "";
      this.notice = "";
      this.status = "";
      this.family = [];
      this.loaded = false;
      this.readError = "";
      this.amountRaw = "";
      this.reference = "";
      this.receipt = null;
      this.unknown = "";
      this.tender = "CASH";
      this.keys.reset();
      this.reload();
    }
  }

  reload(): void {
    void this.load();
  }

  /** One read of the quote's state and its document family: the offer and the figures all come from it. */
  private async load(): Promise<void> {
    const client = this.client;
    const ctx = this.ctx;
    if (client === null || ctx.id === "") {
      this.status = "";
      this.family = [];
      this.loaded = false;
      return;
    }
    // A cancelled context issues no more requests: the panel left the page (sign out) and a read of its is not this session's.
    if (ctx.stale) return;
    this.readError = "";
    const seq = ctx.startRead();
    const answers = await ctx.guardedRead(seq, Promise.all([client.readQuote(ctx.id), client.relatedDocuments(ctx.id, "quote")]));
    if (answers === undefined) return;
    const [view, family] = answers;
    if (!view.ok) {
      this.status = "";
      this.family = [];
      this.loaded = false;
      this.readError = spokenRefusal(quoteErrorMessage(view.status, view.error, "read this quote for its payment"));
      return;
    }
    if (!family.ok) {
      this.family = [];
      this.loaded = false;
      this.readError = "The quote's related documents could not be read, so the payment figures below them are not known.";
      this.status = view.value.status;
      return;
    }
    this.status = view.value.status;
    this.family = family.value;
    this.loaded = true;
    const late = this.lateNotes.get(ctx.id);
    if (late !== undefined) {
      this.lateNotes.delete(ctx.id);
      this.notice = late;
    }
  }

  /** The order rows of the family: what the quote became, as the server holds it. */
  private get orderRows(): RelatedDoc[] {
    return this.family.filter((row) => row.kind === "ORDER");
  }

  /** The server's invoice rows of the family. */
  private get invoiceRows(): RelatedDoc[] {
    return this.family.filter((row) => row.kind === "INVOICE");
  }

  /** The payment offer: the plain payment on a CONVERTED quote, the one action on an ACCEPTED quote. The server decides every other case. */
  private get offered(): boolean {
    return this.status === "CONVERTED" || this.status === "ACCEPTED";
  }

  private async take(): Promise<void> {
    const client = this.client;
    if (client === null || this.busy) return;
    const ctx = this.ctx;
    const cents = typedCents(this.amountRaw);
    if (cents === null) {
      this.error = "The amount is not a number this desk can send: type dollars and cents, like 40.00.";
      this.unknown = "";
      return;
    }
    this.busy = true;
    this.error = "";
    this.notice = "";
    this.unknown = "";
    // The key of THIS attempt: minted once, kept across every retry of it.
    const key = this.keys.current();
    const result = await ctx.guarded(
      client.takeOrderPayment(ctx.id, { method: this.tender, amountCents: cents, reference: this.reference.trim(), idempotencyKey: key }),
      (late) => this.lateAnswer(ctx, late),
    );
    // The quote the person moved to meanwhile owns the screen now: this answer says nothing on it.
    if (result === undefined) return;
    this.busy = false;
    if (!result.ok) {
      if (isUnknownOutcome(result.status)) {
        // No answer the desk can read: the attempt is still open, the key stays, a retry replays.
        this.unknown = unknownOutcomeNote();
        return;
      }
      this.keys.settled(false);
      this.error = paymentRefusalWords(result.status, result.error);
      return;
    }
    this.keys.settled(true);
    this.receipt = result.value;
    this.amountRaw = "";
    this.reference = "";
    // The family changed (an order, an invoice, a payment): every view of this quote re-reads.
    this.announceChanged();
  }

  /**
   * A payment's answer that came back after the person had left the quote it was made on. It is never applied to the
   * quote on screen, but a payment that succeeded did change its quote: the screens are told, so every view of it
   * re-reads, and the person who comes back is told the receipt went with the answer. A refusal is said only on the
   * quote it belongs to, when the person is on it again; on any other quote it says nothing.
   */
  private lateAnswer(ctx: QuoteContext, late: Result<OrderPaymentAnswer>): void {
    if (late.ok) {
      this.lateNotes.set(ctx.id, LATE_PAYMENT_NOTE);
      this.announceChangedFor(ctx);
      return;
    }
    const now = this.ctx;
    if (now.stale || now.id !== ctx.id) return;
    if (isUnknownOutcome(late.status)) this.unknown = unknownOutcomeNote();
    else this.error = paymentRefusalWords(late.status, late.error ?? "");
  }

  // ── delegation ────────────────────────────────────────────────────────────

  protected override onClickAction(action: string, control: HTMLElement): void {
    if (action === "take-payment") void this.take();
    if (action === "tender") {
      const picked = control.getAttribute("data-tender");
      if (picked !== null && (TENDER_METHODS as readonly string[]).includes(picked)) this.tender = picked as TenderMethod;
    }
  }

  protected override onFieldInput(field: string, control: HTMLElement): void {
    const value = (control as HTMLInputElement).value;
    if (field === "amount") this.amountRaw = value;
    if (field === "reference") this.reference = value;
  }

  // ── the uncontrolled fields ───────────────────────────────────────────────

  protected override updated(): void {
    this.syncValue('[data-field="amount"]', this.amountRaw);
    this.syncValue('[data-field="reference"]', this.reference);
  }

  // ── render ────────────────────────────────────────────────────────────────

  /** The server's own figures for what this payment lands on: the quote's agreed total before any order exists, then the order row, the invoice row, never a computed figure. */
  private renderFigures(): TemplateResult | typeof nothing {
    const rows = this.orderRows;
    if (rows.length === 0) {
      // Before any order exists (an accepted quote), the one figure the server holds is the quote's own agreed total.
      const self = this.family.find((row) => row.kind === "QUOTE");
      if (self === undefined) return nothing;
      return html`<p class="figures" data-testid="op-figures">
        ${kindLabel(self.kind)} ${self.number} is ${docStatusWord(self.kind, self.status)} at ${moneyWord(self.totalCents)}, the agreed total
      </p>`;
    }
    const order = rows[0];
    const invoices = this.invoiceRows;
    const parts = [
      `${kindLabel(order.kind)} ${order.number} is ${docStatusWord(order.kind, order.status)} at ${moneyWord(order.totalCents)}`,
      ...invoices.map((inv) => `${kindLabel(inv.kind)} ${inv.number} is ${docStatusWord(inv.kind, inv.status)} at ${moneyWord(inv.totalCents)}`),
    ];
    if (rows.length > 1) parts.push(`the server holds ${rows.length} orders for this quote and will say which one to pay`);
    return html`<p class="figures" data-testid="op-figures">${parts.join(" · ")}</p>`;
  }

  private renderReceipt(): TemplateResult | typeof nothing {
    const r = this.receipt;
    if (r === null) return nothing;
    const rows = receiptRows(
      {
        orderNo: r.orderNo,
        invoiceNo: r.invoiceNo,
        paymentNo: r.paymentNo,
        method: r.method,
        amountCents: r.amountCents,
        balanceCents: r.balanceCents,
        receivedBy: r.receivedBy,
        reference: r.reference,
      },
      this.client?.callerId() ?? "",
    );
    return html`
      <div class="receipt" role="status" data-testid="op-receipt">
        <h4>Receipt</h4>
        <dl>
          ${rows.map((row) => html`<dt>${row.label}</dt><dd data-testid="op-receipt-${row.label.toLowerCase().replace(/[^a-z0-9]+/g, "-")}">${row.value}</dd>`)}
        </dl>
        ${r.converted ? html`<p class="note" data-testid="op-converted-note">${convertedNote()}</p>` : nothing}
        ${r.replayed ? html`<p class="note" data-testid="op-replayed-note">${replayedNote()}</p>` : nothing}
      </div>
    `;
  }

  override render() {
    if (this.quoteId === "" || !this.offered) return nothing;
    return html`
      <fieldset data-testid="op-box">
        <legend>Take payment at the counter</legend>
        ${this.readError !== "" ? html`<p class="err" role="alert" data-testid="op-unread">${this.readError}</p>` : nothing}
        <p class="sub">
          ${this.status === "ACCEPTED"
            ? "This quote is accepted. For a customer with no credit account this one action converts the quote, raises the invoice and takes the payment in full; the server decides, and its refusal names what to do."
            : "Payment on the order this quote became. The server holds the figures and decides the amount rules; its refusal names what to do."}
        </p>
        ${this.renderFigures()}
        <div class="tenders" role="group" aria-label="Payment method" data-testid="op-tenders">
          ${TENDER_METHODS.map(
            (m) => html`<button
              type="button"
              class="${this.tender === m ? "active" : ""}"
              aria-pressed=${this.tender === m ? "true" : "false"}
              data-action="tender"
              data-tender=${m}
              ?disabled=${this.busy}
            >
              ${m}
            </button>`,
          )}
        </div>
        <div class="row">
          <label class="flush" for="op-amount">Amount</label>
          <input id="op-amount" class="amt" type="text" inputmode="decimal" placeholder="0.00" data-field="amount" ?disabled=${this.busy} />
          <label for="op-ref">Reference</label>
          <input id="op-ref" class="ref" type="text" placeholder="Cheque number, EFT trace or terminal reference (may be empty for cash)" data-field="reference" ?disabled=${this.busy} />
          <button type="button" class="primary" data-action="take-payment" data-testid="op-take" ?disabled=${this.busy}>
            ${this.busy ? "Taking payment..." : "Take payment"}
          </button>
        </div>
        ${this.unknown !== "" ? html`<p class="err" role="status" data-testid="op-unknown">${this.unknown}</p>` : nothing}
        ${this.error !== "" ? html`<p class="err" role="alert" data-testid="op-error">${this.error}</p>` : nothing}
        ${this.notice !== "" ? html`<p class="ok" role="status" data-testid="op-notice">${this.notice}</p>` : nothing}
        ${this.renderReceipt()}
      </fieldset>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-order-payment": QdOrderPayment;
  }
}
