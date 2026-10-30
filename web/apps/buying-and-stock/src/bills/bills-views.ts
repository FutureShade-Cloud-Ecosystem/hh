// The bills area's screens, as plain render functions of a model: the list of vendor bills with
// the match state beside each, a bill with its three way match line by line (the server's figures,
// said in words), the form that enters a bill against an order, and the match rules. They hold no
// state and own no events. Every figure on a screen is the server's, formatted: the app computes no
// money and no variance. Controls a seat would be refused are not offered; the note says who does.
//
// References: the Money Desk's cards and tables, and the original app's bills and match panels,
// drawn to HH's design system.
import { html, nothing, type TemplateResult } from "lit";
import type { PurchasingBill, PurchasingBillDetail, PurchasingBillLine, PurchasingPO, PurchasingPODetail, PurchasingPolicy, PurchasingTolerance } from "@hardscapehouse/api-client";
import { areaHead, confirmBox, dateOnly, emptyState, errorNote, loadFailed, loadingNote, noticeBox, pill, segs } from "../purch/purch-views.js";
import { formatMoney, formatQty } from "../purch/purch-money.js";
import { relativeDay } from "../po/po-logic.js";
import { billStatusTone, billStatusWord, FALLBACK, matchTone, matchWord, priceVarianceWords, qtyVarianceWords, toleranceWords, VISIBILITY } from "./bills-logic.js";

export type BillsView = "list" | "enter" | "detail";
export type BillConfirm = "post" | "accept" | "tolerance" | "policy" | null;

export interface BillsModel {
  readonly phase: "loading" | "ready" | "error";
  readonly view: BillsView;
  readonly bills: readonly PurchasingBill[];
  readonly statusFilter: string;
  readonly matchFilter: string;
  readonly billable: readonly PurchasingPO[];
  readonly branchNames: ReadonlyMap<string, string>;
  /** The order the entry form is working from, read in full. */
  readonly entryPo: PurchasingPODetail | null;
  readonly entryReading: boolean;
  readonly entryCurrency: string;
  readonly entryExtraSupplyKind: string;
  readonly detail: PurchasingBillDetail | null;
  /** The unit of each order line the open bill is against, by order line id (a bill line carries none). */
  readonly lineUoms: ReadonlyMap<string, string>;
  readonly confirm: { readonly kind: BillConfirm; readonly words: string } | null;
  readonly tolerance: PurchasingTolerance | null;
  readonly policy: PurchasingPolicy | null;
  readonly ruleVisibility: string;
  readonly ruleFallback: string;
  readonly canWrite: boolean;
  readonly canSetRules: boolean;
  readonly busy: boolean;
  readonly error: string;
  readonly errorAt: "bill" | "rules";
  readonly notice: string;
  readonly today: string;
}

const STATUS_FILTERS = [
  { id: "", word: "All" },
  { id: "DRAFT", word: "Drafts" },
  { id: "POSTED", word: "Posted" },
  { id: "VOID", word: "Void" },
] as const;

const MATCH_FILTERS = [
  { id: "", word: "Any match" },
  { id: "MATCHED", word: "Matched" },
  { id: "VARIANCE", word: "Variance" },
  { id: "SHORT_BILLED", word: "Short billed" },
  { id: "UNMATCHED", word: "Unmatched" },
] as const;

export function orderOptionText(po: PurchasingPO): string {
  return `${po.po_no} · ${po.vendor_name}`;
}

const noteAt = (model: BillsModel, at: "bill" | "rules"): TemplateResult => (model.errorAt === at ? errorNote(model.error) : html`${nothing}`);

// ---- the list ------------------------------------------------------------------------------------

function tiles(model: BillsModel): TemplateResult {
  const count = (pick: (b: PurchasingBill) => boolean): number => model.bills.filter(pick).length;
  return html`
    <div class="tiles tiles-3">
      <section class="tile" data-tile="to-post" aria-label="Drafts to post">
        <span class="kicker">Drafts to post</span>
        <span class="figure">${count((b) => b.status === "DRAFT")}</span>
        <span class="small">entered, not yet owed</span>
      </section>
      <section class="tile ${count((b) => b.status === "DRAFT" && b.match_state === "VARIANCE") > 0 ? "tile-hot" : ""}" data-tile="variance" aria-label="Drafts in variance">
        <span class="kicker">In variance</span>
        <span class="figure ${count((b) => b.status === "DRAFT" && b.match_state === "VARIANCE") > 0 ? "stop" : ""}">${count((b) => b.status === "DRAFT" && b.match_state === "VARIANCE")}</span>
        <span class="small">outside the match tolerance, waiting for a decision</span>
      </section>
      <section class="tile" data-tile="posted" aria-label="Posted">
        <span class="kicker">Posted</span>
        <span class="figure">${count((b) => b.status === "POSTED")}</span>
        <span class="small">owed to the vendor</span>
      </section>
    </div>
  `;
}

