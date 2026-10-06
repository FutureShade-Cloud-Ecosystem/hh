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
//   the next deliberate payment mints a fresh key.
// - THE ATTEMPT BELONGS TO ITS QUOTE and is kept across quote switches within
//   the session (AttemptBook, keyed by quote id): a person who leaves a quote
//   with a payment out, or with its outcome not known, and comes back finds
//   the same attempt, the same figures and the same note, the button held
//   while the payment is still out, and the SAME key on the retry. A late
//   answer for a quote the person left is recorded against that quote's
//   attempt and never shown on another quote. Sign out (or another login)
//   ends the session and every kept attempt with it.
// - The answer is shown as the receipt, every figure the SERVER answered:
//   the invoice number, the payment number, the tender, the amount, the
//   balance, who received it and the reference as stored. Never an invented
//   figure: the panel computes nothing.
// - A refusal is shown in the server's words (403 role or branch, 400
//   amounts, 409 including an order already invoiced another route, whose
//   wording names who can record the payment).
// - An UNKNOWN outcome (no status, a 5xx, an unreadable body) says so
//   plainly and tells the person that trying again with the same amount and
//   method is safe: the same key replays the first result. While the outcome
//   is not known the form's fields are locked to the attempt's own figures,
//   so a retry cannot silently change what is sent.
// - The cash customer's one action: the same door converts, invoices and
//   takes the payment in full. The contract gives this desk no signal in the
//   quote view or the family read that says a conversion is pending, so the
//   panel offers the action on an ACCEPTED quote (and the plain payment on a
//   CONVERTED one) and lets the SERVER refuse, showing its words: the app
//   decides nothing by customer type or by role.
//
// Every read and write goes through the quote context: an answer that lands
// after the person left the quote is never applied to the quote now open (a
// late success is recorded for ITS quote and announced, so every view of it
// re-reads; a late refusal is said only on the quote it belongs to).
import { css, html, nothing, type TemplateResult } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { moneyWord, quoteErrorMessage } from "./_quote-edit.js";
import type { RelatedDoc } from "./_related-docs.js";
import {
  AttemptBook,
  TENDER_METHODS,
  convertedNote,
  figuresLine,
  isDifferentPaymentRefusal,
  isUnknownOutcome,
  keepLeftoverAttempt,
  leftoverAttemptNote,
  leftoverTakenNote,
  parseTypedAmount,
  paymentRefusalWords,
  paymentTargetBalance,
  receiptRows,
  replayedNote,
  takeLeftoverAttempt,
  tenderWord,
  unknownOutcomeNote,
  type PaymentAttempt,
  type TenderMethod,
} from "./_order-payment.js";
import type { OrderPaymentAnswer } from "./qd-client.js";
import { SIGN_OUT_EVENT, UNSENT_WAIT_MS, waitUpTo, type SignOutDetail } from "./quote-context.js";
import type { QuoteContext } from "./quote-context.js";
import type { Result } from "@hardscapehouse/api-client";
import { spokenRefusal } from "./_quote-workarea.js";

/** Said on return when a payment was taken on this quote while the person was away: its receipt is below, with the figures the server answered. */
const LATE_PAYMENT_NOTE = "A payment was taken on this quote while you were away. Its receipt is below, with the figures the server answered.";

/**
 * The tender glyphs, the till's own icon discipline: inline currentColor SVG on the 24x24 grid,
 * 2px stroke, round caps and joins, aria-hidden (the tile's label already names it), zero new
 * colours. Cash and Card are the till's own tender art verbatim; Cheque and EFT are drawn in the
 * same discipline for the two tenders the till's payment step does not carry.
 */
const TENDER_GLYPH: Record<TenderMethod, TemplateResult> = {
  CASH: html`<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
    <rect x="2.5" y="6.5" width="19" height="11" rx="1.5"></rect>
    <circle cx="12" cy="12" r="2.6"></circle>
    <path d="M5.5 9.5v0M18.5 14.5v0"></path>
  </svg>`,
  CARD: html`<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
    <rect x="2.5" y="5.5" width="19" height="13" rx="2"></rect>
    <path d="M2.5 9.5h19M6 15h4"></path>
  </svg>`,
  CHEQUE: html`<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <rect x="3" y="5" width="18" height="14" rx="2"></rect>
    <path d="M7 10h6M7 13h3M13 16l1.4-2 1.4 1.6L17.5 13"></path>
  </svg>`,
  EFT: html`<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M4 8h13M13.5 4.5 17 8l-3.5 3.5M20 16H7M10.5 12.5 7 16l3.5 3.5"></path>
  </svg>`,
};

