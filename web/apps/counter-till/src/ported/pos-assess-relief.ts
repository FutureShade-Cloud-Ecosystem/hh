// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-assess-relief.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { assessRelief, type ReliefOutcome } from '@hardscapehouse/pos-kit/src/ported/pos-api.js';
import './pos-signature.js';
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';
import { ModalFocus } from '@hardscapehouse/pos-kit/src/ported/modal-focus.js';
import { ratePercent, taxReasonWord } from './tax-api.js';
import { plainDollars } from '@hardscapehouse/pos-kit/src/ported/_money-format.js';

// HH note: display formatting of a server-computed figure, through the ported
// surface's named display helper (CLOSURE.md names it); the local wrapper the
// port first carried is gone, so the division spelling lives only in
// _money-format.ts.

/**
 * A-19 "Assess tax relief" dialog (US-A19-06). A neutral, per-transaction workflow offered to any
 * customer who asks: it NEVER auto-exempts. It records a PIPEDA purpose notice + use declaration,
 * classifies use + fulfilment, pre-populates a MASKED identifier only, surfaces the 0%/5%/13%/review
 * outcome on the flat-13% fallback, enforces the 0% delivery-evidence hard gate, and captures an
 * on-screen attestation. There is deliberately NO card-image scan/upload field (data-minimization).
 */
@customElement('hos-pos-relief')
export class HosPosRelief extends LitElement {
  @property() profileId = '';
  @property() orderId = '';
  @property({ type: Number }) subtotalCents = 0;

  // E6: parent-mounted, so connected IS open. Escape abandons an assessment that has not been
  // submitted, which is the same outcome as the Cancel button and the scrim click already give.
  readonly modalFocus = new ModalFocus(this, {
    container: () => this.renderRoot.querySelector<HTMLElement>('.hos-dialog'),
    onEscape: () => this.close(),
  });

  @state() private purposeAck = false;
  @state() private declaredUse = 'personal';
  @state() private fulfilment = 'pickup';
  @state() private evidenceAttached = false;
  @state() private outcome: ReliefOutcome | null = null;
  @state() private busy = false;
  @state() private error = '';
  @state() private signatureRef = '';

  static styles = [
    focusRing,
    css`
    :host {
      display: block;
      font-family: var(--hos-font-ui);
      color: var(--hos-text);
    }
    .hos-dialog {
      position: fixed;
      inset: 0;
      /* THE TOKEN. A scrim's opacity is a judgement about the page behind it, and that page is a
         different colour in each of three themes: so a hand-rolled black is tuned for one of them
         and wrong in the other two. Over the near-black loud page a 55% black barely reads as a
         layer, which is the whole job of a scrim. */
      background: var(--hos-scrim);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 50;
    }
    .card {
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: 14px;
      width: min(560px, 92vw);
      max-height: 90vh;
      overflow: auto;
      padding: 22px 24px;
    }
    h2 {
      margin: 0 0 4px;
      font-size: 14px;
      text-transform: uppercase;
      letter-spacing: 0.12em;
      color: var(--hos-stone-amber-text);
    }
    .notice {
      font-size: 12px;
      color: var(--hos-text-dim);
      background: var(--hos-deep-earth);
      border: 1px solid var(--hos-line);
      border-radius: 8px;
      padding: 10px 12px;
      margin: 10px 0 14px;
    }
    label {
      display: block;
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      margin: 12px 0 4px;
    }
    select {
      width: 100%;
      background: var(--hos-field);
      color: var(--hos-text);
      border: 1px solid var(--hos-line);
      border-radius: 7px;
      padding: 9px 11px;
      font-size: 14px;
      font-family: inherit;
    }
    .check {
      display: flex;
      gap: 8px;
      align-items: flex-start;
      font-size: 13px;
      margin: 12px 0;
    }
    .masked {
      font-family: var(--hos-font-mono);
      color: var(--hos-info-blue);
      font-size: 13px;
    }
    .outcome {
      margin-top: 14px;
      border: 1px solid var(--hos-line);
      border-radius: 10px;
      padding: 12px 14px;
    }
    .outcome .pct {
      font-family: var(--hos-font-mono);
      font-size: 22px;
      color: var(--hos-stone-amber-text);
      font-weight: 600;
    }
    .outcome .reason {
      font-family: var(--hos-font-mono);
      font-size: 11px;
      color: var(--hos-text-dim);
    }
    .gate {
      margin-top: 8px;
      font-size: 12px;
      color: var(--hos-safety-red);
    }
    .num {
      font-family: var(--hos-font-mono);
    }
    .actions {
      display: flex;
      justify-content: space-between;
      gap: 10px;
      margin-top: 18px;
    }
    button {
      background: var(--hos-stone-amber);
      color: #0b1725;
      border: 0;
      border-radius: 8px;
      padding: 9px 16px;
      font-weight: 600;
      font-family: inherit;
      cursor: pointer;
    }
    button.ghost {
      background: transparent;
      color: var(--hos-text-dim);
      border: 1px solid var(--hos-line);
    }
    button:disabled {
      background: transparent;
      border-color: var(--hos-line);
      color: var(--hos-text-dim);
      opacity: 1;
      cursor: not-allowed;
    }
    .err {
      color: var(--hos-safety-red);
      font-size: 12px;
      margin-top: 8px;
    }
  `,
  ];

