// Manager Home's reporting door to the core: the twenty two reporting reads
// and writes, over the generated api-client, answering a Result and never
// throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into a
// narrow type that names only what the reporting panes use. A field the
// projection does not name is dropped at this door and never reaches a
// screen. Money stays integer cents end to end: the core computes it, this
// door carries it, and nothing here sums, converts or derives a figure.
//
// Port provenance: the original app's reporting-api.ts (repository
// hardscapeos_dibbits, read only; see PROVENANCE.md), rebuilt on HH's
// generated client. The original fetched raw JSON and read the error
// envelope itself; here the generated client resolves every refusal to a
// Result and this door whitelists only the answer's shape.
import {
  printReceiptInto,
  openBlankWindow,
} from "@hardscapehouse/pos-kit/src/ported/ports.js";
import { ApiClient, staffReporting, type Result } from "@hardscapehouse/api-client";

/** The outcome of opening a frozen document at the paper: opened, or the blocked pop-up's words. */
export type PrintOutcome = { ok: true } | { ok: false; reason: "popup_blocked"; message: string };

// -- the projected shapes --------------------------------------------------------------

export interface ParamSpecView {
  readonly name: string;
  readonly kind: string;
  readonly required: boolean;
  readonly label: string;
  readonly values: readonly string[];
}

export interface ColumnView {
  readonly name: string;
  readonly label: string;
  readonly kind: string;
  readonly total: boolean;
}

export interface TemplateView {
  readonly code: string;
  readonly name: string;
  readonly category: string;
  readonly description: string;
  readonly legacyName: string;
  readonly params: readonly ParamSpecView[];
  readonly columns: readonly ColumnView[];
}

export interface CategoryView {
  readonly code: string;
  readonly label: string;
  readonly templates: readonly TemplateView[];
}

export interface ReportResultView {
  readonly code: string;
  readonly name: string;
  readonly category: string;
  readonly columns: readonly ColumnView[];
  readonly rows: readonly (readonly unknown[])[];
  readonly rowCount: number;
  readonly totals: Readonly<Record<string, unknown>>;
  readonly params: Readonly<Record<string, string>>;
}

export interface RunView {
  readonly id: string;
  readonly kind: string;
  readonly templateCode: string;
  readonly name: string;
  readonly params: Readonly<Record<string, string>>;
  readonly rowCount: number;
  readonly printJobId: string;
  readonly createdAt: string;
}

export interface SavedViewView {
  readonly id: string;
  readonly templateCode: string;
  readonly name: string;
  readonly params: Readonly<Record<string, string>>;
  readonly isShared: boolean;
}

export interface RuleView {
  readonly id: string;
  readonly repUserId: string;
  readonly repName: string;
  readonly basis: string;
  readonly rateBps: number;
  readonly branchId: string;
  readonly effectiveFrom: string;
  readonly effectiveTo: string;
  readonly isActive: boolean;
  readonly note: string;
}

export interface CommissionLineView {
  readonly invoiceNo: string;
  readonly invoiceDate: string;
  readonly customerName: string;
  readonly productSku: string;
  readonly categoryCode: string;
  readonly qtyBase: number;
  readonly revenueCents: number;
  readonly costCents: number;
  readonly marginCents: number;
  readonly basis: string;
  readonly rateBps: number;
  readonly commissionCents: number;
  readonly noRuleReason: string;
}

export interface PeriodView {
  readonly runId: string;
  readonly repUserId: string;
  readonly repName: string;
  readonly periodStart: string;
  readonly periodEnd: string;
  readonly saleCount: number;
  readonly revenueCents: number;
  readonly costCents: number;
  readonly marginCents: number;
  readonly commissionCents: number;
  readonly unattributedCount: number;
  readonly unattributedCents: number;
  readonly lines: readonly CommissionLineView[];
}

export interface PeriodPageView {
  readonly items: readonly PeriodView[];
  readonly postsToGl: boolean;
  readonly note: string;
}

export interface AccrualLineView {
  readonly repUserId: string;
  readonly repName: string;
  readonly periodCount: number;
  readonly commissionCents: number;
}

