// The builder's decisions, with no DOM in sight: the preset spans and their
// words, what a move or a resize does to the working set, the whole-layout
// body a save PUTs, the form's draft rules, the pinned-tab strip's edit rule,
// and the classic tile editor's rows.
//
// Port provenance: the preset table, the snap set, the move/resize/remove
// rules, the draft reconcile, the gallery placement and the pin strip's
// whole-or-absent edit rule are the original app's _widget.ts,
// home-bento-logic.ts, dashboard-logic.ts and dashboard-nav.ts (repository
// hardscapeos_dibbits, read only; see PROVENANCE.md), narrowed to what the
// arrange view uses and re-worded for HH. The core's own arithmetic
// (core/internal/dashboard/widget.go: SpanOf, WidgetColSet, WidgetMaxRows,
// AutoTitle) is mirrored, not reinvented: a span the server would refuse with
// a 422 never leaves this file.
import type {
  BuilderGalleryCard,
  BuilderGalleryEntryRow,
  BuilderMeasure,
  BuilderPin,
  BuilderTileCatalogEntry,
  BuilderTileRow,
  BuilderWidgetRow,
  BuilderWidgetView,
} from "./builder-api.js";

// -- the vocabulary --------------------------------------------------------------------------------------
//
// No raw enum reaches a screen. A value this build has never heard of
// degrades into something plain and harmless, never into a confident lie.

function plainly(value: string): string {
  return value.trim().toLowerCase().replace(/[_.-]+/g, " ").replace(/\s+/g, " ");
}

/** The bento presets, SMALLEST FIRST BY AREA: S 3x1, T 3x2, M 6x1, L 6x2, XL 12x1. */
export const WIDGET_SIZES: readonly string[] = ["S", "T", "M", "L", "XL"];
export const DEFAULT_SIZE = "M";

const SIZE_WORD: Readonly<Record<string, string>> = Object.freeze({
  S: "Small",
  T: "Tall",
  M: "Medium",
  L: "Large",
  XL: "Wide",
});

export function sizeWord(size: string): string {
  return SIZE_WORD[size] ?? plainly(size);
}

const SHAPE_WORD: Readonly<Record<string, string>> = Object.freeze({
  stat: "Number",
  trend: "Trend",
  bars: "Bars",
  list: "List",
  board: "Board",
  gauge: "Gauge",
});

export function shapeWord(shape: string): string {
  return SHAPE_WORD[shape] ?? plainly(shape);
}

const SPLIT_WORD: Readonly<Record<string, string>> = Object.freeze({
  none: "No split",
  branch: "By branch",
  status: "By status",
  bucket: "By age",
});

export function splitWord(split: string): string {
  return SPLIT_WORD[split] ?? plainly(split);
}

/** Which tier a layout came from, as the reset button's words. */
const SOURCE_WORD: Readonly<Record<string, string>> = Object.freeze({
  user: "Your layout",
  role: "The role default",
  catalog: "The catalog default",
});

export function sourceWord(source: string): string {
  return SOURCE_WORD[source] ?? (source === "" ? "" : plainly(source));
}

/** The body forms a widget may take, in builder order; the closed set the core validates against. */
export const WIDGET_SHAPES: readonly string[] = ["stat", "trend", "bars", "list", "board", "gauge"];

// -- the grid arithmetic ---------------------------------------------------------------------------------

export interface BentoSpan {
  readonly cols: number;
  readonly rows: number;
}

/** The footprint of a size, mirroring SpanOf. An unknown size is 0x0: the caller decides. */
export function spanOf(size: string): BentoSpan {
  if (size === "S") return { cols: 3, rows: 1 };
  if (size === "T") return { cols: 3, rows: 2 };
  if (size === "M") return { cols: 6, rows: 1 };
  if (size === "L") return { cols: 6, rows: 2 };
  if (size === "XL") return { cols: 12, rows: 1 };
  return { cols: 0, rows: 0 };
}

