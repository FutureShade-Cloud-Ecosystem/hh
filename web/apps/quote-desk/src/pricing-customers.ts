// Ported from hardscapeos_dibbits master 87d7941b app/src/customer-pricing.ts; adapted for HH.
//
// The customer prices tab: pick a customer and see the category prices set for them. A
// customer with none is system based and follows the tier rules. Add, Edit and Delete work
// on one category price (a percent off list or a markup on cost, never a fixed price, which
// is a customer and product rule). A save runs the editors' floor warning first (CP0-6).
//
// A read or a write belongs to the customer it began for: the customer id is captured when
// a load or a save starts, and an answer that comes back after the picker moved to another
// customer is dropped, never shown under the new name. Edit re-reads the customer's prices
// so the form carries the version the server holds now.
//
// HH changes: the customer comes from the app's own picker; reads and writes go through
// PricingClient; the floor warning is new. The ERP's text message consent block sits under
// this tab in the console and is not one of the five tabs this panel ports.
import { html, nothing } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import { err, ok } from "@hardscapehouse/api-client";
import type { PickerCustomer } from "@hardscapehouse/pos-kit/src/ported/_customer-picker.js";
import "./customer-picker.js";
import { PricingTabBase } from "./pricing-tab-base.js";
import { pricingStyles } from "./pricing-styles.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { CATEGORY_OPTIONS, categoryLabel } from "./_pricing-shared.js";
import { emptyOverrideForm, overrideFormFromRow, overrideSave, overrideValueText, type OverrideForm } from "./_pricing-forms.js";
import type { OverrideRow } from "./_pricing-wire.js";

/** Fired (bubbling, composed) when the editor picks a customer by name: the one place the
 * panel learns a customer id's name, which the rules list then shows where it is known. */
export const PRICING_CUSTOMER_KNOWN_EVENT = "pricing-customer-known";

const CALC_OPTIONS: ReadonlyArray<readonly [string, string]> = [
  ["PCT_OFF_LIST", "Discount % off list"],
  ["MARKUP_ON_COST", "Markup % on cost"],
];

@customElement("qd-pricing-customers")
export class QdPricingCustomers extends PricingTabBase {
  /** The customer an audit link pointed at: its prices load straight away. */
  @property({ attribute: false }) focusCustomerId = "";
  /** The branch the customer search is fenced to. */
  @property({ attribute: false }) branchScope = "";

  @state() private customerId = "";
  @state() private picked: PickerCustomer | null = null;
  @state() private loaded = false;
  @state() private overrides: OverrideRow[] = [];
  @state() private loading = false;
  @state() private listError = "";
  @state() private dialogOpen = false;
  @state() private form: OverrideForm = emptyOverrideForm();
  @state() private confirmDelete: OverrideRow | null = null;
  private loadSeq = 0;
  private editSeq = 0;

  static styles = pricingStyles;

  override connectedCallback(): void {
    super.connectedCallback();
    this.renderRoot.addEventListener("customer-picked", this.onPicked as EventListener);
    this.renderRoot.addEventListener("customer-cleared", this.onCleared);
  }

  override disconnectedCallback(): void {
    this.renderRoot.removeEventListener("customer-picked", this.onPicked as EventListener);
    this.renderRoot.removeEventListener("customer-cleared", this.onCleared);
    super.disconnectedCallback();
  }

  reload(): void {
    if (this.customerId !== "") void this.load(this.customerId);
  }

  private readonly onPicked = (event: CustomEvent<PickerCustomer>): void => {
    this.picked = event.detail;
    this.customerId = event.detail.id;
    this.dispatchEvent(new CustomEvent(PRICING_CUSTOMER_KNOWN_EVENT, { detail: { id: event.detail.id, name: event.detail.name }, bubbles: true, composed: true }));
    void this.load(event.detail.id);
  };

  private readonly onCleared = (): void => {
    this.loadSeq += 1;
    this.picked = null;
    this.customerId = "";
    this.loaded = false;
    this.loading = false;
    this.overrides = [];
    this.listError = "";
  };

