// Where a landing card or a needs row goes when the app that owns it is live.
//
// The first slice of Manager Home said "not on this box yet" for every surface
// that was not one of its own views. Four apps are live now, so a row or a card
// whose source one of them owns links there, and only a source with no app
// (dispatch, the order list) keeps the honest coming words. The table is closed:
// the hrefs are the deployment prefixes the front door serves (roles.ts tile
// hrefs), and a click may only ever carry one of them (isLiveHref), so no
// attribute value can send the page elsewhere.
//
// Port provenance: the original's home bento linked each widget to its
// #unit/module screen (repository hardscapeos_dibbits, read only; see
// PROVENANCE.md). HH's shell is one micro app per unit, so the same keys become
// this closed table of app prefixes.

/** The live apps a card can open, by the name the front door's tile carries. */
export const LIVE_APP_HREFS: Readonly<Record<string, string>> = Object.freeze({
  "Money Desk": "/money-desk/",
  "Buying and Stock": "/buying-and-stock/",
  "Counter Till": "/till/",
  "Yard Pick": "/yard/",
});

export interface AppLink {
  readonly app: string;
  readonly href: string;
}

function appLink(app: string): AppLink {
  return { app, href: LIVE_APP_HREFS[app] };
}

/** A click may carry only an address the table names. */
export function isLiveHref(href: string): boolean {
  return Object.values(LIVE_APP_HREFS).includes(href);
}

/**
 * The live app one needs source belongs to. The three yard manager sources and
 * approvals are this app's own views (needsRowTarget answers those first);
 * dispatch has no app yet and answers null.
 */
const NEEDS_APP: Readonly<Record<string, string>> = Object.freeze({
  "ar.escalation": "Money Desk",
  "pos.parked": "Counter Till",
  "tasks.stale_picks": "Yard Pick",
  "weights.pending": "Yard Pick",
});

export function needsLink(sourceKey: string): AppLink | null {
  return Object.hasOwn(NEEDS_APP, sourceKey) ? appLink(NEEDS_APP[sourceKey]) : null;
}

/** The module that measures a card, to the app that owns that module's screens. */
const MODULE_APP: Readonly<Record<string, string>> = Object.freeze({
  billing: "Money Desk",
  pos: "Counter Till",
  tasks: "Yard Pick",
  inventory: "Buying and Stock",
  purchasing: "Buying and Stock",
});

export type WidgetLink =
  | { readonly kind: "app"; readonly app: string; readonly href: string }
  | { readonly kind: "view"; readonly view: "warranty" }
  | { readonly kind: "coming" };

export function widgetLink(w: { readonly module: string }): WidgetLink {
  if (w.module === "warranty") return { kind: "view", view: "warranty" };
  if (Object.hasOwn(MODULE_APP, w.module)) return { kind: "app", ...appLink(MODULE_APP[w.module]) };
  return { kind: "coming" };
}
