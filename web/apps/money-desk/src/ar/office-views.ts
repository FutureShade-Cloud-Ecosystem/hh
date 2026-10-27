// The receivables office's screens, as plain render functions of a model. They hold no state and own no events:
// every control is a plain element carrying a data-action name and the slice's click listener acts on it. Form
// fields carry data-field names and are read when the button is pressed; none is bound. Every figure on a screen is
// the server's, formatted. A status is a word before it is a colour. No address or number a customer gave is drawn.
//
// References: the Money Desk references in hh-sup/design-refs/money-desk (receivables-home, account-open-invoices,
// credit-hold) for the cards, tables and the right hand panels; the original app's ar-console, ar-monthend and
// ar-invoice-register screens for what each panel holds.
import { html, nothing, type TemplateResult } from "lit";
import type { BillingInvoiceDetail, BillingOpenCreditSummary, BillingReminderRule, BillingStatement } from "@hardscapehouse/api-client";
import { formatMoney } from "./ar-money.js";
import { cardTitle, relativeDay } from "./ar-logic.js";
import type { Listing, OfficeModel } from "./office-desk.js";
import { PAYOUT_KIND_WORDS, DISPUTE_OUTCOMES, DISPUTE_REASONS, channelWord, contactLine, deliveryWord, disputeOutcomeWord, disputeReasonWord, recipientWords, relativeStamp, reminderPreview, runModeWord, runNoticeWords, sendStatusWord, statementHow, STATEMENT_ROUTE_LINE } from "./office-logic.js";

function deliveryPill(statement: BillingStatement): TemplateResult {
  const tone = statement.delivery_status === "FAILED" ? "stop" : statement.delivery_status === "NOT_QUEUED" ? "plain" : "good";
  return html`<span class="pill pill-${tone}">${deliveryWord(statement.delivery_status)}</span>`;
}

// ---- the last statement run (Statements tab) ------------------------------------------------------------------

