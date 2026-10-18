// Yard Pick's one door to the core: the yard team's reads and writes, over
// the generated tasks client, answering a Result and never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the yard's screens use. A field the
// projection does not name is dropped at this door and never reaches a
// screen, so the no money rule does not rest on the server alone: the app
// could not show a figure if one arrived. Three contract declared money
// shapes pass this door never: the held order's owed figure (the hold's own
// words carry the fact on the ticket this file shapes), the skid answer's
// deposit figures (a skid deposit is the office's money, not the yard's),
// and the change queue's delta figure and threshold (the yard acknowledges
// whether the work can still be done; what the change is worth is not the
// yard's question).
//
// Port provenance: the call set is the original app's app/src/yard-api.ts
// (the original HardscapeOS app, repository hardscapeos_dibbits, commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab), narrowed to the pick slice and
// rebuilt on HH's generated client. The change reads are HH's own (there is
// no change queue in the original), written against the core's order module
// (core/internal/order/change_queue.go) the same way.
import { ApiClient, staffBranches, staffOrder, staffProduct, staffTasks, type Result } from "@hardscapehouse/api-client";

export interface YardBranch {
  readonly id: string;
  readonly code: string;
  readonly name: string;
}

export interface YardLine {
  readonly line_no: number;
  readonly product_id: string;
  readonly sku: string;
  readonly description: string;
  readonly qty_ordered: string;
  /** The part of the ordered quantity held on backorder after a short pick; empty when none. */
  readonly qty_backordered: string;
  readonly uom: string;
  readonly location: string;
}

export interface YardTicket {
  readonly order_id: string;
  readonly order_no: string;
  readonly customer_name: string;
  readonly fulfillment: string;
  readonly mto_class: string;
  readonly priority: string;
  readonly reference: string;
  readonly lines: readonly YardLine[];
  readonly source: string;
  /** A held order waits in the queue marked: the hold is words, never a figure. */
  readonly held_from_picking: boolean;
  readonly hold_words: string;
  readonly run_no?: string;
  readonly run_departs_at?: string;
  readonly load_position?: number;
}

export interface YardTask {
  readonly id: string;
  readonly order_id: string;
  readonly priority: string;
  readonly status: string;
  readonly claimed_by: string;
  readonly created_at: string;
  readonly updated_at: string;
  readonly stage_location: string;
  readonly stage_ref: string;
  readonly open_shortages: number;
  readonly total_shortages: number;
  readonly run_no?: string;
  readonly run_departs_at?: string;
  readonly load_position?: number;
}

export interface YardShortage {
  readonly id: string;
  readonly task_id: string;
  readonly order_line_no: number | null;
  readonly sku: string;
  readonly description: string;
  readonly qty_short: string;
  readonly uom: string;
  readonly note: string;
  readonly reported_at: string;
  readonly resolved_at: string;
  readonly resolved_by: string;
  readonly resolution: string;
}

export interface YardPickedLine {
  readonly id: string;
  readonly task_id: string;
  readonly order_line_no: number;
  readonly sku: string;
  readonly description: string;
  readonly qty_ordered: string;
  readonly qty_picked: string;
  readonly uom: string;
  readonly short_reason: string;
  readonly short_note: string;
  readonly picked_at: string;
}

export interface YardPick {
  readonly task: YardTask;
  readonly ticket: YardTicket | null;
  readonly shortages: readonly YardShortage[];
  readonly picked: readonly YardPickedLine[];
}

/** The skid answer as the yard reads it: the count. The deposit figures stop at this door. */
export interface YardSkidAnswer {
  readonly skid_count: number;
  readonly previous_skid_count: number | null;
  readonly changed: boolean;
  readonly order_no: string;
}

/**
 * One row of the changes waiting on the yard, as the yard reads it: the
 * facts the row answers on and the actions the server offers this caller.
 * The delta figure and the threshold ride the wire for other callers and
 * stop at this door: the yard's question is "can we still do this", and the
 * server's own next_actions decide what this screen may offer.
 */
