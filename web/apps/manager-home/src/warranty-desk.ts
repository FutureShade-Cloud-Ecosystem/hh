// Warranty's state machine: which tab is up, what each tab's load answered,
// and every press. Pure state and calls, no DOM: the views render the model,
// the app element wires the host's clicks and fields in, as the procurement
// desk does in the buying and stock app.
//
// THE SCOPE RULE. The branch on every board, list and report is the caller's
// own resolved branch (the branches route), never a person's typing; the
// boards answer for the whole org when the all chip is picked, and the server
// applies the caller's grants to every read. A branch the caller cannot reach
// is the core's own refusal, in its own words.
//
// THE REFUSAL RULE. A load the core refuses replaces that tab's pane with the
// server's own words. A press the core refuses keeps the pane standing and
// puts the words, walled in plain terms when the body is the shared Forbidden,
// in the control's own error slot.
//
// THE MONEY RULE. Settlement and the recovery log are server calls and
// nothing else: no optimistic figures, no queue, no outbox. The answer's own
// numbers are what the screen shows, and a refusal keeps the sheet standing
// with the server's words in it.
import { WarrantyApi, type BoardColumn, type Claim, type ChecklistState, type ClaimEvent, type DefectFilter, type DefectsTab, type IntakePhoto, type RecoveryAction, type RecoveryBoardModel, type ReportBundle, type SlaBoardModel, type SlaTarget, type SettlementPosting, type VendorCreditsTab, type VendorProfile } from "./warranty-api.js";
import type { Result } from "@hardscapehouse/api-client";
import {
  CLAIM_CHANNELS,
  OUTCOMES,
  RECOVERY_ACTIONS,
  VENDOR_STATUSES,
  advanceRefusalWords,
  centsFromDollars,
  channelWord,
  dollarsTyped,
  phaseWord,
  recoveryRefusalWords,
  settleRefusalWords,
  slaRefusalWords,
  vendorRefusalWords,
  vendorStatusWord,
} from "./warranty-logic.js";

/** The tabs the warranty view carries, in their order on the chip row. */
export type WTab = "board" | "claims" | "defects" | "vendors-credits" | "vendors" | "recovery" | "sla" | "reports";

export const WARRANTY_TABS: readonly WTab[] = ["board", "claims", "defects", "vendors-credits", "vendors", "recovery", "sla", "reports"];

export const WARRANTY_TAB_TITLE: Readonly<Record<WTab, string>> = Object.freeze({
  board: "Board",
  claims: "Claims",
  defects: "Defects",
  "vendors-credits": "Vendors and credits",
  vendors: "Vendor registry",
  recovery: "Recovery",
  sla: "Service levels",
  reports: "Reports",
});

/** One tab's load state. A refusal keeps the server's own words; an error keeps a try again. */
export interface TabModel<T> {
  readonly phase: "loading" | "ready" | "error" | "refused";
  readonly error: string;
  readonly refusal: string;
  readonly data: T;
}

const tabModel = <T>(data: T): TabModel<T> => ({ phase: "loading", error: "", refusal: "", data });

export interface ClaimsTab {
  readonly phaseFilter: string;
  readonly rows: readonly Claim[];
}

// -- change 6 item 5: defects and claim evidence drawer ------------------------------------------------

/** One event on a claim's "What happened" timeline; the view renders the
 *  sentence the core answered, with a when the desk stamps in people words. */
export interface ClaimEvidenceEvent {
  readonly id: string;
  readonly when: string;
  readonly sentence: string;
}

/** The drawer opened from a defect's Claim cell. Holds the claim's evidence
 *  (photos), what happened, and the claim rows the claim-evidence board shows.
 *  Null when the drawer is closed. */
export interface ClaimEvidenceModel {
  readonly phase: "loading" | "ready" | "error" | "refused";
  readonly error: string;
  readonly refusal: string;
  readonly defectId: string;
  readonly claimId: string;
  readonly claimNo: string;
  readonly returnNo: string;
  readonly customerName: string;
  readonly productText: string;
  readonly quantityText: string;
  readonly lotOrBatch: string;
  readonly costCents: number;
  readonly vendorName: string;
  readonly agreementSummary: string;
  readonly statusBadge: string;
  readonly photos: readonly IntakePhoto[];
  readonly photoError: string;
  readonly timeline: readonly ClaimEvidenceEvent[];
}

const emptyClaimEvidence = (): ClaimEvidenceModel => ({
  phase: "loading",
  error: "",
  refusal: "",
  defectId: "",
  claimId: "",
  claimNo: "",
  returnNo: "",
  customerName: "",
  productText: "",
  quantityText: "",
  lotOrBatch: "",
  costCents: 0,
  vendorName: "",
  agreementSummary: "",
  statusBadge: "",
  photos: [],
  photoError: "",
  timeline: [],
});

export interface RecoveryTab {
  readonly board: RecoveryBoardModel | null;
  readonly includeReconciled: boolean;
  readonly openLogFor: string;
  readonly log: readonly RecoveryAction[];
  readonly logError: string;
  readonly logNotice: string;
  readonly logBusy: boolean;
}

export interface SlaTab {
  readonly board: SlaBoardModel | null;
  readonly targets: readonly SlaTarget[];
  readonly includeAcknowledged: boolean;
  readonly sweeping: boolean;
  readonly notice: string;
}

export interface VendorTab {
  readonly rows: readonly VendorProfile[];
  readonly openId: string;
  /** The expanded profile, read whole through the single-profile route (overrides and docs included). */
  readonly openProfile: VendorProfile | null;
  readonly openError: string;
  readonly editing: VendorProfile | null;
  readonly saving: boolean;
  readonly notice: string;
  readonly overrideFor: string;
}

export interface ClaimDetailModel {
  readonly id: string;
  readonly phase: "loading" | "ready" | "error" | "refused";
  readonly error: string;
  readonly refusal: string;
  readonly notice: string;
  readonly claim: Claim | null;
  readonly checklist: readonly ChecklistState[];
  readonly events: readonly ClaimEvent[];
  readonly postings: readonly SettlementPosting[];
  readonly photos: readonly IntakePhoto[];
  readonly photoError: string;
  readonly busy: boolean;
  readonly settleOpen: boolean;
  readonly assignOpen: boolean;
  readonly checklistBusyFor: string;
}

