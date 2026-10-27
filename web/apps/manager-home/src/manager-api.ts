// Manager Home's one door to the core: the manager's reads and the two
// writes the landing owns (resolve a shortage, decide an approval), over the
// generated api-client, answering a Result and never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the landing's screens use. A field the
// projection does not name is dropped at this door and never reaches a
// screen. Money stays integer cents end to end: the core computes it, this
// door carries it, and only the view formats it for a person. Nothing here
// sums, converts or derives a figure.
//
// Port provenance: the widget shapes and their wire contracts are the
// original app's home bento (repository hardscapeos_dibbits, read only; see
// PROVENANCE.md), rebuilt on HH's generated client and narrowed to the
// read-only landing this first slice builds.
import {
  ApiClient,
  staffBranches,
  staffDashboard,
  staffDispatch,
  staffIdentity,
  staffOrder,
  staffPurchasing,
  staffTasks,
  type IdentityPolicyType,
  type Result,
} from "@hardscapehouse/api-client";
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";
import { toDispatchBoard, type DispatchBoardView } from "./drivers-api.js";

// -- the needs-you queue --------------------------------------------------------------------------

export interface NeedsRow {
  readonly source_key: string;
  readonly what: string;
  readonly count: number;
  readonly age: string;
  readonly severity: string;
  readonly module: string;
  readonly unit: string;
  readonly screen: string;
  readonly view: string;
  readonly action: string;
}

export interface NeedsQueue {
  readonly profile: string;
  readonly rows: readonly NeedsRow[];
  readonly total: number;
  readonly worst: string;
}

// -- the widget bento ------------------------------------------------------------------------------

export interface WidgetView {
  readonly widget_key: string;
  readonly shape: string;
  readonly measure: string;
  readonly split: string;
  readonly range: string;
  readonly size: string;
  readonly title: string;
  readonly position: number;
  readonly visible: boolean;
  readonly label: string;
  readonly kind: string;
  readonly module: string;
  readonly span_cols: number;
  readonly span_rows: number;
}

export interface WidgetLayout {
  readonly profile: string;
  readonly source: string;
  readonly widgets: readonly WidgetView[];
}

export interface WidgetPoint {
  readonly label: string;
  readonly number: number;
  readonly tone: string;
}

export interface WidgetDatum {
  readonly key: string;
  readonly state: string;
  readonly total: number | null;
  readonly points: readonly WidgetPoint[];
  readonly note: string;
  readonly range: string;
}

// -- the yard's shortages ----------------------------------------------------------------------------

export interface Shortage {
  readonly id: string;
  readonly task_id: string;
  readonly order_line_no: number | null;
  readonly sku: string;
  readonly description: string;
  readonly qty_short: string;
  readonly uom: string;
  readonly note: string;
  readonly reported_at: string;
  readonly resolved_at: string | null;
  readonly resolution: string;
}

export interface QueueTask {
  readonly id: string;
  readonly order_id: string;
  readonly task_type: string;
  readonly status: string;
  readonly priority: string;
  readonly branch_id: string;
  readonly open_shortages: number;
  readonly total_shortages: number;
  readonly stage_location: string;
  readonly stage_ref: string;
  readonly created_at: string;
  readonly accept_deadline_at: string | null;
}

/** One line of the pick's ticket: what the order asked for, the resolve panel's ordered figure. */
export interface TicketLine {
  readonly line_no: number;
  readonly product_id: string;
  readonly description: string;
  readonly qty_ordered: string;
  readonly uom: string;
}

export interface QueueEntry {
  readonly task: QueueTask;
  /** The ticket's order number, when the pick source carries one; empty says "not on the ticket". */
  readonly order_no: string;
  readonly customer_name: string;
  readonly lines: readonly TicketLine[];
  readonly shortages: readonly Shortage[];
}

// -- receiving ---------------------------------------------------------------------------------------

export interface ReceivingRow {
  readonly po_id: string;
  readonly po_no: string;
  readonly vendor_name: string;
  readonly branch_id: string;
  readonly status: string;
  readonly expected_at: string;
  readonly open_lines: number;
  readonly qty_outstanding_base: number;
  /** The order's still-open value, integer cents. Absent when the cost gate refuses the caller. */
  readonly value_open_cents: number | null;
}

// -- approvals ---------------------------------------------------------------------------------------

