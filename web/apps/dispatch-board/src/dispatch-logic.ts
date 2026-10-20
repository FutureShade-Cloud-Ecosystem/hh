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
  DispatchFreightRaiseView,
  DispatchLoadStop,
  DispatchPolicy,
  DispatchPolicyChange,
  DispatchScanLoad,
  DispatchScanTrail,
  DispatchScheduleInput,
  DispatchSplitInput,
  DispatchSplitResult,
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
  return Math.round(toWire(Number(text)) * 100) / 100;
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
  return `Pickup code made for ${minted.order_no ?? "the order"}, good until ${shortDate(minted.expires_at)}. It is shown once: print the tag now.`;
}

// ---- the dispatch rules (part B) --------------------------------------------------------------------------

/** Each knob named the way the controls and the history name it, the unit riding the name when the value is a bare number. */
export const DISPATCH_RULE_WORDS: Readonly<Record<string, string>> = Object.freeze({
  commit_prompt_hours: "Prompt to commit, hours ahead",
  reopen_policy: "Who may reopen a committed load",
  load_value_visibility: "Who sees a load's dollar value",
  commit_horizon_weeks: "How far the board plans, in weeks",
  working_view_days: "How far the daily view reaches, in days",
  freight_split_mode: "How freight is split across loads",
  driver_session_hours: "How long a driver stays signed in, in hours",
});

export interface PolicyChoice {
  readonly value: string;
  readonly label: string;
}

export const REOPEN_CHOICES: readonly PolicyChoice[] = Object.freeze([
  { value: "ANYONE", label: "Anyone who could have committed it" },
  { value: "MANAGER", label: "A dispatch manager" },
  { value: "NOBODY", label: "Nobody, a commitment is final" },
]);

export const LOAD_VALUE_CHOICES: readonly PolicyChoice[] = Object.freeze([
  { value: "MANAGER_UP", label: "Managers and above" },
  { value: "ALL_STAFF", label: "Every staff member" },
  { value: "HIDDEN", label: "Nobody" },
]);

const FREIGHT_SPLIT_WORDS: Readonly<Record<string, string>> = Object.freeze({
  EVEN: "Evenly across the loads",
  MANUAL: "By hand, per load",
});

const VALUE_WORDS: Readonly<Record<string, Readonly<Record<string, string>>>> = Object.freeze({
  reopen_policy: Object.fromEntries(REOPEN_CHOICES.map((c) => [c.value, c.label])),
  load_value_visibility: Object.fromEntries(LOAD_VALUE_CHOICES.map((c) => [c.value, c.label])),
  freight_split_mode: FREIGHT_SPLIT_WORDS,
});

/** A stored value in words. An unmapped value comes back as itself, lowercased, never as an invented phrase. */
function valueWord(field: string, value: string): string {
  const mapped = VALUE_WORDS[field]?.[value];
  if (mapped !== undefined) return mapped;
  return /^\d+$/.test(value) ? value : value.toLowerCase().replace(/_/g, " ");
}

export interface PolicyRow {
  readonly key: keyof DispatchPolicy;
  readonly label: string;
  readonly word: string;
  readonly note: string;
}

const count = (n: number, one: string, many: string): string => `${n} ${n === 1 ? one : many}`;

/** The rules as the read-out draws them: the name, the value with its unit, and the one sentence that keeps a rule from being confused with its neighbour. */
export function policyRows(p: DispatchPolicy): PolicyRow[] {
  return [
    { key: "commit_prompt_hours", label: "Prompt to commit", word: count(p.commit_prompt_hours, "hour", "hours"), note: "How far ahead of a delivery the board asks for the date to be committed." },
    { key: "reopen_policy", label: "Who may reopen a committed load", word: valueWord("reopen_policy", p.reopen_policy), note: "A committed load is a promise. This says who may take it back." },
    { key: "load_value_visibility", label: "Who sees a load's dollar value", word: valueWord("load_value_visibility", p.load_value_visibility), note: "A figure the core is told not to send is not on the board at all." },
    { key: "commit_horizon_weeks", label: "How far the board plans", word: count(p.commit_horizon_weeks, "week", "weeks"), note: "How far ahead a delivery may be promised." },
    { key: "working_view_days", label: "How far the daily view reaches", word: count(p.working_view_days, "day", "days"), note: "How many days the daily board shows from its date. It is not how far ahead a delivery may be promised." },
    { key: "freight_split_mode", label: "How freight is split across loads", word: valueWord("freight_split_mode", p.freight_split_mode), note: "Set with the freight split, not here." },
    { key: "driver_session_hours", label: "How long a driver stays signed in", word: count(p.driver_session_hours, "hour", "hours"), note: "A company rule: it is read from the dealer's default, so it is set by the dealer's administrators, not for one yard." },
  ];
}

