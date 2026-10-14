// The compliance slice's one door to the core: every read and write over the generated client, answering a Result
// and never throwing. Every route here is the core's existing surface (core/internal/compliance, and the lien jobs
// read for the words on a review subject); nothing new was asked of the core.
//
// Reads: the calendar (every deadline, optionally one status), the upcoming deadlines inside a horizon, a review
// status. Write: set a review status (the core audits it). The route has no idempotency key; it is an upsert on
// the subject, so a second press with the same form changes nothing, and the desk ignores a press in flight.
//
// An empty calendar answers {"items": null} in the core's own way; listOf reads it as an empty list.
import {
  staffCompliance,
  staffLien,
  type ApiClient,
  type ComplianceDeadline,
  type ComplianceReviewStatus,
  type ComplianceReviewStatusInput,
  type LienJob,
  type Result,
} from "@hardscapehouse/api-client";
import { listOf } from "../lien/lien-logic.js";

export type StatusFilter = "" | "OPEN" | "OVERDUE" | "SATISFIED" | "WAIVED";

function readList<T>(answer: Result<unknown>, what: string): Result<T[]> {
  if (!answer.ok) return answer;
  const rows = listOf<T>(answer.value);
  return rows === null ? { ok: false, error: `The ${what} were not readable.` } : { ok: true, value: rows };
}

export class ComplianceApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  async calendar(status: StatusFilter): Promise<Result<ComplianceDeadline[]>> {
    return readList<ComplianceDeadline>(await staffCompliance.complianceListCalendar(this.#client, { limit: 100, ...(status === "" ? {} : { status }) }), "deadlines");
  }

  /** The open deadlines due inside the horizon; "default" leaves the horizon to the core. */
  async upcoming(horizon: number | "default"): Promise<Result<ComplianceDeadline[]>> {
    return readList<ComplianceDeadline>(await staffCompliance.complianceListUpcoming(this.#client, { limit: 100, ...(horizon === "default" ? {} : { horizon_days: horizon }) }), "upcoming deadlines");
  }

  reviewStatus(subjectType: ComplianceReviewStatusInput["subject_type"], subjectId: string): Promise<Result<ComplianceReviewStatus>> {
    return staffCompliance.complianceGetReviewStatus(this.#client, { subject_type: subjectType, subject_id: subjectId });
  }

  setReviewStatus(body: ComplianceReviewStatusInput): Promise<Result<ComplianceReviewStatus>> {
    return staffCompliance.complianceUpsertReviewStatus(this.#client, body);
  }

  /** The jobs a review can be about, read only. */
  async jobs(): Promise<Result<LienJob[]>> {
    return readList<LienJob>(await staffLien.lienListJobs(this.#client, { limit: 100 }), "jobs");
  }
}
