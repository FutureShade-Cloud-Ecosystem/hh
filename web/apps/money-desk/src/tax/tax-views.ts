// The tax slice's screens, as plain render functions of a model. They hold no state and own no events: every
// control is a plain element carrying a data-action name and the slice's click listener acts on it (the design
// system's position rules allow no @ or . binding in an app, and the only bound values are class, role, aria- and
// data- attributes). Form fields carry data-field names; the desk reads them as they change.
//
// Every figure on a screen is the server's, formatted: the app computes no money. Visual reference: the Money Desk
// boards (trial-balance, controller-home) for the frame, the panels and the pills, and the original app's tax
// administration screens (app/src/tax-admin.ts and tax-evidence.ts) for what each screen holds.
import { html, nothing, type TemplateResult } from "lit";
import type { TaxConfig, TaxEvidence, TaxRate, TaxReconReport, TaxRecomputeResult, TaxReliefEligibility, TaxStatusCardProfile } from "@hardscapehouse/api-client";
import {
  areaWord,
  attestationWords,
  blockReasonWords,
  dayWords,
  forcedEvidence,
  periodWords,
  profileStatusWord,
  profileTone,
  PROFILE_STATUSES,
  rateState,
  rateStateTone,
  rateStateWord,
  REASONS,
  reasonLine,
  reasonWord,
  ROUND_MODES,
  roundModeLine,
  roundModeWord,
  varianceWords,
  type Tone,
} from "./tax-logic.js";
import { formatMoney, formatRate, parseRateToBps } from "./tax-money.js";

export type TxTab = "rates" | "profiles" | "certificates" | "settings" | "checks";

export interface ConfirmView {
  readonly kind: "rate" | "profile" | "config" | "recompute";
  readonly title: string;
  readonly lines: readonly string[];
  readonly go: string;
}

export interface TaxModel {
  readonly phase: "loading" | "ready" | "error";
  readonly tab: TxTab;
  readonly today: string;
  readonly rates: readonly TaxRate[];
  readonly profiles: readonly TaxStatusCardProfile[];
  readonly queue: readonly TaxStatusCardProfile[];
  readonly config: TaxConfig | null;
  readonly evidence: readonly TaxEvidence[];
  readonly evidenceLoaded: boolean;
  readonly selected: TaxEvidence | null;
  readonly check: TaxReliefEligibility | null;
  readonly downloaded: boolean;
  readonly recon: TaxReconReport | null;
  readonly recompute: TaxRecomputeResult | null;
  readonly confirm: ConfirmView | null;
  readonly form: Readonly<Record<string, string>>;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
}

const MONTH_NAMES = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

function pill(tone: Tone, word: string): TemplateResult {
  return html`<span class="tx-status tx-status-${tone}">${word}</span>`;
}

/** A feature the core serves that this increment does not draw yet, said in words. */
function later(word: string): TemplateResult {
  return html`<span class="tx-later">${word}</span>`;
}

function messages(model: TaxModel): TemplateResult {
  return html`
    ${model.error !== "" ? html`<p class="tx-alert tx-alert-error" role="alert" data-error>${model.error}</p>` : nothing}
    ${model.notice !== "" ? html`<p class="tx-alert tx-alert-ok" role="status" data-notice>${model.notice}</p>` : nothing}
  `;
}

function confirmBox(model: TaxModel, kind: ConfirmView["kind"]): TemplateResult {
  const c = model.confirm;
  if (c === null || c.kind !== kind) return html`${nothing}`;
  return html`
    <div class="tx-confirm" role="group" aria-label="Confirm" data-confirm=${kind}>
      <strong>${c.title}</strong>
      ${c.lines.map((line) => html`<p>${line}</p>`)}
      <span class="tx-actions">
        <button type="button" class="btn btn-primary" data-action="tx-confirm" data-kind=${kind} ?disabled=${model.busy}>${c.go}</button>
        <button type="button" class="btn btn-secondary" data-action="tx-cancel">Not yet</button>
      </span>
    </div>
  `;
}

function empty(attr: string, head: string, line: string): TemplateResult {
  return html`<div class="tx-empty" data-empty=${attr}><strong>${head}</strong><span class="tx-dim">${line}</span></div>`;
}

// ---- the way in ---------------------------------------------------------------------------------------

function loading(): TemplateResult {
  return html`<p class="tx-dim" role="status" data-loading>Loading the tax tables.</p>`;
}