export interface PolicyFields {
  readonly prompt: string;
  readonly reopen: string;
  readonly visibility: string;
  readonly horizon: string;
  readonly view: string;
}

/** The form's own check: whole numbers only. The bounds live in one place, the core's, and its words are what the person reads. */
export function validatePolicyFields(f: PolicyFields): string {
  const whole = (text: string): boolean => /^\d+$/.test(text.trim());
  if (!whole(f.prompt)) return "Hours ahead is a whole number, like 48.";
  if (!whole(f.horizon)) return "Weeks is a whole number, like 4.";
  if (!whole(f.view)) return "Days is a whole number, like 14.";
  return "";
}

/** The whole policy for the core's absolute write: the five edited knobs, the other two as they were, the yard it is for. */
export function policyBody(f: PolicyFields, base: DispatchPolicy, branchId: string): DispatchPolicy {
  return {
    branch_id: branchId,
    commit_prompt_hours: Number(f.prompt.trim()),
    reopen_policy: f.reopen as DispatchPolicy["reopen_policy"],
    load_value_visibility: f.visibility as DispatchPolicy["load_value_visibility"],
    commit_horizon_weeks: Number(f.horizon.trim()),
    working_view_days: Number(f.view.trim()),
    freight_split_mode: base.freight_split_mode,
    driver_session_hours: base.driver_session_hours,
  };
}

/** A refused rule: the core's sentence with its column names turned into the rules' names. A bare Forbidden is the setters list. */
export function policyRefusal(error: string, status: number | null): string {
  if (status === 403 && /^forbidden\.?$/i.test(plainSentence(error))) {
    return "You can read the dispatch rules, but your dealer names who may change them.";
  }
  let words = refusalWords("Saving the rules", error, status);
  const names: Record<string, string> = { ...DISPATCH_RULE_WORDS, cost_visibility: DISPATCH_RULE_WORDS.load_value_visibility ?? "" };
  for (const [key, name] of Object.entries(names)) words = words.split(key).join(name);
  return words;
}

export interface HistoryRow {
  readonly rule: string;
  readonly was: string;
  readonly now: string;
  readonly who: string;
  readonly when: string;
}

/** One change in the same words the controls use. A field the screen has no words for reads as itself. */
export function historyRow(c: DispatchPolicyChange): HistoryRow {
  const when = c.when ? new Date(c.when) : null;
  return {
    rule: DISPATCH_RULE_WORDS[c.field] ?? c.field.replace(/_/g, " "),
    was: valueWord(c.field, c.before),
    now: valueWord(c.field, c.after),
    who: c.who && c.who.trim() !== "" ? c.who : "Somebody",
    when: when !== null && !Number.isNaN(when.getTime())
      ? when.toLocaleString("en-US", { month: "short", day: "numeric", year: "numeric", hour: "numeric", minute: "2-digit" })
      : "",
  };
}

/** Why the trail is not on screen. The trail is a manager's; every other fault is the core's own sentence. */
export function historyFaultWords(error: string, status: number | null): string {
  if (status === 403) return "Managers read the change history. It is not shown to this login.";
  return refusalWords("The change history", error, status);
}

// ---- the manifest and the scan trail (part B) ------------------------------------------------------------

export function manifestRefusal(error: string, status: number | null): string {
  if (status === 403) return "That run belongs to another yard. Its manifest is not available here.";
  if (status === 404) return "There is no such run.";
  return refusalWords("The manifest", error, status);
}

export function manifestNotice(runNo: string): string {
  return `Manifest for ${runNo} sent to the printer window, and the print is recorded.`;
}

/** The five checkpoints a pallet passes, in order. Mirrors the core's sequence. */
export const SCAN_SEQUENCE = ["RECEIVED", "YARD", "PICKED", "IN_TRANSIT", "DELIVERED"] as const;

