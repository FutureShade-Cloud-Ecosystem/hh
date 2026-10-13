// The procurement slice's screens, as plain render functions of a model. They
// hold no state and own no events: every control is a plain element carrying a
// data-action name and the slice's click listener acts on it (the design
// system's position rules allow no @ or . binding in an app, and the only
// bound values are class, role, aria- and data- attributes). Form fields carry
// data-field names and are read when a button is pressed; none is bound, the
// desk seeds a field's starting value after the render.
//
// Every figure on a screen is the server's, formatted: the app computes no
// money. References: the procurement screens in design-refs/procurement
// (procurement home, the draft from the shortfall, placed), the Buying and
// Stock board, and the original app's purchasing-console and
// inventory-replenish screens, drawn to HH's design system.
import { html, nothing, type TemplateResult } from "lit";
import type {
  InventoryShortfallRow,
  Product,
  PurchasingPO,
  PurchasingPODetail,
} from "@hardscapehouse/api-client";
import { dateWords, deliveryWord, draftOf, isPlaced, refusalWords, relativeDay, statusTone, statusWord, type DraftMarker } from "./po-logic.js";
import { formatMoney, formatQty } from "./po-money.js";

export type Tab = "orders" | "incoming";

/** The draft being worked: the order the core made, the worklist row it came from, and the draft
 * the core said was already open when this one was pressed (null when there was none). */
export interface DraftModel {
  readonly po: PurchasingPODetail;
  readonly row: InventoryShortfallRow | null;
  readonly existingDraft: DraftMarker | null;
}

export interface DeskModel {
  readonly phase: "loading" | "ready" | "error";
  readonly tab: Tab;
  readonly shortfall: readonly InventoryShortfallRow[];
  readonly orders: readonly PurchasingPO[];
  readonly picked: string | null;
  readonly draft: DraftModel | null;
  readonly placedId: string | null;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
  /** The delivery the draft's segmented control holds ("UNKNOWN" until a way is chosen). */
  readonly method: string;
  /** The promised date as typed in the draft's field, YYYY-MM-DD or "". */
  readonly expectedText: string;
  /** Branch names by id, and vendor codes by product id, as the core's reads gave them. */
  readonly branchNames: ReadonlyMap<string, string>;
  readonly vendorCodes: ReadonlyMap<string, string>;
  /** "6 of 10 pc landed" by PO id, for the part received orders. */
  readonly landed: ReadonlyMap<string, string>;
  /** The active catalogue, the add a line picker's list; empty when the read refused. */
  readonly products: readonly Product[];
  /** The draft line being corrected, its id; null when no line is in the editor. */
  readonly editing: string | null;
  /** The add a line row is open under the draft's lines. */
  readonly adding: boolean;
  /** The refusal that answers the line editor's Save, shown beside it; "" when there is none. */
  readonly editorError: string;
  /** Today, YYYY-MM-DD in the reader's own day. */
  readonly today: string;
}

/** A placed notice reads as a bold first line and a plain second; the desk joins them with a newline. */
function notice(model: DeskModel): TemplateResult {
  if (model.notice === "") return html`${nothing}`;
  const [lead = "", ...rest] = model.notice.split("\n");
  return html`<div class="alert alert-ok" role="status" data-notice><strong>${lead}</strong>${rest.length > 0 ? html`<p>${rest.join(" ")}</p>` : nothing}</div>`;
}

function errorNote(model: DeskModel): TemplateResult {
  return model.error !== "" ? html`<p class="alert alert-error" role="alert" data-error>${model.error}</p>` : html`${nothing}`;
}

function pill(status: string): TemplateResult {
  return html`<span class="status status-${statusTone(status)}" data-status>${statusWord(status)}</span>`;
}

function dateOnly(stamp: string | undefined): string {
  return stamp === undefined || stamp === "" ? "" : stamp.slice(0, 10);
}

// ---- the way in --------------------------------------------------------------------------------

export function renderLoading(): TemplateResult {
  return html`<p class="dim" role="status">Loading the worklist and the orders.</p>`;
}

export function renderLoadFailed(error: string): TemplateResult {
  return html`
    <p class="alert alert-error" role="alert" data-error>${error}</p>
    <button type="button" class="btn btn-secondary" data-action="reload">Try again</button>
  `;
}

// ---- the head, the tabs and the tiles --------------------------------------------------------------

function head(): TemplateResult {
  return html`
    <div class="head">
      <div>
        <h1>Buying and Stock</h1>
        <p class="dim">Purchase orders and the reorder worklist. Draft, place and track the order that closes the gap.</p>
      </div>
    </div>
  `;
}

