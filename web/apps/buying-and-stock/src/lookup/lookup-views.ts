// The Sales stock lookup's screen (change 5, board bs-stock-lookup-sales-search-a-product): a
// search card over a two card split, the products on the left, the picked product's stock at each
// branch and what is coming in on the right. The words are the board's; the units ride every
// quantity; and no cell on this surface draws cost, whatever the reads carried.
import { html, nothing, type TemplateResult } from "lit";
import type { InventoryBranchStock, InventoryBrowseRow, Product } from "@hardscapehouse/api-client";
import { hhAddress } from "@hardscapehouse/design-system/src/address.js";
import { areaHead, emptyState, errorNote, loadFailed, loadingNote } from "../purch/purch-views.js";
import { formatQty } from "../po/po-money.js";
import { availabilityIsOut, availabilityWords, listPriceWords, LOOKUP_CHIPS, soldByWords, type ComingInRow } from "./lookup-logic.js";

export interface LookupModel {
  readonly phase: "loading" | "ready";
  readonly error: string;
  readonly busy: boolean;
  /** The text the running search carries, "" when none has run yet. */
  readonly searched: string;
  readonly chip: string;
  /** The rows the current chip grants, out of the page the search answered. */
  readonly rows: readonly InventoryBrowseRow[];
  readonly total: number;
  readonly picked: InventoryBrowseRow | undefined;
  readonly pickedId: string | null;
  readonly product: Product | null;
  readonly branches: readonly InventoryBranchStock[];
  readonly coming: readonly ComingInRow[];
  readonly detailError: string;
  readonly imageUrl: (productId: string) => string;
  /** The products the image read has answered for; the others keep the empty slot. */
  readonly hasImage: (productId: string) => boolean;
}

export function renderLookup(model: LookupModel): TemplateResult {
  if (model.phase === "loading") return loadingNote("Reading the branches.");
  if (model.error !== "" && model.rows.length === 0 && model.searched !== "") {
    return html`
      ${head()}
      ${searchCard(model)}
      ${loadFailed(model.error)}
    `;
  }
  return html`
    ${head()}
    ${searchCard(model)}
    <div class="lookup" data-lookup-sales>
      ${listCard(model)}
      ${detailCard(model)}
    </div>
  `;
}

function head(): TemplateResult {
  return areaHead("Stock lookup", "What the yard holds and what is coming, while the customer is standing there.");
}