const STAGE_LABEL: Record<string, string> = {
  RECEIVED: "Received",
  YARD: "In the yard",
  PICKED: "Picked",
  IN_TRANSIT: "On the road",
  DELIVERED: "Delivered",
};

/** A stage in plain words. An unmapped stage comes back as itself, lowercased, never as an invented phrase. */
export function scanStageLabel(stage: string): string {
  return STAGE_LABEL[stage] ?? stage.toLowerCase().replace(/_/g, " ");
}

/**
 * The one line answering "where is this order". It says all delivered only when the core does:
 * an order of three pallets with two delivered is not a delivered order, and a summary that rounds up
 * is the most dangerous sentence this screen could print.
 */
export function trailSummary(trail: DispatchScanTrail | null | undefined): string {
  if (!trail || trail.loads.length === 0) return "No scans yet";
  const n = trail.loads.length;
  const noun = n === 1 ? "load" : "loads";
  if (trail.complete) return `${n} ${noun}, all delivered`;
  const delivered = trail.loads.filter((l) => l.furthest === "DELIVERED").length;
  return `${delivered} of ${n} ${noun} delivered`;
}

/** One pallet's line: how far it got, whether it was signed for, and when it was last scanned. */
export function scanLoadLine(l: DispatchScanLoad): string {
  const when = new Date(l.last_scanned_at);
  const parts = [scanStageLabel(l.furthest).toLowerCase()];
  if (l.pod_signature_ref) parts.push("signed for");
  if (!Number.isNaN(when.getTime())) {
    parts.push(`last scanned ${when.toLocaleString("en-US", { month: "short", day: "numeric", year: "numeric", hour: "numeric", minute: "2-digit" })}`);
  }
  return `${l.load_ref}: ${parts.join(", ")}`;
}

// ---- split freight (part C) ------------------------------------------------------------------------------
//
// Money rules, from the state note: a figure is drawn only if the core sent it; cents are integers end
// to end; a typed amount becomes cents by its digits, never through a float; the board computes no
// share, no total and no difference (the core divides, checks the sum and subtracts).

/** Typed dollars to integer cents by digits. "" is nothing typed (null); anything that is not dollars and cents is undefined. */
export function dollarsToCents(text: string): number | null | undefined {
  const t = text.trim().replace(/^\$/, "");
  if (t === "") return text.trim() === "" ? null : undefined;
  if (!/^(\d{1,3}(,\d{3})+|\d+)(\.\d{1,2})?$/.test(t)) return undefined;
  const [whole = "0", frac = ""] = t.replace(/,/g, "").split(".");
  return Number(whole) * 100 + Number(frac.padEnd(2, "0"));
}

/** Integer cents as dollars, by integer arithmetic. */
export function centsText(cents: number): string {
  const neg = cents < 0 ? "-" : "";
  const abs = Math.abs(cents);
  const whole = Math.floor(abs / 100).toLocaleString("en-US");
  return `${neg}$${whole}.${String(abs % 100).padStart(2, "0")}`;
}

export interface SplitFields {
  readonly loads: string;
  /** The order's whole freight after the split, dollars, optional (empty keeps it as it is). */
  readonly freight: string;
  /** Each truck's share by hand, dollars, comma separated, optional. */
  readonly amounts: string;
}

const shares = (text: string): string[] => text.split(",").map((p) => p.trim()).filter((p, i, all) => !(p === "" && all.length === 1));

/** The form's own check: a whole number of trucks and amounts that are dollars. The bounds and the sum are the core's. */
export function validateSplit(f: SplitFields): string {
  if (!/^\d+$/.test(f.loads.trim())) return "Trucks is a whole number, like 2.";
  if (dollarsToCents(f.freight) === undefined) return "The freight is dollars and cents, like 150.00.";
  if (f.amounts.trim() !== "" && shares(f.amounts).some((p) => typeof dollarsToCents(p) !== "number")) return "Each share is dollars and cents, like 100.00, separated by commas.";
  return "";
}

export function splitBody(f: SplitFields, orderId: string): DispatchSplitInput {
  const body: DispatchSplitInput = { order_id: orderId, loads: Number(f.loads.trim()) };
  const freight = dollarsToCents(f.freight);
  if (typeof freight === "number") body.freight_cents = freight;
  if (f.amounts.trim() !== "") body.amounts = shares(f.amounts).map((p) => dollarsToCents(p) as number);
  return body;
}

