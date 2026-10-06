// The desk's surfaces and their role walls (decision log rows 955, 956, 957).
// Every job is its own route: the four desk surfaces in the top navigation
// and the four quote surfaces as tabs on the quote page. This module is the
// whole table, framework-free: it parses a hash into a route, writes a route
// back, and says which role sees which surface. Hiding is the wall's own
// answer, so the navigation and the router read the same table and cannot
// disagree.
//
//   #/quotes                    Quotes, the landing (Sales, Sales manager)
//   #/quotes/new                a new quote at its Customer step
//   #/quotes/<id>               Build, the stepper work area (both roles)
//   #/quotes/<id>/activity      Activity: history and follow ups (both roles)
//   #/quotes/<id>/stock         Stock: availability at the branch (both roles)
//   #/quotes/<id>/admin         Admin: per line admin and Check Quote (manager)
//   #/approvals                 the margin approvals queue (manager)
//   #/approvals/<request id>    one request in its quote's context (manager)
//   #/pricing                   the pricing console (manager)
//   #/settings                  screen layout, acceptance, scope rules (manager)
//
// Anything the hash does not name is the Quotes list: a stale fragment is the
// landing page, never a dead end.

export type DeskRole = "sales" | "manager";
export type QuoteTab = "build" | "activity" | "stock" | "admin";
export type DeskTab = "quotes" | "approvals" | "pricing" | "settings";

export type SurfaceRoute =
  | { kind: "quotes" }
  | { kind: "compose" }
  | { kind: "quote"; id: string; tab: QuoteTab }
  | { kind: "approvals" }
  | { kind: "approval"; id: string }
  | { kind: "pricing" }
  | { kind: "settings" };

/** The role code the demo mint carries for the Sales manager; every other caller is Sales. */
export const MANAGER_ROLE_CODE = "sales_manager";

export function roleOf(roleCode: string | null | undefined): DeskRole {
  return roleCode === MANAGER_ROLE_CODE ? "manager" : "sales";
}

export const QUOTES_ROUTE: SurfaceRoute = { kind: "quotes" };

const QUOTE_TABS: readonly QuoteTab[] = ["build", "activity", "stock", "admin"];

/** Who sees each quote tab. */
const QUOTE_TAB_ROLES: Record<QuoteTab, readonly DeskRole[]> = {
  build: ["sales", "manager"],
  activity: ["sales", "manager"],
  stock: ["sales", "manager"],
  admin: ["manager"],
};

/** Who sees each desk tab, in the order the bar draws them. */
const DESK_TAB_ROLES: Record<DeskTab, readonly DeskRole[]> = {
  quotes: ["sales", "manager"],
  approvals: ["manager"],
  pricing: ["manager"],
  settings: ["manager"],
};

export const DESK_TAB_LABEL: Record<DeskTab, string> = {
  quotes: "Quotes",
  approvals: "Approvals",
  pricing: "Pricing",
  settings: "Settings",
};

export const QUOTE_TAB_LABEL: Record<QuoteTab, string> = {
  build: "Build",
  activity: "Activity",
  stock: "Stock",
  admin: "Admin",
};

export function deskTabsFor(role: DeskRole): DeskTab[] {
  return (Object.keys(DESK_TAB_ROLES) as DeskTab[]).filter((tab) => DESK_TAB_ROLES[tab].includes(role));
}

export function quoteTabsFor(role: DeskRole): QuoteTab[] {
  return QUOTE_TABS.filter((tab) => QUOTE_TAB_ROLES[tab].includes(role));
}

export function deskTabHash(tab: DeskTab): string {
  return `#/${tab}`;
}

export function quoteTabHash(id: string, tab: QuoteTab): string {
  return tab === "build" ? `#/quotes/${encodeURIComponent(id)}` : `#/quotes/${encodeURIComponent(id)}/${tab}`;
}

/** The desk tab a route belongs to (a quote page and a new quote sit under Quotes; a request detail under Approvals). */
export function deskTabOf(route: SurfaceRoute): DeskTab {
  if (route.kind === "approvals" || route.kind === "approval") return "approvals";
  if (route.kind === "pricing") return "pricing";
  if (route.kind === "settings") return "settings";
  return "quotes";
}

export function routeHash(route: SurfaceRoute): string {
  switch (route.kind) {
    case "quotes":
      return "#/quotes";
    case "compose":
      return "#/quotes/new";
    case "quote":
      return quoteTabHash(route.id, route.tab);
    case "approvals":
      return "#/approvals";
    case "approval":
      return `#/approvals/${encodeURIComponent(route.id)}`;
    case "pricing":
      return "#/pricing";
    case "settings":
      return "#/settings";
  }
}

function segment(part: string): string {
  try {
    return decodeURIComponent(part);
  } catch {
    return part;
  }
}

export function parseSurfaceRoute(hash: string): SurfaceRoute {
  const parts = hash
    .replace(/^#/, "")
    .split("/")
    .filter((p) => p !== "");
  const [head, second, third] = parts;
  if (head === "approvals") {
    if (parts.length === 1) return { kind: "approvals" };
    if (parts.length === 2 && second !== undefined) return { kind: "approval", id: segment(second) };
    return QUOTES_ROUTE;
  }
  if (head === "pricing" && parts.length === 1) return { kind: "pricing" };
  if (head === "settings" && parts.length === 1) return { kind: "settings" };
  if (head === "quotes" && second !== undefined) {
    if (second === "new" && parts.length === 2) return { kind: "compose" };
    if (parts.length === 2) return { kind: "quote", id: segment(second), tab: "build" };
    if (parts.length === 3 && third !== undefined && (QUOTE_TABS as readonly string[]).includes(third) && third !== "build") {
      return { kind: "quote", id: segment(second), tab: third as QuoteTab };
    }
  }
  return QUOTES_ROUTE;
}

/** The role wall: whether the role sees the route's surface. */
export function canSee(role: DeskRole, route: SurfaceRoute): boolean {
  if (route.kind === "quote") return QUOTE_TAB_ROLES[route.tab].includes(role);
  return DESK_TAB_ROLES[deskTabOf(route)].includes(role);
}
