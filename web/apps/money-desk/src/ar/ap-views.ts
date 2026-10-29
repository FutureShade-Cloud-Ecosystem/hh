// The Payables board: the open AP by payee, a payee's open items, and the empty state.
//
// It is the Receivables aging's sibling: the same head, the same panel, the same table with
// its figures right aligned, the same pills, the same empty state and the same as of control.
// Nothing is restyled for it; the desk's own components carry it.
//
// Every figure is the server's, formatted. The board never adds a bucket up and never takes a
// figure of its own: the route's row totals and the route's book totals are what show.
import { html, nothing, type TemplateResult } from "lit";
import { PAYABLES_BANDS, isLandedCostRow, landedCostWords, rowItems, rowKey, type PayablesBoardModel, type PayablesView, type PurchasingAPAgingItem, type PurchasingAPAgingPage, type PurchasingAPAgingRow } from "./ap-logic.js";
import { asOfDay, bucketWord, daysLateWord, daysPastDue, pastDueMarkWord, relativeDay, statusTone, statusWord, type Mark } from "./ar-logic.js";
import { formatMoney } from "./ar-money.js";
import { markPill } from "./ar-views.js";
import { businessNumberIsMissing, businessNumberWords, t5018EmptyKind, t5018Rows, type T5018Model } from "./t5018-logic.js";

/** One band of the board's five. */
type Band = (typeof PAYABLES_BANDS)[number];

/**
 * The band's signal class: the desk's own class for that age, the one the Receivables aging tile
 * carries (tile-late for 61 to 90 days, tile-escalation for over 90 days). The board uses the
 * desk's class, so both screens colour the same age the same way.
 */
function bandClass(band: Band, base: string): string {
  return band.signal === "" ? base : `${base} ${band.signal}`;
}

/**
 * A band cell's class: the signal rides the money that is there, never a zero. A column of red
 * zeros says nothing, so a cell with no money in it keeps the plain ink.
 */
function bandCellClass(band: Band, cents: number): string {
  return cents === 0 ? "num" : bandClass(band, "num");
}

/**
 * The status a document wears, the register's own vocabulary: a document past its due day says
 * the day count, a part paid bill says so, an open one says open. A landed cost is a type and
 * rides the document's own line, never this column.
 */
function statusMark(item: PurchasingAPAgingItem, late: number): Mark {
  if (late > 0) return { word: pastDueMarkWord(late), tone: "stop", dashed: false };
  if (item.paid_cents > 0) return { word: statusWord("PARTIALLY_PAID").toLowerCase(), tone: statusTone("PARTIALLY_PAID"), dashed: false };
  return { word: statusWord("OPEN").toLowerCase(), tone: statusTone("OPEN"), dashed: false };
}

/**
 * The document's own line: what the document is. A landed cost is a type and rides here, beside
 * the charge it was for; a bill on a carrier is the carrier's own invoice, so it says so.
 */
function documentKindWord(item: PurchasingAPAgingItem, onCarrier: boolean): string {
  if (item.is_landed_cost) {
    const kind = landedCostWords(item.charge_kind)[0];
    return kind === undefined ? "Landed cost" : `Landed cost · ${kind}`;
  }
  const bill = onCarrier ? "Carrier bill" : "Bill";
  return item.paid_cents > 0 ? `${bill} · ${formatMoney(item.paid_cents)} paid` : bill;
}

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
      ${PAYABLES_BANDS.map((band) => html`<td class="${bandCellClass(band, row[band.field])}" data-band-cell="${band.key}" data-band-label="${band.word}">${formatMoney(row[band.field])}</td>`)}
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
              <span class="dim">Nothing is owed to a vendor, a carrier or a broker on ${asOfDay(page.as_of)}.</span>
            </div>
          `
        : html`
            <div class="scroll">
              <table class="stack payaging" data-payables-aging>
                <thead>
                  <tr>
                    <th>Payee</th>
                    ${PAYABLES_BANDS.map((band) => html`<th class="${bandClass(band, "num")}" data-band-head="${band.key}">${band.word}</th>`)}
                    <th class="num">Open total</th>
                  </tr>
                </thead>
                <tbody>
                  ${rows.map((row) => payeeRow(row))}
                </tbody>
                <tfoot>
                  <tr>
                    <td><b>Open AP</b></td>
                    ${PAYABLES_BANDS.map((band) => html`<td class="${bandCellClass(band, page[band.field])}" data-band-total="${band.key}" data-band-label="${band.word}">${formatMoney(page[band.field])}</td>`)}
                    <td class="num"><b data-payables-total>${formatMoney(page.total_cents)}</b></td>
                  </tr>
                </tfoot>
              </table>
            </div>
          `}
      ${rows.length === 0
        ? nothing
        : html`<p class="dim small foot" data-payables-foot>As of ${asOfDay(page.as_of)} · ${payeesOwe(rows.length)}.</p>`}
    </section>
  `;
}

// ---- a payee's open items ----------------------------------------------------------------------

/** One open document: a bill, or a landed cost line to a carrier or a broker. */
function payeeLineRow(item: PurchasingAPAgingItem, asOf: string, onCarrier: boolean): TemplateResult {
  const late = daysPastDue(item.due_date, asOf);
  return html`
    <tr data-line-row data-doc-kind="${item.kind}">
      <td>
        <span class="who">${item.ref_no}</span>
        <span class="dim small">${documentKindWord(item, onCarrier)}</span>
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
        <span class="marks">${markPill(statusMark(item, late))}</span>
      </td>
      <td class="num">${formatMoney(item.open_cents)}</td>
    </tr>
  `;
}

