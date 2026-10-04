// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-builder.ts;
// adapted for HH.
//
// The Quote Desk's list and shell: it lists the quotes the caller can reach
// (a Work caller's list defaults to its own quotes, the server's own rule),
// filters by status, opens one, creates a new one, and hosts the work area
// above the list (the work area IS the screen; the list sits under it). The
// edit panel, the history and the settings seams (the second coder's panels)
// and the assist, follow-ups, hold expiry, availability and approvals seams
// (the third coder's) are hosted under the detail disclosure as named empty
// seams, and the quote-changed fan-out reaches them all through reload().
//
// The binding discipline is the app's own: the templates carry no property or
// event bindings; the host delegates by data-action and data-field, and the
// work area and the picker receive their facts through the wiring pass.
import { LitElement, css, html, nothing } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import "./quote-workarea.js";
import "./panel-seams.js";
import "./edit-panel.js";
import "./history-panel.js";
import "./settings-panel.js";
import "./assist-panel.js";
import "./followups-panel.js";
import "./hold-expiry-panel.js";
import "./availability-panel.js";
import "./approvals-panel.js";
import "./floor-refusal-dialog.js";
import {
  SEAM_APPROVALS,
  SEAM_ASSIST,
  SEAM_AVAILABILITY,
  SEAM_EDIT_PANEL,
  SEAM_FOLLOWUPS,
  SEAM_HISTORY,
  SEAM_HOLD_EXPIRY,
  SEAM_SETTINGS,
} from "./panel-seams.js";
import { money } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";
import "./customer-picker.js";
import type { PickerCustomer } from "@hardscapehouse/pos-kit/src/ported/_customer-picker.js";
import {
  expiryLabel,
  quoteErrorMessage,
  quoteStatusWord,
} from "./_quote-edit.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { OPEN_SETTINGS_EVENT } from "./panel-seams.js";
import type { QdClient, QuoteRow } from "./qd-client.js";

interface BranchRow {
  id: string;
  code: string;
  name: string;
  isDefault: boolean;
}

@customElement("qd-quote-builder")
export class QdQuoteBuilder extends LitElement {
  /** The host wires the validated client before the first render. */
  @property({ attribute: false }) client: QdClient | null = null;

  @state() private rows: QuoteRow[] = [];
  @state() private selected = "";
  @state() private error = "";
  @state() private busy = false;
  @state() private statusFilter = "";
  @state() private branches: BranchRow[] = [];
  @state() private newBranch = "";
  @state() private newExpiry = "";
  @state() private detailOpen = false;
  /** The picked account for a new quote, from the picker (never a typed id). */
  private newCustomer: PickerCustomer | null = null;

  static styles = css`
    :host {
      display: block;
      padding: 14px;
      font-family: var(--hos-font-ui, var(--hh-font-ui));
      color: var(--hos-text, var(--hh-text));
    }
    header {
      display: flex;
      align-items: baseline;
      gap: 12px;
      flex-wrap: wrap;
      margin-bottom: 12px;
    }
    h2 {
      margin: 0;
      font-size: 1.05rem;
    }
    .box {
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 8px;
      padding: 12px 14px;
      background: var(--hos-slate-warm, var(--hh-card));
    }
    h3 {
      margin: 0 0 10px;
      font-size: 0.95rem;
    }
    ul {
      list-style: none;
      margin: 0;
      padding: 0;
      display: flex;
      flex-direction: column;
      gap: 6px;
      max-height: 60vh;
      overflow-y: auto;
    }
    li {
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 6px;
      padding: 6px 8px;
      cursor: pointer;
      font-size: 0.83rem;
    }
    li[aria-selected="true"] {
      border-color: var(--hos-stone-amber, var(--hh-accent));
      background: var(--hos-line, var(--hh-line));
    }
    .no {
      font-weight: 600;
    }
    .meta {
      color: var(--hos-text-dim, var(--hh-text-dim));
      font-size: 0.76rem;
      margin-top: 2px;
    }
    .stack {
      display: flex;
      flex-direction: column;
      gap: 12px;
    }
    .row {
      display: flex;
      gap: 8px;
      align-items: flex-end;
      flex-wrap: wrap;
    }
    button:focus-visible,
    input:focus-visible,
    select:focus-visible {
      outline: 2px solid var(--hos-stone-amber-text, var(--hh-accent-text));
      outline-offset: 2px;
    }
    input,
    select {
      font: inherit;
      font-size: 0.83rem;
      padding: 4px 6px;
      border: 1px solid var(--hos-control-line, var(--hh-control-line, var(--hh-line)));
      border-radius: 4px;
      background: var(--hos-deep-earth, var(--hh-page));
      color: var(--hos-text, var(--hh-text));
    }
    button {
      font: inherit;
      font-size: 0.83rem;
      padding: 4px 10px;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 5px;
      background: var(--hos-slate-warm, var(--hh-card));
      color: var(--hos-text, var(--hh-text));
      cursor: pointer;
    }
    button:disabled {
      opacity: 0.5;
      cursor: default;
    }
    .err {
      color: var(--hos-safety-red, var(--hh-negative));
      font-size: 0.82rem;
    }
    .empty {
      color: var(--hos-text-dim, var(--hh-text-dim));
      font-size: 0.83rem;
    }
    fieldset {
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 6px;
      margin: 10px 0 0;
      padding: 8px 10px;
    }
    legend {
      font-size: 0.78rem;
      color: var(--hos-text-dim, var(--hh-text-dim));
      padding: 0 4px;
    }
    /* The detail disclosure's seam rows: nothing shows today, so the row
       keeps its own height at zero until a panel lands in the seam. */
    .seams qd-panel-seam {
      display: block;
    }
  `;

