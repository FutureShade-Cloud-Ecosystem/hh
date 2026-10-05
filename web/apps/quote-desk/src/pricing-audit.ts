// Ported from hardscapeos_dibbits master 87d7941b app/src/pricing-audit.ts; adapted for HH.
//
// The pricing log tab: the newest changes to rules, margin policies, customer prices and
// quantity breaks, filterable by entity and action and by a date range, paged, with the
// actor and the change (before, then after). A row that is about a customer or a category
// links to that record's tab in this panel; a row about a product, quote or order has no
// destination here and says so rather than showing a dead link. The confirmations the
// floor warning logs (CP0-6) read here too, as "Floor warning confirmed".
//
// The server takes no date bounds, so the range runs over the window the server sent and
// the page says when that window came back at its cap. The pricing log is on the cost and
// margin list: a role off it is refused by the server, and the refusal is shown.
import { html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PricingTabBase } from "./pricing-tab-base.js";
import { pricingStyles } from "./pricing-styles.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { AUDIT_ACTIONS, ENTITY_TYPES, auditActionWord, auditFieldWord, categoryLabel, entityLabel } from "./_pricing-shared.js";
import { EMPTY_RANGE, PAGE_SIZE, auditWindowNote, bypassWord, drillTarget, entriesCountText, filterByDate, floorConfirmationText, floorProductIds, pageOf, type DateRange, type DrillTarget } from "./_pricing-audit.js";
import type { AuditRow } from "./_pricing-wire.js";

const AUDIT_FETCH_LIMIT = 200;

/** Fired when a log row's link is pressed: the host switches to the tab that owns the record. */
export const PRICING_DRILL_EVENT = "pricing-drill";

@customElement("qd-pricing-audit")
export class QdPricingAudit extends PricingTabBase {
  @state() private rows: AuditRow[] = [];
  @state() private loading = true;
  @state() private listError = "";
  @state() private fEntity = "";
  @state() private fAction = "";
  @state() private range: DateRange = { ...EMPTY_RANGE };
  @state() private page = 1;
  @state() private actors: Map<string, string> = new Map();
  /** Product names for the floor confirmations' ids, looked up as the rows arrive. */
  @state() private productNames: Map<string, string> = new Map();
  private namesAsked = new Set<string>();
  private loadSeq = 0;
  private actorsAsked = false;

  static styles = pricingStyles;

  override connectedCallback(): void {
    super.connectedCallback();
    this.reload();
  }

