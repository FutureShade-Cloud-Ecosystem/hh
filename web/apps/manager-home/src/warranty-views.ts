// Warranty's screens: the board, the claims list, the vendor registry, the
// recovery worklist, the service levels, the reports, and the claim detail
// with its lifecycle. Pure template functions over the desk's model; the app
// element owns every call and every state change, as views.ts does for the
// landing.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings, ?disabled the one boolean. Controls
// ride data-action names and the host's listeners act on them; checkbox rows
// are buttons carrying role and aria-checked; a select's starting value is
// seeded after render, never bound; a photo's object address is set on the
// element after render, because the gate admits only http, https and
// relative addresses in a src binding.
//
// Port provenance: each tab's shape is the original app's warranty screen
// (repository hardscapeos_dibbits, read only; see PROVENANCE.md), rebuilt on
// HH's tokens and this app's sheet. Money renders from the core's integer
// cents through centsWords, and is computed nowhere.
import { html, nothing, type TemplateResult } from "lit";
import type { BoardColumn, Claim, ClaimEvent, ChecklistState, IntakePhoto, RecoveryBoardModel, RecoveryRow, ReportBundle, SettlementPosting, SlaAlert, SlaTarget, SlaTimer, VendorProfile } from "./warranty-api.js";
import { WARRANTY_TABS, WARRANTY_TAB_TITLE, type ClaimsTab, type ClaimDetailModel, type RecoveryTab, type SlaTab, type VendorTab, type WarrantyModel } from "./warranty-desk.js";
import {
  CLAIM_CHANNELS,
  OUTCOMES,
  PHASES,
  RECOVERY_ACTIONS,
  RECOVERY_BUCKETS,
  SETTLEMENT_PHASES,
  VENDOR_STATUSES,
  bucketWord,
  captureWords,
  centsWords,
  channelWord,
  checklistWords,
  dollarsTyped,
  eventLine,
  hoursWords,
  legalMoves,
  moneyPathWord,
  notifyWord,
  outcomeWord,
  pathStatusWord,
  phaseWord,
  recoveryActionWord,
  slaStateWord,
  sourceWord,
  stampWords,
  vendorStatusWord,
} from "./warranty-logic.js";
import { dateWords } from "./manager-logic.js";

/** The value that is simply not on file, the catalogue's own no-value mark. */
const NOT_ON_FILE = "-";

// -- the view's head, the scope and the tabs -------------------------------------------------------------

export function renderWarranty(model: WarrantyModel): TemplateResult {
  if (!model.open) {
    return html`<p class="notice-line">Opening the warranty desk…</p>`;
  }
  const where = model.scopeAll ? "All branches your grants reach" : model.branchName;
  return html`
    <div class="view-head">
      <button type="button" class="btn btn-secondary back" data-action="back"><span class="chev" aria-hidden="true">‹</span>Back to Home</button>
      <h1>Warranty</h1>
      <span class="where">${where}</span>
      <p class="page-sub">Vendor warranty claims, from capture to settlement, and the money they recover.</p>
    </div>
    <div class="scope-row" role="group" aria-label="Scope">
      <button type="button" class="kind-tab" data-action="w-scope" data-scope="all" aria-selected=${model.scopeAll ? "true" : "false"}>All branches</button>
      <button type="button" class="kind-tab" data-action="w-scope" data-scope="branch" aria-selected=${model.scopeAll ? "false" : "true"}>${model.branchName !== "" ? model.branchName : "Your branch"}</button>
    </div>
    ${model.detail !== null
      ? renderClaimDetail(model.detail, model.vendorOptions)
      : html`
          <div class="kind-tabs" role="tablist" aria-label="Warranty tabs">
            ${WARRANTY_TABS.map((tab) => html`
              <button type="button" class="kind-tab" role="tab" data-action="w-tab" data-tab=${tab} aria-selected=${model.tab === tab ? "true" : "false"}>
                ${WARRANTY_TAB_TITLE[tab]}
              </button>
            `)}
          </div>
          ${renderPane(model)}
        `}
  `;
}

function renderPane(model: WarrantyModel): TemplateResult {
  switch (model.tab) {
    case "board":
      return renderBoardTab(model);
    case "claims":
      return renderClaimsTab(model.claims);
    case "vendors":
      return renderVendorsTab(model.vendors);
    case "recovery":
      return renderRecoveryTab(model.recovery);
    case "sla":
      return renderSlaTab(model.sla);
    case "reports":
      return renderReportsTab(model.reports);
    default:
      return html`${nothing}`;
  }
}

/** The four states every tab shares: the load, the refusal, the error, then the pane's own body. */
function tabFrame(
  state: { readonly phase: "loading" | "ready" | "error" | "refused"; readonly error: string; readonly refusal: string },
  loadingWords: string,
  body: TemplateResult,
): TemplateResult {
  return html`
    ${state.refusal !== "" ? html`<p class="refusal" data-test="warranty-refusal">${state.refusal}</p>` : nothing}
    ${state.phase === "loading"
      ? html`<p class="notice-line">${loadingWords}</p>`
      : state.phase === "error"
        ? html`
            <p class="error-line" data-test="warranty-refusal">${state.error}</p>
            <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="w-reload">Try again</button></div>
          `
        : body}
  `;
}

// -- the board -------------------------------------------------------------------------------------------

const BOARD_WORDS = "A claim moves left to right until the vendor answers and the money is booked. Click a card to work the claim.";

function renderBoardTab(model: WarrantyModel): TemplateResult {
  return tabFrame(
    model.board,
    "Loading the board…",
    html`
      <div class="toolbar">
        <p class="def-note">${BOARD_WORDS}</p>
        <div class="row-actions">
          <button type="button" class="btn btn-primary" data-action="w-capture-toggle">${model.captureOpen ? "Close" : "+ Capture a claim"}</button>
          <button type="button" class="btn btn-secondary" data-action="w-reload">Refresh</button>
        </div>
      </div>
      ${model.captureError !== "" ? html`<p class="error-line" data-test="warranty-refusal">${model.captureError}</p>` : nothing}
      ${model.captureOpen ? renderCaptureForm(model) : nothing}
      ${boardBody(model.board.data, model.captureOpen)}
    `,
  );
}

function boardBody(columns: readonly BoardColumn[], formOpen: boolean): TemplateResult {
  const total = columns.reduce((sum, col) => sum + col.claims.length, 0);
  if (columns.length === 0 || (total === 0 && !formOpen)) {
    return html`
      <section class="sheet">
        <div class="list-card"><p class="notice-line" data-test="warranty-board-empty">No warranty claims on the board yet. Capture the first one when a failure comes in.</p></div>
      </section>
    `;
  }
  return html`
    <section class="sheet" data-test="warranty-board">
      <div class="board">
        ${columns.map((col) => html`
          <div class="col" data-phase=${col.phase}>
            <div class="col-head">
              <span>${phaseWord(col.phase)}</span>
              <span class="count">${col.claims.length}</span>
            </div>
            <div class="cards">
              ${col.claims.length === 0
                ? html`<p class="col-empty">${NOT_ON_FILE}</p>`
                : col.claims.map((claim) => renderClaimCard(claim))}
            </div>
          </div>
        `)}
      </div>
    </section>
  `;
}

