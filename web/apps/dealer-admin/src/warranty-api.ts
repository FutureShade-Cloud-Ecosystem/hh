// The Warranty surface's one door to the core: the claim threshold's policy
// and the vendors' credit agreements, over the generated api-client,
// answering a Result and never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into
// a narrow type that names only what the screen uses, the settings door's
// rule: a field the projection does not name is dropped at this door and
// never reaches the screen.
//
// THE WRITES GO TO THE SERVER AND NEVER QUEUE. The threshold and each
// vendor's agreement are policy writes: they are awaited Result calls
// against the core, this app holds no outbox and no offline store, and
// nothing about them is ever deferred.
import { ApiClient, staffWarranty, type Result } from "@hardscapehouse/api-client";

/** The effective policy, exactly what the contract returns and no more. */
export interface WarrantyPolicyView {
  readonly branch_id: string;
  readonly claim_threshold_cents: number;
}

/** One vendor's effective agreement, exactly what the contract returns and no more. */
export interface VendorAgreementView {
  readonly vendor_id: string;
  readonly vendor_no: string;
  readonly vendor_name: string;
  readonly agreement_kind: string;
  readonly period: string;
  readonly allowance_bp: number;
}

/** One agreement write payload, the shapes the contract's input schema names. */
export interface AgreementPut {
  readonly agreement_kind: "claim_by_claim" | "periodic_credit_note" | "fixed_allowance";
  readonly period?: "monthly" | "quarterly";
  readonly allowance_bp: number;
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);

const kindOf = (value: unknown): AgreementPut["agreement_kind"] => {
  if (value === "periodic_credit_note" || value === "fixed_allowance") return value;
  return "claim_by_claim";
};

const periodOf = (value: unknown): AgreementPut["period"] => {
  if (value === "monthly" || value === "quarterly") return value;
  return undefined;
};

/** Every call the Warranty surface makes. */
export class WarrantyApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // -- the claim threshold ---------------------------------------------------------------------------

  /** The effective policy (the org default, or a branch's override when asked). */
  async policy(branchId = ""): Promise<Result<WarrantyPolicyView>> {
    const answer = await staffWarranty.warrantyGetPolicy(this.#client, branchId === "" ? undefined : { branch_id: branchId });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return {
      ok: true,
      value: {
        branch_id: text(r.branch_id),
        claim_threshold_cents: num(r.claim_threshold_cents),
      },
    };
  }

  /** Save the threshold, whole, in integer cents. A policy write never queues. */
  async putPolicy(cents: number, branchId = ""): Promise<Result<WarrantyPolicyView>> {
    const answer = await staffWarranty.warrantyPutPolicy(this.#client, {
      branch_id: branchId,
      claim_threshold_cents: cents,
    });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return {
      ok: true,
      value: {
        branch_id: text(r.branch_id),
        claim_threshold_cents: num(r.claim_threshold_cents),
      },
    };
  }

  // -- the vendor credit agreements ------------------------------------------------------------------

  /** Every active vendor with its effective agreement, in vendor name order. */
  async agreements(): Promise<Result<readonly VendorAgreementView[]>> {
    const answer = await staffWarranty.warrantyListVendorAgreements(this.#client);
    if (!answer.ok) return answer;
    const page = bag(answer.value);
    return {
      ok: true,
      value: list(page.items).map((raw) => {
        const r = bag(raw);
        return {
          vendor_id: text(r.vendor_id),
          vendor_no: text(r.vendor_no),
          vendor_name: text(r.vendor_name),
          agreement_kind: kindOf(r.agreement_kind),
          period: periodOf(r.period) ?? "",
          allowance_bp: num(r.allowance_bp),
        };
      }),
    };
  }

  /** Save one vendor's agreement. An agreement write never queues. */
  async putAgreement(vendorId: string, put: AgreementPut): Promise<Result<VendorAgreementView>> {
    const answer = await staffWarranty.warrantyUpsertVendorAgreement(this.#client, vendorId, {
      agreement_kind: put.agreement_kind,
      period: put.period,
      allowance_bp: put.allowance_bp,
    });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return {
      ok: true,
      value: {
        vendor_id: text(r.vendor_id),
        vendor_no: text(r.vendor_no),
        vendor_name: text(r.vendor_name),
        agreement_kind: kindOf(r.agreement_kind),
        period: periodOf(r.period) ?? "",
        allowance_bp: num(r.allowance_bp),
      },
    };
  }
}