function loadFailed(model: TaxModel): TemplateResult {
  return html`
    <p class="tx-alert tx-alert-error" role="alert" data-error>${model.error}</p>
    <button type="button" class="btn btn-secondary" data-action="tx-reload">Try again</button>
  `;
}

function tabs(model: TaxModel): TemplateResult {
  const tab = (id: TxTab, label: string, count?: number): TemplateResult => html`
    <button type="button" class="tx-tab" data-action="tx-tab" data-tab=${id} data-tab-id=${id} aria-current="${model.tab === id ? "true" : "false"}">${label}${count === undefined || count === 0 ? nothing : html` <span class="tx-count" data-tab-count=${id}>${count}</span>`}</button>
  `;
  return html`
    <nav class="tx-tabs" aria-label="Tax views">
      ${tab("rates", "Rates")} ${tab("profiles", "Exemption profiles", model.queue.length)} ${tab("certificates", "Certificates")} ${tab("settings", "Settings")} ${tab("checks", "Recompute and reconcile")}
    </nav>
  `;
}

// ---- rates --------------------------------------------------------------------------------------------

function rateRows(model: TaxModel): TemplateResult {
  if (model.rates.length === 0) {
    return empty("rates", "No rate is on file.", "Without a standard rate in force the till refuses to ring a taxable sale. Add the standard rate first.");
  }
  const state = (r: TaxRate) => rateState(r.effective_from, r.effective_to, model.today);
  return html`
    <div class="tx-scroll tx-hide-narrow">
      <table aria-label="Rate table">
        <thead><tr><th>Reason</th><th class="num">Rate</th><th>Area</th><th>First day</th><th>Last day</th><th>Standing</th></tr></thead>
        <tbody>
          ${model.rates.map(
            (r) => html`<tr data-rate-id=${r.id}>
              <td><strong>${reasonWord(r.reason_code)}</strong>${r.notes === undefined || r.notes === "" ? nothing : html`<span class="tx-sub">${r.notes}</span>`}</td>
              <td class="num" data-rate-figure>${formatRate(r.rate_bps)}</td>
              <td>${areaWord(r.jurisdiction)}</td>
              <td class="tx-nowrap">${dayWords(r.effective_from)}</td>
              <td class="tx-nowrap">${r.effective_to === undefined || r.effective_to === "" ? "No end" : dayWords(r.effective_to)}</td>
              <td>${pill(rateStateTone(state(r)), rateStateWord(state(r)))}</td>
            </tr>`,
          )}
        </tbody>
      </table>
    </div>
    <ul class="tx-cards" aria-label="Rate table, as cards">
      ${model.rates.map(
        (r) => html`<li class="tx-card" data-rate-card=${r.id}>
          <div class="tx-card-top"><strong>${reasonWord(r.reason_code)}</strong><span class="tx-card-amount">${formatRate(r.rate_bps)}</span></div>
          <dl class="tx-facts">
            <dt>Area</dt><dd>${areaWord(r.jurisdiction)}</dd>
            <dt>First day</dt><dd>${dayWords(r.effective_from)}</dd>
            <dt>Last day</dt><dd>${r.effective_to === undefined || r.effective_to === "" ? "No end" : dayWords(r.effective_to)}</dd>
            <dt>Standing</dt><dd>${pill(rateStateTone(state(r)), rateStateWord(state(r)))}</dd>
          </dl>
          ${r.notes === undefined || r.notes === "" ? nothing : html`<span class="tx-sub">${r.notes}</span>`}
        </li>`,
      )}
    </ul>
  `;
}

