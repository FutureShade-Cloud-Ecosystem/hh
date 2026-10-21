// The board's one door to the core: every read and write the dispatch board makes, over the
// generated client, answering a Result and never throwing. Every route here is a route the core
// already serves (core/internal/dispatch). The board draws no money of its own: a figure the core
// sends is formatted where it is shown, and a figure it leaves out is absent.
//
// Reads: the branches the caller reaches, the load board, the day board, the trucks and drivers (with
// the ones out of use for the registry),
// the procurement gaps, the calendar and a run's payment holds.
// Writes: schedule an order's delivery, open a run, crew it, dispatch it, cancel it, complete it, reopen it, clear the reopen
// marker, move its date; put a stop on a run or take it off, commit its date, reopen it, move its
// date, advance it along the lifecycle; load many stops in one call; and ask the auto assign seam.
import {
  staffBranches,
  staffDispatch,
  staffSettings,
  type ApiClient,
  type Branch,
  type DispatchBoard,
  type DispatchCalendarDayPage,
  type DispatchCreatedRef,
  type DispatchDriver,
  type DispatchDriverPage,
  type DispatchFreightRaiseView,
  type DispatchGapPage,
  type DispatchLoadBoardView,
  type DispatchLoadOutcomeList,
  type DispatchMintedPickup,
  type DispatchPolicy,
  type DispatchPolicyChangePage,
  type DispatchPaymentHoldPage,
  type DispatchPrintJobRef,
  type DispatchRescheduleResult,
  type DispatchRun,
  type DispatchRunDetail,
  type DispatchScanTrail,
  type DispatchScheduleInput,
  type DispatchSplitInput,
  type DispatchSplitResult,
  type DispatchStop,
  type DispatchTruck,
  type DispatchTruckPage,
  type DispatchWillCallPage,
  type DispatchWillCallRevokeResult,
  type Result,
} from "@hardscapehouse/api-client";
import { houseZone, serveHouseZoneFrom } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";

/** A stalling connection must not hold the board: the zone read carries a short timeout. */
const ZONE_READ_TIMEOUT_MS = 4_000;

const zoneReads = new WeakMap<ApiClient, Promise<void>>();
const zoneWatchers = new WeakMap<ApiClient, Set<() => void>>();

/**
 * The dealer's business day zone is served on the dispatch-board surface's config read
 * (business_zone) and held by the kit; a read that fails keeps the default, so every screen still
 * names a time, never a blank screen. One read serves a session: the promise is kept per client and
 * dropped only when the read fails, so the next call retries.
 */
export function serveDispatchZone(client: ApiClient): Promise<void> {
  const held = zoneReads.get(client);
  if (held !== undefined) return held;
  const read = staffSettings
    .settingsGetSurfaceConfig(client, "dispatch-board", {}, { timeoutMs: ZONE_READ_TIMEOUT_MS })
    .then((config) => {
      if (!config.ok) {
        zoneReads.delete(client);
        return;
      }
      const before = houseZone();
      serveHouseZoneFrom(config.value);
      if (houseZone() !== before) zoneWatchers.get(client)?.forEach((repaint) => repaint());
    });
  zoneReads.set(client, read);
  return read;
}

/** Calls back when a zone that lands differs from the one in force, so the board's times are painted again. */
export function watchDispatchZone(client: ApiClient, repaint: () => void): void {
  const held = zoneWatchers.get(client) ?? new Set<() => void>();
  held.add(repaint);
  zoneWatchers.set(client, held);
}

/** Starts the zone read beside a data read, never ahead of it: the board shows at once. */
export function serveDispatchZoneBeside(client: ApiClient): void {
  void serveDispatchZone(client);
}

