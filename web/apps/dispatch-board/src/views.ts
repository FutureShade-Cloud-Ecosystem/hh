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
  DispatchLoadBoardView,
  DispatchLoadRow,
  DispatchLoadStop,
  DispatchRun,
  DispatchStop,
  DispatchStopPaymentHold,
  DispatchTruck,
} from "@hardscapehouse/api-client";
import {
  BOARD_COLUMNS,
  CARD_LABEL,
  CARD_ORDER,
  RUN_LABEL,
  STATUS_LANES,
  STOP_LABEL,
  addressLine,
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
  formatDayHeading,
  formatTime,
  groupByStatus,
  holdWords,
  laneScopeNote,
  loadableStops,
  pickupSplitLabel,
  rescheduleBlockedReason,
  rowEditable,
  stopActions,
  stopBlockedReason,
  targetLine,
  targetName,
  unloadableSummary,
  type Band,
  type CardId,
} from "./dispatch-logic.js";

export type Tab = "load" | "day";
export type Axis = "TRUCK" | "STATUS";

/** An inline form open on one card: a date and a reason, a reason alone, or a new truck. */
export interface Sheet {
  readonly kind: "reschedule-stop" | "reschedule-run" | "reopen-stop" | "reopen-run" | "fail-stop" | "new-run";
  readonly id: string;
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
  readonly notice: string;
  /** A neutral sentence from the core that is neither a success nor a failure (the auto assign seam). */
  readonly info: string;
  readonly refusals: readonly string[];
  readonly view: DispatchLoadBoardView | null;
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
}

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

// ---- the inline sheet ----------------------------------------------------------------------------

