// The receiving area's screens, as plain render functions of a model: the queue of orders with
// something still to arrive, the order beside it (its lines and the receive form, its receipts,
// its document and its sends), and a receipt with the landed cost that can be attached to it. They
// hold no state and own no events. Every figure on a screen is the server's, formatted. Where the
// purchasing policy hides cost from the caller the screen says so in words, never a zero.
//
// References: the Money Desk's cards and tables, the procurement panels, and the original app's
// receiving screens, drawn to HH's design system.
import { html, nothing, type TemplateResult } from "lit";
import type { PurchasingPODetail, PurchasingPOSend, PurchasingReceipt, PurchasingWorklistItem } from "@hardscapehouse/api-client";
import { areaHead, confirmBox, emptyState, errorNote, loadFailed, loadingNote, noticeBox, pill, segs } from "../purch/purch-views.js";
import { formatMoney, formatQty } from "../purch/purch-money.js";
import { deliveryWord, whenWords, statusTone, statusWord, type Tone } from "../po/po-logic.js";
import { BASES, basisWord } from "../landed/landed-logic.js";
import { receivable, sendOutcome } from "./receiving-logic.js";

export type ConfirmKind = "receive" | "attach" | "send" | "send-fallback";
export type ErrorAt = "order" | "receipt" | "send";

export interface ReceivingModel {
  readonly phase: "loading" | "ready" | "error";
  readonly worklist: readonly PurchasingWorklistItem[];
  readonly branchNames: ReadonlyMap<string, string>;
  readonly po: PurchasingPODetail | null;
  /** The purchasing policy hides cost from this role: no unit cost, no value, said in words. */
  readonly hidden: boolean;
  readonly sends: readonly PurchasingPOSend[];
  readonly receipt: PurchasingReceipt | null;
  readonly attachCurrency: string;
  readonly attachBasis: string;
  readonly confirm: { readonly kind: ConfirmKind; readonly words: string } | null;
  readonly busy: boolean;
  readonly error: string;
  readonly errorAt: ErrorAt;
  readonly notice: string;
  /** What the last send came to; the request succeeds even when the delivery failed. */
  readonly sendResult: { readonly tone: Tone; readonly words: string } | null;
  readonly today: string;
}

const noteAt = (model: ReceivingModel, at: ErrorAt): TemplateResult => (model.errorAt === at ? errorNote(model.error) : html`${nothing}`);

// ---- the queue -------------------------------------------------------------------------------------------

