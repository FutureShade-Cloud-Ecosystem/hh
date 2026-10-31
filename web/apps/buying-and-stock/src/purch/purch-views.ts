// The pieces every purchasing area draws the same way: the head, the loading and failed states,
// the status pill (a word before a colour), the notice and the refusal note, the empty state, and
// the confirm box that stands between a consequential press and the request. Plain render
// functions of plain values; no state, no events (a control carries a data-action name and the
// root's click listener acts on it).
import { html, nothing, type TemplateResult } from "lit";
import { stampDay, type Tone } from "../po/po-logic.js";

export function areaHead(title: string, lead: string): TemplateResult {
  return html`
    <div class="head">
      <div>
        <h1>${title}</h1>
        <p class="dim">${lead}</p>
      </div>
    </div>
  `;
}

export function loadingNote(words: string): TemplateResult {
  return html`<p class="dim" role="status" data-loading>${words}</p>`;
}

export function loadFailed(error: string): TemplateResult {
  return html`
    <p class="alert alert-error" role="alert" data-error>${error}</p>
    <button type="button" class="btn btn-secondary" data-action="reload">Try again</button>
  `;
}

/** A refusal or a failure, in words, where the press that caused it is. */
export function errorNote(error: string): TemplateResult {
  return error === "" ? html`${nothing}` : html`<p class="alert alert-error" role="alert" data-error>${error}</p>`;
}

/** A done notice reads as a bold first line and a plain second; the desk joins them with a newline. */
export function noticeBox(words: string): TemplateResult {
  if (words === "") return html`${nothing}`;
  const [lead = "", ...rest] = words.split("\n");
  return html`<div class="alert alert-ok" role="status" data-notice><strong>${lead}</strong>${rest.length > 0 ? html`<p>${rest.join(" ")}</p>` : nothing}</div>`;
}

export function pill(tone: Tone, word: string): TemplateResult {
  return html`<span class="status status-${tone}" data-status>${word}</span>`;
}

export function emptyState(lead: string, rest: string): TemplateResult {
  return html`<div class="empty" data-empty><strong>${lead}</strong><span class="dim">${rest}</span></div>`;
}

export function dateOnly(stamp: string | null | undefined): string {
  return stampDay(stamp);
}

/**
 * The step before a consequential press: what it will do, in the item's own words, with the
 * button that does it beside the one that backs out. The confirm button carries the action it
 * confirms; the back out button carries "confirm-cancel".
 */
export function confirmBox(words: string, confirmLabel: string, confirmAction: string, busy: boolean): TemplateResult {
  return html`
    <div class="confirm" role="group" aria-label="Confirm" data-confirm>
      <p data-confirm-words><strong>Check before you go on.</strong> ${words}</p>
      <div class="actions">
        <button type="button" class="btn btn-secondary" data-action="confirm-cancel" ?disabled=${busy}>Go back</button>
        <button type="button" class="btn btn-primary" data-action=${confirmAction} data-confirmed ?disabled=${busy}>${confirmLabel}</button>
      </div>
    </div>
  `;
}

/** A button that is a checkbox, so the position gate has nothing to bind. */
export function checkBox(label: string, action: string, on: boolean): TemplateResult {
  return html`
    <div class="check">
      <button type="button" class="box" role="checkbox" aria-checked="${on ? "true" : "false"}" aria-label=${label} data-action=${action}></button>
      <span>${label}</span>
    </div>
  `;
}

/** A segmented control: the lit way is pressed, the words beside; pressing the lit one again is the caller's call. */
export function segs(label: string, action: string, choices: readonly { id: string; word: string }[], current: string, hint = ""): TemplateResult {
  return html`
    <div class="field" role="group" aria-label=${label}>
      <span class="label">${label}</span>
      <div class="segs">
        ${choices.map(
          (c) => html`<button type="button" class="seg" data-action=${action} data-choice=${c.id} aria-pressed="${current === c.id ? "true" : "false"}">${c.word}</button>`,
        )}
      </div>
      ${hint === "" ? nothing : html`<span class="dim" data-extra-supply-hint>${hint}</span>`}
    </div>
  `;
}
