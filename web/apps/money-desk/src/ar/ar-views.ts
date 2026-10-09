// The receivables slice's screens, as plain render functions of a model. They
// hold no state and own no events: every control is a plain element carrying a
// data-action name and the slice's click listener acts on it (the design
// system's position rules allow no @ or . binding in an app, and the only
// bound values are class, role, aria- and data- attributes). Form fields carry
// data-field names and are read when the button is pressed; none is bound.
//
// Every figure on a screen is the server's, formatted. The one place the app
// does bookkeeping is the apply column (ar-logic allocateOldestFirst and the
// applied against unapplied lines), which only proposes what is posted; the
// server accepts or refuses every post and its balances are what show after.
//
// References: hh-sup/design-refs/money-desk/ receivables-home, account-open-invoices
// and credit-hold (light and brand, 1440 and 390), drawn from the original
// app's AR landing, invoice register and the AR console screen; anything the references
// mark "later" reads as later in words here.
import { html, nothing, type TemplateResult } from "lit";
import type {
  BillingAgingRow,
  BillingARLanding,
  BillingCreditHold,
  BillingInvoiceDetail,
  BillingPaperPathEntry,
  BillingRegisterRow,
  BillingStatement,
  Branch,
  TillBouncedPayment,
  TillInvoicePayments,
  TillPaymentHistory,
} from "@hardscapehouse/api-client";
import { cardRange, cardTitle, holdKindWord, holdMarks, relativeDay, rowMarks, type Mark } from "./ar-logic.js";
import { formatMoney, parseAmountToCents } from "./ar-money.js";

export type Tab = "receivables" | "statements" | "holds";
export type RegisterFilter = "open" | "overdue" | "promised" | "disputed" | "paid";
export type RightMode = "receive" | "invoice" | "promise";
export type PayMethod = "CHEQUE" | "EFT" | "CARD" | "CASH";
export type WorkChoice = "replacement" | "promise" | "reminder";

export interface AccountModel {
  readonly customerId: string;
  readonly name: string;
  readonly accountNo: string;
  readonly terms: number | null;
  readonly branchName: string;
  readonly invoices: readonly BillingRegisterRow[];
  readonly showAll: boolean;
  readonly sub: "invoices" | "statement";
  readonly right: RightMode;
  readonly selectedInvoiceId: string | null;
  readonly detail: BillingInvoiceDetail | null;
  readonly payments: TillInvoicePayments | null;
  readonly bounceFor: string | null;
  readonly statement: BillingStatement | "none" | null;
  readonly method: PayMethod;
  readonly amountText: string;
  readonly apply: Readonly<Record<string, string>>;
}

export interface BouncedWork {
  readonly history: TillPaymentHistory;
  readonly accountName: string;
  readonly choice: WorkChoice;
  readonly note: string;
}

export interface DeskModel {
  readonly tab: Tab;
  readonly asOf: string;
  readonly nowMs: number;
  readonly roleCode: string;
  readonly takenBy: string;
  readonly canLift: boolean;
  readonly landing: BillingARLanding | null;
  readonly aging: readonly BillingAgingRow[];
  readonly rows: readonly BillingRegisterRow[];
  readonly filter: RegisterFilter;
  readonly cardFilter: string | null;
  readonly selection: { readonly count: number; readonly cents: number } | null;
  readonly bounced: readonly TillBouncedPayment[];
  readonly holds: readonly BillingCreditHold[];
  readonly holdScope: "active" | "all";
  readonly branches: readonly Branch[];
  readonly work: BouncedWork | null;
  readonly picker: boolean;
  readonly statements: ReadonlyMap<string, BillingStatement | "none">;
  readonly selectedHoldId: string | null;
  readonly holdPanel: "about" | "lift" | "place-later";
  readonly liftChoice: "lift" | "override";
  readonly account: AccountModel | null;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
}

// ---- small parts -------------------------------------------------------------------------------

function messages(model: DeskModel): TemplateResult {
  return html`
    ${model.error !== "" ? html`<p class="alert alert-error" role="alert" data-error>${model.error}</p>` : nothing}
    ${model.notice !== "" ? html`<p class="alert alert-ok" role="status" data-notice>${model.notice}</p>` : nothing}
  `;
}

function markPill(mark: Mark): TemplateResult {
  return html`<span class="pill pill-${mark.tone} ${mark.dashed ? "pill-dashed" : ""}">${mark.word}</span>`;
}

function later(): TemplateResult {
  return html`<span class="later">later</span>`;
}

function dateOnly(stamp: string): string {
  return stamp.slice(0, 10);
}

function accountName(model: DeskModel, customerId: string | undefined): string {
  if (customerId === undefined) return "Account";
  return model.aging.find((a) => a.customer_id === customerId)?.customer_name ?? "Account";
}

function branchName(model: DeskModel, id: string): string {
  return model.branches.find((b) => b.id === id)?.name.replace(/ \(demo\)$/, "") ?? "";
}

function segmented(label: string, items: readonly { id: string; word: string }[], current: string, action: string): TemplateResult {
  return html`
    <div class="seg" role="group" aria-label="${label}">
      ${items.map((i) => html`<button type="button" class="seg-item" data-action="${action}" data-choice="${i.id}" aria-pressed="${current === i.id ? "true" : "false"}">${i.word}</button>`)}
    </div>
  `;
}

