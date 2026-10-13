// The Drivers view's one door to the core: the day board, the driver registry
// and the truck registry, over the generated api-client, answering a Result
// and never throwing. The same read wall the dispatch surface serves
// (x-access dispatch-board, level read): the role gate decides, this door
// never re-decides, and a refusal arrives in the core's own words.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the Drivers view shows. A field the
// projection does not name is dropped at this door and never reaches a
// screen. There is no money here and no position of any kind: the contract
// has no live position route, and this view reads the state of the day.
//
// Port provenance: the board shape (one branch, one day, its runs and stops)
// is the original app's dispatch day board (repository hardscapeos_dibbits,
// read only; see PROVENANCE.md), rebuilt on HH's generated client and
// narrowed to a read-only manager's view.
import { ApiClient, staffDispatch, type Result } from "@hardscapehouse/api-client";
import { buildDriverRow, type DriverRegistryView, type DriverRow, type DriverRunView, type DriverStopView } from "./drivers-logic.js";

// -- the projected shapes beyond the logic's own ------------------------------------------------------

/** One truck in the branch's registry, narrowed to the words a row shows. */
export interface DriverTruckView {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly truck_type: string;
  readonly is_active: boolean;
}

export interface DispatchBoardView {
  readonly branch_id: string;
  readonly date: string;
  readonly runs: readonly DriverRunView[];
  readonly stops: readonly DriverStopView[];
  readonly unassigned: readonly DriverStopView[];
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);
const maybeText = (value: unknown): string | null => (typeof value === "string" ? value : null);
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);

export function toDispatchRun(raw: unknown): DriverRunView {
  const r = bag(raw);
  return {
    id: text(r.id),
    run_no: text(r.run_no),
    status: text(r.status),
    driver_id: text(r.driver_id),
    driver_name: text(r.driver_name),
    truck_id: text(r.truck_id),
    truck_code: text(r.truck_code),
    stop_count: num(r.stop_count),
    created_at: text(r.created_at),
    dispatched_at: maybeText(r.dispatched_at),
    completed_at: maybeText(r.completed_at),
    reopened_at: text(r.reopened_at),
  };
}

export function toDispatchStop(raw: unknown): DriverStopView {
  const r = bag(raw);
  return {
    id: text(r.id),
    run_id: text(r.run_id),
    stop_no: num(r.stop_no),
    status: text(r.status),
    loaded_at: maybeText(r.loaded_at),
    en_route_at: maybeText(r.en_route_at),
    delivered_at: maybeText(r.delivered_at),
    reopened_at: maybeText(r.reopened_at),
  };
}

export function toDispatchBoard(raw: unknown): DispatchBoardView {
  const r = bag(raw);
  return {
    branch_id: text(r.branch_id),
    date: text(r.date),
    runs: list(r.runs).map(toDispatchRun),
    stops: list(r.stops).map(toDispatchStop),
    unassigned: list(r.unassigned).map(toDispatchStop),
  };
}

export function toDispatchDriver(raw: unknown): DriverRegistryView {
  const r = bag(raw);
  return { id: text(r.id), name: text(r.name), is_active: r.is_active === true };
}

export function toDispatchTruck(raw: unknown): DriverTruckView {
  const r = bag(raw);
  return {
    id: text(r.id),
    code: text(r.code),
    name: text(r.name),
    truck_type: text(r.truck_type),
    is_active: r.is_active === true,
  };
}

// -- the rows -------------------------------------------------------------------------------------------

/**
 * One row per run on the board, from that run's own stops only. The
 * unassigned lane's stops are nobody's row (no driver holds them), and the
 * stops read in their stop order.
 */
export function rowsForBoard(runs: readonly DriverRunView[], stops: readonly DriverStopView[]): readonly DriverRow[] {
  return runs.map((run) =>
    buildDriverRow(
      run,
      stops
        .filter((s) => s.run_id === run.id)
        .slice()
        .sort((a, b) => a.stop_no - b.stop_no),
    ),
  );
}

/**
 * The row's truck words: the run's own code when the board carries one, else
 * the registry's code for the run's truck, else nothing and the view omits
 * the line.
 */
export function truckWords(run: Pick<DriverRunView, "truck_code" | "truck_id">, trucks: readonly DriverTruckView[]): string {
  if (run.truck_code !== "") return run.truck_code;
  return trucks.find((t) => t.id === run.truck_id)?.code ?? "";
}

// -- the calls ------------------------------------------------------------------------------------------

/** Every call the Drivers view makes. All three reads sit behind the same dispatch-board gate. */
export class DriversApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /**
   * The day board for one branch: its runs and stops in one read. The date is
   * never sent: the core answers its own today, so this view never computes a
   * day from the browser's clock. The branch comes from the caller's own
   * grants (the branches route), never from a person's typing.
   */
  async board(branchId: string): Promise<Result<DispatchBoardView>> {
    const answer = await staffDispatch.dispatchGetBoard(this.#client, { branch_id: branchId });
    return answer.ok ? { ok: true, value: toDispatchBoard(answer.value) } : answer;
  }

  /** The branch's driver registry, the roster the idle line counts against. */
  async drivers(branchId: string): Promise<Result<readonly DriverRegistryView[]>> {
    const answer = await staffDispatch.dispatchListDrivers(this.#client, { branch_id: branchId, limit: 100 });
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return { ok: true, value: list(page.items).map(toDispatchDriver) };
  }

  /** The branch's truck registry, the code fallback for a run whose own code is empty. */
  async trucks(branchId: string): Promise<Result<readonly DriverTruckView[]>> {
    const answer = await staffDispatch.dispatchListTrucks(this.#client, { branch_id: branchId, limit: 100 });
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return { ok: true, value: list(page.items).map(toDispatchTruck) };
  }
}
