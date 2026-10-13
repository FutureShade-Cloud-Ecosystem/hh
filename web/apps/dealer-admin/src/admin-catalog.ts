// The Catalog surface's screens: find a product, edit its description
// fields, set or remove its picture, and read the unit catalogue and the
// categories. Pure template functions over plain model objects; AdminWork
// owns every call and every state change.
//
// Port provenance: the editor's shape is the original app's product-edit.ts
// (repository hardscapeos_dibbits, read only; see PROVENANCE.md): the
// description fields grouped as a form, the changed-only save, the picture
// put and remove. The finder is new (the original opened the editor from a
// catalogue list; this app has no catalogue screen to open it from), and the
// units and categories cards are the two staff reads the surface is marked
// for. The list price reads through the shared formatter, and an absent
// anchor is words, never $0.00 (the original's DIB-404 rule).
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings, bound values only on data- and aria-
// attributes. Every field is mirrored in code; the picture's file input
// answers on the host's change listener.
import { html, nothing, type TemplateResult } from "lit";
import type { CategoryRow, ProductRow, UomRow } from "./admin-api.js";
import {
  PRODUCT_TEXT_FIELDS,
  catalogEmptyWords,
  listPriceWords,
} from "./admin-logic.js";

export interface CatalogModel {
  /** idle: nothing searched yet. */
  readonly phase: "loading" | "idle" | "ready";
  /** A search, a save or a picture write in flight. */
  readonly busy: boolean;
  /** A refused search's words. */
  readonly error: string;
  /** A save or a picture write's answer. */
  readonly notice: string;
  readonly q: string;
  readonly hits: readonly ProductRow[];
  /** The product in the editor, if one is open. */
  readonly opened: ProductRow | null;
  readonly units: readonly UomRow[];
  readonly categories: readonly CategoryRow[];
  /** The picked picture's words, for the line beside the input. */
  readonly pickedName: string;
  readonly altText: string;
}

/** A field's mirror key for one editor field. */
function editorField(key: string): string {
  return `pf-${key.replace(/_/g, "-")}`;
}

export function renderCatalog(model: CatalogModel): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Catalog</h1>
      <p class="page-sub">The products' words and their pictures: find one, describe it better, give it its picture.</p>
    </header>
    ${model.error === "" ? nothing : html`<p class="error-line" data-test="catalog-error">${model.error}</p>`}
    ${model.notice === "" ? nothing : html`<p class="notice-line" data-test="catalog-notice">${model.notice}</p>`}
    ${renderSearch(model)}
    ${renderEditor(model)}
    ${renderUnits(model.units)}
    ${renderCategories(model.categories)}
  `;
}

// -- the finder -----------------------------------------------------------------------------------

function renderSearch(model: CatalogModel): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Find a product</h2>
        ${model.phase === "ready" ? html`<span class="count" data-test="catalog-count">${model.hits.length} found</span>` : nothing}
      </div>
      <div class="filters">
        <input
          class="text-field"
          type="text"
          placeholder="A name, a SKU, a collection"
          aria-label="The product to find"
          data-field="catalog-q"
        />
        <button type="button" class="btn btn-primary" data-action="catalog-find" ?disabled=${model.busy}>
          ${model.busy && model.opened === null ? "Looking…" : "Find"}
        </button>
      </div>
      ${model.phase === "idle"
        ? html`<p class="notice-line" data-test="catalog-idle">Search the catalogue by name, SKU or collection to open a product.</p>`
        : nothing}
      ${model.phase === "ready" && model.hits.length === 0 && model.error === ""
        ? html`<p class="notice-line" data-test="catalog-empty">${catalogEmptyWords(model.q)}</p>`
        : nothing}
      ${model.phase === "ready" && model.hits.length > 0 ? renderHits(model) : nothing}
    </section>
  `;
}

function renderHits(model: CatalogModel): TemplateResult {
  return html`
    <div class="tablewrap">
      <table>
        <thead>
          <tr>
            <th scope="col">SKU</th>
            <th scope="col">Name</th>
            <th scope="col">Manufacturer</th>
            <th scope="col">Collection</th>
            <th scope="col">List price</th>
            <th scope="col"></th>
          </tr>
        </thead>
        <tbody>
          ${model.hits.map((row, i) => renderHitRow(row, i, model))}
        </tbody>
      </table>
    </div>
  `;
}

