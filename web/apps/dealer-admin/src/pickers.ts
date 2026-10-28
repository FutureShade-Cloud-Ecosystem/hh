// Inline customer and product search-and-pick helpers for Dealer Admin forms (UX-117).
//
// Reuses the house picker logic from @hardscapehouse/pos-kit:
// - _customer-picker.ts (customerTypeWord, isSearchable, orderResults, pickerStatus, termsLabel, PickerCustomer)
// - _pos-searchdrop.ts (DROP_ROWS, dropView, shouldQuery, DropView)
//
// Template bindings stay strictly within the design-system position gate:
// only text content, class, role, data-*, aria-*, and ?disabled are bound.
import { html, nothing, type TemplateResult } from "lit";
import { ApiClient, staffCustomer, staffProduct } from "@hardscapehouse/api-client";
import {
  customerTypeWord,
  isSearchable,
  orderResults,
  pickerStatus,
  termsLabel,
  type PickerCustomer,
} from "@hardscapehouse/pos-kit/src/ported/_customer-picker.js";
import {
  DROP_ROWS,
  dropView,
  shouldQuery,
  type DropView,
} from "@hardscapehouse/pos-kit/src/ported/_pos-searchdrop.js";
import type { CounterHit } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";
import { UNKNOWN_CUSTOMER, UNKNOWN_PRODUCT } from "./placeholders.js";

export interface EntityPickerSpec {
  readonly field: string;
  readonly selectedId: string;
  readonly emptyLabel: string;
  readonly ariaLabel: string;
  readonly disabled?: boolean;
}

export class DealerPickers {
  readonly #client: ApiClient;
  readonly #repaint: () => void;

  readonly #queries = new Map<string, string>();
  readonly #customerHits = new Map<string, readonly PickerCustomer[]>();
  readonly #productDrops = new Map<string, DropView>();
  readonly #loading = new Set<string>();
  readonly #errors = new Map<string, string>();
  #openField = "";

