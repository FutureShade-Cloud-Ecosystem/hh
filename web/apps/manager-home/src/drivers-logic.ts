// The Drivers view's decisions, with no DOM and no network in sight: which of
// the board's four states a driver's day sits in, what the row's counts and
// words are, and how long ago the last change came. The shapes here are the
// narrow projections drivers-api.ts copies the wire into (the whitelist owns
// the fields; this file owns the reading of them).
//
// Port provenance: the four board states, the run and stop status words and
// the one-day-one-branch shape are the original app's dispatch day board
// (repository hardscapeos_dibbits, read only; see PROVENANCE.md), rebuilt as a
// read-only manager's view on HH's generated client. There is no GPS and no
// position of any kind: the contract has no live position route (verified by
// grep over core/api/openapi.yaml), and "where" here means the state of the
// day, nothing more.

// -- the wire-narrow shapes the logic reads -----------------------------------------------------------

/** One run on today's board, narrowed to what a driver row shows. */
export interface DriverRunView {
  readonly id: string;
  readonly run_no: string;
  readonly status: string;
  readonly driver_id: string;
  readonly driver_name: string;
  readonly truck_id: string;
  readonly truck_code: string;
  readonly stop_count: number;
  readonly created_at: string;
  readonly dispatched_at: string | null;
  readonly completed_at: string | null;
  readonly reopened_at: string;
}

/** One stop of a run, narrowed to its order in the day and its status timestamps. */
export interface DriverStopView {
  readonly id: string;
  readonly run_id: string;
  readonly stop_no: number;
  readonly status: string;
  readonly loaded_at: string | null;
  readonly en_route_at: string | null;
  readonly delivered_at: string | null;
  readonly reopened_at: string | null;
}

/** One driver in the branch's registry, narrowed to the roster facts. */
export interface DriverRegistryView {
  readonly id: string;
  readonly name: string;
  readonly is_active: boolean;
}

/** One driver row on the board: the run, its state, and the counts a manager reads. */
export interface DriverRow {
  readonly run: DriverRunView;
  readonly group: DriverGroupKey;
  readonly current_stop: DriverStopView | null;
  readonly done_stops: number;
  readonly left_stops: number;
  readonly did_not_go: number;
  readonly last_changed_at: string | null;
}

// -- the four states -----------------------------------------------------------------------------------

/**
 * The board's four states, in the order the board draws them. A driver is ON A
 * STOP while a stop is en route, BETWEEN STOPS once one is delivered and the
 * next waits loaded or ready, NOT STARTED while the run is planned, and DONE
 * when the day's driving is over: the run completed or cancelled, or a
 * dispatched run whose every stop has settled while the completion step waits.
 */
export type DriverGroupKey = "on-stop" | "between-stops" | "not-started" | "done";

export const DRIVER_GROUP_ORDER: readonly DriverGroupKey[] = Object.freeze(["on-stop", "between-stops", "not-started", "done"]);

const GROUP_WORD: Readonly<Record<DriverGroupKey, string>> = Object.freeze({
  "on-stop": "On a stop",
  "between-stops": "Between stops",
  "not-started": "Not started",
  done: "Done",
});

export function driverGroupWord(group: DriverGroupKey): string {
  return GROUP_WORD[group];
}

/** The statuses a stop can sit in while a driver still has it ahead of them. */
const UNSETTLED = new Set(["READY", "LOADED", "EN_ROUTE"]);

/**
 * The stop the driver is working towards: the lowest stop number that has not
 * settled. A failed or cancelled stop is settled (it went nowhere); the row's
 * did-not-go count carries it instead.
 */
export function currentStopOf(stops: readonly DriverStopView[]): DriverStopView | null {
  const open = stops.filter((s) => UNSETTLED.has(s.status));
  if (open.length === 0) return null;
  return open.reduce((lowest, s) => (s.stop_no < lowest.stop_no ? s : lowest));
}

/** The day's state for one run and its stops, by the rules under DRIVER_GROUP_ORDER. */
export function driverGroupOf(run: DriverRunView, stops: readonly DriverStopView[]): DriverGroupKey {
  if (run.status === "PLANNED") return "not-started";
  if (run.status === "COMPLETED" || run.status === "CANCELLED") return "done";
  if (run.status === "DISPATCHED") {
    const stop = currentStopOf(stops);
    if (stop === null) return "done";
    return stop.status === "EN_ROUTE" ? "on-stop" : "between-stops";
  }
  // A status outside the contract's closed set (the descriptors hold the wire
  // to PLANNED | DISPATCHED | COMPLETED | CANCELLED) reads as not started, and
  // its status word (below) answers "" so nothing raw ever shows.
  return "not-started";
}

/** The row's three counts: delivered, still ahead of the truck, and did not go. */
export function stopsDoneLeft(stops: readonly DriverStopView[]): { done: number; left: number; didNotGo: number } {
  let done = 0;
  let left = 0;
  let didNotGo = 0;
  for (const s of stops) {
    if (s.status === "DELIVERED") done += 1;
    else if (UNSETTLED.has(s.status)) left += 1;
    else didNotGo += 1;
  }
  return { done, left, didNotGo };
}

