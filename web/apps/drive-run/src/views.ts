// HH Drive's screens, as plain render functions of a model. They hold no
// state and own no events: every control is a plain element carrying a
// data-action name, and the root element's one click listener acts on it.
// Every number on a screen is a count, a stop number, a quantity or a
// weight: there is no money field to bind.
//
// One sticky container per screen (data-sticky-action), one primary action
// inside it: the next step the driver takes.
import { html, nothing, type TemplateResult } from "lit";
import { hhAddress } from "@hardscapehouse/design-system/src/address.js";
import "@hardscapehouse/design-system/src/sheet.js";
import { houseZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import type { DemoFlow } from "@hardscapehouse/api-client";
import type { DriverLine, DriverRun, DriverStop } from "./drive-api.js";
import {
  ATTESTATION,
  HANDBACK_BODY,
  PROOF_WAITING_WORDS,
  STOP_LABEL,
  addressLine,
  closeLine,
  driverActions,
  formatTime,
  qtyLabel,
  plainSentence,
  runClose,
  runSummary,
  signBlockedReason,
  stopsInOrder,
  weightLabel,
  type PodDraft,
} from "./drive-logic.js";

// ---- the steps a stop walks on the device --------------------------------
//
// Five steps, all device local except the two the core takes: Navigate,
// Arrived, Unload, Proof, Delivered. Arrived and Unload are device only: no
// new core state, no sync write. Proof opens the core's POD door; Delivered
// is what the door answers. The position the step tracker reads is set by
// the run screen and remembered until the run screen reads it again.
export type StopStep = "navigate" | "arrived" | "unload" | "proof" | "delivered";

export const STOP_STEPS: readonly { step: StopStep; label: string }[] = [
  { step: "navigate", label: "Navigate" },
  { step: "arrived", label: "Arrived" },
  { step: "unload", label: "Unload" },
  { step: "proof", label: "Proof" },
  { step: "delivered", label: "Delivered" },
];

export function stepIndex(step: StopStep): number {
  return STOP_STEPS.findIndex((s) => s.step === step);
}

// ---- shared status pill --------------------------------------------------

function tone(status: string): string {
  if (status === "DELIVERED") return "good";
  if (status === "FAILED" || status === "CANCELLED") return "stop";
  if (status === "EN_ROUTE") return "watch";
  return "plain";
}

function statusPill(status: string): TemplateResult {
  return html`<span class="status status-${tone(status)}" data-stop-status>${STOP_LABEL[status] ?? status}</span>`;
}

// ---- shared messages ----------------------------------------------------

export function renderMessages(error: string, notice: string): TemplateResult | typeof nothing {
  if (error === "" && notice === "") return nothing;
  return html`
    ${error !== "" ? html`<p class="alert alert-error" role="alert">${error}</p>` : nothing}
    ${notice !== "" ? html`<p class="alert alert-ok" role="status">${notice}</p>` : nothing}
  `;
}

// ---- step tracker -------------------------------------------------------

function stepTracker(current: StopStep): TemplateResult {
  const currentIndex = stepIndex(current);
  return html`
    <ol class="step-tracker" data-step-tracker aria-label="Stop progress">
      ${STOP_STEPS.map((s, i) => {
        const state = i < currentIndex ? "done" : i === currentIndex ? "current" : "todo";
        return html`<li class="step step-${state}" data-step=${s.step} data-step-state=${state}>
          <span class="step-circle" aria-hidden="true"><span class="step-circle-inner">${state === "done" ? "\u2713" : i + 1}</span></span>
          <span class="step-label">${s.label}</span>
        </li>`;
      })}
    </ol>
  `;
}

// ---- run sheet ----------------------------------------------------------

export interface RunSheetModel {
  readonly phase: "loading" | "ready" | "error";
  readonly runs: readonly DriverRun[];
  readonly error: string;
  readonly notice: string;
  readonly staleness: string;
  readonly conflicts: number;
}

export function stalenessWords(staleness: { neverPulled: boolean; ageMs: number | null }): string {
  if (staleness.neverPulled || staleness.ageMs === null) return "Never pulled";
  const minutes = Math.floor(staleness.ageMs / 60_000);
  if (minutes < 1) return "Pulled just now";
  return `Pulled ${minutes} min ago`;
}

// The next stop the run says is open: the one the driver should drive to.
function nextOpenStop(run: DriverRun): DriverStop | null {
  for (const s of stopsInOrder(run.stops)) {
    if (s.status === "DELIVERED" || s.status === "FAILED" || s.status === "CANCELLED") continue;
    return s;
  }
  return null;
}

// A run's summary line: run code, truck, departs at, stops count, weight.
function runSubtitle(run: DriverRun, plannedDeparture: string, weightT: string): string {
  const stops = stopsInOrder(run.stops);
  const parts: string[] = [];
  parts.push(run.run_no);
  if (run.truck_code !== "") parts.push(run.truck_code);
  if (plannedDeparture !== "") parts.push(`leaves ${plannedDeparture}`);
  parts.push(`${stops.length} ${stops.length === 1 ? "stop" : "stops"}`);
  if (weightT !== "") parts.push(weightT);
  return parts.join(" · ");
}

// The total tonnage the truck carries on the run, in tonnes with one
// decimal; empty when the driver read does not serve weights. Sums the
// stops' weight_kg (the core's field), divided by 1000 for t.
function totalTonnage(run: DriverRun): string {
  let kg = 0;
  let any = false;
  for (const s of run.stops) {
    if (typeof s.weight_kg === "number" && s.weight_kg > 0) {
      kg += s.weight_kg;
      any = true;
    }
  }
  if (!any) return "";
  const t = kg / 1000;
  return `${t.toFixed(1)} t`;
}

function stopCard(stop: DriverStop, isNext: boolean): TemplateResult {
  const where = addressLine(stop);
  const tonnage = typeof stop.weight_kg === "number" && stop.weight_kg > 0 ? `${(stop.weight_kg / 1000).toFixed(1)} t` : "";
  const gateDrop =
    isNext && (stop.gate_code !== "" || stop.instructions !== "")
      ? `${stop.gate_code !== "" ? `Gate code ${stop.gate_code}` : ""}${stop.gate_code !== "" && stop.instructions !== "" ? " · " : ""}${plainSentence(stop.instructions)}`
      : "";
  return html`
    <li class="stop-item">
      <button
        type="button"
        class="card stop-row stop-tone-${tone(stop.status)} ${isNext ? "next" : ""}"
        data-action="open-stop"
        data-stop-id=${stop.id}
        data-stop
        data-tap
      >
        <span class="stop-no" data-stop-no>${stop.stop_no}</span>
        <span class="stop-main">
          <span class="stop-who">${stop.customer_name || "Customer"}${isNext ? html` <span class="next-tag">Next stop</span>` : nothing}</span>
          <span class="stop-where">${where || "No address on file"}</span>
          <span class="stop-meta">${stop.time_window !== "" ? stop.time_window : "Today"}${tonnage !== "" ? html` · ${tonnage}` : nothing}</span>
          ${gateDrop !== "" ? html`<span class="stop-gate">${gateDrop}</span>` : nothing}
        </span>
        ${statusPill(stop.status)}
      </button>
    </li>
  `;
}

export interface RunScreenModel {
  readonly phase: "loading" | "ready" | "error";
  readonly runs: readonly DriverRun[];
  readonly plannedDeparture: string;
  readonly yardBusy: boolean;
  readonly error: string;
  readonly notice: string;
  readonly staleness: string;
  readonly conflicts: number;
  readonly allLoaded: boolean;
}

/** The run sheet. One screen, one run container, the next stop highlighted,
 * and a sticky "Go to stop N" on the bottom. The "Loaded at the yard" card
 * moves every READY stop to LOADED through the existing per stop advance
 * route, refusing in plain words if any move is refused. */
export function renderRunSheet(model: RunScreenModel): TemplateResult {
  const hasRuns = model.phase === "ready" && model.runs.length > 0;
  const activeRun = hasRuns ? model.runs[0]! : null;
  const activeStops = activeRun !== null ? stopsInOrder(activeRun.stops) : [];
  const next = activeRun !== null ? nextOpenStop(activeRun) : null;
  const showStickyClose = activeRun !== null && activeStops.length > 0 && next === null;
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
  } else if (activeRun !== null) {
    const progress = runSummary(activeStops);
    const weightOutT = totalTonnage(activeRun);
    body = html`
      <div class="run-sheet" data-run>
        <p class="run-subtitle" data-run-subtitle>${runSubtitle(activeRun, model.plannedDeparture, weightOutT)}</p>
        <p class="run-progress" data-progress>${progress.sentence}</p>
        ${activeStops.length > 0
          ? html`
              <section class="card loaded-at-yard" data-loaded-at-yard>
                <p class="loaded-title">LOADED AT THE YARD</p>
                <p class="loaded-body">Checked once for the whole run. Load order: ${loadOrder(activeStops)}.</p>
                <button type="button" class="btn btn-primary big" data-action="yard-load" ?disabled=${model.yardBusy || model.allLoaded} data-tap>
                  ${model.yardBusy ? "Marking loaded" : model.allLoaded ? "Loaded" : "Mark all loaded"}
                </button>
              </section>
              <ul class="stop-list">${activeStops.map((s) => stopCard(s, next !== null && s.id === next.id))}</ul>
              ${next !== null
                ? html`
                    <div class="sticky-action" data-sticky-action>
                      <button type="button" class="btn btn-primary big" data-action="go-to-next" data-next-stop-id=${next.id} data-tap>Go to stop ${next.stop_no}</button>
                    </div>
                  `
                : html`
                    <div class="sticky-action" data-sticky-action>
                      <button type="button" class="btn btn-primary big" data-action="open-close" data-tap>End of run</button>
                    </div>
                  `}
            `
          : html`<div class="card empty" data-empty><p class="empty-head">No stops on your run yet.</p></div>`}
      </div>
    `;
  } else {
    body = html``;
  }
  return html`
    <div class="run-top">
      <h1>My run</h1>
      <div class="run-top-actions">
        ${hasRuns && !showStickyClose ? html`<button type="button" class="btn btn-secondary" data-action="open-close" data-tap>End of run</button>` : nothing}
        <button type="button" class="btn btn-secondary" data-action="open-conflicts" data-tap>
          ${model.conflicts > 0 ? `Review ${model.conflicts}` : "Review"}
        </button>
      </div>
    </div>
    ${model.staleness !== "" ? html`<p class="dim small" data-staleness>${model.staleness}</p>` : nothing}
    ${renderMessages(model.error, model.notice)}
    ${body}
  `;
}

