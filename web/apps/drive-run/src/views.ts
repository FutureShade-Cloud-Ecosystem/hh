// HH Drive's screens, as plain render functions of a model. They hold no
// state and own no events: every control is a plain element carrying a
// data-action name, and the root element's one click listener acts on it
// (the design system's position rules allow no @ or . binding here, and the
// shared header takes the same shape). Every number on a screen is a count,
// a stop number, a quantity or a weight: there is no money field to bind.
//
// References: the FB Design boards "HH: field apps" (my run, proof of
// delivery) and the original app's phone screens, drawn to HH's design
// system under row 563 (take the existing and improve it).
import { html, nothing, type TemplateResult } from "lit";
import { servedConsoleConfig } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { offlineLine } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { demoNote } from "./band-identity.js";
import { hhAddress } from "@hardscapehouse/design-system/src/address.js";
import { houseZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import type { DemoFlow } from "@hardscapehouse/api-client";
import type { DriverLine, DriverRun, DriverStop } from "./drive-api.js";
import {
  ATTESTATION,
  FAILURE_REASON_OTHER,
  FAILURE_REASON_PRESETS,
  FAILURE_REASON_SHEET_PROMPT,
  HANDBACK_BODY,
  HANDBACK_PROMPT,
  PROOF_WAITING_WORDS,
  STOP_LABEL,
  addressLine,
  canRecordFailure,
  closeLine,
  driverActions,
  formatGlossaryTime,
  loadOrderLine,
  nextStopOf,
  qtyLabel,
  plainSentence,
  runClose,
  routeStops,
  runSummary,
  signBlockedReason,
  stopsInOrder,
  weightLabel,
  type PodDraft,
  type RouteStop,
  type StopChip,
} from "./drive-logic.js";

/** The status pill's tone, by status: colour never carries the meaning alone, the word is always there. */
function tone(status: string): string {
  if (status === "DELIVERED") return "good";
  if (status === "FAILED" || status === "CANCELLED") return "stop";
  if (status === "EN_ROUTE") return "watch";
  return "plain";
}

function statusPill(status: string): TemplateResult {
  return html`<span class="status status-${tone(status)}" data-stop-status>${STOP_LABEL[status] ?? status}</span>`;
}

/** Messages the driver must see: a refusal in the core's words, or an outcome. */
export function renderMessages(error: string, notice: string): TemplateResult | typeof nothing {
  if (error === "" && notice === "") return nothing;
  return html`
    ${error !== "" ? html`<p class="alert alert-error" role="alert">${error}</p>` : nothing}
    ${notice !== "" ? html`<p class="alert alert-ok" role="status">${notice}</p>` : nothing}
  `;
}

export interface RunSheetModel {
  readonly phase: "loading" | "ready" | "error";
  readonly runs: readonly DriverRun[];
  readonly error: string;
  readonly notice: string;
  /** How stale the store's run world is, in words ("Pulled 2 min ago"). */
  readonly staleness: string;
  /** The open conflict cards: the Review door names the count. */
  readonly conflicts: number;
}

/** The staleness words for a scope: reads answer from the device, so the sheet
 * says when the world last came down, with the clock time in the dealer's zone. */
export function stalenessWords(
  staleness: { neverPulled: boolean; ageMs: number | null; lastPullAt?: string | null },
  zone: string = houseZone(),
): string {
  if (staleness.neverPulled || staleness.ageMs === null) return "Never pulled";
  const minutes = Math.floor(staleness.ageMs / 60_000);
  const relative = minutes < 1 ? "Pulled just now" : `Pulled ${minutes} min ago`;
  const clock = formatGlossaryTime(staleness.lastPullAt, zone);
  return clock !== "" ? `${relative} · as of ${clock}` : relative;
}

const CHIP_TONE: Record<StopChip, string> = {
  Delivered: "good",
  "Next stop": "watch",
  Waiting: "plain",
  "Not delivered": "stop",
  Cancelled: "stop",
};

function stopRow(entry: RouteStop<DriverStop>): TemplateResult {
  const { stop } = entry;
  const where = addressLine(stop);
  return html`
    <li class="stop-item ${entry.next ? "stop-item-next" : ""}" data-stop-card=${entry.chip}>
      <button type="button" class="stop-row" data-action="open-stop" data-stop-id=${stop.id} data-stop data-tap>
        <span class="stop-no" data-stop-no>${stop.stop_no}</span>
        <span class="stop-main">
          <span class="stop-who">${stop.customer_name || "Customer"}</span>
          <span class="stop-where">${where || "No address on file"}${stop.time_window ? ` · ${stop.time_window}` : ""}</span>
        </span>
        <span class="status status-${CHIP_TONE[entry.chip]} stop-chip" data-stop-status>${entry.chip}</span>
      </button>
    </li>
  `;
}

function runCard(run: DriverRun): TemplateResult {
  const stops = stopsInOrder(run.stops);
  const progress = runSummary(stops);
  const loadOrder = loadOrderLine(stops);
  return html`
    <section class="card run" data-run>
      <h2 class="run-title">${run.run_no}${run.truck_code ? ` · ${run.truck_code}` : ""}</h2>
      <p class="run-count">${stops.length} ${stops.length === 1 ? "stop" : "stops"}</p>
      <p class="run-progress" data-progress>${progress.sentence}</p>
      ${loadOrder !== ""
        ? html`<div class="run-load-order" data-load-order-banner>
            <p class="run-load-eyebrow">Loaded at the yard</p>
            <p class="run-load-line" data-load-order>${loadOrder}</p>
          </div>`
        : nothing}
      <ul class="stop-list">${routeStops(stops).map(stopRow)}</ul>
    </section>
  `;
}

/** The landing: today's run, the driver's own, in stop order. */
export function renderRunSheet(model: RunSheetModel): TemplateResult {
  let body: TemplateResult;
  if (model.phase === "loading" && model.runs.length === 0) {
    body = html`<p class="dim" role="status">Checking your run.</p>`;
  } else if (model.phase === "ready" && model.runs.length === 0) {
    body = html`
      <div class="card empty" data-empty>
        <p class="empty-head">No run assigned to you today.</p>
        <p class="dim">A dispatcher puts stops on your truck from the day board, and the run shows here as soon as it is crewed.</p>
      </div>
    `;
  } else if (model.phase === "ready") {
    let nextStop: DriverStop | null = null;
    for (const run of model.runs) {
      nextStop = nextStopOf(run.stops);
      if (nextStop !== null) break;
    }
    const goBar =
      nextStop === null
        ? nothing
        : html`<div class="go-bar-spacer" aria-hidden="true"></div>
          <div class="go-bar" data-go-bar>
            <button type="button" class="btn btn-primary big" data-action="open-stop" data-stop-id=${nextStop.id} data-go-next data-tap>Go to stop ${nextStop.stop_no}</button>
          </div>`;
    body = html`
      ${model.runs.map(runCard)}
      <button type="button" class="btn btn-secondary big" data-action="open-close" data-tap>End of run</button>
      ${goBar}
    `;
  } else {
    body = html``;
  }
  return html`
    <div class="run-top">
      <h1>My run</h1>
      <button type="button" class="btn btn-secondary" data-action="open-conflicts" data-tap>
        ${model.conflicts > 0 ? `Review ${model.conflicts}` : "Review"}
      </button>
    </div>
    ${model.staleness !== "" ? html`<p class="dim small" data-staleness>${model.staleness}</p>` : nothing}
    ${renderMessages(model.error, model.notice)}
    ${body}
  `;
}

/** The conflict card list: every outcome the sync came back with, dismissible,
 * never deletable (the dismiss is the store's own, and it keeps the row). */
export function renderConflicts(model: { cards: number }): TemplateResult {
  return html`
    <button type="button" class="back" data-action="back" data-tap>Back to the run</button>
    <h1>Review</h1>
    <p class="dim small">What happened to the changes you made while you drove. A card goes away when you have read it; the record stays.</p>
    <hh-shell-conflict-list data-conflict-list></hh-shell-conflict-list>
  `;
}

export interface StopDetailModel {
  readonly run: DriverRun;
  readonly stop: DriverStop;
  /** True while a move is on its way: the controls go grey. */
  readonly busy: boolean;
  /** True while a late photo is going up. */
  readonly photoBusy: boolean;
  readonly error: string;
  readonly notice: string;
}

function linesBlock(stop: DriverStop, lines: readonly DriverLine[] | undefined): TemplateResult {
  const split = stop.load_count > 1;
  return html`
    <section class="card" data-cargo>
      <h3>On the truck · ${weightLabel(stop.weight_kg)}${split ? ` · load ${stop.load_seq} of ${stop.load_count}` : ""}</h3>
      ${split && lines !== undefined && lines.length > 0
        ? html`<p class="dim">This is the whole order. It goes out on ${stop.load_count} loads, and which lines ride on which truck is not recorded: check the paperwork before you tell anyone something is short.</p>`
        : nothing}
      ${lines !== undefined && lines.length > 0
        ? html`<ul class="lines">
            ${lines.map(
              (l) => html`<li data-line>
                <span class="line-desc">${l.description}${l.sku !== "" ? html`<span class="line-sku">${l.sku}</span>` : nothing}</span>
                <span class="line-qty">${qtyLabel(l.qty, l.uom)}</span>
              </li>`,
            )}
          </ul>`
        : html`<p class="dim">The lines for this stop did not come through: the paperwork on the truck is the list.</p>`}
    </section>
  `;
}

/** A stop's detail: where, who to call, how to get in, what is on the truck, and the moves it offers. */
export function renderStopDetail(model: StopDetailModel): TemplateResult {
  const { stop, run } = model;
  const where = addressLine(stop);
  const actions = driverActions(stop.status);
  const mapsUrl = where !== "" ? `https://www.openstreetmap.org/search?query=${encodeURIComponent(where)}` : "";
  return html`
    <button type="button" class="back" data-action="back" data-tap>Back to my run</button>
    <h1>Stop ${stop.stop_no}</h1>
    ${renderMessages(model.error, model.notice)}
    <section class="card" data-stop-detail>
      <div class="detail-head">
        <p class="detail-who">${stop.customer_name || "Customer"}</p>
        ${statusPill(stop.status)}
      </div>
      <p class="dim">${stop.order_no}${stop.time_window ? ` · ${stop.time_window}` : ""}</p>
      ${where !== "" ? html`<p class="detail-where">${where}</p>` : html`<p class="dim">No address on file.</p>`}
      ${mapsUrl !== "" ? html`<a class="link-button" href=${hhAddress(mapsUrl)} target="_blank" rel="noopener" data-tap>Open in maps</a>` : nothing}
      ${stop.contact_name !== "" || stop.contact_phone !== ""
        ? html`<p class="detail-contact">${stop.contact_name}${stop.contact_phone !== "" ? html` <a class="phone" data-tel=${stop.contact_phone} data-tap>${stop.contact_phone}</a>` : nothing}</p>`
        : nothing}
      ${stop.instructions !== "" ? html`<p class="detail-note">${stop.instructions}</p>` : nothing}
      ${stop.status === "FAILED"
        ? html`<div class="handback" data-handback>
            <p class="handback-why">Not delivered${stop.failure_reason !== "" ? `: ${plainSentence(stop.failure_reason)}` : ""}</p>
            <p>${HANDBACK_BODY}</p>
          </div>`
        : nothing}
      ${stop.pod_received_by !== ""
        ? html`<p class="signed">Signed by ${stop.pod_received_by}${stop.pod_at !== "" ? ` at ${formatGlossaryTime(stop.pod_at, houseZone())}` : ""}${stop.photo_count > 0 ? ` · ${stop.photo_count} ${stop.photo_count === 1 ? "photo" : "photos"} on the proof` : ""}</p>`
        : nothing}
      ${stop.proof_waiting ? html`<p class="dim" data-proof-waiting role="status">${PROOF_WAITING_WORDS}</p>` : nothing}
    </section>
    ${linesBlock(stop, run.lines[stop.id])}
    ${stop.status === "DELIVERED" && !stop.proof_waiting ? renderLatePhoto(stop, model.photoBusy) : nothing}
    ${actions.length > 0
      ? html`<div class="actions">
          ${actions.map(
            (a) => html`<button
              type="button"
              class="btn ${a.destructive === true ? "btn-door-fail" : "btn-primary"} big"
              data-action=${a.action === "POD" ? "open-pod" : a.action === "FAILED" ? "open-fail" : "advance"}
              data-status=${a.action}
              ?disabled=${model.busy}
              data-tap
            >${a.label}</button>`,
          )}
        </div>`
      : nothing}
  `;
}

function photoTiles(stop: DriverStop, addLabel: string, busy: boolean): TemplateResult {
  // The board's tiles: each photo taken, then the add tile. A picture taken
  // in this visit shows itself; an earlier one shows its words on a tile.
  return html`<ul class="photo-tiles">
    ${Array.from(
      { length: stop.photo_count },
      (_, i) => html`<li class="photo-tile" data-photo-sent>
        <img class="thumb" data-thumb=${`${stop.id}:${i}`} alt="A photo of the drop" aria-label=${`Photo ${i + 1}`} hidden />
        <span class="photo-tile-word">Photo ${i + 1} sent</span>
      </li>`,
    )}
    <li class="photo-tile-add">${photoAdd(addLabel, busy)}</li>
  </ul>`;
}

function photoAdd(label: string, busy: boolean): TemplateResult {
  // The picker is a real file input behind a large tile: the camera or the
  // library, as the phone offers. It stays focusable for a keyboard.
  return html`<label class="photo-add" data-tap>
    <input type="file" accept="image/jpeg,image/png,image/webp" class="visually-hidden" data-field="photo" />
    <span class="photo-plus" aria-hidden="true">+</span>
    <span>${busy ? "Sending the photo" : label}</span>
  </label>`;
}

export interface PodModel {
  readonly stop: DriverStop;
  readonly draft: PodDraft;
  readonly photoBusy: boolean;
  readonly error: string;
}

/** Proof of delivery: a typed name, the recipient's own confirmation, and an optional photo of the drop. */
export function renderPod(model: PodModel): TemplateResult {
  const { stop, draft } = model;
  const blocked = signBlockedReason(draft);
  return html`
    <div class="sheet-scrim" data-sheet-scrim>
      <section class="sheet" role="dialog" aria-modal="true" aria-labelledby="sheet-title" data-sheet="pod">
        <div class="sheet-grab" aria-hidden="true"></div>
        <div class="sheet-head">
          <div class="sheet-titles">
            <h1 id="sheet-title">Proof of delivery</h1>
            <p class="dim" data-pod>Stop ${stop.stop_no} · ${stop.customer_name || "Customer"} · ${stop.order_no}</p>
          </div>
          <button type="button" class="sheet-close" data-action="back-to-stop" aria-label="Close" ?disabled=${draft.busy} data-tap>
            Close
          </button>
        </div>
        ${renderMessages(model.error, "")}
        <div class="sheet-body">
          <label class="field-label" for="pod-name">Full name of the person receiving</label>
          <input id="pod-name" class="text-field" type="text" autocomplete="off" data-field="name" ?disabled=${draft.busy} />
          <label class="confirm" for="pod-affirm">
            <input id="pod-affirm" class="confirm-box" type="checkbox" data-field="affirm" ?disabled=${draft.busy} />
            <span>${ATTESTATION}</span>
          </label>
          <button type="button" class="btn btn-primary big" data-action="sign" ?disabled=${blocked !== ""} data-tap>
            ${draft.busy ? "Signing" : "Sign for this delivery"}
          </button>
          ${blocked !== "" ? html`<p class="hint" data-sign-hint>${blocked}</p>` : nothing}
          <button type="button" class="btn btn-secondary big" data-action="back-to-stop" ?disabled=${draft.busy} data-tap>Not yet</button>
        </div>
        <section class="sheet-photos" data-photos>
          <h3 class="section-label">Photo of the drop · optional</h3>
          ${photoTiles(stop, stop.photo_count > 0 ? "Add another" : "Add a photo", model.photoBusy)}
          <p class="dim small">Photos back the attestation. A photo that does not send never blocks the signature.</p>
        </section>
      </section>
    </div>
  `;
}

/** The late photo control on a delivered stop: the phone found signal after the drop. */
export function renderLatePhoto(stop: DriverStop, busy: boolean): TemplateResult {
  return html`<section data-photos>${photoTiles(stop, "Add a photo to the proof", busy)}</section>`;
}

export interface FailModel {
  readonly stop: DriverStop;
  readonly preset?: string;
  readonly reason: string;
  readonly busy: boolean;
  readonly error: string;
}

function reasonRadioRow(reason: string, selectedPreset: string, busy: boolean): TemplateResult {
  const isSelected = selectedPreset === reason;
  return html`
    <label class="reason-row ${isSelected ? "reason-row-selected" : ""}" data-reason-option=${reason} data-tap>
      <input
        type="radio"
        name="fail-reason"
        class="reason-radio"
        data-field="preset-reason"
        data-reason=${reason}
        ?disabled=${busy}
      />
      <span class="reason-text">${reason}</span>
    </label>
  `;
}

/** Could not deliver: the board's preset reasons plus free text for Other, and what happens to the load. */
export function renderFail(model: FailModel): TemplateResult {
  const { stop } = model;
  const selectedPreset = model.preset ?? "";
  return html`
    <div class="sheet-scrim" data-sheet-scrim>
      <section class="sheet" role="dialog" aria-modal="true" aria-labelledby="sheet-title" data-sheet="fail">
        <div class="sheet-grab" aria-hidden="true"></div>
        <div class="sheet-head">
          <div class="sheet-titles">
            <h1 id="sheet-title">Could not deliver</h1>
            <p class="dim">Stop ${stop.stop_no} · ${stop.customer_name || "Customer"} · ${stop.order_no}</p>
          </div>
          <button type="button" class="sheet-close" data-action="back-to-stop" aria-label="Close" ?disabled=${model.busy} data-tap>
            Close
          </button>
        </div>
        ${renderMessages(model.error, "")}
        <div class="sheet-body">
          <fieldset class="reason-group" data-reason-group>
            <legend class="field-label">${FAILURE_REASON_SHEET_PROMPT}</legend>
            <div class="reason-list">
              ${FAILURE_REASON_PRESETS.map((reason) => reasonRadioRow(reason, selectedPreset, model.busy))}
            </div>
          </fieldset>
          ${selectedPreset === FAILURE_REASON_OTHER
            ? html`
                <label class="field-label" for="fail-why">${HANDBACK_PROMPT}</label>
                <textarea id="fail-why" class="text-field text-area" rows="3" data-field="reason" ?disabled=${model.busy}></textarea>
              `
            : nothing}
          <p class="handback-words" data-handback-words>${HANDBACK_BODY}</p>
          <button type="button" class="btn btn-danger big" data-action="confirm-fail" ?disabled=${model.busy || !canRecordFailure(model.reason)} data-tap>
            Could not deliver
          </button>
          <button type="button" class="btn btn-secondary big" data-action="back-to-stop" ?disabled=${model.busy} data-tap>Back</button>
        </div>
      </section>
    </div>
  `;
}

export interface CloseModel {
  readonly runs: readonly DriverRun[];
  readonly closing: boolean;
  readonly error: string;
  readonly notice: string;
}

/** The end of the run: where each stop ended, and the one control that closes the run when it may be closed. */
export function renderClose(model: CloseModel): TemplateResult {
  const cards = model.runs.map((run) => {
    const stops = stopsInOrder(run.stops);
    const close = runClose(run, stops);
    return html`
      <section class="card" data-close-run data-can-complete=${close.can ? "yes" : "no"}>
        <h2 class="run-title">${run.run_no}${run.truck_code ? ` · ${run.truck_code}` : ""}</h2>
        <p class="run-progress" data-close-progress>${close.progress.sentence}</p>
        <ul class="recap">${stops.map((s) => html`<li data-recap-stop>${closeLine(s)}</li>`)}</ul>
        ${close.can
          ? html`<button type="button" class="btn btn-primary big" data-action="complete-run" data-run-id=${run.id} ?disabled=${model.closing} data-tap>
              ${model.closing ? "Closing the run" : "Complete run"}
            </button>`
          : html`<p class="blocked" data-close-blocked>${close.blocked}</p>`}
      </section>
    `;
  });
  return html`
    <button type="button" class="back" data-action="back" data-tap>Back to my run</button>
    <h1>End of run</h1>
    ${renderMessages(model.error, model.notice)}
    ${model.runs.length === 0
      ? html`<div class="card empty" data-empty><p class="empty-head">No run assigned to you today.</p></div>`
      : cards}
  `;
}

// ---- the way in ----

export type EntryFlows =
  | { kind: "loading" }
  | { kind: "unavailable" }
  | { kind: "signin" }
  | { kind: "offline"; reason: "offline" | "unavailable" | "rate_limited" }
  | { kind: "ready"; flows: readonly DemoFlow[] };

export interface EntryView {
  readonly flows: EntryFlows;
  /** True while a mint is in flight: the card goes grey. */
  readonly busy: boolean;
  /** A refusal in words, shown above the card. */
  readonly message: string | null;
}

/** The one flow this app signs in: the Driver's. The demo routes list every live role; HH Drive offers its own. */
export const DRIVER_FLOW = "driver";

/**
 * The demo way in. It is this app's own card, not the shared entry view: the
 * shared one draws every role's card and a list of the roles still to come,
 * which means nothing to a driver, and carries every other role's one liner
 * into the driver's bytes.
 */
export function renderEntry(view: EntryView): TemplateResult {
  let body: TemplateResult;
  if (view.flows.kind === "loading") {
    body = html`<p class="dim" role="status">Loading the demo.</p>`;
  } else if (view.flows.kind === "signin") {
    body = html`<p class="dim" role="status">Sign in to open Drive. <a href="/" target="_top">Go to sign in</a></p>`;
  } else if (view.flows.kind === "offline") {
    body = offlineLine("Drive", view.flows.reason, "dim");
  } else if (view.flows.kind === "unavailable") {
    body = html`<p class="alert alert-error" role="alert">The sign in list could not load. Check your signal and try again.</p>`;
  } else {
    const flow = view.flows.flows.find((f) => f.flow === DRIVER_FLOW);
    body =
      flow === undefined
        ? html`<p class="alert alert-error" role="alert">The Driver role is not available here yet.</p>`
        : html`<button type="button" class="btn btn-primary big" data-action="choose" data-flow=${flow.flow} ?disabled=${view.busy} data-tap>${flow.label}</button>`;
  }
  return html`
    <h1>Drive</h1>
    <p class="dim">Your run, your stops and proof of delivery, on your phone.${demoNote(servedConsoleConfig())}</p>
    ${view.message !== null ? html`<p class="alert alert-error" role="alert">${view.message}</p>` : nothing}
    ${body}
  `;
}