function rateForm(model: TaxModel): TemplateResult {
  const typed = (model.form["tx-rate"] ?? "").trim();
  const bad = typed !== "" && parseRateToBps(typed) === null;
  const reason = model.form["tx-reason"] ?? REASONS[0] ?? "";
  return html`
    <div class="tx-form" role="group" aria-label="Add a rate">
      <label class="tx-field tx-field-wide">Reason
        <select data-field="tx-reason" aria-label="Reason">${REASONS.map((code) => html`<option data-id=${code}>${reasonWord(code)}</option>`)}</select>
        <span class="tx-hint" data-reason-line>${reasonLine(reason)}</span>
      </label>
      <label class="tx-field">Rate, percent
        <input type="text" inputmode="decimal" data-field="tx-rate" autocomplete="off" placeholder="Such as 7.75" aria-label="Rate, percent" />
        ${bad
          ? html`<span class="tx-hint tx-hint-bad" role="alert" data-rate-hint>Enter a percent between 0 and 100 with at most two decimals, such as 7.75.</span>`
          : html`<span class="tx-hint">A percent, with at most two decimals.</span>`}
      </label>
      <label class="tx-field">First day
        <input type="text" inputmode="numeric" data-field="tx-from" autocomplete="off" placeholder="Year-month-day" aria-label="First day" />
      </label>
      <label class="tx-field">Last day, if it ends
        <input type="text" inputmode="numeric" data-field="tx-to" autocomplete="off" placeholder="No end" aria-label="Last day, if it ends" />
      </label>
      <label class="tx-field tx-field-wide">Note
        <input type="text" data-field="tx-notes" autocomplete="off" aria-label="Note" />
      </label>
    </div>
    <div class="tx-actions">
      <button type="button" class="btn btn-primary" data-action="tx-rate-add" ?disabled=${model.busy}>Add the rate</button>
    </div>
    ${confirmBox(model, "rate")}
  `;
}

function ratesView(model: TaxModel): TemplateResult {
  return html`
    <div class="tx-split" data-tx-rates>
      <section class="tx-panel" aria-label="Rate table">
        <div class="tx-panel-head"><h3>Rate table</h3></div>
        <p class="tx-note" data-rates-note>A sale uses the row in force on its day. Rows are added and never edited: to change a rate, add a new row that starts the day it changes.</p>
        ${rateRows(model)}
      </section>
      <section class="tx-panel" aria-label="Add a rate">
        <div class="tx-panel-head"><h3>Add a rate</h3></div>
        ${rateForm(model)}
      </section>
    </div>
  `;
}

// ---- profiles -----------------------------------------------------------------------------------------

function profileButtons(model: TaxModel, p: TaxStatusCardProfile): TemplateResult {
  return html`<span class="tx-rowbtns">
    <button type="button" class="tx-link" data-action="tx-profile-edit" data-id=${p.code} ?disabled=${model.busy}>Edit</button>
    ${p.status === "PENDING" ? html`<button type="button" class="tx-link" data-action="tx-profile-ratify" data-id=${p.code} ?disabled=${model.busy}>Put in force</button>` : nothing}
  </span>`;
}

function queuePanel(model: TaxModel): TemplateResult {
  return html`
    <section class="tx-panel" aria-label="Waiting for review" data-tx-queue>
      <div class="tx-panel-head"><h3>Waiting for review</h3>${model.queue.length === 0 ? nothing : pill("watch", `${model.queue.length} waiting`)}</div>
      ${model.queue.length === 0
        ? empty("queue", "Nothing is waiting.", "A profile saved as Waiting for review lands here until someone puts it in force.")
        : html`<ul class="tx-list" aria-label="Profiles waiting for review">
            ${model.queue.map(
              (p) => html`<li class="tx-card" data-queue-id=${p.code}>
                <div class="tx-card-top"><strong>${p.name}</strong>${pill(profileTone(p.status), profileStatusWord(p.status))}</div>
                <span class="tx-sub">${p.code} · ${formatRate(p.rate_bps)} · ${forcedEvidence(p)}</span>
                ${profileButtons(model, p)}
              </li>`,
            )}
          </ul>`}
    </section>
  `;
}

function profileRows(model: TaxModel): TemplateResult {
  if (model.profiles.length === 0) {
    return empty("profiles", "No exemption profile is on file.", "A profile says which certificate a buyer shows to pay less than the standard rate, and what must be kept. Add one beside this list.");
  }
  return html`
    <div class="tx-scroll tx-hide-narrow">
      <table aria-label="Exemption profiles">
        <thead><tr><th>Profile</th><th class="num">Rate</th><th>Kept with each sale</th><th>Standing</th><th></th></tr></thead>
        <tbody>
          ${model.profiles.map(
            (p) => html`<tr data-profile-id=${p.code}>
              <td><strong>${p.name}</strong><span class="tx-sub">${p.code}</span>${p.notes === undefined || p.notes === "" ? nothing : html`<span class="tx-sub">${p.notes}</span>`}</td>
              <td class="num">${formatRate(p.rate_bps)}</td>
              <td>${forcedEvidence(p)}</td>
              <td>${pill(profileTone(p.status), profileStatusWord(p.status))}</td>
              <td>${profileButtons(model, p)}</td>
            </tr>`,
          )}
        </tbody>
      </table>
    </div>
    <ul class="tx-cards" aria-label="Exemption profiles, as cards">
      ${model.profiles.map(
        (p) => html`<li class="tx-card" data-profile-card=${p.code}>
          <div class="tx-card-top"><strong>${p.name}</strong><span class="tx-card-amount">${formatRate(p.rate_bps)}</span></div>
          <span class="tx-sub">${p.code}</span>
          <dl class="tx-facts"><dt>Kept</dt><dd>${forcedEvidence(p)}</dd><dt>Standing</dt><dd>${pill(profileTone(p.status), profileStatusWord(p.status))}</dd></dl>
          ${profileButtons(model, p)}
        </li>`,
      )}
    </ul>
  `;
}