/** What the core did, from what it sent: the order's freight and each truck's share only where a figure came. */
export function splitNotice(r: DispatchSplitResult): string {
  const n = r.loads.length;
  const trucks = `${n} ${n === 1 ? "truck" : "trucks"}`;
  if (r.parked) return "Nothing changed. The new freight is waiting for approval; the loads and the freight are as they were.";
  const each = r.loads.map((l) => l.freight_cents);
  const known = each.length > 0 && each.every((c): c is number => typeof c === "number");
  if (n === 1) return typeof r.freight_cents === "number" ? `Freight ${centsText(r.freight_cents)} on 1 truck.` : "Freight on 1 truck.";
  if (!known) return `Freight split across ${trucks}.`;
  return `Freight ${centsText(r.freight_cents)} split across ${trucks}: ${(each as number[]).map(centsText).join(", ")}.`;
}

const RAISE_WORD: Record<string, string> = { PARKED: "Waiting for approval", APPLIED: "Approved and applied", REFUSED: "Refused" };

export function raiseStateWord(state: string): string {
  return RAISE_WORD[state] ?? state.toLowerCase().replace(/_/g, " ");
}

/** A parked ask's lines. The figures ride only when the core sent them; an absent figure is absent. */
export function raiseLine(v: DispatchFreightRaiseView): string[] {
  const who = v.customer_name ? ` for ${v.customer_name}` : "";
  const out = [`${v.order_no ?? "The order"}${who}`, `${v.loads} ${v.loads === 1 ? "truck" : "trucks"}, asked by ${v.asked_by}`];
  const m = v.money;
  if (m) {
    out.push(`Quoted ${centsText(m.quoted_freight_cents)}, charged now ${centsText(m.current_freight_cents)}, asked for ${centsText(m.proposed_freight_cents)}`);
    out.push(`${centsText(m.delta_cents)} above the quote`);
  }
  return out;
}

/** A refused split in the core's own words. A lost reply is not a refusal: asking the same again is safe, because the core converges. */
export function splitRefusal(error: string, status: number | null): string {
  if (status === null || status === 0) {
    return "The board could not reach the server, so it may or may not have gone through. Read the load again, then ask again: asking again is safe.";
  }
  return refusalWords("The split", error, status);
}

// ---- the redesigned board (WALK-C16) ---------------------------------------------------------------
//
// The 16th change redraws the board on three ideas: a TIME AXIS the day rides left to right, a
// RUN DETAIL drawer that opens when a run is named, and a PROBLEMS strip the dispatchers hold in
// their peripheral vision. Each helper is small and honest: a constant the screen fits, a word
// the screen prints, a list the screen lays out, or a check the screen does. The DOM never sees
// the raw core fields; the views call these helpers and render what they say.

/** The hours the time axis spans from start of day. Twelve covers any San Diego yard's day: a
 *  truck that leaves at 5 in the morning and is back before 5 in the afternoon still fits. */
export const TIME_AXIS_HOURS: readonly number[] = Object.freeze([
  5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
]);

/** The Moffett forklift weight, in pounds. A Moffett rides on the back of a delivery truck: the
 *  load's net payload is the truck's capacity MINUS the Moffett's weight, never its full
 *  capacity. The house rule (this page) says the figure is named once, here. */
export const MOFFETT_WEIGHT_LB = 4500;

/** True when the truck carries a Moffett, by the dealer's own flag. Absent flag is honest: a
 *  truck without a Moffett is the default and the gauge reads the full capacity. The contract
 *  does not yet ship the flag, so this helper reads the truck type: a BOOM truck is the kind
 *  that always rides with a Moffett in the demo tenant's San Diego yard. A dealer with a
 *  different rule sets the flag at the core and the helper picks it up. */
export function carriesMoffett(t: { truck_type?: string; carries_moffett?: boolean } | undefined | null): boolean {
  if (t === null || t === undefined) return false;
  if (typeof t.carries_moffett === "boolean") return t.carries_moffett;
  return t.truck_type === "BOOM";
}

/** Net payload in pounds: the truck's capacity in pounds minus the Moffett's weight when the
 *  truck carries one. A capacity the core holds as kilograms is converted here for the
 *  display only (nothing ever goes back to the wire in this unit). */
export function netPayloadLb(capacityKg: number | null | undefined, hasMoffett: boolean): number {
  if (typeof capacityKg !== "number" || !Number.isFinite(capacityKg)) return 0;
  const cap = capacityKg * LB_PER_KG;
  return Math.max(0, cap - (hasMoffett ? MOFFETT_WEIGHT_LB : 0));
}

