// Yard Pick's one door to the core: the yard team's reads and writes, over
// the generated tasks client, answering a Result and never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the yard's screens use. A field the
// projection does not name is dropped at this door and never reaches a
// screen, so the no money rule does not rest on the server alone: the app
// could not show a figure if one arrived. Two contract declared money fields
// pass this door never: the held order's owed figure (the hold's own words
// carry the fact on the ticket this file shapes) and the skid answer's
// deposit figures (a skid deposit is the office's money, not the yard's).
//
// Port provenance: the call set is the original app's app/src/yard-api.ts
// (the original HardscapeOS app, repository hardscapeos_dibbits, commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab), narrowed to the pick slice and
// rebuilt on HH's generated client.
import { ApiClient, staffBranches, staffTasks, type Result } from "@hardscapehouse/api-client";

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
    resolution: text(r.resolution),
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
  /** Claim an unclaimed pick: it becomes the caller's own. */
  async claim(id: string): Promise<Result<YardTask>> {
    const answer = await staffTasks.tasksClaim(this.#client, id);
    return answer.ok ? { ok: true, value: toTask(answer.value) } : answer;
  }
}
