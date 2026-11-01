// The liens slice's screens, as plain render functions of a model. They hold no state and own no events: every
// control is a plain element carrying a data-action name and the slice's click listener acts on it (the design
// system's position rules allow no @ or . binding in an app, and the only bound values are class, role, aria- and
// data- attributes). Form fields carry data-field names and are read when the button is pressed; none is bound.
//
// Every figure on a screen is the server's, formatted: the app computes no money. References: the Money Desk
// boards (cards border first, no shadow, status as a word before a colour, a table that gives way to cards on a
// phone), and the original app's Lien Cockpit tabs (triage, jobs, holdbacks, documents).
import { html, nothing, type TemplateResult } from "lit";
import { servedConsoleConfig } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { demoNote } from "../band-identity.js";
import type { BillingHoldbackBranchDue, Branch, IdentityStaffMember, LienAgingRow, LienDocument, LienJob, LienTriageItem } from "@hardscapehouse/api-client";
import { dueWords, type BannerView } from "../compliance/compliance-logic.js";
import { bannerStrip, branchName, empty, loadFailed, loading, messages, pill, tabsNav } from "./lien-kit.js";
import {
  ageWords,
  badgeTone,
  badgeWord,
  bucketWord,
  cosigners,
  directionLine,
  directionWord,
  documentStatusWord,
  type DueGroup,
  documentTone,
  dueConfirmWords,
  dueStateWords,
  dueTone,
  heldNowWords,
  holdingWords,
  jobStatusTone,
  jobStatusWord,
  noteWords,
  READ_ONLY_LIENS,
  refusalWords,
  releaseConfirmWords,
  releaseStatusWord,
  templateFields,
  templateWord,
  TEMPLATE_KINDS,
} from "./lien-logic.js";
import { bpsWords, formatMoney } from "./lien-money.js";

export type LienTab = "triage" | "due" | "jobs" | "holdbacks" | "documents";

/** The due to release list as the core answered it: the customer jobs grouped, and the due totals by branch. */
export interface DueModel {
  readonly phase: "ready" | "error";
  readonly groups: readonly DueGroup[];
  readonly totals: readonly BillingHoldbackBranchDue[];
  readonly error: string;
}

export interface ReleaseModel {
  readonly jobId: string;
  readonly roster: "loading" | "ready" | "refused";
  readonly people: readonly IdentityStaffMember[];
  readonly rosterError: string;
  readonly cosigner: string;
}

/** One invoice's release made on this visit, kept so it can be named and reversed. */
export interface ReleasedItem {
  readonly releaseId: string;
  readonly invoiceNo: string;
  readonly amountCents: number;
  readonly label: string;
  readonly reversed: boolean;
}

export interface LiensModel {
  readonly phase: "loading" | "ready" | "error";
  readonly tab: LienTab;
  readonly canRelease: boolean;
  readonly canWrite: boolean;
  readonly userId: string;
  readonly banner: BannerView;
  readonly bannerState: string;
  readonly jobs: readonly LienJob[];
  readonly triage: readonly LienTriageItem[];
  readonly aging: readonly LienAgingRow[];
  readonly due: DueModel;
  readonly canReleaseDue: boolean;
  readonly dueOpen: DueGroup | null;
  readonly released: readonly ReleasedItem[];
  readonly reversing: ReleasedItem | null;
  readonly branches: readonly Branch[];
  readonly selectedJob: string | null;
  readonly newJob: boolean;
  readonly docJob: string;
  readonly docs: readonly LienDocument[];
  readonly docsPhase: "idle" | "loading" | "ready" | "error";
  readonly docKind: string;
  readonly viewing: LienDocument | null;
  readonly versions: readonly LienDocument[] | null;
  readonly release: ReleaseModel | null;
  readonly jobLevelRefused: boolean;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
}

const jobNoOf = (jobs: readonly LienJob[], id: string): string => jobs.find((j) => j.id === id)?.job_no ?? "";

function agingOf(model: LiensModel, jobId: string): LienAgingRow | undefined {
  return model.aging.find((r) => r.job_id === jobId);
}

function stamp(iso: string | undefined): string {
  return iso === undefined || iso === "" ? "" : dueWords(iso.slice(0, 10));
}

// ---- the tabs ---------------------------------------------------------------------------------------

function tabs(model: LiensModel): TemplateResult {
  return tabsNav("Lien views", "li-tab", model.tab, [
    { id: "triage", label: "Triage", count: model.triage.length === 0 ? null : model.triage.length },
    { id: "due", label: "Due to release", count: model.due.groups.filter((g) => g.due).length === 0 ? null : model.due.groups.filter((g) => g.due).length },
    { id: "jobs", label: "Jobs", count: null },
    { id: "holdbacks", label: "Holdbacks", count: null },
    { id: "documents", label: "Documents", count: null },
  ]);
}

// ---- triage -----------------------------------------------------------------------------------------