  private async load(customerId: string): Promise<void> {
    const pricing = this.pricing;
    const ctx = this.ctx; // the tab's own context: cancelled when the panel closes (the Pricing swap, a tab switch) or the person signs out
    if (pricing === null || ctx.stale) return;
    const seq = (this.loadSeq += 1);
    this.loading = true;
    this.listError = "";
    const result = await ctx.guarded(pricing.listOverrides(customerId));
    // An answer for a customer the picker has since left is dropped.
    if (result === undefined || seq !== this.loadSeq || customerId !== this.customerId) return;
    this.loading = false;
    if (!result.ok) {
      this.overrides = [];
      this.loaded = false;
      this.noteRefusal(result, false);
      this.listError = `This customer's prices could not be read. ${spokenRefusal(result.error)}`;
      return;
    }
    this.overrides = result.value;
    this.loaded = true;
  }

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("focusCustomerId")) {
      const id = this.focusCustomerId.trim();
      if (id !== "" && id !== this.customerId) {
        this.customerId = id;
        this.picked = { id, name: `Customer ${id.slice(0, 8)}`, account_no: "", customer_type: "", payment_terms_days: 0, credit_limit_cents: 0, open_balance_cents: 0 };
        void this.load(id);
      }
    }
  }

  protected override closeTopDialog(): boolean {
    if (this.confirmDelete !== null) {
      this.confirmDelete = null;
      this.formError = "";
      this.restoreFocus();
      return true;
    }
    if (this.dialogOpen) {
      this.dialogOpen = false;
      this.formError = "";
      this.restoreFocus();
      return true;
    }
    return false;
  }

  protected override onClickAction(action: string, control: HTMLElement, event: MouseEvent): void {
    if (this.onFloorAction(action)) return;
    switch (action) {
      case "add":
        this.captureFocus();
        this.form = emptyOverrideForm();
        this.formError = "";
        this.dialogOpen = true;
        break;
      case "edit":
        void this.openEdit(control.getAttribute("data-id") ?? "");
        break;
      case "delete-ask": {
        const row = this.overrides.find((o) => o.id === control.getAttribute("data-id"));
        if (row !== undefined) {
          this.captureFocus();
          this.formError = "";
          this.confirmDelete = row;
        }
        break;
      }
      case "delete":
        void this.removePrice();
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
    if (field === "valuePct") this.form = { ...this.form, valuePct: value };
    if (field === "priceUom") this.form = { ...this.form, priceUom: value };
    if (field === "notes") this.form = { ...this.form, notes: value };
  }

  protected override onFieldChange(field: string, control: HTMLElement): void {
    const value = (control as HTMLSelectElement).value;
    if (field === "categoryId") this.form = { ...this.form, categoryId: value };
    if (field === "calcType") this.form = { ...this.form, calcType: value === "MARKUP_ON_COST" ? "MARKUP_ON_COST" : "PCT_OFF_LIST" };
  }

  /** Edit re-reads this customer's prices so the form carries the version the server holds now. */
  private async openEdit(id: string): Promise<void> {
    const pricing = this.pricing;
    const customerId = this.customerId;
    const ctx = this.ctx;
    if (pricing === null || customerId === "" || id === "" || ctx.stale) return;
    this.captureFocus();
    // Two quick Edit clicks: only the latest answer opens, a slower earlier one is dropped.
    const seq = (this.editSeq += 1);
    const fresh = await ctx.guarded(pricing.listOverrides(customerId));
    if (fresh === undefined || customerId !== this.customerId || seq !== this.editSeq) return;
    if (!fresh.ok) {
      if (this.noteRefusal(fresh, false)) return;
      this.listError = `That price could not be opened. ${spokenRefusal(fresh.error)}`;
      return;
    }
    this.overrides = fresh.value;
    const row = fresh.value.find((o) => o.id === id);
    if (row === undefined) {
      this.listError = "That price no longer exists; the list has been read again.";
      return;
    }
    this.listError = "";
    this.form = overrideFormFromRow(row);
    this.formError = "";
    this.dialogOpen = true;
  }

  private async save(): Promise<void> {
    const pricing = this.pricing;
    if (pricing === null || this.saving) return;
    const built = overrideSave(this.form);
    if (!built.ok) {
      this.formError = built.error;
      return;
    }
    // Bound to the customer and record this save began for.
    const customerId = this.customerId;
    const id = this.form.id;
    const write = built.value;
    await this.saveWithFloor(
      { kind: "override", id, customerId, write },
      async () => {
        if (id === "") {
          const created = await pricing.createOverride(customerId, write);
          return created.ok ? ok(created.value.id) : err(created.error, created.status);
        }
        const updated = await pricing.updateOverride(id, write);
        return updated.ok ? ok(id) : err(updated.error, updated.status);
      },
      () => {
        this.dialogOpen = false;
        this.formError = "";
        this.restoreFocus();
        if (customerId === this.customerId) void this.load(customerId);
      },
    );
    if (this.formError !== "" && /changed|conflict|version/i.test(this.formError)) this.reload();
  }

  private async removePrice(): Promise<void> {
    const pricing = this.pricing;
    const row = this.confirmDelete;
    const customerId = this.customerId;
    const ctx = this.ctx;
    if (pricing === null || row === null || this.saving || ctx.stale) return;
    this.saving = true;
    this.formError = "";
    // The delete is bound to the row's id and version captured here, and its answer is the tab's
    // context's: a panel that closed while it was out reads nothing again from the dead panel.
    const result = await ctx.guarded(pricing.deleteOverride(row.id, row.version));
    if (result === undefined) return;
    this.saving = false;
    if (!result.ok) {
      if (!this.noteRefusal(result, true)) this.formError = spokenRefusal(result.error);
      if (customerId === this.customerId) void this.load(customerId);
      return;
    }
    this.confirmDelete = null;
    this.restoreFocus();
    if (customerId === this.customerId) void this.load(customerId);
  }

  protected override updated(changed: Map<string, unknown>): void {
    super.updated(changed);
    const picker = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("hos-customer-picker");
    if (picker !== null) {
      if (picker["selected"] !== this.picked) picker["selected"] = this.picked;
      if (picker["branchId"] !== this.branchScope) picker["branchId"] = this.branchScope;
    }
    if (this.dialogOpen) {
      const f = this.form;
      this.fillSelect("categoryId", CATEGORY_OPTIONS.map((c) => [c.id, c.name] as const), f.categoryId);
      this.fillSelect("calcType", CALC_OPTIONS, f.calcType);
      this.syncFields({ valuePct: f.valuePct, priceUom: f.priceUom, notes: f.notes });
      const category = this.renderRoot.querySelector<HTMLSelectElement>('select[data-field="categoryId"]');
      if (category !== null) category.disabled = f.id !== "";
    }
    if (changed.has("dialogOpen") && this.dialogOpen) this.focusDialog(this.form.id === "" ? '[data-field="categoryId"]' : '[data-field="calcType"]');
    if (changed.has("confirmDelete") && this.confirmDelete !== null) this.focusDialog('[data-action="close"]');
  }

  render() {
    const who = this.picked?.name ?? "";
    return html`
      <div class="panel" data-testid="pc-panel">
        <div class="head">
          <h2>Customer prices</h2>
          <span class="count" data-testid="pc-count"
            >${this.loading ? "loading..." : this.loaded ? `${who} · ${this.overrides.length} prices` : "search a customer"}</span
          >
        </div>
        <div class="lookup">
          <hos-customer-picker label-text="Customer"></hos-customer-picker>
          ${this.canEdit && this.loaded ? html`<button type="button" class="push" data-action="add" data-testid="pc-add">+ Add price</button>` : nothing}
        </div>
        ${this.listError !== ""
          ? html`<p class="msg err" role="alert" data-testid="pc-error">${this.listError}</p>`
          : !this.loaded
            ? html`<p class="msg" data-testid="pc-hint">Search a customer to see their prices. With none set, a customer is <strong>system based</strong>: they follow the tier and category rules.</p>`
            : this.overrides.length === 0
              ? html`<p class="msg" data-testid="pc-system"><strong>System based.</strong> This customer has no category prices.</p>`
              : html`
                  <table>
                    <thead>
                      <tr>
                        <th>Category</th>
                        <th class="r">Price</th>
                        <th>Notes</th>
                        <th class="r"></th>
                      </tr>
                    </thead>
                    <tbody>
                      ${this.overrides.map(
                        (o) => html`
                          <tr data-testid="pc-row">
                            <td>${categoryLabel(o.categoryId)}</td>
                            <td class="r mono amber">${overrideValueText(o)}</td>
                            <td class="dim">${o.notes !== "" ? o.notes : "-"}</td>
                            <td class="r">
                              ${this.canEdit
                                ? html`<span class="rowbtns"
                                    ><button type="button" data-action="edit" data-id="${o.id}" data-testid="pc-edit">Edit</button
                                    ><button type="button" class="danger" data-action="delete-ask" data-id="${o.id}" data-testid="pc-delete">Delete</button></span
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
      ${this.confirmDelete !== null ? this.renderConfirm(this.confirmDelete) : nothing}
      ${this.renderFloorDialog()}
    `;
  }

  private renderDialog() {
    const f = this.form;
    return html`
      <div class="backdrop" data-action="backdrop" data-testid="pc-backdrop">
        <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="pc-dialog-title" data-testid="pc-dialog">
          <div class="dialog-head">
            <h3 id="pc-dialog-title">${f.id !== "" ? "Edit customer price" : "Add customer price"}</h3>
            <button type="button" class="x" data-action="close" aria-label="Close">&times;</button>
          </div>
          <div class="dialog-body">
            <div class="field">
              <label for="pc-category">Category</label>
              <select id="pc-category" data-field="categoryId" data-testid="pc-category"></select>
            </div>
            <div class="field">
              <label for="pc-calc">Calculation</label>
              <select id="pc-calc" data-field="calcType" data-testid="pc-calc"></select>
              <span class="hint">No fixed price here. A negotiated fixed price is a customer and product rule.</span>
            </div>
            <div class="field">
              <label for="pc-pct">${f.calcType === "MARKUP_ON_COST" ? "Markup %" : "Discount %"}</label>
              <input id="pc-pct" class="mono" inputmode="decimal" data-field="valuePct" data-testid="pc-value" />
            </div>
            <div class="field">
              <label for="pc-uom">Price unit</label>
              <input id="pc-uom" class="mono" data-field="priceUom" data-testid="pc-uom" />
            </div>
            <div class="field">
              <label for="pc-notes">Notes</label>
              <input id="pc-notes" data-field="notes" data-testid="pc-notes" />
            </div>
            ${this.formError !== "" ? html`<div class="err" role="alert" data-testid="pc-form-error">${this.formError}</div>` : nothing}
          </div>
          <div class="dialog-foot">
            <button type="button" data-action="close">Cancel</button>
            <button type="button" class="primary" data-action="save" data-testid="pc-save" ?disabled=${this.saving}>${this.saving ? "Saving..." : "Save price"}</button>
          </div>
        </div>
      </div>
    `;
  }

  private renderConfirm(o: OverrideRow) {
    return html`
      <div class="backdrop" data-action="backdrop" data-testid="pc-confirm-backdrop">
        <div class="dialog narrow" role="dialog" aria-modal="true" aria-labelledby="pc-delete-title" data-testid="pc-confirm">
          <div class="dialog-head">
            <h3 id="pc-delete-title">Delete customer price</h3>
            <button type="button" class="x" data-action="close" aria-label="Close">&times;</button>
          </div>
          <div class="dialog-body">
            <div class="confirm-note">Remove the ${categoryLabel(o.categoryId)} price (${overrideValueText(o)})? The customer goes back to system pricing for this category.</div>
            ${this.formError !== "" ? html`<div class="err" role="alert" data-testid="pc-delete-error">${this.formError}</div>` : nothing}
          </div>
          <div class="dialog-foot">
            <button type="button" data-action="close">Cancel</button>
            <button type="button" class="danger" data-action="delete" data-testid="pc-delete-confirm" ?disabled=${this.saving}>${this.saving ? "Deleting..." : "Delete"}</button>
          </div>
        </div>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-pricing-customers": QdPricingCustomers;
  }
}
