// The Pro Pay screens as pure templates over one state object (the root owns the state and the actions).
// Visual references: HH: Pro `pay-hosted-phone-light` (the pay sheet: methods with the fee on each, Add a card, the
// gold Pay button) and the live captures `app-phone-05-pay` and `app-desktop-04-pay` (the outstanding balance, the
// aging tiles, select to pay, the sticky Pay bar). Phone first, then tablet and desktop.
//
// Position gate: no bound property or event anywhere; actions ride data-action and data-id and the root's host
// listener acts on them. A checkbox or a radio is a button with role and aria-checked (a quoted aria value is a bound
// position the gate allows); there is no text input in this app at all (the card field is the processor's frame).
import { html, nothing, type TemplateResult } from "lit";
import type { TenantBillingSummary, TenantInvoice, TenantInvoiceDetail, TenantInvoiceLine, TenantPayment, TenantPaymentMethod } from "@hardscapehouse/api-client";
import { money, calendarDay, houseDay, qtyWithUnit, statusChip, NO_FIGURE } from "@hardscapehouse/pro-kit";
import { BUCKETS, BUCKET_WORDS, agingTotals, chargeFor, daysPastDue, isPayable, payableCents, type MethodKind } from "./pay-core.js";
import { SANDBOX_NOTE } from "./hosted-field.js";

export type Phase = "choose" | "sending" | "unknown" | "declined" | "refused" | "paid";

export interface Sheet {
  phase: Phase;
  ids: string[];
  expectedCents: number;
  /** "" none yet, a stored method id, or "new" (a card added at checkout, paid with its token). */
  choice: string;
  /** The one time token from the hosted frame; null until the frame hands one up. */
  nonce: string | null;
  methods: readonly TenantPaymentMethod[];
  methodsNote: string;
  words: string;
  receipt: TenantPayment | null;
  differs: boolean;
  /** The processor's hosted field is reachable (sandbox build); false shows plain words in its place. */
  frameAvailable: boolean;
}

export type Load = "loading" | "ready" | "error" | "refusal";

export interface ViewState {
  today: string;
  canPay: boolean;
  invoices: readonly TenantInvoice[];
  summary: TenantBillingSummary | null;
  selected: ReadonlySet<string>;
  openInvoice: { id: string; load: Load; detail: TenantInvoiceDetail | null } | null;
  sheet: Sheet | null;
  /** A payment held for an unknown outcome, shown on the invoices screen until it is checked. */
  waiting: boolean;
  methods: readonly TenantPaymentMethod[];
  methodAdd: { open: boolean; makeDefault: boolean; busy: boolean; words: string; nonce: string | null; frameAvailable: boolean };
  confirmRemove: string | null;
  methodWords: string;
  payments: readonly TenantPayment[];
}

// ---- words ---------------------------------------------------------------------------------------------------

function titleCase(word: string): string {
  return word.length === 0 ? word : word[0]!.toUpperCase() + word.slice(1).toLowerCase();
}

export function methodName(m: Pick<TenantPaymentMethod, "method_type" | "brand" | "last4">): string {
  return m.method_type === "BANK" ? `Bank account · ${m.brand} ·· ${m.last4}` : `${titleCase(m.brand)} ·· ${m.last4}`;
}

function methodExpiry(m: TenantPaymentMethod): string {
  return m.method_type === "CARD" && m.exp_month > 0 ? `Expires ${String(m.exp_month).padStart(2, "0")}/${String(m.exp_year % 100).padStart(2, "0")}` : "";
}

function tender(method: string): string {
  return method === "" ? "Payment" : method === "EFT" ? "Bank account" : method === "CARD" ? "Card" : titleCase(method);
}

function plural(n: number, one: string, many: string): string {
  return n === 1 ? one : many;
}

function lateWords(invoice: TenantInvoice, today: string): { words: string; late: boolean } {
  if (invoice.status === "PAID" || invoice.status === "VOID" || invoice.status === "DRAFT") return { words: `Issued ${calendarDay(invoice.issue_date)}`, late: false };
  const late = daysPastDue(invoice.due_date, today);
  return late > 0 ? { words: `${late} ${plural(late, "day", "days")} late`, late: true } : { words: `Due ${calendarDay(invoice.due_date)}`, late: false };
}

// ---- Pay home: Invoices ---------------------------------------------------------------------------------------

