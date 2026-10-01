// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-workspace.ts; adapted for HH.

// _pos-workspace.ts: DIB-488 rev 2: the POS two-panel workspace's geometry and rules, pure.
//
// The counter page is two PEER panels: INVENTORY LOOKUP (search/scan/browse, used constantly,
// often without ever adding to a sale) and THE SALE (the tracked 1→2→3 flow): separated by a
// draggable, keyboard-operable divider. Everything that has to be provably right about that
// arrangement lives here, DOM-free, per the `_*.ts` house convention (_pos-flow.ts, _pos.ts):
//
// geometry the split is ONE number: the LOOKUP panel's percent share of the workspace,
// side-independent, clamped to 20-80. Swapping sides never renumbers the split;
// only the pointer/arrow→percent conversions care which side the lookup panel is on.
// rules maximize wins over collapse and restores the panel it maximizes; at least one
// panel is always open (collapsing the only open panel is refused); collapsing a
// maximized panel first surrenders the maximize.
// resolve dealer defaults vs the per-user override, WHOLE OR ABSENT (the dashboard
// user-tiles/user-tabs ruling): a stored user row IS the workspace; reset = DELETE.
//
// PERSISTENCE RULING (0166; mirrored in backend/internal/pos/workspace.go):
// dealer (pos_policy) default split + default arrangement: "configure once at
// dealer level", written only through the admin policy PUT.
// user (pos_user_workspace) split / collapse / swap: one person's ergonomics, following
// them across tills.
// never maximize: a temporary gesture, session-only by design.

export const SPLIT_MIN = 20;
export const SPLIT_MAX = 80;
export const SPLIT_DEFAULT = 55;
/** One arrow-key press moves the divider this many percent; PageUp/Down takes the coarse step. */
export const SPLIT_STEP = 2;
export const SPLIT_STEP_COARSE = 10;

export type PanelId = 'lookup' | 'sale';
export type Arrangement = 'LOOKUP_LEFT' | 'SALE_LEFT';
/**
 * DIB-488 rev 3 (0167): the INVENTORY-LOOKUP results' presentation: the client's "toggle between
 * the current card view and more of a listed row view". Same data, same behaviors (search, chips,
 * scan feedback, click-to-price-&-add); only the shape changes. CARDS (the shipped grid) is the
 * default; the preference follows the rev-2 persistence ruling exactly: dealer default on
 * pos_policy, per-user override riding the SAME whole-or-absent pos_user_workspace row.
 */
export type LookupPresentation = 'CARDS' | 'ROWS';

/**
 * 0220: the counter's LAYOUT. STEPPER is the four-step single column: the standard counter, the
 * shipped default at every level, and what anybody who never opens the view menu keeps. PANELS
 * opens all four at once as boxes a person sizes and places.
 *
 * WHERE THE PANELS ENDED UP IS NOT HERE. A panel's x/y/width/height is measured in the pixels of
 * the screen it was arranged on, and a counter person moves between a 27-inch till and a back-office
 * laptop; carrying coordinates between the two puts a panel off the bottom of one of them. So the
 * geometry is per DEVICE (`_pos-panels.ts`, the floating-assistant precedent) and only the CHOICE:
 * which is about the person, not the glass: rides this row across tills.
 */
export type CounterLayout = 'STEPPER' | 'PANELS';

/** The whole workspace view state the terminal renders from (maximized is transient: see above). */
export interface WorkspaceView {
 /** The LOOKUP panel's percent share (20-80), side-independent. */
  split: number;
  arrangement: Arrangement;
  lookupCollapsed: boolean;
  saleCollapsed: boolean;
 /** rev 3: how the lookup panel's results paint: CARDS (grid) or ROWS (dense list). */
  lookupPresentation: LookupPresentation;
 /**
 * rev 8 (0170): the margin quick-hide (eye) toggle: counter privacy. Display-only (the ROLE
 * gate on the server decides whether margin data is ever sent); persists per user on the same
 * whole-or-absent row through the same merge-save contract.
 */
  marginHidden: boolean;
 /** 0220: STEPPER (the standard four-step column) | PANELS (all four open, arrangeable). */
  counterLayout: CounterLayout;
 /** '' = none. Never persisted. */
  maximized: '' | PanelId;
}

