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
  clearanceValueWords,
  promoValueWords,
  promosEmptyWords,
  accrualResultWords,
  chainLabel,
  isUomConfirmed,
  pickSentence,
  rebateEmptyWords,
  rebateRateWords,
  batchEmptyWords,
  batchLineWords,
  batchStatusWord,
  BATCH_STATUSES,
  derivationWord,
  importResultWords,
  outcomeWord,
  auditActionWord,
  deltaWords,
  lowMarginEmptyWords,
  lowMarginWords,
  priceFileCountsWords,
  priceFileEmptyWords,
  priceFileOriginWords,
  trimNumber,
  uomEmptyWords,
  uomSourceWord,
  uomUnconfirmedWords,
  vendorWords,
  weightSentence,
  categoryName,
  CUSTOMER_TYPES,
  customerTypeWord,
  marginEmptyWords,
  costCellWords,
  ledgerTotalWords,
  WITHHELD_FIGURE_WORDS,
  marginCellWords,
  moneyWords,
  overrideEmptyWords,
  overrideValueWords,
  pctWords,
  pricingEmptyWords,
  priceSourceWord,
  RULE_SCOPES,
  ruleEmptyWords,
  ruleValueWords,
  scopeWord,
  validityWords,
  PRICING_TABS,
} from "./pricing-logic.js";
import { whenWords } from "./audit-logic.js";
import type { BreakRow, ChangeLogRow, ClearanceRow, LowMarginScanRow as LowMarginScanRowApi, OverrideRow, PricingAuditRow, PromotionRow, RebateRow, RuleRow, UomConversionRow } from "./pricing-api.js";
type BatchLineRow = Parameters<typeof batchLineWords>[0];
type PriceFileModel = PricingModel["priceFile"];
type LowMarginScanRow = LowMarginScanRowApi;
type LowMarginEntry = PricingModel["lowMargin"]["probe"]["entries"][number];
import type { FloorPanel, PricingModel } from "./pricing-controller.js";