function checkbox(on: boolean, id: string, label: string): TemplateResult {
  return html`<button type="button" class="box" role="checkbox" aria-checked="${on ? "true" : "false"}" aria-label="${label}" data-action="toggle" data-id="${id}"><span class="mark"></span></button>`;
}

function invoiceRow(invoice: TenantInvoice, state: ViewState): TemplateResult {
  const payable = isPayable(invoice);
  const { words, late } = lateWords(invoice, state.today);
  const pickable = state.canPay && payable;
  const on = state.selected.has(invoice.id);
  return html`<li class="inv ${on ? "is-on" : ""}" data-testid="invoice-row" data-invoice="${invoice.invoice_no}">
    ${pickable ? checkbox(on, invoice.id, `Select ${invoice.invoice_no}`) : html`<span class="box-gap"></span>`}
    <button type="button" class="inv-main" data-action="open" data-id="${invoice.id}">
      <span class="inv-top"><span class="mono inv-no">${invoice.invoice_no}</span>${statusChip("invoice", invoice.status)}</span>
      <span class="inv-sub ${late ? "late" : ""}">${words}</span>
    </button>
    <span class="inv-amount mono">${payable ? html`${money(payableCents(invoice))}<span class="inv-of">of ${money(invoice.total_cents)}</span>` : money(invoice.total_cents)}</span>
  </li>`;
}

export function invoicesScreen(state: ViewState): TemplateResult {
  const totals = agingTotals(state.invoices, state.today);
  const payable = state.invoices.filter(isPayable);
  const picked = payable.filter((i) => state.selected.has(i.id));
  const pickedCents = picked.reduce((sum, i) => sum + payableCents(i), 0);
  const owed = state.summary === null ? NO_FIGURE : money(state.summary.open_balance_cents);
  return html`
    <section class="owed" data-testid="owed">
      <p class="label">Outstanding balance</p>
      <p class="big mono" data-testid="owed-total">${owed}</p>
      ${totals.overdueCount > 0
        ? html`<p class="late" data-testid="owed-overdue">${money(totals.overdueCents)} overdue across ${totals.overdueCount} ${plural(totals.overdueCount, "invoice", "invoices")}</p>`
        : html`<p class="quiet">Nothing is overdue.</p>`}
      ${state.summary !== null && state.summary.on_hold ? html`<p class="late" data-testid="owed-hold">The account is on hold. Contact the dealer.</p>` : nothing}
      <ul class="tiles" data-testid="aging">
        ${BUCKETS.map((b) => html`<li class="tile" data-bucket="${b}"><span class="tile-label">${BUCKET_WORDS[b]}</span><span class="tile-sum mono">${totals.buckets[b] === 0 ? "None" : money(totals.buckets[b])}</span></li>`)}
      </ul>
    </section>
    ${state.waiting
      ? html`<div class="notice" role="alert" data-testid="waiting-notice"><span>A payment is waiting for an answer. It may have gone through.</span><button type="button" class="btn btn-secondary" data-action="check-waiting">Check payment</button></div>`
      : nothing}
    ${!state.canPay ? html`<p class="quiet" data-testid="read-only">Your role can read invoices. Paying is for the roles that hold it.</p>` : nothing}
    ${state.canPay && payable.length > 0
      ? html`<div class="pickbar"><span data-testid="picked-count">${picked.length} of ${payable.length} selected</span><button type="button" class="btn btn-secondary" data-action="select-all">${picked.length === payable.length ? "Clear" : `Select all ${payable.length}`}</button></div>`
      : nothing}
    <ul class="invoices" data-testid="landing-rows">${state.invoices.map((i) => invoiceRow(i, state))}</ul>
    ${state.canPay
      ? html`<div class="paybar" data-testid="paybar"><button type="button" class="btn btn-primary paybtn" data-action="open-sheet" data-testid="pay-open" ?disabled=${picked.length === 0}><span>${picked.length === 0 ? "Pick invoices to pay" : `Pay ${picked.length} ${plural(picked.length, "invoice", "invoices")}`}</span>${picked.length === 0 ? nothing : html`<span class="mono">${money(pickedCents)}</span>`}</button></div>`
      : nothing}`;
}

// ---- Invoice detail with its statement ------------------------------------------------------------------------

