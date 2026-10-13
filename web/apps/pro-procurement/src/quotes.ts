// The Quotes surface: the dealer's quotes, listed, read line by line, and
// answered. Reject and convert are the moves the core serves; acceptance
// happens on the link the dealer sent, and the core says so in its own words
// when Accept is tried here, which the modal shows rather than hides. A
// conversion takes how the order will be collected (pickup or will call; a
// delivery is booked by scheduling a stop, and the core refuses it with that
// sentence). Visual references: the original portal's quotes screen
// (portal-quotes.ts: list, line review, the action modals) and the ref's quote
// document pages, on the design system's cards.
import { html, nothing, type TemplateResult } from "lit";
import { tenantQuotes, type TenantQuote, type TenantQuoteDetail } from "@hardscapehouse/api-client";
import { calendarDay, houseDay, money, qtyWithUnit, statusChip, totalsLine } from "@hardscapehouse/pro-kit";
import { backHeader, button, cancelButton, confirmModal, metaLine, modalSheet, actionFailure, type Page, type SurfaceAnswer } from "./views.js";

export const QUOTES_EMPTY = "No quotes yet. Submit an RFQ and the dealer's quotes land here.";
const NO_QUOTE_WORDS = "That quote is not here anymore.";

export interface QuoteListData {
  view: "list";
  quotes: TenantQuote[];
}

export interface QuoteDetailData {
  view: "detail";
  quote: TenantQuoteDetail;
}

export type QuotesData = QuoteListData | QuoteDetailData;

export async function quotesLoad(page: Page): Promise<SurfaceAnswer> {
  if (!page.session.can("submit_rfq")) return { kind: "refusal" };
  if (page.route.quoteId !== undefined) {
    const answer = await tenantQuotes.tenantGetQuote(page.client, page.route.quoteId);
    if (answer.ok) return { kind: "ready", data: { view: "detail", quote: answer.value }, heading: `Quote ${answer.value.quote_no}` };
    if (answer.status === 404) return { kind: "empty" };
    if (answer.status === 403) return { kind: "refusal" };
    return { kind: "error", status: answer.status };
  }
  const answer = await tenantQuotes.tenantListQuotes(page.client);
  if (answer.ok) {
    if (answer.value.items.length === 0) return { kind: "empty" };
    return { kind: "ready", data: { view: "list", quotes: answer.value.items } };
  }
  if (answer.status === 403) return { kind: "refusal" };
  return { kind: "error", status: answer.status };
}

export function quotesHeading(dataRaw: unknown): string {
  const data = dataRaw as QuotesData | null;
  if (data === null || data === undefined) return "Quotes";
  return data.view === "detail" ? `Quote ${data.quote.quote_no}` : "Quotes";
}

function lineRow(line: TenantQuoteDetail["lines"][number]): TemplateResult {
  return html`<li>
    <div class="card" data-testid="quote-line">
      <span class="rowline"><span class="name">${line.description}</span><span class="grow"></span><span class="mono figure">${money(line.line_total_cents)}</span></span>
      <span class="rowline dim"><span>${qtyWithUnit(line.qty_entered, line.uom_entered)}</span><span class="grow"></span><span class="mono">${money(line.unit_price_cents)}</span></span>
    </div>
  </li>`;
}