function check(name: string, label: string): TemplateResult {
  return html`<label class="tx-check"><input type="checkbox" data-field=${name} aria-label=${label} /><span>${label}</span></label>`;
}

function profileForm(model: TaxModel): TemplateResult {
  const typed = (model.form["tx-p-rate"] ?? "").trim();
  const bad = typed !== "" && parseRateToBps(typed) === null;
  const editing = (model.form["tx-p-editing"] ?? "") !== "";
  return html`
    <div class="tx-form" role="group" aria-label="Save an exemption profile">
      <label class="tx-field">Code
        <input type="text" data-field="tx-p-code" autocomplete="off" placeholder="Such as RESALE" aria-label="Code" />
        <span class="tx-hint">Capitals, digits and underscores. Saving an existing code replaces that profile.</span>
      </label>
      <label class="tx-field">Name
        <input type="text" data-field="tx-p-name" autocomplete="off" placeholder="Such as Resale certificate" aria-label="Name" />
      </label>
      <label class="tx-field">Rate, percent
        <input type="text" inputmode="decimal" data-field="tx-p-rate" autocomplete="off" placeholder="Such as 0" aria-label="Profile rate, percent" />
        ${bad ? html`<span class="tx-hint tx-hint-bad" role="alert" data-profile-rate-hint>Enter a percent between 0 and 100 with at most two decimals.</span>` : html`<span class="tx-hint">What a buyer with this certificate pays.</span>`}
      </label>
      <label class="tx-field">Standing
        <select data-field="tx-p-status" aria-label="Standing">${PROFILE_STATUSES.map((s) => html`<option data-id=${s}>${profileStatusWord(s)}</option>`)}</select>
      </label>
      <div class="tx-checks" role="group" aria-label="Kept with each sale">
        ${check("tx-p-photo", "Photo of the certificate")} ${check("tx-p-number", "Certificate number")} ${check("tx-p-attest", "Signed statement")}
      </div>
      <label class="tx-field tx-field-wide">Note
        <input type="text" data-field="tx-p-notes" autocomplete="off" aria-label="Profile note" />
      </label>
    </div>
    <div class="tx-actions">
      <button type="button" class="btn btn-primary" data-action="tx-profile-save" ?disabled=${model.busy}>${editing ? "Save changes" : "Save the profile"}</button>
      <button type="button" class="btn btn-secondary" data-action="tx-profile-clear">Clear</button>
    </div>
    ${confirmBox(model, "profile")}
  `;
}

function profilesView(model: TaxModel): TemplateResult {
  return html`
    <div class="tx-split" data-tx-profiles>
      <div>
        ${queuePanel(model)}
        <section class="tx-panel" aria-label="Exemption profiles">
          <div class="tx-panel-head"><h3>Exemption profiles</h3></div>
          ${profileRows(model)}
        </section>
      </div>
      <section class="tx-panel" aria-label="Save an exemption profile" data-profile-form>
        <div class="tx-panel-head"><h3>${(model.form["tx-p-editing"] ?? "") !== "" ? html`Edit ${model.form["tx-p-editing"]}` : "Add a profile"}</h3></div>
        ${profileForm(model)}
      </section>
    </div>
  `;
}

// ---- certificates -------------------------------------------------------------------------------------

function profileName(model: TaxModel, e: TaxEvidence): string {
  const found = model.profiles.find((p) => p.id === (e.card_profile_id ?? ""));
  return found === undefined ? "No profile" : found.name;
}

