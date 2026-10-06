// The desk's own hash router (the CP1 split-list round, decision log row
// 941). The desk is two screens, the quotes list and the quote (a new quote
// included), and the browser's own history carries the person between them:
// three routes, nothing more.
//
//   #/quotes          the list, the desk's landing page (the default: a boot
//                     with no hash at all lands here)
//   #/quotes/new      the work area at its Customer step (compose)
//   #/quotes/<id>     the work area with that quote's data
//   #/quotes/<id>/convert  the guided Convert to Order surface for that quote
//                     (a third screen: it shows in place of the desk, which stays mounted behind it)
//
// A hash route is chosen on purpose: the app is served from one page by the
// box (the front door's tile and the handoff walk land on it), the history
// API moves back and forward through hash changes with no server leg, and
// no route name can be mistaken for a file the box must serve. Anything the
// hash does not name is the list: an unknown fragment is the landing page,
// never a dead end.
//
// The router is a plain module (framework-free, like everything that is not
// a Lit element): the host parses, listens for hashchange, and hands the
// parsed route down through its own wiring pass. No template ever binds a
// route.

/** The one screen the desk is on. */
export type DeskRoute =
  | { kind: "list" }
  | { kind: "compose" }
  | { kind: "quote"; id: string }
  | { kind: "convert"; id: string };

/** The landing page's own route. */
export const LIST_ROUTE: DeskRoute = { kind: "list" };

/** The compose page's own route. */
export const COMPOSE_ROUTE: DeskRoute = { kind: "compose" };

/** The quote page's route for one quote. */
export function quoteRoute(id: string): DeskRoute {
  return { kind: "quote", id };
}

/** The guided convert surface's route for one quote. */
export function convertRoute(id: string): DeskRoute {
  return { kind: "convert", id };
}

/** The hash a route is written as. */
export function routeHash(route: DeskRoute): string {
  if (route.kind === "list") return "#/quotes";
  if (route.kind === "compose") return "#/quotes/new";
  if (route.kind === "convert") return `#/quotes/${route.id}/convert`;
  return `#/quotes/${route.id}`;
}

/** Whether the route is one of the quote pages (a quote or a new quote); the convert surface is its own screen. */
export function routeIsQuotePage(route: DeskRoute): boolean {
  return route.kind === "quote" || route.kind === "compose";
}

/**
 * The route the address bar names. A quote id is any single path segment
 * after #/quotes/ except the compose word; an empty id, a trailing slash, a
 * doubled id or any other shape is the list, so a mistyped or stale fragment
 * can never strand the person off the desk.
 */
export function parseRoute(hash: string): DeskRoute {
  const fragment = hash.replace(/^#/, "");
  if (fragment === "" || fragment === "/") return LIST_ROUTE;
  const parts = fragment.replace(/^\//, "").split("/").filter((p) => p !== "");
  if (parts.length === 0 || parts[0] !== "quotes") return LIST_ROUTE;
  if (parts.length === 1) return LIST_ROUTE;
  if (parts.length === 2 && parts[1] === "new") return COMPOSE_ROUTE;
  if (parts.length === 2 && parts[1] !== "") return quoteRoute(parts[1]);
  if (parts.length === 3 && parts[1] !== "new" && parts[2] === "convert") return convertRoute(parts[1] ?? "");
  return LIST_ROUTE;
}

/** True when the address bar already names the route (a walk that goes nowhere writes nothing). */
export function hashNames(hash: string, route: DeskRoute): boolean {
  const named = parseRoute(hash);
  if (named.kind !== route.kind) return false;
  if ((route.kind === "quote" && named.kind === "quote") || (route.kind === "convert" && named.kind === "convert")) return named.id === route.id;
  return true;
}