/** The column widths a card may rest on: the divisors and half divisors of twelve. */
export const COLSNAP: readonly number[] = [3, 4, 6, 8, 9, 12];
export const BENTO_MAX_COLS = COLSNAP[COLSNAP.length - 1];
export const BENTO_MAX_ROWS = 3;

/** True when a pair names a footprint this grid actually has: the browser half of the write gate. */
export function isBentoSpan(cols: number, rows: number): boolean {
  return COLSNAP.includes(cols) && Number.isInteger(rows) && rows >= 1 && rows <= BENTO_MAX_ROWS;
}

/** Which span a card is drawn at: the free pair, then the preset, then the served span, then 1x1. */
export function widgetSpan(w: Pick<BuilderWidgetView, "size" | "span_cols" | "span_rows"> & { cols?: number; rows?: number }): BentoSpan {
  const free = { cols: Math.floor(Number(w.cols)), rows: Math.floor(Number(w.rows)) };
  if (isBentoSpan(free.cols, free.rows)) return free;
  const preset = spanOf(w.size);
  if (preset.cols > 0 && preset.rows > 0) return preset;
  const served = { cols: Math.floor(Number(w.span_cols)), rows: Math.floor(Number(w.span_rows)) };
  if (isBentoSpan(served.cols, served.rows)) return served;
  return { cols: 1, rows: 1 };
}

/** The preset a footprint is, or null: an 8x2 is a card somebody dragged, not a letter. */
export function sizeForSpan(cols: number, rows: number): string | null {
  for (const size of WIDGET_SIZES) {
    const span = spanOf(size);
    if (span.cols === cols && span.rows === rows) return size;
  }
  return null;
}

/** What a resize announces: the preset's word where one matches, the bare arithmetic where none does. */
export function spanTag(cols: number, rows: number): string {
  const preset = sizeForSpan(cols, rows);
  return preset ? sizeWord(preset) : `${cols}\u00d7${rows}`;
}

/**
 * One step bigger or smaller THROUGH THE FIVE PRESETS, clamped, never wrapped.
 * A card at a free span returns to a preset: that is the exit from a free span
 * for somebody who never touches the grip.
 */
export function nextSize(size: string, step: number): string {
  const here = WIDGET_SIZES.indexOf(size);
  const from = here < 0 ? 0 : here;
  const to = Math.max(0, Math.min(WIDGET_SIZES.length - 1, from + step));
  return WIDGET_SIZES[to];
}

// -- the working set -------------------------------------------------------------------------------------

/** The index of a widget by key, or -1. */
export function indexOfKey(views: readonly { widget_key: string }[], key: string): number {
  return views.findIndex((w) => w.widget_key === key);
}

/**
 * Move the widget at `from` so it sits at `to`. A `to` past either end is
 * CLAMPED rather than refused: a drag that ends past the end of the list is a
 * real thing people do, and it means "put it last". A `from` that names
 * nothing moves nothing, never throws.
 */
export function moveTo<T extends { widget_key: string }>(views: readonly T[], from: number, to: number): T[] {
  const out = [...views];
  if (from < 0 || from >= out.length) return out;
  const clamped = Math.max(0, Math.min(out.length - 1, to));
  if (from === clamped) return out;
  const [it] = out.splice(from, 1);
  out.splice(clamped, 0, it);
  return out;
}

/** One place left or right by key, clamped at the ends. */
export function moveBy<T extends { widget_key: string }>(views: readonly T[], key: string, delta: number): T[] {
  const i = indexOfKey(views, key);
  if (i < 0) return [...views];
  return moveTo(views, i, Math.max(0, Math.min(views.length - 1, i + delta)));
}

/**
 * Set one widget's span. A span that IS a preset is stored as the preset (and
 * clears the free pair, because choosing a name is how a person leaves a free
 * span); a pair the grid does not offer is refused here, the same ruling the
 * server makes with a 422, so the save never carries a span that would come
 * back refused.
 */
