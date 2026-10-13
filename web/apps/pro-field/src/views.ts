// Pro Field's screens as pure template functions over the state the root
// holds: the section row, the orders list with its states, the order view
// (the journey, the asked-for day, the items), and the section placeholders.
// Ported and polished from the original HH Pro demo's OrderTrackingPage,
// OrderTimeline and ProjectPage (ref-hh-pro src/ui) and the dealer portal's
// orders, order detail, projects and team screens (INVENTORY section 7).
//
// The design system's position gate binds this app like every dependent: a
// value rides only in a class, role, data- or aria- attribute, always quoted.
// A control's value is the root's wiring pass (assignment in updated()),
// never a binding; every control names itself with data-field or data-action
// so the root's one click and one input listener act for the whole app.
import { html, nothing, type TemplateResult } from "lit";
import type { TenantOrder, TenantOrderDetail } from "@hardscapehouse/api-client";
import {
  money,
  qtyWithUnit,
  statusChip,
  statusWords,
  trackingChip,
  trackingOf,
  TRACKING_WORDS,
} from "@hardscapehouse/pro-kit";
import { button, segmented } from "@hardscapehouse/design-system/src/primitives.js";
import type { SectionRef } from "./sections.js";
import { fulfillmentWords, promisedWords, lateWords, timelineSteps } from "./timeline.js";

// ---- the section row ----------------------------------------------------------------------------

export function sectionNav(sections: readonly SectionRef[], current: string): TemplateResult {
  return segmented({
    label: "Sections",
    field: "section",
    action: "section",
    testid: "section-nav",
    options: sections.map((s) => ({ label: s.label, value: s.id })),
    selected: current,
  });
}

// ---- the orders list ----------------------------------------------------------------------------

/** The words an order row reads, for the tests and for nothing else. */
export function orderRowWords(order: TenantOrder): string {
  const tracking = trackingOf(order.status, order.fulfillment);
  return [
    order.order_no,
    tracking === null ? statusWords("order", order.status) : TRACKING_WORDS[tracking],
    fulfillmentWords(order.fulfillment),
    money(order.total_cents),
    shortDay2(order.requested_date),
  ].join(" ");
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** The dealer's short day for a bare day the core ships; anything else reads as nothing. */
function shortDay2(value: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (m === null) return "";
  const month = Number(m[2]);
  const day = Number(m[3]);
  if (month < 1 || month > 12 || day < 1 || day > 31) return "";
  return `${MONTHS[month - 1]} ${day}`;
}

export function orderRow(order: TenantOrder): TemplateResult {
  const tracking = trackingOf(order.status, order.fulfillment);
  return html`<li class="orow">
    <button type="button" class="rowbtn" data-action="open-order" data-order-id="${order.id}" data-testid="order-row">
      <span class="cell head"><span class="mono name">${order.order_no}</span>${tracking === null ? statusChip("order", order.status) : trackingChip(tracking)}</span>
      <span class="cell"><span class="dim">${fulfillmentWords(order.fulfillment)}</span><span class="dim">Asked for ${shortDay2(order.requested_date)}</span><span class="mono total">${money(order.total_cents)}</span></span>
    </button>
  </li>`;
}

export type Phase = "idle" | "loading" | "ready" | "empty" | "error" | "refusal";

export interface OrdersListProps {
  phase: Phase;
  orders: readonly TenantOrder[];
  emptyWords: string;
}

export function ordersList(props: OrdersListProps): TemplateResult {
  switch (props.phase) {
    case "ready":
      return html`<ul class="orows" data-testid="landing-rows">${props.orders.map((order) => orderRow(order))}</ul>`;
    case "empty":
      return html`<hh-pro-state data-state="empty" data-testid="landing-state">${props.emptyWords}</hh-pro-state>`;
    case "error":
      return html`<hh-pro-state data-state="error" data-testid="landing-state">This did not load. Nothing was changed.</hh-pro-state>`;
    case "refusal":
      return html`<hh-pro-state data-state="refusal" data-testid="landing-state"></hh-pro-state>`;
    default:
      return html`<hh-pro-state data-state="loading" data-testid="landing-state"></hh-pro-state>`;
  }
}

// ---- the order view -----------------------------------------------------------------------------

export interface OrderViewProps {
  detail: TenantOrderDetail | null;
  phase: Phase;
  /** The dealer's own today, as a bare day, for the late line. */
  today: string;
  /** Later surfaces of this view ride these slots so the head never moves. */
  instructions?: TemplateResult;
  returns?: TemplateResult;
}

export function orderView(props: OrderViewProps): TemplateResult {
  if (props.phase !== "ready" || props.detail === null) {
    if (props.phase === "refusal") return html`<hh-pro-state data-state="refusal" data-testid="order-state"></hh-pro-state>`;
    if (props.phase === "error") return html`<hh-pro-state data-state="error" data-testid="order-state">This did not load. Nothing was changed.</hh-pro-state>`;
    return html`<hh-pro-state data-state="loading" data-testid="order-state"></hh-pro-state>`;
  }
  const o = props.detail;
  const tracking = trackingOf(o.status, o.fulfillment);
  const promised = promisedWords(o);
  const late = lateWords(o, props.today);
  return html`<div class="card" data-testid="order-head">
      ${button({ label: "Orders", kind: "secondary", action: "back-to-orders" })}
      <div class="titlerow">
        <h2 class="mono">${o.order_no}</h2>
        ${tracking === null ? statusChip("order", o.status) : trackingChip(tracking)}
      </div>
      <p class="promised">
        <span class="dim">${promised.label}</span>
        <span class="when">${promised.day}</span>
      </p>
      <p class="facts">
        <span>${fulfillmentWords(o.fulfillment)}</span>
        <span class="dim" data-testid="order-placed">Placed ${placedDay(o.created_at)}</span>
        <span class="mono">${money(o.total_cents)}</span>
      </p>
      ${late === "" ? nothing : html`<p class="late" role="note">${late}</p>`}
      ${o.status === "CANCELLED" ? html`<p class="dim">${o.order_no} was cancelled. Nothing further is scheduled.</p>` : nothing}
    </div>
    ${journeyCard(o)}
    ${itemsCard(o)}
    ${props.instructions ?? nothing}
    ${props.returns ?? nothing}`;
}

// The dealer's day for the placed line and the journey's first step.
function placedDay(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso);
  if (m === null) return "";
  const month = Number(m[2]);
  const day = Number(m[3]);
  if (month < 1 || month > 12 || day < 1 || day > 31) return "";
  return `${MONTHS[month - 1]} ${day}`;
}

