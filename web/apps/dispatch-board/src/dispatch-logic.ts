// The dispatch board's rules, as plain code with no DOM: the lifecycle matrix the
// screens read to decide which buttons exist, the capacity bands behind the load
// gauges, the quick cards' filters, the keyboard path's move projection, the bulk
// load words, the reschedule check and the sentences the board prints.
//
// THIS FILE MIRRORS THE SERVER AND DECIDES NOTHING. The matrix, the commitment, the
// bands and the split all live in the core (internal/dispatch) and the server re-grades
// every move inside a guarded write. What is here saves a round trip and lets the board
// avoid offering a move the server would refuse. Ported from the original app's
// dispatch-logic.ts and dispatch-load-logic.ts (see PROVENANCE.md).
//
// Money: nothing here adds money up. A figure the core sent is formatted and nothing
// more, and a figure the core left out is absent, never a zero.
import type {
  DispatchLoadBoardView,
  DispatchLoadOutcome,
  DispatchLoadRow,
  DispatchDriver,
  DispatchLoadStop,
  DispatchScheduleInput,
  DispatchTruck,
  DispatchStopPaymentHold,
} from "@hardscapehouse/api-client";

export type StopStatus = "READY" | "LOADED" | "EN_ROUTE" | "DELIVERED" | "FAILED" | "CANCELLED";
export type RunStatus = "PLANNED" | "DISPATCHED" | "COMPLETED" | "CANCELLED";
export type Band = "GREEN" | "AMBER" | "RED" | "UNKNOWN";

// ---- the lifecycle, mirrored -------------------------------------------------------------------

const STOP_NEXT: Record<string, StopStatus[]> = {
  READY: ["LOADED", "CANCELLED"],
  LOADED: ["EN_ROUTE", "CANCELLED"],
  EN_ROUTE: ["DELIVERED", "FAILED"],
  DELIVERED: [],
  FAILED: [],
  CANCELLED: [],
};

const RUN_NEXT: Record<string, RunStatus[]> = {
  PLANNED: ["DISPATCHED", "CANCELLED"],
  DISPATCHED: ["COMPLETED"],
  COMPLETED: [],
  CANCELLED: [],
};

/** The day board's columns, in the order the day actually runs. */
export const BOARD_COLUMNS: readonly StopStatus[] = ["READY", "LOADED", "EN_ROUTE", "DELIVERED"];

export const STOP_LABEL: Record<string, string> = {
  READY: "Ready",
  LOADED: "Loaded",
  EN_ROUTE: "En route",
  DELIVERED: "Delivered",
  FAILED: "Failed",
  CANCELLED: "Cancelled",
};

export const RUN_LABEL: Record<string, string> = {
  PLANNED: "Planned",
  DISPATCHED: "Dispatched",
  COMPLETED: "Completed",
  CANCELLED: "Cancelled",
};

// A state read inside a sentence: the label with its capital and underscore taken off.
// An unmapped value degrades honestly (it comes back opened out, so a new state reads as
// strange rather than as a confident lie about where a truck is).
function midSentence(word: string): string {
  return word.toLowerCase().replace(/_/g, " ");
}

export function stopWord(status: string): string {
  return midSentence(STOP_LABEL[status] ?? status);
}

export function runWord(status: string): string {
  return midSentence(RUN_LABEL[status] ?? status);
}

/** Legal successors of a stop status. An unknown status offers nothing, never everything. */
export function stopNextStates(status: string): StopStatus[] {
  return [...(STOP_NEXT[status] ?? [])];
}

export function runNextStates(status: string): RunStatus[] {
  return [...(RUN_NEXT[status] ?? [])];
}

export interface StopAction {
  /** The status this button moves to, or "POD" for the proof of delivery the driver captures. */
  readonly action: StopStatus | "POD";
  readonly label: string;
  readonly destructive?: boolean;
}

/**
 * The actions a stop card offers, in the order a thumb moves down the card. DELIVERED is not
 * an action: it is the outcome of a proof of delivery, which the driver signs on HH Drive,
 * and offering it beside "Could not deliver" would invite the unevidenced delivery the
 * server refuses. The matrix names it POD and the card says where it is signed.
 */
export function stopActions(status: string, opts: { assigned: boolean } = { assigned: true }): StopAction[] {
  const out: StopAction[] = [];
  for (const next of stopNextStates(status)) {
    switch (next) {
      case "LOADED":
        // A stop on no run cannot be loaded onto one: the server says so, the board does not draw the button.
        if (opts.assigned) out.push({ action: "LOADED", label: "Mark loaded" });
        break;
      case "EN_ROUTE":
        out.push({ action: "EN_ROUTE", label: "Mark en route" });
        break;
      case "DELIVERED":
        out.push({ action: "POD", label: "Proof of delivery" });
        break;
      case "FAILED":
        out.push({ action: "FAILED", label: "Could not deliver", destructive: true });
        break;
      case "CANCELLED":
        out.push({ action: "CANCELLED", label: "Cancel stop", destructive: true });
        break;
      default:
        break;
    }
  }
  return out;
}

// ---- the gauges ---------------------------------------------------------------------------------

/** The amber line, mirrored from the core: where a dispatcher still has time to move a pallet. */
export const AMBER_FROM = 0.85;

/**
 * capacityBand grades a load against a capacity. An unknown capacity is unknown, never green:
 * a truck nobody measured, or a load nobody weighed, gauges UNKNOWN and the card says so.
 */