export function invoiceDetailScreen(state: ViewState): TemplateResult {
  const open = state.openInvoice;
  if (open === null) return html``;
  const back = html`<button type="button" class="btn btn-secondary back" data-action="back" data-testid="invoice-back">All invoices</button>`;
  if (open.load === "loading") return html`${back}<hh-pro-state data-state="loading" data-testid="detail-state"></hh-pro-state>`;
  if (open.load === "refusal") return html`${back}<hh-pro-state data-state="refusal" data-testid="detail-state"></hh-pro-state>`;
  if (open.load === "error" || open.detail === null) return html`${back}<hh-pro-state data-state="error" data-testid="detail-state">This invoice did not load. Nothing was changed.</hh-pro-state>`;
  const d = open.detail;
  const row = state.invoices.find((i) => i.id === d.id);
  const payNowCents = row !== undefined && isPayable(row) ? payableCents(row) : 0;
  // The generated detail type crosses InvoiceView with the ERP line shape; the wire carries the ERP lines.
  const lines = (d.lines ?? []) as unknown as readonly TenantInvoiceLine[];
  const { words, late } = row === undefined ? { words: "", late: false } : lateWords(row, state.today);
  const history = d.payment_history ?? [];
  const hold = d.holdback_cents ?? 0;
  return html`${back}
    <section class="detail" data-testid="invoice-detail">
      <div class="detail-head">
        <h2 class="mono" data-testid="detail-no">${d.invoice_no}</h2>
        ${statusChip("invoice", d.status)}
      </div>
      <p class="quiet">Issued ${calendarDay(d.issue_date)} · Due ${calendarDay(d.due_date)}${late ? html` · <span class="late">${words}</span>` : nothing}</p>
      <h3>Statement</h3>
      <ul class="lines" data-testid="detail-lines">
        ${lines.map((l) => html`<li class="line"><span class="line-desc">${l.description}</span><span class="mono line-qty">${qtyWithUnit(l.qty_entered, l.uom_entered)}</span><span class="mono line-total">${money(l.line_total_cents)}</span></li>`)}
      </ul>
      <dl class="sums" data-testid="detail-sums">
        <div><dt>Subtotal</dt><dd class="mono">${money(d.subtotal_cents)}</dd></div>
        <div><dt>Tax</dt><dd class="mono">${money(d.tax_cents)}</dd></div>
        ${hold > 0 ? html`<div><dt>Held back</dt><dd class="mono">${money(hold)}</dd></div>` : nothing}
        <div class="sum-total"><dt>Total</dt><dd class="mono">${money(d.total_cents)}</dd></div>
        <div class="sum-total"><dt>Balance</dt><dd class="mono" data-testid="detail-balance">${money(d.balance_cents)}</dd></div>
      </dl>
      <h3>Payments on this invoice</h3>
      ${history.length === 0
        ? html`<p class="quiet" data-testid="detail-history-empty">No payments yet.</p>`
        : html`<ul class="lines" data-testid="detail-history">${history.map((p) => html`<li class="line"><span class="line-desc">${tender(p.method ?? "")}</span><span class="mono line-qty">${houseDay(p.received_at)}</span><span class="mono line-total">${money(p.amount_cents)}</span></li>`)}</ul>`}
      ${state.canPay && payNowCents > 0 ? html`<button type="button" class="btn btn-primary detail-pay" data-action="pay-this" data-id="${d.id}" data-testid="pay-this">Pay ${money(payNowCents)}</button>` : nothing}
    </section>`;
}

// ---- The pay sheet ----------------------------------------------------------------------------------------------

function methodChoice(m: TenantPaymentMethod, sheet: Sheet, locked: boolean): TemplateResult {
  const kind: MethodKind = m.method_type;
  const charge = chargeFor(kind, sheet.expectedCents);
  const on = sheet.choice === m.id;
  const expiry = methodExpiry(m);
  return html`<button type="button" class="opt ${on ? "is-on" : ""}" role="radio" aria-checked="${on ? "true" : "false"}" data-action="choose" data-id="${m.id}" data-testid="method-option" ?disabled=${locked}>
    <span class="dot"></span>
    <span class="opt-text">
      <span class="opt-name">${methodName(m)}${m.is_default ? html` <span class="pchip pchip-neutral">Default</span>` : nothing}</span>
      <span class="opt-sub">${charge.feeWords} · you pay <span class="mono">${money(charge.totalCents)}</span>${expiry === "" ? nothing : html` · ${expiry}`}</span>
    </span>
  </button>`;
}

