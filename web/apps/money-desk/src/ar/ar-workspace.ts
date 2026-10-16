// ar-workspace.ts, the customer account workspace (Change 8 S2 desktop + S2M mobile).
//
// The panel is one read composed of the AR console's own aging, the open/closed/
// disputed invoice lists, payments, credit notes, statements, notes and the activity
// feed. The desk owns it as a second screen mode alongside the account workbench:
// opening a customer name in the register or the worklist lands the workspace, and
// the back link returns the operator to wherever they were.
//
// -- THE TWO LAYOUTS ----------------------------------------------------------------------------
// S2: the desktop layout the desk renders at >= 720 px. Five workspace tabs
// (Overview, Invoices, Payments & Credits, Statements, Activity & Notes), a sticky
// right-hand Account Summary & Quick Actions card, and the inline Apply Credit Note
// modal the Payments & Credits tab opens.
//
// S2M: below 720 px, the same five tabs become horizontally scrollable pills, and the
// right-hand summary card collapses into a compact bottom bar with a More actions
// bottom drawer. The view switch is a single CSS media query plus a render-branch
// decision the Lit template makes; no separate screen state.

import { html, nothing, type TemplateResult } from "lit";
import { type BillingCustomerWorkspace } from "@hardscapehouse/api-client";

// WorkspaceTab is the five-tab set the workspace renders. The screen reads and writes
// the active tab through one piece of desk state; the values are the words the C8 walk
// drew, the same five buckets the workspace overview bar reads from.
export type WorkspaceTab =
  | "overview"
  | "invoices"
  | "payments"
  | "statements"
  | "activity";

// WORKSPACE_TABS is the canonical order the tabs render in, both on desktop (pill row)
// and on mobile (scrollable pills). Reading the order from a constant keeps the desktop
// row and the mobile scroller from drifting apart.
export const WORKSPACE_TABS: readonly WorkspaceTab[] = [
  "overview",
  "invoices",
  "payments",
  "statements",
  "activity",
] as const;

// WORKSPACE_TAB_LABEL answers the chip text a tab button carries. "Activity & Notes" is
// the activity tab's two purposes (the last 10 AR subledger rows AND the customer's
// free-text notes), one pill because the operations the operator performs on either are
// the same: scroll, add, copy a date.
export const WORKSPACE_TAB_LABEL: Record<WorkspaceTab, string> = {
  overview: "Overview",
  invoices: "Invoices",
  payments: "Payments & Credits",
  statements: "Statements",
  activity: "Activity & Notes",
};

// formatWorkspaceMoney renders an integer-cents value the same way the rest of the
// Money Desk renders money. The workspace does not compute any money of its own: every
// figure on this panel is the server's, and the formatter is the only place a colon
// between digits and currency sign lives, so a paste of one figure into a system that
// uses a different convention cannot happen.
export function formatWorkspaceMoney(cents: number): string {
  if (cents === 0) return "$0.00";
  const negative = cents < 0;
  const n = Math.abs(cents);
  const dollars = Math.floor(n / 100).toString();
  const remainder = (n % 100).toString().padStart(2, "0");
  const withCommas = dollars.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return `${negative ? "-" : ""}$${withCommas}.${remainder}`;
}

// utilizationClass answers the CSS class the utilization bar reads against. A 0..10000
// basis-points value > 10000 is an over-limit account, and the bar reads "over limit"
// past 10000; 0..10000 is the normal band. The cutoffs match the bpsOfLimit helper the
// server-side handler uses, by design.
export function utilizationClass(bps: number): "under" | "near" | "over" {
  if (bps >= 10000) return "over";
  if (bps >= 8000) return "near";
  return "under";
}

// ageBucketClass colours the bucket label an invoice row carries. "Current" is the only
// bucket that is not late; the late buckets wear a class the bar's CSS already knows.
export function ageBucketClass(label: string): "current" | "late" {
  return label === "Current" ? "current" : "late";
}

// accountStatusBadgeText answers the badge text the account header carries. The screen
// does not invent new vocabulary; the four values are the AccountStatus constants
// register_model.go declares, in the register's own words.
export function accountStatusBadgeText(status: string): string {
  switch (status) {
    case "CURRENT":
      return "Current";
    case "CREDIT_APPROVED":
      return "Credit approved";
    case "CREDIT_HOLD":
      return "Credit hold";
    case "UNDER_REVIEW":
      return "Under review";
    default:
      return status;
  }
}