export function capacityBand(used: number | null | undefined, capacity: number | null | undefined): Band {
  if (typeof capacity !== "number" || !Number.isFinite(capacity) || capacity <= 0) return "UNKNOWN";
  if (typeof used !== "number" || !Number.isFinite(used) || used < 0) return "UNKNOWN";
  const ratio = used / capacity;
  if (ratio > 1) return "RED";
  if (ratio >= AMBER_FROM) return "AMBER";
  return "GREEN";
}

const BAND_RANK: Record<Band, number> = { UNKNOWN: 0, GREEN: 1, AMBER: 2, RED: 3 };

/** The pessimistic fold: a load that is fine on weight and over on volume is an over load. */
export function worstBand(...bands: Band[]): Band {
  let worst: Band = "UNKNOWN";
  for (const band of bands) if (BAND_RANK[band] > BAND_RANK[worst]) worst = band;
  return worst;
}

/** The percentage a bar fills, clamped so a 130 percent load still draws inside its track. */
export function barPct(used: number | null | undefined, capacity: number | null | undefined): number {
  if (typeof capacity !== "number" || !Number.isFinite(capacity) || capacity <= 0) return 0;
  if (typeof used !== "number" || !Number.isFinite(used) || used < 0) return 0;
  return Math.min(100, Math.round((used / capacity) * 100));
}

/** The word beside the colour: colour never carries the meaning alone. */
export function bandWord(band: Band): string {
  switch (band) {
    case "RED":
      return "Over";
    case "AMBER":
      return "Near full";
    case "GREEN":
      return "Room";
    default:
      return "Not measured";
  }
}

// ---- display units -------------------------------------------------------------------------------
//
// The core keeps metric (kilograms, cubic metres) and so does every wire field. The demo tenant is a
// San Diego yard, so the screens show pounds and short tons and cubic yards. The conversion is for
// display only: nothing converted is ever sent back, and the ratios (bands, bars) are unit free.

const LB_PER_KG = 2.2046226218;
const CUYD_PER_M3 = 1.30795062;

/** Pounds below four thousand of them, short tons from there up, picked once for a gauge so both figures share a unit. */
export function weightUnit(...refsKg: Array<number | null | undefined>): "lb" | "tons" {
  const finite = refsKg.filter((v): v is number => typeof v === "number" && Number.isFinite(v));
  const top = finite.length > 0 ? Math.max(...finite) : 0;
  return top * LB_PER_KG >= 4000 ? "tons" : "lb";
}

/** Kilograms as the number shown in the given unit. */
export function toWeight(kg: number, unit: "lb" | "tons"): number {
  const pounds = kg * LB_PER_KG;
  return unit === "tons" ? pounds / 2000 : pounds;
}

/** Cubic metres as cubic yards. */
export function toCuYd(m3: number): number {
  return m3 * CUYD_PER_M3;
}

/** One weight with its unit: "7,496 lb" or "5.6 tons". An absent weight is words, never zero. */
export function fmtWeight(kg: number | null | undefined, unit?: "lb" | "tons"): string {
  if (typeof kg !== "number" || !Number.isFinite(kg)) return "n/a";
  const u = unit ?? weightUnit(kg);
  return `${fmtNum(toWeight(kg, u))} ${u}`;
}

/** Numbers are read at arm's length: no decimals unless they are the whole story. */
export function fmtNum(value: number | null | undefined): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "n/a";
  const rounded = Math.abs(value) >= 100 ? Math.round(value) : Math.round(value * 10) / 10;
  return rounded.toLocaleString("en-US");
}

/** A gauge's accessible sentence: what it is, how much, of what, and the verdict in words. */
export function gaugeLabel(kind: string, used: number | null | undefined, capacity: number | null | undefined, unit: string): string {
  const band = capacityBand(used, capacity);
  if (band === "UNKNOWN") {
    return typeof used === "number" ? `${kind} ${fmtNum(used)} ${unit}, capacity not measured` : `${kind} not measured`;
  }
  return `${kind} ${fmtNum(used)} of ${fmtNum(capacity as number)} ${unit}, ${bandWord(band).toLowerCase()}`;
}

// ---- requested and committed --------------------------------------------------------------------

/** The pill's word. A requested date is an ask and must never read like a promise. */
export function dateStateLabel(state: string | undefined): string {
  return state === "COMMITTED" ? "Committed" : "Requested";
}

/** True when the ask and the promise differ and the difference is worth showing. */
export function askDiffers(stop: { requested_date?: string; scheduled_date?: string; commit_state?: string }): boolean {
  const asked = (stop.requested_date ?? "").trim();
  return asked !== "" && asked !== (stop.scheduled_date ?? "") && stop.commit_state === "COMMITTED";
}

type RowCommit = Pick<DispatchLoadRow, "commit" | "run">;

/**
 * Whether the board may offer load changing controls on a truck's row. A committed load is
 * still editable by somebody who could reopen it: the server's rule is who may pass, and it
 * records the override. `committed` is about the load, `can_reopen` is about this reader.
 */
export function rowEditable(row: RowCommit): boolean {
  if (row.commit?.committed && !row.commit.can_reopen) return false;
  return row.run.status === "PLANNED";
}

const COMMITTED_REOPENABLE = "Committed. A dispatch manager can reopen it.";
const COMMITTED_NO_REOPEN = "Committed. This dealer does not reopen a committed load.";

