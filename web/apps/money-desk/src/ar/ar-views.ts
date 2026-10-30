// The receivables slice's screens, as plain render functions of a model. They
// hold no state and own no events: every control is a plain element carrying a
// data-action name and the slice's click listener acts on it (the design
// system's position rules allow no @ or . binding in an app, and the only
// bound values are class, role, aria- and data- attributes). Form fields carry
// data-field names and are read when the button is pressed; none is bound.
//
// Every figure on a screen is the server's, formatted. The apply column and the
// applied against unapplied lines are the received payment route's preview: the
// server splits the payment oldest first and the app only shows its answer; the
// server accepts or refuses every post and its balances are what show after.
//
// References: hh-sup/design-refs/money-desk/ receivables-home, account-open-invoices
// and credit-hold (light and brand, 1440 and 390), drawn from the original
// app's AR landing, invoice register and the AR console screen; anything the references
// mark "later" reads as later in words here.
import { html, nothing, type TemplateResult } from "lit";
import type {
  BillingAccountPaymentPolicy,
  BillingAgingRow,
  BillingARLanding,
  BillingCreditHold,
  BillingCustomerWorkspace,
  BillingInvoiceDetail,
  BillingPaperPathEntry,
  BillingRegisterRow,
  BillingStatement,
  Branch,
  TillBouncedPayment,
  TillInvoicePayments,
  TillPaymentHistory,
} from "@hardscapehouse/api-client";
import { renderWorkspace, type WorkspaceTab } from "./ar-workspace.js";
import type { ApplyAnswer } from "./ar-api.js";
import { STATEMENT_OVERDUE_NOTE, STATEMENT_STRIP_LABELS, bucketWord, canVoidReceipt, cardCovers, cardShown, cardTitleFor, creditApprovalWords, creditTargetWords, holdKindWord, holdMarks, isUnpaidInvoice, lineWords, offeredChips, onAccountWords, relativeDay, reopenedInvoiceNos, rowMarks, type Mark, type RegisterChip, pastDueLine } from "./ar-logic.js";
import { formatMoney, parseAmountToCents } from "./ar-money.js";
import type { OfficeModel } from "./office-desk.js";
import { renderContacts, renderInvoiceActions, renderInvoiceCredits, renderReapply, renderRemindersPanels, renderReverse, renderRunPanel, renderVoid } from "./office-views.js";
import { renderSettingsPanels } from "./office-settings-views.js";

export type Tab = "receivables" | "statements" | "holds" | "reminders" | "settings";
// C8 register filter chips. The legacy strings ("open", "overdue", "promised", "disputed", "paid")
// stay valid for backward compatibility with every stored URL and every automation that already
// names them.
export type RegisterFilter = "all" | "past_due" | "current" | "on_hold"
  | "open" | "overdue" | "promised" | "disputed" | "paid";
export type RightMode = "receive" | "invoice" | "promise";
export type PayMethod = "CHEQUE" | "EFT" | "CARD" | "CASH";
export type WorkChoice = "replacement" | "promise" | "reminder";

/**
 * The credit memo form (FW1-12): which invoice the credit is against, or nothing for a credit on the
 * account itself. The amount and the reason are read from the form's own fields when it is sent.
 */
export interface CreditForm {
  readonly invoiceId: string | null;
  /** What the manager has typed into the amount box, so the form can say what the credit will do. */
  readonly amountText: string;
  /** The branch's approval threshold in cents, or null when the desk could not read it. */
  readonly thresholdCents: number | null;
  /**
   * What the core says this credit would come to, and how much room the invoice has left. The
   * figures are the core's: the desk never works out a tax and never guesses a cap. Null until the
   * core answers.
   */
  readonly quote: CreditQuote | null;
  /** The core's own words when the amount does not fit the room; empty when there is nothing to say. */
  readonly quoteError: string;
}

/**
 * What the core says a credit against an invoice comes to, and how much room that invoice has left
 * to credit. The room is the cap the core holds the amount to: what is left BEFORE tax, and the same
 * room WITH tax. A paid invoice still has a room: what has been paid is not what has been credited.
 */
export interface CreditQuote {
  /** What the invoice has left to credit, before tax. */
  readonly remainingSubtotalCents: number;
  /** What the invoice has left to credit, with tax. */
  readonly remainingTotalCents: number;
  /** The core's own words about the amount typed: what the credit comes to, tax included. */
  readonly message: string;
}

export interface AccountModel {
  readonly customerId: string;
  readonly name: string;
  readonly accountNo: string;
  readonly terms: number | null;
  readonly branchName: string;
  readonly invoices: readonly BillingRegisterRow[];
  /** Whether the account's invoices have arrived: a failed read is said in words, never shown as an empty table. */
  readonly invoicesState: "loading" | "ready" | "failed";
  readonly showAll: boolean;
  readonly sub: "invoices" | "statement" | "contacts";
  readonly right: RightMode;
  readonly selectedInvoiceId: string | null;
  readonly detail: BillingInvoiceDetail | null;
  readonly payments: TillInvoicePayments | null;
  readonly bounceFor: string | null;
  readonly statement: BillingStatement | "none" | null;
  readonly method: PayMethod;
  readonly amountText: string;
  /** The invoices picked for the payment, in the order they are sent: the server splits across them. */
  readonly picked: readonly string[];
  /** The server's answer to the payment as it stands, or why it could not answer. */
  readonly preview: ApplyAnswer | null;
  readonly previewError: string;
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
  /** The credit memo form, when it is open; null when it is not. */
  readonly credit: CreditForm | null;
  readonly office: OfficeModel;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
  // The customer account workspace (Change 8 S2 + S2M). workspace is the read payload
  // from billingGetCustomerWorkspace; activeWorkspaceTab is the desktop / mobile
  // workspace tab the operator last picked; workspaceCustomerId is the customer the
  // workspace is currently showing (a back link clears all three).
  readonly workspace: BillingCustomerWorkspace | null;
  readonly workspaceCustomerId: string | null;
  readonly activeWorkspaceTab: WorkspaceTab;
  // The Place-a-hold draft (Change 8 S4): the operator's typed reason and the picked
  // account id, so the form rerenders without losing state. The draft is empty when
  // the panel has never been opened; the desk resets it on close.
  readonly placeHoldDraft: { readonly customerId: string; readonly reason: string };
  // The register summary the Place-a-hold pick-list reads from. A small slice of the
  // register's customer list so the form has SOMETHING to pick without pulling every
  // invoice of every account. The empty slice renders an empty select; the operator
  // can still open the workspace from the Customer tab and place the hold there.
  readonly registerSummary: readonly BillingRegisterRow[];
}