/** Net payload as the gauge and the card name it: "7,250 lb net" when the Moffett is on, the
 *  full capacity when not. An absent capacity is words, never zero. */
export function netPayloadWords(capacityKg: number | null | undefined, hasMoffett: boolean): string {
  if (typeof capacityKg !== "number" || !Number.isFinite(capacityKg)) return "Capacity not measured";
  return hasMoffett
    ? `${fmtNum(netPayloadLb(capacityKg, true))} lb net (after Moffett)`
    : `${fmtNum(netPayloadLb(capacityKg, false))} lb payload`;
}

/** The kind of problem a truck row carries. Each kind maps to a one-sentence explanation the
 *  strip reads in its own words, so the strip never invents its own sentences. */
export type BoardProblem =
  | { readonly kind: "over-weight"; readonly runNo: string; readonly figure: string }
  | { readonly kind: "missing-crew"; readonly runNo: string; readonly what: "truck" | "driver" | "both" }
  | { readonly kind: "due-to-commit"; readonly runNo: string; readonly daysToCommit: number }
  | { readonly kind: "moffett-tight"; readonly runNo: string; readonly afterWeight: string }
  | { readonly kind: "no-runs"; readonly runNo: "" };

const PROBLEM_WORDS: Record<BoardProblem["kind"], (p: BoardProblem) => string> = {
  "over-weight": (p) => `${p.kind === "over-weight" ? p.runNo : ""} is over weight: ${(p as { kind: "over-weight"; figure: string }).figure}`,
  "missing-crew": (p) => {
    const pp = p as Extract<BoardProblem, { kind: "missing-crew" }>;
    return pp.what === "both" ? `${pp.runNo} is missing a truck and a driver.` : `${pp.runNo} is missing a ${pp.what}.`;
  },
  "due-to-commit": (p) => {
    const pp = p as Extract<BoardProblem, { kind: "due-to-commit" }>;
    return `${pp.runNo} is due to commit in ${pp.daysToCommit} ${pp.daysToCommit === 1 ? "day" : "days"}.`;
  },
  "moffett-tight": (p) => {
    const pp = p as Extract<BoardProblem, { kind: "moffett-tight" }>;
    return `${pp.runNo} carries a Moffett: payload after the Moffett is ${pp.afterWeight}.`;
  },
  "no-runs": () => "No trucks on the board yet. Open one to start the day.",
};

/** One problem as a sentence. The strip's tone is one line per row, the words fit a phone. */
export function boardProblemLine(p: BoardProblem): string {
  return PROBLEM_WORDS[p.kind](p);
}

/** The board's problems: over-weight trucks, missing crews, due-to-commit asks, tight Moffetts,
 *  and the empty-board note. The list is in the order the dispatcher reads them: a missing
 *  crew first (it blocks dispatch), over-weight second (it blocks load), then the asks, then
 *  the Moffett advisory. The helper is pure: a fresh array each call, never the same
 *  reference twice, so a re-render can compare by content. */
export function boardProblems(view: DispatchLoadBoardView | null): readonly BoardProblem[] {
  if (view === null) return [];
  const out: BoardProblem[] = [];
  if (view.loads.length === 0) {
    out.push({ kind: "no-runs", runNo: "" });
    return out;
  }
  for (const row of view.loads) {
    const runNo = row.run.run_no;
    if (!row.run.truck_id && !row.run.driver_id) {
      out.push({ kind: "missing-crew", runNo, what: "both" });
    } else if (!row.run.truck_id) {
      out.push({ kind: "missing-crew", runNo, what: "truck" });
    } else if (!row.run.driver_id) {
      out.push({ kind: "missing-crew", runNo, what: "driver" });
    }
    if (row.gauge.band === "RED") {
      const figure = gaugeLabel("Weight", row.gauge.weight_kg, row.gauge.capacity_kg, weightUnit(row.gauge.capacity_kg, row.gauge.weight_kg));
      out.push({ kind: "over-weight", runNo, figure });
    }
    if (row.commit.due_to_commit) {
      out.push({ kind: "due-to-commit", runNo, daysToCommit: 1 });
    }
  }
  return out;
}