  override connectedCallback(): void {
    super.connectedCallback();
    this.startLoads();
    // The expiry defaults to thirty days out so nobody types a date to make
    // the form move.
    if (this.newExpiry === "") {
      const d = new Date(Date.now() + 30 * 86400000);
      this.newExpiry = d.toISOString().slice(0, 10);
    }
    this.renderRoot.addEventListener("click", this.onActionClick as EventListener);
    this.renderRoot.addEventListener("change", this.onFieldChange as EventListener);
    this.renderRoot.addEventListener("quote-changed", this.onQuoteChanged as EventListener);
    this.renderRoot.addEventListener("quote-open", this.onQuoteOpen as EventListener);
    window.addEventListener(OPEN_SETTINGS_EVENT, this.onOpenSettings);
    this.renderRoot.addEventListener("customer-picked", this.onCustomerPicked as EventListener);
    this.renderRoot.addEventListener("customer-cleared", this.onCustomerCleared as EventListener);
  }

  protected override willUpdate(changed: Map<string, unknown>): void {
    // The host wires the client after this element mounts; the loads start
    // the moment it arrives (the entry page's cards never reach here).
    if (changed.has("client") && this.client !== null) this.startLoads();
  }

  private startLoads(): void {
    if (this.client === null) return;
    void this.loadBranches();
    void this.loadList();
  }

  override disconnectedCallback(): void {
    super.disconnectedCallback();
    this.renderRoot.removeEventListener("click", this.onActionClick as EventListener);
    this.renderRoot.removeEventListener("change", this.onFieldChange as EventListener);
    this.renderRoot.removeEventListener("quote-changed", this.onQuoteChanged as EventListener);
    this.renderRoot.removeEventListener("quote-open", this.onQuoteOpen as EventListener);
    window.removeEventListener(OPEN_SETTINGS_EVENT, this.onOpenSettings);
    this.renderRoot.removeEventListener("customer-picked", this.onCustomerPicked as EventListener);
    this.renderRoot.removeEventListener("customer-cleared", this.onCustomerCleared as EventListener);
  }

  protected override updated(): void {
    // The wiring pass: the work area and the picker receive their facts.
    const area = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("qd-quote-workarea");
    if (area !== null) {
      area.client = this.client;
      area.quoteId = this.selected;
      area.branchId = this.selectedBranch() || this.newBranch;
      area.branchName = this.branchDisplayName();
    }
    const picker = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("hos-customer-picker");
    if (picker !== null) {
      picker.branchId = this.newBranch;
      if (picker.labelText === undefined || picker.labelText === "") picker.labelText = "Customer";
    }
    // The seam rows and the panels mounted in their place receive the open
    // quote the same way: the edit panel, the change history and the settings
    // also take the validated client.
    for (const seam of this.renderRoot.querySelectorAll<HTMLElement & Record<string, unknown>>(
      "qd-panel-seam, qd-quote-edit, qd-quote-history, qd-quote-settings, qd-quote-assist, qd-quote-followups, qd-quote-hold-expiry, qd-quote-availability, qd-approvals, qd-floor-refusal",
    )) {
      seam.quoteId = this.selected;
      seam.branchId = this.selectedBranch() || this.newBranch;
      if (seam.tagName !== "QD-PANEL-SEAM") seam.client = this.client;
    }
    // The new quote form's uncontrolled fields.
    const branchSelect = this.renderRoot.querySelector<HTMLSelectElement>('[data-field="new-branch"]');
    if (branchSelect !== null && this.branches.length > 0) {
      const shape = this.branches.map((b) => `${b.id}:${b.code}:${b.name}`).join("|");
      if ((branchSelect as HTMLElement & { lastShape?: string }).lastShape !== shape) {
        (branchSelect as HTMLElement & { lastShape?: string }).lastShape = shape;
        branchSelect.textContent = "";
        for (const b of this.branches) {
          const option = document.createElement("option");
          option.value = b.id;
          option.textContent = `${b.code}: ${b.name}`;
          branchSelect.append(option);
        }
      }
      if (branchSelect.value !== this.newBranch) branchSelect.value = this.newBranch;
    }
    const statusSelect = this.renderRoot.querySelector<HTMLSelectElement>('[data-field="status-filter"]');
    if (statusSelect !== null && statusSelect.value !== this.statusFilter) statusSelect.value = this.statusFilter;
    const expiry = this.renderRoot.querySelector<HTMLInputElement>('[data-field="new-expiry"]');
    if (expiry !== null && expiry.value !== this.newExpiry) expiry.value = this.newExpiry;
  }