export function renderRunPanel(om: OfficeModel, asOf: string): TemplateResult {
  const run = om.run;
  if (run.phase === "none") return html`<p class="dim small" data-run-none>No statement run yet on this visit. A run shows here with what went to whom.</p>`;
  if (run.phase === "loading") return html`<section class="panel" aria-busy="true" data-run><p class="dim" role="status">Reading the run back.</p><div class="ghost"></div></section>`;
  if (run.phase === "error") return html`<section class="panel" data-run><p class="alert alert-error" role="alert" data-run-error>${run.words}</p></section>`;
  const r = run.result.run;
  const rows = run.result.statements;
  return html`
    <section class="panel" aria-label="Last statement run" data-run>
      <div class="panel-head"><h3>Last statement run</h3><span class="pill pill-${r.status === "COMPLETED" ? "good" : r.status === "FAILED" ? "stop" : "watch"}" data-run-status>${r.status === "COMPLETED" ? "Completed" : r.status === "FAILED" ? "Failed" : "Running"}</span></div>
      <dl class="facts">
        <dt>Kind</dt><dd data-run-mode>${runModeWord(r.mode)}</dd>
        <dt>As of</dt><dd>${relativeDay(r.as_of_date, asOf)}</dd>
        <dt>Statements made</dt><dd data-run-count>${r.generated_count}</dd>
        <dt>Sent out</dt><dd data-run-notices>${runNoticeWords(r)}</dd>
        <dt>How</dt><dd data-run-how>${STATEMENT_ROUTE_LINE}</dd>
        ${r.error !== undefined && r.error !== "" ? html`<dt>Problem</dt><dd>${r.error}</dd>` : nothing}
      </dl>
      ${rows.length === 0
        ? html`<p class="dim" data-run-empty>No account had an open balance, so no statement was made.</p>`
        : html`
            <div class="scroll">
              <table class="stack">
                <thead><tr><th>Account</th><th class="num">Total due</th><th>Delivery</th><th>How</th></tr></thead>
                <tbody>
                  ${rows.map(
                    (s) => html`
                      <tr data-run-row>
                        <td><span class="who">${s.customer_name ?? "Account"}</span></td>
                        <td class="num">${formatMoney(s.closing_balance_cents)}</td>
                        <td>${deliveryPill(s)}</td>
                        <td data-run-goes>${statementHow(s.delivery_status)}</td>
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

// ---- an account's AP contact (the account's Contacts tab) -----------------------------------------------------

export function renderContacts(om: OfficeModel, account: { customerId: string; name: string }): TemplateResult {
  const read = om.contacts.get(account.customerId);
  const tagged = read !== undefined && read !== "error" && read.tagged;
  return html`
    <section class="panel" aria-label="AP contact for ${account.name}" data-contact-panel>
      <div class="panel-head"><h2>Accounts payable contact</h2>${tagged ? html`<span class="pill pill-good">Tagged</span>` : read === undefined || read === "error" ? nothing : html`<span class="pill pill-watch">Not tagged</span>`}</div>
      ${read === undefined
        ? html`<p class="dim" role="status" data-contact-loading>Reading the contact.</p>`
        : read === "error"
          ? html`<p class="alert alert-error" role="alert" data-contact-error>The contact could not be read. Nothing was changed.</p>`
          : html`<p data-contact-line>${contactLine(read)}</p>`}
      <p class="dim small">Reminders for this account go to this contact, through the messaging sink. The address and number are not shown back, here or anywhere on this desk.</p>
      <form class="held-form" aria-label="Tag or replace the AP contact">
        <label>Name or role at the customer<input type="text" data-field="contact-name" placeholder="Such as Accounts payable desk" autocomplete="off" maxlength="120" /></label>
        <label>Email<input type="text" inputmode="email" data-field="contact-email" placeholder="Enter the email to keep" autocomplete="off" /></label>
        <label>Phone<input type="text" inputmode="tel" data-field="contact-phone" placeholder="Enter the phone to keep" autocomplete="off" /></label>
        <p class="dim small">Saving replaces the whole contact, so enter every detail you want kept.</p>
        <div class="actions">
          <button type="button" class="btn btn-primary" data-action="contact-save" data-customer-id="${account.customerId}" ?disabled=${om.busy}>${tagged ? "Replace the contact" : "Tag the contact"}</button>
          ${tagged
            ? om.removing === account.customerId
              ? html`<button type="button" class="btn btn-danger" data-action="contact-remove-confirm" data-customer-id="${account.customerId}" ?disabled=${om.busy}>Remove it now</button><button type="button" class="btn btn-secondary" data-action="contact-remove-cancel">Keep it</button>`
              : html`<button type="button" class="btn btn-secondary" data-action="contact-remove" data-customer-id="${account.customerId}">Remove the contact</button>`
            : nothing}
        </div>
      </form>
    </section>
  `;
}

// ---- reminders (Reminders tab) ---------------------------------------------------------------------------------

function listingState<T>(listing: Listing<T>, what: string): TemplateResult | null {
  if (listing.phase === "none" || listing.phase === "loading") return html`<p class="dim" role="status" aria-busy="true">Loading ${what}.</p><div class="ghost"></div>`;
  if (listing.phase === "error") return html`<p class="alert alert-error" role="alert" data-error>${listing.words}</p>`;
  return null;
}

function checkbox(label: string, action: string, choice: string, on: boolean, note: string): TemplateResult {
  return html`
    <div class="check-row">
      <button type="button" class="pick" role="checkbox" aria-checked="${on ? "true" : "false"}" aria-label="${label}" data-action="${action}" data-choice="${choice}"></button>
      <span><strong>${label}</strong><span class="dim small">${note}</span></span>
    </div>
  `;
}

function remindForm(om: OfficeModel): TemplateResult {
  const f = om.remind;
  const policy = om.policy;
  const contact = f.customerId === "" ? null : (om.contacts.get(f.customerId) ?? null);
  const preview = policy === null || f.customerId === "" ? null : reminderPreview({ policy, channel: f.channel, contact, confirm: f.confirm });
  const blocked = policy !== null && policy.ar_tier !== "PRO" ? "Reminders are part of accelerated AR, and this dealer is on the base plan." : (preview?.blocked ?? "");
  const invoices = f.invoices === "loading" ? [] : f.invoices;
  return html`
    <section class="panel side" aria-label="Send a reminder" data-remind-form>
      <div class="panel-head"><h2>Send a reminder</h2><span class="pill pill-${policy?.ar_tier === "PRO" ? "good" : "watch"}" data-plan>${policy?.ar_tier === "PRO" ? "Accelerated AR" : "Base plan"}</span></div>
      <form>
        <label>Account
          <select data-field="remind-account">
            <option data-id="">Choose an account</option>
            ${om.accounts.map((a) => html`<option data-id="${a.customer_id}">${a.customer_name}</option>`)}
          </select>
        </label>
        <label>Invoice
          <select data-field="remind-invoice" ?disabled=${f.customerId === "" || f.invoices === "loading"}>
            ${f.customerId === "" ? html`<option>Choose an account first</option>` : f.invoices === "loading" ? html`<option>Loading the invoices</option>` : invoices.length === 0 ? html`<option>Nothing is owing</option>` : invoices.map((r) => html`<option data-id="${r.id}">${r.invoice_no}, ${formatMoney(r.balance_cents)}${r.days_overdue > 0 ? `, ${r.days_overdue} days over` : ""}</option>`)}
          </select>
        </label>
        <div><div class="label" id="remind-channel-label">Channel</div>
          <div class="seg" role="group" aria-labelledby="remind-channel-label">
            <button type="button" class="seg-item" data-action="remind-channel" data-choice="EMAIL" aria-pressed="${f.channel === "EMAIL" ? "true" : "false"}">Email</button>
            <button type="button" class="seg-item" data-action="remind-channel" data-choice="SMS" aria-pressed="${f.channel === "SMS" ? "true" : "false"}">Text</button>
          </div>
        </div>
        ${checkbox("Send it again", "remind-toggle", "resend", f.resend, "A resend carries a watermark with the days past due.")}
        ${checkbox("Confirm the account's own address", "remind-toggle", "confirm", f.confirm, "Used only when nobody is tagged for AP and the dealer's policy allows it.")}
      </form>
      <div class="preview" data-remind-preview aria-live="polite">
        <h3>What would go</h3>
        ${f.customerId === "" ? html`<p class="dim">Choose an account to see where a reminder would go.</p>` : nothing}
        ${blocked !== "" ? html`<p class="alert alert-warn" data-remind-blocked>${blocked}</p>` : nothing}
        ${blocked === "" ? (preview?.lines ?? []).map((l) => html`<p data-remind-line>${l}</p>`) : nothing}
        ${f.customerId !== "" ? html`<p class="dim small">${contactLine(contact === "error" ? null : contact)}</p>` : nothing}
      </div>
      <div class="actions">
        <button type="button" class="btn btn-primary ${blocked !== "" ? "held" : ""}" data-action="remind-send" aria-disabled="${blocked !== "" ? "true" : "false"}" ?disabled=${om.busy}>Send reminder</button>
      </div>
    </section>
  `;
}

function historyTable(om: OfficeModel): TemplateResult {
  const h = om.history;
  return html`
    <section class="panel" aria-label="Reminders sent" data-history>
      <div class="panel-head">
        <h3>Reminders sent</h3>
        <label class="inline-select"><span class="visually-hidden">Show reminders for</span>
          <select data-field="history-account">
            <option data-id="">Every account</option>
            ${om.accounts.map((a) => html`<option data-id="${a.customer_id}">${a.customer_name}</option>`)}
          </select>
        </label>
      </div>
      ${listingState(h, "the reminders") ??
      (h.phase === "ready" && h.items.length === 0
        ? html`<p class="dim" data-history-empty>No reminder has been sent${om.historyFor === "" ? "" : " to this account"} yet.</p>`
        : h.phase === "ready"
          ? html`
              <div class="scroll">
                <table class="stack hist">
                  <thead><tr><th>Sent</th><th>Account</th><th>Goes to</th><th>Age</th><th>Status</th><th>Opened</th></tr></thead>
                  <tbody>
                    ${h.items.map(
                      (r) => html`
                        <tr data-history-row>
                          <td class="nowrap">${relativeStamp(r.sent_at, om.nowMs)}</td>
                          <td>${om.accounts.find((a) => a.customer_id === r.customer_id)?.customer_name ?? "Account"}</td>
                          <td>${recipientWords(r)}, by ${channelWord(r.channel)}</td>
                          <td>${cardTitle(r.bucket)}${r.resend ? " (resend)" : ""}</td>
                          <td><span class="pill pill-${r.status === "FAILED" ? "stop" : r.status === "QUEUED" ? "watch" : "good"}">${sendStatusWord(r.status)}</span></td>
                          <td>${r.open_count === 0 ? "Not opened" : `Opened ${r.open_count}`}${r.click_count > 0 ? `, clicked ${r.click_count}` : ""}</td>
                        </tr>
                      `,
                    )}
                  </tbody>
                </table>
              </div>
            `
          : nothing)}
    </section>
  `;
}

function paperPanel(om: OfficeModel): TemplateResult {
  const p = om.paper;
  return html`
    <section class="panel" aria-label="Paper path" data-paper>
      <div class="panel-head"><h3>Paper path</h3><button type="button" class="btn btn-secondary small-btn" data-action="paper-sweep" ?disabled=${om.busy}>Run the sweep</button></div>
      <p class="dim small">An account that has been sent several reminders and opened none is flagged here. Move it to paper statements, or dismiss the flag, and say why: the note stays on the account.</p>
      ${listingState(p, "the paper path") ??
      (p.phase === "ready" && p.items.length === 0
        ? html`<p class="dim" data-paper-empty>No account is flagged.</p>`
        : p.phase === "ready"
          ? html`
              <ul class="alloc paper-list">
                ${p.items.map(
                  (e) => html`
                    <li data-paper-row>
                      <span class="who-block"><span class="who">${e.customer_name}</span><span class="dim small">${e.account_no} · ${e.delivered_sends} reminders in ${e.window_days} days, none opened · flagged ${relativeStamp(e.raised_at, om.nowMs)}</span></span>
                      ${om.closing !== null && om.closing.flagId === e.flag_id
                        ? html`<span class="close-form">
                            <label><span class="visually-hidden">Why</span><input type="text" data-field="paper-note" data-key="${e.flag_id}" placeholder="Why, in a few words" maxlength="300" autocomplete="off" /></label>
                            <button type="button" class="btn btn-primary small-btn" data-action="paper-close-confirm" data-flag-id="${e.flag_id}" ?disabled=${om.busy}>${om.closing.status === "MOVED_TO_PAPER" ? "Move to paper" : "Dismiss the flag"}</button>
                            <button type="button" class="btn btn-secondary small-btn" data-action="paper-close-cancel">Cancel</button>
                          </span>`
                        : html`<span class="close-form">
                            <button type="button" class="btn btn-secondary small-btn" data-action="paper-close" data-flag-id="${e.flag_id}" data-choice="MOVED_TO_PAPER">Moved to paper</button>
                            <button type="button" class="btn btn-secondary small-btn" data-action="paper-close" data-flag-id="${e.flag_id}" data-choice="DISMISSED">Dismiss</button>
                          </span>`}
                    </li>
                  `,
                )}
              </ul>
            `
          : nothing)}
    </section>
  `;
}

export function renderRemindersPanels(om: OfficeModel): TemplateResult {
  return html`
    <div class="cols remind-cols">
      <div class="col-main">${historyTable(om)}${paperPanel(om)}${reminderRulesTable(om)}</div>
      <div class="col-side">${remindForm(om)}</div>
    </div>
  `;
}

// ---- reminder rules (the C8 5-rule table, S5M / S8) ------------------------------------------------------

// reminderRulesTable renders the office's 5-rule reminder table with last_sent_at, the cumulative
// amount reminders have brought in, the per-rule paylink URL template (the {token} placeholder
// is substituted with the customer token before rendering) and the per-rule enrolled_count beside
// the inverse exclusion_count. Each row carries data-rule-id for the listener; the "Enrol again"
// button shows only on rows for which the current account has been exempted (data-excluded="true").
function reminderRulesTable(om: OfficeModel): TemplateResult {
  const rules = om.rules;
  const account = om.accounts.find((a) => a.customer_id === om.remind.customerId) ?? null;
  return html`
    <section class="panel" aria-label="Reminder rules" data-rules>
      <div class="panel-head">
        <h3>Reminder rules</h3>
        <span class="pill pill-plain">${rules.phase === "ready" ? rules.items.length : 0} rules</span>
      </div>
      ${rules.phase === "error"
        ? html`<p class="alert alert-error" role="alert" data-error>${rules.words}</p>`
        : rules.phase !== "ready"
          ? html`<p class="dim" role="status">Loading the rules.</p><div class="ghost"></div>`
          : rules.items.length === 0
            ? html`<p class="dim" data-rules-empty>No reminder rule is set up yet. Five default rules belong in the office view.</p>`
            : html`
                <div class="scroll">
                  <table class="stack" data-rules-table>
                    <thead>
                      <tr>
                        <th>Rule</th>
                        <th>Channel</th>
                        <th>Last sent</th>
                        <th class="num">Brought in</th>
                        <th class="num">Enrolled</th>
                        <th class="num">Excluded</th>
                        <th>Paylink</th>
                        <th></th>
                      </tr>
                    </thead>
                    <tbody>
                      ${rules.items.map(
                        (r: BillingReminderRule) => reminderRuleRow(r, account?.customer_id ?? "", om.busy),
                      )}
                    </tbody>
                  </table>
                </div>
              `}
      <p class="dim small">Every excluded account sees an "Enrol again" button. The {token} placeholder is substituted with the customer token before the paylink renders.</p>
    </section>
  `;
}

function reminderRuleRow(r: BillingReminderRule, customerId: string, busy: boolean): TemplateResult {
  const lastSent = r.last_sent_at !== null && r.last_sent_at !== undefined ? relativeStamp(r.last_sent_at, Date.now()) : "Never";
  return html`
    <tr data-reminder-rule-row data-rule-id="${r.id}" data-excluded="${customerId === "" ? "false" : "false"}">
      <td><span class="who">${r.label}</span><span class="dim small">${r.code} · past due ${r.days_past_due}d · max ${r.max_per_window}/window</span></td>
      <td>${r.channel}</td>
      <td class="nowrap">${lastSent}</td>
      <td class="num">${formatMoney(r.amount_brought_in_cents)}</td>
      <td class="num">${r.enrolled_count}</td>
      <td class="num">${r.exclusion_count}</td>
      <td class="dim small">${r.paylink_url_template}</td>
      <td>
        ${customerId === ""
          ? nothing
          : html`<button type="button" class="btn btn-secondary small-btn" data-action="reminder-enrol" data-rule-id="${r.id}" data-customer-id="${customerId}" ?disabled=${busy}>Enrol again</button>`}
      </td>
    </tr>
  `;
}

// ---- an invoice's extra actions (inside the invoice panel) -------------------------------------------------------

export function renderInvoiceActions(om: OfficeModel, d: BillingInvoiceDetail): TemplateResult {
  const open = d.dispute !== undefined && d.dispute.status === "OPEN";
  const closed = d.dispute !== undefined && d.dispute.status === "RESOLVED";
  return html`
    <div class="inv-actions" data-invoice-actions>
      <div class="actions">
        <button type="button" class="btn btn-secondary small-btn" data-action="invoice-send" data-invoice-id="${d.id}" ?disabled=${om.busy}>Send the invoice</button>
        <button type="button" class="btn btn-secondary small-btn" data-action="invoice-document" data-invoice-id="${d.id}" aria-pressed="${om.invoiceMode === "document" ? "true" : "false"}">View the document</button>
        ${d.order_id === undefined ? nothing : html`<button type="button" class="btn btn-secondary small-btn" data-action="invoice-dispatch" data-order-id="${d.order_id}" data-order-no="${d.order_no ?? ""}" ?disabled=${om.busy}>Can the order go out?</button>`}
        ${open
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="invoice-mode" data-choice="resolve" aria-pressed="${om.invoiceMode === "resolve" ? "true" : "false"}">Close the dispute</button>`
          : html`<button type="button" class="btn btn-secondary small-btn" data-action="invoice-mode" data-choice="dispute" aria-pressed="${om.invoiceMode === "dispute" ? "true" : "false"}">Open a dispute</button>`}
      </div>
      <p class="dim small">Sending goes to the account's contact through the messaging sink. The address is never shown.</p>
      ${closed && d.dispute !== undefined ? html`<p class="dim small" data-dispute-closed>Last dispute: ${disputeReasonWord(d.dispute.reason)}, closed as ${disputeOutcomeWord(d.dispute.outcome ?? "")}.</p>` : nothing}
      ${om.invoiceMode === "document" ? documentFrame(om) : nothing}
      ${om.invoiceMode === "dispute" ? disputeForm(om, d) : nothing}
      ${om.invoiceMode === "resolve" ? resolveForm(om, d) : nothing}
    </div>
  `;
}

function documentFrame(om: OfficeModel): TemplateResult {
  const doc = om.document;
  return html`
    <section class="doc-view" aria-label="Invoice document" data-doc-view>
      ${doc.phase === "error" ? html`<p class="alert alert-error" role="alert" data-error>${doc.words}</p>` : doc.phase === "ready" ? nothing : html`<p class="dim" role="status">Loading the document.</p>`}
      <div class="doc-slot" data-doc-slot></div>
      <div class="actions"><button type="button" class="btn btn-secondary small-btn" data-action="invoice-mode" data-choice="none">Close the document</button></div>
    </section>
  `;
}

function disputeForm(om: OfficeModel, d: BillingInvoiceDetail): TemplateResult {
  return html`
    <form class="held-form" aria-label="Open a dispute" data-dispute-form>
      <label>Why it is disputed
        <select data-field="dispute-reason">
          <option value="">Choose a reason</option>
          <option value="SHORT_DELIVERY">Short delivery</option>
          <option value="DAMAGED">Damaged</option>
          <option value="PRICING">Pricing</option>
          <option value="NOT_ORDERED">Not ordered</option>
          <option value="OTHER">Other</option>
        </select>
      </label>
      <label>Amount disputed (dollars)<input type="text" inputmode="decimal" data-field="dispute-amount" placeholder="Up to the balance" autocomplete="off" /></label>
      <label>What the customer said<input type="text" data-field="dispute-note" placeholder="Such as two pallets arrived cracked" autocomplete="off" /></label>
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="dispute-open" data-invoice-id="${d.id}" data-balance-cents="${d.totals.balance_cents}" ?disabled=${om.busy}>Open the dispute</button>
        <button type="button" class="btn btn-secondary" data-action="invoice-mode" data-choice="none">Cancel</button>
      </div>
    </form>
  `;
}

function resolveForm(om: OfficeModel, d: BillingInvoiceDetail): TemplateResult {
  return html`
    <form class="held-form" aria-label="Close the dispute" data-resolve-form>
      <label>How it ended
        <select data-field="dispute-outcome">
          <option value="">Choose an outcome</option>
          <option value="PAID">Paid</option>
          <option value="CREDITED">Credited</option>
          <option value="WRITTEN_OFF">Written off</option>
          <option value="WITHDRAWN">Withdrawn</option>
        </select>
      </label>
      <p class="dim small">Closing records how the argument ended. It does not itself move any money.</p>
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="dispute-close" data-invoice-id="${d.id}" ?disabled=${om.busy}>Close the dispute</button>
        <button type="button" class="btn btn-secondary" data-action="invoice-mode" data-choice="none">Cancel</button>
      </div>
    </form>
  `;
}

/** The reversal's inline form, or its start button, for one payment row. */
export function renderReverse(om: OfficeModel, payment: { payment_id: string; payment_no: string }): TemplateResult {
  if (om.reversing === payment.payment_id) {
    return html`<span class="inline-form"><label><span class="visually-hidden">Why it is reversed</span><input type="text" data-field="reverse-reason" placeholder="Why it is reversed" autocomplete="off" /></label><button type="button" class="btn btn-danger small-btn" data-action="reverse-confirm" data-payment-id="${payment.payment_id}" data-payment-no="${payment.payment_no}" ?disabled=${om.busy}>Confirm reversed</button><button type="button" class="btn btn-secondary small-btn" data-action="reverse-cancel">Cancel</button></span>`;
  }
  return html`<button type="button" class="btn btn-secondary small-btn" data-action="reverse-start" data-payment-id="${payment.payment_id}">Reverse</button>`;
}

/**
 * The re-application's form for a reversed payment's row: the money the core still holds for it, the open invoice it
 * goes on, how much and why. It follows the Reverse form above it (the same inline form, the same buttons, the same
 * words of consequence), and the picker offers the account's open invoices as the receivables list drew them.
 */
export function renderReapply(
  om: OfficeModel,
  payment: { payment_id: string; payment_no: string },
  open: readonly { id: string; invoice_no: string; balance_cents: number }[],
  currentInvoiceId: string,
): TemplateResult {
  const view = om.reapplying;
  const choices = open.filter((i) => i.balance_cents > 0);
  if (view === null || view.paymentId !== payment.payment_id) {
    // The invoice the panel is open on is picked to start with, when it is open: the id and balance ride data attributes.
    const current = choices.find((i) => i.id === currentInvoiceId);
    return html`<button type="button" class="btn btn-secondary small-btn" data-action="reapply-start" data-payment-id="${payment.payment_id}" data-invoice-id="${current?.id ?? ""}" data-balance-cents="${current === undefined ? "" : String(current.balance_cents)}">Apply its money</button><button type="button" class="btn btn-secondary small-btn" data-action="payout-start" data-payment-id="${payment.payment_id}">Pay it out</button>`;
  }
  if (view.held === "loading") return html`<span class="dim small" role="status">Reading what is held.</span>`;
  if (view.held === "error") return html`<button type="button" class="btn btn-secondary small-btn" data-action="reapply-cancel">Close</button>`;
  const held = view.held;
  if (held.held_cents <= 0) {
    return html`<span class="dim small" data-held>Nothing is held for this payment now.</span><button type="button" class="btn btn-secondary small-btn" data-action="reapply-cancel">Close</button>`;
  }
  if (view.mode === "payout") return renderPayout(om, payment, view.kind, held.held_cents);
  return html`
    <form class="held-form" aria-label="Apply held money to an invoice" data-reapply-form>
      <p class="callout" data-held><b>${formatMoney(held.held_cents)} is held for this customer.</b> The reversal put it back on their account as owing and kept the money. Putting it on an invoice settles that balance without taking the payment again.</p>
      <p class="dim small">Put it on</p>
      <div class="row-actions" role="group" aria-label="Invoice to put it on" data-reapply-invoices>
        ${choices.length === 0
          ? html`<span class="dim small">No open invoice to put it on.</span>`
          : choices.map(
              (i) => html`<button type="button" class="btn ${i.id === view.invoiceId ? "btn-primary" : "btn-secondary"} small-btn" data-action="reapply-pick" data-invoice-id="${i.id}" data-balance-cents="${String(i.balance_cents)}" aria-pressed="${i.id === view.invoiceId ? "true" : "false"}">${i.invoice_no} · ${formatMoney(i.balance_cents)}</button>`,
            )}
      </div>
      <label>Amount <span class="dim small" data-held-up-to>(up to ${formatMoney(held.held_cents)})</span>
        <input type="text" inputmode="decimal" data-field="reapply-amount" placeholder="Such as 100.00" autocomplete="off" />
      </label>
      <label>Why
        <input type="text" data-field="reapply-reason" placeholder="Such as keyed on the wrong account" autocomplete="off" />
      </label>
      <div class="actions">
        <button type="button" class="btn btn-primary small-btn" data-action="reapply-confirm" data-payment-id="${payment.payment_id}" data-payment-no="${payment.payment_no}" ?disabled=${om.busy || choices.length === 0}>Apply to invoice</button>
        <button type="button" class="btn btn-secondary small-btn" data-action="reapply-cancel">Cancel</button>
      </div>
    </form>
  `;
}

const PAYOUT_KINDS = ["DRAWER", "BANK", "WRITE_OFF"] as const;

/**
 * The pay out form for a reversed payment's held money (SF-2m, SF-2n): how it leaves (cash from the drawer, a cheque or
 * transfer, a write off after the count), how much, a reference for a cheque or transfer, and why. It follows the
 * "Apply its money" form beside it: the same inline form, the same picker buttons and the same words of consequence.
 */
function renderPayout(om: OfficeModel, payment: { payment_id: string; payment_no: string }, kind: string, heldCents: number): TemplateResult {
  const picked = kind === "DRAWER" || kind === "BANK" || kind === "WRITE_OFF" ? PAYOUT_KIND_WORDS[kind] : null;
  return html`
    <form class="held-form" aria-label="Pay out held money" data-payout-form>
      <p class="callout" data-held><b>${formatMoney(heldCents)} is held for this customer.</b> The money is still the dealer's, held for them. It leaves here only by the act that moves it: cash handed back, a cheque or transfer, or a write off once the till count has shown the shortage.</p>
      <p class="dim small">How it leaves</p>
      <div class="row-actions" role="group" aria-label="How the money leaves" data-payout-kinds>
        ${PAYOUT_KINDS.map(
          (k) => html`<button type="button" class="btn ${k === kind ? "btn-primary" : "btn-secondary"} small-btn" data-action="payout-kind" data-kind="${k}" aria-pressed="${k === kind ? "true" : "false"}">${PAYOUT_KIND_WORDS[k].pick}</button>`,
        )}
      </div>
      <label>Amount <span class="dim small" data-held-up-to>(up to ${formatMoney(heldCents)})</span>
        <input type="text" inputmode="decimal" data-field="payout-amount" placeholder="Such as 100.00" autocomplete="off" />
      </label>
      ${kind === "BANK"
        ? html`<label>Cheque number or transfer reference
            <input type="text" data-field="payout-reference" placeholder="Such as CHQ 100245" autocomplete="off" />
          </label>`
        : nothing}
      <label>Why
        <input type="text" data-field="payout-reason" placeholder="Such as the customer asked for it back" autocomplete="off" />
      </label>
      <div class="actions">
        <button type="button" class="btn btn-primary small-btn" data-action="payout-confirm" data-payment-id="${payment.payment_id}" data-payment-no="${payment.payment_no}" ?disabled=${om.busy || picked === null}>${picked?.button ?? "Pay it out"}</button>
        <button type="button" class="btn btn-secondary small-btn" data-action="reapply-cancel">Cancel</button>
      </div>
    </form>
  `;
}

/**
 * The invoice panel's credits section (FW1-12b): applied credits on the invoice (with inline reversal by reason) and
 * the customer's open credits (open credit memos and unapplied account credits, with inline apply-to-invoice form).
 */
export function renderInvoiceCredits(
  om: OfficeModel,
  d: BillingInvoiceDetail,
  open: readonly { id: string; invoice_no: string; balance_cents: number }[],
): TemplateResult {
  const apps = d.credit_applications ?? [];
  const credits = d.open_credits ?? [];
  const choices = open.filter((i) => i.balance_cents > 0);
  const current = choices.find((i) => i.id === d.id) ?? choices[0];
  const currentPayable = current === undefined ? null : current.id === d.id ? d.totals.payable_cents : current.balance_cents;
  return html`
    <h3>Credits on this invoice</h3>
    ${apps.length === 0
      ? html`<p class="dim" data-credit-apps-empty>No credit has been applied to this invoice yet.</p>`
      : html`
          <ul class="alloc" data-credit-apps>
            ${apps.map(
              (app) => html`
                <li data-credit-app-row>
                  <span>${app.credit_no} · ${app.credit_kind === "MEMO" ? "credit memo" : "account credit"} · ${formatMoney(app.amount_cents)}${app.reason ? ` · ${app.reason}` : ""}
                    ${app.reversed_at !== undefined ? html`<span class="pill pill-plain">Reversed</span>` : html`<span class="pill pill-good">Applied</span>`}</span>
                  ${app.reversed_at !== undefined
                    ? nothing
                    : om.reversingCreditApp === app.application_id
                      ? html`<span class="inline-form" data-credit-reverse-form><label><span class="visually-hidden">Why the credit application is reversed</span><input type="text" data-field="credit-reverse-reason" placeholder="Why it is reversed, such as applied to the wrong invoice" autocomplete="off" /></label><button type="button" class="btn btn-danger small-btn" data-action="credit-reverse-confirm" data-invoice-id="${d.id}" data-application-id="${app.application_id}" ?disabled=${om.busy}>Confirm reverse credit</button><button type="button" class="btn btn-secondary small-btn" data-action="credit-reverse-cancel">Cancel</button></span>`
                      : html`<span class="row-actions"><button type="button" class="btn btn-secondary small-btn" data-action="credit-reverse-start" data-application-id="${app.application_id}">Reverse credit</button></span>`}
                </li>
              `,
            )}
          </ul>
        `}
    <h3>Open credits on this account</h3>
    ${credits.length === 0
      ? html`<p class="dim" data-open-credits-empty>No open credit memo or account credit is on this account.</p>`
      : html`
          <ul class="alloc" data-open-credits>
            ${credits.map(
              (c) => html`
                <li data-open-credit-row>
                  <span><b>${c.credit_no}</b> · ${c.kind === "MEMO" ? "credit memo" : "account credit"} · ${c.issued_at}${c.reason ? ` · ${c.reason}` : ""} · <span data-open-credit-amount>${formatMoney(c.open_cents)} open</span></span>
                  ${om.applyingCredit !== null && om.applyingCredit.creditId === c.id
                    ? renderApplyCreditForm(om, c, d, choices)
                    : choices.length === 0
                      ? nothing
                      : html`<span class="row-actions"><button type="button" class="btn btn-secondary small-btn" data-action="credit-apply-start" data-customer-id="${d.customer.customer_id}" data-credit-id="${c.id}" data-credit-kind="${c.kind}" data-credit-no="${c.credit_no}" data-credit-open="${String(c.open_cents)}" data-invoice-id="${current?.id ?? ""}" data-balance-cents="${currentPayable === null ? "" : String(currentPayable)}">Apply credit</button></span>`}
                </li>
              `,
            )}
          </ul>
        `}
  `;
}

function renderApplyCreditForm(
  om: OfficeModel,
  credit: BillingOpenCreditSummary,
  d: BillingInvoiceDetail,
  choices: readonly { id: string; invoice_no: string; balance_cents: number }[],
): TemplateResult {
  const view = om.applyingCredit;
  if (view === null) return html``;
  const maxCents = view.invoiceBalanceCents === null ? credit.open_cents : Math.min(credit.open_cents, view.invoiceBalanceCents);
  const kindLabel = credit.kind === "MEMO" ? "credit memo" : "account credit";
  return html`
    <form class="held-form" aria-label="Apply credit to an invoice" data-credit-apply-form>
      <p class="callout" data-credit-callout><b>${formatMoney(credit.open_cents)} is open on ${kindLabel} ${credit.credit_no}.</b> Applying it reduces the open balance of the chosen invoice without recording a new payment.</p>
      <p class="dim small">Apply to invoice</p>
      <div class="row-actions" role="group" aria-label="Invoice to apply credit to" data-credit-apply-invoices>
        ${choices.length === 0
          ? html`<span class="dim small">No open invoice to apply it to.</span>`
          : choices.map((i) => {
              const payable = i.id === d.id ? d.totals.payable_cents : i.balance_cents;
              return html`<button type="button" class="btn ${i.id === view.invoiceId ? "btn-primary" : "btn-secondary"} small-btn" data-action="credit-apply-pick" data-invoice-id="${i.id}" data-balance-cents="${String(payable)}" aria-pressed="${i.id === view.invoiceId ? "true" : "false"}">${i.invoice_no} · ${formatMoney(payable)}</button>`;
            })}
      </div>
      <label>Amount <span class="dim small" data-credit-up-to>(up to ${formatMoney(maxCents)})</span>
        <input type="text" inputmode="decimal" data-field="credit-apply-amount" placeholder="Such as 100.00" autocomplete="off" />
      </label>
      <div class="row-actions">
        <button type="button" class="btn btn-secondary small-btn" data-action="credit-apply-full" ?disabled=${om.busy}>Use full ${formatMoney(maxCents)}</button>
      </div>
      <label>Why
        <input type="text" data-field="credit-apply-reason" placeholder="Such as applying open credit memo to balance" autocomplete="off" />
      </label>
      <div class="actions">
        <button type="button" class="btn btn-primary small-btn" data-action="credit-apply-confirm" ?disabled=${om.busy || choices.length === 0}>Apply to invoice</button>
        <button type="button" class="btn btn-secondary small-btn" data-action="credit-apply-cancel">Cancel</button>
      </div>
    </form>
  `;
}