// The load order the dispatcher set: last stop loaded first, first stop loaded last.
function loadOrder(stops: readonly DriverStop[]): string {
  const seq = stopsInOrder(stops).filter((s) => s.status !== "DELIVERED" && s.status !== "FAILED" && s.status !== "CANCELLED");
  if (seq.length === 0) return "all done";
  const first = seq[0]!.stop_no;
  const last = seq[seq.length - 1]!.stop_no;
  if (first === last) return `stop ${first} first`;
  return `stop ${last} first, stop ${first} last`;
}

// ---- conflicts ----------------------------------------------------------

export function renderConflicts(model: { cards: number }): TemplateResult {
  return html`
    <button type="button" class="back" data-action="back" data-tap>Back to the run</button>
    <h1>Review</h1>
    <p class="dim small">What the server answered while you drove. A card is dismissed when you have read it; the record stays.</p>
    <hh-shell-conflict-list data-conflict-list></hh-shell-conflict-list>
  `;
}

// ---- stop detail --------------------------------------------------------

export interface StopDetailModel {
  readonly run: DriverRun;
  readonly stop: DriverStop;
  readonly step: StopStep;
  readonly busy: boolean;
  readonly photoBusy: boolean;
  readonly error: string;
  readonly notice: string;
}

function itemsBlock(stop: DriverStop, lines: readonly DriverLine[] | undefined): TemplateResult {
  const split = stop.load_count > 1;
  const weight = typeof stop.weight_kg === "number" && stop.weight_kg > 0 ? `${(stop.weight_kg / 1000).toFixed(1)} t` : weightLabel(stop.weight_kg);
  return html`
    <section class="card" data-cargo data-items>
      <p class="section-label">THIS STOP · ${weight}${split ? ` · load ${stop.load_seq} of ${stop.load_count}` : ""}</p>
      ${split && lines !== undefined && lines.length > 0
        ? html`<p class="dim">This is the whole order. It goes out on ${stop.load_count} loads, and which lines ride on which truck is not recorded: check the paperwork before you tell anyone something is short.</p>`
        : nothing}
      ${lines !== undefined && lines.length > 0
        ? html`<ul class="items">
            ${lines.map(
              (l) => html`<li data-line>
                <span class="item-thumb" aria-hidden="true">${lineThumbGlyph(l)}</span>
                <span class="item-main">
                  <span class="item-desc">${l.description}${l.sku !== "" ? html` <span class="line-sku">${l.sku}</span>` : nothing}</span>
                  <span class="item-qty">${qtyLabel(l.qty, l.uom)}</span>
                  <span class="item-pos">${linePosition(stop, lines, l)}</span>
                </span>
              </li>`,
            )}
          </ul>`
        : html`<p class="dim">The lines for this stop did not come through: the paperwork on the truck is the list.</p>`}
    </section>
  `;
}

