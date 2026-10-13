// Warranty's one door to the core: the board, the claims and their lifecycle,
// the vendor registry, the recovery board, the service levels and the reports,
// over the generated api-client, answering a Result and never throwing.
//
// THE READ PROJECTION, as manager-api.ts runs it. Every wire object is copied
// through a whitelist into a narrow type that names only what the warranty
// screens use. A field the projection does not name is dropped at this door
// and never reaches a screen. Money stays integer cents end to end: the core
// computes it, this door carries it, and only the view formats it for a
// person. Nothing here sums, converts or derives a figure; the one exception
// is the settlement form, whose dollars a person types and whose cents are
// parsed once on the way out.
//
// Port provenance: the screens and their call set are the original app's
// warranty surface (repository hardscapeos_dibbits, read only; see
// PROVENANCE.md), rebuilt on HH's generated client and narrowed to what these
// screens draw. The public intake belongs to the public desk, not here.
import { ApiClient, staffWarranty, type Result } from "@hardscapehouse/api-client";

// -- shared narrow types ---------------------------------------------------------------------------

/** The lifecycle phases, in the board's order, the two exits last. */
export type ClaimPhase =
  | "INTAKE"
  | "TRIAGE"
  | "SUBMITTED"
  | "WAITING_ON_DECISION"
  | "DECISION"
  | "SETTLEMENT"
  | "CLOSED"
  | "REJECTED"
  | "ABANDONED";

export interface ClaimAddress {
  readonly line1: string;
  readonly city: string;
  readonly province: string;
  readonly postal: string;
}

export interface ClaimLine {
  readonly id: string;
  readonly product_text: string;
  readonly qty: string;
  readonly uom_code: string;
  readonly purchase_date_supplier: string;
  readonly notes: string;
}

export interface Claim {
  readonly id: string;
  readonly claim_no: string;
  readonly customer_name: string;
  readonly job_address: ClaimAddress;
  readonly vendor_id: string;
  readonly vendor_name: string;
  readonly owner_id: string;
  readonly owner_name: string;
  readonly branch_id: string;
  readonly phase: ClaimPhase;
  readonly source: string;
  readonly description: string;
  readonly order_id: string;
  readonly submitted_at: string;
  readonly sla_due: string;
  readonly outcome: string;
  readonly expected_cents: number;
  readonly realized_cents: number;
  readonly opened_at: string;
  readonly closed_at: string;
  readonly lines: readonly ClaimLine[];
}

export interface BoardColumn {
  readonly phase: ClaimPhase;
  readonly claims: readonly Claim[];
}

export interface ClaimEvent {
  readonly id: string;
  readonly event_type: string;
  readonly from_phase: string;
  readonly to_phase: string;
  readonly checklist_key: string;
  readonly checked: boolean | null;
  readonly note: string;
  readonly evidence_ref: string;
  readonly occurred_at: string;
}

export interface ChecklistItem {
  readonly id: string;
  readonly phase: string;
  readonly item_key: string;
  readonly label: string;
  readonly required: boolean;
  readonly sort_order: number;
}

export interface ChecklistState {
  readonly item: ChecklistItem;
  readonly checked: boolean;
}

export interface SettlementResult {
  readonly claim_id: string;
  readonly outcome: string;
  readonly expected_cents: number;
  readonly realized_cents: number;
  readonly accrued_cents: number;
  readonly gl_journal_id: string;
  readonly gl_posted: boolean;
  readonly gl_note: string;
  readonly money_path: string;
  readonly path_status: string;
}

export interface SettlementPosting {
  readonly id: string;
  readonly outcome: string;
  readonly money_path: string;
  readonly seam: string;
  readonly status: string;
  readonly amount_cents: number;
  readonly accrues_recovery: boolean;
  readonly gl_journal_id: string;
  readonly external_ref: string;
  readonly error_text: string;
  readonly note: string;
  readonly posted_at: string;
}

export interface IntakePhoto {
  readonly id: string;
  readonly content_type: string;
  readonly byte_size: number;
  readonly caption: string;
}

// -- the vendor registry ----------------------------------------------------------------------------

export interface ProductOverride {
  readonly id: string;
  readonly product_id: string;
  readonly product_name: string;
  readonly coverage: string;
  readonly term_months: number | null;
  readonly notes: string;
}

