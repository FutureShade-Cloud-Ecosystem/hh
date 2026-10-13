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
import type { QuickAction } from "./quick-api.js";
import { actionHref, actionIconId, wordOf } from "./quick-logic.js";

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