  private close(): void {
    this.dispatchEvent(new CustomEvent('pos-relief-close', { bubbles: true, composed: true }));
  }

  private async assess(): Promise<void> {
    this.busy = true;
    this.error = '';
    try {
      this.outcome = await assessRelief({
        profile_id: this.profileId || undefined,
        order_id: this.orderId || undefined,
        declared_use: this.declaredUse,
        fulfilment: this.fulfilment,
        subtotal_cents: this.subtotalCents,
        evidence_attached: this.evidenceAttached,
        purpose_ack: this.purposeAck,
      });
    } catch (e) {
      this.error = e instanceof Error ? e.message : 'assessment failed';
    } finally {
      this.busy = false;
    }
  }

  private apply(): void {
    if (!this.outcome) return;
    this.dispatchEvent(
      new CustomEvent('pos-relief-applied', {
        detail: { outcome: this.outcome, signatureRef: this.signatureRef },
        bubbles: true,
        composed: true,
      }),
    );
    this.close();
  }

  render() {
    const o = this.outcome;
    return html`
      <div class="hos-dialog" @click=${(e: Event) => e.target === e.currentTarget && this.close()}>
        <div class="card">
          <h2>Assess Tax Relief</h2>
          <div class="notice">
            Per-transaction assessment for this sale only: it does not set a permanent exemption. We
            collect a use declaration and proof reference to determine the correct tax treatment
            (PIPEDA). The cashier sees a masked identifier only; documents require tax-admin access.
          </div>

          <label class="check">
            <input
              type="checkbox"
              .checked=${this.purposeAck}
              @change=${(e: Event) => (this.purposeAck = (e.target as HTMLInputElement).checked)}
            />
            Purpose notice read to the purchaser and consent given.
          </label>

          ${this.profileId
            ? html`<div>
                <label>Eligibility (pre-filled, masked)</label>
                <div class="masked">${o?.masked_identifier || '•••• ••••'}</div>
              </div>`
            : nothing}

          <label>Declared use (this transaction)</label>
          <select @change=${(e: Event) => (this.declaredUse = (e.target as HTMLSelectElement).value)}>
            <option value="personal">Personal use</option>
            <option value="commercial">Commercial use</option>
            <option value="band">Band / band management</option>
            <option value="mixed">Mixed</option>
            <option value="unknown">Unknown</option>
          </select>

          <label>Fulfilment</label>
          <select @change=${(e: Event) => (this.fulfilment = (e.target as HTMLSelectElement).value)}>
            <option value="pickup">Customer pickup (counter)</option>
            <option value="purchaser_carrier">Purchaser's own carrier</option>
            <option value="house_truck">House truck → reserve</option>
            <option value="house_carrier">House-arranged carrier → reserve</option>
          </select>

          ${this.fulfilment.startsWith('house')
            ? html`<label class="check">
                <input
                  type="checkbox"
                  .checked=${this.evidenceAttached}
                  @change=${(e: Event) =>
                    (this.evidenceAttached = (e.target as HTMLInputElement).checked)}
                />
                Proof of delivery / waybill attached (required to finalize at 0%).
              </label>`
            : nothing}

          ${o
            ? html`<div class="outcome">
                  <span class="pct">${o.outcome}</span>
                  <span class="reason"> · ${taxReasonWord(o.reason_code)}</span>
                  <div class="num">
                    Full ${o.tax_label} ${plainDollars(o.full_hst_cents)} · On-credit
                    ${plainDollars(o.on_credit_cents)} · Collect
                    ${plainDollars(o.net_collected_cents)}
                  </div>
                  ${o.requires_evidence && !o.evidence_satisfied
                    ? html`<div class="gate">
                        0% cannot finalize until delivery evidence is attached: binding tax falls
                        back to ${ratePercent(o.rate_bps)}.
                      </div>`
                    : nothing}
                </div>
                <label>Attestation signature</label>
                <hos-pos-signature
                  purpose="TAX_ATTESTATION"
                  refType="tax_decision_snapshot"
                  .refId=${this.orderId}
                  @pos-signature=${(e: CustomEvent<{ ref: string }>) =>
                    (this.signatureRef = e.detail.ref)}
                ></hos-pos-signature>`
            : nothing}

          ${this.error ? html`<div class="err" role="alert">${this.error}</div>` : nothing}

          <div class="actions">
            <button class="ghost" @click=${this.close}>Cancel</button>
            ${o
              ? html`<button @click=${this.apply}>Apply to sale</button>`
              : html`<button ?disabled=${this.busy} @click=${this.assess}>
                  ${this.busy ? 'Assessing…' : 'Assess'}
                </button>`}
          </div>
        </div>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-relief': HosPosRelief;
  }
}
