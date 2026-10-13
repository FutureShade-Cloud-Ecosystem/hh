// The Today surface: the morning briefing. The account's standing (open
// balance, available credit, terms, the hold flag) and the orders most recently
// in motion, each with its status word and, where the contract's state map
// derives one, its tracking word. Visual reference: the ref's TodayPage (the
// first screen of the day leads with what is in motion, not with a pipeline
// total) and the HH: Pro board's account strip. Every figure is the core's own
// through the kit's money(); nothing here adds, and an absent figure reads as
// the kit's placeholder, never a confident zero.
import { html, nothing, type TemplateResult } from "lit";
import { tenantDashboard, type TenantDashboard } from "@hardscapehouse/api-client";
import { calendarDay, money, statusChip, trackingChip, trackingOf } from "@hardscapehouse/pro-kit";
import { metaLine, type Page, type SurfaceAnswer } from "./views.js";

export const TODAY_EMPTY = "No orders yet. Your orders and quotes will land here.";

export async function todayLoad(page: Page): Promise<SurfaceAnswer> {
  const answer = await tenantDashboard.tenantGetDashboard(page.client);
  if (answer.ok) return { kind: "ready", data: answer.value };
  if (answer.status === 403) return { kind: "refusal" };
  return { kind: "error", status: answer.status };
}

function orderRow(order: TenantDashboard["recent_orders"][number]): TemplateResult {
  const tracking = trackingOf(order.status, order.fulfillment);
  return html`<div class="rowline">
    <span class="mono figure">${order.order_no}</span>
    ${statusChip("order", order.status)}
    ${tracking === null ? nothing : trackingChip(tracking)}
    <span class="grow"></span>
    <span>${calendarDay(order.requested_date)}</span>
    <span class="mono">${money(order.total_cents)}</span>
  </div>`;
}

export function todayView(dataRaw: unknown, page: Page): TemplateResult {
  void page;
  const data = dataRaw as TenantDashboard;
  const hold = data.account.on_hold === true;
  return html`<section aria-label="Account standing">
      <div class="card">
        <div data-testid="today-account">
          <div class="rowline">
            <span class="name figure">${data.account.name}</span>
            <span class="grow"></span>
            <span class="mono dim">${data.account.account_no}</span>
          </div>
          ${metaLine([
            html`<span>Open balance <span class="mono">${money(data.open_balance_cents)}</span></span>`,
            html`<span>Available credit <span class="mono">${money(data.available_credit_cents)}</span></span>`,
            html`<span>Net ${data.account.payment_terms_days}</span>`,
            hold ? html`<span class="pchip pchip-stop" data-status="HOLD">On hold</span>` : nothing,
          ])}
        </div>
      </div>
    </section>
    <section class="section" aria-label="Recent orders">
      <h2>Recent orders</h2>
      ${data.recent_orders.length === 0
        ? html`<p class="emptyline" data-testid="recent-empty">${TODAY_EMPTY}</p>`
        : html`<ul class="cards" data-testid="today-rows">
            ${data.recent_orders.map((order) => html`<li><div class="card">${orderRow(order)}</div></li>`)}
          </ul>`}
    </section>`;
}
