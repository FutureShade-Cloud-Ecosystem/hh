// The inventory area's screens, as plain render functions of a model. They
// hold no state and own no events: every control carries a data-action name
// and the desk's press acts on it (the design system's position rules allow
// no @ or . binding, and the only bound values are class, role, aria- and
// data- attributes). Form fields carry data-field names and are read when a
// button is pressed; the desk seeds a field's starting value after the render.
//
// Every figure on a screen is the server's, formatted: the app computes no
// stock and no money. References: the original app's inventory console and
// its on-hand, branch-stock, browse, pending, transfers and replenish panes
// (repository hardscapeos_dibbits, commit 87d7941b, read only), drawn to HH's
// design system at 1440 and 390.
import { html, nothing, type TemplateResult } from "lit";
import type {
  InventoryBranchStock,
  InventoryInTransitLine,
  InventoryOnHand,
  InventoryPendingFulfilment,
  InventoryReorderPoint,
  InventoryReservation,
  InventoryStockMove,
  InventoryTransitDaysPair,
  InventoryTransfer,
} from "@hardscapehouse/api-client";
import type { BrowseAnswer } from "./inventory-api.js";
import {
  configWord,
  holdModeWord,
  inTransitBase,
  moveDirectionWord,
  moveTypeWord,
  refusalWords,
  reservationStatusWord,
  saleTypeWord,
  transferActions,
  transferStatusWord,
  transitDaysFor,
} from "./inventory-logic.js";
import { formatMoney, formatQty } from "../po/po-money.js";
import { whenWords } from "../po/po-logic.js";

export type InvTab = "lookup" | "browse" | "branch-stock" | "pending" | "reservations" | "transfers" | "receive" | "replenish";

export const INV_TABS: readonly { readonly id: InvTab; readonly label: string }[] = [
  { id: "lookup", label: "Stock lookup" },
  { id: "browse", label: "Browse" },
  { id: "branch-stock", label: "Branch stock" },
  { id: "pending", label: "Pending fulfilment" },
  { id: "reservations", label: "Reservations" },
  { id: "transfers", label: "Transfers" },
  { id: "receive", label: "Receive" },
  { id: "replenish", label: "Replenishment" },
];

/** One placed order as the receive tab lists it: its lines with what is still outstanding. */
export interface ReceiveOrder {
  readonly id: string;
  readonly no: string;
  readonly vendorName: string;
  readonly status: string;
  readonly branchName: string;
  readonly lines: readonly { readonly id: string; readonly sku: string; readonly name: string; readonly uom: string; readonly outstanding: number }[];
}

/** A press waiting on its confirm: what it is, which row it is for, and the words the confirm says. */
export interface InvConfirm {
  readonly kind: "receive-transfer" | "cancel-transfer" | "release" | "expire" | "bootstrap";
  readonly id: string;
  readonly title: string;
  readonly lines: readonly string[];
  readonly go: string;
}

/** The order whose receive is waiting on its confirm, with the words the confirm names. */
export interface ReceiveConfirm {
  readonly order: ReceiveOrder;
}

export interface InvModel {
  readonly phase: "loading" | "ready" | "error";
  readonly tab: InvTab;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
  readonly today: string;
  /** The catalogue entries the pickers list, SKU first like the procurement picker's. */
  readonly catalogue: readonly { readonly id: string; readonly sku: string; readonly name: string }[];
  readonly branchNames: ReadonlyMap<string, string>;
  // The lookup.
  readonly lookupPicked: string;
  readonly lookupName: string;
  readonly lookupSearched: boolean;
  readonly lookupLoading: boolean;
  readonly onHand: readonly InventoryOnHand[];
  readonly moves: readonly InventoryStockMove[];
  // Browse.
  readonly browse: BrowseAnswer | null;
  readonly browseLoading: boolean;
  // Branch stock.
  readonly bsPicked: string;
  readonly bsRows: readonly InventoryBranchStock[];
  readonly bsLoading: boolean;
  // Pending fulfilment.
  readonly pending: readonly InventoryPendingFulfilment[];
  // Reservations.
  readonly reservations: readonly InventoryReservation[];
  readonly policy: { readonly order_mode: string; readonly quote_mode: string; readonly order_ttl_hours: number; readonly quote_ttl_hours: number; readonly is_default: boolean } | null;
  // Transfers.
  readonly transfers: readonly InventoryTransfer[];
  readonly transit: readonly InventoryTransitDaysPair[];
  readonly shipping: string;
  readonly shipEta: string;
  readonly incoming: readonly InventoryInTransitLine[];
  readonly incomingName: string;
  // Receive.
  readonly receiveOrders: readonly ReceiveOrder[];
  readonly receiveConfirm: ReceiveConfirm | null;
  // Replenishment.
  readonly points: readonly InventoryReorderPoint[];
  readonly editingPoint: string | null;
  // The held press that is asking first.
  readonly confirm: InvConfirm | null;
}

