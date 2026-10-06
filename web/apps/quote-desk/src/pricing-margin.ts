// Ported from hardscapeos_dibbits master 87d7941b app/src/margin-grid.ts; adapted for HH.
//
// The category margin policy tab: one row per category with an inline target and floor
// (percent, mono), an Enforce floor box and a Save per row, written through the generated
// client. The Save arms (gold) only when its row differs from what the server holds. A
// save runs the editors' floor warning first (CP0-6): the products the new floor would put
// under it are shown and the editor confirms. One row's save never touches another row's
// half typed edit.
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
import type { PolicyRow } from "./_pricing-wire.js";

@customElement("qd-pricing-margin")
export class QdPricingMargin extends PricingTabBase {
  /** The category an audit link pointed at: its row is marked and scrolled into view. */
  @property({ attribute: false }) focusCategoryId = "";

  @state() private rows: PolicyRow[] = [];
  @state() private drafts: Record<string, PolicyDraft> = {};
  @state() private loading = true;
  @state() private loadError = "";
  @state() private savingId = "";
  @state() private savedId = "";
  @state() private rowError: Record<string, string> = {};
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
  }

  protected override onFieldInput(field: string, control: HTMLElement): void {
    const at = field.indexOf("-");
    if (at < 0) return;
    const kind = field.slice(0, at);
    const id = field.slice(at + 1);
    const input = control as HTMLInputElement;
    if (kind === "target") this.setDraft(id, { target: input.value });
    if (kind === "floor") this.setDraft(id, { floor: input.value });
  }

  protected override onFieldChange(field: string, control: HTMLElement): void {
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

  protected override updated(changed: Map<string, unknown>): void {
    super.updated(changed);
    const values: Record<string, string | boolean> = {};
    for (const row of this.rows) {
      const d = this.draftOf(row);
      values[`target-${row.categoryId}`] = d.target;
      values[`floor-${row.categoryId}`] = d.floor;
      values[`enforce-${row.categoryId}`] = d.enforce;
    }
    this.syncFields(values);
    if ((changed.has("focusCategoryId") || changed.has("rows")) && this.focusCategoryId !== "") {
      this.renderRoot.querySelector(`tr[data-category-id="${this.focusCategoryId}"]`)?.scrollIntoView({ block: "center" });
    }
  }

  private renderRow(row: PolicyRow) {
    const id = row.categoryId;
    const d = this.draftOf(row);
    const label = categoryLabel(id);
    const err = this.rowError[id] ?? "";
    const dirty = policyDirty(row, d);
    const focused = this.focusCategoryId !== "" && this.focusCategoryId === id;
    return html`
      <tr class="${focused ? "focused" : ""}" data-category-id="${id}" data-testid="pm-row">
        <td>
          <div data-testid="pm-category">${label}</div>
          ${isKnownCategory(id) ? nothing : html`<div class="mono dim small">${id.slice(0, 8)}</div>`}
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
                  </tbody>
                </table>
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
