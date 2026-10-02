// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-panels.ts; adapted for HH.

// _pos-panels.ts: where the counter's four panels sit when a person has arranged them, and the
// rules that keep every one of them reachable. Pure: no DOM, no window, no storage side effects
// above the divider.
//
// ── WHY THIS IS A MODULE AND NOT THIRTY LINES INSIDE THE ELEMENT ────────────────────────────────
//
// It is arithmetic over four numbers and a box, and it is the kind of arithmetic that is wrong in
// one direction only: a panel that ends up past the edge is a panel nobody can drag back, and on a
// counter that means a clerk who cannot reach Payment with a customer in front of them. That is
// worth testing in node against invented viewports rather than discovering at a till. The element
// keeps the pointer plumbing; every decision about where a box is ALLOWED to be lives here.
//
// This is `_tanya-geo.ts` generalised from one floating window to four tiled ones, and the two
// files agree on the one rule that matters: a panel's own header band stays inside the box, always,
// because the header is the handle.
//
// ── WHY THE GEOMETRY IS PER DEVICE AND THE CHOICE IS PER PERSON ─────────────────────────────────
//
// The layout CHOICE (stepper or panels) rides the pos_user_workspace row and follows a person
// across tills: that is the 0166 ruling and 0220 extends it. These COORDINATES do not, and must
// not: they are measured in the pixels of the screen they were arranged on, and a counter person
// moves between a 27-inch till and a back-office laptop. Carrying x=1400 from the first to the
// second puts a panel off the side of it. So the boxes live in the host's store beside the floating
// assistant's, and every read is clamped to the box it is being read INTO.

import type { FlowStepId } from './_pos-flow.js';
import { clearPanelLayoutRaw, readPanelLayoutRaw, writePanelLayoutRaw } from './ports.js';