export interface WarrantyModel {
  readonly open: boolean;
  readonly tab: WTab;
  readonly scopeAll: boolean;
  readonly branchName: string;
  /** The vendor pick list the capture sheet and the assign sheet share, read once on open. */
  readonly vendorOptions: readonly { readonly vendor_id: string; readonly vendor_name: string }[];
  readonly board: TabModel<readonly BoardColumn[]>;
  readonly claims: TabModel<ClaimsTab>;
  readonly vendors: TabModel<VendorTab>;
  readonly recovery: TabModel<RecoveryTab>;
  readonly sla: TabModel<SlaTab>;
  readonly reports: TabModel<ReportBundle | null>;
  readonly detail: ClaimDetailModel | null;
  readonly captureOpen: boolean;
  readonly captureBusy: boolean;
  readonly captureError: string;
  /** Change 6 item 5: the Defects tab (lines of damaged product, claim or shrinkage). */
  readonly defects: TabModel<DefectsTab>;
  /** Change 6 item 5: the Vendors and credits tab (each vendor's agreement and what they owe). */
  readonly vendorCredits: TabModel<VendorCreditsTab>;
  /** Change 6 item 5: the claim-evidence drawer opened from a defect's Claim cell. */
  readonly claimEvidence: ClaimEvidenceModel | null;
}

const initialModel = (branchName: string): WarrantyModel => ({
  open: false,
  tab: "board",
  scopeAll: true,
  branchName,
  vendorOptions: [],
  board: tabModel<readonly BoardColumn[]>([]),
  claims: tabModel<ClaimsTab>({ phaseFilter: "", rows: [] }),
  vendors: tabModel<VendorTab>({ rows: [], openId: "", openProfile: null, openError: "", editing: null, saving: false, notice: "", overrideFor: "" }),
  recovery: tabModel<RecoveryTab>({
    board: null,
    includeReconciled: false,
    openLogFor: "",
    log: [],
    logError: "",
    logNotice: "",
    logBusy: false,
  }),
  sla: tabModel<SlaTab>({
    board: null,
    targets: [],
    includeAcknowledged: false,
    sweeping: false,
    notice: "",
  }),
  reports: tabModel<ReportBundle | null>(null),
  detail: null,
  captureOpen: false,
  captureBusy: false,
  captureError: "",
  defects: tabModel<DefectsTab>({ rows: [], threshold_cents: 5000, filter: "" }),
  vendorCredits: tabModel<VendorCreditsTab>({ rows: [], openVendorId: "", openDetail: null, openError: "", requestingFor: "", requestNotice: "", requestError: "" }),
  claimEvidence: null,
});

/** The seam the app element answers: a repaint, a field's current words, and
 *  a select's picked row (the pick lists carry words only, so their stored
 *  values come back by row). */
export interface WarrantyHost {
  readonly repaint: () => void;
  readonly field: (name: string) => string;
  readonly index: (name: string) => number;
}

export class WarrantyDesk {
  readonly #api: WarrantyApi;
  readonly #host: WarrantyHost;
  readonly #photoUrls = new Map<string, string>();
  readonly #photoAlts = new Map<string, string>();
  #branchId = "";
  #branchName = "";
  #model: WarrantyModel;

  constructor(api: WarrantyApi, host: WarrantyHost) {
    this.#api = api;
    this.#host = host;
    this.#model = initialModel("");
  }

  model(): WarrantyModel {
    return this.#model;
  }

  /** The caller's own branch, resolved once by the app through the branches route. */
  setBranch(branchId: string, branchName: string): void {
    this.#branchId = branchId;
    this.#branchName = branchName;
    this.#model = { ...this.#model, branchName };
    this.#host.repaint();
  }

  /** Sign out: everything held is dropped, nothing persists. */
  reset(): void {
    this.#model = initialModel("");
    this.#photoUrls.forEach((url) => URL.revokeObjectURL(url));
    this.#photoUrls.clear();
    this.#photoAlts.clear();
  }

  /** First show of the view: the vendor pick list once, then the default tab. */
  open(): void {
    this.#model = { ...this.#model, open: true };
    void this.#loadVendorOptions();
    void this.#loadTab(this.#model.tab);
  }