/** What the server resolves (GET /api/v1/pos/workspace): a subset of the view + its source. */
export interface WorkspacePrefsWire {
  split_pct?: unknown;
  arrangement?: unknown;
  lookup_collapsed?: unknown;
  sale_collapsed?: unknown;
  lookup_presentation?: unknown;
  margin_hidden?: unknown;
  counter_layout?: unknown;
  source?: unknown;
}

export function clampSplit(pct: number): number {
  if (!Number.isFinite(pct)) return SPLIT_DEFAULT;
  return Math.min(SPLIT_MAX, Math.max(SPLIT_MIN, Math.round(pct)));
}

/** The default view: what a till shows when nothing configured anything (empty-never-error). */
export function defaultView(): WorkspaceView {
  return {
    split: SPLIT_DEFAULT,
    arrangement: 'LOOKUP_LEFT',
    lookupCollapsed: false,
    saleCollapsed: false,
    lookupPresentation: 'CARDS',
    marginHidden: false,
    counterLayout: 'STEPPER',
    maximized: '',
  };
}

/**
 * resolveWorkspaceView: the resolved server answer (user row when one exists, else dealer
 * defaults; the server already applied whole-or-absent) hardened at the edge: a missing body or a
 * malformed field degrades to the shipped defaults, never to an error and never to an unusable 0%
 * panel. Maximize always starts '': it is not a preference.
 */
export function resolveWorkspaceView(wire: WorkspacePrefsWire | null | undefined): WorkspaceView {
  const v = defaultView();
  if (!wire || typeof wire !== 'object') return v;
  if (typeof wire.split_pct === 'number') v.split = clampSplit(wire.split_pct);
  if (wire.arrangement === 'SALE_LEFT') v.arrangement = 'SALE_LEFT';
  v.lookupCollapsed = wire.lookup_collapsed === true;
  v.saleCollapsed = wire.sale_collapsed === true;
 // rev 3: only the literal 'ROWS' flips the presentation: anything else (absent, garbage, an
 // unknown future value) stays on the shipped CARDS, never an error and never a blank panel.
  if (wire.lookup_presentation === 'ROWS') v.lookupPresentation = 'ROWS';
 // rev 8: only the literal true hides: absence/garbage keeps the margin surface visible (the
 // role gate server-side is what decides whether there is anything to see at all).
  v.marginHidden = wire.margin_hidden === true;
 // 0220: only the literal 'PANELS' leaves the standard counter. Absent (a pre-0220 row), garbage,
 // or an unknown future value all keep the four-step column the person is already looking at:
 // the same posture lookup_presentation takes, and for the same reason: a layout nobody chose is
 // not a layout to put somebody in.
  if (wire.counter_layout === 'PANELS') v.counterLayout = 'PANELS';
 // Both panels stored collapsed (a hand-written PUT could) would open on NOTHING: refuse the
 // combination in favour of the sale panel, the one with the money on it.
  if (v.lookupCollapsed && v.saleCollapsed) v.saleCollapsed = false;
  return v;
}

/** The PUT body (idempotency key added by the caller: minted once per save attempt). */
export function workspacePayload(v: WorkspaceView): {
  split_pct: number;
  arrangement: Arrangement;
  lookup_collapsed: boolean;
  sale_collapsed: boolean;
} {
  return {
    split_pct: clampSplit(v.split),
    arrangement: v.arrangement,
    lookup_collapsed: v.lookupCollapsed,
    sale_collapsed: v.saleCollapsed,
  };
}

