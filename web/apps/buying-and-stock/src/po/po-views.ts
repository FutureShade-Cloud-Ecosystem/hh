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
  PurchasingIncomingLine,
  PurchasingPO,
  PurchasingPODetail,
} from "@hardscapehouse/api-client";
import { deliveryWord, isPlaced, refusalWords, statusTone, statusWord } from "./po-logic.js";
import { formatMoney, formatQty } from "./po-money.js";

export type Tab = "orders" | "incoming";

/** The draft being worked: the order the core made, and the worklist row it came from. */
export interface DraftModel {
  readonly po: PurchasingPODetail;
  readonly row: InventoryShortfallRow | null;
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
}

function messages(model: DeskModel): TemplateResult {
  return html`
    ${model.error !== "" ? html`<p class="alert alert-error" role="alert" data-error>${model.error}</p>` : nothing}
    ${model.notice !== "" ? html`<p class="alert alert-ok" role="status" data-notice>${model.notice}</p>` : nothing}
  `;
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
      ${tab("orders", "Purchase orders")} ${tab("incoming", "Incoming")}
      <span class="tag-later" data-later>Receiving <em>later</em></span>
      <span class="tag-later" data-later>Vendors <em>later</em></span>
      <span class="tag-later" data-later>Vendor bills <em>later</em></span>
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
        <span class="small">${ordered} ordered, ${partial} part received</span>
        <button type="button" class="link" data-action="tab" data-tab-id="orders">Open</button>
      </section>
      <section class="tile" data-tile="drafts" aria-label="Drafts waiting">
        <span class="kicker">Drafts waiting</span>
        <span class="figure">${drafts}</span>
        <span class="small">drafted, not yet placed with the vendor</span>
        <button type="button" class="link" data-action="tab" data-tab-id="orders">Open</button>
      </section>
      <section class="tile" data-tile="below-point" aria-label="Lines below reorder point">
        <span class="kicker">Lines below reorder point</span>
        <span class="figure ${short > 0 ? "stop" : ""}">${short}</span>
        <span class="small">${short > 0 ? "worst first on the worklist" : "nothing is short"}</span>
        <button type="button" class="link" data-action="tab" data-tab-id="orders">Open the worklist</button>
      </section>
      <section class="tile" data-tile="incoming" aria-label="Incoming">
        <span class="kicker">Incoming</span>
        <span class="figure">${placed.length}</span>
        <span class="small">${dated} with a promised date</span>
        <button type="button" class="link" data-action="tab" data-tab-id="incoming">Open incoming</button>
      </section>
    </div>
  `;
}

// ---- the reorder worklist -----------------------------------------------------------------------------

function worklist(model: DeskModel): TemplateResult {
  const picked = model.picked;
  const count = picked === null ? 0 : 1;
  return html`
    <section class="panel" aria-label="Reorder worklist" data-worklist>
      <div class="panel-head">
        <h3>Reorder worklist</h3>
        <span class="dim">Every managed line whose projected position fell below its reorder point, worst first.</span>
      </div>
      ${model.shortfall.length === 0
        ? html`<div class="empty"><strong>Nothing is below its reorder point.</strong><span class="dim">A line that falls short, or an order that closes its gap, shows here.</span></div>`
        : html`
            <div class="scroll">
              <table>
                <thead>
                  <tr>
                    <th><span class="visually-hidden">Pick</span></th>
                    <th>SKU</th>
                    <th>Product</th>
                    <th class="num">On hand</th>
                    <th class="num opt1">Reserved</th>
                    <th class="num opt1">In transit</th>
                    <th class="num opt2">On order</th>
                    <th class="num">Projected</th>
                    <th class="num opt1">Point</th>
                    <th class="num">Short by</th>
                    <th class="num">Suggest</th>
                    <th class="opt1">Vendor</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.shortfall.map((row) => worklistRow(row, picked === row.product_id))}
                </tbody>
              </table>
            </div>
          `}
      <p class="foot dim">A negative projected position is stock promised to customers that is neither on hand nor on order: the yard's gap. Drafting an order counts it as on order, so the line leaves the worklist.</p>
      <div class="actions end">
        <button type="button" class="btn btn-primary" data-action="draft" ?disabled=${picked === null || model.busy}>Draft PO (${count})</button>
      </div>
    </section>
  `;
}

function worklistRow(row: InventoryShortfallRow, isPicked: boolean): TemplateResult {
  const hot = row.projected_base < 0;
  return html`
    <tr data-shortfall-row data-sku=${row.sku} data-hot=${hot ? "true" : nothing} aria-selected="${isPicked ? "true" : "false"}" class="${hot ? "hot" : ""}">
      <td><button type="button" class="box" role="checkbox" aria-checked="${isPicked ? "true" : "false"}" aria-label="Pick ${row.sku}" data-action="pick" data-product-id=${row.product_id}></button></td>
      <td class="sku">${row.sku}</td>
      <td>${row.product_name}</td>
      <td class="num">${formatQty(row.on_hand_base, row.uom)}</td>
      <td class="num opt1">${formatQty(row.reserved_base, row.uom)}</td>
      <td class="num opt1">${formatQty(row.in_transit_base, row.uom)}</td>
      <td class="num opt2">${formatQty(row.on_order_base, row.uom)}</td>
      <td class="num ${row.projected_base < 0 ? "stop" : "good"}">${formatQty(row.projected_base, row.uom)}</td>
      <td class="num opt1">${formatQty(row.reorder_point_base, row.uom)}</td>
      <td class="num stop">${formatQty(row.short_base, row.uom)}</td>
      <td class="num">${formatQty(row.suggested_qty_base, row.uom)}</td>
      <td class="opt1">${row.preferred_vendor_name}</td>
    </tr>
  `;
}

// ---- the draft from the shortfall -----------------------------------------------------------------------

function draftPanel(model: DeskModel, draft: DraftModel): TemplateResult {
  const po = draft.po;
  const row = draft.row;
  const lineCount = po.lines.length;
  return html`
    <section class="panel" aria-label="New purchase order" data-draft>
      <div class="panel-head">
        <h2>New purchase order from the shortfall</h2>
        ${pill(po.status)}
      </div>
      <dl class="facts">
        <dt>Order</dt>
        <dd data-po-no>${po.po_no}</dd>
        <dt>Vendor</dt>
        <dd>${po.vendor_name}</dd>
        <dt>Ship to</dt>
        <dd>${row?.branch_code ?? "Your branch"}</dd>
      </dl>
      <div class="forms">
        <label>Expected arrival
          <input type="date" data-field="expected" />
        </label>
        <label>Delivery
          <select data-field="delivery">
            <option value="UNKNOWN">Not set</option>
            <option value="DELIVERY">Vendor truck</option>
            <option value="PICKUP">Pickup</option>
          </select>
        </label>
      </div>
      <h3 class="sub">Lines</h3>
      <div class="scroll">
        <table>
          <thead>
            <tr>
              <th>SKU</th>
              <th class="opt2">Product</th>
              <th class="num">Qty</th>
              <th class="num">Unit cost</th>
              <th class="num">Line</th>
              <th><span class="visually-hidden">Remove</span></th>
            </tr>
          </thead>
          <tbody>
            ${po.lines.map(
              (line) => html`
                <tr data-draft-line>
                  <td class="sku">${line.sku}</td>
                  <td class="opt2">${line.description}</td>
                  <td class="num">${formatQty(line.qty_ordered_base, line.uom)}</td>
                  <td class="num">${formatMoney(line.unit_cost_cents)}</td>
                  <td class="num">${formatMoney(line.line_total_cents)}</td>
                  <td><button type="button" class="btn btn-secondary small-btn" data-action="remove-line" data-line-id=${line.id} ?disabled=${model.busy}>Remove</button></td>
                </tr>
              `,
            )}
            ${lineCount === 0 ? html`<tr><td colspan="6" class="dim">No lines on this order.</td></tr>` : nothing}
          </tbody>
          ${lineCount === 0
            ? nothing
            : html`<tfoot><tr><td colspan="4" class="num dim">Order total</td><td class="num" data-draft-total>${formatMoney(po.subtotal_cents)}</td><td></td></tr></tfoot>`}
        </table>
      </div>
      ${row === null
        ? nothing
        : html`<p class="foot dim" data-suggested>Suggested from the worklist: short by ${formatQty(row.short_base, row.uom)}, ordered in multiples of ${formatQty(row.order_multiple_base, row.uom)}.</p>`}
      <div class="actions end">
        <button type="button" class="btn btn-secondary" data-action="cancel-draft" ?disabled=${model.busy}>Cancel draft</button>
        <button type="button" class="btn btn-secondary" data-action="save-draft" ?disabled=${model.busy}>Save draft</button>
        <button type="button" class="btn btn-primary" data-action="place" ?disabled=${model.busy}>Place order</button>
      </div>
      <p class="callout">Place moves the order to Ordered. Receiving moves it to Part received and Received as stock lands; nothing here does.</p>
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
                    <th class="opt2">Vendor</th>
                    <th class="num">Value</th>
                    <th>Status</th>
                  </tr>
                </thead>
                <tbody>
                  ${rows.map(
                    (po) => html`
                      <tr data-po-row aria-current="${model.placedId === po.id ? "true" : "false"}">
                        <td><span class="who" data-po-no>${po.po_no}</span><span class="small dim">${po.line_count} ${po.line_count === 1 ? "line" : "lines"}<span class="mob"> · ${po.vendor_name}</span></span></td>
                        <td class="opt2">${po.vendor_name}</td>
                        <td class="num">${formatMoney(po.subtotal_cents)}</td>
                        <td>${pill(po.status)}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
      <p class="foot dim">Ordered, Part received and Received are states, never buttons: receiving sets them as stock lands.</p>
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
                  ${rows.map(
                    (po) => html`
                      <tr data-incoming-row aria-current="${model.placedId === po.id ? "true" : "false"}">
                        <td><span class="who">${po.po_no}</span><span class="small dim">${po.vendor_name}</span></td>
                        <td>${dateOnly(po.expected_at) === "" ? html`<span class="nodate">no date</span>` : html`<span data-expected>${dateOnly(po.expected_at)}</span>`}</td>
                        <td class="opt2">${deliveryWord(po.delivery_method)}</td>
                        <td class="num opt2">${po.line_count}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
      <p class="foot dim">The yard plans around this list. A promised date is the buyer's statement; an order without one asks the vendor.</p>
      <p class="foot dim" data-bills-note>${refusalWords("post-bill", "Forbidden", 403)}</p>
    </section>
  `;
}

/** What is on order for the product just ordered, as the core's incoming read answers it. */
export function incomingWords(lines: readonly PurchasingIncomingLine[]): string {
  if (lines.length === 0) return "";
  return lines
    .map((l) => `${l.po_no}: ${formatQty(l.qty_outstanding_base, l.uom)} outstanding, ${l.expected_at === "" ? "no promised date" : `expected ${l.expected_at.slice(0, 10)}`}`)
    .join("; ");
}

// ---- the screens ---------------------------------------------------------------------------------------------

export function renderDesk(model: DeskModel): TemplateResult {
  if (model.phase === "loading") return renderLoading();
  if (model.phase === "error") return renderLoadFailed(model.error);
  return html`
    ${head()} ${tabs(model)} ${tiles(model)} ${messages(model)}
    ${model.tab === "incoming"
      ? incoming(model)
      : html`
          <div class="home">
            <div class="home-main">${model.draft !== null ? draftPanel(model, model.draft) : worklist(model)}</div>
            <div class="home-side">${register(model)} ${incoming(model)}</div>
          </div>
        `}
  `;
}