function renderHitRow(row: ProductRow, index: number, model: CatalogModel): TemplateResult {
  const open = row.id === model.opened?.id;
  return html`
    <tr data-test="catalog-row-${index}">
      <td class="mono">${row.sku}</td>
      <td>${row.name}${row.is_active ? nothing : html`<span class="chip chip-wait">inactive</span>`}</td>
      <td>${row.manufacturer}</td>
      <td>${row.collection}</td>
      <td class="mono">${listPriceWords(row.list_price_cents, row.list_price_uom)}</td>
      <td>
        ${open
          ? html`<span class="chip">In the editor</span>`
          : html`<button
              type="button"
              class="btn btn-secondary btn-small"
              data-action="catalog-open"
              data-product-id=${row.id}
              ?disabled=${model.busy}
            >
              Open
            </button>`}
      </td>
    </tr>
  `;
}

// -- the editor -----------------------------------------------------------------------------------

function renderEditor(model: CatalogModel): TemplateResult {
  const p = model.opened;
  if (p === null) {
    return html`
      <section class="card">
        <div class="card-head"><h2 class="card-title">The product editor</h2></div>
        <p class="notice-line" data-test="editor-idle">No product is open. Find one above, then Open it here.</p>
      </section>
    `;
  }
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">${p.name}</h2>
        <span class="chip mono">${p.sku}</span>
      </div>
      <p class="card-sub">
        ${listPriceWords(p.list_price_cents, p.list_price_uom)}
        ${p.sold_by === "" ? nothing : html`, sold by ${p.sold_by}`}
        ${p.base_uom === "" ? nothing : html`, stocked in ${p.base_uom}`}.
        ${p.minimum_order_qty === "" ? nothing : html`The minimum order is ${p.minimum_order_qty}.`}
      </p>
      <div class="form-grid">
        ${PRODUCT_TEXT_FIELDS.map(
          (f) => html`
            <label class="field ${f.wide ? "field-full" : ""}">
              <span class="field-label">${f.label}</span>
              <input class="text-field" type="text" data-field=${editorField(f.key)} aria-label=${f.label} />
            </label>
          `,
        )}
      </div>
      <div class="row-actions">
        <button type="button" class="btn btn-primary" data-action="catalog-save" ?disabled=${model.busy}>
          ${model.busy ? "Saving…" : "Save changes"}
        </button>
      </div>
      ${renderPicture(model)}
    </section>
  `;
}

function renderPicture(model: CatalogModel): TemplateResult {
  return html`
    <div class="picture-block">
      <div class="card-head">
        <h3 class="card-title">The picture</h3>
      </div>
      <p class="card-sub">One picture per product, a JPEG, a PNG or a WebP. It rides the product's own image route.</p>
      <div class="filters">
        <input
          class="text-field"
          type="text"
          placeholder="Words for the picture (alt)"
          aria-label="Words for the picture"
          data-field="catalog-alt"
        />
        <input
          type="file"
          accept="image/jpeg,image/png,image/webp"
          data-action="catalog-image-file"
          aria-label="Choose a picture"
        />
        <button type="button" class="btn btn-secondary" data-action="catalog-image-set" ?disabled=${model.busy || model.pickedName === ""}>
          Set picture
        </button>
        <button type="button" class="btn" data-action="catalog-image-remove" ?disabled=${model.busy}>Remove picture</button>
      </div>
      ${model.pickedName === "" ? nothing : html`<p class="notice-line" data-test="catalog-picked">Chosen: ${model.pickedName}</p>`}
    </div>
  `;
}

// -- the unit catalogue and the categories -----------------------------------------------------------

function renderUnits(units: readonly UomRow[]): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Units</h2>
        <span class="count" data-test="units-count">${units.length} units</span>
      </div>
      ${units.length === 0
        ? html`<p class="notice-line">The unit catalogue did not load.</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Code</th>
                    <th scope="col">Name</th>
                    <th scope="col">Dimension</th>
                  </tr>
                </thead>
                <tbody>
                  ${units.map(
                    (u) => html`
                      <tr data-test="units-row-${u.code}">
                        <td class="mono">${u.code}</td>
                        <td>${u.name}</td>
                        <td><span class="chip">${u.dimension}</span></td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function renderCategories(categories: readonly CategoryRow[]): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Categories</h2>
        <span class="count" data-test="categories-count">${categories.length} categories</span>
      </div>
      ${categories.length === 0
        ? html`<p class="notice-line">The categories did not load.</p>`
        : html`
            <div class="row-actions">
              ${categories.map(
                (c) => html`
                  <span class="chip" data-test="category-${c.code}">${c.name}${c.is_active ? nothing : html` (inactive)`}</span>
                `,
              )}
            </div>
          `}
    </section>
  `;
}