/**
 * workspaceSaveBody: rev 3: the FULL save body, the geometry payload plus the lookup presentation
 * (0167 rides the same whole-or-absent row). A separate composer rather than a fifth field on
 * workspacePayload so the rev-2 payload contract stands untouched; maximize stays excluded by
 * construction (it is not in workspacePayload and not added here).
 */
export function workspaceSaveBody(v: WorkspaceView): {
  split_pct: number;
  arrangement: Arrangement;
  lookup_collapsed: boolean;
  sale_collapsed: boolean;
  lookup_presentation: LookupPresentation;
} {
  return { ...workspacePayload(v), lookup_presentation: v.lookupPresentation };
}

/**
 * workspaceSaveBodyV8: rev 8: the FULL save body, the rev-3 body plus margin_hidden (0170 rides
 * the same whole-or-absent row). A separate composer beside workspaceSaveBody for exactly the
 * reason workspaceSaveBody stands beside workspacePayload: the earlier contracts are pinned and
 * stand untouched; maximize stays excluded by construction.
 */
export function workspaceSaveBodyV8(v: WorkspaceView): {
  split_pct: number;
  arrangement: Arrangement;
  lookup_collapsed: boolean;
  sale_collapsed: boolean;
  lookup_presentation: LookupPresentation;
  margin_hidden: boolean;
} {
  return { ...workspaceSaveBody(v), margin_hidden: v.marginHidden };
}

/**
 * workspaceSaveBodyV9: 0220: the rev-8 body plus counter_layout, riding the same whole-or-absent
 * row. A separate composer beside workspaceSaveBodyV8 for exactly the reason that one stands beside
 * workspaceSaveBody: the earlier contracts are pinned by tests and stand untouched, and maximize
 * stays excluded by construction.
 */
export function workspaceSaveBodyV9(v: WorkspaceView): {
  split_pct: number;
  arrangement: Arrangement;
  lookup_collapsed: boolean;
  sale_collapsed: boolean;
  lookup_presentation: LookupPresentation;
  margin_hidden: boolean;
  counter_layout: CounterLayout;
} {
  return { ...workspaceSaveBodyV8(v), counter_layout: v.counterLayout };
}

// ── rev 6 (item G): merge-changed-fields before each debounced save ─────────────────────────────

/** The persisted view fields a gesture can change (maximize is never persisted). rev 8 adds marginHidden. */
export type WorkspaceField =
  | 'split'
  | 'arrangement'
  | 'lookupCollapsed'
  | 'saleCollapsed'
  | 'lookupPresentation'
  | 'marginHidden'
  | 'counterLayout';

export const WORKSPACE_FIELDS: readonly WorkspaceField[] = [
  'split',
  'arrangement',
  'lookupCollapsed',
  'saleCollapsed',
  'lookupPresentation',
  'marginHidden',
  'counterLayout',
];

/** changedWorkspaceFields: which persisted fields differ between two views (the gesture's diff). */
export function changedWorkspaceFields(before: WorkspaceView, after: WorkspaceView): WorkspaceField[] {
  return WORKSPACE_FIELDS.filter((f) => before[f] !== after[f]);
}

/**
 * mergeWorkspaceForSave: the rev-5 opinion-list #2 fix, ruled by the commander for rev 6: two live
 * surfaces used to clobber each other because every debounced save wrote the WHOLE local view
 * (whole-or-absent), flattening whatever the other till had saved meanwhile. Before each save the
 * terminal now RE-READS the stored row and overlays ONLY the fields this surface's gestures
 * actually changed: the base is the server's answer, the wire contract stays whole-or-absent (one
 * complete row is still PUT), and the server's whole-or-absent ruling is untouched.
 *
 * A failed/absent re-read degrades to the local view (`wire` null/undefined resolves to the
 * defaults, so the caller passes the LOCAL view's fields as all-changed in that case: see the
 * terminal). Both-collapsed is refused the same way resolveWorkspaceView refuses it; maximize is
 * excluded by construction (never merged, never sent).
 */