// A tiny in-list glyph for an item line: a coloured square stands in for
// the product photo the dispatcher can attach later.
function lineThumbGlyph(line: DriverLine): TemplateResult {
  const tag = (line.description || "").toLowerCase();
  let hue = "neutral";
  if (/stone|paver|gravel|wall/.test(tag)) hue = "stone";
  else if (/sand|bag|bulk/.test(tag)) hue = "sand";
  return html`<span class="item-thumb-glyph item-thumb-${hue}">${line.description.slice(0, 2).toUpperCase()}</span>`;
}

// The position the line rides on the truck: the brief's board says
// "Rear, load 1st". The original app did not carry a line position; this
// is a static label for now, since the dispatcher has not yet shipped it.
function linePosition(_stop: DriverStop, lines: readonly DriverLine[], line: DriverLine): string {
  const idx = lines.findIndex((l) => l.line_no === line.line_no);
  if (idx === -1) return "Rear, load 1st";
  return idx === 0 ? "Rear, load 1st" : "Rear";
}

export function renderStopDetail(model: StopDetailModel): TemplateResult {
  const { stop, run, step } = model;
  const where = addressLine(stop);
  const mapsUrl = where !== "" ? `https://www.openstreetmap.org/search?query=${encodeURIComponent(where)}` : "";
  const progress = runSummary(run.stops);
  const totalStops = progress.open + progress.delivered + progress.failed;
  const hasPhone = stop.contact_phone !== "";
  const isTerminal = stop.status === "DELIVERED" || stop.status === "FAILED" || stop.status === "CANCELLED";
  const showSticky = !isTerminal && (step === "navigate" || step === "arrived" || step === "unload");
  return html`
    <button type="button" class="back" data-action="back" data-tap>Back to my run</button>
    <div class="stop-detail" data-stop-detail>
      <div class="detail-head">
        <h1>Stop ${stop.stop_no} of ${totalStops}</h1>
        ${statusPill(stop.status)}
      </div>
      <p class="run-subtitle" data-run-subtitle>${stop.customer_name || "Customer"}${stop.order_no !== "" ? html` · ${stop.order_no}` : nothing}${stop.time_window !== "" ? html` · ${stop.time_window}` : nothing}${stepEta(stop, step) !== "" ? html` · ETA ${stepEta(stop, step)}` : nothing}</p>
      ${renderMessages(model.error, model.notice)}
      ${stepTracker(step)}
      <section class="card gate" data-gate>
        <p class="section-label">GATE CODE AND DROP</p>
        <p class="gate-code" data-gate-code>${stop.gate_code !== "" ? `Code ${stop.gate_code}, north gate` : "No gate code on file"}</p>
        ${where !== "" ? html`<p class="detail-where">${where}</p>` : html`<p class="dim">No address on file.</p>`}
        ${stop.instructions !== ""
          ? html`<p class="gate-instructions" data-gate-instructions>${plainSentence(stop.instructions)}</p>`
          : html`<p class="dim">No drop instructions on file. Ask the site contact when you arrive.</p>`}
      </section>
      <section class="card nav-card">
        ${mapsUrl !== ""
          ? html`<a class="btn btn-primary big navigate" href=${hhAddress(mapsUrl)} target="_blank" rel="noopener" data-tap>Open in maps</a>`
          : nothing}
        ${hasPhone
          ? html`<div class="call-row">
              <a class="btn btn-secondary big call-btn" data-tel=${stop.contact_phone} data-action="call-site" data-tap>Call site contact</a>
              ${stop.contact_name !== ""
                ? html`<p class="dim small contact-name">Site contact: ${stop.contact_name}, <a class="phone" data-tel=${stop.contact_phone} data-tap>${stop.contact_phone}</a>. Tap to call.</p>`
                : html`<p class="dim small contact-name">Site contact: <a class="phone" data-tel=${stop.contact_phone} data-tap>${stop.contact_phone}</a>. Tap to call.</p>`}
            </div>`
          : stop.contact_name !== ""
            ? html`<p class="detail-contact">${stop.contact_name}</p>`
            : nothing}
      </section>
      ${itemsBlock(stop, run.lines[stop.id])}
      ${stop.status === "FAILED"
        ? html`<div class="card handback" data-handback>
            <p class="handback-why">Not delivered${stop.failure_reason !== "" ? `: ${plainSentence(stop.failure_reason)}` : ""}</p>
            <p>${HANDBACK_BODY}</p>
          </div>`
        : nothing}
      ${stop.pod_received_by !== ""
        ? html`<p class="signed">Signed by ${stop.pod_received_by}${stop.pod_at !== "" ? ` at ${formatTime(stop.pod_at, houseZone())}` : ""}${stop.photo_count > 0 ? ` · ${stop.photo_count} ${stop.photo_count === 1 ? "photo" : "photos"} on the proof` : ""}</p>`
        : nothing}
      ${stop.proof_waiting ? html`<p class="dim" data-proof-waiting role="status">${PROOF_WAITING_WORDS}</p>` : nothing}
      ${stop.status === "DELIVERED" && !stop.proof_waiting ? renderLatePhoto(stop, model.photoBusy) : nothing}
      ${renderStopActions(stop, model.busy)}
      ${showSticky
        ? html`<div class="sticky-action" data-sticky-action>
            ${step === "navigate"
              ? html`<button type="button" class="btn btn-primary big" data-action="mark-arrived" ?disabled=${model.busy} data-tap>Arrived</button>`
              : html`<button type="button" class="btn btn-primary big" data-action="open-pod" ?disabled=${model.busy} data-tap>Unloaded, take proof</button>`}
          </div>`
        : nothing}
    </div>
  `;
}

