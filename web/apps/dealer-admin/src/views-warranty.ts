// The Warranty surface's screen: claim threshold and vendor credit agreements.
// Pure template function matching the Change 11 Warranty board.
import { html, type TemplateResult } from "lit";

export interface VendorAgreementRow {
  readonly vendor: string;
  readonly method: "claim_by_claim" | "periodic_credit_note" | "fixed_allowance";
  readonly period?: string;
  readonly rate?: string;
}

export const VENDOR_AGREEMENTS: readonly VendorAgreementRow[] = [
  {
    vendor: "Heronfield Stone",
    method: "fixed_allowance",
    period: "Quarterly",
    rate: "1% of purchases",
  },
  {
    vendor: "Prairie Bend Soils",
    method: "periodic_credit_note",
    period: "Monthly",
  },
  {
    vendor: "Copperline Pavers",
    method: "claim_by_claim",
  },
  {
    vendor: "Granite Run Quarry",
    method: "periodic_credit_note",
    period: "Quarterly",
  },
];

export function renderWarranty(): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Warranty</h1>
      <p class="page-sub">When a defect becomes a warranty claim, and how each vendor credits it.</p>
    </header>

    <section class="card" data-test="warranty-threshold-card">
      <div class="card-head">
        <h2 class="card-title">Claim threshold</h2>
      </div>
      <p class="card-sub">
        A defect found on a return or in the yard becomes a warranty claim when the cost of the damaged product is over this amount. Under it, the defect is logged as shrinkage. Both are tracked.
      </p>
      <div class="threshold-form">
        <label class="field">
          <span class="field-label">Threshold on the cost of the damaged product</span>
          <div class="input-action-row">
            <input class="text-field" type="text" value="$50.00" data-field="warranty-threshold" />
            <button type="button" class="btn btn-primary" data-action="save-warranty-threshold">Save</button>
          </div>
        </label>
        <p class="field-hint">The default is $50. You can lower it as your process improves: more defects then become claims you can recover.</p>
      </div>
    </section>

    <section class="card" data-test="warranty-agreements-card">
      <div class="card-head">
        <h2 class="card-title">How each vendor credits warranty</h2>
      </div>
      <p class="card-sub">
        Claim by claim, one periodic credit note rolling up the period's claims, or a fixed damage allowance.
      </p>
      <div class="list-stack">
        ${VENDOR_AGREEMENTS.map(
          (ag) => html`
            <div class="pill-row">
              <span class="pill-name"><strong>${ag.vendor}</strong></span>
              <div class="segmented-control" role="group" aria-label="Agreement method">
                <button
                  type="button"
                  class="seg-btn ${ag.method === "claim_by_claim" ? "seg-btn-active" : ""}"
                >
                  Claim by claim
                </button>
                <button
                  type="button"
                  class="seg-btn ${ag.method === "periodic_credit_note" ? "seg-btn-active" : ""}"
                >
                  Periodic credit note
                </button>
                <button
                  type="button"
                  class="seg-btn ${ag.method === "fixed_allowance" ? "seg-btn-active" : ""}"
                >
                  Fixed allowance
                </button>
              </div>
              ${ag.period ? html`<span class="pill-detail badge-outline">${ag.period}</span>` : html`<span class="pill-detail"></span>`}
              ${ag.rate ? html`<span class="pill-fee badge-outline">${ag.rate}</span>` : html`<span class="pill-fee"></span>`}
            </div>
          `,
        )}
      </div>
    </section>
  `;
}