export function mergeWorkspaceForSave(
  wire: WorkspacePrefsWire | null | undefined,
  local: WorkspaceView,
  changed: ReadonlyArray<WorkspaceField> | ReadonlySet<WorkspaceField>,
): WorkspaceView {
  const base = resolveWorkspaceView(wire);
  const has = (f: WorkspaceField) => (changed instanceof Set ? changed.has(f) : (changed as readonly WorkspaceField[]).includes(f));
  const out: WorkspaceView = { ...base, maximized: '' };
  if (has('split')) out.split = clampSplit(local.split);
  if (has('arrangement')) out.arrangement = local.arrangement;
  if (has('lookupCollapsed')) out.lookupCollapsed = local.lookupCollapsed;
  if (has('saleCollapsed')) out.saleCollapsed = local.saleCollapsed;
  if (has('lookupPresentation')) out.lookupPresentation = local.lookupPresentation;
  if (has('marginHidden')) out.marginHidden = local.marginHidden; // rev 8: same merge discipline
  if (has('counterLayout')) out.counterLayout = local.counterLayout; // 0220: same merge discipline
  if (out.lookupCollapsed && out.saleCollapsed) out.saleCollapsed = false;
  return out;
}

// ── geometry: pointer and keyboard both speak "percent of the LEFT panel" ───────────────────────

/** Which panel is physically on the left under this arrangement. */
export function leftPanel(v: WorkspaceView): PanelId {
  return v.arrangement === 'SALE_LEFT' ? 'sale' : 'lookup';
}

/**
 * splitFromPointer: the divider is AT the pointer. Converts a pointer x inside the workspace box
 * into the lookup panel's percent share, honouring the arrangement: with the sale panel on the
 * left, the raw left-share belongs to the SALE panel, so the lookup share is its complement.
 * A degenerate box (width ≤ 0: mid-layout) returns null: no update rather than a wild one.
 */
export function splitFromPointer(pointerX: number, boxLeft: number, boxWidth: number, arrangement: Arrangement): number | null {
  if (!Number.isFinite(boxWidth) || boxWidth <= 0) return null;
  const rawLeftPct = ((pointerX - boxLeft) / boxWidth) * 100;
  return clampSplit(arrangement === 'SALE_LEFT' ? 100 - rawLeftPct : rawLeftPct);
}

/**
 * nudgeSplit: ArrowLeft/ArrowRight on the focused divider. The gesture is PHYSICAL: ArrowLeft
 * moves the divider left (the left panel shrinks) whichever panel is on the left: so under
 * SALE_LEFT an ArrowLeft GROWS the lookup share. `dir` is -1 (left) or +1 (right).
 */
export function nudgeSplit(v: WorkspaceView, dir: -1 | 1, coarse = false): number {
  const step = (coarse ? SPLIT_STEP_COARSE : SPLIT_STEP) * dir;
  const delta = leftPanel(v) === 'lookup' ? step : -step;
  return clampSplit(v.split + delta);
}

/** The separator's ARIA value facts: spoken as the position of the divider (percent from the left). */
export function splitAria(v: WorkspaceView): { now: number; min: number; max: number; text: string } {
  const leftShare = leftPanel(v) === 'lookup' ? v.split : 100 - v.split;
  return {
    now: leftShare,
    min: 100 - SPLIT_MAX, // the left panel can never drop below the complement of the other's cap
    max: SPLIT_MAX,
    text: `Inventory lookup ${v.split}%: Sale ${100 - v.split}%`,
  };
}

// ── the collapse / maximize rules ───────────────────────────────────────────────────────────────

/**
 * toggleCollapse: collapse a panel to its slim rail, or restore it. At least one panel is always
 * open: collapsing the only open panel restores the OTHER one in the same act (the operator asked
 * for this panel gone, not for an empty page). Collapsing a maximized panel surrenders the
 * maximize first: a collapsed maximum is a contradiction.
 */