function journeyCard(o: TenantOrderDetail): TemplateResult {
  const steps = timelineSteps(o);
  const notes: TemplateResult[] = [];
  if (o.status === "PARTIALLY_FULFILLED") notes.push(html`<p class="dim">Part of this order has been delivered. The rest follows.</p>`);
  if (o.has_backorder === true) notes.push(html`<p class="dim">Some lines are on backorder. The dealer sends them when they land.</p>`);
  return html`<div class="card">
    <h3>Progress</h3>
    ${notes}
    <ol class="timeline" data-testid="order-timeline">
      ${steps.map(
        (step, index) => html`<li class="tstep ${step.state}" data-tracking="${step.tracking}">
          <span class="rail"><span class="dot"></span>${index < steps.length - 1 ? html`<span class="rail-line"></span>` : nothing}</span>
          <span class="tbody">
            <span class="twords">${step.words}</span>
            ${step.day === undefined ? nothing : html`<span class="dim tday">${step.day}</span>`}
          </span>
        </li>`,
      )}
    </ol>
  </div>`;
}

function itemsCard(o: TenantOrderDetail): TemplateResult {
  return html`<div class="card">
    <h3>Items</h3>
    <ul class="lines" data-testid="order-lines">
      ${o.lines.map(
        (line) => html`<li class="line">
          <span class="linehead"><span>${line.description}</span><span class="mono">${money(line.line_total_cents)}</span></span>
          <span class="dim">${qtyWithUnit(line.qty_entered, line.uom_entered)} at ${money(line.unit_price_cents)}</span>
        </li>`,
      )}
    </ul>
    <p class="totals" data-testid="order-totals">
      <span><span class="dim">Subtotal</span> <span class="mono">${money(o.subtotal_cents)}</span></span>
      <span><span class="dim">Tax</span> <span class="mono">${money(o.tax_cents)}</span></span>
      <span><span class="dim">Total</span> <span class="mono">${money(o.total_cents)}</span></span>
    </p>
  </div>`;
}

// ---- the sections still to open -----------------------------------------------------------------

export function sectionPlaceholder(): TemplateResult {
  return html`<hh-pro-state data-state="empty" data-testid="section-state">This section is not open yet.</hh-pro-state>`;
}
