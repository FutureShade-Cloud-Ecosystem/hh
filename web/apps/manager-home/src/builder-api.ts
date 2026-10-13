// The builder's one door to the core's dashboard routes: the reads and writes
// the arrange view makes, over the generated api-client, answering a Result
// and never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the arrange view uses. A field the
// projection does not name is dropped at this door and never reaches a
// screen. The server's colour and glyph fields are dropped on purpose: HH's
// screens paint from the design system's tokens, never from a payload value.
//
// Port provenance: the door's shape is the original app's dashboard-api.ts and
// home-bento-logic.ts read layer (repository hardscapeos_dibbits, read only;
// see PROVENANCE.md), rebuilt on HH's generated client and narrowed. WHOLE OR
// ABSENT stays the write rule it was there: every layout save PUTs the whole
// list, every reset is one DELETE that answers what the reset landed on.
import {
  ApiClient,
  staffDashboard,
  type DashboardGalleryEntry,
  type DashboardProfile,
  type DashboardWidget,
  type Result,
} from "@hardscapehouse/api-client";

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);
const maybeNum = (value: unknown): number | null => (typeof value === "number" && Number.isFinite(value) ? value : null);
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);
const words = (value: unknown): readonly string[] => list(value).filter((v): v is string => typeof v === "string");

// -- the widget bento --------------------------------------------------------------------------------

/** One stored widget row: the PUT body's row and the role defaults' row. */
export interface BuilderWidgetRow {
  readonly widget_key: string;
  readonly shape: string;
  readonly measure: string;
  readonly split: string;
  readonly branch_id: string;
  readonly range: string;
  readonly size: string;
  /** The free span, as a PAIR or not at all: absent means the preset decides. */
  readonly cols?: number;
  readonly rows?: number;
  readonly title: string;
  readonly position: number;
  readonly visible: boolean;
}

/** One resolved widget row: the stored row plus what drawing and deep-linking need. */
export interface BuilderWidgetView extends BuilderWidgetRow {
  readonly label: string;
  readonly kind: string;
  readonly module: string;
  readonly unit: string;
  readonly screen: string;
  readonly span_cols: number;
  readonly span_rows: number;
}

export interface BuilderLayout {
  readonly profile: string;
  readonly source: string;
  readonly widgets: readonly BuilderWidgetView[];
}

/** One measure the caller may build a widget from, with its own windows and forms. */
export interface BuilderMeasure {
  readonly metric_id: string;
  readonly label: string;
  readonly module: string;
  readonly kind: string;
  readonly dimensions: readonly string[];
  readonly shapes: readonly string[];
  readonly splits: readonly string[];
  readonly widget_shapes: readonly string[];
  readonly ranges: readonly string[];
  readonly unit: string;
  readonly screen: string;
  readonly lead: string;
}

export interface BuilderWidgetCatalog {
  readonly measures: readonly BuilderMeasure[];
  readonly shapes: readonly string[];
  readonly sizes: readonly string[];
  readonly spanCols: readonly number[];
  readonly maxRows: number;
  readonly splits: readonly string[];
  readonly ranges: readonly string[];
  readonly profile: string;
}

/** One gallery entry as the server resolves it for this caller: the words and the span ride on it. */
export interface BuilderGalleryCard {
  readonly entry_key: string;
  readonly shape: string;
  readonly measure: string;
  readonly split: string;
  readonly range: string;
  readonly size: string;
  readonly cols?: number;
  readonly rows?: number;
  readonly title: string;
  readonly blurb: string;
  readonly position: number;
  readonly visible: boolean;
  readonly label: string;
  readonly kind: string;
  readonly module: string;
  readonly unit: string;
  readonly screen: string;
  readonly span_cols: number;
  readonly span_rows: number;
}

export interface BuilderGallery {
  readonly profile: string;
  readonly entries: readonly BuilderGalleryCard[];
}

/** One gallery entry as a ROLE stores it: unresolved, because one row is read by many callers. */
export interface BuilderGalleryEntryRow {
  readonly entry_key: string;
  readonly shape: string;
  readonly measure: string;
  readonly split: string;
  readonly range: string;
  readonly size: string;
  readonly cols?: number;
  readonly rows?: number;
  readonly title: string;
  readonly blurb: string;
  readonly position: number;
  readonly visible: boolean;
}

