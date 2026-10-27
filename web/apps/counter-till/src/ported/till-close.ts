// The A-03 end-of-day close flow (FW1-13), ported from hardscapeos_dibbits
// master 87d7941b app/src/till-close.ts and adapted for HH: the counter's own
// dialog chrome, the app's money helper, and the count the brief asks for, by
// denomination.
//
// Steps: drawer movements (paid-in / paid-out / drop / lodgement) → BLIND count
// (the cash is counted by denomination and the cheques by hand; no expected
// figure is on screen before the count posts) → Z-report (sales by tender,
// expected vs counted vs over/short, and the drawer's movements) → complete.
// The completion is the session close the core already serves: it posts the
// over or short to the books. An over/short beyond the branch till_policy
// threshold requires a manager's note; the backend enforces it, this surface
// asks for it.
import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { api, sumSlipCents } from '@hardscapehouse/pos-kit/src/ported/ports.js';
import { plainDollars } from '@hardscapehouse/pos-kit/src/ported/_money-format.js'; // HH note: the display spelling lives in the named helper (CLOSURE.md)
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';
import { ModalFocus } from '@hardscapehouse/pos-kit/src/ported/modal-focus.js';
import { rowStripe } from './styles/row-stripe.js';
import { tenderMethodWord } from './tender-logic.js';
import { formatMoney } from "../money.js";

interface TenderTotal {
  method: string;
  expected_cents: number;
  actual_cents: number;
  variance_cents: number;
  lodged_cents: number;
}

interface ZReport {
  session_id: string;
  per_tender: TenderTotal[];
  cash: {
    opening_float_cents: number;
    expected_cents: number;
    counted_cents: number;
    over_short_cents: number;
  };
  closing_float_cents: number;
}

interface MovementRow {
  id?: string;
  movement_type: string;
  method: string;
  amount_cents: number;
  reason?: string;
  reference?: string;
}

interface SlipCandidate {
  payment_id: string;
  payment_no: string;
  amount_cents: number;
  method: string;
  reference: string;
}

interface TillPolicy {
  branch_id: string;
  variance_threshold_cents: number;
  close_grain: string;
  require_blind_close: boolean;
}

type Step = 'movements' | 'count' | 'zreport' | 'done';

// The cash pieces a Canadian drawer holds, smallest last. The count is typed
// as piece quantities; the screen derives the money and never lets a total be
// typed, so the counted figure is the count.
const DENOMINATIONS: number[] = [5, 10, 25, 100, 200, 500, 1000, 2000];

const cents = (v: string): number => Math.round((parseFloat(v || '0') || 0) * 100);

const denomWord = (d: number): string => (d >= 100 ? `$${d / 100}` : `${d}¢`);

