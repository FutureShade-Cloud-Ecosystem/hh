// The Board surface: the contractor's chain in four stage columns, the kit's
// projection (stageOfProject, stageOfQuote, stageOfOrder, pinned to the
// contract's state map) over the three lists the core serves. Plan holds the
// planning projects; Quote holds the dealer's draft and sent quotes; Order and
// Invoice hold the dealer's orders. A project that has moved past planning has
// no stage of its own; the quote or order that carries it shows instead. The
// line above the columns counts what is in motion and never sums money: money
// arithmetic is the core's, never the page's. Visual references: the ref's
// BoardPage (the stage board, New plan) and the original portal's procurement
// lanes (portal-procurement.ts); the stage chips are the kit's.
import { html, nothing, type TemplateResult } from "lit";
import { tenantOrders, tenantProcurement, tenantQuotes, type TenantOrderPage } from "@hardscapehouse/api-client";
import type { TenantOrder, TenantProject, TenantProjectBoard, TenantQuote, TenantQuotePage } from "@hardscapehouse/api-client";
import {
  money,
  stageChip,
  stageOfOrder,
  stageOfProject,
  stageOfQuote,
  statusChip,
  trackingChip,
  trackingOf,
  calendarDay,
} from "@hardscapehouse/pro-kit";
import { confirmModal, metaLine, type Page, type SurfaceAnswer } from "./views.js";
import { openReorder, reorderCommand } from "./reorder.js";

export const BOARD_EMPTY = "Nothing is in motion. Start a plan from the catalog.";

export interface BoardData {
  projects: TenantProject[];
  quotes: TenantQuote[];
  orders: TenantOrder[];
}

const ORDERED: readonly string[] = ["CONFIRMED", "PICKING", "READY", "PARTIALLY_FULFILLED", "FULFILLED"];

export async function boardLoad(page: Page): Promise<SurfaceAnswer> {
  // The gate before the wire: a role without the surface's capability makes no call at all.
  if (!page.session.can("submit_rfq")) return { kind: "refusal" };
  const [projects, quotes, orders] = await Promise.all([
    tenantProcurement.tenantListProcurementProjects(page.client),
    tenantQuotes.tenantListQuotes(page.client),
    tenantOrders.tenantListOrders(page.client),
  ]);
  for (const answer of [projects, quotes, orders]) {
    if (answer.ok) continue;
    if (answer.status === 403) return { kind: "refusal" };
    return { kind: "error", status: answer.status };
  }
  const data: BoardData = {
    projects: (projects as { ok: true; value: TenantProjectBoard }).value.projects,
    quotes: (quotes as { ok: true; value: TenantQuotePage }).value.items,
    orders: (orders as { ok: true; value: TenantOrderPage }).value.items,
  };
  if (data.projects.length === 0 && data.quotes.length === 0 && data.orders.length === 0) return { kind: "empty" };
  return { kind: "ready", data };
}

function projectCard(project: TenantProject): TemplateResult {
  const priced = project.status !== "PLANNING";
  return html`<button type="button" class="card" data-action="open-scope" data-project="${project.id}" data-from="board" data-testid="board-project">
    <span class="rowline"><span class="name figure">${project.name}</span></span>
    <span class="rowline">${statusChip("project", project.status)}<span class="grow"></span><span class="mono">${project.line_count} lines</span></span>
    <span class="rowline"><span class="dim">${priced ? "Indicative" : "Awaiting price"}</span><span class="grow"></span><span class="mono figure">${priced ? money(project.subtotal_cents) : money(null)}</span></span>
  </button>`;
}

function quoteCard(quote: TenantQuote): TemplateResult {
  return html`<button type="button" class="card" data-action="open-quote" data-quote="${quote.id}" data-testid="board-quote">
    <span class="rowline"><span class="mono figure">${quote.quote_no}</span><span class="grow"></span><span class="mono figure">${money(quote.total_cents)}</span></span>
    <span class="rowline">${statusChip("quote", quote.status)}<span class="grow"></span><span class="dim">Valid until ${calendarDay(quote.valid_until)}</span></span>
  </button>`;
}