function triageRow(model: LiensModel, t: LienTriageItem): TemplateResult {
  return html`
    <tr data-row="triage" data-job-no=${t.job_no}>
      <td><strong class="li-mono">${t.job_no}</strong><span class="li-sub">${directionWord(t.holdback_direction)}</span></td>
      <td>${branchName(model.branches, t.branch_id)}</td>
      <td>${pill(badgeTone(t.badge), badgeWord(t.badge))}</td>
      <td class="num">${formatMoney(t.outstanding_cents)}</td>
      <td class="num">${ageWords(t.oldest_days)}</td>
      <td>${noteWords(t.note)}</td>
      <td><button type="button" class="li-link" data-action="li-open-job" data-id=${t.job_id}>Open job</button></td>
    </tr>
  `;
}

function triageCard(model: LiensModel, t: LienTriageItem): TemplateResult {
  return html`
    <li class="li-card" data-row="triage" data-job-no=${t.job_no}>
      <div class="li-card-top"><strong class="li-mono">${t.job_no}</strong>${pill(badgeTone(t.badge), badgeWord(t.badge))}</div>
      <div class="li-card-amount">${formatMoney(t.outstanding_cents)} <span class="li-dim">· ${ageWords(t.oldest_days)} · ${directionWord(t.holdback_direction)}</span></div>
      <span class="li-sub">${branchName(model.branches, t.branch_id)}</span>
      <span>${noteWords(t.note)}</span>
      <button type="button" class="li-link" data-action="li-open-job" data-id=${t.job_id}>Open job</button>
    </li>
  `;
}

function triage(model: LiensModel): TemplateResult {
  const rows = model.triage;
  return html`
    <section class="li-panel">
      <div class="li-panel-head">
        <h2>Monday triage</h2>
        <span class="li-summary" data-triage-count>${holdingWords(rows.length)}</span>
      </div>
      <p class="li-dim">The holdback worklist: what needs attention, oldest first.</p>
      ${rows.length === 0
        ? empty("triage", "Nothing on the worklist", "Every held retention is inside its window or released.")
        : html`
            <div class="li-scroll li-hide-narrow">
              <table aria-label="Monday triage">
                <thead><tr><th>Job</th><th>Branch</th><th>Status</th><th class="num">Held</th><th class="num">Oldest</th><th>What the core says</th><th></th></tr></thead>
                <tbody>${rows.map((t) => triageRow(model, t))}</tbody>
              </table>
            </div>
            <ul class="li-cards">${rows.map((t) => triageCard(model, t))}</ul>
          `}
    </section>
  `;
}

// ---- due to release ---------------------------------------------------------------------------------

function dueInvoices(g: DueGroup): TemplateResult {
  return html`<ul class="li-invoices">${g.rows.map((r) => html`<li data-invoice-no=${r.invoice_no}><span class="li-mono">${r.invoice_no}</span> <span class="li-amt">${formatMoney(r.holdback_cents)}</span></li>`)}</ul>`;
}

function dueState(g: DueGroup): TemplateResult {
  return html`
    <div data-due-state>${pill(dueTone(g), dueStateWords(g))}</div>
    ${g.due || g.reason === "" ? nothing : html`<span class="li-sub" data-due-reason>${noteWords(g.reason)}</span>`}
  `;
}

const lastDayWords = (g: DueGroup): string => (g.lastDay === "" ? "No date" : dueWords(g.lastDay));

function dueRow(model: LiensModel, g: DueGroup): TemplateResult {
  return html`
    <tr data-row="due" data-customer-job=${g.customerJobId} data-lien-job=${g.lienJobNo}>
      <td><strong class="li-mono">${g.lienJobNo === "" ? "No lien job" : g.lienJobNo}</strong><span class="li-sub">${branchName(model.branches, g.branchId)}</span></td>
      <td>${dueInvoices(g)}</td>
      <td class="li-nowrap" data-last-day>${lastDayWords(g)}</td>
      <td>${dueState(g)}</td>
      <td>${dueAction(model, g)}</td>
    </tr>
  `;
}

function dueCard(model: LiensModel, g: DueGroup): TemplateResult {
  return html`
    <li class="li-card" data-row="due" data-customer-job=${g.customerJobId} data-lien-job=${g.lienJobNo}>
      <div class="li-card-top"><strong class="li-mono">${g.lienJobNo === "" ? "No lien job" : g.lienJobNo}</strong>${pill(dueTone(g), dueStateWords(g))}</div>
      ${dueInvoices(g)}
      <span class="li-sub">${branchName(model.branches, g.branchId)} · Last day to preserve: <span data-last-day>${lastDayWords(g)}</span></span>
      ${g.due || g.reason === "" ? nothing : html`<span class="li-sub" data-due-reason>${noteWords(g.reason)}</span>`}
      ${dueAction(model, g)}
    </li>
  `;
}

