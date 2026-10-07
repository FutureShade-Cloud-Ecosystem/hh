// The guided Convert to Order surface, #/quotes/<id>/convert (decision log row 993, the row 955
// surfaces rule). Five steps: Review (the lines, the customer, the total and each line's stock at the
// branch), Fulfilment (the server's two, Pickup and Will-call), Customer standing (the terms and the
// server's own credit facts, said before confirming so a refusal is never a surprise), a Payment
// (method, amount, reference, one idempotency key) and Confirm, which ends on the done screen. On
// success the host emits `quote-converted` with { orderId, quoteId }.
//
// Two roads to an order. A customer on credit converts through the plain conversion, and may leave a
// deposit or a payment, taken after the order exists. A Cash On Demand customer (no credit account;
// decision log row 999: the order is made, gets its number, and nothing is released until it is
// paid) is refused by the plain conversion, so the surface goes through the payment door, which
// converts, bills and takes the whole payment as one action: the payment is required, it is the whole
// amount the server holds for the quote, and it is never skippable.
//
// The server holds every rule: the gate, the totals, the stock and who may convert. This surface
// reads those answers and words them; the role wall is the quote.convert_order action of the caller's
// own curation plus the server's own can_convert flag, and both fail closed.
//
// The binding discipline is the app's own (the design system's position gate holds this app's
// templates): no property or event bindings, the host delegates by data-action and data-field, and
// the inputs are uncontrolled and synced in the wiring pass after each render.
import { html, css, nothing, type PropertyValues, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles, chip, select, type ChipTone } from "@hardscapehouse/design-system/src/primitives.js";
import type { AccountStandingRead, QdClient, StandingRead } from "./qd-client.js";
import { QB_ACTIONS, effectiveItems, visibleItems } from "./quote-surface.js";
import { moneyWord, quoteStatusWord, type QuoteView } from "./_quote-edit.js";
import { qtyLabel, spokenRefusal } from "./_quote-workarea.js";
import {
  CONVERT_NOTICE_EVENT,
  CONVERT_STEPS,
  COD_TERMS_TEXT,
  DELIVERY_NOTE,
  FULFILMENTS,
  MAX_REFERENCE,
  PAY_METHODS,
  PAY_METHOD_LABEL,
  QUOTES_ROUTE,
  STEP_LABEL,
  awayCodNotice,
  awayNotice,
  codRefusalWords,
  fulfilmentLabel,
  isCashOnDemand,
  isCodRefusal,
  parsePayAmount,
  paymentKeyFor,
  refusalWords,
  standingOf,
  stepAfter,
  stepBefore,
  stockWords,
  type ConvertStep,
  type Fulfilment,
  type KeyedIntent,
  type LineStockIn,
  type PayMethod,
  type Standing,
} from "./_quote-convert.js";

type Phase = "loading" | "ready" | "no-access" | "not-ready" | "missing" | "failed";


export class QuoteConvert extends HHElement {
  static properties = {
    ...HHElement.properties,
    client: { attribute: false },
    quoteId: { attribute: "quote-id" },
    branchName: { attribute: "branch-name" },
  };

  /** The host wires the validated client before the first load. */
  declare client: QdClient | null;
  declare quoteId: string;
  declare branchName: string;

