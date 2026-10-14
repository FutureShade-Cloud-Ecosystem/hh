// The tax slice's one door to the core: every read and write over the generated client, answering a Result and
// never throwing. Every route here is the core's existing surface (core/internal/tax); nothing new was asked of
// the core.
//
// Reads (a manager's): the rate table, the exemption profiles, the review queue, the config, the evidence list and
// one evidence record, the reconciliation. Writes (an administrator's): a new rate row, a profile, the config and
// the recompute. Capturing evidence is any staff member's. Viewing, downloading and validating an evidence record
// each write an access row on the core's side, so the desk asks for them only on a press.
//
// The core answers its four lists under a name (rates, profiles, pending_profiles, evidence) where the contract
// says a page of items, and an empty list as null. A list is read as any of those (listOf) and anything else is
// not a list.
//
// None of these routes takes an idempotency key (the contract has none). What stands in for it: the rate table is
// append only and a repeat is a second dated row the person sees, a profile is an upsert on its code, the config
// is an upsert, the recompute is idempotent (a second run skips what the first added), and an evidence capture is
// held to one press at a time by the desk. The desk also ignores a press while one is in flight.
import {
  staffTax,
  type ApiClient,
  type Result,
  type TaxConfig,
  type TaxEvidence,
  type TaxRate,
  type TaxReasonCode,
  type TaxProfileStatus,
  type TaxRecomputeResult,
  type TaxReconReport,
  type TaxReliefEligibility,
  type TaxStatusCardProfile,
} from "@hardscapehouse/api-client";
import { listOf } from "./tax-logic.js";

export interface RateInput {
  readonly jurisdiction: string;
  readonly reasonCode: TaxReasonCode;
  readonly rateBps: number;
  readonly effectiveFrom: string;
  readonly effectiveTo: string;
  readonly notes: string;
}

export interface ProfileInput {
  readonly code: string;
  readonly name: string;
  readonly rateBps: number;
  readonly requiresPhoto: boolean;
  readonly requiresNumber: boolean;
  readonly requiresAttestation: boolean;
  readonly status: TaxProfileStatus;
  readonly notes: string;
}

export interface CaptureInput {
  readonly cardProfileCode: string;
  readonly orderId: string;
  readonly photoRef: string;
  readonly rawNumber: string;
  readonly attestation: boolean;
  readonly expiry: string;
}

function readList<T>(answer: Result<unknown>, key: string, what: string): Result<T[]> {
  if (!answer.ok) return answer;
  const rows = listOf<T>(answer.value, key);
  return rows === null ? { ok: false, error: `The ${what} were not readable.` } : { ok: true, value: rows };
}

export class TaxApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  async rates(): Promise<Result<TaxRate[]>> {
    return readList<TaxRate>(await staffTax.taxListRates(this.#client), "rates", "rates");
  }

  createRate(input: RateInput): Promise<Result<TaxRate>> {
    return staffTax.taxCreateRate(this.#client, {
      id: "",
      jurisdiction: input.jurisdiction,
      reason_code: input.reasonCode,
      rate_bps: input.rateBps,
      effective_from: input.effectiveFrom,
      ...(input.effectiveTo === "" ? {} : { effective_to: input.effectiveTo }),
      ...(input.notes === "" ? {} : { notes: input.notes }),
    });
  }

  async profiles(): Promise<Result<TaxStatusCardProfile[]>> {
    return readList<TaxStatusCardProfile>(await staffTax.taxListProfiles(this.#client), "profiles", "profiles");
  }

  async reviewQueue(): Promise<Result<TaxStatusCardProfile[]>> {
    return readList<TaxStatusCardProfile>(await staffTax.taxReviewQueue(this.#client), "pending_profiles", "profiles waiting for review");
  }

  saveProfile(input: ProfileInput): Promise<Result<TaxStatusCardProfile>> {
    return staffTax.taxUpsertProfile(this.#client, {
      id: "",
      code: input.code,
      name: input.name,
      rate_bps: input.rateBps,
      requires_photo: input.requiresPhoto,
      requires_number: input.requiresNumber,
      requires_attestation: input.requiresAttestation,
      status: input.status,
      ...(input.notes === "" ? {} : { notes: input.notes }),
    });
  }

  config(): Promise<Result<TaxConfig>> {
    return staffTax.taxGetConfig(this.#client);
  }

  /** The two dormant switches are always sent off: the core refuses to turn either on. */
  saveConfig(registrationNo: string, roundMode: TaxConfig["round_mode"], notes: string): Promise<Result<TaxConfig>> {
    return staffTax.taxUpdateConfig(this.#client, {
      hst_registration_no: registrationNo,
      round_mode: roundMode,
      avalara_enabled: false,
      postal_routing_enabled: false,
      ...(notes === "" ? {} : { notes }),
    });
  }

  async evidence(orderId: string): Promise<Result<TaxEvidence[]>> {
    return readList<TaxEvidence>(await staffTax.taxListEvidence(this.#client, orderId === "" ? {} : { order_id: orderId }), "evidence", "evidence records");
  }

  /** The raw number goes into this one call and no further: it is not kept, returned or shown anywhere. */
  capture(input: CaptureInput): Promise<Result<TaxEvidence>> {
    return staffTax.taxCaptureEvidence(this.#client, {
      profile_id: "",
      card_profile_code: input.cardProfileCode,
      order_id: input.orderId,
      photo_ref: input.photoRef,
      raw_number: input.rawNumber,
      attestation: input.attestation,
      tcrd_expiry: input.expiry,
    });
  }

  view(id: string): Promise<Result<TaxEvidence>> {
    return staffTax.taxViewEvidence(this.#client, id);
  }

  download(id: string): Promise<Result<TaxEvidence>> {
    return staffTax.taxDownloadEvidence(this.#client, id);
  }

  validate(id: string, asOf: string): Promise<Result<TaxReliefEligibility>> {
    return staffTax.taxValidateEvidence(this.#client, id, asOf === "" ? {} : { as_of: asOf });
  }

  recompute(from: string, to: string): Promise<Result<TaxRecomputeResult>> {
    return staffTax.taxRecompute(this.#client, { from_date: from, to_date: to });
  }

  reconcile(year: number, month: number): Promise<Result<TaxReconReport>> {
    return staffTax.taxReconcile(this.#client, month === 0 ? { year } : { year, month });
  }
}