export function respanTo<T extends BuilderWidgetRow>(views: readonly T[], key: string, cols: number, rows: number): T[] {
  if (!isBentoSpan(cols, rows)) return [...views];
  const preset = sizeForSpan(cols, rows);
  if (preset === null) return views.map((w) => (w.widget_key === key ? { ...w, cols, rows } : w));
  return views.map((w): T => {
    if (w.widget_key !== key) return w;
    // The span is rewritten here rather than waited for: the grid draws from
    // span_cols/span_rows, and a resize that left the old span in place would
    // move the card back one frame later.
    const { cols: _c, rows: _r, ...rest } = w;
    return { ...rest, size: preset, span_cols: cols, span_rows: rows } as unknown as T;
  });
}

/** The ONE reserved measure: the role's needs-you queue, the widget a person may never end up without. */
export const MEASURE_NEEDS_YOU = "needs.you";

export function isNeedsYou(w: Pick<BuilderWidgetRow, "measure">): boolean {
  return w.measure === MEASURE_NEEDS_YOU;
}

/** Every widget may be removed except the queue that earns the page. */
export function canRemove(w: Pick<BuilderWidgetRow, "measure">): boolean {
  return !isNeedsYou(w);
}

/** Remove one widget. The queue refuses, and nothing reorders. */
export function removeKey<T extends BuilderWidgetRow>(views: readonly T[], key: string): T[] {
  const w = views.find((v) => v.widget_key === key);
  if (!w || !canRemove(w)) return [...views];
  return views.filter((v) => v.widget_key !== key);
}

/** A gallery placement lands at position 0: the newest thing is the first thing. */
export function placeAtTop<T extends BuilderWidgetRow>(views: readonly T[], w: T): T[] {
  return [w, ...views.filter((v) => v.widget_key !== w.widget_key)];
}

/**
 * The PUT body: the WHOLE layout, renumbered from the array order, with the
 * view-only fields dropped. WHOLE OR ABSENT is the substrate's rule, so every
 * change (a move, a resize, a remove, a place) sends the same shape. The free
 * pair travels only when there is one, and so does a branch: absent is the
 * honest shape for "every branch I can see".
 */
export function layoutBody(views: readonly BuilderWidgetRow[]): BuilderWidgetRow[] {
  return views.map((w, i) => {
    let row: BuilderWidgetRow = {
      widget_key: w.widget_key,
      shape: w.shape,
      measure: w.measure,
      split: w.split,
      range: w.range,
      size: w.size,
      title: w.title,
      position: i,
      visible: w.visible,
    };
    // Absent is the honest shape for "every branch I can see"; the free pair
    // travels only when there is one.
    if (w.branch_id !== undefined && w.branch_id !== "") row = { ...row, branch_id: w.branch_id };
    if (w.cols !== undefined && w.rows !== undefined) row = { ...row, cols: w.cols, rows: w.rows };
    return row;
  });
}

/** True when two layouts differ in order, size, free span, title, shape, split, window or branch. */
export function layoutChanged(before: readonly BuilderWidgetRow[], after: readonly BuilderWidgetRow[]): boolean {
  const sig = (rows: readonly BuilderWidgetRow[]) =>
    rows
      .map(
        (w) =>
          `${w.widget_key}:${w.size}:${w.cols ?? ""}x${w.rows ?? ""}:${w.title}:${w.shape}:${w.split}:${w.range}:${w.branch_id ?? ""}`,
      )
      .join(" ");
  return sig(before) !== sig(after);
}

// -- the builder form -------------------------------------------------------------------------------------

/** What the form holds while somebody is filling it in. */
export interface WidgetDraft {
  shape: string;
  measure: string;
  split: string;
  branch_id: string;
  range: string;
  size: string;
  title: string;
}

/** The splits THIS measure supports, "none" first, defended against an entry without any. */
export function splitsFor(m: Pick<BuilderMeasure, "splits"> | undefined): string[] {
  const rows = m !== undefined && m.splits.length > 0 ? [...m.splits] : ["none"];
  return ["none", ...rows.filter((s) => s !== "none")];
}

/** The body forms THIS measure supports, in the measure's own order, defended to the stat floor. */
export function shapesFor(m: Pick<BuilderMeasure, "widget_shapes"> | undefined): string[] {
  return m !== undefined && m.widget_shapes.length > 0 ? [...m.widget_shapes] : ["stat"];
}

