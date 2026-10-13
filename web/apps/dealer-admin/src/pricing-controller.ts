// The Pricing surface's state machine: every call, every phase change and
// every piece of wire shaping the twelve tabs need, held in one immutable
// model the views only read. No DOM, no Lit: the app-root forwards clicks,
// inputs and changes here by name, and the notify callback asks Lit to
// re-render. A fake door stands in for the api in the tests.
//
// THE MONEY RULE, enforced by shape: a margin or price write is one awaited
// call to the server and nothing else. There is no outbox, no retry, no
// optimistic copy to reconcile: a refusal lands in the form's error words
// and the person decides again. The floor warning is the same: the server
// grades, this file carries the grade to the glass, and the confirmation
// the editor gives is logged by the server against the record the save
// wrote, its verdict shown whole.
import type { PricingFloorPreviewRequest } from "@hardscapehouse/api-client";
import type { CategoryRow, FloorItemLike, LoadPhase, PricingTab } from "./pricing-logic.js";
import {
  bpsFromPctInput,
  affectedFloorItems,
  categoryName,
  floorConfirmVerdict,
  floorNoteLines,
  needsFloorConfirm,
  pricingRefusalWords,
  pricingWriteError,
  shownProductIds,
} from "./pricing-logic.js";
import type { FloorPreview, PolicyRow, PolicyWrite, PricingApi, StatusResult } from "./pricing-api.js";

/** What the controller calls; the real PricingApi satisfies it structurally. */
export interface PricingDoor {
  categories(): Promise<{ ok: true; value: readonly CategoryRow[] } | { ok: false; error: string; status?: number }>;
  policies(): Promise<{ ok: true; value: readonly PolicyRow[] } | { ok: false; error: string; status?: number }>;
  upsertPolicy(
    categoryId: string,
    body: PolicyWrite,
  ): Promise<{ ok: true; value: PolicyRow } | { ok: false; error: string; status?: number }>;
  previewFloor(body: PricingFloorPreviewRequest): Promise<
    { ok: true; value: FloorPreview } | { ok: false; error: string; status?: number }
  >;
  confirmFloor(body: {
    entity_type: string;
    entity_id: string;
    product_ids: readonly string[];
  }): Promise<{ ok: true; value: StatusResult } | { ok: false; error: string; status?: number }>;
}

export type Notify = () => void;

// -- the model ---------------------------------------------------------------------------------------

/** One category policy row on the glass. */
export type { PolicyRow };

/** The margin editor's own words, as the person last left them. */
export interface PolicyEdit {
  readonly categoryId: string;
  readonly targetInput: string;
  readonly floorInput: string;
  readonly enforcement: boolean;
}

/** The open floor warning: the server's grading, shown whole. */
export interface FloorPanel {
  readonly items: readonly FloorItemLike[];
  readonly notes: readonly string[];
  /** The ids of the products the editor was shown; the confirmation names them. */
  readonly productIds: readonly string[];
  readonly busy: boolean;
}

export interface MarginModel {
  readonly phase: LoadPhase;
  readonly rows: readonly PolicyRow[];
  readonly error: string;
  readonly editing: PolicyEdit | null;
  readonly busy: boolean;
  readonly formError: string;
  /** The success or verdict words that outlive the reload a save triggers. */
  readonly notice: string;
  readonly floor: FloorPanel | null;
}

export interface PricingModel {
  readonly tab: PricingTab["id"];
  /** The categories catalog: every tab's picker names. */
  readonly categories: readonly CategoryRow[];
  readonly categoriesPhase: LoadPhase;
  readonly margin: MarginModel;
}

function emptyMargin(): MarginModel {
  return { phase: "idle", rows: [], error: "", editing: null, busy: false, formError: "", notice: "", floor: null };
}

function emptyModel(): PricingModel {
  return { tab: "margin", categories: [], categoriesPhase: "idle", margin: emptyMargin() };
}

/**
 * The data-* attributes of one control, prefix stripped, as the controller's
 * action table reads them. app-root calls this; the tests pass plain
 * objects.
 */
export function pricingAttrs(control: Element): Readonly<Record<string, string>> {
  const attrs: Record<string, string> = {};
  for (const attr of Array.from(control.attributes)) {
    if (attr.name.startsWith("data-") && attr.name !== "data-action") {
      attrs[attr.name.slice(5)] = attr.value;
    }
  }
  return attrs;
}

