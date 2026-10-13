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
  accrualResultWords,
  importResultWords,
  vendorWords,
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
  AccrualResult,
  BreakRow,
  ClearanceRow,
  CreatedRef,
  FloorPreview,
  LedgerRow,
  OverrideRow,
  PolicyRow,
  PolicyWrite,
  PricingApi,
  PromotionRow,
  RebateRow,
  RuleQuery,
  RuleRow,
  StatusResult,
  UomConversionRow,
  UomPlanRow,
  BatchImportResult,
  BatchRow,
  ImportProfileRow,
} from "./pricing-api.js";
import type { PricingOverrideWrite, PricingRulePatch, PricingRuleWrite } from "@hardscapehouse/api-client";

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
  overrides(customerId: string): Promise<{ ok: true; value: readonly OverrideRow[] } | { ok: false; error: string; status?: number }>;
  createOverride(
    customerId: string,
    body: PricingOverrideWrite,
  ): Promise<{ ok: true; value: CreatedRef } | { ok: false; error: string; status?: number }>;
  updateOverride(id: string, body: PricingOverrideWrite): Promise<{ ok: true; value: OverrideRow } | { ok: false; error: string; status?: number }>;
  deleteOverride(id: string, opts: { version: number }): Promise<{ ok: true; value: StatusResult } | { ok: false; error: string; status?: number }>;
  breaks(): Promise<{ ok: true; value: readonly BreakRow[] } | { ok: false; error: string; status?: number }>;
  break(id: string): Promise<{ ok: true; value: BreakRow } | { ok: false; error: string; status?: number }>;
  createBreak(body: Record<string, unknown>): Promise<{ ok: true; value: CreatedRef } | { ok: false; error: string; status?: number }>;
  updateBreak(id: string, body: Record<string, unknown>): Promise<{ ok: true; value: BreakRow } | { ok: false; error: string; status?: number }>;
  deleteBreak(id: string, opts: { version: number }): Promise<{ ok: true; value: StatusResult } | { ok: false; error: string; status?: number }>;
  promotions(): Promise<{ ok: true; value: readonly PromotionRow[] } | { ok: false; error: string; status?: number }>;
  createPromotion(body: Record<string, unknown>): Promise<{ ok: true; value: PromotionRow } | { ok: false; error: string; status?: number }>;
  updatePromotion(id: string, body: Record<string, unknown>): Promise<{ ok: true; value: PromotionRow } | { ok: false; error: string; status?: number }>;
  deletePromotion(id: string, opts: { version: number }): Promise<{ ok: true; value: StatusResult } | { ok: false; error: string; status?: number }>;
  clearance(): Promise<{ ok: true; value: readonly ClearanceRow[] } | { ok: false; error: string; status?: number }>;
  upsertClearance(productId: string, body: Record<string, unknown>): Promise<{ ok: true; value: ClearanceRow } | { ok: false; error: string; status?: number }>;
  deleteClearance(productId: string): Promise<{ ok: true; value: StatusResult } | { ok: false; error: string; status?: number }>;
  rebatePrograms(): Promise<{ ok: true; value: readonly RebateRow[] } | { ok: false; error: string; status?: number }>;
  createRebate(body: Record<string, unknown>): Promise<{ ok: true; value: RebateRow } | { ok: false; error: string; status?: number }>;
  updateRebate(id: string, body: Record<string, unknown>): Promise<{ ok: true; value: RebateRow } | { ok: false; error: string; status?: number }>;
  deleteRebate(id: string, opts: { version: number }): Promise<{ ok: true; value: StatusResult } | { ok: false; error: string; status?: number }>;
  vendorLedger(vendorId: string): Promise<{ ok: true; value: LedgerRow } | { ok: false; error: string; status?: number }>;
  accrueRebates(body: Record<string, unknown>): Promise<{ ok: true; value: AccrualResult } | { ok: false; error: string; status?: number }>;
  uomConversions(): Promise<{ ok: true; value: readonly UomConversionRow[] } | { ok: false; error: string; status?: number }>;
  upsertUom(body: Record<string, unknown>): Promise<{ ok: true; value: UomConversionRow } | { ok: false; error: string; status?: number }>;
  deleteUom(id: string): Promise<{ ok: true; value: StatusResult } | { ok: false; error: string; status?: number }>;
  planUom(body: Record<string, unknown>): Promise<{ ok: true; value: UomPlanRow } | { ok: false; error: string; status?: number }>;
  batches(status?: string): Promise<{ ok: true; value: readonly BatchRow[] } | { ok: false; error: string; status?: number }>;
  createBatch(body: Record<string, unknown>): Promise<{ ok: true; value: BatchRow } | { ok: false; error: string; status?: number }>;
  batchLines(id: string): Promise<{ ok: true; value: readonly { readonly id: string; readonly sku: string; readonly before_retail_cents: number | null; readonly after_retail_cents: number | null; readonly is_outlier: boolean; readonly unmatched: boolean; readonly derived_from: string }[] } | { ok: false; error: string; status?: number }>;
  importBatch(id: string, csv: string): Promise<{ ok: true; value: BatchImportResult } | { ok: false; error: string; status?: number }>;
  approveBatch(id: string): Promise<{ ok: true; value: BatchRow } | { ok: false; error: string; status?: number }>;
  applyBatch(id: string): Promise<{ ok: true; value: { status: string; applied: number } } | { ok: false; error: string; status?: number }>;
  applyDueBatches(): Promise<{ ok: true; value: { status: string; applied: number } } | { ok: false; error: string; status?: number }>;
  importProfiles(): Promise<{ ok: true; value: readonly ImportProfileRow[] } | { ok: false; error: string; status?: number }>;
  updateImportProfile(id: string, mapping: Readonly<Record<string, string>>): Promise<{ ok: true; value: ImportProfileRow } | { ok: false; error: string; status?: number }>;
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

/** The override editor's own words, as the person last left them. */
export interface OverrideEdit {
  /** Empty on a create; the override's id on an edit. */
  readonly id: string;
  readonly version: number;
  readonly categoryId: string;
  readonly calc: string;
  readonly discountInput: string;
  readonly markupInput: string;
  readonly priceUom: string;
  readonly notes: string;
}

export interface CustomersModel {
  readonly phase: LoadPhase;
  /** The customer id as typed; the load answers exactly this id. */
  readonly customerIdInput: string;
  /** The customer the rows belong to, captured when the load started. */
  readonly loadedFor: string;
  readonly rows: readonly OverrideRow[];
  readonly error: string;
  readonly editing: OverrideEdit | null;
  readonly busy: boolean;
  readonly formError: string;
  readonly notice: string;
  readonly floor: FloorPanel | null;
  /** The row whose delete button was tapped once and is waiting for the yes. */
  readonly confirmingId: string;
}

/** One tier row in the breaks editor, as the person last left it. */
export interface BreakItemDraft {
  readonly fromQty: string;
  readonly toQty: string;
  readonly type: string;
  readonly valueInput: string;
  readonly uom: string;
}

/** The breaks editor's own words, as the person last left them. */
export interface BreakEdit {
  readonly id: string;
  readonly version: number;
  readonly code: string;
  readonly nameInput: string;
  readonly productId: string;
  readonly categoryId: string;
  readonly thresholdUom: string;
  readonly active: boolean;
  readonly items: readonly BreakItemDraft[];
}

/** The promotion editor's own words, as the person last left them. */
export interface PromoEdit {
  readonly id: string;
  readonly version: number;
  readonly code: string;
  readonly nameInput: string;
  readonly categoryId: string;
  readonly productId: string;
  readonly branchId: string;
  readonly starts: string;
  readonly ends: string;
  readonly calc: string;
  readonly valueInput: string;
  readonly priceUom: string;
  readonly priorityInput: string;
  readonly detail: string;
  readonly active: boolean;
}

export interface PromotionsPanel {
  readonly phase: LoadPhase;
  readonly rows: readonly PromotionRow[];
  readonly error: string;
  readonly editing: PromoEdit | null;
  readonly busy: boolean;
  readonly formError: string;
  readonly notice: string;
  readonly confirmingId: string;
}

/** The clearance editor's own words: one product's mark, or a new one. */
export interface ClearanceEdit {
  readonly productId: string;
  readonly reason: string;
  readonly priceInput: string;
  readonly priceUom: string;
  readonly starts: string;
  readonly ends: string;
}

export interface ClearancePanel {
  readonly phase: LoadPhase;
  readonly rows: readonly ClearanceRow[];
  readonly error: string;
  readonly editing: ClearanceEdit | null;
  readonly busy: boolean;
  readonly formError: string;
  readonly notice: string;
  readonly confirmingId: string;
}

/** The rebate editor's own words, as the person last left them. */
export interface RebateEdit {
  readonly id: string;
  readonly version: number;
  readonly code: string;
  readonly nameInput: string;
  readonly vendorId: string;
  readonly categoryId: string;
  readonly productId: string;
  readonly basis: string;
  readonly rateInput: string;
  readonly starts: string;
  readonly ends: string;
  readonly notes: string;
  readonly active: boolean;
}

/** The ledger panel: one vendor's claim side. */
export interface LedgerPanel {
  readonly phase: LoadPhase;
  readonly vendorId: string;
  readonly vendorName: string;
  readonly totalCents: number;
  readonly count: number;
  readonly rows: readonly { readonly accrued_at: string; readonly program_code: string; readonly product_sku: string; readonly source_type: string; readonly source_id: string; readonly basis_cents: number; readonly rate_note: string; readonly accrual_cents: number }[];
  readonly error: string;
}

/** The accrual form and its verdict. */
export interface AccrualPanel {
  readonly sourceType: string;
  readonly sourceId: string;
  readonly occurred: string;
  readonly productId: string;
  readonly categoryId: string;
  readonly qtyInput: string;
  readonly saleInput: string;
  readonly costInput: string;
  readonly busy: boolean;
  readonly formError: string;
  readonly result: string;
}

/** The conversion editor's own words, as the person last left them. */
export interface UomEdit {
  readonly productId: string;
  readonly categoryId: string;
  readonly fromUom: string;
  readonly toUom: string;
  readonly factorInput: string;
  readonly roundUp: boolean;
  readonly source: string;
  readonly sourceNote: string;
}

/** The probe: the typed question, and the engine's answer. */
export interface UomProbe {
  readonly productId: string;
  readonly categoryId: string;
  readonly qtyInput: string;
  readonly uomInput: string;
  readonly pickUom: string;
  readonly weightUom: string;
  readonly busy: boolean;
  readonly error: string;
  readonly result: UomPlanRow | null;
}

/** The batch create form's own words. */
export interface BatchCreateForm {
  readonly batchNo: string;
  readonly vendorId: string;
  readonly profileId: string;
  readonly effective: string;
  readonly sourceRef: string;
}