export interface YardChange {
  readonly id: string;
  readonly change_no: string;
  readonly order_no: string;
  readonly status: string;
  readonly reason: string;
  readonly requested_by_role: string;
  readonly requested_at: string;
  readonly next_actions: readonly string[];
}

/** The acknowledgement's answer, as the yard reads it: the change's new stage. */
export interface YardChangeAnswer {
  readonly id: string;
  readonly change_no: string;
  readonly order_id: string;
  readonly status: string;
}

/** One line of a pick record the yard sends: the whole set in one call, the server's own rule. */
export interface YardRecordLine {
  readonly order_line_no: number;
  readonly product_id: string;
  readonly sku: string;
  readonly description: string;
  readonly qty_ordered: string;
  readonly qty_picked: string;
  readonly uom: string;
  readonly short_reason: string;
  readonly short_note: string;
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);

export function toBranch(raw: unknown): YardBranch {
  const r = bag(raw);
  return { id: text(r.id), code: text(r.code), name: text(r.name) };
}

export function toLine(raw: unknown): YardLine {
  const r = bag(raw);
  return {
    line_no: num(r.line_no),
    product_id: text(r.product_id),
    sku: text(r.sku),
    description: text(r.description),
    qty_ordered: text(r.qty_ordered),
    qty_backordered: text(r.qty_backordered),
    uom: text(r.uom),
    location: text(r.location),
  };
}

function runProps(r: Bag): { run_no?: string; run_departs_at?: string; load_position?: number } {
  const runNo = text(r.run_no).trim();
  const runDepartsAt = text(r.run_departs_at).trim();
  const loadPos = typeof r.load_position === "number" && Number.isInteger(r.load_position) && r.load_position > 0 ? r.load_position : undefined;
  return {
    ...(runNo !== "" ? { run_no: runNo } : {}),
    ...(runDepartsAt !== "" ? { run_departs_at: runDepartsAt } : {}),
    ...(loadPos !== undefined ? { load_position: loadPos } : {}),
  };
}

export function toTicket(raw: unknown): YardTicket | null {
  if (raw === null || raw === undefined) return null;
  const r = bag(raw);
  return {
    order_id: text(r.order_id),
    order_no: text(r.order_no),
    customer_name: text(r.customer_name),
    fulfillment: text(r.fulfillment),
    mto_class: text(r.mto_class),
    priority: text(r.priority),
    reference: text(r.reference),
    lines: Array.isArray(r.lines) ? r.lines.map(toLine) : [],
    source: text(r.source),
    held_from_picking: r.held_from_picking === true,
    hold_words: text(r.hold_words),
    ...runProps(r),
  };
}

export function toTask(raw: unknown): YardTask {
  const r = bag(raw);
  return {
    id: text(r.id),
    order_id: text(r.order_id),
    priority: text(r.priority),
    status: text(r.status),
    claimed_by: text(r.claimed_by),
    created_at: text(r.created_at),
    updated_at: text(r.updated_at),
    stage_location: text(r.stage_location),
    stage_ref: text(r.stage_ref),
    open_shortages: num(r.open_shortages),
    total_shortages: num(r.total_shortages),
    ...runProps(r),
  };
}

export function toShortage(raw: unknown): YardShortage {
  const r = bag(raw);
  return {
    id: text(r.id),
    task_id: text(r.task_id),
    order_line_no: typeof r.order_line_no === "number" && Number.isFinite(r.order_line_no) ? r.order_line_no : null,
    sku: text(r.sku),
    description: text(r.description),
    qty_short: text(r.qty_short),
    uom: text(r.uom),
    note: text(r.note),
    reported_at: text(r.reported_at),
    resolved_at: text(r.resolved_at),
    resolved_by: text(r.resolved_by),
    resolution: text(r.resolution),
  };
}