function evidenceButtons(model: TaxModel, e: TaxEvidence): TemplateResult {
  const open = model.selected?.id === e.id;
  return html`<button type="button" class="tx-link" data-action="tx-evidence-open" data-id=${e.id} aria-pressed="${open ? "true" : "false"}" ?disabled=${model.busy}>Open the record</button>`;
}

function evidenceRows(model: TaxModel): TemplateResult {
  if (!model.evidenceLoaded) return html`<p class="tx-dim" role="status" data-loading-evidence>Reading the certificate records.</p>`;
  if (model.evidence.length === 0) {
    return empty("evidence", "No certificate is on file.", (model.form["tx-e-filter"] ?? "") === "" ? "Record one from the form beside this list when a buyer shows a certificate." : "None is on file for that order. Clear the filter to see them all.");
  }
  return html`
    <div class="tx-scroll tx-hide-narrow">
      <table aria-label="Certificate records">
        <thead><tr><th>Certificate</th><th>Order</th><th>Expires</th><th>Signed statement</th><th>Recorded</th><th></th></tr></thead>
        <tbody>
          ${model.evidence.map(
            (e) => html`<tr data-evidence-id=${e.id} aria-current="${model.selected?.id === e.id ? "true" : "false"}">
              <td><strong class="tx-mono">${e.number_mask}</strong><span class="tx-sub">${profileName(model, e)}</span></td>
              <td class="tx-mono tx-nowrap">${e.order_id === undefined || e.order_id === "" ? "No order" : e.order_id.slice(0, 8)}</td>
              <td class="tx-nowrap">${e.tcrd_expiry === undefined || e.tcrd_expiry === "" ? "No expiry" : dayWords(e.tcrd_expiry)}</td>
              <td>${e.attestation ? pill("good", "Signed") : pill("watch", "Not signed")}</td>
              <td class="tx-nowrap">${e.created_at === undefined ? "" : dayWords(e.created_at)}</td>
              <td>${evidenceButtons(model, e)}</td>
            </tr>`,
          )}
        </tbody>
      </table>
    </div>
    <ul class="tx-cards" aria-label="Certificate records, as cards">
      ${model.evidence.map(
        (e) => html`<li class="tx-card" data-evidence-card=${e.id} aria-current="${model.selected?.id === e.id ? "true" : "false"}">
          <div class="tx-card-top"><strong class="tx-mono">${e.number_mask}</strong>${e.attestation ? pill("good", "Signed") : pill("watch", "Not signed")}</div>
          <span class="tx-sub">${profileName(model, e)}</span>
          <dl class="tx-facts">
            <dt>Expires</dt><dd>${e.tcrd_expiry === undefined || e.tcrd_expiry === "" ? "No expiry" : dayWords(e.tcrd_expiry)}</dd>
            <dt>Recorded</dt><dd>${e.created_at === undefined ? "" : dayWords(e.created_at)}</dd>
          </dl>
          ${evidenceButtons(model, e)}
        </li>`,
      )}
    </ul>
  `;
}