export interface AccrualView {
  readonly from: string;
  readonly to: string;
  readonly lines: readonly AccrualLineView[];
  readonly totalCents: number;
  readonly note: string;
}

export interface AttributionMismatchView {
  readonly invoiceNo: string;
  readonly reportRepUserId: string;
  readonly seamRepUserId: string;
  readonly seamLevel: string;
}

export interface AttributionView {
  readonly bound: boolean;
  readonly checked: number;
  readonly mismatches: readonly AttributionMismatchView[];
  readonly explanation: string;
}

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);
const bool = (value: unknown): boolean => value === true;
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);

const stringRecord = (value: unknown): Record<string, string> => {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(bag(value))) if (typeof v === "string") out[k] = v;
  return out;
};

const toParamSpec = (raw: unknown): ParamSpecView => {
  const r = bag(raw);
  return {
    name: text(r.name),
    kind: text(r.kind),
    required: bool(r.required),
    label: text(r.label),
    values: list(r.values).filter((v): v is string => typeof v === "string"),
  };
};

const toColumn = (raw: unknown): ColumnView => {
  const r = bag(raw);
  return { name: text(r.name), label: text(r.label), kind: text(r.kind), total: bool(r.total) };
};

const toTemplate = (raw: unknown): TemplateView => {
  const r = bag(raw);
  return {
    code: text(r.code),
    name: text(r.name),
    category: text(r.category),
    description: text(r.description),
    legacyName: text(r.legacy_name),
    params: list(r.params).map(toParamSpec),
    columns: list(r.columns).map(toColumn),
  };
};

const toRun = (raw: unknown): RunView => {
  const r = bag(raw);
  return {
    id: text(r.id),
    kind: text(r.kind),
    templateCode: text(r.template_code),
    name: text(r.name),
    params: stringRecord(r.params),
    rowCount: num(r.row_count),
    printJobId: text(r.print_job_id),
    createdAt: text(r.created_at),
  };
};

const toSavedView = (raw: unknown): SavedViewView => {
  const r = bag(raw);
  return {
    id: text(r.id),
    templateCode: text(r.template_code),
    name: text(r.name),
    params: stringRecord(r.params),
    isShared: bool(r.is_shared),
  };
};

const toRule = (raw: unknown): RuleView => {
  const r = bag(raw);
  return {
    id: text(r.id),
    repUserId: text(r.rep_user_id),
    repName: text(r.rep_name),
    basis: text(r.basis),
    rateBps: num(r.rate_bps),
    branchId: text(r.branch_id),
    effectiveFrom: text(r.effective_from),
    effectiveTo: text(r.effective_to),
    isActive: bool(r.is_active),
    note: text(r.note),
  };
};

const toCommissionLine = (raw: unknown): CommissionLineView => {
  const r = bag(raw);
  return {
    invoiceNo: text(r.invoice_no),
    invoiceDate: text(r.invoice_date),
    customerName: text(r.customer_name),
    productSku: text(r.product_sku),
    categoryCode: text(r.category_code),
    qtyBase: num(r.qty_base),
    revenueCents: num(r.revenue_cents),
    costCents: num(r.cost_cents),
    marginCents: num(r.margin_cents),
    basis: text(r.basis),
    rateBps: num(r.rate_bps),
    commissionCents: num(r.commission_cents),
    noRuleReason: text(r.no_rule_reason),
  };
};

const toPeriod = (raw: unknown): PeriodView => {
  const r = bag(raw);
  return {
    runId: text(r.run_id),
    repUserId: text(r.rep_user_id),
    repName: text(r.rep_name),
    periodStart: text(r.period_start),
    periodEnd: text(r.period_end),
    saleCount: num(r.sale_count),
    revenueCents: num(r.revenue_cents),
    costCents: num(r.cost_cents),
    marginCents: num(r.margin_cents),
    commissionCents: num(r.commission_cents),
    unattributedCount: num(r.unattributed_count),
    unattributedCents: num(r.unattributed_cents),
    lines: list(r.lines).map(toCommissionLine),
  };
};

