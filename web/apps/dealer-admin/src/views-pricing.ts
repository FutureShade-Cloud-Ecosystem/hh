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
import {
  bandWords,
  breakItemWords,
  breaksEmptyWords,
  calcTypeWord,
  categoryName,
  CUSTOMER_TYPES,
  customerTypeWord,
  marginEmptyWords,
  moneyWords,
  overrideEmptyWords,
  overrideValueWords,
  pctWords,
  pricingEmptyWords,
  RULE_SCOPES,
  ruleEmptyWords,
  ruleValueWords,
  scopeWord,
  validityWords,
  PRICING_TABS,
} from "./pricing-logic.js";
import type { BreakRow, OverrideRow, RuleRow } from "./pricing-api.js";
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
  if (model.tab === "rules") return renderRules(model);
  if (model.tab === "customers") return renderCustomers(model);
  if (model.tab === "breaks") return renderBreaks(model);
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
      ${m.floor === null ? nothing : renderFloorPanel(m.floor, "pr-policy-confirm", "pr-policy-review")}
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

function renderFloorPanel(floor: FloorPanel, confirmAction: string, reviewAction: string): TemplateResult {
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
        <button type="button" class="btn btn-primary" data-action=${confirmAction} ?disabled=${floor.busy}>
          ${floor.busy ? "Saving…" : "Save anyway, and log that I saw this"}
        </button>
        <button type="button" class="btn btn-secondary" data-action=${reviewAction} ?disabled=${floor.busy}>Go back</button>
      </div>
    </section>
  `;
}

// -- the rules tab -------------------------------------------------------------------------------------

function ruleDimsWords(rule: { readonly scope: string; readonly customer_id: string; readonly customer_type: string; readonly category_id: string; readonly product_id: string; readonly branch_id: string }, model: PricingModel): string {
  const parts: string[] = [];
  if (rule.customer_id !== "") parts.push(`customer ${rule.customer_id.slice(0, 8)}`);
  if (rule.customer_type !== "") parts.push(customerTypeWord(rule.customer_type));
  if (rule.category_id !== "") parts.push(categoryName(rule.category_id, model.categories));
  if (rule.product_id !== "") parts.push(`product ${rule.product_id.slice(0, 8)}`);
  if (rule.branch_id !== "") parts.push(`branch ${rule.branch_id.slice(0, 8)}`);
  return parts.join(" · ");
}

export function renderRules(model: PricingModel): TemplateResult {
  const m = model.rules;
  return html`
    ${m.phase === "loading" ? html`<p class="notice-line" data-test="pr-rules-loading">Loading the pricing rules…</p>` : nothing}
    ${m.phase === "error" || m.phase === "refused"
      ? html`
          <p class="error-line" data-test="pr-rules-error">${m.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="pr-rules-reload">Try again</button></div>
        `
      : nothing}
    ${m.phase === "ready" ? renderRulesList(model) : nothing}
    ${m.phase === "ready" && m.editing !== null ? renderRuleEditor(model) : nothing}
  `;
}

function renderRulesList(model: PricingModel): TemplateResult {
  const m = model.rules;
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Pricing rules</h2>
        <span class="count" data-test="pr-rules-count">${m.rows.length} ${m.rows.length === 1 ? "rule" : "rules"}</span>
        <button type="button" class="btn btn-primary btn-small" data-action="pr-rule-edit">New rule</button>
      </div>
      ${m.notice === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-rules-notice">${m.notice}</p>`}
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-rules-form-error">${m.formError}</p>`}
      <div class="filters" role="group" aria-label="Rule filters">
        <select class="select" aria-label="Scope" data-field="pr-rules-scope">
          <option value="">Scope: all</option>
          <option value="CUSTOMER_PRODUCT">${scopeWord("CUSTOMER_PRODUCT")}</option>
          <option value="CUSTOMER_CATEGORY">${scopeWord("CUSTOMER_CATEGORY")}</option>
          <option value="TIER_CATEGORY">${scopeWord("TIER_CATEGORY")}</option>
          <option value="TIER_DEFAULT">${scopeWord("TIER_DEFAULT")}</option>
        </select>
        <select class="select" aria-label="Customer type" data-field="pr-rules-type">
          <option value="">Customer type: all</option>
          <option value="CONTRACTOR">${customerTypeWord("CONTRACTOR")}</option>
          <option value="RETAIL">${customerTypeWord("RETAIL")}</option>
          <option value="BUILDER">${customerTypeWord("BUILDER")}</option>
          <option value="GOVERNMENT">${customerTypeWord("GOVERNMENT")}</option>
          <option value="INTERNAL">${customerTypeWord("INTERNAL")}</option>
        </select>
        <button type="button" class="btn btn-secondary" data-action="pr-rules-apply">Apply</button>
        <button type="button" class="btn btn-secondary" data-action="pr-rules-clear">Clear</button>
      </div>
      ${m.rows.length === 0
        ? html`<p class="notice-line" data-test="pr-rules-empty">${ruleEmptyWords()}</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Scope</th>
                    <th scope="col">Reaches</th>
                    <th scope="col">Value</th>
                    <th scope="col" class="num">Priority</th>
                    <th scope="col">Valid</th>
                    <th scope="col">State</th>
                    <th scope="col">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.rows.map((rule) => renderRuleRow(rule, model))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function renderRuleRow(rule: RuleRow, model: PricingModel): TemplateResult {
  const m = model.rules;
  const confirming = m.confirmingId === rule.id;
  return html`
    <tr data-rule=${rule.id}>
      <td><span class="chip">${scopeWord(rule.scope)}</span></td>
      <td>
        <div>${ruleDimsWords(rule, model)}</div>
        <div class="mono dim">${rule.detail_text}</div>
      </td>
      <td class="num" data-test="pr-rule-value">${ruleValueWords(rule)}</td>
      <td class="num">${rule.priority}</td>
      <td class="mono dim">${validityWords(rule.valid_from, rule.valid_to)}</td>
      <td>
        ${rule.is_active
          ? html`<span class="chip chip-good">active</span>`
          : html`<span class="chip chip-dim">retired</span>`}
      </td>
      <td>
        <div class="pr-actions">
          <button
            type="button"
            class="btn btn-secondary btn-small"
            data-action="pr-rule-edit"
            data-id=${rule.id}
            aria-label="Edit the rule ${rule.detail_text}"
          >
            Edit
          </button>
          ${rule.is_active
            ? html`
                <button
                  type="button"
                  class="btn btn-secondary btn-small"
                  data-action="pr-rule-retire"
                  data-id=${rule.id}
                  ?disabled=${m.busy}
                >
                  ${confirming ? "Retire: tap again" : "Retire"}
                </button>
              `
            : nothing}
        </div>
      </td>
    </tr>
  `;
}

function renderRuleEditor(model: PricingModel): TemplateResult | typeof nothing {
  const m = model.rules;
  const e = m.editing;
  if (e === null) return nothing;
  const heading = e.id === "" ? "New rule" : `Rule: ${e.detail === "" ? e.id.slice(0, 8) : e.detail}`;
  return html`
    <section class="card" data-test="pr-rule-editor">
      <div class="card-head">
        <h2 class="card-title">${heading}</h2>
        ${e.id === "" ? html`<span class="chip chip-wait">create</span>` : html`<span class="chip">version ${e.version}</span>`}
      </div>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Scope</span>
          <select class="select" data-field="pr-rule-scope" ?disabled=${m.busy}>
            <option value="">Choose a scope</option>
            <option value="CUSTOMER_PRODUCT">${scopeWord("CUSTOMER_PRODUCT")}</option>
            <option value="CUSTOMER_CATEGORY">${scopeWord("CUSTOMER_CATEGORY")}</option>
            <option value="TIER_CATEGORY">${scopeWord("TIER_CATEGORY")}</option>
            <option value="TIER_DEFAULT">${scopeWord("TIER_DEFAULT")}</option>
          </select>
        </label>
        <label class="field">
          <span class="field-label">Customer id (empty unless the rule names one)</span>
          <input class="text-field" type="text" data-field="pr-rule-customer" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Customer type</span>
          <select class="select" data-field="pr-rule-type" ?disabled=${m.busy}>
            <option value="">Any</option>
            <option value="CONTRACTOR">${customerTypeWord("CONTRACTOR")}</option>
            <option value="RETAIL">${customerTypeWord("RETAIL")}</option>
            <option value="BUILDER">${customerTypeWord("BUILDER")}</option>
            <option value="GOVERNMENT">${customerTypeWord("GOVERNMENT")}</option>
            <option value="INTERNAL">${customerTypeWord("INTERNAL")}</option>
          </select>
        </label>
        <label class="field">
          <span class="field-label">Category</span>
          <select class="select" data-field="pr-rule-category" ?disabled=${m.busy}>
            <option value="">Any category</option>
            ${model.categories.map((c) => html`<option>${c.name}</option>`)}
          </select>
        </label>
        <label class="field">
          <span class="field-label">Product id (empty unless the rule names one)</span>
          <input class="text-field" type="text" data-field="pr-rule-product" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Branch id (empty prices every yard)</span>
          <input class="text-field" type="text" data-field="pr-rule-branch" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">What the rule does</span>
          <select class="select" data-field="pr-rule-calc" ?disabled=${m.busy}>
            <option value="">Choose a calc</option>
            <option value="ABSOLUTE">${calcTypeWord("ABSOLUTE")}</option>
            <option value="PCT_OFF_LIST">${calcTypeWord("PCT_OFF_LIST")}</option>
            <option value="MARKUP_ON_COST">${calcTypeWord("MARKUP_ON_COST")}</option>
          </select>
        </label>
        ${e.calc === "ABSOLUTE"
          ? html`<label class="field"><span class="field-label">Fixed price, dollars</span>
              <input class="text-field" type="text" inputmode="decimal" data-field="pr-rule-price" ?disabled=${m.busy} /></label>`
          : nothing}
        ${e.calc === "PCT_OFF_LIST"
          ? html`<label class="field"><span class="field-label">Discount, percent off list</span>
              <input class="text-field" type="text" inputmode="decimal" data-field="pr-rule-discount" ?disabled=${m.busy} /></label>`
          : nothing}
        ${e.calc === "MARKUP_ON_COST"
          ? html`<label class="field"><span class="field-label">Markup, percent on cost</span>
              <input class="text-field" type="text" inputmode="decimal" data-field="pr-rule-markup" ?disabled=${m.busy} /></label>`
          : nothing}
        <label class="field">
          <span class="field-label">Price unit</span>
          <input class="text-field" type="text" placeholder="SF" data-field="pr-rule-uom" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Priority (lower runs first)</span>
          <input class="text-field" type="text" inputmode="numeric" data-field="pr-rule-priority" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Valid from (empty is open)</span>
          <input class="text-field" type="date" data-field="pr-rule-from" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Valid to (empty is open)</span>
          <input class="text-field" type="date" data-field="pr-rule-to" ?disabled=${m.busy} />
        </label>
        <label class="field field-full">
          <span class="field-label">What it says, as the list shows it</span>
          <input class="text-field" type="text" data-field="pr-rule-detail" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Active</span>
          <span class="branch-choice">
            <input type="checkbox" data-field="pr-rule-active" ?disabled=${m.busy} />
            <span>An inactive rule never resolves</span>
          </span>
        </label>
      </div>
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-rule-error">${m.formError}</p>`}
      ${m.floor === null ? nothing : renderFloorPanel(m.floor, "pr-rule-confirm", "pr-rule-review")}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-rule-save" ?disabled=${m.busy || m.floor !== null}>
          ${m.busy ? "Checking the floor…" : e.id === "" ? "Check the floor, then create" : "Check the floor, then save"}
        </button>
        <button type="button" class="btn btn-secondary" data-action="pr-rule-cancel" ?disabled=${m.busy}>Cancel</button>
      </div>
    </section>
  `;
}

// -- the customer prices tab ---------------------------------------------------------------------------

export function renderCustomers(model: PricingModel): TemplateResult {
  const m = model.customers;
  const asked = m.loadedFor !== "";
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Customer prices</h2>
        ${m.rows.length === 0 ? nothing : html`<span class="count" data-test="pr-cust-count">${m.rows.length} ${m.rows.length === 1 ? "price" : "prices"}</span>`}
        ${asked
          ? html`<button type="button" class="btn btn-primary btn-small" data-action="pr-cust-edit">New price</button>`
          : nothing}
      </div>
      <p class="pr-hint">
        The category prices one account rides over its tier rules. The pricing reads here are the contract's own: this
        surface holds no customer search, so the id is typed whole.
      </p>
      <div class="filters" role="group" aria-label="The customer">
        <input class="text-field" type="text" placeholder="Customer id" aria-label="Customer id" data-field="pr-cust-id" />
        <button type="button" class="btn btn-primary" data-action="pr-cust-load" ?disabled=${m.busy}>Load prices</button>
      </div>
      ${m.phase === "loading" ? html`<p class="notice-line" data-test="pr-cust-loading">Loading the customer's prices…</p>` : nothing}
      ${m.phase === "error" || m.phase === "refused"
        ? html`<p class="error-line" data-test="pr-cust-error">${m.error}</p>`
        : nothing}
      ${m.notice === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-cust-notice">${m.notice}</p>`}
      ${m.phase === "ready" ? renderOverrides(model, asked) : nothing}
    </section>
    ${m.phase === "ready" && m.editing !== null ? renderOverrideEditor(model) : nothing}
  `;
}

function renderOverrides(model: PricingModel, asked: boolean): TemplateResult | typeof nothing {
  const m = model.customers;
  if (!asked) return html`<p class="notice-line" data-test="pr-cust-empty">${overrideEmptyWords(false)}</p>`;
  if (m.rows.length === 0) return html`<p class="notice-line" data-test="pr-cust-empty">${overrideEmptyWords(true)}</p>`;
  return html`
    <div class="tablewrap">
      <table>
        <thead>
          <tr>
            <th scope="col">Category</th>
            <th scope="col">Value</th>
            <th scope="col">Notes</th>
            <th scope="col">Actions</th>
          </tr>
        </thead>
        <tbody>
          ${m.rows.map((row) => renderOverrideRow(row, model))}
        </tbody>
      </table>
    </div>
  `;
}

function renderOverrideRow(row: OverrideRow, model: PricingModel): TemplateResult {
  const m = model.customers;
  const confirming = m.confirmingId === row.id;
  return html`
    <tr data-override=${row.id}>
      <td><span class="person">${categoryName(row.category_id, model.categories)}</span></td>
      <td class="num" data-test="pr-cust-value">${overrideValueWords(row)}</td>
      <td class="dim">${row.notes}</td>
      <td>
        <div class="pr-actions">
          <button
            type="button"
            class="btn btn-secondary btn-small"
            data-action="pr-cust-edit"
            data-id=${row.id}
            aria-label=${`Edit the price for ${categoryName(row.category_id, model.categories)}`}
          >
            Edit
          </button>
          <button
            type="button"
            class="btn btn-secondary btn-small"
            data-action="pr-cust-del"
            data-id=${row.id}
            ?disabled=${m.busy}
          >
            ${confirming ? "Delete: tap again" : "Delete"}
          </button>
        </div>
      </td>
    </tr>
  `;
}

function renderOverrideEditor(model: PricingModel): TemplateResult | typeof nothing {
  const m = model.customers;
  const e = m.editing;
  if (e === null) return nothing;
  const heading = e.id === "" ? "New category price" : `Category price: ${categoryName(e.categoryId, model.categories)}`;
  return html`
    <section class="card" data-test="pr-cust-editor">
      <div class="card-head">
        <h2 class="card-title">${heading}</h2>
        ${e.id === "" ? html`<span class="chip chip-wait">create</span>` : html`<span class="chip">version ${e.version}</span>`}
      </div>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Category</span>
          <select class="select" data-field="pr-cust-category" ?disabled=${m.busy}>
            <option value="">Choose a category</option>
            ${model.categories.map((c) => html`<option>${c.name}</option>`)}
          </select>
        </label>
        <label class="field">
          <span class="field-label">What the price does</span>
          <select class="select" data-field="pr-cust-calc" ?disabled=${m.busy}>
            <option value="">Choose a calc</option>
            <option value="PCT_OFF_LIST">${calcTypeWord("PCT_OFF_LIST")}</option>
            <option value="MARKUP_ON_COST">${calcTypeWord("MARKUP_ON_COST")}</option>
          </select>
        </label>
        ${e.calc === "PCT_OFF_LIST"
          ? html`<label class="field"><span class="field-label">Discount, percent off list</span>
              <input class="text-field" type="text" inputmode="decimal" data-field="pr-cust-discount" ?disabled=${m.busy} /></label>`
          : nothing}
        ${e.calc === "MARKUP_ON_COST"
          ? html`<label class="field"><span class="field-label">Markup, percent on cost</span>
              <input class="text-field" type="text" inputmode="decimal" data-field="pr-cust-markup" ?disabled=${m.busy} /></label>`
          : nothing}
        <label class="field">
          <span class="field-label">Price unit</span>
          <input class="text-field" type="text" placeholder="SF" data-field="pr-cust-uom" ?disabled=${m.busy} />
        </label>
        <label class="field field-full">
          <span class="field-label">Notes, as the row shows them</span>
          <input class="text-field" type="text" data-field="pr-cust-notes" ?disabled=${m.busy} />
        </label>
      </div>
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-cust-form-error">${m.formError}</p>`}
      ${m.floor === null ? nothing : renderFloorPanel(m.floor, "pr-cust-confirm", "pr-cust-review")}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-cust-save" ?disabled=${m.busy || m.floor !== null}>
          ${m.busy ? "Checking the floor…" : e.id === "" ? "Check the floor, then add" : "Check the floor, then save"}
        </button>
        <button type="button" class="btn btn-secondary" data-action="pr-cust-cancel" ?disabled=${m.busy}>Cancel</button>
      </div>
    </section>
  `;
}

// -- the quantity breaks tab ---------------------------------------------------------------------------

function breakScopeWords(row: BreakRow, model: PricingModel): string {
  if (row.product_id !== "") return `product ${row.product_id.slice(0, 8)}`;
  if (row.category_id !== "") return categoryName(row.category_id, model.categories);
  return "unscoped";
}

export function renderBreaks(model: PricingModel): TemplateResult {
  const m = model.breaks;
  return html`
    ${m.phase === "loading" ? html`<p class="notice-line" data-test="pr-breaks-loading">Loading the quantity breaks…</p>` : nothing}
    ${m.phase === "error" || m.phase === "refused"
      ? html`
          <p class="error-line" data-test="pr-breaks-error">${m.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="pr-breaks-reload">Try again</button></div>
        `
      : nothing}
    ${m.phase === "ready" ? renderBreaksList(model) : nothing}
    ${m.phase === "ready" && m.editing !== null ? renderBreakEditor(model) : nothing}
  `;
}

function renderBreaksList(model: PricingModel): TemplateResult {
  const m = model.breaks;
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Quantity break schedules</h2>
        <span class="count" data-test="pr-breaks-count">${m.rows.length} ${m.rows.length === 1 ? "schedule" : "schedules"}</span>
        <button type="button" class="btn btn-primary btn-small" data-action="pr-break-edit">New schedule</button>
      </div>
      ${m.notice === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-breaks-notice">${m.notice}</p>`}
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-breaks-form-error">${m.formError}</p>`}
      ${m.rows.length === 0
        ? html`<p class="notice-line" data-test="pr-breaks-empty">${breaksEmptyWords()}</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Schedule</th>
                    <th scope="col">Covers</th>
                    <th scope="col">Tiers</th>
                    <th scope="col">State</th>
                    <th scope="col">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.rows.map((row) => renderBreakRowView(row, model))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function renderBreakRowView(row: BreakRow, model: PricingModel): TemplateResult {
  const m = model.breaks;
  const confirming = m.confirmingId === row.id;
  return html`
    <tr data-break=${row.id}>
      <td>
        <div class="person">${row.name}</div>
        <div class="mono dim">${row.code} · by ${row.threshold_uom}</div>
      </td>
      <td>${breakScopeWords(row, model)}</td>
      <td>
        ${row.items.length === 0
          ? html`<span class="dim">no tiers</span>`
          : row.items.map(
              (item) => html`
                <div class="mono">${bandWords(item.from_qty, item.to_qty)}: ${breakItemWords(item)}</div>
              `,
            )}
      </td>
      <td>
        ${row.is_active ? html`<span class="chip chip-good">active</span>` : html`<span class="chip chip-dim">retired</span>`}
      </td>
      <td>
        <div class="pr-actions">
          <button
            type="button"
            class="btn btn-secondary btn-small"
            data-action="pr-break-edit"
            data-id=${row.id}
            aria-label=${`Edit the schedule ${row.name}`}
          >
            Edit
          </button>
          ${row.is_active
            ? html`
                <button type="button" class="btn btn-secondary btn-small" data-action="pr-break-del" data-id=${row.id} ?disabled=${m.busy}>
                  ${confirming ? "Retire: tap again" : "Retire"}
                </button>
              `
            : nothing}
        </div>
      </td>
    </tr>
  `;
}

function renderBreakEditor(model: PricingModel): TemplateResult | typeof nothing {
  const m = model.breaks;
  const e = m.editing;
  if (e === null) return nothing;
  const heading = e.id === "" ? "New schedule" : `Schedule: ${e.nameInput === "" ? e.code : e.nameInput}`;
  const tierWord = (n: number): string => `Tier ${n + 1}`;
  return html`
    <section class="card" data-test="pr-break-editor">
      <div class="card-head">
        <h2 class="card-title">${heading}</h2>
        ${e.id === "" ? html`<span class="chip chip-wait">create</span>` : html`<span class="chip">version ${e.version}</span>`}
      </div>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Code</span>
          <input class="text-field" type="text" data-field="pr-brk-code" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Name</span>
          <input class="text-field" type="text" data-field="pr-brk-name" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Category (or name a product below; exactly one wins)</span>
          <select class="select" data-field="pr-brk-category" ?disabled=${m.busy}>
            <option value="">Any category</option>
            ${model.categories.map((c) => html`<option>${c.name}</option>`)}
          </select>
        </label>
        <label class="field">
          <span class="field-label">Product id (empty prices by the category)</span>
          <input class="text-field" type="text" data-field="pr-brk-product" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Threshold unit (the bands count in this)</span>
          <input class="text-field" type="text" placeholder="SF" data-field="pr-brk-uom" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Active</span>
          <span class="branch-choice">
            <input type="checkbox" data-field="pr-brk-active" ?disabled=${m.busy} />
            <span>An inactive schedule never stacks</span>
          </span>
        </label>
      </div>
      <div class="tablewrap">
        <table>
          <thead>
            <tr>
              <th scope="col">From</th>
              <th scope="col">To (empty is the open top)</th>
              <th scope="col">What the tier does</th>
              <th scope="col">Value</th>
              <th scope="col">Unit</th>
              <th scope="col"></th>
            </tr>
          </thead>
          <tbody>
            ${e.items.map(
              (item, index) => html`
                <tr data-tier=${index}>
                  <td><input class="text-field" type="text" inputmode="decimal" aria-label=${tierWord(index)} data-field="pr-brk-item-${index}-from" ?disabled=${m.busy} /></td>
                  <td><input class="text-field" type="text" inputmode="decimal" aria-label="Tier top" data-field="pr-brk-item-${index}-to" ?disabled=${m.busy} /></td>
                  <td>
                    <select class="select" aria-label="What the tier does" data-field="pr-brk-item-${index}-type" ?disabled=${m.busy}>
                      <option value="PCT_OFF">Percent off</option>
                      <option value="ABS_OFF">Amount off</option>
                      <option value="SURCHARGE_PCT">Percent surcharge</option>
                      <option value="SURCHARGE_ABS">Amount surcharge</option>
                    </select>
                  </td>
                  <td><input class="text-field" type="text" inputmode="decimal" aria-label="Tier value" data-field="pr-brk-item-${index}-value" ?disabled=${m.busy} /></td>
                  <td><input class="text-field" type="text" aria-label="Tier unit" data-field="pr-brk-item-${index}-uom" ?disabled=${m.busy} /></td>
                  <td>
                    <button
                      type="button"
                      class="btn btn-secondary btn-small"
                      data-action="pr-break-drop-item"
                      data-index=${index}
                      ?disabled=${m.busy}
                      aria-label="Drop this tier"
                    >
                      Drop
                    </button>
                  </td>
                </tr>
              `,
            )}
          </tbody>
        </table>
      </div>
      <div class="row-actions">
        <button type="button" class="btn btn-secondary btn-small" data-action="pr-break-add-item" ?disabled=${m.busy || e.items.length >= 20}>
          Add a tier (20 at most)
        </button>
      </div>
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-break-form-error">${m.formError}</p>`}
      ${m.floor === null ? nothing : renderFloorPanel(m.floor, "pr-break-confirm", "pr-break-review")}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-break-save" ?disabled=${m.busy || m.floor !== null}>
          ${m.busy ? "Checking the floor…" : e.id === "" ? "Check the floor, then create" : "Check the floor, then save"}
        </button>
        <button type="button" class="btn btn-secondary" data-action="pr-break-cancel" ?disabled=${m.busy}>Cancel</button>
      </div>
    </section>
  `;
}