// -- the controller ------------------------------------------------------------------------------------

export class PricingController {
  readonly model: PricingModel = emptyModel();
  readonly #door: PricingDoor;
  readonly #notify: Notify;

  constructor(door: PricingDoor, notify: Notify) {
    this.#door = door;
    this.#notify = notify;
  }

  #set(patch: Partial<PricingModel>): void {
    const model = this.model as unknown as Record<string, unknown>;
    for (const [key, value] of Object.entries(patch)) {
      model[key] = value;
    }
    this.#notify();
  }

  #setMargin(patch: Partial<MarginModel>): void {
    this.#set({ margin: { ...this.model.margin, ...patch } });
  }

  /** The app opens the surface: the categories once, then the open tab's group. */
  async open(): Promise<void> {
    if (this.model.categoriesPhase === "idle") {
      this.#set({ categoriesPhase: "loading" });
      const result = await this.#door.categories();
      if (result.ok) {
        this.#set({ categories: result.value, categoriesPhase: "ready" });
      } else {
        // A refused catalog costs the pickers their names, not the tab: the
        // ids still show. The words land with the tab's own state.
        this.#set({ categories: [], categoriesPhase: "error" });
      }
    }
    if (this.model.margin.phase === "idle") await this.loadMargin();
  }

  /** The sign out: the model returns to empty, nothing survives. */
  reset(): void {
    const model = this.model as unknown as Record<string, unknown>;
    for (const [key, value] of Object.entries(emptyModel())) {
      model[key] = value;
    }
    this.#notify();
  }

  async loadMargin(): Promise<void> {
    this.#setMargin({ ...emptyMargin(), notice: this.model.margin.notice });
    const result = await this.#door.policies();
    if (!result.ok) {
      this.#setMargin({
        phase: result.status === 403 ? "refused" : "error",
        error: pricingRefusalWords(result.error, result.status, "read"),
      });
      return;
    }
    this.#setMargin({ phase: "ready", rows: result.value });
  }

  setTab(tab: PricingTab["id"]): void {
    if (this.model.tab === tab) return;
    this.#set({ tab });
  }

  /** A field mirrored from its input event: the editor's words, never bound. */
  input(field: string, value: string): void {
    const editing = this.model.margin.editing;
    if (editing === null) return;
    if (field === "policy-target") this.#setMargin({ editing: { ...editing, targetInput: value } });
    if (field === "policy-floor") this.#setMargin({ editing: { ...editing, floorInput: value } });
  }

  /** A select or checkbox answered: the category (by its picker name), or the enforcement flag. */
  change(field: string, value: string): void {
    const editing = this.model.margin.editing;
    if (editing === null) return;
    if (field === "policy-category") {
      const hit = this.model.categories.find((c) => c.name === value);
      this.#setMargin({ editing: { ...editing, categoryId: value === "" || hit !== undefined ? (hit?.id ?? "") : editing.categoryId } });
    }
    if (field === "policy-enforce") this.#setMargin({ editing: { ...editing, enforcement: value === "yes" } });
  }

  /**
   * The value a mirrored field shows, as its element should carry it: the
   * text fields take what the person typed, the select takes the category's
   * picker name (the options carry names, the model holds ids), and the
   * checkbox takes yes or no. app-root's updated() mirrors these onto the
   * elements; nothing in a template binds a value.
   */
  fieldValue(field: string): string {
    const editing = this.model.margin.editing;
    if (editing === null) return "";
    switch (field) {
      case "policy-target":
        return editing.targetInput;
      case "policy-floor":
        return editing.floorInput;
      case "policy-category":
        return editing.categoryId === "" ? "" : categoryName(editing.categoryId, this.model.categories);
      case "policy-enforce":
        return editing.enforcement ? "yes" : "no";
      default:
        return "";
    }
  }

  async click(action: string, attrs: Readonly<Record<string, string>>): Promise<void> {
    switch (action) {
      case "pr-set-tab":
        this.setTab(attrs.tab ?? "");
        return;
      case "pr-reload-margin":
        await this.loadMargin();
        return;
      case "pr-policy-edit":
        this.#openPolicyEditor(attrs.category ?? "");
        return;
      case "pr-policy-cancel":
        this.#setMargin({ editing: null, floor: null, formError: "" });
        return;
      case "pr-policy-review":
        this.#setMargin({ floor: null });
        return;
      case "pr-policy-save":
        await this.#savePolicy();
        return;
      case "pr-policy-confirm":
        await this.#confirmPolicy();
        return;
      default:
    }
  }

  // -- the margin editor ---------------------------------------------------------------------------

  #openPolicyEditor(categoryId: string): void {
    const row = this.model.margin.rows.find((r) => r.category_id === categoryId);
    this.#setMargin({
      editing: {
        categoryId,
        targetInput: row !== undefined ? (row.margin_target_bps / 100).toString() : "",
        floorInput: row !== undefined ? (row.margin_floor_bps / 100).toString() : "",
        enforcement: row !== undefined ? row.margin_enforcement_disabled : false,
      },
      floor: null,
      formError: "",
      notice: "",
    });
  }

  /** The write the editor holds, or null while a percent refuses to parse. */
  #policyWrite(): { write: PolicyWrite; error: string } {
    const editing = this.model.margin.editing;
    if (editing === null) return { write: null as unknown as PolicyWrite, error: "" };
    const target = bpsFromPctInput(editing.targetInput);
    const floor = bpsFromPctInput(editing.floorInput);
    if (target === null) return { write: null as unknown as PolicyWrite, error: "Enter the margin target as a percent, like 35." };
    if (floor === null) return { write: null as unknown as PolicyWrite, error: "Enter the margin floor as a percent, like 20." };
    return {
      write: { margin_target_bps: target, margin_floor_bps: floor, margin_enforcement_disabled: editing.enforcement },
      error: "",
    };
  }

  async #savePolicy(): Promise<void> {
    const editing = this.model.margin.editing;
    if (editing === null || this.model.margin.busy) return;
    if (editing.categoryId === "") {
      this.#setMargin({ formError: "Choose the category this policy prices." });
      return;
    }
    const { write, error } = this.#policyWrite();
    if (error !== "") {
      this.#setMargin({ formError: error });
      return;
    }
    this.#setMargin({ busy: true, formError: "" });
    const categoryId = editing.categoryId;
    const preview = await this.#door.previewFloor({ kind: "category_policy", category_id: categoryId, policy: write });
    if (!preview.ok) {
      this.#setMargin({ busy: false, formError: pricingWriteError(preview.error, preview.status) });
      return;
    }
    if (needsFloorConfirm(preview.value)) {
      this.#setMargin({
        busy: false,
        floor: {
          items: affectedFloorItems(preview.value),
          notes: floorNoteLines(preview.value),
          productIds: shownProductIds(preview.value),
          busy: false,
        },
      });
      return;
    }
    await this.#writePolicy(categoryId, write);
  }

  /** Save past an open warning: the write, then the server's own log of the confirmation. */
  async #confirmPolicy(): Promise<void> {
    const editing = this.model.margin.editing;
    const floor = this.model.margin.floor;
    if (editing === null || floor === null || floor.busy || this.model.margin.busy) return;
    const { write, error } = this.#policyWrite();
    if (error !== "") {
      this.#setMargin({ formError: error });
      return;
    }
    this.#setMargin({ floor: { ...floor, busy: true }, formError: "" });
    await this.#writePolicy(editing.categoryId, write, floor.productIds);
  }

  /** One write, awaited, its answer shown, and a reload that keeps the words. */
  async #writePolicy(categoryId: string, write: PolicyWrite, productIds: readonly string[] = []): Promise<void> {
    const result = await this.#door.upsertPolicy(categoryId, write);
    if (!result.ok) {
      this.#setMargin({ busy: false, floor: this.model.margin.floor === null ? null : { ...this.model.margin.floor, busy: false }, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    if (productIds.length > 0) {
      const logged = await this.#door.confirmFloor({
        entity_type: "category_policy",
        entity_id: categoryId,
        product_ids: productIds,
      });
      this.#setMargin({
        busy: false,
        editing: null,
        floor: null,
        notice: logged.ok ? floorConfirmVerdict(logged.value) : floorConfirmVerdict(null),
      });
    } else {
      this.#setMargin({ busy: false, editing: null, floor: null, notice: "Saved." });
    }
    await this.loadMargin();
  }
}

/** The api-client door, typed once for the app-root's construction. */
export function pricingController(api: PricingApi, notify: Notify): PricingController {
  return new PricingController(api, notify);
}
