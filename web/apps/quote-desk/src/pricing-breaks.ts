// Ported from hardscapeos_dibbits master 87d7941b app/src/quantity-breaks.ts; adapted for HH.
//
// The quantity breaks tab: each schedule with its tiers (from, to, kind, adjustment), a New
// quantity break button, and a dialog with a dynamic tier list. Edit opens the one schedule
// fresh from the server; Retire asks first and keeps the history. A save runs the editors'
// floor warning first (CP0-6).
//
// CP0-8 (answer A): a surcharge tier never changes a price. Under the replace rule a break
// prices off the list price and replaces the tier price only when it is cheaper, and an
// uplift is never cheaper (core/internal/pricing/promo.go, docs/BLOCKERS.md). The ERP
// editor offers both surcharge kinds as if they worked; this one says plainly that they do
// not, in a note on the list and on the editor, and marks every such tier "not applied".
// Nothing is deleted or hidden: the tiers stay as configured and the server still accepts
// them.
//
// HH changes: the ERP's precedence text said a break stacks on the tier price; the replace
// rule is the decided behaviour, so the text says that. Reads and writes go through
// PricingClient; the tier form is the unit tested _pricing-forms.ts.
import { html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { err, ok } from "@hardscapehouse/api-client";
import { PricingTabBase } from "./pricing-tab-base.js";
import { pricingStyles } from "./pricing-styles.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { CATEGORY_OPTIONS, categoryLabel } from "./_pricing-shared.js";
import {
  BREAK_SURCHARGE_NOTE,
  BREAK_UOMS,
  BREAK_VALUE_CHOICES,
  breakFormFromRow,
  breakSave,
  emptyBreakForm,
  emptyTierForm,
  isSurchargeTier,
  schedulesWithUnappliedTiers,
  tierMark,
  tierValueDisplay,
  type BreakForm,
  type BreakValueType,
} from "./_pricing-forms.js";
import type { BreakRow } from "./_pricing-wire.js";

@customElement("qd-pricing-breaks")
export class QdPricingBreaks extends PricingTabBase {
  @state() private breaks: BreakRow[] = [];
  @state() private productNames: Record<string, string> = {};
  @state() private loading = true;
  @state() private listError = "";
  @state() private dialogOpen = false;
  @state() private form: BreakForm = emptyBreakForm();
  @state() private confirmRetire: BreakRow | null = null;
  private loadSeq = 0;
  private editSeq = 0;

  static styles = pricingStyles;

  override connectedCallback(): void {
    super.connectedCallback();
    this.reload();
  }

  reload(): void {
    void this.load();
  }

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("pricing") && this.pricing !== null && this.breaks.length === 0 && this.listError === "") this.reload();
  }

  private async load(): Promise<void> {
    const pricing = this.pricing;
    const ctx = this.ctx; // the tab's own context: cancelled when the panel closes (the Pricing swap, a tab switch) or the person signs out
    if (pricing === null || ctx.stale) return;
    const seq = (this.loadSeq += 1);
    this.loading = true;
    this.listError = "";
    const result = await ctx.guarded(pricing.listBreaks());
    if (result === undefined || seq !== this.loadSeq) return;
    this.loading = false;
    if (!result.ok) {
      this.breaks = [];
      this.noteRefusal(result, false);
      this.listError = `The quantity breaks could not be read. ${spokenRefusal(result.error)}`;
      return;
    }
    this.breaks = result.value;
    void this.nameProducts(result.value);
  }

  /** A product scoped schedule shows "SKU, name" when the catalogue answers; the short id stands in until then. */
  private async nameProducts(rows: BreakRow[]): Promise<void> {
    const pricing = this.pricing;
    if (pricing === null) return;
    // The lookups run in the tab's context: a panel that closed while they were out paints no names.
    const ctx = this.ctx;
    const missing = [...new Set(rows.map((b) => b.productId).filter((id): id is string => id !== undefined))].filter((id) => !(id in this.productNames));
    const labels = await ctx.guarded(Promise.all(missing.map(async (id) => [id, await pricing.productLabel(id)] as const)));
    if (labels === undefined) return;
    const next = { ...this.productNames };
    for (const [id, label] of labels) if (label.ok) next[id] = label.value;
    this.productNames = next;
  }

  protected override closeTopDialog(): boolean {
    if (this.confirmRetire !== null) {
      this.confirmRetire = null;
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
    const index = Number(control.getAttribute("data-index") ?? "-1");
    switch (action) {
      case "new":
        this.captureFocus();
        this.form = emptyBreakForm();
        this.formError = "";
        this.dialogOpen = true;
        break;
      case "edit":
        void this.openEdit(control.getAttribute("data-id") ?? "");
        break;
      case "retire-ask": {
        const row = this.breaks.find((b) => b.id === control.getAttribute("data-id"));
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
      case "add-tier":
        this.form = { ...this.form, tiers: [...this.form.tiers, emptyTierForm()] };
        break;
      case "remove-tier":
        this.form = { ...this.form, tiers: this.form.tiers.filter((_, i) => i !== index) };
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

  private setTier(index: number, patch: Partial<BreakForm["tiers"][number]>): void {
    this.form = { ...this.form, tiers: this.form.tiers.map((t, i) => (i === index ? { ...t, ...patch } : t)) };
  }

  protected override onFieldInput(field: string, control: HTMLElement): void {
    const value = (control as HTMLInputElement).value;
    if (field === "code") this.form = { ...this.form, code: value };
    else if (field === "name") this.form = { ...this.form, name: value };
    else if (field === "productId") this.form = { ...this.form, productId: value };
    else if (field.startsWith("tier-")) {
      const [, key, index] = field.split("-");
      const i = Number(index);
      if (key === "fromQty") this.setTier(i, { fromQty: value });
      if (key === "toQty") this.setTier(i, { toQty: value });
      if (key === "valueInput") this.setTier(i, { valueInput: value });
    }
  }

  protected override onFieldChange(field: string, control: HTMLElement): void {
    const value = (control as HTMLSelectElement).value;
    if (field === "scopeKind") this.form = { ...this.form, scopeKind: value === "product" ? "product" : "category" };
    else if (field === "categoryId") this.form = { ...this.form, categoryId: value };
    else if (field === "thresholdUom") this.form = { ...this.form, thresholdUom: value };
    else if (field.startsWith("tier-")) {
      const [, key, index] = field.split("-");
      const i = Number(index);
      if (key === "valueType") this.setTier(i, { valueType: value as BreakValueType });
      if (key === "priceUom") this.setTier(i, { priceUom: value });
    }
  }

  /** Edit reads the one schedule fresh, so the form carries the version the server holds now. */
  private async openEdit(id: string): Promise<void> {
    const pricing = this.pricing;
    const ctx = this.ctx;
    if (pricing === null || id === "" || ctx.stale) return;
    this.captureFocus();
    // Two quick Edit clicks: only the latest answer opens.
    const seq = (this.editSeq += 1);
    const fresh = await ctx.guarded(pricing.getBreak(id));
    if (fresh === undefined || seq !== this.editSeq) return;
    if (!fresh.ok) {
      if (this.noteRefusal(fresh, false)) return;
      this.listError = `That quantity break could not be opened. ${spokenRefusal(fresh.error)}`;
      this.reload();
      return;
    }
    this.listError = "";
    this.form = breakFormFromRow(fresh.value);
    this.formError = "";
    this.dialogOpen = true;
  }

  private async save(): Promise<void> {
    const pricing = this.pricing;
    if (pricing === null || this.saving) return;
    const built = breakSave(this.form);
    if (!built.ok) {
      this.formError = built.error;
      return;
    }
    const id = this.form.id;
    const write = built.value;
    await this.saveWithFloor(
      { kind: "quantity_break", id, write },
      async () => {
        if (id === "") {
          const created = await pricing.createBreak(write);
          return created.ok ? ok(created.value.id) : err(created.error, created.status);
        }
        const updated = await pricing.updateBreak(id, write);
        return updated.ok ? ok(id) : err(updated.error, updated.status);
      },
      () => {
        this.dialogOpen = false;
        this.formError = "";
        this.restoreFocus();
        this.reload();
      },
    );
    if (this.formError !== "" && /changed|conflict|version/i.test(this.formError)) this.reload();
  }

  private async retire(): Promise<void> {
    const pricing = this.pricing;
    const row = this.confirmRetire;
    const ctx = this.ctx;
    if (pricing === null || row === null || this.saving || ctx.stale) return;
    this.saving = true;
    this.formError = "";
    // The retire is bound to the row's id and version captured here, and its answer is the tab's
    // context's: a panel that closed while it was out reads nothing again from the dead panel.
    const result = await ctx.guarded(pricing.retireBreak(row.id, row.version));
    if (result === undefined) return;
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
    if (this.dialogOpen) {
      const f = this.form;
      this.fillSelect("scopeKind", [["category", "Category"], ["product", "Product"]], f.scopeKind);
      this.fillSelect("categoryId", CATEGORY_OPTIONS.map((c) => [c.id, c.name] as const), f.categoryId);
      this.fillSelect("thresholdUom", BREAK_UOMS.map((u) => [u, u] as const), f.thresholdUom);
      const values: Record<string, string | boolean> = { code: f.code, name: f.name, productId: f.productId };
      f.tiers.forEach((t, i) => {
        this.fillSelect(`tier-valueType-${i}`, BREAK_VALUE_CHOICES.map((c) => [c.value, c.label] as const), t.valueType);
        this.fillSelect(`tier-priceUom-${i}`, BREAK_UOMS.map((u) => [u, u] as const), t.priceUom);
        values[`tier-fromQty-${i}`] = t.fromQty;
        values[`tier-toQty-${i}`] = t.toQty;
        values[`tier-valueInput-${i}`] = t.valueInput;
      });
      this.syncFields(values);
    }
    if (changed.has("dialogOpen") && this.dialogOpen) this.focusDialog('[data-field="code"]');
    if (changed.has("confirmRetire") && this.confirmRetire !== null) this.focusDialog('[data-action="close"]');
  }

  private scopeText(h: BreakRow): string {
    if (h.productId !== undefined) return `product · ${this.productNames[h.productId] ?? "a product"}`;
    return `category · ${categoryLabel(h.categoryId ?? "")}`;
  }

  render() {
    const unapplied = schedulesWithUnappliedTiers(this.breaks);
    return html`
      <div class="panel" data-testid="pb-panel">
        <div class="head">
          <h2>Quantity breaks</h2>
          ${this.loading || this.listError !== "" ? nothing : html`<span class="count" data-testid="pb-count">${this.breaks.length} schedules</span>`}
        </div>
        <details class="info">
          <summary>Precedence</summary>
          <p class="dim small">
            A break prices off the list price and replaces the tier price only when it is cheaper. It never stacks on top of a tier discount.
          </p>
        </details>
        ${unapplied > 0
          ? html`<div class="note" data-testid="pb-surcharge-note">
              ${BREAK_SURCHARGE_NOTE} ${unapplied === 1 ? "1 schedule below holds such a tier." : `${unapplied} schedules below hold such tiers.`}
            </div>`
          : nothing}
        ${this.canEdit
          ? html`<div class="toolbar"><button type="button" class="primary push" data-action="new" data-testid="pb-new">+ New quantity break</button></div>`
          : nothing}
        ${this.loading
          ? html`<p class="msg">Loading the quantity breaks...</p>`
          : this.listError !== ""
            ? html`<p class="msg err" role="alert" data-testid="pb-error">${this.listError}</p>`
            : this.breaks.length === 0
              ? html`<p class="msg" data-testid="pb-empty">No quantity breaks are set up yet.</p>`
              : this.breaks.map((h) => this.renderSchedule(h))}
      </div>
      ${this.dialogOpen ? this.renderDialog() : nothing}
      ${this.confirmRetire !== null ? this.renderConfirm(this.confirmRetire) : nothing}
      ${this.renderFloorDialog()}
    `;
  }

  private renderSchedule(h: BreakRow) {
    return html`
      <div class="sched ${h.isActive ? "" : "inactive"}" data-testid="pb-schedule" data-code="${h.code}">
        <div class="sched-head">
          <span class="code">${h.code}</span>
          <span>${h.name}</span>
          <span class="scope">${this.scopeText(h)} · per ${h.thresholdUom}</span>
          ${this.canEdit
            ? html`<span class="acts"
                ><button type="button" data-action="edit" data-id="${h.id}" data-testid="pb-edit">Edit</button
                ><button type="button" class="danger" data-action="retire-ask" data-id="${h.id}" data-testid="pb-retire">Retire</button></span
              >`
            : nothing}
        </div>
        <table>
          <thead>
            <tr>
              <th>From</th>
              <th>To</th>
              <th>Kind</th>
              <th class="r">Adjustment</th>
            </tr>
          </thead>
          <tbody>
            ${h.items.map(
              (it) => html`
                <tr data-testid="pb-tier">
                  <td class="mono">${it.fromQty} ${h.thresholdUom}</td>
                  <td class="mono dim">${it.toQty === null ? "∞" : `${it.toQty} ${h.thresholdUom}`}</td>
                  <td>
                    <span class="badge ${isSurchargeTier(it.valueType) ? "red" : "green"}">${isSurchargeTier(it.valueType) ? "surcharge" : "discount"}</span>
                    ${tierMark(it.valueType) !== null ? html` <span class="badge" data-testid="pb-not-applied">${tierMark(it.valueType)}</span>` : nothing}
                  </td>
                  <td class="r mono ${isSurchargeTier(it.valueType) ? "dim" : "amber"}">${tierValueDisplay(it)}</td>
                </tr>
              `,
            )}
          </tbody>
        </table>
      </div>
    `;
  }

  private renderDialog() {
    const f = this.form;
    return html`
      <div class="backdrop" data-action="backdrop" data-testid="pb-backdrop">
        <div class="dialog wide" role="dialog" aria-modal="true" aria-labelledby="pb-dialog-title" data-testid="pb-dialog">
          <div class="dialog-head">
            <h3 id="pb-dialog-title">${f.id !== "" ? "Edit quantity break" : "New quantity break"}</h3>
            <button type="button" class="x" data-action="close" aria-label="Close">&times;</button>
          </div>
          <div class="dialog-body">
            <div class="field">
              <label for="pb-code">Code (unique)</label>
              <input id="pb-code" class="mono" data-field="code" data-testid="pb-code" />
            </div>
            <div class="field">
              <label for="pb-name">Name</label>
              <input id="pb-name" data-field="name" data-testid="pb-name" />
            </div>
            <div class="field">
              <label for="pb-scope">Scope</label>
              <select id="pb-scope" data-field="scopeKind" data-testid="pb-scope"></select>
            </div>
            ${f.scopeKind === "category"
              ? html`<div class="field">
                  <label for="pb-category">Category</label>
                  <select id="pb-category" data-field="categoryId" data-testid="pb-category"></select>
                </div>`
              : html`<div class="field">
                  <label for="pb-product">Product id (uuid)</label>
                  <input id="pb-product" class="mono" data-field="productId" data-testid="pb-product" />
                </div>`}
            <div class="field">
              <label for="pb-threshold">Threshold unit (the unit from and to are measured in)</label>
              <select id="pb-threshold" data-field="thresholdUom" data-testid="pb-threshold"></select>
            </div>
            <div class="note" style="margin: 0" data-testid="pb-dialog-note">${BREAK_SURCHARGE_NOTE}</div>
            <div class="field">
              <label>Tiers (thresholds strictly ascending)</label>
              ${f.tiers.map((t, i) => this.renderTier(i, isSurchargeTier(t.valueType)))}
              <button type="button" data-action="add-tier" data-testid="pb-add-tier">+ Add tier</button>
            </div>
            ${this.formError !== "" ? html`<div class="err" role="alert" data-testid="pb-form-error">${this.formError}</div>` : nothing}
          </div>
          <div class="dialog-foot">
            <button type="button" data-action="close">Cancel</button>
            <button type="button" class="primary" data-action="save" data-testid="pb-save" ?disabled=${this.saving}>${this.saving ? "Saving..." : "Save break"}</button>
          </div>
        </div>
      </div>
    `;
  }

  private renderTier(i: number, surcharge: boolean) {
    const t = this.form.tiers[i];
    const percent = t !== undefined && BREAK_VALUE_CHOICES.find((c) => c.value === t.valueType)?.unit === "pct";
    return html`
      <div class="tier-row ${surcharge ? "unapplied" : ""}" data-testid="pb-tier-row">
        <div class="field">
          <label>From</label>
          <input class="mono" inputmode="decimal" aria-label="Tier ${i + 1} from" data-field="tier-fromQty-${i}" />
        </div>
        <div class="field">
          <label>To</label>
          <input class="mono" inputmode="decimal" aria-label="Tier ${i + 1} to" data-field="tier-toQty-${i}" />
        </div>
        <div class="field">
          <label>Kind</label>
          <select aria-label="Tier ${i + 1} kind" data-field="tier-valueType-${i}"></select>
        </div>
        <div class="field">
          <label>${percent ? "Percent" : "Dollars ($)"}</label>
          <input class="mono" inputmode="decimal" aria-label="Tier ${i + 1} value" data-field="tier-valueInput-${i}" />
        </div>
        <div class="field">
          <label>Unit</label>
          <select aria-label="Tier ${i + 1} unit" data-field="tier-priceUom-${i}"></select>
        </div>
        <button type="button" class="danger" aria-label="Remove tier ${i + 1}" data-action="remove-tier" data-index="${i}" ?disabled=${this.form.tiers.length <= 1}>&times;</button>
        ${surcharge ? html`<div class="tier-mark" data-testid="pb-tier-mark">Not applied: this tier never changes a price.</div>` : nothing}
      </div>
    `;
  }

  private renderConfirm(h: BreakRow) {
    return html`
      <div class="backdrop" data-action="backdrop" data-testid="pb-confirm-backdrop">
        <div class="dialog narrow" role="dialog" aria-modal="true" aria-labelledby="pb-retire-title" data-testid="pb-confirm">
          <div class="dialog-head">
            <h3 id="pb-retire-title">Retire quantity break</h3>
            <button type="button" class="x" data-action="close" aria-label="Close">&times;</button>
          </div>
          <div class="dialog-body">
            <div class="confirm-note">This retires ${h.code} (${h.name}). The schedule stops applying but its history is kept and logged.</div>
            ${this.formError !== "" ? html`<div class="err" role="alert" data-testid="pb-retire-error">${this.formError}</div>` : nothing}
          </div>
          <div class="dialog-foot">
            <button type="button" data-action="close">Cancel</button>
            <button type="button" class="danger" data-action="retire" data-testid="pb-retire-confirm" ?disabled=${this.saving}>${this.saving ? "Retiring..." : "Retire"}</button>
          </div>
        </div>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-pricing-breaks": QdPricingBreaks;
  }
}