export interface VendorProfile {
  readonly id: string;
  readonly vendor_id: string;
  readonly vendor_name: string;
  readonly vendor_no: string;
  readonly status: string;
  readonly coverage: string;
  readonly term_months: number | null;
  readonly channel: string;
  readonly channel_ref: string;
  readonly docs: readonly { readonly title: string; readonly ref: string }[];
  readonly notes: string;
  readonly overrides: readonly ProductOverride[];
}

// -- recovery ----------------------------------------------------------------------------------------

export interface RecoveryRow {
  readonly claim_id: string;
  readonly claim_no: string;
  readonly vendor_name: string;
  readonly accrued_cents: number;
  readonly received_cents: number;
  readonly outstanding_cents: number;
  readonly promised_cents: number;
  readonly accrued_at: string;
  readonly last_action_at: string;
  readonly action_count: number;
  readonly age_days: number;
  readonly bucket: string;
  readonly reconciled: boolean;
}

export interface VendorRecovery {
  readonly vendor_id: string;
  readonly vendor_name: string;
  readonly claim_count: number;
  readonly accrued_cents: number;
  readonly received_cents: number;
  readonly outstanding_cents: number;
  readonly promised_cents: number;
  readonly buckets: Readonly<Record<string, number>>;
  readonly oldest_days: number;
  readonly claims: readonly RecoveryRow[];
}

export interface RecoveryBoardModel {
  readonly vendors: readonly VendorRecovery[];
  readonly buckets: Readonly<Record<string, number>>;
  readonly total_accrued_cents: number;
  readonly total_received_cents: number;
  readonly total_outstanding_cents: number;
  readonly total_promised_cents: number;
  readonly open_claim_count: number;
  readonly reconciled_claim_count: number;
  readonly as_of: string;
}

export interface RecoveryAction {
  readonly id: string;
  readonly claim_id: string;
  readonly action: string;
  readonly amount_cents: number;
  readonly promised_date: string;
  readonly reference: string;
  readonly note: string;
  readonly actor_name: string;
  readonly occurred_at: string;
}

// -- service levels ----------------------------------------------------------------------------------

export interface SlaTimer {
  readonly claim_id: string;
  readonly claim_no: string;
  readonly vendor_name: string;
  readonly phase: string;
  readonly target_hours: number;
  readonly due_at: string;
  readonly state: string;
  readonly elapsed_hours: number;
  readonly remaining_hours: number;
  readonly overdue_hours: number;
  readonly exposure_cents: number;
}

export interface SlaAlert {
  readonly id: string;
  readonly claim_id: string;
  readonly claim_no: string;
  readonly phase: string;
  readonly due_at: string;
  readonly overdue_hours: number;
  readonly acknowledged_at: string;
  readonly notify_status: string;
  readonly exposure_cents: number;
}

export interface SlaTarget {
  readonly id: string;
  readonly phase: string;
  readonly vendor_name: string;
  readonly target_hours: number;
  readonly is_active: boolean;
  readonly notes: string;
}

export interface SlaBoardModel {
  readonly timers: readonly SlaTimer[];
  readonly alerts: readonly SlaAlert[];
  readonly breached_count: number;
  readonly due_soon_count: number;
  readonly as_of: string;
}

export interface SlaSweepResult {
  readonly count: number;
}

// -- reports -----------------------------------------------------------------------------------------

export interface FunnelCount {
  readonly phase: string;
  readonly count: number;
}

export interface ManufacturerFrequency {
  readonly vendor_id: string;
  readonly vendor_name: string;
  readonly total_claims: number;
  readonly open_claims: number;
  readonly won_claims: number;
  readonly lost_claims: number;
  readonly win_rate: number;
  readonly expected_cents: number;
  readonly realized_cents: number;
}

export interface TimeToSettlement {
  readonly settled_count: number;
  readonly avg_days: number;
  readonly median_days: number;
}

export interface ExposureByVendor {
  readonly vendor_id: string;
  readonly vendor_name: string;
  readonly open_claims: number;
  readonly exposure_cents: number;
}