function renderClaimCard(claim: Claim): TemplateResult {
  return html`
    <button type="button" class="claim-card" data-action="w-open-claim" data-claim-id=${claim.id}>
      <span class="no">${claim.claim_no}</span>
      <span class="vendor">${claim.vendor_name !== "" ? claim.vendor_name : NOT_ON_FILE}</span>
      <span class="meta">
        <span>${sourceWord(claim.source)}</span>
        <span class="amount">${claim.expected_cents !== 0 ? centsWords(claim.expected_cents) : NOT_ON_FILE}</span>
      </span>
    </button>
  `;
}

function renderCaptureForm(model: WarrantyModel): TemplateResult {
  return html`
    <form class="form-grid" data-form="warranty-capture">
      <p class="form-title">Capture a claim at ${model.branchName !== "" ? model.branchName : "your branch"}</p>
      <label class="field">Source
        <select class="select-field" data-field="w-capture-source" aria-label="Where the claim came from">
          <option>Office intake</option>
          <option>Counter (from invoice)</option>
        </select>
      </label>
      <label class="field">Vendor
        <select class="select-field" data-field="w-capture-vendor" aria-label="Vendor, or none yet">
          <option>No vendor yet (triage picks one)</option>
          ${model.vendorOptions.map((v) => html`<option>${v.vendor_name}</option>`)}
        </select>
      </label>
      <label class="field">Expected recovery ($)
        <input class="text-field" type="number" step="0.01" min="0" placeholder="0.00" data-field="w-capture-expected" aria-label="Expected recovery in dollars" ?disabled=${model.captureBusy} />
      </label>
      <label class="field">Opened (an earlier day is honest, blank is today)
        <input class="text-field" type="date" data-field="w-capture-opened" aria-label="Opened on" ?disabled=${model.captureBusy} />
      </label>
      <label class="field">Job address
        <input class="text-field" placeholder="Street the product sits at" data-field="w-capture-addr" aria-label="Job address" ?disabled=${model.captureBusy} />
      </label>
      <label class="field">City
        <input class="text-field" placeholder="City" data-field="w-capture-city" aria-label="City" ?disabled=${model.captureBusy} />
      </label>
      <label class="field full">Failure description
        <textarea class="text-field text-area" rows="2" placeholder="Efflorescence, spalling, breakage" data-field="w-capture-desc" aria-label="Failure description" ?disabled=${model.captureBusy}></textarea>
      </label>
      <label class="field">Product
        <input class="text-field" placeholder="The product that failed" data-field="w-capture-product" aria-label="Product" ?disabled=${model.captureBusy} />
      </label>
      <label class="field">Quantity
        <input class="text-field" type="number" step="0.0001" min="0" placeholder="0" data-field="w-capture-qty" aria-label="Quantity" ?disabled=${model.captureBusy} />
      </label>
      <label class="field">Unit
        <input class="text-field" placeholder="SF" data-field="w-capture-uom" aria-label="Unit" ?disabled=${model.captureBusy} />
      </label>
      <label class="field">Purchase date (supplier)
        <input class="text-field" type="date" data-field="w-capture-pds" aria-label="Supplier purchase date" ?disabled=${model.captureBusy} />
      </label>
      <div class="form-actions full">
        <button type="button" class="btn btn-primary" data-action="w-capture" ?disabled=${model.captureBusy}>${model.captureBusy ? "Capturing…" : "Create claim"}</button>
        <button type="button" class="btn btn-secondary" data-action="w-capture-toggle" ?disabled=${model.captureBusy}>Cancel</button>
      </div>
      <p class="form-note full">${captureWords.owner}</p>
      <p class="form-note full">${captureWords.backdate}</p>
    </form>
  `;
}

// -- the claims list ---------------------------------------------------------------------------------------

function renderClaimsTab(state: { readonly phase: "loading" | "ready" | "error" | "refused"; readonly error: string; readonly refusal: string; readonly data: ClaimsTab }): TemplateResult {
  return tabFrame(
    state,
    "Loading the claims…",
    html`
      <div class="kind-tabs" role="group" aria-label="Filter by phase">
        <button type="button" class="kind-tab" data-action="w-claims-phase" data-phase="" aria-selected=${state.data.phaseFilter === "" ? "true" : "false"}>All</button>
        ${PHASES.map((p) => html`
          <button type="button" class="kind-tab" data-action="w-claims-phase" data-phase=${p} aria-selected=${state.data.phaseFilter === p ? "true" : "false"}>${phaseWord(p)}</button>
        `)}
      </div>
      ${claimsBody(state.data)}
    `,
  );
}

function claimsBody(data: ClaimsTab): TemplateResult {
  if (data.rows.length === 0) {
    const words = data.phaseFilter === "" ? "No warranty claims yet." : `No claims sitting in ${phaseWord(data.phaseFilter).toLowerCase()} right now.`;
    return html`<section class="sheet"><div class="list-card"><p class="notice-line" data-test="warranty-claims-empty">${words}</p></div></section>`;
  }
  return html`
    <section class="sheet">
      <div class="list-card table-card">
        <table class="table">
          <thead>
            <tr><th>Claim</th><th>Vendor</th><th>Phase</th><th>Source</th><th>Opened</th><th class="num">Expected</th></tr>
          </thead>
          <tbody>
            ${data.rows.map((claim) => html`
              <tr data-claim=${claim.id}>
                <td><button type="button" class="claim-link" data-action="w-open-claim" data-claim-id=${claim.id}>${claim.claim_no}</button></td>
                <td>${claim.vendor_name !== "" ? claim.vendor_name : NOT_ON_FILE}</td>
                <td><span class="badge ${phaseBadge(claim.phase)}">${phaseWord(claim.phase)}</span></td>
                <td>${sourceWord(claim.source)}</td>
                <td class="mono">${dateWords(claim.opened_at)}</td>
                <td class="num amount">${claim.expected_cents !== 0 ? centsWords(claim.expected_cents) : NOT_ON_FILE}</td>
              </tr>
            `)}
          </tbody>
        </table>
      </div>
    </section>
  `;
}

/** The badge class a phase wears: the exits are red, closed is plain, the work is gold-adjacent amber. */
function phaseBadge(value: string): string {
  if (value === "REJECTED" || value === "ABANDONED") return "stop";
  if (value === "CLOSED") return "dim";
  if (value === "DECISION" || value === "SETTLEMENT") return "warn";
  return "work";
}

// -- the vendor registry --------------------------------------------------------------------------------------

const VENDOR_WORDS =
  "Each vendor's warranty terms in one place. Undocumented is explicit: a claim against an undocumented vendor is the prompt to write down the terms you negotiate, and the registry learns.";