/** The yard's load order. PLANNED trucks come first (the day's plan), then DISPATCHED (on the
 *  road), then COMPLETED and CANCELLED (the day's record). Within a status the rows ride the
 *  core's order on the wire (the stub already returns them by run_no, which matches the day's
 *  plan). The helper is the cross-status sort only. */
export function yardLoadOrder(rows: readonly DispatchLoadRow[]): readonly DispatchLoadRow[] {
  const rank: Record<string, number> = { PLANNED: 0, DISPATCHED: 1, COMPLETED: 2, CANCELLED: 3 };
  return [...rows].sort((a, b) => {
    const ra = rank[a.run.status] ?? 99;
    const rb = rank[b.run.status] ?? 99;
    if (ra !== rb) return ra - rb;
    return a.run.run_no.localeCompare(b.run.run_no);
  });
}

/** A stop's deck slot: the index it occupies on the time axis. A slot is the hour the stop is
 *  scheduled to be picked up; an absent hour places the stop at the end of the row so it
 *  never collides with a real one. The helper is pure: same input, same slots. */
export function deckSlotsForStops(
  stops: readonly Pick<DispatchLoadStop, "scheduled_date" | "stop_no">[],
  today: string,
): Map<number, number> {
  const slots = new Map<number, number>();
  let idx = 0;
  for (const stop of stops) {
    if (stop.scheduled_date === today) idx += 1;
    slots.set(stop.stop_no, idx);
  }
  return slots;
}

/** One suggested run for a load: the same words assignTargets prints, but ranked for the
 *  suggest-runs panel. The panel picks the first three. */
export interface SuggestedRun {
  readonly runId: string;
  readonly runNo: string;
  readonly truckCode: string;
  readonly reason: string;
}

export function suggestRunsForLoad(
  view: DispatchLoadBoardView,
  stop: DispatchLoadStop,
): readonly SuggestedRun[] {
  const targets = assignTargets(view, stop);
  const out: SuggestedRun[] = [];
  for (const t of targets) {
    if (t.blocked !== "") continue;
    out.push({ runId: t.runId, runNo: t.runNo, truckCode: t.truckCode, reason: targetName(t) });
  }
  return out.slice(0, 3);
}

// ---- dispatch confirm and commit note (UX-30) ---------------------------------------------------

/** Shared commit rule shown at the "Put on a truck" control and on the Schedule form. */
export const PUT_ON_TRUCK_COMMIT_NOTE = "Putting a load on a truck commits the order.";

/**
 * The confirm sheet before a run goes out: names the truck, the driver as the record names
 * them, and the stop count, states what the core dispatch handler (Service.DispatchRun in
 * core/internal/dispatch/service.go:930-937) actually does (sends the run to the driver at
 * once and marks its ready loads loaded), and names any stop on the run that is held for
 * payment using the same hold record the card's chip reads.
 */
export function dispatchConfirmWords(
  run: { readonly run_no?: string; readonly truck_code?: string; readonly driver_name?: string; readonly stop_count?: number } | null | undefined,
  stops?: readonly { readonly id: string; readonly order_no?: string; readonly status: string }[],
  holds?: ReadonlyMap<string, Pick<DispatchStopPaymentHold, "held" | "reason" | "amount_due_cents">>,
): { readonly title: string; readonly body: string; readonly holds: readonly string[] } {
  const truck = (run?.truck_code ?? "").trim() || (run?.run_no ?? "").trim() || "this truck";
  const driver = (run?.driver_name ?? "").trim() || "the driver";
  const n = run?.stop_count ?? (stops?.length ?? 0);
  const ready = stops !== undefined ? stops.filter((s) => s.status === "READY").length : n;
  const stopsWord = `${n} ${n === 1 ? "stop" : "stops"}`;
  const readyWord = `${ready} ready ${ready === 1 ? "load" : "loads"}`;
  const heldLines: string[] = [];
  for (const s of stops ?? []) {
    const hold = holds?.get(s.id);
    if (holdWords(hold) !== "") {
      const orderNo = (s.order_no ?? "").trim() || "This order";
      heldLines.push(`${orderNo} is held for payment and cannot be released until it is paid.`);
    }
  }
  return {
    title: `Send ${truck} with ${driver} and ${stopsWord}?`,
    body: `The driver sees the run at once. Sending it marks its ${readyWord} loaded. A sent run cannot be called back here.`,
    holds: heldLines,
  };
}