  /** The pick list behind the capture sheet's and the assign sheet's vendor selects. */
  async #loadVendorOptions(): Promise<void> {
    const result = await this.#api.vendorProfiles();
    if (result.ok) {
      this.#model = {
        ...this.#model,
        vendorOptions: result.value
          .filter((v) => v.vendor_id !== "")
          .map((v) => ({ vendor_id: v.vendor_id, vendor_name: v.vendor_name })),
      };
      this.#host.repaint();
    }
  }

  /**
   * The one-shot field seeds: a form's starting values ride the DOM, set once
   * per form after the render that drew it, never re-set over a person's
   * typing. The app element calls this from its own updated().
   */
  seedFields(set: (name: string, value: string) => void): void {
    const detail = this.#model.detail;
    if (detail?.settleOpen === true && detail.claim !== null && this.#seededSettleFor !== detail.id) {
      this.#seededSettleFor = detail.id;
      set("w-settle-expected", dollarsTyped(detail.claim.expected_cents));
    }
    const vendors = this.#model.vendors.data;
    if (vendors.editing !== null && this.#seededVendorFor !== vendors.editing.vendor_id) {
      this.#seededVendorFor = vendors.editing.vendor_id;
      const p = vendors.editing;
      // The pick lists carry words only, so the seed is the word the row shows.
      set("w-vendor-status", vendorStatusWord(p.status));
      set("w-vendor-channel", channelWord(p.channel));
      set("w-vendor-term", p.term_months === null ? "" : String(p.term_months));
      set("w-vendor-ref", p.channel_ref);
      set("w-vendor-coverage", p.coverage);
      set("w-vendor-notes", p.notes);
    }
  }

  #seededSettleFor = "";
  #seededVendorFor = "";

  photoUrl(photoId: string): string | undefined {
    return this.#photoUrls.get(photoId);
  }

  /** The words a photo's img carries as its alt, set after render beside the address. */
  photoAlt(photoId: string): string {
    return this.#photoAlts.get(photoId) ?? "A photo attached to the claim";
  }

  /** The one field listener the desk needs: the chase form's action pick reshapes the form. */
  onField(name: string): void {
    if (name === "w-recovery-action") {
      this.#host.repaint();
    }
  }

  /**
   * Every warranty control's press. Answers true when the action was the
   * desk's, so the app's own listener stops there.
   */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action") ?? "";
    if (!action.startsWith("w-")) return false;
    switch (action) {
      case "w-tab":
        this.#pickTab(control.getAttribute("data-tab") ?? "");
        return true;
      case "w-scope":
        this.#pickScope(control.getAttribute("data-scope") ?? "");
        return true;
      case "w-reload":
        void this.#loadTab(this.#model.tab);
        return true;
      case "w-open-claim":
        void this.#openClaim(control.getAttribute("data-claim-id") ?? "");
        return true;
      case "w-close-claim":
        this.#model = { ...this.#model, detail: null };
        this.#host.repaint();
        return true;
      case "w-capture-toggle":
        this.#model = { ...this.#model, captureOpen: !this.#model.captureOpen, captureError: "" };
        this.#host.repaint();
        return true;
      case "w-capture":
        void this.#capture();
        return true;
      case "w-tick":
        if (this.#model.detail !== null) {
          void this.#tick(this.#model.detail.id, control.getAttribute("data-item-key") ?? "", control.getAttribute("data-checked") === "true");
        }
        return true;
      case "w-advance":
        if (this.#model.detail !== null) {
          void this.#advance(this.#model.detail.id, control.getAttribute("data-to-phase") ?? "");
        }
        return true;
      case "w-settle-toggle":
        this.#seededSettleFor = "";
        this.#patchDetail({ settleOpen: !this.#model.detail?.settleOpen, notice: "", refusal: "", error: "" });
        return true;
      case "w-settle":
        void this.#settle();
        return true;
      case "w-assign-toggle":
        this.#patchDetail({ assignOpen: !this.#model.detail?.assignOpen, notice: "", refusal: "", error: "" });
        return true;
      case "w-assign":
        void this.#assign();
        return true;
      case "w-evidence":
        void this.#attachEvidence();
        return true;
      case "w-vendor-open": {
        const vendors = this.#model.vendors.data;
        const id = control.getAttribute("data-vendor-id") ?? "";
        if (id === "") return true;
        const opening = vendors.openId !== id;
        this.#model = {
          ...this.#model,
          vendors: {
            ...this.#model.vendors,
            data: { ...vendors, openId: opening ? id : "", openProfile: null, openError: "" },
          },
        };
        this.#host.repaint();
        if (opening) void this.#expandVendor(id);
        return true;
      }
      case "w-vendor-edit": {
        const vendors = this.#model.vendors.data;
        const id = control.getAttribute("data-vendor-id") ?? "";
        const editing = vendors.rows.find((v) => v.vendor_id === id) ?? null;
        this.#seededVendorFor = "";
        this.#model = { ...this.#model, vendors: { ...this.#model.vendors, data: { ...vendors, editing, notice: "" } } };
        this.#host.repaint();
        return true;
      }
      case "w-vendor-cancel": {
        const vendors = this.#model.vendors.data;
        this.#seededVendorFor = "";
        this.#model = { ...this.#model, vendors: { ...this.#model.vendors, data: { ...vendors, editing: null } } };
        this.#host.repaint();
        return true;
      }
      case "w-vendor-save":
        void this.#saveVendor();
        return true;
      case "w-vendor-refresh":
        void this.#loadVendors();
        return true;
      case "w-recovery-chase": {
        const recovery = this.#model.recovery.data;
        const claimId = control.getAttribute("data-claim-id") ?? "";
        const openLogFor = recovery.openLogFor === claimId ? "" : claimId;
        this.#model = {
          ...this.#model,
          recovery: { ...this.#model.recovery, data: { ...recovery, openLogFor, log: [], logError: "", logNotice: "" } },
        };
        this.#host.repaint();
        if (openLogFor !== "") void this.#loadChaseLog(openLogFor);
        return true;
      }
      case "w-recovery-log": {
        const claimId = this.#model.recovery.data.openLogFor;
        if (claimId !== "") void this.#logChase(claimId);
        return true;
      }
      case "w-recovery-toggle":
        this.#model = {
          ...this.#model,
          recovery: {
            ...this.#model.recovery,
            data: { ...this.#model.recovery.data, includeReconciled: !this.#model.recovery.data.includeReconciled },
          },
        };
        void this.#loadRecovery();
        return true;
      case "w-sla-sweep":
        void this.#sweep();
        return true;
      case "w-sla-include-ack": {
        const sla = this.#model.sla.data;
        this.#model = {
          ...this.#model,
          sla: { ...this.#model.sla, data: { ...sla, includeAcknowledged: !sla.includeAcknowledged } },
        };
        void this.#loadSla();
        return true;
      }
      case "w-sla-ack":
        void this.#ack(control.getAttribute("data-claim-id") ?? "", control.getAttribute("data-alert-id") ?? "");
        return true;
      case "w-claims-phase": {
        const claims = this.#model.claims.data;
        this.#model = {
          ...this.#model,
          claims: { ...this.#model.claims, data: { ...claims, phaseFilter: control.getAttribute("data-phase") ?? "" } },
        };
        void this.#loadClaims();
        return true;
      }
      case "w-override-toggle": {
        const vendors = this.#model.vendors.data;
        const vendorId = control.getAttribute("data-vendor-id") ?? "";
        this.#model = {
          ...this.#model,
          vendors: { ...this.#model.vendors, data: { ...vendors, overrideFor: vendors.overrideFor === vendorId ? "" : vendorId } },
        };
        this.#host.repaint();
        return true;
      }
      case "w-override-add":
        void this.#addOverride();
        return true;
      // -- change 6 item 5: defects tab and claim-evidence drawer -----------------------------------------
      case "w-defects-filter": {
        const filter = (control.getAttribute("data-filter") ?? "") as DefectFilter;
        const defects = this.#model.defects.data;
        if (filter === defects.filter) return true;
        this.#model = {
          ...this.#model,
          defects: { ...this.#model.defects, data: { ...defects, filter } },
        };
        this.#host.repaint();
        return true;
      }
      case "w-defects-reload":
        void this.#loadDefects(true);
        return true;
      case "w-defects-open-claim": {
        const defectId = control.getAttribute("data-defect-id") ?? "";
        void this.#openClaimEvidence(defectId);
        return true;
      }
      case "w-evidence-close":
        this.#closeClaimEvidence();
        return true;
      // -- change 6 item 5: vendors and credits tab ---------------------------------------------------------
      case "w-vc-reload":
        void this.#loadVendorCredits(true);
        return true;
      case "w-vc-open": {
        const vendorId = control.getAttribute("data-vendor-id") ?? "";
        void this.#expandVendorCredit(vendorId);
        return true;
      }
      case "w-vc-request": {
        const vendorId = control.getAttribute("data-vendor-id") ?? "";
        void this.#requestVendorCreditNote(vendorId);
        return true;
      }
      default:
        return true;
    }
  }

  // -- loads --------------------------------------------------------------------------------------------

  #scopeId(): string {
    return this.#model.scopeAll ? "" : this.#branchId;
  }

  async #loadTab(tab: WTab): Promise<void> {
    if (tab === "board") await this.#loadBoard();
    if (tab === "claims") await this.#loadClaims();
    if (tab === "defects") await this.#loadDefects();
    if (tab === "vendors-credits") await this.#loadVendorCredits();
    if (tab === "vendors") await this.#loadVendors();
    if (tab === "recovery") await this.#loadRecovery();
    if (tab === "sla") await this.#loadSla();
    if (tab === "reports") await this.#loadReports();
  }

  #tabState(tab: WTab): TabModel<unknown> {
    if (tab === "board") return this.#model.board;
    if (tab === "claims") return this.#model.claims;
    if (tab === "defects") return this.#model.defects;
    if (tab === "vendors-credits") return this.#model.vendorCredits;
    if (tab === "vendors") return this.#model.vendors;
    if (tab === "recovery") return this.#model.recovery;
    if (tab === "sla") return this.#model.sla;
    return this.#model.reports;
  }

  #pickTab(tab: string): void {
    if (!WARRANTY_TABS.some((t) => t === tab) || tab === this.#model.tab) return;
    this.#model = { ...this.#model, tab: tab as WTab, detail: null, captureOpen: false, claimEvidence: null };
    this.#host.repaint();
    if (this.#tabState(this.#model.tab).phase === "loading") void this.#loadTab(this.#model.tab);
  }

  #pickScope(scope: string): void {
    const scopeAll = scope !== "branch";
    if (scopeAll === this.#model.scopeAll) return;
    this.#model = { ...this.#model, scopeAll, detail: null };
    this.#host.repaint();
    void this.#loadTab(this.#model.tab);
  }

  async #loadBoard(): Promise<void> {
    this.#model = { ...this.#model, board: { ...tabModel(this.#model.board.data) } };
    this.#host.repaint();
    const result = await this.#api.board(this.#scopeId());
    this.#model = {
      ...this.#model,
      board: loadEnded(result, result.ok ? result.value : []),
    };
    this.#host.repaint();
  }

  async #loadClaims(): Promise<void> {
    const filter = this.#model.claims.data.phaseFilter;
    this.#model = { ...this.#model, claims: { ...tabModel(this.#model.claims.data) } };
    this.#host.repaint();
    const answer = await this.#api.claims(filter);
    const shaped: Result<ClaimsTab> = answer.ok
      ? { ok: true, value: { phaseFilter: filter, rows: answer.value } }
      : answer;
    this.#model = { ...this.#model, claims: loadEnded(shaped, { phaseFilter: filter, rows: [] }) };
    this.#host.repaint();
  }

  async #loadVendors(): Promise<void> {
    const current = this.#model.vendors.data;
    this.#model = { ...this.#model, vendors: { ...tabModel(current) } };
    this.#host.repaint();
    const answer = await this.#api.vendorProfiles();
    const shaped: Result<VendorTab> = answer.ok
      ? { ok: true, value: { ...current, rows: answer.value } }
      : answer;
    this.#model = { ...this.#model, vendors: loadEnded(shaped, { ...current, rows: [] }) };
    this.#host.repaint();
  }

  async #loadRecovery(): Promise<void> {
    const current = this.#model.recovery.data;
    this.#model = { ...this.#model, recovery: { ...tabModel(current) } };
    this.#host.repaint();
    const answer = await this.#api.recoveryBoard(this.#scopeId(), !current.includeReconciled);
    const shaped: Result<RecoveryTab> = answer.ok
      ? { ok: true, value: { ...current, board: answer.value } }
      : answer;
    this.#model = { ...this.#model, recovery: loadEnded(shaped, { ...current, board: null }) };
    this.#host.repaint();
  }

  async #loadSla(): Promise<void> {
    const sla = this.#model.sla.data;
    this.#model = { ...this.#model, sla: { ...tabModel({ ...sla, notice: "" }) } };
    this.#host.repaint();
    const [board, targets] = await Promise.all([this.#api.slaBoard(this.#scopeId(), sla.includeAcknowledged), this.#api.slaTargets()]);
    if (board.ok && targets.ok) {
      this.#model = {
        ...this.#model,
        sla: { phase: "ready", error: "", refusal: "", data: { ...sla, board: board.value, targets: targets.value, sweeping: false } },
      };
    } else {
      const failureError = board.ok ? (targets.ok ? "" : targets.error) : board.error;
      const failureStatus = board.ok ? (targets.ok ? undefined : targets.status) : board.status;
      const refused = failureStatus === 403;
      this.#model = {
        ...this.#model,
        sla: {
          phase: refused ? "refused" : "error",
          error: refused ? "" : failureError,
          refusal: refused ? failureError : "",
          data: { ...sla, board: null, targets: [], sweeping: false },
        },
      };
    }
    this.#host.repaint();
  }

  async #loadReports(): Promise<void> {
    this.#model = { ...this.#model, reports: { ...tabModel(this.#model.reports.data) } };
    this.#host.repaint();
    const answer = await this.#api.reportBundle(this.#scopeId());
    const shaped: Result<ReportBundle | null> = answer.ok ? { ok: true, value: answer.value } : answer;
    this.#model = { ...this.#model, reports: loadEnded(shaped, null) };
    this.#host.repaint();
  }

  // -- the claim detail ------------------------------------------------------------------------------------

  #patchDetail(patch: Partial<ClaimDetailModel>): void {
    if (this.#model.detail === null) return;
    this.#model = { ...this.#model, detail: { ...this.#model.detail, ...patch } };
    this.#host.repaint();
  }

  async #openClaim(id: string): Promise<void> {
    if (id === "") return;
    this.#model = {
      ...this.#model,
      detail: {
        id,
        phase: "loading",
        error: "",
        refusal: "",
        notice: "",
        claim: null,
        checklist: [],
        events: [],
        postings: [],
        photos: [],
        photoError: "",
        busy: false,
        settleOpen: false,
        assignOpen: false,
        checklistBusyFor: "",
      },
    };
    this.#host.repaint();
    await this.#readClaim(id, true);
  }

  /** The claim's four reads in one pass; firstIn controls the loading state it writes. */
  async #readClaim(id: string, firstIn: boolean): Promise<void> {
    const [claim, checklist, events, postings] = await Promise.all([
      this.#api.claim(id),
      this.#api.checklist(id),
      this.#api.claimEvents(id),
      this.#api.postings(id),
    ]);
    if (this.#model.detail === null || this.#model.detail.id !== id) return;
    if (!claim.ok) {
      if (firstIn) {
        this.#patchDetail({
          phase: claim.status === 403 ? "refused" : "error",
          error: claim.status === 403 ? "" : claim.error,
          refusal: claim.status === 403 ? claim.error : "",
        });
      }
      return;
    }
    const partFailed =
      (!checklist.ok ? `The checklist did not load. ${checklist.error} ` : "") +
      (!events.ok ? `The timeline did not load. ${events.error} ` : "") +
      (!postings.ok ? `The postings did not load. ${postings.error}` : "");
    this.#patchDetail({
      phase: "ready",
      claim: claim.value,
      checklist: checklist.ok ? checklist.value : [],
      events: events.ok ? [...events.value].reverse() : [],
      postings: postings.ok ? postings.value : [],
      error: firstIn ? partFailed.trim() : this.#model.detail.error,
      refusal: "",
    });
    await this.#loadPhotos(id, claim.value);
  }

  /** A quiet re-read of the open claim: the server's answer wins, a failure keeps the glass. */
  async #refreshClaim(claimId: string): Promise<void> {
    await this.#readClaim(claimId, false);
  }

  /** The public intake's photos: the list first, then each picture's bytes as an object address. */
  async #loadPhotos(claimId: string, claim: Claim): Promise<void> {
    if (claim.source !== "WEB") return;
    const photos = await this.#api.intakePhotos(claimId);
    if (!photos.ok) {
      this.#patchDetail({ photoError: photos.error });
      return;
    }
    this.#patchDetail({ photos: photos.value });
    for (const photo of photos.value) {
      if (photo.id === "") continue;
      this.#photoAlts.set(photo.id, photo.caption !== "" ? photo.caption : "A photo attached to the claim");
      if (this.#photoUrls.has(photo.id)) continue;
      const bytes = await this.#api.intakePhotoBytes(claimId, photo.id);
      if (bytes.ok) {
        this.#photoUrls.set(photo.id, URL.createObjectURL(new Blob([bytes.value as BlobPart], { type: photo.content_type })));
        this.#host.repaint();
      } else {
        this.#patchDetail({ photoError: bytes.error });
      }
    }
  }

  async #tick(claimId: string, itemKey: string, checked: boolean): Promise<void> {
    if (itemKey === "" || this.#model.detail?.busy || this.#model.detail?.checklistBusyFor !== "") return;
    this.#patchDetail({ checklistBusyFor: itemKey, error: "", refusal: "", notice: "" });
    const result = await this.#api.tick(claimId, itemKey, checked);
    if (result.ok) {
      await this.#refreshClaim(claimId);
    } else {
      this.#patchDetail({ checklistBusyFor: "", error: advanceRefusalWords(result.error, result.status) });
    }
  }

  async #advance(claimId: string, toPhase: string): Promise<void> {
    if (toPhase === "" || this.#model.detail?.busy) return;
    this.#patchDetail({ busy: true, error: "", refusal: "", notice: "" });
    const result = await this.#api.advance(claimId, toPhase, "", "");
    if (result.ok) {
      this.#patchDetail({ busy: false, notice: `Moved to ${phaseWord(result.value.phase)}.` });
      await this.#refreshClaim(claimId);
      await this.#reloadStandingTabs();
    } else {
      this.#patchDetail({ busy: false, error: advanceRefusalWords(result.error, result.status) });
    }
  }

  async #assign(): Promise<void> {
    const detail = this.#model.detail;
    if (detail === null || detail.claim === null || detail.busy) return;
    const claim = detail.claim;
    const picked = this.#host.index("w-assign-vendor") - 1;
    const vendorId = picked >= 0 && picked < this.#model.vendorOptions.length ? this.#model.vendorOptions[picked].vendor_id : "";
    if (vendorId === "") {
      this.#patchDetail({ error: "Pick the vendor this claim sits with, then move it." });
      return;
    }
    this.#patchDetail({ busy: true, error: "", refusal: "", notice: "" });
    const result = await this.#api.assign(claim.id, vendorId, claim.owner_id);
    if (result.ok) {
      this.#patchDetail({ busy: false, assignOpen: false, notice: "The claim now sits with that vendor." });
      await this.#refreshClaim(claim.id);
      await this.#reloadStandingTabs();
    } else {
      this.#patchDetail({ busy: false, error: advanceRefusalWords(result.error, result.status) });
    }
  }

  async #attachEvidence(): Promise<void> {
    const detail = this.#model.detail;
    if (detail === null || detail.busy) return;
    const ref = this.#host.field("w-evidence-ref").trim();
    if (ref === "") {
      this.#patchDetail({ error: "Say where the evidence is kept, then attach it." });
      return;
    }
    this.#patchDetail({ busy: true, error: "", refusal: "", notice: "" });
    const result = await this.#api.attachEvidence(detail.id, ref, "Attached from the claim detail.");
    if (result.ok) {
      this.#patchDetail({ busy: false, notice: "Evidence recorded on the claim." });
      await this.#refreshClaim(detail.id);
    } else {
      this.#patchDetail({ busy: false, error: advanceRefusalWords(result.error, result.status) });
    }
  }

  async #settle(): Promise<void> {
    const detail = this.#model.detail;
    if (detail === null || detail.claim === null || detail.busy) return;
    const claim = detail.claim;
    const outcomeIndex = this.#host.index("w-settle-outcome");
    const outcome: string = OUTCOMES[outcomeIndex] ?? "";
    const expectedTyped = this.#host.field("w-settle-expected");
    const realizedTyped = this.#host.field("w-settle-realized");
    const note = this.#host.field("w-settle-note").trim();
    // A blank expected field sends null (the server keeps the captured
    // exposure); a typed one must be plain dollars. Realized is never blank.
    const expectedBlank = expectedTyped.trim() === "";
    const expectedCents = expectedBlank ? null : centsFromDollars(expectedTyped);
    const realizedCents = centsFromDollars(realizedTyped);
    if (outcome === "") {
      this.#patchDetail({ error: "Pick an outcome before recording the settlement." });
      return;
    }
    if (realizedCents === null) {
      this.#patchDetail({ error: "Type the realized amount as plain dollars with at most two decimal places, 0 or more. It cannot be left blank." });
      return;
    }
    if (!expectedBlank && expectedCents === null) {
      this.#patchDetail({ error: "The expected amount must be plain dollars with at most two decimal places, 0 or more, or left blank to keep the captured figure." });
      return;
    }
    this.#patchDetail({ busy: true, error: "", refusal: "", notice: "" });
    // The settle goes to the server and stops there: no queue, no retry, no
    // optimistic figures. The answer's own numbers are what the screen shows.
    const result = await this.#api.settle(claim.id, {
      outcome,
      expectedCents,
      realizedCents,
      note,
      manualOverride: false,
      overrideReason: "",
    });    if (result.ok) {
      const words = result.value.gl_posted
        ? `Settlement recorded and posted to the ledger. ${result.value.gl_note}`
        : `Settlement recorded. ${result.value.gl_note}`;
      this.#patchDetail({ busy: false, settleOpen: false, notice: words });
      await this.#refreshClaim(claim.id);
      await this.#reloadStandingTabs();
    } else {
      this.#patchDetail({ busy: false, error: settleRefusalWords(result.error, result.status) });
    }
  }

  /** The tabs whose numbers a lifecycle move changes, re-read when one is standing. */
  async #reloadStandingTabs(): Promise<void> {
    if (this.#model.tab === "board") await this.#loadBoard();
    if (this.#model.tab === "claims") await this.#loadClaims();
    if (this.#model.tab === "defects") await this.#loadDefects();
    if (this.#model.tab === "reports") await this.#loadReports();
    if (this.#model.tab === "recovery") await this.#loadRecovery();
    if (this.#model.tab === "sla") await this.#loadSla();
  }

  // -- capture ----------------------------------------------------------------------------------------------

  async #capture(): Promise<void> {
    if (this.#model.captureBusy) return;
    const field = (name: string): string => this.#host.field(name).trim();
    const description = field("w-capture-desc");
    const productText = field("w-capture-product");
    if (description === "" || productText === "") {
      this.#model = { ...this.#model, captureError: "A claim needs the failure described and at least one product named." };
      this.#host.repaint();
      return;
    }
    const expectedTyped = field("w-capture-expected");
    const expectedCents = expectedTyped === "" ? 0 : centsFromDollars(expectedTyped);
    if (expectedCents === null) {
      this.#model = { ...this.#model, captureError: "The expected recovery must be plain dollars with at most two decimal places, 0 or more, or left blank." };
      this.#host.repaint();
      return;
    }
    this.#model = { ...this.#model, captureBusy: true, captureError: "" };
    this.#host.repaint();
    const sourceIndex = this.#host.index("w-capture-source");
    const vendorIndex = this.#host.index("w-capture-vendor") - 1;
    const vendorId = vendorIndex >= 0 && vendorIndex < this.#model.vendorOptions.length ? this.#model.vendorOptions[vendorIndex].vendor_id : "";
    const result = await this.#api.createClaim({
      branchId: this.#branchId,
      source: sourceIndex === 1 ? "COUNTER" : "INTAKE",
      vendorId,
      description,
      expectedCents,
      openedAt: field("w-capture-opened"),
      addressLine1: field("w-capture-addr"),
      city: field("w-capture-city"),
      productText,
      qty: field("w-capture-qty") || "0",
      uom: field("w-capture-uom"),
      purchaseDateSupplier: field("w-capture-pds"),
    });
    if (result.ok) {
      this.#model = { ...this.#model, captureBusy: false, captureOpen: false, captureError: "" };
      this.#host.repaint();
      await this.#reloadStandingTabs();
      await this.#openClaim(result.value.id);
    } else {
      // A capture refusal is the core's own sentence (a missing branch, an
      // unreachable branch, a bad date); it stands above the form as written.
      this.#model = { ...this.#model, captureBusy: false, captureError: result.error };
      this.#host.repaint();
    }
  }

  // -- vendors -----------------------------------------------------------------------------------------------

  /** The expanded row: the whole profile through the single-profile route, overrides and docs included. */
  async #expandVendor(vendorId: string): Promise<void> {
    const answer = await this.#api.vendorProfile(vendorId);
    // A stale expand (the row was closed while the read ran) writes nothing.
    if (this.#model.vendors.data.openId !== vendorId) return;
    this.#model = {
      ...this.#model,
      vendors: {
        ...this.#model.vendors,
        data: {
          ...this.#model.vendors.data,
          openProfile: answer.ok ? answer.value : null,
          openError: answer.ok ? "" : answer.error,
        },
      },
    };
    this.#host.repaint();
  }

  async #saveVendor(): Promise<void> {
    const vendors = this.#model.vendors.data;
    const editing = vendors.editing;
    if (editing === null || vendors.saving) return;
    const status = VENDOR_STATUSES[this.#host.index("w-vendor-status")] ?? editing.status;
    const channel = CLAIM_CHANNELS[this.#host.index("w-vendor-channel")] ?? editing.channel;
    const termTyped = this.#host.field("w-vendor-term").trim();
    const parsedTerm = termTyped === "" ? null : Number.parseInt(termTyped, 10);
    this.#model = { ...this.#model, vendors: { ...this.#model.vendors, data: { ...vendors, saving: true, notice: "" } } };
    this.#host.repaint();
    const result = await this.#api.upsertVendorProfile(editing.vendor_id, {
      status,
      coverage: this.#host.field("w-vendor-coverage").trim(),
      termMonths: parsedTerm !== null && Number.isFinite(parsedTerm) ? parsedTerm : null,
      channel,
      channelRef: this.#host.field("w-vendor-ref").trim(),
      notes: this.#host.field("w-vendor-notes").trim(),
    });
    this.#saveEnded(result, `${editing.vendor_name} saved.`);
  }

  async #addOverride(): Promise<void> {
    const vendors = this.#model.vendors.data;
    const vendorId = vendors.overrideFor;
    if (vendorId === "" || vendors.saving) return;
    const productId = this.#host.field("w-override-product").trim();
    const coverage = this.#host.field("w-override-coverage").trim();
    if (productId === "" || coverage === "") {
      this.#model = {
        ...this.#model,
        vendors: { ...this.#model.vendors, error: "A product override needs the product id and what is covered." },
      };
      this.#host.repaint();
      return;
    }
    const termTyped = this.#host.field("w-override-term").trim();
    const parsedTerm = termTyped === "" ? null : Number.parseInt(termTyped, 10);
    this.#model = { ...this.#model, vendors: { ...this.#model.vendors, data: { ...vendors, saving: true, notice: "" } } };
    this.#host.repaint();
    const result = await this.#api.addProductOverride(vendorId, {
      productId,
      coverage,
      termMonths: parsedTerm !== null && Number.isFinite(parsedTerm) ? parsedTerm : null,
      notes: this.#host.field("w-override-notes").trim(),
    });
    this.#saveEnded(result, "The product override is on the profile.");
  }

  /** A registry write's end: saved, or the core's refusal in its own words, walled when it is the shared body. */
  #saveEnded(result: { ok: true } | { ok: false; error: string; status?: number }, savedWords: string): void {
    if (result.ok) {
      this.#model = {
        ...this.#model,
        vendors: {
          ...this.#model.vendors,
          data: { ...this.#model.vendors.data, editing: null, saving: false, overrideFor: "", notice: savedWords },
        },
      };
      this.#host.repaint();
      void this.#loadVendors();
      return;
    }
    const walled = result.status === 403;
    this.#model = {
      ...this.#model,
      vendors: {
        ...this.#model.vendors,
        error: walled ? "" : result.error,
        refusal: walled ? vendorRefusalWords(result.error, result.status) : "",
        data: { ...this.#model.vendors.data, saving: false },
      },
    };
    this.#host.repaint();
  }

  // -- recovery -----------------------------------------------------------------------------------------------

  async #loadChaseLog(claimId: string): Promise<void> {
    const result = await this.#api.recoveryActions(claimId);
    if (this.#model.recovery.data.openLogFor !== claimId) return;
    this.#model = {
      ...this.#model,
      recovery: {
        ...this.#model.recovery,
        data: {
          ...this.#model.recovery.data,
          log: result.ok ? result.value : [],
          logError: result.ok ? "" : result.error,
        },
      },
    };
    this.#host.repaint();
  }

  async #logChase(claimId: string): Promise<void> {
    const recovery = this.#model.recovery.data;
    if (recovery.logBusy) return;
    const actionRow = RECOVERY_ACTIONS[this.#host.index("w-recovery-action")] ?? RECOVERY_ACTIONS[0];
    const action = actionRow.action;
    let amountCents = 0;
    let promisedDate = "";
    if (actionRow.money === true) {
      const cents = centsFromDollars(this.#host.field("w-recovery-amount"));
      if (cents === null) {
        this.#model = { ...this.#model, recovery: { ...this.#model.recovery, data: { ...recovery, logError: "Type the amount as plain dollars with at most two decimal places, 0 or more. It cannot be left blank." } } };
        this.#host.repaint();
        return;
      }
      amountCents = cents;
    }
    if (actionRow.promised === true) {
      promisedDate = this.#host.field("w-recovery-promised").trim();
      if (!/^\d{4}-\d{2}-\d{2}$/.test(promisedDate)) {
        this.#model = { ...this.#model, recovery: { ...this.#model.recovery, data: { ...recovery, logError: "Pick the day the vendor promised, as a date." } } };
        this.#host.repaint();
        return;
      }
    }
    this.#model = { ...this.#model, recovery: { ...this.#model.recovery, data: { ...recovery, logBusy: true, logError: "", logNotice: "" } } };
    this.#host.repaint();
    const result = await this.#api.logRecoveryAction(claimId, {
      action,
      amountCents,
      promisedDate,
      reference: this.#host.field("w-recovery-ref").trim(),
      note: this.#host.field("w-recovery-note").trim(),
      occurredAt: "",
    });
    if (result.ok) {
      this.#model = {
        ...this.#model,
        recovery: { ...this.#model.recovery, data: { ...this.#model.recovery.data, logBusy: false, logNotice: "Logged." } },
      };
      this.#host.repaint();
      // A receipt changes the balance, so the board is re-read rather than
      // patched: the server owns the subtraction and the screen shows the
      // server's answer.
      await this.#loadChaseLog(claimId);
      await this.#loadRecovery();
    } else {
      this.#model = {
        ...this.#model,
        recovery: {
          ...this.#model.recovery,
          data: {
            ...this.#model.recovery.data,
            logBusy: false,
            logError: result.status === 403 ? recoveryRefusalWords(result.error, result.status) : result.error,
          },
        },
      };
      this.#host.repaint();
    }
  }
  // -- service levels -------------------------------------------------------------------------------------------

  async #sweep(): Promise<void> {
    const sla = this.#model.sla.data;
    if (sla.sweeping) return;
    this.#model = { ...this.#model, sla: { ...this.#model.sla, data: { ...sla, sweeping: true, notice: "" } } };
    this.#host.repaint();
    const result = await this.#api.sweepSla(this.#scopeId());
    if (result.ok) {
      const raised = result.value.count;
      this.#model = {
        ...this.#model,
        sla: {
          ...this.#model.sla,
          data: {
            ...this.#model.sla.data,
            sweeping: false,
            notice: raised === 0 ? "The sweep found nothing new overdue." : `The sweep raised ${raised} alert${raised === 1 ? "" : "s"}.`,
          },
        },
      };
      this.#host.repaint();
      await this.#loadSla();
    } else {
      const walled = result.status === 403;
      this.#model = {
        ...this.#model,
        sla: {
          ...this.#model.sla,
          error: walled ? "" : result.error,
          refusal: walled ? slaRefusalWords(result.error, result.status) : "",
          data: { ...this.#model.sla.data, sweeping: false },
        },
      };
      this.#host.repaint();
    }
  }

  async #ack(claimId: string, alertId: string): Promise<void> {
    if (claimId === "" || alertId === "") return;
    const result = await this.#api.ackSlaAlert(claimId, alertId);
    if (result.ok) {
      await this.#loadSla();
    } else {
      const walled = result.status === 403;
      this.#model = {
        ...this.#model,
        sla: {
          ...this.#model.sla,
          error: walled ? "" : result.error,
          refusal: walled ? slaRefusalWords(result.error, result.status) : "",
        },
      };
      this.#host.repaint();
    }
  }

  // -- change 6 item 5: defects and vendor credits ---------------------------------------------------------

  /** The defects list: till returns of damaged product, classified by where the line cost sits. */
  async #loadDefects(force: boolean = false): Promise<void> {
    if (!force && this.#model.defects.phase !== "loading") return;
    const current = this.#model.defects.data;
    this.#model = { ...this.#model, defects: { ...tabModel(current) } };
    this.#host.repaint();
    const result = await this.#api.readDefects(this.#scopeId(), current.filter);
    this.#model = { ...this.#model, defects: loadEnded(result, current) };
    this.#host.repaint();
  }

  /** The Vendors and credits table: each vendor's agreement and what they owe. */
  async #loadVendorCredits(force: boolean = false): Promise<void> {
    if (!force && this.#model.vendorCredits.phase !== "loading") return;
    const current = this.#model.vendorCredits.data;
    this.#model = { ...this.#model, vendorCredits: { ...tabModel(current) } };
    this.#host.repaint();
    const result = await this.#api.readVendorCredits(this.#scopeId());
    if (result.ok) {
      const merged: VendorCreditsTab = {
        ...result.value,
        openVendorId: current.openVendorId,
        openDetail: current.openDetail,
        openError: current.openError,
        requestingFor: current.requestingFor,
        requestNotice: current.requestNotice,
        requestError: current.requestError,
      };
      this.#model = { ...this.#model, vendorCredits: loadEnded({ ok: true, value: merged }, current) };
    } else {
      this.#model = { ...this.#model, vendorCredits: loadEnded(result, current) };
    }
    this.#host.repaint();
  }

  /** Open a vendor's right panel on the Vendors and credits tab. */
  async #expandVendorCredit(vendorId: string): Promise<void> {
    const current = this.#model.vendorCredits.data;
    if (vendorId === current.openVendorId) return;
    this.#model = {
      ...this.#model,
      vendorCredits: { ...this.#model.vendorCredits, data: { ...current, openVendorId: vendorId, openError: "" } },
    };
    this.#host.repaint();
    if (vendorId === "") return;
    const result = await this.#api.readVendorCreditDetail(this.#scopeId(), vendorId);
    if (this.#model.vendorCredits.data.openVendorId !== vendorId) return;
    if (result.ok) {
      this.#model = {
        ...this.#model,
        vendorCredits: {
          ...this.#model.vendorCredits,
          data: { ...this.#model.vendorCredits.data, openDetail: result.value, openError: "" },
        },
      };
    } else {
      this.#model = {
        ...this.#model,
        vendorCredits: {
          ...this.#model.vendorCredits,
          data: { ...this.#model.vendorCredits.data, openDetail: null, openError: result.error },
        },
      };
    }
    this.#host.repaint();
  }

  /** Fire a credit note request for a vendor; the route is not yet wired
   *  and the stub returns 501, which the panel surfaces as its own refusal. */
  async #requestVendorCreditNote(vendorId: string): Promise<void> {
    if (vendorId === "") return;
    const current = this.#model.vendorCredits.data;
    this.#model = {
      ...this.#model,
      vendorCredits: {
        ...this.#model.vendorCredits,
        data: { ...current, requestingFor: vendorId, requestNotice: "", requestError: "" },
      },
    };
    this.#host.repaint();
    const result = await this.#api.requestVendorCreditNote(vendorId);
    if (this.#model.vendorCredits.data.requestingFor !== vendorId) return;
    if (result.ok) {
      this.#model = {
        ...this.#model,
        vendorCredits: {
          ...this.#model.vendorCredits,
          data: { ...this.#model.vendorCredits.data, requestingFor: "", requestNotice: "Credit note requested.", requestError: "" },
        },
      };
    } else {
      this.#model = {
        ...this.#model,
        vendorCredits: {
          ...this.#model.vendorCredits,
          data: { ...this.#model.vendorCredits.data, requestingFor: "", requestNotice: "", requestError: result.error },
        },
      };
    }
    this.#host.repaint();
  }

  /** Open the claim-evidence drawer from a defect's Claim cell. */
  async #openClaimEvidence(defectId: string): Promise<void> {
    if (defectId === "") return;
    const defect = this.#model.defects.data.rows.find((row) => row.id === defectId);
    if (!defect) return;
    if (defect.claim_id === "") return;
    const draft: ClaimEvidenceModel = {
      ...emptyClaimEvidence(),
      defectId: defect.id,
      claimId: defect.claim_id,
      claimNo: defect.claim_no,
      returnNo: defect.return_no,
      customerName: defect.customer_name,
      productText: defect.product_text,
      quantityText: defect.qty,
      lotOrBatch: defect.lot_or_batch,
      costCents: defect.cost_cents,
      vendorName: defect.vendor_name,
    };
    this.#model = { ...this.#model, claimEvidence: draft };
    this.#host.repaint();
    void this.#loadClaimEvidencePhotos(defect.claim_id);
  }

  /** The photos and timeline the claim-evidence drawer renders. The core
   *  has both routes already; the read is the seam for when the wire lands. */
  async #loadClaimEvidencePhotos(claimId: string): Promise<void> {
    if (this.#model.claimEvidence?.claimId !== claimId) return;
    const photos = await this.#api.intakePhotos(claimId);
    if (this.#model.claimEvidence?.claimId !== claimId) return;
    if (photos.ok) {
      this.#model = {
        ...this.#model,
        claimEvidence: { ...this.#model.claimEvidence, phase: "ready", photos: photos.value, photoError: "" },
      };
    } else {
      this.#model = {
        ...this.#model,
        claimEvidence: {
          ...this.#model.claimEvidence,
          phase: "error",
          photoError: photos.status === 403 ? "" : photos.error,
        },
      };
    }
    this.#host.repaint();
  }

  /** Close the claim-evidence drawer. */
  #closeClaimEvidence(): void {
    if (this.#model.claimEvidence === null) return;
    this.#model = { ...this.#model, claimEvidence: null };
    this.#host.repaint();
  }
}

/** The one shape every tab load ends in: ready with the answer, or the refusal, or the error. */
function loadEnded<T>(result: { ok: true; value: T } | { ok: false; error: string; status?: number }, empty: T): TabModel<T> {
  if (result.ok) return { phase: "ready", error: "", refusal: "", data: result.value };
  const refused = result.status === 403;
  return {
    phase: refused ? "refused" : "error",
    error: refused ? "" : result.error,
    refusal: refused ? result.error : "",
    data: empty,
  };
}