function renderVendorsTab(state: { readonly phase: "loading" | "ready" | "error" | "refused"; readonly error: string; readonly refusal: string; readonly data: VendorTab }): TemplateResult {
  return tabFrame(
    state,
    "Loading the vendor registry…",
    html`
      <p class="def-note">${VENDOR_WORDS}</p>
      ${state.data.notice !== "" ? html`<p class="notice-line" data-test="warranty-vendor-notice">${state.data.notice}</p>` : nothing}
      ${state.data.openError !== "" ? html`<p class="error-line">${state.data.openError}</p>` : nothing}
      ${state.data.editing !== null ? renderVendorEditForm(state.data.editing, state.data.saving) : nothing}
      <section class="sheet">
        <div class="list-card table-card">
          <table class="table" data-test="warranty-vendors">
            <thead>
              <tr><th>Vendor</th><th>Status</th><th>Term</th><th>Claim channel</th><th>Coverage</th><th></th></tr>
            </thead>
            <tbody>
              ${state.data.rows.length === 0
                ? html`<tr><td colspan="6"><p class="notice-line">No vendor profiles yet. The first claim against a vendor is the prompt to record its terms.</p></td></tr>`
                : state.data.rows.map((profile) => renderVendorRow(profile, state.data))}
            </tbody>
          </table>
        </div>
      </section>
    `,
  );
}

function renderVendorRow(profile: VendorProfile, data: VendorTab): TemplateResult {
  const open = data.openId === profile.vendor_id;
  return html`
    <tr data-vendor=${profile.vendor_id}>
      <td>
        <button type="button" class="claim-link" data-action="w-vendor-open" data-vendor-id=${profile.vendor_id} aria-expanded=${open ? "true" : "false"}>${profile.vendor_name}</button>
        <span class="mono vendor-no">${profile.vendor_no}</span>
      </td>
      <td><span class="badge ${profile.status === "DOCUMENTED" ? "ok" : "warn"}">${vendorStatusWord(profile.status)}</span></td>
      <td class="mono">${profile.term_months !== null ? `${profile.term_months} months` : NOT_ON_FILE}</td>
      <td>
        ${channelWord(profile.channel)}
        ${profile.channel_ref !== "" ? html`<span class="vendor-no">${profile.channel_ref}</span>` : nothing}
      </td>
      <td class="coverage-cell">${profile.coverage !== "" ? profile.coverage : html`<span class="vendor-no">${NOT_ON_FILE}</span>`}</td>
      <td>
        <button type="button" class="btn btn-secondary btn-small" data-action="w-vendor-edit" data-vendor-id=${profile.vendor_id} ?disabled=${data.saving}>Terms</button>
      </td>
    </tr>
    ${open
      ? html`<tr class="panel-row"><td colspan="6">${renderVendorPanel(data, profile)}</td></tr>`
      : nothing}
  `;
}

function renderVendorPanel(data: VendorTab, rowProfile: VendorProfile): TemplateResult {
  if (data.openProfile === null) {
    return html`<p class="notice-line">${data.openError !== "" ? data.openError : "Reading the profile…"}</p>`;
  }
  const profile = data.openProfile;
  return html`
    <div class="vendor-panel">
      <div class="vendor-panel-col">
        <h4 class="panel-title">Terms on file</h4>
        ${profile.coverage !== "" ? html`<p class="detail">${profile.coverage}</p>` : html`<p class="detail vendor-no">${NOT_ON_FILE}</p>`}
        ${profile.notes !== "" ? html`<p class="detail">${profile.notes}</p>` : nothing}
        <h4 class="panel-title">Documents</h4>
        ${profile.docs.length === 0
          ? html`<p class="detail vendor-no">No documents recorded.</p>`
          : html`<ul class="plain-list">${profile.docs.map((d) => html`<li class="detail">${d.title} <span class="mono">${d.ref}</span></li>`)}</ul>`}
      </div>
      <div class="vendor-panel-col">
        <h4 class="panel-title">Product overrides</h4>
        ${profile.overrides.length === 0
          ? html`<p class="detail vendor-no">No per-product overrides.</p>`
          : html`
              <table class="table">
                <thead><tr><th>Product</th><th>Coverage</th><th class="num">Term</th></tr></thead>
                <tbody>
                  ${profile.overrides.map((o) => html`
                    <tr data-override=${o.id}>
                      <td>${o.product_name !== "" ? o.product_name : o.product_id}</td>
                      <td>${o.coverage}</td>
                      <td class="num mono">${o.term_months !== null ? `${o.term_months} months` : NOT_ON_FILE}</td>
                    </tr>
                  `)}
                </tbody>
              </table>
            `}
        ${data.overrideFor === profile.vendor_id
          ? html`
              <div class="override-form">
                <label class="field">Product id
                  <input class="text-field" data-field="w-override-product" aria-label="Product id" ?disabled=${data.saving} />
                </label>
                <label class="field">Term in months (blank is unknown)
                  <input class="text-field" type="number" min="0" data-field="w-override-term" aria-label="Term in months" ?disabled=${data.saving} />
                </label>
                <label class="field full">What is covered
                  <textarea class="text-field text-area" rows="2" data-field="w-override-coverage" aria-label="What is covered" ?disabled=${data.saving}></textarea>
                </label>
                <label class="field full">Notes
                  <input class="text-field" data-field="w-override-notes" aria-label="Notes" ?disabled=${data.saving} />
                </label>
                <div class="row-actions full">
                  <button type="button" class="btn btn-primary" data-action="w-override-add" ?disabled=${data.saving}>Add override</button>
                  <button type="button" class="btn btn-secondary" data-action="w-override-toggle" data-vendor-id=${profile.vendor_id} ?disabled=${data.saving}>Cancel</button>
                </div>
              </div>
            `
          : html`<div class="row-actions"><button type="button" class="btn btn-secondary" data-action="w-override-toggle" data-vendor-id=${profile.vendor_id} ?disabled=${data.saving}>Add a product override</button></div>`}
        <p class="vendor-no">The registry read answers for ${rowProfile.vendor_name}; a save writes through the same route the terms sheet uses.</p>
      </div>
    </div>
  `;
}

function renderVendorEditForm(profile: VendorProfile, saving: boolean): TemplateResult {
  return html`
    <form class="form-grid edit-grid" data-form="warranty-vendor">
      <p class="form-title">Terms for ${profile.vendor_name} <span class="mono">${profile.vendor_no}</span></p>
      <label class="field">Status
        <select class="select-field" data-field="w-vendor-status" aria-label="Documentation status" ?disabled=${saving}>
          ${VENDOR_STATUSES.map((s) => html`<option>${vendorStatusWord(s)}</option>`)}
        </select>
      </label>
      <label class="field">Term in months (blank is unknown)
        <input class="text-field" type="number" min="0" data-field="w-vendor-term" aria-label="Term in months" ?disabled=${saving} />
      </label>
      <label class="field">Claim channel
        <select class="select-field" data-field="w-vendor-channel" aria-label="Claim channel" ?disabled=${saving}>
          ${CLAIM_CHANNELS.map((c) => html`<option>${channelWord(c)}</option>`)}
        </select>
      </label>
      <label class="field">Channel ref
        <input class="text-field" placeholder="Email, portal address, or the rep" data-field="w-vendor-ref" aria-label="Channel reference" ?disabled=${saving} />
      </label>
      <label class="field full">Coverage summary
        <textarea class="text-field text-area" rows="2" placeholder="What is covered, what is excluded" data-field="w-vendor-coverage" aria-label="Coverage summary" ?disabled=${saving}></textarea>
      </label>
      <label class="field full">Notes
        <textarea class="text-field text-area" rows="2" data-field="w-vendor-notes" aria-label="Notes" ?disabled=${saving}></textarea>
      </label>
      <div class="form-actions full">
        <button type="button" class="btn btn-primary" data-action="w-vendor-save" ?disabled=${saving}>${saving ? "Saving…" : "Save terms"}</button>
        <button type="button" class="btn btn-secondary" data-action="w-vendor-cancel" ?disabled=${saving}>Cancel</button>
      </div>
      <p class="form-note full">The pick list's words are the counter's; the desk maps the picked row back to the stored value the server holds.</p>
    </form>
  `;
}