function sheetBody(state: ViewState, sheet: Sheet): TemplateResult {
  const locked = sheet.phase !== "choose";
  const newOn = sheet.choice === "new";
  const cardCharge = chargeFor("CARD", sheet.expectedCents);
  const ready = sheet.choice !== "" && (sheet.choice !== "new" || sheet.nonce !== null);
  const count = sheet.ids.length;
  if (sheet.phase === "paid" && sheet.receipt !== null) {
    const r = sheet.receipt;
    return html`<div class="receipt" data-testid="receipt" role="status">
      <p class="receipt-head">Payment received</p>
      <p class="big mono" data-testid="receipt-amount">${money(r.amount_cents)}</p>
      <p class="quiet"><span class="mono" data-testid="receipt-no">${r.payment_no}</span> · ${tender(r.method)} · ${houseDay(r.received_at)}</p>
      ${sheet.differs ? html`<p class="notice-line" data-testid="receipt-differs">The dealer's books settled ${money(r.amount_cents)}, not the ${money(sheet.expectedCents)} shown before paying. The receipt above is the figure that counts.</p>` : nothing}
      <button type="button" class="btn btn-primary" data-action="done" data-testid="receipt-done">Done</button>
    </div>`;
  }
  return html`
    <h2 class="sheet-title" data-testid="sheet-title">Pay ${count} ${plural(count, "invoice", "invoices")} · <span class="mono">${money(sheet.expectedCents)}</span></h2>
    <p class="sandbox" data-testid="sandbox-note">${SANDBOX_NOTE}</p>
    <div class="opts" role="radiogroup" aria-label="Pay with" data-testid="methods">
      ${sheet.methods.map((m) => methodChoice(m, sheet, locked))}
      <button type="button" class="opt opt-add ${newOn ? "is-on" : ""}" role="radio" aria-checked="${newOn ? "true" : "false"}" data-action="choose" data-id="new" data-testid="method-new" ?disabled=${locked}>
        <span class="dot"></span>
        <span class="opt-text">
          <span class="opt-name">Add a card</span>
          <span class="opt-sub">${cardCharge.feeWords} · you pay <span class="mono">${money(cardCharge.totalCents)}</span></span>
        </span>
      </button>
    </div>
    ${sheet.methodsNote === "" ? nothing : html`<p class="quiet">${sheet.methodsNote}</p>`}
    ${newOn ? cardFrame(sheet.frameAvailable, sheet.nonce !== null) : nothing}
    <p class="quiet fine">No fee is added to any method. The card form is the payment processor's, in a frame of its own; the card number never touches HH.</p>
    ${sheet.words === "" ? nothing : html`<p class="${sheet.phase === "unknown" ? "notice-line" : "late"}" role="alert" data-testid="sheet-words">${sheet.words}</p>`}
    ${sheet.phase === "unknown"
      ? html`<button type="button" class="btn btn-primary paybtn" data-action="pay" data-testid="check-payment"><span>Check payment</span><span class="mono">${money(sheet.expectedCents)}</span></button>`
      : html`<button type="button" class="btn btn-primary paybtn" data-action="pay" data-testid="pay-confirm" ?disabled=${!ready || sheet.phase === "sending"}><span>${sheet.phase === "sending" ? "Paying" : "Pay"}</span><span class="mono">${money(sheet.expectedCents)}</span></button>`}
    ${sheet.phase === "unknown" ? html`<p class="quiet fine">Pressing it asks the dealer's books about this same payment. It will not charge twice.</p>` : nothing}`;
}

/** The processor's hosted field in a frame of its own; the root assigns its address after render (the gate refuses a bound src). */
function cardFrame(available: boolean, got: boolean): TemplateResult {
  if (!available) return html`<p class="quiet" data-testid="no-processor">Adding a card opens when the dealer connects a payment processor.</p>`;
  return html`<div class="frame-wrap" data-testid="card-wrap">
    <iframe class="card-frame" data-testid="card-frame" data-hosted="field" sandbox="allow-scripts allow-forms" title="Card form"></iframe>
    ${got ? html`<p class="ok" data-testid="card-ready">Card ready to use.</p>` : nothing}
  </div>`;
}

export function sheetView(state: ViewState): TemplateResult {
  const sheet = state.sheet;
  if (sheet === null) return html``;
  const closable = sheet.phase !== "sending";
  return html`<div class="scrim" data-action="${closable ? "close-sheet" : "none"}"></div>
    <div class="sheet" role="dialog" aria-modal="true" aria-label="Pay invoices" tabindex="-1" data-testid="pay-sheet">
      <div class="grab"></div>
      ${closable && sheet.phase !== "paid" ? html`<button type="button" class="btn btn-secondary sheet-close" data-action="close-sheet" data-testid="sheet-close">Close</button>` : nothing}
      ${sheetBody(state, sheet)}
    </div>`;
}

// ---- Payment methods --------------------------------------------------------------------------------------------

export function methodsScreen(state: ViewState): TemplateResult {
  const add = state.methodAdd;
  return html`
    <p class="sandbox" data-testid="sandbox-note">${SANDBOX_NOTE}</p>
    ${state.methodWords === "" ? nothing : html`<p class="late" role="alert" data-testid="method-words">${state.methodWords}</p>`}
    <ul class="methods" data-testid="method-list">
      ${state.methods.map((m) => html`<li class="method" data-testid="method-row">
        <div class="method-text">
          <span class="opt-name">${methodName(m)}${m.is_default ? html` <span class="pchip pchip-good" data-testid="default-chip">Default</span>` : nothing}</span>
          <span class="opt-sub">${methodExpiry(m) === "" ? "Used for invoices" : methodExpiry(m)}</span>
        </div>
        <div class="method-actions">
          ${m.is_default ? nothing : html`<button type="button" class="btn btn-secondary" data-action="make-default" data-id="${m.id}">Make default</button>`}
          ${state.confirmRemove === m.id
            ? html`<button type="button" class="btn btn-danger" data-action="confirm-remove" data-id="${m.id}" data-testid="confirm-remove">Remove it</button><button type="button" class="btn btn-secondary" data-action="keep">Keep</button>`
            : html`<button type="button" class="btn btn-secondary" data-action="remove" data-id="${m.id}" data-testid="remove-method">Remove</button>`}
        </div>
      </li>`)}
    </ul>
    ${state.methods.length === 0 ? html`<p class="quiet" data-testid="methods-none">No payment methods yet.</p>` : nothing}
    <section class="add" data-testid="add-method">
      ${add.open
        ? html`<h3>Add a card</h3>
            ${add.frameAvailable ? cardFrame(true, add.nonce !== null) : cardFrame(false, false)}
            <button type="button" class="box-line" role="checkbox" aria-checked="${add.makeDefault ? "true" : "false"}" data-action="toggle-default" data-testid="make-default-box"><span class="box"><span class="mark"></span></span><span>Make this the default</span></button>
            ${add.words === "" ? nothing : html`<p class="late" role="alert" data-testid="add-words">${add.words}</p>`}
            <div class="row-actions">
              <button type="button" class="btn btn-primary" data-action="save-method" data-testid="save-method" ?disabled=${add.nonce === null || add.busy}>${add.busy ? "Saving" : "Save card"}</button>
              <button type="button" class="btn btn-secondary" data-action="cancel-add">Cancel</button>
            </div>`
        : html`<button type="button" class="btn btn-secondary" data-action="open-add" data-testid="open-add">Add a card</button>`}
    </section>`;
}

// ---- Statements: payment history ---------------------------------------------------------------------------------

export function statementsScreen(state: ViewState): TemplateResult {
  const byId = new Map(state.invoices.map((i) => [i.id, i.invoice_no] as const));
  const paid = state.payments.filter((p) => p.status === "RECEIVED" || p.status === "DEPOSITED").reduce((sum, p) => sum + p.amount_cents, 0);
  return html`
    <section class="owed" data-testid="statement-summary">
      <p class="label">Paid to the dealer</p>
      <p class="big mono" data-testid="paid-total">${money(paid)}</p>
      <p class="quiet">${state.summary === null ? "" : html`Still open <span class="mono">${money(state.summary.open_balance_cents)}</span> · `}${state.payments.length} ${plural(state.payments.length, "payment", "payments")}</p>
    </section>
    <ul class="payments" data-testid="payment-rows">
      ${state.payments.map((p) => html`<li class="pay-row" data-testid="payment-row">
        <div class="pay-top"><span class="mono">${p.payment_no}</span>${statusChip("payment", p.status)}<span class="pay-amount mono">${money(p.amount_cents)}</span></div>
        <div class="quiet">${tender(p.method)} · ${houseDay(p.received_at)} · Applied to ${p.allocations.length === 0 ? "no invoice yet" : p.allocations.map((a) => byId.get(a.invoice_id) ?? "an invoice").join(", ")}</div>
      </li>`)}
    </ul>`;
}