function tabsBar(model: DeskModel): TemplateResult {
  const tab = (id: Tab, word: string): TemplateResult => html`<button type="button" class="tab" data-action="tab" data-tab="${id}" aria-current="${model.tab === id ? "true" : "false"}">${word}</button>`;
  return html`
    <nav class="tabs" aria-label="Money Desk views">
      ${tab("receivables", "Receivables")} ${tab("statements", "Statements")} ${tab("holds", "Credit holds")}
      <span class="tab tab-later" aria-disabled="true">Payables ${later()}</span>
    </nav>
  `;
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

// ---- receivables home --------------------------------------------------------------------------

const TILE_ORDER = ["card.escalation", "card.late", "card.warn", "card.current"] as const;
const TILE_WORD: Record<string, string> = { ESCALATION: "Escalation", LATE: "Late", WARN: "Overdue", CURRENT: "Current" };

function tiles(model: DeskModel): TemplateResult {
  const cards = model.landing?.cards ?? [];
  return html`
    <section class="tiles" aria-label="Receivables by age">
      ${TILE_ORDER.map((id) => cards.find((c) => c.id === id)).map((card) =>
        card === undefined
          ? nothing
          : html`
              <article class="tile" data-card="${card.id}">
                <h3 data-card-title>${cardTitle(card.bucket)}</h3>
                <p class="tile-figure tile-${card.bucket.toLowerCase()}">${formatMoney(card.total_cents)}</p>
                <p class="dim">${card.accounts} ${card.accounts === 1 ? "account" : "accounts"} · ${cardRange(card)}</p>
                <button type="button" class="link" data-action="card" data-card-id="${card.id}" aria-pressed="${model.cardFilter === card.id ? "true" : "false"}">${model.cardFilter === card.id ? "Showing" : "Open"} ›</button>
              </article>
            `,
      )}
    </section>
  `;
}

const FILTERS: readonly { id: RegisterFilter; word: string }[] = [
  { id: "open", word: "Open" },
  { id: "overdue", word: "Overdue" },
  { id: "promised", word: "Promised" },
  { id: "disputed", word: "Disputed" },
  { id: "paid", word: "Paid" },
];

function shownRows(model: DeskModel): BillingRegisterRow[] {
  const ids = model.cardFilter === null ? null : new Set(model.landing?.cards.find((c) => c.id === model.cardFilter)?.account_ids ?? []);
  let rows = model.rows.filter((r) => ids === null || ids.has(r.customer_id));
  if (model.filter === "overdue") rows = rows.filter((r) => r.days_overdue > 0 && r.status !== "PAID");
  else if (model.filter === "promised") rows = rows.filter((r) => typeof r.promised_date === "string" && r.promised_date !== "");
  else if (model.filter === "disputed") rows = rows.filter((r) => r.disputed);
  else if (model.filter === "paid") rows = rows.filter((r) => r.status === "PAID");
  else rows = rows.filter((r) => r.status !== "PAID");
  return rows.sort((a, b) => b.days_overdue - a.days_overdue || a.due_date.localeCompare(b.due_date));
}

export function shownInvoiceIds(model: DeskModel): string[] {
  return shownRows(model).map((r) => r.id);
}

function registerRowView(model: DeskModel, r: BillingRegisterRow): TemplateResult {
  const overdue = r.days_overdue > 0 && r.status !== "PAID";
  const acct = model.aging.find((a) => a.customer_id === r.customer_id);
  const terms = acct === undefined ? "" : ` · Net ${acct.payment_terms_days}`;
  return html`
    <tr data-register-row>
      <td><span class="who">${r.invoice_no}</span><span class="dim small">${r.order_no ?? ""}</span></td>
      <td><span>${r.customer_name}</span><span class="dim small">${r.account_no}${terms}</span></td>
      <td>${branchName(model, r.branch_id)}</td>
      <td class="${overdue ? "late" : ""}">${relativeDay(r.due_date, model.asOf)}</td>
      <td><span class="marks">${rowMarks(r).map(markPill)}</span></td>
      <td class="num">${formatMoney(r.balance_cents)}</td>
      <td class="act">
        ${r.disputed || r.status === "DISPUTED"
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="open-account" data-customer-id="${r.customer_id}" data-invoice-id="${r.id}" data-right="invoice">Open dispute</button>`
          : r.status === "PAID"
            ? html`<button type="button" class="btn btn-secondary small-btn" data-action="open-account" data-customer-id="${r.customer_id}" data-invoice-id="${r.id}" data-right="invoice">Open invoice</button>`
            : html`<button type="button" class="btn btn-secondary small-btn" data-action="open-account" data-customer-id="${r.customer_id}" data-invoice-id="${r.id}" data-right="receive">Record payment</button>`}
      </td>
    </tr>
  `;
}

function register(model: DeskModel): TemplateResult {
  const rows = shownRows(model);
  return html`
    <section class="panel" aria-label="AR register">
      <div class="panel-head">
        <h3>AR register</h3>
        <div class="tools">
          ${segmented("Register filter", FILTERS, model.filter, "filter")}
          <button type="button" class="btn btn-secondary small-btn" data-action="download-csv">Download CSV</button>
        </div>
      </div>
      ${rows.length === 0
        ? html`<div class="empty"><strong>No invoices in this view.</strong><span class="dim">Nothing matches the filter.</span></div>`
        : html`
            <div class="scroll">
              <table class="stack reg">
                <thead>
                  <tr><th>Invoice</th><th>Account</th><th>Branch</th><th>Due</th><th>Status</th><th class="num">Balance</th><th><span class="visually-hidden">Act</span></th></tr>
                </thead>
                <tbody>${rows.map((r) => registerRowView(model, r))}</tbody>
              </table>
            </div>
          `}
      <p class="dim small foot" data-register-foot>
        ${model.selection === null ? "" : `${model.selection.count} ${model.selection.count === 1 ? "invoice" : "invoices"}, ${formatMoney(model.selection.cents)}. `}Overdue first, then by due date. A promise or a dispute rides the row as a second mark, never a colour alone.
      </p>
    </section>
  `;
}

function needsYou(model: DeskModel): TemplateResult {
  const holds = model.holds.filter((h) => h.status === "ACTIVE" && holdMarks(h, model.nowMs).indexOf("override in force") === -1);
  const paper: readonly BillingPaperPathEntry[] = model.landing?.paper_path ?? [];
  return html`
    <section class="panel needs" aria-label="Needs you">
      <h3>Needs you</h3>
      <ul class="needlist">
        ${model.bounced.map(
          (p) => html`
            <li class="need need-stop" data-bounced-row>
              <button type="button" class="need-body" data-action="work-bounced" data-payment-id="${p.payment_id}" data-customer-id="${p.customer_id ?? ""}">
                <strong>Payment bounced</strong>
                <span class="dim small">${accountName(model, p.customer_id)} · ${p.method.toLowerCase()} ${p.payment_no} · <b>${formatMoney(p.amount_cents)}</b> returned, fee ${formatMoney(p.fee_cents)}</span>
              </button>
              <span class="pill pill-plain">Bounced</span>
            </li>
          `,
        )}
        ${holds.map(
          (h) => html`
            <li class="need need-watch" data-need-hold>
              <button type="button" class="need-body" data-action="show-hold" data-hold-id="${h.id}">
                <strong>On credit hold</strong>
                <span class="dim small">${h.customer_name ?? accountName(model, h.customer_id)} · ${holdKindWord(h.hold_kind)}, placed ${h.auto_placed ? "by the sweep" : "by hand"} · ${h.reason}</span>
              </button>
              <span class="pill pill-plain">Credit hold</span>
            </li>
          `,
        )}
        ${paper.map(
          (e) => html`
            <li class="need need-info" data-need-paper>
              <button type="button" class="need-body" data-action="remind-account" data-customer-id="${e.customer_id}">
                <strong>Reminder never opened</strong>
                <span class="dim small">${e.customer_name} · paper path, ${e.window_days} days · call the AP contact</span>
              </button>
              <span class="pill pill-plain">Remind</span>
            </li>
          `,
        )}
        ${model.bounced.length + holds.length + paper.length === 0 ? html`<li class="dim">Nothing needs you right now.</li>` : nothing}
      </ul>
      <p class="dim small">The queue is the role's own: payables and receivables sees bounced payments, holds and the paper path. Lifting a hold is the controller's (row 345).</p>
    </section>
  `;
}

const CHOICES: readonly { id: WorkChoice; head: string; body: string }[] = [
  { id: "replacement", head: "Replacement payment received", body: "Record it against the invoices it reopened; the hold stays until the controller lifts it." },
  { id: "promise", head: "Promise to pay", body: "Record a date and an amount; the queue keeps the item until it lands." },
  { id: "reminder", head: "Send a reminder", body: "To the AP contact on file, by email." },
];

function bouncedWork(model: DeskModel, work: BouncedWork): TemplateResult {
  const h = work.history;
  const reopened = h.allocations.map((a) => a.invoice_no).join(" and ");
  const bounce = model.bounced.find((b) => b.payment_id === h.payment_id);
  return html`
    <section class="panel work" aria-label="Payment bounced" data-bounced-work>
      <div class="panel-head"><h2>Payment bounced</h2><span class="pill pill-stop">Bounced</span></div>
      <dl class="facts">
        <dt>Account</dt><dd>${work.accountName}</dd>
        <dt>Payment</dt><dd>${h.payment_no} · ${h.method.toLowerCase()} · ${formatMoney(h.amount_cents)}</dd>
        <dt>What happened</dt><dd>${bounce?.reason ?? "Returned by the bank"}. Reversed across ${reopened === "" ? "no invoices" : reopened}${bounce === undefined ? "" : `; fee ${formatMoney(bounce.fee_cents)} charged`}.</dd>
        <dt>Received by</dt><dd>${h.received_by_name ?? "Not recorded"}, ${relativeDay(h.received_at, model.asOf)}</dd>
      </dl>
      <h3>Work it</h3>
      <div class="choices" role="radiogroup" aria-label="How to work it">
        ${CHOICES.map(
          (c) => html`
            <button type="button" class="choice" role="radio" aria-checked="${work.choice === c.id ? "true" : "false"}" data-action="work-choice" data-choice="${c.id}">
              <span class="radio"></span><span><strong>${c.head}</strong><span class="dim small">${c.body}</span></span>
            </button>
          `,
        )}
      </div>
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="work-go">${work.choice === "reminder" ? "Send reminder" : work.choice === "promise" ? "Record promise" : "Record payment"}</button>
        <button type="button" class="btn btn-secondary" data-action="close-work">Cancel</button>
      </div>
      ${work.note === "" ? nothing : html`<p class="dim small">${work.note}</p>`}
    </section>
  `;
}

function picker(model: DeskModel): TemplateResult {
  return html`
    <section class="panel" aria-label="Record a payment" data-picker>
      <div class="panel-head"><h3>Record a payment</h3><button type="button" class="btn btn-secondary small-btn" data-action="close-picker">Cancel</button></div>
      <div class="inline-form">
        <label>Account
          <select data-field="picker-account">${model.aging.map((a) => html`<option>${a.customer_name}</option>`)}</select>
        </label>
        <button type="button" class="btn btn-primary" data-action="picker-go">Open account</button>
      </div>
    </section>
  `;
}

export function renderHome(model: DeskModel): TemplateResult {
  return html`
    <div class="head">
      <div>
        <h1>Receivables</h1>
        <p class="dim">The AR register with its aging, overdue first. Bounced payments and accounts on hold wait in Needs you.</p>
      </div>
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="open-picker">Record a payment</button>
        <button type="button" class="btn btn-secondary" data-action="run-statements">Run statements</button>
      </div>
    </div>
    ${tabsBar(model)} ${messages(model)} ${model.picker ? picker(model) : nothing}
    <div class="cols">
      <div class="col-main">${tiles(model)} ${register(model)}</div>
      <div class="col-side">${model.work === null ? nothing : bouncedWork(model, model.work)} ${needsYou(model)}</div>
    </div>
  `;
}

// ---- statements tab ----------------------------------------------------------------------------

export function renderStatements(model: DeskModel): TemplateResult {
  return html`
    <div class="head">
      <div><h1>Statements</h1><p class="dim">One statement per account with an open balance. Run them all at month end, or one account from its page.</p></div>
      <div class="actions"><button type="button" class="btn btn-primary" data-action="run-statements" ?disabled=${model.busy}>Run for every account</button></div>
    </div>
    ${tabsBar(model)} ${messages(model)}
    <section class="panel" aria-label="Statements by account">
      <div class="panel-head"><h3>Statements</h3></div>
      <div class="scroll">
        <table>
          <thead><tr><th>Account</th><th class="num">Balance</th><th>Last statement</th><th><span class="visually-hidden">Open</span></th></tr></thead>
          <tbody>
            ${model.aging.map((a) => {
              const s = model.statements.get(a.customer_id);
              return html`
                <tr data-statement-row>
                  <td><span class="who">${a.customer_name}</span><span class="dim small">${a.account_no}</span></td>
                  <td class="num">${formatMoney(a.total_cents)}</td>
                  <td>${s === undefined ? "Loading" : s === "none" ? "None yet" : `As of ${s.as_of_date}`}</td>
                  <td class="act"><button type="button" class="btn btn-secondary small-btn" data-action="open-account" data-customer-id="${a.customer_id}" data-right="receive" data-sub="statement">Open statement</button></td>
                </tr>
              `;
            })}
          </tbody>
        </table>
      </div>
    </section>
  `;
}

// ---- credit holds ------------------------------------------------------------------------------

function holdActions(model: DeskModel, h: BillingCreditHold): TemplateResult {
  const held = !model.canLift;
  return html`
    <div class="holdbtns">
      <button type="button" class="btn btn-secondary small-btn ${held ? "held" : ""}" data-action="hold-act" data-hold-id="${h.id}" data-choice="override" aria-disabled="${held ? "true" : "false"}">Override...</button>
      <button type="button" class="btn btn-secondary small-btn ${held ? "held" : ""}" data-action="hold-act" data-hold-id="${h.id}" data-choice="lift" aria-disabled="${held ? "true" : "false"}">Lift...</button>
    </div>
  `;
}

function holdsPanel(model: DeskModel): TemplateResult {
  const hold = model.holds.find((h) => h.id === model.selectedHoldId);
  if (model.holdPanel === "place-later") {
    return html`
      <section class="panel side" aria-label="Place a hold" data-hold-panel>
        <div class="panel-head"><h2>Place a hold</h2>${later()}</div>
        <p>Placing a hold by hand is later: the core has no route for it yet. The sweep places the Aged AR, Over limit and Pay before release holds, and a returned payment places its own.</p>
        <p class="dim small">When it lands, a manual hold will carry a reason and the list of what it blocks (new orders at the till, release to dispatch, portal checkout), and both the branch and the account's sales rep will hear of it.</p>
        <div class="actions"><button type="button" class="btn btn-secondary" data-action="hold-close">Close</button></div>
      </section>
    `;
  }
  if (hold === undefined) {
    return html`
      <section class="panel side" data-hold-panel>
        <h2>About a hold</h2>
        <p class="dim">Pick a hold to read why it was placed. A hold stops release at the till, the gate and the portal.</p>
      </section>
    `;
  }
  const name = hold.customer_name ?? accountName(model, hold.customer_id);
  const facts = html`
    <dl class="facts">
      <dt>Account</dt><dd>${name}</dd>
      <dt>Placed</dt><dd>${relativeDay(hold.created_at, model.asOf)} ${hold.auto_placed ? "by the credit sweep · automatic" : "by hand"}</dd>
      <dt>Why</dt><dd>${hold.reason}</dd>
      ${hold.override_until !== undefined && hold.override_until !== null ? html`<dt>Override</dt><dd>Until ${dateOnly(hold.override_until)}${hold.override_reason ? `: ${hold.override_reason}` : ""}</dd>` : nothing}
    </dl>
  `;
  if (!model.canLift) {
    return html`
      <section class="panel side" aria-label="About this hold" data-hold-panel>
        <div class="panel-head"><h2>About this hold</h2><span class="pill pill-stop">${holdKindWord(hold.hold_kind)}</span></div>
        ${facts}
        <p class="note" data-held-words>Lifting and overriding are the controller's, held for this role (row 345). What you can do: record the payment that clears it, or a promise to pay, and the controller decides.</p>
        <div class="actions"><button type="button" class="btn btn-secondary" data-action="open-account" data-customer-id="${hold.customer_id}" data-right="receive">Open the account</button></div>
      </section>
    `;
  }
  return html`
    <section class="panel side" aria-label="Lift the hold" data-hold-panel>
      <div class="panel-head"><h2>Lift the hold</h2><span class="pill pill-stop">${holdKindWord(hold.hold_kind)}</span></div>
      ${facts}
      <h3>Decision</h3>
      <div class="choices" role="radiogroup" aria-label="Decision">
        <button type="button" class="choice" role="radio" aria-checked="${model.liftChoice === "lift" ? "true" : "false"}" data-action="lift-choice" data-choice="lift"><span class="radio"></span><span><strong>Lift the hold</strong><span class="dim small">Ends the hold now. The sweep may place it again if the balance ages past the policy edge.</span></span></button>
        <button type="button" class="choice" role="radio" aria-checked="${model.liftChoice === "override" ? "true" : "false"}" data-action="lift-choice" data-choice="override"><span class="radio"></span><span><strong>Override instead</strong><span class="dim small">Keep the hold on the record and let the account transact until the override expires.</span></span></button>
      </div>
      <label>Reason<textarea data-field="release-reason" rows="3" placeholder="Why this is right"></textarea></label>
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="lift-go" ?disabled=${model.busy}>${model.liftChoice === "lift" ? "Lift hold" : "Override hold"}</button>
        <button type="button" class="btn btn-secondary" data-action="hold-close">Cancel</button>
      </div>
      <p class="ok-note small"><b>Audited.</b> The release carries your role, the reason and the time.</p>
    </section>
  `;
}

export function renderHolds(model: DeskModel): TemplateResult {
  const holds = model.holds.filter((h) => model.holdScope === "all" || h.status === "ACTIVE");
  return html`
    <div class="head">
      <div><h1>Credit holds</h1><p class="dim">A hold stops release at the till, the gate and the portal. Lifting and overriding both carry a reason, and both are audited.</p></div>
      <div class="actions"><button type="button" class="btn btn-primary" data-action="place-later">Place a hold ${later()}</button></div>
    </div>
    ${tabsBar(model)} ${messages(model)}
    <div class="cols">
      <section class="panel col-main" aria-label="Credit holds">
        <div class="panel-head">
          <h3>Credit holds</h3>
          <div class="tools">
            ${segmented("Hold scope", [{ id: "active", word: "Active" }, { id: "all", word: "All" }], model.holdScope, "hold-scope")}
            <button type="button" class="btn btn-secondary small-btn ${model.canLift ? "" : "held"}" data-action="run-sweep" aria-disabled="${model.canLift ? "false" : "true"}">Run the sweep</button>
          </div>
        </div>
        ${holds.length === 0
          ? html`<div class="empty"><strong>No account is on hold.</strong></div>`
          : html`
              <div class="scroll">
                <table class="stack holdtbl">
                  <thead><tr><th>Account</th><th>Kind</th><th>Reason</th><th>Placed</th><th><span class="visually-hidden">Act</span></th></tr></thead>
                  <tbody>
                    ${holds.map(
                      (h) => html`
                        <tr data-hold-row aria-current="${model.selectedHoldId === h.id ? "true" : "false"}">
                          <td><button type="button" class="link-row" data-action="show-hold" data-hold-id="${h.id}"><span class="who">${h.customer_name ?? accountName(model, h.customer_id)}</span></button></td>
                          <td><span class="marks"><span class="pill pill-stop">${holdKindWord(h.hold_kind)}</span>${holdMarks(h, model.nowMs).map((m) => html`<span class="pill pill-plain ${m === "override in force" ? "pill-dashed" : ""}">${m}</span>`)}${h.status === "RELEASED" ? html`<span class="pill pill-good">released</span>` : nothing}</span></td>
                          <td>${h.reason}</td>
                          <td>${relativeDay(h.created_at, model.asOf)}<span class="dim small">${h.auto_placed ? "by the credit sweep" : "by hand"}</span></td>
                          <td class="act">${h.status === "ACTIVE" ? holdActions(model, h) : nothing}</td>
                        </tr>
                      `,
                    )}
                  </tbody>
                </table>
              </div>
            `}
        <p class="dim small foot">An override leaves the hold in place and lets the account transact until it expires. Lifting ends the hold. Both carry a reason and both are audited.</p>
        <p class="dim small">Lifting and overriding are the controller's; payables and receivables places and reports (row 345). Held controls go grey, never faint.</p>
      </section>
      <div class="col-side">${holdsPanel(model)}</div>
    </div>
  `;
}

// ---- an account --------------------------------------------------------------------------------

function applyCents(model: AccountModel, id: string): number {
  return parseAmountToCents(model.apply[id] ?? "") ?? 0;
}

function invoiceTable(model: DeskModel, account: AccountModel): TemplateResult {
  const rows = account.invoices.filter((r) => account.showAll || r.status !== "PAID");
  return html`
    <section class="panel" aria-label="Open invoices">
      <div class="panel-head">
        <h3>Open invoices</h3>
        ${segmented("Which invoices", [{ id: "open", word: "Open" }, { id: "all", word: "All" }], account.showAll ? "all" : "open", "invoice-scope")}
      </div>
      <div class="scroll">
        <table class="stack inv">
          <thead>
            <tr><th>Invoice</th><th>Issued</th><th>Due</th><th>Status</th><th class="num">Amount</th><th class="num">Paid</th><th class="num">Balance</th><th class="num">Apply</th></tr>
          </thead>
          <tbody>
            ${rows.map((r) => {
              const paid = r.status === "PAID";
              return html`
                <tr data-invoice-row aria-current="${account.selectedInvoiceId === r.id ? "true" : "false"}">
                  <td><button type="button" class="link-row" data-action="open-invoice" data-invoice-id="${r.id}"><span class="who">${r.invoice_no}</span></button><span class="dim small">${r.order_no ?? ""}</span></td>
                  <td>${relativeDay(r.issue_date, model.asOf)}</td>
                  <td class="${r.days_overdue > 0 && !paid ? "late" : ""}">${relativeDay(r.due_date, model.asOf)}</td>
                  <td><span class="marks">${rowMarks(r).map(markPill)}</span></td>
                  <td class="num">${formatMoney(r.total_cents)}</td>
                  <td class="num">${formatMoney(r.total_cents - r.balance_cents)}</td>
                  <td class="num"><b>${formatMoney(r.balance_cents)}</b></td>
                  <td class="num"><input type="text" inputmode="decimal" class="apply" data-field="apply" data-key="${r.id}" autocomplete="off" aria-label="Apply to ${r.invoice_no}" ?disabled=${paid} /></td>
                </tr>
              `;
            })}
          </tbody>
          <tfoot>
            <tr><td colspan="6"><b>Open balance</b></td><td class="num"><b data-open-balance>${formatMoney(account.invoices.find((r) => r.account !== undefined)?.account?.open_balance_cents)}</b></td><td></td></tr>
          </tfoot>
        </table>
      </div>
      <p class="dim small foot">Oldest first fills the apply column; edit any cell to split differently. A paid invoice stays for context and takes nothing.</p>
    </section>
  `;
}

function receivePanel(model: DeskModel, account: AccountModel): TemplateResult {
  const open = account.invoices.filter((r) => r.status !== "PAID");
  const amount = parseAmountToCents(account.amountText) ?? 0;
  const lines = open.map((r) => ({ r, cents: applyCents(account, r.id) })).filter((l) => l.cents > 0);
  const applied = lines.reduce((t, l) => t + l.cents, 0);
  const unapplied = amount - applied;
  const onHold = account.invoices.some((r) => r.account_status === "CREDIT_HOLD") || model.holds.some((h) => h.status === "ACTIVE" && h.customer_id === account.customerId);
  return html`
    <section class="panel side" aria-label="Receive a payment" data-receive>
      <div class="panel-head"><h2>Receive a payment</h2><span class="pill ${unapplied === 0 ? "pill-good" : "pill-watch"}" data-unapplied>unapplied ${formatMoney(unapplied)}</span></div>
      <div class="two">
        <label>Amount<input type="text" inputmode="decimal" class="amount" data-field="receive-amount" autocomplete="off" placeholder="0.00" /></label>
        <label>Received at<input type="text" readonly class="ro" data-field="received-at" /></label>
      </div>
      <p class="label">Method</p>
      ${segmented("Method", [{ id: "CHEQUE", word: "Cheque" }, { id: "EFT", word: "EFT" }, { id: "CARD", word: "Card" }, { id: "CASH", word: "Cash" }], account.method, "method")}
      <div class="two">
        <label>Reference<input type="text" data-field="receive-reference" autocomplete="off" placeholder="Cheque number or transfer reference" /></label>
        <label>Taken by<input type="text" readonly class="ro" data-field="taken-by" /></label>
      </div>
      <h3>Allocation</h3>
      <div class="actions"><button type="button" class="btn btn-secondary small-btn" data-action="oldest-first">Oldest first</button><button type="button" class="btn btn-secondary small-btn" data-action="clear-apply">Clear</button></div>
      <ul class="alloc" data-allocation>
        ${lines.map((l) => html`<li><span>${l.r.invoice_no} · ${l.cents >= l.r.balance_cents ? "closes" : "part"}</span><span>${formatMoney(l.cents)}</span></li>`)}
        <li class="dim"><span>Unapplied</span><span>${formatMoney(unapplied)}</span></li>
      </ul>
      <p class="applied"><b>Applied</b><b data-applied>${formatMoney(applied)} of ${formatMoney(amount)}</b></p>
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="apply-payment" ?disabled=${model.busy}>Record payment</button>
        <button type="button" class="btn btn-secondary" data-action="clear-apply">Cancel</button>
      </div>
      ${onHold ? html`<p class="note" data-hold-stays><b>The hold stays.</b> Recording this payment does not lift it; lifting is the controller's.</p>` : nothing}
      <p class="dim small">One payment across several invoices posts once per invoice: the register records a payment per invoice. What shows afterwards is the server's balance.</p>
    </section>
  `;
}

function paymentList(account: AccountModel): TemplateResult {
  const payments = account.payments?.items ?? [];
  if (payments.length === 0) return html`<p class="dim">No payment has been applied to this invoice yet.</p>`;
  return html`
    <ul class="alloc">
      ${payments.map(
        (p) => html`
          <li data-payment-row>
            <span>${p.payment_no} · ${p.method.toLowerCase()} · ${formatMoney(p.amount_cents)}
              ${p.status === "BOUNCED" ? html`<span class="pill pill-stop">Bounced</span>` : html`<span class="pill pill-good">${p.status === "DEPOSITED" ? "Deposited" : "Received"}</span>`}</span>
            ${p.status === "BOUNCED"
              ? nothing
              : account.bounceFor === p.payment_id
                ? html`<span class="inline-form"><label><span class="visually-hidden">Reason it bounced</span><input type="text" data-field="bounce-reason" placeholder="Why it came back, such as cheque returned NSF" autocomplete="off" /></label><button type="button" class="btn btn-danger small-btn" data-action="bounce-confirm" data-payment-id="${p.payment_id}">Confirm bounced</button><button type="button" class="btn btn-secondary small-btn" data-action="bounce-cancel">Cancel</button></span>`
                : html`<button type="button" class="btn btn-secondary small-btn" data-action="bounce-start" data-payment-id="${p.payment_id}">Mark bounced</button>`}
          </li>
        `,
      )}
    </ul>
  `;
}

function invoicePanel(model: DeskModel, account: AccountModel): TemplateResult {
  const d = account.detail;
  if (d === null) return html`<section class="panel side"><p class="dim" role="status">Loading the invoice.</p></section>`;
  return html`
    <section class="panel side" aria-label="Invoice ${d.invoice_no}" data-invoice-panel>
      <div class="panel-head"><h2>Invoice ${d.invoice_no}</h2><button type="button" class="btn btn-secondary small-btn" data-action="back-to-receive">Back to payment</button></div>
      <dl class="facts">
        <dt>Status</dt><dd><span class="marks">${rowMarks({ status: d.status, days_overdue: d.days_overdue, disputed: d.dispute !== undefined, promised_date: d.promise?.promised_date }).map(markPill)}</span></dd>
        <dt>Issued</dt><dd>${d.issue_date}</dd>
        <dt>Due</dt><dd>${d.due_date}${d.days_overdue > 0 ? ` (${d.days_overdue} days over)` : ""}</dd>
        <dt>Total</dt><dd>${formatMoney(d.totals.total_cents)}</dd>
        <dt>Applied</dt><dd>${formatMoney(d.totals.applied_cents)}</dd>
        <dt>Balance</dt><dd data-invoice-balance>${formatMoney(d.totals.balance_cents)}</dd>
        ${d.promise !== undefined ? html`<dt>Promise to pay</dt><dd data-promise>${formatMoney(d.promise.promised_cents)} by ${d.promise.promised_date}${d.promise.note ? `: ${d.promise.note}` : ""}</dd>` : nothing}
        ${d.dispute !== undefined ? html`<dt>Dispute</dt><dd data-dispute>${d.dispute.status === "OPEN" ? "Open" : "Resolved"}: ${formatMoney(d.dispute.amount_cents)}${d.dispute.note ? `, ${d.dispute.note}` : ""}</dd>` : nothing}
      </dl>
      <h3>Payments on this invoice</h3>
      ${paymentList(account)}
    </section>
  `;
}

function promisePanel(model: DeskModel, account: AccountModel): TemplateResult {
  const open = account.invoices.filter((r) => r.status !== "PAID");
  return html`
    <section class="panel side" aria-label="Record a promise to pay" data-promise-form>
      <div class="panel-head"><h2>Record a promise</h2><button type="button" class="btn btn-secondary small-btn" data-action="back-to-receive">Back to payment</button></div>
      <form>
        <label>Invoice<select data-field="promise-invoice">${open.map((r) => html`<option>${r.invoice_no}</option>`)}</select></label>
        <label>Amount promised (dollars)<input type="text" inputmode="decimal" data-field="promise-amount" placeholder="Such as 100.00" autocomplete="off" /></label>
        <label>Promised by date<input type="date" data-field="promise-date" /></label>
        <label>Who promised<input type="text" data-field="promise-by" placeholder="Name or role at the customer" autocomplete="off" /></label>
        <label>Note<input type="text" data-field="promise-note" placeholder="What was said" autocomplete="off" /></label>
        <div class="actions"><button type="button" class="btn btn-primary" data-action="record-promise" ?disabled=${model.busy}>Record promise</button></div>
      </form>
    </section>
  `;
}

function statementDoc(model: DeskModel, account: AccountModel): TemplateResult {
  const s = account.statement;
  return html`
    <div class="toolbar">
      <button type="button" class="btn btn-primary" data-action="send-statement" ?disabled=${model.busy}>Send to the AP contact</button>
      <button type="button" class="btn btn-secondary held" aria-disabled="true" data-action="download-pdf">Download PDF ${later()}</button>
      <button type="button" class="btn btn-secondary" data-action="run-statements" ?disabled=${model.busy}>Run for every account</button>
      <span class="dim small">Month end run: one statement per account with an open balance.</span>
    </div>
    <section class="panel doc" aria-label="Statement" data-statement>
      ${s === null
        ? html`<p class="dim" role="status">Loading the statement.</p>`
        : s === "none"
          ? html`<div class="empty"><strong>No statement has been run for this account yet.</strong><button type="button" class="btn btn-secondary" data-action="run-statement" ?disabled=${model.busy}>Run a statement</button></div>`
          : html`
              <div class="doc-head"><h2>Statement</h2><span class="dim">As of ${s.as_of_date}</span></div>
              <p class="who">${account.name}</p>
              <p class="dim">Account ${account.accountNo}${account.terms === null ? "" : ` · Net ${account.terms}`}</p>
              <div class="scroll">
                <table>
                  <thead><tr><th>Document</th><th>Description</th><th class="num">Charges</th><th class="num">Balance owing</th></tr></thead>
                  <tbody>
                    ${account.invoices.filter((r) => r.balance_cents > 0).map((r) => html`<tr><td>${r.invoice_no}</td><td>Invoice · ${r.order_no ?? ""}</td><td class="num">${formatMoney(r.total_cents)}</td><td class="num">${formatMoney(r.balance_cents)}</td></tr>`)}
                  </tbody>
                </table>
              </div>
              <dl class="strip">
                <div><dt>Current</dt><dd>${formatMoney(s.current_cents)}</dd></div>
                <div><dt>1 to 30</dt><dd>${formatMoney(s.days_30_cents)}</dd></div>
                <div><dt>31 to 60</dt><dd>${formatMoney(s.days_60_cents)}</dd></div>
                <div><dt>Over 60</dt><dd>${formatMoney(s.days_90_plus_cents)}</dd></div>
                <div><dt>Total due</dt><dd data-closing><span class="visually-hidden">Closing balance </span>${formatMoney(s.closing_balance_cents)}</dd></div>
              </dl>
              ${s.days_90_plus_cents > 0 ? html`<p class="overdue-note"><b>${formatMoney(s.days_90_plus_cents)}</b> of this balance is more than 60 days past due. Please contact our office to arrange payment.</p>` : nothing}
              <p class="dim small">Statement generated ${dateOnly(s.created_at)}. Please contact the branch with any discrepancy.</p>
            `}
    </section>
  `;
}

export function renderAccount(model: DeskModel, account: AccountModel): TemplateResult {
  const hold = model.holds.find((h) => h.status === "ACTIVE" && h.customer_id === account.customerId);
  const promised = account.invoices.some((r) => typeof r.promised_date === "string" && r.promised_date !== "");
  const disputes = account.invoices.filter((r) => r.disputed).length;
  return html`
    <button type="button" class="link back" data-action="back">‹ Receivables</button>
    <div class="head">
      <div>
        <h1>${account.name}</h1>
        <p class="dim meta">${account.accountNo}${account.terms === null ? "" : ` · Net ${account.terms}`}${account.branchName === "" ? "" : ` · ${account.branchName}`}
          ${hold === undefined ? nothing : html`<span class="pill pill-stop" data-hold-banner>on hold: ${holdKindWord(hold.hold_kind)}</span>`}
          ${promised ? html`<span class="pill pill-watch pill-dashed">promise open</span>` : nothing}
          ${disputes > 0 ? html`<span class="pill pill-info">${disputes} ${disputes === 1 ? "dispute" : "disputes"}</span>` : nothing}</p>
      </div>
      <div class="actions">
        <button type="button" class="btn btn-secondary" data-action="remind-account" data-customer-id="${account.customerId}">Remind</button>
        <button type="button" class="btn btn-secondary" data-action="promise-panel">Record a promise</button>
        ${hold === undefined ? nothing : html`<button type="button" class="btn btn-secondary" data-action="ask-lift">Ask to lift the hold</button>`}
      </div>
    </div>
    <nav class="tabs" aria-label="Account views">
      <button type="button" class="tab" data-action="sub" data-choice="invoices" aria-current="${account.sub === "invoices" ? "true" : "false"}">Open invoices</button>
      <button type="button" class="tab" data-action="sub" data-choice="statement" aria-current="${account.sub === "statement" ? "true" : "false"}">Statement</button>
      <span class="tab tab-later" aria-disabled="true">Activity ${later()}</span>
      <span class="tab tab-later" aria-disabled="true">Contacts ${later()}</span>
    </nav>
    ${messages(model)}
    ${account.sub === "statement"
      ? statementDoc(model, account)
      : html`
          <div class="cols">
            <div class="col-main">${invoiceTable(model, account)}</div>
            <div class="col-side">${account.right === "invoice" ? invoicePanel(model, account) : account.right === "promise" ? promisePanel(model, account) : receivePanel(model, account)}</div>
          </div>
        `}
  `;
}