  reload(): void {
    void this.load();
  }

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("pricing") && this.pricing !== null && this.rows.length === 0 && this.listError === "") this.reload();
  }

  protected override closeTopDialog(): boolean {
    return false;
  }

  private async load(): Promise<void> {
    const pricing = this.pricing;
    const ctx = this.ctx; // the tab's own context: cancelled when the panel closes (the Pricing swap, a tab switch) or the person signs out
    if (pricing === null || ctx.stale) return;
    const seq = (this.loadSeq += 1);
    this.loading = true;
    this.listError = "";
    const result = await ctx.guarded(pricing.listAudit({ entityType: this.fEntity, action: this.fAction }, AUDIT_FETCH_LIMIT));
    if (result === undefined || seq !== this.loadSeq) return;
    this.loading = false;
    if (!result.ok) {
      this.rows = [];
      this.noteRefusal(result, false);
      this.listError = `The pricing log could not be read. ${spokenRefusal(result.error)}`;
      return;
    }
    this.rows = result.value;
    this.page = 1;
    void this.nameProducts(pricing, result.value);
    if (!this.actorsAsked) {
      this.actorsAsked = true;
      void ctx
        .guarded(pricing.actorNames())
        .then((names) => {
          if (names !== undefined && names.ok) this.actors = names.value;
        });
    }
  }

  /** The floor confirmations name products by id; ask for the names (best effort, a bounded number). */
  private async nameProducts(pricing: NonNullable<typeof this.pricing>, rows: readonly AuditRow[]): Promise<void> {
    const ctx = this.ctx;
    if (ctx.stale) return;
    const wanted = [...new Set(rows.filter((r) => r.fieldName === "floor_warning_confirmed").flatMap((r) => floorProductIds(r.newValue)))]
      .filter((id) => !this.namesAsked.has(id))
      .slice(0, 40);
    for (const id of wanted) this.namesAsked.add(id);
    // The lookups are the tab's context's: names for a panel that closed meanwhile paint nothing on it.
    const found = await ctx.guarded(Promise.all(wanted.map(async (id) => [id, await pricing.productLabel(id)] as const)));
    if (found === undefined) return;
    const next = new Map(this.productNames);
    for (const [id, label] of found) if (label.ok) next.set(id, label.value);
    if (next.size !== this.productNames.size) this.productNames = next;
  }

  protected override onClickAction(action: string, control: HTMLElement): void {
    if (action === "prev") this.page = Math.max(1, this.page - 1);
    if (action === "next") this.page += 1;
    if (action === "clear-dates") this.setRange({ ...EMPTY_RANGE });
    if (action === "drill") {
      const tab = control.getAttribute("data-tab");
      const id = control.getAttribute("data-id");
      const kind = control.getAttribute("data-kind");
      if (tab !== null && id !== null && kind !== null) {
        this.dispatchEvent(new CustomEvent(PRICING_DRILL_EVENT, { detail: { tab, id, kind }, bubbles: true, composed: true }));
      }
    }
  }

  protected override onFieldChange(field: string, control: HTMLElement): void {
    const value = (control as HTMLSelectElement | HTMLInputElement).value;
    if (field === "entity") {
      this.fEntity = value;
      this.reload();
    } else if (field === "action") {
      this.fAction = value;
      this.reload();
    } else if (field === "from") {
      this.setRange({ from: value });
    } else if (field === "to") {
      this.setRange({ to: value });
    }
  }

  private setRange(part: Partial<DateRange>): void {
    this.range = { ...this.range, ...part };
    this.page = 1;
  }

  private fmtTime(iso: string): string {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    const p = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
  }

  protected override updated(changed: Map<string, unknown>): void {
    super.updated(changed);
    this.fillSelect("entity", [["", "All entities"], ...ENTITY_TYPES.map((t) => [t, entityLabel(t)] as const)], this.fEntity);
    this.fillSelect("action", [["", "All actions"], ...AUDIT_ACTIONS.map((a) => [a, auditActionWord(a)] as const)], this.fAction);
    this.syncFields({ from: this.range.from, to: this.range.to });
  }

  private actorLabel(userId: string | undefined) {
    if (userId === undefined) return html`<span class="dim">system</span>`;
    return html`${this.actors.get(userId) ?? userId.slice(0, 8)}`;
  }

  private renderRecord(row: AuditRow) {
    const target: DrillTarget | null = drillTarget({ ...(row.refEntityType === undefined ? {} : { ref_entity_type: row.refEntityType }), ...(row.refEntityId === undefined ? {} : { ref_entity_id: row.refEntityId }) });
    if (target !== null) {
      return html`<button type="button" class="link" data-action="drill" data-tab="${target.tab}" data-id="${target.id}" data-kind="${target.kind}" data-testid="pa-link">${target.label} &rsaquo;</button>`;
    }
    if (row.refEntityType !== undefined) return html`<span class="no-ref" data-testid="pa-no-link">${row.refEntityType} record (not opened from here)</span>`;
    return html`<span class="no-ref" data-testid="pa-no-ref">no linked record</span>`;
  }

  private renderChange(row: AuditRow) {
    const why = row.action === "BYPASS" ? bypassWord(row.bypassReason) : "";
    const reason = why === "" ? nothing : html`<div class="dim small" data-testid="pa-bypass-reason">Bypassed because ${why}</div>`;
    if (row.oldValue === undefined && row.newValue === undefined) return why === "" ? html`<span class="dim">-</span>` : reason;
    if (row.oldValue === undefined) {
      const text = row.fieldName === "floor_warning_confirmed" && row.newValue !== undefined ? floorConfirmationText(row.newValue, this.productNames) : row.newValue;
      return html`<span class="delta amber" data-testid="pa-change">${text}</span>${reason}`;
    }
    return html`<span class="delta" data-testid="pa-change"
      ><span class="old">${row.oldValue}</span><span class="arrow">&rarr;</span><span class="amber">${row.newValue ?? "-"}</span></span
    >${reason}`;
  }

  private renderRow(row: AuditRow) {
    const badge = row.action === "BYPASS" || row.action === "DELETE" ? "badge red" : "badge";
    return html`
      <tr data-testid="pa-row">
        <td><span class="ts">${this.fmtTime(row.createdAt)}</span></td>
        <td>
          ${entityLabel(row.entityType)}
          <div class="mono dim small">${row.entityType === "category_policy" ? categoryLabel(row.entityId) : row.entityId.slice(0, 8)}</div>
        </td>
        <td>${this.renderRecord(row)}</td>
        <td><span class="${badge}">${auditActionWord(row.action)}</span></td>
        <td data-testid="pa-field">${row.fieldName !== undefined ? auditFieldWord(row.fieldName) : "-"}</td>
        <td>${this.renderChange(row)}</td>
        <td class="mono">${this.actorLabel(row.userId)}</td>
      </tr>
    `;
  }

  render() {
    // The range reads the wire's own created_at; each row rides with it.
    const filtered = filterByDate(this.rows.map((row) => ({ created_at: row.createdAt, row })), this.range).map((d) => d.row);
    const shown = pageOf(filtered, this.page, PAGE_SIZE);
    const note = auditWindowNote(this.rows.length, AUDIT_FETCH_LIMIT);
    return html`
      <div class="panel" data-testid="pa-panel">
        <div class="head">
          <h2>Pricing log</h2>
          ${this.loading || this.listError !== "" ? nothing : html`<span class="count" data-testid="pa-count">${entriesCountText(filtered.length, this.rows.length)}</span>`}
        </div>
        <div class="toolbar">
          <select aria-label="Filter by entity" data-field="entity" data-testid="pa-entity"></select>
          <select aria-label="Filter by action" data-field="action" data-testid="pa-action"></select>
          <label class="range">From <input type="date" aria-label="From date" data-field="from" data-testid="pa-from" /></label>
          <label class="range">To <input type="date" aria-label="To date" data-field="to" data-testid="pa-to" /></label>
          ${this.range.from !== "" || this.range.to !== "" ? html`<button type="button" data-action="clear-dates">Clear dates</button>` : nothing}
        </div>
        ${this.loading
          ? html`<p class="msg">Loading the pricing log...</p>`
          : this.listError !== ""
            ? html`<p class="msg err" role="alert" data-testid="pa-error">${this.listError}</p>`
            : filtered.length === 0
              ? html`<p class="msg" data-testid="pa-empty">No log entries match.</p>`
              : html`
                      <div
                        class="table-scroll"
                        tabindex="0"
                        role="region"
                        aria-label="Pricing log table, scrolls sideways"
                        data-testid="pa-table-scroll"
                      >
                    <table>
                      <thead>
                        <tr>
                          <th>When</th>
                          <th>Entity</th>
                          <th>Record</th>
                          <th>Action</th>
                          <th>Field</th>
                          <th>Change</th>
                          <th>Actor</th>
                        </tr>
                      </thead>
                      <tbody>
                        ${shown.rows.map((row) => this.renderRow(row))}
                      </tbody>
                    </table>
                  </div>
                  <div class="pager">
                    <button type="button" data-action="prev" ?disabled=${!shown.hasPrev}>&lsaquo; Prev</button>
                    <button type="button" data-action="next" ?disabled=${!shown.hasNext}>Next &rsaquo;</button>
                    <span data-testid="pa-pager">${shown.total === 0 ? "No entries" : `${shown.firstIndex}-${shown.lastIndex} of ${shown.total}`} · page ${shown.page} of ${shown.pages}</span>
                    ${note !== null ? html`<span class="window" data-testid="pa-window">${note}</span>` : nothing}
                  </div>
                `}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-pricing-audit": QdPricingAudit;
  }
}
