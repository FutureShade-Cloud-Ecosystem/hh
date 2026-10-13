// The Pricing surface's screens: the tab strip over the twelve tabs, and one
// render function per landed tab. Pure template functions over plain model
// objects; pricing-controller.ts owns every call and every state change, and
// the model is read only here.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings; unquoted whole values in the data-
// and aria- namespaces; ?disabled is the one boolean. Controls ride pr-
// prefixed data-action names and app-root's host listeners forward them; a
// field's value is mirrored from its input or change event, never bound.
//
// A money figure on any tab is a server figure: the cells show what the
// server answered, the editors encode what the person typed, and nothing in
// between computes.
import { html, nothing, type TemplateResult } from "lit";
import { categoryName, marginEmptyWords, moneyWords, pctWords, pricingEmptyWords, PRICING_TABS } from "./pricing-logic.js";
import type { FloorPanel, PricingModel } from "./pricing-controller.js";

export function renderPricing(model: PricingModel): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Pricing</h1>
      <p class="page-sub">The dealer's price governance: the floors, the rules, and the records of what moved.</p>
    </header>
    <nav class="pr-tabs" role="group" aria-label="Pricing tabs">
      ${PRICING_TABS.map(
        (t) => html`
          <button
            type="button"
            class="pr-tab"
            data-action="pr-set-tab"
            data-tab=${t.id}
            aria-current=${model.tab === t.id ? "true" : "false"}
          >
            ${t.title}
          </button>
        `,
      )}
    </nav>
    ${renderTab(model)}
  `;
}

function renderTab(model: PricingModel): TemplateResult {
  // The router line: one case per landed tab. The next group's commit adds
  // its case beside this one; until then the tab says plainly it is not wired.
  if (model.tab === "margin") return renderMargin(model);
  const tab = PRICING_TABS.find((t) => t.id === model.tab);
  return html`<p class="notice-line">${pricingEmptyWords(tab?.title ?? model.tab)}</p>`;
}

// -- the margin policy tab ---------------------------------------------------------------------------

function policyAriaWords(name: string): string {
  return `Edit the margin policy for ${name}`;
}

export function renderMargin(model: PricingModel): TemplateResult {
  const m = model.margin;
  return html`
    ${m.phase === "loading" ? html`<p class="notice-line" data-test="pr-margin-loading">Loading the margin policies…</p>` : nothing}
    ${m.phase === "error" || m.phase === "refused"
      ? html`
          <p class="error-line" data-test="pr-margin-error">${m.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="pr-reload-margin">Try again</button></div>
        `
      : nothing}
    ${m.phase === "ready" ? renderPolicies(model) : nothing}
    ${m.phase === "ready" && m.editing !== null ? renderPolicyEditor(model) : nothing}
  `;
}

function renderPolicies(model: PricingModel): TemplateResult {
  const m = model.margin;
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Margin policies</h2>
        <span class="count" data-test="pr-margin-count">${m.rows.length} ${m.rows.length === 1 ? "policy" : "policies"}</span>
        <button type="button" class="btn btn-primary btn-small" data-action="pr-policy-edit">New policy</button>
      </div>
      ${m.notice === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-margin-notice">${m.notice}</p>`}
      ${m.rows.length === 0
        ? html`<p class="notice-line" data-test="pr-margin-empty">${marginEmptyWords()}</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Category</th>
                    <th scope="col" class="num">Margin target</th>
                    <th scope="col" class="num">Margin floor</th>
                    <th scope="col">Enforcement</th>
                    <th scope="col">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.rows.map((row) => renderPolicyRow(row, model))}
                </tbody>
              </table>
            </div>
          `}
      <p class="pr-hint">
        The target is the margin a price is graded to want; the floor is the margin a price may not pass. The resolve
        grades every price against the category's floor before it answers.
      </p>
    </section>
  `;
}

function renderPolicyRow(row: { readonly category_id: string; readonly margin_target_bps: number; readonly margin_floor_bps: number; readonly margin_enforcement_disabled: boolean }, model: PricingModel): TemplateResult {
  const name = categoryName(row.category_id, model.categories);
  return html`
    <tr data-category=${row.category_id}>
      <td>
        <span class="person">${name}</span>
        <span class="mono dim">${row.category_id === name ? "" : row.category_id}</span>
      </td>
      <td class="num" data-test="pr-margin-target">${pctWords(row.margin_target_bps)}</td>
      <td class="num" data-test="pr-margin-floor">${pctWords(row.margin_floor_bps)}</td>
      <td>
        ${row.margin_enforcement_disabled
          ? html`<span class="chip chip-danger">Off: bypasses are logged</span>`
          : html`<span class="chip">On</span>`}
      </td>
      <td>
        <button
          type="button"
          class="btn btn-secondary btn-small"
          data-action="pr-policy-edit"
          data-category=${row.category_id}
          aria-label=${policyAriaWords(name)}
        >
          Edit
        </button>
      </td>
    </tr>
  `;
}

function renderPolicyEditor(model: PricingModel): TemplateResult | typeof nothing {
  const m = model.margin;
  const editing = m.editing;
  if (editing === null) return nothing;
  const known = model.categories.some((c) => c.id === editing.categoryId);
  const heading = editing.categoryId !== "" ? categoryName(editing.categoryId, model.categories) : "New policy";
  return html`
    <section class="card" data-test="pr-policy-editor">
      <div class="card-head">
        <h2 class="card-title">Margin policy: ${heading}</h2>
        ${known || editing.categoryId === "" ? nothing : html`<span class="chip chip-danger">category not in the catalog</span>`}
      </div>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Category</span>
          <select class="select" data-field="pr-policy-category" ?disabled=${m.busy}>
            <option value="">Choose a category</option>
            ${model.categories.map((c) => html`<option>${c.name}</option>`)}
          </select>
        </label>
        <label class="field">
          <span class="field-label">Margin target, percent</span>
          <input class="text-field" type="text" inputmode="decimal" placeholder="35" data-field="pr-policy-target" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Margin floor, percent</span>
          <input class="text-field" type="text" inputmode="decimal" placeholder="20" data-field="pr-policy-floor" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Enforcement</span>
          <span class="branch-choice">
            <input type="checkbox" data-field="pr-policy-enforce" ?disabled=${m.busy} />
            <span>Disable the floor gate on this category (every bypass is logged)</span>
          </span>
        </label>
      </div>
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-policy-error">${m.formError}</p>`}
      ${m.floor === null ? nothing : renderFloorPanel(m.floor)}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-policy-save" ?disabled=${m.busy || m.floor !== null}>
          ${m.busy ? "Checking the floor…" : "Check the floor, then save"}
        </button>
        <button type="button" class="btn btn-secondary" data-action="pr-policy-cancel" ?disabled=${m.busy}>Cancel</button>
      </div>
    </section>
  `;
}

// -- the floor warning: the server graded, the screen shows the grade ---------------------------------

function floorItemWords(item: FloorPanel["items"][number]): string {
  return `${item.sku} ${item.name}`;
}

function renderFloorPanel(floor: FloorPanel): TemplateResult {
  return html`
    <div class="pr-warn" role="group" aria-label="The floor check's warning" data-test="pr-floor-panel">
      <span class="pr-warn-title">
        ${floor.items.length === 0
          ? "The check could not grade every product."
          : `The floor check names ${floor.items.length} ${floor.items.length === 1 ? "product" : "products"} this change puts at or under their floor:`}
      </span>
      ${floor.items.length === 0
        ? nothing
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Product</th>
                    <th scope="col" class="num">Price now</th>
                    <th scope="col" class="num">Price with the change</th>
                    <th scope="col" class="num">Margin with the change</th>
                    <th scope="col" class="num">Floor</th>
                    <th scope="col">Was under the floor already</th>
                  </tr>
                </thead>
                <tbody>
                  ${floor.items.map(
                    (item) => html`
                      <tr data-product=${item.product_id}>
                        <td>${floorItemWords(item)}</td>
                        <td class="num">${moneyWords(item.previous_final_cents)}</td>
                        <td class="num">${moneyWords(item.final_cents)}</td>
                        <td class="num">${pctWords(item.margin_bps)}</td>
                        <td class="num">${pctWords(item.floor_bps)}</td>
                        <td>${item.already_under_floor ? html`<span class="chip chip-dim">yes</span>` : html`<span class="chip chip-danger">no</span>`}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
      ${floor.notes.map((line) => html`<p>${line}</p>`)}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-policy-confirm" ?disabled=${floor.busy}>
          ${floor.busy ? "Saving…" : "Save anyway, and log that I saw this"}
        </button>
        <button type="button" class="btn btn-secondary" data-action="pr-policy-review" ?disabled=${floor.busy}>Go back</button>
      </div>
    </div>
  `;
}