export interface OpenClaimAge {
  readonly claim_id: string;
  readonly claim_no: string;
  readonly phase: string;
  readonly vendor_name: string;
  readonly age_days: number;
  readonly bucket: string;
  readonly expected_cents: number;
}

export interface ReportBundle {
  readonly funnel: readonly FunnelCount[];
  readonly by_manufacturer: readonly ManufacturerFrequency[];
  readonly time_to_settlement: TimeToSettlement;
  readonly exposure_by_vendor: readonly ExposureByVendor[];
  readonly open_claims_by_age: readonly OpenClaimAge[];
  readonly total_open: number;
  readonly total_exposure_cents: number;
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);
const maybeNum = (value: unknown): number | null => (typeof value === "number" && Number.isFinite(value) ? value : null);
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);

/** A phase as the wire names it, narrowed to the contract's own union. */
const phase = (value: unknown): ClaimPhase => {
  const raw = text(value) as ClaimPhase;
  return PHASE_SET.has(raw) ? raw : "INTAKE";
};

const PHASES_WIRE = [
  "INTAKE",
  "TRIAGE",
  "SUBMITTED",
  "WAITING_ON_DECISION",
  "DECISION",
  "SETTLEMENT",
  "CLOSED",
  "REJECTED",
  "ABANDONED",
] as const;
const PHASE_SET = new Set<string>(PHASES_WIRE);

export function toClaimAddress(raw: unknown): ClaimAddress {
  const r = bag(raw);
  return { line1: text(r.line1), city: text(r.city), province: text(r.province), postal: text(r.postal) };
}

export function toClaimLine(raw: unknown): ClaimLine {
  const r = bag(raw);
  return {
    id: text(r.id),
    product_text: text(r.product_text),
    qty: text(r.qty),
    uom_code: text(r.uom_code),
    purchase_date_supplier: text(r.purchase_date_supplier),
    notes: text(r.notes),
  };
}

export function toClaim(raw: unknown): Claim {
  const r = bag(raw);
  return {
    id: text(r.id),
    claim_no: text(r.claim_no),
    customer_name: text(r.customer_name),
    job_address: toClaimAddress(r.job_address),
    vendor_id: text(r.vendor_id),
    vendor_name: text(r.vendor_name),
    owner_id: text(r.owner_id),
    owner_name: text(r.owner_name),
    branch_id: text(r.branch_id),
    phase: phase(r.phase),
    source: text(r.source),
    description: text(r.description),
    order_id: text(r.order_id),
    submitted_at: text(r.submitted_at),
    sla_due: text(r.sla_due),
    outcome: text(r.outcome),
    expected_cents: num(r.expected_cents),
    realized_cents: num(r.realized_cents),
    opened_at: text(r.opened_at),
    closed_at: text(r.closed_at),
    lines: list(r.lines).map(toClaimLine),
  };
}

export function toBoardColumn(raw: unknown): BoardColumn {
  const r = bag(raw);
  return { phase: phase(r.phase), claims: list(r.claims).map(toClaim) };
}

export function toClaimEvent(raw: unknown): ClaimEvent {
  const r = bag(raw);
  return {
    id: text(r.id),
    event_type: text(r.event_type),
    from_phase: text(r.from_phase),
    to_phase: text(r.to_phase),
    checklist_key: text(r.checklist_key),
    checked: r.checked === null || r.checked === undefined ? null : r.checked === true,
    note: text(r.note),
    evidence_ref: text(r.evidence_ref),
    occurred_at: text(r.occurred_at),
  };
}

export function toChecklistState(raw: unknown): ChecklistState {
  const r = bag(raw);
  const item = bag(r.item);
  return {
    item: {
      id: text(item.id),
      phase: text(item.phase),
      item_key: text(item.item_key),
      label: text(item.label),
      required: item.required === true,
      sort_order: num(item.sort_order),
    },
    checked: r.checked === true,
  };
}

export function toSettlementResult(raw: unknown): SettlementResult {
  const r = bag(raw);
  return {
    claim_id: text(r.claim_id),
    outcome: text(r.outcome),
    expected_cents: num(r.expected_cents),
    realized_cents: num(r.realized_cents),
    accrued_cents: num(r.accrued_cents),
    gl_journal_id: text(r.gl_journal_id),
    gl_posted: r.gl_posted === true,
    gl_note: text(r.gl_note),
    money_path: text(r.money_path),
    path_status: text(r.path_status),
  };
}