// -- recovery -----------------------------------------------------------------------------------------------------

const RECOVERY_WORDS =
  "Money a vendor has agreed to and not yet paid. Outstanding is what was accrued at settlement minus what has actually been received; the aging runs from the day the vendor agreed.";

function renderRecoveryTab(state: { readonly phase: "loading" | "ready" | "error" | "refused"; readonly error: string; readonly refusal: string; readonly data: RecoveryTab }): TemplateResult {
  return tabFrame(
    state,
    "Loading the recovery worklist…",
    html`
      <p class="def-note">${RECOVERY_WORDS}</p>
      <div class="toolbar">
        <button type="button" class="check-row" role="checkbox" aria-checked=${state.data.includeReconciled ? "true" : "false"} data-action="w-recovery-toggle">
          <span class="mark" aria-hidden="true"></span>Include reconciled
        </button>
        <button type="button" class="btn btn-secondary" data-action="w-reload">Refresh</button>
      </div>
      ${state.data.board === null
        ? html`<section class="sheet"><div class="list-card"><p class="notice-line">Nothing to chase here yet.</p></div></section>`
        : recoveryBody(state.data)}
    `,
  );
}

function recoveryBody(data: RecoveryTab): TemplateResult {
  const board = data.board;
  if (board === null) {
    return html`<section class="sheet"><div class="list-card"><p class="notice-line">Nothing to chase here yet.</p></div></section>`;
  }
  const nothingOutstanding = board.total_outstanding_cents === 0;
  return html`
    <div class="stat-grid" data-test="warranty-recovery">
      <div class="stat headline"><span class="n">${centsWords(board.total_outstanding_cents)}</span><span class="l">Outstanding</span></div>
      <div class="stat"><span class="n">${centsWords(board.total_accrued_cents)}</span><span class="l">Accrued</span></div>
      <div class="stat"><span class="n">${centsWords(board.total_received_cents)}</span><span class="l">Received</span></div>
      <div class="stat"><span class="n">${centsWords(board.total_promised_cents)}</span><span class="l">Promised</span></div>
      <div class="stat"><span class="n">${board.open_claim_count}</span><span class="l">Open claims</span></div>
    </div>
    ${bucketChips(board)}
    ${board.vendors.length === 0
      ? html`<section class="sheet"><div class="list-card"><p class="notice-line" data-test="warranty-recovery-empty">Nothing outstanding. Every agreed recovery has been received.</p></div></section>`
      : board.vendors.map((vendor) => renderRecoveryVendor(vendor, data, nothingOutstanding))}
  `;
}

function bucketChips(board: RecoveryBoardModel): TemplateResult {
  const chips = RECOVERY_BUCKETS.filter((b) => (board.buckets[b] ?? 0) > 0);
  if (chips.length === 0) return html`${nothing}`;
  return html`
    <div class="buckets">
      ${chips.map((b) => html`<span class="bucket ${b === "90+" || b === "61-90" ? "hot" : ""}">${b} · ${centsWords(board.buckets[b] ?? 0)}</span>`)}
    </div>
  `;
}

function renderRecoveryVendor(vendor: RecoveryBoardModel["vendors"][number], data: RecoveryTab, nothingOutstanding: boolean): TemplateResult {
  const vendorChips = RECOVERY_BUCKETS.filter((b) => (vendor.buckets[b] ?? 0) > 0);
  return html`
    <section class="list-card vendor-panel-card">
      <div class="vendor-head">
        <h4 class="panel-title">${vendor.vendor_name}</h4>
        <span class="amount big">${centsWords(vendor.outstanding_cents)}</span>
        <span class="vendor-no">${vendor.claim_count} claim${vendor.claim_count === 1 ? "" : "s"}${vendor.oldest_days > 0 ? ` · oldest ${vendor.oldest_days} days` : ""}</span>
        ${vendorChips.length > 0
          ? html`<div class="buckets">
              ${vendorChips.map((b) => html`<span class="bucket ${b === "90+" || b === "61-90" ? "hot" : ""}">${b} · ${centsWords(vendor.buckets[b] ?? 0)}</span>`)}
            </div>`
          : nothing}
      </div>
      <table class="table">
        <thead>
          <tr><th>Claim</th><th class="num">Accrued</th><th class="num">Received</th><th class="num">Outstanding</th><th class="num">Age</th><th>Last contact</th><th></th></tr>
        </thead>
        <tbody>
          ${vendor.claims.map((row) => renderRecoveryRow(row, data))}
        </tbody>
      </table>
      ${nothingOutstanding ? html`<p class="notice-line">Every agreed recovery on this board has been received.</p>` : nothing}
    </section>
  `;
}

function renderRecoveryRow(row: RecoveryRow, data: RecoveryTab): TemplateResult {
  const chaseOpen = data.openLogFor === row.claim_id;
  return html`
    <tr data-claim=${row.claim_id}>
      <td><button type="button" class="claim-link" data-action="w-open-claim" data-claim-id=${row.claim_id}>${row.claim_no}</button></td>
      <td class="num">${centsWords(row.accrued_cents)}</td>
      <td class="num">${centsWords(row.received_cents)}</td>
      <td class="num amount">${centsWords(row.outstanding_cents)}</td>
      <td class="num mono">${row.age_days} days</td>
      <td class="mono">${row.last_action_at !== "" ? dateWords(row.last_action_at) : NOT_ON_FILE}</td>
      <td>
        <button type="button" class="btn btn-secondary btn-small" data-action="w-recovery-chase" data-claim-id=${row.claim_id} ?disabled=${data.logBusy}>${chaseOpen ? "Hide" : "Chase"}</button>
      </td>
    </tr>
    ${chaseOpen ? html`<tr class="panel-row"><td colspan="7">${renderChasePanel(data)}</td></tr>` : nothing}
  `;
}