export interface ApprovalRow {
  readonly id: string;
  readonly branch_id: string;
  readonly policy_type: string;
  readonly detail: string;
  readonly subject_ref: string;
  readonly amount_cents: number;
  readonly status: string;
}

// -- change orders ---------------------------------------------------------------------------------

export interface ChangeOrderQueueRow {
  readonly id: string;
  readonly change_no: string;
  readonly order_id: string;
  readonly order_no: string;
  readonly branch_id: string;
  readonly status: string;
  readonly reason: string;
  readonly delta_cents: number;
  readonly finance_threshold_cents: number;
  readonly finance_required: boolean;
  readonly requested_by_role: string;
  readonly requested_at: string;
  readonly yard_ack_note: string;
  readonly next_actions: readonly string[];
}

export interface ChangeOrderLine {
  readonly seq: number;
  readonly op: string;
  readonly line_no: number | null;
  readonly product_id: string;
  readonly product_name: string;
  readonly description: string;
  readonly qty_base_before: number;
  readonly qty_base_after: number;
  readonly unit_price_cents: number;
  readonly line_delta_cents: number;
}

export interface ChangeOrder {
  readonly id: string;
  readonly change_no: string;
  readonly order_no: string;
  readonly order_id: string;
  readonly status: string;
  readonly reason: string;
  readonly delta_cents: number;
  readonly finance_threshold_cents: number;
  readonly finance_required: boolean;
  readonly requested_by_role: string;
  readonly yard_ack_note: string;
  readonly lines: readonly ChangeOrderLine[];
  readonly next_actions: readonly string[];
}

// -- the caller's branch ---------------------------------------------------------------------------

export interface BranchRef {
  readonly id: string;
  readonly code: string;
  readonly name: string;
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);
const maybeNum = (value: unknown): number | null => (typeof value === "number" && Number.isFinite(value) ? value : null);
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);

export function toNeedsRow(raw: unknown): NeedsRow {
  const r = bag(raw);
  return {
    source_key: text(r.source_key),
    what: text(r.what),
    count: num(r.count),
    age: text(r.age),
    severity: text(r.severity),
    module: text(r.module),
    unit: text(r.unit),
    screen: text(r.screen),
    view: text(r.view),
    action: text(r.action),
  };
}

export function toNeedsQueue(raw: unknown): NeedsQueue {
  const r = bag(raw);
  return {
    profile: text(r.profile),
    rows: list(r.rows).map(toNeedsRow),
    total: num(r.total),
    worst: text(r.worst),
  };
}

export function toWidgetView(raw: unknown): WidgetView {
  const r = bag(raw);
  return {
    widget_key: text(r.widget_key),
    shape: text(r.shape),
    measure: text(r.measure),
    split: text(r.split),
    range: text(r.range),
    size: text(r.size),
    title: text(r.title),
    position: num(r.position),
    visible: r.visible === true,
    label: text(r.label),
    kind: text(r.kind),
    module: text(r.module),
    span_cols: num(r.span_cols),
    span_rows: num(r.span_rows),
  };
}

export function toWidgetLayout(raw: unknown): WidgetLayout {
  const r = bag(raw);
  return {
    profile: text(r.profile),
    source: text(r.source),
    widgets: list(r.widgets).map(toWidgetView),
  };
}

export function toWidgetDatum(raw: unknown): WidgetDatum {
  const r = bag(raw);
  return {
    key: text(r.widget_key),
    state: text(r.state),
    total: maybeNum(r.total),
    points: list(r.points).map((p) => {
      const b = bag(p);
      return { label: text(b.label), number: num(b.number), tone: text(b.tone) };
    }),
    note: text(r.note),
    range: text(r.range),
  };
}

export function toShortage(raw: unknown): Shortage {
  const r = bag(raw);
  return {
    id: text(r.id),
    task_id: text(r.task_id),
    order_line_no: maybeNum(r.order_line_no),
    sku: text(r.sku),
    description: text(r.description),
    qty_short: text(r.qty_short),
    uom: text(r.uom),
    note: text(r.note),
    reported_at: text(r.reported_at),
    resolved_at: r.resolved_at == null ? null : text(r.resolved_at),
    resolution: text(r.resolution),
  };
}