function evidenceDetail(model: TaxModel): TemplateResult {
  const e = model.selected;
  if (e === null) return html`${nothing}`;
  const banner = attestationWords(e.attestation_status);
  const result = model.check;
  return html`
    <section class="tx-panel" aria-label="Certificate record" data-evidence-panel>
      <div class="tx-panel-head"><h3>Certificate ${e.number_mask}</h3><button type="button" class="tx-link" data-action="tx-evidence-close">Close</button></div>
      <dl class="tx-facts">
        <dt>Profile</dt><dd>${profileName(model, e)}</dd>
        <dt>Number kept</dt><dd class="tx-mono" data-mask>${e.number_mask}</dd>
        <dt>Order</dt><dd class="tx-mono">${e.order_id === undefined || e.order_id === "" ? "No order" : e.order_id}</dd>
        <dt>Photo kept at</dt><dd class="tx-mono">${e.photo_ref === undefined || e.photo_ref === "" ? "No photo" : e.photo_ref}</dd>
        <dt>Signed statement</dt><dd>${e.attestation ? "Signed" : "Not signed"}</dd>
        <dt>Expires</dt><dd>${e.tcrd_expiry === undefined || e.tcrd_expiry === "" ? "No expiry" : dayWords(e.tcrd_expiry)}</dd>
      </dl>
      ${banner === "" ? nothing : html`<p class="tx-note" data-attestation-note>${banner}</p>`}
      <p class="tx-dim">Only the last four digits are kept. The full number is never stored or shown. Opening, checking and downloading a record are each logged.</p>
      <div class="tx-form" role="group" aria-label="Check the certificate">
        <label class="tx-field">Check it as of
          <input type="text" inputmode="numeric" data-field="tx-e-asof" autocomplete="off" placeholder="Leave empty for today" aria-label="Check it as of" />
        </label>
      </div>
      <div class="tx-actions">
        <button type="button" class="btn btn-primary" data-action="tx-evidence-check" data-id=${e.id} ?disabled=${model.busy}>Check the certificate</button>
        <button type="button" class="btn btn-secondary" data-action="tx-evidence-download" data-id=${e.id} ?disabled=${model.busy}>Download the record</button>
      </div>
      ${result === null
        ? nothing
        : html`<div class="tx-alert ${result.eligible ? "tx-alert-ok" : "tx-alert-watch"}" role="status" data-check-result>
            ${result.eligible ? pill("good", "Can back a reduced rate") : pill("stop", "Cannot back a reduced rate")}
            <span>${result.eligible ? "The signed statement is on file and the certificate has not expired." : blockReasonWords(result.block_reason)}</span>
          </div>`}
      ${model.downloaded
        ? html`<p class="tx-alert tx-alert-ok" role="status" data-download-result>Download logged. The record carries the masked number only, and the photo is kept at ${e.photo_ref === undefined || e.photo_ref === "" ? "no address" : e.photo_ref}. Fetching the photo itself is ${later("later")}</p>`
        : nothing}
    </section>
  `;
}

function captureForm(model: TaxModel): TemplateResult {
  const profile = model.profiles.find((p) => p.code === (model.form["tx-c-profile"] ?? ""));
  return html`
    <div class="tx-form" role="group" aria-label="Record a certificate">
      <label class="tx-field tx-field-wide">Profile
        <select data-field="tx-c-profile" aria-label="Profile for the certificate">
          <option data-id="">Choose a profile</option>
          ${model.profiles.map((p) => html`<option data-id=${p.code}>${p.name}</option>`)}
        </select>
        <span class="tx-hint" data-capture-hint>${profile === undefined ? "The profile says what must be kept." : forcedEvidence(profile)}</span>
      </label>
      <label class="tx-field">Certificate number
        <input type="text" data-field="tx-c-number" data-private autocomplete="off" aria-label="Certificate number" />
        <span class="tx-hint">Only the last four digits are kept.</span>
      </label>
      <label class="tx-field">Order, if there is one
        <input type="text" data-field="tx-c-order" autocomplete="off" placeholder="Order id" aria-label="Order" />
      </label>
      <label class="tx-field">Photo kept at
        <input type="text" data-field="tx-c-photo" autocomplete="off" placeholder="Storage path" aria-label="Photo kept at" />
        <span class="tx-hint">Uploading the photo from here is ${later("later")}</span>
      </label>
      <label class="tx-field">Certificate expires
        <input type="text" inputmode="numeric" data-field="tx-c-expiry" autocomplete="off" placeholder="Year-month-day" aria-label="Certificate expires" />
      </label>
      <div class="tx-checks">${check("tx-c-attest", "The buyer signed the statement")}</div>
    </div>
    <div class="tx-actions">
      <button type="button" class="btn btn-primary" data-action="tx-capture" ?disabled=${model.busy}>Record the certificate</button>
    </div>
  `;
}

function certificatesView(model: TaxModel): TemplateResult {
  return html`
    <div class="tx-split" data-tx-certificates>
      <div>
        <section class="tx-panel" aria-label="Certificate records">
          <div class="tx-panel-head"><h3>Certificates on file</h3></div>
          <div class="tx-toolbar">
            <label class="tx-field">Only this order
              <input type="text" data-field="tx-e-filter" autocomplete="off" placeholder="Order id" aria-label="Only this order" />
            </label>
            <button type="button" class="btn btn-secondary" data-action="tx-evidence-filter" ?disabled=${model.busy}>Filter</button>
            <button type="button" class="btn btn-secondary" data-action="tx-evidence-unfilter">Clear</button>
          </div>
          ${evidenceRows(model)}
        </section>
        ${evidenceDetail(model)}
      </div>
      <section class="tx-panel" aria-label="Record a certificate" data-capture>
        <div class="tx-panel-head"><h3>Record a certificate</h3></div>
        ${captureForm(model)}
      </section>
    </div>
  `;
}