function queue(model: ReceivingModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Orders to receive" data-queue>
      <div class="panel-head">
        <h3>To receive</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="refresh" ?disabled=${model.busy}>Refresh</button>
      </div>
      ${model.worklist.length === 0
        ? emptyState("Nothing is waiting to be received.", "An order placed with a vendor shows here until every line has landed.")
        : html`
            <div class="scroll">
              <table class="stack">
                <thead><tr><th>Order</th><th>State</th><th data-expected-col>Expected</th><th class="num">Open lines</th><th class="num">Value open</th></tr></thead>
                <tbody>
                  ${model.worklist.map((w) => {
                    const words = whenWords(w.expected_at, model.today);
                    return html`<tr data-queue-row data-po-no=${w.po_no} aria-current="${model.po !== null && model.po.id === w.po_id ? "true" : "false"}">
                      <td class="full" data-label="Order"><button type="button" class="link-inline" data-action="open-order" data-po-id=${w.po_id}>${w.po_no}</button><span class="small dim">${w.vendor_name} · ${model.branchNames.get(w.branch_id) ?? ""}</span></td>
                      <td data-label="State">${pill(statusTone(w.status), statusWord(w.status))}</td>
                      <td data-expected-col data-label="Expected">${words === "" ? html`<span class="nodate">no date</span>` : html`<span class="who">${words}</span>`}</td>
                      <td class="num" data-label="Open lines">${w.open_lines}</td>
                      <td class="num" data-label="Value open">${w.value_open_cents === undefined ? html`<span class="dim">hidden</span>` : formatMoney(w.value_open_cents)}</td>
                    </tr>`;
                  })}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

// ---- the order ----------------------------------------------------------------------------------------------

function linesTable(model: ReceivingModel, po: PurchasingPODetail): TemplateResult {
  const can = receivable(po.status) === "";
  return html`
    <div class="scroll">
      <table class="stack" data-order-lines>
        <thead>
          <tr>
            <th>SKU</th><th>Product</th><th class="num">Ordered</th><th class="num">Received</th><th class="num">Outstanding</th>
            ${model.hidden ? nothing : html`<th class="num">Unit cost</th>`}
            ${can ? html`<th>Arrived now</th>` : nothing}
          </tr>
        </thead>
        <tbody>
          ${po.lines.map(
            (l) => html`<tr data-order-line>
              <td class="sku" data-label="SKU">${l.sku}</td>
              <td class="full" data-label="Product">${l.description}</td>
              <td class="num" data-label="Ordered">${formatQty(l.qty_ordered_base, l.uom)}</td>
              <td class="num" data-label="Received">${formatQty(l.qty_received_base, l.uom)}</td>
              <td class="num" data-label="Outstanding">${formatQty(l.qty_outstanding_base, l.uom)}</td>
              ${model.hidden ? nothing : html`<td class="num" data-label="Unit cost">${formatMoney(l.unit_cost_cents)}</td>`}
              ${can
                ? html`<td data-label="Arrived now">${l.qty_outstanding_base > 0
                    ? html`<input type="text" inputmode="decimal" autocomplete="off" aria-label=${`Arrived now: ${l.sku}`} data-field=${`recv-${l.id}`} data-recv />`
                    : html`<span class="dim">all in</span>`}</td>`
                : nothing}
            </tr>`,
          )}
        </tbody>
      </table>
    </div>
  `;
}

function receiveBlock(model: ReceivingModel, po: PurchasingPODetail): TemplateResult {
  const why = receivable(po.status);
  if (why !== "") return html`<p class="foot dim" data-not-receivable>${why}</p>`;
  return html`
    <label>Notes on the delivery
      <input type="text" autocomplete="off" data-field="recv-notes" data-recv-notes />
    </label>
    ${noteAt(model, "order")}
    ${model.confirm?.kind === "receive"
      ? confirmBox(model.confirm.words, "Receive the goods", "do-receive", model.busy)
      : html`<div class="actions end">
          <button type="button" class="btn btn-primary" data-action="ask-receive" ?disabled=${model.busy}>Receive goods</button>
        </div>`}
  `;
}

function receiptsBlock(model: ReceivingModel, po: PurchasingPODetail): TemplateResult {
  return html`
    <h3 class="sub">Receipts</h3>
    ${po.receipts.length === 0
      ? html`<p class="dim" data-no-receipts>Nothing has been received against this order yet.</p>`
      : html`
          <div class="scroll">
            <table class="stack" data-receipts>
              <thead><tr><th>Receipt</th><th>Received</th>${model.hidden ? nothing : html`<th class="num">Value</th>`}<th>Landed cost</th><th><span class="visually-hidden">Open</span></th></tr></thead>
              <tbody>
                ${po.receipts.map(
                  (r) => html`<tr data-receipt-row data-grn=${r.grn_no}>
                    <td class="code" data-label="Receipt">${r.grn_no}</td>
                    <td data-label="Received">${whenWords(r.received_at, model.today)}</td>
                    ${model.hidden ? nothing : html`<td class="num" data-label="Value">${formatMoney(r.value_cad_cents)}</td>`}
                    <td data-label="Landed cost">${r.landed_cost_id === undefined || r.landed_cost_id === "" ? html`<span class="dim">none</span>` : pill("good", "Applied")}</td>
                    <td class="pick"><button type="button" class="btn btn-secondary small-btn" data-action="open-receipt" data-receipt-id=${r.id}>Open</button></td>
                  </tr>`,
                )}
              </tbody>
            </table>
          </div>
        `}
  `;
}

function receiptPanel(model: ReceivingModel, rc: PurchasingReceipt): TemplateResult {
  const applied = rc.landed_cost_id !== undefined && rc.landed_cost_id !== "";
  return html`
    <section class="panel inner" aria-label=${`Receipt ${rc.grn_no}`} data-receipt-panel>
      <div class="panel-head">
        <h3>Receipt ${rc.grn_no}</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="close-receipt">Close</button>
      </div>
      <div class="scroll">
        <table class="stack" data-receipt-lines>
          <thead><tr><th>SKU</th><th class="num">Received</th>${model.hidden ? nothing : html`<th class="num">Unit cost</th><th class="num">Value</th>`}</tr></thead>
          <tbody>
            ${(rc.lines ?? []).map(
              (l) => html`<tr>
                <td class="sku" data-label="SKU">${l.sku}</td>
                <td class="num" data-label="Received">${formatQty(l.qty_base, "")}</td>
                ${model.hidden ? nothing : html`<td class="num" data-label="Unit cost">${formatMoney(l.unit_cost_cents)}</td><td class="num" data-label="Value">${formatMoney(l.value_cents)}</td>`}
              </tr>`,
            )}
          </tbody>
        </table>
      </div>
      ${applied
        ? html`<p class="alert alert-ok" data-landed-applied><strong>A landed cost is applied to this receipt.</strong> It is in Landed cost, and the cost of these goods includes it.</p>${noteAt(model, "receipt")}`
        : attachForm(model, rc)}
    </section>
  `;
}

function attachForm(model: ReceivingModel, rc: PurchasingReceipt): TemplateResult {
  const usd = model.attachCurrency === "USD";
  return html`
    <h3 class="sub">Landed cost for ${rc.grn_no}</h3>
    <p class="dim">Freight, duty and handling that followed this truck. One per receipt; it is applied at once.</p>
    <div class="forms" data-attach>
      ${segs("Bill currency", "attach-currency", [{ id: "CAD", word: "Books currency" }, { id: "USD", word: "USD" }], model.attachCurrency)}
      ${usd ? html`<label>Exchange rate to the books' currency<input type="text" inputmode="decimal" autocomplete="off" data-field="at-fx" data-at-fx /></label>` : nothing}
      <label>Freight${usd ? " (USD)" : ""}<input type="text" inputmode="decimal" autocomplete="off" data-field="at-freight" data-at-freight /></label>
      <label>Duty${usd ? " (USD)" : ""}<input type="text" inputmode="decimal" autocomplete="off" data-field="at-duty" data-at-duty /></label>
      <label>Handling${usd ? " (USD)" : ""}<input type="text" inputmode="decimal" autocomplete="off" data-field="at-handling" data-at-handling /></label>
      ${segs("Spread across the lines", "attach-basis", BASES.map((b) => ({ id: b, word: basisWord(b) })), model.attachBasis)}
    </div>
    ${noteAt(model, "receipt")}
    ${model.confirm?.kind === "attach"
      ? confirmBox(model.confirm.words, "Apply the landed cost", "do-attach", model.busy)
      : html`<div class="actions end"><button type="button" class="btn btn-primary" data-action="ask-attach" ?disabled=${model.busy}>Apply a landed cost</button></div>`}
  `;
}

function documentAndSends(model: ReceivingModel, po: PurchasingPODetail): TemplateResult {
  const sendable = po.status !== "DRAFT" && po.status !== "CANCELLED";
  return html`
    <h3 class="sub">The order document</h3>
    <p class="dim">What the vendor is sent: the order with its lines and prices.</p>
    <div class="actions" data-document>
      <button type="button" class="btn btn-secondary small-btn" data-action="download-csv" ?disabled=${model.busy}>Download as a spreadsheet</button>
      <button type="button" class="btn btn-secondary small-btn" data-action="download-html" ?disabled=${model.busy}>Download to print</button>
    </div>
    <h3 class="sub">Sent to the vendor</h3>
    ${model.sends.length === 0
      ? html`<p class="dim" data-no-sends>This order has not been sent.</p>`
      : html`
          <div class="scroll">
            <table class="stack" data-sends>
              <thead><tr><th>Sent</th><th>To</th><th>How it was chosen</th><th>Result</th></tr></thead>
              <tbody>
                ${model.sends.map((s) => {
                  const out = sendOutcome(s);
                  return html`<tr data-send-row>
                    <td data-label="Sent">${whenWords(s.sent_at, model.today)}</td>
                    <td data-label="To">${s.recipient}</td>
                    <td data-label="How it was chosen">${s.recipient_kind === "VENDOR_CONTACT" ? "Tagged ordering contact" : "Address on the vendor record"}</td>
                    <td data-label="Result">${pill(out.tone, s.status === "DELIVERED" ? "Delivered" : s.status === "QUEUED" ? "Queued" : "Failed")}${s.detail === undefined || s.detail === "" ? nothing : html`<span class="small dim">${s.detail}</span>`}</td>
                  </tr>`;
                })}
              </tbody>
            </table>
          </div>
        `}
    ${model.sendResult === null ? nothing : html`<p class="alert ${model.sendResult.tone === "good" ? "alert-ok" : "alert-error"}" role="status" data-send-result>${model.sendResult.words}</p>`}
    ${noteAt(model, "send")}
    ${model.confirm?.kind === "send" || model.confirm?.kind === "send-fallback"
      ? confirmBox(model.confirm.words, model.confirm.kind === "send" ? "Send the order" : "Send to the address on file", model.confirm.kind === "send" ? "do-send" : "do-send-fallback", model.busy)
      : html`<div class="actions end">
          ${sendable
            ? html`<button type="button" class="btn btn-primary" data-action="ask-send" ?disabled=${model.busy}>Send to the vendor</button>`
            : html`<span class="dim" data-not-sendable>A draft or cancelled order is not sent.</span>`}
        </div>`}
  `;
}

function orderPanel(model: ReceivingModel, po: PurchasingPODetail): TemplateResult {
  const expected = whenWords(po.expected_at, model.today);
  return html`
    <section class="panel" aria-label=${`Order ${po.po_no}`} data-order-panel>
      <div class="panel-head">
        <h2>${po.po_no}</h2>
        <div class="badge-row">${pill(statusTone(po.status), statusWord(po.status))}<button type="button" class="btn btn-secondary small-btn" data-action="close-order">Close</button></div>
      </div>
      <dl class="facts">
        <dt>Vendor</dt><dd>${po.vendor_name}</dd>
        <dt>Branch</dt><dd>${model.branchNames.get(po.branch_id) ?? "Your branch"}</dd>
        <dt>Expected</dt><dd>${expected === "" ? "no date" : expected}</dd>
        <dt>Arrives</dt><dd>${deliveryWord(po.delivery_method)}</dd>
        ${model.hidden ? nothing : html`<dt>Order value</dt><dd data-order-value>${formatMoney(po.subtotal_cents)}</dd>`}
      </dl>
      ${model.hidden ? html`<p class="foot dim" data-costs-hidden>Cost is hidden for your role by the purchasing policy, so no price or value shows here. Quantities do.</p>` : nothing}
      <h3 class="sub">Lines</h3>
      ${linesTable(model, po)}
      ${receiveBlock(model, po)}
      ${receiptsBlock(model, po)}
      ${model.receipt === null ? nothing : receiptPanel(model, model.receipt)}
      ${documentAndSends(model, po)}
    </section>
  `;
}

export function renderReceiving(model: ReceivingModel): TemplateResult {
  if (model.phase === "loading") return loadingNote("Reading the orders to receive.");
  if (model.phase === "error") return loadFailed(model.error);
  return html`
    ${areaHead("Receiving", "Goods arriving against placed orders: count them in, attach the freight that followed them, send the order to the vendor.")}
    ${noticeBox(model.notice)}
    <div class="split split-rev">
      <div>${queue(model)}</div>
      <div>${model.po === null
        ? html`<section class="panel" data-order-empty>${emptyState("Pick an order.", "Its lines, its receipts and its document open here.")}</section>`
        : orderPanel(model, model.po)}</div>
    </div>
  `;
}