export function toPickedLine(raw: unknown): YardPickedLine {
  const r = bag(raw);
  return {
    id: text(r.id),
    task_id: text(r.task_id),
    order_line_no: num(r.order_line_no),
    sku: text(r.sku),
    description: text(r.description),
    qty_ordered: text(r.qty_ordered),
    qty_picked: text(r.qty_picked),
    uom: text(r.uom),
    short_reason: text(r.short_reason),
    short_note: text(r.short_note),
    picked_at: text(r.picked_at),
  };
}

export function toPick(raw: unknown): YardPick {
  const r = bag(raw);
  return {
    task: toTask(r.task),
    ticket: toTicket(r.ticket),
    shortages: Array.isArray(r.shortages) ? r.shortages.map(toShortage) : [],
    picked: Array.isArray(r.picked) ? r.picked.map(toPickedLine) : [],
  };
}

/** The skid answer keeps the count and drops the deposit figures: they never reach a screen. */
export function toSkidAnswer(raw: unknown): YardSkidAnswer {
  const r = bag(raw);
  return {
    skid_count: num(r.skid_count),
    previous_skid_count: typeof r.previous_skid_count === "number" && Number.isFinite(r.previous_skid_count) ? r.previous_skid_count : null,
    changed: r.changed === true,
    order_no: text(r.order_no),
  };
}

export function toChange(raw: unknown): YardChange {
  const r = bag(raw);
  return {
    id: text(r.id),
    change_no: text(r.change_no),
    order_no: text(r.order_no),
    status: text(r.status),
    reason: text(r.reason),
    requested_by_role: text(r.requested_by_role),
    requested_at: text(r.requested_at),
    next_actions: Array.isArray(r.next_actions) ? r.next_actions.filter((a): a is string => typeof a === "string") : [],
  };
}

export function toChangeAnswer(raw: unknown): YardChangeAnswer {
  const r = bag(raw);
  return { id: text(r.id), change_no: text(r.change_no), order_id: text(r.order_id), status: text(r.status) };
}