function renderChasePanel(data: RecoveryTab): TemplateResult {
  return html`
    <div class="chase">
      <ol class="chase-log">
        ${data.log.length === 0
          ? html`<li class="detail">No contact logged yet.</li>`
          : data.log.map((a) => html`
              <li class="detail">
                <strong>${recoveryActionWord(a.action)}</strong>
                ${a.amount_cents !== 0 ? html` · <span class="amount">${centsWords(a.amount_cents)}</span>` : nothing}
                ${a.reference !== "" ? html` (ref ${a.reference})` : nothing}
                ${a.promised_date !== "" ? html` by ${dateWords(a.promised_date)}` : nothing}
                ${a.note !== "" ? html` · ${a.note}` : nothing}
                <span class="mono"> ${dateWords(a.occurred_at)}</span>
              </li>
            `)}
      </ol>
      <div class="chase-form" data-form="warranty-chase">
        <label class="field">Action
          <select class="select-field" data-field="w-recovery-action" aria-label="Chase action">
            ${RECOVERY_ACTIONS.map((a) => html`<option>${a.word}</option>`)}
          </select>
        </label>
        <label class="field">Amount ($, for a receipt or a promise)
          <input class="text-field" inputmode="decimal" placeholder="0.00" data-field="w-recovery-amount" aria-label="Amount in dollars" ?disabled=${data.logBusy} />
        </label>
        <label class="field">Promised for (a promise's day)
          <input class="text-field" type="date" data-field="w-recovery-promised" aria-label="Promised for" ?disabled=${data.logBusy} />
        </label>
        <label class="field">Reference
          <input class="text-field" placeholder="Credit note or cheque no" data-field="w-recovery-ref" aria-label="Reference" ?disabled=${data.logBusy} />
        </label>
        <label class="field">Note
          <input class="text-field" data-field="w-recovery-note" aria-label="Note" ?disabled=${data.logBusy} />
        </label>
        <div class="row-actions">
          <button type="button" class="btn btn-primary" data-action="w-recovery-log" ?disabled=${data.logBusy}>${data.logBusy ? "Logging…" : "Log"}</button>
        </div>
      </div>
      ${data.logError !== "" ? html`<p class="error-line" data-test="warranty-refusal">${data.logError}</p>` : nothing}
      ${data.logNotice !== "" ? html`<p class="notice-line">${data.logNotice}</p>` : nothing}
      <p class="vendor-no">A receipt that exceeds what was accrued is refused by the server, by name and by amount. The board re-reads the server's answer after every entry.</p>
    </div>
  `;
}

// -- service levels -------------------------------------------------------------------------------------------------

const SLA_WORDS =
  "Every phase has a clock, not just the wait on the vendor. The alert is a durable row on the claim; the notification is a courtesy, and its status rides beside each breach.";

function renderSlaTab(state: { readonly phase: "loading" | "ready" | "error" | "refused"; readonly error: string; readonly refusal: string; readonly data: SlaTab }): TemplateResult {
  return tabFrame(
    state,
    "Loading the service levels…",
    html`
      <p class="def-note">${SLA_WORDS}</p>
      <div class="toolbar">
        <button type="button" class="check-row" role="checkbox" aria-checked=${state.data.includeAcknowledged ? "true" : "false"} data-action="w-sla-include-ack">
          <span class="mark" aria-hidden="true"></span>Include acknowledged
        </button>
        <button type="button" class="btn btn-primary" data-action="w-sla-sweep" ?disabled=${state.data.sweeping}>${state.data.sweeping ? "Checking…" : "Check now"}</button>
        <button type="button" class="btn btn-secondary" data-action="w-reload">Refresh</button>
      </div>
      ${state.data.notice !== "" ? html`<p class="notice-line" data-test="warranty-sla-notice">${state.data.notice}</p>` : nothing}
      ${state.data.board === null
        ? html`<section class="sheet"><div class="list-card"><p class="notice-line">No clocks running.</p></div></section>`
        : slaBody(state.data)}
    `,
  );
}

function slaBody(data: SlaTab): TemplateResult {
  const board = data.board;
  if (board === null) {
    return html`<section class="sheet"><div class="list-card"><p class="notice-line">No clocks running.</p></div></section>`;
  }
  return html`
    <div class="stat-grid" data-test="warranty-sla">
      <div class="stat bad"><span class="n" data-test="warranty-sla-breached">${board.breached_count}</span><span class="l">Overdue</span></div>
      <div class="stat warn"><span class="n">${board.due_soon_count}</span><span class="l">Due soon</span></div>
      <div class="stat"><span class="n">${board.timers.length}</span><span class="l">Open claims</span></div>
      <div class="stat"><span class="n">${board.alerts.length}</span><span class="l">Alerts</span></div>
    </div>
    <h4 class="block-title">Alerts</h4>
    <section class="sheet">
      <div class="list-card table-card">
        ${board.alerts.length === 0
          ? html`<p class="notice-line">No open alerts.</p>`
          : html`
              <table class="table">
                <thead><tr><th>Claim</th><th>Phase</th><th class="num">Overdue</th><th>Due</th><th>Notification</th><th></th></tr></thead>
                <tbody>${board.alerts.map((alert) => renderSlaAlert(alert))}</tbody>
              </table>
            `}
      </div>
    </section>
    <h4 class="block-title">Timers</h4>
    <section class="sheet">
      <div class="list-card table-card">
        ${board.timers.length === 0
          ? html`<p class="notice-line">No open claims.</p>`
          : html`
              <table class="table">
                <thead><tr><th>Claim</th><th>Phase</th><th>State</th><th class="num">Target</th><th class="num">Elapsed</th><th class="num">Remaining</th><th class="num">Exposure</th></tr></thead>
                <tbody>${board.timers.map((timer) => renderSlaTimer(timer))}</tbody>
              </table>
            `}
      </div>
    </section>
    <h4 class="block-title">Configured targets</h4>
    <section class="sheet">
      <div class="list-card table-card">
        ${data.targets.length === 0
          ? html`<p class="notice-line">No targets configured.</p>`
          : html`
              <table class="table">
                <thead><tr><th>Phase</th><th>Vendor</th><th class="num">Target</th><th>Notes</th></tr></thead>
                <tbody>${data.targets.map((target) => renderSlaTarget(target))}</tbody>
              </table>
            `}
      </div>
    </section>
  `;
}

function renderSlaAlert(alert: SlaAlert): TemplateResult {
  const suppressed = alert.notify_status === "SUPPRESSED";
  return html`
    <tr data-alert=${alert.id}>
      <td><button type="button" class="claim-link" data-action="w-open-claim" data-claim-id=${alert.claim_id}>${alert.claim_no}</button></td>
      <td>${phaseWord(alert.phase)}</td>
      <td class="num mono">${hoursWords(alert.overdue_hours)}</td>
      <td class="mono">${dateWords(alert.due_at)}</td>
      <td>
        <span class="vendor-no">${notifyWord(alert.notify_status)}</span>
        ${suppressed ? html`<span class="vendor-no"> · notifications not enabled here</span>` : nothing}
      </td>
      <td>
        ${alert.acknowledged_at !== ""
          ? html`<span class="vendor-no">acknowledged</span>`
          : html`<button type="button" class="btn btn-secondary btn-small" data-action="w-sla-ack" data-claim-id=${alert.claim_id} data-alert-id=${alert.id}>Acknowledge</button>`}
      </td>
    </tr>
  `;
}