/** The windows THIS measure can be asked for, falling back to the substrate's flat set. */
export function rangesFor(m: Pick<BuilderMeasure, "ranges"> | undefined, fallback: readonly string[] = []): string[] {
  if (m !== undefined && m.ranges.length > 0) return [...m.ranges];
  return fallback.length > 0 ? [...fallback] : ["open"];
}

/** The title a widget shows when nobody typed one: the measure's label, with the split as words. */
export function autoTitle(label: string, split: string): string {
  if (label === "") return "";
  if (split === "" || split === "none") return label;
  const suffix = splitWord(split);
  return suffix.toLowerCase() !== split ? `${label} ${suffix.toLowerCase()}` : label;
}

/** The title a card actually wears: the person's, else the derived one. */
export function widgetTitle(w: Pick<BuilderWidgetRow, "title"> & { label?: string; split?: string }): string {
  const typed = w.title.trim();
  if (typed !== "") return typed;
  return autoTitle(w.label ?? "", w.split ?? "") || w.label || "";
}

/** The title a gallery card wears, so the offer and the card it places cannot say different words. */
export function entryTitle(e: Pick<BuilderGalleryCard | BuilderGalleryEntryRow, "title"> & { label?: string; split?: string }): string {
  return widgetTitle(e);
}

/**
 * A draft, corrected against the catalog it must live inside. A measure
 * outside the caller's catalog never appears: the function can only return a
 * measure it FOUND in the list it was handed, and everything else follows from
 * the measure, so the form can never hold a combination the server would
 * refuse with a 422.
 */
export function reconcileDraft(draft: WidgetDraft, catalog: readonly BuilderMeasure[], fallbackRanges: readonly string[]): WidgetDraft {
  const m = catalog.find((x) => x.metric_id === draft.measure) ?? catalog[0];
  if (m === undefined) return { ...draft, measure: "", split: "none" };
  const splits = splitsFor(m);
  const shapes = shapesFor(m);
  const okRanges = rangesFor(m, fallbackRanges);
  return {
    shape: shapes.includes(draft.shape) ? draft.shape : shapes[0],
    measure: m.metric_id,
    split: splits.includes(draft.split) ? draft.split : "none",
    branch_id: draft.branch_id,
    range: okRanges.includes(draft.range) ? draft.range : okRanges[0],
    size: WIDGET_SIZES.includes(draft.size) ? draft.size : DEFAULT_SIZE,
    title: draft.title,
  };
}

/** The draft the form opens on for a brand-new widget. */
export function draftFromCatalog(catalog: readonly BuilderMeasure[], fallbackRanges: readonly string[]): WidgetDraft {
  return reconcileDraft(
    {
      shape: "stat",
      measure: catalog[0]?.metric_id ?? "",
      split: "none",
      branch_id: "",
      range: fallbackRanges[0] ?? "open",
      size: DEFAULT_SIZE,
      title: "",
    },
    catalog,
    fallbackRanges,
  );
}

/** The draft the form opens on for a widget that already exists: the widget exactly as it stands. */
export function draftFromWidget(w: Pick<BuilderWidgetRow, "shape" | "measure" | "split" | "range" | "size" | "title"> & { branch_id?: string }): WidgetDraft {
  return {
    shape: w.shape,
    measure: w.measure,
    split: w.split,
    branch_id: w.branch_id ?? "",
    range: w.range,
    size: w.size,
    title: w.title,
  };
}

/**
 * A key for a new widget, inside the core's `^[a-z0-9][a-z0-9._-]{0,63}$`
 * alphabet, derived from the measure and disambiguated with the SMALLEST
 * unused ordinal: a number a person may read should count the things they
 * have, not the milliseconds since 1970.
 */
export function nextWidgetKey(measure: string, taken: readonly string[]): string {
  const base =
    measure
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9._-]+/g, "-")
      .replace(/^[^a-z0-9]+/, "")
      .replace(/-+$/, "")
      .slice(0, 48) || "widget";
  const used = new Set(taken);
  if (!used.has(base)) return base;
  let n = 2;
  while (used.has(`${base}-${n}`)) n++;
  return `${base}-${n}`;
}