export function toSettlementPosting(raw: unknown): SettlementPosting {
  const r = bag(raw);
  return {
    id: text(r.id),
    outcome: text(r.outcome),
    money_path: text(r.money_path),
    seam: text(r.seam),
    status: text(r.status),
    amount_cents: num(r.amount_cents),
    accrues_recovery: r.accrues_recovery === true,
    gl_journal_id: text(r.gl_journal_id),
    external_ref: text(r.external_ref),
    error_text: text(r.error_text),
    note: text(r.note),
    posted_at: text(r.posted_at),
  };
}

export function toIntakePhoto(raw: unknown): IntakePhoto {
  const r = bag(raw);
  return {
    id: text(r.id),
    content_type: text(r.content_type),
    byte_size: num(r.byte_size),
    caption: text(r.caption),
  };
}

export function toProductOverride(raw: unknown): ProductOverride {
  const r = bag(raw);
  return {
    id: text(r.id),
    product_id: text(r.product_id),
    product_name: text(r.product_name),
    coverage: text(r.coverage),
    term_months: maybeNum(r.term_months),
    notes: text(r.notes),
  };
}

export function toVendorProfile(raw: unknown): VendorProfile {
  const r = bag(raw);
  return {
    id: text(r.id),
    vendor_id: text(r.vendor_id),
    vendor_name: text(r.vendor_name),
    vendor_no: text(r.vendor_no),
    status: text(r.status),
    coverage: text(r.coverage),
    term_months: maybeNum(r.term_months),
    channel: text(r.channel),
    channel_ref: text(r.channel_ref),
    docs: list(r.docs).map((d) => {
      const b = bag(d);
      return { title: text(b.title), ref: text(b.ref) };
    }),
    notes: text(r.notes),
    overrides: list(r.overrides).map(toProductOverride),
  };
}

export function toRecoveryRow(raw: unknown): RecoveryRow {
  const r = bag(raw);
  return {
    claim_id: text(r.claim_id),
    claim_no: text(r.claim_no),
    vendor_name: text(r.vendor_name),
    accrued_cents: num(r.accrued_cents),
    received_cents: num(r.received_cents),
    outstanding_cents: num(r.outstanding_cents),
    promised_cents: num(r.promised_cents),
    accrued_at: text(r.accrued_at),
    last_action_at: text(r.last_action_at),
    action_count: num(r.action_count),
    age_days: num(r.age_days),
    bucket: text(r.bucket),
    reconciled: r.reconciled === true,
  };
}

const buckets = (value: unknown): Readonly<Record<string, number>> => {
  const out: Record<string, number> = {};
  for (const [key, v] of Object.entries(bag(value))) out[key] = num(v);
  return out;
};

export function toVendorRecovery(raw: unknown): VendorRecovery {
  const r = bag(raw);
  return {
    vendor_id: text(r.vendor_id),
    vendor_name: text(r.vendor_name),
    claim_count: num(r.claim_count),
    accrued_cents: num(r.accrued_cents),
    received_cents: num(r.received_cents),
    outstanding_cents: num(r.outstanding_cents),
    promised_cents: num(r.promised_cents),
    buckets: buckets(r.buckets),
    oldest_days: num(r.oldest_days),
    claims: list(r.claims).map(toRecoveryRow),
  };
}

export function toRecoveryBoard(raw: unknown): RecoveryBoardModel {
  const r = bag(raw);
  return {
    vendors: list(r.vendors).map(toVendorRecovery),
    buckets: buckets(r.buckets),
    total_accrued_cents: num(r.total_accrued_cents),
    total_received_cents: num(r.total_received_cents),
    total_outstanding_cents: num(r.total_outstanding_cents),
    total_promised_cents: num(r.total_promised_cents),
    open_claim_count: num(r.open_claim_count),
    reconciled_claim_count: num(r.reconciled_claim_count),
    as_of: text(r.as_of),
  };
}