  readonly #customerNames = new Map<string, string>([
    ["cu110000-0000-4000-9000-0000000000a1", "Point Loma Hardscape & Masonry (ACCT-1042)"],
    ["cu110000-0000-4000-9000-0000000000a2", "Julian Stoneworks (ACCT-1088)"],
  ]);

  readonly #productNames = new Map<string, string>([
    ["fa110000-0000-4000-9000-0000000000a1", "Paver 60 Charcoal (PAV-60-CH)"],
    ["fa110000-0000-4000-9000-0000000000a2", "Wall block 8 (WAL-8)"],
    ["3a000000-0000-4000-9000-000000000001", "Charcoal 60 paver (PAV-60-CH)"],
    ["3a000000-0000-4000-9000-000000000002", "Seminole wall block (WALL-SEM-BR)"],
  ]);

  readonly #recentCustomers: string[] = [];

  constructor(client: ApiClient, repaint: () => void) {
    this.#client = client;
    this.#repaint = repaint;
  }

  queryOf(field: string): string {
    return this.#queries.get(field) ?? "";
  }

  customerLabel(id: string): string {
    if (id === "") return "";
    return this.#customerNames.get(id) ?? UNKNOWN_CUSTOMER;
  }

  productLabel(id: string): string {
    if (id === "") return "";
    return this.#productNames.get(id) ?? UNKNOWN_PRODUCT;
  }

  rememberProduct(id: string, sku: string, name: string): void {
    if (id === "") return;
    const label = name !== "" && sku !== "" ? `${name} (${sku})` : name !== "" ? name : sku;
    if (label !== "") this.#productNames.set(id, label);
  }

  async searchCustomer(field: string, q: string): Promise<void> {
    this.#queries.set(field, q);
    this.#errors.delete(field);
    if (!isSearchable(q)) {
      this.#customerHits.delete(field);
      if (this.#openField === field) this.#openField = "";
      this.#repaint();
      return;
    }
    this.#openField = field;
    this.#loading.add(field);
    this.#repaint();
    const res = await staffCustomer.customerSearch(this.#client, { q: q.trim(), limit: 8 });
    this.#loading.delete(field);
    if (!res.ok) {
      this.#errors.set(field, res.error || "Customer search failed.");
      this.#customerHits.set(field, []);
      this.#repaint();
      return;
    }
    const rawList = Array.isArray(res.value) ? res.value : [];
    const normalized: PickerCustomer[] = rawList.map((row) => ({
      id: String(row.id ?? ""),
      name: String(row.name ?? ""),
      account_no: String(row.account_no ?? ""),
      customer_type: String(row.customer_type ?? ""),
      payment_terms_days: typeof row.payment_terms_days === "number" ? row.payment_terms_days : 0,
    }));
    const ordered = orderResults(normalized, this.#recentCustomers);
    for (const c of ordered) {
      if (c.id !== "" && c.name !== "") {
        this.#customerNames.set(c.id, c.account_no !== "" ? `${c.name} (${c.account_no})` : c.name);
      }
    }
    this.#customerHits.set(field, ordered);
    this.#repaint();
  }

  async searchProduct(field: string, q: string): Promise<void> {
    this.#queries.set(field, q);
    this.#errors.delete(field);
    if (!shouldQuery(q)) {
      this.#productDrops.delete(field);
      if (this.#openField === field) this.#openField = "";
      this.#repaint();
      return;
    }
    this.#openField = field;
    this.#loading.add(field);
    this.#repaint();
    const res = await staffProduct.productCounterSearch(this.#client, { q: q.trim(), limit: 12 });
    this.#loading.delete(field);
    if (!res.ok) {
      this.#errors.set(field, res.error || "Product search failed.");
      this.#productDrops.set(field, dropView([], DROP_ROWS));
      this.#repaint();
      return;
    }
    const items = Array.isArray(res.value?.items) ? (res.value.items as unknown as CounterHit[]) : [];
    const drop = dropView(items, DROP_ROWS);
    for (const h of drop.rows) {
      this.rememberProduct(h.product_id, h.sku, h.name);
    }
    this.#productDrops.set(field, drop);
    this.#repaint();
  }

  choose(field: string, id: string, label: string, kind: "customer" | "product"): void {
    if (kind === "customer" && id !== "" && label !== "") {
      this.#customerNames.set(id, label);
      const idx = this.#recentCustomers.indexOf(id);
      if (idx >= 0) this.#recentCustomers.splice(idx, 1);
      this.#recentCustomers.unshift(id);
    }
    if (kind === "product" && id !== "" && label !== "") {
      this.#productNames.set(id, label);
    }
    this.#queries.set(field, "");
    this.#customerHits.delete(field);
    this.#productDrops.delete(field);
    if (this.#openField === field) this.#openField = "";
  }

  clear(field: string): void {
    this.#queries.set(field, "");
    this.#customerHits.delete(field);
    this.#productDrops.delete(field);
    if (this.#openField === field) this.#openField = "";
  }

  reset(): void {
    this.#queries.clear();
    this.#customerHits.clear();
    this.#productDrops.clear();
    this.#loading.clear();
    this.#errors.clear();
    this.#openField = "";
  }

  renderCustomerPicker(spec: EntityPickerSpec): TemplateResult {
    const selected = spec.selectedId !== "";
    const currentText = selected ? this.customerLabel(spec.selectedId) : spec.emptyLabel;
    const isOpen = this.#openField === spec.field;
    const hits = this.#customerHits.get(spec.field) ?? [];
    const loading = this.#loading.has(spec.field);
    const error = this.#errors.get(spec.field) ?? "";
    const query = this.queryOf(spec.field);
    const statusText = isOpen ? pickerStatus({ query, loading, error, count: hits.length }) : "";

    return html`
      <div class="hh-entity-picker" data-picker=${spec.field} data-picker-kind="customer">
        <div class="picker-selected" data-test=${`picker-selected-${spec.field}`}>
          <span class="picker-current ${selected ? "" : "picker-empty"}">${currentText}</span>
          ${selected && !spec.disabled
            ? html`<button
                type="button"
                class="btn btn-secondary btn-small picker-clear"
                data-action="picker-clear"
                data-picker-field=${spec.field}
                aria-label=${`Clear ${spec.ariaLabel}`}
              >
                Clear
              </button>`
            : nothing}
        </div>
        <input
          class="text-field picker-search"
          type="search"
          placeholder="Name or account number"
          aria-label=${spec.ariaLabel}
          data-picker-search=${spec.field}
          data-picker-kind="customer"
          ?disabled=${spec.disabled ?? false}
        />
        ${isOpen
          ? html`
              <div class="picker-results" role="listbox" aria-label=${`${spec.ariaLabel} matches`}>
                ${hits.map((c) => {
                  const fullLabel = c.account_no !== "" ? `${c.name} (${c.account_no})` : c.name;
                  const meta = `${c.account_no} · ${customerTypeWord(c.customer_type)} · ${termsLabel(c.payment_terms_days)}`;
                  return html`
                    <button
                      type="button"
                      class="picker-option"
                      role="option"
                      data-action="picker-choose"
                      data-picker-field=${spec.field}
                      data-picker-kind="customer"
                      data-picker-id=${c.id}
                      data-picker-label=${fullLabel}
                    >
                      <span class="picker-option-name">${c.name}</span>
                      <span class="picker-option-meta">${meta}</span>
                    </button>
                  `;
                })}
                <p class="picker-status" role="status">${statusText}</p>
              </div>
            `
          : nothing}
      </div>
    `;
  }

  renderProductPicker(spec: EntityPickerSpec): TemplateResult {
    const selected = spec.selectedId !== "";
    const currentText = selected ? this.productLabel(spec.selectedId) : spec.emptyLabel;
    const isOpen = this.#openField === spec.field;
    const drop = this.#productDrops.get(spec.field);
    const rows = drop?.rows ?? [];
    const loading = this.#loading.has(spec.field);
    const error = this.#errors.get(spec.field) ?? "";
    const query = this.queryOf(spec.field);

    return html`
      <div class="hh-entity-picker" data-picker=${spec.field} data-picker-kind="product">
        <div class="picker-selected" data-test=${`picker-selected-${spec.field}`}>
          <span class="picker-current ${selected ? "" : "picker-empty"}">${currentText}</span>
          ${selected && !spec.disabled
            ? html`<button
                type="button"
                class="btn btn-secondary btn-small picker-clear"
                data-action="picker-clear"
                data-picker-field=${spec.field}
                aria-label=${`Clear ${spec.ariaLabel}`}
              >
                Clear
              </button>`
            : nothing}
        </div>
        <input
          class="text-field picker-search"
          type="search"
          placeholder="Search products by name or SKU"
          aria-label=${spec.ariaLabel}
          data-picker-search=${spec.field}
          data-picker-kind="product"
          ?disabled=${spec.disabled ?? false}
        />
        ${isOpen
          ? html`
              <div class="picker-results" role="listbox" aria-label=${`${spec.ariaLabel} matches`}>
                ${rows.map((h) => {
                  const fullLabel = h.sku !== "" ? `${h.name} (${h.sku})` : h.name;
                  const meta = `${h.sku}${h.base_uom ? ` · ${h.base_uom}` : ""}`;
                  return html`
                    <button
                      type="button"
                      class="picker-option"
                      role="option"
                      data-action="picker-choose"
                      data-picker-field=${spec.field}
                      data-picker-kind="product"
                      data-picker-id=${h.product_id}
                      data-picker-label=${fullLabel}
                    >
                      <span class="picker-option-name">${h.name}</span>
                      <span class="picker-option-meta">${meta}</span>
                    </button>
                  `;
                })}
                ${loading
                  ? html`<p class="picker-status" role="status">Searching products…</p>`
                  : error !== ""
                    ? html`<p class="picker-status" role="status">${error}</p>`
                    : rows.length === 0
                      ? html`<p class="picker-status" role="status">No products match ${query.trim()}.</p>`
                      : drop && drop.overflow > 0
                        ? html`<p class="picker-status" role="status">+${drop.overflow} more matches; keep typing to narrow.</p>`
                        : nothing}
              </div>
            `
          : nothing}
      </div>
    `;
  }
}