/** The open batch: its lines, its import panel, and its maker check buttons. */
export interface OpenBatch {
  readonly id: string;
  readonly linesPhase: LoadPhase;
  readonly lines: readonly { readonly id: string; readonly sku: string; readonly before_retail_cents: number | null; readonly after_retail_cents: number | null; readonly is_outlier: boolean; readonly unmatched: boolean; readonly derived_from: string }[];
  readonly linesError: string;
  readonly importText: string;
  readonly importBusy: boolean;
  readonly importError: string;
  readonly importResult: string;
  readonly actionBusy: boolean;
  readonly actionError: string;
  readonly actionNotice: string;
}

/** One profile's mapping editor, as the person last left it. */
export interface ProfileEdit {
  readonly id: string;
  readonly name: string;
  readonly skuCol: string;
  readonly costCol: string;
  readonly retailCol: string;
  readonly tradeCol: string;
  readonly effectiveCol: string;
  readonly busy: boolean;
  readonly error: string;
}

export interface BatchesModel {
  readonly phase: LoadPhase;
  readonly rows: readonly BatchRow[];
  readonly statusFilter: string;
  readonly error: string;
  readonly busy: boolean;
  readonly formError: string;
  readonly notice: string;
  readonly creating: BatchCreateForm | null;
  readonly open: OpenBatch | null;
  readonly profiles: readonly ImportProfileRow[];
  readonly profilesPhase: LoadPhase;
  readonly profilesError: string;
  readonly profileEdit: ProfileEdit | null;
}

export interface UomModel {
  readonly phase: LoadPhase;
  readonly rows: readonly UomConversionRow[];
  readonly error: string;
  readonly busy: boolean;
  readonly formError: string;
  readonly notice: string;
  readonly editing: UomEdit | null;
  readonly confirmingId: string;
  readonly probe: UomProbe;
}

export interface RebatesModel {
  readonly phase: LoadPhase;
  readonly rows: readonly RebateRow[];
  readonly error: string;
  readonly editing: RebateEdit | null;
  readonly busy: boolean;
  readonly formError: string;
  readonly notice: string;
  readonly confirmingId: string;
  readonly ledger: LedgerPanel;
  readonly accrual: AccrualPanel;
}

export interface PromosModel {
  readonly promotions: PromotionsPanel;
  readonly clearance: ClearancePanel;
}

export interface BreaksModel {
  readonly phase: LoadPhase;
  readonly rows: readonly BreakRow[];
  readonly error: string;
  readonly editing: BreakEdit | null;
  readonly busy: boolean;
  readonly formError: string;
  readonly notice: string;
  readonly floor: FloorPanel | null;
  readonly confirmingId: string;
}

export interface PricingModel {
  readonly tab: PricingTab["id"];
  /** The categories catalog: every tab's picker names. */
  readonly categories: readonly CategoryRow[];
  readonly categoriesPhase: LoadPhase;
  readonly margin: MarginModel;
  readonly rules: RulesModel;
  readonly customers: CustomersModel;
  readonly breaks: BreaksModel;
  readonly promos: PromosModel;
  readonly rebates: RebatesModel;
  readonly uom: UomModel;
  readonly batches: BatchesModel;
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

function emptyCustomers(): CustomersModel {
  return {
    // Ready from the start: the tab is a typed question, and before a
    // customer is named it says so rather than loading.
    phase: "ready",
    customerIdInput: "",
    loadedFor: "",
    rows: [],
    error: "",
    editing: null,
    busy: false,
    formError: "",
    notice: "",
    floor: null,
    confirmingId: "",
  };
}

function emptyPromotions(): PromotionsPanel {
  return { phase: "idle", rows: [], error: "", editing: null, busy: false, formError: "", notice: "", confirmingId: "" };
}

function emptyClearance(): ClearancePanel {
  return { phase: "idle", rows: [], error: "", editing: null, busy: false, formError: "", notice: "", confirmingId: "" };
}

function emptyRebates(): RebatesModel {
  return {
    phase: "idle",
    rows: [],
    error: "",
    editing: null,
    busy: false,
    formError: "",
    notice: "",
    confirmingId: "",
    ledger: { phase: "idle", vendorId: "", vendorName: "", totalCents: 0, count: 0, rows: [], error: "" },
    accrual: {
      sourceType: "POS_SALE",
      sourceId: "",
      occurred: "",
      productId: "",
      categoryId: "",
      qtyInput: "",
      saleInput: "",
      costInput: "",
      busy: false,
      formError: "",
      result: "",
    },
  };
}

function emptyBatches(): BatchesModel {
  return {
    phase: "idle",
    rows: [],
    statusFilter: "",
    error: "",
    busy: false,
    formError: "",
    notice: "",
    creating: null,
    open: null,
    profiles: [],
    profilesPhase: "idle",
    profilesError: "",
    profileEdit: null,
  };
}

function emptyUom(): UomModel {
  return {
    phase: "idle",
    rows: [],
    error: "",
    busy: false,
    formError: "",
    notice: "",
    editing: null,
    confirmingId: "",
    probe: {
      productId: "",
      categoryId: "",
      qtyInput: "",
      uomInput: "",
      pickUom: "",
      weightUom: "",
      busy: false,
      error: "",
      result: null,
    },
  };
}

function emptyPromos(): PromosModel {
  return { promotions: emptyPromotions(), clearance: emptyClearance() };
}

function emptyBreaks(): BreaksModel {
  return {
    phase: "idle",
    rows: [],
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
    customers: emptyCustomers(),
    breaks: emptyBreaks(),
    promos: emptyPromos(),
    rebates: emptyRebates(),
    uom: emptyUom(),
    batches: emptyBatches(),
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

  #setCustomers(patch: Partial<CustomersModel>): void {
    this.#set({ customers: { ...this.model.customers, ...patch } });
  }

  #setBreaks(patch: Partial<BreaksModel>): void {
    this.#set({ breaks: { ...this.model.breaks, ...patch } });
  }

