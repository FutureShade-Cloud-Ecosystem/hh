// Ported from hardscapeos_dibbits master 87d7941b app/src/quick-action-registry.ts;
// adapted for HH (the registry's decisions, split from the wire types, which
// live in quick-api.ts's whitelist projections).

// quick-action-logic.ts: DIB-434. The client half of the quick-action
// registry: where an action goes on THIS box, the words and the mark its
// button draws, and the ONE decision the client is allowed to make about
// visibility (which is: none).
//
// WHY THE CLIENT HOLDS NO GATE
// ----------------------------
// GET /api/v1/quick-actions returns the actions this caller may see, already
// resolved against the admin's configuration (core/internal/dashboard/
// quickaction.go). The client renders that list and nothing else. There is
// deliberately no "fetch everything, hide some of it" step: a gate the client
// applies is a gate a client can be made to skip, and this one decides whether
// a yard hand sees a till button. The only thing this module decides is WHERE
// an action goes once clicked, and it never starts money or a write by
// itself: an action navigates, and the destination owns whatever happens next.
import { DEFAULTS } from "@hardscapehouse/pos-kit/src/ported/_icon-consumer.js";
import type { QuickAction } from "./quick-api.js";
import { isForbiddenBody, VENDOR_ADMIN_WORDS } from "./warranty-logic.js";

/** The known intents. Exported so a destination names the same string the catalog does. */
export const INTENT_POS_NEW_TICKET = "pos.new_ticket";

/**
 * The box's own address for the destination the core names.
 *
 * The original resolved an action to a `#unit/module` hash of the one shell.
 * HH's shell is one micro app per unit, so the same frozen contract becomes a
 * closed table from the core's (unit, screen) keys to the deployment prefix
 * the front door serves the surface at (roles.ts's tile hrefs name the same
 * prefixes). An action whose destination no app serves yet answers null: the
 * bar says so in words instead of drawing a dead button, the same honesty the
 * landing's needs rows carry.
 */
const DESTINATION_HREF: Readonly<Record<string, string>> = Object.freeze({
  "sales/pos": "/till/",
});

export function actionHref(action: Pick<QuickAction, "unit" | "screen">): string | null {
  const key = `${action.unit}/${action.screen}`;
  return Object.hasOwn(DESTINATION_HREF, key) ? DESTINATION_HREF[key] : null;
}

/**
 * Find one action by id in a resolved list. Returns null when the caller may
 * not see it, which is the same answer as "it does not exist", and deliberately
 * so: a keyboard shortcut or a future palette entry must not be a way around
 * the role gate.
 */
export function findAction(actions: readonly QuickAction[], id: string): QuickAction | null {
  return actions.find((a) => a.action_id === id) ?? null;
}

/** True when the wire label carries the add-plus (the Sale action's leading "+ "). */
export function hasLeadPlus(label: string): boolean {
  return /^\+\s/.test(label);
}

/**
 * The visible word: the wire label minus ONE leading "+ ". The field itself is
 * untouched (the wire label stays byte for byte), and no spelling can lose the
 * plus entirely, because the mark is drawn only when the plus was there.
 */
export function wordOf(label: string): string {
  return hasLeadPlus(label) ? label.replace(/^\+\s/, "") : label;
}

/**
 * The mark the button draws, resolved by the label's own leading plus (the
 * original's row 523 rule): an action that IS an add draws the add mark, every
 * other action draws its destination screen's approved default. The ids are
 * registry ids, never characters; the icon consumer falls anything unknown to
 * the neutral square, so a mark never renders as an empty box or as text.
 */
export function actionIconId(action: Pick<QuickAction, "label" | "screen">): string {
  if (hasLeadPlus(action.label)) return "pencil:add";
  const modules = DEFAULTS.modules as Readonly<Record<string, string>>;
  return Object.hasOwn(modules, action.screen) ? modules[action.screen] : "lucide:square";
}

/** The saved confirmation, the action's own words in it. */
export function savedWords(label: string): string {
  if (label === "") return "Saved. Everyone picks it up on their next page load.";
  return `${label} saved. Everyone picks it up on their next page load.`;
}

/**
 * The job shapes as the words a person reads. The closed set is the core's
 * Profiles (model.go), the five shapes and the manager refinements born from
 * them; the words are the role names the core's own comments and the demo
 * flow table carry. A shape outside the set answers as itself, never an
 * invented word.
 */
const PROFILE_WORD: Readonly<Record<string, string>> = Object.freeze({
  counter: "Counter",
  yard: "Yard",
  driver: "Driver",
  manager: "Manager",
  admin: "Admin",
  "manager.branch": "Branch manager",
  "manager.sales": "Sales manager",
  "manager.yard": "Yard manager",
  "manager.logistics": "Logistics manager",
  "manager.procurement": "Procurement manager",
  "manager.payables": "Payables and Receivables",
  "manager.controller": "Financial controller",
  "manager.general": "General manager",
});

export function profileWord(profile: string): string {
  return PROFILE_WORD[profile] ?? profile;
}

/**
 * The admin view's refusal. The core decides who configures; when it refuses
 * with a sentence of its own, the screen shows that sentence VERBATIM, never a
 * paraphrase. Only the shared Forbidden body, which is exactly one word and
 * says nothing, is turned into words naming who may configure (the warranty
 * desk's words and test), so no manager meets a bare wall.
 */
export function configRefusalWords(error: string, status: number | undefined): string {
  if (status === 403 && isForbiddenBody(error)) {
    return `You may not change the quick actions. ${VENDOR_ADMIN_WORDS}`;
  }
  return error;
}
