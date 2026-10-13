// The statements slice's state and its presses. The Financial Controller's statements pack: the income
// statement, the balance sheet, the trial balance, the ledger detail behind any line, the receivables
// aging, and the saved packs registry.
//
// The app holds no authority and no arithmetic of its own. Every figure is the server's; the screen picks a
// fiscal year (and a period, and a branch) from the books' own lists and asks. A line on a statement opens
// the ledger detail behind it, and the screen says whether the detail ties to the line, by comparing the two
// figures the server sent. The CSV a person downloads is made from the figures on the screen, so the file
// and the page cannot disagree.
//
// Every control is a plain element carrying a data-action name; the host's one click listener hands it to
// press(). Form fields carry data-field names; their values are kept here and written back after each render.
import { html, type TemplateResult } from "lit";
import type {
  Branch,
  FinStmtAgingSummary,
  FinStmtBalanceSheetReport,
  FinStmtComparativeRow,
  FinStmtGLDetailResponse,
  FinStmtIncomeStatementReport,
  FinStmtSchedule,
  FinStmtTrialBalanceReport,
  GlPeriod,
  Result,
} from "@hardscapehouse/api-client";
import type { FinstmtApi } from "./finstmt-api.js";
import {
  agingRows,
  balanceRows,
  comparativeRows,
  csvDocument,
  csvFilename,
  glDetailRows,
  incomeRows,
  periodYears,
  refusalWords,
  trialRows,
  validatePack,
  type StmtTab,
} from "./finstmt-logic.js";
import { renderFinstmt, type Drill, type FinstmtModel, type PackFormState, type TabStatus } from "./finstmt-views.js";

export interface FinstmtHost {
  readonly repaint: () => void;
  /** Writes a value into the named field of the desk's own render root (a select picks the option whose data-id matches). */
  readonly setField: (name: string, value: string) => void;
  /** Brings the first element matching the selector into view in the desk's render root. */
  readonly reveal: (selector: string) => void;
  /** Hands a CSV document to the browser as a download. */
  readonly download: (text: string, filename: string) => void;
  /** Opens the browser's print dialog for the page. */
  readonly print: () => void;
}

const EMPTY_FORM: PackFormState = { report: "", recipients: "", cadence: "", branchId: "", active: "yes" };
const PAGE = 100;

type Statuses = Record<StmtTab, TabStatus>;
const IDLE: Statuses = { income: "idle", balance: "idle", trial: "idle", gl: "idle", aging: "idle", packs: "idle" };

export class FinstmtDesk {
  readonly #api: FinstmtApi;
  readonly #host: FinstmtHost;
  #phase: FinstmtModel["phase"] = "idle";
  #loadError = "";
  #tab: StmtTab = "income";
  #periods: GlPeriod[] = [];
  #branches: Branch[] = [];
  #year = 0;
  #month = 0;
  #branchId = "";
  #compare = false;
  #status: Statuses = { ...IDLE };
  #errors: Record<StmtTab, string> = { income: "", balance: "", trial: "", gl: "", aging: "", packs: "" };
  #income: FinStmtIncomeStatementReport | null = null;
  #incomeCompare: FinStmtComparativeRow[] | null = null;
  #balance: FinStmtBalanceSheetReport | null = null;
  #balanceCompare: FinStmtComparativeRow[] | null = null;
  #trial: FinStmtTrialBalanceReport | null = null;
  #detail: FinStmtGLDetailResponse | null = null;
  #aging: FinStmtAgingSummary | null = null;
  #packs: FinStmtSchedule[] = [];
  // The ledger detail's own filter: an account code, and the line it was opened from.
  #account = "";
  #drill: Drill | null = null;
  #shown = PAGE;
  #form: PackFormState = { ...EMPTY_FORM };
  #editing: string | null = null;
  #confirmDelete: string | null = null;
  #busy = false;
  #error = "";
  #notice = "";
  // Bumped on every change of the books asked about, so an answer to an old question is dropped.
  #epoch = 0;
  #reveal = "";

  constructor(api: FinstmtApi, host: FinstmtHost) {
    this.#api = api;
    this.#host = host;
  }