// ---- settings -----------------------------------------------------------------------------------------

function settingsView(model: TaxModel): TemplateResult {
  const mode = model.form["tx-s-mode"] ?? model.config?.round_mode ?? "INVOICE_TOTAL";
  return html`
    <section class="tx-panel" aria-label="Tax settings" data-tx-settings>
      <div class="tx-panel-head"><h3>Tax settings</h3></div>
      <p class="tx-note">These settings are company wide. A change here does not touch past sales: recomputing them is a separate step.</p>
      <div class="tx-form" role="group" aria-label="Tax settings">
        <label class="tx-field tx-field-wide">Seller's permit number
          <input type="text" data-field="tx-s-permit" autocomplete="off" aria-label="Seller's permit number" />
          <span class="tx-hint">Printed on invoices.</span>
        </label>
      </div>
      <div class="tx-choices" role="radiogroup" aria-label="How tax is rounded">
        ${ROUND_MODES.map(
          (m) => html`<button type="button" class="tx-choice" role="radio" aria-checked="${mode === m ? "true" : "false"}" data-action="tx-round" data-mode=${m}>
            <span class="tx-choice-title">${roundModeWord(m)}</span><span class="tx-choice-line">${roundModeLine(m)}</span>
          </button>`,
        )}
      </div>
      <div class="tx-form" role="group" aria-label="Note">
        <label class="tx-field tx-field-wide">Note
          <input type="text" data-field="tx-s-notes" autocomplete="off" aria-label="Settings note" />
        </label>
      </div>
      <ul class="tx-list" aria-label="Switches that are not available yet">
        <li class="tx-card"><div class="tx-card-top"><strong>Automatic tax lookup</strong>${later("later")}</div><span class="tx-sub">Off. The core does not allow it to be turned on yet.</span></li>
        <li class="tx-card"><div class="tx-card-top"><strong>Postal code routing</strong>${later("later")}</div><span class="tx-sub">Off. The core does not allow it to be turned on yet.</span></li>
      </ul>
      <div class="tx-actions">
        <button type="button" class="btn btn-primary" data-action="tx-config-save" ?disabled=${model.busy || model.config === null}>Save the settings</button>
      </div>
      ${confirmBox(model, "config")}
    </section>
  `;
}

// ---- recompute and reconcile --------------------------------------------------------------------------

function reconResult(r: TaxReconReport | null): TemplateResult {
  if (r === null) return html`${nothing}`;
  const v = varianceWords(r.variance_cents);
  return html`
    <div data-recon-result>
      <div class="tx-stats">
        <div class="tx-stat"><span class="tx-stat-label">Collected by sales</span><span class="tx-stat-figure" data-collected>${formatMoney(r.collected_cents)}</span></div>
        <div class="tx-stat"><span class="tx-stat-label">Refunded on returns</span><span class="tx-stat-figure" data-return-tax>${formatMoney(r.return_tax_cents)}</span></div>
        <div class="tx-stat"><span class="tx-stat-label">Input tax credits on vendor bills</span><span class="tx-stat-figure" data-input-tax>${formatMoney(r.input_tax_cents)}</span></div>
        <div class="tx-stat"><span class="tx-stat-label">The ledger's month movement on sales tax payable</span><span class="tx-stat-figure" data-ledger>${formatMoney(r.gl_payable_cents)}</span></div>
        <div class="tx-stat"><span class="tx-stat-label">Difference</span><span class="tx-stat-figure">${pill(v.tone, v.word)}</span></div>
      </div>
      <p class="tx-alert ${v.tone === "good" ? "tx-alert-ok" : "tx-alert-error"}" role="status" data-recon-words>${periodWords(r.year, r.month)}: ${v.detail}</p>
      ${r.gl_federal_cents === 0 && r.gl_ontario_cents === 0
        ? nothing
        : html`<dl class="tx-facts" aria-label="Other tax accounts">
            ${r.gl_federal_cents === 0 ? nothing : html`<dt>Ledger account 2201</dt><dd>${formatMoney(r.gl_federal_cents)}</dd>`}
            ${r.gl_ontario_cents === 0 ? nothing : html`<dt>Ledger account 2202</dt><dd>${formatMoney(r.gl_ontario_cents)}</dd>`}
          </dl>`}
    </div>
  `;
}

