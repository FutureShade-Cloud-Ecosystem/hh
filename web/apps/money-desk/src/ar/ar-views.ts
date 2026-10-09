// The receivables slice's screens, as plain render functions of a model. They
// hold no state and own no events: every control is a plain element carrying a
// data-action name and the slice's click listener acts on it (the design
// system's position rules allow no @ or . binding in an app, and the only
// bound values are class, role, aria- and data- attributes). Form fields carry
// data-field names and are read when the button is pressed; none is bound.
//
// Every figure on a screen is the server's, formatted: the app computes no
// money. References: the original app's AR landing, AR console and
// invoice register files and their verification/ar-landing screens, drawn to
// HH's design system (take the existing and improve it).
import { html, nothing, type TemplateResult } from "lit";
import type {
  BillingAgingRow,
  BillingARLanding,
  BillingCreditHold,
  BillingInvoiceDetail,
  BillingRegisterRow,
  BillingStatement,
  TillBouncedPayment,
  TillInvoicePayments,
  TillPaymentHistory,
} from "@hardscapehouse/api-client";
import { cardRange, cardTitle, holdKindWord, statusTone, statusWord } from "./ar-logic.js";
import { formatMoney } from "./ar-money.js";

export type Tab = "aging" | "bounced" | "holds";

export interface AccountModel {
  readonly customerId: string;
  readonly name: string;
  readonly accountNo: string;
  readonly invoices: readonly BillingRegisterRow[];
  readonly openInvoiceId: string | null;
  readonly detail: BillingInvoiceDetail | null;
  readonly payments: TillInvoicePayments | null;
  readonly bounceFor: string | null;
  readonly statement: BillingStatement | "none" | null;
}

export interface BouncedWork {
  readonly history: TillPaymentHistory;
  readonly accountName: string;
}

export interface DeskModel {
  readonly phase: "loading" | "ready" | "error";
  readonly tab: Tab;
  readonly landing: BillingARLanding | null;
  readonly aging: readonly BillingAgingRow[];
  readonly bounced: readonly TillBouncedPayment[];
  readonly holds: readonly BillingCreditHold[];
  readonly cardFilter: string | null;
  readonly account: AccountModel | null;
  readonly work: BouncedWork | null;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
  readonly asOf: string;
}

function messages(model: DeskModel): TemplateResult {
  return html`
    ${model.error !== "" ? html`<p class="alert alert-error" role="alert" data-error>${model.error}</p>` : nothing}
    ${model.notice !== "" ? html`<p class="alert alert-ok" role="status" data-notice>${model.notice}</p>` : nothing}
  `;
}

function pill(status: string): TemplateResult {
  return html`<span class="status status-${statusTone(status)}">${statusWord(status)}</span>`;
}

function accountName(model: DeskModel, customerId: string | undefined): string {
  if (customerId === undefined) return "Account";
  return model.aging.find((a) => a.customer_id === customerId)?.customer_name ?? "Account";
}

function dateOnly(stamp: string): string {
  return stamp.slice(0, 10);
}

// ---- the way in --------------------------------------------------------------------------------

export function renderLoading(): TemplateResult {
  return html`<p class="dim" role="status">Loading receivables.</p>`;
}

export function renderLoadFailed(error: string): TemplateResult {
  return html`
    <p class="alert alert-error" role="alert" data-error>${error}</p>
    <button type="button" class="btn btn-secondary" data-action="reload">Try again</button>
  `;
}

// ---- the landing: cards, then the tab that is open -----------------------------------------------