  /** Loads the books' own periods and the branch words once; the statements load as a tab is first shown. */
  ensureLoaded(): void {
    if (this.#phase === "idle") void this.#load();
  }

  async #load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const [periods, branches] = await Promise.all([this.#api.periods(), this.#api.branches()]);
    if (!periods.ok) {
      this.#phase = "error";
      this.#loadError = refusalWords("read", periods.error, periods.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#periods = periods.value;
    this.#branches = branches.ok ? branches.value : [];
    const years = periodYears(this.#periods);
    this.#year = years[0] ?? 0;
    this.#month = 0;
    this.#phase = "ready";
    this.#host.repaint();
    void this.#ensure(this.#tab);
  }

  /** A control was pressed; true when this slice owns it. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    if (action === null || !action.startsWith("fs-")) return false;
    switch (action) {
      case "fs-tab": {
        const tab = control.getAttribute("data-tab");
        if (tab === "income" || tab === "balance" || tab === "trial" || tab === "gl" || tab === "aging" || tab === "packs") {
          this.#tab = tab;
          this.#error = "";
          this.#notice = "";
          this.#host.repaint();
          void this.#ensure(tab);
        }
        return true;
      }
      case "fs-reload":
        this.#phase = "idle";
        this.#status = { ...IDLE };
        this.ensureLoaded();
        return true;
      case "fs-retry":
        this.#status[this.#tab] = "idle";
        void this.#ensure(this.#tab);
        return true;
      case "fs-csv":
        this.#csv();
        return true;
      case "fs-print":
        this.#host.print();
        return true;
      case "fs-drill":
        this.#openDetail(control);
        return true;
      case "fs-back":
        if (this.#drill !== null) {
          this.#tab = this.#drill.from;
          this.#drill = null;
          this.#account = "";
          this.#status.gl = "idle";
          this.#host.repaint();
          void this.#ensure(this.#tab);
        }
        return true;
      case "fs-gl-apply":
        this.#drill = null;
        this.#status.gl = "idle";
        void this.#ensure("gl");
        return true;
      case "fs-gl-clear":
        this.#account = "";
        this.#drill = null;
        this.#status.gl = "idle";
        void this.#ensure("gl");
        return true;
      case "fs-more":
        this.#shown += PAGE;
        this.#host.repaint();
        return true;
      case "fs-pack-save":
        void this.#savePack();
        return true;
      case "fs-pack-edit":
        this.#editPack(control.getAttribute("data-id") ?? "");
        return true;
      case "fs-pack-cancel":
        this.#editing = null;
        this.#form = { ...EMPTY_FORM };
        this.#error = "";
        this.#host.repaint();
        return true;
      case "fs-pack-delete":
        this.#confirmDelete = control.getAttribute("data-id");
        this.#error = "";
        this.#notice = "";
        this.#host.repaint();
        return true;
      case "fs-pack-delete-no":
        this.#confirmDelete = null;
        this.#host.repaint();
        return true;
      case "fs-pack-delete-yes":
        void this.#deletePack(control.getAttribute("data-id") ?? "");
        return true;
      default:
        return false;
    }
  }

  /** A text field changed: its value is kept, nothing is sent. */
  input(control: Element): void {
    const name = control.getAttribute("data-field");
    if (name === "fs-account" && control instanceof HTMLInputElement) this.#account = control.value.trim();
    if (name === "fs-recipients" && control instanceof HTMLInputElement) this.#form = { ...this.#form, recipients: control.value };
  }

  /** A choice changed. The books asked about move for year, period, branch and comparison; the rest are the pack form's. */
  change(control: Element): void {
    const name = control.getAttribute("data-field");
    if (name === null || !name.startsWith("fs-")) return;
    const chosen = control instanceof HTMLSelectElement ? (control.selectedOptions[0]?.getAttribute("data-id") ?? "") : "";
    switch (name) {
      case "fs-year":
        this.#year = Number(chosen);
        this.#month = 0;
        this.#moved();
        return;
      case "fs-month":
        this.#month = Number(chosen);
        this.#moved();
        return;
      case "fs-branch":
        this.#branchId = chosen;
        this.#moved();
        return;
      case "fs-compare":
        this.#compare = chosen === "yes";
        this.#moved();
        return;
      case "fs-pack-report":
        this.#form = { ...this.#form, report: chosen };
        return;
      case "fs-pack-cadence":
        this.#form = { ...this.#form, cadence: chosen };
        return;
      case "fs-pack-branch":
        this.#form = { ...this.#form, branchId: chosen };
        return;
      case "fs-pack-active":
        this.#form = { ...this.#form, active: chosen === "no" ? "no" : "yes" };
        return;
      default:
        return;
    }
  }

  /** Called after each render: writes the kept choices back into the fields, and brings a panel into view. */
  afterRender(): void {
    if (this.#phase === "ready") {
      this.#host.setField("fs-year", String(this.#year));
      this.#host.setField("fs-month", String(this.#month));
      this.#host.setField("fs-branch", this.#branchId);
      this.#host.setField("fs-compare", this.#compare ? "yes" : "no");
      this.#host.setField("fs-account", this.#account);
      this.#host.setField("fs-recipients", this.#form.recipients);
      this.#host.setField("fs-pack-report", this.#form.report);
      this.#host.setField("fs-pack-cadence", this.#form.cadence);
      this.#host.setField("fs-pack-branch", this.#form.branchId);
      this.#host.setField("fs-pack-active", this.#form.active);
    }
    if (this.#reveal === "") return;
    const selector = this.#reveal;
    this.#reveal = "";
    this.#host.reveal(selector);
  }

  /** The books asked about moved: every statement is stale, the saved packs are not. */
  #moved(): void {
    this.#epoch += 1;
    for (const tab of ["income", "balance", "trial", "gl", "aging"] as const) this.#status[tab] = "idle";
    this.#drill = null;
    this.#shown = PAGE;
    this.#host.repaint();
    void this.#ensure(this.#tab);
  }

  async #ensure(tab: StmtTab): Promise<void> {
    if (this.#phase !== "ready" || this.#status[tab] !== "idle") return;
    if (tab !== "packs" && this.#year === 0) return;
    this.#status[tab] = "loading";
    this.#errors[tab] = "";
    const epoch = this.#epoch;
    this.#host.repaint();
    const outcome = await this.#fetch(tab);
    if (epoch !== this.#epoch) return;
    if (outcome === null) {
      this.#status[tab] = "ready";
    } else {
      this.#status[tab] = "error";
      this.#errors[tab] = refusalWords("read", outcome.error, outcome.status ?? 0);
    }
    this.#host.repaint();
  }

  /** Reads one tab's books into place; null when it was read, the refusal when it was not. */
  async #fetch(tab: StmtTab): Promise<{ error: string; status?: number } | null> {
    const keep = <T>(answer: Result<T>, set: (v: T) => void): { error: string; status?: number } | null => {
      if (!answer.ok) return answer;
      set(answer.value);
      return null;
    };
    switch (tab) {
      case "income":
        return this.#compare
          ? keep(await this.#api.incomeCompare(this.#year, this.#month, this.#branchId), (v) => (this.#incomeCompare = v))
          : keep(await this.#api.income(this.#year, this.#month, this.#branchId), (v) => (this.#income = v));
      case "balance":
        return this.#compare
          ? keep(await this.#api.balanceCompare(this.#year, this.#branchId), (v) => (this.#balanceCompare = v))
          : keep(await this.#api.balance(this.#year, this.#branchId), (v) => (this.#balance = v));
      case "trial":
        return keep(await this.#api.trial(this.#year), (v) => (this.#trial = v));
      case "gl":
        return keep(
          await this.#api.glDetail({ year: this.#year, month: this.#month, accountCode: this.#account, accountType: this.#drill?.accountType ?? "", branchId: this.#branchId }),
          (v) => {
            this.#detail = v;
            this.#shown = PAGE;
          },
        );
      case "aging":
        return keep(await this.#api.aging(this.#branchId), (v) => (this.#aging = v));
      case "packs":
        return keep(await this.#api.packs(), (v) => (this.#packs = v));
    }
  }

  #openDetail(control: Element): void {
    if (this.#tab !== "income" && this.#tab !== "balance") return;
    const accountType = control.getAttribute("data-type") ?? "";
    const amount = Number(control.getAttribute("data-amount") ?? "");
    this.#drill = {
      from: this.#tab,
      accountCode: control.getAttribute("data-code") ?? "",
      accountName: control.getAttribute("data-name") ?? "",
      accountType,
      statementCents: Number.isInteger(amount) ? amount : null,
    };
    // A balance sheet is a fiscal year's position, so the detail behind it is the whole year.
    if (this.#tab === "balance") this.#month = 0;
    this.#account = this.#drill.accountCode;
    this.#tab = "gl";
    this.#status.gl = "idle";
    this.#reveal = "[data-detail-panel]";
    this.#host.repaint();
    void this.#ensure("gl");
  }

  #csv(): void {
    let rows: ReadonlyArray<readonly string[]> | null = null;
    if (this.#tab === "income") {
      if (this.#compare && this.#incomeCompare !== null) rows = comparativeRows(this.#incomeCompare);
      else if (!this.#compare && this.#income !== null) rows = incomeRows(this.#income);
    } else if (this.#tab === "balance") {
      if (this.#compare && this.#balanceCompare !== null) rows = comparativeRows(this.#balanceCompare);
      else if (!this.#compare && this.#balance !== null) rows = balanceRows(this.#balance);
    } else if (this.#tab === "trial" && this.#trial !== null) rows = trialRows(this.#trial);
    else if (this.#tab === "gl" && this.#detail !== null) rows = glDetailRows(this.#detail.rows);
    else if (this.#tab === "aging" && this.#aging !== null) rows = agingRows(this.#aging);
    if (rows === null) return;
    this.#host.download(csvDocument(rows), csvFilename(this.#tab, this.#year, this.#month));
    this.#notice = "The file is made from the figures on this page.";
    this.#host.repaint();
  }

  // ---- the saved packs ----------------------------------------------------------------------------

  #editPack(id: string): void {
    const pack = this.#packs.find((p) => p.id === id);
    if (pack === undefined) return;
    this.#editing = id;
    this.#form = { report: pack.report, recipients: pack.recipients, cadence: pack.cadence, branchId: pack.branch_id, active: pack.is_active ? "yes" : "no" };
    this.#error = "";
    this.#notice = "";
    this.#reveal = "[data-pack-form]";
    this.#host.repaint();
  }

  async #savePack(): Promise<void> {
    if (this.#busy) return;
    const checked = validatePack({ ...this.#form, active: this.#form.active !== "no" });
    if (!checked.ok) {
      this.#error = checked.message;
      this.#notice = "";
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.#host.repaint();
    const editing = this.#editing;
    const answer = editing === null ? await this.#api.createPack(checked.input) : await this.#api.updatePack(editing, checked.input);
    this.#busy = false;
    if (!answer.ok) {
      // A write that got no answer may or may not have landed: say so, and never offer a blind second press.
      this.#error = answer.status === undefined && editing === null
        ? "No answer came back, so the pack may or may not be saved. Reload the list before you try again."
        : refusalWords("pack", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#notice = editing === null ? "Pack saved." : "Pack changed.";
    this.#editing = null;
    this.#form = { ...EMPTY_FORM };
    this.#status.packs = "idle";
    await this.#ensure("packs");
  }

  async #deletePack(id: string): Promise<void> {
    if (this.#busy || id === "") return;
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.#host.repaint();
    const answer = await this.#api.deletePack(id);
    this.#busy = false;
    this.#confirmDelete = null;
    if (!answer.ok) {
      this.#error = refusalWords("pack", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    if (this.#editing === id) {
      this.#editing = null;
      this.#form = { ...EMPTY_FORM };
    }
    this.#notice = "Pack deleted.";
    this.#status.packs = "idle";
    await this.#ensure("packs");
  }

  render(): TemplateResult {
    const model: FinstmtModel = {
      phase: this.#phase,
      loadError: this.#loadError,
      tab: this.#tab,
      periods: this.#periods,
      branches: this.#branches,
      year: this.#year,
      month: this.#month,
      branchId: this.#branchId,
      compare: this.#compare,
      status: this.#status[this.#tab],
      tabError: this.#errors[this.#tab],
      income: this.#income,
      incomeCompare: this.#incomeCompare,
      balance: this.#balance,
      balanceCompare: this.#balanceCompare,
      trial: this.#trial,
      detail: this.#detail,
      aging: this.#aging,
      packs: this.#packs,
      account: this.#account,
      drill: this.#drill,
      shown: this.#shown,
      form: this.#form,
      editing: this.#editing,
      confirmDelete: this.#confirmDelete,
      busy: this.#busy,
      error: this.#error,
      notice: this.#notice,
    };
    return html`${renderFinstmt(model)}`;
  }
}
