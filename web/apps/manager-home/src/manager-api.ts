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
  staffIdentity,
  staffPurchasing,
  staffTasks,
  type IdentityPolicyType,
  type Result,
} from "@hardscapehouse/api-client";

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
}