/**
 * commitBlockedReason explains, before anything is pressed, why a load cannot be changed.
 * Returning the reason rather than a boolean lets a control be disabled and say why.
 */
export function commitBlockedReason(row: RowCommit): string {
  if (row.commit?.committed && !row.commit.can_reopen) {
    // The sentence names the way forward only when there is one.
    return row.commit.reopenable ? COMMITTED_REOPENABLE : COMMITTED_NO_REOPEN;
  }
  if (row.run.status !== "PLANNED") {
    return `This load is ${runWord(row.run.status)}. It is a record now, not a plan.`;
  }
  return "";
}

/**
 * stopBlockedReason is commitBlockedReason for a load drawn without a truck row. A stop that
 * names a run is graded by that run's row, so the two paths cannot drift; a stop on no truck is
 * graded by its own commit state and the caller echo `can_reopen`, never the dealer's policy on
 * its own (the policy picks the sentence, never the control).
 */
export function stopBlockedReason(
  stop: Pick<DispatchLoadStop, "commit_state" | "run_id">,
  view: Pick<DispatchLoadBoardView, "loads" | "can_reopen" | "policy"> | null,
): string {
  if (view === null) return "";
  if (stop.run_id) {
    const row = view.loads.find((r) => r.run.id === stop.run_id);
    if (row !== undefined) return commitBlockedReason(row);
  }
  if (stop.commit_state !== "COMMITTED" || view.can_reopen) return "";
  return view.policy?.reopen_policy === "NOBODY" ? COMMITTED_NO_REOPEN : COMMITTED_REOPENABLE;
}

/** The prompt on a truck that still carries asks with the delivery day close. A nudge, never a refusal. */
export function dueLabel(row: Pick<DispatchLoadRow, "commit">): string {
  return row.commit?.due_to_commit ? "Due to commit" : "";
}

/** What the board says after a reopen, and it says what actually happened: the date is an ask again. */
export function reopenNotice(stop: Pick<DispatchLoadStop, "order_no" | "run_id">): string {
  const what = stop.order_no ?? "Load";
  return stop.run_id
    ? `${what} reopened. Its date is an ask again, and it can be moved off this truck.`
    : `${what} reopened. Its date is an ask again, and it is still waiting for a truck.`;
}

/** The bulk form: reopening a run leaves every load exactly where it is. */
export function reopenRunNotice(row: Pick<DispatchLoadRow, "run">): string {
  return `${row.run.run_no} reopened. Every load on it is an ask again, and the truck can be rebuilt.`;
}

// ---- the quick cards ----------------------------------------------------------------------------

export type CardId =
  | "card.deliveries"
  | "card.pickups"
  | "card.unassigned"
  | "card.ready"
  | "card.requested"
  | "card.committed"
  | "card.due";

export const CARD_LABEL: Record<CardId, string> = {
  "card.deliveries": "Deliveries",
  "card.pickups": "Pickups",
  "card.unassigned": "Unassigned",
  "card.ready": "Ready to load",
  "card.requested": "Requested",
  "card.committed": "Committed",
  "card.due": "Due to commit",
};

export const CARD_ORDER: readonly CardId[] = [
  "card.deliveries",
  "card.pickups",
  "card.unassigned",
  "card.ready",
  "card.requested",
  "card.committed",
  "card.due",
];

/** cardCount reads the server's count. The client never adds anything up here. */
export function cardCount(view: DispatchLoadBoardView | null, id: CardId): number {
  const counts = view?.counts;
  if (counts === undefined) return 0;
  switch (id) {
    case "card.deliveries":
      return counts.deliveries;
    case "card.pickups":
      return counts.pickups;
    case "card.unassigned":
      return counts.unassigned;
    case "card.ready":
      return counts.ready;
    case "card.requested":
      return counts.requested;
    case "card.committed":
      return counts.committed;
    case "card.due":
      return counts.due;
    default:
      return 0;
  }
}

/**
 * cardMatches is the filter a card applies when opened. Every card filters in place: the card
 * opens a filtered board underneath itself and does not navigate. The pickups card is the
 * exception and says so: a customer pickup is not a delivery stop, so it matches no row here.
 */
export function cardMatches(id: CardId, stop: DispatchLoadStop, committed: boolean): boolean {
  switch (id) {
    case "card.deliveries":
      return true;
    case "card.unassigned":
      return !stop.run_id;
    case "card.ready":
      return stop.status === "READY";
    case "card.requested":
      return stop.commit_state === "REQUESTED";
    case "card.committed":
      return stop.commit_state === "COMMITTED" || committed;
    case "card.due":
      return !stop.run_id && stop.commit_state !== "COMMITTED";
    case "card.pickups":
      return false;
    default:
      return true;
  }
}

/**
 * The denominator each card counts against. Four cards count the board's day, two count the
 * whole working window the staging lane draws, and pickups count the branch with no date at
 * all. The scope is named once, here: filterBoard holds a day card's drill down to that day,
 * and the shelf prints the scope so a reader can tell "none today" from "none at all".
 */
export type CardScope = "DAY" | "WINDOW" | "BRANCH";

export const CARD_SCOPE: Record<CardId, CardScope> = {
  "card.deliveries": "DAY",
  "card.ready": "DAY",
  "card.requested": "DAY",
  "card.committed": "DAY",
  "card.unassigned": "WINDOW",
  "card.due": "WINDOW",
  "card.pickups": "BRANCH",
};