export function toggleCollapse(v: WorkspaceView, panel: PanelId): WorkspaceView {
  const next: WorkspaceView = { ...v };
  if (next.maximized === panel) next.maximized = '';
  if (panel === 'lookup') {
    next.lookupCollapsed = !next.lookupCollapsed;
    if (next.lookupCollapsed && next.saleCollapsed) next.saleCollapsed = false;
  } else {
    next.saleCollapsed = !next.saleCollapsed;
    if (next.saleCollapsed && next.lookupCollapsed) next.lookupCollapsed = false;
  }
  return next;
}

/**
 * toggleMaximize: temporarily give a panel the whole workspace, or hand the space back.
 * Maximizing restores the panel's own collapse (you cannot look at a maximized rail); the OTHER
 * panel's collapse preference is untouched underneath, because maximize is a moment, not a layout.
 */
export function toggleMaximize(v: WorkspaceView, panel: PanelId): WorkspaceView {
  const next: WorkspaceView = { ...v };
  if (next.maximized === panel) {
    next.maximized = '';
    return next;
  }
  next.maximized = panel;
  if (panel === 'lookup') next.lookupCollapsed = false;
  else next.saleCollapsed = false;
  return next;
}

// ── layout resolution: view state → the grid the terminal paints ────────────────────────────────

export type PanelState = 'open' | 'rail' | 'hidden';

export interface WorkspaceLayout {
 /** Panel order, left to right (the divider renders between them when both are open). */
  order: [PanelId, PanelId];
  lookup: PanelState;
  sale: PanelState;
 /** Whether the divider participates at all (both panels open, neither maximized). */
  divider: boolean;
 /** The grid-template-columns value, in panel `order`, divider between when present. */
  gridColumns: string;
}

/** The slim rail's fixed track (px): wide enough for the restore control, nothing else. */
export const RAIL_PX = 46;

/**
 * resolveLayout: one honest answer for what the workspace shows. Maximize wins over everything;
 * a collapsed panel is a rail (still present: the way back is visible where the panel was);
 * otherwise both panels split the width at `split`, each track floored so a panel can never be
 * dragged into unusability (minmax floor + the 20-80 clamp belt-and-braces).
 */
export function resolveLayout(v: WorkspaceView): WorkspaceLayout {
  const order: [PanelId, PanelId] = leftPanel(v) === 'sale' ? ['sale', 'lookup'] : ['lookup', 'sale'];
  if (v.maximized !== '') {
    const lookup: PanelState = v.maximized === 'lookup' ? 'open' : 'hidden';
    const sale: PanelState = v.maximized === 'sale' ? 'open' : 'hidden';
    return { order, lookup, sale, divider: false, gridColumns: 'minmax(0, 1fr)' };
  }
  const state: Record<PanelId, PanelState> = {
    lookup: v.lookupCollapsed ? 'rail' : 'open',
    sale: v.saleCollapsed ? 'rail' : 'open',
  };
  const track = (p: PanelId): string => {
    if (state[p] === 'rail') return `${RAIL_PX}px`;
    const share = p === 'lookup' ? v.split : 100 - v.split;
    return `minmax(240px, ${share}fr)`;
  };
  if (state.lookup === 'rail' || state.sale === 'rail') {
 // One rail: the open panel takes everything left; no divider to drag against a rail.
    const cols = order.map((p) => (state[p] === 'rail' ? `${RAIL_PX}px` : 'minmax(0, 1fr)')).join(' ');
    return { order, lookup: state.lookup, sale: state.sale, divider: false, gridColumns: cols };
  }
  return {
    order,
    lookup: 'open',
    sale: 'open',
    divider: true,
    gridColumns: `${track(order[0])} auto ${track(order[1])}`,
  };
}
