// Ported from hardscapeos_dibbits master 87d7941b app/src/quick-action-registry.ts; adapted for HH.

// quick-action-registry.ts: DIB-434. The client half of the quick-action registry: the wire
// types, the deep link an action resolves to, and the ONE decision the client is allowed to make
// about visibility (which is: none).
//
// NO DOM, NO LIT, NO api(): the same shape as dashboard-nav.ts and hash-route.ts, so every rule
// here is exercised on the Node runner without booting a browser.
//
// WHY THE CLIENT HOLDS NO GATE
// ----------------------------
// GET /api/v1/quick-actions returns the actions this caller may see, already resolved against the
// admin's configuration (internal/dashboard/quickaction.go). The client renders that list and
// nothing else. There is deliberately no "fetch everything, hide some of it" step: a gate the
// client applies is a gate a client can be made to skip, and this one decides whether a yard hand
// sees a till button. The only thing this module decides is WHERE an action goes once clicked.

import { hashTargetFor } from '@hardscapehouse/pos-kit/src/ported/ports.js';

/** One registered quick action, exactly as the server sends it. */
export interface QuickAction {
  action_id: string;
  label: string;
  glyph: string;
 /** Tooltip AND accessible name: one string so the two can never drift. */
  title: string;
 /** Shell unit key (e.g. 'sales'): the frozen deep-link contract, not a second router. */
  unit: string;
  /** Shell module key (e.g. 'pos'). */
  screen: string;
  /** What the destination surface is asked to do on arrival (e.g. 'pos.new_ticket'). */
  intent: string;
  lead?: string;
}

/** GET /api/v1/quick-actions. */
export interface QuickActionsResponse {
 /** The caller's resolved job shape: carried for the admin surface and for diagnosis. */
  profile: string;
  actions: QuickAction[];
}

/** One action's admin view: the catalog entry plus the resolved switch and per-shape matrix. */
export interface QuickActionConfig extends QuickAction {
  enabled: boolean;
  profiles: Record<string, boolean>;
 /** False when nothing has ever been saved: the admin surface says "default", not "chosen". */
  stored: boolean;
  default_enabled?: boolean;
  default_profiles?: Record<string, boolean>;
}

/** GET /api/v1/quick-actions/config (admin). */
export interface QuickActionConfigResponse {
  actions: QuickActionConfig[];
  profiles: string[];
}

/** The known intents. Exported so a destination surface names the same string the catalog does. */
export const INTENT_POS_NEW_TICKET = 'pos.new_ticket';

/**
 * UNION-TRAIN11 · the dealer pay key (DIB-496 map × DIB-488 workspace: the concept's "F8 Pay"
 * chip). Asks the till to complete the sale through the SAME guarded CTA path a click takes; the
 * terminal decides what an un-armed till does with it (walk to the payment step, never ring).
 */
export const INTENT_POS_PAY = 'pos.pay';

/**
 * The hash an action navigates to. It goes through hashTargetFor: the same helper the shell uses
 * to mirror its own state: so a quick action produces a bookmarkable `#sales/pos` and nothing
 * else. An action that named a surface outside the registry would produce a hash the shell refuses
 * to adopt, which is a visible dead click rather than a silent parallel navigation path.
 */
export function actionHash(action: Pick<QuickAction, 'unit' | 'screen'>): string {
  return hashTargetFor(action.unit, action.screen);
}

/**
 * True when clicking the action would not move the shell: i.e. the destination is already on
 * screen. The caller still publishes the intent (the destination has to act on it either way);
 * this only decides whether a hash write is needed, because writing the SAME hash pushes a
 * duplicate history entry and makes Back a no-op the user has to press twice.
 */
export function isAlreadyThere(action: Pick<QuickAction, 'unit' | 'screen'>, currentHash: string): boolean {
  return currentHash === actionHash(action);
}

/**
 * Find one action by id in a resolved list. Returns null when the caller may not see it: which is
 * the same answer as "it does not exist", and deliberately so: a keyboard shortcut or a future
 * palette entry must not be a way around the role gate.
 */
export function findAction(actions: readonly QuickAction[], id: string): QuickAction | null {
  return actions.find((a) => a.action_id === id) ?? null;
}
