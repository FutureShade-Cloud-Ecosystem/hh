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
// The approvals list and the journal register are the contract's pages (items,
// next_cursor); the branches are the contract's plain array. Each is read as
// its contract says and nothing else is accepted.
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

/** A hard stop on cursor following, so a server that never ends cannot hold the desk. */
const MAX_PAGES = 50;

export class GlApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** Every pending approval the caller may see, following the page's cursor to its end. */
  async pendingApprovals(): Promise<Result<IdentityApproval[]>> {
    const rows: IdentityApproval[] = [];
    let cursor: string | undefined;
    for (let pages = 0; pages < MAX_PAGES; pages += 1) {
      const answer = await staffIdentity.identityListApprovals(this.#client, { status: "PENDING", limit: 100, ...(cursor === undefined ? {} : { cursor }) });
      if (!answer.ok) return answer;
      if (!Array.isArray(answer.value.items)) return { ok: false, error: "The approvals list was not readable." };
      rows.push(...answer.value.items);
      const next = answer.value.next_cursor;
      if (next === undefined || next === null || next === "") return { ok: true, value: rows };
      cursor = next;
    }
    return { ok: false, error: "The approvals list did not end." };
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
    if (!Array.isArray(answer.value.items)) return { ok: false, error: "The journal register was not readable." };
    return { ok: true, value: answer.value.items };
  }

  trialBalance(): Promise<Result<GlTrialBalanceResult>> {
    return staffGl.glTrialBalance(this.#client);
  }

  branches(): Promise<Result<Branch[]>> {
    return staffBranches.branchesList(this.#client);
  }
}
