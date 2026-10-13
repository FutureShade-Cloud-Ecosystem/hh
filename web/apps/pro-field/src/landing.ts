// The Pro Field landing surface: Tracking (GET /api/v1/tenant/orders,
// tenantListOrders, held by view_orders_deliveries). Visual reference: HH: Pro,
// field-tracking-phone-brand (an order per row with its tracking word). The
// tracking word comes from the contract's state map (the kit's board.ts); where
// the contract leaves it to a delivery stop no tenant route serves, the row
// shows the order's own state word instead of a guess.
import { html, type TemplateResult } from "lit";
import { tenantOrders, type TenantOrder } from "@hardscapehouse/api-client";
import { statusChip, trackingChip, trackingOf, type LandingSpec } from "@hardscapehouse/pro-kit";

export type Item = TenantOrder;

const FULFILLMENT_WORDS: Record<string, string> = { PICKUP: "Pickup", DELIVERY: "Delivery", WILL_CALL: "Will call" };

function row(order: Item): TemplateResult {
  const tracking = trackingOf(order.status, order.fulfillment);
  return html`<span class="mono">${order.order_no}</span>
    ${tracking === null ? statusChip("order", order.status) : trackingChip(tracking)}
    <span>${FULFILLMENT_WORDS[order.fulfillment] ?? ""}</span>`;
}

export const landing: LandingSpec<Item> = {
  surface: "tracking",
  capability: "view_orders_deliveries",
  async load(client) {
    const answer = await tenantOrders.tenantListOrders(client);
    return answer.ok ? { ok: true, value: answer.value.items } : answer;
  },
  row,
  emptyWords: "No orders to track yet. They appear here once the dealer receives one.",
};