/** One panel's box, in pixels relative to the panel area's top-left. */
export interface PanelBox {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** The area the panels live in: the pane's own content box, not the viewport. */
export interface PanelArea {
  w: number;
  h: number;
}

/** Which edges a resize gesture is pulling. A move is the empty set. */
export interface PanelEdges {
  left?: boolean;
  right?: boolean;
  top?: boolean;
  bottom?: boolean;
}

/** Every panel's box, by step. A layout is always total over the four steps. */
export type PanelLayout = Record<FlowStepId, PanelBox>;

/** The four, in the order the sale happens: the order they tile into and the order they tab in. */
export const PANEL_ORDER: readonly FlowStepId[] = ['customer', 'items', 'payment', 'summary'];

/**
 * The smallest a panel may become, and neither number is an aesthetic floor.
 *
 * 280 is the width at which the Items table stops wrapping a product name onto three lines, and 140
 * is the height that still shows a panel's header plus one row of its content: which is the least
 * that is still a panel rather than a title bar somebody has to drag back open.
 */
export const PANEL_MIN_W = 280;
export const PANEL_MIN_H = 140;

/** A panel's own header band: the grab handle, and therefore the thing that may never leave. */
export const PANEL_HEAD_H = 34;

/**
 * How much of a panel must stay inside the area.
 *
 * Enough of the header to aim a pointer at, horizontally; the whole header band, vertically. A
 * panel dragged to x = -300 would leave its header off the left edge with nothing to grab, and the
 * only way back would be Reset layout: which exists, but should be for changing your mind, not
 * for undoing a slip.
 */
export const PANEL_KEEP_X = 120;

/** One arrow-key press moves or resizes by this much; with Shift, by the coarse step. */
export const PANEL_STEP = 8;
export const PANEL_STEP_COARSE = 40;

/** The gap the default tiling leaves between panels and around the edges. */
const GAP = 10;

/**
 * defaultLayout: the 2×2 a person lands on the first time they switch to panels.
 *
 * DEFAULTS DO THE WORK: switching layouts should show four usable panels, not four boxes stacked at
 * the origin waiting to be dragged apart. The order is the order the sale happens: Customer and
 * Items on top where the work starts, Payment and Summary beneath: so the reading order, the tab
 * order and the flow order are the same thing.
 *
 * On a NARROW area the 2×2 becomes a single column, because two 280px panels plus the gaps do not
 * fit under about 590px and a tiling that overlaps itself is not a default, it is a bug with a
 * plausible cause. The column is taller than the area on purpose: the panel surface scrolls.
 */
export function defaultLayout(area: PanelArea): PanelLayout {
  const usableW = Math.max(PANEL_MIN_W, area.w - GAP * 2);
  const twoUp = usableW >= PANEL_MIN_W * 2 + GAP;
  if (!twoUp) {
    const h = Math.max(PANEL_MIN_H, Math.round((Math.max(area.h, PANEL_MIN_H * 4) - GAP * 5) / 4));
    const out = {} as PanelLayout;
    PANEL_ORDER.forEach((id, i) => {
      out[id] = { x: GAP, y: GAP + i * (h + GAP), w: usableW, h };
    });
    return out;
  }
  const colW = Math.floor((usableW - GAP) / 2);
  const usableH = Math.max(PANEL_MIN_H * 2 + GAP, area.h - GAP * 2);
  const rowH = Math.floor((usableH - GAP) / 2);
  const col = (i: number) => GAP + (i % 2) * (colW + GAP);
  const row = (i: number) => GAP + Math.floor(i / 2) * (rowH + GAP);
  const out = {} as PanelLayout;
  PANEL_ORDER.forEach((id, i) => {
    out[id] = { x: col(i), y: row(i), w: colW, h: rowH };
  });
  return out;
}

/**
 * clampBox: the rule the whole file exists for.
 *
 * Size is floored first and then the position is brought back, in that order: clamping position
 * against a width that is about to change gives an answer for a box that never existed. A panel may
 * hang off the RIGHT and the BOTTOM (the surface scrolls, and a person dragging a panel to the edge
 * of a big layout means it), but never so far that less than PANEL_KEEP_X of it is still in, and
 * never above the top: a header at y = -20 cannot be grabbed.
 */
export function clampBox(box: PanelBox, area: PanelArea): PanelBox {
  const w = Math.max(PANEL_MIN_W, Math.round(box.w));
  const h = Math.max(PANEL_MIN_H, Math.round(box.h));
  const maxX = Math.max(0, area.w - PANEL_KEEP_X);
  const minX = Math.min(0, PANEL_KEEP_X - w);
  const x = Math.min(maxX, Math.max(minX, Math.round(box.x)));
  const maxY = Math.max(0, area.h - PANEL_HEAD_H);
  const y = Math.min(maxY, Math.max(0, Math.round(box.y)));
  return { x, y, w, h };
}

/** moveBox: a drag, clamped. dx/dy are the pointer's travel since the gesture started. */
export function moveBox(from: PanelBox, dx: number, dy: number, area: PanelArea): PanelBox {
  return clampBox({ ...from, x: from.x + dx, y: from.y + dy }, area);
}

/**
 * resizeBox: a pull on one or two edges, clamped.
 *
 * A left or top edge moves the origin AND the size, and the two have to stay consistent when the
 * size hits its floor: dragging the left edge right past the minimum must stop the edge, not carry
 * on moving the panel's x while the width sits pinned at 280. So each axis computes the far edge
 * first, floors the near edge against it, and derives the size from the pair.
 */
export function resizeBox(from: PanelBox, dx: number, dy: number, edges: PanelEdges, area: PanelArea): PanelBox {
  let { x, y, w, h } = from;
  if (edges.right) w = from.w + dx;
  if (edges.left) {
    const right = from.x + from.w;
    x = Math.min(from.x + dx, right - PANEL_MIN_W);
    w = right - x;
  }
  if (edges.bottom) h = from.h + dy;
  if (edges.top) {
    const bottom = from.y + from.h;
    y = Math.min(from.y + dy, bottom - PANEL_MIN_H);
    h = bottom - y;
  }
  return clampBox({ x, y, w, h }, area);
}

/**
 * normalizeLayout: a stored layout, made safe for the area it is about to be painted into.
 *
 * Every box is clamped, and a panel the stored layout has never heard of (a build that adds a fifth
 * step, a truncated write, somebody's hand-edited store entry) takes its box from the default
 * tiling rather than from nothing. The result is always total over the four steps, which is what
 * lets the caller index it without a guard at every use.
 */
export function normalizeLayout(stored: Partial<PanelLayout> | null | undefined, area: PanelArea): PanelLayout {
  const fallback = defaultLayout(area);
  const out = {} as PanelLayout;
  for (const id of PANEL_ORDER) {
    const box = stored && stored[id];
    out[id] = isBox(box) ? clampBox(box, area) : fallback[id];
  }
  return out;
}

function isBox(v: unknown): v is PanelBox {
  if (!v || typeof v !== 'object') return false;
  const b = v as Record<string, unknown>;
  return ['x', 'y', 'w', 'h'].every((k) => typeof b[k] === 'number' && Number.isFinite(b[k] as number));
}

/**
 * bringToFront: the stacking order, which is a list and not a z-index on each box.
 *
 * Kept as an ORDER rather than a number per panel because a number per panel is a thing that drifts:
 * two panels end up sharing a z-index, or the numbers climb every click until somebody wonders why
 * a panel is at 40,000. A list of four ids cannot do either.
 */
export function bringToFront(order: readonly FlowStepId[], id: FlowStepId): FlowStepId[] {
  const rest = order.filter((p) => p !== id);
  return [...rest, id];
}

/** normalizeOrder: a stored stack order, made total and deduplicated, in PANEL_ORDER for the rest. */
export function normalizeOrder(stored: unknown): FlowStepId[] {
  const seen = new Set<FlowStepId>();
  const out: FlowStepId[] = [];
  if (Array.isArray(stored)) {
    for (const v of stored) {
      if (typeof v === 'string' && (PANEL_ORDER as readonly string[]).includes(v) && !seen.has(v as FlowStepId)) {
        seen.add(v as FlowStepId);
        out.push(v as FlowStepId);
      }
    }
  }
  for (const id of PANEL_ORDER) if (!seen.has(id)) out.push(id);
  return out;
}

/** What one device remembers: where the boxes are and which one is on top. */
export interface StoredPanels {
  boxes: Partial<PanelLayout>;
  order: FlowStepId[];
}

// ── side effects (host only; never executed at import, so this module stays test-loadable) ──────

/**
 * The key. Per device, never per account: see this file's header for the argument.
 *
 * Versioned in the name rather than inside the value: if the shape of a stored layout ever has to
 * change, the old key is simply not read, and a person gets the default tiling back instead of a
 * half-understood box. That is cheaper than a migration for something worth this little.
 */
export const PANELS_KEY = 'hos-pos-panels-v1';

/** readPanels: whatever this device remembers, or null.
 * HH note: the reference read the browser's local store here and caught everything; the seam
 * call stands OUTSIDE the catch on purpose, so an unwired host refuses loudly (the port
 * message, ports.ts), and the catch answers null only for a store entry the parser cannot
 * read: a hand-edited or half-written layout takes the default tiling, as the reference's own
 * comment promised (ports.ts, named in CLOSURE.md). */
export function readPanels(): StoredPanels | null {
  const raw = readPanelLayoutRaw(PANELS_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as { boxes?: unknown; order?: unknown };
    if (!parsed || typeof parsed !== 'object') return null;
    return {
      boxes: (parsed.boxes ?? {}) as Partial<PanelLayout>,
      order: normalizeOrder(parsed.order),
    };
  } catch {
    return null;
  }
}

/** writePanels: persist the layout under the versioned key.
 * HH note: no catch here. The reference swallowed store failures because its own store could
 * throw; the seam's contract is to refuse loudly until wired (ports.ts), and whether a
 * refusing store is fatal or best effort is the host implementation's decision, not this
 * module's silence. */
export function writePanels(v: StoredPanels): void {
  writePanelLayoutRaw(PANELS_KEY, JSON.stringify(v));
}

/** clearPanels: Reset layout. The next read returns the default tiling for the current area.
 * HH note: unguarded, for the same ground as writePanels. */
export function clearPanels(): void {
  clearPanelLayoutRaw(PANELS_KEY);
}