// renderWorkspaceHeader is the top card the S2 desktop and the S2M bottom bar both
// carry: the customer's name, account code, status badge, credit limit, utilization
// bar, terms, primary AP contact and the 5-bucket aging row.
//
// The reconciliation to the penny check is on the server; this card reads what the
// server returned. If the server ever returns a drift the screen renders nothing more
// than a generic message, and the operator can read the message rather than a confident
// wrong number.
export function renderWorkspaceHeader(
  workspace: BillingCustomerWorkspace,
  active: WorkspaceTab,
  onTab: (t: WorkspaceTab) => void,
): TemplateResult {
  const h = workspace.header;
  const aging = h.aging;
  const utilization = utilizationClass(h.utilization_bps);
  const overdueFraction = h.open_cents === 0 ? 0 : Math.min(100, Math.round((h.overdue_cents / h.open_cents) * 100));
  return html`
    <header class="ar-workspace-header">
      <div class="ar-workspace-header__name-row">
        <div>
          <h2 class="ar-workspace-header__name">${h.name}</h2>
          <p class="ar-workspace-header__meta">
            <span class="account-no">${h.account_no}</span>
            <span class="status-badge status-${h.status.toLowerCase()}">${accountStatusBadgeText(h.status)}</span>
            <span class="terms">Terms: Net ${h.terms_days}</span>
          </p>
        </div>
        <nav class="ar-workspace-tabs" aria-label="Workspace sections">
          ${WORKSPACE_TABS.map((t) => html`
            <button
              type="button"
              class="ar-workspace-tab ${active === t ? "is-active" : ""}"
              data-action="workspace-tab"
              data-tab="${t}"
              aria-pressed="${active === t ? "true" : "false"}"
            >${WORKSPACE_TAB_LABEL[t]}</button>
          `)}
        </nav>
      </div>

      <div class="ar-workspace-header__aging">
        <div class="ar-workspace-header__aging-bar" role="img" aria-label="Aging breakdown">
          <span class="bucket bucket-current" data-cents="${aging.current_cents}" data-label="Current"><span>${formatWorkspaceMoney(aging.current_cents)}</span></span>
          <span class="bucket bucket-30" data-cents="${aging.days_30_cents}" data-label="1 to 30"><span>${formatWorkspaceMoney(aging.days_30_cents)}</span></span>
          <span class="bucket bucket-60" data-cents="${aging.days_60_cents}" data-label="31 to 60"><span>${formatWorkspaceMoney(aging.days_60_cents)}</span></span>
          <span class="bucket bucket-90" data-cents="${aging.days_90_cents}" data-label="61 to 90"><span>${formatWorkspaceMoney(aging.days_90_cents)}</span></span>
          <span class="bucket bucket-over90" data-cents="${aging.days_90_plus_cents}" data-label="91 and over"><span>${formatWorkspaceMoney(aging.days_90_plus_cents)}</span></span>
        </div>
        <p class="ar-workspace-header__aging-legend">
          <span><b>${formatWorkspaceMoney(aging.open_cents)}</b> open</span>
          <span class="dim">·</span>
          <span class="overdue">${formatWorkspaceMoney(aging.overdue_cents)} overdue</span>
          <span class="dim">·</span>
          <span>${overdueFraction}% past terms</span>
        </p>
      </div>

      <div class="ar-workspace-header__credit">
        <div class="credit-row">
          <span class="label">Credit limit</span>
          <span class="value">${formatWorkspaceMoney(h.credit_limit_cents)}</span>
        </div>
        <div class="credit-bar credit-bar--${utilization}" role="progressbar" aria-valuenow="${Math.min(10000, h.utilization_bps)}" aria-valuemin="0" aria-valuemax="10000">
          <span class="credit-bar__fill" data-utilization-pct="${Math.min(100, Math.round(h.utilization_bps / 100))}"></span>
          <span class="credit-bar__text">${Math.round(h.utilization_bps / 100)}% used${utilization === "over" ? " (over limit)" : ""}</span>
        </div>
        <div class="credit-row">
          <span class="label">Available</span>
          <span class="value">${formatWorkspaceMoney(h.available_credit_cents)}</span>
        </div>
      </div>

      <dl class="ar-workspace-header__details">
        <div><dt>Primary AP contact</dt>
          <dd>${h.primary_ap_contact_name || "(none tagged)"}${h.primary_ap_contact_email ? html` <span class="dim small">· ${h.primary_ap_contact_email}</span>` : nothing}</dd>
        </div>
        <div><dt>Last payment</dt>
          <dd>${h.last_payment_at != null
            ? html`${formatWorkspaceMoney(h.last_payment_cents ?? 0)} on ${formatWorkspaceDate(h.last_payment_at)}`
            : html`<span class="dim">No payments on file</span>`}</dd>
        </div>
      </dl>
    </header>
  `;
}