// ---- small parts -------------------------------------------------------------------------------

function messages(model: DeskModel): TemplateResult {
  // tabindex lets the desk move focus to a message when it appears: role=alert announces it, and
  // focus puts a screen reader's reading position and the phone's viewport on it (a payment's
  // unknown outcome or refusal shows here, far above the Record payment button on a phone).
  return html`
    ${model.error !== "" ? html`<p class="alert alert-error" role="alert" data-error tabindex="-1">${model.error}</p>` : nothing}
    ${model.notice !== "" ? html`<p class="alert alert-ok" role="status" data-notice tabindex="-1">${model.notice}</p>` : nothing}
  `;
}

export function markPill(mark: Mark): TemplateResult {
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

export function tabsBar(model: DeskModel): TemplateResult {
  const tab = (id: Tab, word: string): TemplateResult => html`<button type="button" class="tab" data-action="tab" data-tab="${id}" aria-current="${model.tab === id ? "true" : "false"}">${word}</button>`;
  return html`
    <nav class="tabs" aria-label="Money Desk views">
      ${tab("receivables", "Receivables")} ${tab("statements", "Statements")} ${tab("holds", "Credit holds")} ${tab("reminders", "Reminders")} ${tab("settings", "Settings")}
    </nav>
  `;
}

// ---- the way in --------------------------------------------------------------------------------

// The page keeps its frame while it loads or fails, so nothing jumps when the data lands.
function frame(body: TemplateResult): TemplateResult {
  return html`
    <div class="head">
      <div><h1>Receivables</h1><p class="dim">The AR register with its aging, past due first. Bounced payments and accounts on hold wait in Needs you.</p></div>
    </div>
    <nav class="tabs" aria-label="Money Desk views">
      <span class="tab" aria-disabled="true">Receivables</span><span class="tab" aria-disabled="true">Statements</span><span class="tab" aria-disabled="true">Credit holds</span><span class="tab" aria-disabled="true">Reminders</span><span class="tab" aria-disabled="true">Settings</span>
    </nav>
    ${body}
  `;
}

export function renderLoading(): TemplateResult {
  return frame(html`<section class="panel" aria-busy="true"><p class="dim" role="status">Loading receivables.</p><div class="ghost"></div><div class="ghost"></div><div class="ghost"></div></section>`);
}

export function renderLoadFailed(error: string): TemplateResult {
  return frame(html`
    <section class="panel">
      <p class="alert alert-error" role="alert" data-error>${error}</p>
      <button type="button" class="btn btn-secondary" data-action="reload">Try again</button>
    </section>
  `);
}

// ---- receivables home --------------------------------------------------------------------------

const TILE_ORDER = ["card.escalation", "card.late", "card.warn", "card.current"] as const;

function tiles(model: DeskModel): TemplateResult {
  const cards = model.landing?.cards ?? [];
  return html`
    <section class="tiles" aria-label="Receivables by age">
      ${TILE_ORDER.map((id) => cards.find((c) => c.id === id)).map((card) =>
        card === undefined || !cardShown(card)
          ? nothing
          : html`
              <article class="tile" data-card="${card.id}">
                <h3 data-card-title>${cardTitleFor(card)}</h3>
                <p class="tile-figure tile-${card.bucket.toLowerCase()}">${formatMoney(card.total_cents)}</p>
                <p class="dim">${card.accounts} ${card.accounts === 1 ? "account" : "accounts"} · ${cardCovers(card)}</p>
                <button type="button" class="link" data-action="card" data-card-id="${card.id}" aria-pressed="${model.cardFilter === card.id ? "true" : "false"}">${model.cardFilter === card.id ? "Showing" : "Open"} ›</button>
              </article>
            `,
      )}
    </section>
    <p class="dim small" data-card-note>As of ${relativeDay(model.asOf, model.asOf)} · Grouped by your past due settings</p>
  `;
}

// C8 register filter chips. "All" is every open document (excludes PAID), "Past due" is the overdue
// subset, "Current" is the not-yet-past-due subset, "On hold" is the credit-hold subset. The chip
// order is the order the screen reads them: All, then the three status slices an operator most
// often wants to see one at a time. WHICH chips are offered follows the rows the core sent
// (offeredChips): a caller the account position policy refuses is offered the stored set, never
// Current.
const CHIP_WORDS: Record<RegisterChip, string> = {
  all: "All",
  past_due: "Past due",
  current: "Current",
  on_hold: "On hold",
};

// isOpenRow is the shared "is this row one we still have to collect on" predicate. Both "all" and
// "past_due" rely on it: paid invoices never appear, regardless of how late they once were, and
// credit memos are excluded because they offset what is owed rather than what is due.
function isOpenRow(r: BillingRegisterRow): boolean {
  return isUnpaidInvoice(r);
}

function shownRows(model: DeskModel): BillingRegisterRow[] {
  const ids = model.cardFilter === null ? null : new Set(model.landing?.cards.find((c) => c.id === model.cardFilter)?.account_ids ?? []);
  let rows = model.rows.filter((r) => ids === null || ids.has(r.customer_id));
  switch (model.filter) {
    case "past_due":
      rows = rows.filter((r) => isOpenRow(r) && (r.days_overdue ?? 0) > 0);
      break;
    case "current":
      rows = rows.filter((r) => isOpenRow(r) && r.days_overdue === 0);
      break;
    case "on_hold":
      rows = rows.filter((r) => isOpenRow(r) && r.account_status === "CREDIT_HOLD");
      break;
    case "all":
      rows = rows.filter(isOpenRow);
      break;
    // Legacy chips: keep every old behaviour so a stored URL still works.
    case "overdue":
      rows = rows.filter((r) => isOpenRow(r) && (r.days_overdue ?? 0) > 0);
      break;
    case "promised":
      rows = rows.filter((r) => isOpenRow(r) && typeof r.promised_date === "string" && r.promised_date !== "");
      break;
    case "disputed":
      rows = rows.filter((r) => isOpenRow(r) && r.disputed);
      break;
    case "paid":
      rows = rows.filter((r) => r.status === "PAID");
      break;
  }
  return rows.sort((a, b) => (b.days_overdue ?? 0) - (a.days_overdue ?? 0) || a.due_date.localeCompare(b.due_date));
}

// pastDueLine is the "X of Y past due · $Z" line the register header renders when the
// Past due chip is active, computed from the currently loaded rows; a paginated
// server reads the page from this same expression and the totals must agree. It answers
// "withheld" when the core sent no day counts (a caller the account position policy refuses),
// so the header says so in words rather than summing nothing.

export function shownInvoiceIds(model: DeskModel): string[] {
  return shownRows(model).map((r) => r.id);
}

function registerRowView(model: DeskModel, r: BillingRegisterRow): TemplateResult {
  const daysOverdue = r.days_overdue ?? 0;
  const overdue = daysOverdue > 0 && r.status !== "PAID";
  const acct = model.aging.find((a) => a.customer_id === r.customer_id);
  const terms = acct === undefined ? "" : ` · Net ${acct.payment_terms_days}`;
  // bucket_label is the C8 5-bucket label the server stamped for this row. When it is present
  // (i.e. the row is past due) the pill sits next to the overdue line; when it is absent (the
  // row is Current) we render nothing beside the due date so the column stays at one line.
  const bucketLabel = typeof r.bucket_label === "string" && r.bucket_label !== "" ? r.bucket_label : "";
  return html`
    <tr data-register-row>
      <td><span class="who">${r.invoice_no}</span><span class="dim small">${r.order_no ?? ""}</span></td>
      <td><span class="link-row" data-action="workspace-open" data-customer-id="${r.customer_id}"><span class="who">${r.customer_name}</span></span><span class="dim small">${r.account_no}${terms}</span></td>
      <td>${branchName(model, r.branch_id)}</td>
      <td class="nowrap due-col">
        <div class="due-line">${relativeDay(r.due_date, model.asOf)}</div>
        ${overdue
          ? html`<div class="late-line" data-testid="overdue-line">
              <span class="late-age">${r.days_overdue} ${r.days_overdue === 1 ? "day" : "days"} past due</span>
              ${bucketLabel !== "" ? html`<span class="bucket-pill" data-bucket-key="${r.bucket_key ?? ""}" data-testid="bucket-pill">${bucketWord(r.bucket_key, bucketLabel)}</span>` : nothing}
            </div>`
          : nothing}
      </td>
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
  // The C8 summary line reads "N of M past due · $X" when the Past due chip is active; "off" when
  // a different chip is active so the header does not contradict the table below it, and the
  // withheld words when the server sent no day counts for this caller.
  const summary = pastDueLine(model.filter, model.rows);
  return html`
    <section class="panel" aria-label="AR register">
      <div class="panel-head">
        <h3>AR register</h3>
        <div class="tools">
          ${segmented("Register filter", offeredChips(model.rows).map((id) => ({ id, word: CHIP_WORDS[id] })), model.filter, "filter")}
          <button type="button" class="btn btn-secondary small-btn" data-action="download-csv">Download CSV</button>
        </div>
      </div>
      ${summary.kind === "off"
        ? nothing
        : summary.kind === "withheld"
          ? html`<p class="dim small c8-summary" data-testid="c8-summary" data-past-due-withheld="true">Past due is not shown for your role.</p>`
          : html`<p class="dim small c8-summary" data-testid="c8-summary">
              ${summary.pastDueCount} of ${summary.totalCount} past due · ${formatMoney(summary.pastDueCents)}
            </p>`}
      ${rows.length === 0
        ? html`<div class="empty"><strong>No invoices in this view.</strong><span class="dim">Nothing matches the filter.</span></div>`
        : html`
            <div class="scroll">
              <table class="stack reg">
                <thead>
                  <tr><th>Invoice</th><th>Account</th><th>Branch</th><th>Due</th><th>Status</th><th class="num">Balance due</th><th><span class="visually-hidden">Act</span></th></tr>
                </thead>
                <tbody>${rows.map((r) => registerRowView(model, r))}</tbody>
              </table>
            </div>
          `}
      <p class="dim small foot" data-register-foot>
        ${model.selection === null ? "" : `${model.selection.count} ${model.selection.count === 1 ? "invoice" : "invoices"}, ${formatMoney(model.selection.cents)}. `}Past due first, then by due date. A promise or a dispute rides the row as a second mark, never a colour alone.
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
  // The history carries the application row and the reversal beside it (PR 668); the card
  // names each invoice once, from the application rows, in their order.
  const reopened = reopenedInvoiceNos(h.allocations).join(" and ");
  const bounce = model.bounced.find((b) => b.payment_id === h.payment_id);
  return html`
    <section class="panel work" aria-label="Payment bounced" data-bounced-work>
      <div class="panel-head"><h2>Payment bounced</h2><span class="pill pill-stop">Bounced</span></div>
      <dl class="facts">
        <dt>Account</dt><dd>${work.accountName}</dd>
        <dt>Payment</dt><dd>${h.payment_no} · ${h.method.toLowerCase()} · ${formatMoney(h.amount_cents)}</dd>
        <dt>What happened</dt><dd>${bounce?.reason ?? "Returned by the bank"}. Reversed across ${reopened === "" ? "no invoices" : reopened}${bounce === undefined ? "" : `; fee ${formatMoney(bounce.fee_cents)} charged`}.</dd>
        ${h.received_by_name === undefined || h.received_by_name === "" ? nothing : html`<dt>Received by</dt><dd>${h.received_by_name}, ${relativeDay(h.received_at, model.asOf)}</dd>`}
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
        <p class="dim">The AR register with its aging, past due first. Bounced payments and accounts on hold wait in Needs you.</p>
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
    ${renderRunPanel(model.office, model.asOf)}
    <section class="panel" aria-label="Statements by account">
      <div class="panel-head"><h3>Statements</h3></div>
      <div class="scroll">
        <table class="stack stmt">
          <thead><tr><th>Account</th><th class="num">Balance</th><th>Last statement</th><th><span class="visually-hidden">Open</span></th></tr></thead>
          <tbody>
            ${model.aging.map((a) => {
              const s = model.statements.get(a.customer_id);
              return html`
                <tr data-statement-row>
                  <td><span class="who">${a.customer_name}</span><span class="dim small">${a.account_no}</span></td>
                  <td class="num">${formatMoney(a.total_cents)}</td>
                  <td>${s === undefined ? "Loading" : s === "none" ? "None yet" : `As of ${relativeDay(s.as_of_date, model.asOf)}`}</td>
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

// ---- reminders tab -----------------------------------------------------------------------------

export function renderReminders(model: DeskModel): TemplateResult {
  return html`
    <div class="head">
      <div><h1>Reminders</h1><p class="dim">Chase an account's overdue invoice, see what has been sent, and work the accounts that ignore reminders. Everything leaves through the messaging sink and goes to the AP contact by its role.</p></div>
    </div>
    ${tabsBar(model)} ${messages(model)}
    ${renderRemindersPanels(model.office)}
  `;
}

// ---- settings tab ------------------------------------------------------------------------------

export function renderSettings(model: DeskModel): TemplateResult {
  return html`
    <div class="head">
      <div><h1>Settings</h1><p class="dim">The rules this desk runs on. Payables and Receivables reads them; the Financial Controller or the General Manager changes them, and every change is on the record.</p></div>
    </div>
    ${tabsBar(model)} ${messages(model)}
    ${renderSettingsPanels(model.office.settings, model.office.nowMs)}
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

/** The Place-a-hold panel (Change 8 S4). Manager-gated on the server: the form is the
 *  same one every other workspace writer uses, the gate refuses every other role with the
 *  server's own 403. The disabled placeholder the S2 scaffold left behind is gone, the
 *  reason textarea is the only input that is required, and the cancel/close button keeps
 *  the panel close to its previous shape.
 */
function placeHoldPanel(model: DeskModel): TemplateResult {
  const draft = model.placeHoldDraft;
  return html`
    <section class="panel side" aria-label="Place a hold" data-hold-panel>
      <div class="panel-head"><h2>Place a hold</h2></div>
      <p class="note" data-place-words>Placing a hold by hand stops the account at the till, the gate and the portal. The sweep places the Aged AR, Over limit and Pay-before-release holds, and a returned payment places its own.</p>
      <fieldset class="held-form">
        <label>Account<select data-field="place-hold-account">
          <option data-id="">Pick an account</option>
          ${model.registerSummary.map((r) => html`<option data-id="${r.customer_id}">${r.customer_name}</option>`)}
        </select></label>
        <label>Kind<input type="text" value="Manual" readonly /></label>
        <label>Reason<textarea data-field="place-hold-reason" rows="3" placeholder="Why the account is held" maxlength="1024"></textarea></label>
        <p class="label">What it blocks</p>
        <ul class="blocks"><li>New orders at the till</li><li>Release to dispatch</li><li>Portal checkout</li><li>Cash sales (still ring)</li></ul>
      </fieldset>
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="place-hold-go" ?disabled=${model.busy || draft.customerId === "" || draft.reason.trim() === ""}>Place hold</button>
        <button type="button" class="btn btn-secondary" data-action="hold-close">Close</button>
      </div>
      <p class="ok-note" data-place-audited><b>Audited.</b> The hold carries your role, the reason and the time.</p>
    </section>
  `;
}

/** The payment a returned payment hold names, once the till's list has said it. */
function placedBy(model: DeskModel, holdId: string): TemplateResult | typeof nothing {
  const paymentId = model.office.tillHolds.get(holdId);
  if (paymentId === undefined) return nothing;
  const bounced = model.bounced.find((b) => b.payment_id === paymentId);
  return html`<dt>Placed by</dt><dd data-hold-source>${bounced === undefined ? "A returned payment" : `Payment ${bounced.payment_no}, returned by the bank`}</dd>`;
}

function holdsPanel(model: DeskModel): TemplateResult {
  const hold = model.holds.find((h) => h.id === model.selectedHoldId);
  if (model.holdPanel === "place-later") {
    if (!model.canLift) {
      return html`
        <section class="panel side" aria-label="Place a hold" data-hold-panel>
          <div class="panel-head"><h2>Place a hold</h2><span class="later">later</span></div>
          <p class="note" data-later-words>Placing a hold by hand arrives later: there is no path for it yet. The sweep places the Aged AR, Over limit and Pay-before-release holds, and a returned payment places its own.</p>
          <fieldset class="held-form" disabled>
            <label>Account<select><option>Pick an account</option></select></label>
            <label>Kind<input type="text" value="Manual" readonly /></label>
            <label>Reason<textarea rows="2" placeholder="Why the account is held"></textarea></label>
            <p class="label">What it blocks</p>
            <ul class="blocks"><li>New orders at the till</li><li>Release to dispatch</li><li>Portal checkout</li><li>Cash sales (still ring)</li></ul>
          </fieldset>
          <div class="actions"><button type="button" class="btn btn-secondary held" aria-disabled="true">Place hold</button><button type="button" class="btn btn-secondary" data-action="hold-close">Close</button></div>
        </section>
      `;
    }
    return placeHoldPanel(model);
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
      ${placedBy(model, hold.id)}
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
      <label class="gap">Reason<textarea data-field="release-reason" rows="3" placeholder="Why this is right"></textarea></label>
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="lift-go" ?disabled=${model.busy}>${model.liftChoice === "lift" ? "Lift hold" : "Override hold"}</button>
        <button type="button" class="btn btn-secondary" data-action="hold-close">Cancel</button>
      </div>
      <p class="ok-note" data-audited><b>Audited.</b> The release carries your role, the reason and the time.</p>
    </section>
  `;
}

export function renderHolds(model: DeskModel): TemplateResult {
  const holds = model.holds.filter((h) => model.holdScope === "all" || h.status === "ACTIVE");
  return html`
    <div class="head">
      <div><h1>Credit holds</h1><p class="dim">A hold stops release at the till, the gate and the portal. Lifting and overriding both carry a reason, and both are audited.</p></div>
      ${model.canLift ? nothing : html`<div class="actions"><button type="button" class="btn btn-secondary held" data-action="place-later" aria-disabled="true">Place a hold <span class="later">later</span></button></div>`}
    </div>
    ${tabsBar(model)}
    <div class="cols">
      <section class="panel col-main" aria-label="Credit holds">
        ${messages(model)}
        <div class="panel-head">
          <h3>Credit holds</h3>
          <div class="tools">
            ${segmented("Show holds", [{ id: "active", word: "Active" }, { id: "all", word: "All" }], model.holdScope, "hold-scope")}
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
                          <td class="placed"><span class="nowrap">${relativeDay(h.created_at, model.asOf)}</span><span class="dim small">${h.auto_placed ? "by the credit sweep" : "by hand"}</span></td>
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

/**
 * The credit memo form (FW1-12): how much to credit and why, then one press that raises it.
 *
 * It is the Reverse form's shape on purpose: an inline row of fields and the press, with nothing else
 * to learn. The reason is asked for because the server asks for it: a credit with no stated reason
 * cannot be explained to the customer, to the Controller, or to whoever reads the file in a year.
 *
 * It says what the credit is AGAINST, in words, and what it DOES: the one invoice it names and the
 * cap that invoice has left, or the account and the balance it lowers. A credit against the account
 * and a credit against one invoice are different credits, and the person pressing the button must be
 * able to see which one they are about to give.
 *
 * It says what the approval rail will do too, before the press: a credit at or over the branch's
 * threshold waits for a second pair of eyes and moves no money until it is approved. The threshold is
 * the server's own number, read the way the route reads it, never a number typed into this file.
 */
function creditForm(model: DeskModel, account: AccountModel): TemplateResult {
  const form = model.credit;
  const invoice = form === null || form.invoiceId === null ? null : account.invoices.find((r) => r.id === form.invoiceId) ?? null;
  const quote = form?.quote ?? null;
  const target = creditTargetWords(invoice === null ? null : { invoice_no: invoice.invoice_no }, quote);
  const approval = creditApprovalWords(form?.thresholdCents ?? null, form === null ? null : parseAmountToCents(form.amountText));
  // The cap is the core's room: what the invoice has LEFT to credit, before tax. The balance with tax
  // is a different number, and it was the wrong one on a paid invoice.
  const cap = quote === null ? null : quote.remainingSubtotalCents;
  return html`
    <form class="credit-form" data-credit-form aria-label="Issue a credit">
      <p class="callout" data-credit-target>${target}</p>
      ${approval === "" ? nothing : html`<p class="note" data-credit-approval>${approval}</p>`}
      <label>
        <span data-credit-amount-label>Amount to credit${cap === null ? nothing : html` <span class="dim small">(up to ${formatMoney(cap)} before tax)</span>`}</span>
        <input type="text" inputmode="decimal" data-field="credit-amount" placeholder="Such as 100.00" autocomplete="off" />
      </label>
      ${quote === null || form?.quoteError ? nothing : html`<p class="note" data-credit-amount-words>${quote.message}</p>`}
      ${form?.quoteError ? html`<p class="note" data-credit-quote-error>${form.quoteError}</p>` : nothing}
      <label>
        <span data-credit-reason-label>Why the customer is credited (required)</span>
        <input type="text" data-field="credit-reason" placeholder="Such as the goods came back" autocomplete="off" />
      </label>
      <div class="actions">
        <button type="button" class="btn btn-danger" data-action="credit-confirm" ?disabled=${model.busy}>Issue the credit</button>
        <button type="button" class="btn btn-secondary" data-action="credit-cancel">Cancel</button>
      </div>
    </form>
  `;
}

/**
 * The credit form in a card of its own: the credit on the ACCOUNT, which belongs to no single row,
 * so it opens under the tabs in a panel rather than in the table.
 */
function creditPanel(model: DeskModel, account: AccountModel): TemplateResult {
  return html`<section class="panel" aria-label="Issue a credit">${creditForm(model, account)}</section>`;
}

function invoiceTable(model: DeskModel, account: AccountModel): TemplateResult {
  // Drafts and voids are not owed and not settled: they are never listed here, so never picked.
  const paid = account.invoices.filter((r) => r.stored_status === "PAID").sort((x, y) => y.due_date.localeCompare(x.due_date));
  const rows = account.invoices.filter((r) => (account.showAll ? isUnpaidInvoice(r) || r.stored_status === "PAID" : isUnpaidInvoice(r))).concat(account.showAll ? [] : paid.slice(0, 1));
  rows.sort((x, y) => Number(x.status === "PAID") - Number(y.status === "PAID") || (y.days_overdue ?? 0) - (x.days_overdue ?? 0) || x.due_date.localeCompare(y.due_date));
  const split = account.preview === null ? new Map<string, number>() : new Map(account.preview.lines.map((l) => [l.invoice_id, l.applied_cents]));
  const firstId = rows.find((r) => r.status !== "PAID")?.id ?? "";
  if (account.invoicesState !== "ready") {
    return html`
      <section class="panel" aria-label="Open invoices">
        <div class="panel-head"><h3>Open invoices</h3></div>
        ${account.invoicesState === "loading"
          ? html`<p class="dim" role="status" data-invoices-loading>Loading this account's invoices.</p>`
          : html`
              <p class="alert alert-error" role="alert" data-invoices-failed>We could not load this account's invoices. Try again.</p>
              <div class="actions"><button type="button" class="btn btn-secondary" data-action="retry-account-invoices">Try again</button></div>
            `}
      </section>
    `;
  }
  const balance = account.invoices.find((r) => r.account !== undefined)?.account?.open_balance_cents;
  return html`
    <section class="panel" aria-label="Open invoices">
      <div class="panel-head">
        <h3>Open invoices</h3>
        ${segmented("Which invoices", [{ id: "open", word: "Open" }, { id: "all", word: "All" }], account.showAll ? "all" : "open", "invoice-scope")}
      </div>
      <div class="scroll">
        <table class="stack inv">
          <thead>
            <tr><th>Invoice</th><th>Issued</th><th>Due</th><th>Status</th><th class="num">Amount</th><th class="num">Balance due</th><th class="num">Pick</th><th class="num">Applied</th></tr>
          </thead>
          <tbody>
            ${rows.map((r) => {
              const paidRow = r.status === "PAID";
              const picked = account.picked.includes(r.id);
              const applied = split.get(r.id);
              return html`
                <tr data-invoice-row class="${paidRow ? "dimrow" : ""}" aria-current="${account.selectedInvoiceId === r.id ? "true" : "false"}">
                  <td><button type="button" class="link-row" data-action="open-invoice" data-invoice-id="${r.id}"><span class="who">${r.invoice_no}</span></button><span class="dim small">${r.order_no ?? ""}</span><button type="button" class="link small" data-action="credit-start" data-invoice-id="${r.id}">Credit</button></td>
                  <td class="nowrap">${relativeDay(r.issue_date, model.asOf)}</td>
                  <td class="nowrap ${(r.days_overdue ?? 0) > 0 && !paidRow ? "late" : ""}" data-card-label="Due">${relativeDay(r.due_date, model.asOf)}</td>
                  <td><span class="marks">${rowMarks(r).map(markPill)}</span></td>
                  <td class="num">${formatMoney(r.total_cents)}</td>
                  <td class="num" data-card-label="Balance due"><b>${formatMoney(r.balance_cents)}</b></td>
                  <td class="num">${paidRow ? nothing : html`<button type="button" class="pick" role="checkbox" aria-checked="${picked ? "true" : "false"}" aria-label="Pay ${r.invoice_no}" data-action="pick" data-invoice-id="${r.id}"></button>`}</td>
                  <td class="num applied-cell" data-applied-cell data-card-label="Applied">${paidRow || applied === undefined ? "" : formatMoney(applied)}</td>
                </tr>
                ${model.credit !== null && model.credit.invoiceId === r.id
                  ? html`<tr class="credit-form-row"><td colspan="8">${creditForm(model, account)}</td></tr>`
                  : nothing}
              `;
            })}
          </tbody>
          <tfoot>
            <tr><td colspan="5"><b>Balance</b> <span class="dim small">as of ${relativeDay(model.asOf, model.asOf)}</span></td><td class="num"><b data-open-balance>${balance === undefined ? html`<span class="dim small">Not shown for this account</span>` : formatMoney(balance)}</b></td><td></td><td></td></tr>
          </tfoot>
        </table>
      </div>
      <div class="foot-row">
        <p class="dim small">Pick the invoices the payment is for and type the amount: the payment is split across the picked invoices, oldest first. A paid invoice stays for context and takes nothing.</p>
        ${firstId === "" ? nothing : html`<button type="button" class="link" data-action="open-invoice" data-invoice-id="${account.selectedInvoiceId ?? firstId}">Invoice history ›</button>`}
      </div>
    </section>
  `;
}

function receivePanel(model: DeskModel, account: AccountModel): TemplateResult {
  const answer = account.preview;
  const onHold = account.invoices.some((r) => r.account_status === "CREDIT_HOLD") || model.holds.some((h) => h.status === "ACTIVE" && h.customer_id === account.customerId);
  const stays = answer?.lines.filter((l) => l.outcome === "PART") ?? [];
  // Walk change 8 S6: the policy from the office reads the take-a-payment panel's tender order.
  // Missing row answer 200 with house default (ach_first true, surcharge 240 bps, min 0), so the
  // panel never special-cases "no policy" and the order reads from one source.
  const pol = model.office.paymentPolicy;
  const tenderIds: PayMethod[] = pol !== null && pol.ach_first ? ["EFT", "CARD", "CASH", "CHEQUE"] : ["CARD", "EFT", "CASH", "CHEQUE"];
  const tenderWords: Record<PayMethod, string> = { EFT: "EFT", CARD: "Card", CASH: "Cash", CHEQUE: "Cheque" };
  const surchargeCents = computeSurchargeCents(pol, answer?.amount_cents ?? null);
  const forcedEft = pol !== null && pol.min_ach_cents > 0 && (answer?.amount_cents ?? 0) > 0 && pol.min_ach_cents >= (answer?.amount_cents ?? 0);
  return html`
    <section class="panel side" aria-label="Take a payment" data-receive>
      <div class="panel-head">
        <h2>Take a payment</h2>
        ${answer === null ? nothing : html`<span class="pill ${answer.unapplied_cents === 0 ? "pill-good" : "pill-watch"}" data-unapplied>unapplied ${formatMoney(answer.unapplied_cents)}</span>`}
      </div>
      <div class="two">
        <label>Amount<input type="text" inputmode="decimal" class="amount" data-field="receive-amount" autocomplete="off" placeholder="$0.00" />${account.amountText.trim() !== "" && parseAmountToCents(account.amountText) === null ? html`<span class="hint" role="alert" data-amount-hint>Enter dollars and cents, such as $100.00.</span>` : nothing}</label>
        <label>Received at, from the account<span class="ro" data-ro="received-at"></span></label>
      </div>
      <p class="label gap">Payment method</p>
      <p class="dim small" data-policy-ach-first>${pol !== null && pol.ach_first ? "EFT is the usual choice" : "Card is the usual choice"}</p>
      ${segmented("Payment method", tenderIds.map((id) => ({ id, word: tenderWords[id] })), forcedEft ? "EFT" : account.method, "method")}
      ${pol !== null && pol.surcharge_rate_bps > 0 && account.method === "CARD" && surchargeCents > 0
        ? html`<p class="alert alert-warn" role="status" data-card-surcharge>Card surcharge: ${formatMoney(surchargeCents)} at ${(pol.surcharge_rate_bps / 100).toFixed(2)}% (${pol.surcharge_rate_bps} bps).</p>`
        : nothing}
      ${forcedEft
        ? html`<p class="alert alert-warn" role="status" data-ach-min>Below the policy minimum of ${formatMoney(pol!.min_ach_cents)}; EFT is the only tender.</p>`
        : nothing}
      <div class="two gap">
        <label>Reference<input type="text" data-field="receive-reference" autocomplete="off" placeholder="Cheque number or transfer reference" /></label>
        <label>Taken by<span class="ro" data-ro="taken-by"></span></label>
      </div>
      <h3 class="gap">Allocation</h3>
      <div class="actions"><button type="button" class="btn btn-secondary small-btn" data-action="pick-all">Pick all open</button><button type="button" class="btn btn-secondary small-btn" data-action="clear-apply">Clear</button></div>
      ${account.previewError === "" ? nothing : html`<p class="alert alert-error" role="alert" data-preview-error>${account.previewError}</p>`}
      <ul class="alloc" data-allocation>
        ${answer === null
          ? html`<li class="dim"><span>Type an amount and pick the invoices; the split shows here.</span></li>`
          : html`
              ${answer.lines.filter((l) => l.outcome !== "NONE").map((l) => html`<li><span>${l.invoice_no} · ${lineWords(l)}</span><span>${formatMoney(l.applied_cents)}</span></li>`)}
              <li class="dim"><span>Unapplied</span><span>${formatMoney(answer.unapplied_cents)}</span></li>
            `}
      </ul>
      ${answer !== null && answer.on_account_cents > 0 ? html`<p class="callout" data-on-account>${onAccountWords(answer.on_account_cents)}</p>` : nothing}
      <p class="applied"><b>Applied</b><b data-applied>${answer === null ? "Nothing to apply yet" : `${formatMoney(answer.applied_cents)} of ${formatMoney(answer.amount_cents)}`}</b></p>
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="apply-payment" ?disabled=${model.busy}>Record payment</button>
        <button type="button" class="btn btn-secondary" data-action="clear-apply">Cancel</button>
      </div>
      ${onHold
        ? html`<p class="callout" data-hold-stays><b>The hold stays.</b> Lifting it is the controller's.${stays.length > 0 ? ` The ${stays.map((l) => `${formatMoney(l.stays_open_cents)} left on ${l.invoice_no}`).join(" and the ")} keeps the account where it is.` : ""}</p>`
        : nothing}
      <p class="dim small">One payment is one record: the payment is split across the picked invoices in one step, oldest first. What shows afterwards is the customer's balance.</p>
    </section>
  `;
}

// computeSurchargeCents answers the cards's surcharge line. The single source of truth is the
// server's stored rate (surcharge_rate_bps); the maths is cents_x_bps / 10000 with a ceil so the
// dealer never absorbs a partial cent.
function computeSurchargeCents(pol: BillingAccountPaymentPolicy | null, amountCents: number | null): number {
  if (pol === null || amountCents === null || amountCents <= 0) return 0;
  if (pol.surcharge_rate_bps <= 0) return 0;
  return Math.ceil((amountCents * pol.surcharge_rate_bps) / 10000);
}

function paymentList(model: DeskModel, account: AccountModel): TemplateResult {
  const payments = account.payments?.items ?? [];
  if (payments.length === 0) return html`<p class="dim">No payment has been applied to this invoice yet.</p>`;
  return html`
    <ul class="alloc">
      ${payments.map(
        (p) => html`
          <li data-payment-row>
            <span>${p.payment_no} · ${p.method.toLowerCase()} · ${formatMoney(p.amount_cents)}
              ${p.status === "BOUNCED" ? html`<span class="pill pill-stop">Bounced</span>` : p.status === "VOID" ? html`<span class="pill pill-plain">Reversal</span>` : p.reversed_at !== undefined ? html`<span class="pill pill-plain">Reversed</span>` : html`<span class="pill pill-good">${p.status === "DEPOSITED" ? "Deposited" : "Received"}</span>`}</span>
            ${p.reversed_at !== undefined && p.status !== "VOID" && p.status !== "BOUNCED"
              ? html`<span class="row-actions">${renderReapply(model.office, p, account.invoices.filter(isUnpaidInvoice), account.detail?.id ?? "")}</span>`
              : p.status === "BOUNCED" || p.status === "VOID" || p.reversed_at !== undefined
              ? nothing
              : account.bounceFor === p.payment_id
                ? html`<span class="inline-form"><label><span class="visually-hidden">Reason it bounced</span><input type="text" data-field="bounce-reason" placeholder="Why it came back, such as cheque returned NSF" autocomplete="off" /></label><button type="button" class="btn btn-danger small-btn" data-action="bounce-confirm" data-payment-id="${p.payment_id}">Confirm bounced</button><button type="button" class="btn btn-secondary small-btn" data-action="bounce-cancel">Cancel</button></span>`
                : html`<span class="row-actions"><button type="button" class="btn btn-secondary small-btn" data-action="bounce-start" data-payment-id="${p.payment_id}">Mark bounced</button>${renderReverse(model.office, p)}${p.status === "RECEIVED" && canVoidReceipt(model.roleCode) ? renderVoid(model.office, p) : nothing}</span>`}
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
        <dt>Due</dt><dd>${d.due_date}${(d.days_overdue ?? 0) > 0 ? ` (${d.days_overdue} days over)` : ""}</dd>
        <dt>Total</dt><dd>${formatMoney(d.totals.total_cents)}</dd>
        <dt>Applied</dt><dd>${formatMoney(d.totals.applied_cents)}</dd>
        <dt>Balance</dt><dd data-invoice-balance>${formatMoney(d.totals.balance_cents)}</dd>
        ${d.promise !== undefined ? html`<dt>Promise to pay</dt><dd data-promise>${formatMoney(d.promise.promised_cents)} by ${d.promise.promised_date}${d.promise.note ? `: ${d.promise.note}` : ""}</dd>` : nothing}
        ${d.dispute !== undefined ? html`<dt>Dispute</dt><dd data-dispute>${d.dispute.status === "OPEN" ? "Open" : "Resolved"}: ${formatMoney(d.dispute.amount_cents)}${d.dispute.note ? `, ${d.dispute.note}` : ""}</dd>` : nothing}
      </dl>
      ${renderInvoiceActions(model.office, d)}
      <h3>Payments on this invoice</h3>
      ${paymentList(model, account)}
      ${renderInvoiceCredits(model.office, d, account.invoices.filter(isUnpaidInvoice))}
    </section>
  `;
}

function promisePanel(model: DeskModel, account: AccountModel): TemplateResult {
  const open = account.invoices.filter(isUnpaidInvoice);
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
              <div class="doc-head"><h2>${account.branchName === "" ? "Statement" : account.branchName}</h2><span><b>STATEMENT</b> <span class="dim">As of ${relativeDay(s.as_of_date, model.asOf)}</span></span></div>
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
              ${s.closing_balance_cents == null || s.current_cents == null || s.days_30_cents == null || s.days_60_cents == null || s.days_90_plus_cents == null
                ? html`<p class="dim" data-account-position-withheld="true">Account position: Not shown for your role</p>`
                : html`
              <dl class="strip">
                <div><dt>${STATEMENT_STRIP_LABELS[0]}</dt><dd>${formatMoney(s.current_cents)}</dd></div>
                <div><dt>${STATEMENT_STRIP_LABELS[1]}</dt><dd>${formatMoney(s.days_30_cents)}</dd></div>
                <div><dt>${STATEMENT_STRIP_LABELS[2]}</dt><dd>${formatMoney(s.days_60_cents)}</dd></div>
                <div><dt>${STATEMENT_STRIP_LABELS[3]}</dt><dd>${formatMoney(s.days_90_plus_cents)}</dd></div>
                <div><dt>${STATEMENT_STRIP_LABELS[4]}</dt><dd data-closing><span class="visually-hidden">Closing balance </span>${formatMoney(s.closing_balance_cents)}</dd></div>
              </dl>
              ${s.days_90_plus_cents > 0 ? html`<p class="overdue-note"><b>${formatMoney(s.days_90_plus_cents)}</b> ${STATEMENT_OVERDUE_NOTE}</p>` : nothing}
              `}
              <p class="dim small">Statement generated ${relativeDay(s.created_at, model.asOf)}. Please contact the branch with any discrepancy.</p>
            `}
    </section>
  `;
}

export function renderAccount(model: DeskModel, account: AccountModel): TemplateResult {
  const hold = model.holds.find((h) => h.status === "ACTIVE" && h.customer_id === account.customerId);
  const promised = account.invoices.some((r) => typeof r.promised_date === "string" && r.promised_date !== "");
  const disputes = account.invoices.filter((r) => r.disputed).length;
  const overdueOnAccount = account.invoices.some((r) => (r.days_overdue ?? 0) > 0 && isUnpaidInvoice(r));
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
        <button type="button" class="btn btn-secondary" data-action="remind-account" data-customer-id="${account.customerId}" ?disabled=${!overdueOnAccount}>Remind</button>
        ${overdueOnAccount || account.invoicesState !== "ready" ? nothing : html`<span id="remind-why" class="dim small">Nothing on this account is overdue.</span>`}
        <button type="button" class="btn btn-secondary" data-action="promise-panel">Record a promise</button>
        <button type="button" class="btn btn-secondary" data-action="credit-start">Issue a credit</button>
        ${hold === undefined ? nothing : html`<button type="button" class="btn btn-secondary" data-action="ask-lift">Ask to lift the hold</button>`}
      </div>
    </div>
    <nav class="tabs" aria-label="Account views">
      <button type="button" class="tab" data-action="sub" data-choice="invoices" aria-current="${account.sub === "invoices" ? "true" : "false"}">Open invoices</button>
      <button type="button" class="tab" data-action="sub" data-choice="statement" aria-current="${account.sub === "statement" ? "true" : "false"}">Statement</button>
      <span class="tab tab-later" aria-disabled="true">Activity ${later()}</span>
      <button type="button" class="tab" data-action="sub" data-choice="contacts" aria-current="${account.sub === "contacts" ? "true" : "false"}">Contacts</button>
    </nav>
    ${model.credit !== null && model.credit.invoiceId === null ? creditPanel(model, account) : nothing}
    ${messages(model)}
    ${account.sub === "statement"
      ? statementDoc(model, account)
      : account.sub === "contacts"
        ? renderContacts(model.office, account)
        : html`
          <div class="cols">
            <div class="col-main">${invoiceTable(model, account)}</div>
            <div class="col-side">${account.right === "invoice" ? invoicePanel(model, account) : account.right === "promise" ? promisePanel(model, account) : receivePanel(model, account)}</div>
          </div>
        `}
  `;
}

// renderWorkspaceScreen is the second screen mode the desk offers: the customer
// account workspace (Change 8 S2 desktop + S2M mobile). The back link clears the
// workspace state and returns the operator to the page they came from; the screen
// itself is delegated to ar-workspace.ts.
//
// Returns the loading tile while the read is in flight; the route is server-only
// and we never fake the payload client-side, so a slow workspace read renders an
// honest loading indicator and not a stale copy of a previous customer's data.
export function renderWorkspaceScreen(model: DeskModel): TemplateResult {
  if (model.workspace === null || model.workspaceCustomerId === null) {
    return html`
      <button type="button" class="link back" data-action="workspace-back">‹ Receivables</button>
      <div class="head">
        <h1>Account workspace</h1>
        <p class="dim">Loading the workspace…</p>
      </div>
    `;
  }
  return html`
    <button type="button" class="link back" data-action="workspace-back">‹ Receivables</button>
    <div class="head">
      <h1>Account workspace</h1>
      <p class="dim">${model.workspace.header.name} · ${model.workspace.header.account_no}</p>
    </div>
    ${messages(model)}
    ${renderWorkspace(
      model.workspace,
      model.activeWorkspaceTab,
      () => {}, // tab change is handled by the desk press
      () => {}, // apply credit is handled by the desk press
      () => {}, // add note is handled by the desk press
      () => {}, // quick action is handled by the desk press
      false,
    )}
  `;
}