function filterRow(label: string, action: string, choices: readonly { id: string; word: string }[], current: string): TemplateResult {
  return html`<div class="filters" role="group" aria-label=${label}>
    ${choices.map((f) => html`<button type="button" class="tab" data-action=${action} data-choice=${f.id} aria-current="${current === f.id ? "true" : "false"}">${f.word}</button>`)}
  </div>`;
}

function billRow(model: BillsModel, b: PurchasingBill): TemplateResult {
  const due = dateOnly(b.due_date);
  const rel = due === "" ? "" : relativeDay(due, model.today);
  return html`
    <tr data-bill-row data-bill-no=${b.bill_no}>
      <td class="full" data-label="Bill"><button type="button" class="link-inline" data-action="open-bill" data-bill-id=${b.id}>${b.bill_no}</button><span class="small dim">${b.vendor_name}</span></td>
      <td data-label="Order">${b.po_no}</td>
      <td class="opt2" data-label="Due">${due === "" ? html`<span class="dim">no due date</span>` : html`${rel === "" ? nothing : html`<span class="who">${rel}</span>`}<span class="small dim">${due}</span>`}</td>
      <td class="num" data-label="Total">${formatMoney(b.total_cents)}</td>
      <td data-label="Three way match">${pill(matchTone(b.match_state), matchWord(b.match_state))}</td>
      <td data-label="Status">${pill(billStatusTone(b.status), billStatusWord(b.status))}</td>
    </tr>
  `;
}

function list(model: BillsModel): TemplateResult {
  return html`
    ${tiles(model)}
    <section class="panel" aria-label="Vendor bills" data-bill-list>
      <div class="panel-head">
        <h3>Vendor bills</h3>
        <div class="actions">
          <button type="button" class="btn btn-secondary small-btn" data-action="refresh" ?disabled=${model.busy}>Refresh</button>
          ${model.canWrite ? html`<button type="button" class="btn btn-primary" data-action="enter-bill" ?disabled=${model.busy}>Enter a bill</button>` : nothing}
        </div>
      </div>
      ${filterRow("Show bills", "status-filter", STATUS_FILTERS, model.statusFilter)}
      ${filterRow("Show by match", "match-filter", MATCH_FILTERS, model.matchFilter)}
      ${model.bills.length === 0
        ? emptyState(model.statusFilter === "" && model.matchFilter === "" ? "No vendor bills yet." : "No bill matches that.", "A vendor's invoice entered against an order shows here, matched line by line against what was ordered and received.")
        : html`
            <div class="scroll">
              <table class="stack">
                <thead><tr><th>Bill</th><th>Order</th><th class="opt2">Due</th><th class="num">Total</th><th>Three way match</th><th>Status</th></tr></thead>
                <tbody>${model.bills.map((b) => billRow(model, b))}</tbody>
              </table>
            </div>
          `}
      ${model.canWrite ? nothing : html`<p class="foot dim" data-bills-note>Entering and posting a vendor bill is for Payables and Receivables. You can read the bills and the three way match here.</p>`}
    </section>
    ${rules(model)}
  `;
}

// ---- the match rules ------------------------------------------------------------------------------------

function rules(model: BillsModel): TemplateResult {
  const t = model.tolerance;
  const p = model.policy;
  return html`
    <section class="panel" aria-label="Match rules" data-rules>
      <div class="panel-head"><h3>Match rules</h3></div>
      ${t === null ? html`<p class="dim">The match tolerance could not be read.</p>` : html`<p data-tolerance-words>${toleranceWords(t)}</p>`}
      ${p === null
        ? html`<p class="dim">The purchasing policy could not be read.</p>`
        : html`<p data-policy-words>Cost is visible to ${VISIBILITY.find((v) => v.id === p.cost_visibility)?.word.toLowerCase() ?? p.cost_visibility}. An order with nobody tagged for ordering: ${FALLBACK.find((f) => f.id === p.untagged_fallback)?.word.toLowerCase() ?? p.untagged_fallback}.</p>`}
      ${model.canSetRules && t !== null && p !== null ? ruleForms(model, t) : html`<p class="foot dim" data-rules-note>The rules are set by the super admin only.</p>`}
    </section>
  `;
}