function dueTotals(totals: readonly BillingHoldbackBranchDue[], branches: readonly Branch[]): TemplateResult {
  const rows = totals.filter((t) => t.due_cents > 0);
  return html`<p class="li-summary" data-due-total>${rows.length === 0
    ? "Nothing is due to release now."
    : rows.map((t, i) => html`${i === 0 ? "" : "; "}${formatMoney(t.due_cents)} due now${branchName(branches, t.branch_id) === "" ? "" : html` at ${branchName(branches, t.branch_id)}`}`)}</p>`;
}

function dueTab(model: LiensModel): TemplateResult {
  const due = model.due;
  return html`
    <div class="li-due">
      <section class="li-panel">
        <div class="li-panel-head"><h2>Due to release</h2></div>
        <p class="li-dim">Each customer job's held invoices, the lien job that governs them, the last day a lien can be preserved and where the holdback stands.</p>
        ${model.canReleaseDue ? nothing : html`<p class="li-note" data-due-note>${refusalWords("due-release", "", 403)}</p>`}
        ${due.phase === "error"
          ? loadFailed(due.error, "li-reload")
          : html`
              ${dueTotals(due.totals, model.branches)}
              ${due.groups.length === 0
                ? empty("due", "No holdback is held on an invoice", "Everything held has been released, or nothing is held yet.")
                : html`
                    <div class="li-scroll li-hide-narrow">
                      <table aria-label="Due to release">
                        <thead><tr><th>Lien job</th><th>Invoices</th><th>Last day to preserve</th><th>State</th><th></th></tr></thead>
                        <tbody>${due.groups.map((g) => dueRow(model, g))}</tbody>
                      </table>
                    </div>
                    <ul class="li-cards">${due.groups.map((g) => dueCard(model, g))}</ul>
                  `}
            `}
      </section>
      ${model.dueOpen === null ? nothing : duePanel(model, model.dueOpen)}${model.reversing === null ? nothing : reversePanel(model, model.reversing)}${releasedList(model)}
    </div>
  `;
}

function dueAction(model: LiensModel, g: DueGroup): TemplateResult | typeof nothing {
  if (!g.due || !model.canReleaseDue) return nothing;
  const open = model.dueOpen?.customerJobId === g.customerJobId;
  return html`<button type="button" class="btn btn-secondary" data-action="li-due-open" data-id=${g.customerJobId} aria-pressed="${open ? "true" : "false"}">Release</button>`;
}

/** The question first: the customer job, each invoice and its amount; the core releases the whole holdback of the job at once. */
function duePanel(model: LiensModel, g: DueGroup): TemplateResult {
  return html`
    <section class="li-panel" data-due-panel tabindex="-1" role="region" aria-label="Release holdback">
      <div class="li-panel-head"><h2>Release holdback</h2></div>
      <p class="li-confirm" data-confirm-words>${dueConfirmWords(g)}</p>
      <div class="li-actions">
        <button type="button" class="btn btn-primary" data-action="li-due-confirm" ?disabled=${model.busy}>Confirm release</button>
        <button type="button" class="btn btn-secondary" data-action="li-due-close">Cancel</button>
      </div>
    </section>
  `;
}

/** The reversal asks first, with the invoice and the amount, and will not go without a reason. */
function reversePanel(model: LiensModel, r: ReleasedItem): TemplateResult {
  return html`
    <section class="li-panel" data-reverse-panel tabindex="-1" role="region" aria-label="Reverse release">
      <div class="li-panel-head"><h2>Reverse release</h2></div>
      <dl class="li-facts">
        <dt>Invoice</dt><dd>${r.invoiceNo}</dd>
        <dt>Released</dt><dd>${formatMoney(r.amountCents)} on ${r.label}</dd>
      </dl>
      <p class="li-confirm" data-confirm-words>Reverse the release of ${r.invoiceNo}. ${formatMoney(r.amountCents)} goes back to being held on the invoice and a reversing journal is posted to the ledger.</p>
      <label class="li-field">Reason<textarea data-field="reverse_reason" aria-label="Reason" rows="3"></textarea></label>
      <div class="li-actions">
        <button type="button" class="btn btn-primary" data-action="li-reverse-confirm" ?disabled=${model.busy}>Reverse release</button>
        <button type="button" class="btn btn-secondary" data-action="li-reverse-close">Cancel</button>
      </div>
    </section>
  `;
}

function releasedRow(model: LiensModel, r: ReleasedItem): TemplateResult {
  return html`
    <li class="li-card" data-row="released" data-invoice-no=${r.invoiceNo} data-release-id=${r.releaseId}>
      <div class="li-card-top"><strong class="li-mono">${r.invoiceNo}</strong>${r.reversed ? pill("plain", "Reversed") : pill("good", "Released")}</div>
      <div class="li-card-amount">${formatMoney(r.amountCents)} <span class="li-dim">· ${r.label}</span></div>
      ${r.reversed || !model.canReleaseDue ? nothing : html`<button type="button" class="btn btn-secondary" data-action="li-reverse-open" data-id=${r.releaseId}>Reverse</button>`}
    </li>
  `;
}

