// The ledger slice's one door to the core: the reads and the writes the
// Financial Controller makes, over the generated client, answering a Result
// and never throwing. Every route here is the core's existing surface
// (approvals under /api/v1/staff, the ledger under /api/v1/gl); nothing new
// was asked of the core.
//
// Reads: the pending approvals, a journal, the journal register, the late posted
// journals, the trial balance (by branch, year and month), the branches (for the words
// on a row), the chart, the recurring templates, the periods, the policy, the receivables
// tie out and the legacy import check. Writes: decide an approval (which is also how
// a parked journal is approved: the core posts the journal in the same
// transaction that stamps the request), post a journal, approve, void or
// reverse one, run a template, create or deactivate an account, close or
// reopen a period, close the year, open the next fiscal year, and take the GP parity export.
//
// The approvals list, the journal register and the ledger lists are the
// contract's pages (items, next_cursor); the branches are the contract's plain
// array. Each is read as its contract says and nothing else is accepted.
import {
  staffBranches,
  staffGl,
  staffIdentity,
  staffSettings,
  type ApiClient,
  type Branch,
  type GlAccount,
  type GlAccountCreate,
  type GlBisTrackTieoutReport,
  type GlExportResult,
  type GlJournal,
  type GlJournalCreate,
  type GlPeriod,
  type GlPolicy,
  type GlPostingQueueCounts,
  type GlRecurringTemplate,
  type GlTieoutState,
  type GlTrialBalanceResult,
  type GlYearCloseResult,
  type GlYearOpenResult,
  type IdentityApproval,
  type Result,
} from "@hardscapehouse/api-client";
import { serveHouseZoneFrom } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { journalListParams, nextCursorOf, type JournalWindow } from "./gl-logic.js";

