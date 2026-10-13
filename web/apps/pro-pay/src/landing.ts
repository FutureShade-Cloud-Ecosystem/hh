// The Pro Pay landing surface: Invoices (GET /api/v1/tenant/invoices,
// tenantListInvoices, held by view_billing). Visual reference: HH: Pro,
// role-ap-desktop-light-r2 for the rows and pay-hosted-phone-light for the pay
// flow that follows. Paying is the next build and is money class: hosted fields
// or tokens only, sandbox only, never card data in this app.
import { html, type TemplateResult } from "lit";
import { tenantBilling, type TenantInvoice } from "@hardscapehouse/api-client";
import { money, calendarDay, statusChip, type LandingSpec } from "@hardscapehouse/pro-kit";

export type Item = TenantInvoice;

function row(invoice: Item): TemplateResult {
  return html`<span class="mono">${invoice.invoice_no}</span>
    ${statusChip("invoice", invoice.status)}
    <span>Due ${calendarDay(invoice.due_date)}</span>
    <span class="mono">${money(invoice.balance_cents)} due of ${money(invoice.total_cents)}</span>`;
}

export const landing: LandingSpec<Item> = {
  surface: "invoices",
  capability: "view_billing",
  async load(client) {
    const answer = await tenantBilling.tenantListInvoices(client);
    return answer.ok ? { ok: true, value: answer.value.items } : answer;
  },
  row,
  emptyWords: "No invoices yet. They appear here when an order is billed.",
};