  // ── loading ────────────────────────────────────────────────────────────────

  private async loadList(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    this.error = "";
    const result = await client.listQuotes(this.statusFilter);
    if (!result.ok) {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "list quotes"));
      return;
    }
    this.rows = result.value;
  }

  private async loadBranches(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const result = await client.resolveBranch();
    if (!result.ok) return; // the select degrades to empty; the create still validates
    this.branches = [{ id: result.value.id, code: result.value.code, name: result.value.name, isDefault: true }];
    if (this.newBranch === "") this.newBranch = result.value.id;
  }

  private async createQuote(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    if (this.newCustomer === null || this.newBranch.trim() === "") {
      this.error = this.newCustomer === null ? "Choose a customer for the quote." : "Choose a branch for the quote.";
      return;
    }
    this.busy = true;
    this.error = "";
    const result = await client.createQuote(this.newCustomer.id, this.newBranch.trim(), this.newExpiry.trim());
    this.busy = false;
    if (!result.ok) {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "create a quote"));
      return;
    }
    this.newCustomer = null;
    this.newExpiry = new Date(Date.now() + 30 * 86400000).toISOString().slice(0, 10); // stays pre-filled for the next quote
    await this.loadList();
    this.selected = result.value;
  }

  // ── the fan-out and the open events ────────────────────────────────────────

  /** An edit anywhere reaches every mounted panel and the list itself. */
  private readonly onQuoteChanged = (): void => {
    this.reloadDetailPanels();
    void this.loadList();
  };

  private readonly onQuoteOpen = (event: CustomEvent<{ quoteId: string }>): void => {
    this.selected = event.detail.quoteId;
    void this.loadList();
  };

  private readonly onCustomerPicked = (event: CustomEvent<PickerCustomer>): void => {
    // The work area's own compose picker bubbles the same event; the form
    // only takes the pick that came from its own fieldset.
    if (!event.composedPath().some((el) => el instanceof HTMLFieldSetElement)) return;
    event.stopPropagation();
    this.newCustomer = event.detail;
    this.error = "";
  };

  private readonly onCustomerCleared = (): void => {
    this.newCustomer = null;
  };

  /**
   * The work area's Screen settings button asks for the settings region: the
   * detail disclosure opens and the settings panel opens its screen settings.
   */
  private readonly onOpenSettings = (): void => {
    const details = this.renderRoot.querySelector<HTMLDetailsElement>('[data-testid="qb-detail"]');
    if (details !== null) details.open = true;
    this.renderRoot.querySelector<HTMLElement & { openScreenSettings?: () => void }>("qd-quote-settings")?.openScreenSettings?.();
  };

  private reloadDetailPanels(): void {
    this.renderRoot
      ?.querySelectorAll<HTMLElement & { reload?: () => void }>(
        "qd-quote-workarea, qd-panel-seam, qd-quote-edit, qd-quote-history, qd-quote-settings, qd-quote-assist, qd-quote-followups, qd-quote-hold-expiry, qd-quote-availability, qd-approvals",
      )
      .forEach((el) => el.reload?.());
  }

  // ── the host delegation ────────────────────────────────────────────────────

  private readonly onActionClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const action = control.getAttribute("data-action");
    if (action === "open") {
      this.selected = control.getAttribute("data-quote") ?? "";
    }
    if (action === "refresh") {
      void this.loadList();
    }
    if (action === "create") {
      void this.createQuote();
    }
  };

  private readonly onFieldChange = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-field"));
    if (control === undefined) return;
    const field = control.getAttribute("data-field") ?? "";
    if (control instanceof HTMLSelectElement && field === "status-filter") {
      this.statusFilter = control.value;
      void this.loadList();
    }
    if (control instanceof HTMLSelectElement && field === "new-branch") {
      this.newBranch = control.value;
    }
    if (control instanceof HTMLInputElement && field === "new-expiry") {
      this.newExpiry = control.value;
    }
  };

  // ── the branch facts ───────────────────────────────────────────────────────

  /**
   * The open quote's branch, read from the list row rather than from the new
   * quote form: the form's branch is what the NEXT quote would be created
   * in, and using it here would scope the catalogue to the wrong yard the
   * moment the create dropdown changed.
   */
  private selectedBranch(): string {
    return this.rows.find((r) => r.id === this.selected)?.branchId ?? "";
  }

  private branchDisplayName(): string {
    const id = this.selectedBranch() === "" ? this.newBranch : this.selectedBranch();
    const b = this.branches.find((x) => x.id === id);
    return b !== undefined ? b.name : "";
  }

  // ── render ─────────────────────────────────────────────────────────────────

  private renderList() {
    return html`
      <div class="box">
        <h3>Quotes</h3>
        <div class="row">
          <select aria-label="Filter by status" data-field="status-filter" data-testid="qb-list-filter">
            <option value="">All statuses</option>
            <option value="DRAFT">Draft</option>
            <option value="SENT">Sent</option>
            <option value="ACCEPTED">Accepted</option>
            <option value="CONVERTED">Converted</option>
            <option value="EXPIRED">Expired</option>
            <option value="LOST">Lost</option>
          </select>
          <button type="button" data-action="refresh" ?disabled=${this.busy}>Refresh</button>
        </div>
        ${this.rows.length === 0
          ? html`<p class="empty" style="margin-top: 10px" data-testid="qb-list">No quotes.</p>`
          : html`<ul style="margin-top: 10px" data-testid="qb-list">
              ${this.rows.map(
                (r) => html`<li
                  data-action="open"
                  data-quote=${r.id}
                  data-testid="qb-list-row"
                  aria-selected=${r.id === this.selected ? "true" : "false"}
                >
                  <div class="no">${r.quoteNo} · ${quoteStatusWord(r.status)}</div>
                  <div class="meta">
                    ${r.customerName} · ${money(r.totalCents)} · ${expiryLabel(r)}
                    ${r.ownerName !== null ? html` · ${r.ownerName}` : nothing}
                  </div>
                </li>`,
              )}
            </ul>`}
        <fieldset>
          <legend>New quote</legend>
          <div class="row">
            <hos-customer-picker></hos-customer-picker>
            <select aria-label="Branch" data-field="new-branch"></select>
            <input type="date" aria-label="Expiry date" data-field="new-expiry" />
            <button type="button" data-action="create" ?disabled=${this.busy}>Create</button>
          </div>
        </fieldset>
      </div>
    `;
  }

  /** The named empty seams the second and third coders fill. */
  private renderSeam(name: string, label: string) {
    return html`<qd-panel-seam data-seam=${name} data-seam-label=${label}></qd-panel-seam>`;
  }

  override render() {
    return html`
      ${this.error !== "" ? html`<header><span class="err" role="alert">${this.error}</span></header>` : nothing}
      <qd-quote-workarea></qd-quote-workarea>
      <qd-floor-refusal></qd-floor-refusal>
      <div style="margin-top: 12px">${this.renderList()}</div>
      <details class="box" style="margin-top: 12px" data-testid="qb-detail">
        <summary style="cursor: pointer">Quote detail &amp; admin</summary>
        <div class="stack seams" style="margin-top: 10px">
          ${this.selected !== ""
            ? html`
                <qd-quote-edit data-seam=${SEAM_EDIT_PANEL}></qd-quote-edit>
                <qd-quote-assist data-seam=${SEAM_ASSIST}></qd-quote-assist>
                <qd-quote-history data-seam=${SEAM_HISTORY}></qd-quote-history>
                <qd-quote-followups data-seam=${SEAM_FOLLOWUPS}></qd-quote-followups>
                <qd-quote-hold-expiry data-seam=${SEAM_HOLD_EXPIRY}></qd-quote-hold-expiry>
                <qd-quote-availability data-seam=${SEAM_AVAILABILITY}></qd-quote-availability>
              `
            : html`<div class="box"><p class="empty">Select a quote to open it.</p></div>`}
          <qd-approvals data-seam=${SEAM_APPROVALS}></qd-approvals>
          <qd-quote-settings data-seam=${SEAM_SETTINGS}></qd-quote-settings>
        </div>
      </details>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-builder": QdQuoteBuilder;
  }
}
