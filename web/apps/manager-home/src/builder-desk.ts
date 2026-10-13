// The desk behind the arrange view: every call and every state change the
// builder makes, in one plain class the app element holds. No DOM in sight, so
// the whole edit surface is testable on the node runner against fake doors;
// the view (builder-views.ts) is a pure template over this state, and the app
// element only forwards data-action controls here.
//
// THE WRITE RULES THE DESK CARRIES, because the view cannot:
//
//   WHOLE OR ABSENT. Every layout save PUTs the whole renumbered list
//   (layoutBody); every reset is one DELETE whose answer IS the layout the
//   reset landed on, so the desk replaces its working set with the answer and
//   never re-fetches.
//
//   THE SERVER'S OWN WORDS. A refusal lands in writeError verbatim and the
//   working set stands: an override that was refused stored nothing, so there
//   is nothing to unwind. The admin gate is never guessed at client side; a
//   caller the role routes refuse reads the core's Forbidden plus one sentence
//   naming who sets role defaults.
//
// Port provenance: the desk's shape is the original app's dashboard-editor.ts
// and home-bento.ts state machine (repository hardscapeos_dibbits, read only;
// see PROVENANCE.md), rebuilt as a plain class over HH's generated client.
import type { Result } from "@hardscapehouse/api-client";
import type { BranchRef } from "./manager-api.js";
import type {
  BuilderGalleryCard,
  BuilderGalleryEntryRow,
  BuilderHomeConfig,
  BuilderMeasure,
  BuilderPin,
  BuilderProfileDetail,
  BuilderTileCatalogEntry,
  BuilderTileRow,
  BuilderVizTile,
  BuilderWidgetCatalog,
  BuilderWidgetRow,
  BuilderWidgetView,
} from "./builder-api.js";
import { BuilderApi } from "./builder-api.js";
import {
  applyPin,
  buildTileEditorRows,
  canRemove,
  draftFromCatalog,
  layoutBody,
  layoutChanged,
  reconcileDraft,
  moveBy,
  movePin,
  nextSize,
  nextWidgetKey,
  placeAtTop,
  removeKey,
  respanTo,
  spanOf,
  tileLayoutBody,
  vizIdFor,
  widgetFromDraft,
  widgetFromEntry,
  widgetTitle,
  type TileEditorRow,
  type WidgetDraft,
} from "./builder-logic.js";

/** The read the desk reuses from the landing's own door, so a route has one door in this app. */
export interface MineDoor {
  branches(): Promise<Result<readonly BranchRef[]>>;
}

export interface BuilderDoors {
  readonly builder: BuilderApi;
  readonly mine: MineDoor;
}

export type BuilderPhase = "closed" | "loading" | "ready" | "error";

/** One role's four defaults, as the admin scope edits them. */
export interface RoleState {
  readonly profile: string;
  readonly widgets: readonly BuilderWidgetRow[];
  readonly tiles: readonly TileEditorRow[];
  readonly pins: readonly BuilderPin[];
  readonly entries: readonly BuilderGalleryEntryRow[];
  readonly home: BuilderHomeConfig;
}

export interface BuilderState {
  readonly phase: BuilderPhase;
  /** The load failure, in the core's own words. */
  readonly error: string;
  /** The last success, in one sentence. */
  readonly notice: string;
  /** The last refused write, in the core's own words. */
  readonly writeError: string;
  readonly busy: boolean;
  readonly profile: string;
  readonly source: string;
  readonly widgets: readonly BuilderWidgetView[];
  /** My layout exactly as it was loaded: the test for "is a write needed". */
  readonly savedWidgets: readonly BuilderWidgetRow[];
  readonly tabs: readonly BuilderPin[];
  readonly tiles: readonly TileEditorRow[];
  readonly catalog: BuilderWidgetCatalog | null;
  readonly gallery: readonly BuilderGalleryCard[];
  readonly measures: readonly BuilderMeasure[];
  readonly tileCatalog: readonly BuilderTileCatalogEntry[];
  readonly profiles: readonly string[];
  readonly branches: readonly BranchRef[];
  readonly scope: "mine" | "role";
  readonly role: RoleState | null;
  readonly roleLoading: boolean;
  readonly roleError: string;
  readonly galleryOpen: boolean;
  readonly formOpen: boolean;
  readonly draft: WidgetDraft | null;
  readonly previewTile: BuilderVizTile | null;
  readonly previewNote: string;
}