// -- pinned tabs --------------------------------------------------------------------------------------

export interface BuilderPin {
  readonly tab_key: string;
  readonly position: number;
  readonly visible: boolean;
}

export interface BuilderTabs {
  readonly source: string;
  readonly pins: readonly BuilderPin[];
}

// -- the tile-era cockpit (the classic home the dashboard route still serves) --------------------------

export interface BuilderTileRow {
  readonly tile_id: string;
  readonly position: number;
  readonly visible: boolean;
}

export interface BuilderTileCatalogEntry {
  readonly tile_id: string;
  readonly label: string;
  readonly module: string;
  readonly default_visible: boolean;
  readonly lead: string;
  /** False when nothing is measuring the tile: the editor says so before anybody pins a dash. */
  readonly wired: boolean;
}

export interface BuilderTileCatalog {
  readonly tiles: readonly BuilderTileCatalogEntry[];
  readonly profiles: readonly string[];
  readonly profile: string;
}

export interface BuilderHomeConfig {
  readonly profile: string;
  readonly landing_unit: string;
  readonly landing_module: string;
  readonly takeover: boolean;
  readonly mobile_only: boolean;
}

export interface BuilderMyHome {
  readonly profile: string;
  readonly source: string;
  readonly tiles: readonly BuilderTileRow[];
  readonly home: BuilderHomeConfig;
  readonly tabs: BuilderTabs;
}

// -- the role defaults (admin) -------------------------------------------------------------------------

export interface BuilderProfileSummary {
  readonly profile: string;
  readonly home: BuilderHomeConfig;
}

export interface BuilderProfilesView {
  readonly profiles: readonly BuilderProfileSummary[];
}

export interface BuilderProfileDetail {
  readonly profile: string;
  readonly tiles: readonly BuilderTileRow[];
  readonly home: BuilderHomeConfig;
}

export interface BuilderProfileWidgets {
  readonly profile: string;
  readonly widgets: readonly BuilderWidgetRow[];
}

export interface BuilderProfileGallery {
  readonly profile: string;
  readonly entries: readonly BuilderGalleryEntryRow[];
}

// -- the composed-chart catalog and its series -----------------------------------------------------------

export interface BuilderVizPoint {
  readonly label: string;
  readonly number: number;
  readonly tone: string;
}

export interface BuilderVizTile {
  readonly tile_id: string;
  readonly label: string;
  readonly kind: string;
  readonly module: string;
  readonly state: string;
  readonly total: number | null;
  readonly points: readonly BuilderVizPoint[];
  readonly note: string;
}

// -- the projections -------------------------------------------------------------------------------------

export function toWidgetRow(raw: unknown): BuilderWidgetRow {
  const r = bag(raw);
  const row: BuilderWidgetRow = {
    widget_key: text(r.widget_key),
    shape: text(r.shape),
    measure: text(r.measure),
    split: text(r.split),
    branch_id: text(r.branch_id),
    range: text(r.range),
    size: text(r.size),
    title: text(r.title),
    position: num(r.position),
    visible: r.visible === true,
  };
  // THE PAIR TRAVELS AS A PAIR OR NOT AT ALL: a half pair is the omission an
  // older client makes, and the door hands the server "the preset decides"
  // rather than a half-fact.
  const cols = num(r.cols);
  const rows = num(r.rows);
  return cols > 0 && rows > 0 ? { ...row, cols, rows } : row;
}

export function toWidgetView(raw: unknown): BuilderWidgetView {
  const r = bag(raw);
  return {
    ...toWidgetRow(raw),
    label: text(r.label),
    kind: text(r.kind),
    module: text(r.module),
    unit: text(r.unit),
    screen: text(r.screen),
    span_cols: num(r.span_cols),
    span_rows: num(r.span_rows),
  };
}

export function toLayout(raw: unknown): BuilderLayout {
  const r = bag(raw);
  return {
    profile: text(r.profile),
    source: text(r.source),
    widgets: list(r.widgets).map(toWidgetView),
  };
}

