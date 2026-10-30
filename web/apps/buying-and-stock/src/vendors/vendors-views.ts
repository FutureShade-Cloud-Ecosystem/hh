// The vendors area's screens, as plain render functions of a model: the vendor list with a search,
// the vendor beside it (its details, its ordering contact, its own codes), a new vendor form, and
// the lookup of the order a vendor quoted back. They hold no state and own no events. Reference:
// the Money Desk's cards and the procurement panels, and the original app's vendor screens.
import { html, nothing, type TemplateResult } from "lit";
import type { Product, PurchasingPO, PurchasingVendor, PurchasingVendorContactResult, PurchasingVendorSKU } from "@hardscapehouse/api-client";
import { areaHead, confirmBox, emptyState, errorNote, loadFailed, loadingNote, noticeBox, pill, checkBox } from "../purch/purch-views.js";
import { formatMoney, formatQty } from "../purch/purch-money.js";
import { statusTone, statusWord } from "../po/po-logic.js";
import { filterVendors } from "./vendors-logic.js";

export type VendorTab = "details" | "contact" | "codes";
export type Removal = "contact" | "code" | null;

export interface VendorsModel {
  readonly phase: "loading" | "ready" | "error";
  readonly vendors: readonly PurchasingVendor[];
  readonly query: string;
  readonly showInactive: boolean;
  /** The vendor being worked, or null; "new" is the new vendor form. */
  readonly selected: PurchasingVendor | "new" | null;
  readonly tab: VendorTab;
  /** The email the last save answered with: the list never carries it. */
  readonly savedEmail: string | null;
  readonly clearEmail: boolean;
  /** True when the signed in seat may mark a vendor a subcontractor: the Controller's own mark. */
  readonly canSetT5018: boolean;
  /** The mark as the screen holds it, before it is saved. */
  readonly t5018Mark: boolean;
  readonly contact: PurchasingVendorContactResult | null;
  readonly codes: readonly PurchasingVendorSKU[] | null;
  readonly products: readonly Product[];
  /** The code being corrected, by product id, or "new" for the add row; null when none is open. */
  readonly editingCode: string | null;
  readonly removal: Removal;
  readonly removalCode: PurchasingVendorSKU | null;
  readonly orderLookup: PurchasingPO | null;
  readonly lookupError: string;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
}

// ---- the list ------------------------------------------------------------------------------------