function tabs(model: DeskModel): TemplateResult {
  const tab = (id: Tab, label: string): TemplateResult => html`
    <button type="button" class="tab" data-action="tab" data-tab-id=${id} aria-current="${model.tab === id ? "true" : "false"}">${label}</button>
  `;
  return html`
    <nav class="tabs" aria-label="Buying and Stock views">
      ${tab("orders", "Purchase orders")}
      ${tab("incoming", "Incoming")}
    </nav>
  `;
}

function tiles(model: DeskModel): TemplateResult {
  const placed = model.orders.filter((o) => isPlaced(o.status));
  const ordered = placed.filter((o) => o.status === "ORDERED").length;
  const partial = placed.filter((o) => o.status === "PARTIAL").length;
  const drafts = model.orders.filter((o) => o.status === "DRAFT").length;
  const dated = placed.filter((o) => o.expected_at !== undefined && o.expected_at !== "").length;
  const short = model.shortfall.length;
  return html`
    <div class="tiles">
      <section class="tile" data-tile="open-orders" aria-label="Open purchase orders">
        <span class="kicker">Open purchase orders</span>
        <span class="figure">${placed.length}</span>
        <span class="small">${ordered} ordered, ${partial} partial</span>
        <button type="button" class="link" data-action="tab" data-tab-id="orders">Open</button>
      </section>
      <section class="tile" data-tile="drafts" aria-label="Drafts waiting">
        <span class="kicker">Drafts waiting</span>
        <span class="figure">${drafts}</span>
        <span class="small">drafted, not yet placed with the vendor</span>
        <button type="button" class="link" data-action="tab" data-tab-id="orders">Open</button>
      </section>
      <section class="tile ${short > 0 ? "tile-hot" : ""}" data-tile="below-point" aria-label="Lines below reorder point">
        <span class="kicker">Lines below reorder point</span>
        <span class="figure ${short > 0 ? "stop" : ""}">${short}</span>
        <span class="small">${short > 0 ? "worst first on the worklist" : "nothing is short"}</span>
        <button type="button" class="link" data-action="tab" data-tab-id="orders">Open the worklist</button>
      </section>
      <section class="tile" data-tile="incoming" aria-label="Incoming">
        <span class="kicker">Incoming</span>
        <span class="figure">${dated}</span>
        <span class="small">arrivals with a promised date</span>
        <button type="button" class="link" data-action="tab" data-tab-id="incoming">Open incoming</button>
      </section>
    </div>
  `;
}

// ---- the reorder worklist -----------------------------------------------------------------------------