/** Every call the yard's screens make. */
export class YardApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The branches this login may reach: the yard names one for the queue, and the board shows its name. */
  async branches(): Promise<Result<YardBranch[]>> {
    const answer = await staffBranches.branchesList(this.#client);
    return answer.ok ? { ok: true, value: answer.value.map(toBranch) } : answer;
  }

  /** The yard board: one branch's pick tasks joined to their tickets and open shortages. */
  async queue(branchId: string, cursor = ""): Promise<Result<{ picks: YardPick[]; nextCursor: string }>> {
    const answer = await staffTasks.yardListPickQueue(this.#client, { branch_id: branchId, cursor: cursor === "" ? undefined : cursor, limit: 50 });
    if (!answer.ok) return answer;
    return { ok: true, value: { picks: answer.value.items.map(toPick), nextCursor: answer.value.next_cursor ?? "" } };
  }

  /** One pick: the task, its ticket, its whole shortage history, and what was pulled. */
  async pick(id: string): Promise<Result<YardPick>> {
    const answer = await staffTasks.yardGetPick(this.#client, id);
    return answer.ok ? { ok: true, value: toPick(answer.value) } : answer;
  }

  /** The product's reference picture, fetched through the generated binary client. */
  async productImage(productId: string): Promise<Result<Uint8Array>> {
    return staffProduct.productGetImage(this.#client, productId);
  }

  /** Claim an unclaimed pick: it becomes the caller's own. */
  async claim(id: string): Promise<Result<YardTask>> {
    const answer = await staffTasks.tasksClaim(this.#client, id);
    return answer.ok ? { ok: true, value: toTask(answer.value) } : answer;
  }

  /** Start picking a claimed pick: the record and stage doors open. */
  async start(id: string): Promise<Result<YardTask>> {
    const answer = await staffTasks.tasksAccept(this.#client, id);
    return answer.ok ? { ok: true, value: toTask(answer.value) } : answer;
  }

  /** Record what was actually pulled, the whole set in one call. Zero is an answer, above the ordered number is refused. */
  async recordLines(id: string, lines: readonly YardRecordLine[]): Promise<Result<YardPickedLine[]>> {
    const answer = await staffTasks.yardRecordPick(this.#client, id, {
      lines: lines.map((l) => ({
        order_line_no: l.order_line_no,
        product_id: l.product_id,
        sku: l.sku,
        description: l.description,
        qty_ordered: l.qty_ordered,
        qty_picked: l.qty_picked,
        uom: l.uom,
        ...(l.short_reason !== "" ? { short_reason: l.short_reason as "CONDENSED_SKIDS" | "DAMAGED" | "NOT_IN_YARD" | "COUNT_CORRECTION" | "OTHER" } : {}),
        ...(l.short_note !== "" ? { short_note: l.short_note } : {}),
      })),
    });
    return answer.ok ? { ok: true, value: answer.value.items.map(toPickedLine) } : answer;
  }

  /** Record or change the order's skid count, with a reason. */
  async recordSkids(id: string, skidCount: number, reason: string, note: string): Promise<Result<YardSkidAnswer>> {
    const answer = await staffTasks.yardRecordSkids(this.#client, id, {
      skid_count: skidCount,
      reason: reason as "CONDENSED_SKIDS" | "DAMAGED" | "NOT_IN_YARD" | "COUNT_CORRECTION" | "OTHER",
      ...(note !== "" ? { note } : {}),
    });
    return answer.ok ? { ok: true, value: toSkidAnswer(answer.value) } : answer;
  }

  /** Park the pick: where the material sits, and the short handle the counter reads out. */
  async stage(id: string, location: string, ref: string, note: string): Promise<Result<YardTask>> {
    const answer = await staffTasks.yardStagePick(this.#client, id, {
      location,
      ...(ref !== "" ? { ref } : {}),
      ...(note !== "" ? { note } : {}),
    });
    return answer.ok ? { ok: true, value: toTask(answer.value) } : answer;
  }

  /** Report one shortage: the deliberate act that tells the counter, separate from the measurement. */
  async reportShortage(
    id: string,
    line: { orderLineNo: number | null; description: string; qtyShort: string; uom: string; note: string },
  ): Promise<Result<YardShortage>> {
    const answer = await staffTasks.yardReportShortage(this.#client, id, {
      order_line_no: line.orderLineNo,
      product_id: "",
      sku: "",
      description: line.description,
      qty_short: line.qtyShort,
      uom: line.uom,
      note: line.note,
    });
    return answer.ok ? { ok: true, value: toShortage(answer.value) } : answer;
  }

  /** Clear one shortage with a stated resolution. Refused to a caller off the shortage resolvers list. */
  async resolveShortage(id: string, shortageId: string, resolution: string): Promise<Result<YardShortage>> {
    const answer = await staffTasks.yardResolveShortage(this.#client, id, shortageId, { resolution });
    return answer.ok ? { ok: true, value: toShortage(answer.value) } : answer;
  }

  /** The changes waiting on the caller's branches, oldest first, one page. The status is the server's ladder word; the yard asks for its own stage. */
  async changeQueue(status: "PENDING_YARD", cursor = ""): Promise<Result<{ changes: YardChange[]; nextCursor: string }>> {
    const answer = await staffOrder.orderListChangeQueue(this.#client, {
      status,
      ...(cursor === "" ? {} : { cursor }),
      limit: 50,
    });
    if (!answer.ok) return answer;
    return { ok: true, value: { changes: answer.value.items.map(toChange), nextCursor: answer.value.next_cursor ?? "" } };
  }

  /** Acknowledge one change for the yard, with an optional note: the word the ladder waits on. */
  async acknowledgeChange(changeId: string, note: string): Promise<Result<YardChangeAnswer>> {
    const trimmed = note.trim();
    const answer = await staffOrder.orderChangeYardAck(this.#client, changeId, trimmed === "" ? {} : { note: trimmed });
    return answer.ok ? { ok: true, value: toChangeAnswer(answer.value) } : answer;
  }
}