export function renderPricing(model: PricingModel): TemplateResult {
  const isPaymentTerms = model.tab === "payment-terms" || model.tab === "terms";
  return html`
    <header class="page-head">
      <h1>Pricing</h1>
      <p class="page-sub">
        ${isPaymentTerms
          ? "The margin floors, the rules, the ladders, the imports and the ledgers. This page: when a customer pays, and what is asked up front."
          : "The dealer's price governance: the floors, the rules, and the records of what moved."}
      </p>
    </header>

    <div class="pr-board-pills" role="tablist" aria-label="Pricing views">
      <button
        type="button"
        class="pr-board-pill ${model.tab === "margin" ? "pr-board-pill-active" : ""}"
        data-action="pr-set-tab"
        data-tab="margin"
      >
        Margin floors
      </button>
      <button
        type="button"
        class="pr-board-pill ${model.tab === "rules" ? "pr-board-pill-active" : ""}"
        data-action="pr-set-tab"
        data-tab="rules"
      >
        Rules
      </button>
      <button
        type="button"
        class="pr-board-pill ${model.tab === "breaks" ? "pr-board-pill-active" : ""}"
        data-action="pr-set-tab"
        data-tab="breaks"
      >
        Ladders
      </button>
      <button
        type="button"
        class="pr-board-pill ${model.tab === "batches" ? "pr-board-pill-active" : ""}"
        data-action="pr-set-tab"
        data-tab="batches"
      >
        Imports
      </button>
      <button
        type="button"
        class="pr-board-pill ${isPaymentTerms ? "pr-board-pill-active" : ""}"
        data-action="pr-set-tab"
        data-tab="payment-terms"
      >
        Payment terms and deposits
      </button>
    </div>

    <nav class="pr-tabs ${isPaymentTerms ? "pr-tabs-hidden" : ""}" role="group" aria-label="Pricing tabs">
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
  if (model.tab === "payment-terms" || model.tab === "terms") return renderPaymentTermsAndDeposits();
  if (model.tab === "margin") return renderMargin(model);
  if (model.tab === "rules") return renderRules(model);
  if (model.tab === "customers") return renderCustomers(model);
  if (model.tab === "breaks") return renderBreaks(model);
  if (model.tab === "promos") return renderPromos(model);
  if (model.tab === "rebates") return renderRebates(model);
  if (model.tab === "uom") return renderUom(model);
  if (model.tab === "batches") return renderBatches(model);
  if (model.tab === "pricefile") return renderPriceFile(model);
  if (model.tab === "lowmargin") return renderLowMargin(model);
  if (model.tab === "audit") return renderPricingAudit(model);
  if (model.tab === "changelog") return renderChangeLog(model);
  const tab = PRICING_TABS.find((t) => t.id === model.tab);
  return html`<p class="notice-line">${pricingEmptyWords(tab?.title ?? model.tab)}</p>`;
}

export function renderPaymentTermsAndDeposits(): TemplateResult {
  return html`
    <section class="card" data-test="pricing-cod-card">
      <div class="card-head">
        <h2 class="card-title">Cash On Demand by customer class</h2>
      </div>
      <p class="card-sub">
        Cash On Demand means the customer pays before the goods are released, for any fulfilment: the counter, will-call or delivery. The invoice number is issued while the invoice is unpaid.
      </p>
      <div class="tablewrap">
        <table>
          <thead>
            <tr>
              <th scope="col">CUSTOMER CLASS</th>
              <th scope="col">CASH ON DEMAND</th>
              <th scope="col">CREDIT</th>
              <th scope="col">TURNS ON</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td><strong>Walk-in, no account</strong></td>
              <td><span class="badge-always">ALWAYS</span></td>
              <td>No credit is ever offered</td>
              <td>Pay in full, or a deposit when the order waits</td>
            </tr>
            <tr>
              <td><strong>Homeowner</strong></td>
              <td><span class="badge-always">ALWAYS</span></td>
              <td>Paid by card or cash at the order</td>
              <td>Deposit when the order waits</td>
            </tr>
            <tr>
              <td><strong>Contractor</strong></td>
              <td><span class="badge-not-default">NOT BY DEFAULT</span></td>
              <td>Terms 30 days once approved</td>
              <td>Only on an account that falls past due</td>
            </tr>
            <tr>
              <td><strong>Builder</strong></td>
              <td><span class="badge-not-default">NOT BY DEFAULT</span></td>
              <td>Terms 30 days once approved</td>
              <td>Only on an account that falls past due</td>
            </tr>
            <tr>
              <td><strong>Landscaper</strong></td>
              <td><span class="badge-not-default">NOT BY DEFAULT</span></td>
              <td>Terms 30 days once approved</td>
              <td>Only on an account that falls past due</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section class="card" data-test="pricing-accounts-card">
      <div class="card-head">
        <h2 class="card-title">Accounts set by hand</h2>
        <span class="count dim">3 accounts</span>
      </div>
      <div class="tablewrap">
        <table>
          <thead>
            <tr>
              <th scope="col">ACCOUNT</th>
              <th scope="col">SETTING</th>
              <th scope="col">WHY</th>
              <th scope="col">ENDS</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td><strong>Fieldstone Pools and Patios</strong></td>
              <td><span class="badge-cod">CASH ON DEMAND</span></td>
              <td>Over limit by $2,100.00, order held</td>
              <td>Until the balance is under the limit</td>
            </tr>
            <tr>
              <td><strong>Granite Edge Paving</strong></td>
              <td><span class="badge-cod">CASH ON DEMAND</span></td>
              <td>91 days late, $22,300.00 past due</td>
              <td>Until the account is current</td>
            </tr>
            <tr>
              <td><strong>Bluffside Builders</strong></td>
              <td><span class="badge-cod">CASH ON DEMAND</span></td>
              <td>On hold: promise missed, $14,820.00 past due</td>
              <td>Until the controller releases it</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="card-actions">
        <button type="button" class="btn btn-secondary" data-action="set-account">Set an account</button>
      </div>
    </section>

    <section class="card" data-test="pricing-deposits-card">
      <div class="card-head">
        <h2 class="card-title">Deposits</h2>
      </div>
      <p class="card-sub">
        A deposit is offered only when the order waits: will-call, a scheduled delivery or a special order. A sale rung and released now takes the full amount.
      </p>
      <div class="tablewrap">
        <table>
          <thead>
            <tr>
              <th scope="col">ORDER KIND</th>
              <th scope="col">DEPOSIT %</th>
              <th scope="col">DUE</th>
              <th scope="col">BALANCE DUE</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td><strong>Will-call at the branch</strong></td>
              <td><strong>25%</strong></td>
              <td>At the order</td>
              <td>Before the order is released</td>
            </tr>
            <tr>
              <td><strong>Scheduled delivery</strong></td>
              <td><strong>25%</strong></td>
              <td>At the order</td>
              <td>Before the truck leaves</td>
            </tr>
            <tr>
              <td><strong>Special order</strong></td>
              <td><strong>50%</strong></td>
              <td>At the order</td>
              <td>Before the order is released</td>
            </tr>
            <tr>
              <td><strong>Cash On Demand order that waits</strong></td>
              <td><strong>25%</strong></td>
              <td>At the order</td>
              <td>Before the order is released</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="card-actions">
        <button type="button" class="btn btn-primary" data-action="save-deposits">Save changes</button>
      </div>
    </section>
  `;
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
                        <td class="num">${marginCellWords(item.margin_bps)}</td>
                        <td class="num">${marginCellWords(item.floor_bps)}</td>
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

// -- the promotions and clearance tab -------------------------------------------------------------------

function promoScopeWords(row: PromotionRow, model: PricingModel): string {
  if (row.product_id !== "") return `product ${row.product_id.slice(0, 8)}`;
  if (row.category_id !== "") return categoryName(row.category_id, model.categories);
  if (row.branch_id !== "") return `branch ${row.branch_id.slice(0, 8)}`;
  return "everywhere";
}

export function renderPromos(model: PricingModel): TemplateResult {
  const promos = model.promos.promotions;
  const clearance = model.promos.clearance;
  return html`
    ${promos.phase === "loading" || clearance.phase === "loading"
      ? html`<p class="notice-line" data-test="pr-promos-loading">Loading the promotions and the clearance marks…</p>`
      : nothing}
    ${promos.phase === "error" || promos.phase === "refused"
      ? html`<p class="error-line" data-test="pr-promos-error">${promos.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="pr-promos-reload">Try again</button></div>`
      : nothing}
    ${promos.phase === "ready" ? renderPromotionsPanel(model) : nothing}
    ${promos.phase === "ready" && promos.editing !== null ? renderPromoEditor(model) : nothing}
    ${promos.phase === "ready" && clearance.phase === "ready" ? renderClearancePanel(model) : nothing}
    ${promos.phase === "ready" && clearance.phase === "ready" && clearance.editing !== null ? renderClearanceEditor(model) : nothing}
  `;
}

function renderPromotionsPanel(model: PricingModel): TemplateResult {
  const m = model.promos.promotions;
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Promotions</h2>
        <span class="count" data-test="pr-promo-count">${m.rows.length} ${m.rows.length === 1 ? "promotion" : "promotions"}</span>
        <button type="button" class="btn btn-primary btn-small" data-action="pr-promo-edit">New promotion</button>
      </div>
      ${m.notice === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-promo-notice">${m.notice}</p>`}
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-promo-form-error">${m.formError}</p>`}
      ${m.rows.length === 0
        ? html`<p class="notice-line" data-test="pr-promos-empty">${promosEmptyWords()}</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Promotion</th>
                    <th scope="col">Covers</th>
                    <th scope="col">Window</th>
                    <th scope="col">Value</th>
                    <th scope="col" class="num">Priority</th>
                    <th scope="col">State</th>
                    <th scope="col">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.rows.map((row) => renderPromoRow(row, model))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function renderPromoRow(row: PromotionRow, model: PricingModel): TemplateResult {
  const m = model.promos.promotions;
  const confirming = m.confirmingId === row.id;
  return html`
    <tr data-promo=${row.id}>
      <td>
        <div class="person">${row.name}</div>
        <div class="mono dim">${row.code} · ${row.detail_text}</div>
      </td>
      <td>${promoScopeWords(row, model)}</td>
      <td class="mono dim">${validityWords(row.starts_at, row.ends_at)}</td>
      <td class="num" data-test="pr-promo-value">${promoValueWords(row)}</td>
      <td class="num">${row.priority}</td>
      <td>${row.is_active ? html`<span class="chip chip-good">active</span>` : html`<span class="chip chip-dim">retired</span>`}</td>
      <td>
        <div class="pr-actions">
          <button
            type="button"
            class="btn btn-secondary btn-small"
            data-action="pr-promo-edit"
            data-id=${row.id}
            aria-label=${`Edit the promotion ${row.name}`}
          >
            Edit
          </button>
          ${row.is_active
            ? html`
                <button type="button" class="btn btn-secondary btn-small" data-action="pr-promo-del" data-id=${row.id} ?disabled=${m.busy}>
                  ${confirming ? "Retire: tap again" : "Retire"}
                </button>
              `
            : nothing}
        </div>
      </td>
    </tr>
  `;
}

function renderPromoEditor(model: PricingModel): TemplateResult | typeof nothing {
  const m = model.promos.promotions;
  const e = m.editing;
  if (e === null) return nothing;
  const heading = e.id === "" ? "New promotion" : `Promotion: ${e.nameInput === "" ? e.code : e.nameInput}`;
  return html`
    <section class="card" data-test="pr-promo-editor">
      <div class="card-head">
        <h2 class="card-title">${heading}</h2>
        ${e.id === "" ? html`<span class="chip chip-wait">create</span>` : html`<span class="chip">version ${e.version}</span>`}
      </div>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Code</span>
          <input class="text-field" type="text" data-field="pr-prm-code" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Name</span>
          <input class="text-field" type="text" data-field="pr-prm-name" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Category</span>
          <select class="select" data-field="pr-prm-category" ?disabled=${m.busy}>
            <option value="">Any category</option>
            ${model.categories.map((c) => html`<option>${c.name}</option>`)}
          </select>
        </label>
        <label class="field">
          <span class="field-label">Product id (empty unless the promo names one)</span>
          <input class="text-field" type="text" data-field="pr-prm-product" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Branch id (empty runs everywhere)</span>
          <input class="text-field" type="text" data-field="pr-prm-branch" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Starts</span>
          <input class="text-field" type="date" data-field="pr-prm-starts" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Ends (the promo's last day)</span>
          <input class="text-field" type="date" data-field="pr-prm-ends" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">What the promo does</span>
          <select class="select" data-field="pr-prm-calc" ?disabled=${m.busy}>
            <option value="">Choose a calc</option>
            <option value="PCT_OFF">Percent off</option>
            <option value="AMT_OFF">Amount off</option>
            <option value="FIXED_PRICE">Fixed price</option>
          </select>
        </label>
        ${e.calc !== ""
          ? html`<label class="field"><span class="field-label">Value (percent, or dollars)</span>
              <input class="text-field" type="text" inputmode="decimal" data-field="pr-prm-value" ?disabled=${m.busy} /></label>`
          : nothing}
        ${e.calc === "FIXED_PRICE"
          ? html`<label class="field"><span class="field-label">Price unit</span>
              <input class="text-field" type="text" placeholder="SF" data-field="pr-prm-uom" ?disabled=${m.busy} /></label>`
          : nothing}
        <label class="field">
          <span class="field-label">Priority (lower runs first)</span>
          <input class="text-field" type="text" inputmode="numeric" data-field="pr-prm-priority" ?disabled=${m.busy} />
        </label>
        <label class="field field-full">
          <span class="field-label">What it says, as the list shows it</span>
          <input class="text-field" type="text" data-field="pr-prm-detail" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Active</span>
          <span class="branch-choice">
            <input type="checkbox" data-field="pr-prm-active" ?disabled=${m.busy} />
            <span>An inactive promo never resolves</span>
          </span>
        </label>
      </div>
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-promo-error">${m.formError}</p>`}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-promo-save" ?disabled=${m.busy}>
          ${m.busy ? "Saving…" : e.id === "" ? "Create" : "Save"}
        </button>
        <button type="button" class="btn btn-secondary" data-action="pr-promo-cancel" ?disabled=${m.busy}>Cancel</button>
      </div>
    </section>
  `;
}

function renderClearancePanel(model: PricingModel): TemplateResult {
  const m = model.promos.clearance;
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Clearance</h2>
        <span class="count" data-test="pr-clear-count">${m.rows.length} ${m.rows.length === 1 ? "item" : "items"}</span>
        <button type="button" class="btn btn-primary btn-small" data-action="pr-clear-edit">Mark an item</button>
      </div>
      ${m.notice === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-clear-notice">${m.notice}</p>`}
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-clear-form-error">${m.formError}</p>`}
      ${m.rows.length === 0
        ? html`<p class="notice-line" data-test="pr-clear-empty">No clearance marks. An item prices through the ordinary waterfall until it is marked.</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Product</th>
                    <th scope="col">Why</th>
                    <th scope="col">Price</th>
                    <th scope="col">Marked</th>
                    <th scope="col">State</th>
                    <th scope="col">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.rows.map((row) => renderClearanceRowView(row, model))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function renderClearanceRowView(row: ClearanceRow, model: PricingModel): TemplateResult {
  void model;
  const m = model.promos.clearance;
  const confirming = m.confirmingId === row.product_id;
  return html`
    <tr data-clearance=${row.product_id}>
      <td>
        <div class="person">${row.name === "" ? "Unknown product" : row.name}</div>
        <div class="mono dim">${row.sku === "" ? row.product_id.slice(0, 8) : row.sku}</div>
      </td>
      <td>${row.reason}</td>
      <td class="num" data-test="pr-clear-value">${clearanceValueWords(row)}</td>
      <td class="mono dim">${validityWords(row.started_at, row.ends_at)}</td>
      <td>${row.is_active ? html`<span class="chip chip-good">marked</span>` : html`<span class="chip chip-dim">ended</span>`}</td>
      <td>
        <div class="pr-actions">
          <button
            type="button"
            class="btn btn-secondary btn-small"
            data-action="pr-clear-edit"
            data-product=${row.product_id}
            aria-label=${`Edit the mark on ${row.sku === "" ? row.product_id.slice(0, 8) : row.sku}`}
          >
            Edit
          </button>
          ${row.is_active
            ? html`
                <button type="button" class="btn btn-secondary btn-small" data-action="pr-clear-del" data-product=${row.product_id} ?disabled=${m.busy}>
                  ${confirming ? "Take off: tap again" : "Take off"}
                </button>
              `
            : nothing}
        </div>
      </td>
    </tr>
  `;
}

function renderClearanceEditor(model: PricingModel): TemplateResult | typeof nothing {
  const m = model.promos.clearance;
  const e = m.editing;
  if (e === null) return nothing;
  const heading = e.productId === "" ? "Mark an item" : "Rewrite the mark";
  return html`
    <section class="card" data-test="pr-clear-editor">
      <div class="card-head">
        <h2 class="card-title">${heading}</h2>
      </div>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Product id</span>
          <input class="text-field" type="text" data-field="pr-clr-product" ?disabled=${m.busy} />
        </label>
        <label class="field field-full">
          <span class="field-label">Why it is marked (the reason is what the row shows)</span>
          <input class="text-field" type="text" data-field="pr-clr-reason" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Clearance price, dollars (empty is the badge only mark)</span>
          <input class="text-field" type="text" inputmode="decimal" data-field="pr-clr-price" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Price unit (a price needs one)</span>
          <input class="text-field" type="text" placeholder="EA" data-field="pr-clr-uom" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Marked from</span>
          <input class="text-field" type="date" data-field="pr-clr-starts" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Ends (empty is open)</span>
          <input class="text-field" type="date" data-field="pr-clr-ends" ?disabled=${m.busy} />
        </label>
      </div>
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-clear-error">${m.formError}</p>`}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-clear-save" ?disabled=${m.busy}>
          ${m.busy ? "Saving…" : "Save the mark"}
        </button>
        <button type="button" class="btn btn-secondary" data-action="pr-clear-cancel" ?disabled=${m.busy}>Cancel</button>
      </div>
    </section>
  `;
}

// -- the rebates tab ---------------------------------------------------------------------------------------

function rebateCoversWords(row: RebateRow, model: PricingModel): string {
  if (row.product_id !== "") return "one product";
  if (row.category_id !== "") return categoryName(row.category_id, model.categories);
  return "all this vendor supplies";
}

function rebateVendorOptions(model: PricingModel): ReadonlyMap<string, string> {
  const seen = new Map<string, string>();
  for (const row of model.rebates.rows) {
    if (row.vendor_id !== "" && !seen.has(row.vendor_id)) seen.set(row.vendor_id, vendorWords(row.vendor_name, row.vendor_id));
  }
  return seen;
}

export function renderRebates(model: PricingModel): TemplateResult {
  const m = model.rebates;
  return html`
    ${m.phase === "loading" ? html`<p class="notice-line" data-test="pr-rebates-loading">Loading the rebate programs…</p>` : nothing}
    ${m.phase === "error" || m.phase === "refused"
      ? html`<p class="error-line" data-test="pr-rebates-error">${m.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="pr-rebates-reload">Try again</button></div>`
      : nothing}
    ${m.phase === "ready" ? renderRebatePrograms(model) : nothing}
    ${m.phase === "ready" && m.editing !== null ? renderRebateEditor(model) : nothing}
    ${m.phase === "ready" ? renderLedger(model) : nothing}
    ${m.phase === "ready" ? renderAccrual(model) : nothing}
  `;
}

function renderRebatePrograms(model: PricingModel): TemplateResult {
  const m = model.rebates;
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Rebate programs</h2>
        <span class="count" data-test="pr-rebate-count">${m.rows.length} ${m.rows.length === 1 ? "program" : "programs"}</span>
        <button type="button" class="btn btn-primary btn-small" data-action="pr-rebate-edit">New program</button>
      </div>
      ${m.notice === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-rebate-notice">${m.notice}</p>`}
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-rebate-form-error">${m.formError}</p>`}
      ${m.rows.length === 0
        ? html`<p class="notice-line" data-test="pr-rebates-empty">${rebateEmptyWords()}</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Program</th>
                    <th scope="col">Vendor</th>
                    <th scope="col">Covers</th>
                    <th scope="col">Rate</th>
                    <th scope="col">From</th>
                    <th scope="col">State</th>
                    <th scope="col">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.rows.map((row) => renderRebateRowView(row, model))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function renderRebateRowView(row: RebateRow, model: PricingModel): TemplateResult {
  const m = model.rebates;
  const confirming = m.confirmingId === row.id;
  return html`
    <tr data-rebate=${row.id}>
      <td>
        <div class="person">${row.name}</div>
        <div class="mono dim">${row.code} · ${row.notes}</div>
      </td>
      <td>${vendorWords(row.vendor_name, row.vendor_id)}</td>
      <td>${rebateCoversWords(row, model)}</td>
      <td class="num" data-test="pr-rebate-rate">${rebateRateWords(row)}</td>
      <td class="mono dim">${row.starts_at.slice(0, 10)}</td>
      <td>${row.is_active ? html`<span class="chip chip-good">active</span>` : html`<span class="chip chip-dim">retired</span>`}</td>
      <td>
        <div class="pr-actions">
          <button
            type="button"
            class="btn btn-secondary btn-small"
            data-action="pr-rebate-edit"
            data-id=${row.id}
            aria-label=${`Edit the program ${row.name}`}
          >
            Edit
          </button>
          ${row.is_active
            ? html`
                <button type="button" class="btn btn-secondary btn-small" data-action="pr-rebate-del" data-id=${row.id} ?disabled=${m.busy}>
                  ${confirming ? "Retire: tap again" : "Retire"}
                </button>
              `
            : nothing}
        </div>
      </td>
    </tr>
  `;
}

function renderRebateEditor(model: PricingModel): TemplateResult | typeof nothing {
  const m = model.rebates;
  const e = m.editing;
  if (e === null) return nothing;
  const heading = e.id === "" ? "New program" : `Program: ${e.nameInput === "" ? e.code : e.nameInput}`;
  return html`
    <section class="card" data-test="pr-rebate-editor">
      <div class="card-head">
        <h2 class="card-title">${heading}</h2>
        ${e.id === "" ? html`<span class="chip chip-wait">create</span>` : html`<span class="chip">version ${e.version}</span>`}
      </div>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Code</span>
          <input class="text-field" type="text" data-field="pr-reb-code" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Name</span>
          <input class="text-field" type="text" data-field="pr-reb-name" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Vendor id (immutable after create)</span>
          <input class="text-field" type="text" data-field="pr-reb-vendor" ?disabled=${m.busy || e.id !== ""} />
        </label>
        <label class="field">
          <span class="field-label">Category</span>
          <select class="select" data-field="pr-reb-category" ?disabled=${m.busy}>
            <option value="">All this vendor supplies</option>
            ${model.categories.map((c) => html`<option>${c.name}</option>`)}
          </select>
        </label>
        <label class="field">
          <span class="field-label">Product id (empty unless the program names one)</span>
          <input class="text-field" type="text" data-field="pr-reb-product" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Basis</span>
          <select class="select" data-field="pr-reb-basis" ?disabled=${m.busy}>
            <option value="">Choose a basis</option>
            <option value="PCT_OF_SALE">Percent of sale</option>
            <option value="PCT_OF_COST">Percent of cost</option>
            <option value="AMT_PER_UNIT">Amount per base unit</option>
          </select>
        </label>
        <label class="field">
          <span class="field-label">Rate (percent, or dollars per base unit)</span>
          <input class="text-field" type="text" inputmode="decimal" data-field="pr-reb-rate" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">From</span>
          <input class="text-field" type="date" data-field="pr-reb-starts" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Ends (empty is open)</span>
          <input class="text-field" type="date" data-field="pr-reb-ends" ?disabled=${m.busy} />
        </label>
        <label class="field field-full">
          <span class="field-label">Notes</span>
          <input class="text-field" type="text" data-field="pr-reb-notes" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Active</span>
          <span class="branch-choice">
            <input type="checkbox" data-field="pr-reb-active" ?disabled=${m.busy} />
            <span>A retired program stops accruing</span>
          </span>
        </label>
      </div>
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-rebate-error">${m.formError}</p>`}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-rebate-save" ?disabled=${m.busy}>
          ${m.busy ? "Saving…" : e.id === "" ? "Create" : "Save"}
        </button>
        <button type="button" class="btn btn-secondary" data-action="pr-rebate-cancel" ?disabled=${m.busy}>Cancel</button>
      </div>
    </section>
  `;
}

function renderLedger(model: PricingModel): TemplateResult {
  const m = model.rebates.ledger;
  const vendors = rebateVendorOptions(model);
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Accrual ledger</h2>
        ${m.phase === "ready"
          ? html`<span class="count" data-test="pr-ledger-total">${ledgerTotalWords(m.totalCents, m.rows)}</span>`
          : nothing}
      </div>
      <div class="filters" role="group" aria-label="The ledger's vendor">
        <select class="select" aria-label="Vendor" data-field="pr-ledger-name">
          <option value="">Choose a vendor</option>
          ${Array.from(vendors.values()).map((name) => html`<option>${name}</option>`)}
        </select>
        <button type="button" class="btn btn-secondary" data-action="pr-ledger-vendor" ?disabled=${m.phase === "loading"}>Load ledger</button>
      </div>
      ${m.phase === "loading" ? html`<p class="notice-line">Loading the ledger…</p>` : nothing}
      ${m.phase === "error" ? html`<p class="error-line" data-test="pr-ledger-error">${m.error}</p>` : nothing}
      ${m.phase === "ready" && m.rows.length === 0
        ? html`<p class="notice-line" data-test="pr-ledger-empty">No accruals for this vendor yet. Claims appear here as covered products sell.</p>`
        : nothing}
      ${m.phase === "ready" && m.rows.length > 0
        ? html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Accrued</th>
                    <th scope="col">Program</th>
                    <th scope="col">SKU</th>
                    <th scope="col">Source</th>
                    <th scope="col" class="num">Basis</th>
                    <th scope="col">Rate note</th>
                    <th scope="col" class="num">Claim</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.rows.map(
                    (row) => html`
                      <tr data-accrual=${row.accrual_cents ?? ""}>
                        <td class="mono dim">${row.accrued_at.slice(0, 10)}</td>
                        <td class="mono">${row.program_code === "" ? "-" : row.program_code}</td>
                        <td class="mono">${row.product_sku === "" ? row.product_sku : row.product_sku}</td>
                        <td><span class="chip">${row.source_type}</span></td>
                        <td class="num">${costCellWords(row.basis_cents)}</td>
                        <td class="mono dim">${row.rate_note === null ? WITHHELD_FIGURE_WORDS : row.rate_note}</td>
                        <td class="num">${costCellWords(row.accrual_cents)}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `
        : nothing}
      <p class="pr-hint">The ledger is the claim side: what the programs have accrued, not what the vendor has paid.</p>
    </section>
  `;
}

function renderAccrual(model: PricingModel): TemplateResult {
  const m = model.rebates.accrual;
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Accrue one sale event</h2>
      </div>
      <p class="pr-hint">
        The caller supplies the facts; the engine reads no sales table. The source type and id are the idempotency key:
        a retry answers skipped, not an error.
      </p>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Source type</span>
          <select class="select" data-field="pr-acc-source-type" ?disabled=${m.busy}>
            <option value="POS_SALE">Counter sale</option>
            <option value="ORDER">Order</option>
            <option value="ADJUSTMENT">Adjustment</option>
          </select>
        </label>
        <label class="field">
          <span class="field-label">Source id (empty only on an adjustment)</span>
          <input class="text-field" type="text" data-field="pr-acc-source-id" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">When it happened (empty is the server clock)</span>
          <input class="text-field" type="date" data-field="pr-acc-occurred" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Product id</span>
          <input class="text-field" type="text" data-field="pr-acc-product" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Category id</span>
          <input class="text-field" type="text" data-field="pr-acc-category" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Quantity, base unit</span>
          <input class="text-field" type="text" inputmode="decimal" data-field="pr-acc-qty" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Extended sale, dollars, pre-tax</span>
          <input class="text-field" type="text" inputmode="decimal" data-field="pr-acc-sale" ?disabled=${m.busy} />
        </label>
        <label class="field">
          <span class="field-label">Extended cost, dollars</span>
          <input class="text-field" type="text" inputmode="decimal" data-field="pr-acc-cost" ?disabled=${m.busy} />
        </label>
      </div>
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-accrue-error">${m.formError}</p>`}
      ${m.result === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-accrue-result">${m.result}</p>`}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-accrue" ?disabled=${m.busy}>
          ${m.busy ? "Accruing…" : "Offer to the accrual engine"}
        </button>
      </div>
    </section>
  `;
}

// -- the units of measure tab -----------------------------------------------------------------------------

function uomProductWords(row: UomConversionRow): string {
  if (row.product_sku !== "") return row.product_sku;
  if (row.product_id !== "") return row.product_id.slice(0, 8);
  if (row.category_id !== "") return `category ${row.category_id.slice(0, 8)}`;
  return "all products";
}

export function renderUom(model: PricingModel): TemplateResult {
  const m = model.uom;
  const unconfirmed = m.rows.filter((r) => !isUomConfirmed(r.source)).length;
  return html`
    ${m.phase === "loading" ? html`<p class="notice-line" data-test="pr-uom-loading">Loading the conversion edges…</p>` : nothing}
    ${m.phase === "error" || m.phase === "refused"
      ? html`<p class="error-line" data-test="pr-uom-error">${m.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="pr-uom-reload">Try again</button></div>`
      : nothing}
    ${m.phase === "ready"
      ? html`
          ${unconfirmed > 0
            ? html`<p class="error-line" data-test="pr-uom-warn">${uomUnconfirmedWords(unconfirmed, m.rows.length)}</p>`
            : nothing}
          ${renderUomProbe(model)}
          ${renderUomTable(model)}
        `
      : nothing}
    ${m.phase === "ready" && m.editing !== null ? renderUomEditor(model) : nothing}
  `;
}

function renderUomProbe(model: PricingModel): TemplateResult {
  const m = model.uom.probe;
  const plan = m.result;
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Line-entry preview</h2>
        <span class="count">what the yard actually picks</span>
      </div>
      <p class="pr-hint">
        A grid of factors is not reviewable. Typing a quantity and reading what it becomes, in the units the person
        knows, is the review.
      </p>
      <div class="form-grid">
        <label class="field"><span class="field-label">Product id</span>
          <input class="text-field" type="text" data-field="pr-uom-probe-product" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Category id (empty is fine)</span>
          <input class="text-field" type="text" data-field="pr-uom-probe-category" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Quantity</span>
          <input class="text-field" type="text" inputmode="decimal" data-field="pr-uom-probe-qty" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Unit entered</span>
          <input class="text-field" type="text" placeholder="SF" data-field="pr-uom-probe-uom" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Pick unit (empty follows the edge)</span>
          <input class="text-field" type="text" data-field="pr-uom-probe-pick" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Weight in (empty skips the weight leg)</span>
          <input class="text-field" type="text" placeholder="T" data-field="pr-uom-probe-weight" ?disabled=${m.busy} /></label>
      </div>
      ${m.error === "" ? nothing : html`<p class="error-line" data-test="pr-uom-plan-error">${m.error}</p>`}
      ${plan === null
        ? nothing
        : html`
            <div class="pr-readout" data-test="pr-uom-plan">
              <div class="${plan.rounded_up ? "warn" : ""}">${pickSentence(plan)}</div>
              ${weightSentence(plan) === "" ? nothing : html`<div>${weightSentence(plan)}</div>`}
              ${plan.pick_steps.length === 0 ? nothing : html`<div>Via ${chainLabel(plan.pick_steps)}</div>`}
              ${plan.unconfirmed ? html`<div><span class="chip chip-danger">rests on unconfirmed factors</span></div>` : nothing}
            </div>
          `}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-uom-plan" ?disabled=${m.busy}>
          ${m.busy ? "Planning…" : "Plan"}
        </button>
      </div>
    </section>
  `;
}

function renderUomTable(model: PricingModel): TemplateResult {
  const m = model.uom;
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Conversion data</h2>
        <span class="count" data-test="pr-uom-count">${m.rows.length} ${m.rows.length === 1 ? "edge" : "edges"}</span>
        <button type="button" class="btn btn-primary btn-small" data-action="pr-uom-edit">New edge</button>
      </div>
      ${m.notice === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-uom-notice">${m.notice}</p>`}
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-uom-form-error">${m.formError}</p>`}
      ${m.rows.length === 0
        ? html`<p class="notice-line" data-test="pr-uom-empty">${uomEmptyWords()}</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Product</th>
                    <th scope="col">Edge</th>
                    <th scope="col" class="num">Factor</th>
                    <th scope="col">Pick</th>
                    <th scope="col">Source</th>
                    <th scope="col">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.rows.map((row) => renderUomRow(row, model))}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function renderUomRow(row: UomConversionRow, model: PricingModel): TemplateResult {
  const m = model.uom;
  const confirming = m.confirmingId === row.id;
  const confirmed = isUomConfirmed(row.source);
  return html`
    <tr data-conversion=${row.id}>
      <td>${uomProductWords(row)}</td>
      <td class="mono">${row.from_uom} → ${row.to_uom}</td>
      <td class="num">${trimNumber(row.factor, 6)}</td>
      <td>${row.round_up_at_pick ? html`<span class="chip">rounds up at pick</span>` : nothing}</td>
      <td>
        <span class="chip ${confirmed ? "chip-good" : "chip-danger"}">${confirmed ? uomSourceWord(row.source) : "unconfirmed"}</span>
        ${row.source_note === "" ? nothing : html`<div class="dim">${row.source_note}</div>`}
      </td>
      <td>
        <div class="pr-actions">
          <button
            type="button"
            class="btn btn-secondary btn-small"
            data-action="pr-uom-edit"
            data-id=${row.id}
            aria-label=${`Edit the edge ${row.from_uom} to ${row.to_uom}`}
          >
            Edit
          </button>
          <button type="button" class="btn btn-secondary btn-small" data-action="pr-uom-del" data-id=${row.id} ?disabled=${m.busy}>
            ${confirming ? "Delete: tap again" : "Delete"}
          </button>
        </div>
      </td>
    </tr>
  `;
}

function renderUomEditor(model: PricingModel): TemplateResult | typeof nothing {
  const m = model.uom;
  const e = m.editing;
  if (e === null) return nothing;
  return html`
    <section class="card" data-test="pr-uom-editor">
      <div class="card-head">
        <h2 class="card-title">Conversion edge</h2>
      </div>
      <div class="form-grid">
        <label class="field"><span class="field-label">Product id (empty is every product)</span>
          <input class="text-field" type="text" data-field="pr-uom-product" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Category id (when the edge is category wide)</span>
          <input class="text-field" type="text" data-field="pr-uom-category-id" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">From unit</span>
          <input class="text-field" type="text" placeholder="SF" data-field="pr-uom-from" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">To unit</span>
          <input class="text-field" type="text" placeholder="PLT" data-field="pr-uom-to" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Factor (target units per entered unit)</span>
          <input class="text-field" type="text" inputmode="decimal" data-field="pr-uom-factor" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Provenance</span>
          <select class="select" data-field="pr-uom-source" ?disabled=${m.busy}>
            <option value="CLIENT_CONFIRMED">Client confirmed</option>
            <option value="VENDOR_SHEET">Vendor sheet</option>
            <option value="MEASURED">Measured</option>
            <option value="SEEDED_DEFAULT">Seeded default</option>
          </select>
        </label>
        <label class="field field-full"><span class="field-label">Where the factor came from, as the row's hint</span>
          <input class="text-field" type="text" data-field="pr-uom-note" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Pick</span>
          <span class="branch-choice">
            <input type="checkbox" data-field="pr-uom-round" ?disabled=${m.busy} />
            <span>The target unit is indivisible: round up at pick</span>
          </span>
        </label>
      </div>
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-uom-error-line">${m.formError}</p>`}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-uom-save" ?disabled=${m.busy}>
          ${m.busy ? "Saving…" : "Save the edge"}
        </button>
        <button type="button" class="btn btn-secondary" data-action="pr-uom-cancel" ?disabled=${m.busy}>Cancel</button>
      </div>
    </section>
  `;
}

// -- the batches and imports tab -------------------------------------------------------------------------

export function renderBatches(model: PricingModel): TemplateResult {
  const m = model.batches;
  return html`
    ${m.phase === "loading" ? html`<p class="notice-line" data-test="pr-batches-loading">Loading the price batches…</p>` : nothing}
    ${m.phase === "error" || m.phase === "refused"
      ? html`<p class="error-line" data-test="pr-batches-error">${m.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="pr-batches-reload">Try again</button></div>`
      : nothing}
    ${m.phase === "ready"
      ? html`
          <section class="card">
            <div class="card-head">
              <h2 class="card-title">Price batches</h2>
              <span class="count" data-test="pr-batches-count">${m.rows.length} ${m.rows.length === 1 ? "batch" : "batches"}</span>
              <button type="button" class="btn btn-secondary btn-small" data-action="pr-batches-apply-due" ?disabled=${m.busy}>Apply due</button>
              <button type="button" class="btn btn-primary btn-small" data-action="pr-batch-new">New batch</button>
            </div>
            ${m.notice === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-batches-notice">${m.notice}</p>`}
            ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-batches-form-error">${m.formError}</p>`}
            <div class="filters" role="group" aria-label="Batch status filter">
              <select class="select" aria-label="Status" data-field="pr-batches-status">
                <option value="">Status: all</option>
                <option value="DRAFT">${batchStatusWord("DRAFT")}</option>
                <option value="STAGED">${batchStatusWord("STAGED")}</option>
                <option value="APPROVED">${batchStatusWord("APPROVED")}</option>
                <option value="APPLIED">${batchStatusWord("APPLIED")}</option>
                <option value="CANCELLED">${batchStatusWord("CANCELLED")}</option>
              </select>
              <button type="button" class="btn btn-secondary" data-action="pr-batches-filter">Apply</button>
            </div>
            ${m.rows.length === 0
              ? html`<p class="notice-line" data-test="pr-batches-empty">${batchEmptyWords()}</p>`
              : html`
                  <div class="tablewrap">
                    <table>
                      <thead>
                        <tr>
                          <th scope="col">Batch</th>
                          <th scope="col">Vendor</th>
                          <th scope="col">Status</th>
                          <th scope="col">Effective</th>
                          <th scope="col" class="num">Lines</th>
                          <th scope="col" class="num">Outliers</th>
                          <th scope="col">Actions</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${m.rows.map((row) => renderBatchRowView(row, model))}
                      </tbody>
                    </table>
                  </div>
                `}
          </section>
          ${m.creating !== null ? renderBatchCreate(model) : nothing}
          ${m.open !== null ? renderOpenBatch(model) : nothing}
          ${renderProfiles(model)}
        `
      : nothing}
  `;
}

function renderBatchRowView(row: Parameters<typeof batchStatusWord>[0] extends string ? { readonly id: string; readonly batch_no: string; readonly vendor_name: string; readonly status: string; readonly effective_date: string; readonly line_count: number; readonly outlier_count: number } : never, model: PricingModel): TemplateResult {
  void model;
  const m = model.batches;
  const open = m.open?.id === row.id;
  return html`
    <tr data-batch=${row.id}>
      <td class="mono">${row.batch_no}</td>
      <td>${row.vendor_name === "" ? html`<span class="dim">unnamed</span>` : row.vendor_name}</td>
      <td><span class="chip">${batchStatusWord(row.status)}</span></td>
      <td class="mono dim">${row.effective_date === "" ? "no date" : row.effective_date.slice(0, 10)}</td>
      <td class="num">${row.line_count}</td>
      <td class="num ${row.outlier_count > 0 ? "" : "dim"}">${row.outlier_count}</td>
      <td>
        <div class="pr-actions">
          <button
            type="button"
            class="btn btn-secondary btn-small"
            data-action="pr-batch-open"
            data-id=${row.id}
            aria-current=${open ? "true" : "false"}
          >
            ${open ? "Open" : "Lines"}
          </button>
        </div>
      </td>
    </tr>
  `;
}

function renderBatchCreate(model: PricingModel): TemplateResult | typeof nothing {
  const m = model.batches;
  const e = m.creating;
  if (e === null) return nothing;
  return html`
    <section class="card" data-test="pr-batch-create">
      <div class="card-head"><h2 class="card-title">New batch</h2></div>
      <div class="form-grid">
        <label class="field"><span class="field-label">Batch number</span>
          <input class="text-field" type="text" data-field="pr-batch-no" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Vendor id</span>
          <input class="text-field" type="text" data-field="pr-batch-vendor" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Import profile id (the mapping it reads with)</span>
          <input class="text-field" type="text" data-field="pr-batch-profile" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Effective date (empty applies by hand)</span>
          <input class="text-field" type="date" data-field="pr-batch-effective" ?disabled=${m.busy} /></label>
        <label class="field field-full"><span class="field-label">Source reference (the file it came from)</span>
          <input class="text-field" type="text" data-field="pr-batch-source" ?disabled=${m.busy} /></label>
      </div>
      ${m.formError === "" ? nothing : html`<p class="error-line" data-test="pr-batch-create-error">${m.formError}</p>`}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-batch-create" ?disabled=${m.busy}>${m.busy ? "Creating…" : "Create draft"}</button>
        <button type="button" class="btn btn-secondary" data-action="pr-batch-cancel" ?disabled=${m.busy}>Cancel</button>
      </div>
    </section>
  `;
}

function renderOpenBatch(model: PricingModel): TemplateResult | typeof nothing {
  const m = model.batches.open;
  if (m === null) return nothing;
  const row = model.batches.openRow;
  const status = row?.status ?? "";
  return html`
    <section class="card" data-test="pr-batch-open">
      <div class="card-head">
        <h2 class="card-title">Batch ${row?.batch_no ?? m.id.slice(0, 8)}</h2>
        ${row?.effective_date ? html`<span class="chip">effective ${row.effective_date.slice(0, 10)}</span>` : nothing}
        <span class="chip">${batchStatusWord(status)}</span>
        <button type="button" class="btn btn-secondary btn-small" data-action="pr-batch-close">Close</button>
      </div>
      ${m.actionNotice === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-batch-notice">${m.actionNotice}</p>`}
      ${m.actionError === "" ? nothing : html`<p class="error-line" data-test="pr-batch-action-error">${m.actionError}</p>`}
      <div class="pr-actions">
        ${status === "STAGED"
          ? html`<button type="button" class="btn btn-primary" data-action="pr-batch-approve" data-id=${m.id} ?disabled=${m.actionBusy}>Approve</button>`
          : nothing}
        ${status === "APPROVED"
          ? html`<button type="button" class="btn btn-primary" data-action="pr-batch-apply" data-id=${m.id} ?disabled=${m.actionBusy}>Apply now</button>`
          : nothing}
      </div>
      ${status === "DRAFT" || status === "STAGED"
        ? html`
            <div class="card-head"><h2 class="card-title">Import the manufacturer's file</h2></div>
            <label class="field">
              <span class="field-label">The CSV, whole (the import reads it with the batch's profile)</span>
              <textarea class="text-field" rows="5" data-field="pr-batch-csv" ?disabled=${m.importBusy}></textarea>
            </label>
            ${m.importError === "" ? nothing : html`<p class="error-line" data-test="pr-batch-import-error">${m.importError}</p>`}
            ${m.importResult === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-batch-import-result">${m.importResult}</p>`}
            <div class="pr-actions">
              <button type="button" class="btn btn-primary" data-action="pr-batch-import" ?disabled=${m.importBusy}>
                ${m.importBusy ? "Importing…" : "Import into this batch"}
              </button>
            </div>
          `
        : nothing}
      <div class="card-head"><h2 class="card-title">Change lines</h2></div>
      ${m.linesPhase === "loading" ? html`<p class="notice-line">Loading the lines…</p>` : nothing}
      ${m.linesPhase === "error" ? html`<p class="error-line">${m.linesError}</p>` : nothing}
      ${m.linesPhase === "ready" && m.lines.length === 0 ? html`<p class="notice-line" data-test="pr-batch-lines-empty">No lines yet. Import the file to fill the batch.</p>` : nothing}
      ${m.linesPhase === "ready" && m.lines.length > 0
        ? html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">SKU</th>
                    <th scope="col">Retail</th>
                    <th scope="col">Set automatically</th>
                    <th scope="col">Flag</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.lines.map(
                    (line) => html`
                      <tr data-line=${line.id}>
                        <td class="mono">${line.sku}</td>
                        <td>${batchLineWords(line)}</td>
                        <td class="dim">${derivationWord(line.derived_from)}</td>
                        <td>${line.unmatched ? html`<span class="chip chip-danger">unmatched</span>` : line.is_outlier ? html`<span class="chip chip-danger">outlier</span>` : html`<span class="chip chip-dim">ok</span>`}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `
        : nothing}
    </section>
  `;
}

function renderProfiles(model: PricingModel): TemplateResult {
  const m = model.batches;
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Import profiles</h2>
        <span class="count" data-test="pr-profiles-count">${m.profiles.length} ${m.profiles.length === 1 ? "profile" : "profiles"}</span>
      </div>
      ${m.profilesPhase === "error" ? html`<p class="error-line" data-test="pr-profiles-error">${m.profilesError}</p>` : nothing}
      ${m.profiles.length === 0
        ? html`<p class="notice-line" data-test="pr-profiles-empty">No import profiles yet. The first import against a vendor's file mints one.</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Profile</th>
                    <th scope="col">Vendor</th>
                    <th scope="col">Mapping</th>
                    <th scope="col" class="num">Markup</th>
                    <th scope="col">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.profiles.map(
                    (p) => html`
                      <tr data-profile=${p.id}>
                        <td class="person">${p.name}</td>
                        <td>${p.vendor_name === "" ? "unnamed" : p.vendor_name}</td>
                        <td class="mono dim">${Object.entries(p.mapping).map(([k, v]) => `${k}: ${v}`).join(", ")}</td>
                        <td class="num">${p.markup_bps}</td>
                        <td>
                          <button
                            type="button"
                            class="btn btn-secondary btn-small"
                            data-action="pr-profile-edit"
                            data-id=${p.id}
                            aria-label=${`Edit the mapping for ${p.name}`}
                          >
                            Edit mapping
                          </button>
                        </td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
      ${m.profileEdit !== null ? renderProfileEdit(model) : nothing}
    </section>
  `;
}

function renderProfileEdit(model: PricingModel): TemplateResult | typeof nothing {
  const e = model.batches.profileEdit;
  if (e === null) return nothing;
  return html`
    <div class="card" data-test="pr-profile-editor">
      <div class="card-head"><h2 class="card-title">Mapping: ${e.name}</h2></div>
      <div class="form-grid">
        <label class="field"><span class="field-label">SKU column header</span>
          <input class="text-field" type="text" data-field="pr-profile-sku" ?disabled=${e.busy} /></label>
        <label class="field"><span class="field-label">Cost column header</span>
          <input class="text-field" type="text" data-field="pr-profile-cost" ?disabled=${e.busy} /></label>
        <label class="field"><span class="field-label">Retail column header</span>
          <input class="text-field" type="text" data-field="pr-profile-retail" ?disabled=${e.busy} /></label>
        <label class="field"><span class="field-label">Trade column header (optional)</span>
          <input class="text-field" type="text" data-field="pr-profile-trade" ?disabled=${e.busy} /></label>
        <label class="field"><span class="field-label">Effective date column header (optional)</span>
          <input class="text-field" type="text" data-field="pr-profile-effective" ?disabled=${e.busy} /></label>
      </div>
      ${e.error === "" ? nothing : html`<p class="error-line" data-test="pr-profile-error">${e.error}</p>`}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-profile-save" ?disabled=${e.busy}>${e.busy ? "Saving…" : "Save the mapping"}</button>
        <button type="button" class="btn btn-secondary" data-action="pr-profile-cancel" ?disabled=${e.busy}>Cancel</button>
      </div>
    </div>
  `;
}

// -- the price file tab ----------------------------------------------------------------------------------

export function renderPriceFile(model: PricingModel): TemplateResult {
  const m = model.priceFile;
  return html`
    ${m.vendorsPhase === "loading" ? html`<p class="notice-line" data-test="pr-pf-loading">Loading the manufacturers…</p>` : nothing}
    ${m.vendorsPhase === "error" || m.vendorsPhase === "refused"
      ? html`<p class="error-line" data-test="pr-pf-error">${m.vendorsError}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="pr-pf-reload">Try again</button></div>`
      : nothing}
    ${m.vendorsPhase === "ready"
      ? html`
          <section class="card">
            <div class="card-head">
              <h2 class="card-title">Manufacturers</h2>
              <span class="count" data-test="pr-pf-vendor-count">${m.vendors.length} ${m.vendors.length === 1 ? "vendor" : "vendors"}</span>
            </div>
            ${m.vendors.length === 0
              ? html`<p class="notice-line" data-test="pr-pf-empty">${priceFileEmptyWords()}</p>`
              : html`
                  <div class="tablewrap">
                    <table>
                      <thead>
                        <tr>
                          <th scope="col">Vendor</th>
                          <th scope="col">No.</th>
                          <th scope="col">Mapping</th>
                          <th scope="col">Last import</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${m.vendors.map(
                          (v) => html`
                            <tr data-vendor=${v.vendor_id}>
                              <td class="person">${v.name}</td>
                              <td class="mono dim">${v.vendor_no}</td>
                              <td>${priceFileOriginWords(v.origin)}</td>
                              <td class="mono dim">${v.last_import_at === "" ? "never" : v.last_import_at.slice(0, 10)}</td>
                            </tr>
                          `,
                        )}
                      </tbody>
                    </table>
                  </div>
                `}
          </section>
          ${renderPriceFileForm(model)}
          ${renderPriceFilePreview(m)}
          ${m.applyResult === "" ? nothing : html`<p class="notice-line" role="status" data-test="pr-pf-apply-result">${m.applyResult}</p>`}
          ${m.applyError === "" ? nothing : html`<p class="error-line" data-test="pr-pf-apply-error">${m.applyError}</p>`}
          ${renderPriceFileRuns(model)}
        `
      : nothing}
  `;
}

function renderPriceFileForm(model: PricingModel): TemplateResult {
  const m = model.priceFile;
  return html`
    <section class="card">
      <div class="card-head"><h2 class="card-title">Preview a price file</h2></div>
      <p class="pr-hint">
        The preview diffs the file against the retail anchor and writes nothing. The apply must echo the preview's
        digest; the server refuses a file that changed under you.
      </p>
      <div class="form-grid">
        <label class="field"><span class="field-label">Manufacturer</span>
          <select class="select" data-field="pr-pf-vendor" ?disabled=${m.previewBusy}>
            <option value="">Choose a manufacturer</option>
            ${m.vendors.map((v) => html`<option>${v.name}</option>`)}
          </select>
        </label>
        <label class="field"><span class="field-label">The file (CSV)</span>
          <input class="text-field" type="file" accept=".csv,text/csv" data-field="pr-pf-file" ?disabled=${m.previewBusy} /></label>
        <label class="field"><span class="field-label">Effective date (schedules the staged batch)</span>
          <input class="text-field" type="date" data-field="pr-pf-effective" ?disabled=${m.previewBusy} /></label>
      </div>
      ${m.previewError === "" ? nothing : html`<p class="error-line" data-test="pr-pf-preview-error">${m.previewError}</p>`}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-pf-preview" ?disabled=${m.previewBusy}>
          ${m.previewBusy ? "Reading the file…" : "Preview the diff"}
        </button>
      </div>
    </section>
  `;
}

function renderPriceFilePreview(m: PriceFileModel): TemplateResult | typeof nothing {
  const p = m.preview;
  if (p === null) return nothing;
  return html`
    <section class="card" data-test="pr-pf-preview">
      <div class="card-head">
        <h2 class="card-title">The diff</h2>
        <span class="chip">${p.format}</span>
        <span class="chip">mapping: ${p.mapping_source}</span>
      </div>
      <p class="notice-line">${priceFileCountsWords(p.counts, p.truncated)}</p>
      ${p.catalog_checked
        ? nothing
        : html`<p class="error-line">The catalog could not answer: every row reads as unknown. Preview again before applying.</p>`}
      <div class="tablewrap">
        <table>
          <thead>
            <tr>
              <th scope="col" class="num">Row</th>
              <th scope="col">SKU</th>
              <th scope="col">Outcome</th>
              <th scope="col">Retail now</th>
              <th scope="col">Retail in the file</th>
              <th scope="col">Set automatically</th>
            </tr>
          </thead>
          <tbody>
            ${p.lines.map(
              (line) => html`
                <tr data-pf-row=${line.row}>
                  <td class="num">${line.row}</td>
                  <td class="mono">${line.sku}</td>
                  <td><span class="chip ${line.outcome === "UNKNOWN" ? "chip-danger" : ""}">${outcomeWord(line.outcome)}</span></td>
                  <td class="num">${moneyWords(line.current_retail_cents)}</td>
                  <td class="num">${moneyWords(line.new_retail_cents)}${line.freight_estimated ? html` <span class="chip chip-dim">est. freight</span>` : nothing}</td>
                  <td class="dim">${derivationWord(line.derived_from)}</td>
                </tr>
              `,
            )}
          </tbody>
        </table>
      </div>
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-pf-apply" ?disabled=${m.applyBusy}>
          ${m.applyBusy ? "Staging…" : "Stage this file (the digest goes with it)"}
        </button>
      </div>
    </section>
  `;
}

function renderPriceFileRuns(model: PricingModel): TemplateResult {
  const m = model.priceFile;
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Import runs</h2>
        <span class="count" data-test="pr-pf-runs-count">${m.runs.length} ${m.runs.length === 1 ? "run" : "runs"}</span>
      </div>
      ${m.runsPhase === "error" ? html`<p class="error-line" data-test="pr-pf-runs-error">${m.runsError}</p>` : nothing}
      ${m.runs.length === 0
        ? html`<p class="notice-line" data-test="pr-pf-runs-empty">No runs yet. The road's own log fills as files are staged.</p>`
        : html`
            <div class="tablewrap">
              <table>
                <thead>
                  <tr>
                    <th scope="col">When</th>
                    <th scope="col">File</th>
                    <th scope="col">Batch</th>
                    <th scope="col" class="num">Rows</th>
                    <th scope="col" class="num">Changed</th>
                    <th scope="col">By</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.runs.map(
                    (run) => html`
                      <tr data-run=${run.id}>
                        <td class="mono dim">${run.created_at.slice(0, 10)}</td>
                        <td class="mono">${run.source_name} (${run.source_format})</td>
                        <td class="mono">${run.batch_no === "" ? "-" : run.batch_no}</td>
                        <td class="num">${run.counts.rows}</td>
                        <td class="num">${run.counts.changed}</td>
                        <td class="dim">${run.imported_by_name === "" ? "-" : run.imported_by_name}</td>
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

// -- the low margin tab ---------------------------------------------------------------------------------

function lowMarginLevelChip(level: string): string {
  if (level === "critical") return "chip-danger";
  if (level === "invalid") return "chip-dim";
  return "chip-warn-word";
}

export function renderLowMargin(model: PricingModel): TemplateResult {
  const m = model.lowMargin;
  return html`
    ${m.phase === "loading" ? html`<p class="notice-line" data-test="pr-lm-loading">Scanning the committed lines…</p>` : nothing}
    ${m.phase === "error" || m.phase === "refused"
      ? html`<p class="error-line" data-test="pr-lm-error">${m.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="pr-lm-reload">Try again</button></div>`
      : nothing}
    ${m.phase === "ready"
      ? html`
          <section class="card">
            <div class="card-head">
              <h2 class="card-title">Committed lines under target or floor</h2>
              <span class="count" data-test="pr-lm-count">${m.rows.length} ${m.rows.length === 1 ? "line" : "lines"}</span>
            </div>
            <div class="filters" role="group" aria-label="Scan filters">
              <select class="select" aria-label="Document type" data-field="pr-lm-doc-type">
                <option value="both">Quotes and orders</option>
                <option value="quotes">Quotes only</option>
                <option value="orders">Orders only</option>
              </select>
              <select class="select" aria-label="Sort" data-field="pr-lm-sort">
                <option value="margin_asc">Thinnest margin first</option>
                <option value="margin_desc">Fattest margin first</option>
              </select>
              <span class="branch-choice"><input type="checkbox" data-field="pr-lm-cleared" /> <span>Cleared only</span></span>
              <input class="text-field" type="date" aria-label="From" data-field="pr-lm-from" />
              <input class="text-field" type="date" aria-label="To" data-field="pr-lm-to" />
              <button type="button" class="btn btn-secondary" data-action="pr-lm-apply">Apply</button>
              <button type="button" class="btn btn-secondary" data-action="pr-lm-clear">Clear</button>
            </div>
            ${m.rows.length === 0
              ? html`<p class="notice-line" data-test="pr-lm-empty">${lowMarginEmptyWords()}</p>`
              : html`
                  <div class="tablewrap">
                    <table>
                      <thead>
                        <tr>
                          <th scope="col">Document</th>
                          <th scope="col">Line</th>
                          <th scope="col" class="num">Price</th>
                          <th scope="col" class="num">Margin</th>
                          <th scope="col">Level</th>
                          <th scope="col">Sold by</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${m.rows.map((row) => renderLowMarginRow(row))}
                      </tbody>
                    </table>
                  </div>
                `}
          </section>
          ${renderLowMarginProbe(model)}
        `
      : nothing}
  `;
}

function renderLowMarginRow(row: LowMarginScanRow): TemplateResult {
  return html`
    <tr data-lm=${row.doc_id}>
      <td>
        <div class="mono">${row.doc_no}</div>
        <div class="mono dim">${row.doc_at.slice(0, 10)}</div>
      </td>
      <td>${lowMarginWords(row)}</td>
      <td class="num">${moneyWords(row.unit_price_cents)}</td>
      <td class="num">${pctWords(row.margin_bps)}</td>
      <td>
        <span class="chip ${lowMarginLevelChip(row.level)}">${row.level}</span>
        ${row.cleared_below_floor ? html`<span class="chip chip-good">cleared: ${row.cleared_by === "" ? "a manager" : row.cleared_by}</span>` : nothing}
      </td>
      <td class="dim">${row.salesperson === "" ? "-" : row.salesperson}</td>
    </tr>
  `;
}

function renderLowMarginProbe(model: PricingModel): TemplateResult {
  const m = model.lowMargin.probe;
  return html`
    <section class="card">
      <div class="card-head"><h2 class="card-title">Grade a proposed line</h2></div>
      <p class="pr-hint">
        One line through the resolver, before it is quoted: the server grades it against the category's target and floor
        and names the breach, if there is one.
      </p>
      <div class="form-grid">
        <label class="field"><span class="field-label">Customer id</span>
          <input class="text-field" type="text" data-field="pr-lm-customer" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Customer type</span>
          <select class="select" data-field="pr-lm-customer-type" ?disabled=${m.busy}>
            <option value="CONTRACTOR">Contractor</option>
            <option value="RETAIL">Retail</option>
            <option value="BUILDER">Builder</option>
            <option value="GOVERNMENT">Government</option>
            <option value="INTERNAL">Internal</option>
          </select>
        </label>
        <label class="field"><span class="field-label">Product id</span>
          <input class="text-field" type="text" data-field="pr-lm-product" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Category id</span>
          <input class="text-field" type="text" data-field="pr-lm-category" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Quantity</span>
          <input class="text-field" type="text" inputmode="decimal" data-field="pr-lm-qty" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Unit</span>
          <input class="text-field" type="text" placeholder="SF" data-field="pr-lm-uom" ?disabled=${m.busy} /></label>
        <label class="field"><span class="field-label">Cost basis, dollars per base unit</span>
          <input class="text-field" type="text" inputmode="decimal" data-field="pr-lm-cost" ?disabled=${m.busy} /></label>
      </div>
      ${m.error === "" ? nothing : html`<p class="error-line" data-test="pr-lm-probe-error">${m.error}</p>`}
      ${m.entries.length === 0
        ? nothing
        : html`
            <div class="tablewrap" data-test="pr-lm-entries">
              <table>
                <thead>
                  <tr>
                    <th scope="col" class="num">Price</th>
                    <th scope="col" class="num">Margin</th>
                    <th scope="col">Level</th>
                    <th scope="col">Source</th>
                    <th scope="col">Why</th>
                  </tr>
                </thead>
                <tbody>
                  ${m.entries.map(
                    (entry) => html`
                      <tr data-entry=${entry.product_id}>
                        <td class="num">${moneyWords(entry.final_cents)}</td>
                        <td class="num">${pctWords(entry.margin_bps)}</td>
                        <td><span class="chip ${lowMarginLevelChip(entry.level)}">${entry.level}</span></td>
                        <td>${priceSourceWord(entry.source)}</td>
                        <td class="dim">${entry.invalid ? entry.reason : entry.reason === "" ? "-" : entry.reason}</td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
      <div class="pr-actions">
        <button type="button" class="btn btn-primary" data-action="pr-lm-grade" ?disabled=${m.busy}>
          ${m.busy ? "Grading…" : "Grade the line"}
        </button>
      </div>
    </section>
  `;
}

// -- the pricing audit tab ---------------------------------------------------------------------------

function auditEntityWords(row: PricingAuditRow): string {
  return `${row.entity_type} / ${row.entity_id.slice(0, 8)}`;
}

export function renderPricingAudit(model: PricingModel): TemplateResult {
  const m = model.audit;
  return html`
    ${m.phase === "loading" ? html`<p class="notice-line" data-test="pr-audit-loading">Loading the pricing log…</p>` : nothing}
    ${m.phase === "error" || m.phase === "refused"
      ? html`<p class="error-line" data-test="pr-audit-error">${m.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="pr-audit-reload">Try again</button></div>`
      : nothing}
    ${m.phase === "ready"
      ? html`
          <section class="card">
            <div class="card-head">
              <h2 class="card-title">Pricing log</h2>
              <span class="count" data-test="pr-audit-count">${m.rows.length} ${m.rows.length === 1 ? "entry" : "entries"}</span>
            </div>
            <div class="filters" role="group" aria-label="Log filters">
              <select class="select" aria-label="Entity" data-field="pr-aud-entity">
                <option value="">All entities</option>
                <option value="pricing_rule">Pricing rules</option>
                <option value="category_policy">Category policies</option>
                <option value="customer_override">Customer overrides</option>
                <option value="margin_enforcement">Margin enforcement</option>
              </select>
              <select class="select" aria-label="Action" data-field="pr-aud-action">
                <option value="">All actions</option>
                <option value="CREATE">${auditActionWord("CREATE")}</option>
                <option value="UPDATE">${auditActionWord("UPDATE")}</option>
                <option value="DELETE">${auditActionWord("DELETE")}</option>
                <option value="BYPASS">${auditActionWord("BYPASS")}</option>
              </select>
              <button type="button" class="btn btn-secondary" data-action="pr-audit-apply">Apply</button>
              <button type="button" class="btn btn-secondary" data-action="pr-audit-clear">Clear</button>
            </div>
            ${m.rows.length === 0
              ? html`<p class="notice-line" data-test="pr-audit-empty">No pricing log entries match. The log is append only: the filters are narrower than the record, not the actions undone.</p>`
              : html`
                  <div class="tablewrap">
                    <table>
                      <thead>
                        <tr>
                          <th scope="col">When</th>
                          <th scope="col">Entity</th>
                          <th scope="col">Action</th>
                          <th scope="col">Field</th>
                          <th scope="col">Change</th>
                          <th scope="col">Actor</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${m.rows.map((row) => renderPricingAuditRow(row))}
                      </tbody>
                    </table>
                  </div>
                `}
          </section>
        `
      : nothing}
  `;
}

function renderPricingAuditRow(row: PricingAuditRow): TemplateResult {
  const bypass = row.action === "BYPASS";
  const delta = deltaWords(row.old_value === "" ? null : row.old_value, row.new_value === "" ? null : row.new_value);
  return html`
    <tr data-audit=${row.id}>
      <td class="mono dim">${whenWords(row.created_at)}</td>
      <td class="mono">${auditEntityWords(row)}</td>
      <td><span class="chip ${bypass ? "chip-danger" : ""}">${auditActionWord(row.action)}</span></td>
      <td class="dim">${row.field_name === "" ? "-" : row.field_name}</td>
      <td>${delta === "" ? html`<span class="dim">-</span>` : delta}</td>
      <td class="mono dim">${row.user_id === "" ? "the engine" : row.user_id.slice(0, 8)}</td>
    </tr>
    ${bypass && row.bypass_reason !== ""
      ? html`<tr data-audit-reason=${row.id}><td colspan="6"><span class="chip chip-danger">bypass: ${row.bypass_reason}</span></td></tr>`
      : nothing}
  `;
}

// -- the change log tab ---------------------------------------------------------------------------------

function changeFieldWord(field: string): string {
  if (field === "cost_cents") return "cost";
  if (field === "retail_cents") return "retail";
  return field;
}

export function renderChangeLog(model: PricingModel): TemplateResult {
  const m = model.changeLog;
  return html`
    ${m.phase === "loading" ? html`<p class="notice-line" data-test="pr-log-loading">Loading the change log…</p>` : nothing}
    ${m.phase === "error" || m.phase === "refused"
      ? html`<p class="error-line" data-test="pr-log-error">${m.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="pr-log-reload">Try again</button></div>`
      : nothing}
    ${m.phase === "ready"
      ? html`
          <section class="card">
            <div class="card-head">
              <h2 class="card-title">Change log</h2>
              <span class="count" data-test="pr-log-count">${m.rows.length} ${m.rows.length === 1 ? "change" : "changes"}</span>
            </div>
            <div class="filters" role="group" aria-label="Change log filters">
              <input class="text-field" type="text" placeholder="Product id" aria-label="Product id" data-field="pr-log-product" />
              <input class="text-field" type="text" placeholder="Batch id" aria-label="Batch id" data-field="pr-log-batch" />
              <button type="button" class="btn btn-secondary" data-action="pr-log-apply">Apply</button>
              <button type="button" class="btn btn-secondary" data-action="pr-log-clear">Clear</button>
            </div>
            ${m.rows.length === 0
              ? html`<p class="notice-line" data-test="pr-log-empty">No changes logged yet. A cost or a retail that moved through a batch lands here: who moved it, from what to what.</p>`
              : html`
                  <div class="tablewrap">
                    <table>
                      <thead>
                        <tr>
                          <th scope="col">When</th>
                          <th scope="col">Product</th>
                          <th scope="col">Field</th>
                          <th scope="col">From</th>
                          <th scope="col">To</th>
                          <th scope="col">Effective</th>
                          <th scope="col">Batch</th>
                          <th scope="col">By</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${m.rows.map((row) => renderChangeLogRow(row))}
                      </tbody>
                    </table>
                  </div>
                `}
          </section>
        `
      : nothing}
  `;
}

function renderChangeLogRow(row: ChangeLogRow): TemplateResult {
  return html`
    <tr data-log=${row.id}>
      <td class="mono dim">${whenWords(row.created_at)}</td>
      <td class="mono">${row.product_id === "" ? "-" : row.product_id.slice(0, 8)}</td>
      <td>${changeFieldWord(row.field)}</td>
      <td class="num">${moneyWords(row.before_cents)}</td>
      <td class="num">${moneyWords(row.after_cents)}</td>
      <td class="mono dim">${row.effective_date === "" ? "-" : row.effective_date.slice(0, 10)}</td>
      <td class="mono dim">${row.batch_id === "" ? "-" : row.batch_id.slice(0, 8)}</td>
      <td class="mono dim">${row.changed_by === "" ? "-" : row.changed_by.slice(0, 8)}</td>
    </tr>
  `;
}
