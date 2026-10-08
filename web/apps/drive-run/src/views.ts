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
import { hhAddress } from "@hardscapehouse/design-system/src/address.js";
import { HOUSE_ZONE } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import type { DriverLine, DriverRun, DriverStop } from "./drive-api.js";
import {
  ATTESTATION,
  STOP_LABEL,
  addressLine,
  driverActions,
  formatTime,
  qtyLabel,
  runSummary,
  signBlockedReason,
  stopsInOrder,
  weightLabel,
  type PodDraft,
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
}

function stopRow(stop: DriverStop): TemplateResult {
  const where = addressLine(stop);
  return html`
    <li>
      <button type="button" class="stop-row" data-action="open-stop" data-stop-id=${stop.id} data-stop data-tap>
        <span class="stop-no" data-stop-no>${stop.stop_no}</span>
        <span class="stop-main">
          <span class="stop-who">${stop.customer_name || "Customer"}</span>
          <span class="stop-where">${where || "No address on file"}${stop.time_window ? ` · ${stop.time_window}` : ""}</span>
        </span>
        ${statusPill(stop.status)}
      </button>
    </li>
  `;
}

function runCard(run: DriverRun): TemplateResult {
  const stops = stopsInOrder(run.stops);
  const progress = runSummary(stops);
  return html`
    <section class="card run" data-run>
      <h2 class="run-title">${run.run_no}${run.truck_code ? ` · ${run.truck_code}` : ""}</h2>
      <p class="run-count">${stops.length} ${stops.length === 1 ? "stop" : "stops"}</p>
      <p class="run-progress" data-progress>${progress.sentence}</p>
      <ul class="stop-list">${stops.map(stopRow)}</ul>
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
  } else {
    body = html`${model.runs.map(runCard)}`;
  }
  return html`
    <h1>My run</h1>
    ${renderMessages(model.error, model.notice)}
    ${body}
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
      ${stop.pod_received_by !== ""
        ? html`<p class="signed">Signed by ${stop.pod_received_by}${stop.pod_at !== "" ? ` at ${formatTime(stop.pod_at, HOUSE_ZONE)}` : ""}${stop.photo_count > 0 ? ` · ${stop.photo_count} ${stop.photo_count === 1 ? "photo" : "photos"} on the proof` : ""}</p>`
        : nothing}
    </section>
    ${linesBlock(stop, run.lines[stop.id])}
    ${stop.status === "DELIVERED" ? renderLatePhoto(model.photoBusy) : nothing}
    ${actions.length > 0
      ? html`<div class="actions">
          ${actions.map(
            (a) => html`<button
              type="button"
              class="btn ${a.destructive === true ? "btn-danger" : "btn-primary"} big"
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

function photoAdd(label: string, busy: boolean): TemplateResult {
  // The picker is a real file input behind a large label: the camera or the
  // library, as the phone offers. It stays focusable for a keyboard.
  return html`<label class="photo-add" data-tap>
    <input type="file" accept="image/jpeg,image/png,image/webp" class="visually-hidden" data-field="photo" ?disabled=${busy} />
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
    <button type="button" class="back" data-action="back-to-stop" data-tap>Stop ${stop.stop_no}</button>
    <h1>Proof of delivery</h1>
    <p class="dim" data-pod>Stop ${stop.stop_no} · ${stop.customer_name || "Customer"} · ${stop.order_no}</p>
    ${renderMessages(model.error, "")}
    <section class="card">
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
    </section>
    <section data-photos>
      <h3 class="section-label">Photo of the drop · optional</h3>
      <ul class="photo-list">
        ${Array.from({ length: stop.photo_count }, (_, i) => html`<li class="photo-sent" data-photo-sent>Photo ${i + 1} sent</li>`)}
      </ul>
      ${photoAdd(stop.photo_count > 0 ? "Add another photo" : "Add a photo", model.photoBusy)}
      <p class="dim small">Photos back the attestation. A photo that does not send never blocks the signature.</p>
    </section>
  `;
}

/** The late photo control on a delivered stop: the phone found signal after the drop. */
export function renderLatePhoto(busy: boolean): TemplateResult {
  return html`<section data-photos>${photoAdd("Add a photo to the proof", busy)}</section>`;
}