function releasedList(model: LiensModel): TemplateResult | typeof nothing {
  if (model.released.length === 0) return nothing;
  return html`
    <section class="li-panel" data-released>
      <div class="li-panel-head"><h2>Released on this visit</h2></div>
      <p class="li-dim">Each release the core made for you since this page opened, one row per invoice.</p>
      <ul class="li-cards li-cards-always">${model.released.map((r) => releasedRow(model, r))}</ul>
    </section>
  `;
}

// ---- jobs -------------------------------------------------------------------------------------------

function jobRow(model: LiensModel, j: LienJob): TemplateResult {
  const selected = model.selectedJob === j.id;
  return html`
    <tr data-row="job" data-job-no=${j.job_no} aria-current="${selected ? "true" : "false"}">
      <td><strong class="li-mono">${j.job_no}</strong><span class="li-sub">${branchName(model.branches, j.branch_id)} · ${directionWord(j.holdback_direction)}</span></td>
      <td class="num">${j.contract_cents === 0 ? "Not set" : formatMoney(j.contract_cents)}</td>
      <td>${pill(jobStatusTone(j.status), jobStatusWord(j.status))}</td>
      <td><button type="button" class="li-link" data-action="li-select-job" data-id=${j.id} aria-pressed="${selected ? "true" : "false"}">Open</button></td>
    </tr>
  `;
}

function jobCard(model: LiensModel, j: LienJob): TemplateResult {
  const selected = model.selectedJob === j.id;
  return html`
    <li class="li-card" data-row="job" data-job-no=${j.job_no} aria-current="${selected ? "true" : "false"}">
      <div class="li-card-top"><strong class="li-mono">${j.job_no}</strong>${pill(jobStatusTone(j.status), jobStatusWord(j.status))}</div>
      <span>${j.description}</span>
      <div class="li-card-amount">${j.contract_cents === 0 ? "Contract not set" : formatMoney(j.contract_cents)} <span class="li-dim">· ${directionWord(j.holdback_direction)}</span></div>
      <span class="li-sub">${branchName(model.branches, j.branch_id)}</span>
      <button type="button" class="li-link" data-action="li-select-job" data-id=${j.id} aria-pressed="${selected ? "true" : "false"}">Open</button>
    </li>
  `;
}

function jobForm(model: LiensModel): TemplateResult {
  return html`
    <section class="li-panel" data-job-form>
      <div class="li-panel-head"><h2>New job</h2></div>
      <div class="li-form">
        <label class="li-field">Job number<input data-field="job_no" aria-label="Job number" autocomplete="off" /></label>
        <label class="li-field">Branch
          <select data-field="branch_id" aria-label="Branch"><option data-id="">Pick a branch</option>${model.branches.map((b) => html`<option data-id=${b.id}>${b.name}</option>`)}</select>
        </label>
        <label class="li-field li-field-wide">Description<input data-field="description" aria-label="Description" autocomplete="off" /></label>
        <label class="li-field">Contract amount<input data-field="contract" aria-label="Contract amount" inputmode="decimal" autocomplete="off" /></label>
        <label class="li-field">Holdback percent<input data-field="percent" aria-label="Holdback percent" inputmode="decimal" autocomplete="off" /></label>
        <label class="li-field">Direction
          <select data-field="direction" aria-label="Direction"><option data-id="RECEIVABLE">Receivable</option><option data-id="PAYABLE">Payable</option></select>
        </label>
        <label class="li-field">Substantial performance date<input type="date" data-field="perf" aria-label="Substantial performance date" /></label>
        <label class="li-field">Last supply date<input type="date" data-field="supply" aria-label="Last supply date" /></label>
      </div>
      <p class="li-hint">A blank percent is the core's own default. With no customer on the job, the sync reads every held invoice at the branch.</p>
      <div class="li-actions">
        <button type="button" class="btn btn-primary" data-action="li-create-job" ?disabled=${model.busy}>Create job</button>
        <button type="button" class="btn btn-secondary" data-action="li-cancel-job">Cancel</button>
      </div>
    </section>
  `;
}

/** What the core holds about a preserved lien on the job, in a sentence. */
function lienStateWords(job: LienJob): string {
  if (job.lien_preserved_on === undefined) return "No lien is recorded on this job.";
  const note = job.lien_preserved_note === undefined || job.lien_preserved_note === "" ? "" : ` ${job.lien_preserved_note}.`;
  if (job.lien_discharged_on === undefined) return `A lien was preserved on ${dueWords(job.lien_preserved_on)}.${note} It has not been discharged, so the holdback stays held.`;
  return `A lien was preserved on ${dueWords(job.lien_preserved_on)} and discharged on ${dueWords(job.lien_discharged_on)}.${note}`;
}