/** What one quote's payment state is when the person is not on it: everything a return must find again. */
interface QuotePayment {
  tender: TenderMethod;
  amountRaw: string;
  reference: string;
  /** The receipt of the payment the server took (or replayed) on this quote. */
  receipt: OrderPaymentAnswer | null;
  /** True when the receipt on screen is of an EARLIER payment, not of the attempt now open. */
  receiptEarlier: boolean;
  /** The server's words of the last definite refusal on this quote. */
  refusal: string;
  /** The quote's kept payment attempt, null when none is open. */
  attempt: PaymentAttempt | null;
}

@customElement("qd-order-payment")
export class QdOrderPayment extends PanelBase {
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
  /** True when the receipt on screen is of an EARLIER payment, so it never reads as the result of a refusal under it. */
  @state() private receiptEarlier = false;
  /** The unknown outcome note: set when no answer the desk can read came back; a retry keeps the same key. */
  @state() private unknown = "";
  /** The kept attempt of the quote now open, mirrored from the book for render. */
  @state() private attempt: PaymentAttempt | null = null;

  /** One payment attempt per quote, kept across switches: the idempotency key's whole lifecycle. */
  private readonly book = new AttemptBook();

  /** Each quote's payment state as the person left it, so a return finds it again. */
  private readonly byQuote = new Map<string, QuotePayment>();

  /** What a late write changed that the screen cannot show, said to the person who comes back, by quote. */
  private readonly lateNotes = new Map<string, string>();

  /** The POST still out for a quote, by quote id: what a sign out waits on, bounded. */
  private readonly posting = new Map<string, Promise<Result<OrderPaymentAnswer>>>();