const ROLE_DEFAULTS_WORDS = "Role defaults belong to an administrator.";

/** Map an admin read refusal to the core's own word plus the sentence naming who may. */
export function roleRefusalWords(error: string, status: number | undefined): string {
  if (status === 403) return `The core refused this read: ${error}. ${ROLE_DEFAULTS_WORDS}`;
  return error !== "" ? error : "The role defaults did not load. Try again in a moment.";
}

/** The words an empty edit grid opens on (the inventory's own sentence). */
export const EMPTY_HOME_WORDS = "No widgets yet - add one";

export class BuilderDesk {
  #state: BuilderState = {
    phase: "closed", error: "", notice: "", writeError: "", busy: false,
    profile: "", source: "", widgets: [], savedWidgets: [], tabs: [], tiles: [],
    catalog: null, gallery: [], measures: [], tileCatalog: [], profiles: [], branches: [],
    scope: "mine", role: null, roleLoading: false, roleError: "",
    galleryOpen: false, formOpen: false, draft: null, previewTile: null, previewNote: "",
  };

  readonly #doors: BuilderDoors;
  readonly #onChange: () => void;
  /** The preview's in-flight read, so a caller can await what the form just asked for. */
  #previewRun: Promise<void> = Promise.resolve();

  constructor(doors: BuilderDoors, onChange: () => void = () => {}) {
    this.#doors = doors;
    this.#onChange = onChange;
  }

  get state(): BuilderState {
    return this.#state;
  }

  #set(patch: Partial<BuilderState>): void {
    this.#state = { ...this.#state, ...patch };
    this.#onChange();
  }

  // -- open, close, reload -----------------------------------------------------------------------------

  async open(): Promise<void> {
    this.#set({ phase: "loading", error: "", notice: "", writeError: "", scope: "mine", galleryOpen: false, formOpen: false, draft: null });
    const [mine, catalog, gallery, metrics, tileCatalog, myHome, branches] = await Promise.all([
      this.#doors.builder.myWidgets(),
      this.#doors.builder.widgetCatalog(),
      this.#doors.builder.gallery(),
      this.#doors.builder.metricCatalog(),
      this.#doors.builder.tileCatalog(),
      this.#doors.builder.myHome(),
      this.#doors.mine.branches(),
    ]);
    if (!mine.ok || !catalog.ok || !gallery.ok || !metrics.ok || !tileCatalog.ok || !myHome.ok || !branches.ok) {
      const failure = [mine, catalog, gallery, metrics, tileCatalog, myHome, branches].find((r) => !r.ok);
      const detail = failure !== undefined && !failure.ok ? failure.error : "";
      this.#set({ phase: "error", error: `Your home did not load. ${detail}` });
      return;
    }
    this.#set({
      phase: "ready",
      profile: mine.value.profile,
      source: mine.value.source,
      widgets: mine.value.widgets,
      savedWidgets: mine.value.widgets,
      tabs: myHome.value.tabs.pins,
      tiles: this.#mergeTiles(myHome.value.tiles, tileCatalog.value.tiles),
      catalog: catalog.value,
      gallery: gallery.value.entries,
      measures: metrics.value.metrics,
      tileCatalog: tileCatalog.value.tiles,
      branches: branches.value,
    });
  }

  async reload(): Promise<void> {
    await this.open();
  }

  close(): void {
    this.#set({
      phase: "closed", error: "", notice: "", writeError: "", widgets: [], savedWidgets: [],
      tabs: [], tiles: [], catalog: null, gallery: [], measures: [], tileCatalog: [],
      profiles: [], scope: "mine", role: null, roleLoading: false, roleError: "",
      galleryOpen: false, formOpen: false, draft: null, previewTile: null, previewNote: "",
    });
  }

  // The tile editor rows are merged where both answers exist: the layout for
  // order and visibility, the catalog for the words and the wired flag.
  #mergeTiles(layout: readonly BuilderTileRow[], catalog: readonly BuilderTileCatalogEntry[]): TileEditorRow[] {
    return buildTileEditorRows(catalog, layout);
  }

  // -- my widgets ---------------------------------------------------------------------------------------

  moveWidget(key: string, delta: number): void {
    this.#set({ widgets: moveBy(this.#state.widgets, key, delta) });
  }

  sizeStep(key: string, step: number): void {
    const here = this.#state.widgets.find((w) => w.widget_key === key);
    if (here === undefined) return;
    const span = spanOf(nextSize(here.size, step));
    this.#set({ widgets: respanTo(this.#state.widgets, key, span.cols, span.rows) });
  }

  sizeTo(key: string, size: string): void {
    const span = spanOf(size);
    if (span.cols === 0) return;
    this.#set({ widgets: respanTo(this.#state.widgets, key, span.cols, span.rows) });
  }

  removeWidget(key: string): void {
    const w = this.#state.widgets.find((v) => v.widget_key === key);
    if (w === undefined) return;
    if (!canRemove(w)) {
      this.#set({ notice: "The needs-you queue stays on every home, so it cannot be removed." });
      return;
    }
    this.#set({ widgets: removeKey(this.#state.widgets, key) });
  }

  /** True when the working set differs from what was loaded: the save button's test. */
  get layoutDirty(): boolean {
    return layoutChanged(this.#state.savedWidgets, this.#state.widgets);
  }

  async saveLayout(): Promise<void> {
    await this.#write(async () => {
      const answer = await this.#doors.builder.saveMyWidgets(layoutBody(this.#state.widgets));
      if (!answer.ok) return `Your home did not save. ${answer.error}`;
      // The save stored the caller's override: the layout is theirs from here,
      // which is what the source chip says and what arms the reset button.
      this.#set({ notice: "Your home is saved.", savedWidgets: this.#state.widgets, source: "user" });
      return "";
    });
  }

  async resetLayout(): Promise<void> {
    await this.#write(async () => {
      const answer = await this.#doors.builder.resetMyWidgets();
      if (!answer.ok) return `Your home did not reset. ${answer.error}`;
      this.#set({
        widgets: answer.value.layout.widgets,
        savedWidgets: answer.value.layout.widgets,
        source: answer.value.layout.source,
        notice: "Your home is back to the role default.",
      });
      return "";
    });
  }

  // -- the gallery and the form --------------------------------------------------------------------------

  openGallery(): void {
    this.#set({ galleryOpen: true, formOpen: false });
  }

  closeGallery(): void {
    this.#set({ galleryOpen: false });
  }

  placeEntry(entryKey: string): void {
    const entry = this.#state.gallery.find((e) => e.entry_key === entryKey);
    if (entry === undefined) return;
    const key = nextWidgetKey(entry.measure, this.#state.widgets.map((w) => w.widget_key));
    const placed = widgetFromEntry(entry, key, 0);
    this.#set({
      widgets: placeAtTop(this.#state.widgets, placed),
      galleryOpen: false,
      notice: `${widgetTitle(placed)} is on your home. Save to keep it.`,
    });
  }

  openForm(): void {
    if (this.#state.catalog === null) return;
    this.#set({
      formOpen: true,
      galleryOpen: false,
      draft: draftFromCatalog(this.#state.catalog.measures, this.#state.catalog.ranges),
      previewTile: null,
      previewNote: "",
    });
    this.#previewRun = this.#refreshPreview();
  }

  /** Resolves when the form's preview read (if any) has landed. */
  waitForPreview(): Promise<void> {
    return this.#previewRun;
  }

  closeForm(): void {
    this.#set({ formOpen: false, draft: null, previewTile: null, previewNote: "" });
  }

  /** One form choice, corrected into the catalog: the measure decides what everything else may be. */
  pick(field: string, value: string): void {
    const draft = this.#state.draft;
    if (draft === null || this.#state.catalog === null) return;
    const patched = reconcileDraft({ ...draft, [field]: value }, this.#state.catalog.measures, this.#state.catalog.ranges);
    this.#set({ draft: patched });
    this.#previewRun = this.#refreshPreview();
  }

  /** The one free-text field, mirrored from the input event. */
  setDraftTitle(value: string): void {
    const draft = this.#state.draft;
    if (draft === null) return;
    this.#set({ draft: { ...draft, title: value } });
  }

  placeDraft(): void {
    const draft = this.#state.draft;
    const measure = this.#state.catalog?.measures.find((m) => m.metric_id === draft?.measure);
    if (draft === undefined || draft === null || measure === undefined) return;
    const key = nextWidgetKey(draft.measure, this.#state.widgets.map((w) => w.widget_key));
    const placed = widgetFromDraft(draft, measure, key, 0);
    this.#set({
      widgets: placeAtTop(this.#state.widgets, placed),
      formOpen: false,
      draft: null,
      previewTile: null,
      previewNote: "",
      notice: `${widgetTitle(placed)} is on your home. Save to keep it.`,
    });
  }

  /**
   * The preview's one composed read. The METRIC catalog is the preview's
   * source of truth: a measure missing there cannot be composed into a chart,
   * and the honest preview is its empty state, never a zero.
   */
  async #refreshPreview(): Promise<void> {
    const draft = this.#state.draft;
    if (draft === null) return;
    const measure = this.#state.measures.find((m) => m.metric_id === draft.measure);
    const id = measure === undefined ? null : vizIdFor({ measure: draft.measure, split: draft.split }, measure);
    if (id === null) {
      this.#set({ previewTile: null, previewNote: "This measure has no chart preview." });
      return;
    }
    const answer = await this.#doors.builder.viz([id]);
    if (!answer.ok) {
      this.#set({ previewTile: null, previewNote: answer.error });
      return;
    }
    const tile = answer.value.find((t) => t.tile_id === id) ?? null;
    this.#set({ previewTile: tile, previewNote: tile === null ? "This measure has no chart preview." : "" });
  }

  // -- my pinned tabs -------------------------------------------------------------------------------------

  pinToggle(key: string): void {
    this.#set({ tabs: applyPin(this.#state.tabs, key, !(this.#state.tabs.find((p) => p.tab_key === key)?.visible ?? false)) });
  }

  pinMove(key: string, delta: number): void {
    this.#set({ tabs: movePin(this.#state.tabs, key, delta) });
  }

  async saveTabs(): Promise<void> {
    await this.#write(async () => {
      const answer = await this.#doors.builder.saveMyTabs(renumberPins(this.#state.tabs));
      if (!answer.ok) return `Your pinned tabs did not save. ${answer.error}`;
      this.#set({ tabs: answer.value.pins, notice: "Your pinned tabs are saved." });
      return "";
    });
  }

  async resetTabs(): Promise<void> {
    await this.#write(async () => {
      const answer = await this.#doors.builder.resetMyTabs();
      if (!answer.ok) return `Your pinned tabs did not reset. ${answer.error}`;
      this.#set({ tabs: answer.value.tabs.pins, notice: "Your pinned tabs are back to the role default." });
      return "";
    });
  }

  // -- my classic tiles ------------------------------------------------------------------------------------

  tileToggle(tileId: string): void {
    this.#set({ tiles: this.#state.tiles.map((t) => (t.tile_id === tileId ? { ...t, visible: !t.visible } : t)) });
  }

  tileMove(tileId: string, delta: number): void {
    const i = this.#state.tiles.findIndex((t) => t.tile_id === tileId);
    if (i < 0) return;
    const clamped = Math.max(0, Math.min(this.#state.tiles.length - 1, i + delta));
    const tiles = [...this.#state.tiles];
    const [it] = tiles.splice(i, 1);
    tiles.splice(clamped, 0, it);
    this.#set({ tiles });
  }

  async saveTiles(): Promise<void> {
    await this.#write(async () => {
      const answer = await this.#doors.builder.saveMyTiles(tileLayoutBody(this.#state.tiles));
      if (!answer.ok) return `Your tiles did not save. ${answer.error}`;
      this.#set({ notice: "Your classic tiles are saved." });
      return "";
    });
  }

  async resetTiles(): Promise<void> {
    await this.#write(async () => {
      const answer = await this.#doors.builder.resetMyTiles();
      if (!answer.ok) return `Your tiles did not reset. ${answer.error}`;
      this.#set({ tiles: this.#mergeTiles(answer.value.home.tiles, this.#state.tileCatalog), notice: "Your classic tiles are back to the role default." });
      return "";
    });
  }

  // -- the role defaults (the server re-checks the admin gate) ----------------------------------------------

  async openRole(): Promise<void> {
    this.#set({ scope: "role", roleError: "" });
    if (this.#state.profiles.length === 0) {
      const answer = await this.#doors.builder.profiles();
      if (!answer.ok) {
        this.#set({ roleError: roleRefusalWords(answer.error, answer.status) });
        return;
      }
      this.#set({ profiles: answer.value.profiles.map((p) => p.profile) });
    }
    await this.pickProfile(this.#state.profiles[0] ?? "");
  }

  async pickProfile(profile: string): Promise<void> {
    if (profile === "") return;
    this.#set({ roleLoading: true, roleError: "" });
    const [detail, tabs, widgets, gallery] = await Promise.all([
      this.#doors.builder.profileDetail(profile),
      this.#doors.builder.profileTabs(profile),
      this.#doors.builder.profileWidgets(profile),
      this.#doors.builder.profileGallery(profile),
    ]);
    if (!detail.ok || !tabs.ok || !widgets.ok || !gallery.ok) {
      const failure = [detail, tabs, widgets, gallery].find((r) => !r.ok);
      const refused = failure !== undefined && !failure.ok ? roleRefusalWords(failure.error, failure.status) : "";
      this.#set({ roleLoading: false, roleError: refused });
      return;
    }
    this.#set({ roleLoading: false, role: this.#roleFrom(detail.value, tabs.value.pins, widgets.value.widgets, gallery.value.entries) });
  }

  #roleFrom(detail: BuilderProfileDetail, pins: readonly BuilderPin[], widgets: readonly BuilderWidgetRow[], entries: readonly BuilderGalleryEntryRow[]): RoleState {
    return {
      profile: detail.profile,
      widgets,
      tiles: this.#mergeTiles(detail.tiles, this.#state.tileCatalog),
      pins,
      entries: [...entries],
      home: detail.home,
    };
  }

  roleMove(key: string, delta: number): void {
    const role = this.#state.role;
    if (role === null) return;
    this.#set({ role: { ...role, widgets: moveBy(role.widgets, key, delta) } });
  }

  roleSizeStep(key: string, step: number): void {
    const role = this.#state.role;
    const here = role?.widgets.find((w) => w.widget_key === key);
    if (role === null || role === undefined || here === undefined) return;
    const span = spanOf(nextSize(here.size, step));
    this.#set({ role: { ...role, widgets: respanTo(role.widgets, key, span.cols, span.rows) } });
  }

  roleSizeTo(key: string, size: string): void {
    const role = this.#state.role;
    if (role === null) return;
    const span = spanOf(size);
    if (span.cols === 0) return;
    this.#set({ role: { ...role, widgets: respanTo(role.widgets, key, span.cols, span.rows) } });
  }

  roleRemove(key: string): void {
    const role = this.#state.role;
    if (role === null) return;
    const w = role.widgets.find((v) => v.widget_key === key);
    if (w !== undefined && !canRemove(w)) {
      this.#set({ notice: "The needs-you queue stays on every home, so it cannot be removed." });
      return;
    }
    this.#set({ role: { ...role, widgets: removeKey(role.widgets, key) } });
  }

  async saveRoleWidgets(): Promise<void> {
    const role = this.#state.role;
    if (role === null) return;
    await this.#write(async () => {
      const answer = await this.#doors.builder.saveProfileWidgets(role.profile, layoutBody(role.widgets));
      if (!answer.ok) return `The role defaults did not save. ${roleRefusalWords(answer.error, answer.status)}`;
      this.#set({ role: { ...role, widgets: answer.value.widgets }, notice: `The ${role.profile} role's widgets are saved.` });
      return "";
    });
  }

  rolePinToggle(key: string): void {
    const role = this.#state.role;
    if (role === null) return;
    this.#set({ role: { ...role, pins: applyPin(role.pins, key, !(role.pins.find((p) => p.tab_key === key)?.visible ?? false)) } });
  }

  rolePinMove(key: string, delta: number): void {
    const role = this.#state.role;
    if (role === null) return;
    this.#set({ role: { ...role, pins: movePin(role.pins, key, delta) } });
  }

  async saveRoleTabs(): Promise<void> {
    const role = this.#state.role;
    if (role === null) return;
    await this.#write(async () => {
      const answer = await this.#doors.builder.saveProfileTabs(role.profile, renumberPins(role.pins));
      if (!answer.ok) return `The role's tabs did not save. ${roleRefusalWords(answer.error, answer.status)}`;
      this.#set({ role: { ...role, pins: answer.value.pins }, notice: `The ${role.profile} role's tabs are saved.` });
      return "";
    });
  }

  roleEntryRemove(entryKey: string): void {
    const role = this.#state.role;
    if (role === null) return;
    this.#set({ role: { ...role, entries: role.entries.filter((e) => e.entry_key !== entryKey).map((e, i) => ({ ...e, position: i })) } });
  }

  roleEntryMove(entryKey: string, delta: number): void {
    const role = this.#state.role;
    if (role === null) return;
    const i = role.entries.findIndex((e) => e.entry_key === entryKey);
    if (i < 0) return;
    const clamped = Math.max(0, Math.min(role.entries.length - 1, i + delta));
    const entries = [...role.entries];
    const [it] = entries.splice(i, 1);
    entries.splice(clamped, 0, it);
    this.#set({ role: { ...role, entries: entries.map((e, j) => ({ ...e, position: j })) } });
  }

  async saveRoleGallery(): Promise<void> {
    const role = this.#state.role;
    if (role === null) return;
    await this.#write(async () => {
      const answer = await this.#doors.builder.saveProfileGallery(role.profile, role.entries);
      if (!answer.ok) return `The role's gallery did not save. ${roleRefusalWords(answer.error, answer.status)}`;
      this.#set({ role: { ...role, entries: answer.value.entries }, notice: `The ${role.profile} role's gallery is saved.` });
      return "";
    });
  }

  roleTileToggle(tileId: string): void {
    const role = this.#state.role;
    if (role === null) return;
    this.#set({ role: { ...role, tiles: role.tiles.map((t) => (t.tile_id === tileId ? { ...t, visible: !t.visible } : t)) } });
  }

  roleTileMove(tileId: string, delta: number): void {
    const role = this.#state.role;
    if (role === null) return;
    const i = role.tiles.findIndex((t) => t.tile_id === tileId);
    if (i < 0) return;
    const clamped = Math.max(0, Math.min(role.tiles.length - 1, i + delta));
    const tiles = [...role.tiles];
    const [it] = tiles.splice(i, 1);
    tiles.splice(clamped, 0, it);
    this.#set({ role: { ...role, tiles } });
  }

  async saveRoleTiles(): Promise<void> {
    const role = this.#state.role;
    if (role === null) return;
    await this.#write(async () => {
      const answer = await this.#doors.builder.saveProfileTiles(role.profile, tileLayoutBody(role.tiles));
      if (!answer.ok) return `The role's tiles did not save. ${roleRefusalWords(answer.error, answer.status)}`;
      this.#set({ notice: `The ${role.profile} role's tiles are saved.` });
      return "";
    });
  }

  /** One landing fact, mirrored from the view's controls. */
  setHome(patch: Partial<Pick<BuilderHomeConfig, "takeover" | "landing_unit" | "landing_module" | "mobile_only">>): void {
    const role = this.#state.role;
    if (role === null) return;
    this.#set({ role: { ...role, home: { ...role.home, ...patch } } });
  }

  async saveRoleHome(): Promise<void> {
    const role = this.#state.role;
    if (role === null) return;
    await this.#write(async () => {
      const answer = await this.#doors.builder.saveProfileHome(role.profile, role.home);
      if (!answer.ok) return `The landing did not save. ${answer.error}`;
      this.#set({ role: { ...role, home: answer.value }, notice: `The ${role.profile} role's landing is saved.` });
      return "";
    });
  }

  // -- the view's controls ----------------------------------------------------------------------------------

  /**
   * The one entry point the app element's click listener calls: the control
   * carries data-op and its parameters, and the desk dispatches. A name this
   * desk does not know is ignored, never guessed at.
   */
  act(control: Element): void {
    const op = control.getAttribute("data-op") ?? "";
    const key = control.getAttribute("data-key") ?? "";
    const delta = Number.parseInt(control.getAttribute("data-delta") ?? "0", 10);
    const step = Number.parseInt(control.getAttribute("data-step") ?? "0", 10);
    switch (op) {
      case "reload": void this.reload(); return;
      case "move": this.moveWidget(key, delta); return;
      case "size": this.sizeTo(key, control.getAttribute("data-size") ?? ""); return;
      case "size-step": this.sizeStep(key, step); return;
      case "remove": this.removeWidget(key); return;
      case "open-gallery": this.openGallery(); return;
      case "close-gallery": this.closeGallery(); return;
      case "place-entry": this.placeEntry(control.getAttribute("data-entry") ?? ""); return;
      case "open-form": this.openForm(); return;
      case "close-form": this.closeForm(); return;
      case "pick": this.pick(control.getAttribute("data-field") ?? "", control.getAttribute("data-value") ?? ""); return;
      case "place-draft": this.placeDraft(); return;
      case "save": void this.saveLayout(); return;
      case "reset-layout": void this.resetLayout(); return;
      case "pin-toggle": this.pinToggle(key); return;
      case "pin-move": this.pinMove(key, delta); return;
      case "save-tabs": void this.saveTabs(); return;
      case "reset-tabs": void this.resetTabs(); return;
      case "save-tiles": void this.saveTiles(); return;
      case "reset-tiles": void this.resetTiles(); return;
      case "tile-toggle": this.tileToggle(key); return;
      case "tile-move": this.tileMove(key, delta); return;
      case "scope": void this.openRole(); return;
      case "scope-mine": this.#set({ scope: "mine" }); return;
      case "home-takeover": this.setHome({ takeover: !(this.#state.role?.home.takeover ?? false) }); return;
      case "home-mobile": this.setHome({ mobile_only: !(this.#state.role?.home.mobile_only ?? false) }); return;
      case "pick-profile": void this.pickProfile(control.getAttribute("data-profile") ?? ""); return;
      case "role-move": this.roleMove(key, delta); return;
      case "role-size": this.roleSizeTo(key, control.getAttribute("data-size") ?? ""); return;
      case "role-size-step": this.roleSizeStep(key, step); return;
      case "role-remove": this.roleRemove(key); return;
      case "role-save": void this.saveRoleWidgets(); return;
      case "role-pin-toggle": this.rolePinToggle(key); return;
      case "role-pin-move": this.rolePinMove(key, delta); return;
      case "role-save-tabs": void this.saveRoleTabs(); return;
      case "role-entry-remove": this.roleEntryRemove(control.getAttribute("data-entry") ?? ""); return;
      case "role-entry-move": this.roleEntryMove(control.getAttribute("data-entry") ?? "", delta); return;
      case "role-save-gallery": void this.saveRoleGallery(); return;
      case "role-tile-toggle": this.roleTileToggle(key); return;
      case "role-tile-move": this.roleTileMove(key, delta); return;
      case "role-save-tiles": void this.saveRoleTiles(); return;
      case "role-home-save": void this.saveRoleHome(); return;
      default: return;
    }
  }

  /** The inputs the app element's field mirror forwards: the form's title and the landing's two words. */
  field(name: string, value: string): void {
    if (name === "builder-title") this.setDraftTitle(value);
    if (name === "builder-landing-unit") this.setHome({ landing_unit: value.trim() });
    if (name === "builder-landing-module") this.setHome({ landing_module: value.trim() });
  }

  // One write at a time, the refusal words land in writeError, a success in
  // the notice: the two channels the view renders, and nothing else.
  async #write(run: () => Promise<string>): Promise<void> {
    this.#set({ busy: true, writeError: "", notice: "" });
    const error = await run();
    this.#set({ busy: false });
    if (error !== "") this.#set({ writeError: error });
  }
}

function renumberPins(pins: readonly BuilderPin[]): BuilderPin[] {
  return pins.map((p, i) => ({ ...p, position: i }));
}