@customElement('hos-till-close')
export class HosTillClose extends LitElement {
  static styles = [
    focusRing,
    rowStripe,
    css`
    :host {
      display: block;
      font-family: var(--hos-font-ui);
      color: var(--hos-text);
    }
    .hos-dialog {
      position: fixed;
      inset: 0;
      background: var(--hos-scrim);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: var(--hos-z-modal);
    }
    .card {
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: 14px;
      width: min(640px, 94vw);
      max-height: 92vh;
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
    .sub {
      font-size: 12px;
      color: var(--hos-text-dim);
      margin: 0 0 14px;
    }
    .notice {
      font-size: 12px;
      color: var(--hos-text-dim);
      background: var(--hos-field);
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
    input,
    select,
    textarea {
      width: 100%;
      box-sizing: border-box;
      background: var(--hos-field);
      color: var(--hos-text);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      padding: 8px 10px;
      font-size: 13px;
      font-family: inherit;
    }
    input.num {
      font-family: var(--hos-font-mono);
    }
    .grid2 {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 0 14px;
    }
    .denoms {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 8px 10px;
      margin-top: 4px;
    }
    .denom label {
      margin: 0 0 2px;
    }
    .denom input {
      font-family: var(--hos-font-mono);
      padding: 6px 8px;
    }
    @media (max-width: 480px) {
      .denoms {
        grid-template-columns: repeat(2, minmax(0, 1fr));
      }
    }
    .sumline {
      font-size: 13px;
      margin-top: 10px;
      color: var(--hos-text);
    }
    .sumline b {
      font-family: var(--hos-font-mono);
    }
    .row {
      display: flex;
      gap: 10px;
      align-items: flex-end;
    }
    .row > div {
      flex: 1;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      font-size: 13px;
      margin-top: 8px;
    }
    th {
      text-align: left;
      font-size: 10.5px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      border-bottom: 1px solid var(--hos-line);
      padding: 6px 8px;
    }
    td {
      padding: 7px 8px;
      border-bottom: 1px solid var(--hos-line);
    }
    td.num,
    th.num {
      text-align: right;
      font-family: var(--hos-font-mono);
    }
    .pos {
      color: var(--hos-success);
    }
    .neg {
      color: var(--hos-safety-red);
    }
    .chip {
      display: inline-block;
      font-family: var(--hos-font-mono);
      font-size: 11px;
      padding: 3px 10px;
      border-radius: var(--hos-radius-pill);
      border: 1px solid var(--hos-line);
      margin-top: 8px;
    }
    .chip.ok {
      border-color: var(--hos-success);
      color: var(--hos-success);
    }
    .chip.warn {
      border-color: var(--hos-safety-red);
      color: var(--hos-safety-red);
    }
    .slip {
      margin-top: 12px;
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      padding: 10px 12px;
    }
    .slip.empty {
      color: var(--hos-text-dim);
      font-size: 12px;
    }
    .slip-head {
      font-size: 12px;
      color: var(--hos-text-dim);
      margin-bottom: 6px;
    }
    .slip-row {
      display: flex;
      align-items: center;
      gap: 10px;
      font-size: 13px;
      padding: 4px 0;
      margin: 0;
      cursor: pointer;
    }
    /* The tick is a tick, not a text field: the component's own input rule gives
       every input the full width, which would push the cheque's number out of
       the row and centre the box. The tick keeps its own size. */
    .slip-row input[type='checkbox'] {
      flex: none;
      width: 16px;
      height: 16px;
      margin: 0;
      padding: 0;
      accent-color: var(--hos-stone-amber);
    }
    .slip-no {
      flex: 1 1 auto;
      min-width: 0;
      font-family: var(--hos-font-mono);
      font-size: 12px;
      color: var(--hos-text);
      text-transform: none;
      letter-spacing: normal;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .slip-amt {
      font-family: var(--hos-font-mono);
      text-align: right;
      white-space: nowrap;
      min-width: 72px;
    }
    .slip-sum {
      display: flex;
      justify-content: space-between;
      border-top: 1px solid var(--hos-line);
      margin-top: 6px;
      padding-top: 6px;
      font-size: 12px;
    }
    .moves {
      margin-top: 10px;
      font-size: 12px;
    }
    .moves li {
      display: flex;
      justify-content: space-between;
      gap: 10px;
      padding: 4px 0;
      border-bottom: 1px solid var(--hos-line);
    }
    .moves .why {
      color: var(--hos-text-dim);
    }
    .actions {
      display: flex;
      justify-content: space-between;
      gap: 10px;
      margin-top: 18px;
    }
    .actions > div {
      display: flex;
      gap: 10px;
    }
    button {
      font-family: inherit;
      font-size: 13px;
      border-radius: var(--hos-radius-control);
      padding: 9px 16px;
      cursor: pointer;
      border: 1px solid var(--hos-line);
      background: transparent;
      color: var(--hos-text-dim);
    }
    button:hover {
      color: var(--hos-text);
    }
    button.primary {
      background: var(--hos-stone-amber);
      border-color: var(--hos-stone-amber);
      color: #0b1725;
      font-weight: 600;
    }
    button[disabled] {
      opacity: 0.5;
      cursor: default;
    }
    .err {
      color: var(--hos-safety-red);
      font-size: 12px;
      margin-top: 10px;
    }
    .okmsg {
      color: var(--hos-success);
      font-size: 12px;
      margin-top: 10px;
    }
    .fieldhint {
      font-size: 11px;
      color: var(--hos-text-dim);
      margin-top: 4px;
    }
  `,
  ];

  @property({ type: String }) sessionId = '';
  @property({ type: String }) branchId = '';

  // The Z-report step is a document: once the count is posted the operator
  // acknowledges it rather than dismisses it, so Escape is not uniform here
  // (the same guard the reference's wizard carries).
  readonly modalFocus = new ModalFocus(this, {
    container: () => this.renderRoot.querySelector<HTMLElement>('.hos-dialog'),
    onEscape: () => this.close(),
    escapeSafe: () => this.step !== 'zreport',
  });

  @state() private step: Step = 'movements';
  @state() private busy = false;
  @state() private error = '';
  @state() private moveOk = '';