function ruleForms(model: BillsModel, t: PurchasingTolerance): TemplateResult {
  return html`
    <h3 class="sub">Change the tolerance</h3>
    <div class="forms">
      <label>Quantity, percent<input type="text" inputmode="decimal" autocomplete="off" data-field="tol-qty" data-tol-qty /></label>
      <label>Price, percent<input type="text" inputmode="decimal" autocomplete="off" data-field="tol-price" data-tol-price /></label>
      <label>Price allowance, dollars<input type="text" inputmode="decimal" autocomplete="off" data-field="tol-abs" data-tol-abs /></label>
    </div>
    <p class="foot dim">Each percentage is at most 25. ${t.is_branch_override ? "This changes the branch rule." : "This changes the dealer default."}</p>
    ${model.confirm?.kind === "tolerance"
      ? confirmBox(model.confirm.words, "Change the tolerance", "do-tolerance", model.busy)
      : html`<div class="actions end"><button type="button" class="btn btn-secondary" data-action="ask-tolerance" ?disabled=${model.busy}>Change the tolerance</button></div>`}
    <h3 class="sub">Change the policy</h3>
    <div class="forms">
      ${segs("Who sees cost", "rule-visibility", VISIBILITY, model.ruleVisibility)}
      ${segs("Nobody tagged for ordering", "rule-fallback", FALLBACK, model.ruleFallback)}
    </div>
    ${model.confirm?.kind === "policy"
      ? confirmBox(model.confirm.words, "Change the policy", "do-policy", model.busy)
      : html`<div class="actions end"><button type="button" class="btn btn-secondary" data-action="ask-policy" ?disabled=${model.busy}>Change the policy</button></div>`}
    ${noteAt(model, "rules")}
  `;
}

// ---- entering a bill ---------------------------------------------------------------------------------------

function entry(model: BillsModel): TemplateResult {
  const po = model.entryPo;
  const usd = model.entryCurrency === "USD";
  return html`
    <section class="panel" aria-label="Enter a bill" data-bill-entry>
      <div class="panel-head">
        <h2>Enter a vendor bill</h2>
        <button type="button" class="btn btn-secondary small-btn" data-action="back" ?disabled=${model.busy}>Back to the list</button>
      </div>
      <p class="dim">The vendor's invoice, against one order. It is matched line by line when it is entered, and nothing is owed until it is posted.</p>
      <div class="forms">
        <label class="full-span">Purchase order
          <select data-field="bill-po" data-bill-po>
            <option>Pick an order</option>
            ${model.billable.map((o) => html`<option>${orderOptionText(o)}</option>`)}
          </select>
        </label>
        <label>Vendor's invoice number<input type="text" autocomplete="off" data-field="bill-no" data-bill-no-field /></label>
        <label>Bill date<input type="text" inputmode="numeric" autocomplete="off" data-field="bill-date" data-bill-date /><span class="dim">Year, month, day. Blank is today.</span></label>
        <label>Due date<input type="text" inputmode="numeric" autocomplete="off" data-field="bill-due" data-bill-due /><span class="dim">Year, month, day. Leave it blank for no due date.</span></label>
        ${segs("Bill currency", "bill-currency", [{ id: "CAD", word: "Books currency" }, { id: "USD", word: "USD" }], model.entryCurrency)}
        ${usd ? html`<label>Vendor's exchange rate<input type="text" inputmode="decimal" autocomplete="off" data-field="bill-fx" data-bill-fx /><span class="dim">The rate on the invoice, not the order's.</span></label>` : nothing}
        <label>Tax on the invoice${usd ? " (USD)" : ""}<input type="text" inputmode="decimal" autocomplete="off" data-field="bill-tax" data-bill-tax /></label>
      </div>
      ${po === null
        ? html`<p class="dim" data-pick-first>${model.entryReading ? "Reading the order." : "Pick the order: its lines are billed here."}</p>`
        : html`
            <h3 class="sub">Lines</h3>
            <p class="dim">Write what the vendor billed. Leave a quantity blank to leave the line off. The quantity starts at what has arrived and not been billed; the price at the order's.</p>
            <div class="scroll">
              <table class="stack" data-entry-lines>
                <thead><tr><th>SKU</th><th>Product</th><th class="num">Ordered</th><th class="num">Received</th><th class="num">Billed so far</th><th>Billed now</th><th>Unit price${usd ? " (USD)" : ""}</th></tr></thead>
                <tbody>
                  ${po.lines.map(
                    (l) => html`<tr data-entry-line>
                      <td class="sku" data-label="SKU">${l.sku}</td>
                      <td class="full" data-label="Product">${l.description}</td>
                      <td class="num" data-label="Ordered">${formatQty(l.qty_ordered_base, l.uom)}</td>
                      <td class="num" data-label="Received">${formatQty(l.qty_received_base, l.uom)}</td>
                      <td class="num" data-label="Billed so far">${formatQty(l.qty_billed_base, l.uom)}</td>
                      <td data-label="Billed now"><input type="text" inputmode="decimal" autocomplete="off" aria-label=${`Billed now: ${l.sku}`} data-field=${`bq-${l.id}`} data-bq /></td>
                      <td data-label="Unit price"><input type="text" inputmode="decimal" autocomplete="off" aria-label=${`Unit price: ${l.sku}`} data-field=${`bc-${l.id}`} data-bc /></td>
                    </tr>`,
                  )}
                </tbody>
              </table>
            </div>
          `}
      <h3 class="sub">Line with no purchase order line</h3>
      <p class="dim">A line not on the purchase order (such as subcontractor work or site labour). Mark whether it is goods or services for T5018.</p>
      <div class="forms" data-extra-line>
        <label>Line description<input type="text" autocomplete="off" data-field="extra-desc" data-extra-desc /></label>
        <label>Quantity billed<input type="text" inputmode="decimal" autocomplete="off" data-field="extra-qty" data-extra-qty /></label>
        <label>Unit price${usd ? " (USD)" : ""}<input type="text" inputmode="decimal" autocomplete="off" data-field="extra-cost" data-extra-cost /></label>
        ${segs("Goods or services", "bill-extra-supply-kind", [{ id: "", word: "Not marked" }, { id: "GOODS", word: "Goods" }, { id: "SERVICES", word: "Services" }], model.entryExtraSupplyKind, "Not marked counts as services on the T5018 report until someone marks it.")}
      </div>
      ${noteAt(model, "bill")}
      <div class="actions end">
        <button type="button" class="btn btn-secondary" data-action="back" ?disabled=${model.busy}>Cancel</button>
        <button type="button" class="btn btn-primary" data-action="save-bill" ?disabled=${model.busy || model.entryReading}>Enter the bill</button>
      </div>
    </section>
  `;
}