  #setPromotions(patch: Partial<PromotionsPanel>): void {
    this.#set({ promos: { ...this.model.promos, promotions: { ...this.model.promos.promotions, ...patch } } });
  }

  #setClearance(patch: Partial<ClearancePanel>): void {
    this.#set({ promos: { ...this.model.promos, clearance: { ...this.model.promos.clearance, ...patch } } });
  }

  #setRebates(patch: Partial<RebatesModel>): void {
    this.#set({ rebates: { ...this.model.rebates, ...patch } });
  }

  #setLedger(patch: Partial<LedgerPanel>): void {
    this.#set({ rebates: { ...this.model.rebates, ledger: { ...this.model.rebates.ledger, ...patch } } });
  }

  #setAccrual(patch: Partial<AccrualPanel>): void {
    this.#set({ rebates: { ...this.model.rebates, accrual: { ...this.model.rebates.accrual, ...patch } } });
  }

  #setUom(patch: Partial<UomModel>): void {
    this.#set({ uom: { ...this.model.uom, ...patch } });
  }

  #setProbe(patch: Partial<UomProbe>): void {
    this.#set({ uom: { ...this.model.uom, probe: { ...this.model.uom.probe, ...patch } } });
  }

  #setBatches(patch: Partial<BatchesModel>): void {
    this.#set({ batches: { ...this.model.batches, ...patch } });
  }

  #setOpen(patch: Partial<OpenBatch>): void {
    const open = this.model.batches.open;
    if (open === null) return;
    this.#set({ batches: { ...this.model.batches, open: { ...open, ...patch } } });
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
    const override = this.model.customers.editing;
    if (override !== null) {
      if (field === "cust-discount") this.#setCustomers({ editing: { ...override, discountInput: value } });
      if (field === "cust-markup") this.#setCustomers({ editing: { ...override, markupInput: value } });
      if (field === "cust-uom") this.#setCustomers({ editing: { ...override, priceUom: value } });
      if (field === "cust-notes") this.#setCustomers({ editing: { ...override, notes: value } });
    }
    if (field === "cust-id" && this.model.customers.editing === null) {
      this.#setCustomers({ customerIdInput: value });
    }
    // The breaks editor: the header fields by name, a tier row by its index
    // (brk-item-<n>-<part>).
    const promo = this.model.promos.promotions.editing;
    if (promo !== null) {
      const promoText: Partial<Record<string, keyof PromoEdit>> = {
        "prm-code": "code",
        "prm-name": "nameInput",
        "prm-product": "productId",
        "prm-branch": "branchId",
        "prm-starts": "starts",
        "prm-ends": "ends",
        "prm-value": "valueInput",
        "prm-uom": "priceUom",
        "prm-priority": "priorityInput",
        "prm-detail": "detail",
      };
      const promoKey = promoText[field];
      if (promoKey !== undefined) this.#setPromotions({ editing: { ...promo, [promoKey]: value } });
    }
    const clear = this.model.promos.clearance.editing;
    if (clear !== null) {
      const clearText: Partial<Record<string, keyof ClearanceEdit>> = {
        "clr-product": "productId",
        "clr-reason": "reason",
        "clr-price": "priceInput",
        "clr-uom": "priceUom",
        "clr-starts": "starts",
        "clr-ends": "ends",
      };
      const clearKey = clearText[field];
      if (clearKey !== undefined) this.#setClearance({ editing: { ...clear, [clearKey]: value } });
    }
    const rebate = this.model.rebates.editing;
    if (rebate !== null) {
      const rebText: Partial<Record<string, keyof RebateEdit>> = {
        "reb-code": "code",
        "reb-name": "nameInput",
        "reb-vendor": "vendorId",
        "reb-product": "productId",
        "reb-rate": "rateInput",
        "reb-starts": "starts",
        "reb-ends": "ends",
        "reb-notes": "notes",
      };
      const rebKey = rebText[field];
      if (rebKey !== undefined) this.#setRebates({ editing: { ...rebate, [rebKey]: value } });
    }
    const accrual = this.model.rebates.accrual;
    {
      const accText: Partial<Record<string, keyof AccrualPanel>> = {
        "acc-source-id": "sourceId",
        "acc-occurred": "occurred",
        "acc-product": "productId",
        "acc-category": "categoryId",
        "acc-qty": "qtyInput",
        "acc-sale": "saleInput",
        "acc-cost": "costInput",
      };
      const accKey = accText[field];
      if (accKey !== undefined) this.#setAccrual({ [accKey]: value } as Partial<AccrualPanel>);
    }
    const uomEdit = this.model.uom.editing;
    if (uomEdit !== null) {
      const uomText: Partial<Record<string, keyof UomEdit>> = {
        "uom-product": "productId",
        "uom-category-id": "categoryId",
        "uom-from": "fromUom",
        "uom-to": "toUom",
        "uom-factor": "factorInput",
        "uom-note": "sourceNote",
      };
      const uomKey = uomText[field];
      if (uomKey !== undefined) this.#setUom({ editing: { ...uomEdit, [uomKey]: value } });
    } else {
      const probe = this.model.uom.probe;
      const probeText: Partial<Record<string, keyof UomProbe>> = {
        "uom-probe-product": "productId",
        "uom-probe-category": "categoryId",
        "uom-probe-qty": "qtyInput",
        "uom-probe-uom": "uomInput",
        "uom-probe-pick": "pickUom",
        "uom-probe-weight": "weightUom",
      };
      const probeKey = probeText[field];
      if (probeKey !== undefined) this.#setProbe({ [probeKey]: value } as Partial<UomProbe>);
    }
    const creating = this.model.batches.creating;
    if (creating !== null) {
      const createText: Partial<Record<string, keyof BatchCreateForm>> = {
        "batch-no": "batchNo",
        "batch-vendor": "vendorId",
        "batch-profile": "profileId",
        "batch-effective": "effective",
        "batch-source": "sourceRef",
      };
      const createKey = createText[field];
      if (createKey !== undefined) this.#setBatches({ creating: { ...creating, [createKey]: value } });
    }
    if (field === "batch-csv") this.#setOpen({ importText: value });
    const profileEdit = this.model.batches.profileEdit;
    if (profileEdit !== null) {
      const profileText: Partial<Record<string, keyof ProfileEdit>> = {
        "profile-sku": "skuCol",
        "profile-cost": "costCol",
        "profile-retail": "retailCol",
        "profile-trade": "tradeCol",
        "profile-effective": "effectiveCol",
      };
      const profileKey = profileText[field];
      if (profileKey !== undefined) this.#setBatches({ profileEdit: { ...profileEdit, [profileKey]: value } });
    }
    const brk = this.model.breaks.editing;
    if (brk !== null) {
      if (field === "brk-code") this.#setBreaks({ editing: { ...brk, code: value } });
      if (field === "brk-name") this.#setBreaks({ editing: { ...brk, nameInput: value } });
      if (field === "brk-product") this.#setBreaks({ editing: { ...brk, productId: value } });
      if (field === "brk-uom") this.#setBreaks({ editing: { ...brk, thresholdUom: value } });
      const itemField = field.match(/^brk-item-(\d+)-(from|to|value|uom)$/);
      if (itemField !== null) {
        const index = Number(itemField[1]);
        const part = itemField[2] as "from" | "to" | "value" | "uom";
        if (index < brk.items.length) {
          const items = brk.items.map((item, i) =>
            i === index
              ? {
                  ...item,
                  fromQty: part === "from" ? value : item.fromQty,
                  toQty: part === "to" ? value : item.toQty,
                  valueInput: part === "value" ? value : item.valueInput,
                  uom: part === "uom" ? value : item.uom,
                }
              : item,
          );
          this.#setBreaks({ editing: { ...brk, items } });
        }
      }
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
    const override = this.model.customers.editing;
    if (override !== null) {
      if (field === "cust-category") {
        const hit = this.model.categories.find((c) => c.name === value);
        this.#setCustomers({ editing: { ...override, categoryId: value === "" || hit !== undefined ? (hit?.id ?? "") : override.categoryId } });
      }
      if (field === "cust-calc") this.#setCustomers({ editing: { ...override, calc: value } });
    }
    const promo = this.model.promos.promotions.editing;
    if (promo !== null) {
      if (field === "prm-category") {
        const hit = this.model.categories.find((c) => c.name === value);
        this.#setPromotions({ editing: { ...promo, categoryId: value === "" || hit !== undefined ? (hit?.id ?? "") : promo.categoryId } });
      }
      if (field === "prm-calc") this.#setPromotions({ editing: { ...promo, calc: value } });
      if (field === "prm-active") this.#setPromotions({ editing: { ...promo, active: value === "yes" } });
    }
    const rebate = this.model.rebates.editing;
    if (rebate !== null) {
      if (field === "reb-category") {
        const hit = this.model.categories.find((c) => c.name === value);
        this.#setRebates({ editing: { ...rebate, categoryId: value === "" || hit !== undefined ? (hit?.id ?? "") : rebate.categoryId } });
      }
      if (field === "reb-basis") this.#setRebates({ editing: { ...rebate, basis: value } });
      if (field === "reb-active") this.#setRebates({ editing: { ...rebate, active: value === "yes" } });
    }
    if (field === "acc-source-type") this.#setAccrual({ sourceType: value });
    if (field === "batches-status") this.#setBatches({ statusFilter: value });
    const uomEdit = this.model.uom.editing;
    if (uomEdit !== null) {
      if (field === "uom-source") this.#setUom({ editing: { ...uomEdit, source: value } });
      if (field === "uom-round") this.#setUom({ editing: { ...uomEdit, roundUp: value === "yes" } });
    }
    if (field === "ledger-name") {
      // The picker's options carry the vendors' display words; the answer is
      // mapped back to the id it stands for, as the register's role select does.
      const hit = this.model.rebates.rows.find((r) => vendorWords(r.vendor_name, r.vendor_id) === value);
      if (hit !== undefined) this.#setLedger({ vendorId: hit.vendor_id, vendorName: hit.vendor_name });
    }
    const brk = this.model.breaks.editing;
    if (brk !== null) {
      if (field === "brk-category") {
        const hit = this.model.categories.find((c) => c.name === value);
        this.#setBreaks({ editing: { ...brk, categoryId: value === "" || hit !== undefined ? (hit?.id ?? "") : brk.categoryId } });
      }
      if (field === "brk-active") this.#setBreaks({ editing: { ...brk, active: value === "yes" } });
      const itemField = field.match(/^brk-item-(\d+)-type$/);
      if (itemField !== null) {
        const index = Number(itemField[1]);
        if (index < brk.items.length) {
          const items = brk.items.map((item, i) => (i === index ? { ...item, type: value } : item));
          this.#setBreaks({ editing: { ...brk, items } });
        }
      }
    }
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
      case "cust-id":
        return this.model.customers.customerIdInput;
      case "cust-category":
        return this.#overrideField("categoryId") === ""
          ? ""
          : categoryName(this.#overrideField("categoryId"), this.model.categories);
      case "cust-calc":
        return this.#overrideField("calc");
      case "cust-discount":
        return this.#overrideField("discountInput");
      case "cust-markup":
        return this.#overrideField("markupInput");
      case "cust-uom":
        return this.#overrideField("priceUom");
      case "cust-notes":
        return this.#overrideField("notes");
      case "brk-code":
        return this.model.breaks.editing?.code ?? "";
      case "brk-name":
        return this.model.breaks.editing?.nameInput ?? "";
      case "brk-product":
        return this.model.breaks.editing?.productId ?? "";
      case "brk-category": {
        const cat = this.model.breaks.editing?.categoryId ?? "";
        return cat === "" ? "" : categoryName(cat, this.model.categories);
      }
      case "brk-uom":
        return this.model.breaks.editing?.thresholdUom ?? "";
      case "brk-active":
        return (this.model.breaks.editing?.active ?? false) ? "yes" : "no";
      case "prm-code":
      case "prm-name":
      case "prm-product":
      case "prm-branch":
      case "prm-starts":
      case "prm-ends":
      case "prm-value":
      case "prm-uom":
      case "prm-priority":
      case "prm-detail": {
        const promo = this.model.promos.promotions.editing;
        if (promo === null) return "";
        const promoMap: Record<string, keyof PromoEdit> = {
          "prm-code": "code",
          "prm-name": "nameInput",
          "prm-product": "productId",
          "prm-branch": "branchId",
          "prm-starts": "starts",
          "prm-ends": "ends",
          "prm-value": "valueInput",
          "prm-uom": "priceUom",
          "prm-priority": "priorityInput",
          "prm-detail": "detail",
        };
        return String(promo[promoMap[field]]);
      }
      case "prm-category": {
        const cat = this.model.promos.promotions.editing?.categoryId ?? "";
        return cat === "" ? "" : categoryName(cat, this.model.categories);
      }
      case "prm-calc":
        return this.model.promos.promotions.editing?.calc ?? "";
      case "prm-active":
        return (this.model.promos.promotions.editing?.active ?? false) ? "yes" : "no";
      case "reb-code":
      case "reb-name":
      case "reb-vendor":
      case "reb-product":
      case "reb-rate":
      case "reb-starts":
      case "reb-ends":
      case "reb-notes": {
        const rebate = this.model.rebates.editing;
        if (rebate === null) return "";
        const rebMap: Record<string, keyof RebateEdit> = {
          "reb-code": "code",
          "reb-name": "nameInput",
          "reb-vendor": "vendorId",
          "reb-product": "productId",
          "reb-rate": "rateInput",
          "reb-starts": "starts",
          "reb-ends": "ends",
          "reb-notes": "notes",
        };
        return String(rebate[rebMap[field]]);
      }
      case "reb-category": {
        const cat = this.model.rebates.editing?.categoryId ?? "";
        return cat === "" ? "" : categoryName(cat, this.model.categories);
      }
      case "reb-basis":
        return this.model.rebates.editing?.basis ?? "";
      case "reb-active":
        return (this.model.rebates.editing?.active ?? false) ? "yes" : "no";
      case "acc-source-type":
        return this.model.rebates.accrual.sourceType;
      case "acc-source-id":
        return this.model.rebates.accrual.sourceId;
      case "acc-occurred":
        return this.model.rebates.accrual.occurred;
      case "acc-product":
        return this.model.rebates.accrual.productId;
      case "acc-category":
        return this.model.rebates.accrual.categoryId;
      case "acc-qty":
        return this.model.rebates.accrual.qtyInput;
      case "acc-sale":
        return this.model.rebates.accrual.saleInput;
      case "acc-cost":
        return this.model.rebates.accrual.costInput;
      case "ledger-name": {
        const ledger = this.model.rebates.ledger;
        if (ledger.vendorId === "") return "";
        const row = this.model.rebates.rows.find((r) => r.vendor_id === ledger.vendorId);
        return row === undefined ? "" : vendorWords(row.vendor_name, row.vendor_id);
      }
      case "uom-product":
        return this.model.uom.editing?.productId ?? "";
      case "uom-category-id":
        return this.model.uom.editing?.categoryId ?? "";
      case "uom-from":
        return this.model.uom.editing?.fromUom ?? "";
      case "uom-to":
        return this.model.uom.editing?.toUom ?? "";
      case "uom-factor":
        return this.model.uom.editing?.factorInput ?? "";
      case "uom-note":
        return this.model.uom.editing?.sourceNote ?? "";
      case "uom-source":
        return this.model.uom.editing?.source ?? "";
      case "uom-round":
        return (this.model.uom.editing?.roundUp ?? false) ? "yes" : "no";
      case "uom-probe-product":
        return this.model.uom.probe.productId;
      case "uom-probe-category":
        return this.model.uom.probe.categoryId;
      case "uom-probe-qty":
        return this.model.uom.probe.qtyInput;
      case "uom-probe-uom":
        return this.model.uom.probe.uomInput;
      case "uom-probe-pick":
        return this.model.uom.probe.pickUom;
      case "uom-probe-weight":
        return this.model.uom.probe.weightUom;
      case "batches-status":
        return this.model.batches.statusFilter;
      case "batch-no":
        return this.model.batches.creating?.batchNo ?? "";
      case "batch-vendor":
        return this.model.batches.creating?.vendorId ?? "";
      case "batch-profile":
        return this.model.batches.creating?.profileId ?? "";
      case "batch-effective":
        return this.model.batches.creating?.effective ?? "";
      case "batch-source":
        return this.model.batches.creating?.sourceRef ?? "";
      case "batch-csv":
        return this.model.batches.open?.importText ?? "";
      case "profile-sku":
        return this.model.batches.profileEdit?.skuCol ?? "";
      case "profile-cost":
        return this.model.batches.profileEdit?.costCol ?? "";
      case "profile-retail":
        return this.model.batches.profileEdit?.retailCol ?? "";
      case "profile-trade":
        return this.model.batches.profileEdit?.tradeCol ?? "";
      case "profile-effective":
        return this.model.batches.profileEdit?.effectiveCol ?? "";
      case "clr-product":
      case "clr-reason":
      case "clr-price":
      case "clr-uom":
      case "clr-starts":
      case "clr-ends": {
        const clear = this.model.promos.clearance.editing;
        if (clear === null) return "";
        const clearMap: Record<string, keyof ClearanceEdit> = {
          "clr-product": "productId",
          "clr-reason": "reason",
          "clr-price": "priceInput",
          "clr-uom": "priceUom",
          "clr-starts": "starts",
          "clr-ends": "ends",
        };
        return String(clear[clearMap[field]]);
      }
      default: {
        const brkItem = field.match(/^brk-item-(\d+)-(from|to|value|uom|type)$/);
        if (brkItem !== null) {
          const item = this.model.breaks.editing?.items[Number(brkItem[1])];
          if (item !== undefined) {
            if (brkItem[2] === "from") return item.fromQty;
            if (brkItem[2] === "to") return item.toQty;
            if (brkItem[2] === "value") return item.valueInput;
            if (brkItem[2] === "uom") return item.uom;
            if (brkItem[2] === "type") return item.type;
          }
        }
        return "";
      }
    }
  }

  /** One override editor field, or the empty word when no editor is open. */
  #overrideField(key: "categoryId" | "calc" | "discountInput" | "markupInput" | "priceUom" | "notes"): string {
    return this.model.customers.editing?.[key] ?? "";
  }

  async click(action: string, attrs: Readonly<Record<string, string>>): Promise<void> {
    switch (action) {
      case "pr-set-tab": {
        const tab = attrs.tab ?? "";
        this.setTab(tab);
        // A tab loads its own reads the first time it is opened.
        if (tab === "rules" && this.model.rules.phase === "idle") await this.loadRules();
        if (tab === "breaks" && this.model.breaks.phase === "idle") await this.loadBreaks();
        if (tab === "promos" && this.model.promos.promotions.phase === "idle") await this.loadPromosTab();
        if (tab === "rebates" && this.model.rebates.phase === "idle") await this.loadRebates();
        if (tab === "uom" && this.model.uom.phase === "idle") await this.loadUom();
        if (tab === "batches" && this.model.batches.phase === "idle") await this.loadBatches();
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
      // -- the customer prices tab --
      case "pr-cust-load":
        await this.loadOverrides();
        return;
      case "pr-cust-edit":
        this.#openOverrideEditor(attrs.id ?? "");
        return;
      case "pr-cust-cancel":
        this.#setCustomers({ editing: null, floor: null, formError: "" });
        return;
      case "pr-cust-review":
        this.#setCustomers({ floor: null });
        return;
      case "pr-cust-save":
        await this.#saveOverride();
        return;
      case "pr-cust-confirm":
        await this.#confirmOverride();
        return;
      case "pr-cust-del":
        await this.#deleteOverride(attrs.id ?? "");
        return;
      // -- the quantity breaks tab --
      case "pr-breaks-reload":
        await this.loadBreaks();
        return;
      case "pr-break-edit":
        await this.#openBreakEditor(attrs.id ?? "");
        return;
      case "pr-break-cancel":
        this.#setBreaks({ editing: null, floor: null, formError: "" });
        return;
      case "pr-break-review":
        this.#setBreaks({ floor: null });
        return;
      case "pr-break-save":
        await this.#saveBreak();
        return;
      case "pr-break-confirm":
        await this.#confirmBreak();
        return;
      case "pr-break-del":
        await this.#deleteBreak(attrs.id ?? "");
        return;
      case "pr-break-add-item": {
        const editing = this.model.breaks.editing;
        if (editing !== null && editing.items.length < 20) {
          this.#setBreaks({
            editing: {
              ...editing,
              items: [...editing.items, { fromQty: "", toQty: "", type: "PCT_OFF", valueInput: "", uom: editing.thresholdUom }],
            },
          });
        }
        return;
      }
      // -- the promotions and clearance tab --
      case "pr-promos-reload":
        await this.loadPromosTab();
        return;
      case "pr-promo-edit":
        this.#openPromoEditor(attrs.id ?? "");
        return;
      case "pr-promo-cancel":
        this.#setPromotions({ editing: null, formError: "" });
        return;
      case "pr-promo-save":
        await this.#savePromotion();
        return;
      case "pr-promo-del":
        await this.#deletePromotion(attrs.id ?? "");
        return;
      case "pr-clear-edit":
        this.#openClearanceEditor(attrs.product ?? "");
        return;
      case "pr-clear-cancel":
        this.#setClearance({ editing: null, formError: "" });
        return;
      case "pr-clear-save":
        await this.#saveClearance();
        return;
      case "pr-clear-del":
        await this.#deleteClearance(attrs.product ?? "");
        return;
      // -- the rebates tab --
      case "pr-rebates-reload":
        await this.loadRebates();
        return;
      case "pr-rebate-edit":
        this.#openRebateEditor(attrs.id ?? "");
        return;
      case "pr-rebate-cancel":
        this.#setRebates({ editing: null, formError: "" });
        return;
      case "pr-rebate-save":
        await this.#saveRebate();
        return;
      case "pr-rebate-del":
        await this.#deleteRebate(attrs.id ?? "");
        return;
      case "pr-ledger-vendor":
        await this.loadLedger(this.model.rebates.ledger.vendorId);
        return;
      case "pr-accrue":
        await this.#accrue();
        return;
      // -- the units of measure tab --
      case "pr-uom-reload":
        await this.loadUom();
        return;
      case "pr-uom-edit":
        this.#openUomEditor(attrs.id ?? "");
        return;
      case "pr-uom-cancel":
        this.#setUom({ editing: null, formError: "" });
        return;
      case "pr-uom-save":
        await this.#saveUom();
        return;
      case "pr-uom-del":
        await this.#deleteUom(attrs.id ?? "");
        return;
      case "pr-uom-plan":
        await this.#runPlan();
        return;
      // -- the batches and imports tab --
      case "pr-batches-apply":
        this.#setBatches({ statusFilter: "" });
        await this.loadBatches();
        return;
      case "pr-batches-filter":
        await this.loadBatches();
        return;
      case "pr-batches-reload":
        await this.loadBatches();
        return;
      case "pr-batch-new":
        this.#setBatches({
          creating: { batchNo: "", vendorId: "", profileId: "", effective: "", sourceRef: "" },
          formError: "",
        });
        return;
      case "pr-batch-create":
        await this.#createBatch();
        return;
      case "pr-batch-cancel":
        this.#setBatches({ creating: null, formError: "" });
        return;
      case "pr-batch-open":
        await this.#openBatch(attrs.id ?? "");
        return;
      case "pr-batch-close":
        this.#setBatches({ open: null });
        return;
      case "pr-batch-import":
        await this.#importBatch();
        return;
      case "pr-batch-approve":
        await this.#approveBatch(attrs.id ?? "");
        return;
      case "pr-batch-apply":
        await this.#applyBatch(attrs.id ?? "");
        return;
      case "pr-batches-apply-due":
        await this.#applyDue();
        return;
      case "pr-profile-edit":
        this.#openProfileEdit(attrs.id ?? "");
        return;
      case "pr-profile-cancel":
        this.#setBatches({ profileEdit: null });
        return;
      case "pr-profile-save":
        await this.#saveProfile();
        return;
      case "pr-break-drop-item": {
        const editing = this.model.breaks.editing;
        const index = Number(attrs.index ?? "-1");
        if (editing !== null && Number.isInteger(index) && index >= 0 && index < editing.items.length) {
          const items = editing.items.filter((_, i) => i !== index);
          this.#setBreaks({ editing: { ...editing, items } });
        }
        return;
      }
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

  // -- the customer prices tab -------------------------------------------------------------------------

  async loadOverrides(): Promise<void> {
    const customerId = this.model.customers.customerIdInput.trim();
    if (customerId === "") {
      this.#setCustomers({ phase: "ready", rows: [], loadedFor: "", error: "", editing: null, floor: null });
      return;
    }
    this.#setCustomers({ ...emptyCustomers(), customerIdInput: this.model.customers.customerIdInput, notice: this.model.customers.notice });
    // A read or a write belongs to the customer it began for: the id is
    // captured when the load starts, and an answer that comes back after the
    // field moved is dropped, never shown under the new name.
    const asked = customerId;
    const result = await this.#door.overrides(customerId);
    if (this.model.customers.customerIdInput.trim() !== asked) return;
    if (!result.ok) {
      this.#setCustomers({
        phase: result.status === 403 ? "refused" : "error",
        loadedFor: asked,
        error: pricingRefusalWords(result.error, result.status, "read"),
      });
      return;
    }
    this.#setCustomers({ phase: "ready", rows: result.value, loadedFor: asked });
  }

  #openOverrideEditor(id: string): void {
    const row = id === "" ? undefined : this.model.customers.rows.find((r) => r.id === id);
    this.#setCustomers({
      editing: {
        id: row?.id ?? "",
        version: row?.version ?? 0,
        categoryId: row?.category_id ?? "",
        calc: row?.calc_type ?? "",
        discountInput: row?.discount_bps !== null && row?.discount_bps !== undefined ? (row.discount_bps / 100).toString() : "",
        markupInput: row?.markup_bps !== null && row?.markup_bps !== undefined ? (row.markup_bps / 100).toString() : "",
        priceUom: row?.price_uom ?? "",
        notes: row?.notes ?? "",
      },
      floor: null,
      formError: "",
      notice: "",
    });
  }

  #overrideWrite(): { write: PricingOverrideWrite | null; error: string } {
    const editing = this.model.customers.editing;
    if (editing === null) return { write: null, error: "" };
    if (editing.calc === "PCT_OFF_LIST") {
      const bps = bpsFromPctInput(editing.discountInput);
      if (bps === null) return { write: null, error: "Enter the discount as a percent, like 8." };
      return {
        write: {
          category_id: editing.categoryId,
          calc_type: editing.calc,
          price_cents: null,
          discount_bps: bps,
          markup_bps: null,
          price_uom: editing.priceUom.trim(),
          notes: editing.notes,
          version: editing.version,
        },
        error: "",
      };
    }
    if (editing.calc === "MARKUP_ON_COST") {
      const bps = bpsFromPctInput(editing.markupInput);
      if (bps === null) return { write: null, error: "Enter the markup as a percent, like 12." };
      return {
        write: {
          category_id: editing.categoryId,
          calc_type: editing.calc,
          price_cents: null,
          discount_bps: null,
          markup_bps: bps,
          price_uom: editing.priceUom.trim(),
          notes: editing.notes,
          version: editing.version,
        },
        error: "",
      };
    }
    return { write: null, error: "Choose what the price does: a percent off list, or a markup on cost." };
  }

  async #saveOverride(): Promise<void> {
    const editing = this.model.customers.editing;
    if (editing === null || this.model.customers.busy) return;
    if (editing.categoryId === "") {
      this.#setCustomers({ formError: "Choose the category this price names." });
      return;
    }
    const { write, error } = this.#overrideWrite();
    if (error !== "" || write === null) {
      this.#setCustomers({ formError: error });
      return;
    }
    const customerId = this.model.customers.loadedFor;
    this.#setCustomers({ busy: true, formError: "" });
    const preview = await this.#door.previewFloor({
      kind: "override",
      customer_id: customerId,
      ...(editing.id !== "" ? { id: editing.id } : {}),
      override: write,
    });
    if (!preview.ok) {
      this.#setCustomers({ busy: false, formError: pricingWriteError(preview.error, preview.status) });
      return;
    }
    if (needsFloorConfirm(preview.value)) {
      this.#setCustomers({
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
    await this.#writeOverride(customerId, write);
  }

  async #confirmOverride(): Promise<void> {
    const editing = this.model.customers.editing;
    const floor = this.model.customers.floor;
    if (editing === null || floor === null || floor.busy || this.model.customers.busy) return;
    const { write, error } = this.#overrideWrite();
    if (error !== "" || write === null) {
      this.#setCustomers({ formError: error });
      return;
    }
    this.#setCustomers({ floor: { ...floor, busy: true } });
    await this.#writeOverride(this.model.customers.loadedFor, write, floor.productIds);
  }

  async #writeOverride(customerId: string, write: PricingOverrideWrite, productIds: readonly string[] = []): Promise<void> {
    const editing = this.model.customers.editing;
    if (editing === null) return;
    let entityId = editing.id;
    if (editing.id === "") {
      const created = await this.#door.createOverride(customerId, write);
      if (!created.ok) {
        this.#setCustomers({ busy: false, floor: null, formError: pricingWriteError(created.error, created.status) });
        return;
      }
      entityId = created.value.id;
    } else {
      const updated = await this.#door.updateOverride(editing.id, write);
      if (!updated.ok) {
        this.#setCustomers({ busy: false, floor: this.model.customers.floor === null ? null : { ...this.model.customers.floor, busy: false }, formError: pricingWriteError(updated.error, updated.status) });
        return;
      }
    }
    if (productIds.length > 0) {
      const logged = await this.#door.confirmFloor({
        entity_type: "customer_override",
        entity_id: entityId,
        product_ids: productIds,
      });
      this.#setCustomers({
        busy: false,
        editing: null,
        floor: null,
        notice: logged.ok ? floorConfirmVerdict(logged.value) : floorConfirmVerdict(null),
      });
    } else {
      this.#setCustomers({ busy: false, editing: null, floor: null, notice: "Saved." });
    }
    await this.loadOverrides();
  }

  /** Delete is two taps: the first asks, the second carries the row's version. */
  async #deleteOverride(id: string): Promise<void> {
    if (id === "" || this.model.customers.busy) return;
    if (this.model.customers.confirmingId !== id) {
      this.#setCustomers({ confirmingId: id });
      return;
    }
    const row = this.model.customers.rows.find((r) => r.id === id);
    if (row === undefined) {
      this.#setCustomers({ confirmingId: "" });
      return;
    }
    this.#setCustomers({ busy: true, confirmingId: "" });
    const result = await this.#door.deleteOverride(id, { version: row.version });
    if (!result.ok) {
      this.#setCustomers({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setCustomers({ busy: false, notice: "Deleted. The account prices by its tier rules again." });
    await this.loadOverrides();
  }

  // -- the quantity breaks tab ---------------------------------------------------------------------------

  async loadBreaks(): Promise<void> {
    this.#setBreaks({ ...emptyBreaks(), notice: this.model.breaks.notice });
    const result = await this.#door.breaks();
    if (!result.ok) {
      this.#setBreaks({
        phase: result.status === 403 ? "refused" : "error",
        error: pricingRefusalWords(result.error, result.status, "read"),
      });
      return;
    }
    this.#setBreaks({ phase: "ready", rows: result.value });
  }

  async #openBreakEditor(id: string): Promise<void> {
    if (id === "") {
      this.#setBreaks({
        editing: {
          id: "",
          version: 0,
          code: "",
          nameInput: "",
          productId: "",
          categoryId: "",
          thresholdUom: "",
          active: true,
          items: [{ fromQty: "", toQty: "", type: "PCT_OFF", valueInput: "", uom: "" }],
        },
        floor: null,
        formError: "",
        notice: "",
      });
      return;
    }
    this.#setBreaks({ editing: null });
    const read = await this.#door.break(id);
    if (!read.ok) {
      this.#setBreaks({ formError: pricingWriteError(read.error, read.status), editing: null });
      return;
    }
    const row = read.value;
    this.#setBreaks({
      editing: {
        id: row.id,
        version: row.version,
        code: row.code,
        nameInput: row.name,
        productId: row.product_id,
        categoryId: row.category_id,
        thresholdUom: row.threshold_uom,
        active: row.is_active,
        items: row.items.map((item) => ({
          fromQty: item.from_qty.toString(),
          toQty: item.to_qty === null ? "" : item.to_qty.toString(),
          type: item.value_type,
          valueInput: breakDraftValue(item),
          uom: item.price_uom,
        })),
      },
      floor: null,
      formError: "",
      notice: "",
    });
  }

  /** The wire body the editor holds, or the words for the input it cannot encode. */
  #breakWrite(): { write: Record<string, unknown> | null; error: string } {
    const editing = this.model.breaks.editing;
    if (editing === null) return { write: null, error: "" };
    const items: Record<string, unknown>[] = [];
    for (const [index, draft] of editing.items.entries()) {
      const from = Number(draft.fromQty.trim());
      if (draft.fromQty.trim() === "" || !Number.isFinite(from) || from < 0) {
        return { write: null, error: `Tier ${index + 1}: enter the quantity the band starts at.` };
      }
      let to: number | null = null;
      if (draft.toQty.trim() !== "") {
        to = Number(draft.toQty.trim());
        if (!Number.isFinite(to) || to <= from) {
          return { write: null, error: `Tier ${index + 1}: the band's top must be a number above its start, or empty for the open top tier.` };
        }
      }
      const item: Record<string, unknown> = {
        from_qty: from,
        to_qty: to,
        value_type: draft.type,
        price_uom: draft.uom.trim() === "" ? editing.thresholdUom.trim() : draft.uom.trim(),
        discount_bps: null,
        discount_cents: null,
        surcharge_bps: null,
        surcharge_cents: null,
      };
      if (draft.type === "PCT_OFF" || draft.type === "SURCHARGE_PCT") {
        const bps = bpsFromPctInput(draft.valueInput);
        if (bps === null) return { write: null, error: `Tier ${index + 1}: enter the percent, like 5.` };
        if (draft.type === "PCT_OFF") item.discount_bps = bps;
        else item.surcharge_bps = bps;
      } else if (draft.type === "ABS_OFF" || draft.type === "SURCHARGE_ABS") {
        const cents = centsFromMoneyInput(draft.valueInput);
        if (cents === null) return { write: null, error: `Tier ${index + 1}: enter the amount in dollars, like 2.50.` };
        if (draft.type === "ABS_OFF") item.discount_cents = cents;
        else item.surcharge_cents = cents;
      } else {
        return { write: null, error: `Tier ${index + 1}: choose what the tier does.` };
      }
      items.push(item);
    }
    return {
      write: {
        code: editing.code.trim(),
        name: editing.nameInput.trim(),
        product_id: editing.productId.trim(),
        category_id: editing.categoryId,
        threshold_uom: editing.thresholdUom.trim(),
        items,
        version: editing.version,
        is_active: editing.active,
      },
      error: "",
    };
  }

  async #saveBreak(): Promise<void> {
    const editing = this.model.breaks.editing;
    if (editing === null || this.model.breaks.busy) return;
    const { write, error } = this.#breakWrite();
    if (error !== "" || write === null) {
      this.#setBreaks({ formError: error });
      return;
    }
    this.#setBreaks({ busy: true, formError: "" });
    const preview = await this.#door.previewFloor({
      kind: "quantity_break",
      ...(editing.id !== "" ? { id: editing.id } : {}),
      quantity_break: write as never,
    });
    if (!preview.ok) {
      this.#setBreaks({ busy: false, formError: pricingWriteError(preview.error, preview.status) });
      return;
    }
    if (needsFloorConfirm(preview.value)) {
      this.#setBreaks({
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
    await this.#writeBreak(write);
  }

  async #confirmBreak(): Promise<void> {
    const editing = this.model.breaks.editing;
    const floor = this.model.breaks.floor;
    if (editing === null || floor === null || floor.busy || this.model.breaks.busy) return;
    const { write, error } = this.#breakWrite();
    if (error !== "" || write === null) {
      this.#setBreaks({ formError: error });
      return;
    }
    this.#setBreaks({ floor: { ...floor, busy: true } });
    await this.#writeBreak(write, floor.productIds);
  }

  async #writeBreak(write: Record<string, unknown>, productIds: readonly string[] = []): Promise<void> {
    const editing = this.model.breaks.editing;
    if (editing === null) return;
    let entityId = editing.id;
    if (editing.id === "") {
      const created = await this.#door.createBreak(write);
      if (!created.ok) {
        this.#setBreaks({ busy: false, floor: null, formError: pricingWriteError(created.error, created.status) });
        return;
      }
      entityId = created.value.id;
    } else {
      const updated = await this.#door.updateBreak(editing.id, write);
      if (!updated.ok) {
        this.#setBreaks({ busy: false, floor: this.model.breaks.floor === null ? null : { ...this.model.breaks.floor, busy: false }, formError: pricingWriteError(updated.error, updated.status) });
        return;
      }
    }
    if (productIds.length > 0) {
      const logged = await this.#door.confirmFloor({
        entity_type: "quantity_break",
        entity_id: entityId,
        product_ids: productIds,
      });
      this.#setBreaks({
        busy: false,
        editing: null,
        floor: null,
        notice: logged.ok ? floorConfirmVerdict(logged.value) : floorConfirmVerdict(null),
      });
    } else {
      this.#setBreaks({ busy: false, editing: null, floor: null, notice: "Saved." });
    }
    await this.loadBreaks();
  }

  /** Retire is two taps: the first asks, the second carries the row's version. */
  async #deleteBreak(id: string): Promise<void> {
    if (id === "" || this.model.breaks.busy) return;
    if (this.model.breaks.confirmingId !== id) {
      this.#setBreaks({ confirmingId: id });
      return;
    }
    const row = this.model.breaks.rows.find((r) => r.id === id);
    if (row === undefined) {
      this.#setBreaks({ confirmingId: "" });
      return;
    }
    this.#setBreaks({ busy: true, confirmingId: "" });
    const result = await this.#door.deleteBreak(id, { version: row.version });
    if (!result.ok) {
      this.#setBreaks({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setBreaks({ busy: false, notice: "Retired. Resolves stop stacking the schedule's tiers." });
    await this.loadBreaks();
  }

  // -- the promotions and clearance tab -----------------------------------------------------------------

  /** The tab's first open: each panel's read runs once. */
  async loadPromosTab(): Promise<void> {
    await this.reloadPromotions();
    await this.reloadClearance();
  }

  async reloadPromotions(): Promise<void> {
    this.#setPromotions({ ...emptyPromotions(), notice: this.model.promos.promotions.notice });
    const result = await this.#door.promotions();
    if (!result.ok) {
      this.#setPromotions({
        phase: result.status === 403 ? "refused" : "error",
        error: pricingRefusalWords(result.error, result.status, "read"),
      });
      return;
    }
    this.#setPromotions({ phase: "ready", rows: result.value });
  }

  async reloadClearance(): Promise<void> {
    this.#setClearance({ ...emptyClearance(), notice: this.model.promos.clearance.notice });
    const result = await this.#door.clearance();
    if (!result.ok) {
      this.#setClearance({
        phase: result.status === 403 ? "refused" : "error",
        error: pricingRefusalWords(result.error, result.status, "read"),
      });
      return;
    }
    this.#setClearance({ phase: "ready", rows: result.value });
  }

  #openPromoEditor(id: string): void {
    const row = id === "" ? undefined : this.model.promos.promotions.rows.find((r) => r.id === id);
    this.#setPromotions({
      editing: {
        id: row?.id ?? "",
        version: row?.version ?? 0,
        code: row?.code ?? "",
        nameInput: row?.name ?? "",
        categoryId: row?.category_id ?? "",
        productId: row?.product_id ?? "",
        branchId: row?.branch_id ?? "",
        starts: (row?.starts_at ?? "").slice(0, 10),
        ends: (row?.ends_at ?? "").slice(0, 10),
        calc: row?.calc_type ?? "",
        valueInput: promoDraftValue(row),
        priceUom: row?.price_uom ?? "",
        priorityInput: row !== undefined ? row.priority.toString() : "",
        detail: row?.detail_text ?? "",
        active: row?.is_active ?? true,
      },
      formError: "",
      notice: "",
    });
  }

  /**
   * The wire body the editor holds, or the words for the input it cannot
   * encode. The window is required both ends: starts and ends go as
   * midnight instants on the named days.
   */
  #promoWrite(): { write: Record<string, unknown> | null; error: string } {
    const editing = this.model.promos.promotions.editing;
    if (editing === null) return { write: null, error: "" };
    if (editing.starts.trim() === "" || editing.ends.trim() === "") {
      return { write: null, error: "A promotion needs its window: a start day and an end day." };
    }
    const priority = Number(editing.priorityInput.trim());
    if (editing.priorityInput.trim() === "" || !Number.isFinite(priority) || !Number.isInteger(priority)) {
      return { write: null, error: "Enter the priority as a whole number, like 5." };
    }
    const body: Record<string, unknown> = {
      code: editing.code.trim(),
      name: editing.nameInput.trim(),
      product_id: editing.productId.trim(),
      category_id: editing.categoryId,
      branch_id: editing.branchId.trim(),
      starts_at: `${editing.starts.trim()}T00:00:00Z`,
      ends_at: `${editing.ends.trim()}T23:59:59Z`,
      calc_type: editing.calc,
      discount_bps: null,
      discount_cents: null,
      price_cents: null,
      price_uom: editing.priceUom.trim(),
      priority,
      detail_text: editing.detail,
      version: editing.version,
      is_active: editing.active,
    };
    if (editing.calc === "PCT_OFF") {
      const bps = bpsFromPctInput(editing.valueInput);
      if (bps === null) return { write: null, error: "Enter the discount as a percent, like 10." };
      body.discount_bps = bps;
    } else if (editing.calc === "AMT_OFF") {
      const cents = centsFromMoneyInput(editing.valueInput);
      if (cents === null) return { write: null, error: "Enter the amount in dollars, like 1.50." };
      body.discount_cents = cents;
    } else if (editing.calc === "FIXED_PRICE") {
      const cents = centsFromMoneyInput(editing.valueInput);
      if (cents === null) return { write: null, error: "Enter the fixed price in dollars, like 4.25." };
      if (editing.priceUom.trim() === "") return { write: null, error: "A fixed price needs its unit." };
      body.price_cents = cents;
    } else {
      return { write: null, error: "Choose what the promotion does to the price." };
    }
    return { write: body, error: "" };
  }

  async #savePromotion(): Promise<void> {
    const editing = this.model.promos.promotions.editing;
    if (editing === null || this.model.promos.promotions.busy) return;
    const { write, error } = this.#promoWrite();
    if (error !== "" || write === null) {
      this.#setPromotions({ formError: error });
      return;
    }
    this.#setPromotions({ busy: true, formError: "" });
    // The floor preview grades rule, override, policy and quantity break
    // only: a promotion is its own precedence level and saves straight
    // through, its refusal the server's own words.
    let entityId = editing.id;
    if (editing.id === "") {
      const created = await this.#door.createPromotion(write);
      if (!created.ok) {
        this.#setPromotions({ busy: false, formError: pricingWriteError(created.error, created.status) });
        return;
      }
      entityId = created.value.id;
    } else {
      const updated = await this.#door.updatePromotion(editing.id, write);
      if (!updated.ok) {
        this.#setPromotions({ busy: false, formError: pricingWriteError(updated.error, updated.status) });
        return;
      }
    }
    void entityId;
    this.#setPromotions({ busy: false, editing: null, notice: "Saved." });
    await this.reloadPromotions();
  }

  /** Retire is two taps: the first asks, the second carries the row's version. */
  async #deletePromotion(id: string): Promise<void> {
    if (id === "" || this.model.promos.promotions.busy) return;
    if (this.model.promos.promotions.confirmingId !== id) {
      this.#setPromotions({ confirmingId: id });
      return;
    }
    const row = this.model.promos.promotions.rows.find((r) => r.id === id);
    if (row === undefined) {
      this.#setPromotions({ confirmingId: "" });
      return;
    }
    this.#setPromotions({ busy: true, confirmingId: "" });
    const result = await this.#door.deletePromotion(id, { version: row.version });
    if (!result.ok) {
      this.#setPromotions({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setPromotions({ busy: false, notice: "Retired. The resolve stops offering it." });
    await this.reloadPromotions();
  }

  #openClearanceEditor(productId: string): void {
    const row = productId === "" ? undefined : this.model.promos.clearance.rows.find((r) => r.product_id === productId);
    this.#setClearance({
      editing: {
        productId: row?.product_id ?? "",
        reason: row?.reason ?? "",
        priceInput: row?.clearance_price_cents !== null && row?.clearance_price_cents !== undefined ? (row.clearance_price_cents / 100).toString() : "",
        priceUom: row?.price_uom ?? "",
        starts: (row?.started_at ?? "").slice(0, 10),
        ends: (row?.ends_at ?? "").slice(0, 10),
      },
      formError: "",
      notice: "",
    });
  }

  #clearanceWrite(): { productId: string; write: Record<string, unknown> | null; error: string } {
    const editing = this.model.promos.clearance.editing;
    if (editing === null) return { productId: "", write: null, error: "" };
    const productId = editing.productId.trim();
    if (productId === "") return { productId: "", write: null, error: "Type the product id the mark names." };
    if (editing.reason.trim() === "") return { productId: "", write: null, error: "Say why the item is marked down; the reason is what the row shows." };
    if (editing.starts.trim() === "") return { productId: "", write: null, error: "A mark needs its start day." };
    let priceCents: number | null = null;
    const priceUom = editing.priceUom.trim();
    if (editing.priceInput.trim() !== "") {
      priceCents = centsFromMoneyInput(editing.priceInput);
      if (priceCents === null) return { productId: "", write: null, error: "Enter the clearance price in dollars, like 3.50." };
      if (priceUom === "") return { productId: "", write: null, error: "A clearance price needs its unit: a price with no unit is not a price." };
    }
    return {
      productId,
      write: {
        reason: editing.reason.trim(),
        clearance_price_cents: priceCents,
        price_uom: priceUom,
        started_at: `${editing.starts.trim()}T00:00:00Z`,
        ends_at: editing.ends.trim() === "" ? "" : `${editing.ends.trim()}T23:59:59Z`,
      },
      error: "",
    };
  }

  async #saveClearance(): Promise<void> {
    const editing = this.model.promos.clearance.editing;
    if (editing === null || this.model.promos.clearance.busy) return;
    const { productId, write, error } = this.#clearanceWrite();
    if (error !== "" || write === null) {
      this.#setClearance({ formError: error });
      return;
    }
    this.#setClearance({ busy: true, formError: "" });
    const result = await this.#door.upsertClearance(productId, write);
    if (!result.ok) {
      this.#setClearance({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setClearance({ busy: false, editing: null, notice: "Saved." });
    await this.reloadClearance();
  }

  /** Take the mark off is two taps: the first asks, the second deletes. */
  async #deleteClearance(productId: string): Promise<void> {
    if (productId === "" || this.model.promos.clearance.busy) return;
    if (this.model.promos.clearance.confirmingId !== productId) {
      this.#setClearance({ confirmingId: productId });
      return;
    }
    this.#setClearance({ busy: true, confirmingId: "" });
    const result = await this.#door.deleteClearance(productId);
    if (!result.ok) {
      this.#setClearance({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setClearance({ busy: false, notice: "Taken off. The item prices through the waterfall again." });
    await this.reloadClearance();
  }

  // -- the rebates tab ------------------------------------------------------------------------------------

  async loadRebates(): Promise<void> {
    // The notice outlives the re-read a save triggers, and the accrual form
    // keeps what the person typed.
    this.#setRebates({ ...emptyRebates(), notice: this.model.rebates.notice, accrual: this.model.rebates.accrual });
    const result = await this.#door.rebatePrograms();
    if (!result.ok) {
      this.#setRebates({
        phase: result.status === 403 ? "refused" : "error",
        error: pricingRefusalWords(result.error, result.status, "read"),
      });
      return;
    }
    this.#setRebates({ phase: "ready", rows: result.value });
    // The ledger follows the first vendor the programs name, so the screen
    // demonstrates itself instead of waiting for a pick.
    const firstVendor = result.value.find((r) => r.is_active && r.vendor_id !== "") ?? result.value.find((r) => r.vendor_id !== "");
    if (firstVendor !== undefined) await this.loadLedger(firstVendor.vendor_id);
  }

  async loadLedger(vendorId: string): Promise<void> {
    if (vendorId === "") return;
    this.#setLedger({ phase: "loading", vendorId, error: "" });
    const result = await this.#door.vendorLedger(vendorId);
    if (!result.ok) {
      this.#setLedger({ phase: "error", error: pricingRefusalWords(result.error, result.status, "read") });
      return;
    }
    this.#setLedger({
      phase: "ready",
      vendorId: result.value.vendor_id,
      vendorName: result.value.vendor_name,
      totalCents: result.value.total_cents,
      count: result.value.accrual_count,
      rows: result.value.rows,
    });
  }

  #openRebateEditor(id: string): void {
    const row = id === "" ? undefined : this.model.rebates.rows.find((r) => r.id === id);
    this.#setRebates({
      editing: {
        id: row?.id ?? "",
        version: row?.version ?? 0,
        code: row?.code ?? "",
        nameInput: row?.name ?? "",
        vendorId: row?.vendor_id ?? "",
        categoryId: row?.category_id ?? "",
        productId: row?.product_id ?? "",
        basis: row?.basis ?? "",
        rateInput: row === undefined ? "" : (row.rate_bps ?? row.rate_cents_per_unit ?? null) === null ? "" : ((row.rate_bps ?? row.rate_cents_per_unit) as number / 100).toString(),
        starts: (row?.starts_at ?? "").slice(0, 10),
        ends: (row?.ends_at ?? "").slice(0, 10),
        notes: row?.notes ?? "",
        active: row?.is_active ?? true,
      },
      formError: "",
      notice: "",
    });
  }

  #rebateWrite(): { write: Record<string, unknown> | null; error: string } {
    const editing = this.model.rebates.editing;
    if (editing === null) return { write: null, error: "" };
    if (editing.vendorId.trim() === "") return { write: null, error: "A rebate program names its vendor; the vendor is immutable after create." };
    if (editing.starts.trim() === "") return { write: null, error: "A program needs its start day." };
    const body: Record<string, unknown> = {
      code: editing.code.trim(),
      name: editing.nameInput.trim(),
      vendor_id: editing.vendorId.trim(),
      product_id: editing.productId.trim(),
      category_id: editing.categoryId,
      basis: editing.basis,
      rate_bps: null,
      rate_cents_per_unit: null,
      starts_at: `${editing.starts.trim()}T00:00:00Z`,
      ends_at: editing.ends.trim() === "" ? "" : `${editing.ends.trim()}T23:59:59Z`,
      notes: editing.notes,
      version: editing.version,
      is_active: editing.active,
    };
    if (editing.basis === "PCT_OF_SALE" || editing.basis === "PCT_OF_COST") {
      const bps = bpsFromPctInput(editing.rateInput);
      if (bps === null) return { write: null, error: "Enter the rate as a percent, like 2." };
      body.rate_bps = bps;
    } else if (editing.basis === "AMT_PER_UNIT") {
      const cents = centsFromMoneyInput(editing.rateInput);
      if (cents === null) return { write: null, error: "Enter the rate in dollars per base unit, like 0.40." };
      body.rate_cents_per_unit = cents;
    } else {
      return { write: null, error: "Choose the basis the rate rides." };
    }
    return { write: body, error: "" };
  }

  async #saveRebate(): Promise<void> {
    const editing = this.model.rebates.editing;
    if (editing === null || this.model.rebates.busy) return;
    const { write, error } = this.#rebateWrite();
    if (error !== "" || write === null) {
      this.#setRebates({ formError: error });
      return;
    }
    this.#setRebates({ busy: true, formError: "" });
    if (editing.id === "") {
      const created = await this.#door.createRebate(write);
      if (!created.ok) {
        this.#setRebates({ busy: false, formError: pricingWriteError(created.error, created.status) });
        return;
      }
    } else {
      const updated = await this.#door.updateRebate(editing.id, write);
      if (!updated.ok) {
        this.#setRebates({ busy: false, formError: pricingWriteError(updated.error, updated.status) });
        return;
      }
    }
    this.#setRebates({ busy: false, editing: null, notice: "Saved." });
    await this.loadRebates();
  }

  /** Retire is two taps: the first asks, the second carries the row's version. */
  async #deleteRebate(id: string): Promise<void> {
    if (id === "" || this.model.rebates.busy) return;
    if (this.model.rebates.confirmingId !== id) {
      this.#setRebates({ confirmingId: id });
      return;
    }
    const row = this.model.rebates.rows.find((r) => r.id === id);
    if (row === undefined) {
      this.#setRebates({ confirmingId: "" });
      return;
    }
    this.#setRebates({ busy: true, confirmingId: "" });
    const result = await this.#door.deleteRebate(id, { version: row.version });
    if (!result.ok) {
      this.#setRebates({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setRebates({ busy: false, notice: "Retired. Covered lines stop accruing to it." });
    await this.loadRebates();
  }

  /** One sale event offered to the engine; the verdict is the server's own counts. */
  async #accrue(): Promise<void> {
    const accrual = this.model.rebates.accrual;
    if (accrual.busy) return;
    const qty = Number(accrual.qtyInput.trim());
    if (accrual.productId.trim() === "" || accrual.categoryId.trim() === "") {
      this.#setAccrual({ formError: "The line names its product and its category, by id." });
      return;
    }
    if (accrual.qtyInput.trim() === "" || !Number.isFinite(qty) || qty <= 0) {
      this.#setAccrual({ formError: "Enter the quantity in the product's base unit." });
      return;
    }
    const sale = centsFromMoneyInput(accrual.saleInput);
    const cost = centsFromMoneyInput(accrual.costInput);
    if (sale === null || cost === null) {
      this.#setAccrual({ formError: "Enter the extended sale and cost in dollars; both are the basis the engine reads." });
      return;
    }
    if (accrual.sourceType !== "ADJUSTMENT" && accrual.sourceId.trim() === "") {
      this.#setAccrual({ formError: "A sale or an order names its source id; the pair is the idempotency key." });
      return;
    }
    this.#setAccrual({ busy: true, formError: "", result: "" });
    const result = await this.#door.accrueRebates({
      source_type: accrual.sourceType,
      source_id: accrual.sourceId.trim(),
      ...(accrual.occurred.trim() === "" ? {} : { occurred_at: `${accrual.occurred.trim()}T12:00:00Z` }),
      lines: [
        {
          product_id: accrual.productId.trim(),
          category_id: accrual.categoryId.trim(),
          qty_base: qty,
          sale_cents: sale,
          ext_cost_cents: cost,
        },
      ],
    });
    if (!result.ok) {
      this.#setAccrual({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setAccrual({ busy: false, result: accrualResultWords(result.value) });
    if (this.model.rebates.ledger.vendorId !== "") await this.loadLedger(this.model.rebates.ledger.vendorId);
  }
  // -- the units of measure tab -----------------------------------------------------------------------------

  async loadUom(): Promise<void> {
    this.#setUom({ ...emptyUom(), probe: this.model.uom.probe, notice: this.model.uom.notice });
    const result = await this.#door.uomConversions();
    if (!result.ok) {
      this.#setUom({
        phase: result.status === 403 ? "refused" : "error",
        error: pricingRefusalWords(result.error, result.status, "read"),
      });
      return;
    }
    this.#setUom({ phase: "ready", rows: result.value });
  }

  #openUomEditor(id: string): void {
    const row = id === "" ? undefined : this.model.uom.rows.find((r) => r.id === id);
    this.#setUom({
      editing: {
        productId: row?.product_id ?? "",
        categoryId: row?.category_id ?? "",
        fromUom: row?.from_uom ?? "",
        toUom: row?.to_uom ?? "",
        factorInput: row !== undefined ? row.factor.toString() : "",
        roundUp: row?.round_up_at_pick ?? false,
        source: row?.source ?? "CLIENT_CONFIRMED",
        sourceNote: row?.source_note ?? "",
      },
      formError: "",
      notice: "",
    });
  }

  async #saveUom(): Promise<void> {
    const editing = this.model.uom.editing;
    if (editing === null || this.model.uom.busy) return;
    const factor = Number(editing.factorInput.trim());
    if (editing.fromUom.trim() === "" || editing.toUom.trim() === "") {
      this.#setUom({ formError: "An edge names both its ends: the unit the line is entered in, and the unit it becomes." });
      return;
    }
    if (editing.factorInput.trim() === "" || !Number.isFinite(factor) || factor <= 0) {
      this.#setUom({ formError: "Enter the factor: how many of the target unit one entered unit is." });
      return;
    }
    const row = editing.productId.trim() === "" ? undefined : this.model.uom.rows.find((r) => r.product_id === editing.productId && r.from_uom === editing.fromUom && r.to_uom === editing.toUom);
    this.#setUom({ busy: true, formError: "" });
    const result = await this.#door.upsertUom({
      id: row?.id ?? "",
      product_id: editing.productId.trim(),
      category_id: editing.categoryId.trim(),
      from_uom: editing.fromUom.trim(),
      to_uom: editing.toUom.trim(),
      factor,
      round_up_at_pick: editing.roundUp,
      source: editing.source,
      source_note: editing.sourceNote,
      is_active: true,
      version: row?.version ?? 0,
    });
    if (!result.ok) {
      this.#setUom({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setUom({ busy: false, editing: null, notice: "Saved." });
    await this.loadUom();
  }

  /** Take an edge off is two taps: the first asks, the second deletes. */
  async #deleteUom(id: string): Promise<void> {
    if (id === "" || this.model.uom.busy) return;
    if (this.model.uom.confirmingId !== id) {
      this.#setUom({ confirmingId: id });
      return;
    }
    this.#setUom({ busy: true, confirmingId: "" });
    const result = await this.#door.deleteUom(id);
    if (!result.ok) {
      this.#setUom({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setUom({ busy: false, notice: "Deleted. Lines in that unit can no longer be converted." });
    await this.loadUom();
  }

  /** The probe: the typed question posted whole, the engine's answer shown. */
  async #runPlan(): Promise<void> {
    const probe = this.model.uom.probe;
    if (probe.busy) return;
    const qty = Number(probe.qtyInput.trim());
    if (probe.productId.trim() === "" || probe.uomInput.trim() === "") {
      this.#setProbe({ error: "The probe names the product and the unit the line is entered in." });
      return;
    }
    if (probe.qtyInput.trim() === "" || !Number.isFinite(qty) || qty <= 0) {
      this.#setProbe({ error: "Enter the quantity as a number above zero." });
      return;
    }
    this.#setProbe({ busy: true, error: "", result: null });
    const result = await this.#door.planUom({
      product_id: probe.productId.trim(),
      ...(probe.categoryId.trim() === "" ? {} : { category_id: probe.categoryId.trim() }),
      qty_entered: qty,
      uom_entered: probe.uomInput.trim(),
      ...(probe.pickUom.trim() === "" ? {} : { pick_uom: probe.pickUom.trim() }),
      ...(probe.weightUom.trim() === "" ? {} : { weight_uom: probe.weightUom.trim() }),
    });
    if (!result.ok) {
      // A missing chain is a real answer here, not a crash: the refusal
      // names the unit that could not be reached, which is what the admin
      // needs in order to author the row.
      this.#setProbe({ busy: false, error: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setProbe({ busy: false, result: result.value });
  }
  // -- the batches and imports tab ---------------------------------------------------------------------------

  async loadBatches(): Promise<void> {
    this.#setBatches({ ...emptyBatches(), statusFilter: this.model.batches.statusFilter, notice: this.model.batches.notice });
    const result = await this.#door.batches(this.model.batches.statusFilter);
    if (!result.ok) {
      this.#setBatches({
        phase: result.status === 403 ? "refused" : "error",
        error: pricingRefusalWords(result.error, result.status, "read"),
      });
      return;
    }
    this.#setBatches({ phase: "ready", rows: result.value });
    if (this.model.batches.profilesPhase === "idle") {
      const profiles = await this.#door.importProfiles();
      if (profiles.ok) this.#setBatches({ profiles: profiles.value, profilesPhase: "ready" });
      else this.#setBatches({ profilesPhase: "error", profilesError: profiles.error });
    }
  }

  async #createBatch(): Promise<void> {
    const creating = this.model.batches.creating;
    if (creating === null || this.model.batches.busy) return;
    if (creating.batchNo.trim() === "" || creating.sourceRef.trim() === "") {
      this.#setBatches({ formError: "A batch names itself (the number) and names its source file." });
      return;
    }
    this.#setBatches({ busy: true, formError: "" });
    const result = await this.#door.createBatch({
      batch_no: creating.batchNo.trim(),
      vendor_id: creating.vendorId.trim(),
      profile_id: creating.profileId.trim(),
      effective_date: creating.effective.trim(),
      source_ref: creating.sourceRef.trim(),
    });
    if (!result.ok) {
      this.#setBatches({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setBatches({ busy: false, creating: null, notice: "The batch is open in draft. Import the manufacturer's file into it." });
    await this.loadBatches();
    await this.#openBatch(result.value.id);
  }

  async #openBatch(id: string): Promise<void> {
    if (id === "") return;
    this.#setBatches({
      open: {
        id,
        linesPhase: "loading",
        lines: [],
        linesError: "",
        importText: "",
        importBusy: false,
        importError: "",
        importResult: "",
        actionBusy: false,
        actionError: "",
        actionNotice: "",
      },
    });
    const lines = await this.#door.batchLines(id);
    if (this.model.batches.open?.id !== id) return;
    if (!lines.ok) {
      this.#setOpen({ linesPhase: "error", linesError: pricingRefusalWords(lines.error, lines.status, "read") });
      return;
    }
    this.#setOpen({ linesPhase: "ready", lines: lines.value });
  }

  async #importBatch(): Promise<void> {
    const open = this.model.batches.open;
    if (open === null || open.importBusy) return;
    if (open.importText.trim() === "") {
      this.#setOpen({ importError: "Paste or load the manufacturer's CSV first; the import reads the file whole." });
      return;
    }
    this.#setOpen({ importBusy: true, importError: "", importResult: "" });
    const result = await this.#door.importBatch(open.id, open.importText);
    if (!result.ok) {
      this.#setOpen({ importBusy: false, importError: pricingWriteError(result.error, result.status) });
      return;
    }
    await this.loadBatches();
    await this.#openBatch(open.id);
    // The result outlives the reopen the reload triggers: the answer is the
    // server's counts, and the person is mid work.
    this.#setOpen({ importBusy: false, importResult: importResultWords(result.value) });
  }

  async #approveBatch(id: string): Promise<void> {
    if (id === "" || this.model.batches.open?.actionBusy) return;
    this.#setOpen({ actionBusy: true, actionError: "", actionNotice: "" });
    const result = await this.#door.approveBatch(id);
    if (!result.ok) {
      this.#setOpen({ actionBusy: false, actionError: pricingWriteError(result.error, result.status) });
      return;
    }
    await this.loadBatches();
    await this.#openBatch(id);
    this.#setOpen({ actionBusy: false, actionNotice: "Approved. It applies on its effective date, or when someone applies it." });
  }

  async #applyBatch(id: string): Promise<void> {
    if (id === "" || this.model.batches.open?.actionBusy) return;
    this.#setOpen({ actionBusy: true, actionError: "", actionNotice: "" });
    const result = await this.#door.applyBatch(id);
    if (!result.ok) {
      this.#setOpen({ actionBusy: false, actionError: pricingWriteError(result.error, result.status) });
      return;
    }
    await this.loadBatches();
    await this.#openBatch(id);
    this.#setOpen({ actionBusy: false, actionNotice: `Applied: ${result.value.applied} prices written to the retail anchor.` });
  }

  async #applyDue(): Promise<void> {
    if (this.model.batches.busy) return;
    this.#setBatches({ busy: true, formError: "" });
    const result = await this.#door.applyDueBatches();
    if (!result.ok) {
      this.#setBatches({ busy: false, formError: pricingWriteError(result.error, result.status) });
      return;
    }
    this.#setBatches({ busy: false, notice: `${result.value.applied} due ${result.value.applied === 1 ? "batch" : "batches"} applied.` });
    await this.loadBatches();
  }

  #openProfileEdit(id: string): void {
    const row = id === "" ? undefined : this.model.batches.profiles.find((p) => p.id === id);
    if (row === undefined) return;
    this.#setBatches({
      profileEdit: {
        id: row.id,
        name: row.name,
        skuCol: row.mapping.sku_col ?? "",
        costCol: row.mapping.cost_col ?? "",
        retailCol: row.mapping.retail_col ?? "",
        tradeCol: row.mapping.trade_col ?? "",
        effectiveCol: row.mapping.effective_col ?? "",
        busy: false,
        error: "",
      },
    });
  }

  async #saveProfile(): Promise<void> {
    const edit = this.model.batches.profileEdit;
    if (edit === null || edit.busy) return;
    if (edit.skuCol.trim() === "") {
      this.#setBatches({ profileEdit: { ...edit, error: "The mapping must at least name the SKU column." } });
      return;
    }
    this.#setBatches({ profileEdit: { ...edit, busy: true, error: "" } });
    const mapping: Record<string, string> = { sku_col: edit.skuCol.trim() };
    if (edit.costCol.trim() !== "") mapping.cost_col = edit.costCol.trim();
    if (edit.retailCol.trim() !== "") mapping.retail_col = edit.retailCol.trim();
    if (edit.tradeCol.trim() !== "") mapping.trade_col = edit.tradeCol.trim();
    if (edit.effectiveCol.trim() !== "") mapping.effective_col = edit.effectiveCol.trim();
    const result = await this.#door.updateImportProfile(edit.id, mapping);
    if (!result.ok) {
      this.#setBatches({ profileEdit: { ...edit, busy: false, error: pricingWriteError(result.error, result.status) } });
      return;
    }
    this.#setBatches({ profileEdit: null, notice: "The mapping is saved; the next import reads the file with it." });
    const profiles = await this.#door.importProfiles();
    if (profiles.ok) this.#setBatches({ profiles: profiles.value, profilesPhase: "ready" });
  }
}