function quoteBody(quote: TenantQuoteDetail): TemplateResult {
  const totals = totalsLine(quote);
  const canAccept = quote.status === "SENT";
  const canReject = quote.status === "SENT" || quote.status === "ACCEPTED";
  const canConvert = quote.status === "ACCEPTED";
  return html`<div class="card">
      ${metaLine([
        statusChip("quote", quote.status),
        html`<span>Created ${houseDay(quote.created_at)}</span>`,
        html`<span>Valid until ${calendarDay(quote.valid_until)}</span>`,
      ])}
      <ul class="cards" data-testid="quote-lines">${quote.lines.map(lineRow)}</ul>
      <div class="facts" data-testid="quote-totals">
        <div class="rowline"><span class="dim">Subtotal</span><span class="grow"></span><span class="mono">${totals.subtotal}</span></div>
        <div class="rowline"><span class="dim">Tax</span><span class="grow"></span><span class="mono">${totals.tax}</span></div>
        <div class="rowline"><span class="figure">Total</span><span class="grow"></span><span class="mono figure">${totals.total}</span></div>
      </div>
    </div>
    ${canAccept || canReject || canConvert
      ? html`<div class="actions" data-testid="quote-actions">
          ${canReject ? html`<button type="button" class="btn btn-secondary" data-action="reject-quote-open" data-testid="reject-quote">Reject</button>` : nothing}
          ${canAccept ? html`<button type="button" class="btn btn-primary" data-action="accept-quote-open" data-testid="accept-quote">Accept</button>` : nothing}
          ${canConvert ? html`<button type="button" class="btn btn-primary" data-action="convert-quote-open" data-testid="convert-quote">Convert to order</button>` : nothing}
        </div>`
      : nothing}`;
}

export function quotesView(dataRaw: unknown, page: Page): TemplateResult {
  void page;
  const data = dataRaw as QuotesData;
  if (data.view === "list") {
    return html`<ul class="cards" data-testid="quotes-rows">
      ${data.quotes.map(
        (quote) => html`<li>
          <button type="button" class="card" data-action="open-quote" data-quote="${quote.id}" data-testid="quotes-row">
            <span class="rowline"><span class="mono figure">${quote.quote_no}</span><span class="grow"></span><span class="mono figure">${money(quote.total_cents)}</span></span>
            <span class="rowline">${statusChip("quote", quote.status)}<span class="grow"></span><span class="dim">Valid until ${calendarDay(quote.valid_until)}</span></span>
          </button>
        </li>`,
      )}
    </ul>`;
  }
  return html`${backHeader("Quotes", "open-quotes")}${quoteBody(data.quote)}`;
}

// ---- the action modals ---------------------------------------------------------------------------

interface QuoteUi {
  modal: { kind: "accept" | "reject" | "convert"; busy: boolean; error: string; fulfillment: string } | null;
}

const quoteStates = new WeakMap<Page, QuoteUi>();

function quoteUi(page: Page): QuoteUi {
  let held = quoteStates.get(page);
  if (held === undefined) {
    held = { modal: null };
    quoteStates.set(page, held);
  }
  return held;
}

function quoteModal(ui: QuoteUi): TemplateResult {
  const modal = ui.modal;
  if (modal === null) return html``;
  const heading = modal.kind === "accept" ? "Accept this quote?" : modal.kind === "reject" ? "Reject this quote?" : "Convert to an order";
  const body =
    modal.kind === "convert"
      ? html`<p class="words">How will the order be collected? A delivery is booked later, by scheduling a stop.</p>
          <div class="actions">
            <button type="button" class="btn btn-secondary ${modal.fulfillment === "PICKUP" ? "btn-primary" : ""}" data-action="choose-fulfillment" data-value="PICKUP" data-testid="choose-pickup">Pickup</button>
            <button type="button" class="btn btn-secondary ${modal.fulfillment === "WILL_CALL" ? "btn-primary" : ""}" data-action="choose-fulfillment" data-value="WILL_CALL" data-testid="choose-will-call">Will call</button>
          </div>`
      : modal.kind === "accept"
        ? html`<p class="words">The quoted lines are what you are agreeing to.</p>`
        : html`<p class="words">The dealer sees that the quote was not taken. The quote closes.</p>`;
  const confirmLabel = modal.kind === "convert" ? "Convert" : modal.kind === "accept" ? "Accept" : "Reject";
  const confirmDisabled = modal.kind === "convert" && modal.fulfillment === "";
  return confirmModal({
    heading,
    body,
    confirmLabel,
    confirmAction: `${modal.kind}-quote`,
    confirmDisabled,
    busy: modal.busy,
    error: modal.error,
    testid: "quote-modal",
  });
}

