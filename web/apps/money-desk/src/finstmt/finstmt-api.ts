// The statements slice's one door to the core: the statement reads, the books' periods and branches for the
// words on a screen, and the saved pack registry's writes, over the generated client, answering a Result and
// never throwing. Every route here is the core's existing surface (/api/v1/finstmt, /api/v1/gl/periods,
// /api/v1/branches); nothing new was asked of the core.
//
// Each answer is checked against the shape the contract sends before it is handed on; one that is not that
// shape is refused whole with a sentence, never half read. The core's CSV answer is text under a route the
// generated client types as JSON, so the CSV a person downloads is made from the figures on the screen
// (finstmt-logic.ts), not fetched.
import {
  staffBranches,
  staffFinstmt,
  staffGl,
  type ApiClient,
  type Branch,
  type FinStmtAgingSummary,
  type FinStmtBalanceSheetReport,
  type FinStmtComparativeRow,
  type FinStmtGLDetailResponse,
  type FinStmtIncomeStatementReport,
  type FinStmtSchedule,
  type FinStmtScheduleInput,
  type FinStmtTrialBalanceReport,
  type GlPeriod,
  type Result,
} from "@hardscapehouse/api-client";
import { readAging, readBalance, readComparative, readGlDetail, readIncome, readPeriods, readSchedules, readTrial } from "./finstmt-logic.js";

/** A hard stop on cursor following, so a server that never ends cannot hold the desk. */
const MAX_PAGES = 50;

export interface DetailFilter {
  readonly year: number;
  readonly month: number;
  readonly accountCode: string;
  readonly accountType: string;
  readonly branchId: string;
}

function unreadable<T>(what: string): Result<T> {
  return { ok: false, error: `${what} was not in a shape this screen can read.` };
}

function checked<T, U>(answer: Result<T>, read: (v: unknown) => U | null, what: string): Result<U> {
  if (!answer.ok) return answer;
  const value = read(answer.value);
  return value === null ? unreadable(what) : { ok: true, value };
}

const branchOpt = (branchId: string): { branch_id?: string } => (branchId === "" ? {} : { branch_id: branchId });