// ---- the bill and its match --------------------------------------------------------------------------------------

function matchLine(l: PurchasingBillLine, uom: string): TemplateResult {
  return html`
    <tr data-match-line data-match-state=${l.match_state}>
      <td class="full" data-label="Line">${l.sku === undefined || l.sku === "" ? "" : html`<span class="who sku">${l.sku}</span>`}${l.description}</td>
      <td class="num" data-label="Billed">${formatQty(l.qty_billed_base, uom)}</td>
      <td class="num" data-label="Ordered">${formatQty(l.qty_ordered_base, uom)}</td>
      <td class="num" data-label="Received">${formatQty(l.qty_received_base, uom)}</td>
      <td class="num" data-label="Order price">${formatMoney(l.po_unit_cost_cents)}</td>
      <td class="num" data-label="Billed price">${formatMoney(l.unit_cost_cents)}</td>
      <td class="num" data-label="Line total">${formatMoney(l.line_total_cents)}</td>
      <td class="full" data-label="Result">${pill(matchTone(l.match_state), matchWord(l.match_state))}
        <span class="small dim" data-qty-variance>${qtyVarianceWords(l.qty_variance_base, uom)}</span>
        <span class="small dim" data-price-variance>${priceVarianceWords(l.price_variance_cents)}</span></td>
    </tr>
  `;
}

function postActions(model: BillsModel, bill: PurchasingBillDetail): TemplateResult {
  if (model.confirm?.kind === "post") return confirmBox(model.confirm.words, "Post the bill", "do-post", model.busy);
  if (model.confirm?.kind === "accept") {
    return html`
      <div class="confirm" role="group" aria-label="Accept the variance" data-confirm>
        <p data-confirm-words><strong>This bill is outside the tolerance.</strong> ${model.confirm.words}</p>
        <label>Why you are accepting it
          <textarea data-field="accept-note" data-accept-note></textarea>
        </label>
        <div class="actions">
          <button type="button" class="btn btn-secondary" data-action="confirm-cancel" ?disabled=${model.busy}>Go back</button>
          <button type="button" class="btn btn-danger" data-action="do-accept" ?disabled=${model.busy}>Accept the variance and post</button>
        </div>
      </div>
    `;
  }
  return html`
    <div class="actions">
      <button type="button" class="btn btn-secondary" data-action="back" ?disabled=${model.busy}>Back to the list</button>
      ${model.canWrite && bill.status === "DRAFT" ? html`<button type="button" class="btn btn-primary" data-action="ask-post" ?disabled=${model.busy}>Post the bill</button>` : nothing}
    </div>
  `;
}