function vendorList(model: VendorsModel): TemplateResult {
  const rows = filterVendors(model.vendors, model.query, model.showInactive);
  return html`
    <section class="panel" aria-label="Vendors" data-vendor-list>
      <div class="panel-head">
        <h3>Vendors</h3>
        <button type="button" class="btn btn-primary small-btn" data-action="new-vendor" ?disabled=${model.busy}>New vendor</button>
      </div>
      <label>Find a vendor
        <input type="text" autocomplete="off" data-field="v-query" data-v-query />
      </label>
      ${checkBox("Show inactive vendors", "toggle-inactive", model.showInactive)}
      ${rows.length === 0
        ? emptyState(model.vendors.length === 0 ? "No vendors yet." : "No vendor matches that.", "Add a vendor to order from it.")
        : html`
            <div class="scroll">
              <table class="stack">
                <thead><tr><th>Vendor</th><th class="num">Terms</th><th>State</th></tr></thead>
                <tbody>
                  ${rows.map(
                    (v) => html`<tr data-vendor-row data-vendor-id=${v.id} aria-current="${model.selected !== null && model.selected !== "new" && model.selected.id === v.id ? "true" : "false"}">
                      <td class="full" data-label="Vendor"><button type="button" class="link-inline" data-action="pick-vendor" data-vendor-id=${v.id}>${v.name}</button><span class="small dim">${v.vendor_no}</span></td>
                      <td class="num" data-label="Terms">${v.payment_terms_days === 0 ? "Due on receipt" : `${v.payment_terms_days} days`}</td>
                      <td data-label="State">${pill(v.is_active ? "good" : "plain", v.is_active ? "Active" : "Inactive")}</td>
                    </tr>`,
                  )}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

// ---- the lookup of a quoted order -----------------------------------------------------------------------

function lookup(model: VendorsModel): TemplateResult {
  const po = model.orderLookup;
  return html`
    <section class="panel" aria-label="Find an order by the vendor's number" data-order-lookup>
      <div class="panel-head"><h3>Find the order a vendor quoted</h3></div>
      <p class="dim">A vendor writes our order number its own way. Type it as it came back, and the order is found.</p>
      <div class="inline-form">
        <label>The number as the vendor wrote it
          <input type="text" autocomplete="off" data-field="v-ref" data-v-ref />
        </label>
        <button type="button" class="btn btn-secondary" data-action="find-order" ?disabled=${model.busy}>Find the order</button>
      </div>
      ${model.lookupError === "" ? nothing : html`<p class="alert alert-error" role="alert" data-error data-lookup-error>${model.lookupError}</p>`}
      ${po === null
        ? nothing
        : html`<dl class="facts" data-order-found>
            <dt>Order</dt><dd>${po.po_no}</dd>
            <dt>Vendor</dt><dd>${po.vendor_name}</dd>
            <dt>State</dt><dd>${pill(statusTone(po.status), statusWord(po.status))}</dd>
            <dt>Lines</dt><dd>${po.line_count}</dd>
            <dt>Value</dt><dd>${formatMoney(po.subtotal_cents)}</dd>
          </dl>`}
    </section>
  `;
}

// ---- the vendor ------------------------------------------------------------------------------------------------

/**
 * The T5018 record on the vendor's own page: the mark and the contractor's CRA business number.
 *
 * The mark is the Financial Controller's, so the control is offered only to that seat: the core's
 * wall is the authority, and a seat that would be refused is not offered the control at all. The
 * record has its own save, because it is its own door: the vendor's name and terms are a buyer's
 * to correct, and the mark is the Controller's.
 */
function t5018Panel(model: VendorsModel, vendor: PurchasingVendor): TemplateResult {
  if (!model.canSetT5018) {
    return html`<p class="foot dim" data-t5018-note>The subcontractor mark for the CRA's Form T5018 is the Financial Controller's to set.</p>`;
  }
  return html`
    <div data-t5018>
      <h3 class="sub">T5018 subcontractor</h3>
      <p class="dim">The CRA's Form T5018 names the subcontractors a dealer paid during a year. Marking a vendor here puts it on that year's report.</p>
      ${checkBox("This vendor is a subcontractor (T5018)", "toggle-t5018", model.t5018Mark)}
      <div class="forms">
        <label>CRA business number
          <input type="text" inputmode="numeric" autocomplete="off" data-field="v-bn" data-v-bn />
          <span class="dim">Nine digits, with RC0001 after them for a program account. Leave blank if the contractor has not given one.</span>
        </label>
      </div>
      <div class="actions end">
        <span class="dim" data-t5018-save-hint>Saves only this block</span>
        <button type="button" class="btn btn-secondary" data-action="save-t5018" ?disabled=${model.busy}>Save the T5018 record</button>
      </div>
      <p class="foot dim">${vendor.is_t5018_subcontractor ? "This vendor is on the T5018 report." : "This vendor is not on the T5018 report."}</p>
    </div>
  `;
}

function detailsForm(model: VendorsModel, vendor: PurchasingVendor | "new"): TemplateResult {
  const isNew = vendor === "new";
  return html`
    <div class="forms">
      <label>Vendor name
        <input type="text" autocomplete="off" data-field="v-name" data-v-name />
      </label>
      <label>Payment terms, days
        <input type="text" inputmode="numeric" autocomplete="off" data-field="v-terms" data-v-terms />
        <span class="dim">${isNew ? "Leave blank for 30 days." : "From 0 to 365."}</span>
      </label>
      <label>Email on the vendor record
        <input type="text" inputmode="email" autocomplete="off" data-field="v-email" data-v-email />
        <span class="dim" data-email-note>${isNew
          ? "Optional."
          : model.savedEmail === null
            ? "The list does not show the address on file. Type a new one to change it."
            : model.savedEmail === ""
              ? "No address is on file."
              : `On file: ${model.savedEmail}`}</span>
      </label>
      ${isNew ? nothing : checkBox("Clear the email on file", "toggle-clear-email", model.clearEmail)}
    </div>
    ${isNew ? nothing : t5018Panel(model, vendor)}
    ${errorNote(model.error)}
    <div class="actions end">
      ${isNew ? html`<button type="button" class="btn btn-secondary" data-action="close-vendor" ?disabled=${model.busy}>Cancel</button>` : nothing}
      ${isNew
        ? nothing
        : html`<button type="button" class="btn btn-secondary" data-action="toggle-active" ?disabled=${model.busy}>${vendor.is_active ? "Mark inactive" : "Mark active"}</button>`}
      <button type="button" class="btn btn-primary" data-action="save-vendor" ?disabled=${model.busy}>${isNew ? "Add the vendor" : "Save changes"}</button>
    </div>
  `;
}

function contactTab(model: VendorsModel): TemplateResult {
  if (model.contact === null) return loadingNote("Reading the ordering contact.");
  const c = model.contact;
  return html`
    <div data-contact>
      <p class="dim">The desk a purchase order is sent to. ${c.tagged ? "" : "Nobody is tagged for ordering at this vendor yet."}</p>
      ${c.tagged
        ? html`<dl class="facts" data-contact-on-file>
            <dt>Name</dt><dd>${c.contact.contact_name === "" ? "(none)" : c.contact.contact_name}</dd>
            <dt>Email</dt><dd>${c.contact.email === "" ? "(none)" : c.contact.email}</dd>
            <dt>Phone</dt><dd>${c.contact.phone === "" ? "(none)" : c.contact.phone}</dd>
          </dl>`
        : nothing}
      <div class="forms">
        <label>Contact name
          <input type="text" autocomplete="off" data-field="c-name" data-c-name />
        </label>
        <label>Ordering email
          <input type="text" inputmode="email" autocomplete="off" data-field="c-email" data-c-email />
        </label>
        <label>Phone
          <input type="text" inputmode="tel" autocomplete="off" data-field="c-phone" data-c-phone />
        </label>
      </div>
      <p class="foot dim">An order can be sent by email only; a contact with a phone and no email is kept but cannot be mailed.</p>
      ${errorNote(model.error)}
      ${model.removal === "contact"
        ? confirmBox("Remove the ordering contact for this vendor? Orders to it will ask for a fallback address or refuse, by the purchasing policy.", "Remove the contact", "do-remove-contact", model.busy)
        : html`<div class="actions end">
            ${c.tagged ? html`<button type="button" class="btn btn-secondary" data-action="ask-remove-contact" ?disabled=${model.busy}>Remove the contact</button>` : nothing}
            <button type="button" class="btn btn-primary" data-action="save-contact" ?disabled=${model.busy}>${c.tagged ? "Save the contact" : "Tag the contact"}</button>
          </div>`}
    </div>
  `;
}

function codeEditor(model: VendorsModel, existing: PurchasingVendorSKU | null): TemplateResult {
  return html`
    <tr data-code-editor>
      <td colspan="5">
        <div class="addline code-line">
          <label>Product
            ${existing === null
              ? html`<select data-field="m-product" data-m-product><option>Pick a product</option>${model.products.map((p) => html`<option>${p.sku} ${p.name}</option>`)}</select>`
              : html`<span data-m-product-fixed>${existing.sku ?? ""} ${existing.product_name ?? ""}</span>`}
          </label>
          <label>Vendor code <input type="text" autocomplete="off" data-field="m-code" data-m-code /></label>
          <label>Vendor unit <input type="text" autocomplete="off" data-field="m-unit" data-m-unit /></label>
          <label>Our units in one <input type="text" inputmode="decimal" autocomplete="off" data-field="m-factor" data-m-factor /></label>
          <div class="addline-actions">
            <button type="button" class="btn btn-secondary small-btn" data-action="close-code" ?disabled=${model.busy}>Close</button>
            <button type="button" class="btn btn-primary small-btn" data-action="save-code" ?disabled=${model.busy}>Save the code</button>
          </div>
        </div>
        ${errorNote(model.error)}
      </td>
    </tr>
  `;
}

function codesTab(model: VendorsModel): TemplateResult {
  if (model.codes === null) return loadingNote("Reading the vendor's codes.");
  return html`
    <div data-codes>
      <p class="dim">The vendor's own code for each product of ours, shown on a purchase order beside our SKU.</p>
      <div class="scroll">
        <table class="stack">
          <thead><tr><th>SKU</th><th>Product</th><th>Vendor code</th><th>Vendor unit</th><th><span class="visually-hidden">Actions</span></th></tr></thead>
          <tbody>
            ${model.codes.map((k) =>
              model.editingCode === k.product_id
                ? codeEditor(model, k)
                : html`<tr data-code-row>
                    <td class="sku" data-label="SKU">${k.sku ?? ""}</td>
                    <td class="full" data-label="Product">${k.product_name ?? ""}</td>
                    <td class="code" data-label="Vendor code">${k.vendor_sku === "" ? "(none)" : k.vendor_sku}</td>
                    <td data-label="Vendor unit">${k.vendor_uom === undefined || k.vendor_uom === "" ? "(ours)" : `${k.vendor_uom}, ${formatQty(k.qty_per_vendor_uom, "")} of ours`}</td>
                    <td class="pick">
                      <button type="button" class="btn btn-secondary small-btn" data-action="edit-code" data-product-id=${k.product_id} ?disabled=${model.busy}>Change</button>
                      <button type="button" class="btn btn-secondary small-btn" data-action="ask-remove-code" data-product-id=${k.product_id} ?disabled=${model.busy}>Remove</button>
                    </td>
                  </tr>`,
            )}
            ${model.editingCode === "new" ? codeEditor(model, null) : nothing}
            ${model.codes.length === 0 && model.editingCode !== "new" ? html`<tr><td colspan="5" class="dim" data-no-codes>No codes on file for this vendor.</td></tr>` : nothing}
          </tbody>
        </table>
      </div>
      ${model.removal === "code" && model.removalCode !== null
        ? confirmBox(
            `Remove the code ${model.removalCode.vendor_sku === "" ? "" : `${model.removalCode.vendor_sku} `}for ${model.removalCode.sku ?? "this product"}? Purchase orders will show none on file for it.`,
            "Remove the code",
            "do-remove-code",
            model.busy,
          )
        : html`<div class="actions end">
            ${model.editingCode === null ? html`<button type="button" class="btn btn-secondary small-btn" data-action="add-code" ?disabled=${model.busy}>Add a code</button>` : nothing}
          </div>`}
    </div>
  `;
}

function vendorPanel(model: VendorsModel, vendor: PurchasingVendor | "new"): TemplateResult {
  const tabButton = (id: VendorTab, word: string): TemplateResult =>
    html`<button type="button" class="tab" data-action="vendor-tab" data-choice=${id} aria-current="${model.tab === id ? "true" : "false"}">${word}</button>`;
  return html`
    <section class="panel" aria-label=${vendor === "new" ? "New vendor" : vendor.name} data-vendor-panel>
      <div class="panel-head">
        <h2>${vendor === "new" ? "New vendor" : vendor.name}</h2>
        ${vendor === "new"
          ? nothing
          : html`<div class="badge-row">${pill(vendor.is_active ? "good" : "plain", vendor.is_active ? "Active" : "Inactive")}<span class="dim mono">${vendor.vendor_no}</span>
              <button type="button" class="btn btn-secondary small-btn" data-action="close-vendor">Close</button></div>`}
      </div>
      ${vendor === "new"
        ? detailsForm(model, vendor)
        : html`
            <nav class="tabs" aria-label="Vendor">${tabButton("details", "Details")}${tabButton("contact", "Ordering contact")}${tabButton("codes", "Vendor codes")}</nav>
            ${model.tab === "details" ? detailsForm(model, vendor) : model.tab === "contact" ? contactTab(model) : codesTab(model)}
          `}
    </section>
  `;
}

export function renderVendors(model: VendorsModel): TemplateResult {
  if (model.phase === "loading") return loadingNote("Reading the vendors.");
  if (model.phase === "error") return loadFailed(model.error);
  return html`
    ${areaHead("Vendors", "The vendors we order from, who to send an order to, and each vendor's own codes for our products.")}
    ${noticeBox(model.notice)}
    <div class="split">
      <div>${vendorList(model)}${lookup(model)}</div>
      <div>${model.selected === null
        ? html`<section class="panel" data-vendor-empty>${emptyState("Pick a vendor.", "Its details, its ordering contact and its codes open here.")}</section>`
        : vendorPanel(model, model.selected)}</div>
    </div>
  `;
}
