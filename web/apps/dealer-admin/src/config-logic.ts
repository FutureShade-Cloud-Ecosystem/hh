// The role configuration surface's decisions, with no DOM in sight: the
// grid's axes, the seeded role to shape catalog, the cell summary words, the
// item set's shape checks, and the refusal words. No browser, no network.
//
// THE CORE'S OWN VOCABULARY, SPOKEN BACK. The thirteen profiles are the
// settings kit's validProfiles: 0140's five job shapes plus the eight
// manager refinements 0381 added (core/internal/settings/settings.go; the
// 422 sentence still names the five, a stale message, not the vocabulary).
// The ten surfaces are the kit's
// substrate's own route table marks (roleconfig's surface vocabulary, the
// same ten the config route's x-access names). The role to shape mapping is
// the seed of record (core/db/migrations/0140_dashboard_layouts.sql, the
// dashboard_role_profiles rows, as amended by 0381_manager_home_role_
// profiles.sql, which re-filed eight manager roles; a role with no row resolves to the kit's
// ProfileFallback, counter). The shape checks mirror the server's own
// validators in the server's own words: the client's copy only spares a
// round trip, the server's 422 stays the last word.
import type { RoleRow } from "./dealer-api.js";
import { UNKNOWN_SHAPE, UNKNOWN_SURFACE } from "./placeholders.js";

// -- the grid's axes ---------------------------------------------------------------------------------

/** One job shape row of the grid: the profile id and the words the row shows. */
export interface JobShape {
  readonly profile: string;
  readonly word: string;
  readonly what: string;
}

export const JOB_SHAPES: readonly JobShape[] = Object.freeze([
  { profile: "counter", word: "Counter", what: "The desk's own view, the least privileged shape by default." },
  { profile: "yard", word: "Yard", what: "The yard's view: the queue, the picks, the stock." },
  { profile: "driver", word: "Driver", what: "The road's view: the runs, the stops, the proof." },
  { profile: "manager", word: "Manager", what: "A manager role with no refinement row below; every seeded manager role has one." },
  { profile: "admin", word: "Admin", what: "Leadership's view: the register, the settings, the people." },
  { profile: "manager.branch", word: "Branch manager", what: "The branch manager's own Home and curation." },
  { profile: "manager.sales", word: "Sales manager", what: "The sales manager's own Home and curation." },
  { profile: "manager.yard", word: "Yard manager", what: "The yard manager's own Home, on the yard's tiles." },
  { profile: "manager.logistics", word: "Logistics manager", what: "The logistics manager's own Home, branched from the dock." },
  { profile: "manager.procurement", word: "Procurement manager", what: "The procurement manager's own Home and curation." },
  { profile: "manager.payables", word: "Payables and receivables", what: "The payables and receivables role's own Home." },
  { profile: "manager.controller", word: "Financial controller", what: "The financial controller's own Home and curation." },
  { profile: "manager.general", word: "General manager", what: "The general manager's own Home, branched from admin." },
]);

/** One surface column of the grid: the surface id and the words the column shows. */
export interface ConfigSurface {
  readonly id: string;
  readonly word: string;
}

export const CONFIG_SURFACES: readonly ConfigSurface[] = Object.freeze([
  { id: "counter-till", word: "Counter Till" },
  { id: "quote-desk", word: "Quote Desk" },
  { id: "yard-pick", word: "Yard Pick" },
  { id: "drive-run", word: "Drive Run" },
  { id: "dispatch-board", word: "Dispatch Board" },
  { id: "buying-and-stock", word: "Buying and Stock" },
  { id: "money-desk", word: "Money Desk" },
  { id: "manager-home", word: "Manager Home" },
  { id: "house-messages", word: "House Messages" },
  { id: "dealer-admin", word: "Dealer Admin" },
]);

/**
 * A job shape's word, and a surface's word.
 *
 * UX-106 round 3: a lookup that misses reads as the house placeholder word.
 * The stored key must not reach a person as text: a dealer reads
 * "manager.branch" as a key they cannot use, not as a shape.
 */