/** The doors: record or discharge a preserved lien, and correct the three dates the preservation period counts from. Each is recorded by the core. */
function doors(model: LiensModel, job: LienJob): TemplateResult {
  const active = job.lien_preserved_on !== undefined && job.lien_discharged_on === undefined;
  return html`
    <div class="li-doors" data-doors>
      <h3>Preservation</h3>
      <p class="li-fact" data-lien-state>${lienStateWords(job)}</p>
      ${active
        ? html`
            <div class="li-form">
              <label class="li-field">Discharged on<input type="date" data-field="discharge_day" aria-label="Discharged on" /></label>
              <label class="li-field">Note<input data-field="discharge_note" aria-label="Note" autocomplete="off" /></label>
            </div>
            <div class="li-actions"><button type="button" class="btn btn-secondary" data-action="li-lien-discharge" ?disabled=${model.busy}>Record discharge</button></div>
          `
        : html`
            <div class="li-form">
              <label class="li-field">Lien preserved on<input type="date" data-field="lien_day" aria-label="Lien preserved on" /></label>
              <label class="li-field">Note<input data-field="lien_note" aria-label="Note" autocomplete="off" /></label>
            </div>
            <div class="li-actions"><button type="button" class="btn btn-secondary" data-action="li-lien-record" ?disabled=${model.busy}>Record preserved lien</button></div>
          `}
      <h3>Dates the period counts from</h3>
      <p class="li-hint">The latest of these starts the preservation period. Clear a box to remove its date.</p>
      <div class="li-form">
        <label class="li-field">Certificate published on<input type="date" data-field="dates_csp" aria-label="Certificate published on" /></label>
        <label class="li-field">Substantial performance date<input type="date" data-field="dates_perf" aria-label="Substantial performance date" /></label>
        <label class="li-field">Last supply date<input type="date" data-field="dates_supply" aria-label="Last supply date" /></label>
      </div>
      <div class="li-actions"><button type="button" class="btn btn-secondary" data-action="li-dates-save" ?disabled=${model.busy}>Save dates</button></div>
    </div>
  `;
}

function jobPanel(model: LiensModel, job: LienJob): TemplateResult {
  const held = agingOf(model, job.id);
  return html`
    <section class="li-panel" data-job-panel>
      <div class="li-panel-head"><h2>${job.job_no}</h2>${pill(jobStatusTone(job.status), jobStatusWord(job.status))}</div>
      <p class="li-fact">${job.description}</p>
      <dl class="li-facts">
        <dt>Branch</dt><dd>${branchName(model.branches, job.branch_id)}</dd>
        <dt>Holdback</dt><dd>${directionWord(job.holdback_direction)}. ${directionLine(job.holdback_direction)}</dd>
        <dt>Contract</dt><dd>${job.contract_cents === 0 ? "Not set" : formatMoney(job.contract_cents)}</dd>
        <dt>Held back</dt><dd>${bpsWords(job.holdback_pct_bps)}</dd>
        <dt>Certificate published</dt><dd>${job.csp_published_on === undefined ? "Not set" : dueWords(job.csp_published_on)}</dd>
        <dt>Substantial performance</dt><dd>${job.substantial_perf_date === undefined ? "Not set" : dueWords(job.substantial_perf_date)}</dd>
        <dt>Last supply</dt><dd>${job.last_supply_date === undefined ? "Not set" : dueWords(job.last_supply_date)}</dd>
        <dt>Held now</dt><dd data-held-now>${heldNowWords(held === undefined ? null : formatMoney(held.outstanding_cents), held?.oldest_days ?? 0)}</dd>
      </dl>
      <div class="li-actions">
        ${model.canWrite ? html`<button type="button" class="btn btn-primary" data-action="li-sync" ?disabled=${model.busy}>Sync holdbacks</button>` : nothing}
        <button type="button" class="btn btn-secondary" data-action="li-go-docs" data-id=${job.id}>Documents</button>
        ${held !== undefined && held.holdback_direction === "RECEIVABLE"
          ? html`<button type="button" class="btn btn-secondary" data-action="li-release-open" data-id=${job.id}>Release</button>`
          : nothing}
      </div>
      ${model.canWrite ? doors(model, job) : nothing}
    </section>
  `;
}