function detail(model: BillsModel, bill: PurchasingBillDetail): TemplateResult {
  const usd = bill.currency === "USD";
  const uomOf = (l: PurchasingBillLine): string => model.lineUoms.get(l.po_line_id ?? "") ?? "";
  return html`
    <section class="panel" aria-label=${`Bill ${bill.bill_no}`} data-bill-detail>
      <div class="panel-head">
        <h2>Bill ${bill.bill_no}</h2>
        <div class="badge-row">${pill(billStatusTone(bill.status), billStatusWord(bill.status))}${pill(matchTone(bill.match_state), `Match: ${matchWord(bill.match_state)}`)}</div>
      </div>
      <dl class="facts" data-facts>
        <dt>Vendor</dt><dd>${bill.vendor_name}</dd>
        <dt>Order</dt><dd>${bill.po_no}</dd>
        <dt>Branch</dt><dd>${model.branchNames.get(bill.branch_id) ?? "Your branch"}</dd>
        <dt>Bill date</dt><dd>${dateOnly(bill.bill_date)}</dd>
        <dt>Due</dt><dd>${dateOnly(bill.due_date) === "" ? "no due date" : dateOnly(bill.due_date)}</dd>
        ${usd ? html`<dt>Currency</dt><dd>USD at ${bill.fx_rate}</dd>` : nothing}
        <dt>Subtotal</dt><dd data-subtotal>${formatMoney(bill.subtotal_cents)}</dd>
        <dt>Tax</dt><dd>${formatMoney(bill.tax_cents)}</dd>
        <dt>Total</dt><dd data-total>${formatMoney(bill.total_cents)}</dd>
        ${bill.status === "POSTED" ? html`<dt>Posted</dt><dd>${dateOnly(bill.posted_at)}</dd>` : nothing}
        ${bill.variance_accepted_by === undefined || bill.variance_accepted_by === "" ? nothing : html`<dt>Variance</dt><dd data-accepted>Accepted: ${bill.variance_accepted_note === undefined || bill.variance_accepted_note === "" ? "no note" : bill.variance_accepted_note}</dd>`}
      </dl>
      <h3 class="sub">Three way match</h3>
      <p class="dim">Each line is compared with what was ordered and what has arrived, within the match tolerance. Each result is decided when the bill is entered, and run again when you post against what has arrived by then.</p>
      <div class="scroll">
        <table class="stack" data-match>
          <thead><tr><th>Line</th><th class="num">Billed</th><th class="num">Ordered</th><th class="num">Received</th><th class="num">Order price</th><th class="num">Billed price</th><th class="num">Line total</th><th>Result</th></tr></thead>
          <tbody>${bill.lines.map((l) => matchLine(l, uomOf(l)))}</tbody>
        </table>
      </div>
      ${bill.instalments.length === 0
        ? html`<p class="foot dim" data-no-instalments>Due in full on its due date.</p>`
        : html`<h3 class="sub">Instalments</h3>
            <div class="scroll"><table class="stack" data-instalments>
              <thead><tr><th>Due</th><th class="num">Amount</th><th>State</th><th>Note</th></tr></thead>
              <tbody>${bill.instalments.map((i) => html`<tr><td data-label="Due">${dateOnly(i.due_date)}</td><td class="num" data-label="Amount">${formatMoney(i.amount_cents)}</td><td data-label="State">${i.status}</td><td data-label="Note">${i.note}</td></tr>`)}</tbody>
            </table></div>`}
      ${model.canWrite || bill.status !== "DRAFT" ? nothing : html`<p class="foot dim" data-bills-note>Posting a vendor bill is for Payables and Receivables.</p>`}
      ${noteAt(model, "bill")}
      ${postActions(model, bill)}
    </section>
  `;
}

export function renderBills(model: BillsModel): TemplateResult {
  if (model.phase === "loading") return loadingNote("Reading the vendor bills.");
  if (model.phase === "error") return loadFailed(model.error);
  return html`
    ${areaHead("Vendor bills", "What vendors have billed, matched line by line against what was ordered and what arrived.")}
    ${noticeBox(model.notice)}
    ${model.view === "enter" ? entry(model) : model.view === "detail" && model.detail !== null ? detail(model, model.detail) : list(model)}
  `;
}
