// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-surface-settings.ts;
// adapted for HH.
//
// The quote desk's screen settings, two halves on one page (the dealer's
// hybrid): the typed quote policy knobs, and the dealer's curation of
// columns, panels and actions per role profile. The page is drawn from the
// surface registry (quote-surface.ts is the form), so it holds no hard coded
// list beyond the typed policy fields the module declares.
//
// ROLE RULES ARE THE SERVER'S. The Sales manager writes a branch row it
// reaches, the General Manager the dealer wide default (CP0-5 answer A); the
// app holds neither rule. It offers both scopes, sends the write, and shows
// the server's own sentence when the write is refused: the refused scope is
// then locked (its fields and its save drop their fill and go grey) with that
// sentence in place, exactly as the server answered.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { drawIcon, iconForId } from "@hardscapehouse/pos-kit/src/ported/_icon-consumer.js";
import { listProducts } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import type { QuotePolicyWire } from "./_panel-wire.js";
import type { ResolvedItem, SurfaceItemKind, WireItem } from "./quote-surface.js";
import {
  buildItemsBody,
  chipChoices,
  CURATION_KINDS,
  curationDraft,
  kindHeading,
  moveDraftItem,
  policyBody,
  PROFILES,
  toggleChip,
  toggleDraftItem,
  validatePolicy,
  writeRefusalLine,
  type CurationProfile,
} from "./_quote-settings.js";

type Scope = "branch" | "dealer";

const NUMERIC_FIELDS = ["default_validity_days", "default_deposit_pct", "workspace_split_pct"] as const;
type NumericField = (typeof NUMERIC_FIELDS)[number];

@customElement("qd-surface-settings")
export class QdSurfaceSettings extends PanelBase {
  @state() private scope: Scope = "branch";
  @state() private policy: QuotePolicyWire | null = null;
  @state() private numDraft: Partial<Record<NumericField, string>> = {};
  @state() private chipsPick: string[] = [];
  @state() private chipVocabulary: string[] = [];
  @state() private profile: CurationProfile = "counter";
  @state() private roleItems: WireItem[] = [];
  @state() private drafts: Record<string, ResolvedItem[]> = {};
  /** A refused write locks its scope: the server's sentence, in place of an enabled save. */
  @state() private policyLocks: Partial<Record<Scope, string>> = {};
  @state() private itemsLock = "";

