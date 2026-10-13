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
import type { TenantOrder, TenantOrderDetail, TenantReturnEligibility, TenantReturnRequest } from "@hardscapehouse/api-client";
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
import { RETURN_STATUS_WORDS, returnChipTone, returnLineWords, returnReasonWords } from "./returns.js";

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

// ---- delivery instructions ----------------------------------------------------------------------

export interface InstructionsProps {
  detail: TenantOrderDetail;
  editable: boolean;
  /** The read-only reason, empty when the form shows. */
  note: string;
  dirty: boolean;
  busy: boolean;
  saved: boolean;
  error: string;
}

export function instructionsCard(props: InstructionsProps): TemplateResult {
  const stored = props.detail.delivery_instructions;
  return html`<div class="card" data-testid="instructions-card">
    <h3>Delivery instructions</h3>
    <p class="dim">Gate codes, where to stack it, who to call on arrival. The driver sees this.</p>
    ${props.saved ? html`<p class="notice" role="status" data-testid="instructions-saved">Delivery instructions saved.</p>` : nothing}
    ${props.error === "" ? nothing : html`<p class="fault" role="alert" data-testid="instructions-error">${props.error}</p>`}
    ${props.editable
      ? html`<div class="form">
          <label for="instructions-input">Notes for the delivery</label>
          <textarea class="control" id="instructions-input" data-field="instructions" data-testid="instructions-input" rows="3" placeholder="Gate code 4471. Stack on the gravel pad north of the garage, do not block the drive."></textarea>
          <div class="formrow">
            <span class="grow"></span>
            ${button({ label: "Save notes", kind: "secondary", action: "save-instructions", disabled: props.busy || !props.dirty })}
          </div>
        </div>`
      : html`<p class="readonly-note" data-testid="instructions-note">${stored === "" ? "No instructions." : stored}</p>
          ${props.note === "" ? nothing : html`<p class="dim">${props.note}</p>`}`}
  </div>`;
}

// ---- returns ------------------------------------------------------------------------------------

export interface EligibilityState {
  phase: "idle" | "loading" | "done" | "error";
  data: TenantReturnEligibility | null;
  words: string;
  canRetry: boolean;
}

export interface ReturnFormState {
  qty: Map<string, string>;
  reason: string;
  note: string;
  busy: boolean;
  error: string;
}

export interface ReturnsProps {
  mayRequest: boolean;
  requests: readonly TenantReturnRequest[] | null;
  eligibility: EligibilityState;
  form: ReturnFormState;
  notice: string;
}

export function returnsCard(props: ReturnsProps): TemplateResult {
  return html`<div class="card" data-testid="returns-card">
    <h3>Returns</h3>
    ${props.notice === "" ? nothing : html`<p class="notice" role="status" data-testid="return-notice">${props.notice}</p>`}
    ${props.requests === null
      ? nothing
      : props.requests.length === 0
        ? html`<p class="dim" data-testid="returns-empty">No return requests on this order.</p>`
        : html`<ul class="lines" data-testid="return-requests">${props.requests.map(returnRequestRow)}</ul>`}
    ${props.eligibility.phase === "idle" && props.mayRequest
      ? html`<button type="button" class="btn btn-secondary" data-action="request-return" data-testid="request-return">Request a return</button>`
      : nothing}
    ${props.eligibility.phase === "loading" ? html`<p class="dim">Checking what can go back...</p>` : nothing}
    ${props.eligibility.phase === "error"
      ? html`<p class="dim" data-testid="return-words">${props.eligibility.words}</p>
          ${props.eligibility.canRetry ? button({ label: "Try again", kind: "secondary", action: "request-return" }) : nothing}`
      : nothing}
    ${props.eligibility.phase === "done" && props.eligibility.data !== null ? returnForm(props.eligibility.data, props.form) : nothing}
  </div>`;
}

function returnRequestRow(request: TenantReturnRequest): TemplateResult {
  const tone = returnChipTone(request.status);
  const words = Object.prototype.hasOwnProperty.call(RETURN_STATUS_WORDS, request.status) ? RETURN_STATUS_WORDS[request.status] : "Unknown";
  const what = request.lines.map((line) => `${line.qty_base} of ${line.description === "" ? line.product_id : line.description}`).join(", ");
  return html`<li class="line" data-request-no="${request.request_no}">
    <span class="linehead"><span class="mono name">${request.request_no}</span><span class="pchip pchip-${tone}" data-return-status="${request.status}">${words}</span></span>
    <span class="dim">${returnReasonWords(request.reason_code)} · ${what}</span>
    ${request.status === "APPROVED"
      ? html`<span class="dim">Credited to your account${request.fee_cents > 0 ? ` (less ${money(request.fee_cents)} restocking fee)` : ""}${request.return_no === undefined ? "" : ` as ${request.return_no}`}</span>`
      : nothing}
    ${request.status === "REJECTED" && request.decision_reason !== undefined && request.decision_reason !== "" ? html`<span class="dim">Declined: ${request.decision_reason}</span>` : nothing}
  </li>`;
}

// The options stay literal markup: the position gate refuses a bound value
// outside the data and aria namespaces, and a select reads its own option's
// value, so the stored reason codes are written out here as constants.
function returnForm(eligibility: TenantReturnEligibility, form: ReturnFormState): TemplateResult {
  const open = eligibility.lines.filter((line) => line.qty_sold - line.qty_returned > 0);
  if (open.length === 0) {
    return html`<p class="dim">Everything returnable on this order has already gone back.</p>`;
  }
  return html`<div class="form" data-testid="return-form">
    ${open.map(
      (line) => html`<div class="formrow">
        <span class="returnline">${returnLineWords(line)}</span>
        <input class="control qty" type="number" min="0" inputmode="numeric" aria-label="How many of ${line.description} to return" data-field="return-qty" data-line-id="${line.order_line_id}" data-testid="return-qty" />
      </div>`,
    )}
    <label>
      Why is it going back
      <select class="control" data-field="return-reason" data-testid="return-reason" aria-label="Why is it going back">
        <option value="DAMAGED">Arrived damaged</option>
        <option value="DEFECT">Faulty</option>
        <option value="WRONG_ITEM">Wrong item</option>
        <option value="NO_LONGER_NEEDED">No longer needed</option>
        <option value="OTHER">Something else</option>
      </select>
    </label>
    <label>
      Anything the dealer should know
      <textarea class="control" data-field="return-note" rows="2"></textarea>
    </label>
    ${form.error === "" ? nothing : html`<p class="fault" role="alert" data-testid="return-error">${form.error}</p>`}
    <div class="formrow">
      ${button({ label: "Send request", action: "submit-return", disabled: form.busy })}
      ${button({ label: "Cancel", kind: "secondary", action: "cancel-return", disabled: form.busy })}
    </div>
  </div>`;
}
