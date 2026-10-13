// The receivables office's screens, as plain render functions of a model. They hold no state and own no events:
// every control is a plain element carrying a data-action name and the slice's click listener acts on it. Form
// fields carry data-field names and are read when the button is pressed; none is bound. Every figure on a screen is
// the server's, formatted. A status is a word before it is a colour. No address or number a customer gave is drawn.
//
// References: the Money Desk references in hh-sup/design-refs/money-desk (receivables-home, account-open-invoices,
// credit-hold) for the cards, tables and the right hand panels; the original app's ar-console, ar-monthend and
// ar-invoice-register screens for what each panel holds.
import { html, nothing, type TemplateResult } from "lit";
import type { BillingInvoiceDetail, BillingStatement } from "@hardscapehouse/api-client";
import { formatMoney } from "./ar-money.js";
import { cardTitle, relativeDay } from "./ar-logic.js";
import type { Listing, OfficeModel } from "./office-desk.js";
import { DISPUTE_OUTCOMES, DISPUTE_REASONS, channelWord, contactLine, deliveryWord, disputeOutcomeWord, disputeReasonWord, recipientWords, relativeStamp, reminderPreview, runModeWord, runNoticeWords, sendStatusWord, statementGoes } from "./office-logic.js";

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
        ${r.error !== undefined && r.error !== "" ? html`<dt>Problem</dt><dd>${r.error}</dd>` : nothing}
      </dl>
      ${rows.length === 0
        ? html`<p class="dim" data-run-empty>No account had an open balance, so no statement was made.</p>`
        : html`
            <div class="scroll">
              <table class="stack">
                <thead><tr><th>Account</th><th class="num">Total due</th><th>Delivery</th><th>Goes to</th></tr></thead>
                <tbody>
                  ${rows.map(
                    (s) => html`
                      <tr data-run-row>
                        <td><span class="who">${s.customer_name ?? "Account"}</span></td>
                        <td class="num">${formatMoney(s.closing_balance_cents)}</td>
                        <td>${deliveryPill(s)}</td>
                        <td data-run-goes>${statementGoes(om.contacts.get(s.customer_id) ?? null, s.delivery_status)}</td>
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
      <p class="dim small">Statements, reminders and invoices for this account go to this contact, through the messaging sink. The address and number are not shown back, here or anywhere on this desk.</p>
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
            <option value="" ?selected=${f.customerId === ""}>Choose an account</option>
            ${om.accounts.map((a) => html`<option value="${a.customer_id}" ?selected=${f.customerId === a.customer_id}>${a.customer_name}</option>`)}
          </select>
        </label>
        <label>Invoice
          <select data-field="remind-invoice" ?disabled=${f.customerId === "" || f.invoices === "loading"}>
            ${f.customerId === "" ? html`<option>Choose an account first</option>` : f.invoices === "loading" ? html`<option>Loading the invoices</option>` : invoices.length === 0 ? html`<option>Nothing is owing</option>` : invoices.map((r) => html`<option data-id="${r.id}" ?selected=${f.invoiceId === r.id}>${r.invoice_no}, ${formatMoney(r.balance_cents)}${r.days_overdue > 0 ? `, ${r.days_overdue} days over` : ""}</option>`)}
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
            <option data-id="" ?selected=${om.historyFor === ""}>Every account</option>
            ${om.accounts.map((a) => html`<option data-id="${a.customer_id}" ?selected=${om.historyFor === a.customer_id}>${a.customer_name}</option>`)}
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
                            <label><span class="visually-hidden">Why</span><input type="text" data-field="paper-note" data-key="${e.flag_id}" placeholder="${om.closing.status === "MOVED_TO_PAPER" ? "Why it moves to paper" : "Why it is dismissed"}" maxlength="300" autocomplete="off" /></label>
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
      <div class="col-main">${historyTable(om)}${paperPanel(om)}</div>
      <div class="col-side">${remindForm(om)}</div>
    </div>
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
          ${DISPUTE_REASONS.map((r) => html`<option value="${r}">${disputeReasonWord(r)}</option>`)}
        </select>
      </label>
      <label>Amount disputed (dollars)<input type="text" inputmode="decimal" data-field="dispute-amount" placeholder="Up to ${formatMoney(d.totals.balance_cents)}" autocomplete="off" /></label>
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
          ${DISPUTE_OUTCOMES.map((o) => html`<option value="${o}">${disputeOutcomeWord(o)}</option>`)}
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
