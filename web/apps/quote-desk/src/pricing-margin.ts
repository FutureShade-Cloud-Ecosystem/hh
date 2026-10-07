// Ported from hardscapeos_dibbits master 87d7941b app/src/margin-grid.ts; adapted for HH.
//
// The category margin policy tab: one row per category with an inline target and floor
// (percent, mono), an Enforce floor box and a Save per row, written through the generated
// client. The Save arms (gold) only when its row differs from what the server holds. A
// save runs the editors' floor warning first (CP0-6): the products the new floor would put
// under it are shown and the editor confirms. One row's save never touches another row's
// half typed edit.
//
// The last row of the grid is the Add category row (decision log row 988): the categories
// that hold no policy yet as a picker, with target, floor and Enforce floor, saved through
// the same PUT (the server creates or replaces one category's policy) and the same floor
// warning; the saved category joins the grid as a row edited in place. The server has no
// route to remove a policy, so no row carries a Remove.
//
// The picker lists the catalogue's own categories (GET /api/v1/product-categories) that hold no
// policy yet, named as that read names them. The server still refuses (422) a policy for a category
// missing from the catalogue; no category id is built in, and a refusal shows the server's words.
//
// HH changes: the list and the write go through PricingClient (every answer read field by
// field); the floor warning is new; the unmapped category fallback stays a short id.
import { html, nothing } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import { err, ok } from "@hardscapehouse/api-client";
import { PricingTabBase } from "./pricing-tab-base.js";
import { pricingStyles } from "./pricing-styles.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { categoryLabel, formatPct, isKnownCategory } from "./_pricing-shared.js";
import { policyDirty, policyDraftFromRow, policySave, type PolicyDraft } from "./_pricing-forms.js";
import type { CategoryRow, PolicyRow } from "./_pricing-wire.js";

const ADD_ID = "__add";

@customElement("qd-pricing-margin")
export class QdPricingMargin extends PricingTabBase {
  /** The category an audit link pointed at: its row is marked and scrolled into view. */
  @property({ attribute: false }) focusCategoryId = "";

  @state() private rows: PolicyRow[] = [];
  @state() private drafts: Record<string, PolicyDraft> = {};
  /** The catalogue's active categories, as GET /api/v1/product-categories names them. */
  @state() private categories: CategoryRow[] = [];
  /** Said where the categories could not be read: the picker then offers nothing, rather than a guess. */
  @state() private categoriesError = "";
  @state() private loading = true;
  @state() private loadError = "";
  @state() private savingId = "";
  @state() private savedId = "";
  @state() private rowError: Record<string, string> = {};
  @state() private addDraft: PolicyDraft & { category: string } = { category: "", target: "", floor: "", enforce: true };
  @state() private addError = "";
  private loadSeq = 0;

  static styles = pricingStyles;

  override connectedCallback(): void {
    super.connectedCallback();
    this.reload();
  }

  reload(): void {
    void this.load();
  }

  private async load(): Promise<void> {
    const pricing = this.pricing;
    const ctx = this.ctx; // the tab's own context: cancelled when the panel closes (the Pricing swap, a tab switch) or the person signs out
    if (pricing === null || ctx.stale) return;
    const seq = (this.loadSeq += 1);
    this.loading = true;
    this.loadError = "";
    const result = await ctx.guarded(pricing.listPolicies());
    if (result === undefined || seq !== this.loadSeq) return;
    this.loading = false;
    if (!result.ok) {
      this.rows = [];
      this.noteRefusal(result, false);
      this.loadError = `The margin policy could not be read. ${spokenRefusal(result.error)}`;
      return;
    }
    this.rows = result.value;
    const drafts: Record<string, PolicyDraft> = {};
    for (const r of result.value) drafts[r.categoryId] = policyDraftFromRow(r);
    this.drafts = drafts;
    await this.loadCategories(pricing, ctx, seq);
  }