export function cardScopeLabel(view: DispatchLoadBoardView | null, id: CardId): string {
  switch (CARD_SCOPE[id]) {
    case "WINDOW":
      return `next ${view?.working_days ?? 14} days`;
    case "BRANCH":
      return "this branch";
    default:
      return "this day";
  }
}

// An absent date is not a mismatch: hiding a row because a field is missing is the failure
// every filter on this screen is written to avoid.
function inDayScope(stop: Pick<DispatchLoadStop, "scheduled_date">, date: string): boolean {
  const day = (stop.scheduled_date ?? "").trim();
  return day === "" || day === date;
}

/** A card that filters to nothing still opens, with an empty state and never an error. */
export function filterBoard(view: DispatchLoadBoardView, card: CardId | ""): DispatchLoadBoardView {
  if (card === "") return view;
  // The truck lanes are already the board's date, so the day scope only has to be applied to the working window lane.
  const dayOnly = CARD_SCOPE[card] === "DAY";
  const loads = view.loads
    .map((row) => ({ ...row, stops: row.stops.filter((s) => cardMatches(card, s, Boolean(row.commit?.committed))) }))
    .filter((row) => (card === "card.committed" ? Boolean(row.commit?.committed) : row.stops.length > 0 || card === "card.deliveries"));
  return {
    ...view,
    loads,
    staging: view.staging.filter((s) => cardMatches(card, s, false) && (!dayOnly || inDayScope(s, view.date))),
  };
}

/** A lane's scope note, shown only when the lane holds work for another day. */
export function laneScopeNote(stops: readonly Pick<DispatchLoadStop, "scheduled_date">[], date: string, workingDays: number): string {
  return stops.some((s) => s.scheduled_date && s.scheduled_date !== date) ? `next ${workingDays} days` : "";
}

/** The day pill on a card: a card that is not for the lane's date says which day it is for. */
export function dayOfLabel(stop: Pick<DispatchLoadStop, "scheduled_date">, laneDate: string): string {
  const day = (stop.scheduled_date ?? "").trim();
  return day !== "" && day !== laneDate ? shortDay(day) : "";
}

// ---- the keyboard path --------------------------------------------------------------------------

/**
 * A move is two decisions, which load and onto which truck. assignTargets returns, for a load
 * being moved, every truck it could go on with the gauge it would have after the move, so the
 * second decision is made with the same information a pointer user sees.
 */
export interface AssignTarget {
  readonly runId: string;
  readonly runNo: string;
  readonly truckCode: string;
  readonly bandAfter: Band;
  readonly weightAfter: number | null;
  readonly capacityKg: number | null;
  readonly volumeAfter: number | null;
  readonly capacityM3: number | null;
  /** Empty when the move is offered; the reason when it is not. */
  readonly blocked: string;
}

function addMeasure(total: number | null | undefined, add: number | null | undefined, alreadyThere: boolean): number | null {
  if (typeof total !== "number") return typeof add === "number" && !alreadyThere ? add : null;
  if (alreadyThere || typeof add !== "number") return total;
  return total + add;
}

export function assignTargets(view: DispatchLoadBoardView, stop: DispatchLoadStop): AssignTarget[] {
  const out: AssignTarget[] = [];
  for (const row of view.loads) {
    const alreadyHere = stop.run_id === row.run.id;
    const weight = addMeasure(row.gauge.weight_kg, stop.weight_kg, alreadyHere);
    const volume = addMeasure(row.gauge.volume_m3, stop.volume_m3, alreadyHere);
    const pallets = addMeasure(row.gauge.pallets, stop.pallet_positions, alreadyHere);
    let blocked = alreadyHere ? "Already on this load" : commitBlockedReason(row);
    if (blocked === "" && row.run.scheduled_date !== stop.scheduled_date) {
      blocked = `This truck runs on ${row.run.scheduled_date}, the delivery is for ${stop.scheduled_date}.`;
    }
    out.push({
      runId: row.run.id,
      runNo: row.run.run_no,
      truckCode: row.run.truck_code ?? "",
      bandAfter: worstBand(
        capacityBand(weight, row.gauge.capacity_kg),
        capacityBand(volume, row.gauge.capacity_m3),
        capacityBand(pallets, row.gauge.pallet_positions),
      ),
      weightAfter: weight,
      capacityKg: row.gauge.capacity_kg ?? null,
      volumeAfter: volume,
      capacityM3: row.gauge.capacity_m3 ?? null,
      blocked,
    });
  }
  return out;
}

export function targetName(target: Pick<AssignTarget, "runNo" | "truckCode">): string {
  return `${target.runNo}${target.truckCode ? ` · ${target.truckCode}` : ""}`;
}

/**
 * targetLine is the right hand half of a row in the move list. A blocked target says why it is
 * blocked instead of projecting a gauge for a move that cannot happen.
 */
export function targetLine(target: AssignTarget): string {
  if (target.blocked !== "") return target.blocked;
  const unit = weightUnit(target.capacityKg, target.weightAfter);
  const gauge = target.capacityKg && target.weightAfter !== null ? `${fmtNum(toWeight(target.weightAfter, unit))} of ${fmtNum(toWeight(target.capacityKg, unit))} ${unit} · ` : "";
  return `${gauge}${bandWord(target.bandAfter)}`;
}

/** pruneSelection drops from a bulk selection every load the board no longer draws. */
export function pruneSelection(selected: readonly string[], stops: readonly Pick<DispatchLoadStop, "id">[]): string[] {
  const live = new Set(stops.map((s) => s.id));
  return selected.filter((id) => live.has(id));
}

