// The Drivers view: the managers' "where are the drivers" read. A board of
// driver rows grouped by the day's four states (on a stop, between stops, not
// started, done), each row the driver's role label, its truck, its run, its
// place in the day (stop n of m, done and left), the status word, and how
// long ago the day last moved. Read only: no action here moves a stop.
//
// Visual reference: the original's dispatch day board (app/src/dispatch-
// board.ts, one row per run, the run's status word as a pill, the crew line
// "truck · driver") and this app's own landing style (the view header, the
// sheet, the eyebrow labels, the token inks). No position and no GPS: the row
// shows the state of the day, the only "where" the contract holds.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings. Controls ride data-action names and
// the host's listeners act on them.
import { html, nothing, type TemplateResult } from "lit";
import type { DriverRow } from "./drivers-logic.js";
import {
  DRIVER_GROUP_ORDER,
  DRIVERS_SUB_WORDS,
  EMPTY_BOARD_WORDS,
  driverGroupWord,
  runStatusWord,
  sinceWords,
  stopPositionWords,
  stopStatusWord,
  type DriverGroupKey,
} from "./drivers-logic.js";
import { truckWords, type DriverTruckView } from "./drivers-api.js";
import { viewHeader } from "./views.js";

export interface DriversModel {
  readonly phase: "loading" | "ready" | "error" | "refused";
  readonly branchName: string;
  readonly groups: readonly { readonly key: DriverGroupKey; readonly rows: readonly DriverRow[] }[];
  readonly trucks: readonly DriverTruckView[];
  readonly runCount: number;
  readonly idleWords: string;
  readonly error: string;
  readonly refused: string;
}

export function renderDrivers(model: DriversModel): TemplateResult {
  return html`
    ${viewHeader("drivers", model.branchName, DRIVERS_SUB_WORDS)}
    ${model.refused !== "" ? html`<p class="refusal" data-test="drivers-refusal">${model.refused}</p>` : nothing}
    ${model.error !== "" ? html`<p class="error-line" data-test="drivers-error">${model.error}</p>` : nothing}
    ${model.phase === "loading"
      ? html`<p class="notice-line">Loading the drivers' day…</p>`
      : model.phase === "ready"
        ? renderBoard(model)
        : html`<div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-view">Try again</button></div>`}
  `;
}

function renderBoard(model: DriversModel): TemplateResult {
  if (model.runCount === 0) {
    return html`
      <section class="sheet">
        <div class="list-card"><p class="notice-line" data-test="drivers-empty">${EMPTY_BOARD_WORDS}</p></div>
      </section>
    `;
  }
  const now = Date.now();
  return html`
    <section class="sheet">
      <div class="board-bar">
        <p class="count-line" data-test="drivers-count">${model.runCount} run${model.runCount === 1 ? "" : "s"} on the board today</p>
        <button type="button" class="btn btn-secondary" data-action="reload-view">Refresh</button>
      </div>
      ${DRIVER_GROUP_ORDER.map((key) => {
        const group = model.groups.find((g) => g.key === key);
        return group === undefined
          ? nothing
          : html`
              <div class="driver-group" data-group=${key}>
                <p class="eyebrow">${driverGroupWord(key)} · ${group.rows.length}</p>
                ${group.rows.map((row) => renderDriverRow(row, model.trucks, now))}
              </div>
            `;
      })}
      ${model.idleWords !== "" ? html`<p class="count-line" data-test="drivers-idle">${model.idleWords}</p>` : nothing}
    </section>
  `;
}

function renderDriverRow(row: DriverRow, trucks: readonly DriverTruckView[], now: number): TemplateResult {
  const truck = truckWords(row.run, trucks);
  const stopWord = row.current_stop !== null ? stopStatusWord(row.current_stop.status) : "";
  const statusWord = stopWord !== "" ? stopWord : runStatusWord(row.run.status);
  const since = sinceWords(row.last_changed_at ?? "", now);
  const trouble = row.did_not_go > 0 || row.run.status === "CANCELLED";
  return html`
    <article class="driver-row${trouble ? " trouble" : ""}" data-run=${row.run.id}>
      <span class="sev" aria-hidden="true"></span>
      ${trouble ? html`<span class="visually-hidden">Urgent</span>` : nothing}
      <div class="who">
        <p class="driver">${row.run.driver_name !== "" ? row.run.driver_name : "No driver on the run"}</p>
        <p class="detail">${row.run.run_no}${truck !== "" ? html` · ${truck}` : nothing}</p>
      </div>
      <div class="place">
        <p class="state-word">${statusWord !== "" ? statusWord : "On the board"}</p>
        <p class="detail" data-test="driver-place">
          ${stopPositionWords(row) !== "" ? html`<span>${stopPositionWords(row)}</span>` : nothing}
          ${row.done_stops > 0 ? html`<span>${row.done_stops} done</span>` : nothing}
          ${row.left_stops > 0 ? html`<span>${row.left_stops} left</span>` : nothing}
          ${row.did_not_go > 0 ? html`<span class="did-not-go">${row.did_not_go} did not go</span>` : nothing}
          ${stopPositionWords(row) === "" && row.done_stops === 0 && row.left_stops === 0 && row.did_not_go === 0
            ? html`<span>no stops on the run</span>`
            : nothing}
        </p>
      </div>
      <p class="since" data-test="driver-since">${since !== "" ? html`last change ${since}` : nothing}</p>
    </article>
  `;
}
