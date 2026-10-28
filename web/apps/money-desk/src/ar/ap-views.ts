// The Payables board: the open AP by payee, the payee's open items, and the empty state.
//
// It is the Receivables aging's sibling: the same head, the same panel, the same table with
// its figures right aligned, the same pills, the same empty state and the same as of control.
// Nothing is restyled for it; the desk's own components carry it.
//
// Every figure is the server's, formatted. The board never adds a bucket up and never takes
// a figure of its own: the route's row totals and the route's book totals are what show.
import { html, nothing, type TemplateResult } from "lit";
import { PAYABLES_BANDS, isLandedCostPayee, landedCostWords, payeeLines, type PayablesBoardLine, type PayablesBoardPage, type PayablesBoardRow } from "./ap-logic.js";
import { bucketLabelFor, relativeDay } from "./ar-logic.js";
import { formatMoney } from "./ar-money.js";
import { tabsBar, type DeskModel } from "./ar-views.js";

// ---- the aging list ----------------------------------------------------------------------------

/** How many payees owe money, said so the way a person says it. */
function payeesOwe(count: number): string {
  if (count === 0) return "no payee owes money";
  if (count === 1) return "1 payee owes money";
  return `${count} payees owe money`;
}

/** One payee's row: the name, the five bands, and the open total the server sent. */
function payeeRow(row: PayablesBoardRow): TemplateResult {
  return html`
    <tr data-payee-row data-payee-id="${row.payee_id}">
      <td>
        <button type="button" class="link-row" data-action="payables-open" data-payee-id="${row.payee_id}"><span class="who">${row.payee_name}</span></button>
        <span class="dim small">${isLandedCostPayee(row) ? "Landed cost carrier" : `Net ${row.payment_terms_days}`}</span>
      </td>
      ${PAYABLES_BANDS.map((band) => html`<td class="num" data-band-cell="${band.key}" data-band-label="${band.word}">${formatMoney(row[band.field])}</td>`)}
      <td class="num"><b data-payee-total>${formatMoney(row.open_cents)}</b></td>
    </tr>
  `;
}

function payablesAging(model: DeskModel, page: PayablesBoardPage): TemplateResult {
  const rows = page.items;
  return html`
    <section class="panel" aria-label="Open AP aging">
      <div class="panel-head">
        <h3>Open AP aging</h3>
        <div class="tools">
          <label>As of<input type="date" data-field="payables-asof" aria-label="Read the books as of" /></label>
        </div>
      </div>
      ${rows.length === 0
        ? html`
            <div class="empty" data-payables-empty>
              <strong>No open AP as of this day.</strong>
              <span class="dim">Nothing is owed to a vendor, a carrier or a broker on ${page.as_of}.</span>
            </div>
          `
        : html`
            <div class="scroll">
              <table class="stack payaging" data-payables-aging>
                <thead>
                  <tr>
                    <th>Payee</th>
                    ${PAYABLES_BANDS.map((band) => html`<th class="num" data-band-head="${band.key}">${band.word}</th>`)}
                    <th class="num">Open total</th>
                  </tr>
                </thead>
                <tbody>
                  ${rows.map((row) => payeeRow(row))}
                </tbody>
                <tfoot>
                  <tr>
                    <td><b>Open AP</b></td>
                    ${PAYABLES_BANDS.map((band) => html`<td class="num" data-band-total="${band.key}" data-band-label="${band.word}">${formatMoney(page.bands[band.field])}</td>`)}
                    <td class="num"><b data-payables-total>${formatMoney(page.total_cents)}</b></td>
                  </tr>
                </tfoot>
              </table>
            </div>
          `}
      <p class="dim small foot" data-payables-foot>As of ${page.as_of} · ${payeesOwe(rows.length)} · a landed cost rides its own payee row, never a vendor's bill.</p>
    </section>
  `;
}

// ---- a payee's open items ----------------------------------------------------------------------

