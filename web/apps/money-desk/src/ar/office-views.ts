// The receivables office's screens, as plain render functions of a model. They hold no state and own no events:
// every control is a plain element carrying a data-action name and the slice's click listener acts on it. Form
// fields carry data-field names and are read when the button is pressed; none is bound. Every figure on a screen is
// the server's, formatted. A status is a word before it is a colour. No address or number a customer gave is drawn.
//
// References: the Money Desk references in hh-sup/design-refs/money-desk (receivables-home, account-open-invoices,
// credit-hold) for the cards, tables and the right hand panels; the original app's ar-console, ar-monthend and
// ar-invoice-register screens for what each panel holds.
import { html, nothing, type TemplateResult } from "lit";
import type { BillingStatement } from "@hardscapehouse/api-client";
import { formatMoney } from "./ar-money.js";
import { relativeDay } from "./ar-logic.js";
import type { OfficeModel } from "./office-desk.js";
import { contactLine, deliveryWord, runModeWord, runNoticeWords, statementGoes } from "./office-logic.js";

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
