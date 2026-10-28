// The Payables board: the open AP by payee, a payee's open items, and the empty state.
//
// It is the Receivables aging's sibling: the same head, the same panel, the same table with
// its figures right aligned, the same pills, the same empty state and the same as of control.
// Nothing is restyled for it; the desk's own components carry it.
//
// Every figure is the server's, formatted. The board never adds a bucket up and never takes a
// figure of its own: the route's row totals and the route's book totals are what show.
import { html, nothing, type TemplateResult } from "lit";
import { PAYABLES_BANDS, isLandedCostRow, landedCostWords, rowItems, rowKey, type PayablesBoardModel, type PurchasingAPAgingItem, type PurchasingAPAgingPage, type PurchasingAPAgingRow } from "./ap-logic.js";
import { bucketWord, daysLateWord, daysPastDue, relativeDay } from "./ar-logic.js";
import { formatMoney } from "./ar-money.js";

// ---- the aging list ----------------------------------------------------------------------------

/** How many payees owe money, said so the way a person says it. */
function payeesOwe(count: number): string {
  if (count === 0) return "no payee owes money";
  if (count === 1) return "1 payee owes money";
  return `${count} payees owe money`;
}

/** One payee's row: the name, the five bands, and the open total the server sent. */
function payeeRow(row: PurchasingAPAgingRow): TemplateResult {
  const key = rowKey(row);
  return html`
    <tr data-payee-row data-payee-id="${key}">
      <td>
        <button type="button" class="link-row" data-action="payables-open" data-payee-id="${key}"><span class="who">${row.vendor_name}</span></button>
        <span class="dim small">${isLandedCostRow(row) ? "Landed cost carrier" : `Net ${row.payment_terms_days}`}</span>
      </td>
      ${PAYABLES_BANDS.map((band) => html`<td class="num" data-band-cell="${band.key}" data-band-label="${band.word}">${formatMoney(row[band.field])}</td>`)}
      <td class="num"><b data-payee-total>${formatMoney(row.total_cents)}</b></td>
    </tr>
  `;
}

/**
 * The rows, the most overdue first. The desk's Receivables aging sorts its own rows the same
 * way, and the board keeps that rule: the route's order is not the screen's order. A row's due
 * date is the earliest one it holds, so the oldest date is the most overdue, and a row with no
 * due date reads last.
 */
function boardRows(page: PurchasingAPAgingPage): readonly PurchasingAPAgingRow[] {
  return [...page.items].sort((a, b) => {
    const aDue = a.due_date ?? "";
    const bDue = b.due_date ?? "";
    if (aDue === "" || bDue === "") {
      if (aDue !== bDue) return aDue === "" ? 1 : -1;
      return a.vendor_name.localeCompare(b.vendor_name);
    }
    return aDue.localeCompare(bDue) || a.vendor_name.localeCompare(b.vendor_name);
  });
}

function payablesAging(page: PurchasingAPAgingPage): TemplateResult {
  const rows = boardRows(page);
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
                    ${PAYABLES_BANDS.map((band) => html`<td class="num" data-band-total="${band.key}" data-band-label="${band.word}">${formatMoney(page[band.field])}</td>`)}
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
function payeeLineRow(item: PurchasingAPAgingItem, asOf: string): TemplateResult {
  const landed = item.is_landed_cost;
  const kind = landedCostWords(item.charge_kind)[0] ?? "Landed cost";
  const late = daysPastDue(item.due_date, asOf);
  return html`
    <tr data-line-row data-doc-kind="${item.kind}">
      <td>
        <span class="who">${item.ref_no}</span>
        <span class="dim small">${landed ? kind : item.paid_cents > 0 ? `Bill · ${formatMoney(item.paid_cents)} paid` : "Bill"}</span>
      </td>
      <td class="nowrap due-col">
        <div class="due-line">${relativeDay(item.due_date, asOf)}</div>
        ${late > 0
          ? html`
              <div class="late-line" data-testid="overdue-line">
                <span class="late-age">${daysLateWord(late)}</span>
                <span class="bucket-pill" data-bucket-key="${item.bucket}" data-testid="bucket-pill">${bucketWord(item.bucket, "")}</span>
              </div>
            `
          : nothing}
      </td>
      <td>
        <span class="marks">
          ${landed
            ? html`<span class="pill pill-info" data-landed-cost>Landed cost</span>`
            : item.paid_cents > 0
              ? html`<span class="pill pill-watch">Part paid</span>`
              : html`<span class="pill pill-plain">Open</span>`}
        </span>
      </td>
      <td class="num">${formatMoney(item.open_cents)}</td>
    </tr>
  `;
}

function payablesPayee(page: PurchasingAPAgingPage, row: PurchasingAPAgingRow): TemplateResult {
  return html`
    <button type="button" class="link back" data-action="payables-back">‹ Payables</button>
    <div class="head">
      <div>
        <h1>${row.vendor_name}</h1>
        <p class="dim meta">
          ${isLandedCostRow(row) ? "Landed cost carrier" : "Vendor"} · Net ${row.payment_terms_days}
          ${isLandedCostRow(row) ? html`<span class="pill pill-info" data-landed-cost>Landed cost</span>` : nothing}
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
            ${rowItems(row).map((item) => payeeLineRow(item, page.as_of))}
          </tbody>
          <tfoot>
            <tr>
              <td colspan="3"><b>Open total</b> <span class="dim small">as of ${page.as_of}</span></td>
              <td class="num"><b data-payee-lines-total>${formatMoney(row.total_cents)}</b></td>
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
 * The board: the open AP by payee, the day it is read as of, and the row whose open items are
 * open. The board is an area of the Money Desk in its own right, beside the receivables desk, so
 * it carries no view nav of that desk's: the area switch above it is the way in.
 */
export function renderPayables(board: PayablesBoardModel): TemplateResult {
  const page = board.page;
  if (board.openRowKey !== null && page !== null) {
    const row = page.items.find((item) => rowKey(item) === board.openRowKey);
    if (row !== undefined) return html`${payablesPayee(page, row)}`;
  }
  return html`
    <div class="head">
      <div>
        <h1>Payables</h1>
        <p class="dim">Open AP by payee, the most overdue first. A landed cost rides its own line and its own payee.</p>
      </div>
    </div>
    ${board.state === "loading"
      ? html`<section class="panel" aria-busy="true"><p class="dim" role="status">Loading open AP.</p><div class="ghost"></div><div class="ghost"></div><div class="ghost"></div></section>`
      : board.state === "error" || page === null
        ? html`
            <section class="panel">
              <p class="alert alert-error" role="alert" data-error>${board.error}</p>
              <div class="actions"><button type="button" class="btn btn-secondary" data-action="payables-reload">Try again</button></div>
            </section>
          `
        : html`${payablesAging(page)}`}
  `;
}