  static styles = [
    panelStyles,
    css`
      h3 {
        display: flex;
        align-items: center;
        gap: 8px;
        margin-bottom: 8px;
      }
      .box + .box {
        margin-top: 10px;
      }
      button.info {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 20px;
        height: 20px;
        padding: 0;
        border: none;
        background: transparent;
        color: var(--hos-text-dim, var(--hh-text-dim));
        cursor: help;
      }
      .knobs {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(min(var(--hos-form-col, 320px), 100%), 1fr));
        gap: var(--hos-space-5, 20px);
        align-items: end;
      }
      .knobs label.f {
        margin-top: 0;
      }
      .knobs input,
      .knobs select {
        width: 100%;
        box-sizing: border-box;
        padding: 6px 8px;
      }
      .span {
        grid-column: 1 / -1;
      }
      .check {
        display: inline-flex;
        gap: 6px;
        align-items: center;
        font-size: var(--qp-body);
        color: var(--hos-text, var(--hh-text));
        margin: 0;
      }
      .check input {
        width: auto;
      }
      .chiprow {
        display: flex;
        flex-wrap: wrap;
        gap: 6px;
      }
      .chip {
        font-size: var(--qp-caption);
        padding: 3px 10px;
        border-radius: 999px;
        background: transparent;
      }
      .chip.picked {
        border-color: var(--hos-stone-amber, var(--hh-accent));
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
        background: color-mix(in srgb, var(--hos-stone-amber, var(--hh-accent)) 12%, transparent);
      }
      .kindgrid {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(min(var(--hos-form-col, 320px), 100%), 1fr));
        gap: var(--hos-space-5, 20px);
        align-items: start;
        margin-top: var(--hos-space-3, 12px);
      }
      .kindgrid > table {
        min-width: 0;
      }
      .mv button {
        padding: 2px 7px;
      }
      td.show,
      th.show {
        text-align: center;
        width: 4.5rem;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("client") || changed.has("branchId")) this.reload();
  }

  reload(): void {
    void this.loadAll();
  }

  // ── reads ─────────────────────────────────────────────────────────────────

  private async loadAll(): Promise<void> {
    if (this.client === null) return;
    await Promise.all([this.loadPolicy(), this.loadItems(), this.loadVocabulary()]);
  }

  private scopeBranch(): string {
    return this.scope === "branch" ? this.branchId : "";
  }

  private async loadPolicy(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const result = await client.readPolicy(this.scopeBranch());
    if (!result.ok) {
      this.error = writeRefusalLine(result.status, result.error);
      return;
    }
    this.policy = result.value;
    this.chipsPick = [...result.value.quick_chip_set];
    this.numDraft = {};
  }

  private async loadItems(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const result = await client.surfaceConfig(this.branchId, this.profile);
    if (!result.ok) {
      this.error = writeRefusalLine(result.status, result.error);
      return;
    }
    this.roleItems = result.value.roleItems;
    this.drafts = {};
  }

  /** The category vocabulary, from the same catalogue read the grid's chips run on. */
  private async loadVocabulary(): Promise<void> {
    try {
      this.chipVocabulary = chipChoices(await listProducts());
    } catch {
      this.chipVocabulary = []; // the picker degrades to showing only the current picks
    }
  }

  // ── the policy half ───────────────────────────────────────────────────────

  private edit(patch: Partial<QuotePolicyWire>): void {
    if (this.policy !== null) this.policy = { ...this.policy, ...patch };
  }

  private async savePolicy(): Promise<void> {
    const client = this.client;
    const policy = this.policy;
    if (client === null || policy === null || this.busy) return;
    const candidate: QuotePolicyWire = { ...policy, quick_chip_set: this.chipsPick };
    const bad = validatePolicy(candidate);
    if (bad !== null) {
      this.error = bad;
      this.notice = "";
      return;
    }
    this.busy = true;
    this.error = "";
    this.notice = "";
    const scope = this.scope;
    const result = await client.putPolicy(policyBody(candidate, this.scopeBranch()));
    this.busy = false;
    if (!result.ok) {
      const line = writeRefusalLine(result.status, result.error);
      if (result.status === 403) this.policyLocks = { ...this.policyLocks, [scope]: line };
      else this.error = line;
      return;
    }
    this.policy = result.value;
    this.chipsPick = [...result.value.quick_chip_set];
    this.numDraft = {};
    this.notice = "Quote settings saved. They are live now.";
    this.announceChanged();
  }

  // ── the curation half ─────────────────────────────────────────────────────

  private draftFor(kind: SurfaceItemKind): ResolvedItem[] {
    return this.drafts[kind] ?? curationDraft(kind, this.roleItems);
  }

  private async saveItems(): Promise<void> {
    const client = this.client;
    if (client === null || this.busy) return;
    this.busy = true;
    this.error = "";
    this.notice = "";
    const drafts: Record<string, ResolvedItem[]> = {};
    for (const kind of CURATION_KINDS) drafts[kind] = this.draftFor(kind);
    const body = buildItemsBody(this.profile, drafts);
    const result = await client.putRoleItems(body.profile, body.items);
    this.busy = false;
    if (!result.ok) {
      const line = writeRefusalLine(result.status, result.error);
      if (result.status === 403) this.itemsLock = line;
      else this.error = line;
      return;
    }
    this.notice = `Screen layout saved for the ${this.profile} profile. It is live now.`;
    await this.loadItems();
    this.announceChanged();
  }

  // ── delegation ────────────────────────────────────────────────────────────

  protected override onClickAction(action: string, control: HTMLElement): void {
    const kind = (control.getAttribute("data-kind") ?? "") as SurfaceItemKind;
    const id = control.getAttribute("data-id") ?? "";
    if (action === "save-policy") void this.savePolicy();
    if (action === "save-items") void this.saveItems();
    if (action === "chip") this.chipsPick = toggleChip(this.chipsPick, control.getAttribute("data-category") ?? "");
    if (action === "item-up") this.drafts = { ...this.drafts, [kind]: moveDraftItem(this.draftFor(kind), id, -1) };
    if (action === "item-down") this.drafts = { ...this.drafts, [kind]: moveDraftItem(this.draftFor(kind), id, 1) };
  }

  protected override onFieldInput(field: string, control: HTMLElement): void {
    const value = (control as HTMLInputElement).value;
    const numeric = NUMERIC_FIELDS.find((n) => n === field);
    if (numeric !== undefined) {
      this.numDraft = { ...this.numDraft, [numeric]: value };
      this.edit({ [numeric]: value.trim() === "" ? Number.NaN : Number(value) } as Partial<QuotePolicyWire>);
    }
  }

  protected override onFieldChange(field: string, control: HTMLElement): void {
    const input = control as HTMLInputElement;
    const select = control as HTMLSelectElement;
    switch (field) {
      case "scope":
        this.scope = select.value === "dealer" ? "dealer" : "branch";
        this.error = "";
        this.notice = "";
        void this.loadPolicy();
        break;
      case "cost_visibility":
        this.edit({ cost_visibility: select.value as QuotePolicyWire["cost_visibility"] });
        break;
      case "workspace_arrangement":
        this.edit({ workspace_arrangement: select.value as QuotePolicyWire["workspace_arrangement"] });
        break;
      case "line_discounts_enabled":
      case "include_product_images":
      case "send_email_enabled":
      case "send_sms_enabled":
        this.edit({ [field]: input.checked } as Partial<QuotePolicyWire>);
        break;
      case "profile":
        this.profile = select.value as CurationProfile;
        this.itemsLock = "";
        void this.loadItems();
        break;
      case "item-show":
        this.drafts = {
          ...this.drafts,
          [input.getAttribute("data-kind") ?? ""]: toggleDraftItem(this.draftFor((input.getAttribute("data-kind") ?? "") as SurfaceItemKind), input.getAttribute("data-id") ?? ""),
        };
        break;
    }
  }

  // ── the uncontrolled fields ───────────────────────────────────────────────

  protected override updated(): void {
    const p = this.policy;
    if (p !== null) {
      for (const field of NUMERIC_FIELDS) this.syncValue(`[data-field="${field}"]`, this.numDraft[field] ?? String(p[field]));
      this.syncValue('[data-field="cost_visibility"]', p.cost_visibility);
      this.syncValue('[data-field="workspace_arrangement"]', p.workspace_arrangement);
      for (const field of ["line_discounts_enabled", "include_product_images", "send_email_enabled", "send_sms_enabled"] as const) {
        const box = this.renderRoot.querySelector<HTMLInputElement>(`[data-field="${field}"]`);
        if (box !== null && box.checked !== p[field]) box.checked = p[field];
      }
    }
    this.syncValue('[data-field="scope"]', this.scope);
    this.syncValue('[data-field="profile"]', this.profile);
    for (const box of this.renderRoot.querySelectorAll<HTMLInputElement>('[data-field="item-show"]')) {
      const kind = (box.getAttribute("data-kind") ?? "") as SurfaceItemKind;
      const row = this.draftFor(kind).find((c) => c.id === box.getAttribute("data-id"));
      const want = row !== undefined && (row.visible || row.min === true);
      if (box.checked !== want) box.checked = want;
    }
  }

  // ── render ────────────────────────────────────────────────────────────────

  private renderPolicy() {
    const p = this.policy;
    if (p === null) return html`<div class="box"><p class="dim">Loading the quote settings...</p></div>`;
    const lock = this.policyLocks[this.scope] ?? "";
    const locked = lock !== "";
    return html`<div class="box" data-testid="qs-policy">
      <h3>
        Quote policy
        <button
          class="info"
          type="button"
          aria-label="About quote policy"
          title="Applies the moment you save. Cost visibility is enforced by the server: a role it refuses never receives cost data."
        >
          ${drawIcon(iconForId("pencil:info"), 19)}
        </button>
      </h3>
      <div class="row" style="margin-top:0">
        <label class="f" for="qs-scope" style="margin:0">Applies to</label>
        <select id="qs-scope" data-field="scope" data-testid="qs-scope" style="width:auto">
          <option value="branch">This branch</option>
          <option value="dealer">Dealer wide default</option>
        </select>
        <span class="picked">The server decides who may save which: a branch manager sets its branch, the General Manager the dealer default.</span>
      </div>
      <div class="knobs" style="margin-top:12px">
        <div>
          <label class="f" for="qs-vis">Cost / margin visibility</label>
          <select id="qs-vis" data-field="cost_visibility" data-testid="qs-cost-visibility" ?disabled=${locked}>
            <option value="MANAGER_UP">Managers and up</option>
            <option value="ALL_STAFF">All staff</option>
            <option value="HIDDEN">Hidden for everyone</option>
          </select>
        </div>
        <div>
          <label class="f" for="qs-days">Default validity (days)</label>
          <input id="qs-days" data-field="default_validity_days" data-testid="qs-validity-days" inputmode="numeric" ?disabled=${locked} />
        </div>
        <div>
          <label class="f" for="qs-dep">Default deposit %</label>
          <input id="qs-dep" data-field="default_deposit_pct" inputmode="numeric" ?disabled=${locked} />
        </div>
        <div class="span">
          <label class="f" id="qs-chips-label">Quick chips</label>
          <div class="chiprow" data-testid="qs-chips" role="group" aria-labelledby="qs-chips-label">
            ${this.chipsPick.map(
              (c, i) => html`<button type="button" class="chip picked" data-action="chip" data-category=${c} ?disabled=${locked}>
                ${i + 1} · ${c} <span aria-hidden="true">${drawIcon(iconForId("pencil:close-m"), 12)}</span>
              </button>`,
            )}
            ${this.chipVocabulary
              .filter((c) => !this.chipsPick.includes(c))
              .map((c) => html`<button type="button" class="chip" data-action="chip" data-category=${c} ?disabled=${locked}>${c}</button>`)}
            ${this.chipsPick.length === 0 ? html`<span class="picked" style="align-self:center">None picked: top sellers show.</span>` : nothing}
          </div>
        </div>
        <label class="check"><input type="checkbox" data-field="line_discounts_enabled" data-testid="qs-line-discounts" ?disabled=${locked} /> Per-line discounts</label>
        <label class="check"><input type="checkbox" data-field="include_product_images" ?disabled=${locked} /> Product images on sent quotes (default)</label>
        <label class="check"><input type="checkbox" data-field="send_email_enabled" data-testid="qs-send-email" ?disabled=${locked} /> Send by email</label>
        <label class="check"><input type="checkbox" data-field="send_sms_enabled" data-testid="qs-send-sms" ?disabled=${locked} /> Send by text (SMS)</label>
        <div>
          <label class="f" for="qs-split">Default panel split %</label>
          <input id="qs-split" data-field="workspace_split_pct" inputmode="numeric" ?disabled=${locked} />
        </div>
        <div>
          <label class="f" for="qs-arr">Default arrangement</label>
          <select id="qs-arr" data-field="workspace_arrangement" ?disabled=${locked}>
            <option value="PRIMARY_LEFT">Products left</option>
            <option value="PRIMARY_RIGHT">Products right</option>
          </select>
        </div>
      </div>
      ${locked ? html`<p class="lock" role="status" data-testid="qs-policy-lock">${lock}</p>` : nothing}
      <div class="row">
        <button type="button" class="primary" data-action="save-policy" data-testid="qs-policy-save" ?disabled=${this.busy || locked}>Save policy</button>
      </div>
    </div>`;
  }

  private renderKindTable(kind: SurfaceItemKind) {
    const items = this.draftFor(kind);
    if (items.length === 0) return nothing;
    const heading = kindHeading(kind);
    const locked = this.itemsLock !== "";
    return html`<table aria-label=${heading}>
      <thead>
        <tr>
          <th>${heading}</th>
          <th class="show">Shown</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        ${items.map(
          (c, i) => html`<tr>
            <td>
              ${c.label}
              ${c.cls === "cost" ? html` <span class="tag">cost: server gated</span>` : nothing}
              ${c.min === true ? html` <span class="tag">always on</span>` : nothing}
            </td>
            <td class="show">
              <input type="checkbox" data-field="item-show" data-kind=${kind} data-id=${c.id} aria-label="Show ${c.label}" ?disabled=${c.min === true || locked} />
            </td>
            <td class="mv">
              <button type="button" data-action="item-up" data-kind=${kind} data-id=${c.id} aria-label="Move up" ?disabled=${i === 0 || locked}>
                <span aria-hidden="true">${drawIcon(iconForId("lucide:chevron-up"), 12)}</span>
              </button>
              <button type="button" data-action="item-down" data-kind=${kind} data-id=${c.id} aria-label="Move down" ?disabled=${i === items.length - 1 || locked}>
                <span aria-hidden="true">${drawIcon(iconForId("pencil:bottom"), 12)}</span>
              </button>
            </td>
          </tr>`,
        )}
      </tbody>
    </table>`;
  }

  private renderItems() {
    const locked = this.itemsLock !== "";
    return html`<div class="box" data-testid="qs-items">
      <h3>
        Screen layout (per role profile)
        <button
          class="info"
          type="button"
          aria-label="About screen layout"
          title="Choose which panels, columns and actions each role sees, and their order. Cost columns stay empty for roles the policy refuses."
        >
          ${drawIcon(iconForId("pencil:info"), 19)}
        </button>
      </h3>
      <div class="row" style="margin-top:0">
        <label class="f" for="qs-profile" style="margin:0">Profile</label>
        <select id="qs-profile" data-field="profile" data-testid="qs-profile" style="width:auto" ?disabled=${locked}>
          ${PROFILES.map((p) => html`<option>${p}</option>`)}
        </select>
      </div>
      <div class="kindgrid">${CURATION_KINDS.map((kind) => this.renderKindTable(kind))}</div>
      ${locked ? html`<p class="lock" role="status" data-testid="qs-items-lock">${this.itemsLock}</p>` : nothing}
      <div class="row">
        <button type="button" class="primary" data-action="save-items" data-testid="qs-items-save" ?disabled=${this.busy || locked}>
          Save layout for ${this.profile}
        </button>
      </div>
    </div>`;
  }

  override render() {
    return html`
      ${this.renderPolicy()} ${this.renderItems()}
      ${this.error !== "" ? html`<p class="err" role="alert" data-testid="qs-error">${this.error}</p>` : nothing}
      ${this.notice !== "" ? html`<p class="ok" role="status" data-testid="qs-notice">${this.notice}</p>` : nothing}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-surface-settings": QdSurfaceSettings;
  }
}
