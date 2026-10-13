// Manager Home's quick-action screens: the bar on the landing (sub item 1)
// and the admin's per-shape config view (sub item 2). Pure template functions
// over plain model objects; app-root owns every call and every state change.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings, and bound values only in class, role
// and the data- and aria- namespaces. The bar's accessible name therefore
// rides aria-label: the core carries the button's sentence as ONE string
// (quickaction.go's Title), so what a screen reader announces is exactly what
// the core wrote, and no second tooltip string can drift from it.
import { html, nothing, type TemplateResult } from "lit";
import { drawIcon, iconForId } from "@hardscapehouse/pos-kit/src/ported/_icon-consumer.js";
import type { QuickAction, QuickActionConfig } from "./quick-api.js";
import { actionHref, actionIconId, profileWord, savedWords, wordOf } from "./quick-logic.js";

// -- the landing's quick-action bar -----------------------------------------------------------------

export interface QuickBarModel {
  readonly phase: "loading" | "ready" | "failed";
  readonly actions: readonly QuickAction[];
}

/**
 * The landing's row of the caller's quick actions.
 *
 * WHAT THIS BAR IS NOT: it is not a "+ Sale" button. It renders whatever
 * GET /api/v1/quick-actions returned, which today is one entry for the shapes
 * that sell and NONE for the shapes that do not. An empty list, a load still
 * in flight and a failed load all render NOTHING (not a disabled button, not
 * a placeholder): this is optional chrome, the landing's own surfaces are all
 * still reachable, and a shortcut that could not be resolved should disappear
 * rather than render an error next to the day's work. The server has already
 * made the opposite choice for the case it owns (an unreadable config table
 * falls back to the shipped defaults rather than to hidden), so "no bar" here
 * means the request itself answered nothing, never that somebody's
 * configuration was quietly lost.
 *
 * An action never starts money or a write by itself. Clicking it publishes
 * the destination's intent (quick-intent.ts) and navigates to the address
 * quick-logic.ts maps the core's destination keys to; the destination surface
 * owns everything that happens on arrival. An action whose destination this
 * box does not serve is not a button: it is its words and the not-here
 * sentence, the landing's honesty for surfaces it does not carry.
 */
export function renderQuickBar(model: QuickBarModel): TemplateResult | typeof nothing {
  if (model.phase !== "ready" || model.actions.length === 0) return nothing;
  return html`
    <div class="quick-bar" data-quick-bar role="toolbar" aria-label="Quick actions">
      ${model.actions.map((a) => renderQuickAction(a))}
    </div>
  `;
}

function renderQuickAction(a: QuickAction): TemplateResult {
  const href = actionHref(a);
  if (href === null) {
    return html`
      <span class="quick-run quick-absent" data-quick=${a.action_id}>
        <span class="mark" aria-hidden="true">${drawIcon(iconForId(actionIconId(a)), 16)}</span>
        <span class="word">${wordOf(a.label)}</span>
        <span class="not-here">not on this box yet</span>
      </span>
    `;
  }
  return html`
    <button
      type="button"
      class="quick-run"
      data-action="quick-run"
      data-action-id=${a.action_id}
      data-href=${href}
      aria-label=${a.title}
    >
      <span class="mark" aria-hidden="true">${drawIcon(iconForId(actionIconId(a)), 16)}</span>
      <span class="word">${wordOf(a.label)}</span>
    </button>
  `;
}

// -- the admin's config view (sub item 2) -------------------------------------------------------------

export interface QuickAdminModel {
  readonly phase: "loading" | "ready" | "refused" | "error";
  readonly actions: readonly QuickActionConfig[];
  /** The closed set of job shapes, in the core's own order. */
  readonly profiles: readonly string[];
  /** A load failure in words. */
  readonly error: string;
  /** The core's refusal, its own words verbatim. */
  readonly refusal: string;
  /** The action whose save just landed, carrying the confirmation under it alone. */
  readonly savedFor: string;
  readonly busyId: string;
}

/**
 * The admin's quick-actions config: one row per registered action, the global
 * switch first (it is the one that overrides everything; its off is final, the
 * core's own gate order), then every job shape with the answer it is actually
 * getting, resolved server side, so no shape is ever a blank cell.
 *
 * THE GATE IS THE SERVER'S, and this app invents none of its own: the view is
 * reachable from every landing, and a caller the core does not make an admin
 * gets the shared Forbidden body, shown here verbatim. An action's draft is
 * local until Save; a mis-click costs nothing.
 */
export function renderQuickAdmin(model: QuickAdminModel): TemplateResult {
  return html`
    <div class="view-head">
      <button type="button" class="btn btn-secondary back" data-action="back"><span class="chev" aria-hidden="true">‹</span>Back to Home</button>
      <h1>Quick actions</h1>
      <p class="page-sub">Turn a registered action on or off, for every job shape or for one at a time.</p>
    </div>
    ${model.error !== "" ? html`<p class="error-line" data-test="quick-error">${model.error}</p>` : nothing}
    ${model.refusal !== "" ? html`<p class="refusal" data-test="quick-refusal">${model.refusal}</p>` : nothing}
    ${model.phase === "loading"
      ? html`<p class="notice-line">Loading the quick actions…</p>`
      : model.phase === "error"
        ? html`<div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-view">Try again</button></div>`
        : model.phase === "refused"
          ? nothing
          : model.actions.length === 0
            ? html`<section class="sheet"><div class="list-card"><p class="notice-line">No quick actions are registered.</p></div></section>`
            : html`<section class="sheet">${model.actions.map((a) => renderQuickAdminRow(a, model))}</section>`}
  `;
}

function renderQuickAdminRow(a: QuickActionConfig, model: QuickAdminModel): TemplateResult {
  const busy = model.busyId === a.action_id;
  return html`
    <article class="list-card quick-config" data-quick=${a.action_id}>
      <div class="qa-head">
        <span class="qa-name">${a.label}</span>
        <span class="qa-id">${a.action_id}</span>
      </div>
      <p class="qa-lead">${a.lead !== "" ? a.lead : a.title}</p>
      <button
        type="button"
        class="qa-switch"
        data-action="quick-toggle-show"
        data-action-id=${a.action_id}
        aria-pressed=${a.enabled ? "true" : "false"}
      >Show</button>
      <div class="qa-shapes" role="group" aria-label="Job shapes that see ${a.label}">
        ${model.profiles.map((p) => html`
          <button
            type="button"
            class="qa-shape"
            data-action="quick-toggle-shape"
            data-action-id=${a.action_id}
            data-profile=${p}
            aria-pressed=${a.profiles[p] === true ? "true" : "false"}
            ?disabled=${!a.enabled}
          >${profileWord(p)}</button>
        `)}
      </div>
      <div class="row-actions qa-foot">
        <button type="button" class="btn btn-primary" data-action="quick-save" data-action-id=${a.action_id} ?disabled=${busy}>
          ${busy ? "Saving…" : "Save"}
        </button>
        ${a.stored ? nothing : html`<span class="qa-default" title="Nothing has been saved for this action yet: these are the shipped defaults.">shipped default</span>`}
        ${model.savedFor === a.action_id
          ? html`<span class="notice-line qa-saved" role="status">${savedWords(a.label)}</span>`
          : nothing}
      </div>
    </article>
  `;
}
