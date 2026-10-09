// The ledger slice's one door to the core: the reads and the one write the
// Financial Controller makes, over the generated client, answering a Result
// and never throwing. Every route here is the core's existing surface
// (approvals under /api/v1/staff, the ledger under /api/v1/gl); nothing new
// was asked of the core.
//
// Reads: the pending approvals, a journal, the journal register, the trial
// balance, the branches (for the words on a row). Write: decide an approval,
// which is also how a parked journal is approved: the core posts the journal
// in the same transaction that stamps the request.
//
// The approvals list is read through readList because the core answers a bare
// array where the contract says a page object.
import {
  staffBranches,
  staffGl,
  staffIdentity,
  type ApiClient,
  type Branch,
  type GlJournal,
  type GlTrialBalanceResult,
  type IdentityApproval,
  type Result,
} from "@hardscapehouse/api-client";
import { readList } from "./gl-logic.js";

export class GlApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** Every pending approval the caller may see, whichever shape the core answers. */
  async pendingApprovals(): Promise<Result<IdentityApproval[]>> {
    const answer = await staffIdentity.identityListApprovals(this.#client, { status: "PENDING", limit: 100 });
    if (!answer.ok) return answer;
    const rows = readList<IdentityApproval>(answer.value as unknown);
    if (rows === null) return { ok: false, error: "The approvals list was not readable." };
    return { ok: true, value: rows };
  }

  /** A decision on one request: approve or decline, with the reason the requester reads. */
  decide(id: string, approve: boolean, reason: string): Promise<Result<unknown>> {
    return staffIdentity.identityDecideApproval(this.#client, id, { approve, reason });
  }

  journal(id: string): Promise<Result<GlJournal>> {
    return staffGl.glGetJournal(this.#client, id);
  }

  async journals(): Promise<Result<GlJournal[]>> {
    const answer = await staffGl.glListJournals(this.#client, { limit: 100 });
    if (!answer.ok) return answer;
    const rows = readList<GlJournal>(answer.value as unknown);
    if (rows === null) return { ok: false, error: "The journal register was not readable." };
    return { ok: true, value: rows };
  }

  trialBalance(): Promise<Result<GlTrialBalanceResult>> {
    return staffGl.glTrialBalance(this.#client);
  }

  async branches(): Promise<Result<Branch[]>> {
    const answer = await staffBranches.branchesList(this.#client);
    if (!answer.ok) return answer;
    const rows = readList<Branch>(answer.value as unknown);
    if (rows === null) return { ok: false, error: "The branch list was not readable." };
    return { ok: true, value: rows };
  }
}