export function toMeasure(raw: unknown): BuilderMeasure {
  const r = bag(raw);
  return {
    metric_id: text(r.metric_id),
    label: text(r.label),
    module: text(r.module),
    kind: text(r.kind),
    dimensions: words(r.dimensions),
    shapes: words(r.shapes),
    splits: words(r.splits),
    widget_shapes: words(r.widget_shapes),
    ranges: words(r.ranges),
    unit: text(r.unit),
    screen: text(r.screen),
    lead: text(r.lead),
  };
}

export function toWidgetCatalog(raw: unknown): BuilderWidgetCatalog {
  const r = bag(raw);
  return {
    measures: list(r.measures).map(toMeasure),
    shapes: words(r.shapes),
    sizes: words(r.sizes),
    spanCols: list(r.span_cols).map(num).filter((v) => v > 0),
    maxRows: num(r.max_rows),
    splits: words(r.splits),
    ranges: words(r.ranges),
    profile: text(r.profile),
  };
}

export function toGalleryCard(raw: unknown): BuilderGalleryCard {
  const r = bag(raw);
  const card: BuilderGalleryCard = {
    entry_key: text(r.entry_key),
    shape: text(r.shape),
    measure: text(r.measure),
    split: text(r.split),
    range: text(r.range),
    size: text(r.size),
    title: text(r.title),
    blurb: text(r.blurb),
    position: num(r.position),
    visible: r.visible === true,
    label: text(r.label),
    kind: text(r.kind),
    module: text(r.module),
    unit: text(r.unit),
    screen: text(r.screen),
    span_cols: num(r.span_cols),
    span_rows: num(r.span_rows),
  };
  const cols = num(r.cols);
  const rows = num(r.rows);
  return cols > 0 && rows > 0 ? { ...card, cols, rows } : card;
}

export function toGalleryEntryRow(raw: unknown): BuilderGalleryEntryRow {
  const r = bag(raw);
  const entry: BuilderGalleryEntryRow = {
    entry_key: text(r.entry_key),
    shape: text(r.shape),
    measure: text(r.measure),
    split: text(r.split),
    range: text(r.range),
    size: text(r.size),
    title: text(r.title),
    blurb: text(r.blurb),
    position: num(r.position),
    visible: r.visible === true,
  };
  const cols = num(r.cols);
  const rows = num(r.rows);
  return cols > 0 && rows > 0 ? { ...entry, cols, rows } : entry;
}

export function toGallery(raw: unknown): BuilderGallery {
  const r = bag(raw);
  return {
    profile: text(r.profile),
    entries: list(r.entries).map(toGalleryCard),
  };
}

export function toPin(raw: unknown): BuilderPin {
  const r = bag(raw);
  return {
    tab_key: text(r.tab_key),
    position: num(r.position),
    visible: r.visible === true,
  };
}

export function toTabStrip(raw: unknown): BuilderTabs {
  const r = bag(raw);
  return {
    source: text(r.source),
    pins: list(r.pins).map(toPin),
  };
}

export function toTileRow(raw: unknown): BuilderTileRow {
  const r = bag(raw);
  return {
    tile_id: text(r.tile_id),
    position: num(r.position),
    visible: r.visible === true,
  };
}

export function toTileCatalogEntry(raw: unknown): BuilderTileCatalogEntry {
  const r = bag(raw);
  return {
    tile_id: text(r.tile_id),
    label: text(r.label),
    module: text(r.module),
    default_visible: r.default_visible === true,
    lead: text(r.lead),
    wired: r.wired === true,
  };
}

export function toTileCatalog(raw: unknown): BuilderTileCatalog {
  const r = bag(raw);
  return {
    tiles: list(r.tiles).map(toTileCatalogEntry),
    profiles: words(r.profiles),
    profile: text(r.profile),
  };
}

export function toHomeConfig(raw: unknown): BuilderHomeConfig {
  const r = bag(raw);
  return {
    profile: text(r.profile),
    landing_unit: text(r.landing_unit),
    landing_module: text(r.landing_module),
    takeover: r.takeover === true,
    mobile_only: r.mobile_only === true,
  };
}

export function toMyHome(raw: unknown): BuilderMyHome {
  const r = bag(raw);
  return {
    profile: text(r.profile),
    source: text(r.source),
    tiles: list(r.tiles).map(toTileRow),
    home: toHomeConfig(r.home),
    tabs: r.tabs === undefined ? { source: "catalog", pins: [] } : toTabStrip(r.tabs),
  };
}