function payablesPayee(page: PurchasingAPAgingPage, row: PurchasingAPAgingRow): TemplateResult {
  const carrier = isLandedCostRow(row);
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
            ${rowItems(row).map((item) => payeeLineRow(item, page.as_of, carrier))}
          </tbody>
          <tfoot>
            <tr>
              <td colspan="3"><b>Open total</b> <span class="dim small">as of ${asOfDay(page.as_of)}</span></td>
              <td class="num"><b data-payee-lines-total>${formatMoney(row.total_cents)}</b></td>
            </tr>
          </tfoot>
        </table>
      </div>
      <p class="dim small foot">Landed costs are owed to the carrier or broker, not to the supplier.</p>
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
        <p class="dim">Open payables by payee, most overdue first. Freight, duty and brokerage are listed under the carrier or broker they are owed to.</p>
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

// ---- the T5018 view ----------------------------------------------------------------------------

/**
 * The Payables area's own view nav: the open AP board and the T5018 year report. The area stands
 * beside the receivables desk in the Money Desk's area switch, so this nav is the way from one of
 * the area's own views to the other.
 */
export function payablesViewNav(view: PayablesView): TemplateResult {
  const tab = (id: PayablesView, word: string): TemplateResult =>
    html`<button type="button" class="tab" data-action="payables-view" data-view="${id}" aria-current="${view === id ? "true" : "false"}">${word}</button>`;
  return html`
    <nav class="tabs" aria-label="Payables views">
      ${tab("aging", "Open AP")} ${tab("t5018", "T5018")}
    </nav>
  `;
}

/**
 * The T5018 view: the year's payments to the vendors the Financial Controller has marked as
 * subcontractors, which is what a CRA Form T5018 is filled from.
 *
 * The year is the DEALER'S year, so the picker is a year and not a date, and the figures are the
 * route's: the rows, the paid amounts and the total are what the server sent.
 */
function t5018Panel(model: T5018Model): TemplateResult {
  const report = model.report;
  const rows = report === null ? [] : t5018Rows(report);
  const emptyKind = report === null ? null : t5018EmptyKind(report);
  return html`
    <section class="panel" aria-label="T5018 subcontractor payments">
      <div class="panel-head">
        <h3>T5018 subcontractor payments</h3>
        <div class="tools">
          <label>Year<input type="number" min="1970" max="9999" step="1" data-field="t5018-year" aria-label="The year to report on" /></label>
          <button type="button" class="btn btn-secondary" data-action="t5018-csv" ?disabled=${model.downloading || emptyKind !== null}>
            ${model.downloading ? "Preparing the file" : "Download CSV"}
          </button>
        </div>
      </div>
      ${model.state === "loading"
        ? html`<p class="dim" role="status">Loading the year.</p><div class="ghost"></div><div class="ghost"></div>`
        : model.state === "error" || report === null
          ? html`
              <p class="alert alert-error" role="alert" data-error>${model.error}</p>
              <div class="actions"><button type="button" class="btn btn-secondary" data-action="t5018-reload">Try again</button></div>
            `
          : emptyKind === "none-marked"
            ? html`
                <div class="empty" data-t5018-empty="none-marked">
                  <strong>No vendor is marked yet.</strong>
                  <span class="dim">A vendor is marked as a subcontractor on its own record in Buying and Stock.</span>
                </div>
              `
            : html`
                ${emptyKind === "none-paid"
                  ? html`<div class="empty" data-t5018-empty="none-paid">
                      <strong>Nothing was paid to marked vendors in ${report.year}.</strong>
                      <span class="dim">The marked vendors are listed below with their CRA business numbers.</span>
                    </div>`
                  : nothing}
                <div class="scroll">
                  <table class="stack t5018" data-t5018-table>
                    <thead>
                      <tr><th>Vendor</th><th>CRA business number</th><th class="num">Paid in ${report.year}</th></tr>
                    </thead>
                    <tbody>
                      ${rows.map(
                        (row) => html`
                          <tr data-t5018-row data-vendor-id="${row.vendor_id}">
                            <td>
                              <span class="who">${row.name}</span>
                              <span class="dim small">${row.vendor_no}</span>
                            </td>
                            <td data-t5018-number data-label="CRA business number">${businessNumberIsMissing(row)
                              ? html`<span class="pill pill-watch" data-t5018-missing>${businessNumberWords(row)}</span>`
                              : businessNumberWords(row)}</td>
                            <td class="num" data-t5018-paid>${formatMoney(row.paid_cents)}</td>
                          </tr>
                        `,
                      )}
                    </tbody>
                    <tfoot>
                      <tr>
                        <td colspan="2"><b>Paid to subcontractors in ${report.year}</b></td>
                        <td class="num"><b data-t5018-total>${formatMoney(report.total_cents)}</b></td>
                      </tr>
                    </tfoot>
                  </table>
                </div>
              `}
      ${model.notice === "" ? nothing : html`<p class="dim small foot" data-t5018-notice>${model.notice}</p>`}
    </section>
  `;
}

/**
 * The T5018 view: the panel above, under the area's own head. The year the report answers is the
 * route's, so the screen shows the route's year and never a year of its own.
 */
export function renderT5018(model: T5018Model): TemplateResult {
  return html`
    <div class="head">
      <div>
        <h1>T5018</h1>
        <p class="dim">What the dealer paid its subcontractors in a calendar year, for the CRA's Form T5018. Only the vendors the Financial Controller has marked are on it.</p>
      </div>
    </div>
    ${t5018Panel(model)}
  `;
}
