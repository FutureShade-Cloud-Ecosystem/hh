// The landed cost area's screens, as plain render functions of a model: the list of landed costs,
// the capture form (freight, duty and handling against an order that has received goods), and the
// allocation (the split across the received lines, a basis to compare, and the apply, retro and
// void steps). They hold no state and own no events. Every figure on a screen is the server's,
// formatted: the app computes no money.
//
// References: the Money Desk screens in design-refs/money-desk (cards border first, no shadow,
// tabular figures), the procurement desk's own panels, and the original app's landed-cost list,
// capture and allocate screens, drawn to HH's design system.
import { html, nothing, type TemplateResult } from "lit";
import type {
  LandedCost,
  LandedCostAllocationPreview,
  LandedCostDetail,
  PurchasingPO,
  PurchasingPODetail,
} from "@hardscapehouse/api-client";
import { areaHead, checkBox, confirmBox, dateOnly, emptyState, errorNote, loadFailed, loadingNote, noticeBox, pill, segs } from "../purch/purch-views.js";
import { formatMoney, formatQty } from "../purch/purch-money.js";
import { BASES, basisFigure, basisWord, confirmWords, retroCandidates, retroLines, statusTone, statusWord, type Action } from "./landed-logic.js";

export type LandedView = "list" | "capture" | "detail";

export interface CaptureModel {
  /** The order picked, read in full (its lines name the products and carry what was received). */
  readonly po: PurchasingPODetail | null;
  /** True while the picked order's lines are being read. */
  readonly reading: boolean;
  readonly currency: string;
  readonly basis: string;
  readonly excludeSpecial: boolean;
  readonly retro: boolean;
}

export interface DetailModel {
  readonly doc: LandedCostDetail;
  /** The live split under the compared basis; null while it is read or when the core refused it. */
  readonly preview: LandedCostAllocationPreview | null;
  readonly previewError: string;
  /** The basis the preview shows: the saved one until a person compares another. */
  readonly compare: string;
  readonly po: PurchasingPODetail | null;
}

export interface LandedModel {
  readonly phase: "loading" | "ready" | "error";
  readonly view: LandedView;
  readonly items: readonly LandedCost[];
  readonly filter: string;
  readonly orders: readonly PurchasingPO[];
  readonly branchNames: ReadonlyMap<string, string>;
  readonly branchCount: number;
  readonly capture: CaptureModel;
  readonly detail: DetailModel | null;
  readonly confirm: Action | null;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
}

const FILTERS: readonly { id: string; word: string }[] = [
  { id: "", word: "All" },
  { id: "DRAFT", word: "Drafts" },
  { id: "POSTED", word: "Applied" },
  { id: "RETRO_ADJUSTED", word: "Retro adjusted" },
  { id: "VOIDED", word: "Voided" },
];

function orderOption(po: PurchasingPO): string {
  return `${po.po_no} · ${po.vendor_name}`;
}

/** The option text the order picker lists for an order; the desk resolves the typed text back through it. */
export const orderOptionText = orderOption;

/** The option text the "follows" picker lists for an applied landed cost. */
export function priorOptionText(p: LandedCost): string {
  return `${p.po_ref} · ${basisWord(p.allocation_basis)} · ${dateOnly(p.created_at)}`;
}

// ---- the list ------------------------------------------------------------------------------------

function tiles(model: LandedModel): TemplateResult {
  const count = (status: string): number => model.items.filter((i) => i.status === status).length;
  const retro = model.items.filter((i) => i.retro_of_id !== undefined && i.retro_of_id !== "").length;
  return html`
    <div class="tiles tiles-3">
      <section class="tile" data-tile="drafts" aria-label="Drafts waiting">
        <span class="kicker">Drafts waiting</span>
        <span class="figure">${count("DRAFT")}</span>
        <span class="small">captured, not yet applied to the cost of the goods</span>
      </section>
      <section class="tile" data-tile="applied" aria-label="Applied">
        <span class="kicker">Applied</span>
        <span class="figure">${count("POSTED") + count("RETRO_ADJUSTED")}</span>
        <span class="small">${retro} of them late freight</span>
      </section>
      <section class="tile" data-tile="voided" aria-label="Voided">
        <span class="kicker">Voided</span>
        <span class="figure">${count("VOIDED")}</span>
        <span class="small">reversed, the cost taken back off</span>
      </section>
    </div>
  `;
}