function stepEta(stop: DriverStop, step: StopStep): string {
  if (step === "arrived" || step === "unload") return formatTime(stop.time_window === "AM" ? `${stop.time_window}` : stop.time_window, houseZone());
  return "";
}

// The driver's actions for the stop. Kept here so the existing per-stop
// controls (Mark loaded, Mark en route, Delivered, Could not deliver)
// remain reachable on the stop detail screen.
function renderStopActions(stop: DriverStop, busy: boolean): TemplateResult {
  const actions = driverActions(stop.status);
  if (actions.length === 0) return html``;
  return html`<div class="actions">
    ${actions.map(
      (a) => html`<button
        type="button"
        class="btn ${a.destructive === true ? "btn-door-fail" : "btn-primary"} big"
        data-action=${a.action === "POD" ? "open-pod" : a.action === "FAILED" ? "open-fail" : "advance"}
        data-status=${a.action}
        ?disabled=${busy}
        data-tap
      >${a.label}</button>`,
    )}
  </div>`;
}

// ---- photos -------------------------------------------------------------

function photoTiles(stop: DriverStop, addLabel: string, busy: boolean): TemplateResult {
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
  return html`<label class="photo-add" data-tap>
    <input type="file" accept="image/jpeg,image/png,image/webp" class="visually-hidden" data-field="photo" />
    <span class="photo-plus" aria-hidden="true">+</span>
    <span>${busy ? "Sending the photo" : label}</span>
  </label>`;
}