function jobs(model: LiensModel): TemplateResult {
  const selected = model.jobs.find((j) => j.id === model.selectedJob);
  return html`
    <div class="li-split">
      <section class="li-panel">
        <div class="li-panel-head">
          <h2>Jobs</h2>
          ${model.canWrite ? html`<button type="button" class="btn btn-secondary" data-action="li-new-job">New job</button>` : nothing}
        </div>
        <p class="li-dim">The jobs a holdback and its release attach to, newest first.</p>
        ${model.canWrite ? nothing : html`<p class="li-note" data-read-only>${READ_ONLY_LIENS}</p>`}
        ${model.jobs.length === 0
          ? empty("jobs", "No jobs yet", "Create the first job to start recording its holdback.")
          : html`
              <div class="li-scroll li-hide-narrow">
                <table aria-label="Jobs">
                  <thead><tr><th>Job</th><th class="num">Contract</th><th>Status</th><th></th></tr></thead>
                  <tbody>${model.jobs.map((j) => jobRow(model, j))}</tbody>
                </table>
              </div>
              <ul class="li-cards">${model.jobs.map((j) => jobCard(model, j))}</ul>
            `}
      </section>
      <div>
        ${model.newJob && model.canWrite
          ? jobForm(model)
          : selected !== undefined
            ? jobPanel(model, selected)
            : html`<section class="li-panel" data-job-hint><p class="li-dim">Open a job to see its facts, sync its holdbacks and reach its documents.</p></section>`}
      </div>
    </div>
  `;
}

// ---- holdbacks and the release ----------------------------------------------------------------------

function agingRow(model: LiensModel, r: LienAgingRow): TemplateResult {
  const open = model.release?.jobId === r.job_id;
  const status = releaseStatusWord(r.holdback_direction, r.release_eligible);
  return html`
    <tr data-row="aging" data-job-no=${r.job_no} aria-current="${open ? "true" : "false"}">
      <td><strong class="li-mono">${r.job_no}</strong><span class="li-sub">${branchName(model.branches, r.branch_id)} · ${directionWord(r.holdback_direction)}</span></td>
      <td class="num">${formatMoney(r.outstanding_cents)}</td>
      <td class="num li-nowrap">${ageWords(r.oldest_days)}<span class="li-sub">${bucketWord(r.bucket)}</span></td>
      <td>
        <div>${pill(r.release_eligible ? "watch" : "plain", status)}</div>
        ${r.holdback_direction === "RECEIVABLE" ? html`<button type="button" class="li-link" data-action="li-release-open" data-id=${r.job_id} aria-pressed="${open ? "true" : "false"}">Release</button>` : nothing}
      </td>
    </tr>
  `;
}

function agingCard(model: LiensModel, r: LienAgingRow): TemplateResult {
  const open = model.release?.jobId === r.job_id;
  const status = releaseStatusWord(r.holdback_direction, r.release_eligible);
  return html`
    <li class="li-card" data-row="aging" data-job-no=${r.job_no} aria-current="${open ? "true" : "false"}">
      <div class="li-card-top"><strong class="li-mono">${r.job_no}</strong>${pill(r.release_eligible ? "watch" : "plain", status)}</div>
      <div class="li-card-amount">${formatMoney(r.outstanding_cents)} <span class="li-dim">· ${ageWords(r.oldest_days)} · ${bucketWord(r.bucket)}</span></div>
      <span class="li-sub">${branchName(model.branches, r.branch_id)} · ${directionWord(r.holdback_direction)}</span>
      ${r.holdback_direction === "RECEIVABLE" ? html`<button type="button" class="li-link" data-action="li-release-open" data-id=${r.job_id} aria-pressed="${open ? "true" : "false"}">Release</button>` : nothing}
    </li>
  `;
}

function releasePanel(model: LiensModel, release: ReleaseModel): TemplateResult {
  const row = agingOf(model, release.jobId);
  const jobNo = row?.job_no ?? jobNoOf(model.jobs, release.jobId);
  const amount = row === undefined ? "" : formatMoney(row.outstanding_cents);
  const people = cosigners(release.people, model.userId);
  return html`
    <section class="li-panel" data-release-panel tabindex="-1" role="region" aria-label="Release holdback">
      <div class="li-panel-head"><h2>Release holdback</h2></div>
      ${model.jobLevelRefused
        ? html`
            <div class="li-note" data-job-level-refused>
              <p>This job's holdback is held on its invoices. It is released from Due to release, by the Financial Controller, once the preservation period has run out.</p>
              <button type="button" class="btn btn-secondary" data-action="li-go-due">Go to Due to release</button>
            </div>
          `
        : nothing}
      <dl class="li-facts">
        <dt>Job</dt><dd>${jobNo}</dd>
        <dt>Held</dt><dd>${amount === "" ? "Nothing held now." : amount}</dd>
        ${row === undefined ? nothing : html`<dt>Oldest</dt><dd>${ageWords(row.oldest_days)}, ${bucketWord(row.bucket)}</dd>`}
      </dl>
      ${model.jobLevelRefused
        ? html`<div class="li-actions"><button type="button" class="btn btn-secondary" data-action="li-release-close">Close</button></div>`
        : !model.canRelease
        ? html`
            <p class="li-note">${refusalWords("release", "", 403)}</p>
            <div class="li-actions"><button type="button" class="btn btn-secondary" data-action="li-release-close">Close</button></div>
          `
        : release.roster === "loading"
          ? loading("Loading the staff who can co-sign.")
          : release.roster === "refused"
            ? html`
                <p class="li-alert li-alert-error" role="alert" data-error>${release.rosterError}</p>
                <div class="li-actions"><button type="button" class="btn btn-secondary" data-action="li-release-close">Close</button></div>
              `
            : html`
                <label class="li-field">Co-signer
                  <select data-field="cosigner" aria-label="Co-signer"><option data-id="">Pick a co-signer</option>${people.map((p) => html`<option data-id=${p.user_id}>${p.display_name}</option>`)}</select>
                </label>
                <p class="li-hint">A Financial Controller or Payables and Receivables reviews the release with you. The core checks who they are.</p>
                ${row === undefined
                  ? html`<p class="li-hint" data-nothing-held>Nothing is held on this job now, so there is nothing to release.</p>`
                  : html`<p class="li-confirm" data-confirm-words>${releaseConfirmWords(jobNo, amount)}</p>`}
                <div class="li-actions">
                  <button type="button" class="btn btn-primary" data-action="li-release-confirm" ?disabled=${model.busy || row === undefined}>Confirm release</button>
                  <button type="button" class="btn btn-secondary" data-action="li-release-close">Cancel</button>
                </div>
              `}
    </section>
  `;
}