function filters(model: LandedModel): TemplateResult {
  return html`
    <div class="filters" role="group" aria-label="Show landed costs">
      ${FILTERS.map(
        (f) => html`<button type="button" class="tab" data-action="filter" data-choice=${f.id} aria-current="${model.filter === f.id ? "true" : "false"}">${f.word}</button>`,
      )}
    </div>
  `;
}

function listRow(model: LandedModel, lc: LandedCost): TemplateResult {
  const retro = lc.retro_of_id !== undefined && lc.retro_of_id !== "";
  return html`
    <tr data-landed-row data-landed-id=${lc.id}>
      <td class="full" data-label="Order"><button type="button" class="link-inline" data-action="open" data-landed-id=${lc.id}>${lc.po_ref === "" ? "(no order)" : lc.po_ref}</button>
        <span class="small dim">${model.branchNames.get(lc.branch_id) ?? ""}${retro ? " · late freight" : ""}</span></td>
      <td data-label="Status">${pill(statusTone(lc.status), statusWord(lc.status))}</td>
      <td data-label="Basis">${basisWord(lc.allocation_basis)}</td>
      <td data-label="Bill in">${lc.currency === "USD" ? html`USD at ${lc.fx_rate}` : "Books currency"}</td>
      <td class="num" data-label="Freight">${formatMoney(lc.freight_cad_cents)}</td>
      <td class="num" data-label="Duty">${formatMoney(lc.duty_cad_cents)}</td>
      <td class="num" data-label="Handling">${formatMoney(lc.handling_cad_cents)}</td>
      <td class="opt2" data-label="Captured">${dateOnly(lc.created_at)}</td>
    </tr>
  `;
}