  // movement form
  @state() private moveType = 'DROP';
  @state() private moveAmount = '';
  @state() private moveReason = '';
  // A lodgement's method: cash banks without a slip, a cheque lodgement names the cheques it carries
  // (FROM-PRINCIPAL 166 P1-1: a cheque bounces, so the deposit has to say which payments it holds).
  @state() private moveMethod = 'CASH';
  // The drawer's unlodged cheques and the ticks on them: every cheque starts ticked, and the drawer
  // unticks the ones this lodgement does not carry.
  @state() private slipCandidates: SlipCandidate[] = [];
  @state() private slipTicked: Record<string, boolean> = {};

  // blind count form: the cash by denomination, the cheques by hand
  @state() private denomQty: Record<number, string> = {};
  @state() private chequeCounted = '';
  @state() private closingFloat = '200.00';

  @state() private zreport: ZReport | null = null;
  @state() private movements: MovementRow[] = [];
  @state() private policy: TillPolicy | null = null;
  @state() private overrideNotes = '';
  @state() private needsManager = false;

  // One exit, two meanings, the reference's own split: after a real close the
  // host hears till-closed and resets the chrome; otherwise till-close-cancel,
  // because a counted session can be completed next morning.
  private close(): void {
    const name = this.step === 'done' ? 'till-closed' : 'till-close-cancel';
    this.dispatchEvent(new CustomEvent(name, { bubbles: true, composed: true }));
  }

  protected firstUpdated(): void {
    void this.loadSessionDetail();
  }

  /** The session detail carries the drawer's unlodged cheques, the slip's raw material. */
  private async loadSessionDetail(): Promise<void> {
    const res = await api(`/api/v1/till/sessions/${this.sessionId}`);
    if (!res.ok) return;
    const detail = (await res.json()) as { movements?: MovementRow[]; slip_candidates?: SlipCandidate[] };
    this.movements = detail.movements ?? [];
    this.slipCandidates = detail.slip_candidates ?? [];
    const ticked: Record<string, boolean> = {};
    for (const c of this.slipCandidates) ticked[c.payment_id] = true;
    this.slipTicked = ticked;
  }

  /** The money the ticked cheques come to: the slip's own sum, in the seam. */
  private slipSum(): number {
    return sumSlipCents(this.slipCandidates.filter((c) => this.slipTicked[c.payment_id] === true).map((c) => c.amount_cents));
  }

  /** The ids of the ticked cheques: the slip this lodgement carries. */
  private slipIds(): string[] {
    return this.slipCandidates.filter((c) => this.slipTicked[c.payment_id] === true).map((c) => c.payment_id);
  }

  /** A tick changes the slip, so the slip's sum is the lodgement's amount. */
  private toggleTick(paymentID: string, on: boolean): void {
    this.slipTicked = { ...this.slipTicked, [paymentID]: on };
    this.moveAmount = plainDollars(this.slipSum());
  }

  /** The method changes: a cheque lodgement carries a slip, a cash one does not. */
  private setMoveMethod(value: string): void {
    this.moveMethod = value;
    if (value === 'CHEQUE') {
      this.moveAmount = plainDollars(this.slipSum());
    }
  }

  /** The type changes, so the method starts over on cash: only a lodgement carries a slip. */
  private setMoveType(value: string): void {
    this.moveType = value;
    this.moveMethod = 'CASH';
    if (value === 'LODGEMENT' && this.slipCandidates.length > 0) {
      this.moveAmount = plainDollars(this.slipSum());
    }
  }

  private denomSum(): number {
    return DENOMINATIONS.reduce((sum, d) => sum + Math.round(parseFloat(this.denomQty[d] || '0') || 0) * d, 0);
  }

  private async recordMovement(): Promise<void> {
    const amount = cents(this.moveAmount);
    if (amount <= 0 || this.busy) return;
    this.busy = true;
    this.error = '';
    this.moveOk = '';
    try {
      const res = await api(`/api/v1/till/sessions/${this.sessionId}/movements`, {
        method: 'POST',
        body: JSON.stringify({
          movement_type: this.moveType,
          method: this.moveMethod,
          amount_cents: amount,
          reason: this.moveReason,
          payment_ids: this.moveType === 'LODGEMENT' ? this.slipIds() : [],
        }),
      });
      if (!res.ok) throw new Error(((await res.json()) as { error?: string }).error ?? `HTTP ${res.status}`);
      this.moveOk = `${this.moveType} ${formatMoney(amount)} recorded`;
      this.moveAmount = '';
      this.moveReason = '';
    } catch (e) {
      this.error = e instanceof Error ? e.message : 'movement failed';
    } finally {
      this.busy = false;
    }
  }