function recomputeResult(r: TaxRecomputeResult | null): TemplateResult {
  if (r === null) return html`${nothing}`;
  return html`
    <div class="tx-alert ${r.inserted === 0 ? "tx-alert-watch" : "tx-alert-ok"}" role="status" data-recompute-result>
      <strong>${r.inserted === 0 ? "Nothing new: every sale in range was already recomputed." : "Recomputed."}</strong>
      <dl class="tx-facts" aria-label="Recompute result">
        <dt>Sales read</dt><dd data-scanned>${r.scanned}</dd>
        <dt>Records added</dt><dd data-inserted>${r.inserted}</dd>
        <dt>Already done</dt><dd data-skipped>${r.skipped_exists}</dd>
      </dl>
    </div>
  `;
}

function checksView(model: TaxModel): TemplateResult {
  const typedYear = (model.form["tx-r-year"] ?? "").trim();
  return html`
    <div class="tx-split" data-tx-checks>
      <section class="tx-panel" aria-label="Reconcile to the ledger" data-reconcile>
        <div class="tx-panel-head"><h3>Reconcile to the ledger</h3></div>
        <p class="tx-note">Compares a month's documents with the ledger for the same month: the sales tax the invoices collected, the input tax credits on posted vendor bills shown apart, the two netted against the sales tax payable account's movement.</p>
        <div class="tx-form" role="group" aria-label="Period to reconcile">
          <label class="tx-field">Month
            <select data-field="tx-r-month" aria-label="Month">
              <option data-id="0">Whole year</option>
              ${MONTH_NAMES.map((name, i) => html`<option data-id=${String(i + 1)}>${name}</option>`)}
            </select>
          </label>
          <label class="tx-field">Year
            <input type="text" inputmode="numeric" data-field="tx-r-year" autocomplete="off" aria-label="Year" />
            ${typedYear !== "" && !/^\d{4}$/.test(typedYear) ? html`<span class="tx-hint tx-hint-bad" role="alert" data-year-hint>Enter a four digit year.</span>` : nothing}
          </label>
        </div>
        <div class="tx-actions">
          <button type="button" class="btn btn-primary" data-action="tx-recon-run" ?disabled=${model.busy}>Reconcile</button>
        </div>
        ${reconResult(model.recon)}
      </section>
      <section class="tx-panel" aria-label="Recompute past sales" data-recompute>
        <div class="tx-panel-head"><h3>Recompute past sales</h3></div>
        <p class="tx-note">Adds a second, audit only tax record for each sale in the range that has none. Nothing is rewritten or removed, and the ledger is not corrected. Running it again adds nothing new.</p>
        <div class="tx-form" role="group" aria-label="Range to recompute">
          <label class="tx-field">From
            <input type="text" inputmode="numeric" data-field="tx-c-from" autocomplete="off" placeholder="Year-month-day" aria-label="Recompute from" />
          </label>
          <label class="tx-field">To
            <input type="text" inputmode="numeric" data-field="tx-c-to" autocomplete="off" placeholder="Year-month-day" aria-label="Recompute to" />
          </label>
        </div>
        <div class="tx-actions">
          <button type="button" class="btn btn-primary" data-action="tx-recompute" ?disabled=${model.busy}>Recompute</button>
        </div>
        ${confirmBox(model, "recompute")}
        ${recomputeResult(model.recompute)}
      </section>
    </div>
  `;
}

// ---- the whole desk -----------------------------------------------------------------------------------

export function renderTax(model: TaxModel): TemplateResult {
  const head = html`
    <header class="tx-head">
      <h1>Tax</h1>
      <p class="tx-dim">The sales tax rates, the exemption profiles and the certificates behind them, the settings, and the checks against the ledger. California sales tax; US dollars.</p>
    </header>
  `;
  if (model.phase === "loading") return html`${head}${loading()}`;
  if (model.phase === "error") return html`${head}${loadFailed(model)}`;
  return html`
    ${head}
    ${tabs(model)}
    ${messages(model)}
    ${model.tab === "rates" ? ratesView(model) : nothing}
    ${model.tab === "profiles" ? profilesView(model) : nothing}
    ${model.tab === "certificates" ? certificatesView(model) : nothing}
    ${model.tab === "settings" ? settingsView(model) : nothing}
    ${model.tab === "checks" ? checksView(model) : nothing}
  `;
}