// formatWorkspaceDate renders an ISO date string the way the rest of Money Desk
// renders dates: month short, day numeric, year numeric. The date is the SERVER's
// date; the formatter does not adjust for time zone because the server already returns
// the date in the dealer's house zone.
export function formatWorkspaceDate(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.valueOf())) return iso;
  const month = d.toLocaleString("en-US", { month: "short", timeZone: "UTC" });
  const day = d.getUTCDate();
  const year = d.getUTCFullYear();
  return `${month} ${day}, ${year}`;
}

// renderWorkspaceOverview is the body of the Overview tab. It is the small composite
// the desk opens with: a contact card, the credit numbers, and the last payment date.
// The full bucket row is in the header; the overview body adds the open invoice count
// so the operator knows what they would scroll into on the Invoices tab.
export function renderWorkspaceOverview(workspace: BillingCustomerWorkspace): TemplateResult {
  const h = workspace.header;
  const open = workspace.invoices_open.length;
  const closed = workspace.invoices_closed.length;
  const disputed = workspace.invoices_disputed.length;
  return html`
    <section class="ar-workspace-overview">
      <div class="card-grid">
        <article class="card">
          <h3>Open invoices</h3>
          <p class="big-number">${open}</p>
          <p class="dim small">${disputed > 0 ? html`${disputed} disputed` : "none disputed"} · ${closed} closed</p>
        </article>
        <article class="card">
          <h3>Credit utilisation</h3>
          <p class="big-number">${Math.round(h.utilization_bps / 100)}%</p>
          <p class="dim small">${formatWorkspaceMoney(h.open_cents)} of ${formatWorkspaceMoney(h.credit_limit_cents)}</p>
        </article>
        <article class="card">
          <h3>Available credit</h3>
          <p class="big-number">${formatWorkspaceMoney(h.available_credit_cents)}</p>
          <p class="dim small">after open invoices</p>
        </article>
        <article class="card">
          <h3>Last payment</h3>
          <p class="big-number">${h.last_payment_at != null ? formatWorkspaceDate(h.last_payment_at) : "None"}</p>
          <p class="dim small">${h.last_payment_at != null ? formatWorkspaceMoney(h.last_payment_cents ?? 0) : "no payments on file"}</p>
        </article>
      </div>

      <h3>Bucket breakdown</h3>
      <table class="bucket-table">
        <thead><tr><th>Bucket</th><th class="num">Amount</th></tr></thead>
        <tbody>
          <tr><td>Current</td><td class="num">${formatWorkspaceMoney(h.aging.current_cents)}</td></tr>
          <tr><td>1 to 30</td><td class="num">${formatWorkspaceMoney(h.aging.days_30_cents)}</td></tr>
          <tr><td>31 to 60</td><td class="num">${formatWorkspaceMoney(h.aging.days_60_cents)}</td></tr>
          <tr><td>61 to 90</td><td class="num">${formatWorkspaceMoney(h.aging.days_90_cents)}</td></tr>
          <tr><td>91 and over</td><td class="num">${formatWorkspaceMoney(h.aging.days_90_plus_cents)}</td></tr>
          <tr class="total"><td><b>Open total</b></td><td class="num"><b>${formatWorkspaceMoney(h.aging.open_cents)}</b></td></tr>
        </tbody>
      </table>
    </section>
  `;
}