export function toQueueTask(raw: unknown): QueueTask {
  const r = bag(raw);
  return {
    id: text(r.id),
    order_id: text(r.order_id),
    task_type: text(r.task_type),
    status: text(r.status),
    priority: text(r.priority),
    branch_id: text(r.branch_id),
    open_shortages: num(r.open_shortages),
    total_shortages: num(r.total_shortages),
    stage_location: text(r.stage_location),
    stage_ref: text(r.stage_ref),
    created_at: text(r.created_at),
    accept_deadline_at: r.accept_deadline_at == null ? null : text(r.accept_deadline_at),
  };
}

export function toQueueEntry(raw: unknown): QueueEntry {
  const r = bag(raw);
  const ticket = bag(r.ticket);
  return {
    task: toQueueTask(r.task),
    order_no: text(ticket.order_no),
    customer_name: text(ticket.customer_name),
    lines: list(ticket.lines).map((l) => {
      const b = bag(l);
      return {
        line_no: num(b.line_no),
        product_id: text(b.product_id),
        description: text(b.description),
        qty_ordered: text(b.qty_ordered),
        uom: text(b.uom),
      };
    }),
    shortages: list(r.shortages).map(toShortage),
  };
}

export function toReceivingRow(raw: unknown): ReceivingRow {
  const r = bag(raw);
  return {
    po_id: text(r.po_id),
    po_no: text(r.po_no),
    vendor_name: text(r.vendor_name),
    branch_id: text(r.branch_id),
    status: text(r.status),
    expected_at: text(r.expected_at),
    open_lines: num(r.open_lines),
    qty_outstanding_base: num(r.qty_outstanding_base),
    value_open_cents: maybeNum(r.value_open_cents),
  };
}

export function toApprovalRow(raw: unknown): ApprovalRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    branch_id: text(r.branch_id),
    policy_type: text(r.policy_type),
    detail: text(r.detail),
    subject_ref: text(r.subject_ref),
    amount_cents: num(r.amount_cents),
    status: text(r.status),
  };
}

export function toChangeOrderQueueRow(raw: unknown): ChangeOrderQueueRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    change_no: text(r.change_no),
    order_id: text(r.order_id),
    order_no: text(r.order_no),
    branch_id: text(r.branch_id),
    status: text(r.status),
    reason: text(r.reason),
    delta_cents: num(r.delta_cents),
    finance_threshold_cents: num(r.finance_threshold_cents),
    finance_required: r.finance_required === true,
    requested_by_role: text(r.requested_by_role),
    requested_at: text(r.requested_at),
    yard_ack_note: text(r.yard_ack_note),
    next_actions: list(r.next_actions).map(text),
  };
}

export function toChangeOrderLine(raw: unknown): ChangeOrderLine {
  const r = bag(raw);
  return {
    seq: num(r.seq),
    op: text(r.op),
    line_no: maybeNum(r.line_no),
    product_id: text(r.product_id),
    product_name: text(r.product_name),
    description: text(r.description),
    qty_base_before: num(r.qty_base_before),
    qty_base_after: num(r.qty_base_after),
    unit_price_cents: num(r.unit_price_cents),
    line_delta_cents: num(r.line_delta_cents),
  };
}

export function toChangeOrder(raw: unknown): ChangeOrder {
  const r = bag(raw);
  return {
    id: text(r.id),
    change_no: text(r.change_no),
    order_no: text(r.order_no),
    order_id: text(r.order_id),
    status: text(r.status),
    reason: text(r.reason),
    delta_cents: num(r.delta_cents),
    finance_threshold_cents: num(r.finance_threshold_cents),
    finance_required: r.finance_required === true,
    requested_by_role: text(r.requested_by_role),
    yard_ack_note: text(r.yard_ack_note),
    lines: list(r.lines).map(toChangeOrderLine),
    next_actions: list(r.next_actions).map(text),
  };
}

export function toBranchRef(raw: unknown): BranchRef {
  const r = bag(raw);
  return { id: text(r.id), code: text(r.code), name: text(r.name) };
}