export function toRecoveryAction(raw: unknown): RecoveryAction {
  const r = bag(raw);
  return {
    id: text(r.id),
    claim_id: text(r.claim_id),
    action: text(r.action),
    amount_cents: num(r.amount_cents),
    promised_date: text(r.promised_date),
    reference: text(r.reference),
    note: text(r.note),
    actor_name: text(r.actor_name),
    occurred_at: text(r.occurred_at),
  };
}

export function toSlaTimer(raw: unknown): SlaTimer {
  const r = bag(raw);
  return {
    claim_id: text(r.claim_id),
    claim_no: text(r.claim_no),
    vendor_name: text(r.vendor_name),
    phase: text(r.phase),
    target_hours: num(r.target_hours),
    due_at: text(r.due_at),
    state: text(r.state),
    elapsed_hours: num(r.elapsed_hours),
    remaining_hours: num(r.remaining_hours),
    overdue_hours: num(r.overdue_hours),
    exposure_cents: num(r.exposure_cents),
  };
}

export function toSlaAlert(raw: unknown): SlaAlert {
  const r = bag(raw);
  return {
    id: text(r.id),
    claim_id: text(r.claim_id),
    claim_no: text(r.claim_no),
    phase: text(r.phase),
    due_at: text(r.due_at),
    overdue_hours: num(r.overdue_hours),
    acknowledged_at: text(r.acknowledged_at),
    notify_status: text(r.notify_status),
    exposure_cents: maybeNum(r.exposure_cents) ?? 0,
  };
}

export function toSlaTarget(raw: unknown): SlaTarget {
  const r = bag(raw);
  return {
    id: text(r.id),
    phase: text(r.phase),
    vendor_name: text(r.vendor_name),
    target_hours: num(r.target_hours),
    is_active: r.is_active === true,
    notes: text(r.notes),
  };
}

export function toSlaBoard(raw: unknown): SlaBoardModel {
  const r = bag(raw);
  return {
    timers: list(r.timers).map(toSlaTimer),
    alerts: list(r.alerts).map(toSlaAlert),
    breached_count: num(r.breached_count),
    due_soon_count: num(r.due_soon_count),
    as_of: text(r.as_of),
  };
}

export function toReportBundle(raw: unknown): ReportBundle {
  const r = bag(raw);
  const times = bag(r.time_to_settlement);
  return {
    funnel: list(r.funnel).map((f) => {
      const b = bag(f);
      return { phase: text(b.phase), count: num(b.count) };
    }),
    by_manufacturer: list(r.by_manufacturer).map((m) => {
      const b = bag(m);
      return {
        vendor_id: text(b.vendor_id),
        vendor_name: text(b.vendor_name),
        total_claims: num(b.total_claims),
        open_claims: num(b.open_claims),
        won_claims: num(b.won_claims),
        lost_claims: num(b.lost_claims),
        win_rate: num(b.win_rate),
        expected_cents: num(b.expected_cents),
        realized_cents: num(b.realized_cents),
      };
    }),
    time_to_settlement: {
      settled_count: num(times.settled_count),
      avg_days: num(times.avg_days),
      median_days: num(times.median_days),
    },
    exposure_by_vendor: list(r.exposure_by_vendor).map((e) => {
      const b = bag(e);
      return {
        vendor_id: text(b.vendor_id),
        vendor_name: text(b.vendor_name),
        open_claims: num(b.open_claims),
        exposure_cents: num(b.exposure_cents),
      };
    }),
    open_claims_by_age: list(r.open_claims_by_age).map((o) => {
      const b = bag(o);
      return {
        claim_id: text(b.claim_id),
        claim_no: text(b.claim_no),
        phase: text(b.phase),
        vendor_name: text(b.vendor_name),
        age_days: num(b.age_days),
        bucket: text(b.bucket),
        expected_cents: num(b.expected_cents),
      };
    }),
    total_open: num(r.total_open),
    total_exposure_cents: num(r.total_exposure_cents),
  };
}

/** The items page a list route answers, unwrapped once at the door. */
const pageItems = <T>(answer: Result<{ items?: unknown[] }>, map: (raw: unknown) => T): Result<readonly T[]> => {
  if (!answer.ok) return answer;
  return { ok: true, value: list(answer.value.items).map(map) };
};