function notice(model: InvModel): TemplateResult {
  if (model.notice === "") return html`${nothing}`;
  return html`<div class="alert alert-ok" role="status" data-notice><strong>${model.notice.split("\n")[0]}</strong></div>`;
}

/** The inline confirm for a held press, shown only where that press was made. */
function confirmBlock(model: InvModel, kinds: readonly InvConfirm["kind"][], id?: string): TemplateResult {
  const c = model.confirm;
  if (c === null || !kinds.includes(c.kind) || (id !== undefined && c.id !== id)) return html`${nothing}`;
  return html`
    <div class="confirm" data-confirm=${c.kind} role="group" aria-label="Confirm">
      <strong>${c.title}</strong>
      ${c.lines.map((line) => html`<p>${line}</p>`)}
      <div class="actions">
        <button type="button" class="btn btn-secondary" data-action="confirm-back">Back</button>
        <button type="button" class="btn btn-primary" data-action="confirm-go" ?disabled=${model.busy}>${c.go}</button>
      </div>
    </div>
  `;
}

function errorNote(model: InvModel): TemplateResult {
  return model.error !== "" ? html`<p class="alert alert-error" role="alert" data-error>${model.error}</p>` : html`${nothing}`;
}

function pill(status: string, word: (status: string) => string, tone: (status: string) => string): TemplateResult {
  return html`<span class="status status-${tone(status)}" data-status>${word(status)}</span>`;
}

function loading(words: string): TemplateResult {
  return html`<p class="dim" role="status">${words}</p>`;
}

// ---- the shared head -----------------------------------------------------------------------------

export function renderInvLoading(): TemplateResult {
  return html`<p class="dim" role="status">Loading the stock reads.</p>`;
}

export function renderInvLoadFailed(error: string): TemplateResult {
  return html`
    <p class="alert alert-error" role="alert" data-error>${error}</p>
    <button type="button" class="btn btn-secondary" data-action="inv-reload">Try again</button>
  `;
}

function invHead(): TemplateResult {
  return html`
    <div class="head">
      <div>
        <h1>Inventory</h1>
        <p class="dim">The stock truth: what each yard holds, what is held back, what is on the truck, and what needs buying.</p>
      </div>
    </div>
  `;
}

function invTabs(model: InvModel): TemplateResult {
  return html`
    <nav class="tabs" aria-label="Inventory views">
      ${INV_TABS.map(
        (t) => html`<button type="button" class="tab" data-action="inv-tab" data-inv-tab=${t.id} aria-current="${model.tab === t.id ? "true" : "false"}">${t.label}</button>`,
      )}
    </nav>
  `;
}

function catalogueOptions(model: InvModel): TemplateResult[] {
  return model.catalogue.map((p) => html`<option data-value=${p.id}>${p.sku} ${p.name}</option>`);
}

// ---- the lookup ------------------------------------------------------------------------------------