/** A promotion's value as the editor's text, from the server's own figure. */
function promoDraftValue(row:
  | {
      readonly calc_type: string;
      readonly discount_bps: number | null;
      readonly discount_cents: number | null;
      readonly price_cents: number | null;
    }
  | undefined
): string {
  if (row === undefined) return "";
  if (row.calc_type === "PCT_OFF") return row.discount_bps === null ? "" : (row.discount_bps / 100).toString();
  if (row.calc_type === "AMT_OFF") return row.discount_cents === null ? "" : (row.discount_cents / 100).toString();
  if (row.calc_type === "FIXED_PRICE") return row.price_cents === null ? "" : (row.price_cents / 100).toString();
  return "";
}

/** One tier's value as the editor's text, from the server's own figure. */
function breakDraftValue(item: {
  readonly value_type: string;
  readonly discount_bps: number | null;
  readonly discount_cents: number | null;
  readonly surcharge_bps: number | null;
  readonly surcharge_cents: number | null;
}): string {
  if (item.value_type === "PCT_OFF" || item.value_type === "SURCHARGE_PCT") {
    const bps = item.value_type === "PCT_OFF" ? item.discount_bps : item.surcharge_bps;
    return bps === null ? "" : (bps / 100).toString();
  }
  const cents = item.value_type === "ABS_OFF" ? item.discount_cents : item.surcharge_cents;
  return cents === null ? "" : (cents / 100).toString();
}

/** The api-client door, typed once for the app-root's construction. */
export function pricingController(api: PricingApi, notify: Notify): PricingController {
  return new PricingController(api, notify);
}