function worklist(model: DeskModel): TemplateResult {
  const picked = model.picked;
  const count = picked === null ? 0 : 1;
  const hasHot = model.shortfall.some((r) => r.projected_base < 0);
  return html`
    <section class="panel" aria-label="Reorder worklist" data-worklist>
      <div class="panel-head">
        <h3>Reorder worklist</h3>
        <span class="dim">Every managed line whose projected position fell below its reorder point, worst first.</span>
      </div>
      ${model.shortfall.length === 0
        ? html`<div class="empty"><strong>Nothing is below its reorder point.</strong><span class="dim">A line that falls short shows here, and stays until an order is placed for it.</span></div>`
        : html`
            <div class="scroll">
              <table class="stack worklist">
                <thead>
                  <tr>
                    <th><span class="visually-hidden">Pick</span></th>
                    <th>SKU</th>
                    <th>Product</th>
                    <th class="num">On hand</th>
                    <th class="num">Reserved</th>
                    <th class="num">In transit</th>
                    <th class="num">On order</th>
                    <th class="num">Projected</th>
                    <th class="num">Point</th>
                    <th class="num">Short by</th>
                    <th class="num">Suggest</th>
                    <th>Vendor</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.shortfall.map((row) => worklistRow(row, picked === row.product_id))}
                </tbody>
              </table>
            </div>
          `}
      ${hasHot
        ? html`<p class="foot dim" data-k08-note>The highlighted row is the yard's K08 shortage: stock promised to customers that is neither on hand nor on order. A draft does not cover it; only a placed order does.</p>`
        : html`<p class="foot dim" data-k08-note>A line stays on the worklist until an order is placed for it. A draft does not cover it.</p>`}
      ${errorNote(model)}
      <div class="actions end">
        <button type="button" class="btn btn-primary" data-action="draft" ?disabled=${picked === null || model.busy}>Draft PO (${count})</button>
      </div>
    </section>
  `;
}

function worklistRow(row: InventoryShortfallRow, isPicked: boolean): TemplateResult {
  const hot = row.projected_base < 0;
  const draft = draftOf(row);
  return html`
    <tr data-shortfall-row data-sku=${row.sku} data-hot=${hot ? "true" : nothing} aria-selected="${isPicked ? "true" : "false"}" class="${hot ? "hot" : ""}">
      <td class="pick"><button type="button" class="box" role="checkbox" aria-checked="${isPicked ? "true" : "false"}" aria-label="${draft === null ? `Pick ${row.sku}` : `Drafted, ${draft.no}`}" data-action="pick" data-product-id=${row.product_id} ?disabled=${draft !== null}></button></td>
      <td class="sku" data-label="SKU">${row.sku}</td>
      <td class="full" data-label="Product">${row.product_name}${draft === null
        ? nothing
        : html`<span class="drafted"><span class="status status-watch">Drafted, not placed</span> <button type="button" class="link-inline" data-action="open-draft" data-open-draft data-po-id=${draft.id}>${draft.no}</button></span>`}</td>
      <td class="num" data-label="On hand">${formatQty(row.on_hand_base, row.uom)}</td>
      <td class="num" data-label="Reserved">${formatQty(row.reserved_base, row.uom)}</td>
      <td class="num" data-label="In transit">${formatQty(row.in_transit_base, row.uom)}</td>
      <td class="num" data-label="On order">${formatQty(row.on_order_base, row.uom)}</td>
      <td class="num ${row.projected_base < 0 ? "stop" : "good"}" data-label="Projected">${formatQty(row.projected_base, row.uom)}</td>
      <td class="num" data-label="Point">${formatQty(row.reorder_point_base, row.uom)}</td>
      <td class="num stop" data-label="Short by" data-field-short>${formatQty(row.short_base, row.uom)}</td>
      <td class="num" data-label="Suggest">${formatQty(row.suggested_qty_base, row.uom)}</td>
      <td class="full" data-label="Vendor">${row.preferred_vendor_name}</td>
    </tr>
  `;
}

// ---- the draft from the shortfall -----------------------------------------------------------------------

function methodButton(model: DeskModel, value: string, label: string): TemplateResult {
  return html`<button type="button" class="seg" data-action="method" data-method=${value} aria-pressed="${model.method === value ? "true" : "false"}">${label}</button>`;
}

function draftLineRow(model: DeskModel, line: PurchasingPODetail["lines"][number]): TemplateResult {
  const editing = model.editing === line.id;
  if (editing) {
    // The editor: the two figures a vendor's confirmation corrects, quantity and cost; the
    // product is not editable (that is a different line: remove this one and add it).
    return html`
      <tr data-draft-line data-line-editing>
        <td class="sku" data-label="SKU">${line.sku}</td>
        <td class="full" data-label="Product">${line.description}</td>
        <td class="code" data-label="Vendor code">${model.vendorCodes.get(line.product_id) ?? "(none on file)"}</td>
        <td class="num" data-label="Qty"><input type="number" min="1" step="any" inputmode="decimal" data-field="qty" data-line-qty /></td>
        <td class="num" data-label="Unit cost"><input type="number" min="0" step="0.01" inputmode="decimal" data-field="cost" data-line-cost /></td>
        <td class="num" data-label="Line" data-amount>${formatMoney(line.line_total_cents)}</td>
        <td class="pick">
          <button type="button" class="btn btn-secondary small-btn" data-action="save-line" data-line-id=${line.id} ?disabled=${model.busy}>Save</button>
          <button type="button" class="btn btn-secondary small-btn" data-action="edit-line-close" ?disabled=${model.busy}>Close</button>
          ${model.editorError === "" ? nothing : html`<p class="alert alert-error" role="alert" data-error data-line-error>${model.editorError}</p>`}
        </td>
      </tr>
    `;
  }
  return html`
    <tr data-draft-line>
      <td class="sku" data-label="SKU">${line.sku}</td>
      <td class="full" data-label="Product">${line.description}</td>
      <td class="code" data-label="Vendor code">${model.vendorCodes.get(line.product_id) ?? "(none on file)"}</td>
      <td class="num" data-label="Qty">${formatQty(line.qty_ordered_base, line.uom)}</td>
      <td class="num" data-label="Unit cost">${formatMoney(line.unit_cost_cents)}</td>
      <td class="num" data-label="Line" data-amount>${formatMoney(line.line_total_cents)}</td>
      <td class="pick">
        ${model.busy ? nothing : html`<button type="button" class="btn btn-secondary small-btn" data-action="edit-line" data-line-id=${line.id}>Change</button>`}
        <button type="button" class="btn btn-secondary small-btn" data-action="remove-line" data-line-id=${line.id} ?disabled=${model.busy}>Remove</button>
      </td>
    </tr>
  `;
}

function addLineRow(model: DeskModel): TemplateResult {
  return html`
    <tr data-add-line>
      <td colspan="7">
        <div class="addline">
          <label>Product
            <select data-field="add-product" data-add-product>
              ${model.products.map((p) => html`<option>${p.sku} ${p.name}</option>`)}
            </select>
          </label>
          <label>Qty
            <input type="number" min="1" step="any" inputmode="decimal" data-field="add-qty" data-add-qty />
          </label>
          <label>Unit cost
            <input type="number" min="0" step="0.01" inputmode="decimal" data-field="add-cost" data-add-cost />
          </label>
          <div class="addline-actions">
            <button type="button" class="btn btn-secondary small-btn" data-action="add-line-close" ?disabled=${model.busy}>Close</button>
            <button type="button" class="btn btn-primary small-btn" data-action="add-line-confirm" ?disabled=${model.busy || model.products.length === 0}>Add line</button>
          </div>
        </div>
      </td>
    </tr>
  `;
}

function draftPanel(model: DeskModel, draft: DraftModel): TemplateResult {
  const po = draft.po;
  const row = draft.row;
  const lineCount = po.lines.length;
  const editable = po.status === "DRAFT";
  const shipTo = model.branchNames.get(po.branch_id) ?? row?.branch_code ?? "Your branch";
  const words = dateWords(model.expectedText, model.today);
  return html`
    <section class="panel" aria-label="New purchase order" data-draft>
      <div class="panel-head">
        <h2>New purchase order from the shortfall</h2>
        ${pill(po.status)}
      </div>
      ${draft.existingDraft === null
        ? nothing
        : html`<p class="callout" data-existing-draft><strong>A draft was already open: <button type="button" class="link-inline" data-action="open-draft" data-open-draft data-po-id=${draft.existingDraft.id}>${draft.existingDraft.no}</button>.</strong> Drafted, not placed; a draft never covers the shortfall, so this press drafted the full quantity again.</p>`}
      <dl class="facts">
        <dt>Order</dt>
        <dd data-po-no>${po.po_no}</dd>
        <dt>Vendor</dt>
        <dd>${po.vendor_name}</dd>
        <dt>Ship to</dt>
        <dd>${shipTo}</dd>
      </dl>
      <div class="forms">
        <label>Expected arrival
          <input type="date" data-field="expected" />
          ${words === "" ? nothing : html`<span class="dim" data-date-words>${words}</span>`}
        </label>
        <div class="field" role="group" aria-label="Delivery">
          <span class="label">Delivery</span>
          <div class="segs">
            ${methodButton(model, "DELIVERY", "Vendor truck")} ${methodButton(model, "PICKUP", "Pickup")}
          </div>
        </div>
      </div>
      <h3 class="sub">Lines</h3>
      <div class="scroll">
        <table class="stack">
          <thead>
            <tr>
              <th>SKU</th>
              <th>Product</th>
              <th>Vendor code</th>
              <th class="num">Qty</th>
              <th class="num">Unit cost</th>
              <th class="num">Line</th>
              <th><span class="visually-hidden">Line actions</span></th>
            </tr>
          </thead>
          <tbody>
            ${po.lines.map((line) => draftLineRow(model, line))}
            ${model.adding && editable ? addLineRow(model) : nothing}
            ${lineCount === 0 && !model.adding ? html`<tr><td colspan="7" class="dim">No lines on this order.</td></tr>` : nothing}
          </tbody>
          ${lineCount === 0
            ? nothing
            : html`<tfoot><tr class="total"><td colspan="5" class="num dim">Order total</td><td class="num" data-label="Order total" data-draft-total>${formatMoney(po.subtotal_cents)}</td><td></td></tr></tfoot>`}
        </table>
      </div>
      ${editable
        ? html`<div class="actions">
            ${model.adding
              ? nothing
              : html`<button type="button" class="btn btn-secondary small-btn" data-action="add-line" ?disabled=${model.busy}>Add a line</button>`}
          </div>`
        : nothing}
      ${row === null
        ? nothing
        : html`<p class="foot dim" data-suggested>Suggested from the worklist: short by ${formatQty(row.short_base, row.uom)}, ordered in multiples of ${formatQty(row.order_multiple_base, row.uom)}.</p>`}
      ${errorNote(model)}
      <div class="actions end">
        <button type="button" class="btn btn-secondary" data-action="cancel-draft" ?disabled=${model.busy}>Cancel draft</button>
        <button type="button" class="btn btn-secondary" data-action="save-draft" ?disabled=${model.busy}>Save draft</button>
        <button type="button" class="btn btn-primary" data-action="place" ?disabled=${model.busy}>Place order</button>
      </div>
      <p class="callout" data-callout><strong>Place moves the order to Ordered.</strong> Receiving moves it to Partial and Received as stock lands; nothing here does.</p>
    </section>
  `;
}

// ---- the register and incoming --------------------------------------------------------------------------

function register(model: DeskModel): TemplateResult {
  const rows = model.orders;
  return html`
    <section class="panel" aria-label="Purchase orders" data-register>
      <div class="panel-head">
        <h3>Purchase orders</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="refresh">Refresh</button>
      </div>
      ${rows.length === 0
        ? html`<div class="empty"><strong>No purchase orders yet.</strong><span class="dim">Pick a line on the worklist and draft one.</span></div>`
        : html`
            <div class="scroll">
              <table>
                <thead>
                  <tr>
                    <th>PO</th>
                    <th class="num">Value</th>
                    <th>Status</th>
                  </tr>
                </thead>
                <tbody>
                  ${rows.map(
                    (po) => html`
                      <tr data-po-row aria-current="${model.placedId === po.id ? "true" : "false"}">
                        <td><span class="who" data-po-no>${po.po_no}</span><span class="small dim">${model.landed.get(po.id) ?? `${po.line_count} ${po.line_count === 1 ? "line" : "lines"}`}<span> · ${po.vendor_name}</span></span></td>
                        <td class="num">${formatMoney(po.subtotal_cents)}</td>
                        <td>${pill(po.status)}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
      <p class="foot dim">Ordered, Partial and Received are states, never buttons: receiving sets them as stock lands.</p>
    </section>
  `;
}

function incoming(model: DeskModel): TemplateResult {
  const rows = model.orders.filter((o) => isPlaced(o.status));
  return html`
    <section class="panel" aria-label="Incoming" data-incoming>
      <div class="panel-head">
        <h3>Incoming</h3>
        ${model.tab === "incoming" ? html`<button type="button" class="btn btn-secondary small-btn" data-action="refresh">Refresh</button>` : nothing}
      </div>
      ${rows.length === 0
        ? html`<div class="empty"><strong>Nothing is on order.</strong><span class="dim">An order placed with a vendor shows here with its promised date.</span></div>`
        : html`
            <div class="scroll">
              <table>
                <thead>
                  <tr>
                    <th>PO</th>
                    <th>Expected</th>
                    <th class="opt2">How</th>
                    <th class="num opt2">Lines</th>
                  </tr>
                </thead>
                <tbody>
                  ${rows.map((po) => incomingRow(model, po))}
                </tbody>
              </table>
            </div>
          `}
      <p class="foot dim">The yard plans around this list. A promised date is the buyer's statement; an order without one asks the vendor.</p>
      <p class="foot dim" data-bills-note>${refusalWords("post-bill", "Forbidden", 403)}</p>
    </section>
  `;
}

function incomingRow(model: DeskModel, po: PurchasingPO): TemplateResult {
  const date = dateOnly(po.expected_at);
  const rel = date === "" ? "" : relativeDay(date, model.today);
  return html`
    <tr data-incoming-row aria-current="${model.placedId === po.id ? "true" : "false"}">
      <td><span class="who">${po.po_no}</span><span class="small dim">${po.vendor_name}</span></td>
      <td>${date === ""
        ? html`<span class="nodate">no date</span>`
        : html`${rel === "" ? nothing : html`<span class="who">${rel}</span>`}<span class="small dim" data-expected>${date}</span>`}</td>
      <td class="opt2">${deliveryWord(po.delivery_method)}</td>
      <td class="num opt2">${po.line_count}</td>
    </tr>
  `;
}

// ---- the screens ---------------------------------------------------------------------------------------------

export function renderDesk(model: DeskModel): TemplateResult {
  if (model.phase === "loading") return renderLoading();
  if (model.phase === "error") return renderLoadFailed(model.error);
  const onHome = model.tab === "orders";
  return html`
    ${head()} ${tabs(model)} ${tiles(model)} ${notice(model)} ${onHome ? nothing : errorNote(model)}
    ${onHome
      ? html`
          <div class="home">
            <div class="home-main">${model.draft !== null ? draftPanel(model, model.draft) : worklist(model)}</div>
            <div class="home-side">${register(model)} ${incoming(model)}</div>
          </div>
        `
      : incoming(model)}
  `;
}