function lookupPane(model: InvModel): TemplateResult {
  const balancesEmpty = model.lookupSearched && !model.lookupLoading && model.onHand.length === 0 && model.moves.length === 0;
  return html`
    <section class="panel" aria-label="Stock lookup" data-lookup>
      <div class="panel-head">
        <h3>On hand and ledger</h3>
        <div class="forms">
          <label>Product
            <select data-field="lookup-product">
              <option value="">Pick a product</option>
              ${catalogueOptions(model)}
            </select>
          </label>
          <button type="button" class="btn btn-primary" data-action="lookup-run" ?disabled=${model.busy || model.lookupLoading}>Look up</button>
        </div>
      </div>
      ${model.lookupLoading ? loading("Looking up the product's stock.") : nothing}
      ${!model.lookupSearched && !model.lookupLoading ? html`<p class="dim" data-lookup-idle>Search for a product by name or SKU to see its stock position and ledger.</p>` : nothing}
      ${balancesEmpty
        ? html`<div class="empty" data-lookup-empty>
            <strong>No stock has moved for this product yet.</strong>
            <span class="dim">Nothing has been received, counted, sold or transferred against ${model.lookupName} at any branch, so it has no balances and no ledger lines. A product that has just been added to the catalogue looks exactly like this.</span>
          </div>`
        : nothing}
      ${model.onHand.length > 0
        ? html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Branch</th>
                    <th class="num">On hand</th>
                    <th class="num">Reserved</th>
                    <th class="num">Available</th>
                    <th class="num">Average cost</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.onHand.map(
                    (r) => html`
                      <tr data-onhand-row>
                        <td data-label="Branch">${model.branchNames.get(r.branch_id) ?? r.branch_id}</td>
                        <td class="num" data-label="On hand">${formatQty(r.on_hand_base, "")}</td>
                        <td class="num" data-label="Reserved">${formatQty(r.allocated_base, "")}</td>
                        <td class="num ${r.available_base < 0 ? "stop" : ""}" data-label="Available">${formatQty(r.available_base, "")}</td>
                        <td class="num" data-label="Average cost">${formatMoney(r.avg_cost_cents)}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
            <p class="foot dim">Available is on hand minus reserved. A negative available is stock sold before its receipt, by design.</p>
          `
        : nothing}
      ${model.moves.length > 0
        ? html`
            <h3 class="sub">Ledger</h3>
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>When</th>
                    <th>Type</th>
                    <th>Direction</th>
                    <th class="num">Quantity</th>
                    <th class="num">Unit cost</th>
                    <th>Source</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.moves.map(
                    (m) => html`
                      <tr data-move-row>
                        <td data-label="When">${whenWords(m.occurred_at, model.today)}</td>
                        <td data-label="Type">${moveTypeWord(m.move_type)}</td>
                        <td data-label="Direction" class="${m.direction === "OUT" ? "stop" : ""}">${moveDirectionWord(m.direction)}</td>
                        <td class="num" data-label="Quantity">${formatQty(m.qty_base, m.uom)}</td>
                        <td class="num" data-label="Unit cost">${formatMoney(m.unit_cost_cents)}</td>
                        <td data-label="Source" class="code">${m.source_ref_id}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
            <p class="foot dim">The ledger is append only. A correction is a compensating move, never an edit.</p>
          `
        : nothing}
      ${errorNote(model)}
    </section>
  `;
}

// ---- browse ----------------------------------------------------------------------------------------

function browsePane(model: InvModel): TemplateResult {
  const browse = model.browse;
  return html`
    <section class="panel" aria-label="Browse products" data-browse>
      <div class="panel-head">
        <h3>Browse</h3>
        <div class="forms">
          <label>Search
            <input type="search" data-field="browse-text" placeholder="Name or SKU" />
          </label>
          <button type="button" class="btn btn-primary" data-action="browse-run" ?disabled=${model.busy || model.browseLoading}>Search</button>
        </div>
      </div>
      ${model.browseLoading ? loading("Searching the catalogue.") : nothing}
      ${browse === null && !model.browseLoading ? html`<p class="dim">Search the catalogue to see each product's stock position beside it.</p>` : nothing}
      ${browse !== null
        ? html`
            <p class="dim" data-browse-total>Showing ${browse.rows.length} of ${browse.total} products.</p>
            <div class="chips">
              ${browse.facets.map(
                (f) => html`<span class="chip" data-facet=${f.field}><span class="dim">${f.label}</span><span class="figure">${f.values.slice(0, 3).map((v) => `${v.value} (${v.count})`).join(", ")}</span></span>`,
              )}
            </div>
            ${browse.rows.length === 0
              ? html`<div class="empty"><strong>Nothing matches that search.</strong><span class="dim">A shorter word, or the SKU, usually finds it.</span></div>`
              : html`
                  <div class="scroll">
                    <table class="stack">
                      <thead>
                        <tr>
                          <th>SKU</th>
                          <th>Product</th>
                          <th class="num">On hand</th>
                          <th class="num">Reserved</th>
                          <th class="num">Available</th>
                          <th class="num">Average cost</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${browse.rows.map(
                          (r) => html`
                            <tr data-browse-row>
                              <td class="sku" data-label="SKU">${r.sku}</td>
                              <td data-label="Product">${r.name}<span class="small dim">${r.manufacturer}</span></td>
                              <td class="num" data-label="On hand">${formatQty(r.on_hand_base, r.sold_by)}</td>
                              <td class="num" data-label="Reserved">${formatQty(r.reserved_base, r.sold_by)}</td>
                              <td class="num ${r.available_base < 0 ? "stop" : ""}" data-label="Available">${formatQty(r.available_base, r.sold_by)}</td>
                              <td class="num" data-label="Average cost">${formatMoney(r.avg_cost_cents)}</td>
                            </tr>
                          `,
                        )}
                      </tbody>
                    </table>
                  </div>
                `}
          `
        : nothing}
      ${errorNote(model)}
    </section>
  `;
}

// ---- branch stock ------------------------------------------------------------------------------------

function branchStockPane(model: InvModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Branch stock" data-branch-stock>
      <div class="panel-head">
        <h3>Branch stock</h3>
        <div class="forms">
          <label>Product
            <select data-field="bs-product">
              <option value="">Pick a product</option>
              ${catalogueOptions(model)}
            </select>
          </label>
          <button type="button" class="btn btn-primary" data-action="bs-run" ?disabled=${model.busy || model.bsLoading}>Show stock</button>
        </div>
      </div>
      ${model.bsLoading ? loading("Reading every yard's position.") : nothing}
      ${!model.bsLoading && model.bsRows.length === 0 && model.bsPicked !== "" ? html`<div class="empty"><strong>No stock rows for this product yet.</strong><span class="dim">A product with no receipts, counts or sales has no position at any yard.</span></div>` : nothing}
      ${!model.bsLoading && model.bsRows.length === 0 && model.bsPicked === "" ? html`<p class="dim">Pick a product to see its position at every yard, including the yards that hold none.</p>` : nothing}
      ${model.bsRows.length > 0
        ? html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Branch</th>
                    <th class="num">On hand</th>
                    <th class="num">Reserved</th>
                    <th class="num">In transit</th>
                    <th class="num">Soft held</th>
                    <th class="num">Available</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.bsRows.map(
                    (r) => html`
                      <tr data-bs-row>
                        <td data-label="Branch">${r.branch_name}</td>
                        <td class="num" data-label="On hand">${formatQty(r.on_hand_base, "")}</td>
                        <td class="num" data-label="Reserved">${formatQty(r.reserved_base, "")}</td>
                        <td class="num" data-label="In transit">${formatQty(r.in_transit_base, "")}</td>
                        <td class="num" data-label="Soft held">${formatQty(r.soft_held_base, "")}</td>
                        <td class="num ${r.available_base < 0 ? "stop" : ""}" data-label="Available">${r.available_base < 0 ? html`<span data-bs-negative>${formatQty(r.available_base, "")}</span>` : formatQty(r.available_base, "")}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
            <p class="foot dim">A yard with no stock record reads as zeroes. A negative available has sold before its receipt.</p>
          `
        : nothing}
      ${errorNote(model)}
    </section>
  `;
}

