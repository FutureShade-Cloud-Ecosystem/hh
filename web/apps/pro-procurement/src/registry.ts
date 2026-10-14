// The app's own surface registry: the surfaces the cycle brief names, each
// gated by the one capability that guards its routes in the core
// (core/internal/portal). Today rides the dashboard route (view_billing); the
// rest answer to submit_rfq, the gate the procurement, catalog and quote
// routes share. A surface the person's role does not open shows the refusal
// state; the server stays the boundary, this only keeps a screen from
// offering what the server will refuse.
//
// The frame's selector reads the Pro kit's catalogue; its ids for this app are
// this app's own top surfaces, so the mapping is the identity.
import type { Capability } from "@hardscapehouse/pro-kit";

export type SurfaceId = "today" | "board" | "projects" | "catalog" | "quotes" | "scope" | "product";

export interface SurfaceMeta {
  /** The words a person reads for the surface. */
  readonly label: string;
  /** The capability that opens it; the routes behind it answer to the same gate. */
  readonly capability: Capability;
}

export const SURFACES: Readonly<Record<SurfaceId, SurfaceMeta>> = {
  today: { label: "Today", capability: "view_billing" },
  board: { label: "Board", capability: "submit_rfq" },
  projects: { label: "Projects", capability: "submit_rfq" },
  catalog: { label: "Catalog", capability: "submit_rfq" },
  quotes: { label: "Quotes", capability: "submit_rfq" },
  scope: { label: "Order scope", capability: "submit_rfq" },
  product: { label: "Product", capability: "submit_rfq" },
};

/** The surface a signed in person lands on. */
export const LANDING: SurfaceId = "today";

/** The surfaces the frame's selector can name directly; the drill-downs (order scope, product) hang off these. */
export const TOP_SURFACES: readonly SurfaceId[] = ["today", "board", "projects", "catalog", "quotes"];

const DRILL_DOWN_PARENT: Readonly<Record<string, SurfaceId>> = { scope: "board", product: "catalog" };

export function isSurfaceId(value: string): value is SurfaceId {
  return Object.prototype.hasOwnProperty.call(SURFACES, value);
}

/** The kit catalogue's surface ids for this app, to the surface of this app they open. */
const CATALOGUE_ROUTES: Readonly<Record<string, SurfaceId>> = {
  board: "board",
  projects: "projects",
  catalog: "catalog",
  today: "today",
  quotes: "quotes",
};

export function surfaceRouteFromCatalogue(catalogueId: string): { surface: SurfaceId } | null {
  const surface = CATALOGUE_ROUTES[catalogueId];
  return surface === undefined ? null : { surface };
}

/** Where a route sits in the frame's selector: a drill-down keeps its parent's entry marked. */
export function frameSurfaceOf(route: { surface: SurfaceId; from?: SurfaceId }): SurfaceId {
  if (TOP_SURFACES.includes(route.surface)) return route.surface;
  const parent = route.from !== undefined && TOP_SURFACES.includes(route.from) ? route.from : DRILL_DOWN_PARENT[route.surface];
  return parent ?? LANDING;
}