const toPeriodPage = (raw: unknown): PeriodPageView => {
  const r = bag(raw);
  return {
    items: list(r.items).map(toPeriod),
    postsToGl: bool(r.posts_to_gl),
    note: text(r.note),
  };
};

const toAccrual = (raw: unknown): AccrualView => {
  const r = bag(raw);
  return {
    from: text(r.from),
    to: text(r.to),
    lines: list(r.lines).map((l) => {
      const b = bag(l);
      return {
        repUserId: text(b.rep_user_id),
        repName: text(b.rep_name),
        periodCount: num(b.period_count),
        commissionCents: num(b.commission_cents),
      };
    }),
    totalCents: num(r.total_cents),
    note: text(r.note),
  };
};

const toAttribution = (raw: unknown): AttributionView => {
  const r = bag(raw);
  return {
    bound: bool(r.bound),
    checked: num(r.checked),
    mismatches: list(r.mismatches).map((m) => {
      const b = bag(m);
      return {
        invoiceNo: text(b.invoice_no),
        reportRepUserId: text(b.report_rep_user_id),
        seamRepUserId: text(b.seam_rep_user_id),
        seamLevel: text(b.seam_level),
      };
    }),
    explanation: text(r.explanation),
  };
};

// -- the door ----------------------------------------------------------------------------