/** A hard stop on cursor following, so a server that never ends cannot hold the desk. */
export const MAX_PAGES = 50;

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

  /**
   * One page of the journal register, the newest hundred first, within the applied posted date range:
   * the rows and the core's way to the page after them, "" when this page is the end. The caller
   * follows that cursor one page at a time and appends; the desk's own reads stay the contract's pages.
   */
  async journalsPage(window: JournalWindow): Promise<Result<{ items: GlJournal[]; nextCursor: string }>> {
    const answer = await staffGl.glListJournals(this.#client, journalListParams(window));
    if (!answer.ok) return answer;
    if (!Array.isArray(answer.value.items)) return { ok: false, error: "The journal register was not readable." };
    return { ok: true, value: { items: answer.value.items, nextCursor: nextCursorOf(answer.value) } };
  }

  /** The journals that carry a date from a closed month and landed in an open one: the Controller's late posted list. */
  async latePostedJournals(): Promise<Result<GlJournal[]>> {
    const answer = await staffGl.glListJournals(this.#client, { late_posted: true, limit: 100 });
    if (!answer.ok) return answer;
    if (!Array.isArray(answer.value.items)) return { ok: false, error: "The late posted journals were not readable." };
    return { ok: true, value: answer.value.items };
  }

  /** The trial balance, for every branch or for one, for the year or for one month (the route takes branch_id, year, month). */
  trialBalance(branchId = "", year = 0, month = 0): Promise<Result<GlTrialBalanceResult>> {
    return staffGl.glTrialBalance(this.#client, {
      ...(branchId === "" ? {} : { branch_id: branchId }),
      ...(year > 0 ? { year } : {}),
      ...(month > 0 ? { month } : {}),
    });
  }

  /** Every item of a contract page, following the cursor to its end. */
  async #items<T>(what: string, read: (cursor: string | undefined) => Promise<Result<{ items: T[]; next_cursor?: string | null }>>): Promise<Result<T[]>> {
    const rows: T[] = [];
    let cursor: string | undefined;
    for (let pages = 0; pages < MAX_PAGES; pages += 1) {
      const answer = await read(cursor);
      if (!answer.ok) return answer;
      if (!Array.isArray(answer.value.items)) return { ok: false, error: `The ${what} was not readable.` };
      rows.push(...answer.value.items);
      const next = answer.value.next_cursor;
      if (next === undefined || next === null || next === "") return { ok: true, value: rows };
      cursor = next;
    }
    return { ok: false, error: `The ${what} did not end.` };
  }

  accounts(): Promise<Result<GlAccount[]>> {
    return this.#items("chart of accounts", (cursor) => staffGl.glListAccounts(this.#client, { limit: 100, ...(cursor === undefined ? {} : { cursor }) }));
  }

  createAccount(body: GlAccountCreate): Promise<Result<GlAccount>> {
    return staffGl.glCreateAccount(this.#client, body);
  }

  deactivateAccount(code: string): Promise<Result<GlAccount>> {
    return staffGl.glDeactivateAccount(this.#client, code);
  }

  /** Posts a manual journal; at or over the policy threshold the core parks it for a second approver. */
  createJournal(body: GlJournalCreate): Promise<Result<GlJournal>> {
    return staffGl.glCreateJournal(this.#client, body);
  }

  /** The checker half on the journal's own route; the approvals queue stays the way a parked journal is decided. */
  approveJournal(id: string): Promise<Result<GlJournal>> {
    return staffGl.glApproveJournal(this.#client, id);
  }

  voidJournal(id: string): Promise<Result<GlJournal>> {
    return staffGl.glVoidJournal(this.#client, id);
  }

  reverseJournal(id: string): Promise<Result<GlJournal>> {
    return staffGl.glReverseJournal(this.#client, id);
  }

  recurring(): Promise<Result<GlRecurringTemplate[]>> {
    return this.#items("recurring templates", (cursor) => staffGl.glListRecurring(this.#client, { limit: 100, ...(cursor === undefined ? {} : { cursor }) }));
  }

  runRecurring(id: string): Promise<Result<GlJournal>> {
    return staffGl.glRunRecurring(this.#client, id);
  }

  periods(): Promise<Result<GlPeriod[]>> {
    return this.#items("fiscal periods", (cursor) => staffGl.glListPeriods(this.#client, { limit: 100, ...(cursor === undefined ? {} : { cursor }) }));
  }

  /** Every fiscal period along with the core's answer for the next year the Controller can open early (0 when none can). */
  async periodsPage(): Promise<Result<{ items: GlPeriod[]; nextOpenableYear: number }>> {
    const rows: GlPeriod[] = [];
    let nextOpenableYear = 0;
    let cursor: string | undefined;
    for (let pages = 0; pages < MAX_PAGES; pages += 1) {
      const answer = await staffGl.glListPeriods(this.#client, { limit: 100, ...(cursor === undefined ? {} : { cursor }) });
      if (!answer.ok) return answer;
      if (!Array.isArray(answer.value.items)) return { ok: false, error: "The fiscal periods was not readable." };
      if (pages === 0 && typeof answer.value.next_openable_year === "number" && answer.value.next_openable_year > 0) {
        nextOpenableYear = answer.value.next_openable_year;
      }
      rows.push(...answer.value.items);
      const next = answer.value.next_cursor;
      if (next === undefined || next === null || next === "") return { ok: true, value: { items: rows, nextOpenableYear } };
      cursor = next;
    }
    return { ok: false, error: "The fiscal periods did not end." };
  }

  closePeriod(id: string, reason: string): Promise<Result<GlPeriod>> {
    return staffGl.glClosePeriod(this.#client, id, reason === "" ? {} : { reason });
  }

  reopenPeriod(id: string, reason: string): Promise<Result<GlPeriod>> {
    return staffGl.glReopenPeriod(this.#client, id, { reason });
  }

  closeYear(year: number, reason: string): Promise<Result<GlYearCloseResult>> {
    return staffGl.glCloseYear(this.#client, { year, reason });
  }

  /** The Controller's early open of the next fiscal year: the roll forward the core does on its own, asked for by hand with a reason kept. */
  openNextYear(reason: string, year?: number): Promise<Result<GlYearOpenResult>> {
    return staffGl.glOpenYear(this.#client, year !== undefined && year > 0 ? { year, reason } : { reason });
  }

  policy(branchId = ""): Promise<Result<GlPolicy>> {
    return staffGl.glGetPolicy(this.#client, branchId === "" ? {} : { branch_id: branchId });
  }

  /** The receivables subledger against account 1100, for every branch or one. */
  tieOut(branchId = ""): Promise<Result<GlTieoutState[]>> {
    return this.#items("tie out", (cursor) => staffGl.glTieOut(this.#client, { limit: 100, ...(branchId === "" ? {} : { branch_id: branchId }), ...(cursor === undefined ? {} : { cursor }) }));
  }

  /** The posting queue's two counts (waiting, dead lettered), the banner's figures. */
  async postingQueueCount(): Promise<Result<GlPostingQueueCounts>> {
    return staffGl.glPostingQueueCount(this.#client);
  }

  /**
   * The legacy system this dealer imports from, read from the money-desk surface config. "" when the
   * dealer names none, and "" when the read fails: the legacy import check stays off rather than
   * drawn for a dealer that may not have one.
   */
  async legacyImportSource(): Promise<string> {
    const config = await staffSettings.settingsGetSurfaceConfig(this.#client, "money-desk", {});
    // The dealer's business day zone rides on this read; the desk's day words follow it.
    if (config.ok) serveHouseZoneFrom(config.value);
    return config.ok ? (config.value.legacy_import_source ?? "") : "";
  }

  bisTrack(year: number, month: number, toleranceCents: number, branchId = ""): Promise<Result<GlBisTrackTieoutReport>> {
    return staffGl.glBisTrackTieOut(this.#client, { year, month, tolerance_cents: toleranceCents, ...(branchId === "" ? {} : { branch_id: branchId }) });
  }

  /** The GP parity export: month 0 is the whole year, an empty branch is every branch. */
  exportBooks(year: number, month: number, branchId: string): Promise<Result<GlExportResult>> {
    return staffGl.glExport(this.#client, { year, month, branch_id: branchId });
  }

  branches(): Promise<Result<Branch[]>> {
    return staffBranches.branchesList(this.#client);
  }
}