function holdbacks(model: LiensModel): TemplateResult {
  return html`
    <div class="li-split">
      <section class="li-panel">
        <div class="li-panel-head"><h2>Holdback aging</h2></div>
        <p class="li-dim">What is held back by job, how long it has been held, and whether it is ready to release.</p>
        ${model.aging.length === 0
          ? empty("aging", "No holdback is outstanding", "Everything held has been released, or nothing is held yet.")
          : html`
              <div class="li-scroll li-hide-narrow">
                <table aria-label="Holdback aging">
                  <thead><tr><th>Job</th><th class="num">Held</th><th class="num">Oldest</th><th>Release</th></tr></thead>
                  <tbody>${model.aging.map((r) => agingRow(model, r))}</tbody>
                </table>
              </div>
              <ul class="li-cards">${model.aging.map((r) => agingCard(model, r))}</ul>
            `}
      </section>
      ${model.release === null ? nothing : html`<div>${releasePanel(model, model.release)}</div>`}
    </div>
  `;
}

// ---- documents --------------------------------------------------------------------------------------

function docRow(d: LienDocument, jobNo: string): TemplateResult {
  return html`
    <tr data-row="doc" data-job-no=${jobNo} data-kind=${d.template_kind}>
      <td><strong>${templateWord(d.template_kind)}</strong></td>
      <td class="num">Version ${d.version}</td>
      <td>${pill(documentTone(d.status), documentStatusWord(d.status))}</td>
      <td>${stamp(d.created_at)}</td>
      <td class="li-nowrap"><button type="button" class="li-link" data-action="li-view" data-id=${d.id}>View</button> <button type="button" class="li-link" data-action="li-history" data-id=${d.id}>History</button></td>
    </tr>
  `;
}

function docCard(d: LienDocument, jobNo: string): TemplateResult {
  return html`
    <li class="li-card" data-row="doc" data-job-no=${jobNo} data-kind=${d.template_kind}>
      <div class="li-card-top"><strong>${templateWord(d.template_kind)}</strong>${pill(documentTone(d.status), documentStatusWord(d.status))}</div>
      <span class="li-sub">Version ${d.version} · ${stamp(d.created_at)}</span>
      <div class="li-actions"><button type="button" class="li-link" data-action="li-view" data-id=${d.id}>View</button> <button type="button" class="li-link" data-action="li-history" data-id=${d.id}>History</button></div>
    </li>
  `;
}

function versionRow(d: LienDocument, jobNo: string): TemplateResult {
  return html`
    <tr data-row="version" data-job-no=${jobNo}>
      <td class="num">Version ${d.version}</td>
      <td>${pill(documentTone(d.status), documentStatusWord(d.status))}</td>
      <td>${stamp(d.created_at)}</td>
      <td><button type="button" class="li-link" data-action="li-view" data-id=${d.id}>View</button></td>
    </tr>
  `;
}

function versionCard(d: LienDocument, jobNo: string): TemplateResult {
  return html`
    <li class="li-card" data-row="version" data-job-no=${jobNo}>
      <div class="li-card-top"><strong>Version ${d.version}</strong>${pill(documentTone(d.status), documentStatusWord(d.status))}</div>
      <span class="li-sub">${stamp(d.created_at)}</span>
      <button type="button" class="li-link" data-action="li-view" data-id=${d.id}>View</button>
    </li>
  `;
}