function list(model: LandedModel): TemplateResult {
  return html`
    ${tiles(model)}
    <section class="panel" aria-label="Landed costs" data-landed-list>
      <div class="panel-head">
        <h3>Landed costs</h3>
        <div class="actions">
          <button type="button" class="btn btn-secondary small-btn" data-action="refresh" ?disabled=${model.busy}>Refresh</button>
          <button type="button" class="btn btn-primary" data-action="new" ?disabled=${model.busy}>New landed cost</button>
        </div>
      </div>
      ${filters(model)}
      ${model.items.length === 0
        ? emptyState(
            model.filter === "" ? "No landed costs yet." : "None match that filter.",
            "Freight, duty and handling that follow received goods are captured here and added to what the goods cost.",
          )
        : html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Order</th>
                    <th>Status</th>
                    <th>Basis</th>
                    <th>Bill in</th>
                    <th class="num">Freight</th>
                    <th class="num">Duty</th>
                    <th class="num">Handling</th>
                    <th class="opt2">Captured</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.items.map((lc) => listRow(model, lc))}
                </tbody>
              </table>
            </div>
          `}
      <p class="foot dim">Figures are in the books' currency, as the core converted them. A draft changes nothing until it is applied.</p>
    </section>
  `;
}

// ---- the capture form ------------------------------------------------------------------------------------

function onHandFields(po: PurchasingPODetail): TemplateResult {
  const lines = retroLines(po);
  return html`
    <div class="sub" data-retro-fields>
      <h3>Still on hand</h3>
      <p class="dim">For each line that was received, how many are still on hand. The share for goods already sold goes straight to cost of goods sold.</p>
      <div class="forms">
        ${lines.map(
          (l) => html`<label>${l.sku} · ${l.description} (${formatQty(l.qty_received_base, l.uom)} received)
            <input type="text" inputmode="decimal" autocomplete="off" data-field=${`onhand-${l.id}`} data-onhand />
          </label>`,
        )}
      </div>
    </div>
  `;
}

function capture(model: LandedModel): TemplateResult {
  const cap = model.capture;
  const options = model.orders;
  const usd = cap.currency === "USD";
  const priors = cap.po === null ? [] : retroCandidates(model.items, cap.po.id);
  return html`
    <section class="panel" aria-label="New landed cost" data-landed-capture>
      <div class="panel-head">
        <h2>New landed cost</h2>
        <button type="button" class="btn btn-secondary small-btn" data-action="back" ?disabled=${model.busy}>Back to the list</button>
      </div>
      <p class="dim">Freight, duty and handling that followed an order's goods. Saved as a draft: nothing is added to the cost of the goods until it is applied.</p>
      <div class="forms">
        <label>Purchase order
          <select data-field="lc-po" data-lc-po>
            <option>Pick an order</option>
            ${options.map((po) => html`<option>${orderOption(po)}</option>`)}
          </select>
          ${options.length === 0 ? html`<span class="dim" data-no-orders>No order has received goods yet, so there is nothing for freight to follow.</span>` : nothing}
        </label>
        ${segs("Bill currency", "currency", [{ id: "CAD", word: "Books currency" }, { id: "USD", word: "USD" }], cap.currency)}
        ${usd
          ? html`<label>Exchange rate to the books' currency
              <input type="text" inputmode="decimal" autocomplete="off" data-field="lc-fx" data-lc-fx />
              <span class="dim">Entered by hand, as the vendor's bill was converted. The core converts each amount with it.</span>
            </label>`
          : nothing}
        <label>Freight${usd ? " (USD)" : ""}
          <input type="text" inputmode="decimal" autocomplete="off" data-field="lc-freight" data-lc-freight />
        </label>
        <label>Duty${usd ? " (USD)" : ""}
          <input type="text" inputmode="decimal" autocomplete="off" data-field="lc-duty" data-lc-duty />
        </label>
        <label>Handling${usd ? " (USD)" : ""}
          <input type="text" inputmode="decimal" autocomplete="off" data-field="lc-handling" data-lc-handling />
        </label>
        ${segs("Spread across the lines", "basis", BASES.map((b) => ({ id: b, word: basisWord(b) })), cap.basis)}
      </div>
      <div class="sub">
        ${checkBox("Leave special buys out: they take no share", "toggle-special", cap.excludeSpecial)}
        ${checkBox("This is late freight that follows a landed cost already applied", "toggle-retro", cap.retro)}
      </div>
      ${cap.retro
        ? html`
            <div class="forms">
              <label>The landed cost it follows
                <select data-field="lc-retro-of" data-lc-retro-of>
                  <option>Pick a landed cost</option>
                  ${priors.map((p) => html`<option>${priorOptionText(p)}</option>`)}
                </select>
                ${cap.po !== null && priors.length === 0 ? html`<span class="dim" data-no-priors>This order has no applied landed cost to follow.</span>` : nothing}
              </label>
            </div>
            ${cap.po === null ? html`<p class="dim">Pick the order first: its received lines are counted here.</p>` : onHandFields(cap.po)}
          `
        : nothing}
      ${errorNote(model.error)}
      <div class="actions end">
        <button type="button" class="btn btn-secondary" data-action="back" ?disabled=${model.busy}>Cancel</button>
        <button type="button" class="btn btn-primary" data-action="save" ?disabled=${model.busy || cap.reading}>Save as a draft</button>
      </div>
    </section>
  `;
}

// ---- the allocation ----------------------------------------------------------------------------------------

function productWords(po: PurchasingPODetail | null, poLineId: string, productId: string): { sku: string; name: string } {
  const line = po?.lines.find((l) => l.id === poLineId) ?? po?.lines.find((l) => l.product_id === productId);
  return line === undefined ? { sku: productId.slice(0, 8), name: "" } : { sku: line.sku, name: line.description };
}

function allocationTable(detail: DetailModel): TemplateResult {
  const doc = detail.doc;
  const applied = doc.status !== "DRAFT";
  if (applied) {
    return html`
      <div class="scroll">
        <table class="stack" data-allocations>
          <thead><tr><th>SKU</th><th>Product</th><th class="num">Split by</th><th class="num">Added to cost</th><th class="num">Sold through</th></tr></thead>
          <tbody>
            ${doc.allocations.map((a) => {
              const p = productWords(detail.po, a.po_line_id, a.product_id);
              return html`<tr data-allocation-row>
                <td class="sku" data-label="SKU">${p.sku}</td>
                <td class="full" data-label="Product">${p.name}</td>
                <td class="num" data-label="Split by">${basisFigure(doc.allocation_basis, a.basis_weight)}</td>
                <td class="num" data-label="Added to cost">${formatMoney(a.allocated_cad_cents)}</td>
                <td class="num" data-label="Sold through">${formatMoney(a.sold_through_cad_cents)}</td>
              </tr>`;
            })}
          </tbody>
          <tfoot><tr class="total"><td colspan="3" class="num dim">Landed cost</td><td class="num" data-label="Landed cost" data-total>${formatMoney(detail.preview?.total_cad_cents)}</td><td></td></tr></tfoot>
        </table>
      </div>
    `;
  }
  const preview = detail.preview;
  return preview === null
    ? html`<p class="dim" role="status" data-loading>${detail.previewError === "" ? "Reading the split." : detail.previewError}</p>`
    : html`
        <div class="scroll">
          <table class="stack" data-allocations>
            <thead><tr><th>SKU</th><th>Product</th><th class="num">Split by</th><th class="num">Share</th></tr></thead>
            <tbody>
              ${preview.lines.map((l) => {
                const p = productWords(detail.po, l.po_line_id, l.product_id);
                return html`<tr data-allocation-row>
                  <td class="sku" data-label="SKU">${p.sku}</td>
                  <td class="full" data-label="Product">${p.name}${l.is_special_buy ? html` <span class="status status-plain">Special buy: takes no share</span>` : nothing}</td>
                  <td class="num" data-label="Split by">${basisFigure(preview.basis, l.basis_weight)}</td>
                  <td class="num" data-label="Share">${formatMoney(l.allocated_cad_cents)}</td>
                </tr>`;
              })}
            </tbody>
            <tfoot><tr class="total"><td colspan="3" class="num dim">Landed cost</td><td class="num" data-label="Landed cost" data-total>${formatMoney(preview.total_cad_cents)}</td></tr></tfoot>
          </table>
        </div>
      `;
}

function actions(model: LandedModel, detail: DetailModel): TemplateResult {
  const doc = detail.doc;
  const retro = doc.retro_of_id !== undefined && doc.retro_of_id !== "";
  if (model.confirm !== null) {
    const words = confirmWords(model.confirm, doc, detail.preview?.total_cad_cents);
    const label = model.confirm === "void" ? "Void it" : model.confirm === "retro" ? "Apply late freight" : "Apply it";
    return confirmBox(words, label, `do-${model.confirm}`, model.busy);
  }
  return html`
    <div class="actions">
      <button type="button" class="btn btn-secondary" data-action="back" ?disabled=${model.busy}>Back to the list</button>
      ${doc.status === "DRAFT" && !retro
        ? html`<button type="button" class="btn btn-primary" data-action="ask-apply" ?disabled=${model.busy || detail.preview === null}>Apply</button>`
        : nothing}
      ${doc.status === "DRAFT" && retro
        ? html`<button type="button" class="btn btn-primary" data-action="ask-retro" ?disabled=${model.busy || detail.preview === null}>Apply late freight</button>`
        : nothing}
      ${doc.status === "POSTED" || doc.status === "ALLOCATED" || doc.status === "RETRO_ADJUSTED"
        ? html`<button type="button" class="btn btn-danger" data-action="ask-void" ?disabled=${model.busy}>Void</button>`
        : nothing}
    </div>
  `;
}

function detailView(model: LandedModel, detail: DetailModel): TemplateResult {
  const doc = detail.doc;
  const usd = doc.currency === "USD";
  const retro = doc.retro_of_id !== undefined && doc.retro_of_id !== "";
  return html`
    <section class="panel" aria-label="Landed cost" data-landed-detail>
      <div class="panel-head">
        <h2>${doc.po_ref === "" ? "Landed cost" : `Landed cost on ${doc.po_ref}`}</h2>
        ${pill(statusTone(doc.status), statusWord(doc.status))}
      </div>
      <div class="detail-grid">
        <div>
          <h3>Split across the received lines</h3>
          ${doc.status === "DRAFT"
            ? html`
                <div class="compare" role="group" aria-label="Compare how it spreads">
                  ${BASES.map(
                    (b) => html`<button type="button" class="seg" data-action="compare" data-choice=${b} aria-pressed="${detail.compare === b ? "true" : "false"}">${basisWord(b)}</button>`,
                  )}
                </div>
                <p class="dim" data-compare-note>${detail.compare === doc.allocation_basis
                  ? `Applying uses the basis saved on this draft: ${basisWord(doc.allocation_basis).toLowerCase()}.`
                  : `Comparing ${basisWord(detail.compare).toLowerCase()}. Applying still uses the saved basis, ${basisWord(doc.allocation_basis).toLowerCase()}; the draft cannot be changed.`}</p>
              `
            : nothing}
          ${allocationTable(detail)}
        </div>
        <dl class="facts" data-facts>
          <dt>Order</dt><dd data-po-ref>${doc.po_ref === "" ? "(none)" : doc.po_ref}</dd>
          <dt>Branch</dt><dd>${model.branchNames.get(doc.branch_id) ?? "Your branch"}</dd>
          <dt>Spread</dt><dd>${basisWord(doc.allocation_basis)}</dd>
          <dt>Special buys</dt><dd>${doc.exclude_special_buys ? "Left out" : "Take a share"}</dd>
          <dt>Freight</dt><dd>${formatMoney(doc.freight_cad_cents)}${usd ? html` <span class="dim">(USD ${formatMoney(doc.freight_cents)})</span>` : nothing}</dd>
          <dt>Duty</dt><dd>${formatMoney(doc.duty_cad_cents)}${usd ? html` <span class="dim">(USD ${formatMoney(doc.duty_cents)})</span>` : nothing}</dd>
          <dt>Handling</dt><dd>${formatMoney(doc.handling_cad_cents)}${usd ? html` <span class="dim">(USD ${formatMoney(doc.handling_cents)})</span>` : nothing}</dd>
          ${usd ? html`<dt>Exchange rate</dt><dd>${doc.fx_rate}</dd>` : nothing}
          ${retro ? html`<dt>Follows</dt><dd>Late freight on an applied landed cost</dd>` : nothing}
          <dt>Captured</dt><dd>${dateOnly(doc.created_at)}</dd>
        </dl>
      </div>
      ${doc.status === "DRAFT"
        ? html`<p class="foot dim" data-draft-note>A draft cannot be deleted or voided; leave it, or apply it. Apply it only when the split above is right.</p>`
        : nothing}
      ${errorNote(model.error)}
      ${actions(model, detail)}
    </section>
  `;
}

// ---- the area -------------------------------------------------------------------------------------------------------

export function renderLanded(model: LandedModel): TemplateResult {
  if (model.phase === "loading") return loadingNote("Reading the landed costs and the orders.");
  if (model.phase === "error") return loadFailed(model.error);
  return html`
    ${areaHead("Landed cost", "Freight, duty and handling that follow received goods, added to what the goods cost.")}
    ${noticeBox(model.notice)}
    ${model.view === "capture" ? capture(model) : model.view === "detail" && model.detail !== null ? detailView(model, model.detail) : list(model)}
  `;
}