/** Turn a finished draft into a drawable widget, resolved against the measure it names. */
export function widgetFromDraft(draft: WidgetDraft, m: BuilderMeasure, key: string, position: number): BuilderWidgetView {
  const span = spanOf(draft.size);
  const w: BuilderWidgetView = {
    widget_key: key,
    shape: draft.shape,
    measure: m.metric_id,
    split: draft.split,
    branch_id: draft.branch_id,
    range: draft.range,
    size: draft.size,
    title: draft.title.trim(),
    position,
    visible: true,
    label: m.label,
    kind: m.kind,
    module: m.module,
    unit: m.unit,
    screen: m.screen,
    span_cols: span.cols,
    span_rows: span.rows,
  };
  return w;
}

/**
 * The widget a gallery entry becomes when somebody taps it. It does not
 * consult the catalog: an entry ARRIVED RESOLVED (label, kind, module, span),
 * because the server had to resolve them anyway to draw the preview honestly.
 * The free pair rides through as a pair or not at all.
 */
export function widgetFromEntry(e: BuilderGalleryCard, key: string, position: number): BuilderWidgetView {
  const w: BuilderWidgetView = {
    widget_key: key,
    shape: e.shape,
    measure: e.measure,
    split: e.split,
    branch_id: "",
    range: e.range,
    size: e.size,
    title: e.title.trim(),
    position,
    visible: true,
    label: e.label,
    kind: e.kind,
    module: e.module,
    unit: e.unit,
    screen: e.screen,
    span_cols: e.span_cols,
    span_rows: e.span_rows,
  };
  return e.cols !== undefined && e.rows !== undefined ? { ...w, cols: e.cols, rows: e.rows } : w;
}

// -- measurement: the one composed read that can preview a draft ------------------------------------------

/**
 * The composed viz id that measures this widget, or null when nothing can.
 *
 * The layout payload carries no numbers; the series come from the viz route,
 * keyed by the composed id `viz.<measure>-<dimension>-<shape>`. A measure with
 * no dimensions or no shapes cannot be composed at all, and the honest answer
 * is null: the preview renders its empty state, never a zero.
 */
export function vizIdFor(w: Pick<BuilderWidgetRow, "measure" | "split">, m: Pick<BuilderMeasure, "metric_id" | "dimensions" | "shapes"> | undefined): string | null {
  if (m === undefined || m.dimensions.length === 0 || m.shapes.length === 0) return null;
  const dim = w.split !== "" && w.split !== "none" && m.dimensions.includes(w.split) ? w.split : m.dimensions[0];
  return `viz.${m.metric_id}-${dim}-${m.shapes[0]}`;
}

/** Every viz id the working set needs, de-duplicated, in layout order. */
export function vizIdsFor(views: readonly (Pick<BuilderWidgetRow, "measure" | "split">)[], catalog: readonly BuilderMeasure[]): string[] {
  const out: string[] = [];
  for (const w of views) {
    const id = vizIdFor(w, catalog.find((m) => m.metric_id === w.measure));
    if (id !== null && !out.includes(id)) out.push(id);
  }
  return out;
}

// -- pinned tabs ------------------------------------------------------------------------------------------

/** How many pinned tabs the strip shows before folding the rest: six pins plus Home is one pass. */
export const MAX_PINNED_TABS = 6;

/**
 * Pin or unpin one surface, returning the WHOLE next set, because that is
 * what a save submits. An unpin KEEPS THE ROW at visible false: dropping it
 * would make a deliberately empty strip read as "use the role default", and
 * re-pinning would send the tab to the end instead of back where it was. A pin
 * the set has never heard of is appended: new pins land on the right.
 */
export function applyPin(pins: readonly BuilderPin[], key: string, pinned: boolean): BuilderPin[] {
  const ordered = [...pins].sort((a, b) => a.position - b.position);
  const found = ordered.some((p) => p.tab_key === key);
  const next = ordered.map((p) => (p.tab_key === key ? { ...p, visible: pinned } : { ...p }));
  if (!found && pinned) next.push({ tab_key: key, position: next.length, visible: true });
  return next.map((p, i) => ({ ...p, position: i }));
}

