// The sales seat's Incoming screen: the arrivals, the words the buyers' register uses, nothing to
// press but Refresh. Ruling R1: the seat is read only.
import { html, nothing, type TemplateResult } from "lit";
import { areaHead, emptyState, errorNote, loadingNote } from "../purch/purch-views.js";
import { deliveryWord } from "../po/po-logic.js";

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
                  ${model.rows.map((r) => row(r))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function row(r: SalesIncomingRow): TemplateResult {
  return html`
    <tr data-sales-incoming-row>
      <td><span class="who">${r.po}</span><span class="small dim">${r.vendor}</span></td>
      <td>${r.expected === "" ? html`<span class="nodate">no date</span>` : html`<span class="who">${r.expected}</span>`}</td>
      <td>${deliveryWord(r.how)}</td>
      <td class="num">${r.lines}</td>
    </tr>
  `;
}