  private async submitCount(): Promise<void> {
    if (this.busy) return;
    this.busy = true;
    this.error = '';
    try {
      const cash = this.denomSum();
      const tenders: { method: string; actual_cents: number }[] = [{ method: 'CASH', actual_cents: cash }];
      if (this.chequeCounted !== '') tenders.push({ method: 'CHEQUE', actual_cents: cents(this.chequeCounted) });
      const closingDenominations = DENOMINATIONS.filter((d) => Math.round(parseFloat(this.denomQty[d] || '0') || 0) > 0).map(
        (d) => ({ phase: 'CLOSE', denomination_cents: d, quantity: Math.round(parseFloat(this.denomQty[d] || '0')) }),
      );
      const res = await api(`/api/v1/till/sessions/${this.sessionId}/count`, {
        method: 'POST',
        body: JSON.stringify({
          tenders,
          closing_denominations: closingDenominations,
          closing_float_cents: cents(this.closingFloat),
        }),
      });
      if (!res.ok) throw new Error(((await res.json()) as { error?: string }).error ?? `HTTP ${res.status}`);
      await this.loadZReport();
      this.step = 'zreport';
    } catch (e) {
      this.error = e instanceof Error ? e.message : 'count failed';
    } finally {
      this.busy = false;
    }
  }

  private async loadZReport(): Promise<void> {
    const [zRes, pRes, sRes] = await Promise.all([
      api(`/api/v1/till/sessions/${this.sessionId}/zreport`),
      api(`/api/v1/till/policy?branch_id=${encodeURIComponent(this.branchId)}`),
      api(`/api/v1/till/sessions/${this.sessionId}`),
    ]);
    if (zRes.ok) this.zreport = (await zRes.json()) as ZReport;
    if (pRes.ok) this.policy = (await pRes.json()) as TillPolicy;
    if (sRes.ok) {
      const detail = (await sRes.json()) as { movements?: MovementRow[] };
      this.movements = detail.movements ?? [];
    }
    const overShort = Math.abs(this.zreport?.cash.over_short_cents ?? 0);
    this.needsManager = !!this.policy && overShort > this.policy.variance_threshold_cents;
  }

  private async complete(): Promise<void> {
    if (this.busy) return;
    this.busy = true;
    this.error = '';
    try {
      const res = await api(`/api/v1/till/sessions/${this.sessionId}/complete`, {
        method: 'POST',
        body: JSON.stringify({ override_notes: this.overrideNotes }),
      });
      if (!res.ok) throw new Error(((await res.json()) as { error?: string }).error ?? `HTTP ${res.status}`);
      this.step = 'done';
    } catch (e) {
      this.error = e instanceof Error ? e.message : 'close failed';
    } finally {
      this.busy = false;
    }
  }

  /**
   * The slip: the drawer's unlodged cheques, each ticked by default. The drawer unticks the ones
   * this lodgement does not carry, and the ticked sum is the lodgement's amount.
   */
  private slipList() {
    if (this.slipCandidates.length === 0) {
      return html`<div class="slip empty" data-testid="movement-slip">No cheques on this drawer.</div>`;
    }
    return html`
      <div class="slip" data-testid="movement-slip">
        <div class="slip-head">Cheques on this drawer</div>
        ${this.slipCandidates.map(
          (c) => html`
            <label class="slip-row">
              <input
                type="checkbox"
                data-testid="slip-tick"
                .checked=${this.slipTicked[c.payment_id] === true}
                @change=${(e: Event) => this.toggleTick(c.payment_id, (e.target as HTMLInputElement).checked)}
              />
              <span class="slip-no">${c.reference || c.payment_no}</span>
              <span class="slip-amt">${formatMoney(c.amount_cents)}</span>
            </label>
          `,
        )}
        <div class="slip-sum">
          <span>Ticked</span>
          <span data-testid="slip-sum">${formatMoney(this.slipSum())}</span>
        </div>
      </div>
    `;
  }