export class FinstmtApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** Every period the books hold, following the page's cursor to its end. */
  async periods(): Promise<Result<GlPeriod[]>> {
    const rows: GlPeriod[] = [];
    let cursor: string | undefined;
    for (let pages = 0; pages < MAX_PAGES; pages += 1) {
      const answer = await staffGl.glListPeriods(this.#client, { limit: 100, ...(cursor === undefined ? {} : { cursor }) });
      if (!answer.ok) return answer;
      const page = readPeriods(answer.value);
      if (page === null) return unreadable("The list of periods");
      rows.push(...page);
      const next = answer.value.next_cursor;
      if (next === undefined || next === null || next === "") return { ok: true, value: rows };
      cursor = next;
    }
    return { ok: false, error: "The list of periods did not end." };
  }

  branches(): Promise<Result<Branch[]>> {
    return staffBranches.branchesList(this.#client);
  }

  async income(year: number, month: number, branchId: string): Promise<Result<FinStmtIncomeStatementReport>> {
    const answer = await staffFinstmt.finstmtIncomeStatement(this.#client, { year, ...(month > 0 ? { month } : {}), ...branchOpt(branchId) });
    return checked(answer, readIncome, "The income statement");
  }

  async incomeCompare(year: number, month: number, branchId: string): Promise<Result<FinStmtComparativeRow[]>> {
    const answer = await staffFinstmt.finstmtIncomeStatement(this.#client, { year, ...(month > 0 ? { month } : {}), ...branchOpt(branchId), compare: "1" });
    const read = checked(answer, readComparative, "The comparison");
    return read.ok ? { ok: true, value: read.value.rows } : read;
  }

  async balance(year: number, branchId: string): Promise<Result<FinStmtBalanceSheetReport>> {
    const answer = await staffFinstmt.finstmtBalanceSheet(this.#client, { year, ...branchOpt(branchId) });
    return checked(answer, readBalance, "The balance sheet");
  }

  async balanceCompare(year: number, branchId: string): Promise<Result<FinStmtComparativeRow[]>> {
    const answer = await staffFinstmt.finstmtBalanceSheet(this.#client, { year, ...branchOpt(branchId), compare: "1" });
    const read = checked(answer, readComparative, "The comparison");
    return read.ok ? { ok: true, value: read.value.rows } : read;
  }

  async trial(year: number): Promise<Result<FinStmtTrialBalanceReport>> {
    const answer = await staffFinstmt.finstmtTrialBalance(this.#client, { year });
    return checked(answer, readTrial, "The trial balance");
  }

  async glDetail(filter: DetailFilter): Promise<Result<FinStmtGLDetailResponse>> {
    const answer = await staffFinstmt.finstmtGLDetail(this.#client, {
      year: filter.year,
      ...(filter.month > 0 ? { month: filter.month } : {}),
      ...(filter.accountCode === "" ? {} : { account_code: filter.accountCode }),
      ...(filter.accountType === "" ? {} : { account_type: filter.accountType as "ASSET" | "LIABILITY" | "EQUITY" | "REVENUE" | "EXPENSE" }),
      ...branchOpt(filter.branchId),
    });
    return checked(answer, readGlDetail, "The ledger detail");
  }

  async aging(branchId: string): Promise<Result<FinStmtAgingSummary>> {
    const answer = await staffFinstmt.finstmtARAging(this.#client, branchOpt(branchId));
    return checked(answer, readAging, "The receivables aging");
  }

  /**
   * The core's own CSV for one statement, read as text through the staff client so the core stamps the export on
   * its access trail. The generated function for these routes reads JSON, so this goes through the client's text
   * response seam with the contract's operation id and query. The comparison answers JSON whatever the format, so it
   * is not asked for here.
   */
  exportCsv(tab: "income" | "balance" | "trial" | "gl" | "aging", filter: DetailFilter): Promise<Result<string>> {
    const route = {
      income: ["/api/v1/finstmt/income-statement", "finstmtIncomeStatement"],
      balance: ["/api/v1/finstmt/balance-sheet", "finstmtBalanceSheet"],
      trial: ["/api/v1/finstmt/trial-balance", "finstmtTrialBalance"],
      gl: ["/api/v1/finstmt/gl-detail", "finstmtGLDetail"],
      aging: ["/api/v1/finstmt/ar-aging", "finstmtARAging"],
    }[tab];
    const query: Record<string, string | number> = { format: "csv" };
    if (tab !== "aging") query["year"] = filter.year;
    if ((tab === "income" || tab === "gl") && filter.month > 0) query["month"] = filter.month;
    if (tab === "gl" && filter.accountCode !== "") query["account_code"] = filter.accountCode;
    if (tab === "gl" && filter.accountType !== "") query["account_type"] = filter.accountType;
    if (tab !== "trial" && filter.branchId !== "") query["branch_id"] = filter.branchId;
    return this.#client.request<string>({ method: "GET", path: route[0], query, textResponse: true, operationId: route[1] });
  }

  async packs(): Promise<Result<FinStmtSchedule[]>> {
    const answer = await staffFinstmt.finstmtListSchedules(this.#client, { limit: 100 });
    return checked(answer, readSchedules, "The list of saved packs");
  }

  createPack(input: FinStmtScheduleInput): Promise<Result<FinStmtSchedule>> {
    return staffFinstmt.finstmtCreateSchedule(this.#client, input);
  }

  updatePack(id: string, input: FinStmtScheduleInput): Promise<Result<FinStmtSchedule>> {
    return staffFinstmt.finstmtUpdateSchedule(this.#client, id, input);
  }

  deletePack(id: string): Promise<Result<unknown>> {
    return staffFinstmt.finstmtDeleteSchedule(this.#client, id);
  }
}
