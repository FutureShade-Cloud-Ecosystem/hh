// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-surface.ts;
// adapted for HH.
//
// quote-surface.ts: the `quotes.builder` SURFACE REGISTRY, the front end's
// and only the front end's. This module declares the panels, the column
// catalog (each column carrying its class), and the action catalog for the
// quote work area. The server stores curation rows against these ids but
// validates shape only: an id it has never heard of is stored happily and an
// id THIS module has never heard of renders nothing, silently, which is the
// only honest answer. The resolver below is the one every render reads; the
// work area holds no hard-coded layout list.
//
// IDS ARE ADD-ONLY: a rename orphans saved curation. Add ids, never rename.
//
// COLUMN CLASSES:
//   standard  anyone who can open the surface.
//   cost      unit cost and margin, served ONLY per the dealer's cost and
//             margin list, enforced server-side by byte absence. A cost
//             column a person shows is still EMPTY when the server sent no
//             data; this registry only places the column, never the data.
//   optional  shipped hidden by dealer default; curate on.

export type SurfaceItemKind = "panel" | "column" | "chip" | "action";
export type ColumnClass = "standard" | "cost" | "optional";

export interface SurfaceDef {
  id: string;
  kind: SurfaceItemKind;
  label: string;
  /** Columns only. */
  cls?: ColumnClass;
  /** Panels only: part of the minimum set, which dealer curation cannot remove. */
  min?: boolean;
}

export const QUOTES_BUILDER_SURFACE_ID = "quotes.builder";

/** The panel catalog. The minimum set: the lines and the summary. */
export const QB_PANELS: SurfaceDef[] = [
  { id: "lines", kind: "panel", label: "Products", min: true },
  { id: "summary", kind: "panel", label: "Customer & terms", min: true },
  { id: "addons", kind: "panel", label: "Suggested add-ons" },
];

/** The lines table's column catalog, in the reference's order. */
export const QB_COLUMNS: SurfaceDef[] = [
  { id: "line.thumb", kind: "column", label: "Photo", cls: "standard" },
  { id: "line.item", kind: "column", label: "Item", cls: "standard" },
  { id: "line.qty", kind: "column", label: "Qty", cls: "standard" },
  { id: "line.uom", kind: "column", label: "Unit", cls: "standard" },
  { id: "line.unit_price", kind: "column", label: "Unit Price", cls: "standard" },
  { id: "line.disc_pct", kind: "column", label: "Disc %", cls: "standard" },
  { id: "line.total", kind: "column", label: "Total", cls: "standard" },
  { id: "line.unit_cost", kind: "column", label: "Cost", cls: "cost" },
  { id: "line.margin_pct", kind: "column", label: "Margin", cls: "cost" },
];

/** The action bar catalog. */
export const QB_ACTIONS: SurfaceDef[] = [
  { id: "quote.save_draft", kind: "action", label: "Save" },
  { id: "quote.preview", kind: "action", label: "Preview" },
  { id: "quote.print", kind: "action", label: "Print" },
  { id: "quote.copy_link", kind: "action", label: "Copy Link" },
  { id: "quote.send", kind: "action", label: "Send Quote" },
  { id: "quote.convert_order", kind: "action", label: "Convert to Order" },
];

/** Everything, for a settings page's per-kind curation lists. */
export const QB_CATALOG: SurfaceDef[] = [...QB_PANELS, ...QB_COLUMNS, ...QB_ACTIONS];

// ── resolution ─────────────────────────────────────────────────────────────────

/** One curation row as the settings kit's wire speaks it. */
export interface WireItem {
  kind: SurfaceItemKind;
  id: string;
  position: number;
  visible: boolean;
}

export interface ResolvedItem extends SurfaceDef {
  position: number;
  visible: boolean;
}

/**
 * effectiveItems: the one resolver every render reads.
 *
 *   1. The USER's rows of this kind, when any exist, are the WHOLE story
 *      (whole or absent; reset is a DELETE server-side).
 *   2. Else the ROLE (dealer) rows.
 *   3. Else the catalog's own order, visible by default except cost-class
 *      columns (which ship hidden; the data behind them is server-gated
 *      regardless).
 *
 * Catalog entries missing from the winning set are APPENDED HIDDEN, so a
 * curator can switch an item on without a migration. Rows whose id this
 * registry does not know render nothing, silently.
 */
export function effectiveItems<T extends { id: string; kind: SurfaceItemKind; cls?: string }>(
  kind: SurfaceItemKind,
  catalog: T[],
  roleItems: WireItem[] | null | undefined,
  userItems: WireItem[] | null | undefined,
): (T & { position: number; visible: boolean })[] {
  const ofKind = (rows: WireItem[] | null | undefined): WireItem[] =>
    (Array.isArray(rows) ? rows : []).filter((r) => r && r.kind === kind);
  const user = ofKind(userItems);
  const role = ofKind(roleItems);
  const winning = user.length > 0 ? user : role;

  const byId = new Map(catalog.filter((c) => c.kind === kind).map((c) => [c.id, c]));
  const out: (T & { position: number; visible: boolean })[] = [];
  const seen = new Set<string>();

  if (winning.length > 0) {
    const sorted = [...winning].sort((a, b) => a.position - b.position || (a.id < b.id ? -1 : 1));
    for (const row of sorted) {
      const def = byId.get(row.id);
      if (!def) continue; // unknown id: renders nothing, silently
      seen.add(row.id);
      out.push({ ...def, position: row.position, visible: row.visible !== false });
    }
    for (const def of catalog.filter((c) => c.kind === kind)) {
      if (!seen.has(def.id)) out.push({ ...def, position: out.length, visible: false });
    }
    return out;
  }
  return catalog
    .filter((c) => c.kind === kind)
    .map((def, i) => ({ ...def, position: i, visible: def.cls !== "cost" }));
}

/** The visible subset, in order: what a render actually walks. */
export function visibleItems<T extends { visible: boolean }>(resolved: T[]): T[] {
  return resolved.filter((r) => r.visible);
}

/**
 * panelVisible: the minimum set (min: true) is ALWAYS on, whatever curation
 * says. Curation hides the optional panels; it can never blank the work area.
 */
export function panelVisible(resolved: ResolvedItem[], id: string): boolean {
  const def = QB_PANELS.find((p) => p.id === id);
  if (def?.min) return true;
  const row = resolved.find((r) => r.id === id);
  return row ? row.visible : Boolean(def);
}