  private movementsStep() {
    return html`
      <div class="row">
        <div>
          <label>Type</label>
          <select
            data-testid="movement-type"
            .value=${this.moveType}
            title="Drop: to the safe for a later bank run · Lodgement: already banked · Paid in: non-sale cash added · Paid out: expense cash removed"
            @change=${(e: Event) => this.setMoveType((e.target as HTMLSelectElement).value)}
          >
            <option value="DROP">Drop · to safe, for the bank</option>
            <option value="LODGEMENT">Lodgement · banked</option>
            <option value="PAID_IN">Paid in · non-sale cash</option>
            <option value="PAID_OUT">Paid out · expense</option>
          </select>
        </div>
        <div>
          <label>Amount ($)</label>
          <input
            data-testid="movement-amount"
            class="num"
            inputmode="decimal"
            placeholder="0.00"
            .value=${this.moveAmount}
            @input=${(e: Event) => (this.moveAmount = (e.target as HTMLInputElement).value)}
          />
        </div>
        ${this.moveType === 'LODGEMENT'
          ? html`
              <div>
                <label>Method</label>
                <select
                  data-testid="movement-method"
                  .value=${this.moveMethod}
                  title="Cash banks without a slip. A cheque lodgement names the cheques it carries."
                  @change=${(e: Event) => this.setMoveMethod((e.target as HTMLSelectElement).value)}
                >
                  <option value="CASH">Cash</option>
                  <option value="CHEQUE">Cheque</option>
                </select>
              </div>
            `
          : nothing}
      </div>
      ${this.moveType === 'LODGEMENT' && this.moveMethod === 'CHEQUE' ? this.slipList() : nothing}
      <label>Reason</label>
      <input
        data-testid="movement-reason"
        placeholder="e.g. weekly bank run"
        .value=${this.moveReason}
        @input=${(e: Event) => (this.moveReason = (e.target as HTMLInputElement).value)}
      />
      <div class="actions">
        <button data-testid="till-close-cancel" @click=${this.close}>Cancel</button>
        <div>
          <button data-testid="record-movement" ?disabled=${this.busy || cents(this.moveAmount) <= 0} @click=${this.recordMovement}>
            Record movement
          </button>
          <button
            data-testid="to-count"
            class="primary"
            ?disabled=${this.busy}
            title="Go to the blind count. No movement is required: skip this step if the drawer is untouched."
            @click=${() => (this.step = 'count')}
          >
            ${this.moveOk ? 'Continue to count' : 'Skip · no movements'}
          </button>
        </div>
      </div>
      ${this.moveOk ? html`<div class="okmsg" data-testid="movement-ok">${this.moveOk}</div>` : nothing}
    `;
  }

  private countStep() {
    const sum = this.denomSum();
    return html`
      <div data-testid="count-step">
        <div class="notice" data-testid="blind-note">Count the drawer. The report with the totals comes after the count posts.</div>
        <label>Cash by piece</label>
        <div class="denoms">
          ${DENOMINATIONS.map(
            (d) => html`
              <div class="denom">
                <label>${denomWord(d)}</label>
                <input
                  data-testid="denom-${d}"
                  class="num"
                  inputmode="numeric"
                  placeholder="0"
                  .value=${this.denomQty[d] ?? ''}
                  @input=${(e: Event) => (this.denomQty = { ...this.denomQty, [d]: (e.target as HTMLInputElement).value })}
                />
              </div>
            `,
          )}
        </div>
        <div class="sumline" data-testid="denom-sum">${formatMoney(sum)}</div>
        <div class="grid2">
          <div>
            <label>Cheques counted ($, optional)</label>
            <input
              data-testid="cheque-counted"
              class="num"
              inputmode="decimal"
              .value=${this.chequeCounted}
              @input=${(e: Event) => (this.chequeCounted = (e.target as HTMLInputElement).value)}
            />
          </div>
          <div>
            <label for="tc-float">Closing float kept ($)</label>
            <input
              id="tc-float"
              data-testid="closing-float"
              class="num"
              inputmode="decimal"
              aria-describedby="tc-float-hint"
              .value=${this.closingFloat}
              @input=${(e: Event) => (this.closingFloat = (e.target as HTMLInputElement).value)}
            />
            <div id="tc-float-hint" class="fieldhint">Stays in the drawer overnight</div>
          </div>
        </div>
        <div class="actions">
          <button @click=${() => (this.step = 'movements')}>Back</button>
          <button data-testid="post-count" class="primary" ?disabled=${this.busy} @click=${this.submitCount}>
            Post blind count
          </button>
        </div>
      </div>
    `;
  }