function orderCard(order: TenantOrder, page: Page): TemplateResult {
  const tracking = trackingOf(order.status, order.fulfillment);
  const mayBuy = page.session.can("view_orders_deliveries");
  return html`<div class="card" data-testid="board-order">
    <span class="rowline"><span class="mono figure">${order.order_no}</span><span class="grow"></span><span class="mono figure">${money(order.total_cents)}</span></span>
    <span class="rowline">${tracking === null ? statusChip("order", order.status) : trackingChip(tracking)}<span class="grow"></span><span class="dim">Wanted ${calendarDay(order.requested_date)}</span></span>
    ${mayBuy
      ? html`<span class="rowline line-actions">
          <button type="button" class="btn btn-secondary" data-action="open-reorder" data-order="${order.id}" data-no="${order.order_no}" data-testid="buy-again">Buy again</button>
        </span>`
      : nothing}
  </div>`;
}

function column(stage: "plan" | "quote" | "order" | "invoice", cards: TemplateResult[]): TemplateResult {
  return html`<section class="lane" data-stage="${stage}" aria-label="${stage}">
    ${stageChip(stage)}
    <ul class="lane-body">${cards.length === 0 ? html`<p class="lane-empty dim">Nothing here</p>` : cards.map((card) => html`<li>${card}</li>`)}</ul>
  </section>`;
}

export function boardView(dataRaw: unknown, page: Page): TemplateResult {
  const data = dataRaw as BoardData;
  const planProjects = data.projects.filter((p) => stageOfProject(p.status) === "plan");
  const draftOrders = data.orders.filter((o) => stageOfOrder(o.status) === "plan");
  const quoteCards = data.quotes.filter((q) => stageOfQuote(q.status) === "quote");
  const orderCards = data.orders.filter((o) => ORDERED.includes(o.status));
  const invoiceCards = data.orders.filter((o) => o.status === "INVOICED" || o.status === "CLOSED");
  const mayPlan = page.session.can("submit_rfq");
  return html`<p class="meta">
      <span class="figure">${planProjects.length + draftOrders.length} plans</span>
      <span class="sep" aria-hidden="true">·</span>
      <span class="figure">${quoteCards.length} quotes</span>
      <span class="sep" aria-hidden="true">·</span>
      <span class="figure">${orderCards.length} open orders</span>
    </p>
    ${mayPlan ? html`<div class="actions"><button type="button" class="btn btn-primary" data-action="new-plan" data-testid="new-plan">New plan</button></div>` : nothing}
    <div class="board-columns">
      ${column(
        "plan",
        planProjects.map((p) => projectCard(p)).concat(draftOrders.map((o) => orderCard(o, page))),
      )}
      ${column("quote", quoteCards.map((q) => quoteCard(q)))}
      ${column("order", orderCards.map((o) => orderCard(o, page)))}
      ${column("invoice", invoiceCards.map((o) => orderCard(o, page)))}
    </div>`;
}

// ---- the New plan modal -------------------------------------------------------------------------

// The modal's own state, per open, keyed by the page that opened it: the build
// closure reads it, the create action writes it, and two app roots never share one.
interface PlanModalState {
  error: string;
  busy: boolean;
}

const planStates = new WeakMap<Page, PlanModalState>();

function newPlanModal(state: PlanModalState): TemplateResult {
  return confirmModal({
    heading: "New plan",
    body: html`<label class="field">Project name
        <input class="control" data-control="text" data-field="plan-name" data-testid="plan-name" type="text" />
      </label>
      ${state.error === "" ? nothing : html`<p class="error" role="alert" data-testid="plan-error">${state.error}</p>`}`,
    confirmLabel: "Create",
    confirmAction: "create-plan",
    busy: state.busy,
    testid: "plan-modal",
  });
}

export function boardCommand(action: string, control: Element, page: Page): boolean {
  if (action === "open-reorder") {
    openReorder(page, control.getAttribute("data-order") ?? "", control.getAttribute("data-no") ?? "");
    return true;
  }
  if (reorderCommand(action, control, page) === true) return true;
  if (action === "new-plan") {
    planStates.set(page, { error: "", busy: false });
    page.openModal("plan-modal", () => newPlanModal(planStates.get(page) as PlanModalState));
    return true;
  }
  if (action === "create-plan") {
    void createPlan(page);
    return true;
  }
  return false;
}

async function createPlan(page: Page): Promise<void> {
  const state = planStates.get(page);
  if (state === undefined || state.busy) return;
  const name = (page.field("plan-name")?.value ?? "").trim();
  if (name === "") {
    state.error = "Give the plan a name first.";
    page.touch();
    return;
  }
  state.busy = true;
  page.touch();
  const answer = await tenantProcurement.tenantCreateProcurementProject(page.client, { name });
  state.busy = false;
  if (answer.ok) {
    page.closeModal();
    page.goto({ surface: "scope", projectId: answer.value.id, from: "board" });
  } else {
    state.error = answer.error;
    page.touch();
  }
}