export function toProfilesView(raw: unknown): BuilderProfilesView {
  const r = bag(raw);
  return {
    profiles: list(r.profiles).map((p) => {
      const b = bag(p);
      return { profile: text(b.profile), home: toHomeConfig(b.home) };
    }),
  };
}

export function toProfileDetail(raw: unknown): BuilderProfileDetail {
  const r = bag(raw);
  return {
    profile: text(r.profile),
    tiles: list(r.tiles).map(toTileRow),
    home: toHomeConfig(r.home),
  };
}

export function toProfileWidgets(raw: unknown): BuilderProfileWidgets {
  const r = bag(raw);
  return {
    profile: text(r.profile),
    widgets: list(r.widgets).map(toWidgetRow),
  };
}

export function toProfileGallery(raw: unknown): BuilderProfileGallery {
  const r = bag(raw);
  return {
    profile: text(r.profile),
    entries: list(r.entries).map(toGalleryEntryRow),
  };
}

export function toVizTile(raw: unknown): BuilderVizTile {
  const r = bag(raw);
  return {
    tile_id: text(r.tile_id),
    label: text(r.label),
    kind: text(r.kind),
    module: text(r.module),
    state: text(r.state),
    total: maybeNum(r.total),
    points: list(r.points).map((p) => {
      const b = bag(p);
      return { label: text(b.label), number: num(b.number), tone: text(b.tone) };
    }),
    note: text(r.note),
  };
}

export function toVizTiles(raw: unknown): readonly BuilderVizTile[] {
  return list(bag(raw).tiles).map(toVizTile);
}

