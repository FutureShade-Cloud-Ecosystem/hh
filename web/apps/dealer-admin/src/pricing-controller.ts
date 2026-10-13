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
import type {
  CategoryRow,
  FloorItemLike,
  LoadPhase,
  PricingTab,
} from "./pricing-logic.js";
import {
  bpsFromPctInput,
  centsFromMoneyInput,
  affectedFloorItems,
  categoryName,
  floorConfirmVerdict,
  floorNoteLines,
  needsFloorConfirm,
  pricingRefusalWords,
  pricingWriteError,
  shownProductIds,
} from "./pricing-logic.js";
import type {
  CreatedRef,
  FloorPreview,
  PolicyRow,
  PolicyWrite,
  PricingApi,
  RuleQuery,
  RuleRow,
  StatusResult,
} from "./pricing-api.js";
import type { PricingRulePatch, PricingRuleWrite } from "@hardscapehouse/api-client";

/** What the controller calls; the real PricingApi satisfies it structurally. */
export interface PricingDoor {
  categories(): Promise<{ ok: true; value: readonly CategoryRow[] } | { ok: false; error: string; status?: number }>;
  policies(): Promise<{ ok: true; value: readonly PolicyRow[] } | { ok: false; error: string; status?: number }>;
  upsertPolicy(
    categoryId: string,
    body: PolicyWrite,
  ): Promise<{ ok: true; value: PolicyRow } | { ok: false; error: string; status?: number }>;
  rules(query?: RuleQuery): Promise<{ ok: true; value: readonly RuleRow[] } | { ok: false; error: string; status?: number }>;
  rule(id: string): Promise<{ ok: true; value: RuleRow } | { ok: false; error: string; status?: number }>;
  createRule(body: PricingRuleWrite): Promise<{ ok: true; value: CreatedRef } | { ok: false; error: string; status?: number }>;
  updateRule(id: string, body: PricingRulePatch): Promise<{ ok: true; value: RuleRow } | { ok: false; error: string; status?: number }>;
  deleteRule(id: string, opts: { version: number }): Promise<{ ok: true; value: StatusResult } | { ok: false; error: string; status?: number }>;
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

/** The rules tab's filters, as the selects last answered. */
export interface RulesFilters {
  readonly scope: string;
  readonly customerType: string;
}

/** The rule editor's own words, as the person last left them. */
export interface RuleEdit {
  /** Empty on a create; the rule's id on an edit. */
  readonly id: string;
  /** The version the read brought; zero on a create. */
  readonly version: number;
  readonly scope: string;
  readonly customerId: string;
  readonly customerType: string;
  readonly categoryId: string;
  readonly productId: string;
  readonly branchId: string;
  readonly calc: string;
  readonly priceInput: string;
  readonly discountInput: string;
  readonly markupInput: string;
  readonly priceUom: string;
  readonly priorityInput: string;
  readonly validFrom: string;
  readonly validTo: string;
  readonly detail: string;
  readonly active: boolean;
}

export interface RulesModel {
  readonly phase: LoadPhase;
  readonly rows: readonly RuleRow[];
  readonly filters: RulesFilters;
  readonly error: string;
  readonly editing: RuleEdit | null;
  readonly busy: boolean;
  readonly formError: string;
  readonly notice: string;
  readonly floor: FloorPanel | null;
  /** The row whose retire button was tapped once and is waiting for the yes. */
  readonly confirmingId: string;
}

export interface PricingModel {
  readonly tab: PricingTab["id"];
  /** The categories catalog: every tab's picker names. */
  readonly categories: readonly CategoryRow[];
  readonly categoriesPhase: LoadPhase;
  readonly margin: MarginModel;
  readonly rules: RulesModel;
}

function emptyMargin(): MarginModel {
  return { phase: "idle", rows: [], error: "", editing: null, busy: false, formError: "", notice: "", floor: null };
}

function emptyRules(): RulesModel {
  return {
    phase: "idle",
    rows: [],
    filters: { scope: "", customerType: "" },
    error: "",
    editing: null,
    busy: false,
    formError: "",
    notice: "",
    floor: null,
    confirmingId: "",
  };
}

function emptyModel(): PricingModel {
  return {
    tab: "margin",
    categories: [],
    categoriesPhase: "idle",
    margin: emptyMargin(),
    rules: emptyRules(),
  };
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

  #setRules(patch: Partial<RulesModel>): void {
    this.#set({ rules: { ...this.model.rules, ...patch } });
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
    if (editing !== null) {
      if (field === "policy-target") this.#setMargin({ editing: { ...editing, targetInput: value } });
      if (field === "policy-floor") this.#setMargin({ editing: { ...editing, floorInput: value } });
    }
    const rule = this.model.rules.editing;
    if (rule !== null) {
      const text_: Partial<Record<string, keyof RuleEdit>> = {
        "rule-customer": "customerId",
        "rule-product": "productId",
        "rule-branch": "branchId",
        "rule-price": "priceInput",
        "rule-discount": "discountInput",
        "rule-markup": "markupInput",
        "rule-uom": "priceUom",
        "rule-priority": "priorityInput",
        "rule-from": "validFrom",
        "rule-to": "validTo",
        "rule-detail": "detail",
      };
      const key = text_[field];
      if (key !== undefined) this.#setRules({ editing: { ...rule, [key]: value } });
    }
  }