function renderSlaTimer(timer: SlaTimer): TemplateResult {
  return html`
    <tr data-claim=${timer.claim_id}>
      <td><button type="button" class="claim-link" data-action="w-open-claim" data-claim-id=${timer.claim_id}>${timer.claim_no}</button></td>
      <td>${phaseWord(timer.phase)}</td>
      <td><span class="badge ${slaBadge(timer.state)}">${slaStateWord(timer.state)}</span></td>
      <td class="num mono">${timer.target_hours > 0 ? hoursWords(timer.target_hours) : NOT_ON_FILE}</td>
      <td class="num mono">${hoursWords(timer.elapsed_hours)}</td>
      <td class="num mono">${timer.state === "NO_TARGET" ? NOT_ON_FILE : hoursWords(timer.remaining_hours)}</td>
      <td class="num amount">${centsWords(timer.exposure_cents)}</td>
    </tr>
  `;
}

function slaBadge(state: string): string {
  if (state === "BREACHED") return "stop";
  if (state === "DUE_SOON") return "warn";
  if (state === "OK") return "ok";
  return "dim";
}

function renderSlaTarget(target: SlaTarget): TemplateResult {
  return html`
    <tr data-target=${target.id}>
      <td>${phaseWord(target.phase)}</td>
      <td>${target.vendor_name !== "" ? target.vendor_name : "All vendors (the default)"}</td>
      <td class="num mono">${hoursWords(target.target_hours)}</td>
      <td class="vendor-no">${target.notes}${target.is_active ? "" : " · not active"}</td>
    </tr>
  `;
}

// -- reports ---------------------------------------------------------------------------------------------------------

const REPORTS_WORDS =
  "The headline numbers behind the warranty desk: how often each manufacturer fails, what the claims win, how long a settlement takes, and what is still exposed.";

function renderReportsTab(state: { readonly phase: "loading" | "ready" | "error" | "refused"; readonly error: string; readonly refusal: string; readonly data: ReportBundle | null }): TemplateResult {
  return tabFrame(
    state,
    "Loading the warranty reports…",
    state.data === null
      ? html`<section class="sheet"><div class="list-card"><p class="notice-line">No metrics yet.</p></div></section>`
      : reportsBody(state.data),
  );
}

function reportsBody(data: ReportBundle): TemplateResult {
  const max = Math.max(1, ...data.funnel.map((f) => f.count));
  const times = data.time_to_settlement;
  return html`
    <div class="stat-grid" data-test="warranty-reports">
      <div class="stat"><span class="n">${data.total_open}</span><span class="l">Open claims</span></div>
      <div class="stat"><span class="n">${centsWords(data.total_exposure_cents)}</span><span class="l">Total exposure</span></div>
      <div class="stat"><span class="n">${times.settled_count}</span><span class="l">Settled</span></div>
      <div class="stat"><span class="n">${times.avg_days > 0 ? Math.round(times.avg_days) : NOT_ON_FILE}</span><span class="l">Average days to settle</span></div>
      <div class="stat"><span class="n">${times.median_days > 0 ? Math.round(times.median_days) : NOT_ON_FILE}</span><span class="l">Median days</span></div>
    </div>
    <div class="report-grid">
      <div>
        <h4 class="block-title">Funnel: claims per phase</h4>
        <section class="sheet">
          <div class="list-card">
            <ul class="bars funnel">
              ${data.funnel.map((f) => html`
                <li class="bar-row">
                  <span class="label">${phaseWord(f.phase)}</span>
                  <span class="track"><span class="fill ${fillClass(f.count, max)}"></span></span>
                  <span class="value">${f.count}</span>
                </li>
              `)}
            </ul>
          </div>
        </section>
      </div>
      <div>
        <h4 class="block-title">By manufacturer</h4>
        <section class="sheet">
          <div class="list-card table-card">
            <table class="table">
              <thead><tr><th>Manufacturer</th><th class="num">Claims</th><th class="num">Open</th><th class="num">Won</th><th class="num">Lost</th><th class="num">Win rate</th><th class="num">Recovered</th></tr></thead>
              <tbody>
                ${data.by_manufacturer.length === 0
                  ? html`<tr><td colspan="7"><p class="notice-line">No claims yet.</p></td></tr>`
                  : data.by_manufacturer.map((m) => html`
                      <tr data-vendor=${m.vendor_id}>
                        <td>${m.vendor_name}</td>
                        <td class="num">${m.total_claims}</td>
                        <td class="num">${m.open_claims}</td>
                        <td class="num">${m.won_claims}</td>
                        <td class="num">${m.lost_claims}</td>
                        <td class="num">${m.won_claims + m.lost_claims > 0 ? `${Math.round(m.win_rate * 100)}%` : NOT_ON_FILE}</td>
                        <td class="num amount">${centsWords(m.realized_cents)}</td>
                      </tr>
                    `)}
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </div>
    <div class="report-grid">
      <div>
        <h4 class="block-title">Exposure by vendor</h4>
        <section class="sheet">
          <div class="list-card table-card">
            <table class="table">
              <thead><tr><th>Vendor</th><th class="num">Open</th><th class="num">Exposure</th></tr></thead>
              <tbody>
                ${data.exposure_by_vendor.length === 0
                  ? html`<tr><td colspan="3"><p class="notice-line">No open exposure.</p></td></tr>`
                  : data.exposure_by_vendor.map((e) => html`
                      <tr data-vendor=${e.vendor_id}>
                        <td>${e.vendor_name}</td>
                        <td class="num">${e.open_claims}</td>
                        <td class="num amount">${centsWords(e.exposure_cents)}</td>
                      </tr>
                    `)}
              </tbody>
            </table>
          </div>
        </section>
      </div>
      <div>
        <h4 class="block-title">Open claims by age</h4>
        <section class="sheet">
          <div class="list-card table-card">
            <table class="table">
              <thead><tr><th>Claim</th><th>Phase</th><th>Vendor</th><th class="num">Age</th><th>Bucket</th><th class="num">Exposure</th></tr></thead>
              <tbody>
                ${data.open_claims_by_age.length === 0
                  ? html`<tr><td colspan="6"><p class="notice-line">No open claims.</p></td></tr>`
                  : data.open_claims_by_age.map((o) => html`
                      <tr data-claim=${o.claim_id}>
                        <td><button type="button" class="claim-link" data-action="w-open-claim" data-claim-id=${o.claim_id}>${o.claim_no}</button></td>
                        <td>${phaseWord(o.phase)}</td>
                        <td>${o.vendor_name !== "" ? o.vendor_name : NOT_ON_FILE}</td>
                        <td class="num mono">${o.age_days} days</td>
                        <td><span class="badge dim">${bucketWord(o.bucket)}</span></td>
                        <td class="num amount">${centsWords(o.expected_cents)}</td>
                      </tr>
                    `)}
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </div>
  `;
}

/** The funnel bar's fill, quantized to the sheet's static width classes (the landing's own rule). */
function fillClass(part: number, max: number): string {
  const share = max > 0 ? Math.round((part / max) * 20) * 5 : 0;
  return `w${Math.max(0, Math.min(100, share))}`;
}

// -- the claim detail -------------------------------------------------------------------------------------------------