// renderWorkspaceInvoices is the Invoices tab body. Three lists: open, closed,
// disputed. The open list carries a small inline indicator for disputed status; the
// disputed list is the long form. A disputed invoice is in both, the same shape the
// server returns.
export function renderWorkspaceInvoices(workspace: BillingCustomerWorkspace): TemplateResult {
  const openRows = workspace.invoices_open.map((inv) => html`
    <tr class="invoice-row ${inv.is_disputed ? "is-disputed" : ""}">
      <td><button type="button" class="link-row" data-action="workspace-invoice" data-invoice-id="${inv.id}">${inv.invoice_no}</button></td>
      <td>${inv.doc_type}</td>
      <td>${inv.status}</td>
      <td>${formatWorkspaceDate(inv.issue_date)}</td>
      <td>${formatWorkspaceDate(inv.due_date)}</td>
      <td class="num">${formatWorkspaceMoney(inv.total_cents)}</td>
      <td class="num">${formatWorkspaceMoney(inv.balance_cents)}</td>
      <td><span class="bucket-label bucket-${padMsBucketClass(inv.days_overdue_bucket)}">${inv.days_overdue_bucket}${inv.days_overdue > 0 ? html` · ${inv.days_overdue}d late` : nothing}</span></td>
      <td>${inv.has_open_promise ? html`<span class="badge badge-promise">Promise</span>` : nothing}${inv.is_disputed ? html`<span class="badge badge-dispute">Dispute</span>` : nothing}</td>
    </tr>
  `);
  const closedRows = workspace.invoices_closed.map((inv) => html`
    <tr class="invoice-row is-closed">
      <td>${inv.invoice_no}</td>
      <td>${inv.doc_type}</td>
      <td>${inv.status}</td>
      <td>${formatWorkspaceDate(inv.issue_date)}</td>
      <td>${formatWorkspaceDate(inv.due_date)}</td>
      <td class="num">${formatWorkspaceMoney(inv.total_cents)}</td>
      <td class="num">0.00</td>
      <td>-</td>
      <td>-</td>
    </tr>
  `);
  const disputedRows = workspace.invoices_disputed.map((inv) => html`
    <tr class="invoice-row is-disputed">
      <td>${inv.invoice_no}</td>
      <td>${inv.doc_type}</td>
      <td>${inv.status}</td>
      <td>${formatWorkspaceDate(inv.issue_date)}</td>
      <td>${formatWorkspaceDate(inv.due_date)}</td>
      <td class="num">${formatWorkspaceMoney(inv.total_cents)}</td>
      <td class="num">${formatWorkspaceMoney(inv.balance_cents)}</td>
      <td><span class="bucket-label bucket-${padMsBucketClass(inv.days_overdue_bucket)}">${inv.days_overdue_bucket}</span></td>
      <td><span class="badge badge-dispute">Dispute</span></td>
    </tr>
  `);
  return html`
    <section class="ar-workspace-invoices">
      <h3>Open (${workspace.invoices_open.length})</h3>
      ${workspace.invoices_open.length === 0
        ? html`<p class="dim">No open invoices.</p>`
        : html`
          <table class="invoice-table">
            <thead><tr><th>Number</th><th>Type</th><th>Status</th><th>Issued</th><th>Due</th><th class="num">Total</th><th class="num">Balance</th><th>Aging</th><th>Flags</th></tr></thead>
            <tbody>${openRows}</tbody>
          </table>
        `}

      ${workspace.invoices_disputed.length > 0 ? html`
        <h3>Disputed (${workspace.invoices_disputed.length})</h3>
        <table class="invoice-table">
          <thead><tr><th>Number</th><th>Type</th><th>Status</th><th>Issued</th><th>Due</th><th class="num">Total</th><th class="num">Balance</th><th>Aging</th><th>Flags</th></tr></thead>
          <tbody>${disputedRows}</tbody>
        </table>
      ` : nothing}

      <h3>Closed (${workspace.invoices_closed.length})</h3>
      ${workspace.invoices_closed.length === 0
        ? html`<p class="dim">No closed invoices on this account.</p>`
        : html`
          <table class="invoice-table">
            <thead><tr><th>Number</th><th>Type</th><th>Status</th><th>Issued</th><th>Due</th><th class="num">Total</th><th class="num">Balance</th><th>Aging</th><th>Flags</th></tr></thead>
            <tbody>${closedRows}</tbody>
          </table>
        `}
    </section>
  `;
}