function searchCard(model: LookupModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Search a product">
      <div class="forms">
        <label>Search
          <input type="search" data-field="lookup-text" data-lookup-text placeholder="Product name or SKU" />
        </label>
        <button type="button" class="btn btn-primary" data-action="lookup-search" ?disabled=${model.busy}>Search</button>
      </div>
      <div class="filters" role="group" aria-label="Show" data-lookup-chips>
        ${LOOKUP_CHIPS.map(
          (c) => html`<button type="button" class="tab" data-action="lookup-chip" data-choice=${c.id} aria-current="${model.chip === c.id ? "true" : "false"}">${c.label}</button>`,
        )}
      </div>
      ${errorNote(model.error !== "" && model.rows.length > 0 ? model.error : "")}
    </section>
  `;
}

function listCard(model: LookupModel): TemplateResult {
  const idle = model.searched === "";
  return html`
    <section class="panel lookup-list" aria-label="Products">
      <div class="panel-head">
        <h3>${model.total === 0 ? "Products" : `${model.total} ${model.total === 1 ? "product" : "products"}`}</h3>
      </div>
      ${idle
        ? emptyState("Search for a product.", "The name or the SKU finds it; the chips sort what came back.")
        : model.rows.length === 0
          ? emptyState("Nothing matches.", "Another word, or the All chip, may find it.")
          : html`
              <div class="scroll">
                ${model.rows.map((r) => lookupRow(model, r))}
              </div>
            `}
    </section>
  `;
}

function lookupRow(model: LookupModel, row: InventoryBrowseRow): TemplateResult {
  const words = availabilityWords(row);
  const selected = model.pickedId === row.product_id;
  return html`
    <button type="button" class="lrow ${selected ? "lrow-picked" : ""}" data-action="lookup-pick" data-product-id=${row.product_id} data-lookup-row aria-current="${selected ? "true" : "false"}">
      ${thumb(model, row.product_id)}
      <span class="lrow-words">
        <span class="lrow-name">${row.name}</span>
        <span class="small dim mono">${row.sku}</span>
        <span class="lrow-avail ${availabilityIsOut(row) ? "stop" : "good"}" data-lookup-avail>${words}</span>
      </span>
    </button>
  `;
}

/** The picture slot: an img only where the image read has answered, the empty slot where not. */
function thumb(model: LookupModel, productId: string): TemplateResult {
  const url = model.imageUrl(productId);
  return html`
    <span class="lrow-thumb" data-lookup-thumb>
      ${model.hasImage(productId) ? html`<img src=${hhAddress(url)} alt="" loading="lazy" />` : nothing}
    </span>
  `;
}

function detailCard(model: LookupModel): TemplateResult {
  const row = model.picked;
  if (row === undefined) {
    return html`
      <section class="panel" aria-label="The product's stock" data-lookup-detail>
        ${emptyState("Nothing is picked yet.", "Pick a product on the left to see its stock at each branch and what is coming in for it.")}
      </section>
    `;
  }
  const unit = row.sold_by ?? "";
  const product = model.product;
  return html`
    <section class="panel" aria-label="The product's stock" data-lookup-detail>
      <div class="panel-head">
        <div class="lookup-headline">
          ${thumb(model, row.product_id)}
          <div>
            <h2>${row.name}</h2>
            <p class="small dim">
              <span class="mono">${row.sku}</span>
              ${soldLine(unit, product)}
            </p>
          </div>
        </div>
      </div>
      ${model.detailError !== "" ? errorNote(model.detailError) : nothing}
      <h3 class="sub">At each branch</h3>
      ${model.branches.length === 0
        ? emptyState("No branch holds a stock record for this product yet.", "It may be a special order, or new to the catalogue.")
        : html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Branch</th>
                    <th class="num">On hand</th>
                    <th class="num">Reserved</th>
                    <th class="num">Available</th>
                    <th class="num">In transit</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.branches.map((b) => branchRow(b, unit))}
                </tbody>
              </table>
            </div>
            <p class="foot dim">Available is on hand minus reserved. In transit is on a truck to the branch and not yet in the yard.</p>
          `}
      <h3 class="sub">Coming in</h3>
      ${model.coming.length === 0
        ? emptyState("Nothing is on order for this product.", "An order a buyer places shows here with its promised date.")
        : html`
            <div class="scroll">
              <table class="stack">
                <thead>
                  <tr>
                    <th>Branch</th>
                    <th>Order</th>
                    <th class="num">Quantity</th>
                    <th>Expected</th>
                  </tr>
                </thead>
                <tbody>
                  ${model.coming.map((c) => comingRow(c))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

/** The header's sale words: the unit, and the list price beside it when the product carries one. */
function soldLine(unit: string, product: Product | null): string {
  const sold = soldByWords(unit);
  const price = listPriceWords(product?.list_price_cents);
  if (sold === "" && price === "") return "";
  if (price === "") return `Sold by ${sold}`;
  return `Sold by ${sold} · ${price}`;
}

function branchRow(b: InventoryBranchStock, unit: string): TemplateResult {
  return html`
    <tr data-branch-stock-row>
      <td data-label="Branch">${b.branch_name}</td>
      <td class="num" data-label="On hand">${formatQty(b.on_hand_base, unit)}</td>
      <td class="num" data-label="Reserved">${formatQty(b.reserved_base, unit)}</td>
      <td class="num ${b.available_base < 0 ? "stop" : "good"}" data-label="Available">${formatQty(b.available_base, unit)}</td>
      <td class="num" data-label="In transit">${formatQty(b.in_transit_base, unit)}</td>
    </tr>
  `;
}

function comingRow(c: ComingInRow): TemplateResult {
  return html`
    <tr data-coming-row>
      <td data-label="Branch">${c.branch}</td>
      <td data-label="Order"><span class="who lookup-order">${c.order}</span>${c.draft ? html`<span class="small dim">draft, not placed yet</span>` : nothing}</td>
      <td class="num" data-label="Quantity">${c.qty}</td>
      <td data-label="Expected">${c.expected === "" ? html`<span class="nodate">no date</span>` : html`<span class="who">${c.expected}</span>`}</td>
    </tr>
  `;
}