  /** The quote numbers of the quotes this session read, for the entry page's words at sign out. */
  private readonly quoteNos = new Map<string, string>();

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
      /* The till's own tile language (its payment step's paytiles): a 2x2 grid of icon tiles,
         an unselected tile the card itself with a grey border, the selected tile the amber edge
         and the amber tint over the deep card, normal ink, never a fade and never a second
         accent. Colour is the state. */
      .tenders {
        display: grid;
        grid-template-columns: 1fr 1fr;
        gap: 8px;
        margin-top: 8px;
      }
      .tenders button {
        display: flex;
        align-items: center;
        justify-content: center;
        gap: 8px;
        min-height: 38px;
        border: 1px solid var(--hos-line, var(--hh-line));
        border-radius: var(--hos-radius-control, var(--hh-radius-control));
        padding: 10px 8px;
        font-size: var(--qp-body);
        font-weight: 600;
        background: var(--hos-deep-earth, var(--hh-page));
        color: var(--hos-text, var(--hh-text));
        cursor: pointer;
      }
      /* The drawn glyph takes the tile's own ink in every state (the till's tender icon
         discipline: aria-hidden art, zero new colours). */
      .tenders button svg {
        flex: none;
        opacity: 0.85;
      }
      .tenders button:hover:not([aria-disabled="true"]) {
        border-color: var(--hos-stone-amber, var(--hh-accent));
      }
      .tenders button.active {
        background: color-mix(in srgb, var(--hos-stone-amber, var(--hh-accent)) 10%, var(--hos-deep-earth, var(--hh-page)));
        border-color: var(--hos-stone-amber, var(--hh-accent));
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
      }
      /* While a payment is out (or its key is held) the choice is not offered: the tiles say so
         with the greyed look, EXCEPT the chosen one, whose selection is a fact being carried,
         not a control being offered: it keeps its ink so the person can see what is in flight. */
      .tenders button[aria-disabled="true"]:not(.active) {
        background: transparent;
        border-color: var(--hh-disabled-line);
        color: var(--hh-disabled-ink);
        cursor: not-allowed;
      }
      /* Full height controls on the till's measure: the amount and reference boxes and the Take
         payment action are never smaller than a tile, and the inputs carry the control radius
         (the shared sheet's own), so the primary action is never the smallest control. */
      .payrow input,
      .payrow .primary {
        min-height: 38px;
      }
      .payrow .primary {
        padding: 10px 18px;
      }
      @media (max-width: 1100px) {
        .tenders button,
        .payrow input,
        .payrow .primary {
          min-height: 44px;
        }
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
      .refcount {
        font-size: var(--qp-caption);
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      /* The paid state says what it is in the success ink; it replaces the form, so nothing
         offers a payment nothing is owed for. */
      .paid {
        margin: 8px 0 0;
        font-size: var(--qp-body);
        color: var(--hos-success, var(--hh-success));
      }
      .figures {
        margin: 8px 0 0;
        padding: 0;
        font-size: var(--qp-caption);
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      .preview {
        font-size: var(--qp-caption);
        color: var(--hos-text-dim, var(--hh-text-dim));
        white-space: nowrap;
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

  /** True while the form's figures must not change: a payment is out, or its outcome is not known, or its key is held. */
  private get fieldsLocked(): boolean {
    const phase = this.attempt?.phase;
    return this.busy || phase === "not-known" || phase === "held";
  }

  /**
   * The parsed figure beside the Take payment button, said before the button is pressed so the
   * amount that will leave the desk is on the screen first: the receipt's own money word, the
   * tender's own counter word. While an open attempt holds the form, the preview is the
   * attempt's own figures, the ones a retry will send.
   */
  private get amountPreview(): string {
    const held = this.attempt;
    if (held !== null && (held.phase === "not-known" || held.phase === "held")) {
      return `Taking ${moneyWord(held.amountCents)} by ${tenderWord(held.tender)}`;
    }
    if (this.busy || this.amountRaw.trim() === "") return "";
    const parsed = parseTypedAmount(this.amountRaw);
    return parsed.cents === null ? "" : `Taking ${moneyWord(parsed.cents)} by ${tenderWord(this.tender)}`;
  }

  /** True while an attempt is pending or its outcome is not known: what it may still change is not decided. */
  private get attemptOpen(): boolean {
    const phase = this.attempt?.phase;
    return phase === "in-flight" || phase === "not-known";
  }

  /**
   * True when the invoice is read as owing nothing and every attempt on the order is known: none
   * is out or not known (a refused attempt is decided, and a held key is decided too). The form
   * then gives way to a Paid in full state.
   */
  private get paidInFull(): boolean {
    return this.loaded && this.readError === "" && this.paymentBalance === 0 && !this.attemptOpen;
  }

  /**
   * True when the invoice is read as owing nothing but an attempt is pending or not known: the
   * paid state is not said beside an open attempt, because the attempt may still change the
   * invoice. The panel says the owed nothing with the attempt's status and keeps the retry, which
   * asks the server for that payment's receipt by replay.
   */
  private get paidWhileAttemptOpen(): boolean {
    return !this.paidInFull && this.loaded && this.readError === "" && this.paymentBalance === 0 && this.attemptOpen;
  }

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("client")) {
      // A new client is a new session: no payment, key or receipt of the last one is this one's.
      // The attempts a sign out kept are NOT cleared: the next session meets them as known
      // attempts, through restore().
      this.book.clearAll();
      this.byQuote.clear();
      this.lateNotes.clear();
      this.posting.clear();
      this.quoteNos.clear();
    }
    if (changed.has("quoteId") || changed.has("client")) {
      const left = changed.has("quoteId") ? (changed.get("quoteId") as string | undefined) : undefined;
      // What the person typed, the receipt, the refusal and the open attempt stay with the
      // quote they belong to; the read state (status, family) is re-read for the quote now open.
      if (left !== undefined && left !== "") this.keep(left);
      this.error = "";
      this.notice = "";
      this.status = "";
      this.family = [];
      this.loaded = false;
      this.readError = "";
      this.unknown = "";
      this.restore(this.quoteId);
      this.reload();
    }
  }

  /** The quote's payment record, created empty on first touch. */
  private record(id: string): QuotePayment {
    let rec = this.byQuote.get(id);
    if (rec === undefined) {
      rec = { tender: "CASH", amountRaw: "", reference: "", receipt: null, receiptEarlier: false, refusal: "", attempt: null };
      this.byQuote.set(id, rec);
    }
    return rec;
  }

  /** Fold the live mirrors into the quote's record, so a return finds them. */
  private keep(id: string): void {
    this.record(id).tender = this.tender;
    this.record(id).amountRaw = this.amountRaw;
    this.record(id).reference = this.reference;
    this.record(id).receipt = this.receipt;
    this.record(id).receiptEarlier = this.receiptEarlier;
    this.record(id).refusal = this.error;
    this.record(id).attempt = this.attempt;
  }

  /** Put the quote's record on the screen, attempt first: a payment still out holds the button. */
  private restore(id: string): void {
    const rec = id === "" ? undefined : this.byQuote.get(id);
    this.tender = rec?.tender ?? "CASH";
    this.amountRaw = rec?.amountRaw ?? "";
    this.reference = rec?.reference ?? "";
    this.receipt = rec?.receipt ?? null;
    this.receiptEarlier = rec?.receiptEarlier ?? false;
    this.error = rec?.refusal ?? "";
    this.attempt = rec?.attempt ?? null;
    // An attempt a previous session left on this quote at sign out rejoins the book here: the same
    // key, so a retry replays the server's first outcome instead of posting a second payment. Its
    // own figures come with it, so the fields are locked to what was actually sent.
    if (this.attempt === null && id !== "") {
      const leftover = takeLeftoverAttempt(id);
      if (leftover !== null) {
        this.book.restore(id, leftover);
        this.attempt = leftover;
        const rec = this.record(id);
        rec.attempt = leftover;
        rec.tender = leftover.tender;
        rec.amountRaw = leftover.amountRaw;
        rec.reference = leftover.reference;
        this.tender = leftover.tender;
        this.amountRaw = leftover.amountRaw;
        this.reference = leftover.reference;
      }
    }
    this.busy = this.attempt?.phase === "in-flight";
    this.unknown = this.attempt?.phase === "not-known" ? unknownOutcomeNote() : "";
  }

  /** Put a quote's record back on the screen after its late answer landed (the person is on it again). */
  private refreshLive(id: string): void {
    const rec = this.record(id);
    this.attempt = rec.attempt;
    this.receipt = rec.receipt;
    this.receiptEarlier = rec.receiptEarlier;
    this.error = rec.refusal;
    this.amountRaw = rec.amountRaw;
    this.reference = rec.reference;
    this.tender = rec.tender;
    this.unknown = rec.attempt?.phase === "not-known" ? unknownOutcomeNote() : "";
    this.busy = rec.attempt?.phase === "in-flight";
  }

  reload(): void {
    void this.load();
  }

  /**
   * One read of the quote's state and its document family: the offer and the figures all come from
   * it. True only when THIS read landed as the newest read of the quote still open: a read that was
   * superseded while it was out, or a panel that left the page, answers false, so a caller that
   * unlocks something on the back of the read (startNew) keeps it locked.
   */
  private async load(): Promise<boolean> {
    const client = this.client;
    const ctx = this.ctx;
    if (client === null || ctx.id === "") {
      this.status = "";
      this.family = [];
      this.loaded = false;
      return false;
    }
    // A cancelled context issues no more requests: the panel left the page (sign out) and a read of its is not this session's.
    if (ctx.stale) return false;
    this.readError = "";
    const seq = ctx.startRead();
    const answers = await ctx.guardedRead(seq, Promise.all([client.readQuote(ctx.id), client.relatedDocuments(ctx.id, "quote")]));
    if (answers === undefined) return false;
    const [view, family] = answers;
    if (!view.ok) {
      this.status = "";
      this.family = [];
      this.loaded = false;
      this.readError = spokenRefusal(quoteErrorMessage(view.status, view.error, "read this quote for its payment"));
      return false;
    }
    if (!family.ok) {
      this.family = [];
      this.loaded = false;
      this.readError = "The quote's related documents could not be read, so the payment figures below them are not known.";
      this.status = view.value.status;
      return false;
    }
    this.status = view.value.status;
    this.quoteNos.set(ctx.id, view.value.quote_no);
    this.family = family.value;
    this.loaded = true;
    const late = this.lateNotes.get(ctx.id);
    if (late !== undefined) {
      this.lateNotes.delete(ctx.id);
      this.notice = late;
    }
    return true;
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

  /** The order the family read names as the quote's own, "" when there is none (or more than one). */
  private get familyOrderId(): string {
    const rows = this.orderRows;
    return rows.length === 1 ? rows[0].id : "";
  }

  /**
   * The balance a payment on the quote now open is bounded by, as the family read and the last
   * receipt carry it (paymentTargetBalance holds the rule). The reads that carry a balance
   * figure per invoice are the money desk's register doors, not this desk's, so this is derived
   * from what the family read carries and never by arithmetic over payments this side summed.
   */
  private get paymentBalance(): number | null {
    const rows = this.orderRows;
    const invoices = this.invoiceRows;
    if (invoices.length > 1) return null;
    const invoice = invoices.length === 1 ? invoices[0] : null;
    if (rows.length > 1) return null;
    const agreed = rows.length === 1 ? rows[0] : (this.family.find((row) => row.kind === "QUOTE") ?? null);
    return paymentTargetBalance(agreed, invoice, this.receipt);
  }

  private async take(): Promise<void> {
    const client = this.client;
    if (client === null || this.busy) return;
    const ctx = this.ctx;
    // A press at a new payment retires any receipt already on screen: from here it is the
    // receipt of an EARLIER payment, and it must never read as the result of this attempt.
    if (this.receipt !== null && !this.receiptEarlier) {
      this.receiptEarlier = true;
      this.record(ctx.id).receiptEarlier = true;
    }
    const kept = this.attempt;
    let key: string;
    let cents: number;
    let tender: TenderMethod;
    let reference: string;
    if (kept !== null && (kept.phase === "not-known" || kept.phase === "held")) {
      // The retry of an open attempt: exactly the figures it was made with, and its key, so
      // the server replays the first result and nothing is posted a second time. The retry is
      // marked in flight BEFORE it is sent, so a person who leaves and comes back while it is
      // out finds the attempt still out, its button held, and no third POST can be opened.
      this.book.mark(ctx.id, "in-flight");
      const inflight = this.book.attempt(ctx.id);
      if (inflight !== null) {
        this.attempt = inflight;
        this.record(ctx.id).attempt = inflight;
      }
      key = kept.key;
      cents = kept.amountCents;
      tender = kept.tender;
      reference = kept.reference;
    } else {
      const parsed = parseTypedAmount(this.amountRaw);
      if (parsed.cents === null) {
        this.error = parsed.problem;
        this.unknown = "";
        return;
      }
      cents = parsed.cents;
      // The desk's own bound, in the desk's own money word, before anything is sent: the server
      // still judges every amount, and its own refusal is shown in its own words.
      const balance = this.paymentBalance;
      if (balance !== null && cents > balance) {
        this.error = `The payment is more than the balance of ${moneyWord(balance)}`;
        this.unknown = "";
        return;
      }
      const opened = this.book.open(ctx.id, {
        amountRaw: this.amountRaw,
        amountCents: cents,
        tender: this.tender,
        reference: this.reference.trim(),
        orderId: this.familyOrderId,
      });
      key = opened.key;
      this.attempt = opened;
      this.record(ctx.id).attempt = opened;
      tender = opened.tender;
      reference = opened.reference;
    }
    this.busy = true;
    this.error = "";
    this.notice = "";
    this.unknown = "";
    const posting = client.takeOrderPayment(ctx.id, { method: tender, amountCents: cents, reference, idempotencyKey: key });
    this.posting.set(ctx.id, posting);
    const result = await ctx.guarded(posting, (late) => this.lateAnswer(ctx, late));
    this.posting.delete(ctx.id);
    // The quote the person moved to meanwhile owns the screen now: this answer says nothing on it.
    if (result === undefined) return;
    this.busy = false;
    if (result.ok) {
      this.taken(ctx.id, result.value);
      return;
    }
    if (isUnknownOutcome(result.status)) {
      // No answer the desk can read: the attempt is still open, the key stays, a retry replays.
      this.marked(ctx.id, "not-known", "");
      return;
    }
    const words = paymentRefusalWords(result.status, result.error);
    this.marked(ctx.id, isDifferentPaymentRefusal(result.error) ? "held" : "kept", words);
    // A definite refusal is evidence the server's state moved under the desk (a payment or a
    // credit landed elsewhere, a balance changed): the family is read again, so the figures say
    // what the server said at the latest read, and an invoice that is no longer owed leaves the
    // form. The read is the panel's guarded one (its own row), never applied to another quote.
    void this.load();
  }

  /** A payment the server took: the attempt is complete, the receipt is the answer, and the views re-read. */
  private taken(id: string, value: OrderPaymentAnswer): void {
    this.book.clear(id);
    const rec = this.record(id);
    rec.attempt = null;
    rec.receipt = value;
    rec.receiptEarlier = false;
    rec.refusal = "";
    rec.amountRaw = "";
    rec.reference = "";
    this.attempt = null;
    this.receipt = value;
    this.receiptEarlier = false;
    this.error = "";
    this.unknown = "";
    this.amountRaw = "";
    this.reference = "";
    // The family changed (an order, an invoice, a payment): every view of this quote re-reads.
    this.announceChanged();
  }

  /** A definite answer that was not a taken payment: the attempt's new phase and, when the server spoke, its words. */
  private marked(id: string, phase: "not-known" | "held" | "kept", refusal: string): void {
    this.book.mark(id, phase);
    const rec = this.record(id);
    rec.attempt = this.book.attempt(id);
    if (refusal !== "") rec.refusal = refusal;
    if (phase === "not-known") rec.refusal = "";
    this.attempt = rec.attempt;
    if (refusal !== "") this.error = refusal;
    if (phase === "not-known") {
      this.error = "";
      this.unknown = unknownOutcomeNote();
    } else {
      this.unknown = "";
    }
  }

  /**
   * A payment's answer that came back after the person had left the quote it was made on. It is never
   * applied to the quote on screen: it is recorded against ITS quote's attempt (the receipt, the
   * refusal's words, the not-known note), the screens are told when a payment landed so every view of
   * it re-reads, and the person who comes back finds the answer there. On any other quote it says
   * nothing.
   */
  private lateAnswer(ctx: QuoteContext, late: Result<OrderPaymentAnswer>): void {
    if (late.ok) {
      this.book.clear(ctx.id);
      const rec = this.record(ctx.id);
      rec.attempt = null;
      rec.receipt = late.value;
      rec.receiptEarlier = false;
      rec.refusal = "";
      rec.amountRaw = "";
      rec.reference = "";
      this.lateNotes.set(ctx.id, LATE_PAYMENT_NOTE);
      this.announceChangedFor(ctx);
    } else if (isUnknownOutcome(late.status)) {
      this.book.mark(ctx.id, "not-known");
      const rec = this.record(ctx.id);
      rec.attempt = this.book.attempt(ctx.id);
      rec.refusal = "";
    } else {
      const phase = isDifferentPaymentRefusal(late.error ?? "") ? "held" : "kept";
      this.book.mark(ctx.id, phase);
      const rec = this.record(ctx.id);
      rec.attempt = this.book.attempt(ctx.id);
      rec.refusal = paymentRefusalWords(late.status, late.error ?? "");
    }
    // When the quote the answer belongs to is the one open now, the screen says it at once.
    if (this.quoteId === ctx.id) this.refreshLive(ctx.id);
  }

  // ── sign out: the payment still out is waited for, bounded, and never dropped ──────────────

  override connectedCallback(): void {
    super.connectedCallback();
    window.addEventListener(SIGN_OUT_EVENT, this.onSignOut);
  }

  override disconnectedCallback(): void {
    super.disconnectedCallback();
    window.removeEventListener(SIGN_OUT_EVENT, this.onSignOut);
  }

  /** The quote's number as the session read it, or its id when no read named it. */
  private quoteNoOf(quoteId: string): string {
    return this.quoteNos.get(quoteId) ?? quoteId;
  }

  /** Sign out: the payments still out join the host's bounded waits, and what they leave is said on the entry page. */
  private readonly onSignOut = (event: Event): void => {
    const detail = (event as CustomEvent<SignOutDetail>).detail;
    detail.waits.push(this.flushAtSignOut(detail));
  };

  /**
   * Wait, bounded the way the layout store's flush is bounded, for every payment still out; keep
   * what no answer decided for the next sign in; say on the entry page what was taken and what is
   * kept. A payment whose answer lands inside the bound was heard before the session ended; one
   * that does not answer is kept as a known attempt with its key, so the next sign in's retry
   * replays the server's first outcome instead of posting a second payment.
   */
  private async flushAtSignOut(detail: SignOutDetail): Promise<void> {
    const outs = this.book.out();
    const waits = outs.map((row) => this.waitOneAtSignOut(detail, row.quoteId, row.attempt));
    await Promise.all(waits);
    for (const { quoteId, attempt } of this.book.unsettled()) {
      keepLeftoverAttempt(quoteId, attempt);
      detail.notes = [...(detail.notes ?? []), leftoverAttemptNote(this.quoteNoOf(quoteId), attempt)];
    }
  }

  /**
   * One payment still out, waited for inside the bound. Heard inside it: the entry page says it was
   * taken, or a definite refusal ends the attempt (a held one is kept, with its words). Not heard:
   * the attempt stays as it is, and the keep below hands it to the next sign in.
   */
  private async waitOneAtSignOut(detail: SignOutDetail, quoteId: string, attempt: PaymentAttempt): Promise<void> {
    const posting = this.posting.get(quoteId);
    if (posting === undefined) {
      this.book.mark(quoteId, "not-known");
      return;
    }
    const finished = await waitUpTo(UNSENT_WAIT_MS, posting);
    if (!finished) {
      // The bound ran out with the payment still out: the session ends not knowing, which is the
      // not-known phase for whoever signs in next (a retry there replays; it never posts twice).
      this.book.mark(quoteId, "not-known");
      return;
    }
    const answer = await posting;
    if (answer.ok) {
      this.book.clear(quoteId);
      detail.notes = [...(detail.notes ?? []), leftoverTakenNote(this.quoteNoOf(quoteId), answer.value)];
      return;
    }
    if (isUnknownOutcome(answer.status)) {
      this.book.mark(quoteId, "not-known");
      return;
    }
    if (isDifferentPaymentRefusal(answer.error ?? "")) {
      this.book.mark(quoteId, "held");
      return;
    }
    // A plain refusal decided the attempt: nothing is owed to it, and the session ends with it.
    this.book.clear(quoteId);
  }

  /**
   * The deliberate way out of a held attempt (the key took a different payment): a NEW payment
   * on a fresh key, offered only after the quote and its family are read again, so what landed
   * under the old key is on the screen before the form unlocks. A read that fails keeps the
   * attempt held: the form never unlocks over a family it could not read.
   */
  private async startNew(): Promise<void> {
    if (this.busy || this.attempt?.phase !== "held") return;
    const ctx = this.ctx;
    if (ctx.id === "" || ctx.stale) return;
    // The way out is a read the desk heard: a read superseded while it was out (the fan out of an
    // edit, or another read of the quote) is not the server's truth about this attempt, so the
    // attempt stays held and the way out is offered again over a read that lands.
    const landed = await this.load();
    if (!landed || this.quoteId !== ctx.id) return;
    if (this.readError !== "") return;
    this.book.clear(ctx.id);
    const rec = this.record(ctx.id);
    rec.attempt = null;
    rec.refusal = "";
    this.attempt = null;
    this.error = "";
    this.unknown = "";
  }

  // ── delegation ────────────────────────────────────────────────────────────

  protected override onClickAction(action: string, control: HTMLElement): void {
    if (action === "take-payment") void this.take();
    if (action === "start-new") void this.startNew();
    if (action === "retry-read") this.reload();
    if (action === "tender") {
      if (this.fieldsLocked) return;
      const picked = control.getAttribute("data-tender");
      if (picked !== null && (TENDER_METHODS as readonly string[]).includes(picked)) this.tender = picked as TenderMethod;
    }
  }

  protected override onFieldInput(field: string, control: HTMLElement): void {
    if (this.fieldsLocked) return;
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

  /** The server's own figures for what this payment lands on: the quote's agreed total before any order exists, then the order row and the invoice row, with the balance the reads carry (figuresLine holds the wording; no figure is computed here). */
  private renderFigures(): TemplateResult | typeof nothing {
    const rows = this.orderRows;
    const quote = this.family.find((row) => row.kind === "QUOTE") ?? null;
    const line = figuresLine(quote, rows.length === 1 ? rows[0] : null, this.invoiceRows, this.paymentBalance, rows.length);
    if (line === "") return nothing;
    return html`<p class="figures" data-testid="op-figures">${line}</p>`;
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
        <h4>${this.receiptEarlier ? "Receipt of an earlier payment" : "Receipt"}</h4>
        ${this.receiptEarlier
          ? html`<p class="note" data-testid="op-receipt-earlier">This receipt is of an earlier payment on this quote; it is not the result of the attempt above it.</p>`
          : nothing}
        <dl>
          ${rows.map((row) => html`<dt>${row.label}</dt><dd data-testid="op-receipt-${row.label.toLowerCase().replace(/[^a-z0-9]+/g, "-")}">${row.value}</dd>`)}
        </dl>
        ${r.converted ? html`<p class="note" data-testid="op-converted-note">${convertedNote()}</p>` : nothing}
        ${r.replayed ? html`<p class="note" data-testid="op-replayed-note">${replayedNote()}</p>` : nothing}
      </div>
    `;
  }

  override render() {
    if (this.quoteId === "") return nothing;
    // An unread family (or an unread quote) shows the read failure and the way to try again:
    // the payment controls never render over a guess, and neither do the figures.
    if (this.readError !== "") {
      return html`
        <fieldset data-testid="op-box">
          <legend>Take payment at the counter</legend>
          <p class="err" role="alert" data-testid="op-unread">${this.readError}</p>
          <div class="row">
            <button type="button" data-action="retry-read" data-testid="op-retry-read">Try the read again</button>
          </div>
        </fieldset>
      `;
    }
    if (!this.offered) return nothing;
    return html`
      <fieldset data-testid="op-box">
        <legend>Take payment at the counter</legend>
        <p class="sub">
          ${this.status === "ACCEPTED"
            ? "This quote is accepted. For a customer with no credit account this one action converts the quote, raises the invoice and takes the payment in full; the server decides, and its refusal names what to do."
            : "Payment on the order this quote became. The server holds the figures and decides the amount rules; its refusal names what to do."}
        </p>
        ${this.renderFigures()}
        ${this.paidInFull
          ? html`<p class="paid" data-testid="op-paid">
              Paid in full: nothing is owed on this invoice, so no payment is taken here${this.receipt !== null ? ". The receipt below is the payment that settled it." : "."}
            </p>`
          : nothing}
        ${this.paidWhileAttemptOpen
          ? html`<p class="paid" data-testid="op-paid-unknown">
              The invoice is Paid: nothing is owed on it. The outcome of the payment above is not known, so the desk still holds its attempt: the retry below asks the server for that payment's receipt, and nothing is posted a second time.
            </p>`
          : nothing}
        ${this.paidInFull
          ? nothing
          : html`
            <div class="tenders" role="group" aria-label="Payment method" data-testid="op-tenders">
          ${TENDER_METHODS.map(
            (m) => html`<button
              type="button"
              class="${this.tender === m ? "active" : ""}"
              aria-pressed=${this.tender === m ? "true" : "false"}
              aria-disabled=${this.fieldsLocked ? "true" : "false"}
              data-action="tender"
              data-tender=${m}
            >
              ${TENDER_GLYPH[m]}
              ${m}
            </button>`,
          )}
        </div>
        <div class="row payrow">
          <label class="flush" for="op-amount">Amount</label>
          <input id="op-amount" class="amt" type="text" inputmode="decimal" placeholder="0.00" data-field="amount" ?disabled=${this.fieldsLocked} />
          <label for="op-ref">Reference</label>
          <input id="op-ref" class="ref" type="text" placeholder="Cheque no, EFT trace or terminal ref" maxlength="80" data-field="reference" ?disabled=${this.fieldsLocked} />
          <span class="refcount" data-testid="op-ref-count">${this.reference.length}/80</span>
          <button type="button" class="primary" data-action="take-payment" data-testid="op-take" ?disabled=${this.busy}>
            ${this.busy ? "Taking payment..." : "Take payment"}
          </button>
          ${this.amountPreview !== "" ? html`<span class="preview" data-testid="op-preview">${this.amountPreview}</span>` : nothing}
            </div>
            ${this.attempt?.phase === "held"
              ? html`<div class="row">
                  <button type="button" data-action="start-new" data-testid="op-start-new">Start a new payment</button>
                  <span class="note">A new payment gets a new key. The documents above are read again first, so what landed under the held key is on the screen before the form unlocks.</span>
                </div>`
              : nothing}
          `}
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