/** Every reporting call the four panes make. */
export class ReportingApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  // -- templates and their results -------------------------------------------------------

  /** The catalogue, grouped by category in the server's order. */
  async templates(): Promise<Result<readonly CategoryView[]>> {
    const answer = await staffReporting.reportingListTemplates(this.#client);
    if (!answer.ok) return answer;
    const categories = list(bag(answer.value).categories).map((c) => {
      const b = bag(c);
      return { code: text(b.code), label: text(b.label), templates: list(b.templates).map(toTemplate) };
    });
    return { ok: true, value: categories };
  }

  /** One template by code, its params and columns fresh from the server. */
  async template(code: string): Promise<Result<TemplateView>> {
    const answer = await staffReporting.reportingGetTemplate(this.#client, code);
    return answer.ok ? { ok: true, value: toTemplate(answer.value) } : answer;
  }

  /**
   * Run a template: the server computes the rows, this door carries them.
   *
   * The run's query keys are the template's declared parameters, which differ
   * per template, so the contract documents them in the operation's
   * description and the generated function carries no query slot. The call
   * rides the generated client's own transport here, on the same path and
   * operationId, with the parameters as the query.
   */
  async runTemplate(code: string, params: Record<string, string>): Promise<Result<ReportResultView>> {
    const answer = await this.#client.request<unknown>({
      method: "GET",
      path: "/api/v1/reporting/templates/{code}/run",
      pathParams: { code },
      query: params,
      operationId: "reportingRunTemplate",
    });
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return {
      ok: true,
      value: {
        code: text(r.code),
        name: text(r.name),
        category: text(r.category),
        columns: list(r.columns).map(toColumn),
        rows: list(r.rows).map((row) => (Array.isArray(row) ? row : [])),
        rowCount: num(r.row_count),
        totals: bag(r.totals),
        params: stringRecord(r.params),
      },
    };
  }

  /**
   * The template's CSV, fetched with the caller's bearer. The query rides the
   * run's own dynamic keys, for the same reason runTemplate's does.
   */
  async exportTemplateCsv(code: string, params: Record<string, string>): Promise<Result<string>> {
    return this.#client.request<string>({
      method: "GET",
      path: "/api/v1/reporting/templates/{code}/export.csv",
      pathParams: { code },
      query: params,
      textResponse: true,
      operationId: "reportingExportTemplateCsv",
    });
  }

  /** Print freezes an artefact server-side and queues the paper. */
  async printTemplate(code: string, params: Record<string, string>, printBranchId: string): Promise<Result<RunView>> {
    const answer = await staffReporting.reportingPrintTemplate(this.#client, code, {
      params,
      print_branch_id: printBranchId,
    });
    return answer.ok ? { ok: true, value: toRun(answer.value) } : answer;
  }

  // -- runs --------------------------------------------------------------------------------

  /** The frozen runs, newest first as the server sends them. */
  async runs(limit = 25): Promise<Result<readonly RunView[]>> {
    const answer = await staffReporting.reportingListRuns(this.#client, { limit });
    if (!answer.ok) return answer;
    return { ok: true, value: list(bag(answer.value).items).map(toRun) };
  }

  /** One run, its params and totals. */
  async run(id: string): Promise<Result<RunView>> {
    const answer = await staffReporting.reportingGetRun(this.#client, id);
    return answer.ok ? { ok: true, value: toRun(answer.value) } : answer;
  }

  /** The frozen artefact's HTML, for opening at the paper. */
  async runDocument(id: string): Promise<Result<string>> {
    return staffReporting.reportingGetRunDocument(this.#client, id);
  }

  // -- saved views ---------------------------------------------------------------------------

  /** The caller's own (and the shared) saved views, optionally for one template. */
  async savedViews(templateCode: string): Promise<Result<readonly SavedViewView[]>> {
    const opts = templateCode === "" ? {} : { template_code: templateCode };
    const answer = await staffReporting.reportingListSavedViews(this.#client, opts);
    if (!answer.ok) return answer;
    return { ok: true, value: list(bag(answer.value).items).map(toSavedView) };
  }

  /** Save the current parameter set under a name, for this template. */
  async saveSavedView(templateCode: string, name: string, params: Record<string, string>, isShared: boolean): Promise<Result<SavedViewView>> {
    const answer = await staffReporting.reportingSaveView(this.#client, {
      template_code: templateCode,
      name,
      params,
      is_shared: isShared,
    });
    return answer.ok ? { ok: true, value: toSavedView(answer.value) } : answer;
  }

  /** Delete one of the caller's own views. */
  async deleteSavedView(id: string): Promise<Result<unknown>> {
    return staffReporting.reportingDeleteSavedView(this.#client, id);
  }

  // -- commission rules ------------------------------------------------------------------------

  async commissionRules(repUserId = ""): Promise<Result<readonly RuleView[]>> {
    const opts = repUserId === "" ? {} : { rep_user_id: repUserId };
    const answer = await staffReporting.reportingListCommissionRules(this.#client, opts);
    if (!answer.ok) return answer;
    return { ok: true, value: list(bag(answer.value).items).map(toRule) };
  }

  async createCommissionRule(write: {
    repUserId: string;
    basis: string;
    rateBps: number;
    effectiveFrom: string;
    effectiveTo?: string;
    branchId?: string;
    note?: string;
  }): Promise<Result<RuleView>> {
    // The wire type is the whole rule, but the create decodes and ignores the
    // stored-only fields (the repo assigns the id, the active flag and the
    // timestamps), so they ride as their zero values rather than invented facts.
    const answer = await staffReporting.reportingCreateCommissionRule(this.#client, {
      id: "",
      rep_user_id: write.repUserId,
      basis: write.basis as "REVENUE" | "MARGIN",
      rate_bps: write.rateBps,
      effective_from: write.effectiveFrom,
      ...(write.effectiveTo !== undefined ? { effective_to: write.effectiveTo } : {}),
      ...(write.branchId !== undefined ? { branch_id: write.branchId } : {}),
      ...(write.note !== undefined ? { note: write.note } : {}),
      is_active: true,
      created_at: "",
    });
    return answer.ok ? { ok: true, value: toRule(answer.value) } : answer;
  }

  async retireCommissionRule(id: string): Promise<Result<unknown>> {
    return staffReporting.reportingRetireCommissionRule(this.#client, id);
  }

  // -- the commission register -----------------------------------------------------------------

  /** What each rep has earned in the window, computed on the fly. */
  async commissionPreview(opts: { from: string; to: string; repUserId?: string; branchId?: string }): Promise<Result<PeriodPageView>> {
    const answer = await staffReporting.reportingCommissionPreview(this.#client, {
      from: opts.from,
      to: opts.to,
      ...(opts.repUserId ? { rep_user_id: opts.repUserId } : {}),
      ...(opts.branchId ? { branch_id: opts.branchId } : {}),
    });
    return answer.ok ? { ok: true, value: toPeriodPage(answer.value) } : answer;
  }

  /** Freeze one rep's period: computed, stored, and the accrual's own row. */
  async computeCommissionRun(repUserId: string, from: string, to: string, branchId: string): Promise<Result<PeriodView>> {
    const answer = await staffReporting.reportingComputeCommissionRun(this.#client, {
      rep_user_id: repUserId,
      from,
      to,
      branch_id: branchId,
    });
    return answer.ok ? { ok: true, value: toPeriod(answer.value) } : answer;
  }

  /** The stored commission runs in a window. */
  async commissionRuns(from: string, to: string, repUserId = ""): Promise<Result<readonly PeriodView[]>> {
    const answer = await staffReporting.reportingListCommissionRuns(this.#client, {
      from,
      to,
      ...(repUserId ? { rep_user_id: repUserId } : {}),
    });
    if (!answer.ok) return answer;
    return { ok: true, value: toPeriodPage(answer.value).items };
  }

  /** One stored period, its drill-through lines included. */
  async commissionRun(id: string): Promise<Result<PeriodView>> {
    const answer = await staffReporting.reportingGetCommissionRun(this.#client, id);
    return answer.ok ? { ok: true, value: toPeriod(answer.value) } : answer;
  }

  async exportCommissionRunCsv(id: string): Promise<Result<string>> {
    return staffReporting.reportingExportCommissionRunCsv(this.#client, id);
  }

  async printCommissionRun(id: string, printBranchId: string): Promise<Result<RunView>> {
    const answer = await staffReporting.reportingPrintCommissionRun(this.#client, id, {
      print_branch_id: printBranchId,
    });
    return answer.ok ? { ok: true, value: toRun(answer.value) } : answer;
  }

  // -- accruals and attribution -----------------------------------------------------------------

  async accruals(from: string, to: string): Promise<Result<AccrualView>> {
    const answer = await staffReporting.reportingListCommissionAccruals(this.#client, { from, to });
    return answer.ok ? { ok: true, value: toAccrual(answer.value) } : answer;
  }

  async attributionCheck(from: string, to: string, branchId = ""): Promise<Result<AttributionView>> {
    const answer = await staffReporting.reportingAttributionCheck(this.#client, {
      from,
      to,
      ...(branchId ? { branch_id: branchId } : {}),
    });
    return answer.ok ? { ok: true, value: toAttribution(answer.value) } : answer;
  }
}

// -- the paper ---------------------------------------------------------------------------------
//
// The original opened the print window with the browser's document writer.
// This app's fence refuses raw DOM markup paths outright, so the carriage is
// the shared host seam the till's receipt already rides: the fetched document
// goes into a fresh window as a same-origin blob, the browser's own document
// channel parses it, and the print dialog is that window's own, called on its
// load. The document is the core's own render either way; the blob carries no
// credential (the fetch already presented the bearer).

/** The window the document opens at: the shared receipt seam's own shape, renamed for this surface. */
export interface ReportWindow {
  location: { assign(url: string): void };
  addEventListener(kind: "load", listener: () => void): void;
  focus?(): void;
  print?(): void;
}

/**
 * Open the frozen document at the paper. `openWindow` is injectable so a test
 * can exercise the flow without a browser; null models a blocked pop-up and
 * becomes the operator words, never a silent no-op.
 */
export function openReportDocument(
  documentHtml: string,
  openWindow: () => ReportWindow | null = openBlankWindow,
): PrintOutcome {
  return printReceiptInto(openWindow(), documentHtml);
}

/**
 * Hand the browser a CSV to save. A download has to be a navigation for the
 * browser to honour the filename; the text was fetched with the bearer (the
 * generated client), so the blob is the navigation that carries it.
 * `document` is injectable for tests; without one it is the window's own.
 */
export function saveCsv(
  text: string,
  filename: string,
  doc: Document | null = typeof document === "undefined" ? null : document,
): void {
  if (doc === null) return;
  const url = URL.createObjectURL(new Blob([text], { type: "text/csv" }));
  const link = doc.createElement("a");
  link.href = url;
  link.download = filename;
  doc.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}