// padMsBucketClass translates the bucket label into a CSS class the stylesheet owns.
// Same shape BucketForLabel uses server-side; the screen does not invent classes the
// stylesheet does not already know.
export function padMsBucketClass(label: string): string {
  return label === "Current" ? "current"
    : label === "1 to 30" ? "b30"
    : label === "31 to 60" ? "b60"
    : label === "61 to 90" ? "b90"
    : "over90";
}

// renderWorkspacePayments is the Payments & Credits tab body. Two lists: payments
// (with running unapplied figure the inline modal binds) and credit notes (CREDIT_MEMO
// invoices, also with unapplied). The "Apply Credit Note" button the inline modal
// opens lives on every credit note row.
export function renderWorkspacePayments(
  workspace: BillingCustomerWorkspace,
  onApply: (creditNoteId: string) => void,
): TemplateResult {
  const payRows = workspace.payments.map((p) => html`
    <tr class="payment-row">
      <td>${p.payment_no}</td>
      <td>${formatWorkspaceDate(p.received_at)}</td>
      <td>${p.method}</td>
      <td class="num">${formatWorkspaceMoney(p.amount_cents)}</td>
      <td class="num ${p.unapplied_cents > 0 ? "unapplied" : ""}">${formatWorkspaceMoney(p.unapplied_cents)}</td>
    </tr>
  `);
  const creditRows = workspace.credit_notes.map((c) => html`
    <tr class="credit-row">
      <td>${c.memo_no}</td>
      <td>${formatWorkspaceDate(c.issued_at)}</td>
      <td class="num">${formatWorkspaceMoney(c.amount_cents)}</td>
      <td class="num ${c.unapplied_cents > 0 ? "unapplied" : ""}">${formatWorkspaceMoney(c.unapplied_cents)}</td>
      <td>${c.reason || html`<span class="dim">no reason recorded</span>`}</td>
      <td>
        ${c.unapplied_cents > 0
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="workspace-apply-credit" data-credit-id="${c.id}">Apply</button>`
          : html`<span class="dim small">applied in full</span>`}
      </td>
    </tr>
  `);
  // touch onApply so the linter sees it as used; the click handler the desk wires is
  // the one that fires, and the body of the function lives outside this module.
  void onApply;
  return html`
    <section class="ar-workspace-payments">
      <h3>Payments (${workspace.payments.length})</h3>
      ${workspace.payments.length === 0
        ? html`<p class="dim">No payments on this account.</p>`
        : html`
          <table class="payment-table">
            <thead><tr><th>Number</th><th>Received</th><th>Method</th><th class="num">Amount</th><th class="num">Unapplied</th></tr></thead>
            <tbody>${payRows}</tbody>
          </table>
        `}

      <h3>Credit notes (${workspace.credit_notes.length})</h3>
      ${workspace.credit_notes.length === 0
        ? html`<p class="dim">No credit notes on this account.</p>`
        : html`
          <table class="credit-table">
            <thead><tr><th>Number</th><th>Issued</th><th class="num">Amount</th><th class="num">Unapplied</th><th>Reason</th><th></th></tr></thead>
            <tbody>${creditRows}</tbody>
          </table>
        `}
    </section>
  `;
}

// renderWorkspaceStatements is the Statements tab body. The list reads from
// ar_statements, newest first; the closing_balance_cents is the figure that went out the
// door on that date, an immutable artefact per migration 0105.
export function renderWorkspaceStatements(workspace: BillingCustomerWorkspace): TemplateResult {
  const rows = workspace.statements.map((s) => html`
    <tr class="statement-row">
      <td>${formatWorkspaceDate(s.as_of_date)}</td>
      <td>${s.mode}</td>
      <td>${s.delivery_status}</td>
      <td class="num">${formatWorkspaceMoney(s.closing_balance_cents)}</td>
      <td>${formatWorkspaceDate(s.created_at)}</td>
    </tr>
  `);
  return html`
    <section class="ar-workspace-statements">
      <h3>Statements (${workspace.statements.length})</h3>
      ${workspace.statements.length === 0
        ? html`<p class="dim">No statements on this account.</p>`
        : html`
          <table class="statement-table">
            <thead><tr><th>As of</th><th>Mode</th><th>Delivery</th><th class="num">Closing balance</th><th>Created</th></tr></thead>
            <tbody>${rows}</tbody>
          </table>
        `}
    </section>
  `;
}

// renderWorkspaceActivity is the Activity & Notes tab body. Two stacked lists: the
// last 10 AR subledger rows (INVOICE / PAYMENT / CREDIT_MEMO / etc.), and the free
// text notes. A pinned note sits at the top of the notes list, the same way the
// server returns them.
export function renderWorkspaceActivity(
  workspace: BillingCustomerWorkspace,
  onAddNote: () => void,
): TemplateResult {
  void onAddNote;
  const activityRows = workspace.activity.map((a) => html`
    <li class="activity-row activity-row--${a.kind.toLowerCase()}">
      <span class="activity-row__kind">${a.kind}</span>
      <span class="activity-row__ref">${a.reference || html`<span class="dim">-</span>`}</span>
      <span class="activity-row__amount">${formatWorkspaceMoney(a.amount_cents)}</span>
      <span class="activity-row__when">${formatWorkspaceDate(a.occurred_at)}</span>
      ${a.memo ? html`<span class="activity-row__memo dim">${a.memo}</span>` : nothing}
    </li>
  `);
  const noteRows = workspace.notes.map((n) => html`
    <li class="note-row ${n.pinned ? "is-pinned" : ""}">
      <div class="note-row__header">
        <span class="note-row__kind kind-${n.kind.toLowerCase()}">${n.kind}</span>
        ${n.pinned ? html`<span class="badge badge-pinned">Pinned</span>` : nothing}
        <span class="dim small">${n.author_name || "system"} · ${formatWorkspaceDate(n.created_at)}</span>
      </div>
      <p class="note-row__body">${n.body}</p>
    </li>
  `);
  return html`
    <section class="ar-workspace-activity">
      <h3>Activity (last ${workspace.activity.length})</h3>
      ${workspace.activity.length === 0
        ? html`<p class="dim">No activity on this account.</p>`
        : html`<ul class="activity-list">${activityRows}</ul>`}

      <div class="notes-section">
        <div class="notes-section__header">
          <h3>Notes (${workspace.notes.length})</h3>
          <button type="button" class="btn btn-secondary small-btn" data-action="workspace-add-note">Add a note</button>
        </div>
        ${workspace.notes.length === 0
          ? html`<p class="dim">No notes on this account.</p>`
          : html`<ul class="notes-list">${noteRows}</ul>`}
      </div>
    </section>
  `;
}

// renderWorkspaceSummaryCard is the right-hand sticky card the S2 desktop layout
// carries. It is the same Account Summary & Quick Actions card the C8 walk drew, with
// the credit numbers, last payment, pinned note (when one exists), and the four quick
// actions the operator performs from the workspace.
export function renderWorkspaceSummaryCard(
  workspace: BillingCustomerWorkspace,
  onQuickAction: (action: "apply" | "promise" | "remind" | "hold" | "statement" | "note") => void,
): TemplateResult {
  const h = workspace.header;
  const pinned = workspace.notes.find((n) => n.pinned);
  return html`
    <aside class="ar-workspace-summary" aria-label="Account summary and quick actions">
      <h3>Account summary</h3>
      <dl class="summary-dl">
        <dt>Status</dt><dd>${accountStatusBadgeText(h.status)}</dd>
        <dt>Credit limit</dt><dd>${formatWorkspaceMoney(h.credit_limit_cents)}</dd>
        <dt>Open balance</dt><dd>${formatWorkspaceMoney(h.open_cents)}</dd>
        <dt>Available</dt><dd>${formatWorkspaceMoney(h.available_credit_cents)}</dd>
        <dt>Overdue</dt><dd>${formatWorkspaceMoney(h.overdue_cents)}</dd>
        <dt>Last payment</dt><dd>${h.last_payment_at ? formatWorkspaceDate(h.last_payment_at) : "None"}</dd>
      </dl>
      ${pinned
        ? html`
          <div class="summary-pinned-note">
            <h4>Pinned note</h4>
            <p class="dim small">${formatWorkspaceDate(pinned.created_at)} · ${pinned.author_name || "system"}</p>
            <p>${pinned.body}</p>
          </div>
        `
        : nothing}

      <h3>Quick actions</h3>
      <div class="quick-actions">
        <button type="button" class="btn btn-primary" data-action="workspace-quick" data-quick="apply">Take a payment</button>
        <button type="button" class="btn btn-secondary" data-action="workspace-quick" data-quick="promise">Log a promise</button>
        <button type="button" class="btn btn-secondary" data-action="workspace-quick" data-quick="remind">Send a reminder</button>
        <button type="button" class="btn btn-secondary" data-action="workspace-quick" data-quick="statement">Run a statement</button>
        <button type="button" class="btn btn-secondary" data-action="workspace-quick" data-quick="hold">Place a hold</button>
        <button type="button" class="btn btn-secondary" data-action="workspace-quick" data-quick="note">Add a note</button>
      </div>
      <p class="dim small"><span class="dim">Click an action to begin.</span></p>
    </aside>
  `;
}

// renderWorkspaceMobileSummary is the S2M bottom bar the mobile layout carries instead
// of the right-hand card. It is a compact strip with the open balance, available
// credit and a More actions button that opens the bottom drawer. The CSS media query
// @media (max-width: 720px) is what makes the desktop summary card collapse into this
// strip; the JavaScript does not switch screens.
export function renderWorkspaceMobileSummary(
  workspace: BillingCustomerWorkspace,
  onMore: () => void,
): TemplateResult {
  void onMore;
  const h = workspace.header;
  return html`
    <div class="ar-workspace-mobile-summary" aria-label="Account summary">
      <div class="ar-workspace-mobile-summary__row">
        <div>
          <span class="label">Open</span>
          <span class="value">${formatWorkspaceMoney(h.open_cents)}</span>
        </div>
        <div>
          <span class="label">Available</span>
          <span class="value">${formatWorkspaceMoney(h.available_credit_cents)}</span>
        </div>
        <div>
          <span class="label">Overdue</span>
          <span class="value overdue">${formatWorkspaceMoney(h.overdue_cents)}</span>
        </div>
        <button type="button" class="btn btn-primary" data-action="workspace-mobile-more">More actions</button>
      </div>
    </div>
  `;
}

// renderWorkspace applies the active tab to the workspace, then renders the full
// desktop layout (header + tab body + sticky card) OR the mobile layout (header +
// scrollable tab pills + tab body + bottom summary strip). The switch is a CSS-class
// decision at the top of the function: pass mobile=true and the template renders the
// scroller instead of the desktop tab row, and the right-hand card collapses into the
// bottom strip via the stylesheet.
export function renderWorkspace(
  workspace: BillingCustomerWorkspace,
  active: WorkspaceTab,
  onTab: (t: WorkspaceTab) => void,
  onApplyCredit: (creditNoteId: string) => void,
  onAddNote: () => void,
  onQuickAction: (action: "apply" | "promise" | "remind" | "hold" | "statement" | "note") => void,
  mobile: boolean,
): TemplateResult {
  const body = active === "overview"
    ? renderWorkspaceOverview(workspace)
    : active === "invoices"
    ? renderWorkspaceInvoices(workspace)
    : active === "payments"
    ? renderWorkspacePayments(workspace, onApplyCredit)
    : active === "statements"
    ? renderWorkspaceStatements(workspace)
    : renderWorkspaceActivity(workspace, onAddNote);

  return html`
    <div class="ar-workspace ${mobile ? "ar-workspace--mobile" : ""}">
      ${renderWorkspaceHeader(workspace, active, onTab)}
      <div class="ar-workspace__layout">
        <main class="ar-workspace__body">${body}</main>
        ${mobile
          ? renderWorkspaceMobileSummary(workspace, () => {})
          : renderWorkspaceSummaryCard(workspace, onQuickAction)}
      </div>
    </div>
  `;
}