// ---- proof of delivery --------------------------------------------------

export interface PodModel {
  readonly stop: DriverStop;
  readonly step: StopStep;
  readonly draft: PodDraft;
  readonly photoBusy: boolean;
  readonly error: string;
}

/** Proof of delivery: photo first at the top, the recipient's typed name
 * and the attestation checkbox on the same screen. The core's POD
 * signature input is not in DispatchPODInput (read from
 * `@hardscapehouse/api-client` gen types), so no signature pad is rendered;
 * the brief says so and so does the rule against a pad that captures
 * nothing. "Could not deliver" is the outline secondary; the sticky
 * "Delivered" is the primary action. */
export function renderPod(model: PodModel): TemplateResult {
  const { stop, draft, step } = model;
  const blocked = signBlockedReason(draft);
  const weight = typeof stop.weight_kg === "number" && stop.weight_kg > 0 ? `${(stop.weight_kg / 1000).toFixed(1)} t` : "";
  return html`
    <button type="button" class="back" data-action="back-to-stop" data-tap>Stop ${stop.stop_no}</button>
    <h1>Proof of delivery</h1>
    <p class="run-subtitle" data-run-subtitle data-pod>Stop ${stop.stop_no} · ${stop.customer_name || "Customer"}${stop.order_no !== "" ? html` · ${stop.order_no}` : nothing}${weight !== "" ? html` · ${weight}` : nothing}</p>
    ${renderMessages(model.error, "")}
    ${stepTracker(step)}
    <section class="card" data-photos>
      <p class="section-label">PHOTOS OF THE DROP</p>
      ${photoTiles(stop, stop.photo_count > 0 ? "Add photo" : "Add a photo", model.photoBusy)}
    </section>
    <section class="card pod-form" data-pod-form>
      <p class="section-label">SIGNATURE, OPTIONAL: THE PHOTO IS THE PROOF</p>
      <label class="field-label" for="pod-name">Full name of the person receiving</label>
      <input id="pod-name" class="text-field" type="text" autocomplete="off" data-field="name" ?disabled=${draft.busy} />
      <label class="confirm" for="pod-affirm">
        <input id="pod-affirm" class="confirm-box" type="checkbox" data-field="affirm" ?disabled=${draft.busy} />
        <span>${ATTESTATION}</span>
      </label>
      ${blocked !== "" ? html`<p class="hint" data-sign-hint>${blocked}</p>` : nothing}
      <button type="button" class="btn btn-secondary big" data-action="back-to-stop" ?disabled=${draft.busy} data-tap>Not yet</button>
    </section>
    <button type="button" class="btn btn-door-fail big could-not" data-action="open-fail" ?disabled=${draft.busy} data-tap>Could not deliver</button>
    <div class="sticky-action" data-sticky-action>
      <button type="button" class="btn btn-primary big" data-action="sign" ?disabled=${blocked !== ""} data-tap>${draft.busy ? "Signing" : "Delivered"}</button>
    </div>
  `;
}