  // The cash row is rendered from the cash summary, not the per-tender row
  // (the reference's DIB-513): the summary puts the float in the math, and the
  // chip and the row must state one answer, never two.
  private cashRow(z: ZReport, t: TenderTotal): TenderTotal {
    if (t.method !== 'CASH') return t;
    return {
      ...t,
      expected_cents: z.cash.expected_cents,
      actual_cents: z.cash.counted_cents,
      variance_cents: z.cash.over_short_cents,
    };
  }

  private zreportStep() {
    const z = this.zreport;
    if (!z) return html`<div class="err" role="alert">Z-report unavailable.</div>`;
    const os = z.cash.over_short_cents;
    return html`
      <div data-testid="zreport-step">
        <table data-testid="zreport-table">
          <thead>
            <tr>
              <th>Tender</th>
              <th class="num">Expected</th>
              <th class="num">Counted</th>
              <th class="num">Variance</th>
              <th class="num">Lodged</th>
            </tr>
          </thead>
          <tbody>
            ${z.per_tender.map((raw) => {
              const t = this.cashRow(z, raw);
              return html`<tr>
                <td>${tenderMethodWord(t.method)}</td>
                <td class="num">${formatMoney(t.expected_cents)}</td>
                <td class="num">${formatMoney(t.actual_cents)}</td>
                <td class="num ${t.variance_cents === 0 ? '' : t.variance_cents > 0 ? 'pos' : 'neg'}">
                  ${formatMoney(t.variance_cents)}
                </td>
                <td class="num">${formatMoney(t.lodged_cents)}</td>
              </tr>`;
            })}
          </tbody>
        </table>
        ${this.movements.length > 0
          ? html`
              <ul class="moves" data-testid="zreport-movements">
                ${this.movements.map(
                  (m) => html`<li>
                    <span>${m.movement_type} ${formatMoney(m.amount_cents)}</span>
                    <span class="why">${m.reason || m.reference || ''}</span>
                  </li>`,
                )}
              </ul>
            `
          : nothing}
        <div class="notice" style="margin-top:14px">
          Opening float ${formatMoney(z.cash.opening_float_cents)} · closing float kept
          ${formatMoney(z.closing_float_cents)}
        </div>
        <span data-testid="overshort-chip" class="chip ${this.needsManager ? 'warn' : 'ok'}">
          over/short ${formatMoney(os)}${this.policy ? ` · threshold ${formatMoney(this.policy.variance_threshold_cents)}` : ''}
        </span>
        ${this.needsManager
          ? html`
              <div class="notice" style="margin-top:12px" data-testid="manager-notice">
                The variance is over the branch threshold, so a <b>manager</b> must complete this close
                and record why.
              </div>
              <label>Override note (required)</label>
              <textarea
                data-testid="override-notes"
                rows="2"
                .value=${this.overrideNotes}
                @input=${(e: Event) => (this.overrideNotes = (e.target as HTMLTextAreaElement).value)}
              ></textarea>
            `
          : nothing}
        <div class="actions">
          <button data-testid="finish-later" @click=${this.close}>Finish later · stays counted</button>
          <button
            data-testid="complete-close"
            class="primary"
            ?disabled=${this.busy || (this.needsManager && !this.overrideNotes.trim())}
            @click=${this.complete}
          >
            Complete close
          </button>
        </div>
      </div>
    `;
  }

  private doneStep() {
    return html`
      <div class="okmsg" style="font-size:14px" role="status" data-testid="till-done-step">Till closed and reconciled.</div>
      <div class="notice">Z-report on record · the closing float stays in the drawer · drops held for the bank run</div>
      <div class="actions">
        <span></span>
        <button class="primary" data-testid="till-done" @click=${this.close}>Done</button>
      </div>
    `;
  }

  render() {
    const titles: Record<Step, string> = {
      movements: 'End of day · drawer movements',
      count: 'End of day · blind count',
      zreport: 'Z-report · reconciliation',
      done: 'Till closed',
    };
    return html`
      <div class="hos-dialog" data-testid="till-close-dialog" @click=${(e: Event) => e.target === e.currentTarget && this.step !== 'zreport' && this.close()}>
        <div class="card">
          <h2>${titles[this.step]}</h2>
          <p class="sub">Register 01 · the close posts its over or short to the books</p>
          ${this.step === 'movements' ? this.movementsStep() : nothing}
          ${this.step === 'count' ? this.countStep() : nothing}
          ${this.step === 'zreport' ? this.zreportStep() : nothing}
          ${this.step === 'done' ? this.doneStep() : nothing}
          ${this.error ? html`<div class="err" role="alert">${this.error}</div>` : nothing}
        </div>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-till-close': HosTillClose;
  }
}