export function shapeWord(profile: string): string {
  return JOB_SHAPES.find((s) => s.profile === profile)?.word ?? UNKNOWN_SHAPE;
}

export function surfaceWord(id: string): string {
  return CONFIG_SURFACES.find((s) => s.id === id)?.word ?? UNKNOWN_SURFACE;
}

// -- a bounded pool ----------------------------------------------------------------------------------

/** How many cell reads ride at once: the grid is 130 reads, the core is not asked for them all together. */
export const GRID_READ_POOL = 6;

/** Run a task over every item with at most `limit` in flight at once. */
export async function inPool<T>(items: readonly T[], limit: number, task: (item: T) => Promise<void>): Promise<void> {
  let next = 0;
  const lane = async (): Promise<void> => {
    while (next < items.length) {
      const item = items[next];
      next += 1;
      await task(item);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, lane));
}

// -- the seeded role to shape catalog ----------------------------------------------------------------

/** The seed of record's mapping (migrations 0140, the dashboard_role_profiles rows, as amended by 0381). */
const SHAPE_ROLE_CODES: Readonly<Record<string, string>> = Object.freeze({
  INSIDE_SALES: "counter",
  OUTSIDE_SALES: "counter",
  YARD_TEAM: "yard",
  YARD_MANAGER: "manager.yard",
  DRIVERS: "driver",
  BRANCH_MANAGER: "manager.branch",
  SALES_MANAGER: "manager.sales",
  LOGISTICS_MANAGER: "manager.logistics",
  PROCUREMENT_MANAGER: "manager.procurement",
  PAYABLES_RECEIVABLES: "manager.payables",
  FINANCIAL_CONTROLLER: "manager.controller",
  GENERAL_MANAGER: "manager.general",
  SUPER_ADMIN: "admin",
  HR: "admin",
});

/**
 * A role's job shape: the seed's answer, or the kit's fallback for a role
 * with no row. The least privileged useful shape, never a manager's view by
 * accident, the core's own rule.
 */
export function profileForRoleCode(roleCode: string): string {
  return SHAPE_ROLE_CODES[roleCode] ?? "counter";
}

/** The catalog's role names a shape carries, in the catalog's own order. */
export function rolesForShape(profile: string, roles: readonly RoleRow[]): string[] {
  return roles.filter((r) => profileForRoleCode(r.code) === profile).map((r) => r.name);
}

// -- the cell summary --------------------------------------------------------------------------------

/** One curation row, as the config read carries it. */
export interface ConfigItem {
  readonly kind: string;
  readonly id: string;
  readonly position: number;
  readonly visible: boolean;
}

/**
 * A cell's words: what the dealer has curated here, counted by kind, or the
 * plain fact that the surface shows its own registry default.
 */
export function cellWords(items: readonly ConfigItem[]): string {
  if (items.length === 0) return "Registry default";
  const counts = new Map<string, number>();
  for (const it of items) counts.set(it.kind, (counts.get(it.kind) ?? 0) + 1);
  const parts = [...counts.entries()].map(([kind, n]) => `${n} ${kind}${n === 1 ? "" : "s"}`);
  return parts.join(", ");
}

// -- the item set's shape checks (the server's own words) ---------------------------------------------

export const ITEM_KINDS: readonly string[] = Object.freeze(["panel", "column", "chip", "action"]);

/** One registered item a surface exposes for role curation (UX-117). */
export interface SurfaceRegistryItem {
  readonly kind: string;
  readonly id: string;
  readonly label: string;
}

const DEFAULT_SURFACE_ITEMS: readonly SurfaceRegistryItem[] = Object.freeze([
  { kind: "panel", id: "lookup", label: "Product lookup panel" },
  { kind: "panel", id: "ticket", label: "Active ticket panel" },
  { kind: "panel", id: "summary", label: "Totals and standing panel" },
  { kind: "column", id: "cost", label: "Unit cost" },
  { kind: "column", id: "margin", label: "Line margin" },
  { kind: "column", id: "discount", label: "Line discount" },
  { kind: "column", id: "stock", label: "On-hand stock" },
  { kind: "chip", id: "pavers", label: "Pavers quick filter" },
  { kind: "chip", id: "walls", label: "Retaining walls quick filter" },
  { kind: "chip", id: "slabs", label: "Slabs and steps quick filter" },
  { kind: "chip", id: "aggregates", label: "Aggregates and soils quick filter" },
  { kind: "action", id: "override-price", label: "Override line price" },
  { kind: "action", id: "park-sale", label: "Park ticket" },
  { kind: "action", id: "refund-sale", label: "Refund or return" },
]);