function renderClaimDetail(detail: ClaimDetailModel, vendorOptions: readonly { readonly vendor_id: string; readonly vendor_name: string }[]): TemplateResult {
  if (detail.phase === "loading") {
    return html`<section class="sheet"><p class="notice-line" data-test="warranty-detail">Loading the claim…</p></section>`;
  }
  if (detail.phase === "refused") {
    return html`
      <section class="sheet" data-test="warranty-detail">
        <p class="refusal" data-test="warranty-refusal">${detail.refusal}</p>
        <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="w-close-claim">Back</button></div>
      </section>
    `;
  }
  if (detail.phase === "error" || detail.claim === null) {
    return html`
      <section class="sheet" data-test="warranty-detail">
        <p class="error-line" data-test="warranty-refusal">${detail.error}</p>
        <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="w-close-claim">Back</button></div>
      </section>
    `;
  }
  const claim = detail.claim;
  const moves = legalMoves(claim.phase);
  const forward = moves.filter((m) => m !== "REJECTED" && m !== "ABANDONED");
  const exits = moves.filter((m) => m === "REJECTED" || m === "ABANDONED");
  const canSettleNow = SETTLEMENT_PHASES.includes(claim.phase) && claim.outcome === "";
  return html`
    <section class="sheet" data-test="warranty-detail">
      <div class="detail-head">
        <div class="detail-id">
          <button type="button" class="btn btn-secondary back" data-action="w-close-claim"><span class="chev" aria-hidden="true">‹</span>Back to the board</button>
          <span class="claim-no" data-test="warranty-claim-no">${claim.claim_no}</span>
          <span class="badge ${phaseBadge(claim.phase)}">${phaseWord(claim.phase)}</span>
        </div>
      </div>
      ${detail.notice !== "" ? html`<p class="notice-line" data-test="warranty-detail-notice">${detail.notice}</p>` : nothing}
      ${detail.refusal !== "" ? html`<p class="refusal" data-test="warranty-refusal">${detail.refusal}</p>` : nothing}
      ${detail.error !== "" ? html`<p class="error-line" data-test="warranty-refusal">${detail.error}</p>` : nothing}

      <div class="list-card">
        <h4 class="block-title">Move the claim</h4>
        ${moves.length === 0
          ? html`<p class="notice-line">This claim is closed. Its history below is the record.</p>`
          : html`
              <div class="row-actions">
                ${forward.map((to) => html`
                  <button type="button" class="btn btn-primary" data-action="w-advance" data-to-phase=${to} ?disabled=${detail.busy}>${phaseWord(to)}</button>
                `)}
                ${exits.map((to) => html`
                  <button type="button" class="btn btn-secondary" data-action="w-advance" data-to-phase=${to} ?disabled=${detail.busy}>${phaseWord(to)}</button>
                `)}
              </div>
              <p class="form-note">Forward moves are gated by the current phase's required checklist items; the server names anything missing.</p>
            `}
      </div>

      <div class="detail-grid">
        ${claimFactsBox(claim)}
        ${checklistBox(detail)}
        ${evidenceBox(detail)}
        ${settlementBox(claim, detail)}
        ${assignBox(claim, detail, vendorOptions)}
        ${claim.source === "WEB" ? photosBox(detail) : nothing}
        ${postingsBox(detail)}
        ${timelineBox(detail.events)}
      </div>
    </section>
  `;
}

function claimFactsBox(claim: Claim): TemplateResult {
  const address = [claim.job_address.line1, claim.job_address.city].filter((part) => part !== "").join(", ");
  return html`
    <div class="box">
      <h4 class="block-title">Claim</h4>
      <div class="kv"><span>Vendor</span><span>${claim.vendor_name !== "" ? claim.vendor_name : NOT_ON_FILE}</span></div>
      <div class="kv"><span>Customer</span><span>${claim.customer_name !== "" ? claim.customer_name : NOT_ON_FILE}</span></div>
      <div class="kv"><span>Owner</span><span>${claim.owner_name !== "" ? claim.owner_name : NOT_ON_FILE}</span></div>
      <div class="kv"><span>Source</span><span>${sourceWord(claim.source)}</span></div>
      ${address !== "" ? html`<div class="kv"><span>Job site</span><span>${address}</span></div>` : nothing}
      <div class="kv"><span>Opened</span><span class="mono">${dateWords(claim.opened_at)}</span></div>
      ${claim.submitted_at !== "" ? html`<div class="kv"><span>Submitted</span><span class="mono">${dateWords(claim.submitted_at)}</span></div>` : nothing}
      ${claim.sla_due !== "" ? html`<div class="kv"><span>Service level due</span><span class="mono">${dateWords(claim.sla_due)}</span></div>` : nothing}
      <div class="kv"><span>Expected</span><span class="amount">${claim.expected_cents !== 0 ? centsWords(claim.expected_cents) : NOT_ON_FILE}</span></div>
      <div class="kv"><span>Realized</span><span class="amount">${claim.outcome !== "" || claim.realized_cents !== 0 ? centsWords(claim.realized_cents) : NOT_ON_FILE}</span></div>
      ${claim.outcome !== "" ? html`<div class="kv"><span>Outcome</span><span>${outcomeWord(claim.outcome)}</span></div>` : nothing}
      ${claim.description !== "" ? html`<p class="detail description">${claim.description}</p>` : nothing}
      ${claim.lines.map((line) => html`
        <div class="kv"><span>Line</span><span>${line.qty}${line.uom_code !== "" ? ` ${line.uom_code}` : ""} ${line.product_text}</span></div>
      `)}
    </div>
  `;
}

function checklistBox(detail: ClaimDetailModel): TemplateResult {
  return html`
    <div class="box">
      <h4 class="block-title">Checklist <span class="badge ${checklistWords(detail.checklist) === "Checklist complete" ? "ok" : "warn"}">${checklistWords(detail.checklist)}</span></h4>
      ${detail.checklist.length === 0
        ? html`<p class="notice-line">No checklist for this phase.</p>`
        : html`
            <div class="check-list">
              ${detail.checklist.map((state) => renderCheckItem(state, detail))}
            </div>
          `}
    </div>
  `;
}

function renderCheckItem(state: ChecklistState, detail: ClaimDetailModel): TemplateResult {
  const busy = detail.checklistBusyFor === state.item.item_key;
  return html`
    <button
      type="button"
      class="check-row"
      role="checkbox"
      aria-checked=${state.checked ? "true" : "false"}
      data-action="w-tick"
      data-item-key=${state.item.item_key}
      data-checked=${state.checked ? "false" : "true"}
      ?disabled=${busy || detail.busy}
    >
      <span class="mark" aria-hidden="true"></span>
      <span>${state.item.label}</span>
      ${state.item.required ? html`<span class="req">required</span>` : nothing}
    </button>
  `;
}

function evidenceBox(detail: ClaimDetailModel): TemplateResult {
  return html`
    <div class="box">
      <h4 class="block-title">Evidence</h4>
      <p class="form-note">Record where the evidence is kept. Attaching the file itself is not available yet.</p>
      <div class="row-actions">
        <input class="text-field grow" placeholder="Where the evidence is kept" data-field="w-evidence-ref" aria-label="Evidence reference" ?disabled=${detail.busy} />
        <button type="button" class="btn btn-secondary" data-action="w-evidence" ?disabled=${detail.busy}>Attach</button>
      </div>
    </div>
  `;
}