export class DispatchApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** Repaint the screen when the served zone lands and differs from the one in force. */
  watchZone(repaint: () => void): void {
    watchDispatchZone(this.#client, repaint);
  }

  /** The branches the caller reaches: a dispatcher homed at one yard reaches one. */
  branches(): Promise<Result<Branch[]>> {
    serveDispatchZoneBeside(this.#client);
    return staffBranches.branchesList(this.#client);
  }

  loadBoard(branchId: string, date: string): Promise<Result<DispatchLoadBoardView>> {
    serveDispatchZoneBeside(this.#client);
    return staffDispatch.dispatchGetLoadBoard(this.#client, { branch_id: branchId, date });
  }

  dayBoard(branchId: string, date: string): Promise<Result<DispatchBoard>> {
    serveDispatchZoneBeside(this.#client);
    return staffDispatch.dispatchGetBoard(this.#client, { branch_id: branchId, date });
  }

  /** The pick list: trucks in use. The registry asks for the ones out of use too. */
  trucks(branchId: string, includeInactive = false): Promise<Result<DispatchTruckPage>> {
    return staffDispatch.dispatchListTrucks(this.#client, includeInactive ? { branch_id: branchId, include_inactive: true } : { branch_id: branchId });
  }

  drivers(branchId: string, includeInactive = false): Promise<Result<DispatchDriverPage>> {
    return staffDispatch.dispatchListDrivers(this.#client, includeInactive ? { branch_id: branchId, include_inactive: true } : { branch_id: branchId });
  }

  // ---- split freight (money adjacent: cents only, never queued, the core divides and gates)

  /** Divides an order's freight across its trucks, or parks a raise above the quote for an approver. Asking the same twice converges. */
  splitFreight(body: DispatchSplitInput): Promise<Result<DispatchSplitResult>> {
    return staffDispatch.dispatchSplitFreight(this.#client, body);
  }

  /** A parked ask as the core serves it now. Its money is absent for a caller the value policy refuses. */
  freightRaise(id: string): Promise<Result<DispatchFreightRaiseView>> {
    return staffDispatch.dispatchGetFreightRaise(this.#client, id);
  }

  // ---- the manifest and the scan trail

  /** The core's own manifest document, as the page it prints. */
  runManifest(runId: string): Promise<Result<string>> {
    return staffDispatch.dispatchGetRunManifest(this.#client, runId);
  }

  /** Records a manifest print in the print queue: who printed which run's manifest and when. */
  recordManifestPrint(runId: string): Promise<Result<DispatchPrintJobRef>> {
    return staffDispatch.dispatchPrintRunManifest(this.#client, runId);
  }

  /** Where an order's pallets are, by stage. The trail is read, never written, from this board. */
  scanTrail(orderId: string): Promise<Result<DispatchScanTrail>> {
    return staffDispatch.dispatchGetScanTrail(this.#client, { order_id: orderId });
  }

  // ---- will call: pickup codes for an order on the rack (the order is named by the number on the paperwork)

  willCalls(orderRef: string): Promise<Result<DispatchWillCallPage>> {
    return staffDispatch.dispatchListWillCall(this.#client, { order_id: orderRef });
  }

  /** Mints a code. The raw token rides this answer and nowhere else; the core stores only its hash. */
  mintWillCall(orderRef: string): Promise<Result<DispatchMintedPickup>> {
    return staffDispatch.dispatchMintWillCall(this.#client, { order_id: orderRef, ttl_hours: 0 });
  }

  revokeWillCall(id: string): Promise<Result<DispatchWillCallRevokeResult>> {
    return staffDispatch.dispatchRevokeWillCall(this.#client, id);
  }

  // ---- the rules (the read is open to staff; the write is the setters list's; the trail is a manager's)

  policy(branchId: string): Promise<Result<DispatchPolicy>> {
    return staffDispatch.dispatchGetPolicy(this.#client, { branch_id: branchId });
  }

  /** An absolute write of the whole policy for one yard. */
  savePolicy(body: DispatchPolicy): Promise<Result<DispatchPolicy>> {
    return staffDispatch.dispatchPutPolicy(this.#client, body);
  }

  policyHistory(branchId: string): Promise<Result<DispatchPolicyChangePage>> {
    return staffDispatch.dispatchGetPolicyHistory(this.#client, { branch_id: branchId });
  }

  // ---- the registry (the manage level; the core refuses below it)

  createTruck(body: DispatchTruck): Promise<Result<DispatchCreatedRef>> {
    return staffDispatch.dispatchCreateTruck(this.#client, body);
  }

  /** A rewrite, not a delta: every field of the row rides it. */
  updateTruck(id: string, body: DispatchTruck): Promise<Result<DispatchTruck>> {
    return staffDispatch.dispatchUpdateTruck(this.#client, id, body);
  }

  createDriver(body: DispatchDriver): Promise<Result<DispatchCreatedRef>> {
    return staffDispatch.dispatchCreateDriver(this.#client, body);
  }

  updateDriver(id: string, body: DispatchDriver): Promise<Result<DispatchDriver>> {
    return staffDispatch.dispatchUpdateDriver(this.#client, id, body);
  }

  gaps(branchId: string, date: string): Promise<Result<DispatchGapPage>> {
    return staffDispatch.dispatchGetLoadBoardGaps(this.#client, { branch_id: branchId, date });
  }

  calendar(branchId: string, from: string, to: string): Promise<Result<DispatchCalendarDayPage>> {
    return staffDispatch.dispatchGetLoadBoardCalendar(this.#client, { branch_id: branchId, from, to });
  }

  paymentHolds(runId: string): Promise<Result<DispatchPaymentHoldPage>> {
    return staffDispatch.dispatchListRunPaymentHolds(this.#client, runId);
  }

  // ---- runs

  openRun(branchId: string, date: string, truckId: string, driverId: string, notes: string): Promise<Result<DispatchRun>> {
    return staffDispatch.dispatchCreateRun(this.#client, {
      branch_id: branchId,
      scheduled_date: date,
      truck_id: truckId,
      driver_id: driverId,
      notes,
    });
  }

  crew(runId: string, truckId: string, driverId: string): Promise<Result<DispatchRun>> {
    return staffDispatch.dispatchAssignCrew(this.#client, runId, { truck_id: truckId, driver_id: driverId });
  }

  dispatchRun(runId: string): Promise<Result<DispatchRunDetail>> {
    return staffDispatch.dispatchDispatchRun(this.#client, runId);
  }

  cancelRun(runId: string): Promise<Result<DispatchRun>> {
    return staffDispatch.dispatchCancelRun(this.#client, runId);
  }

  completeRun(runId: string): Promise<Result<DispatchRun>> {
    return staffDispatch.dispatchCompleteRun(this.#client, runId);
  }

  reopenRun(runId: string, reason: string): Promise<Result<DispatchRun>> {
    return staffDispatch.dispatchReopenRun(this.#client, runId, { reason });
  }

  clearRunReopen(runId: string): Promise<Result<DispatchRun>> {
    return staffDispatch.dispatchClearRunReopen(this.#client, runId);
  }

  rescheduleRun(runId: string, toDate: string, reason: string): Promise<Result<DispatchRescheduleResult>> {
    return staffDispatch.dispatchRescheduleRun(this.#client, runId, { to_date: toDate, reason });
  }

  // ---- stops

  /** Turns a confirmed order into a delivery on a day. The date is a request until the load is on a truck. */
  scheduleStop(body: DispatchScheduleInput): Promise<Result<DispatchStop>> {
    return staffDispatch.dispatchScheduleStop(this.#client, body);
  }

  /** Puts a stop on a run (the end of its order), or takes it off with an empty run id. */
  assignStop(stopId: string, runId: string): Promise<Result<DispatchStop>> {
    return staffDispatch.dispatchAssignStop(this.#client, stopId, { run_id: runId, stop_no: 0 });
  }

  commitStop(stopId: string): Promise<Result<DispatchStop>> {
    return staffDispatch.dispatchCommitStop(this.#client, stopId, { scheduled_date: "" });
  }

  reopenStop(stopId: string, reason: string): Promise<Result<DispatchStop>> {
    return staffDispatch.dispatchReopenStop(this.#client, stopId, { reason });
  }

  rescheduleStop(stopId: string, toDate: string, reason: string): Promise<Result<DispatchRescheduleResult>> {
    return staffDispatch.dispatchRescheduleStop(this.#client, stopId, { to_date: toDate, reason });
  }

  advanceStop(stopId: string, status: DispatchStop["status"], reason: string): Promise<Result<DispatchStop>> {
    return staffDispatch.dispatchAdvanceStop(this.#client, stopId, { status, reason });
  }

  /** Loads many stops in one call. It answers a per stop outcome even when some refuse. */
  markLoaded(stopIds: readonly string[]): Promise<Result<DispatchLoadOutcomeList>> {
    return staffDispatch.dispatchMarkLoaded(this.#client, { stop_ids: [...stopIds] });
  }

  /** The auto assign seam. The core build answers 501 and says what would answer it. */
  autoAssign(): Promise<Result<unknown>> {
    return staffDispatch.dispatchAutoAssign(this.#client);
  }
}