function cards(model: DeskModel): TemplateResult {
  const landing = model.landing;
  if (landing === null) return html`${nothing}`;
  return html`
    <section class="panel" aria-label="Receivables by age">
      <div class="panel-head">
        <h3>By age</h3>
        ${model.cardFilter !== null
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="clear-card">Show every account</button>`
          : html`<span class="dim">Press a card to see its accounts.</span>`}
      </div>
      <div class="agecards">
        ${landing.cards.map(
          (card) => html`
            <button type="button" class="agecard" data-action="card" data-card=${card.id} data-card-id=${card.id} aria-pressed="${model.cardFilter === card.id ? "true" : "false"}">
              <span class="kicker" data-card-title>${cardTitle(card.bucket)}</span>
              <span class="figure ${card.bucket === "ESCALATION" ? "stop" : ""}">${formatMoney(card.total_cents)}</span>
              <span class="small">${card.accounts} ${card.accounts === 1 ? "account" : "accounts"} · ${card.invoices} ${card.invoices === 1 ? "invoice" : "invoices"}</span>
              <span class="small">${cardRange(card)}</span>
            </button>
          `,
        )}
      </div>
    </section>
  `;
}

function tabs(model: DeskModel): TemplateResult {
  const tab = (id: Tab, label: string, count: number | null): TemplateResult => html`
    <button type="button" class="tab" data-action="tab" data-tab=${id} data-tab-id=${id} aria-current="${model.tab === id ? "true" : "false"}">${label}${count === null ? nothing : html`<span class="count">${count}</span>`}</button>
  `;
  return html`
    <nav class="tabs" aria-label="Receivables views">
      ${tab("aging", "Aging", null)} ${tab("bounced", "Bounced payments", model.bounced.length)} ${tab("holds", "Credit holds", model.holds.length)}
    </nav>
  `;
}

function agingTable(model: DeskModel): TemplateResult {
  const ids = model.cardFilter === null ? null : new Set(model.landing?.cards.find((c) => c.id === model.cardFilter)?.account_ids ?? []);
  const rows = ids === null ? model.aging : model.aging.filter((a) => ids.has(a.customer_id));
  return html`
    <section class="panel" aria-label="Aging by account">
      <div class="panel-head">
        <h3>Aging</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="refresh">Refresh</button>
      </div>
      ${rows.length === 0
        ? html`<div class="empty"><strong>No accounts to show.</strong><span class="dim">Nothing is owing in this view.</span></div>`
        : html`
            <div class="scroll">
              <table>
                <thead>
                  <tr>
                    <th>Account</th>
                    <th class="num">Current</th>
                    <th class="num">1 to 30</th>
                    <th class="num">31 to 60</th>
                    <th class="num">Over 60</th>
                    <th class="num">Total</th>
                    <th>Status</th>
                    <th><span class="visually-hidden">Open</span></th>
                  </tr>
                </thead>
                <tbody>
                  ${rows.map(
                    (row) => html`
                      <tr data-aging-row>
                        <td><span class="who">${row.customer_name}</span><span class="acct">${row.account_no}</span></td>
                        <td class="num">${formatMoney(row.current_cents)}</td>
                        <td class="num">${formatMoney(row.days_30_cents)}</td>
                        <td class="num">${formatMoney(row.days_60_cents)}</td>
                        <td class="num ${row.days_90_plus_cents > 0 ? "stop" : ""}">${formatMoney(row.days_90_plus_cents)}</td>
                        <td class="num">${formatMoney(row.total_cents)}</td>
                        <td>${row.on_hold ? html`<span class="status status-stop">On hold</span>` : html`<span class="status status-plain">Net ${row.payment_terms_days}</span>`}</td>
                        <td><button type="button" class="btn btn-secondary small-btn" data-action="open-account" data-customer-id=${row.customer_id}>Open account</button></td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
    </section>
  `;
}

function bouncedTab(model: DeskModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Bounced payments">
      <div class="panel-head"><h3>Bounced payments</h3></div>
      ${model.bounced.length === 0
        ? html`<div class="empty"><strong>Nothing has come back from the bank.</strong><span class="dim">A payment marked bounced lands here, with its fee and the invoices it reopened.</span></div>`
        : html`
            <div class="scroll">
              <table>
                <thead>
                  <tr>
                    <th>Payment</th>
                    <th>Account</th>
                    <th>Method</th>
                    <th class="num">Amount</th>
                    <th>Reason</th>
                    <th class="num">Fee</th>
                    <th>Bounced</th>
                    <th><span class="visually-hidden">Work</span></th>
                  </tr>
                </thead>
                <tbody>
                  ${model.bounced.map(
                    (p) => html`
                      <tr data-bounced-row>
                        <td>${p.payment_no}</td>
                        <td>${accountName(model, p.customer_id)}</td>
                        <td>${p.method}</td>
                        <td class="num">${formatMoney(p.amount_cents)}</td>
                        <td>${p.reason}</td>
                        <td class="num">${formatMoney(p.fee_cents)}</td>
                        <td>${dateOnly(p.bounced_at)}</td>
                        <td><button type="button" class="btn btn-secondary small-btn" data-action="work-bounced" data-payment-id=${p.payment_id} data-customer-id=${p.customer_id ?? ""}>Work it</button></td>
                      </tr>
                    `,
                  )}
                </tbody>
              </table>
            </div>
          `}
      <p class="dim sub">To mark a payment bounced, open the account, open the invoice it paid, and press Mark bounced on that payment.</p>
    </section>
    ${model.work === null ? nothing : bouncedWork(model.work)}
  `;
}

