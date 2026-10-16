// The Incoming area's screens, as plain render functions of a model: the card
// of every line still to arrive, soonest promised first. They hold no state and
// own no events: every control carries a data-action name and the desk acts on
// it. Every figure on the screen is the server's, formatted.
//
// Reference: the change 5 board bs-incoming-arrivals (FB Design, "HH Work
// (production)", page Buying and Stock), drawn to HH's design system.
import { html, type TemplateResult } from "lit";
import { areaHead, emptyState, errorNote, loadFailed, loadingNote } from "../purch/purch-views.js";
import { deliveryWord, whenWords } from "../po/po-logic.js";
import { formatQty } from "../po/po-money.js";
import type { ArrivalRow } from "./incoming-logic.js";

export interface IncomingModel {
  readonly phase: "loading" | "ready" | "error";
  readonly rows: readonly ArrivalRow[];
  readonly busy: boolean;
  readonly error: string;
  /** Today, YYYY-MM-DD in the reader's own day. */
  readonly today: string;
}

function arrivalRow(model: IncomingModel, row: ArrivalRow): TemplateResult {
  const words = whenWords(row.expected === "" ? undefined : row.expected, model.today);
  return html`
    <tr data-incoming-row data-po-no=${row.poNo}>
      <td data-label="PO"><span class="who">${row.poNo}</span></td>
      <td data-label="Vendor">${row.vendor}</td>
      <td class="full" data-label="Product">${row.product}</td>
      <td class="num" data-label="Quantity">${formatQty(row.qtyBase, row.uom)}</td>
      <td data-label="Expected">${words === "" ? html`<span class="nodate">no date</span>` : html`<span class="who" data-expected>${words}</span>`}</td>
      <td data-label="How">${deliveryWord(row.method)}</td>
    </tr>
  `;
}

export function renderIncoming(model: IncomingModel): TemplateResult {
  if (model.phase === "loading") return loadingNote("Reading what is on order.");
  if (model.phase === "error") return loadFailed(model.error);
  return html`
    ${areaHead("Incoming", "What the yard is waiting on: every placed order's lines still to arrive, soonest promised first.")}
    ${errorNote(model.error)}
    <section class="panel" aria-label="Incoming" data-incoming>
      <div class="panel-head">
        <h3>Incoming</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="refresh" ?disabled=${model.busy}>Refresh</button>
      </div>
      ${model.rows.length === 0
        ? emptyState("Nothing is on order.", "An order placed with a vendor shows here with its promised date.")
        : html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>PO</th>
                    <th>Vendor</th>
                    <th>Product</th>
                    <th class="num">Quantity</th>
                    <th>Expected</th>
                    <th>How</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.rows.map((row) => arrivalRow(model, row))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

// The sales seat's Incoming screen: the arrivals, the words the buyers' register uses, nothing to
// press but Refresh. Ruling R1: the seat is read only.
export interface SalesIncomingRow {
  readonly po: string;
  readonly vendor: string;
  readonly expected: string;
  readonly how: string | null | undefined;
  readonly lines: number;
  readonly justPlaced: boolean;
}

export interface SalesIncomingModel {
  readonly phase: "loading" | "ready";
  readonly error: string;
  readonly busy: boolean;
  readonly rows: readonly SalesIncomingRow[];
}

export function renderSalesIncoming(model: SalesIncomingModel): TemplateResult {
  if (model.phase === "loading") return loadingNote("Reading what is on order.");
  return html`
    ${areaHead("Incoming", "What is on order with the vendors, and when it is promised.")}
    <section class="panel" aria-label="Incoming" data-sales-incoming>
      <div class="panel-head">
        <h3>Incoming</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="refresh">Refresh</button>
      </div>
      ${errorNote(model.error)}
      ${model.rows.length === 0
        ? emptyState("Nothing is on order.", "An order a buyer places with a vendor shows here with its promised date.")
        : html`
            <div class="scroll">
              <table>
                <thead>
                  <tr>
                    <th>PO</th>
                    <th>Expected</th>
                    <th>How</th>
                    <th class="num">Lines</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.rows.map((r) => salesRow(r))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function salesRow(r: SalesIncomingRow): TemplateResult {
  return html`
    <tr data-sales-incoming-row>
      <td><span class="who">${r.po}</span><span class="small dim">${r.vendor}</span></td>
      <td>${r.expected === "" ? html`<span class="nodate">no date</span>` : html`<span class="who">${r.expected}</span>`}</td>
      <td>${deliveryWord(r.how)}</td>
      <td class="num">${r.lines}</td>
    </tr>
  `;
}