  static styles = [
    baseStyles,
    css`
      :host {
        display: block;
        padding: var(--hh-s-16);
        color: var(--hh-text);
      }
      .wrap {
        max-width: 960px;
        margin: 0 auto;
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-16);
      }
      h2 {
        margin: 0;
        font-size: 1.25rem;
      }
      h3 {
        margin: 0 0 var(--hh-s-8);
        font-size: 1.05rem;
      }
      p {
        margin: 0;
      }
      .sub {
        color: var(--hh-text-dim);
        font-size: 0.9rem;
      }
      .steps {
        list-style: none;
        margin: 0;
        padding: 0;
        display: flex;
        flex-wrap: wrap;
        gap: var(--hh-s-8);
      }
      .steps li {
        padding: var(--hh-s-4) var(--hh-s-12);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-pill);
        color: var(--hh-text-dim);
        font-size: 0.85rem;
      }
      .steps li[aria-current="step"] {
        border-color: var(--hh-accent-text);
        color: var(--hh-accent-text);
        font-weight: 600;
      }
      .card {
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-card);
        background: var(--hh-card);
        padding: var(--hh-s-16) var(--hh-s-20);
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-12);
      }
      table {
        width: 100%;
        border-collapse: collapse;
      }
      th,
      td {
        text-align: left;
        padding: var(--hh-s-8) var(--hh-s-8);
        border-bottom: 1px solid var(--hh-hairline);
        vertical-align: top;
      }
      th {
        font-size: 0.8rem;
        color: var(--hh-text-dim);
        font-weight: 600;
      }
      td.num,
      th.num {
        text-align: right;
      }
      .warn {
        color: var(--hh-state-watch);
        font-size: 0.85rem;
        margin-top: var(--hh-s-4);
      }
      .ok {
        color: var(--hh-state-good);
      }
      .bad {
        color: var(--hh-danger);
      }
      .tiles {
        display: grid;
        grid-template-columns: repeat(2, minmax(0, 1fr));
        gap: var(--hh-s-12);
      }
      .tile {
        text-align: left;
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-4);
        padding: var(--hh-s-16);
        min-height: 88px;
        background: var(--hh-widget);
        color: var(--hh-text);
        border: 1px solid var(--hh-control-line);
        line-height: 1.3;
      }
      .tile[aria-pressed="true"] {
        border-color: var(--hh-accent-text);
        box-shadow: inset 0 0 0 1px var(--hh-accent-text);
      }
      .tile .name {
        font-weight: 600;
      }
      .tile .hint {
        color: var(--hh-text-dim);
        font-size: 0.85rem;
      }
      .fields {
        display: grid;
        grid-template-columns: repeat(3, minmax(0, 1fr));
        gap: var(--hh-s-12);
      }
      label {
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-4);
        font-size: 0.85rem;
        color: var(--hh-text-dim);
      }
      .nav {
        display: flex;
        gap: var(--hh-s-12);
        justify-content: flex-end;
        flex-wrap: wrap;
      }
      .facts {
        display: grid;
        grid-template-columns: max-content 1fr;
        gap: var(--hh-s-4) var(--hh-s-16);
        margin: 0;
      }
      .facts dt {
        color: var(--hh-text-dim);
      }
      .facts dd {
        margin: 0;
      }
      .notice {
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-control);
        padding: var(--hh-s-12) var(--hh-s-16);
        background: var(--hh-widget);
      }
      .notice.stop {
        border-color: var(--hh-danger);
      }
      .done h2 {
        color: var(--hh-state-good);
      }
    `,
  ];

  #phase: Phase = "loading";
  #message = "";
  #view: QuoteView | null = null;
  #stock = new Map<number, LineStockIn>();
  #stockKnown = false;
  #branchLabel = "";
  #standing: StandingRead | null = null;
  #hold: AccountStandingRead | null = null;
  /** The plain conversion was refused under the COD policy: the server itself said this customer has no credit account. */
  #codLearned = false;
  #step: ConvertStep = "review";
  #fulfilment: Fulfilment = "PICKUP";
  #payMethod: PayMethod = "CASH";
  #payAmountRaw = "";
  #payReference = "";
  /** The payment last planned and the key it was sent under, kept so the same payment keeps its key. */
  #keyed: KeyedIntent | null = null;
  #payError = "";
  #planned: KeyedIntent | null = null;
  #busy = false;
  #refusal = "";
  #done: { orderId: string; orderNo: string } | null = null;
  #paid: { paymentNo: string; amountCents: number; balanceCents: number } | null = null;
  #payNote = "";
  /** Each load carries a number; an answer that comes back for an older one is dropped. */
  #seq = 0;

  constructor() {
    super();
    this.client = null;
    this.quoteId = "";
    this.branchName = "";
  }