/** Every call the landing's screens make. */
export class ManagerApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The caller's needs-you queue, counted server side. */
  async needs(): Promise<Result<NeedsQueue>> {
    const answer = await staffDashboard.dashboardGetNeeds(this.#client);
    return answer.ok ? { ok: true, value: toNeedsQueue(answer.value) } : answer;
  }

  /** The caller's resolved widget bento: the profile's widgets in the profile's order. */
  async widgets(): Promise<Result<WidgetLayout>> {
    const answer = await staffDashboard.dashboardGetWidgets(this.#client);
    return answer.ok ? { ok: true, value: toWidgetLayout(answer.value) } : answer;
  }

  /** The numbers behind the caller's own widgets. The keys select; they never describe. */
  async widgetData(keys: readonly string[]): Promise<Result<readonly WidgetDatum[]>> {
    const answer = await staffDashboard.dashboardGetWidgetData(this.#client, { key: [...keys] });
    if (!answer.ok) return answer;
    const data = bag(answer.value);
    return { ok: true, value: list(data.data).map(toWidgetDatum) };
  }

  /** The branches this login reaches, read through the fenced branches route. */
  async branches(): Promise<Result<readonly BranchRef[]>> {
    const answer = await staffBranches.branchesList(this.#client);
    if (!answer.ok) return answer;
    return { ok: true, value: list(answer.value).map(toBranchRef) };
  }

  /**
   * The yard board for one branch. The branch comes from the caller's own grants (the branches
   * route), never from a person's typing, and the enumeration route refuses a branch-scoped
   * caller who names one they cannot reach.
   */
  async pickQueue(branchId: string): Promise<Result<readonly QueueEntry[]>> {
    const answer = await staffTasks.yardListPickQueue(this.#client, { branch_id: branchId, limit: 50 });
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return { ok: true, value: list(page.items).map(toQueueEntry) };
  }

  /** One pick with its full shortage history, resolved included. */
  async pick(id: string): Promise<Result<QueueEntry>> {
    const answer = await staffTasks.yardGetPick(this.#client, id);
    return answer.ok ? { ok: true, value: toQueueEntry(answer.value) } : answer;
  }

  /** Resolve one shortage with a stated resolution. A refusal arrives in the core's words. */
  async resolveShortage(taskId: string, shortageId: string, resolution: string): Promise<Result<Shortage>> {
    const answer = await staffTasks.yardResolveShortage(this.#client, taskId, shortageId, { resolution });
    return answer.ok ? { ok: true, value: toShortage(answer.value) } : answer;
  }

  /** The receiving worklist for one branch: what has been ordered and what has landed. */
  async receivingWorklist(branchId: string): Promise<Result<readonly ReceivingRow[]>> {
    const answer = await staffPurchasing.purchasingListReceivingWorklist(this.#client, { branch_id: branchId, limit: 50 });
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return { ok: true, value: list(page.items).map(toReceivingRow) };
  }

  /**
   * The approvals list, narrowed to one policy kind the caller decides. An empty kind is the
   * whole list; a kind the caller does not decide is refused in the core's own words.
   * The opening read asks for the waiting ones only.
   */
  async approvals(kind: string, pendingOnly = false): Promise<Result<readonly ApprovalRow[]>> {
    const opts = {
      ...(kind === "" ? {} : { kind: kind as IdentityPolicyType }),
      ...(pendingOnly ? { status: "PENDING" as const } : {}),
    };
    const answer = await staffIdentity.identityListApprovals(this.#client, opts);
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return { ok: true, value: list(page.items).map(toApprovalRow) };
  }

  /** Decide one approval. The server owns four-eyes and every authority rule. */
  async decideApproval(id: string, approve: boolean, reason: string): Promise<Result<true>> {
    const answer = await staffIdentity.identityDecideApproval(this.#client, id, { approve, reason });
    return answer.ok ? { ok: true, value: true } : answer;
  }

  /** Change orders waiting on this caller: PENDING_FINANCE and APPROVED only. */
  async changeOrders(status: "PENDING_FINANCE" | "APPROVED"): Promise<Result<readonly ChangeOrderQueueRow[]>> {
    const answer = await staffOrder.orderListChangeQueue(this.#client, { status });
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return { ok: true, value: list(page.items).map(toChangeOrderQueueRow) };
  }

  /** The full change order with its lines. */
  async changeOrder(id: string): Promise<Result<ChangeOrder>> {
    const answer = await staffOrder.orderGetChange(this.#client, id);
    return answer.ok ? { ok: true, value: toChangeOrder(answer.value) } : answer;
  }

  /** Sign when needed and apply in one tap. The key is generated once per press and reused on retry. */
  async changeOrderSignApply(id: string, idempotencyKey: string): Promise<Result<ChangeOrder>> {
    const answer = await staffOrder.orderChangeSignApply(this.#client, id, {}, { idempotencyKey });
    return answer.ok ? { ok: true, value: toChangeOrder(answer.value) } : answer;
  }

  /** Decline a change order with a note. */
  async changeOrderReject(id: string, note: string): Promise<Result<ChangeOrder>> {
    const answer = await staffOrder.orderChangeReject(this.#client, id, { note });
    return answer.ok ? { ok: true, value: toChangeOrder(answer.value) } : answer;
  }

  // -- WALK-C10 / FW1-14: the TODAY cards, every figure the core returned ------------------------

  /** The four TODAY money KPI cards, measured by the core's own viz series. Branch rows carry the
   * caller's own branch names, read through the same fenced branches route every other view uses. */
  async readLandingKpis(): Promise<Result<readonly LandingKpi[]>> {
    const [answer, names] = await Promise.all([
      staffDashboard.dashboardGetViz(this.#client, { tile: [...LANDING_VIZ_TILES] }),
      this.branches(),
    ]);
    if (!answer.ok) return answer;
    const nameByCode = new Map(names.ok ? names.value.map((b) => [b.code, b.name]) : []);
    return { ok: true, value: landingKpisFrom(toLandingTiles(answer.value), nameByCode) };
  }

  /** The DELIVERIES TODAY card, counted off each branch's own dispatch board. */
  async readLandingDeliveries(branchId: string): Promise<Result<LandingDeliveries | null>> {
    const all = await this.branches();
    if (!all.ok) return all;
    const scope = branchId === "" ? all.value : all.value.filter((b) => b.id === branchId);
    const boards: DispatchBoardView[] = [];
    for (const branch of scope) {
      const answer = await staffDispatch.dispatchGetBoard(this.#client, { branch_id: branch.id });
      if (answer.ok) boards.push(toDispatchBoard(answer.value));
    }
    return { ok: true, value: landingDeliveriesFrom(scope, boards) };
  }

  /** The NEEDS YOU, BY MONEY AT STAKE card: the caller's pending approvals, ranked by amount. */
  async readLandingNeeds(): Promise<Result<readonly LandingNeedRow[]>> {
    const answer = await this.approvals("", true);
    if (!answer.ok) return answer;
    return { ok: true, value: landingNeedsFrom(answer.value) };
  }
}

// -- FW1-14: the TODAY cards, built from the core's own reads ---------------------------------------

/** What a card the core does not answer says: the words, never a figure. */
export const LANDING_NOT_CONNECTED = "Not connected yet";

/** One composed series the core measured, narrowed to what the cards read. */
export interface LandingVizTile {
  readonly tile_id: string;
  readonly state: string;
  readonly total: number | null;
  readonly points: readonly { readonly label: string; readonly number: number }[];
}

function toLandingTiles(raw: unknown): readonly LandingVizTile[] {
  return list(bag(raw).tiles).map((tile) => {
    const b = bag(tile);
    return {
      tile_id: text(b.tile_id),
      state: text(b.state),
      total: maybeNum(b.total),
      points: list(b.points).map((p) => ({ label: text(bag(p).label), number: num(bag(p).number) })),
    };
  });
}

/** The composed ids the landing asks for: today's counter sales by branch, the all time margin in
 * basis points, and the AR aging buckets. Every id here must compose clean through the core's
 * ParseComposition (the dashboard package's landing tile contract leg pins that): an id the caller
 * may not compose is byte absent in the answer and its card says so in words. */
const LANDING_VIZ_TILES = [
  "viz.sales.today-branch-split",
  "viz.margin.gross_bps-branch-list",
  "viz.ar.aging-bucket-split",
  "viz.ar.aging-branch-split",
] as const;

function landingMoney(cents: number): string {
  return formatDollars(cents);
}

function landingPercentFromBps(bps: number): string {
  return `${(bps / 100).toFixed(1)}%`;
}

function landingNotConnected(title: string, kind: "money" | "percent"): LandingKpi {
  return { title, kind, connected: false, value: "", note: "", branches: [] };
}

/** The four money KPI cards from the core's viz answer. A measure that did not come back, or came
 * back in any state but ok, is a card with its title and LANDING_NOT_CONNECTED, never a figure.
 *
 * FROM-PRINCIPAL 132: a RATE is never summed and never averaged. The all branches gross margin is
 * the provider's own weighted aggregate, the branch composition's total (the core's totalFor asks
 * the provider's ungrouped run: total margin cents over total sales cents across the branches;
 * FROM-PRINCIPAL 174: there is no none composition, ParseComposition refuses it, the metric
 * composes branch only). Branch points with no aggregate is a Not connected yet card. Branch rows
 * name the branches (the caller's own branch list), never the three letter codes the series
 * carries. */
export function landingKpisFrom(
  tiles: readonly LandingVizTile[],
  branchNames: ReadonlyMap<string, string> = new Map(),
): readonly LandingKpi[] {
  const byId = new Map(tiles.map((t) => [t.tile_id, t]));
  const measured = (id: string): LandingVizTile | null => {
    const tile = byId.get(id);
    return tile !== undefined && tile.state === "ok" ? tile : null;
  };
  const branchLabel = (code: string): string => branchNames.get(code) ?? code;
  const branchRows = (tile: LandingVizTile, show: (n: number) => string) =>
    tile.points.map((p) => ({ label: branchLabel(p.label), value: show(p.number) }));

  const sales = measured("viz.sales.today-branch-split");
  const marginByBranch = measured("viz.margin.gross_bps-branch-list");
  const aging = measured("viz.ar.aging-bucket-split");
  const agingByBranch = measured("viz.ar.aging-branch-split");

  const cards: LandingKpi[] = [];
  cards.push(sales !== null
    ? {
        title: "Sales today", kind: "money", connected: true,
        value: landingMoney(sales.total ?? sales.points.reduce((a, p) => a + p.number, 0)),
        note: "Counter sales committed today, including tax",
        branches: branchRows(sales, landingMoney),
      }
    : landingNotConnected("Sales today", "money"));
  // Money adds; a rate does not. The combined margin is the core's weighted aggregate alone.
  cards.push(marginByBranch !== null && marginByBranch.total !== null
    ? {
        title: "Gross margin", kind: "percent", connected: true,
        value: landingPercentFromBps(marginByBranch.total), note: "All invoiced order lines, all time",
        branches: branchRows(marginByBranch, landingPercentFromBps),
      }
    : landingNotConnected("Gross margin", "percent"));
  cards.push(aging !== null
    ? {
        title: "Receivables past due", kind: "money", connected: true,
        value: landingMoney(aging.points.filter((p) => p.label !== "Current").reduce((a, p) => a + p.number, 0)),
        note: "Open invoices past their due day",
        branches: agingByBranch !== null ? branchRows(agingByBranch, landingMoney) : [],
      }
    : landingNotConnected("Receivables past due", "money"));
  // Cash collected today: no core measure ships for it, so the card says so in words.
  cards.push(landingNotConnected("Cash collected today", "money"));
  return cards;
}

/** The DELIVERIES TODAY card from the branches' own boards: the day's stops, the words of their
 * states, and the runs themselves. No board read says null and the card does not draw. */
export function landingDeliveriesFrom(
  branches: readonly BranchRef[],
  boards: readonly DispatchBoardView[],
): LandingDeliveries | null {
  if (boards.length === 0) return null;
  let delivered = 0;
  let onRoad = 0;
  let toLeave = 0;
  const runs: LandingDeliveries["runs"][number][] = [];
  const byBranch: { label: string; count: number }[] = [];
  for (const board of boards) {
    // A cancelled stop is not a stop the day holds: the core's own deliveries.today measure
    // excludes CANCELLED, and the card counts the same board the same way.
    const stops = board.stops.filter((s) => s.status !== "CANCELLED");
    for (const stop of stops) {
      if (stop.delivered_at !== null) delivered += 1;
      else if (stop.en_route_at !== null) onRoad += 1;
      else toLeave += 1;
    }
    byBranch.push({
      label: branches.find((b) => b.id === board.branch_id)?.name ?? board.branch_id,
      count: stops.length,
    });
    for (const run of board.runs) {
      const done = board.stops.filter((s) => s.run_id === run.id && s.delivered_at !== null).length;
      runs.push({
        runId: run.run_no,
        branch: branches.find((b) => b.id === board.branch_id)?.name ?? board.branch_id,
        when: run.dispatched_at !== null ? `left ${landingClockWords(run.dispatched_at)}` : "not left yet",
        stops: `${done} of ${run.stop_count} ${run.stop_count === 1 ? "stop" : "stops"} done`,
      });
    }
  }
  const count = delivered + onRoad + toLeave;
  const fromWords = byBranch.map((b) => `${b.count} from ${b.label}`).join(", ");
  return {
    count,
    branches: byBranch,
    summary: `${fromWords}. ${delivered} done, ${onRoad} on the road, ${toLeave} still to leave.`,
    runs,
  };
}

function landingClockWords(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  return at.toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit" });
}

/** The approval kinds' words, one per policy type the core decides. */
const LANDING_NEED_KIND_WORDS: Readonly<Record<string, string>> = Object.freeze({
  MARGIN_BELOW_MINIMUM: "Margin floor",
  CREDIT_LIMIT_EXCEEDED: "Credit limit",
  COD_CONSTRAINT: "Cash On Demand",
  JE_OVER_THRESHOLD: "Journal over threshold",
  FREIGHT_ABOVE_QUOTE: "Freight above the quote",
});

/** The needs card from the caller's pending approvals: ranked by the money at stake, each row
 * opening the approvals view where the decision itself lives. */
export function landingNeedsFrom(rows: readonly ApprovalRow[]): readonly LandingNeedRow[] {
  return rows
    .filter((row) => row.status === "PENDING")
    .sort((a, b) => b.amount_cents - a.amount_cents)
    .map((row) => {
      const kindWord = LANDING_NEED_KIND_WORDS[row.policy_type] ?? "Approval";
      const ref = landingRefOfDetail(row.detail);
      const body = ref === ""
        ? row.detail
        : row.detail.replace(new RegExp(`^(?:Quote|Order|Journal entry|Journal)?\\s*${ref}\\s*`), "").trim();
      return {
        id: row.id,
        title: ref === "" ? kindWord : `${kindWord}: ${ref}`,
        body: body === "" ? row.detail : body.charAt(0).toUpperCase() + body.slice(1),
        amountLabel: row.amount_cents > 0 ? landingMoney(row.amount_cents) : "Not priced",
        action: "open-approvals",
        actionLabel: "Open the request",
        accent: row.policy_type === "MARGIN_BELOW_MINIMUM" ? "margin"
          : row.policy_type === "CREDIT_LIMIT_EXCEEDED" || row.policy_type === "COD_CONSTRAINT" ? "credit"
          : "escalation",
        targetView: "approvals",
        targetRef: row.id,
      };
    });
}

// -- WALK-C10: the landing's read shapes (boards final, change 10) ---------------------------------

/** One of the four money KPI cards the TODAY section carries. */
export interface LandingKpi {
  readonly title: string;
  readonly kind: "money" | "percent";
  /** False when the core did not answer: the card shows its title and the not connected words. */
  readonly connected: boolean;
  readonly value: string;
  /** What the figure covers, in words; the card carries it beside the number. */
  readonly note: string;
  readonly branches: readonly { readonly label: string; readonly value: string }[];
}

/** The DELIVERIES TODAY card. */
export interface LandingDeliveries {
  readonly count: number;
  readonly summary: string;
  readonly branches: readonly { readonly label: string; readonly count: number }[];
  readonly runs: readonly { readonly runId: string; readonly branch: string; readonly when: string; readonly stops: string }[];
}

/** The inline action a NEEDS YOU row's button runs. */
export type LandingNeedAction = "open-approvals";

/** The accent bar the row draws. */
export type LandingNeedAccent = "credit" | "margin" | "escalation";

/** One row on the NEEDS YOU, BY MONEY AT STAKE list. */
export interface LandingNeedRow {
  readonly id: string;
  readonly title: string;
  readonly body: string;
  readonly amountLabel: string;
  readonly action: LandingNeedAction;
  readonly actionLabel: string;
  readonly accent: LandingNeedAccent;
  readonly targetView: "approvals";
  readonly targetRef: string;
}

/** The reference a request's words name ("Quote Q-2002 line 1 ..."), read the same way the
 * approvals view reads it, so a row's title here and there can not disagree. */
function landingRefOfDetail(detail: string): string {
  const m = /\b([A-Z]{1,4}-[A-Z0-9][A-Z0-9-]*)\b/.exec(detail);
  return m ? m[1] : "";
}