// ---- pending fulfilment --------------------------------------------------------------------------------

function pendingPane(model: InvModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Pending fulfilment" data-pending>
      <div class="panel-head">
        <h3>Pending fulfilment</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="pending-reload">Refresh</button>
      </div>
      ${model.pending.length === 0
        ? html`<div class="empty"><strong>Nothing is waiting on a stock move.</strong><span class="dim">A collected-now sale moves stock at the till; a delivered or collect-later line waits here until it is fulfilled.</span></div>`
        : html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Order</th>
                    <th>Product</th>
                    <th>Mode</th>
                    <th class="num">Quantity</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.pending.map(
                    (r) => html`
                      <tr data-pending-row>
                        <td class="code" data-label="Order">${r.order_id}</td>
                        <td class="code" data-label="Product">${model.catalogue.find((c) => c.id === r.product_id)?.name ?? r.product_id}</td>
                        <td data-label="Mode">${saleTypeWord(r.sale_type)}</td>
                        <td class="num" data-label="Quantity">${formatQty(r.qty_base, "")}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
            <p class="foot dim">These lines are sold and not yet moved. Receiving or delivering them is what clears the list.</p>
          `}
      ${errorNote(model)}
    </section>
  `;
}

// ---- reservations ----------------------------------------------------------------------------------------

function reservationsPane(model: InvModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Reservations" data-reservations>
      <div class="panel-head">
        <h3>Reservations</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="res-reload">Refresh</button>
      </div>
      ${model.reservations.length === 0
        ? html`<div class="empty"><strong>No stock is held.</strong><span class="dim">A hold reserves stock for an order or a quote; only a hard hold subtracts from what the yard can sell.</span></div>`
        : html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Product</th>
                    <th>Branch</th>
                    <th>Hold</th>
                    <th class="num">Quantity</th>
                    <th>Status</th>
                    <th>Source</th>
                    <th><span class="visually-hidden">Actions</span></th>
                  </tr>
                </thead>
                <tbody>
                  ${model.reservations.map(
                    (r) => html`
                      <tr data-res-row>
                        <td class="sku" data-label="Product">${model.catalogue.find((c) => c.id === r.product_id)?.sku ?? r.product_id}</td>
                        <td data-label="Branch">${model.branchNames.get(r.branch_id) ?? r.branch_id}</td>
                        <td data-label="Hold">${holdModeWord(r.hold_mode)}</td>
                        <td class="num" data-label="Quantity">${formatQty(r.qty_base, "")}</td>
                        <td data-label="Status">${reservationStatusWord(r.status)}</td>
                        <td class="code" data-label="Source">${r.source_ref_id}</td>
                        <td class="pick" data-label="Actions">${r.status === "ACTIVE" ? html`<button type="button" class="btn btn-secondary small-btn" data-action="release-reservation" data-res-id=${r.id} ?disabled=${model.busy}>Release</button>` : nothing}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
            <div class="actions">
              <button type="button" class="btn btn-secondary" data-action="expire-reservations" ?disabled=${model.busy}>Run the expiry sweep</button>
            </div>
          `}
      ${confirmBlock(model, ["release", "expire"])}
      ${notice(model)}
      ${errorNote(model)}
      <h3 class="sub">Hold stock</h3>
      <div class="forms">
        <label>Product
          <select data-field="res-product">
            <option value="">Pick a product</option>
            ${catalogueOptions(model)}
          </select>
        </label>
        <label>Branch
          <select data-field="res-branch">
            ${[...model.branchNames.entries()].map(([id, name]) => html`<option data-value=${id}>${name}</option>`)}
          </select>
        </label>
        <label class="narrow">Quantity
          <input type="number" min="0" step="any" inputmode="decimal" data-field="res-qty" />
        </label>
        <label>For
          <select data-field="res-type">
            <option value="ORDER">An order</option>
            <option value="QUOTE">A quote</option>
          </select>
        </label>
        <label>Reference
          <input type="text" data-field="res-ref" placeholder="Order or quote number" />
        </label>
        <button type="button" class="btn btn-primary" data-action="reserve" ?disabled=${model.busy}>Hold stock</button>
      </div>
      <p class="foot dim">The branch allocation policy grades the hold; only a hard hold subtracts from available.</p>
      ${model.policy !== null
        ? html`<div class="facts" data-policy>
            <dt>Orders</dt><dd>${holdModeWord(model.policy.order_mode)}, expires after ${model.policy.order_ttl_hours} hours${model.policy.is_default ? " (the system default)" : ""}</dd>
            <dt>Quotes</dt><dd>${holdModeWord(model.policy.quote_mode)}, expires after ${model.policy.quote_ttl_hours} hours</dd>
          </div>`
        : nothing}
    </section>
  `;
}

// ---- transfers -------------------------------------------------------------------------------------------

function shipHint(model: InvModel, t: InventoryTransfer): string {
  const days = transitDaysFor(model.transit, t.from_branch_id, t.to_branch_id);
  const route = `${model.branchNames.get(t.from_branch_id) ?? t.from_branch_id} to ${model.branchNames.get(t.to_branch_id) ?? t.to_branch_id}`;
  if (days === null) return `No transit time is set for ${route}, so there is no default. Leave this blank unless you know the arrival day.`;
  return `${route} is set to ${days} ${days === 1 ? "day" : "days"}. Change the date if this load is different; clear it to ship with no promised arrival.`;
}

function transferCard(model: InvModel, t: InventoryTransfer): TemplateResult {
  const can = transferActions(t.status);
  const transit = t.lines.reduce((sum, l) => sum + inTransitBase(l), 0);
  const shipping = model.shipping === t.id;
  return html`
    <section class="panel" data-transfer=${t.transfer_no} aria-label="Transfer ${t.transfer_no}">
      <div class="panel-head">
        <h3>${t.transfer_no}</h3>
        <div class="forms">
          <span>${model.branchNames.get(t.from_branch_id) ?? t.from_branch_id} to ${model.branchNames.get(t.to_branch_id) ?? t.to_branch_id}</span>
          ${pill(t.status, transferStatusWord, (s) => (s === "RECEIVED" ? "good" : s === "CANCELLED" ? "stop" : s === "IN_TRANSIT" ? "watch" : "plain"))}
          ${transit > 0 ? html`<span class="dim">${formatQty(transit, t.lines[0]?.uom)} in transit</span>` : nothing}
          ${t.eta !== "" ? html`<span class="dim" data-eta>expected ${t.eta}</span>` : nothing}
          <span class="actions">
            ${can.ship ? html`<button type="button" class="btn btn-primary small-btn" data-action="ship-transfer" data-transfer-id=${t.id} ?disabled=${model.busy}>Ship</button>` : nothing}
            ${can.receive ? html`<button type="button" class="btn btn-primary small-btn" data-action="receive-transfer" data-transfer-id=${t.id} ?disabled=${model.busy}>Receive</button>` : nothing}
            ${can.cancel ? html`<button type="button" class="btn btn-secondary small-btn" data-action="cancel-transfer" data-transfer-id=${t.id} ?disabled=${model.busy}>Cancel</button>` : nothing}
          </span>
        </div>
      </div>
      ${confirmBlock(model, ["receive-transfer", "cancel-transfer"], t.id)}
      ${shipping
        ? html`
            <div class="confirm" data-ship-row>
              <strong>Loading the truck.</strong>
              <label>Expected arrival
                <input type="date" data-field="ship-eta" />
              </label>
              <p class="dim">${shipHint(model, t)}</p>
              <div class="actions">
                <button type="button" class="btn btn-secondary" data-action="ship-cancel">Back</button>
                <button type="button" class="btn btn-primary" data-action="confirm-ship" data-transfer-id=${t.id} ?disabled=${model.busy}>Confirm ship</button>
              </div>
            </div>
          `
        : nothing}
      <div class="scroll">
        <table class="stack">
          <thead>
            <tr>
              <th>SKU</th>
              <th>Product</th>
              <th class="num">Requested</th>
              <th class="num">Shipped</th>
              <th class="num">Received</th>
            </tr>
          </thead>
          <tbody>
            ${t.lines.map(
              (l) => html`
                <tr data-transfer-line>
                  <td class="sku" data-label="SKU">${l.sku}</td>
                  <td data-label="Product">${l.product_name}</td>
                  <td class="num" data-label="Requested">${formatQty(l.qty_base, l.uom)}</td>
                  <td class="num" data-label="Shipped">${formatQty(l.qty_shipped_base, l.uom)}</td>
                  <td class="num" data-label="Received">${formatQty(l.qty_received_base, l.uom)}</td>
                </tr>
              `,
            )}
          </tbody>
        </table>
      </div>
    </section>
  `;
}

function transfersPane(model: InvModel): TemplateResult {
  return html`
    ${errorNote(model)}
    ${notice(model)}
    <section class="panel" aria-label="Raise a transfer">
      <div class="panel-head">
        <h3>Raise a transfer</h3>
      </div>
      <div class="forms">
        <label>From
          <select data-field="tr-from">
            ${[...model.branchNames.entries()].map(([id, name]) => html`<option data-value=${id}>${name}</option>`)}
          </select>
        </label>
        <label>To
          <select data-field="tr-to">
            ${[...model.branchNames.entries()].map(([id, name]) => html`<option data-value=${id}>${name}</option>`)}
          </select>
        </label>
        <label>Product
          <select data-field="tr-product">
            <option value="">Pick a product</option>
            ${catalogueOptions(model)}
          </select>
        </label>
        <label class="narrow">Quantity
          <input type="number" min="0" step="any" inputmode="decimal" data-field="tr-qty" />
        </label>
        <button type="button" class="btn btn-primary" data-action="raise-transfer" ?disabled=${model.busy}>Raise draft</button>
      </div>
      <p class="foot dim">A draft moves nothing. The overdraft guard sits at ship: the origin yard must hold the stock.</p>
    </section>
    ${model.transfers.length === 0
      ? html`<section class="panel"><div class="empty"><strong>No transfers yet.</strong><span class="dim">A raised draft shows here; shipping loads the truck, receiving unloads it.</span></div></section>`
      : model.transfers.map((t) => transferCard(model, t))}
    <section class="panel" data-transit aria-label="Transit times">
      <div class="panel-head">
        <h3>Transit times</h3>
      </div>
      <div class="scroll">
        <table class="stack">
          <thead>
            <tr>
              <th>From</th>
              <th>To</th>
              <th class="num">Transit time</th>
            </tr>
          </thead>
          <tbody>
            ${model.transit.map(
              (p) => html`
                <tr data-transit-row>
                  <td data-label="From">${p.from_branch_name}</td>
                  <td data-label="To">${p.to_branch_name}</td>
                  <td class="num" data-label="Transit time">${configWord(p)}</td>
                </tr>
              `,
            )}
          </tbody>
        </table>
      </div>
      <p class="foot dim" data-transit-note>Transit times are set by your administrator; this table shows them and pre-fills the ship form.</p>
    </section>
    ${model.incoming.length > 0
      ? html`
          <section class="panel" aria-label="On the truck">
            <div class="panel-head"><h3>On the truck to a yard</h3><span class="dim">${model.incomingName}</span></div>
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Transfer</th>
                    <th>From</th>
                    <th class="num">Quantity</th>
                    <th>Expected</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.incoming.map(
                    (l) => html`
                      <tr data-incoming-row>
                        <td class="code" data-label="Transfer">${l.transfer_no}</td>
                        <td data-label="From">${l.from_branch_name}</td>
                        <td class="num" data-label="Quantity">${formatQty(l.qty_base, l.uom)}</td>
                        <td data-label="Expected">${l.eta === "" ? html`<span class="dim">no date</span>` : l.eta}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          </section>
        `
      : nothing}
  `;
}

// ---- receive ---------------------------------------------------------------------------------------------

function receivePane(model: InvModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Receive an order" data-receive>
      <div class="panel-head">
        <h3>Receive an order</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="receive-reload">Refresh</button>
      </div>
      ${model.receiveOrders.length === 0
        ? html`<div class="empty"><strong>Nothing is on order.</strong><span class="dim">An order the buyer placed shows here once the vendor's truck is expected.</span></div>`
        : html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Order</th>
                    <th>Vendor</th>
                    <th>Status</th>
                    <th>Lines</th>
                    <th><span class="visually-hidden">Receive</span></th>
                  </tr>
                </thead>
                <tbody>
                  ${model.receiveOrders.map(
                    (o) => html`
                      <tr data-receive-order=${o.no}>
                        <td><span class="who">${o.no}</span><span class="small dim">${o.branchName}</span></td>
                        <td data-label="Vendor">${o.vendorName}</td>
                        <td data-label="Status">${o.status === "ORDERED" ? "Ordered" : "Partial"}</td>
                        <td data-label="Lines">${o.lines.map((l) => `${l.sku} ${formatQty(l.outstanding, l.uom)} to land`).join(", ")}</td>
                        <td class="pick" data-label="Receive">
                          <button type="button" class="btn btn-primary small-btn" data-action="receive-order" data-po-id=${o.id} ?disabled=${model.busy}>Receive</button>
                        </td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
      ${model.receiveConfirm !== null
        ? html`
            <div class="confirm" data-receive-confirm>
              <strong>Receive ${model.receiveConfirm.order.no} into ${model.receiveConfirm.order.branchName}.</strong>
              ${model.receiveConfirm.order.lines.map(
                (l) => html`<p>${l.name}: ${formatQty(l.outstanding, "")} ${l.uom}</p>`,
              )}
              <p class="dim">Everything still outstanding is received in one move, and the stock and its value land together. Nothing moves until you confirm.</p>
              <div class="actions">
                <button type="button" class="btn btn-secondary" data-action="receive-back">Back</button>
                <button type="button" class="btn btn-primary" data-action="receive-confirm" ?disabled=${model.busy}>Confirm receive</button>
              </div>
            </div>
          `
        : nothing}
      ${errorNote(model)}
      ${notice(model)}
      <p class="foot dim">Receiving takes the whole outstanding quantity and moves the order to Received. A partial truck is received the same way: what landed lands, and what did not stays on order.</p>
    </section>
  `;
}

// ---- replenishment -------------------------------------------------------------------------------------------

function replenishPane(model: InvModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Replenishment" data-replenish>
      <div class="panel-head">
        <h3>Reorder points</h3>
        <div class="forms">
          <label>Yard
            <select data-field="bootstrap-branch">
              ${[...model.branchNames.entries()].map(([id, name]) => html`<option data-value=${id}>${name}</option>`)}
            </select>
          </label>
          <button type="button" class="btn btn-secondary small-btn" data-action="bootstrap-points" ?disabled=${model.busy}>Open a line for every stocked product</button>
        </div>
      </div>
      ${confirmBlock(model, ["bootstrap"])}
      ${model.points.length === 0
        ? html`<div class="empty"><strong>No managed lines at this yard yet.</strong><span class="dim">A managed line says who the product is bought from and how much of it to keep. The bootstrap opens one per stocked product at half of today's on hand.</span></div>`
        : html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>SKU</th>
                    <th>Product</th>
                    <th class="num">Point</th>
                    <th class="num">Order quantity</th>
                    <th class="num">Multiple</th>
                    <th class="num">Lead days</th>
                    <th><span class="visually-hidden">Actions</span></th>
                  </tr>
                </thead>
                <tbody>
                  ${model.points.map(
                    (r) => html`
                      <tr data-point-row>
                        <td class="sku" data-label="SKU">${r.sku}</td>
                        <td data-label="Product">${r.product_name}</td>
                        <td class="num" data-label="Point">${formatQty(r.reorder_point_base, "")}</td>
                        <td class="num" data-label="Order quantity">${formatQty(r.reorder_qty_base, "")}</td>
                        <td class="num" data-label="Multiple">${formatQty(r.order_multiple_base, "")}</td>
                        <td class="num" data-label="Lead days">${r.lead_time_days}</td>
                        <td class="pick" data-label="Actions">
                          <button type="button" class="btn btn-secondary small-btn" data-action="edit-point" data-point-id=${r.product_id} ?disabled=${model.busy}>Change</button>
                          <button type="button" class="btn btn-secondary small-btn" data-action="nudge" data-point-id=${r.product_id} ?disabled=${model.busy}>Nudge procurement</button>
                        </td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
      ${model.editingPoint !== null
        ? html`
            <div class="confirm" data-point-editor>
              <strong>Change the reorder point.</strong>
              ${pointEditor(model)}
              <div class="actions">
                <button type="button" class="btn btn-secondary" data-action="edit-point-close">Back</button>
                <button type="button" class="btn btn-primary" data-action="save-point" ?disabled=${model.busy}>Save point</button>
              </div>
            </div>
          `
        : nothing}
      ${errorNote(model)}
      ${notice(model)}
      <p class="foot dim">The worklist that grades these lines lives in the Purchasing area, worst first. The nudge posts one message asking procurement to look; it is idempotent per product and branch within the posting cycle.</p>
    </section>
  `;
}

function pointEditor(model: InvModel): TemplateResult {
  const row = model.points.find((r) => r.product_id === model.editingPoint);
  if (row === undefined) return html`${nothing}`;
  return html`
    <dl class="facts">
      <dt>Product</dt><dd>${row.product_name}</dd>
      <dt>Branch</dt><dd>${model.branchNames.get(row.branch_id) ?? row.branch_id}</dd>
      <dt>Now</dt><dd>point ${formatQty(row.reorder_point_base, "")}, order ${formatQty(row.reorder_qty_base, "")}</dd>
    </dl>
    <div class="forms">
      <label class="narrow">Point
        <input type="number" min="0" step="any" inputmode="decimal" data-field="point" />
      </label>
      <label class="narrow">Order quantity
        <input type="number" min="0" step="any" inputmode="decimal" data-field="reorder-qty" />
      </label>
    </div>
  `;
}

// ---- the area -------------------------------------------------------------------------------------------------

export function renderInventory(model: InvModel): TemplateResult {
  if (model.phase === "loading") return renderInvLoading();
  if (model.phase === "error") return renderInvLoadFailed(model.error);
  const body = () => {
    switch (model.tab) {
      case "browse":
        return browsePane(model);
      case "branch-stock":
        return branchStockPane(model);
      case "pending":
        return pendingPane(model);
      case "reservations":
        return reservationsPane(model);
      case "transfers":
        return transfersPane(model);
      case "receive":
        return receivePane(model);
      case "replenish":
        return replenishPane(model);
      case "lookup":
      default:
        return lookupPane(model);
    }
  };
  return html`${invHead()} ${invTabs(model)} ${body()} `;
}

/** A refusal or failure as the inventory area words it. */
export function invRefusalWords(action: string, error: string, status: number | undefined): string {
  return refusalWords(action, error, status);
}