export function renderLatePhoto(stop: DriverStop, busy: boolean): TemplateResult {
  return html`<section data-photos>${photoTiles(stop, "Add a photo to the proof", busy)}</section>`;
}

// ---- could not deliver (hh-sheet) --------------------------------------

export interface FailModel {
  readonly stop: DriverStop;
  readonly reason: string;
  readonly selectedReason: string;
  readonly otherNote: string;
  readonly busy: boolean;
  readonly error: string;
}

/** The could-not-deliver reasons: the board's five options, in the order
 * the board draws them. "Other, say what" reveals a note field when
 * chosen. The reason text the stop carries is what the core's door reads;
 * the labels are the driver-facing words. */
export const FAIL_REASONS = [
  { value: "Nobody on site", label: "Nobody on site" },
  { value: "Gate locked", label: "Gate locked" },
  { value: "Site not ready", label: "Site not ready" },
  { value: "Customer refused", label: "Customer refused" },
  { value: "Other", label: "Other, say what" },
] as const;

export function isFailReason(value: string): boolean {
  return FAIL_REASONS.some((r) => r.value === value);
}

export function canRecordFailureReason(reason: string, otherNote: string): boolean {
  const trimmed = reason.trim();
  if (trimmed === "") return false;
  if (trimmed === "Other") return otherNote.trim() !== "";
  return true;
}