// ---- the moves -----------------------------------------------------------------------------------

function toAnswer(result: { ok: boolean; error?: string; status?: number }, page: Page): Promise<{ ok: true } | { ok: false; error: string; status?: number }> {
  if (result.ok) return Promise.resolve({ ok: true } as { ok: true });
  return actionFailure(page, result).then((words) => {
    page.say("alert", words);
    return { ok: false, error: words, status: result.status };
  });
}

export async function quotesAccept(page: Page, id: string): Promise<{ ok: true } | { ok: false; error: string; status?: number }> {
  const answer = await tenantQuotes.tenantAcceptQuote(page.client, id);
  const said = await toAnswer(answer, page);
  if (!said.ok) return said;
  return { ok: true };
}

export async function quotesReject(page: Page, id: string): Promise<{ ok: true } | { ok: false; error: string; status?: number }> {
  const answer = await tenantQuotes.tenantRejectQuote(page.client, id);
  const said = await toAnswer(answer, page);
  if (!said.ok) return said;
  page.say("ok", "The quote was rejected.");
  return { ok: true };
}

export async function quotesConvert(page: Page, id: string, fulfillment: string): Promise<{ ok: true; order_id: string; order_no: string } | { ok: false; error: string; status?: number }> {
  const answer = await tenantQuotes.tenantConvertQuote(page.client, id, { fulfillment: fulfillment as "PICKUP" | "WILL_CALL" });
  if (answer.ok) {
    page.say("ok", `Converted to order ${answer.value.order_no}.`);
    return { ok: true, order_id: answer.value.order_id, order_no: answer.value.order_no };
  }
  // A conversion the transport lost may stand on the core's side: the words
  // send the person to look, they never promise nothing changed.
  const words = await actionFailure(page, answer, true);
  page.say("alert", words);
  return { ok: false, error: words, status: answer.status };
}

export function quotesCommand(action: string, control: Element, page: Page): boolean {
  const ui = quoteUi(page);
  const quoteId = page.route.quoteId ?? "";
  if (action === "accept-quote-open" || action === "reject-quote-open" || action === "convert-quote-open") {
    const kind = action === "accept-quote-open" ? "accept" : action === "reject-quote-open" ? "reject" : "convert";
    ui.modal = { kind, busy: false, error: "", fulfillment: "" };
    page.openModal("quote-modal", () => quoteModal(ui));
    return true;
  }
  if (ui.modal === null) return false;
  if (action === "choose-fulfillment") {
    ui.modal.fulfillment = control.getAttribute("data-value") ?? "";
    page.touch();
    return true;
  }
  if (action === "accept-quote") {
    void runQuoteAction(page, "accept");
    return true;
  }
  if (action === "reject-quote") {
    void runQuoteAction(page, "reject");
    return true;
  }
  if (action === "convert-quote") {
    void runQuoteAction(page, "convert");
    return true;
  }
  return false;
}

async function runQuoteAction(page: Page, kind: "accept" | "reject" | "convert"): Promise<void> {
  const ui = quoteUi(page);
  const modal = ui.modal;
  const quoteId = page.route.quoteId ?? "";
  if (modal === null || modal.busy || quoteId === "") return;
  if (kind === "convert" && modal.fulfillment === "") return;
  modal.busy = true;
  modal.error = "";
  page.touch();
  const result = kind === "accept" ? await quotesAccept(page, quoteId) : kind === "reject" ? await quotesReject(page, quoteId) : await quotesConvert(page, quoteId, modal.fulfillment);
  modal.busy = false;
  if (result.ok) {
    ui.modal = null;
    page.closeModal();
    page.refresh();
  } else {
    // The server's words stay in the modal, where the action was taken.
    modal.error = result.error;
    page.touch();
  }
}