const SURFACE_ITEMS_BY_SURFACE: Readonly<Record<string, readonly SurfaceRegistryItem[]>> = Object.freeze({
  "counter-till": DEFAULT_SURFACE_ITEMS,
  "quote-desk": [
    { kind: "panel", id: "quote-lines", label: "Quote lines panel" },
    { kind: "panel", id: "customer-standing", label: "Customer standing panel" },
    { kind: "column", id: "cost", label: "Unit cost" },
    { kind: "column", id: "margin", label: "Line margin" },
    { kind: "chip", id: "pavers", label: "Pavers quick filter" },
    { kind: "chip", id: "walls", label: "Retaining walls quick filter" },
    { kind: "action", id: "send-quote", label: "Send quote to customer" },
    { kind: "action", id: "convert-order", label: "Convert quote to order" },
  ],
});

/** The registered items for a surface and kind, for the curation item picker. */
export function surfaceItemsFor(surface: string, kind: string): readonly SurfaceRegistryItem[] {
  const list = SURFACE_ITEMS_BY_SURFACE[surface] ?? DEFAULT_SURFACE_ITEMS;
  return list.filter((item) => item.kind === kind);
}

const ID_SHAPE = /^[a-z0-9][a-z0-9._-]{0,63}$/;
const MAX_ITEMS_PER_PUT = 128;

/** One row's shape faults, in the core's own words. Empty means the row can go. */
export function itemRowError(row: ConfigItem): string {
  if (!ITEM_KINDS.includes(row.kind)) {
    return "Item kind must be panel, column, chip or action.";
  }
  if (!ID_SHAPE.test(row.id)) {
    return "Item id \u201c" + row.id + "\u201d must be 1 to 64 lowercase letters, digits, dots, dashes or underscores.";
  }
  if (row.position < 0 || !Number.isInteger(row.position)) {
    return "Item positions cannot be negative.";
  }
  return "";
}

/** The whole set's faults, in the core's own words. Empty means the PUT can go. */
export function itemSetError(items: readonly ConfigItem[]): string {
  if (items.length === 0) {
    return "The item set is empty; to reset, use the kind's own reset.";
  }
  if (items.length > MAX_ITEMS_PER_PUT) {
    return "At most 128 items per write.";
  }
  const seen = new Set<string>();
  for (const it of items) {
    const key = it.kind + "/" + it.id;
    if (seen.has(key)) {
      return "Item " + key + " appears twice.";
    }
    seen.add(key);
  }
  return "";
}

export function canSaveItems(items: readonly ConfigItem[]): boolean {
  return itemSetError(items) === "" && items.every((it) => itemRowError(it) === "");
}

// -- the workspace's own bounds ----------------------------------------------------------------------

/** The split's bounds, in the core's words. Empty means the save can go. */
export function workspaceSplitError(splitPct: number): string {
  if (splitPct < 20 || splitPct > 80) {
    return "Workspace split must be between 20 and 80.";
  }
  return "";
}

// -- the refusals ------------------------------------------------------------------------------------

/**
 * A refusal a person reads. The server's sentence on a 4xx is the core's own
 * words and stays whole; a bare word or an empty answer becomes plain words.
 */
export function configRefusalWords(error: string, status: number | undefined): string {
  const trimmed = error.trim();
  if (status !== undefined && status >= 400 && status < 500 && trimmed.includes(" ")) {
    return trimmed;
  }
  return "Try again in a moment.";
}

// -- the workspace save's key ------------------------------------------------------------------------

/** A fresh idempotency key per save; a verbatim replay answers the stored row. */
export function newWorkspaceKey(): string {
  return crypto.randomUUID();
}
