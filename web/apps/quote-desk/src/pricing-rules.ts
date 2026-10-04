// Ported from hardscapeos_dibbits master 87d7941b app/src/rule-editor.ts; adapted for HH.
//
// The rule editor tab: the pricing rules (filters by scope, customer type and category),
// a New rule button, and a dialog for the rule form. Edit opens the one rule fresh from
// the server so the form carries its current version; Retire asks first and keeps the
// history. A save runs the editors' floor warning first (CP0-6). The server is the
// authority on who may write and on every rule it refuses; the form only keeps a typo
// from costing a round trip.
//
// HH changes: reads and writes go through PricingClient; the form logic is the unit tested
// _pricing-forms.ts; the floor warning is new; the dialog's focus and Escape come from the
// shared tab base. The scope offers the three the editor can create (a customer and
// category price belongs to the Customer prices tab); a rule of that fourth scope still
// lists, in its own words.
import { html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { err, ok } from "@hardscapehouse/api-client";
import { PricingTabBase } from "./pricing-tab-base.js";
import { pricingStyles } from "./pricing-styles.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { CATEGORY_OPTIONS, CUSTOMER_TYPES, categoryLabel, scopeLabel } from "./_pricing-shared.js";
import {
  EDITABLE_SCOPES,
  emptyRuleForm,
  ruleAppliesTo,
  ruleCalcOptions,
  ruleFormFromRow,
  ruleSave,
  ruleValueText,
  setRuleScope,
  type RuleCalc,
  type RuleForm,
  type RuleScope,
} from "./_pricing-forms.js";
import type { RuleRow } from "./_pricing-wire.js";

const CALC_LABEL: Record<RuleCalc, string> = {
  PCT_OFF_LIST: "Discount % off list",
  MARKUP_ON_COST: "Markup % on cost",
  ABSOLUTE: "Fixed price",
};

const SCOPE_OPTION: Record<string, string> = {
  TIER_DEFAULT: "Tier default",
  TIER_CATEGORY: "Tier + category",
  CUSTOMER_PRODUCT: "Customer + product (negotiated)",
  CUSTOMER_CATEGORY: "Customer + category",
};

@customElement("qd-pricing-rules")
export class QdPricingRules extends PricingTabBase {
  @state() private rules: RuleRow[] = [];
  @state() private loading = true;
  @state() private listError = "";
  @state() private fScope = "";
  @state() private fType = "";
  @state() private fCategory = "";
  @state() private dialogOpen = false;
  @state() private form: RuleForm = emptyRuleForm();
  @state() private confirmRetire: RuleRow | null = null;
  @state() private branchNames: Array<{ id: string; name: string }> = [];
  private loadSeq = 0;
  private editSeq = 0;
  private branchesAsked = false;

  static styles = pricingStyles;

  override connectedCallback(): void {
    super.connectedCallback();
    this.reload();
  }

  reload(): void {
    void this.load();
  }

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("pricing") && this.pricing !== null && this.rules.length === 0 && this.listError === "") this.reload();
  }

  private async load(): Promise<void> {
    const pricing = this.pricing;
    if (pricing === null) return;
    const seq = (this.loadSeq += 1);
    this.loading = true;
    this.listError = "";
    if (!this.branchesAsked) {
      this.branchesAsked = true;
      void pricing.branches().then((b) => {
        if (b.ok) this.branchNames = b.value;
      });
    }
    // The filters are read at the start: an answer for old filters never lands over newer ones.
    const result = await pricing.listRules({ scope: this.fScope, customerType: this.fType, categoryId: this.fCategory });
    if (seq !== this.loadSeq) return;
    this.loading = false;
    if (!result.ok) {
      this.rules = [];
      this.noteRefusal(result, false);
      this.listError = `The rules could not be read. ${spokenRefusal(result.error)}`;
      return;
    }
    this.rules = result.value;
  }

  /** The scopes the form offers: the three the editor can create, and a customer and category rule's own while it is edited, so the select can show it and the edit carries its customer and category through. */
  private scopeChoices(f: RuleForm): readonly RuleScope[] {
    return f.id !== "" && f.scope === "CUSTOMER_CATEGORY" ? [...EDITABLE_SCOPES, "CUSTOMER_CATEGORY"] : EDITABLE_SCOPES;
  }

  private branchName = (id: string): string => this.branchNames.find((b) => b.id === id)?.name ?? id.slice(0, 8);

  protected override closeTopDialog(): boolean {
    if (this.confirmRetire !== null) {
      this.confirmRetire = null;
      this.formError = "";
      this.restoreFocus();
      return true;
    }
    if (this.dialogOpen) {
      this.closeForm();
      return true;
    }
    return false;
  }

  private closeForm(): void {
    this.dialogOpen = false;
    this.formError = "";
    this.restoreFocus();
  }

  protected override onClickAction(action: string, control: HTMLElement, event: MouseEvent): void {
    if (this.onFloorAction(action)) return;
    switch (action) {
      case "new":
        this.captureFocus();
        this.form = emptyRuleForm();
        this.formError = "";
        this.dialogOpen = true;
        break;
      case "edit":
        void this.openEdit(control.getAttribute("data-id") ?? "");
        break;
      case "retire-ask": {
        const row = this.rules.find((r) => r.id === control.getAttribute("data-id"));
        if (row !== undefined) {
          this.captureFocus();
          this.formError = "";
          this.confirmRetire = row;
        }
        break;
      }
      case "retire":
        void this.retire();
        break;
      case "save":
        void this.save();
        break;
      case "close":
        this.closeTopDialog();
        break;
      case "backdrop":
        if (event.composedPath()[0] === control) this.closeTopDialog();
        break;
    }
  }

  protected override onFieldInput(field: string, control: HTMLElement): void {
    const value = (control as HTMLInputElement).value;
    const f = this.form;
    switch (field) {
      case "valuePct":
        this.form = { ...f, valuePct: value };
        break;
      case "priceDollars":
        this.form = { ...f, priceDollars: value };
        break;
      case "priceUom":
        this.form = { ...f, priceUom: value };
        break;
      case "priority":
        this.form = { ...f, priority: value };
        break;
      case "detailText":
        this.form = { ...f, detailText: value };
        break;
      case "customerId":
        this.form = { ...f, customerId: value };
        break;
      case "productId":
        this.form = { ...f, productId: value };
        break;
      case "validFrom":
        this.form = { ...f, validFrom: value };
        break;
      case "validTo":
        this.form = { ...f, validTo: value };
        break;
    }
  }

  protected override onFieldChange(field: string, control: HTMLElement): void {
    const value = (control as HTMLSelectElement | HTMLInputElement).value;
    switch (field) {
      case "filter-scope":
        this.fScope = value;
        this.reload();
        break;
      case "filter-type":
        this.fType = value;
        this.reload();
        break;
      case "filter-category":
        this.fCategory = value;
        this.reload();
        break;
      case "scope":
        this.form = setRuleScope(this.form, value as RuleScope);
        break;
      case "customerType":
        this.form = { ...this.form, customerType: value };
        break;
      case "categoryId":
        this.form = { ...this.form, categoryId: value };
        break;
      case "branchId":
        this.form = { ...this.form, branchId: value };
        break;
      case "calcType":
        this.form = { ...this.form, calcType: value as RuleCalc };
        break;
      case "validFrom":
        this.form = { ...this.form, validFrom: value };
        break;
      case "validTo":
        this.form = { ...this.form, validTo: value };
        break;
    }
  }

  /** Edit reads the one rule fresh, so the form carries the version the server holds now. */
  private async openEdit(id: string): Promise<void> {
    const pricing = this.pricing;
    if (pricing === null || id === "") return;
    this.captureFocus();
    // Two quick Edit clicks: only the latest answer opens, a slower earlier one is dropped.
    const seq = (this.editSeq += 1);
    const fresh = await pricing.getRule(id);
    if (seq !== this.editSeq) return;
    if (!fresh.ok) {
      if (this.noteRefusal(fresh, false)) return;
      this.listError = `That rule could not be opened. ${spokenRefusal(fresh.error)}`;
      this.reload();
      return;
    }
    this.listError = "";
    this.form = ruleFormFromRow(fresh.value);
    this.formError = "";
    this.dialogOpen = true;
  }

  private async save(): Promise<void> {
    const pricing = this.pricing;
    if (pricing === null || this.saving) return;
    const built = ruleSave(this.form);
    if (!built.ok) {
      this.formError = built.error;
      return;
    }
    // The snapshot the write is bound to: the id, the body and the version are taken now.
    const { id, write, patch } = built.value;
    await this.saveWithFloor(
      { kind: "rule", id, write },
      async () => {
        if (id === "") {
          const created = await pricing.createRule(write);
          return created.ok ? ok(created.value.id) : err(created.error, created.status);
        }
        const updated = await pricing.updateRule(id, patch);
        return updated.ok ? ok(id) : err(updated.error, updated.status);
      },
      () => {
        this.dialogOpen = false;
        this.formError = "";
        this.restoreFocus();
        this.reload();
      },
    );
    // A stale version is the server's 409: the list is read again so the next try carries the new one.
    if (this.formError !== "" && /changed|conflict|version/i.test(this.formError)) this.reload();
  }

  private async retire(): Promise<void> {
    const pricing = this.pricing;
    const row = this.confirmRetire;
    if (pricing === null || row === null || this.saving) return;
    this.saving = true;
    this.formError = "";
    const result = await pricing.retireRule(row.id, row.version);
    this.saving = false;
    if (!result.ok) {
      if (!this.noteRefusal(result, true)) this.formError = spokenRefusal(result.error);
      this.reload();
      return;
    }
    this.confirmRetire = null;
    this.restoreFocus();
    this.reload();
  }

  protected override updated(changed: Map<string, unknown>): void {
    super.updated(changed);
    this.fillSelect("filter-scope", [["", "All scopes"], ...EDITABLE_SCOPES.map((s) => [s, SCOPE_OPTION[s] ?? scopeLabel(s)] as const)], this.fScope);
    this.fillSelect("filter-type", [["", "All customer types"], ...CUSTOMER_TYPES.map((t) => [t, t] as const)], this.fType);
    this.fillSelect("filter-category", [["", "All categories"], ...CATEGORY_OPTIONS.map((c) => [c.id, c.name] as const)], this.fCategory);
    if (this.dialogOpen) {
      const f = this.form;
      this.fillSelect("scope", this.scopeChoices(f).map((s) => [s, SCOPE_OPTION[s] ?? scopeLabel(s)] as const), f.scope);
      this.fillSelect("customerType", CUSTOMER_TYPES.map((t) => [t, t] as const), f.customerType);
      this.fillSelect("categoryId", CATEGORY_OPTIONS.map((c) => [c.id, c.name] as const), f.categoryId);
      this.fillSelect("branchId", [["", "All branches"], ...this.branchNames.map((b) => [b.id, b.name] as const)], f.branchId);
      this.fillSelect("calcType", ruleCalcOptions(f.scope).map((c) => [c, CALC_LABEL[c]] as const), f.calcType);
      this.syncFields({
        valuePct: f.valuePct,
        priceDollars: f.priceDollars,
        priceUom: f.priceUom,
        priority: f.priority,
        detailText: f.detailText,
        customerId: f.customerId,
        productId: f.productId,
        validFrom: f.validFrom,
        validTo: f.validTo,
      });
    }
    if (changed.has("dialogOpen") && this.dialogOpen) this.focusDialog('[data-field="scope"]');
    if (changed.has("confirmRetire") && this.confirmRetire !== null) this.focusDialog('[data-action="close"]');
  }

  render() {
    return html`
      <div class="panel" data-testid="pr-panel">
        <div class="head">
          <h2>Pricing rules</h2>
          ${this.loading || this.listError !== "" ? nothing : html`<span class="count" data-testid="pr-count">${this.rules.length} rules</span>`}
        </div>
        <div class="toolbar">
          <select aria-label="Filter by scope" data-field="filter-scope" data-testid="pr-filter-scope"></select>
          <select aria-label="Filter by customer type" data-field="filter-type" data-testid="pr-filter-type"></select>
          <select aria-label="Filter by category" data-field="filter-category" data-testid="pr-filter-category"></select>
          ${this.canEdit ? html`<button type="button" class="primary push" data-action="new" data-testid="pr-new">+ New rule</button>` : nothing}
        </div>
        ${this.loading
          ? html`<p class="msg">Loading the rules...</p>`
          : this.listError !== ""
            ? html`<p class="msg err" role="alert" data-testid="pr-error">${this.listError}</p>`
            : this.rules.length === 0
              ? html`<p class="msg" data-testid="pr-empty">No rules match.</p>`
              : html`
                  <table>
                    <thead>
                      <tr>
                        <th>Scope</th>
                        <th>Applies to</th>
                        <th class="r">Value</th>
                        <th class="r">Priority</th>
                        <th class="r"></th>
                      </tr>
                    </thead>
                    <tbody>
                      ${this.rules.map(
                        (r) => html`
                          <tr class="${r.isActive ? "" : "inactive"}" data-testid="pr-row">
                            <td><span class="scopechip">${scopeLabel(r.scope)}</span></td>
                            <td>
                              <div>${ruleAppliesTo(r, this.branchName, categoryLabel)}</div>
                              <div class="dim small">${r.detailText !== "" ? r.detailText : "-"}</div>
                            </td>
                            <td class="r mono amber">${ruleValueText(r)}</td>
                            <td class="r mono">${r.priority}</td>
                            <td class="r">
                              ${this.canEdit
                                ? html`<span class="rowbtns"
                                    ><button type="button" data-action="edit" data-id="${r.id}" data-testid="pr-edit">Edit</button
                                    ><button type="button" class="danger" data-action="retire-ask" data-id="${r.id}" data-testid="pr-retire">Retire</button></span
                                  >`
                                : nothing}
                            </td>
                          </tr>
                        `,
                      )}
                    </tbody>
                  </table>
                `}
      </div>
      ${this.dialogOpen ? this.renderDialog() : nothing}
      ${this.confirmRetire !== null ? this.renderConfirm(this.confirmRetire) : nothing}
      ${this.renderFloorDialog()}
    `;
  }

  private renderDialog() {
    const f = this.form;
    const isTier = f.scope === "TIER_DEFAULT" || f.scope === "TIER_CATEGORY";
    return html`
      <div class="backdrop" data-action="backdrop" data-testid="pr-backdrop">
        <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="pr-dialog-title" data-testid="pr-dialog">
          <div class="dialog-head">
            <h3 id="pr-dialog-title">${f.id !== "" ? "Edit rule" : "New rule"}</h3>
            <button type="button" class="x" data-action="close" aria-label="Close">&times;</button>
          </div>
          <div class="dialog-body">
            <div class="field">
              <label for="pr-scope">Scope</label>
              <select id="pr-scope" data-field="scope" data-testid="pr-scope"></select>
            </div>
            ${isTier
              ? html`<div class="field">
                  <label for="pr-type">Customer type</label>
                  <select id="pr-type" data-field="customerType" data-testid="pr-customer-type"></select>
                </div>`
              : nothing}
            ${f.scope === "TIER_CATEGORY"
              ? html`<div class="field">
                    <label for="pr-category">Category</label>
                    <select id="pr-category" data-field="categoryId" data-testid="pr-category"></select>
                  </div>
                  <div class="field">
                    <label for="pr-branch">Branch (optional, blank is all)</label>
                    <select id="pr-branch" data-field="branchId" data-testid="pr-branch"></select>
                  </div>`
              : nothing}
            ${f.scope === "CUSTOMER_CATEGORY"
              ? html`<div class="field">
                    <label for="pr-customer">Customer id (uuid)</label>
                    <input id="pr-customer" class="mono" data-field="customerId" data-testid="pr-customer-id" />
                  </div>
                  <div class="field">
                    <label for="pr-category">Category</label>
                    <select id="pr-category" data-field="categoryId" data-testid="pr-category"></select>
                  </div>`
              : nothing}
            ${f.scope === "CUSTOMER_PRODUCT"
              ? html`<div class="field">
                    <label for="pr-customer">Customer id (uuid)</label>
                    <input id="pr-customer" class="mono" data-field="customerId" data-testid="pr-customer-id" />
                  </div>
                  <div class="field">
                    <label for="pr-product">Product id (uuid)</label>
                    <input id="pr-product" class="mono" data-field="productId" data-testid="pr-product-id" />
                  </div>`
              : nothing}
            <div class="field">
              <label for="pr-calc">Calculation</label>
              <select id="pr-calc" data-field="calcType" data-testid="pr-calc"></select>
              <span class="hint">Tier and category rules are a percentage only. A fixed price is a customer and product negotiation.</span>
            </div>
            ${f.calcType !== "ABSOLUTE"
              ? html`<div class="field">
                  <label for="pr-pct">${f.calcType === "MARKUP_ON_COST" ? "Markup %" : "Discount %"}</label>
                  <input id="pr-pct" class="mono" inputmode="decimal" data-field="valuePct" data-testid="pr-value" />
                </div>`
              : html`<div class="field">
                  <label for="pr-dollars">Fixed price (CA$)</label>
                  <input id="pr-dollars" class="mono" inputmode="decimal" data-field="priceDollars" data-testid="pr-price" />
                </div>`}
            <div class="field">
              <label for="pr-uom">Price unit</label>
              <input id="pr-uom" class="mono" data-field="priceUom" data-testid="pr-uom" />
            </div>
            <div class="field">
              <label for="pr-priority">Priority (higher wins within a scope)</label>
              <input id="pr-priority" class="mono" inputmode="numeric" pattern="[0-9]*" data-field="priority" data-testid="pr-priority" />
            </div>
            ${f.id !== ""
              ? html`<div class="field">
                    <label for="pr-from">Valid from (optional)</label>
                    <input id="pr-from" type="date" class="mono" data-field="validFrom" data-testid="pr-valid-from" />
                  </div>
                  <div class="field">
                    <label for="pr-to">Valid to (optional)</label>
                    <input id="pr-to" type="date" class="mono" data-field="validTo" data-testid="pr-valid-to" />
                  </div>`
              : nothing}
            <div class="field">
              <label for="pr-detail">Detail text</label>
              <input id="pr-detail" data-field="detailText" data-testid="pr-detail" />
            </div>
            ${this.formError !== "" ? html`<div class="err" role="alert" data-testid="pr-form-error">${this.formError}</div>` : nothing}
          </div>
          <div class="dialog-foot">
            <button type="button" data-action="close">Cancel</button>
            <button type="button" class="primary" data-action="save" data-testid="pr-save" ?disabled=${this.saving}>${this.saving ? "Saving..." : "Save rule"}</button>
          </div>
        </div>
      </div>
    `;
  }

  private renderConfirm(r: RuleRow) {
    return html`
      <div class="backdrop" data-action="backdrop" data-testid="pr-confirm-backdrop">
        <div class="dialog narrow" role="dialog" aria-modal="true" aria-labelledby="pr-retire-title" data-testid="pr-confirm">
          <div class="dialog-head">
            <h3 id="pr-retire-title">Retire rule</h3>
            <button type="button" class="x" data-action="close" aria-label="Close">&times;</button>
          </div>
          <div class="dialog-body">
            <div class="confirm-note">
              This retires the ${ruleAppliesTo(r, this.branchName, categoryLabel)} rule (${ruleValueText(r)}). The rule stops matching but its history is kept and logged.
            </div>
            ${this.formError !== "" ? html`<div class="err" role="alert" data-testid="pr-retire-error">${this.formError}</div>` : nothing}
          </div>
          <div class="dialog-foot">
            <button type="button" data-action="close">Cancel</button>
            <button type="button" class="danger" data-action="retire" data-testid="pr-retire-confirm" ?disabled=${this.saving}>${this.saving ? "Retiring..." : "Retire"}</button>
          </div>
        </div>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-pricing-rules": QdPricingRules;
  }
}