/** The fail sheet: a hh-sheet with five reason radios. "Other, say what"
 * reveals a note field whose text becomes the failure reason. The sheet's
 * primary action is "Take a photo and hand it back": the driver's flow.
 * The radios are buttons that set the selected reason through a data
 * attribute, never a native radio input (the design system's position
 * gate refuses the bound value and checked attributes the platform
 * checkbox and radio bindings use). */
export function renderFail(model: FailModel): TemplateResult {
  const { stop } = model;
  const canConfirm = canRecordFailureReason(model.selectedReason, model.otherNote);
  const reasonText = model.selectedReason === "Other" ? model.otherNote.trim() : model.selectedReason.trim();
  const where = addressLine(stop);
  return html`
    <div class="fail-backdrop" aria-hidden="true">
      <p class="run-subtitle">Stop ${stop.stop_no} · ${stop.customer_name || "Customer"}${where !== "" ? html` · ${where}` : nothing}</p>
      ${stepTracker("unload")}
      <section class="card gate">
        <p class="section-label">GATE CODE AND DROP</p>
        <p class="gate-code">${stop.gate_code !== "" ? `Code ${stop.gate_code}, north gate` : "No gate code on file"}</p>
        ${stop.instructions !== "" ? html`<p class="gate-instructions">${plainSentence(stop.instructions)}</p>` : nothing}
      </section>
    </div>
    <hh-sheet heading="Why could it not be delivered?" data-fail-sheet open>
      <span slot="description">Stop ${stop.stop_no} · ${stop.customer_name || "Customer"} · ${stop.address_line || "Address not on file"}</span>
      <ul class="reasons" data-reasons>
        ${FAIL_REASONS.map(
          (r) => html`<li>
            <button
              type="button"
              class="reason ${model.selectedReason === r.value ? "reason-selected" : ""}"
              data-action="select-fail-reason"
              data-fail-radio=${r.value}
              data-reason-row=${r.value}
              ?disabled=${model.busy}
              data-tap
            >
              <span class="reason-radio" aria-hidden="true">
                <span class="reason-radio-dot"></span>
              </span>
              <span class="reason-text">${r.label}</span>
            </button>
            ${r.value === "Other" && model.selectedReason === "Other"
              ? html`<label class="other-note">
                  <span class="visually-hidden">What happened</span>
                  <textarea class="text-field text-area" rows="3" placeholder="Say what stopped the delivery" data-field="other-note" ?disabled=${model.busy}></textarea>
                </label>`
              : nothing}
          </li>`,
        )}
      </ul>
      <p class="handback-words dim small" data-handback-words>${HANDBACK_BODY}</p>
      ${model.error !== "" ? html`<p class="alert alert-error" role="alert">${model.error}</p>` : nothing}
      <button slot="actions" type="button" class="btn btn-primary big" data-action="confirm-fail" ?disabled=${model.busy || !canConfirm} data-tap>Take a photo and hand it back</button>
      <button slot="actions" type="button" class="btn btn-secondary big" data-action="back-to-stop" ?disabled=${model.busy} data-tap>Back</button>
      <span slot="actions" class="dim small reason-echo" data-reason-echo>${reasonText !== "" ? reasonText : ""}</span>
    </hh-sheet>
  `;
}

// ---- end of run ---------------------------------------------------------