/** Every call the arrange view makes that the read-only landing does not already own. */
export class BuilderApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // -- my scope (any staff caller) --------------------------------------------------------------------

  /** MY tile-era home: the classic rows, the landing config, and the pinned strip riding along. */
  async myHome(): Promise<Result<BuilderMyHome>> {
    const answer = await staffDashboard.dashboardGetHome(this.#client);
    return answer.ok ? { ok: true, value: toMyHome(answer.value) } : answer;
  }

  /** The full tile catalog, with which tiles have a provider behind them. */
  async tileCatalog(): Promise<Result<BuilderTileCatalog>> {
    const answer = await staffDashboard.dashboardGetCatalog(this.#client);
    return answer.ok ? { ok: true, value: toTileCatalog(answer.value) } : answer;
  }

  /** Save MY classic tile layout. The body is the WHOLE list: an override is whole or absent. */
  async saveMyTiles(rows: readonly BuilderTileRow[]): Promise<Result<{ tiles: readonly BuilderTileRow[] }>> {
    const answer = await staffDashboard.dashboardPutLayout(this.#client, {
      tiles: rows.map((r) => ({ tile_id: r.tile_id, position: r.position, visible: r.visible })),
    });
    if (!answer.ok) return answer;
    return { ok: true, value: { tiles: list(bag(answer.value).tiles).map(toTileRow) } };
  }

  /** Reset MY classic tile layout. The answer is the layout the reset LANDED ON. */
  async resetMyTiles(): Promise<Result<{ cleared: number; home: BuilderMyHome }>> {
    const answer = await staffDashboard.dashboardDeleteLayout(this.#client);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { cleared: num(r.cleared), home: toMyHome(r.layout) } };
  }

  /** Save MY pinned tabs: the whole set, the same whole-or-absent rule. */
  async saveMyTabs(pins: readonly BuilderPin[]): Promise<Result<BuilderTabs>> {
    const answer = await staffDashboard.dashboardPutTabs(this.#client, {
      pins: pins.map((p) => ({ tab_key: p.tab_key, position: p.position, visible: p.visible })),
    });
    return answer.ok ? { ok: true, value: toTabStrip(answer.value) } : answer;
  }

  /** Reset MY pinned tabs. The answer is the strip the reset LANDED ON. */
  async resetMyTabs(): Promise<Result<{ cleared: number; tabs: BuilderTabs }>> {
    const answer = await staffDashboard.dashboardDeleteTabs(this.#client);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { cleared: num(r.cleared), tabs: toTabStrip(r.tabs) } };
  }

  /** The measures THIS caller may build from, with each one's own windows and forms. */
  async widgetCatalog(): Promise<Result<BuilderWidgetCatalog>> {
    const answer = await staffDashboard.dashboardGetWidgetCatalog(this.#client);
    return answer.ok ? { ok: true, value: toWidgetCatalog(answer.value) } : answer;
  }

  /** The ready-made widgets "Add widget" opens on, resolved for this caller. */
  async gallery(): Promise<Result<BuilderGallery>> {
    const answer = await staffDashboard.dashboardGetWidgetGallery(this.#client);
    return answer.ok ? { ok: true, value: toGallery(answer.value) } : answer;
  }

  /** Save MY widget layout. The body is the WHOLE list; there is no per-widget write. */
  async saveMyWidgets(widgets: readonly BuilderWidgetRow[]): Promise<Result<{ widgets: readonly BuilderWidgetRow[] }>> {
    const answer = await staffDashboard.dashboardPutWidgets(this.#client, {
      widgets: widgets.map((w) => this.#wireWidget(w)),
    });
    if (!answer.ok) return answer;
    return { ok: true, value: { widgets: list(bag(answer.value).widgets).map(toWidgetRow) } };
  }

  /** Reset MY widget layout. The answer is the layout the reset LANDED ON. */
  async resetMyWidgets(): Promise<Result<{ cleared: number; layout: BuilderLayout }>> {
    const answer = await staffDashboard.dashboardDeleteWidgets(this.#client);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { cleared: num(r.cleared), layout: toLayout(r.layout) } };
  }

  /** The metric catalog THIS caller may compose from, already cost-gated server side. */
  async metricCatalog(): Promise<Result<{ metrics: readonly BuilderMeasure[]; profile: string }>> {
    const answer = await staffDashboard.dashboardGetMetrics(this.#client);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { metrics: list(r.metrics).map(toMeasure), profile: text(r.profile) } };
  }

  /** The series for composed tiles. An id this caller may not have simply does not come back. */
  async viz(ids: readonly string[]): Promise<Result<readonly BuilderVizTile[]>> {
    if (ids.length === 0) return { ok: true, value: [] };
    const answer = await staffDashboard.dashboardGetViz(this.#client, { tile: [...ids] });
    return answer.ok ? { ok: true, value: toVizTiles(answer.value) } : answer;
  }

  // -- the role defaults (the server re-checks the admin gate on every route) ---------------------------

  /** Every profile and its landing config. */
  async profiles(): Promise<Result<BuilderProfilesView>> {
    const answer = await staffDashboard.dashboardListProfiles(this.#client);
    return answer.ok ? { ok: true, value: toProfilesView(answer.value) } : answer;
  }

  /** One role's classic tile rows and landing config. */
  async profileDetail(profile: string): Promise<Result<BuilderProfileDetail>> {
    const answer = await staffDashboard.dashboardGetProfile(this.#client, profile);
    return answer.ok ? { ok: true, value: toProfileDetail(answer.value) } : answer;
  }

  /** Save one role's classic tile layout. */
  async saveProfileTiles(profile: string, rows: readonly BuilderTileRow[]): Promise<Result<{ tiles: readonly BuilderTileRow[] }>> {
    const answer = await staffDashboard.dashboardPutProfile(this.#client, profile, {
      tiles: rows.map((r) => ({ tile_id: r.tile_id, position: r.position, visible: r.visible })),
    });
    if (!answer.ok) return answer;
    return { ok: true, value: { tiles: list(bag(answer.value).tiles).map(toTileRow) } };
  }

  /** Save one role's landing and takeover config. */
  async saveProfileHome(profile: string, home: BuilderHomeConfig): Promise<Result<BuilderHomeConfig>> {
    const named = home.profile === "" ? profile : home.profile;
    const answer = await staffDashboard.dashboardPutProfileHome(this.#client, profile, {
      profile: named as DashboardProfile,
      takeover: home.takeover,
      mobile_only: home.mobile_only,
      ...(home.landing_unit === "" ? {} : { landing_unit: home.landing_unit }),
      ...(home.landing_module === "" ? {} : { landing_module: home.landing_module }),
    });
    return answer.ok ? { ok: true, value: toHomeConfig(answer.value) } : answer;
  }

  /** One role's stored tab rows. */
  async profileTabs(profile: string): Promise<Result<{ profile: string; pins: readonly BuilderPin[] }>> {
    const answer = await staffDashboard.dashboardGetProfileTabs(this.#client, profile);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { profile: text(r.profile), pins: list(r.pins).map(toPin) } };
  }

  /** Save one role's tab defaults: the whole set. */
  async saveProfileTabs(profile: string, pins: readonly BuilderPin[]): Promise<Result<{ profile: string; pins: readonly BuilderPin[] }>> {
    const answer = await staffDashboard.dashboardPutProfileTabs(this.#client, profile, {
      pins: pins.map((p) => ({ tab_key: p.tab_key, position: p.position, visible: p.visible })),
    });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { profile: text(r.profile), pins: list(r.pins).map(toPin) } };
  }

  /** One role's stored widget defaults, unresolved. */
  async profileWidgets(profile: string): Promise<Result<BuilderProfileWidgets>> {
    const answer = await staffDashboard.dashboardGetProfileWidgets(this.#client, profile);
    return answer.ok ? { ok: true, value: toProfileWidgets(answer.value) } : answer;
  }

  /** Save one role's widget defaults: the whole list. */
  async saveProfileWidgets(profile: string, widgets: readonly BuilderWidgetRow[]): Promise<Result<BuilderProfileWidgets>> {
    const answer = await staffDashboard.dashboardPutProfileWidgets(this.#client, profile, {
      widgets: widgets.map((w) => this.#wireWidget(w)),
    });
    return answer.ok ? { ok: true, value: toProfileWidgets(answer.value) } : answer;
  }

  /** One role's gallery entries, unresolved: one row is read by many callers. */
  async profileGallery(profile: string): Promise<Result<BuilderProfileGallery>> {
    const answer = await staffDashboard.dashboardGetProfileGallery(this.#client, profile);
    return answer.ok ? { ok: true, value: toProfileGallery(answer.value) } : answer;
  }

  /** Save one role's gallery: the whole list, positions renumbered by the caller's order. */
  async saveProfileGallery(profile: string, entries: readonly BuilderGalleryEntryRow[]): Promise<Result<BuilderProfileGallery>> {
    const answer = await staffDashboard.dashboardPutProfileGallery(this.#client, profile, {
      entries: entries.map((e) => this.#wireEntry(e)),
    });
    return answer.ok ? { ok: true, value: toProfileGallery(answer.value) } : answer;
  }

  // The wire spellings of a stored row: the door's narrow types cast back into
  // the contract's closed vocabularies at this one edge and nowhere else.
  #wireWidget(w: BuilderWidgetRow): DashboardWidget {
    const row: DashboardWidget = {
      widget_key: w.widget_key,
      shape: w.shape as DashboardWidget["shape"],
      measure: w.measure,
      split: w.split as DashboardWidget["split"],
      range: w.range as DashboardWidget["range"],
      size: w.size as DashboardWidget["size"],
      title: w.title,
      position: w.position,
      visible: w.visible,
    };
    // Absent is the honest shape for "every branch I can see": the empty
    // string is never sent.
    if (w.branch_id !== "") row.branch_id = w.branch_id;
    if (w.cols !== undefined && w.rows !== undefined) {
      row.cols = w.cols;
      row.rows = w.rows;
    }
    return row;
  }

  #wireEntry(e: BuilderGalleryEntryRow): DashboardGalleryEntry {
    const row: DashboardGalleryEntry = {
      entry_key: e.entry_key,
      shape: e.shape as DashboardGalleryEntry["shape"],
      measure: e.measure,
      split: e.split as DashboardGalleryEntry["split"],
      range: e.range as DashboardGalleryEntry["range"],
      size: e.size as DashboardGalleryEntry["size"],
      title: e.title,
      blurb: e.blurb,
      position: e.position,
      visible: e.visible,
    };
    if (e.cols !== undefined && e.rows !== undefined) {
      row.cols = e.cols;
      row.rows = e.rows;
    }
    return row;
  }
}