  /** The catalogue's categories. A read that fails offers none, and says so. */
  private async loadCategories(pricing: NonNullable<typeof this.pricing>, ctx: typeof this.ctx, seq: number): Promise<void> {
    const read = await ctx.guarded(pricing.listCategories());
    if (read === undefined || seq !== this.loadSeq) return;
    if (!read.ok) {
      this.categories = [];
      this.categoriesError = `The categories could not be read, so a category cannot be added now. ${spokenRefusal(read.error)}`;
      return;
    }
    this.categoriesError = "";
    this.categories = read.value.filter((c) => c.active);
  }

  /** The category's name as the catalogue read gives it, else the seed's, else a short id. */
  private nameOf(id: string): string {
    return this.categories.find((c) => c.id === id)?.name ?? categoryLabel(id);
  }

  /** The pricing client arrives after the first connect on the host's wiring pass. */
  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("pricing") && this.pricing !== null && this.rows.length === 0 && this.loadError === "") this.reload();
  }

  protected override closeTopDialog(): boolean {
    return false;
  }

  private draftOf(row: PolicyRow): PolicyDraft {
    return this.drafts[row.categoryId] ?? policyDraftFromRow(row);
  }

  private setDraft(id: string, patch: Partial<PolicyDraft>): void {
    const row = this.rows.find((r) => r.categoryId === id);
    if (row === undefined) return;
    this.drafts = { ...this.drafts, [id]: { ...this.draftOf(row), ...patch } };
    this.savedId = "";
  }

  protected override onClickAction(action: string, control: HTMLElement): void {
    if (this.onFloorAction(action)) return;
    if (action === "save") void this.saveRow(control.getAttribute("data-id") ?? "");
    if (action === "add-save") void this.saveAdd();
  }

  /** The catalogue's categories that hold no policy yet: the Add row's picker. */
  private uncovered(): string[] {
    const held = new Set(this.rows.map((r) => r.categoryId));
    return this.categories
      .map((c) => c.id)
      .filter((id) => !held.has(id))
      .sort((a, b) => this.nameOf(a).localeCompare(this.nameOf(b)) || a.localeCompare(b));
  }

  protected override onFieldInput(field: string, control: HTMLElement): void {
    if (field === "add-target" || field === "add-floor") {
      this.addDraft = { ...this.addDraft, [field === "add-target" ? "target" : "floor"]: (control as HTMLInputElement).value };
      this.addError = "";
      return;
    }
    const at = field.indexOf("-");
    if (at < 0) return;
    const kind = field.slice(0, at);
    const id = field.slice(at + 1);
    const input = control as HTMLInputElement;
    if (kind === "target") this.setDraft(id, { target: input.value });
    if (kind === "floor") this.setDraft(id, { floor: input.value });
  }

  protected override onFieldChange(field: string, control: HTMLElement): void {
    if (field === "add-category") this.addDraft = { ...this.addDraft, category: (control as HTMLSelectElement).value };
    else if (field === "add-enforce") this.addDraft = { ...this.addDraft, enforce: (control as HTMLInputElement).checked };
    if (field.startsWith("add-")) this.addError = "";
    if (field.startsWith("enforce-")) this.setDraft(field.slice("enforce-".length), { enforce: (control as HTMLInputElement).checked });
  }

  private async saveRow(id: string): Promise<void> {
    const pricing = this.pricing;
    const row = this.rows.find((r) => r.categoryId === id);
    if (pricing === null || row === undefined || this.saving) return;
    this.rowError = { ...this.rowError, [id]: "" };
    const built = policySave(this.draftOf(row));
    if (!built.ok) {
      this.rowError = { ...this.rowError, [id]: built.error };
      return;
    }
    const write = built.value;
    this.savingId = id;
    let savedRow: PolicyRow | null = null;
    await this.saveWithFloor(
      { kind: "category_policy", categoryId: id, write },
      async () => {
        const result = await pricing.upsertPolicy(id, write);
        if (!result.ok) return err(result.error, result.status);
        savedRow = result.value;
        return ok(id);
      },
      () => {
        const saved = savedRow;
        if (saved !== null) {
          this.rows = this.rows.map((r) => (r.categoryId === id ? saved : r));
          this.drafts = { ...this.drafts, [id]: policyDraftFromRow(saved) };
          this.savedId = id;
        }
        this.restoreFocus();
      },
    );
    this.savingId = "";
    if (this.formError !== "") {
      this.rowError = { ...this.rowError, [id]: this.formError };
      this.formError = "";
    }
  }

  private async saveAdd(): Promise<void> {
    const pricing = this.pricing;
    const draft = this.addDraft;
    const id = draft.category;
    if (pricing === null || this.saving) return;
    this.addError = "";
    if (id === "" || !this.uncovered().includes(id)) {
      this.addError = "Pick a category to add.";
      return;
    }
    const built = policySave(draft);
    if (!built.ok) {
      this.addError = built.error;
      return;
    }
    const write = built.value;
    this.savingId = ADD_ID;
    let savedRow: PolicyRow | null = null;
    await this.saveWithFloor(
      { kind: "category_policy", categoryId: id, write },
      async () => {
        const result = await pricing.upsertPolicy(id, write);
        if (!result.ok) return err(result.error, result.status);
        savedRow = result.value;
        return ok(id);
      },
      () => {
        const saved = savedRow;
        if (saved !== null) {
          this.rows = [...this.rows.filter((r) => r.categoryId !== id), saved];
          this.drafts = { ...this.drafts, [id]: policyDraftFromRow(saved) };
          this.savedId = id;
          this.addDraft = { category: "", target: "", floor: "", enforce: true };
        }
        this.restoreFocus();
      },
    );
    this.savingId = "";
    if (this.formError !== "") {
      this.addError = this.formError;
      this.formError = "";
    }
  }

  protected override updated(changed: Map<string, unknown>): void {
    super.updated(changed);
    const values: Record<string, string | boolean> = {};
    for (const row of this.rows) {
      const d = this.draftOf(row);
      values[`target-${row.categoryId}`] = d.target;
      values[`floor-${row.categoryId}`] = d.floor;
      values[`enforce-${row.categoryId}`] = d.enforce;
    }
    const add = this.addDraft;
    values["add-target"] = add.target;
    values["add-floor"] = add.floor;
    values["add-enforce"] = add.enforce;
    this.syncFields(values);
    const uncovered = this.uncovered();
    this.fillSelect("add-category", [["", "Pick a category"], ...uncovered.map((id): [string, string] => [id, this.nameOf(id)])], uncovered.includes(add.category) ? add.category : "");
    if ((changed.has("focusCategoryId") || changed.has("rows")) && this.focusCategoryId !== "") {
      this.renderRoot.querySelector(`tr[data-category-id="${this.focusCategoryId}"]`)?.scrollIntoView({ block: "center" });
    }
  }

  private renderAddRow(uncovered: string[]) {
    return html`
      <tr class="addrow" data-testid="pm-add-row">
        <td>
          <select class="cell" aria-label="Category to add" data-field="add-category" data-testid="pm-add-category"></select>
        </td>
        <td class="r">
          <span class="cellnum"
            ><input class="cell mono ${this.addError !== "" ? "bad" : ""}" inputmode="decimal" aria-label="Target margin for the new category" data-field="add-target" data-testid="pm-add-target" /><span class="pct">%</span></span
          >
        </td>
        <td class="r">
          <span class="cellnum"
            ><input class="cell mono ${this.addError !== "" ? "bad" : ""}" inputmode="decimal" aria-label="Margin floor for the new category" data-field="add-floor" data-testid="pm-add-floor" /><span class="pct">%</span></span
          >
        </td>
        <td>
          <label class="flagrow"><input type="checkbox" data-field="add-enforce" data-testid="pm-add-enforce" />Enforce floor</label>
        </td>
        <td class="r">
          <button type="button" class="primary" data-action="add-save" aria-label="Save the new category" data-testid="pm-add-save" ?disabled=${this.savingId === ADD_ID || this.floorPrompt !== null || uncovered.length === 0}>
            ${this.savingId === ADD_ID ? "Saving..." : "Save"}
          </button>
          ${this.addError !== "" ? html`<div class="rowerr" role="alert" data-testid="pm-add-error">${this.addError}</div>` : nothing}
        </td>
      </tr>
    `;
  }

  private renderRow(row: PolicyRow) {
    const id = row.categoryId;
    const d = this.draftOf(row);
    const label = this.nameOf(id);
    const err = this.rowError[id] ?? "";
    const dirty = policyDirty(row, d);
    const focused = this.focusCategoryId !== "" && this.focusCategoryId === id;
    return html`
      <tr class="${focused ? "focused" : ""}" data-category-id="${id}" data-testid="pm-row">
        <td>
          <div data-testid="pm-category">${label}</div>
          ${isKnownCategory(id) || this.categories.some((c) => c.id === id) ? nothing : nothing}
        </td>
        <td class="r">
          ${this.canEdit
            ? html`<span class="cellnum"
                ><input class="cell mono ${err !== "" ? "bad" : ""}" inputmode="decimal" aria-label="Target margin for ${label}" data-field="target-${id}" /><span class="pct">%</span></span
              >`
            : html`<span class="mono">${formatPct(row.targetBps)}</span>`}
        </td>
        <td class="r">
          ${this.canEdit
            ? html`<span class="cellnum"
                ><input class="cell mono ${err !== "" ? "bad" : ""}" inputmode="decimal" aria-label="Margin floor for ${label}" data-field="floor-${id}" /><span class="pct">%</span></span
              >`
            : html`<span class="mono">${formatPct(row.floorBps)}</span>`}
        </td>
        <td>
          ${this.canEdit
            ? html`<label class="flagrow"
                ><input type="checkbox" data-field="enforce-${id}" />Enforce floor
                ${d.enforce ? nothing : html`<span class="badge red">enforcement off</span>`}</label
              >`
            : row.enforcementOff
              ? html`<span class="badge red">enforcement off</span>`
              : html`<span class="badge">enforced</span>`}
        </td>
        <td class="r">
          ${this.canEdit
            ? html`<button type="button" class="${dirty ? "primary" : ""}" data-action="save" data-id="${id}" data-testid="pm-save" ?disabled=${this.savingId === id || this.floorPrompt !== null}>
                  ${this.savingId === id ? "Saving..." : "Save"}
                </button>
                ${this.savedId === id ? html`<span class="saved" data-testid="pm-saved"> saved</span>` : nothing}`
            : nothing}
          ${err !== "" ? html`<div class="rowerr" role="alert" data-testid="pm-error">${err}</div>` : nothing}
        </td>
      </tr>
    `;
  }

  override render() {
    const uncovered = this.uncovered();
    return html`
      <div class="panel" data-testid="pm-panel">
        <div class="head">
          <h2>Margin policy</h2>
          ${this.loading || this.loadError !== "" ? nothing : html`<span class="count" data-testid="pm-count">${this.rows.length} categories</span>`}
        </div>
        ${this.loading
          ? html`<p class="msg">Loading the margin policy...</p>`
          : this.loadError !== ""
            ? html`<p class="msg err" role="alert" data-testid="pm-load-error">${this.loadError}</p>`
            : html`
                <table>
                  <thead>
                    <tr>
                      <th>Category</th>
                      <th class="r">Target</th>
                      <th class="r">Floor</th>
                      <th>Enforcement</th>
                      <th class="r"></th>
                    </tr>
                  </thead>
                  <tbody>
                    ${this.rows.map((row) => this.renderRow(row))}
                    ${this.canEdit && uncovered.length > 0 ? this.renderAddRow(uncovered) : nothing}
                  </tbody>
                </table>
                ${this.canEdit && this.categoriesError !== "" ? html`<p class="msg" role="alert" data-testid="pm-categories-error">${this.categoriesError}</p>` : nothing}
                ${this.canEdit && this.categoriesError === "" && uncovered.length === 0 && this.rows.length > 0 ? html`<p class="msg" data-testid="pm-add-none">Every category in the catalogue has a margin policy.</p>` : nothing}
                ${this.rows.length === 0 ? html`<p class="msg">No category policy rows yet. Seed the catalogue categories first.</p>` : nothing}
              `}
      </div>
      ${this.renderFloorDialog()}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-pricing-margin": QdPricingMargin;
  }
}