/** One open document: a bill, or a landed cost line to a carrier or a broker. */
function payeeLineRow(line: PayablesBoardLine, asOf: string): TemplateResult {
  const landed = line.doc_kind === "LANDED_COST";
  const band = bucketLabelFor(line.days_overdue);
  const kind = landedCostWords(line.landed_cost_kind)[0] ?? "Landed cost";
  return html`
    <tr data-line-row data-doc-kind="${line.doc_kind}">
      <td>
        <span class="who">${line.doc_no}</span>
        <span class="dim small">${landed ? kind : line.paid_cents > 0 ? `Bill · ${formatMoney(line.paid_cents)} paid` : "Bill"}</span>
      </td>
      <td class="nowrap due-col">
        <div class="due-line">${relativeDay(line.due_date, asOf)}</div>
        ${line.days_overdue > 0
          ? html`
              <div class="late-line" data-testid="overdue-line">
                <span class="late-age">${line.days_overdue} ${line.days_overdue === 1 ? "day" : "days"} past due</span>
                <span class="bucket-pill" data-bucket-key="${band.key}" data-testid="bucket-pill">${band.label}</span>
              </div>
            `
          : nothing}
      </td>
      <td>
        <span class="marks">
          ${landed
            ? html`<span class="pill pill-info" data-landed-cost>Landed cost</span>`
            : line.paid_cents > 0
              ? html`<span class="pill pill-watch">Part paid</span>`
              : html`<span class="pill pill-plain">Open</span>`}
        </span>
      </td>
      <td class="num">${formatMoney(line.open_cents)}</td>
    </tr>
  `;
}

function payablesPayee(model: DeskModel, page: PayablesBoardPage, row: PayablesBoardRow): TemplateResult {
  return html`
    <button type="button" class="link back" data-action="payables-back">‹ Payables</button>
    <div class="head">
      <div>
        <h1>${row.payee_name}</h1>
        <p class="dim meta">
          ${isLandedCostPayee(row) ? "Landed cost carrier" : "Vendor"} · Net ${row.payment_terms_days}
          ${isLandedCostPayee(row) ? html`<span class="pill pill-info" data-landed-cost>Landed cost</span>` : nothing}
        </p>
      </div>
    </div>
    <section class="panel" aria-label="Open items">
      <div class="panel-head"><h3>Open items</h3></div>
      <div class="scroll">
        <table class="stack paylines" data-payables-lines>
          <thead>
            <tr><th>Document</th><th>Due</th><th>Status</th><th class="num">Open amount</th></tr>
          </thead>
          <tbody>
            ${payeeLines(row).map((line) => payeeLineRow(line, page.as_of))}
          </tbody>
          <tfoot>
            <tr>
              <td colspan="3"><b>Open total</b> <span class="dim small">as of ${page.as_of}</span></td>
              <td class="num"><b data-payee-lines-total>${formatMoney(row.open_cents)}</b></td>
            </tr>
          </tfoot>
        </table>
      </div>
      <p class="dim small foot">A landed cost line is owed to its carrier or broker and never folds into a vendor's bill.</p>
    </section>
  `;
}

// ---- the board ---------------------------------------------------------------------------------

/**
 * The board: the open AP by payee, the day it is read as of, and the payee whose open items
 * are open. The desk's views nav sits above it, the way it sits above every other view of
 * this desk, and the Payables tab is the one in hand.
 */
export function renderPayables(model: DeskModel): TemplateResult {
  const board = model.payables;
  const page = board.page;
  if (board.openPayeeId !== null && page !== null) {
    const row = page.items.find((item) => item.payee_id === board.openPayeeId);
    if (row !== undefined) return html`${payablesPayee(model, page, row)}`;
  }
  return html`
    <div class="head">
      <div>
        <h1>Payables</h1>
        <p class="dim">Open AP by payee, the most overdue first. A landed cost rides its own line and its own payee.</p>
      </div>
    </div>
    ${tabsBar(model)}
    ${board.state === "loading"
      ? html`<section class="panel" aria-busy="true"><p class="dim" role="status">Loading open AP.</p><div class="ghost"></div><div class="ghost"></div><div class="ghost"></div></section>`
      : board.state === "error" || page === null
        ? html`
            <section class="panel">
              <p class="alert alert-error" role="alert" data-error>${board.error}</p>
              <div class="actions"><button type="button" class="btn btn-secondary" data-action="payables-reload">Try again</button></div>
            </section>
          `
        : html`${payablesAging(model, page)}`}
  `;
}