function bouncedWork(work: BouncedWork): TemplateResult {
  const h = work.history;
  return html`
    <section class="panel" aria-label="Working a bounced payment" data-bounced-work>
      <div class="panel-head">
        <h3>Working payment ${h.payment_no}</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="close-work">Close</button>
      </div>
      <p>${work.accountName}: ${formatMoney(h.amount_cents)} by ${h.method}, received ${dateOnly(h.received_at)}. The bank returned it, so what it paid is owing again.</p>
      <div class="scroll">
        <table>
          <thead><tr><th>Invoice reopened</th><th class="num">Amount</th><th>State</th></tr></thead>
          <tbody>
            ${h.allocations.map(
              (al) => html`<tr><td>${al.invoice_no}</td><td class="num">${formatMoney(al.amount_cents)}</td><td>${al.reversed_by_nsf === true ? "Reversed by the bounce" : "Applied"}</td></tr>`,
            )}
          </tbody>
        </table>
      </div>
      <div class="actions sub">
        <button type="button" class="btn btn-primary" data-action="open-account" data-customer-id=${h.customer_id ?? ""}>Open the account</button>
        <span class="dim">Chase it there: record a promise to pay, or the payment that replaces it.</span>
      </div>
    </section>
  `;
}

function holdsTab(model: DeskModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Credit holds">
      <div class="panel-head"><h3>Credit holds</h3></div>
      ${messages(model)}
      ${model.holds.length === 0
        ? html`<div class="empty"><strong>No account is on hold.</strong></div>`
        : html`
            <ul class="holdlist">
              ${model.holds.map(
                (h) => html`
                  <li class="hold" data-hold-row>
                    <div class="hold-what">
                      <span class="who">${h.customer_name ?? accountName(model, h.customer_id)}</span>
                      <span class="status status-stop">${holdKindWord(h.hold_kind)}</span>
                      <span class="dim">${h.reason}. Placed ${dateOnly(h.created_at)}.</span>
                    </div>
                    <div class="inline-form">
                      <label>Why it can be released<input type="text" data-field="release-reason" data-hold-id=${h.id} placeholder="Such as paid in full" autocomplete="off" /></label>
                      <button type="button" class="btn btn-secondary" data-action="release-hold" data-hold-id=${h.id}>Release hold</button>
                    </div>
                  </li>
                `,
              )}
            </ul>
          `}
      <p class="dim sub">A hold clears when the payment behind it is recorded. Releasing one by hand is the Financial Controller's or the General Manager's call.</p>
    </section>
  `;
}

export function renderLanding(model: DeskModel): TemplateResult {
  return html`
    <div class="head">
      <h1>Receivables</h1>
      <span class="asof">As of ${model.asOf}</span>
    </div>
    ${model.tab === "holds" ? nothing : messages(model)} ${cards(model)} ${tabs(model)}
    ${model.tab === "aging" ? agingTable(model) : model.tab === "bounced" ? bouncedTab(model) : holdsTab(model)}
  `;
}

// ---- an account ----------------------------------------------------------------------------------

function position(rows: readonly BillingRegisterRow[]): TemplateResult {
  const p = rows.find((r) => r.account !== undefined)?.account;
  if (p === undefined) return html`${nothing}`;
  return html`
    <dl class="position">
      <div><dt>Open balance</dt><dd>${formatMoney(p.open_balance_cents)}</dd></div>
      <div><dt>Overdue</dt><dd>${formatMoney(p.overdue_cents)}</dd></div>
      <div><dt>Credit limit</dt><dd>${formatMoney(p.credit_limit_cents)}</dd></div>
      <div><dt>Available credit</dt><dd>${formatMoney(p.available_credit_cents)}</dd></div>
    </dl>
  `;
}

function invoiceTable(account: AccountModel): TemplateResult {
  return html`
    <div class="scroll">
      <table>
        <thead>
          <tr><th>Invoice</th><th>Due</th><th class="num">Days over</th><th class="num">Total</th><th class="num">Balance</th><th>Status</th><th><span class="visually-hidden">Open</span></th></tr>
        </thead>
        <tbody>
          ${account.invoices.map(
            (r) => html`
              <tr data-invoice-row aria-current="${account.openInvoiceId === r.id ? "true" : "false"}">
                <td><span class="who">${r.invoice_no}</span></td>
                <td>${r.due_date}</td>
                <td class="num">${r.days_overdue}</td>
                <td class="num">${formatMoney(r.total_cents)}</td>
                <td class="num">${formatMoney(r.balance_cents)}</td>
                <td>${pill(r.status)}</td>
                <td><button type="button" class="btn btn-secondary small-btn" data-action="open-invoice" data-invoice-id=${r.id}>Open invoice</button></td>
              </tr>
            `,
          )}
        </tbody>
      </table>
    </div>
  `;
}

function paymentList(account: AccountModel): TemplateResult {
  const payments = account.payments?.items ?? [];
  if (payments.length === 0) return html`<p class="dim">No payment has been applied to this invoice yet.</p>`;
  return html`
    <div class="scroll">
      <table>
        <thead><tr><th>Payment</th><th>Method</th><th class="num">Applied</th><th>Received</th><th>State</th><th><span class="visually-hidden">Bounce</span></th></tr></thead>
        <tbody>
          ${payments.map(
            (p) => html`
              <tr data-payment-row>
                <td>${p.payment_no}</td>
                <td>${p.method}</td>
                <td class="num">${formatMoney(p.amount_cents)}</td>
                <td>${dateOnly(p.received_at)}</td>
                <td>${p.status === "BOUNCED" ? html`<span class="status status-stop">Bounced</span>` : html`<span class="status status-good">${p.status === "RECEIVED" ? "Received" : p.status === "DEPOSITED" ? "Deposited" : p.status}</span>`}</td>
                <td>
                  ${p.status === "BOUNCED"
                    ? nothing
                    : account.bounceFor === p.payment_id
                      ? html`
                          <div class="inline-form">
                            <label><span class="visually-hidden">Reason it bounced</span><input type="text" data-field="bounce-reason" placeholder="Why it came back, such as cheque returned NSF" autocomplete="off" /></label>
                            <button type="button" class="btn btn-danger small-btn" data-action="bounce-confirm" data-payment-id=${p.payment_id}>Confirm bounced</button>
                            <button type="button" class="btn btn-secondary small-btn" data-action="bounce-cancel">Cancel</button>
                          </div>
                        `
                      : html`<button type="button" class="btn btn-secondary small-btn" data-action="bounce-start" data-payment-id=${p.payment_id}>Mark bounced</button>`}
                </td>
              </tr>
            `,
          )}
        </tbody>
      </table>
    </div>
  `;
}

function invoicePanel(account: AccountModel, busy: boolean): TemplateResult {
  const d = account.detail;
  if (d === null) return html`<section class="panel"><p class="dim" role="status">Loading the invoice.</p></section>`;
  const settled = d.totals.balance_cents === 0;
  return html`
    <section class="panel" aria-label="Invoice ${d.invoice_no}" data-invoice-panel>
      <div class="panel-head">
        <h3>Invoice ${d.invoice_no}</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="close-invoice">Close</button>
      </div>
      <dl class="facts">
        <dt>Status</dt><dd>${pill(d.status)}</dd>
        <dt>Issued</dt><dd>${d.issue_date}</dd>
        <dt>Due</dt><dd>${d.due_date}${d.days_overdue > 0 ? ` (${d.days_overdue} days over)` : ""}</dd>
        <dt>Total</dt><dd>${formatMoney(d.totals.total_cents)}</dd>
        <dt>Applied</dt><dd>${formatMoney(d.totals.applied_cents)}</dd>
        <dt>Balance</dt><dd data-invoice-balance>${formatMoney(d.totals.balance_cents)}</dd>
        ${d.promise !== undefined
          ? html`<dt>Promise to pay</dt><dd data-promise>${formatMoney(d.promise.promised_cents)} by ${d.promise.promised_date}${d.promise.note !== undefined && d.promise.note !== "" ? `: ${d.promise.note}` : ""}</dd>`
          : nothing}
      </dl>
      <h3>Payments on this invoice</h3>
      ${paymentList(account)}
      ${settled
        ? html`<p class="sub dim">This invoice is paid in full.</p>`
        : html`
            <div class="forms sub">
              <form aria-label="Record a payment">
                <h3>Record a payment</h3>
                <label>Method
                  <select data-field="pay-method">
                    <option value="CHEQUE">Cheque</option>
                    <option value="EFT">EFT</option>
                    <option value="CASH">Cash</option>
                    <option value="CARD">Card</option>
                  </select>
                </label>
                <label>Amount received (dollars)<input type="text" inputmode="decimal" data-field="pay-amount" placeholder="Such as 100.00" autocomplete="off" /></label>
                <label>Reference<input type="text" data-field="pay-reference" placeholder="Cheque number or transfer reference" autocomplete="off" /></label>
                <div class="actions"><button type="button" class="btn btn-primary" data-action="apply-payment" ?disabled=${busy}>Apply payment</button></div>
              </form>
              <form aria-label="Record a promise to pay">
                <h3>Promise to pay</h3>
                <label>Amount promised (dollars)<input type="text" inputmode="decimal" data-field="promise-amount" placeholder="Such as 100.00" autocomplete="off" /></label>
                <label>Promised by date<input type="date" data-field="promise-date" /></label>
                <label>Who promised<input type="text" data-field="promise-by" placeholder="Name or role at the customer" autocomplete="off" /></label>
                <label>Note<input type="text" data-field="promise-note" placeholder="What was said" autocomplete="off" /></label>
                <div class="actions"><button type="button" class="btn btn-primary" data-action="record-promise" ?disabled=${busy}>Record promise</button></div>
              </form>
            </div>
          `}
    </section>
  `;
}

function statementCard(account: AccountModel, busy: boolean): TemplateResult {
  const s = account.statement;
  return html`
    <section class="panel" aria-label="Statement" data-statement>
      <div class="panel-head">
        <h3>Statement</h3>
        <button type="button" class="btn btn-secondary small-btn" data-action="run-statement" ?disabled=${busy}>Run a statement</button>
      </div>
      ${s === null
        ? html`<p class="dim" role="status">Loading the statement.</p>`
        : s === "none"
          ? html`<p class="dim">No statement has been run for this account yet.</p>`
          : html`
              <dl class="facts">
                <dt>As of</dt><dd>${s.as_of_date}</dd>
                <dt>Current</dt><dd>${formatMoney(s.current_cents)}</dd>
                <dt>1 to 30 days</dt><dd>${formatMoney(s.days_30_cents)}</dd>
                <dt>31 to 60 days</dt><dd>${formatMoney(s.days_60_cents)}</dd>
                <dt>Over 60 days</dt><dd>${formatMoney(s.days_90_plus_cents)}</dd>
                <dt>Closing balance</dt><dd><strong>${formatMoney(s.closing_balance_cents)}</strong></dd>
              </dl>
            `}
    </section>
  `;
}

export function renderAccount(model: DeskModel, account: AccountModel): TemplateResult {
  const onHold = account.invoices.some((r) => r.account_status === "CREDIT_HOLD");
  return html`
    <div class="actions"><button type="button" class="btn btn-secondary" data-action="back">Back to receivables</button></div>
    <div class="head sub">
      <h2>${account.name}</h2>
      <span class="acct">${account.accountNo}</span>
    </div>
    ${messages(model)}
    ${onHold
      ? html`<p class="banner" data-hold-banner><span class="status status-stop">Credit hold</span><span>This account is on credit hold. Recording the payment that clears it is yours; releasing the hold by hand is the Financial Controller's or the General Manager's.</span></p>`
      : nothing}
    ${position(account.invoices)}
    <section class="panel sub" aria-label="Open invoices">
      <div class="panel-head"><h3>Open invoices</h3></div>
      ${account.invoices.length === 0 ? html`<div class="empty"><strong>Nothing is owing on this account.</strong></div>` : invoiceTable(account)}
    </section>
    ${account.openInvoiceId === null ? nothing : invoicePanel(account, model.busy)}
    ${statementCard(account, model.busy)}
  `;
}