const renumber = (pins: readonly BuilderPin[]): BuilderPin[] => pins.map((p, i) => ({ ...p, position: i }));

/** Move one pin by delta, clamped at the ends; an unknown key renumbers and changes nothing. */
export function movePin(pins: readonly BuilderPin[], key: string, delta: number): BuilderPin[] {
  const ordered = [...pins].sort((a, b) => a.position - b.position);
  const i = ordered.findIndex((p) => p.tab_key === key);
  if (i < 0) return renumber(ordered);
  const clamped = Math.max(0, Math.min(ordered.length - 1, i + delta));
  if (clamped === i) return renumber(ordered);
  const next = [...ordered];
  const [it] = next.splice(i, 1);
  next.splice(clamped, 0, it);
  return renumber(next);
}

/** Swap two pins the caller names. An unknown pair is renumbered and left alone. */
export function swapTo(pins: readonly BuilderPin[], keyA: string, keyB: string): BuilderPin[] {
  const ordered = [...pins].sort((a, b) => a.position - b.position);
  const ia = ordered.findIndex((p) => p.tab_key === keyA);
  const ib = ordered.findIndex((p) => p.tab_key === keyB);
  if (ia < 0 || ib < 0 || ia === ib) return renumber(ordered);
  const next = [...ordered];
  [next[ia], next[ib]] = [next[ib], next[ia]];
  return renumber(next);
}

/** A stored pin key as a person reads it: the separators opened out, nothing invented. */
export function pinWord(key: string): string {
  return key.trim().split(/[/_.-]+/).filter((part) => part !== "").join(", ");
}

// -- the classic tile editor ------------------------------------------------------------------------------

/** One editor row: a catalog tile with its current place in the layout being edited. */
export interface TileEditorRow {
  readonly tile_id: string;
  readonly label: string;
  readonly module: string;
  readonly lead: string;
  readonly wired: boolean;
  readonly visible: boolean;
}

export function toTileEditorRow(raw: unknown): BuilderTileRow {
  const r = raw as Record<string, unknown>;
  return {
    tile_id: typeof r.tile_id === "string" ? r.tile_id : "",
    position: typeof r.position === "number" && Number.isFinite(r.position) ? r.position : 0,
    visible: r.visible === true,
  };
}

/**
 * Merges the served layout with the catalog into editor rows. The catalog is
 * the source of the LABELS and the wired flag; the layout is the source of
 * ORDER and VISIBILITY. A catalog tile the layout never mentions still appears
 * (hidden, at the end) so an admin can switch on a tile nobody has placed
 * without anybody running SQL.
 */
export function buildTileEditorRows(
  catalog: readonly BuilderTileCatalogEntry[],
  layout: readonly BuilderTileRow[],
): TileEditorRow[] {
  const byID = new Map(catalog.map((c) => [c.tile_id, c]));
  const rows: TileEditorRow[] = [];
  const seen = new Set<string>();
  for (const l of [...layout].sort((a, b) => a.position - b.position)) {
    const c = byID.get(l.tile_id);
    if (c === undefined || seen.has(l.tile_id)) continue;
    seen.add(l.tile_id);
    rows.push({ tile_id: c.tile_id, label: c.label, module: c.module, lead: c.lead, wired: c.wired, visible: l.visible });
  }
  for (const c of catalog) {
    if (seen.has(c.tile_id)) continue;
    seen.add(c.tile_id);
    rows.push({ tile_id: c.tile_id, label: c.label, module: c.module, lead: c.lead, wired: c.wired, visible: false });
  }
  return rows;
}

/** The classic layout body a save PUTs: the whole list, renumbered from the array order. */
export function tileLayoutBody(rows: readonly TileEditorRow[]): BuilderTileRow[] {
  return rows.map((r, i) => ({ tile_id: r.tile_id, position: i, visible: r.visible }));
}
