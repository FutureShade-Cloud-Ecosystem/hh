// The Hotkeys surface's screens: the two platform rosters side by side, the
// capture-and-save configurator for the caller who may edit, and the calm
// read-only line for the one who may not. Pure template functions over plain
// model objects; app-root owns every call, every state change and the
// keyboard capture.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings; unquoted whole values in the data-
// and aria- namespaces; ?disabled is the one boolean. Controls ride
// data-action names and the host's listeners act on them.
import { html, nothing, type TemplateResult } from "lit";
import {
  PLATFORM_SURFACE_LABEL,
  PLATFORM_SURFACE_SCOPE,
  isBrowserKey,
  platformActionLabel,
  platformActions,
  platformKeycap,
  type PlatformHotkeyView,
} from "./hotkey-logic.js";

export interface HotkeysModel {
  readonly phase: "loading" | "ready" | "error";
  /** The server's answer, whole. null until the first read lands. */
  readonly view: PlatformHotkeyView | null;
  /** Per surface, the roster being edited: action to chord. */
  readonly drafts: Readonly<Record<string, Record<string, string>>>;
  /** The surface and action waiting for a keystroke, or null. */
  readonly capturing: { readonly surface: string; readonly action: string } | null;
  /** The press-time refusal, per surface. */
  readonly refusals: Readonly<Record<string, string>>;
  /**
   * The cross substrate note, per surface. A separate field from the refusal,
   * never a severity flag on it: the two have opposite outcomes. A refusal
   * means the draft did NOT change; this means the draft DID change and the
   * person needs to know why that is fine. One slot would eventually become
   * one role, and role="alert" on "this worked" is how a screen teaches
   * people to ignore alerts.
   */
  readonly notes: Readonly<Record<string, string>>;
  /** A read that did not land at all: the surface is replaced by the error. */
  readonly loadError: string;
  /** A write that did not land, rendered beside the work, which is still on screen. */
  readonly saveError: string;
  readonly notice: string;
  readonly busy: boolean;
}

export function renderHotkeys(model: HotkeysModel): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Hotkeys</h1>
      <p class="page-sub">The keyboard rosters every dealer inherits, until they set their own.</p>
    </header>
    ${model.phase === "loading" ? html`<p class="notice-line" data-test="hotkeys-loading">Loading the shortcut keys…</p>` : nothing}
    ${model.phase === "error"
      ? html`
          <p class="error-line" data-test="hotkeys-error">${model.loadError}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-hotkeys">Try again</button></div>
        `
      : nothing}
    ${model.phase === "ready" ? renderRosters(model) : nothing}
  `;
}

function renderRosters(model: HotkeysModel): TemplateResult {
  const canEdit = model.view?.canEdit === true;
  return html`
    ${canEdit
      ? nothing
      : html`<p class="notice-line" data-test="hotkeys-readonly">
          These are set by the platform administrator. A dealer's own keys are set in Counter settings.
        </p>`}
    <div class="rosters">
      ${(model.view?.rosters ?? []).map((roster) => renderRoster(model, roster.surface, canEdit))}
    </div>
    ${model.saveError === "" ? nothing : html`<p class="error-line" data-test="hotkeys-save-error">${model.saveError}</p>`}
    ${model.notice === "" ? nothing : html`<p class="notice-line" data-test="hotkeys-notice">${model.notice}</p>`}
  `;
}

function renderRoster(model: HotkeysModel, surface: string, canEdit: boolean): TemplateResult {
  const draft = model.drafts[surface] ?? {};
  const actions = platformActions(surface);
  const bound = actions.filter((a) => draft[a.id] !== undefined && draft[a.id] !== "");
  const refusal = model.refusals[surface] ?? "";
  const note = model.notes[surface] ?? "";
  return html`
    <section class="card" data-test=${`hotkeys-roster-${surface}`}>
      <div class="card-head">
        <h2 class="card-title">${PLATFORM_SURFACE_LABEL[surface] ?? surface}</h2>
      </div>
      <p class="card-sub">${PLATFORM_SURFACE_SCOPE[surface] ?? ""}</p>
      ${bound.length === 0 && !canEdit
        ? html`<p class="notice-line">No keys are set here yet. Every terminal falls back to the keys this build ships with until a platform administrator sets some.</p>`
        : actions.map((a) => renderKeyRow(model, surface, a.id, draft[a.id] ?? "", canEdit))}
      ${refusal === "" ? nothing : html`<p class="error-line" data-test=${`hotkeys-refusal-${surface}`}>${refusal}</p>`}
      ${note === "" ? nothing : html`<p class="notice-line" data-test=${`hotkeys-note-${surface}`}>${note}</p>`}
      ${canEdit
        ? html`<div class="row-actions">
            <button type="button" class="btn btn-primary" data-action="ph-save" data-surface=${surface} ?disabled=${model.busy}>
              Save
            </button>
          </div>`
        : nothing}
    </section>
  `;
}

function renderKeyRow(model: HotkeysModel, surface: string, action: string, chord: string, canEdit: boolean): TemplateResult {
  const armed = model.capturing !== null && model.capturing.surface === surface && model.capturing.action === action;
  const owned = isBrowserKey(surface, chord);
  return html`
    <div class="krow">
      <span class="klabel">${platformActionLabel(surface, action)}</span>
      <span class="cap ${owned ? "cap-host" : ""} ${chord === "" ? "cap-none" : ""}" data-test=${`hotkeys-cap-${surface}-${action}`}>
        ${chord === "" ? "-" : platformKeycap(surface, chord)}
      </span>
      <span class="kcontrols">
        ${canEdit
          ? html`
              <button
                type="button"
                class="btn btn-secondary btn-small"
                data-action="ph-set"
                data-surface=${surface}
                data-key-action=${action}
                aria-pressed=${armed ? "true" : "false"}
                aria-label=${setAriaWords(surface, action)}
              >
                ${armed ? "Press a key" : "Set"}
              </button>
              ${chord === ""
                ? nothing
                : html`<button
                    type="button"
                    class="btn btn-secondary btn-small"
                    data-action="ph-clear"
                    data-surface=${surface}
                    data-key-action=${action}
                    aria-label=${clearAriaWords(surface, action)}
                  >
                    Unbind
                  </button>`}
            `
          : nothing}
      </span>
    </div>
  `;
}

/** The accessible names, one whole string each (the position gate's whole-value rule). */
function setAriaWords(surface: string, action: string): string {
  return `Set the key for: ${platformActionLabel(surface, action)}`;
}
function clearAriaWords(surface: string, action: string): string {
  return `Unbind: ${platformActionLabel(surface, action)}`;
}