function renderForm(model: LiensModel): TemplateResult {
  if (!model.canWrite) return html`<section class="li-panel"><p class="li-note" data-read-only>${READ_ONLY_LIENS}</p></section>`;
  const fields = templateFields(model.docKind);
  return html`
    <section class="li-panel">
      <div class="li-panel-head"><h2>Render a document</h2></div>
      <div class="li-form">
        <label class="li-field">Form
          <select data-field="doc_kind" aria-label="Form">${TEMPLATE_KINDS.map((k) => html`<option data-id=${k}>${templateWord(k)}</option>`)}</select>
        </label>
        ${fields.map((f) => html`<label class="li-field" data-merge-field>${f.label}<input data-field="merge" data-key=${f.key} aria-label=${f.label} autocomplete="off" /></label>`)}
      </div>
      <p class="li-hint">A blank field prints as a blank line for the form. A second render of the same form is its next version.</p>
      <div class="li-actions"><button type="button" class="btn btn-primary" data-action="li-render" ?disabled=${model.busy || model.docJob === ""}>Render draft</button></div>
    </section>
  `;
}

function documents(model: LiensModel): TemplateResult {
  const jobNo = jobNoOf(model.jobs, model.docJob);
  return html`
    <div class="li-toolbar">
      <label class="li-field">Job
        <select data-field="doc_job" aria-label="Job">${model.jobs.map((j) => html`<option data-id=${j.id}>${j.job_no}</option>`)}</select>
      </label>
    </div>
    ${model.jobs.length === 0
      ? html`<section class="li-panel">${empty("jobs", "No jobs yet", "Create a job on the Jobs tab, then render its documents here.")}</section>`
      : html`
          <div class="li-split">
            <div>
              ${renderForm(model)}
              <section class="li-panel">
                <div class="li-panel-head"><h2>Documents for ${jobNo}</h2></div>
                ${model.docsPhase === "loading"
                  ? loading("Loading the documents.")
                  : model.docs.length === 0
                    ? html`<div class="li-empty" data-doc-empty><strong>No documents for this job yet.</strong><span class="li-dim">Render a form above and it lands here as a draft.</span></div>`
                    : html`
                        <div class="li-scroll li-hide-narrow">
                          <table aria-label="Documents">
                            <thead><tr><th>Form</th><th class="num">Latest</th><th>Status</th><th>Rendered</th><th></th></tr></thead>
                            <tbody>${model.docs.map((d) => docRow(d, jobNo))}</tbody>
                          </table>
                        </div>
                        <ul class="li-cards">${model.docs.map((d) => docCard(d, jobNo))}</ul>
                      `}
              </section>
              ${model.versions === null
                ? nothing
                : html`
                    <section class="li-panel" data-versions>
                      <div class="li-panel-head"><h2>Version history</h2><button type="button" class="li-link" data-action="li-close-history">Close</button></div>
                      <div class="li-scroll li-hide-narrow">
                        <table aria-label="Versions">
                          <thead><tr><th class="num">Version</th><th>Status</th><th>Rendered</th><th></th></tr></thead>
                          <tbody>${model.versions.map((d) => versionRow(d, jobNo))}</tbody>
                        </table>
                      </div>
                      <ul class="li-cards">${model.versions.map((d) => versionCard(d, jobNo))}</ul>
                    </section>
                  `}
            </div>
            <div>${documentView(model.viewing)}</div>
          </div>
        `}
  `;
}

function documentView(doc: LienDocument | null): TemplateResult {
  if (doc === null) {
    return html`<section class="li-panel" data-doc-hint><p class="li-dim">Render a form, or open one from the list, to read it here.</p></section>`;
  }
  return html`
    <section class="li-panel">
      <div class="li-panel-head"><h2>${templateWord(doc.template_kind)}, version ${doc.version}</h2>${pill(documentTone(doc.status), documentStatusWord(doc.status))}</div>
      ${doc.is_draft_banner ? html`<p class="li-hint">This is a draft. The core words the banner at its head.</p>` : nothing}
      <pre class="li-pre" data-doc-view>${doc.rendered_content}</pre>
    </section>
  `;
}

// ---- the whole area ---------------------------------------------------------------------------------

export function renderLiens(model: LiensModel): TemplateResult {
  return html`
    <div data-liens>
      <header class="li-head">
        <h1>Liens</h1>
        <p class="li-dim">Holdback, release and lien documents for the jobs we are on.${demoNote(servedConsoleConfig())}</p>
      </header>
      ${model.phase === "loading"
        ? loading("Loading the liens.")
        : model.phase === "error"
          ? loadFailed(model.error, "li-reload")
          : html`
              ${bannerStrip(model.banner, model.bannerState)} ${tabs(model)} ${messages(model.error, model.notice)}
              ${model.tab === "triage" ? triage(model) : model.tab === "due" ? dueTab(model) : model.tab === "jobs" ? jobs(model) : model.tab === "holdbacks" ? holdbacks(model) : documents(model)}
            `}
    </div>
  `;
}