  override connectedCallback(): void {
    super.connectedCallback();
    this.renderRoot.addEventListener("click", this.#onClick);
    this.renderRoot.addEventListener("input", this.#onInput);
    this.renderRoot.addEventListener("change", this.#onInput);
  }

  override disconnectedCallback(): void {
    this.renderRoot.removeEventListener("click", this.#onClick);
    this.renderRoot.removeEventListener("input", this.#onInput);
    this.renderRoot.removeEventListener("change", this.#onInput);
    this.#seq += 1;
    super.disconnectedCallback();
  }

  protected override willUpdate(changed: PropertyValues): void {
    if (changed.has("quoteId") || changed.has("client")) {
      if (this.client !== null && this.quoteId !== "") void this.#load();
    }
  }

  protected override updated(): void {
    // The uncontrolled fields take their value from the state after each render.
    this.#sync('[data-field="pay-amount"]', this.#payAmountRaw);
    this.#sync('[data-field="pay-reference"]', this.#payReference);
    this.#sync('[data-field="pay-method"]', this.#payMethod);
  }

  #sync(selector: string, value: string): void {
    const el = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement>(selector);
    if (el !== null && el.value !== value) el.value = value;
  }

  // ── the reads ────────────────────────────────────────────────────────────────

  async #load(): Promise<void> {
    const client = this.client;
    const quoteId = this.quoteId;
    if (client === null || quoteId === "") return;
    const seq = ++this.#seq;
    this.#reset();
    this.#phase = "loading";
    this.requestUpdate();
    const quote = await client.readQuote(quoteId);
    if (seq !== this.#seq) return;
    if (!quote.ok) {
      this.#phase = quote.status === 404 ? "missing" : "failed";
      this.#message = quote.status === 404 ? "That quote could not be found." : spokenRefusal(quote.error) || "The quote could not be read.";
      this.requestUpdate();
      return;
    }
    const view = quote.value;
    // The role wall: the convert action must be in the caller's own curation. An unreadable curation
    // closes the door; the surface never guesses a grant.
    const config = await client.surfaceConfig(view.branch_id);
    if (seq !== this.#seq) return;
    if (!config.ok) {
      this.#phase = "failed";
      this.#message = "Your access to converting quotes could not be checked, so nothing is offered. Try again.";
      this.requestUpdate();
      return;
    }
    const actions = visibleItems(effectiveItems("action", QB_ACTIONS, config.value.roleItems, config.value.userItems));
    if (!actions.some((a) => a.id === "quote.convert_order")) {
      this.#phase = "no-access";
      this.#message = "Your role does not include converting quotes to orders. Ask a manager to convert this quote.";
      this.requestUpdate();
      return;
    }
    this.#view = view;
    if (!view.can_convert) {
      this.#phase = "not-ready";
      this.#message =
        view.status === "CONVERTED"
          ? `Quote ${view.quote_no} is already converted to an order.`
          : `Quote ${view.quote_no} is ${quoteStatusWord(view.status)}, and only an accepted quote can be converted.`;
      this.requestUpdate();
      return;
    }
    this.#phase = "ready";
    this.requestUpdate();
    const [stock, standing, hold] = await Promise.all([
      client.availability(quoteId),
      client.customerStanding(view.customer_id, view.customer_name),
      client.accountStanding(view.branch_id, view.customer_id),
    ]);
    if (seq !== this.#seq) return;
    if (stock.ok) {
      this.#stockKnown = stock.value.seamWired;
      for (const line of stock.value.lines) {
        this.#stock.set(line.line_no, { grade: line.grade, qtyBase: line.qty_base, availableBase: line.branch.available_base });
        if (this.#branchLabel === "") this.#branchLabel = line.branch.branch_name;
      }
    }
    this.#standing = standing.ok ? standing.value : null;
    this.#hold = hold.ok ? hold.value : null;
    // The door takes no fulfilment, so a Cash On Demand order is made as Pickup.
    if (this.#isCod()) this.#fulfilment = "PICKUP";
    this.requestUpdate();
  }

  #reset(): void {
    this.#view = null;
    this.#stock = new Map();
    this.#stockKnown = false;
    this.#branchLabel = "";
    this.#standing = null;
    this.#hold = null;
    this.#codLearned = false;
    this.#step = "review";
    this.#fulfilment = "PICKUP";
    this.#payMethod = "CASH";
    this.#payAmountRaw = "";
    this.#payReference = "";
    this.#keyed = null;
    this.#payError = "";
    this.#planned = null;
    this.#busy = false;
    this.#refusal = "";
    this.#done = null;
    this.#paid = null;
    this.#payNote = "";
  }

  // ── the controls ─────────────────────────────────────────────────────────────

  readonly #onClick = (event: Event): void => {
    const control = event.composedPath().find((node): node is HTMLElement => node instanceof HTMLElement && node.hasAttribute("data-action"));
    if (control === undefined || (control as HTMLButtonElement).disabled === true) return;
    const action = control.getAttribute("data-action");
    if (action === "next") this.#next();
    else if (action === "back") {
      this.#payError = "";
      this.#refusal = "";
      this.#step = stepBefore(this.#step);
      this.requestUpdate();
    } else if (action === "fulfil") {
      const value = control.getAttribute("data-fulfil");
      if (value === "PICKUP" || (value === "WILL_CALL" && !this.#isCod())) {
        this.#fulfilment = value;
        this.requestUpdate();
      }
    } else if (action === "skip-payment" && !this.#isCod()) {
      this.#payAmountRaw = "";
      this.#payReference = "";
      this.#planned = null;
      this.#payError = "";
      this.#step = "confirm";
      this.requestUpdate();
    } else if (action === "confirm") void this.#confirm();
    else if (action === "retry-payment") void this.#takePayment();
    else if (action === "to-quotes") window.location.hash = QUOTES_ROUTE;
  };

  readonly #onInput = (event: Event): void => {
    const control = event.composedPath().find((node): node is HTMLElement => node instanceof HTMLElement && node.hasAttribute("data-field"));
    if (control === undefined) return;
    const field = control.getAttribute("data-field");
    const value = (control as HTMLInputElement | HTMLSelectElement).value;
    if (field === "pay-amount") this.#payAmountRaw = value;
    else if (field === "pay-reference") this.#payReference = value.slice(0, MAX_REFERENCE);
    else if (field === "pay-method" && (PAY_METHODS as readonly string[]).includes(value)) this.#payMethod = value as PayMethod;
  };

  #next(): void {
    if (this.#step === "payment" && !this.#plan()) {
      this.requestUpdate();
      return;
    }
    this.#step = stepAfter(this.#step);
    this.requestUpdate();
  }

  /** Cash On Demand: the server's own rule (no credit account), or the server's refusal of the plain conversion having said so. */
  #isCod(): boolean {
    return this.#codLearned || isCashOnDemand(this.#standing);
  }

  #standingNow(): Standing {
    const read = standingOf(this.#standing, this.#hold);
    if (this.#codLearned && read.terms !== "COD") {
      return {
        terms: "COD",
        termsText: COD_TERMS_TEXT,
        gate: "cod",
        gateText: "The server says this customer has no credit account. The whole payment is taken to create the order, and the server decides.",
      };
    }
    return read;
  }

  /**
   * Validate what the payment step holds. A customer on credit may leave it empty (the step is
   * optional); a Cash On Demand customer pays the whole amount the server holds for the quote, so
   * there is no amount to type and no way to leave it empty.
   */
  #plan(): boolean {
    this.#payError = "";
    let amountCents: number;
    if (this.#isCod()) {
      amountCents = this.#view?.total_cents ?? 0;
      if (amountCents < 1) {
        this.#payError = "The amount due could not be read from the quote, so the payment cannot be taken here.";
        return false;
      }
    } else {
      if (this.#payAmountRaw.trim() === "") {
        this.#planned = null;
        return true;
      }
      const amount = parsePayAmount(this.#payAmountRaw);
      if ("error" in amount) {
        this.#payError = amount.error;
        return false;
      }
      amountCents = amount.cents;
    }
    // The key follows the intended payment: the same one keeps its key, a changed one gets a new key.
    this.#keyed = paymentKeyFor(this.#keyed, { method: this.#payMethod, amountCents, reference: this.#payReference.trim() });
    this.#planned = this.#keyed;
    return true;
  }

  async #confirm(): Promise<void> {
    const client = this.client;
    const view = this.#view;
    const quoteId = this.quoteId;
    if (client === null || view === null || this.#busy) return;
    if (this.#isCod()) return this.#payAndConvert();
    const seq = this.#seq;
    const quoteNo = view.quote_no;
    const planned = this.#planned;
    this.#busy = true;
    this.#refusal = "";
    this.requestUpdate();
    const result = await client.convertWithFulfilment(quoteId, this.#fulfilment);
    // The order exists whatever is on screen now: the quote it was made from is announced either way.
    if (result.ok) {
      this.dispatchEvent(new CustomEvent("quote-converted", { detail: { orderId: result.value.orderId, quoteId }, bubbles: true, composed: true }));
      this.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId }, bubbles: true, composed: true }));
    }
    if (seq !== this.#seq) {
      // The person left while the conversion was out. A payment typed for the order is still taken
      // (their intent stands) and the outcome is said where they are now, never dropped silently.
      if (result.ok && planned !== null) await this.#settleAway(client, quoteId, quoteNo, result.value.orderNo, planned);
      return;
    }
    this.#busy = false;
    if (!result.ok) {
      if (isCodRefusal(result.error)) {
        // The real gate refused the plain conversion under the COD policy: this customer has no
        // credit account, so the order is made by the payment door with the whole payment.
        this.#codLearned = true;
        this.#fulfilment = "PICKUP";
        this.#payError = "";
        this.#step = "payment";
        this.requestUpdate();
        return;
      }
      this.#refusal = refusalWords(result.status, result.error);
      this.requestUpdate();
      return;
    }
    this.#done = result.value;
    this.requestUpdate();
    if (this.#planned !== null) await this.#takePayment();
  }

  /**
   * The Cash On Demand action: the payment door converts, bills and takes the whole payment as one
   * transaction, so the order never exists unpaid. The one planned payment carries the one key, so a
   * try again after a lost answer replays rather than taking the payment twice. Only #confirm calls
   * it, and #confirm has already stopped a second click while one is out.
   */
  async #payAndConvert(): Promise<void> {
    const client = this.client;
    const view = this.#view;
    const planned = this.#planned;
    const quoteId = this.quoteId;
    if (client === null || view === null || planned === null) return;
    const seq = this.#seq;
    const quoteNo = view.quote_no;
    this.#busy = true;
    this.#refusal = "";
    this.requestUpdate();
    const paid = await client.takeOrderPayment(quoteId, {
      method: planned.method,
      amountCents: planned.amountCents,
      reference: planned.reference,
      idempotencyKey: planned.key,
    });
    if (paid.ok) {
      this.dispatchEvent(new CustomEvent("quote-converted", { detail: { orderId: paid.value.orderId, quoteId }, bubbles: true, composed: true }));
      this.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId }, bubbles: true, composed: true }));
    }
    if (seq !== this.#seq) {
      const words = paid.ok ? "" : codRefusalWords(paid.status, paid.error);
      this.#announceAway(quoteId, awayCodNotice({ quoteNo, amountCents: planned.amountCents, paid: paid.ok ? paid.value : null, words }));
      return;
    }
    this.#busy = false;
    if (!paid.ok) {
      this.#refusal = codRefusalWords(paid.status, paid.error);
      this.requestUpdate();
      return;
    }
    this.#done = { orderId: paid.value.orderId, orderNo: paid.value.orderNo };
    this.#paid = paid.value;
    this.#planned = null;
    this.requestUpdate();
  }

  /** The payment after the order exists; a failure here never undoes the order, and the retry carries the same key. */
  async #takePayment(): Promise<void> {
    const client = this.client;
    const planned = this.#planned;
    if (client === null || planned === null || this.#busy) return;
    const seq = this.#seq;
    const quoteId = this.quoteId;
    const quoteNo = this.#view?.quote_no ?? "";
    const orderNo = this.#done?.orderNo ?? "";
    this.#busy = true;
    this.#payNote = "";
    this.requestUpdate();
    const paid = await client.takeOrderPayment(quoteId, {
      method: planned.method,
      amountCents: planned.amountCents,
      reference: planned.reference,
      idempotencyKey: planned.key,
    });
    if (seq !== this.#seq) {
      // The person left while the payment was out: the outcome is said on the desk, never dropped.
      this.#announceAway(quoteId, awayNotice({ orderNo, quoteNo, amountCents: planned.amountCents, paid: paid.ok ? paid.value : null, said: paid.ok ? "" : spokenRefusal(paid.error) }));
      return;
    }
    this.#busy = false;
    if (paid.ok) {
      this.#paid = paid.value;
      this.#planned = null;
    } else {
      const said = spokenRefusal(paid.error);
      this.#payNote = `The order exists, but the payment was not taken: ${said === "" ? "the server did not accept it" : said}${said.endsWith(".") ? "" : "."} Nothing was charged. Try the payment again, or take it at the counter.`;
    }
    this.requestUpdate();
  }

  /** The payment for an order made while the person was away: taken under its own key, and the outcome said where they are. */
  async #settleAway(client: QdClient, quoteId: string, quoteNo: string, orderNo: string, planned: KeyedIntent): Promise<void> {
    const paid = await client.takeOrderPayment(quoteId, {
      method: planned.method,
      amountCents: planned.amountCents,
      reference: planned.reference,
      idempotencyKey: planned.key,
    });
    const said = paid.ok ? "" : spokenRefusal(paid.error);
    this.#announceAway(quoteId, awayNotice({ orderNo, quoteNo, amountCents: planned.amountCents, paid: paid.ok ? paid.value : null, said }));
  }

  /**
   * Tell the desk, wherever the person is now. This element may be gone, and events from a detached
   * element reach nobody, so the window carries it; the desk shows the notice and refreshes its
   * list for the quote (the same work the bubbling quote-changed does while the surface is mounted).
   */
  #announceAway(quoteId: string, text: string): void {
    window.dispatchEvent(new CustomEvent(CONVERT_NOTICE_EVENT, { detail: { quoteId, text } }));
  }

  // ── render ───────────────────────────────────────────────────────────────────

  override render(): TemplateResult {
    if (this.#phase === "loading") {
      return html`<div class="wrap"><p role="status" data-testid="qc-loading">Loading the quote…</p></div>`;
    }
    if (this.#phase !== "ready") return this.#renderStop();
    const view = this.#view;
    if (view === null) return html`${nothing}`;
    return html`
      <div class="wrap">
        <div>
          <h2 data-testid="qc-title">Convert ${view.quote_no} to an order</h2>
          <p class="sub">${view.customer_name}</p>
        </div>
        ${this.#done === null ? this.#renderSteps() : nothing}
        ${this.#done !== null ? this.#renderDone(view) : this.#renderStep(view)}
      </div>
    `;
  }

  #renderStop(): TemplateResult {
    const testid = this.#phase === "no-access" ? "qc-no-access" : this.#phase === "not-ready" ? "qc-not-ready" : "qc-unavailable";
    const heading = this.#phase === "no-access" ? "No access" : this.#phase === "not-ready" ? "This quote cannot be converted yet" : "Convert to Order is unavailable";
    return html`
      <div class="wrap">
        <div class="card" data-testid=${testid} role="alert">
          <h2>${heading}</h2>
          <p>${this.#message}</p>
          <div class="nav"><button type="button" class="btn btn-secondary" data-action="to-quotes" data-testid="qc-back-to-quotes">Back to Quotes</button></div>
        </div>
      </div>
    `;
  }

  #renderSteps(): TemplateResult {
    return html`<ol class="steps" aria-label="Convert steps" data-testid="qc-steps">
      ${CONVERT_STEPS.map(
        (step, index) => html`<li aria-current=${step === this.#step ? "step" : "false"}>${index + 1}. ${STEP_LABEL[step]}</li>`,
      )}
    </ol>`;
  }

  #renderStep(view: QuoteView): TemplateResult {
    switch (this.#step) {
      case "review":
        return this.#renderReview(view);
      case "fulfilment":
        return this.#renderFulfilment();
      case "standing":
        return this.#renderStanding(view);
      case "payment":
        return this.#renderPayment();
      default:
        return this.#renderConfirm(view);
    }
  }

  #nav(opts: { back: boolean; next?: string }): TemplateResult {
    return html`<div class="nav">
      ${opts.back ? html`<button type="button" class="btn btn-secondary" data-action="back" data-testid="qc-back">Back</button>` : nothing}
      ${opts.next !== undefined ? html`<button type="button" class="btn btn-primary" data-action="next" data-testid="qc-next">${opts.next}</button>` : nothing}
    </div>`;
  }

  #renderReview(view: QuoteView): TemplateResult {
    return html`
      <section class="card" data-testid="qc-review" aria-label="Review">
        <h3>Review the order as it will be created</h3>
        <dl class="facts">
          <dt>Customer</dt>
          <dd data-testid="qc-customer">${view.customer_name}</dd>
          <dt>Quote</dt>
          <dd>${view.quote_no}</dd>
        </dl>
        <table>
          <thead>
            <tr><th>Item</th><th class="num">Qty</th><th class="num">Unit price</th><th class="num">Total</th></tr>
          </thead>
          <tbody>
            ${view.lines.map((line) => {
              const words = stockWords(this.#stock.get(line.line_no), this.#branchName());
              return html`<tr data-testid="qc-line">
                <td>
                  ${line.description}
                  ${words.kind === "short" ? html`<p class="warn" data-testid="qc-line-warning">${words.text}</p>` : nothing}
                  ${words.kind === "unknown" && this.#stock.size > 0 ? html`<p class="sub" data-testid="qc-line-stock">${words.text}</p>` : nothing}
                  ${words.kind === "ok" ? html`<p class="sub" data-testid="qc-line-stock">${words.text}</p>` : nothing}
                </td>
                <td class="num">${qtyLabel(line.qty_entered)} ${line.uom_entered}</td>
                <td class="num">${moneyWord(line.unit_price_cents)}</td>
                <td class="num">${moneyWord(line.line_total_cents)}</td>
              </tr>`;
            })}
          </tbody>
        </table>
        <dl class="facts">
          <dt>Subtotal</dt>
          <dd>${moneyWord(view.subtotal_cents)}</dd>
          <dt>Tax</dt>
          <dd>${moneyWord(view.tax_cents)}</dd>
          <dt>Total</dt>
          <dd data-testid="qc-total"><strong>${moneyWord(view.total_cents)}</strong></dd>
        </dl>
        ${this.#stockKnown || this.#stock.size > 0 ? nothing : html`<p class="sub" data-testid="qc-stock-note">Stock at the branch is not reported, so no line is checked against it.</p>`}
        ${this.#nav({ back: false, next: "Continue to fulfilment" })}
      </section>
    `;
  }

  #branchName(): string {
    return this.branchName !== "" ? this.branchName : this.#branchLabel;
  }

  #renderFulfilment(): TemplateResult {
    const cod = this.#isCod();
    return html`
      <section class="card" data-testid="qc-fulfilment" aria-label="Fulfilment">
        <h3>How will the customer get the order?</h3>
        <div class="tiles">
          ${FULFILMENTS.map(
            (f) => html`<button
              type="button"
              class="tile"
              data-action="fulfil"
              data-fulfil=${f.value}
              data-testid=${f.value === "PICKUP" ? "qc-fulfil-pickup" : "qc-fulfil-will-call"}
              aria-pressed=${this.#fulfilment === f.value ? "true" : "false"}
              ?disabled=${cod && f.value === "WILL_CALL"}
            >
              <span class="name">${f.label}</span>
              <span class="hint">${f.hint}</span>
            </button>`,
          )}
        </div>
        ${cod
          ? html`<p class="sub" data-testid="qc-fulfil-cod-note">A Cash On Demand order is created and paid in one action, and that action takes no fulfilment choice, so it is created as Pickup.</p>`
          : nothing}
        <p class="sub" data-testid="qc-delivery-note">${DELIVERY_NOTE}</p>
        ${this.#nav({ back: true, next: "Continue to customer standing" })}
      </section>
    `;
  }

  #renderStanding(view: QuoteView): TemplateResult {
    const standing = this.#standingNow();
    const tone: ChipTone = standing.gate === "hold" ? "stop" : standing.gate === "server" ? "neutral" : "watch";
    const gateWord =
      standing.gate === "hold" ? "Credit hold reported" : standing.gate === "cod" ? "Cash On Demand" : standing.gate === "server" ? "The server decides at confirm" : "Credit not reported";
    return html`
      <section class="card" data-testid="qc-standing" aria-label="Customer standing">
        <h3>${view.customer_name}: standing before confirming</h3>
        <p data-testid="qc-terms">${standing.termsText}</p>
        <div data-testid="qc-credit-chip">${chip(gateWord, tone)}</div>
        <p data-testid="qc-credit" class="${standing.gate === "hold" ? "bad" : ""}">${standing.gateText}</p>
        ${this.#nav({ back: true, next: "Continue to payment" })}
      </section>
    `;
  }

  #renderPayment(): TemplateResult {
    const cod = this.#isCod();
    const due = this.#view?.total_cents ?? 0;
    return html`
      <section class="card" data-testid="qc-payment" aria-label="Payment">
        <h3>${cod ? "Take the whole payment" : "Take a deposit or a payment (optional)"}</h3>
        ${cod
          ? html`<p class="notice" data-testid="qc-pay-cod-note">
                This customer is on Cash On Demand${this.#codLearned ? ": the server confirmed there is no credit account" : ""}. The order is made, and gets its number, when the whole payment is taken, and nothing is released until it is paid.
              </p>
              <p data-testid="qc-pay-due">Amount due: <strong>${moneyWord(due)}</strong>, the quote's total as the server holds it. The whole amount is taken, never a part.</p>`
          : html`<p class="sub">The payment is taken against the order once it exists. Leave it empty to take none now.</p>`}
        <div class="fields">
          <label>
            Method
            ${select({
              label: "Payment method",
              field: "pay-method",
              testid: "qc-pay-method",
              content: html`
                <option value="CASH">${PAY_METHOD_LABEL.CASH}</option>
                <option value="CARD">${PAY_METHOD_LABEL.CARD}</option>
                <option value="CHEQUE">${PAY_METHOD_LABEL.CHEQUE}</option>
                <option value="EFT">${PAY_METHOD_LABEL.EFT}</option>
              `,
            })}
          </label>
          ${cod
            ? nothing
            : html`<label>
                Amount (CA$)
                <input class="control" data-field="pay-amount" data-testid="qc-pay-amount" inputmode="decimal" autocomplete="off" placeholder="0.00" />
              </label>`}
          <label>
            Reference
            <input class="control" data-field="pay-reference" data-testid="qc-pay-reference" autocomplete="off" maxlength="60" placeholder="Cheque number or trace" />
          </label>
        </div>
        ${this.#payError !== "" ? html`<p class="bad" role="alert" data-testid="qc-pay-error">${this.#payError}</p>` : nothing}
        <div class="nav">
          <button type="button" class="btn btn-secondary" data-action="back" data-testid="qc-back">Back</button>
          ${cod ? nothing : html`<button type="button" class="btn btn-secondary" data-action="skip-payment" data-testid="qc-pay-skip">Skip payment</button>`}
          <button type="button" class="btn btn-primary" data-action="next" data-testid="qc-next">Continue to confirm</button>
        </div>
      </section>
    `;
  }

  #renderConfirm(view: QuoteView): TemplateResult {
    const standing = this.#standingNow();
    const cod = this.#isCod();
    const planned = this.#planned;
    return html`
      <section class="card" data-testid="qc-confirm-step" aria-label="Confirm">
        <h3>Confirm the order</h3>
        <dl class="facts">
          <dt>Customer</dt>
          <dd>${view.customer_name}</dd>
          <dt>Total</dt>
          <dd>${moneyWord(view.total_cents)}</dd>
          <dt>Fulfilment</dt>
          <dd>${fulfilmentLabel(this.#fulfilment)}</dd>
          <dt>Terms</dt>
          <dd>${standing.termsText}</dd>
          <dt>${cod ? "Payment, taken as the order is made" : "Payment now"}</dt>
          <dd>${planned === null ? "None" : html`${moneyWord(planned.amountCents)} by ${PAY_METHOD_LABEL[planned.method]}${planned.reference === "" ? nothing : html`, reference ${planned.reference}`}`}</dd>
        </dl>
        ${this.#refusal !== "" ? html`<p class="notice stop" role="alert" data-testid="qc-refusal">${this.#refusal}</p>` : nothing}
        <div class="nav">
          <button type="button" class="btn btn-secondary" data-action="back" data-testid="qc-back" ?disabled=${this.#busy}>Back</button>
          <button type="button" class="btn btn-primary" data-action="confirm" data-testid="qc-confirm" ?disabled=${this.#busy}>${cod ? "Confirm, take the payment and create the order" : "Confirm and create the order"}</button>
        </div>
      </section>
    `;
  }

  #renderDone(view: QuoteView): TemplateResult {
    const done = this.#done;
    if (done === null) return html`${nothing}`;
    return html`
      <section class="card done" data-testid="qc-done" aria-label="Done" role="status">
        <h2 data-testid="qc-done-order">Order ${done.orderNo} created</h2>
        <p>Quote ${view.quote_no} is now marked converted.</p>
        ${this.#paid !== null
          ? html`<p data-testid="qc-done-payment">Payment ${this.#paid.paymentNo} of ${moneyWord(this.#paid.amountCents)} taken. Balance on the invoice: ${moneyWord(this.#paid.balanceCents)}.</p>`
          : nothing}
        ${this.#payNote !== ""
          ? html`<p class="notice stop" role="alert" data-testid="qc-pay-note">${this.#payNote}</p>`
          : nothing}
        <div class="nav">
          ${this.#planned !== null && this.#payNote !== ""
            ? html`<button type="button" class="btn btn-secondary" data-action="retry-payment" data-testid="qc-retry-payment" ?disabled=${this.#busy}>Try the payment again</button>`
            : nothing}
          <button type="button" class="btn btn-primary" data-action="to-quotes" data-testid="qc-back-to-quotes">Back to Quotes</button>
        </div>
      </section>
    `;
  }
}

defineElement("quote-convert", QuoteConvert);

declare global {
  interface HTMLElementTagNameMap {
    "quote-convert": QuoteConvert;
  }
}
