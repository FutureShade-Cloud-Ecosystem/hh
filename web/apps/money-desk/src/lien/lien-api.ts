// The liens slice's one door to the core: every read and write over the generated client, answering a Result and
// never throwing. Every route here is the core's existing surface (core/internal/lien, and the roster and the
// branches for the words on a row); nothing new was asked of the core.
//
// Reads: the jobs, one job, the Monday triage, the holdback aging, a job's documents, a document's versions, the
// shared review status banner, the branches, the staff roster (for the co-signer pick). Writes: create a job, sync
// a job's holdbacks, render a document, release a holdback.
//
// None of the write routes takes an idempotency key (the contract has none). What stands in for it: the holdback
// sync is idempotent on the invoice (a re-run books nothing new), the release's journal is idempotent on the job in
// the ledger and a second release finds nothing outstanding, and a document render is append only (a second render
// is the next version, by design). The desk also ignores a press while one is in flight.
//
// The core answers each list as the contract's page (items, next_cursor) with no cursor in practice: it returns
// every row, and an empty list says {"items": null}. A list is read as either (listOf) and anything else is not a
// list.
import {
  staffBranches,
  staffCompliance,
  staffIdentity,
  staffLien,
  type ApiClient,
  type Branch,
  type ComplianceReviewStatus,
  type IdentityStaffMember,
  type LienAgingRow,
  type LienDocument,
  type LienJob,
  type LienJobInput,
  type LienReleaseResult,
  type LienSyncResult,
  type LienTriageItem,
  type Result,
} from "@hardscapehouse/api-client";
import { COCKPIT_SUBJECT } from "../compliance/compliance-logic.js";
import { listOf } from "./lien-logic.js";

function readList<T>(answer: Result<unknown>, what: string): Result<T[]> {
  if (!answer.ok) return answer;
  const rows = listOf<T>(answer.value);
  return rows === null ? { ok: false, error: `The ${what} were not readable.` } : { ok: true, value: rows };
}

export class LienApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  async jobs(): Promise<Result<LienJob[]>> {
    return readList<LienJob>(await staffLien.lienListJobs(this.#client, { limit: 100 }), "jobs");
  }

  job(id: string): Promise<Result<LienJob>> {
    return staffLien.lienGetJob(this.#client, id);
  }

  async triage(): Promise<Result<LienTriageItem[]>> {
    return readList<LienTriageItem>(await staffLien.lienGetTriage(this.#client, { limit: 100 }), "triage rows");
  }

  async aging(): Promise<Result<LienAgingRow[]>> {
    return readList<LienAgingRow>(await staffLien.lienGetHoldbackAging(this.#client, { limit: 100 }), "holdback aging rows");
  }

  async documents(jobId: string): Promise<Result<LienDocument[]>> {
    return readList<LienDocument>(await staffLien.lienListJobDocuments(this.#client, jobId, { limit: 100 }), "documents");
  }

  async versions(documentId: string): Promise<Result<LienDocument[]>> {
    return readList<LienDocument>(await staffLien.lienListDocumentVersions(this.#client, documentId, { limit: 100 }), "document versions");
  }

  /** The cockpit's shared banner: the core's own state and words for the one cockpit subject. */
  reviewStatus(): Promise<Result<ComplianceReviewStatus>> {
    return staffCompliance.complianceGetReviewStatus(this.#client, { subject_type: "OTHER", subject_id: COCKPIT_SUBJECT });
  }

  branches(): Promise<Result<Branch[]>> {
    return staffBranches.branchesList(this.#client);
  }

  /** The staff roster, for the co-signer pick. It is the dealer admin's read, so a role without it hears the core's refusal. */
  async roster(): Promise<Result<IdentityStaffMember[]>> {
    return readList<IdentityStaffMember>(await staffIdentity.identityListStaff(this.#client, { limit: 100 }), "staff");
  }

  createJob(body: LienJobInput): Promise<Result<LienJob>> {
    return staffLien.lienCreateJob(this.#client, body);
  }

  syncHoldbacks(jobId: string): Promise<Result<LienSyncResult>> {
    return staffLien.lienSyncJobHoldbacks(this.#client, jobId);
  }

  createDocument(jobId: string, templateKind: LienDocument["template_kind"], data: Record<string, string>): Promise<Result<LienDocument>> {
    return staffLien.lienCreateDocument(this.#client, { job_id: jobId, template_kind: templateKind, data });
  }

  /** The release asks the core for the dual gate: the General Manager releases and names a co-signer, whose authority the core resolves itself. */
  release(jobId: string, approverId: string, approverRole: string): Promise<Result<LienReleaseResult>> {
    return staffLien.lienReleaseHoldback(this.#client, jobId, { approver_id: approverId, approver_role: approverRole });
  }
}