function sheetPanel(sheet: Sheet, model: BoardModel): TemplateResult {
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
      <label>${prompt}<input class="control" type="text" maxlength="400" data-field="reason" /></label>
      ${sheet.error !== "" ? html`<p class="alert alert-error" role="alert" data-sheet-error>${sheet.error}</p>` : nothing}
      <div class="actions">
        <button type="button" class="btn btn-primary" data-action="sheet-submit" ?disabled=${model.busy}>${submit}</button>
        <button type="button" class="btn btn-secondary" data-action="sheet-cancel">Cancel</button>
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
      <div class="when" role="group" aria-label="Delivery day">
        <button type="button" class="btn btn-secondary day-btn" data-action="day-prev" aria-label="Previous day">&lsaquo;</button>
        <input class="control" type="date" data-field="date" aria-label="Delivery day" data-current=${model.date} />
        <button type="button" class="btn btn-secondary day-btn" data-action="day-next" aria-label="Next day">&rsaquo;</button>
        <span class="day-words" data-day-words>${formatDayHeading(model.date)}</span>
        ${model.date !== model.today
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="day-today">Today</button>`
          : nothing}
      </div>
      <div class="tools">
        <button type="button" class="btn btn-secondary small-btn" data-action="refresh" ?disabled=${model.busy}>Refresh</button>
        <button type="button" class="btn btn-primary small-btn" data-action="new-run" ?disabled=${model.busy}>New truck</button>
      </div>
    </div>
    ${sheetFor(model, ["new-run"], "new")}
  `;
}

function tabs(model: BoardModel): TemplateResult {
  const tab = (id: Tab, label: string): TemplateResult =>
    html`<button type="button" class="tab" data-action="tab" data-tab-id=${id} aria-current=${model.tab === id ? "true" : "false"}>${label}</button>`;
  const later = (label: string): TemplateResult =>
    html`<span class="tag-later">${label}<em>Later</em></span>`;
  return html`
    <nav class="tabs" aria-label="Dispatch screens">
      ${tab("load", "Load board")}${tab("day", "Day board")}
      ${later("Schedule")}${later("Trucks and drivers")}${later("Will call")}${later("Settings")}
    </nav>
  `;
}

// ---- the load board ------------------------------------------------------------------------------

function quickCards(model: BoardModel, view: DispatchLoadBoardView): TemplateResult {
  return html`
    <section class="cards" aria-label="Quick cards">
      ${CARD_ORDER.map((id) => {
        const count = cardCount(view, id);
        const scope = cardScopeLabel(view, id);
        if (id === "card.pickups") {
          const split = pickupSplitLabel(view);
          return html`<div class="tile" data-card=${id}>
            <span class="kicker">${CARD_LABEL[id]}</span>
            <span class="figure">${count}</span>
            <span class="small">${split !== "" ? split : scope}</span>
            <span class="small">Picked and collected at the counter</span>
          </div>`;
        }
        const on = model.card === id;
        return html`<button type="button" class="tile tile-btn" data-action="card" data-card=${id} aria-pressed=${on ? "true" : "false"}>
          <span class="kicker">${CARD_LABEL[id]}</span>
          <span class="figure">${count}</span>
          <span class="small">${scope}</span>
        </button>`;
      })}
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
    ? html`<span class="fact">${fmtNum(stop.weight_kg)} kg</span>`
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
      <div class="actions">
        ${targets.length > 0
          ? html`<select class="control pick" data-action="assign-pick" data-stop-id=${stop.id} aria-label=${`Put ${stop.order_no ?? "this load"} on a truck`}>
              <option data-run-id="">Put on a truck</option>
              ${targets.map((t) => html`<option data-run-id=${t.runId} ?disabled=${t.blocked !== ""}>${targetName(t)} · ${targetLine(t)}</option>`)}
            </select>`
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
      </div>
      ${sheetFor(model, ["reopen-stop", "reschedule-stop"], stop.id)}
    </li>
  `;
}

function crewLine(model: BoardModel, run: DispatchRun, editable: boolean): TemplateResult {
  if (run.status === "PLANNED" && editable) {
    return html`
      <div class="crew">
        <label>Truck
          <select class="control" data-action="crew-pick" data-run-id=${run.id} data-field="crew-truck" data-current=${run.truck_id ?? ""}>
            <option data-id="">No truck yet</option>
            ${model.trucks.map((t) => html`<option data-id=${t.id}>${t.code}</option>`)}
          </select>
        </label>
        <label>Driver
          <select class="control" data-action="crew-pick" data-run-id=${run.id} data-field="crew-driver" data-current=${run.driver_id ?? ""}>
            <option data-id="">No driver yet</option>
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
      ${gauge("Weight", g.weight_kg, g.capacity_kg, "kg")}
      ${typeof g.capacity_m3 === "number" ? gauge("Volume", g.volume_m3, g.capacity_m3, "m3") : nothing}
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
              ?disabled=${model.busy || !crewed || run.stop_count === 0}>Dispatch</button>`
          : nothing}
        ${run.status === "PLANNED" && (!crewed || run.stop_count === 0)
          ? html`<span class="small dim" data-dispatch-why>${!crewed ? "A truck needs a truck and a driver before it goes." : "Put a load on it first."}</span>`
          : nothing}
        ${run.status === "PLANNED" && editable
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="reschedule-run" data-run-id=${run.id}>Move truck date</button>
              <button type="button" class="btn btn-secondary small-btn" data-action="cancel-run" data-run-id=${run.id} ?disabled=${model.busy}>Cancel truck</button>`
          : nothing}
        ${row.commit.committed && row.commit.can_reopen && run.status === "PLANNED"
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="open-sheet" data-sheet-kind="reopen-run" data-run-id=${run.id}>Reopen truck</button>`
          : nothing}
        ${row.commit.reopened
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="clear-reopen" data-run-id=${run.id} ?disabled=${model.busy}>Clear the reopen marker</button>`
          : nothing}
      </div>
      ${sheetFor(model, ["reopen-run", "reschedule-run"], run.id)}
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
          <span class="day-no">${d.date.slice(5)}</span>
          <span class="small">${d.stops} ${d.stops === 1 ? "stop" : "stops"}${d.unassigned > 0 ? `, ${d.unassigned} not on a truck` : ""}</span>
        </button>`,
      )}
    </section>
  `;
}

export function renderLoadBoard(model: BoardModel): TemplateResult {
  const raw = model.view;
  if (raw === null) return html``;
  const view = filterBoard(raw, model.card);
  return html`
    ${quickCards(model, raw)}
    ${weekStrip(model)}
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
      <div class="actions">
        ${actions.map((a) =>
          a.action === "POD"
            ? html`<span class="small dim" data-pod-note>The driver signs for the delivery on HH Drive.</span>`
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
      </div>
      ${sheetFor(model, ["fail-stop", "reschedule-stop"], stop.id)}
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
        ${run.status === "PLANNED"
          ? html`<button type="button" class="btn btn-primary small-btn" data-action="dispatch-run" data-run-id=${run.id}
                ?disabled=${model.busy || !crewed || run.stop_count === 0}>Dispatch</button>
              <button type="button" class="btn btn-secondary small-btn" data-action="cancel-run" data-run-id=${run.id} ?disabled=${model.busy || !editable}>Cancel truck</button>`
          : nothing}
        ${run.status === "DISPATCHED"
          ? html`<button type="button" class="btn btn-secondary small-btn" data-action="complete-run" data-run-id=${run.id} ?disabled=${model.busy}>Complete run</button>`
          : nothing}
      </header>
      ${crewLine(model, run, editable)}
      ${run.status === "PLANNED" && !crewed
        ? html`<p class="small dim" data-dispatch-why>A truck needs a truck and a driver before it goes.</p>`
        : nothing}
      <div class="columns">
        ${BOARD_COLUMNS.map(
          (col) => html`<div class="col" data-col=${col}>
            <h4>${STOP_LABEL[col]}<span class="count">${stops.filter((s) => s.status === col).length}</span></h4>
            <ul class="loads">${stops.filter((s) => s.status === col).map((s) => dayStopCard(model, s, day.runs))}</ul>
          </div>`,
        )}
      </div>
      ${didNot.length > 0
        ? html`<div class="col did-not" data-col="DID_NOT_GO"><h4>Did not go<span class="count">${didNot.length}</span></h4>
            <ul class="loads">${didNot.map((s) => dayStopCard(model, s, day.runs))}</ul></div>`
        : nothing}
      ${sheetFor(model, ["reopen-run", "reschedule-run"], run.id)}
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
      <p class="dim">Ready, then Loaded, then En route, then Delivered. A stop can be cancelled before it leaves, and could not be delivered once it is on the road. The board only offers the move the stop is allowed, and the driver signs for the delivery on HH Drive.</p>
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
      <button type="button" class="btn btn-primary" data-action="reload">Try again</button>
    `;
  }
  return html`
    <div class="head">
      <h1>Dispatch and Load Building</h1>
    </div>
    ${tabs(model)}
    ${toolbar(model)}
    ${renderMessages(model)}
    ${model.tab === "load" ? renderLoadBoard(model) : renderDayBoard(model)}
  `;
}