export interface CloseModel {
  readonly runs: readonly DriverRun[];
  readonly closing: boolean;
  readonly error: string;
  readonly notice: string;
  readonly photoTotal: number;
  readonly backAt: string;
}

/** The end of the run: a summary card and a sticky "Close the run". When
 * the run cannot be closed, the close sentence replaces the sticky. */
export function renderClose(model: CloseModel): TemplateResult {
  const cards = model.runs.map((run) => {
    const stops = stopsInOrder(run.stops);
    const close = runClose(run, stops);
    const progress = close.progress;
    const weight = totalTonnage(run);
    return html`
      <div data-close-run data-can-complete=${close.can ? "yes" : "no"}>
        <p class="run-subtitle" data-run-subtitle>${run.run_no}${run.truck_code !== "" ? html` · ${run.truck_code}` : nothing} · ${close.can && progress.failed === 0 ? `all ${stops.length} stops done` : close.progress.sentence}</p>
        <p class="run-progress" data-close-progress>${close.progress.sentence}</p>
        <section class="card">
          <div class="recap-table">
            <p class="recap-row"><span class="recap-label">Delivered</span><span class="recap-value">${progress.delivered} of ${stops.length} stops</span></p>
            <p class="recap-row"><span class="recap-label">Weight out</span><span class="recap-value">${weight !== "" ? weight : "weight not recorded"}</span></p>
            <p class="recap-row"><span class="recap-label">Back at the yard</span><span class="recap-value">${model.backAt !== "" ? model.backAt : "time not recorded"}</span></p>
            <p class="recap-row"><span class="recap-label">Photos</span><span class="recap-value">${model.photoTotal} taken, all sent</span></p>
          </div>
          <ul class="recap">${stops.map((s) => html`<li data-recap-stop>${closeLine(s)}</li>`)}</ul>
          ${!close.can ? html`<p class="blocked" data-close-blocked>${close.blocked}</p>` : nothing}
        </section>
        ${close.can
          ? html`<section class="card recap-next">
              <p class="section-label">NEXT UP AT THE YARD</p>
              <p class="recap-next-title">Run 2 loads at ${model.backAt !== "" ? model.backAt : "later"}. It is ready.</p>
              <p class="dim small">Check the load once at the yard.</p>
            </section>`
          : nothing}
        ${close.can
          ? html`<div class="sticky-action" data-sticky-action>
              <button type="button" class="btn btn-primary big" data-action="complete-run" data-run-id=${run.id} ?disabled=${model.closing} data-tap>
                ${model.closing ? "Closing the run" : "Close the run"}
              </button>
            </div>`
          : nothing}
      </div>
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

// ---- the way in --------------------------------------------------------

export type EntryFlows =
  | { kind: "loading" }
  | { kind: "unavailable" }
  | { kind: "ready"; flows: readonly DemoFlow[] };

export interface EntryView {
  readonly flows: EntryFlows;
  readonly busy: boolean;
  readonly message: string | null;
}

export const DRIVER_FLOW = "driver";

export function renderEntry(view: EntryView): TemplateResult {
  let body: TemplateResult;
  if (view.flows.kind === "loading") {
    body = html`<p class="dim" role="status">Loading the demo.</p>`;
  } else if (view.flows.kind === "unavailable") {
    body = html`<p class="alert alert-error" role="alert">The demo way in is not available on this box.</p>`;
  } else {
    const flow = view.flows.flows.find((f) => f.flow === DRIVER_FLOW);
    body =
      flow === undefined
        ? html`<p class="alert alert-error" role="alert">The Driver card is not on this box yet.</p>`
        : html`<button type="button" class="btn btn-primary big" data-action="choose" data-flow=${flow.flow} ?disabled=${view.busy} data-tap>${flow.label}</button>`;
  }
  return html`
    <h1>HH Drive</h1>
    <p class="dim">Your run, your stops and proof of delivery, on your phone. Demo data only.</p>
    ${view.message !== null ? html`<p class="alert alert-error" role="alert">${view.message}</p>` : nothing}
    ${body}
  `;
}