  /** A select or checkbox answered: the category (by its picker name), or the enforcement flag. */
  change(field: string, value: string): void {
    const editing = this.model.margin.editing;
    if (editing !== null) {
      if (field === "policy-category") {
        const hit = this.model.categories.find((c) => c.name === value);
        this.#setMargin({ editing: { ...editing, categoryId: value === "" || hit !== undefined ? (hit?.id ?? "") : editing.categoryId } });
      }
      if (field === "policy-enforce") this.#setMargin({ editing: { ...editing, enforcement: value === "yes" } });
    }
    const rule = this.model.rules.editing;
    if (rule !== null) {
      if (field === "rule-scope") this.#setRules({ editing: { ...rule, scope: value } });
      if (field === "rule-type") this.#setRules({ editing: { ...rule, customerType: value } });
      if (field === "rule-calc") this.#setRules({ editing: { ...rule, calc: value } });
      if (field === "rule-category") {
        const hit = this.model.categories.find((c) => c.name === value);
        this.#setRules({ editing: { ...rule, categoryId: value === "" || hit !== undefined ? (hit?.id ?? "") : rule.categoryId } });
      }
      if (field === "rule-active") this.#setRules({ editing: { ...rule, active: value === "yes" } });
    }
    if (field === "rules-scope") this.#setRules({ filters: { ...this.model.rules.filters, scope: value } });
    if (field === "rules-type") this.#setRules({ filters: { ...this.model.rules.filters, customerType: value } });
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
    if (editing !== null) {
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
      }
    }
    const rule = this.model.rules.editing;
    if (rule !== null) {
      switch (field) {
        case "rule-customer":
          return rule.customerId;
        case "rule-type":
          return rule.customerType;
        case "rule-category":
          return rule.categoryId === "" ? "" : categoryName(rule.categoryId, this.model.categories);
        case "rule-product":
          return rule.productId;
        case "rule-branch":
          return rule.branchId;
        case "rule-scope":
          return rule.scope;
        case "rule-calc":
          return rule.calc;
        case "rule-price":
          return rule.priceInput;
        case "rule-discount":
          return rule.discountInput;
        case "rule-markup":
          return rule.markupInput;
        case "rule-uom":
          return rule.priceUom;
        case "rule-priority":
          return rule.priorityInput;
        case "rule-from":
          return rule.validFrom;
        case "rule-to":
          return rule.validTo;
        case "rule-detail":
          return rule.detail;
        case "rule-active":
          return rule.active ? "yes" : "no";
        default:
      }
    }
    switch (field) {
      case "rules-scope":
        return this.model.rules.filters.scope;
      case "rules-type":
        return this.model.rules.filters.customerType;
      default:
        return "";
    }
  }

  async click(action: string, attrs: Readonly<Record<string, string>>): Promise<void> {
    switch (action) {
      case "pr-set-tab": {
        const tab = attrs.tab ?? "";
        this.setTab(tab);
        // A tab loads its own reads the first time it is opened.
        if (tab === "rules" && this.model.rules.phase === "idle") await this.loadRules();
        return;
      }
      case "pr-reload-margin":
        await this.loadMargin();
        return;
      // -- the margin editor --
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
      // -- the rules tab --
      case "pr-rules-apply":
        await this.loadRules();
        return;
      case "pr-rules-clear":
        this.#setRules({ filters: { scope: "", customerType: "" } });
        await this.loadRules();
        return;
      case "pr-rules-reload":
        await this.loadRules();
        return;
      case "pr-rule-edit":
        await this.#openRuleEditor(attrs.id ?? "");
        return;
      case "pr-rule-cancel":
        this.#setRules({ editing: null, floor: null, formError: "" });
        return;
      case "pr-rule-review":
        this.#setRules({ floor: null });
        return;
      case "pr-rule-save":
        await this.#saveRule();
        return;
      case "pr-rule-confirm":
        await this.#confirmRule();
        return;
      case "pr-rule-retire":
        await this.#retireRule(attrs.id ?? "");
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

  // -- the rules tab ---------------------------------------------------------------------------------

  async loadRules(): Promise<void> {
    this.#setRules({ ...emptyRules(), filters: this.model.rules.filters, notice: this.model.rules.notice });
    const f = this.model.rules.filters;
    const result = await this.#door.rules({ scope: f.scope, customer_type: f.customerType });
    if (!result.ok) {
      this.#setRules({
        phase: result.status === 403 ? "refused" : "error",
        error: pricingRefusalWords(result.error, result.status, "read"),
      });
      return;
    }
    this.#setRules({ phase: "ready", rows: result.value });
  }

  async #openRuleEditor(id: string): Promise<void> {
    if (id === "") {
      this.#setRules({
        editing: {
          id: "",
          version: 0,
          scope: "",
          customerId: "",
          customerType: "",
          categoryId: "",
          productId: "",
          branchId: "",
          calc: "",
          priceInput: "",
          discountInput: "",
          markupInput: "",
          priceUom: "",
          priorityInput: "",
          validFrom: "",
          validTo: "",
          detail: "",
          active: true,
        },
        floor: null,
        formError: "",
        notice: "",
      });
      return;
    }
    // The editor opens on the version the server holds now, not the list's.
    this.#setRules({ editing: null });
    const read = await this.#door.rule(id);
    if (!read.ok) {
      this.#setRules({ formError: pricingWriteError(read.error, read.status), editing: null });
      return;
    }
    const row = read.value;
    this.#setRules({
      editing: {
        id: row.id,
        version: row.version,
        scope: row.scope,
        customerId: row.customer_id,
        customerType: row.customer_type,
        categoryId: row.category_id,
        productId: row.product_id,
        branchId: row.branch_id,
        calc: row.calc_type,
        priceInput: row.price_cents !== null ? (row.price_cents / 100).toString() : "",
        discountInput: row.discount_bps !== null ? (row.discount_bps / 100).toString() : "",
        markupInput: row.markup_bps !== null ? (row.markup_bps / 100).toString() : "",
        priceUom: row.price_uom,
        priorityInput: row.priority.toString(),
        validFrom: (row.valid_from ?? "").slice(0, 10),
        validTo: (row.valid_to ?? "").slice(0, 10),
        detail: row.detail_text,
        active: row.is_active,
      },
      floor: null,
      formError: "",
      notice: "",
    });
  }

  /** The wire body the editor holds, or the words for the input it cannot encode. */
  #ruleWrite(): { write: PricingRuleWrite | null; patch: PricingRulePatch | null; error: string } {
    const editing = this.model.rules.editing;
    if (editing === null) return { write: null, patch: null, error: "" };
    const priority = Number(editing.priorityInput.trim());
    if (editing.priorityInput.trim() === "" || !Number.isFinite(priority) || priority < 0 || !Number.isInteger(priority)) {
      return { write: null, patch: null, error: "Enter the priority as a whole number, like 10." };
    }
    const base = {
      scope: editing.scope,
      customer_id: editing.customerId.trim(),
      customer_type: editing.customerType,
      category_id: editing.categoryId,
      product_id: editing.productId.trim(),
      branch_id: editing.branchId.trim(),
      calc_type: editing.calc,
      price_cents: null as number | null,
      discount_bps: null as number | null,
      markup_bps: null as number | null,
      price_uom: editing.priceUom.trim(),
      priority,
      detail_text: editing.detail,
    };
    if (editing.calc === "ABSOLUTE") {
      const cents = centsFromMoneyInput(editing.priceInput);
      if (cents === null) return { write: null, patch: null, error: "Enter the fixed price in dollars, like 12.34." };
      base.price_cents = cents;
    } else if (editing.calc === "PCT_OFF_LIST") {
      const bps = bpsFromPctInput(editing.discountInput);
      if (bps === null) return { write: null, patch: null, error: "Enter the discount as a percent, like 10." };
      base.discount_bps = bps;
    } else if (editing.calc === "MARKUP_ON_COST") {
      const bps = bpsFromPctInput(editing.markupInput);
      if (bps === null) return { write: null, patch: null, error: "Enter the markup as a percent, like 25." };
      base.markup_bps = bps;
    } else {
      return { write: null, patch: null, error: "Choose what the rule does to the price." };
    }
    if (editing.scope === "") return { write: null, patch: null, error: "Choose the rule's scope." };
    const validity: { valid_from?: string | null; valid_to?: string | null } = {};
    if (editing.validFrom.trim() !== "") validity.valid_from = editing.validFrom.trim();
    if (editing.validTo.trim() !== "") validity.valid_to = editing.validTo.trim();
    const write: PricingRuleWrite = { ...base, ...validity, version: editing.version, is_active: editing.active } as PricingRuleWrite;
    const patch: PricingRulePatch = { ...base, ...validity, version: editing.version, is_active: editing.active } as PricingRulePatch;
    return { write, patch, error: "" };
  }

  async #saveRule(): Promise<void> {
    const editing = this.model.rules.editing;
    if (editing === null || this.model.rules.busy) return;
    const { write, error } = this.#ruleWrite();
    if (error !== "" || write === null) {
      this.#setRules({ formError: error });
      return;
    }
    this.#setRules({ busy: true, formError: "" });
    const preview = await this.#door.previewFloor({
      kind: "rule",
      ...(editing.id !== "" ? { id: editing.id } : {}),
      ...(editing.branchId.trim() !== "" ? { branch_id: editing.branchId.trim() } : {}),
      rule: write,
    });
    if (!preview.ok) {
      this.#setRules({ busy: false, formError: pricingWriteError(preview.error, preview.status) });
      return;
    }
    if (needsFloorConfirm(preview.value)) {
      this.#setRules({
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
    await this.#writeRule(write);
  }

  /** Save past an open warning, then the server's own log of the confirmation. */
  async #confirmRule(): Promise<void> {
    const editing = this.model.rules.editing;
    const floor = this.model.rules.floor;
    if (editing === null || floor === null || floor.busy || this.model.rules.busy) return;
    const { write, error } = this.#ruleWrite();
    if (error !== "" || write === null) {
      this.#setRules({ formError: error });
      return;
    }
    this.#setRules({ floor: { ...floor, busy: true } });
    await this.#writeRule(write, floor.productIds);
  }

  async #writeRule(write: PricingRuleWrite, productIds: readonly string[] = []): Promise<void> {
    const editing = this.model.rules.editing;
    if (editing === null) return;
    let entityId = editing.id;
    if (editing.id === "") {
      const created = await this.#door.createRule(write);
      if (!created.ok) {
        this.#setRules({ busy: false, floor: null, formError: pricingWriteError(created.error, created.status) });
        return;
      }
      entityId = created.value.id;
    } else {
      const patch = { ...write, version: editing.version } as PricingRulePatch;
      const updated = await this.#door.updateRule(editing.id, patch);
      if (!updated.ok) {
        this.#setRules({ busy: false, floor: this.model.rules.floor === null ? null : { ...this.model.rules.floor, busy: false }, formError: pricingWriteError(updated.error, updated.status) });
        return;
      }
    }
    if (productIds.length > 0) {
      const logged = await this.#door.confirmFloor({
        entity_type: "pricing_rule",
        entity_id: entityId,
        product_ids: productIds,
      });
      this.#setRules({
        busy: false,
        editing: null,
        floor: null,
        notice: logged.ok ? floorConfirmVerdict(logged.value) : floorConfirmVerdict(null),
      });
    } else {
      this.#setRules({ busy: false, editing: null, floor: null, notice: "Saved." });
    }
    await this.loadRules();
  }

  /** Retire is two taps: the first asks, the second carries the row's version. */
  async #retireRule(id: string): Promise<void> {
    if (id === "" || this.model.rules.busy) return;
    if (this.model.rules.confirmingId !== id) {
      this.#setRules({ confirmingId: id });
      return;
    }
    const row = this.model.rules.rows.find((r) => r.id === id);
    if (row === undefined) {
      this.#setRules({ confirmingId: "" });
      return;
    }
    this.#setRules({ busy: true, confirmingId: "" });
    const result = await this.#door.deleteRule(id, { version: row.version });
    if (!result.ok) {
      this.#setRules({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setRules({ busy: false, notice: "Retired. The rule stays in the pricing log." });
    await this.loadRules();
  }
}

/** The api-client door, typed once for the app-root's construction. */
export function pricingController(api: PricingApi, notify: Notify): PricingController {
  return new PricingController(api, notify);
}