/** The form fields the capture sheet sends, as the contract's input names them. */
export interface CaptureDraft {
  readonly branchId: string;
  readonly source: string;
  readonly vendorId: string;
  readonly description: string;
  readonly expectedCents: number;
  readonly openedAt: string;
  readonly addressLine1: string;
  readonly city: string;
  readonly productText: string;
  readonly qty: string;
  readonly uom: string;
  readonly purchaseDateSupplier: string;
}

/** The registry edit the vendor sheet sends. */
export interface VendorDraft {
  readonly status: string;
  readonly coverage: string;
  readonly termMonths: number | null;
  readonly channel: string;
  readonly channelRef: string;
  readonly notes: string;
}

/** Every call the warranty screens make. */
export class WarrantyApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The Kanban columns, whole org or one branch; the server applies the caller's scope itself. */
  async board(branchId: string): Promise<Result<readonly BoardColumn[]>> {
    const answer = await staffWarranty.warrantyGetBoard(this.#client, branchId === "" ? {} : { branch_id: branchId });
    if (!answer.ok) return answer;
    return { ok: true, value: list(bag(answer.value).columns).map(toBoardColumn) };
  }

  /** The claims list, narrowed to one phase when one is named. */
  async claims(phase: string): Promise<Result<readonly Claim[]>> {
    const opts =
      phase === ""
        ? { limit: 200 }
        : { limit: 200, phase: phase as "INTAKE" | "TRIAGE" | "SUBMITTED" | "WAITING_ON_DECISION" | "DECISION" | "SETTLEMENT" | "CLOSED" | "REJECTED" | "ABANDONED" };
    return pageItems(await staffWarranty.warrantyListClaims(this.#client, opts), toClaim);
  }

  /** One claim with its lines. */
  async claim(id: string): Promise<Result<Claim>> {
    const answer = await staffWarranty.warrantyGetClaim(this.#client, id);
    return answer.ok ? { ok: true, value: toClaim(answer.value) } : answer;
  }

  /** The claim's audit spine, oldest last. */
  async claimEvents(id: string): Promise<Result<readonly ClaimEvent[]>> {
    return pageItems(await staffWarranty.warrantyListClaimEvents(this.#client, id, { limit: 200 }), toClaimEvent);
  }

  /** The current phase's checklist with its ticks. */
  async checklist(id: string): Promise<Result<readonly ChecklistState[]>> {
    return pageItems(await staffWarranty.warrantyGetClaimChecklist(this.#client, id, { limit: 100 }), toChecklistState);
  }

  /** Capture a claim. The branch is the caller's own; the server defaults the owner to the capturer. */
  async createClaim(draft: CaptureDraft): Promise<Result<Claim>> {
    const answer = await staffWarranty.warrantyCreateClaim(this.#client, {
      customer_id: "",
      job_address: { line1: draft.addressLine1, city: draft.city, province: "", postal: "" },
      vendor_id: draft.vendorId,
      owner_id: "",
      branch_id: draft.branchId,
      source: draft.source as "INTAKE" | "COUNTER" | "WEB",
      description: draft.description,
      invoice_id: "",
      order_id: "",
      po_id: "",
      supplier_purchase_date: draft.purchaseDateSupplier,
      expected_cents: draft.expectedCents,
      opened_at: draft.openedAt,
      lines: [
        {
          product_id: "",
          product_text: draft.productText,
          qty: draft.qty,
          uom_code: draft.uom,
          purchase_date_supplier: draft.purchaseDateSupplier,
          purchase_date_contractor: "",
          notes: "",
        },
      ],
    });
    return answer.ok ? { ok: true, value: toClaim(answer.value) } : answer;
  }

  /** Move the claim one phase. The server re-checks every gate and names the missing items. */
  async advance(id: string, toPhase: string, note: string, occurredAt: string): Promise<Result<Claim>> {
    const answer = await staffWarranty.warrantyAdvanceClaimPhase(this.#client, id, {
      to_phase: toPhase as "INTAKE" | "TRIAGE" | "SUBMITTED" | "WAITING_ON_DECISION" | "DECISION" | "SETTLEMENT" | "CLOSED" | "REJECTED" | "ABANDONED",
      note,
      occurred_at: occurredAt,
    });
    return answer.ok ? { ok: true, value: toClaim(answer.value) } : answer;
  }

  /** Move the claim to another vendor; the owner rides through unchanged. */
  async assign(id: string, vendorId: string, ownerId: string): Promise<Result<Claim>> {
    const answer = await staffWarranty.warrantyAssignClaim(this.#client, id, { vendor_id: vendorId, owner_id: ownerId });
    return answer.ok ? { ok: true, value: toClaim(answer.value) } : answer;
  }

  /** Tick one checklist item, on or off. */
  async tick(id: string, itemKey: string, checked: boolean): Promise<Result<ChecklistState>> {
    const answer = await staffWarranty.warrantyTickClaimChecklist(this.#client, id, { item_key: itemKey, checked });
    return answer.ok ? { ok: true, value: toChecklistState(answer.value) } : answer;
  }

  /** Record where a piece of evidence is kept. The file itself is not carried yet. */
  async attachEvidence(id: string, evidenceRef: string, note: string): Promise<Result<ClaimEvent>> {
    const answer = await staffWarranty.warrantyAttachClaimEvidence(this.#client, id, { evidence_ref: evidenceRef, note });
    return answer.ok ? { ok: true, value: toClaimEvent(answer.value) } : answer;
  }

  /**
   * Record the settlement. This is the one money write the warranty surface
   * owns, and it never queues: the call goes to the server and the answer's
   * own figures are what the screen shows, success or refusal.
   */
  async settle(
    id: string,
    input: { outcome: string; expectedCents: number | null; realizedCents: number; note: string; manualOverride: boolean; overrideReason: string },
  ): Promise<Result<SettlementResult>> {
    const answer = await staffWarranty.warrantySettleClaim(this.#client, id, {
      outcome: input.outcome as "VENDOR_CREDIT" | "REPLACEMENT_STOCK" | "CUSTOMER_ACCOMMODATION" | "WRITE_OFF",
      expected_cents: input.expectedCents,
      realized_cents: input.realizedCents,
      note: input.note,
      manual_override: input.manualOverride,
      override_reason: input.overrideReason,
    });
    return answer.ok ? { ok: true, value: toSettlementResult(answer.value) } : answer;
  }

  /** The settlement's postings: what moved, where, and what it did. */
  async postings(id: string): Promise<Result<readonly SettlementPosting[]>> {
    return pageItems(await staffWarranty.warrantyListClaimPostings(this.#client, id, { limit: 50 }), toSettlementPosting);
  }

  /** The photos the public intake attached to the claim. */
  async intakePhotos(id: string): Promise<Result<readonly IntakePhoto[]>> {
    return pageItems(await staffWarranty.warrantyListIntakePhotos(this.#client, id, { limit: 50 }), toIntakePhoto);
  }

  /** One photo's bytes, for the browser to hold as an object address. */
  async intakePhotoBytes(id: string, photoId: string): Promise<Result<Uint8Array>> {
    return staffWarranty.warrantyGetIntakePhotoBytes(this.#client, id, photoId);
  }

  /** The FR-7 headline bundle: funnel, by manufacturer, time to settlement, exposure, age. */
  async reportBundle(branchId: string): Promise<Result<ReportBundle>> {
    const answer = await staffWarranty.warrantyGetReports(this.#client, branchId === "" ? {} : { branch_id: branchId });
    return answer.ok ? { ok: true, value: toReportBundle(answer.value) } : answer;
  }

  /** The recovery worklist: agreed money not yet received. */
  async recoveryBoard(branchId: string, openOnly: boolean): Promise<Result<RecoveryBoardModel>> {
    const opts = { ...(branchId === "" ? {} : { branch_id: branchId }), open_only: openOnly };
    const answer = await staffWarranty.warrantyGetRecoveryBoard(this.#client, opts);
    return answer.ok ? { ok: true, value: toRecoveryBoard(answer.value) } : answer;
  }

  /** One claim's chase log. */
  async recoveryActions(claimId: string): Promise<Result<readonly RecoveryAction[]>> {
    return pageItems(await staffWarranty.warrantyListRecoveryActions(this.#client, claimId, { limit: 100 }), toRecoveryAction);
  }

  /** Log one chase action. A receipt that exceeds the accrual is the server's refusal, in its words. */
  async logRecoveryAction(
    claimId: string,
    input: { action: string; amountCents: number; promisedDate: string; reference: string; note: string; occurredAt: string },
  ): Promise<Result<RecoveryAction>> {
    const answer = await staffWarranty.warrantyLogRecoveryAction(this.#client, claimId, {
      action: input.action as "CONTACT_MADE" | "PROMISED" | "RECEIVED" | "DISPUTED" | "NOTE",
      amount_cents: input.amountCents,
      promised_date: input.promisedDate,
      reference: input.reference,
      note: input.note,
      occurred_at: input.occurredAt,
    });
    return answer.ok ? { ok: true, value: toRecoveryAction(answer.value) } : answer;
  }

  /** The service level board: timers and open alerts. The board's own target rows are dropped here;
   *  the configured targets section reads the route whose whole job is listing targets. */
  async slaBoard(branchId: string, includeAcknowledged: boolean): Promise<Result<SlaBoardModel>> {
    const opts = { ...(branchId === "" ? {} : { branch_id: branchId }), include_acknowledged: includeAcknowledged };
    const answer = await staffWarranty.warrantyGetSLABoard(this.#client, opts);
    return answer.ok ? { ok: true, value: toSlaBoard(answer.value) } : answer;
  }

  /** The configured per-phase targets, and any per-vendor override of one. */
  async slaTargets(): Promise<Result<readonly SlaTarget[]>> {
    return pageItems(await staffWarranty.warrantyListSLATargets(this.#client, { limit: 100 }), toSlaTarget);
  }

  /** Run the overdue sweep now. Manager-gated; a refusal arrives in the core's words. */
  async sweepSla(branchId: string): Promise<Result<SlaSweepResult>> {
    const answer = await staffWarranty.warrantySweepSLA(this.#client, branchId === "" ? {} : { branch_id: branchId });
    if (!answer.ok) return answer;
    return { ok: true, value: { count: num(bag(answer.value).count) } };
  }

  /** Acknowledge one breach alert. */
  async ackSlaAlert(claimId: string, alertId: string): Promise<Result<true>> {
    const answer = await staffWarranty.warrantyAckSLAAlert(this.#client, claimId, alertId);
    return answer.ok ? { ok: true, value: true } : answer;
  }

  /** The vendor warranty registry. */
  async vendorProfiles(): Promise<Result<readonly VendorProfile[]>> {
    return pageItems(await staffWarranty.warrantyListVendorProfiles(this.#client, { limit: 200 }), toVendorProfile);
  }

  /** One vendor's profile, overrides and document list included. */
  async vendorProfile(vendorId: string): Promise<Result<VendorProfile>> {
    const answer = await staffWarranty.warrantyGetVendorProfile(this.#client, vendorId);
    return answer.ok ? { ok: true, value: toVendorProfile(answer.value) } : answer;
  }

  /** Write one vendor's warranty terms. The core gates the write to its tier 1 roles. */
  async upsertVendorProfile(vendorId: string, draft: VendorDraft): Promise<Result<VendorProfile>> {
    const answer = await staffWarranty.warrantyUpsertVendorProfile(this.#client, vendorId, {
      vendor_id: vendorId,
      status: draft.status as "DOCUMENTED" | "UNDOCUMENTED",
      coverage: draft.coverage,
      term_months: draft.termMonths,
      channel: draft.channel as "EMAIL" | "PORTAL" | "REP" | "PHONE" | "OTHER",
      channel_ref: draft.channelRef,
      docs: [],
      notes: draft.notes,
    });
    return answer.ok ? { ok: true, value: toVendorProfile(answer.value) } : answer;
  }

  /** Add one product's override terms to a vendor's profile. The core gates the write to its tier 1 roles. */
  async addProductOverride(
    vendorId: string,
    input: { productId: string; coverage: string; termMonths: number | null; notes: string },
  ): Promise<Result<ProductOverride>> {
    const answer = await staffWarranty.warrantyAddProductOverride(this.#client, vendorId, {
      profile_id: "",
      product_id: input.productId,
      coverage: input.coverage,
      term_months: input.termMonths,
      notes: input.notes,
    });
    return answer.ok ? { ok: true, value: toProductOverride(answer.value) } : answer;
  }
}
