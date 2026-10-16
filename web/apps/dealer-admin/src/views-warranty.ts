// The Warranty surface's screens: the claim threshold, and how each vendor
// credits warranty. Two cards, one surface; the words are the boards' own.
// Pure template functions over plain model objects; app-root owns every call
// and every state change.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings. The segmented control's buttons and
// the period selects answer through data-action names the host's listeners
// act on; the threshold's field mirrors through data-field and updated().
import { html, nothing, type TemplateResult } from "lit";
import {
  AGREEMENT_KINDS,
  allowanceWords,
  kindWord,
  periodWord,
} from "./warranty-logic.js";

// -- the models --------------------------------------------------------------------------------------

export interface ThresholdSection {
  readonly phase: "loading" | "ready" | "refused" | "error";
  /** The read's refusal words, when the policy never answered. */
  readonly words: string;
  /** The effective threshold in integer cents. */
  readonly cents: number;
  /**
   * The person's words on the glass since the last read or save; empty
   * means the field shows the server's own dollars word.
   */
  readonly draft: string;
  readonly busy: boolean;
  readonly notice: string;
  readonly error: string;
  /** The client side parse fault, in words, before any write. */
  readonly fault: string;
}

export interface AgreementRowView {
  readonly vendor_id: string;
  readonly vendor_no: string;
  readonly vendor_name: string;
  readonly kind: string;
  readonly period: string;
  readonly allowanceBp: number;
  readonly busy: boolean;
  readonly error: string;
}

export interface AgreementsSection {
  readonly phase: "loading" | "ready" | "refused" | "error";
  /** The read's refusal words, when the agreements never answered. */
  readonly words: string;
  readonly rows: readonly AgreementRowView[];
}

export interface WarrantyModel {
  readonly threshold: ThresholdSection;
  readonly agreements: AgreementsSection;
}

export function renderWarranty(model: WarrantyModel): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Warranty</h1>
      <p class="page-sub">When a defect becomes a warranty claim, and how each vendor credits it.</p>
    </header>
    ${renderThreshold(model.threshold)}
    ${renderAgreements(model.agreements)}
  `;
}

// -- the claim threshold -----------------------------------------------------------------------------

function renderThreshold(s: ThresholdSection): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Claim threshold</h2>
      </div>
      <p class="card-sub">
        A defect found on a return or in the yard becomes a warranty claim when the cost of the damaged product is over
        this amount. Under it, the defect is logged as shrinkage. Both are tracked.
      </p>
      ${s.phase === "loading" ? html`<p class="notice-line" data-test="warranty-threshold-loading">Reading the claim threshold…</p>` : nothing}
      ${s.phase === "refused" || s.phase === "error"
        ? html`
            <p class="error-line" data-test="warranty-threshold-error">${s.words}</p>
            <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-warranty">Try again</button></div>
          `
        : nothing}
      ${s.phase === "ready"
        ? html`
            ${s.notice === "" ? nothing : html`<p class="notice-line" data-test="warranty-threshold-notice">${s.notice}</p>`}
            ${s.error === "" ? nothing : html`<p class="error-line" data-test="warranty-threshold-save-error">${s.error}</p>`}
            ${s.fault === "" ? nothing : html`<p class="error-line" data-test="warranty-threshold-fault">${s.fault}</p>`}
            <div class="wa-threshold">
              <label class="field">
                <span class="field-label">Threshold on the cost of the damaged product</span>
                <input
                  class="text-field"
                  type="text"
                  inputmode="decimal"
                  data-field="warranty-threshold"
                  ?disabled=${s.busy}
                />
              </label>
              <button type="button" class="btn btn-primary" data-action="warranty-save-threshold" ?disabled=${s.busy || s.fault !== ""}>
                ${s.busy ? "Saving…" : "Save"}
              </button>
            </div>
            <p class="card-sub">
              The default is $50. You can lower it as your process improves: more defects then become claims you can
              recover.
            </p>
          `
        : nothing}
    </section>
  `;
}

// -- how each vendor credits warranty ----------------------------------------------------------------

function renderAgreements(s: AgreementsSection): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">How each vendor credits warranty</h2>
        ${s.phase === "ready" ? html`<span class="count" data-test="warranty-vendor-count">${s.rows.length} vendors</span>` : nothing}
      </div>
      <p class="card-sub">Claim by claim, one periodic credit note rolling up the period's claims, or a fixed damage allowance.</p>
      ${s.phase === "loading" ? html`<p class="notice-line" data-test="warranty-agreements-loading">Reading the vendor agreements…</p>` : nothing}
      ${s.phase === "refused" || s.phase === "error"
        ? html`
            <p class="error-line" data-test="warranty-agreements-error">${s.words}</p>
            <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-warranty">Try again</button></div>
          `
        : nothing}
      ${s.phase === "ready"
        ? html`
            <div class="wa-rows" data-test="warranty-vendor-rows">
              ${s.rows.map((row) => renderAgreementRow(row))}
            </div>
          `
        : nothing}
    </section>
  `;
}

function renderAgreementRow(row: AgreementRowView): TemplateResult {
  return html`
    <div class="wa-row" data-test="warranty-vendor-row">
      <span class="wa-vendor">${row.vendor_name}</span>
      <div class="wa-controls">
        <div class="seg" role="group" aria-label=${`How ${row.vendor_name} credits warranty`}>
          ${AGREEMENT_KINDS.map(
            (kind) => html`
              <button
                type="button"
                class=${row.kind === kind ? "seg-on" : ""}
                data-action="warranty-kind"
                data-vendor=${row.vendor_id}
                data-kind=${kind}
                ?disabled=${row.busy}
                aria-pressed=${row.kind === kind ? "true" : "false"}
              >
                ${kindWord(kind)}
              </button>
            `,
          )}
        </div>
        ${row.kind === "claim_by_claim"
          ? nothing
          : html`
              <label class="field">
                <span class="field-label">The period it runs on</span>
                <select class="select" data-action="warranty-period" data-vendor=${row.vendor_id} ?disabled=${row.busy}>
                  <option value="monthly" ?selected=${row.period === "monthly"}>${periodWord("monthly")}</option>
                  <option value="quarterly" ?selected=${row.period === "quarterly"}>${periodWord("quarterly")}</option>
                </select>
              </label>
            `}
        ${row.kind === "fixed_allowance"
          ? html`
              <label class="field">
                <span class="field-label">Percent of the vendor's purchases</span>
                <input
                  class="text-field"
                  type="text"
                  inputmode="decimal"
                  placeholder="1% of purchases"
                  data-field="warranty-allowance"
                  data-vendor=${row.vendor_id}
                  ?disabled=${row.busy}
                />
              </label>
            `
          : nothing}
      </div>
      ${row.error === "" ? nothing : html`<p class="error-line wa-row-error" data-test="warranty-row-error">${row.error}</p>`}
    </div>
  `;
}