/** What a screen reader hears after a move: the fact that changed, since a keyboard user cannot see the bar move. */
export function moveAnnouncement(stop: Pick<DispatchLoadStop, "order_no" | "customer_name">, target: AssignTarget | null): string {
  const what = stop.order_no || stop.customer_name || "Load";
  if (target === null) return `${what} taken off its truck. It is back in the staging lane.`;
  const gauge =
    typeof target.weightAfter === "number" && typeof target.capacityKg === "number"
      ? ` ${fmtNum(toWeight(target.weightAfter, weightUnit(target.capacityKg, target.weightAfter)))} of ${fmtNum(toWeight(target.capacityKg, weightUnit(target.capacityKg, target.weightAfter)))} ${weightUnit(target.capacityKg, target.weightAfter)}, ${bandWord(target.bandAfter).toLowerCase()}.`
      : ` ${bandWord(target.bandAfter)}.`;
  return `${what} moved to ${targetName(target)}.${gauge}`;
}

// ---- the status axis ----------------------------------------------------------------------------

/** The status lanes, in the order a delivery day actually runs. */
export const STATUS_LANES = ["READY", "LOADED", "EN_ROUTE", "DELIVERED"] as const;

/** The same data the truck axis draws, re-laned: the board switches axis without going back to the server. */
export function groupByStatus(view: DispatchLoadBoardView): Record<string, DispatchLoadStop[]> {
  const lanes: Record<string, DispatchLoadStop[]> = {};
  for (const lane of STATUS_LANES) lanes[lane] = [];
  lanes.OTHER = [];
  const all: DispatchLoadStop[] = [...view.staging];
  for (const row of view.loads) all.push(...row.stops);
  for (const stop of all) {
    const lane = (STATUS_LANES as readonly string[]).includes(stop.status) ? stop.status : "OTHER";
    lanes[lane]?.push(stop);
  }
  return lanes;
}

/** D-110's answer under the pickups card: not how many but where they are. Empty for none. */
export function pickupSplitLabel(view: DispatchLoadBoardView | null): string {
  const counts = view?.counts;
  if (counts === undefined) return "";
  const awaiting = counts.pickups_awaiting_pick ?? 0;
  const ready = counts.pickups_ready_to_collect ?? 0;
  if (awaiting + ready === 0) return "";
  return `${awaiting} to pick · ${ready} ready`;
}

// ---- loading several trucks in one flow ---------------------------------------------------------

/**
 * loadableStops is which of a selection the board may offer to load. It asks the matrix rather
 * than re-deriving it: a second opinion here would be a second copy of the lifecycle.
 */
export function loadableStops(stops: readonly DispatchLoadStop[], selected: ReadonlySet<string>): DispatchLoadStop[] {
  return stops.filter((s) => selected.has(s.id) && stopActions(s.status, { assigned: Boolean(s.run_id) }).some((a) => a.action === "LOADED"));
}

/** Why some of a selection cannot be loaded, without asserting a reason it has not checked. */
export function unloadableSummary(stops: readonly DispatchLoadStop[], selected: ReadonlySet<string>): string {
  const picked = stops.filter((s) => selected.has(s.id));
  const stuck = picked.filter((s) => !stopActions(s.status, { assigned: Boolean(s.run_id) }).some((a) => a.action === "LOADED"));
  if (stuck.length === 0) return "";
  const noTruck = stuck.filter((s) => !s.run_id).length;
  const already = stuck.filter((s) => s.run_id && s.status === "LOADED").length;
  const other = stuck.length - noTruck - already;
  const parts: string[] = [];
  if (noTruck > 0) parts.push(`${noTruck} ${noTruck === 1 ? "is" : "are"} not on a truck yet`);
  if (already > 0) parts.push(`${already} ${already === 1 ? "is" : "are"} already loaded`);
  if (other > 0) parts.push(`${other} cannot be loaded from here`);
  return parts.join(", ");
}

/** The bulk bar's one button names the whole gesture, including how many trucks it spans. */
export function bulkLoadLabel(stops: readonly DispatchLoadStop[]): string {
  if (stops.length === 0) return "";
  const trucks = new Set(stops.map((s) => s.run_id).filter(Boolean)).size;
  const loads = `${stops.length} load${stops.length === 1 ? "" : "s"}`;
  if (trucks <= 1) return `Mark ${loads} loaded`;
  return `Mark ${loads} loaded across ${trucks} trucks`;
}

/**
 * What the screen says after a bulk load, and it says both halves: the loads that took are
 * counted and each refusal is named with the load it belongs to. A bulk gesture where one of
 * nine refuses is eight loaded trucks and one thing to go and look at.
 */
export function bulkLoadOutcome(outcomes: readonly DispatchLoadOutcome[]): { notice: string; refusals: string[] } {
  const loaded = outcomes.filter((o) => o.loaded).length;
  const refusals = outcomes
    .filter((o) => !o.loaded)
    .map((o) => `${o.order_no || "A load"}: ${plainSentence(o.refusal) || "did not move"}`);
  if (loaded === 0) return { notice: "", refusals };
  return { notice: `${loaded} load${loaded === 1 ? "" : "s"} marked loaded.`, refusals };
}

// ---- reschedule ---------------------------------------------------------------------------------

