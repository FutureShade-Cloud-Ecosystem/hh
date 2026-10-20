// The dispatch board's screens, as plain render functions of a model. They hold no state and own
// no events: every control is a plain element carrying a data-action name, and the root element's
// click and change listeners hand them to the desk (the design system's position rules allow no
// "." or "@" binding here, no bound value outside class, role, data- and aria-, and no bound
// style, so a gauge bar's width is a class from a fixed set of steps).
//
// References: the original app's dispatch screens (dispatch-loadboard and dispatch-board in
// hardscapeos_dibbits), redrawn to HH's design system at the standard HH Drive and Buying and
// Stock set: cards border first with no shadow, status as a word before a colour, figures in
// tabular columns, 44 pixel targets, light by default and brand one tap away, one column at 390.
// No FB Design board exists for dispatch yet (PROVENANCE.md names the reference).
import { html, nothing, type TemplateResult } from "lit";
import type {
  DispatchBoard,
  DispatchCalendarDay,
  DispatchDriver,
  DispatchGapPage,
  DispatchFreightRaiseView,
  DispatchLoadBoardView,
  DispatchLoadRow,
  DispatchLoadStop,
  DispatchMintedPickup,
  DispatchPolicy,
  DispatchPolicyChange,
  DispatchRun,
  DispatchScanTrail,
  DispatchStop,
  DispatchStopPaymentHold,
  DispatchTruck,
  DispatchWillCall,
} from "@hardscapehouse/api-client";
import {
  BOARD_COLUMNS,
  LOAD_VALUE_CHOICES,
  PUT_ON_TRUCK_COMMIT_NOTE,
  REOPEN_CHOICES,
  TIME_AXIS_HOURS,
  TRUCK_TYPES,
  TRUCK_TYPE_LABEL,
  boardProblemLine,
  boardProblems,
  carriesMoffett,
  capacityText,
  deckSlotsForStops,
  dispatchConfirmWords,
  driverLine,
  fleetStateWord,
  gaugeLabel,
  historyRow,
  netPayloadWords,
  policyRows,
  suggestRunsForLoad,
  truckLine,
  volumeText,
  willCallLine,
  willCallWord,
  yardLoadOrder,
  CARD_LABEL,
  CARD_ORDER,
  RUN_LABEL,
  SCAN_SEQUENCE,
  STATUS_LANES,
  STOP_LABEL,
  addressLine,
  asOfLabel,
  askDiffers,
  assignTargets,
  barPct,
  bandWord,
  bulkLoadLabel,
  capacityBand,
  cardCount,
  cardScopeLabel,
  commitBlockedReason,
  dateStateLabel,
  dayOfLabel,
  dueLabel,
  filterBoard,
  fmtNum,
  fmtWeight,
  formatDayHeading,
  formatTime,
  groupByStatus,
  holdWords,
  laneScopeNote,
  loadableStops,
  pickupSplitLabel,
  rescheduleBlockedReason,
  rowEditable,
  scanLoadLine,
  scanStageLabel,
  shortDay,
  toCuYd,
  toWeight,
  raiseLine,
  raiseStateWord,
  trailSummary,
  weightUnit,
  stopActions,
  stopBlockedReason,
  targetLine,
  targetName,
  unloadableSummary,
  type Band,
  type BoardProblem,
  type CardId,
} from "./dispatch-logic.js";
import { houseZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";

export type Tab = "load" | "day" | "schedule" | "fleet" | "willcall" | "settings";
export type Axis = "TRUCK" | "STATUS";

/** An inline form open on one card: a date and a reason, a reason alone, or a new truck. */
export interface Sheet {
  readonly kind:
    | "reschedule-stop" | "reschedule-run" | "reopen-stop" | "reopen-run" | "fail-stop" | "new-run" | "cancel-run" | "dispatch-run"
    | "add-truck" | "edit-truck" | "add-driver" | "edit-driver" | "revoke-willcall" | "edit-policy" | "scan-trail" | "split-freight";
  readonly id: string;
  /** What was typed when a refusal sent the sheet back, so the person keeps their work. */
  readonly values?: Readonly<Record<string, string>>;
  /** The sheet's own refusal, shown beside its own button. */
  readonly error: string;
}

export interface BoardModel {
  readonly phase: "loading" | "ready" | "error";
  readonly tab: Tab;
  readonly date: string;
  readonly today: string;
  readonly branchName: string;
  readonly branches: readonly { readonly id: string; readonly name: string }[];
  readonly branchId: string;
  readonly busy: boolean;
  readonly error: string;
  readonly canRetry?: boolean;
  readonly notice: string;
  /** A neutral sentence from the core that is neither a success nor a failure (the auto assign seam). */
  readonly info: string;
  readonly refusals: readonly string[];
  readonly view: DispatchLoadBoardView | null;
  readonly readAt?: string;
  readonly card: CardId | "";
  readonly axis: Axis;
  readonly selected: ReadonlySet<string>;
  readonly gaps: DispatchGapPage | null;
  readonly calendar: readonly DispatchCalendarDay[];
  readonly day: DispatchBoard | null;
  readonly trucks: readonly DispatchTruck[];
  readonly drivers: readonly DispatchDriver[];
  readonly holds: ReadonlyMap<string, DispatchStopPaymentHold>;
  readonly sheet: Sheet | null;
  /** True while the board for a new day, yard or span is being read: no stale figure is drawn meanwhile. */
  readonly loading: boolean;
  readonly span: Span;
  /** The schedule form's own refusal, shown beside its button. */
  readonly scheduleError: string;
  /** A parked freight ask, held until closed. Its figures are drawn only if the core sent them. */
  readonly raise: DispatchFreightRaiseView | null;
  /** The scan trail open on a card: the order named, and its read in progress, done or refused. */
  readonly trail: {
    readonly stopId: string;
    readonly orderNo: string;
    readonly state: "loading" | "ready" | "error";
    readonly value?: DispatchScanTrail;
    readonly words?: string;
  } | null;
  /** The settings tab: the yard's rules, the trail of changes, and why the trail is not shown when it is not. */
  readonly policy: DispatchPolicy | null;
  readonly history: readonly DispatchPolicyChange[];
  readonly historyFault: string;
  /** The will call tab: the order named, its codes, and the code just minted (in memory, shown once). */
  readonly willCall: {
    readonly order: string;
    readonly items: readonly DispatchWillCall[];
    readonly looked: boolean;
    readonly error: string;
    readonly minted: { readonly code: DispatchMintedPickup; readonly order: string } | null;
  };
  /** A refusal that belongs to one truck's lane, shown in that lane. */
  readonly runError: { readonly id: string; readonly words: string } | null;
  /** The run currently opened in the run-detail drawer, or empty when the drawer is closed. */
  readonly selectedRunId: string;
  /** The fix keys the dispatcher has marked applied on the current view. */
  readonly appliedFixes: ReadonlySet<string>;
  /** The stop the suggest-runs panel is open on, or empty when closed. */
  readonly suggestStopId: string;
}

export type Span = "DAY" | "WEEK";

// ---- small pieces --------------------------------------------------------------------------------

function tone(status: string): string {
  if (status === "DELIVERED" || status === "COMPLETED") return "good";
  if (status === "FAILED" || status === "CANCELLED") return "stop";
  if (status === "EN_ROUTE" || status === "DISPATCHED") return "watch";
  return "plain";
}

function statusPill(status: string, label: string): TemplateResult {
  return html`<span class="status status-${tone(status)}" data-status>${label}</span>`;
}

function bandTone(band: Band): string {
  if (band === "RED") return "stop";
  if (band === "AMBER") return "watch";
  if (band === "GREEN") return "good";
  return "plain";
}

/** The fill step for a bar: the percentage rounded to the nearest five, a class the stylesheet knows. */
function step(pct: number): number {
  return Math.max(0, Math.min(100, Math.round(pct / 5) * 5));
}

/** The weight gauge in the yard's units: the core's kilograms in, pounds or tons shown. */
function weightGauge(used: number | null | undefined, capacity: number | null | undefined): TemplateResult {
  const unit = weightUnit(capacity, used);
  const conv = (v: number | null | undefined): number | null => (typeof v === "number" ? toWeight(v, unit) : null);
  return gauge("Weight", conv(used), conv(capacity), unit);
}

function gauge(kind: string, used: number | null | undefined, capacity: number | null | undefined, unit: string): TemplateResult {
  const band = capacityBand(used, capacity);
  const pct = barPct(used, capacity);
  const figure =
    band === "UNKNOWN"
      ? typeof used === "number"
        ? `${fmtNum(used)} ${unit}, capacity not measured`
        : "not measured"
      : `${fmtNum(used)} of ${fmtNum(capacity)} ${unit}`;
  return html`
    <div class="gauge" data-gauge=${kind.toLowerCase()} data-band=${band.toLowerCase()}>
      <div class="gauge-line">
        <span class="gauge-kind">${kind}</span>
        <span class="gauge-figure">${figure}</span>
        <span class="status status-${bandTone(band)}">${bandWord(band)}</span>
      </div>
      <div class="bar" aria-hidden="true"><div class="fill fill-${bandTone(band)} w-${step(pct)}"></div></div>
    </div>
  `;
}

export function renderMessages(model: Pick<BoardModel, "error" | "notice" | "info" | "refusals">): TemplateResult | typeof nothing {
  if (model.error === "" && model.notice === "" && model.info === "" && model.refusals.length === 0) return nothing;
  return html`
    ${model.error !== "" ? html`<p class="alert alert-error" role="alert" data-error>${model.error}</p>` : nothing}
    ${model.notice !== "" ? html`<p class="alert alert-ok" role="status" data-notice>${model.notice}</p>` : nothing}
    ${model.info !== "" ? html`<p class="alert" role="status" data-info>${model.info}</p>` : nothing}
    ${model.refusals.length > 0
      ? html`<div class="alert alert-error" role="alert" data-refusals>
          <strong>Some loads did not move</strong>
          <ul>${model.refusals.map((r) => html`<li>${r}</li>`)}</ul>
        </div>`
      : nothing}
  `;
}

// ---- the scan trail ------------------------------------------------------------------------------

/** Where an order's pallets are, as a spine of the five stages per pallet: reached ones filled, the furthest ringed. */
function scanTrailSheet(sheet: Sheet, model: BoardModel): TemplateResult {
  const t = model.trail;
  const trail = t !== null && t.stopId === sheet.id ? t : null;
  return html`
    <section class="sheet" data-sheet=${sheet.kind} data-sheet-id=${sheet.id} aria-label="Scan trail">
      <h4>Scan trail for ${trail?.orderNo ?? "the order"}</h4>
      ${trail === null || trail.state === "loading"
        ? html`<p class="dim" role="status">Reading the trail...</p>`
        : trail.state === "error"
          ? html`<p class="alert alert-error" role="alert" data-sheet-error>${trail.words}</p>`
          : html`
              <p class="trail-summary" data-trail-summary>${trailSummary(trail.value)}</p>
              ${(trail.value?.loads ?? []).length === 0
                ? html`<p class="small dim">No pallets scanned against this order yet.</p>`
                : (trail.value?.loads ?? []).map(
                    (l) => html`
                      <div class="trail-load" data-trail-load=${l.load_ref}>
                        <p class="small">${scanLoadLine(l)}</p>
                        <div class="spine">
                          ${SCAN_SEQUENCE.map(
                            (stage) => html`<span class="stage ${l.reached.includes(stage) ? "reached" : ""}" data-stage=${stage} data-reached=${l.reached.includes(stage) ? "true" : "false"} data-furthest=${l.furthest === stage ? "true" : "false"}>${scanStageLabel(stage)}</span>`,
                          )}
                        </div>
                      </div>`,
                  )}
            `}
      <div class="actions">
        <button type="button" class="btn btn-secondary" data-action="sheet-cancel">Close</button>
      </div>
    </section>
  `;
}

// ---- split freight -------------------------------------------------------------------------------

function splitSheet(sheet: Sheet, model: BoardModel): TemplateResult {
  const all: DispatchStop[] = [...(model.view?.staging ?? []), ...(model.view?.loads.flatMap((r) => r.stops) ?? []), ...(model.day?.stops ?? []), ...(model.day?.unassigned ?? [])];
  const stop = all.find((s) => s.id === sheet.id);
  const now = stop?.load_count ?? 1;
  return html`
    <form class="sheet" data-sheet=${sheet.kind} data-sheet-id=${sheet.id} novalidate>
      <h4>Split the freight on ${stop?.order_no ?? "this order"} across trucks</h4>
      <div class="sheet-grid">
        <label>Trucks the order goes out on<input class="control" type="text" inputmode="numeric" data-field="loads" data-initial=${String(now + 1)} autocomplete="off" /></label>
        <label>The order's freight after the split, dollars, optional; empty keeps it as it is<input class="control" type="text" inputmode="decimal" data-field="freight" autocomplete="off" /></label>
        <label class="wide">Each truck's share by hand, dollars, separated by commas, optional<input class="control" type="text" data-field="amounts" autocomplete="off" /></label>
      </div>
      <p class="small dim">Left empty, the freight is divided evenly across the stops. Freight above what the customer was quoted waits for an approver and changes nothing until it is approved. Asking the same thing twice is safe.</p>
      ${sheet.error !== "" ? html`<p class="alert alert-error" role="alert" data-sheet-error>${sheet.error}</p>` : nothing}
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="sheet-submit" ?disabled=${model.busy}>Split it</button>
        <button type="button" class="btn btn-secondary" data-action="sheet-cancel">Cancel</button>
      </div>
    </form>
  `;
}

function splitButton(model: BoardModel, stop: DispatchStop): TemplateResult {
  return html`<button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="split-freight" data-stop-id=${stop.id} ?disabled=${model.busy}>Split across trucks</button>`;
}

/** The parked ask: what was asked, where it stands, and a way to read it again. Figures only where the core sent them. */
function raiseCard(model: BoardModel): TemplateResult | typeof nothing {
  const r = model.raise;
  if (r === null) return nothing;
  const lines = raiseLine(r);
  return html`
    <section class="panel raise" data-raise data-raise-id=${r.id} data-raise-state=${r.state} aria-label="A freight ask waiting for approval">
      <div class="panel-head">
        <h3>Freight ask</h3>
        <span class="status ${r.state === "APPLIED" ? "status-good" : r.state === "REFUSED" ? "status-stop" : "status-watch"}" data-raise-word>${raiseStateWord(r.state)}</span>
      </div>
      ${lines.map((l) => html`<p class="small">${l}</p>`)}
      <p class="small dim">An approver decides it on Manager Home. Nothing on the loads changes until they do.</p>
      <div class="actions">
        <button type="button" class="btn btn-secondary" data-action="raise-check" ?disabled=${model.busy}>Check on it</button>
        <button type="button" class="btn btn-secondary" data-action="raise-close">Close</button>
      </div>
    </section>
  `;
}

function printManifestButton(model: BoardModel, run: DispatchRun): TemplateResult {
  return html`<button type="button" class="btn btn-secondary small-btn" data-action="print-manifest" data-run-id=${run.id} ?disabled=${model.busy}>Print manifest</button>`;
}

function scanTrailButton(model: BoardModel, stop: DispatchStop): TemplateResult {
  return html`<button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="scan-trail" data-stop-id=${stop.id} ?disabled=${model.busy}>Scan trail</button>`;
}

// ---- the registry sheets -------------------------------------------------------------------------

function fleetSheet(sheet: Sheet, model: BoardModel): TemplateResult {
  const truck = sheet.kind === "edit-truck" ? model.trucks.find((t) => t.id === sheet.id) : undefined;
  const driver = sheet.kind === "edit-driver" ? model.drivers.find((d) => d.id === sheet.id) : undefined;
  const isTruck = sheet.kind === "add-truck" || sheet.kind === "edit-truck";
  const editing = sheet.kind === "edit-truck" || sheet.kind === "edit-driver";
  const initial = (value: string | undefined): string | typeof nothing => (editing ? (value ?? "") : nothing);
  return html`
    <form class="sheet" data-sheet=${sheet.kind} data-sheet-id=${sheet.id} novalidate>
      <h4>${isTruck ? (editing ? "Change this truck" : "Add a truck") : editing ? "Change this driver" : "Add a driver"}</h4>
      ${isTruck
        ? html`
            <div class="sheet-grid">
              <label>Code<input class="control" type="text" data-field="code" data-initial=${initial(truck?.code)} maxlength="40" autocomplete="off" /></label>
              <label>Description<input class="control" type="text" data-field="name" data-initial=${initial(truck?.name)} maxlength="120" autocomplete="off" /></label>
              <label>Type
                <select class="control" data-field="type" data-initial=${initial(truck?.truck_type)}>
                  ${TRUCK_TYPES.map((t) => html`<option data-id=${t}>${TRUCK_TYPE_LABEL[t]}</option>`)}
                </select>
              </label>
              <label>Capacity, pounds<input class="control" type="text" inputmode="decimal" data-field="capacity" data-initial=${initial(capacityText(truck?.capacity_kg))} autocomplete="off" /></label>
              <label>Volume, cubic yards<input class="control" type="text" inputmode="decimal" data-field="volume" data-initial=${initial(volumeText(truck?.capacity_m3))} autocomplete="off" /></label>
              <label>Pallet positions<input class="control" type="text" inputmode="numeric" data-field="pallets" data-initial=${initial(truck?.pallet_positions == null ? "" : String(truck.pallet_positions))} autocomplete="off" /></label>
            </div>
            <p class="small dim">Capacity is advisory. It draws the board's gauge; nothing refuses a full truck.</p>
          `
        : html`
            <div class="sheet-grid">
              <label>Name<input class="control" type="text" data-field="name" data-initial=${initial(driver?.name)} maxlength="120" autocomplete="off" /></label>
              <label>Phone<input class="control" type="tel" data-field="phone" data-initial=${initial(driver?.phone)} autocomplete="off" /></label>
              <label>Licence class<input class="control" type="text" data-field="licence" data-initial=${initial(driver?.licence_class)} maxlength="20" autocomplete="off" /></label>
              <label>Driver's sign in account<input class="control" type="text" data-field="login" data-initial=${initial(driver?.user_id)} aria-describedby="driver-login-hint" autocomplete="off" /></label>
            </div>
            <p class="small dim" id="driver-login-hint">Optional. A driver with a sign in account opens Drive and sees their own runs. Leave it empty for a casual or contract driver.</p>
          `}
      ${sheet.error !== "" ? html`<p class="alert alert-error" role="alert" data-sheet-error>${sheet.error}</p>` : nothing}
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="sheet-submit" ?disabled=${model.busy}>${editing ? "Save" : isTruck ? "Add truck" : "Add driver"}</button>
        <button type="button" class="btn btn-secondary" data-action="sheet-cancel">Cancel</button>
      </div>
    </form>
  `;
}

// ---- the inline sheet ----------------------------------------------------------------------------

function sheetPanel(sheet: Sheet, model: BoardModel): TemplateResult {
  if (sheet.kind === "add-truck" || sheet.kind === "edit-truck" || sheet.kind === "add-driver" || sheet.kind === "edit-driver") return fleetSheet(sheet, model);
  if (sheet.kind === "edit-policy") {
    const p = model.policy;
    const options = (choices: readonly { value: string; label: string }[]): TemplateResult[] => choices.map((c) => html`<option data-id=${c.value}>${c.label}</option>`);
    return html`
      <form class="sheet" data-sheet=${sheet.kind} data-sheet-id=${sheet.id} novalidate>
        <h4>Change the dispatch rules</h4>
        <div class="sheet-grid">
          <label>Prompt to commit, hours ahead<input class="control" type="text" inputmode="numeric" data-field="prompt" data-initial=${p === null ? nothing : String(p.commit_prompt_hours)} autocomplete="off" /></label>
          <label>Who may reopen a committed load
            <select class="control" data-field="reopen" data-initial=${p === null ? nothing : p.reopen_policy}>${options(REOPEN_CHOICES)}</select>
          </label>
          <label>Who sees a load's dollar value
            <select class="control" data-field="visibility" data-initial=${p === null ? nothing : p.load_value_visibility}>${options(LOAD_VALUE_CHOICES)}</select>
          </label>
          <label>How far the board plans, in weeks<input class="control" type="text" inputmode="numeric" data-field="horizon" data-initial=${p === null ? nothing : String(p.commit_horizon_weeks)} autocomplete="off" /></label>
          <label>How far the daily view reaches, in days<input class="control" type="text" inputmode="numeric" data-field="view" data-initial=${p === null ? nothing : String(p.working_view_days)} autocomplete="off" /></label>
        </div>
        <p class="small dim">The board plans weeks ahead. The daily view is the days it shows from its date. They are two different numbers.</p>
        ${sheet.error !== "" ? html`<p class="alert alert-error" role="alert" data-sheet-error>${sheet.error}</p>` : nothing}
        <div class="actions">
          <button type="button" class="btn btn-primary" data-action="sheet-submit" ?disabled=${model.busy}>Save the rules</button>
          <button type="button" class="btn btn-secondary" data-action="sheet-cancel">Cancel</button>
        </div>
      </form>
    `;
  }
  if (sheet.kind === "scan-trail") return scanTrailSheet(sheet, model);
  if (sheet.kind === "split-freight") return splitSheet(sheet, model);
  if (sheet.kind === "revoke-willcall") {
    return html`
      <form class="sheet" data-sheet=${sheet.kind} data-sheet-id=${sheet.id} novalidate>
        <h4>Cancel this pickup code?</h4>
        <p class="small">The tag already printed stops working. The customer needs a new code.</p>
        ${sheet.error !== "" ? html`<p class="alert alert-error" role="alert" data-sheet-error>${sheet.error}</p>` : nothing}
        <div class="actions">
          <button type="button" class="btn btn-primary" data-action="sheet-submit" ?disabled=${model.busy}>Cancel the code</button>
          <button type="button" class="btn btn-secondary" data-action="sheet-cancel">Keep it</button>
        </div>
      </form>
    `;
  }
  if (sheet.kind === "dispatch-run") {
    const runs = [...(model.view?.loads.map((r) => r.run) ?? []), ...(model.day?.runs ?? [])];
    const run = runs.find((r) => r.id === sheet.id);
    const stops = model.view?.loads.find((r) => r.run.id === sheet.id)?.stops ?? model.day?.stops.filter((s) => s.run_id === sheet.id);
    const words = dispatchConfirmWords(run, stops, model.holds);
    return html`
      <form class="sheet" data-sheet=${sheet.kind} data-sheet-id=${sheet.id} novalidate>
        <h4>${words.title}</h4>
        <p class="small">${words.body}</p>
        ${words.holds.map((line) => html`<p class="small hold-words" data-sheet-hold>${line}</p>`)}
        ${sheet.error !== "" ? html`<p class="alert alert-error" role="alert" data-sheet-error>${sheet.error}</p>` : nothing}
        <div class="actions">
          <button type="button" class="btn btn-primary" data-action="sheet-submit" ?disabled=${model.busy}>Send the run</button>
          <button type="button" class="btn btn-secondary" data-action="sheet-cancel">Cancel</button>
        </div>
      </form>
    `;
  }
  const dated = sheet.kind === "reschedule-stop" || sheet.kind === "reschedule-run";
  const title =
    sheet.kind === "reschedule-stop"
      ? "Move this delivery to another day"
      : sheet.kind === "reschedule-run"
        ? "Move this whole truck to another day"
        : sheet.kind === "reopen-stop"
          ? "Reopen this load"
          : sheet.kind === "reopen-run"
            ? "Reopen this truck"
            : sheet.kind === "fail-stop"
              ? "Why could this not be delivered?"
              : sheet.kind === "cancel-run"
                ? "Cancel this truck?"
                : "Open a truck for this day";
  const prompt =
    sheet.kind === "fail-stop"
      ? "What stopped this delivery"
      : sheet.kind === "new-run"
        ? "Notes for the driver"
        : dated
          ? "Why is it moving"
          : "Why is it being reopened";
  const submit =
    sheet.kind === "fail-stop"
      ? "Record it"
      : sheet.kind === "new-run"
        ? "Open truck"
        : sheet.kind === "cancel-run"
          ? "Cancel the truck"
          : dated
          ? "Move it"
          : "Reopen it";
  return html`
    <form class="sheet" data-sheet=${sheet.kind} data-sheet-id=${sheet.id} novalidate>
      <h4>${title}</h4>
      ${sheet.kind === "new-run"
        ? html`
            <div class="sheet-grid">
              <label>Truck
                <select class="control" data-field="truck">
                  <option data-id="">Truck, optional now</option>
                  ${model.trucks.map((t) => html`<option data-id=${t.id}>${t.code}, ${t.name}</option>`)}
                </select>
              </label>
              <label>Driver
                <select class="control" data-field="driver">
                  <option data-id="">Driver, optional now</option>
                  ${model.drivers.map((d) => html`<option data-id=${d.id}>${d.name}</option>`)}
                </select>
              </label>
            </div>
          `
        : nothing}
      ${dated
        ? html`<label>New delivery day<input class="control" type="date" data-field="to_date" /></label>`
        : nothing}
      ${sheet.kind === "cancel-run"
        ? html`<p class="small">A cancelled truck stays cancelled. Every ready load on it goes back to the staging lane.</p>`
        : html`<label>${prompt}<input class="control" type="text" maxlength="400" data-field="reason" /></label>`}
      ${sheet.error !== "" ? html`<p class="alert alert-error" role="alert" data-sheet-error>${sheet.error}</p>` : nothing}
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="sheet-submit" ?disabled=${model.busy}>${submit}</button>
        <button type="button" class="btn btn-secondary" data-action="sheet-cancel">${sheet.kind === "cancel-run" ? "Keep it" : "Cancel"}</button>
      </div>
    </form>
  `;
}

function sheetFor(model: BoardModel, kind: Sheet["kind"][], id: string): TemplateResult | typeof nothing {
  const sheet = model.sheet;
  if (sheet === null || sheet.id !== id || !kind.includes(sheet.kind)) return nothing;
  return sheetPanel(sheet, model);
}

// ---- the toolbar ---------------------------------------------------------------------------------

function toolbar(model: BoardModel): TemplateResult {
  return html`
    <div class="toolbar">
      <div class="where">
        ${model.branches.length > 1
          ? html`<label class="inline">Yard
              <select class="control" data-field="branch" data-current=${model.branchId}>
                ${model.branches.map((b) => html`<option data-id=${b.id}>${b.name}</option>`)}
              </select>
            </label>`
          : html`<span class="yard" data-branch-name>${model.branchName}</span>`}
      </div>
      ${model.tab === "fleet" || model.tab === "willcall" || model.tab === "settings" ? nothing : html`<div class="when" role="group" aria-label="Delivery day">
        <button type="button" class="btn btn-secondary day-btn" data-action="day-prev" aria-label="Previous day">&lsaquo;</button>
        <input class="control" type="date" data-field="date" aria-label="Delivery day" data-current=${model.date} />
        <button type="button" class="btn btn-secondary day-btn" data-action="day-next" aria-label="Next day">&rsaquo;</button>
        <span class="day-words" data-day-words>${formatDayHeading(model.date)}</span>
        ${model.date !== model.today
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="day-today">Today</button>`
          : nothing}
      </div>`}
      ${model.tab === "load"
        ? html`<div class="segs" role="group" aria-label="Span">
            <button type="button" class="seg" data-action="span" data-span="DAY" aria-pressed=${model.span === "DAY" ? "true" : "false"}>Day</button>
            <button type="button" class="seg" data-action="span" data-span="WEEK" aria-pressed=${model.span === "WEEK" ? "true" : "false"}>Week</button>
          </div>`
        : nothing}
      <div class="tools">
        <button type="button" class="btn btn-secondary small-btn" data-action="refresh" ?disabled=${model.busy}>Refresh</button>
        ${model.tab === "fleet" || model.tab === "willcall" || model.tab === "settings" ? nothing : html`<button type="button" class="btn btn-primary small-btn" data-action="new-run" ?disabled=${model.busy}>New truck</button>`}
      </div>
    </div>
    ${sheetFor(model, ["new-run"], "new")}
  `;
}

function tabs(model: BoardModel): TemplateResult {
  const tab = (id: Tab, label: string): TemplateResult =>
    html`<button type="button" class="tab" data-action="tab" data-tab-id=${id} aria-current=${model.tab === id ? "true" : "false"}>${label}</button>`;
  return html`
    <nav class="tabs" aria-label="Dispatch screens">
      ${tab("load", "Load board")}${tab("day", "Day board")}${tab("schedule", "Schedule")}
      ${tab("fleet", "Trucks and drivers")}${tab("willcall", "Will call")}${tab("settings", "Settings")}
    </nav>
  `;
}

// ---- the load board ------------------------------------------------------------------------------

function quickCards(model: BoardModel, view: DispatchLoadBoardView): TemplateResult {
  const asOf = asOfLabel(model.readAt, houseZone());
  return html`
    <section class="qcards" aria-label="Quick cards">
      ${CARD_ORDER.map((id) => {
        const count = cardCount(view, id);
        const scope = cardScopeLabel(view, id);
        if (id === "card.pickups") {
          const split = pickupSplitLabel(view);
          return html`<div class="tile" data-card=${id}>
            <span class="kicker">${CARD_LABEL[id]}</span>
            <span class="figure">${count}</span>
            <span class="small">${split !== "" ? `${split}, ${scope}` : scope}</span>
          </div>`;
        }
        const on = model.card === id;
        return html`<button type="button" class="tile tile-btn" data-action="card" data-card=${id} aria-pressed=${on ? "true" : "false"}>
          <span class="kicker">${CARD_LABEL[id]}</span>
          <span class="figure">${count}</span>
          <span class="small">${scope}</span>
        </button>`;
      })}
      ${asOf !== "" ? html`<p class="small dim qcards-as-of" data-cards-as-of>${asOf}</p>` : nothing}
    </section>
    ${model.card !== ""
      ? html`<p class="filter" role="status" data-filter>
          Showing ${CARD_LABEL[model.card].toLowerCase()} only.
          <button type="button" class="link" data-action="card-clear">Show everything</button>
        </p>`
      : nothing}
  `;
}

function weightFact(stop: DispatchStop): TemplateResult {
  return typeof stop.weight_kg === "number"
    ? html`<span class="fact">${fmtWeight(stop.weight_kg)}</span>`
    : html`<span class="fact dim">Weight not on file</span>`;
}

/** One load, wherever the board draws it: staging, a truck lane, a status lane. */
function loadCard(model: BoardModel, view: DispatchLoadBoardView, stop: DispatchLoadStop, laneDate: string, opts: { selectable: boolean; lane?: boolean }): TemplateResult {
  const blocked = stopBlockedReason(stop, view);
  const editable = blocked === "";
  const committed = stop.commit_state === "COMMITTED";
  const hold = model.holds.get(stop.id);
  const holdLine = holdWords(hold);
  const dayPill = dayOfLabel(stop, laneDate);
  const actions = stopActions(stop.status, { assigned: Boolean(stop.run_id) });
  const canLoad = actions.some((a) => a.action === "LOADED");
  const targets = !stop.run_id && stop.status === "READY" && editable ? assignTargets(view, stop) : [];
  const picked = model.selected.has(stop.id);
  const reschedBlock = rescheduleBlockedReason(stop);
  return html`
    <li class="load" data-stop-id=${stop.id} data-order=${stop.order_no ?? ""} data-status=${stop.status} data-commit=${stop.commit_state ?? "REQUESTED"}>
      <div class="load-head">
        ${opts.selectable
          ? html`<button type="button" class="box" role="checkbox" aria-checked=${picked ? "true" : "false"} aria-label=${`Select ${stop.order_no ?? "load"}`} data-action="select" data-stop-id=${stop.id}></button>`
          : nothing}
        <span class="order">${stop.order_no ?? "Order"}</span>
        ${stop.stop_no ? html`<span class="small">stop ${stop.stop_no}</span>` : nothing}
        ${statusPill(stop.status, STOP_LABEL[stop.status] ?? stop.status)}
        ${dayPill !== "" ? html`<span class="nodate" data-day-pill>${dayPill}</span>` : nothing}
      </div>
      <div class="who">${stop.customer_name || "Customer"}</div>
      <div class="small dim">${addressLine(stop) || "No address on file"}${stop.time_window ? ` · ${stop.time_window}` : ""}</div>
      <div class="facts">
        <span class="status ${committed ? "status-good" : "status-plain"}" data-date-state>${dateStateLabel(stop.commit_state)}</span>
        ${askDiffers(stop) ? html`<span class="small dim">asked ${stop.requested_date}</span>` : nothing}
        ${weightFact(stop)}
        ${typeof stop.gap_count === "number" && stop.gap_count > 0 ? html`<span class="status status-watch" data-short>Short ${stop.gap_count}</span>` : nothing}
        ${holdLine !== "" ? html`<span class="status status-stop" data-hold>Held for payment</span>` : nothing}
      </div>
      ${holdLine !== "" ? html`<p class="small hold-words" data-hold-words>${holdLine}</p>` : nothing}
      ${stop.failure_reason ? html`<p class="small bad">${stop.failure_reason}</p>` : nothing}
      ${!editable && opts.lane !== true ? html`<p class="small dim" data-blocked>${blocked}</p>` : nothing}
      ${targets.length > 0 ? html`<p class="small dim" data-commit-note>${PUT_ON_TRUCK_COMMIT_NOTE}</p>` : nothing}
      <div class="actions">
        ${targets.length > 0
          ? html`<select class="control pick" data-action="assign-pick" data-stop-id=${stop.id} aria-label=${`Put ${stop.order_no ?? "this load"} on a truck`}>
              <option data-run-id="">Put on a truck</option>
              ${targets.map((t) => html`<option data-run-id=${t.runId} ?disabled=${t.blocked !== ""}>${targetName(t)} · ${targetLine(t)}</option>`)}
            </select>
            <button type="button" class="btn btn-secondary small-btn" data-action="suggest-open" data-stop-id=${stop.id} ?disabled=${model.busy}>Suggest runs</button>`
          : nothing}
        ${targets.length === 0 && !stop.run_id && stop.status === "READY" && editable && view.loads.length === 0
          ? html`<span class="small dim">No truck is open for this day. Open one first.</span>`
          : nothing}
        ${view.can_commit && stop.commit_state === "REQUESTED" && !stop.run_id && stop.status === "READY"
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="commit" data-stop-id=${stop.id} ?disabled=${model.busy}>Commit date</button>`
          : nothing}
        ${canLoad && stop.run_id
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="stop-status" data-status-to="LOADED" data-stop-id=${stop.id} ?disabled=${model.busy}>Mark loaded</button>`
          : nothing}
        ${stop.run_id && stop.status === "READY" && editable
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="assign-off" data-stop-id=${stop.id} ?disabled=${model.busy}>Take off truck</button>`
          : nothing}
        ${committed && view.can_reopen && stop.status === "READY"
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="reopen-stop" data-stop-id=${stop.id} ?disabled=${model.busy}>Reopen</button>`
          : nothing}
        ${reschedBlock === ""
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="reschedule-stop" data-stop-id=${stop.id} ?disabled=${model.busy}>Move date</button>`
          : nothing}
        ${scanTrailButton(model, stop)}
        ${splitButton(model, stop)}
      </div>
      ${sheetFor(model, ["reopen-stop", "reschedule-stop", "scan-trail", "split-freight"], stop.id)}
    </li>
  `;
}

function laneRefusal(model: BoardModel, runId: string): TemplateResult | typeof nothing {
  const e = model.runError;
  return e !== null && e.id === runId ? html`<p class="alert alert-error" role="alert" data-lane-error>${e.words}</p>` : nothing;
}

function crewLine(model: BoardModel, run: DispatchRun, editable: boolean): TemplateResult {
  if (run.status === "PLANNED" && editable) {
    return html`
      <div class="crew">
        <label>Truck
          <select class="control" data-action="crew-pick" data-run-id=${run.id} data-field="crew-truck" data-current=${run.truck_id ?? ""}>
            <option data-id="">No truck yet</option>
            ${run.truck_id && !model.trucks.some((t) => t.id === run.truck_id) ? html`<option data-id=${run.truck_id}>${run.truck_code ?? "This truck"}, out of use</option>` : nothing}
            ${model.trucks.map((t) => html`<option data-id=${t.id}>${t.code}</option>`)}
          </select>
        </label>
        <label>Driver
          <select class="control" data-action="crew-pick" data-run-id=${run.id} data-field="crew-driver" data-current=${run.driver_id ?? ""}>
            <option data-id="">No driver yet</option>
            ${run.driver_id && !model.drivers.some((d) => d.id === run.driver_id) ? html`<option data-id=${run.driver_id}>${run.driver_name ?? "This driver"}, out of use</option>` : nothing}
            ${model.drivers.map((d) => html`<option data-id=${d.id}>${d.name}</option>`)}
          </select>
        </label>
      </div>
    `;
  }
  return html`<p class="small" data-crew>${run.truck_code || "No truck"} · ${run.driver_name || "No driver"}</p>`;
}

function truckLane(model: BoardModel, view: DispatchLoadBoardView, row: DispatchLoadRow): TemplateResult {
  const run = row.run;
  const editable = rowEditable(row);
  const crewed = Boolean(run.truck_id && run.driver_id);
  const blocked = commitBlockedReason(row);
  const g = row.gauge;
  const due = dueLabel(row);
  return html`
    <section class="truck" data-run-id=${run.id} data-run-no=${run.run_no} data-run-status=${run.status}>
      <header class="truck-head">
        <h3>${run.run_no}</h3>
        ${statusPill(run.status, RUN_LABEL[run.status] ?? run.status)}
        ${row.commit.committed ? html`<span class="status status-good" data-committed>Committed</span>` : nothing}
        ${row.commit.reopened ? html`<span class="status status-watch" data-reopened>Reopened</span>` : nothing}
        ${due !== "" ? html`<span class="status status-watch" data-due>${due}</span>` : nothing}
      </header>
      ${crewLine(model, run, editable)}
      ${laneRefusal(model, run.id)}
      ${weightGauge(g.weight_kg, g.capacity_kg)}
      ${typeof g.capacity_m3 === "number" ? gauge("Volume", typeof g.volume_m3 === "number" ? toCuYd(g.volume_m3) : null, toCuYd(g.capacity_m3), "cu yd") : nothing}
      ${typeof g.pallet_positions === "number" ? gauge("Pallets", g.pallets, g.pallet_positions, "positions") : nothing}
      ${g.weighed_stops < g.stop_count ? html`<p class="small dim">${g.stop_count - g.weighed_stops} of ${g.stop_count} loads not weighed, so the total is a floor.</p>` : nothing}
      ${!editable && blocked !== "" ? html`<p class="small dim" data-blocked>${blocked}</p>` : nothing}
      <ul class="loads">
        ${row.stops.length > 0
          ? row.stops.map((s) => loadCard(model, view, s, view.date, { selectable: s.status === "READY", lane: true }))
          : html`<li class="empty-lane small dim">Nothing on this truck yet.</li>`}
      </ul>
      <div class="actions">
        ${run.status === "PLANNED"
          ? html`<button type="button" class="btn btn-primary" data-action="dispatch-run" data-run-id=${run.id}
              ?disabled=${model.busy}>Dispatch</button>`
          : nothing}
        ${run.status === "PLANNED" && (!crewed || run.stop_count === 0)
          ? html`<span class="small dim" data-dispatch-why>${!crewed ? "A truck needs a truck and a driver before it goes." : "Put a load on it first."}</span>`
          : nothing}
        ${printManifestButton(model, run)}
        ${run.status === "PLANNED" && editable
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="reschedule-run" data-run-id=${run.id}>Move truck date</button>
              <button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="cancel-run" data-run-id=${run.id} ?disabled=${model.busy}>Cancel truck</button>`
          : nothing}
        ${row.commit.committed && row.commit.can_reopen && run.status === "PLANNED"
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="reopen-run" data-run-id=${run.id}>Reopen truck</button>`
          : nothing}
        ${row.commit.reopened
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="clear-reopen" data-run-id=${run.id} ?disabled=${model.busy}>Clear the reopen marker</button>`
          : nothing}
      </div>
      ${sheetFor(model, ["reopen-run", "reschedule-run", "cancel-run", "dispatch-run"], run.id)}
    </section>
  `;
}

function statusLanes(model: BoardModel, view: DispatchLoadBoardView): TemplateResult {
  const lanes = groupByStatus(view);
  const order = [...STATUS_LANES, "OTHER"] as const;
  return html`
    <div class="status-lanes">
      ${order.map((lane) => {
        const stops = lanes[lane] ?? [];
        if (lane === "OTHER" && stops.length === 0) return nothing;
        const note = laneScopeNote(stops, view.date, view.working_days);
        return html`<section class="lane" data-lane=${lane}>
          <h3>${lane === "OTHER" ? "Did not go" : STOP_LABEL[lane]}<span class="count">${stops.length}</span></h3>
          ${note !== "" ? html`<p class="small dim">${note}</p>` : nothing}
          <ul class="loads">
            ${stops.length > 0
              ? stops.map((s) => loadCard(model, view, s, view.date, { selectable: lane === "READY" && Boolean(s.run_id) }))
              : html`<li class="empty-lane small dim">Nothing here.</li>`}
          </ul>
        </section>`;
      })}
    </div>
  `;
}

function bulkBar(model: BoardModel, view: DispatchLoadBoardView): TemplateResult | typeof nothing {
  if (model.selected.size === 0) return nothing;
  const all: DispatchLoadStop[] = [...view.staging, ...view.loads.flatMap((r) => r.stops)];
  const ok = loadableStops(all, model.selected);
  const why = unloadableSummary(all, model.selected);
  const label = bulkLoadLabel(ok);
  return html`
    <div class="bulk" role="region" aria-label="Selected loads" data-bulk>
      <span><strong>${model.selected.size}</strong> selected</span>
      <button type="button" class="link" data-action="select-clear">Clear</button>
      ${ok.length > 0
        ? html`<button type="button" class="btn btn-primary small-btn" data-action="mark-loaded" ?disabled=${model.busy}>${label}</button>`
        : html`<span class="small dim">Nothing here can be loaded.</span>`}
      ${why !== "" ? html`<span class="small dim" data-bulk-why>${why}</span>` : nothing}
    </div>
  `;
}

// ---- the redesigned board (WALK-C16) ---------------------------------------------------------------

/** One fix line as the dispatcher sees it: the problem's sentence and a button that marks it
 *  applied. Applied fixes stay struck through on the strip and are not drawn again until the
 *  board is read. The strip never invents its own sentences: it reads boardProblemLine. */
function fixLine(model: BoardModel, p: BoardProblem): TemplateResult {
  const key = `${p.runNo}:${p.kind}`;
  const applied = model.appliedFixes.has(key);
  return html`
    <li class="problem ${applied ? "applied" : ""}" data-problem-kind=${p.kind}>
      <span class="problem-line" data-problem-line>${boardProblemLine(p)}</span>
      <button type="button" class="link" data-action="apply-fix" data-fix-runno=${p.runNo} data-fix-kind=${p.kind} aria-pressed=${applied ? "true" : "false"}>${applied ? "Undo" : "Dismiss"}</button>
    </li>
  `;
}

/** The problems strip: a one-line-per-row list of what the day needs. The strip sits above the
 *  board and disappears when there are none, so a clean day is silent. */
function problemsStrip(model: BoardModel, view: DispatchLoadBoardView): TemplateResult | typeof nothing {
  const problems = boardProblems(view);
  if (problems.length === 0) return nothing;
  return html`
    <section class="problems" data-problems aria-label="What the day needs">
      <h3 class="problems-head">What the day needs<span class="count">${problems.length}</span></h3>
      <ul class="problem-list">${problems.map((p) => fixLine(model, p))}</ul>
    </section>
  `;
}

/** The time axis: hours across the top, a column per truck, stops placed by stop_no. The axis
 *  is for the redesign board's time view: today only, twelve hours, one cell per slot. The view
 *  never draws money of its own; the rows are the same trucks the load board has. */
function timeAxisRow(view: DispatchLoadBoardView, model: BoardModel): TemplateResult {
  const rows = yardLoadOrder(view.loads);
  const day = view.date;
  return html`
    <div class="time-axis" data-time-axis>
      <div class="time-axis-grid" role="grid" aria-label="Day time axis">
        <div class="time-axis-head" role="row">
          <div class="time-axis-cell head-run" role="columnheader">Run</div>
          ${TIME_AXIS_HOURS.map((h) => html`<div class="time-axis-cell head-hour" role="columnheader">${h}</div>`)}
        </div>
        ${rows.map((row) => {
          const slots = deckSlotsForStops(row.stops, day);
          return html`
            <div class="time-axis-row" role="row" data-axis-run-id=${row.run.id}>
              <div class="time-axis-cell axis-run" role="rowheader">
                <button type="button" class="link" data-action="select-run" data-run-id=${row.run.id} aria-pressed=${model.selectedRunId === row.run.id ? "true" : "false"}>${row.run.run_no}</button>
              </div>
              ${TIME_AXIS_HOURS.map((h) => {
                const inHour = row.stops.some((s) => slots.has(s.stop_no) && Number(s.stop_no) === h + 1);
                return html`<div class="time-axis-cell ${inHour ? "has-stop" : ""}" role="gridcell" data-hour=${h}></div>`;
              })}
            </div>
          `;
        })}
      </div>
    </div>
  `;
}

/** The run-detail drawer: opens when a run is named on the time axis or the load board. The
 *  drawer shows the crew, the gauges, the net payload (with the Moffett figure when present),
 *  and the load list, all in one column so it reads on a phone. */
function runDetailDrawer(model: BoardModel, view: DispatchLoadBoardView): TemplateResult | typeof nothing {
  if (model.selectedRunId === "") return nothing;
  const row = view.loads.find((r) => r.run.id === model.selectedRunId);
  if (row === undefined) return nothing;
  const truck = view.loads.find((r) => r.run.id === model.selectedRunId)?.run.truck_id
    ? model.trucks.find((t) => t.id === row.run.truck_id) ?? null
    : null;
  const hasMoffett = carriesMoffett(truck);
  const g = row.gauge;
  return html`
    <aside class="run-detail" data-run-detail data-run-id=${row.run.id} aria-label="Run detail">
      <header class="run-detail-head">
        <h3>${row.run.run_no}</h3>
        ${statusPill(row.run.status, RUN_LABEL[row.run.status] ?? row.run.status)}
        <span class="spacer"></span>
        <button type="button" class="btn btn-secondary small-btn" data-action="run-close">Close</button>
      </header>
      <p class="small">${row.run.truck_code ?? "No truck"} · ${row.run.driver_name ?? "No driver"}</p>
      <p class="small dim" data-run-payload>${netPayloadWords(g.capacity_kg, hasMoffett)}</p>
      ${weightGauge(g.weight_kg, g.capacity_kg)}
      ${typeof g.capacity_m3 === "number" ? gauge("Volume", typeof g.volume_m3 === "number" ? toCuYd(g.volume_m3) : null, toCuYd(g.capacity_m3), "cu yd") : nothing}
      <ul class="loads">
        ${row.stops.map(
          (s) => html`<li class="run-detail-stop" data-stop-id=${s.id} data-order=${s.order_no ?? ""}>
            <span class="order">${s.order_no ?? "Order"}</span>
            <span class="small">stop ${s.stop_no}</span>
          </li>`,
        )}
      </ul>
    </aside>
  `;
}

/** The suggest-runs panel: a list of runs the core would accept a load onto, drawn from
 *  suggestRunsForLoad. The panel is opened from a load's card and closes with the same press. */
function suggestedRunsPanel(model: BoardModel, view: DispatchLoadBoardView): TemplateResult | typeof nothing {
  if (model.suggestStopId === "") return nothing;
  const all: DispatchLoadStop[] = [...view.staging, ...view.loads.flatMap((r) => r.stops)];
  const stop = all.find((s) => s.id === model.suggestStopId);
  if (stop === undefined) return nothing;
  const suggestions = suggestRunsForLoad(view, stop);
  return html`
    <section class="suggest" data-suggest aria-label="Suggested runs">
      <header class="suggest-head">
        <h3>Suggested runs for ${stop.order_no ?? "this load"}</h3>
        <span class="spacer"></span>
        <button type="button" class="btn btn-secondary small-btn" data-action="suggest-close">Close</button>
      </header>
      ${suggestions.length === 0
        ? html`<p class="dim" data-suggest-empty>No truck can take it on this day. Open one or move the delivery.</p>`
        : html`<ul class="suggest-list" data-suggest-list>
            ${suggestions.map((s) => html`<li class="suggest-row" data-suggest-run-id=${s.runId}>
              <strong>${s.runNo}</strong>
              <span class="small">${s.truckCode}</span>
              <span class="small dim">${s.reason}</span>
            </li>`)}
          </ul>`}
    </section>
  `;
}

/** The live view indicator: a small dot that goes amber while a reload is in flight, and a
 *  reading line when the board is being read. The board does not keep its own clock: the dot
 *  is the only signal, and it sits in the corner so the dispatcher can keep eyes on the work. */
function liveView(model: BoardModel): TemplateResult {
  const busy = model.loading || model.busy;
  return html`<p class="live" data-live data-busy=${busy ? "true" : "false"}><span class="live-dot" aria-hidden="true"></span>${busy ? "Reading the board" : "Live"}</p>`;
}

function gapsPanel(model: BoardModel): TemplateResult {
  const gaps = model.gaps;
  if (gaps === null) return html``;
  const items = gaps.items ?? [];
  return html`
    <section class="panel" data-gaps>
      <div class="panel-head"><h3>Short for the loads on the board</h3></div>
      ${!gaps.checked
        ? html`<p class="dim">Stock was not checked for this day. A clear list here does not mean the yard has it all.</p>`
        : items.length === 0
          ? html`<p class="dim"><strong>Nothing is short.</strong> Every load on the board has its material on hand.</p>`
          : html`<div class="scroll"><table class="stack">
              <thead><tr><th>Order</th><th>Product</th><th class="num">Needed</th><th class="num">On hand</th><th class="num">Short</th></tr></thead>
              <tbody>
                ${items.map((g) => html`<tr>
                  <td data-label="Order" class="code">${g.order_no ?? ""}</td>
                  <td data-label="Product"><span class="who">${g.description ?? g.sku}</span><span class="small dim code">${g.sku}</span></td>
                  <td data-label="Needed" class="num">${fmtNum(g.qty_needed)} ${g.uom ?? ""}</td>
                  <td data-label="On hand" class="num">${fmtNum(g.qty_on_hand)} ${g.uom ?? ""}</td>
                  <td data-label="Short" class="num stop">${fmtNum(g.qty_short)} ${g.uom ?? ""}</td>
                </tr>`)}
              </tbody>
            </table></div>`}
    </section>
  `;
}

function weekStrip(model: BoardModel): TemplateResult | typeof nothing {
  if (model.calendar.length === 0) return nothing;
  return html`
    <section class="week" aria-label="The week ahead" data-week>
      ${model.calendar.map(
        (d) => html`<button type="button" class="day" data-action="goto-day" data-day=${d.date} aria-current=${d.date === model.date ? "true" : "false"}>
          <span class="kicker">${formatDayHeading(d.date).split(",")[0]}</span>
          <span class="day-no">${shortDay(d.date)}</span>
          <span class="small">${d.stops} ${d.stops === 1 ? "stop" : "stops"}${d.unassigned > 0 ? `, ${d.unassigned} not on a truck` : ""}</span>
        </button>`,
      )}
    </section>
  `;
}

/** The span view: the week ahead as a table, one row a day, each row opening that day's board. */
function weekTable(model: BoardModel): TemplateResult {
  const rows = model.calendar;
  return html`
    <section class="panel" data-week-table>
      <div class="panel-head"><h3>The week ahead</h3></div>
      ${rows.length === 0
        ? html`<p class="dim">The week could not be read. Refresh to try again.</p>`
        : html`<div class="scroll"><table class="stack">
            <thead><tr><th>Day</th><th class="num">Trucks</th><th class="num">Stops</th><th class="num">Not on a truck</th><th class="num">Requested</th><th></th></tr></thead>
            <tbody>
              ${rows.map((d) => html`<tr>
                <td data-label="Day"><span class="who">${formatDayHeading(d.date)}</span></td>
                <td data-label="Trucks" class="num">${d.runs}</td>
                <td data-label="Stops" class="num">${d.stops}</td>
                <td data-label="Not on a truck" class="num">${d.unassigned}</td>
                <td data-label="Requested" class="num">${d.requested}</td>
                <td><button type="button" class="btn btn-secondary small-btn" data-action="goto-day" data-day=${d.date}>Open day</button></td>
              </tr>`)}
            </tbody>
          </table></div>`}
    </section>
  `;
}

export function renderLoadBoard(model: BoardModel): TemplateResult {
  const raw = model.view;
  if (raw === null) return html``;
  if (model.span === "WEEK") return html`${quickCards(model, raw)}${weekTable(model)}`;
  const view = filterBoard(raw, model.card);
  return html`
    ${quickCards(model, raw)}
    ${weekStrip(model)}
    ${problemsStrip(model, view)}
    <div class="board-bar">
      <div class="segs" role="group" aria-label="Group the board">
        <button type="button" class="seg" data-action="axis" data-axis="TRUCK" aria-pressed=${model.axis === "TRUCK" ? "true" : "false"}>By truck</button>
        <button type="button" class="seg" data-action="axis" data-axis="STATUS" aria-pressed=${model.axis === "STATUS" ? "true" : "false"}>By status</button>
      </div>
      <button type="button" class="btn btn-secondary small-btn" data-action="auto-assign" ?disabled=${model.busy}>Auto assign</button>
    </div>
    ${bulkBar(model, view)}
    ${model.axis === "STATUS"
      ? statusLanes(model, view)
      : html`
          <div class="board">
            <section class="staging" data-lane="staging">
              <h3>Not on a truck<span class="count" data-staging-count>${view.staging.length}</span></h3>
              <p class="small dim">${view.staging.length > 0 ? `Next ${view.working_days} days, soonest first.` : "Nothing waiting. Every request in the next " + view.working_days + " days is on a truck."}</p>
              <ul class="loads">
                ${view.staging.map((s) => loadCard(model, view, s, view.date, { selectable: false }))}
              </ul>
            </section>
            <div class="trucks" data-lane="trucks">
              ${view.loads.length > 0
                ? view.loads.map((row) => truckLane(model, view, row))
                : html`<div class="panel empty">
                    <strong>No trucks on the board for ${formatDayHeading(view.date)}.</strong>
                    <p class="dim">Open a truck for the day, then put loads on it from the left.</p>
                  </div>`}
            </div>
          </div>
        `}
    ${timeAxisRow(view, model)}
    ${runDetailDrawer(model, view)}
    ${suggestedRunsPanel(model, view)}
    ${gapsPanel(model)}
  `;
}

// ---- the day board -------------------------------------------------------------------------------

function dayStopCard(model: BoardModel, stop: DispatchStop, runs: readonly DispatchRun[]): TemplateResult {
  const actions = stopActions(stop.status, { assigned: Boolean(stop.run_id) });
  const hold = model.holds.get(stop.id);
  const holdLine = holdWords(hold);
  const reschedBlock = rescheduleBlockedReason(stop);
  const planned = runs.filter((r) => r.status === "PLANNED");
  return html`
    <li class="load" data-stop-id=${stop.id} data-order=${stop.order_no ?? ""} data-status=${stop.status}>
      <div class="load-head">
        <span class="order">${stop.order_no ?? "Order"}</span>
        ${stop.stop_no ? html`<span class="small">stop ${stop.stop_no}</span>` : nothing}
      </div>
      <div class="who">${stop.customer_name || "Customer"}</div>
      <div class="small dim">${addressLine(stop) || "No address on file"}${stop.time_window ? ` · ${stop.time_window}` : ""}</div>
      <div class="facts">
        <span class="status ${stop.commit_state === "COMMITTED" ? "status-good" : "status-plain"}" data-date-state>${dateStateLabel(stop.commit_state)}</span>
        ${askDiffers(stop) ? html`<span class="small dim">asked ${stop.requested_date}</span>` : nothing}
        ${weightFact(stop)}
        ${holdLine !== "" ? html`<span class="status status-stop" data-hold>Held for payment</span>` : nothing}
      </div>
      ${holdLine !== "" ? html`<p class="small hold-words" data-hold-words>${holdLine}</p>` : nothing}
      ${stop.pod_received_by
        ? html`<p class="small good">Signed by ${stop.pod_received_by}${stop.pod_at ? ` at ${formatTime(stop.pod_at)}` : ""}${stop.handoff_at ? " · handed to billing" : ""}</p>`
        : nothing}
      ${stop.failure_reason ? html`<p class="small bad">${stop.failure_reason}</p>` : nothing}
      ${!stop.run_id && stop.status === "READY" && planned.length > 0 ? html`<p class="small dim" data-commit-note>${PUT_ON_TRUCK_COMMIT_NOTE}</p>` : nothing}
      <div class="actions">
        ${actions.map((a) =>
          a.action === "POD"
            ? html`<span class="small dim" data-pod-note>The driver signs for the delivery on Drive.</span>`
            : html`<button type="button" class="btn btn-secondary small-btn"
                data-action=${a.action === "FAILED" ? "open-sheet" : "stop-status"}
                data-sheet-kind=${a.action === "FAILED" ? "fail-stop" : ""}
                data-status-to=${a.action} data-stop-id=${stop.id} ?disabled=${model.busy}>${a.label}</button>`,
        )}
        ${reschedBlock === ""
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="reschedule-stop" data-stop-id=${stop.id} ?disabled=${model.busy}>Move date</button>`
          : nothing}
        ${!stop.run_id && stop.status === "READY" && planned.length > 0
          ? html`<select class="control pick" data-action="assign-pick" data-stop-id=${stop.id} aria-label=${`Put ${stop.order_no ?? "this stop"} on a run`}>
              <option data-run-id="">Put on a run</option>
              ${planned.map((r) => html`<option data-run-id=${r.id}>${r.run_no}${r.truck_code ? ` · ${r.truck_code}` : ""}</option>`)}
            </select>`
          : nothing}
        ${scanTrailButton(model, stop)}
        ${splitButton(model, stop)}
      </div>
      ${sheetFor(model, ["fail-stop", "reschedule-stop", "scan-trail", "split-freight"], stop.id)}
    </li>
  `;
}

function dayRun(model: BoardModel, day: DispatchBoard, run: DispatchRun): TemplateResult {
  const stops = day.stops.filter((s) => s.run_id === run.id).sort((a, b) => a.stop_no - b.stop_no);
  const crewed = Boolean(run.truck_id && run.driver_id);
  const editable = !(run.commit?.committed && !run.commit.can_reopen);
  const didNot = stops.filter((s) => s.status === "FAILED" || s.status === "CANCELLED");
  return html`
    <section class="truck day-run" data-run-id=${run.id} data-run-no=${run.run_no} data-run-status=${run.status}>
      <header class="truck-head">
        <h3>${run.run_no}</h3>
        ${statusPill(run.status, RUN_LABEL[run.status] ?? run.status)}
        ${run.commit?.committed ? html`<span class="status status-good" data-committed>Committed</span>` : nothing}
        <span class="spacer"></span>
        ${printManifestButton(model, run)}
        ${run.status === "PLANNED"
          ? html`<button type="button" class="btn btn-primary small-btn" data-action="dispatch-run" data-run-id=${run.id}
                ?disabled=${model.busy}>Dispatch</button>
              <button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="cancel-run" data-run-id=${run.id} ?disabled=${model.busy || !editable}>Cancel truck</button>`
          : nothing}
        ${run.status === "DISPATCHED"
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="complete-run" data-run-id=${run.id} ?disabled=${model.busy}>Complete run</button>`
          : nothing}
      </header>
      ${crewLine(model, run, editable)}
      ${laneRefusal(model, run.id)}
      ${run.status === "PLANNED" && !crewed
        ? html`<p class="small dim" data-dispatch-why>A truck needs a truck and a driver before it goes.</p>`
        : nothing}
      <p class="strip" data-strip>
        ${BOARD_COLUMNS.map((col) => html`<span class="strip-cell"><strong>${stops.filter((s) => s.status === col).length}</strong> ${STOP_LABEL[col]?.toLowerCase()}</span>`)}
      </p>
      <div class="columns">
        ${BOARD_COLUMNS.map(
          (col) => html`<div class="col ${stops.some((s) => s.status === col) ? "has" : "none"}" data-col=${col}>
            <h4>${STOP_LABEL[col]}<span class="count">${stops.filter((s) => s.status === col).length}</span></h4>
            <ul class="loads">${stops.filter((s) => s.status === col).map((s) => dayStopCard(model, s, day.runs))}</ul>
          </div>`,
        )}
      </div>
      ${didNot.length > 0
        ? html`<div class="col did-not" data-col="DID_NOT_GO"><h4>Did not go<span class="count">${didNot.length}</span></h4>
            <ul class="loads">${didNot.map((s) => dayStopCard(model, s, day.runs))}</ul></div>`
        : nothing}
      ${sheetFor(model, ["reopen-run", "reschedule-run", "cancel-run", "dispatch-run"], run.id)}
    </section>
  `;
}

export function renderDayBoard(model: BoardModel): TemplateResult {
  const day = model.day;
  if (day === null) return html``;
  return html`
    <section class="staging" data-lane="unassigned">
      <h3>Not on a truck yet<span class="count">${day.unassigned.length}</span></h3>
      ${day.unassigned.length > 0
        ? html`<ul class="loads wide">${day.unassigned.map((s) => dayStopCard(model, s, day.runs))}</ul>`
        : html`<p class="small dim">Nothing waiting. Every scheduled stop for this day is on a run.</p>`}
    </section>
    ${day.runs.length > 0
      ? day.runs.map((run) => dayRun(model, day, run))
      : html`<div class="panel empty" data-no-runs><strong>No runs on the board for ${formatDayHeading(day.date)}.</strong>
          <p class="dim">Open a truck with New truck, then put loads on it.</p></div>`}
    <section class="panel matrix" aria-label="How a stop moves">
      <h3>How a stop moves</h3>
      <p class="dim">Ready, then Loaded, then En route, then Delivered. A stop can be cancelled before it leaves, and could not be delivered once it is on the road. The board only offers the move the stop is allowed, and the driver signs for the delivery on Drive.</p>
    </section>
  `;
}

// ---- the schedule --------------------------------------------------------------------------------

function scheduleForm(model: BoardModel): TemplateResult {
  const text = (name: string, label: string): TemplateResult =>
    html`<label>${label}<input class="control" type="text" data-field=${name} autocomplete="off" /></label>`;
  return html`
    <section class="panel" aria-label="Schedule a delivery">
      <div class="panel-head"><h2>Schedule a delivery</h2></div>
      <form class="sched-form" data-schedule novalidate>
        <label class="wide">Order number
          <input class="control" type="text" data-field="order" autocomplete="off" maxlength="64" aria-describedby="order-hint" />
          <span class="small dim" id="order-hint">Order number on the paperwork, like SO-1041.</span>
        </label>
        <label>Delivery day<input class="control" type="date" data-field="day" /></label>
        ${text("window", "Time window")}
        ${text("contact", "Site contact")}
        <label>Site phone<input class="control" type="tel" data-field="phone" autocomplete="off" /></label>
        <label class="wide">Delivery address<input class="control" type="text" data-field="address" autocomplete="off" /></label>
        ${text("city", "City")}
        ${text("postal", "Postal code")}
        <div class="wide map-pin" role="group" aria-labelledby="map-pin-label" aria-describedby="map-pin-hint">
          <span class="map-pin-label" id="map-pin-label">Map pin (optional)</span>
          <div class="map-pin-pair">
            <input class="control" type="text" inputmode="decimal" data-field="lat" placeholder="32.7157" aria-label="Map pin latitude" aria-describedby="map-pin-hint" autocomplete="off" />
            <input class="control" type="text" inputmode="decimal" data-field="lon" placeholder="-117.1611" aria-label="Map pin longitude" aria-describedby="map-pin-hint" autocomplete="off" />
          </div>
          <span class="small dim" id="map-pin-hint">For a job site without a street address, enter both coordinates or leave both blank.</span>
        </div>
        <label class="wide">Instructions for the driver<input class="control" type="text" data-field="instructions" maxlength="400" autocomplete="off" /></label>
        <p class="small dim wide">A date scheduled here is a request. ${PUT_ON_TRUCK_COMMIT_NOTE}</p>
        ${model.scheduleError !== "" ? html`<p class="alert alert-error wide" role="alert" data-schedule-error>${model.scheduleError}</p>` : nothing}
        <div class="actions wide">
          <button type="button" class="btn btn-primary" data-action="schedule-submit" ?disabled=${model.busy}>Schedule delivery</button>
        </div>
      </form>
    </section>
  `;
}

function booksStop(model: BoardModel, stop: DispatchStop): TemplateResult {
  const block = rescheduleBlockedReason(stop);
  return html`
    <li class="load" data-stop-id=${stop.id} data-order=${stop.order_no ?? ""} data-status=${stop.status}>
      <div class="load-head">
        <span class="order">${stop.order_no ?? "Order"}</span>
        ${statusPill(stop.status, STOP_LABEL[stop.status] ?? stop.status)}
      </div>
      <div class="who">${stop.customer_name || "Customer"}</div>
      <div class="small dim">${addressLine(stop) || "No address on file"}${stop.time_window ? ` · ${stop.time_window}` : ""}</div>
      <div class="facts">
        <span class="status ${stop.commit_state === "COMMITTED" ? "status-good" : "status-plain"}" data-date-state>${dateStateLabel(stop.commit_state)}</span>
        ${askDiffers(stop) ? html`<span class="small dim">asked ${stop.requested_date}</span>` : nothing}
        <span class="fact">${stop.run_id ? (stop.run_no ?? "On a truck") : "Not on a truck"}</span>
      </div>
      ${block === ""
        ? html`<div class="actions"><button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="reschedule-stop" data-stop-id=${stop.id} ?disabled=${model.busy}>Move date</button></div>`
        : html`<p class="small dim" data-move-why>${block}</p>`}
      <div class="actions">${scanTrailButton(model, stop)}${splitButton(model, stop)}</div>
      ${sheetFor(model, ["reschedule-stop", "scan-trail", "split-freight"], stop.id)}
    </li>
  `;
}

function booksRun(model: BoardModel, run: DispatchRun): TemplateResult {
  const movable = run.status === "PLANNED";
  return html`
    <li class="load" data-books-run=${run.run_no} data-run-id=${run.id}>
      <div class="load-head">
        <span class="order">${run.run_no}</span>
        ${statusPill(run.status, RUN_LABEL[run.status] ?? run.status)}
      </div>
      <div class="small dim">${run.truck_code ?? "No truck yet"} · ${run.stop_count} ${run.stop_count === 1 ? "stop" : "stops"}</div>
      <div class="actions">
        ${printManifestButton(model, run)}
        ${movable
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="reschedule-run" data-run-id=${run.id} ?disabled=${model.busy}>Move truck</button>`
          : nothing}
      </div>
      ${movable ? nothing : html`<p class="small dim">A truck that has left is a record, not a plan.</p>`}
      ${sheetFor(model, ["reschedule-run"], run.id)}
    </li>
  `;
}

/** The schedule tab: the form, the week ahead, and the day's books (every stop and every truck), each date movable. */
export function renderSchedule(model: BoardModel): TemplateResult {
  const day = model.day;
  const stops = day === null ? [] : [...day.unassigned, ...day.stops];
  return html`
    ${scheduleForm(model)}
    ${weekStrip(model)}
    <section class="panel" data-books>
      <div class="panel-head"><h3>On the books for ${formatDayHeading(model.date)}<span class="count">${stops.length}</span></h3></div>
      ${stops.length > 0
        ? html`<ul class="loads wide">${stops.map((s) => booksStop(model, s))}</ul>`
        : html`<p class="dim" data-books-empty>Nothing scheduled for ${formatDayHeading(model.date)}.</p>`}
      ${day !== null && day.runs.length > 0
        ? html`<h4>Trucks that day</h4><ul class="loads wide">${day.runs.map((r) => booksRun(model, r))}</ul>`
        : nothing}
    </section>
  `;
}

// ---- the registry --------------------------------------------------------------------------------

function toggleButton(model: BoardModel, kind: "truck" | "driver", id: string, active: boolean): TemplateResult {
  return html`<button type="button" class="btn btn-secondary small-btn" data-action="fleet-toggle" data-fleet-kind=${kind} data-fleet-id=${id} data-active-to=${active ? "false" : "true"} ?disabled=${model.busy}>${active ? "Take out of use" : "Put back in use"}</button>`;
}

function truckCard(model: BoardModel, t: DispatchTruck): TemplateResult {
  return html`
    <li class="load" data-truck-id=${t.id} data-truck-code=${t.code} data-active=${t.is_active ? "true" : "false"}>
      <div class="load-head">
        <span class="order">${t.code}</span>
        <span class="status ${t.is_active ? "status-good" : "status-plain"}" data-fleet-state>${fleetStateWord(t.is_active)}</span>
      </div>
      <div class="who">${TRUCK_TYPE_LABEL[t.truck_type] ?? t.truck_type}</div>
      ${t.name ? html`<div class="small dim">${t.name}</div>` : nothing}
      <div class="facts">${truckLine(t).map((f) => html`<span class="fact">${f}</span>`)}</div>
      <div class="actions">
        <button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="edit-truck" data-fleet-id=${t.id} ?disabled=${model.busy}>Change</button>
        ${toggleButton(model, "truck", t.id, t.is_active)}
      </div>
      ${sheetFor(model, ["edit-truck"], t.id)}
    </li>
  `;
}

function driverCard(model: BoardModel, d: DispatchDriver): TemplateResult {
  return html`
    <li class="load" data-driver-id=${d.id} data-driver-name=${d.name} data-active=${d.is_active ? "true" : "false"}>
      <div class="load-head">
        <span class="who">${d.name}</span>
        <span class="status ${d.is_active ? "status-good" : "status-plain"}" data-fleet-state>${fleetStateWord(d.is_active)}</span>
      </div>
      <div class="facts">${driverLine(d).map((f) => html`<span class="fact">${f}</span>`)}</div>
      <div class="actions">
        <button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="edit-driver" data-fleet-id=${d.id} ?disabled=${model.busy}>Change</button>
        ${toggleButton(model, "driver", d.id, d.is_active)}
      </div>
      ${sheetFor(model, ["edit-driver"], d.id)}
    </li>
  `;
}

/** The registry tab: every truck and driver of the yard, in use or not, each changeable by the manage level. */
export function renderFleet(model: BoardModel): TemplateResult {
  return html`
    <section class="panel" data-trucks>
      <div class="panel-head">
        <h2>Trucks<span class="count">${model.trucks.length}</span></h2>
        <button type="button" class="btn btn-primary small-btn" data-action="open-sheet" data-sheet-kind="add-truck" data-fleet-id="new" ?disabled=${model.busy}>Add truck</button>
      </div>
      ${sheetFor(model, ["add-truck"], "new")}
      ${model.trucks.length > 0
        ? html`<ul class="loads wide">${model.trucks.map((t) => truckCard(model, t))}</ul>`
        : html`<p class="dim">No trucks at this yard yet. Add the first one.</p>`}
    </section>
    <section class="panel" data-drivers>
      <div class="panel-head">
        <h2>Drivers<span class="count">${model.drivers.length}</span></h2>
        <button type="button" class="btn btn-primary small-btn" data-action="open-sheet" data-sheet-kind="add-driver" data-fleet-id="new" ?disabled=${model.busy}>Add driver</button>
      </div>
      ${sheetFor(model, ["add-driver"], "new")}
      ${model.drivers.length > 0
        ? html`<ul class="loads wide">${model.drivers.map((d) => driverCard(model, d))}</ul>`
        : html`<p class="dim">No drivers at this yard yet. Add the first one.</p>`}
    </section>
    <p class="small dim" data-fleet-note>Adding and changing trucks and drivers is for the Logistics Manager and a Branch Manager. A truck or driver out of use stays on the books and leaves the pick lists.</p>
  `;
}

// ---- will call -----------------------------------------------------------------------------------

function willCallTag(model: BoardModel): TemplateResult | typeof nothing {
  const minted = model.willCall.minted;
  if (minted === null) return nothing;
  return html`
    <section class="panel tag" data-tag aria-label="The pickup tag">
      <p class="once" data-once><strong>Shown once.</strong> Print this tag now. The code is not kept and cannot be shown again.</p>
      <p class="tag-order">${minted.order}</p>
      <p class="tag-code" data-tag-code>${minted.code.token}</p>
      <p class="small dim">${willCallLine(minted.code)}</p>
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="wc-print">Print tag</button>
        <button type="button" class="btn btn-secondary" data-action="wc-dismiss">Done, it is printed</button>
      </div>
    </section>
  `;
}

function willCallRows(model: BoardModel): TemplateResult | typeof nothing {
  const w = model.willCall;
  if (!w.looked) return nothing;
  if (w.items.length === 0) return html`<p class="dim" data-wc-empty>No pickup codes for ${w.order} yet.</p>`;
  return html`
    <ul class="loads wide" data-wc-list>
      ${w.items.map((c) => html`
        <li class="load" data-code-id=${c.id} data-code-status=${c.status}>
          <div class="load-head">
            <span class="status ${c.status === "ACTIVE" ? "status-good" : c.status === "CONFIRMED" ? "status-watch" : "status-plain"}" data-code-state>${willCallWord(c.status)}</span>
          </div>
          <p class="small">${willCallLine(c)}</p>
          ${c.status === "ACTIVE"
            ? html`<div class="actions"><button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="revoke-willcall" data-code-id=${c.id} ?disabled=${model.busy}>Cancel code</button></div>`
            : nothing}
          ${sheetFor(model, ["revoke-willcall"], c.id)}
        </li>`)}
    </ul>
  `;
}

/** The will call tab: name an order, see its pickup codes, print a new one. The customer's own page is a separate screen. */
export function renderWillCall(model: BoardModel): TemplateResult {
  const w = model.willCall;
  return html`
    <section class="panel" data-willcall>
      <div class="panel-head"><h2>Will call pickup codes</h2></div>
      <div class="wc-form">
        <label class="wc-order">Order number
          <input class="control" type="text" data-field="wc-order" autocomplete="off" maxlength="64" aria-describedby="wc-hint" />
          <span class="small dim" id="wc-hint">Order number on the paperwork, like SO-1041.</span>
        </label>
        <div class="actions">
          <button type="button" class="btn btn-secondary" data-action="wc-lookup" ?disabled=${model.busy}>Look up</button>
          <button type="button" class="btn btn-primary" data-action="wc-mint" ?disabled=${model.busy}>Print a pickup code</button>
        </div>
      </div>
      ${w.error !== "" ? html`<p class="alert alert-error" role="alert" data-wc-error>${w.error}</p>` : nothing}
    </section>
    ${willCallTag(model)}
    <section class="panel" data-wc-codes>${willCallRows(model)}${w.looked ? nothing : html`<p class="dim" data-wc-idle>Name an order to see its pickup codes. A code is printed only for a will call order that has been picked.</p>`}</section>
  `;
}

// ---- the rules -----------------------------------------------------------------------------------

/** The settings tab: the yard's dispatch rules in words, a way to change them, and the trail of who changed what. */
export function renderSettings(model: BoardModel): TemplateResult {
  const p = model.policy;
  if (p === null) return html``;
  const rows = model.history.map(historyRow);
  return html`
    <section class="panel" data-rules>
      <div class="panel-head">
        <h2>How dispatch runs at ${model.branchName}</h2>
        <button type="button" class="btn btn-primary small-btn" data-action="open-sheet" data-sheet-kind="edit-policy" data-fleet-id="policy" ?disabled=${model.busy}>Change the rules</button>
      </div>
      ${sheetFor(model, ["edit-policy"], "policy")}
      <ul class="rules">
        ${policyRows(p).map((r) => html`
          <li data-rule=${r.key}>
            <span class="rule-name">${r.label}</span>
            <strong class="rule-word" data-rule-word>${r.word}</strong>
            <span class="small dim">${r.note}</span>
          </li>`)}
      </ul>
    </section>
    <section class="panel" data-history>
      <div class="panel-head"><h3>What changed</h3></div>
      ${model.historyFault !== ""
        ? html`<p class="dim" data-history-fault>${model.historyFault}</p>`
        : rows.length === 0
          ? html`<p class="dim" data-history-empty>No rule has been changed at this yard.</p>`
          : html`<div class="scroll"><table class="stack">
              <thead><tr><th>Rule</th><th>Was</th><th>Now</th><th>Who</th><th>When</th></tr></thead>
              <tbody>
                ${rows.map((r) => html`<tr data-history-row>
                  <td data-label="Rule"><span class="who">${r.rule}</span></td>
                  <td data-label="Was">${r.was}</td>
                  <td data-label="Now"><strong>${r.now}</strong></td>
                  <td data-label="Who">${r.who}</td>
                  <td data-label="When">${r.when}</td>
                </tr>`)}
              </tbody>
            </table></div>`}
    </section>
  `;
}

// ---- the screen ----------------------------------------------------------------------------------

export function renderDesk(model: BoardModel): TemplateResult {
  if (model.phase === "loading") {
    return html`<p class="dim" role="status" data-loading>Reading the board...</p>`;
  }
  if (model.phase === "error") {
    return html`
      ${renderMessages(model)}
      ${model.canRetry !== false ? html`<button type="button" class="btn btn-primary" data-action="reload">Try again</button>` : nothing}
    `;
  }
  return html`
    <div class="head">
      <h1>Dispatch and Load Building</h1>
    </div>
    ${tabs(model)}
    ${toolbar(model)}
    ${model.tab === "load" ? liveView(model) : nothing}
    ${renderMessages(model)}
    ${raiseCard(model)}
    ${model.loading
      ? html`<p class="alert" role="status" data-loading>Reading the board for ${formatDayHeading(model.date)}...</p>`
      : model.tab === "load"
        ? renderLoadBoard(model)
        : model.tab === "schedule"
          ? renderSchedule(model)
          : model.tab === "fleet"
            ? renderFleet(model)
            : model.tab === "willcall"
              ? renderWillCall(model)
              : model.tab === "settings"
                ? renderSettings(model)
                : renderDayBoard(model)}
  `;
}
