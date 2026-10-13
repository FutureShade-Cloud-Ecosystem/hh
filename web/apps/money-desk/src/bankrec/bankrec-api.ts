// The bank reconciliation slice's one door to the core: every read and write over the generated client, answering
// a Result and never throwing. Every route here is the core's existing surface (core/internal/bankrec); nothing new
// was asked of the core.
//
// Reads (any staff): the accounts, the statement lines (the contract's cursor page), the match history of a line,
// the match rules, the reconciliations. Writes: import a statement, run the auto match, pair a line by hand, undo
// a match, approve a rule version, open a month and sign it off.
//
// The core answers its lines list with the contract's page (items, next_cursor) and its other four lists with a
// bare array where the contract says a page. A list is read as either (listOf) and anything else is not a list.
//
// None of these routes takes an idempotency key (the contract has none). What stands in for it: a re-import is
// idempotent on the line hash, an auto match only sees still unmatched lines, opening a month is an upsert, and a
// second manual match or undo is refused by the core in words. The desk also ignores a press while one is in flight.
import {
  staffBankrec,
  staffGl,
  type ApiClient,
  type BankRecAccount,
  type BankRecImportResult,
  type BankRecMatch,
  type BankRecMatchRule,
  type BankRecMatchSummary,
  type BankRecReconciliation,
  type BankRecStatementLine,
  type GlJournal,
  type Result,
} from "@hardscapehouse/api-client";
import { listOf } from "./bankrec-logic.js";

export type LineStatus = "UNMATCHED" | "MATCHED" | "EXCLUDED";
export type TargetType = "PAYMENT" | "LODGEMENT" | "JOURNAL" | "GROUP";

export interface LinesPage {
  readonly items: BankRecStatementLine[];
  readonly next: string | null;
}

const PAGE = 50;

function readList<T>(answer: Result<unknown>, what: string): Result<T[]> {
  if (!answer.ok) return answer;
  const rows = listOf<T>(answer.value);
  return rows === null ? { ok: false, error: `The ${what} were not readable.` } : { ok: true, value: rows };
}

export class BankrecApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  async accounts(): Promise<Result<BankRecAccount[]>> {
    return readList<BankRecAccount>(await staffBankrec.bankrecListAccounts(this.#client), "accounts");
  }

  /** One page of an account's statement lines, newest first; the core refuses a call with no account. */
  async lines(accountId: string, status: LineStatus | "", cursor: string | null): Promise<Result<LinesPage>> {
    const answer = await staffBankrec.bankrecListLines(this.#client, {
      bank_account_id: accountId,
      limit: PAGE,
      ...(status === "" ? {} : { status }),
      ...(cursor === null ? {} : { cursor }),
    });
    if (!answer.ok) return answer;
    if (!Array.isArray(answer.value.items)) return { ok: false, error: "The statement lines were not readable." };
    const next = answer.value.next_cursor;
    return { ok: true, value: { items: answer.value.items, next: next === undefined || next === null || next === "" ? null : next } };
  }

  importStatement(accountId: string, profileCode: string, filename: string, content: string): Promise<Result<BankRecImportResult>> {
    return staffBankrec.bankrecCreateImport(this.#client, { bank_account_id: accountId, profile_code: profileCode, filename, content });
  }

  autoMatch(accountId: string, from: string, to: string): Promise<Result<BankRecMatchSummary>> {
    return staffBankrec.bankrecMatch(this.#client, { mode: "auto", bank_account_id: accountId, ...(from === "" ? {} : { from }), ...(to === "" ? {} : { to }) });
  }

  manualMatch(lineId: string, targetType: TargetType, targetRefId: string): Promise<Result<unknown>> {
    return staffBankrec.bankrecMatch(this.#client, {
      mode: "manual",
      statement_line_id: lineId,
      target_type: targetType,
      ...(targetRefId === "" ? {} : { target_ref_id: targetRefId }),
    });
  }

  async history(lineId: string): Promise<Result<BankRecMatch[]>> {
    return readList<BankRecMatch>(await staffBankrec.bankrecListMatches(this.#client, { statement_line_id: lineId }), "matches");
  }

  unmatch(matchId: string): Promise<Result<unknown>> {
    return staffBankrec.bankrecUnmatch(this.#client, { match_id: matchId });
  }

  async rules(): Promise<Result<BankRecMatchRule[]>> {
    return readList<BankRecMatchRule>(await staffBankrec.bankrecListRules(this.#client), "match rules");
  }

  createRule(code: string, name: string, priority: number, spec: Record<string, unknown>): Promise<Result<BankRecMatchRule>> {
    // The contract types a spec by its kind alone; the core stores the whole object (decodeSpec reads the rest).
    return staffBankrec.bankrecCreateRule(this.#client, { code, name, priority, spec: spec as { kind?: "gateway_ref_exact" | "lodgement_ref_exact" | "descriptor_fuzzy" } });
  }

  async reconciliations(accountId: string): Promise<Result<BankRecReconciliation[]>> {
    return readList<BankRecReconciliation>(
      await staffBankrec.bankrecListReconciliations(this.#client, accountId === "" ? {} : { bank_account_id: accountId }),
      "reconciliations",
    );
  }

  openReconciliation(accountId: string, year: number, month: number, statementBalanceCents: number): Promise<Result<BankRecReconciliation>> {
    return staffBankrec.bankrecOpenReconciliation(this.#client, { bank_account_id: accountId, year, month, statement_balance_cents: statementBalanceCents });
  }

  completeReconciliation(id: string): Promise<Result<BankRecReconciliation>> {
    return staffBankrec.bankrecCompleteReconciliation(this.#client, id);
  }

  /** The posted journals a statement line can be paired to; the ledger's own register, read only. */
  async postedJournals(): Promise<Result<GlJournal[]>> {
    const answer = await staffGl.glListJournals(this.#client, { limit: 100 });
    if (!answer.ok) return answer;
    if (!Array.isArray(answer.value.items)) return { ok: false, error: "The journal register was not readable." };
    return { ok: true, value: answer.value.items.filter((j) => j.derived_status === "POSTED") };
  }
}