/** Today as YYYY-MM-DD in the local calendar: a dispatcher opening the board at 8pm means today. */
export function todayISO(d: Date = new Date()): string {
  const pad = (n: number): string => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** Shift an ISO date by whole days, staying in the local calendar. */
export function shiftISODate(iso: string, days: number): string {
  const [y, m, d] = iso.split("-").map(Number);
  if (!y || !m || !d) return iso;
  const date = new Date(y, m - 1, d);
  date.setDate(date.getDate() + days);
  return todayISO(date);
}

/** A day heading: "Tue, Aug 4, 2026". */
export function formatDayHeading(iso: string): string {
  const [y, m, d] = iso.split("-").map(Number);
  if (!y || !m || !d) return iso;
  return new Date(y, m - 1, d).toLocaleDateString("en-US", { weekday: "short", day: "numeric", month: "short", year: "numeric" });
}

/** A short day for a pill or a strip: "Oct 13". */
export function shortDay(iso: string): string {
  const [y, m, d] = iso.split("-").map(Number);
  if (!y || !m || !d) return iso;
  return new Date(y, m - 1, d).toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

/** A clock time from an ISO timestamp, or an empty string when absent. */
export function formatTime(ts?: string | null): string {
  if (!ts) return "";
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit", hour12: false });
}

/** One line of address from the parts that are present. A blank segment is skipped, not printed. */
export function addressLine(parts: { address_line?: string; city?: string; postal_code?: string }): string {
  return [parts.address_line, parts.city, parts.postal_code]
    .map((p) => (p ?? "").trim())
    .filter((p) => p.length > 0)
    .join(", ");
}

/**
 * validateReschedule mirrors the server's refusals so a dispatcher gets the message before the
 * round trip. The server still enforces every one; this must never accept what it would refuse.
 * `today` is passed in so the boundary is testable.
 */
export function validateReschedule(toDate: string, reason: string, currentDate: string, today: string): string {
  const to = (toDate ?? "").trim();
  if (!/^\d{4}-\d{2}-\d{2}$/.test(to)) return "Pick a delivery date.";
  if (to === currentDate) return "That is already the scheduled date.";
  if (to < today) return "That date has passed. A delivery cannot be promised for a day that has gone.";
  const why = (reason ?? "").trim();
  if (why === "") return "Say why the delivery is moving. The customer will ask.";
  if (why.length > 400) return "Keep the reason under 400 characters.";
  return "";
}

/** Why a stop cannot be moved, before the sheet opens, so the button can be disabled and say why. */
export function rescheduleBlockedReason(stop: { status: string; run_id?: string; run_no?: string }): string {
  if (stop.status !== "READY") {
    return `This delivery is ${stopWord(stop.status)}. Its date is a record now, not a plan.`;
  }
  if (stop.run_id) {
    return `It is on run ${stop.run_no || "a run"}. Take it off the run, or reschedule the whole run.`;
  }
  return "";
}

/** The reopen sheet's check: the server refuses a reopen with no reason. */
export function validateReason(reason: string): string {
  const why = (reason ?? "").trim();
  if (why === "") return "Say why. The reason is kept with the load.";
  if (why.length > 400) return "Keep the reason under 400 characters.";
  return "";
}

// ---- the payment hold, in words -----------------------------------------------------------------

/** Cents in, dollars out; zero renders as an empty string so a hold with no stated amount does not print $0.00. */
export function formatDue(cents: number | undefined | null): string {
  if (typeof cents !== "number" || !Number.isFinite(cents) || cents <= 0) return "";
  return `$${(cents / 100).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

/**
 * The hold as the board prints it: a hold is a hold. The words are the core's (it substitutes
 * a plain sentence for a caller the cost rule refuses), and the amount appears only when the
 * core sent one. A held stop always produces a sentence.
 */
export function holdWords(hold: Pick<DispatchStopPaymentHold, "held" | "reason" | "amount_due_cents"> | null | undefined): string {
  if (hold === null || hold === undefined || !hold.held) return "";
  const said = (hold.reason ?? "").trim() || "Payment is due on this order before it can be delivered.";
  // The core's own sentence may start lower case and carry no stop; the board says it in its voice.
  const reason = `${said.charAt(0).toUpperCase()}${said.slice(1)}`;
  const sentence = /[.!?]$/.test(reason) ? reason : `${reason}.`;
  const amount = formatDue(hold.amount_due_cents);
  return amount !== "" ? `${sentence} ${amount} due.` : sentence;
}

// ---- words for a refusal ------------------------------------------------------------------------

/**
 * plainSentence strips the module prefix a server sentence carries ("dispatch: ...") before a
 * person reads it. Only a leading lower case word and a colon is removed.
 */
export function plainSentence(message: string | null | undefined): string {
  const s = (message ?? "").trim();
  const m = /^([a-z][a-z0-9_]*): (.+)$/s.exec(s);
  return m ? (m[2] ?? "").trim() : s;
}

/**
 * A refusal in words. The server's own sentence is what a person acts on (a 409 says three stops
 * are not finished, a 403 names the role that may do it), so it is kept and only the module
 * prefix comes off. A transport error has no status and says the board could not be reached.
 */
export function refusalWords(what: string, error: string, status: number | null): string {
  if (status === null || status === 0) return `The board could not reach the server. ${what} did not go through.`;
  if (status === 401) return "Your session ended. Sign in again from the front door.";
  if (status === 501) return plainSentence(error);
  if (status >= 500) return `The server could not finish. ${what} did not go through, and nothing changed.`;
  // The role gate answers a bare "Forbidden" where the service would have named who may do it, so a bare
  // one is no sentence at all: say who may (the core's capability flag and its gate can disagree).
  const sentence = status === 403 && /^forbidden\.?$/i.test(plainSentence(error)) ? "" : plainSentence(error);
  if (sentence !== "") return sentence;
  if (status === 403) return "That is not yours to do. The Logistics Manager or a Branch Manager can.";
  return `${what} was refused.`;
}


// ---- the schedule form (part B) --------------------------------------------------------------------

/** What the schedule form holds, every field as the text the person typed. */
export interface ScheduleFields {
  readonly order: string;
  readonly date: string;
  readonly window: string;
  readonly contact: string;
  readonly phone: string;
  readonly address: string;
  readonly city: string;
  readonly postal: string;
  readonly lat: string;
  readonly lon: string;
  readonly instructions: string;
}

/**
 * The form's own check, before the core is asked: an order, a day that has not gone, and the
 * coordinates in a pair. Whether the order exists, is confirmed, is in this yard or is already
 * scheduled is the core's to say, and its sentence is shown as it comes.
 */
export function validateSchedule(f: Pick<ScheduleFields, "order" | "date" | "lat" | "lon">, today: string): string {
  if (f.order.trim() === "") return "Enter an order number, like the one on the paperwork, or paste the order id.";
  const date = f.date.trim();
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return "Pick a delivery date.";
  if (date < today) return "That date has passed. A delivery cannot be promised for a day that has gone.";
  const lat = f.lat.trim();
  const lon = f.lon.trim();
  if ((lat === "") !== (lon === "")) return "Give both coordinates, or neither.";
  if (lat === "") return "";
  const la = Number(lat);
  const lo = Number(lon);
  if (!Number.isFinite(la) || !Number.isFinite(lo)) return "Coordinates are numbers, like 32.7157 and -117.1611.";
  if (la < -90 || la > 90) return "Latitude is between -90 and 90.";
  if (lo < -180 || lo > 180) return "Longitude is between -180 and 180.";
  return "";
}

/**
 * The wire body. The date is asked for, never promised: a load is committed by putting it on a
 * truck, so this form never asks for COMMITTED. The measurements stay unmeasured (null); the core
 * stamps the load's value from the order, so no figure rides this body.
 */
export function scheduleBody(f: ScheduleFields): DispatchScheduleInput {
  const num = (text: string): number | null => (text.trim() === "" ? null : Number(text));
  return {
    order_id: f.order.trim(),
    scheduled_date: f.date.trim(),
    time_window: f.window.trim(),
    contact_name: f.contact.trim(),
    contact_phone: f.phone.trim(),
    address_line: f.address.trim(),
    city: f.city.trim(),
    postal_code: f.postal.trim(),
    latitude: num(f.lat),
    longitude: num(f.lon),
    instructions: f.instructions.trim(),
    commit_state: "REQUESTED",
    requested_date: "",
    weight_kg: null,
    volume_m3: null,
    pallet_positions: null,
  };
}

export function scheduleNotice(stop: { order_no?: string; scheduled_date: string }): string {
  return `${stop.order_no ?? "The order"} scheduled for ${formatDayHeading(stop.scheduled_date)}. It is a request until it is on a truck.`;
}

/** A refused schedule in words. A missing order is the same answer for a number that never existed and one at another yard. */
export function scheduleRefusal(error: string, status: number | null): string {
  if (status === 404) return "No order matches that number at this yard. Check the number on the paperwork.";
  return refusalWords("Scheduling it", error, status);
}

// ---- the trucks and drivers registry (part B) --------------------------------------------------------

export const TRUCK_TYPES = ["FLATBED", "BOOM", "DUMP", "PICKUP", "TRAILER"] as const;
export const TRUCK_TYPE_LABEL: Record<string, string> = {
  FLATBED: "Flatbed",
  BOOM: "Boom truck",
  DUMP: "Dump truck",
  PICKUP: "Pickup",
  TRAILER: "Trailer",
};

export interface TruckFields {
  readonly code: string;
  readonly name: string;
  readonly type: string;
  /** Pounds, as typed. */
  readonly capacity: string;
  /** Cubic yards, as typed. */
  readonly volume: string;
  readonly pallets: string;
}

export interface DriverFields {
  readonly name: string;
  readonly phone: string;
  readonly licence: string;
  /** A staff login id, or empty for a driver with no login. */
  readonly login: string;
}

export const fleetStateWord = (active: boolean): string => (active ? "In use" : "Out of use");

/** The capacity as the edit sheet shows it: whole pounds. Empty when the core holds none. */
export function capacityText(kg: number | null | undefined): string {
  return typeof kg === "number" && Number.isFinite(kg) ? String(Math.round(kg * LB_PER_KG)) : "";
}

/** The volume as the edit sheet shows it: cubic yards to a tenth. Empty when the core holds none. */
export function volumeText(m3: number | null | undefined): string {
  return typeof m3 === "number" && Number.isFinite(m3) ? String(Math.round(m3 * CUYD_PER_M3 * 10) / 10) : "";
}

/** A typed figure back to the wire's unit. A figure left exactly as shown is the stored figure, so saving never drifts it. */
function wireFigure(typed: string, shown: string, original: number | null | undefined, toWire: (v: number) => number): number | null {
  const text = typed.trim();
  if (text === "") return null;
  if (typeof original === "number" && text === shown) return original;
  return Math.round(toWire(Number(text)) * 10) / 10;
}

export function validateTruck(f: TruckFields): string {
  if (f.code.trim() === "") return "A truck needs a code, like Truck 05.";
  if (!(TRUCK_TYPES as readonly string[]).includes(f.type)) return "Pick a truck type.";
  const cap = f.capacity.trim();
  if (cap !== "") {
    if (!Number.isFinite(Number(cap))) return "Capacity is a number of pounds, like 20000.";
    if (Number(cap) < 0) return "Capacity cannot be negative.";
  }
  const vol = f.volume.trim();
  if (vol !== "") {
    if (!Number.isFinite(Number(vol))) return "Volume is a number of cubic yards, like 10.";
    if (Number(vol) < 0) return "Volume cannot be negative.";
  }
  const pal = f.pallets.trim();
  if (pal !== "" && !/^\d+$/.test(pal)) return "Pallet positions are a whole number, like 8.";
  return "";
}

/** The whole row for the core's create or rewrite. The rewrite is absolute, so every field rides it. */
export function truckBody(f: TruckFields, base: DispatchTruck | undefined, branchId: string, active?: boolean): DispatchTruck {
  const pal = f.pallets.trim();
  return {
    id: base?.id ?? "",
    branch_id: base?.branch_id ?? branchId,
    code: f.code.trim(),
    name: f.name.trim(),
    truck_type: f.type as DispatchTruck["truck_type"],
    capacity_kg: wireFigure(f.capacity, capacityText(base?.capacity_kg), base?.capacity_kg, (lb) => lb / LB_PER_KG),
    capacity_m3: wireFigure(f.volume, volumeText(base?.capacity_m3), base?.capacity_m3, (cy) => cy / CUYD_PER_M3),
    pallet_positions: pal === "" ? null : Number(pal),
    is_active: active ?? base?.is_active ?? true,
  };
}

/** A truck's capacities in the yard's units. An unmeasured truck says so, since the board draws no gauge for it. */
export function truckLine(t: Pick<DispatchTruck, "capacity_kg" | "capacity_m3" | "pallet_positions">): string[] {
  const out: string[] = [];
  if (typeof t.capacity_kg === "number") out.push(fmtWeight(t.capacity_kg));
  if (typeof t.capacity_m3 === "number") out.push(`${fmtNum(toCuYd(t.capacity_m3))} cu yd`);
  if (typeof t.pallet_positions === "number") out.push(`${t.pallet_positions} pallet positions`);
  return out.length > 0 ? out : ["Capacity not measured"];
}

const USER_ID_SHAPE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function validateDriver(f: DriverFields): string {
  if (f.name.trim() === "") return "A driver needs a name.";
  const login = f.login.trim();
  if (login !== "" && !USER_ID_SHAPE.test(login)) return "A staff login id is a long id with dashes. Leave it empty for a driver with no login.";
  return "";
}

export function driverBody(f: DriverFields, base: DispatchDriver | undefined, branchId: string, active?: boolean): DispatchDriver {
  return {
    id: base?.id ?? "",
    branch_id: base?.branch_id ?? branchId,
    name: f.name.trim(),
    phone: f.phone.trim(),
    licence_class: f.licence.trim(),
    user_id: f.login.trim(),
    is_active: active ?? base?.is_active ?? true,
  };
}

export function driverLine(d: Pick<DispatchDriver, "licence_class" | "phone" | "user_id">): string[] {
  return [
    d.licence_class ? `Licence ${d.licence_class}` : "No licence on file",
    d.phone ? d.phone : "No phone",
    d.user_id ? "Has a staff login" : "No staff login",
  ];
}

// ---- will call (part B) -------------------------------------------------------------------------------

/** What a rack tag's three states are called at the counter, as against what the column stores. */
export const WILL_CALL_LABEL: Record<string, string> = {
  ACTIVE: "Live",
  CONFIRMED: "Collected",
  REVOKED: "Cancelled",
};

/** A state in the counter's words. An unmapped state comes back as itself, lowercased, never as an invented phrase. */
export function willCallWord(status: string): string {
  return WILL_CALL_LABEL[status] ?? status.toLowerCase().replace(/_/g, " ");
}

function shortDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" });
}

/** One code in a sentence: how long it is good, who collected, or that it was cancelled. */
export function willCallLine(w: { status: string; expires_at: string; collected_by?: string }): string {
  if (w.status === "ACTIVE") return `Good until ${shortDate(w.expires_at)}`;
  if (w.status === "CONFIRMED") return w.collected_by ? `Collected by ${w.collected_by}` : "Collected";
  if (w.status === "REVOKED") return "Cancelled before it was used";
  return willCallWord(w.status);
}

export function validateOrderRef(order: string): string {
  return order.trim() === "" ? "Enter an order number, like the one on the paperwork, or paste the order id." : "";
}

/** A refused pickup code in words. The same answer comes for a number that never existed and one at another yard. */
export function willCallRefusal(error: string, status: number | null): string {
  if (status === 404) return "No order matches that number at this yard. Check the number on the paperwork.";
  return refusalWords("The pickup code", error, status);
}

export function willCallNotice(minted: { order_no?: string; expires_at: string }): string {
  return `Pickup code printed for ${minted.order_no ?? "the order"}, good until ${shortDate(minted.expires_at)}. It is shown once: print the tag now.`;
}