/**
 * When the day last moved: the newest status timestamp on the run or any of
 * its stops (dispatched, completed, reopened, loaded, en route, delivered),
 * falling back to the run's own creation for a day that has not started. Null
 * when the wire carries nothing parseable, and the view omits the line.
 */
export function lastChangedAt(run: DriverRunView, stops: readonly DriverStopView[]): string | null {
  let best = 0;
  let bestStamp: string | null = null;
  const stamps = [
    run.dispatched_at,
    run.completed_at,
    run.reopened_at,
    ...stops.flatMap((s) => [s.loaded_at, s.en_route_at, s.delivered_at, s.reopened_at]),
    run.created_at,
  ];
  for (const stamp of stamps) {
    if (typeof stamp !== "string" || stamp === "") continue;
    const at = Date.parse(stamp);
    if (Number.isFinite(at) && at > best) {
      best = at;
      bestStamp = stamp;
    }
  }
  return bestStamp;
}

/** The row: the run, its state, its counts, and when it last moved. */
export function buildDriverRow(run: DriverRunView, stops: readonly DriverStopView[]): DriverRow {
  const counts = stopsDoneLeft(stops);
  return {
    run,
    group: driverGroupOf(run, stops),
    current_stop: currentStopOf(stops),
    done_stops: counts.done,
    left_stops: counts.left,
    did_not_go: counts.didNotGo,
    last_changed_at: lastChangedAt(run, stops),
  };
}

/**
 * The board: the rows grouped in DRIVER_GROUP_ORDER's order, empty groups
 * dropped, and within a group the day that moved most recently first. The
 * sort reads instants, never strings, so mixed wire formats cannot misorder.
 */
export function groupDriverRows(rows: readonly DriverRow[]): readonly { key: DriverGroupKey; rows: readonly DriverRow[] }[] {
  const changed = (row: DriverRow): number => {
    const at = row.last_changed_at === null ? 0 : Date.parse(row.last_changed_at);
    return Number.isFinite(at) ? at : 0;
  };
  return DRIVER_GROUP_ORDER.map((key) => ({
    key,
    rows: rows.filter((row) => row.group === key).sort((a, b) => changed(b) - changed(a)),
  })).filter((group) => group.rows.length > 0);
}

// -- the words only zone --------------------------------------------------------------------------------
//
// No raw enum and no bare instant reaches the screen: every system value
// renders as the words a person reads, and anything this file cannot say
// honestly is omitted by the view, never shown raw.

/** The board's one empty answer, the day with no runs on it at all. */
export const EMPTY_BOARD_WORDS = "No runs on the board today";

/** Run statuses in the original board's own words; an unknown one is omitted, never shown raw. */
const RUN_STATUS_WORD: Readonly<Record<string, string>> = Object.freeze({
  PLANNED: "Planned",
  DISPATCHED: "Dispatched",
  COMPLETED: "Completed",
  CANCELLED: "Cancelled",
});

export function runStatusWord(status: string): string {
  return RUN_STATUS_WORD[status] ?? "";
}

/** Stop statuses in the original board's own words; an unknown one is omitted, never shown raw. */
const STOP_STATUS_WORD: Readonly<Record<string, string>> = Object.freeze({
  READY: "Ready",
  LOADED: "Loaded",
  EN_ROUTE: "En route",
  DELIVERED: "Delivered",
  FAILED: "Failed",
  CANCELLED: "Cancelled",
});

export function stopStatusWord(status: string): string {
  return STOP_STATUS_WORD[status] ?? "";
}

/**
 * How long ago an instant came, as the row's change line reads it: minutes
 * under the hour, hours under two days, days beyond that. A clock skew (a
 * stamp a moment ahead of now) clamps to under a minute; anything unparsable
 * answers "" and the view omits the line.
 */
export function sinceWords(iso: string, nowMs: number): string {
  const at = Date.parse(iso);
  if (!Number.isFinite(at)) return "";
  const span = Math.max(0, nowMs - at);
  const minutes = Math.floor(span / 60000);
  if (minutes < 1) return "under a minute";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(span / 3600000);
  if (hours < 48) return `${hours}h ago`;
  return `${Math.floor(span / 86400000)}d ago`;
}

/**
 * The row's stop line: stop n of m, the driver's place in the day. Nothing
 * when there is no stop ahead of the truck, or the run carries no stops.
 */
export function stopPositionWords(row: DriverRow): string {
  if (row.current_stop === null || row.run.stop_count <= 0) return "";
  return `stop ${row.current_stop.stop_no} of ${row.run.stop_count}`;
}

/**
 * The roster line under the board: the drivers the branch holds who are on no
 * run today, counted against the active roster. Zero idles answers "" and the
 * view draws nothing; the singular verb rides the count of one.
 */
export function idleDriverWords(drivers: readonly DriverRegistryView[], runs: readonly DriverRunView[]): string {
  const onARun = new Set(runs.map((r) => r.driver_id).filter((id) => id !== ""));
  const active = drivers.filter((d) => d.is_active);
  const idle = active.filter((d) => !onARun.has(d.id));
  if (idle.length === 0) return "";
  const verb = idle.length === 1 ? "holds" : "hold";
  return `${idle.length} of the ${active.length} drivers on the roster ${verb} no run today.`;
}