function settlementBox(claim: Claim, detail: ClaimDetailModel): TemplateResult {
  if (claim.outcome !== "") {
    return html`
      <div class="box">
        <h4 class="block-title">Settlement</h4>
        <div class="kv"><span>Outcome</span><span>${outcomeWord(claim.outcome)}</span></div>
        <div class="kv"><span>Expected</span><span class="amount">${centsWords(claim.expected_cents)}</span></div>
        <div class="kv"><span>Realized</span><span class="amount">${centsWords(claim.realized_cents)}</span></div>
        <p class="form-note">Recorded. The postings below carry what moved.</p>
      </div>
    `;
  }
  if (!SETTLEMENT_PHASES.includes(claim.phase)) {
    return html`
      <div class="box">
        <h4 class="block-title">Settlement</h4>
        <p class="form-note">Settlement opens when the claim reaches decision: the vendor's verdict first, then the money.</p>
      </div>
    `;
  }
  return html`
    <div class="box">
      <h4 class="block-title">Settlement</h4>
      ${detail.settleOpen
        ? html`
            <div class="settle-form" data-form="warranty-settle">
              <label class="field">Outcome
                <select class="select-field" data-field="w-settle-outcome" aria-label="Settlement outcome" ?disabled=${detail.busy}>
                  ${OUTCOMES.map((o) => html`<option>${outcomeWord(o)}</option>`)}
                </select>
              </label>
              <label class="field">Expected ($)
                <input class="text-field" type="number" step="0.01" min="0" data-field="w-settle-expected" aria-label="Expected in dollars" ?disabled=${detail.busy} />
              </label>
              <label class="field">Realized ($)
                <input class="text-field" type="number" step="0.01" min="0" placeholder="0.00" data-field="w-settle-realized" aria-label="Realized in dollars" ?disabled=${detail.busy} />
              </label>
              <label class="field full">Note
                <input class="text-field" placeholder="What the vendor agreed" data-field="w-settle-note" aria-label="Note" ?disabled=${detail.busy} />
              </label>
              <div class="row-actions full">
                <button type="button" class="btn btn-primary" data-action="w-settle" ?disabled=${detail.busy}>${detail.busy ? "Recording…" : "Record settlement"}</button>
                <button type="button" class="btn btn-secondary" data-action="w-settle-toggle" ?disabled=${detail.busy}>Cancel</button>
              </div>
              <p class="form-note full">The settlement is recorded by the server in one call, and the figures it answers with are the ones this claim then carries.</p>
            </div>
          `
        : html`
            <div class="row-actions">
              <button type="button" class="btn btn-primary" data-action="w-settle-toggle" ?disabled=${detail.busy}>Record settlement</button>
            </div>
          `}
    </div>
  `;
}

function assignBox(claim: Claim, detail: ClaimDetailModel, vendorOptions: readonly { readonly vendor_id: string; readonly vendor_name: string }[]): TemplateResult {
  if (claim.phase === "CLOSED" || claim.phase === "REJECTED" || claim.phase === "ABANDONED") return html`${nothing}`;
  return html`
    <div class="box">
      <h4 class="block-title">Vendor on the claim</h4>
      ${detail.assignOpen
        ? html`
            <div class="settle-form">
              <label class="field">Move to vendor
                <select class="select-field" data-field="w-assign-vendor" aria-label="Vendor" ?disabled=${detail.busy}>
                  <option>Pick a vendor</option>
                  ${vendorOptions.map((v) => html`<option>${v.vendor_name}</option>`)}
                </select>
              </label>
              <div class="row-actions full">
                <button type="button" class="btn btn-primary" data-action="w-assign" ?disabled=${detail.busy}>Move claim</button>
                <button type="button" class="btn btn-secondary" data-action="w-assign-toggle" ?disabled=${detail.busy}>Cancel</button>
              </div>
              <p class="form-note full">The claim's owner rides through unchanged; only the vendor moves.</p>
            </div>
          `
        : html`
            <div class="kv"><span>Current</span><span>${claim.vendor_name !== "" ? claim.vendor_name : "No vendor yet"}</span></div>
            <div class="row-actions">
              <button type="button" class="btn btn-secondary" data-action="w-assign-toggle" ?disabled=${detail.busy}>Move to a vendor</button>
            </div>
          `}
    </div>
  `;
}

function photosBox(detail: ClaimDetailModel): TemplateResult {
  return html`
    <div class="box">
      <h4 class="block-title">Photos from the intake</h4>
      ${detail.photoError !== "" ? html`<p class="error-line">${detail.photoError}</p>` : nothing}
      ${detail.photos.length === 0
        ? html`<p class="notice-line">No photos came with this claim.</p>`
        : html`
            <div class="photo-row">
              ${detail.photos.map((photo) => renderPhoto(photo))}
            </div>
          `}
    </div>
  `;
}

function renderPhoto(photo: IntakePhoto): TemplateResult {
  const label = photo.caption !== "" ? photo.caption : `Photo, ${Math.round(photo.byte_size / 1024)} kB`;
  // The picture's address and its alt words are both set on the element after
  // render (the app element's updated), because the gate admits neither
  // through a template binding.
  return html`
    <figure class="photo">
      <img data-w-photo=${photo.id} />
      <figcaption class="vendor-no">${label}</figcaption>
    </figure>
  `;
}

function postingsBox(detail: ClaimDetailModel): TemplateResult {
  return html`
    <div class="box">
      <h4 class="block-title">Postings</h4>
      ${detail.postings.length === 0
        ? html`<p class="notice-line">No postings recorded. A posting appears when a settlement is recorded.</p>`
        : html`
            <table class="table">
              <thead><tr><th>Path</th><th>Seam</th><th>Status</th><th class="num">Amount</th><th>Posted</th></tr></thead>
              <tbody>
                ${detail.postings.map((posting) => renderPosting(posting))}
              </tbody>
            </table>
          `}
    </div>
  `;
}

function renderPosting(posting: SettlementPosting): TemplateResult {
  return html`
    <tr data-posting=${posting.id}>
      <td>${moneyPathWord(posting.money_path)}</td>
      <td>${posting.seam}</td>
      <td><span class="badge ${postingBadge(posting.status)}">${pathStatusWord(posting.status)}</span></td>
      <td class="num amount">${centsWords(posting.amount_cents)}</td>
      <td class="mono">${posting.posted_at !== "" ? dateWords(posting.posted_at) : NOT_ON_FILE}</td>
    </tr>
    ${posting.error_text !== "" ? html`<tr class="panel-row"><td colspan="5"><p class="error-line">${posting.error_text}</p></td></tr>` : nothing}
  `;
}

function postingBadge(status: string): string {
  if (status === "POSTED") return "ok";
  if (status === "FAILED") return "stop";
  return "dim";
}

function timelineBox(events: readonly ClaimEvent[]): TemplateResult {
  return html`
    <div class="box">
      <h4 class="block-title">Timeline</h4>
      ${events.length === 0
        ? html`<p class="notice-line">Nothing on the claim's history yet.</p>`
        : html`
            <ul class="timeline">
              ${events.map((event) => html`
                <li>
                  <p class="detail">${eventLine(event)}</p>
                  <span class="when mono">${stampWords(event.occurred_at)}</span>
                </li>
              `)}
            </ul>
          `}
    </div>
  `;
}
