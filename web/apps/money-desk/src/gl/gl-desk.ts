// The ledger slice's state and its presses. The Financial Controller's desk:
// the approvals queue it decides (floor requests, parked journals, freight
// and the credit exceptions), the journal register with its new journal,
// void and reverse, the chart of accounts, the trial balance with its month
// and export, the fiscal periods with their close and reopen, the checks and
// the policy. Payables and Receivables holds the same desk as a reader: the
// reads, none of the writes, and the words saying who may.
//
// The app holds no authority of its own. Who may decide a request is the
// server's answer; the one rule the screen shows before a press is the four
// eyes rule (a request the caller filed is never theirs to decide), and the
// server enforces it again. A parked journal is approved by deciding its
// request: the core posts the journal in the same transaction that stamps the
// request, so the queue and the ledger never disagree.
//
// Every control is a plain element carrying a data-action name; the host's
// one click listener hands it to press(). Form fields carry data-field names
// and are read when the button is pressed; none is bound.
import { html, type TemplateResult } from "lit";
import type { Branch, GlAccount, GlBisTrackTieoutReport, GlJournal, GlPeriod, GlPolicy, GlRecurringTemplate, GlTieoutState, GlTrialBalanceResult, IdentityApproval } from "@hardscapehouse/api-client";
import type { Result } from "@hardscapehouse/api-client";
import type { GlApi } from "./gl-api.js";
import type { BooksModel, Confirm, Review, Section } from "./gl-books.js";
import { branchChoices } from "./gl-books.js";
import { canDecide, checkDraft, latePostedRow, periodName, postingQueueBannerWords, refusalWords, type DraftLine, type LatePostedRow } from "./gl-logic.js";
import { formatMoney, parseAmountToCents } from "./gl-money.js";
import { renderGl, tabsFor, type GlModel, type GlTab, type JournalFilter } from "./gl-views.js";

/** The most lines one journal form takes. */
const MAX_LINES = 30;

function idle<T>(value: T): Section<T> {
  return { phase: "idle", value, error: "" };
}

export interface GlHost {
  readonly repaint: () => void;
  /** The text of a named field in the desk's own render root; a keyed field is one of several (a journal line). */
  readonly field: (name: string, key?: string) => string;
  /** Puts a value in a named field (to clear a form after it was used). */
  readonly setField: (name: string, value: string, key?: string) => void;
  /** Hands a text file to the browser. */
  readonly download: (text: string, filename: string) => void;
  /** The signed in caller's user id, for the four eyes rule. */
  readonly userId: () => string;
  /** Brings the first element matching the selector into view in the desk's render root. */
  readonly reveal: (selector: string) => void;
}

export type GlMode = "controller" | "reader";

export class GlDesk {
  readonly #api: GlApi;
  readonly #host: GlHost;
  readonly #mode: GlMode;
  #phase: GlModel["phase"] = "loading";
  #tab: GlTab;
  #approvals: IdentityApproval[] = [];
  #selectedApproval: string | null = null;
  #choice: "approve" | "decline" = "approve";
  #journals: GlJournal[] = [];
  #journalFilter: JournalFilter = "all";
  #selectedJournal: string | null = null;
  // The parked journal behind a selected journal request, read for its lines.
  #approvalJournal: GlJournal | null = null;
  #trial: GlTrialBalanceResult | null = null;
  #trialBranch = "";
  #trialMonth = 0;
  // The fiscal year the trial balance last answered for, so a month is asked for within that year.
  #trialYear = 0;
  // A selection asks the screen to bring its panel into view once it has rendered: on a phone the
  // panel sits under the whole list and a press would otherwise change nothing the person can see.
  #reveal = "";
  #branches: Branch[] = [];
  #busy = false;
  #error = "";
  #notice = "";
  #postingQueueWords = "";
  // The books: each part is read the first time its tab opens, and reread after a change to it.
  #chart: Section<GlAccount[]> = idle([]);
  #recurring: Section<GlRecurringTemplate[]> = idle([]);
  #periods: Section<GlPeriod[]> = idle([]);
  #nextOpenableYear = 0;
  // The journals that carry a date from a closed month and landed in an open one: the Controller's list.
  #latePosted: Section<LatePostedRow[]> = idle([]);
  #policy: Section<GlPolicy | null> = idle(null);
  #tie: Section<GlTieoutState[]> = idle([]);
  #bis: Section<GlBisTrackTieoutReport | null> = idle(null);
  // The legacy system the dealer imports from, as its surface config names it; "" is none, and then
  // the Checks tab draws no legacy import section.
  #legacyImport = "";
  #confirm: Confirm | null = null;
  #composing = false;
  #lineCount = 2;
  #review: Review | null = null;
  #composeError = "";
  #exporting = false;

  constructor(api: GlApi, host: GlHost, mode: GlMode = "controller") {
    this.#api = api;
    this.#host = host;
    this.#mode = mode;
    this.#tab = mode === "controller" ? "approvals" : "journals";
  }

  /** The first load after sign in: approvals (the controller's), journals, trial balance and the branch words. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const controller = this.#mode === "controller";
    const [approvals, journals, trial, branches, legacyImport, postingQueue] = await Promise.all([
      controller ? this.#api.pendingApprovals() : Promise.resolve<Result<IdentityApproval[]>>({ ok: true, value: [] }),
      this.#api.journals(),
      this.#api.trialBalance(this.#trialBranch, this.#trialYear, this.#trialMonth),
      this.#api.branches(),
      this.#api.legacyImportSource(),
      this.#api.postingQueueCount(),
    ]);
    if (!approvals.ok) {
      this.#phase = "error";
      this.#error = refusalWords("load", approvals.error, approvals.status ?? 0);
      this.#host.repaint();
      return;
    }
    if (!controller && !journals.ok) {
      this.#phase = "error";
      this.#error = refusalWords("journals", journals.error, journals.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#approvals = approvals.value;
    this.#journals = journals.ok ? journals.value : [];
    this.#trial = trial.ok ? trial.value : null;
    if (trial.ok) this.#trialYear = trial.value.fiscal_year;
    this.#branches = branches.ok ? branches.value : [];
    this.#legacyImport = legacyImport;
    // The posting queue's banner: a count that is not zero says journals are
    // stuck (waiting or dead lettered) and the whole ledger is out of step
    // until they post. A refused count never blocks the desk; the banner
    // simply does not show.
    this.#postingQueueWords = postingQueue.ok ? postingQueueBannerWords(postingQueue.value) : "";
    // The ledger's own refusals are said, not hidden, but they never hide the queue.
    this.#error = !journals.ok
      ? refusalWords("journals", journals.error, journals.status ?? 0)
      : !trial.ok
        ? refusalWords("trial", trial.error, trial.status ?? 0)
        : "";
    this.#phase = "ready";
    this.#host.repaint();
    this.#ensure(this.#tab);
  }

  // ---- the books' lazy reads ---------------------------------------------------------------------

  /** Reads the part a tab needs, once; force reads it again. */
  #ensure(tab: GlTab, force = false): void {
    const wanted = (section: { phase: string }): boolean => force || section.phase === "idle";
    if (tab === "journals") {
      if (wanted(this.#recurring)) void this.#readRecurring();
      if (this.#composing && wanted(this.#chart)) void this.#readChart();
    }
    if (tab === "chart" && wanted(this.#chart)) void this.#readChart();
    if (tab === "trial" && wanted(this.#periods)) void this.#readPeriods();
    if (tab === "periods") {
      if (force) this.#latePosted = idle(this.#latePosted.value);
      // The late posted list names the month each journal landed in, and the periods are what say which
      // month that is, so the list is read after them.
      if (wanted(this.#periods)) void this.#readPeriods();
      else if (this.#periods.phase === "ready" && wanted(this.#latePosted)) void this.#readLatePosted();
    }
    if (tab === "policy" && wanted(this.#policy)) void this.#readPolicy();
    if (tab === "checks" && wanted(this.#tie)) void this.#readTie();
  }

  async #readChart(): Promise<void> {
    this.#chart = { ...this.#chart, phase: "loading" };
    this.#host.repaint();
    const answer = await this.#api.accounts();
    this.#chart = answer.ok ? { phase: "ready", value: answer.value, error: "" } : { phase: "error", value: [], error: refusalWords("books", answer.error, answer.status ?? 0) };
    this.#host.repaint();
  }

  async #readRecurring(): Promise<void> {
    this.#recurring = { ...this.#recurring, phase: "loading" };
    this.#host.repaint();
    const answer = await this.#api.recurring();
    this.#recurring = answer.ok ? { phase: "ready", value: answer.value, error: "" } : { phase: "error", value: [], error: refusalWords("books", answer.error, answer.status ?? 0) };
    this.#host.repaint();
  }

  async #readPeriods(): Promise<void> {
    this.#periods = { ...this.#periods, phase: "loading" };
    this.#host.repaint();
    const answer = await this.#api.periodsPage();
    this.#periods = answer.ok ? { phase: "ready", value: answer.value.items, error: "" } : { phase: "error", value: [], error: refusalWords("books", answer.error, answer.status ?? 0) };
    this.#nextOpenableYear = answer.ok ? answer.value.nextOpenableYear : 0;
    this.#host.repaint();
    // The late posted list names the month each journal landed in, so it waits for the periods to say
    // which month that is. A list read before them would name a month it does not hold.
    if (this.#tab === "periods" && this.#latePosted.phase === "idle") void this.#readLatePosted();
  }

  async #readLatePosted(): Promise<void> {
    this.#latePosted = { ...this.#latePosted, phase: "loading" };
    this.#host.repaint();
    const answer = await this.#api.latePostedJournals();
    this.#latePosted = answer.ok
      ? { phase: "ready", value: answer.value.map((j) => latePostedRow(j, this.#periods.value.find((p) => p.id === j.period_id))), error: "" }
      : { phase: "error", value: [], error: refusalWords("books", answer.error, answer.status ?? 0) };
    this.#host.repaint();
  }

  async #readPolicy(): Promise<void> {
    this.#policy = { ...this.#policy, phase: "loading" };
    this.#host.repaint();
    const answer = await this.#api.policy();
    this.#policy = answer.ok ? { phase: "ready", value: answer.value, error: "" } : { phase: "error", value: null, error: refusalWords("books", answer.error, answer.status ?? 0) };
    this.#host.repaint();
  }

  async #readTie(): Promise<void> {
    this.#tie = { ...this.#tie, phase: "loading" };
    this.#host.repaint();
    const answer = await this.#api.tieOut();
    this.#tie = answer.ok ? { phase: "ready", value: answer.value, error: "" } : { phase: "error", value: [], error: refusalWords("books", answer.error, answer.status ?? 0) };
    this.#host.repaint();
  }

  /** A control was pressed; true when this slice owns it. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    if (action === null) return false;
    switch (action) {
      case "gl-tab": {
        const tab = control.getAttribute("data-tab");
        if (tabsFor(this.#mode).some((t) => t.id === tab)) {
          this.#tab = tab as GlTab;
          this.#error = "";
          this.#notice = "";
          this.#confirm = null;
          this.#host.repaint();
          this.#ensure(this.#tab);
        }
        return true;
      }
      case "gl-reload":
        void this.load();
        return true;
      case "gl-reload-section":
        this.#ensure(this.#tab, true);
        return true;
      case "gl-select-approval":
        this.#reveal = "[data-decide-panel]";
        void this.#selectApproval(control.getAttribute("data-id") ?? "");
        return true;
      case "gl-trial-branch":
        void this.#chooseTrial(control.getAttribute("data-branch") ?? "", this.#trialMonth);
        return true;
      case "gl-trial-month":
        void this.#chooseTrial(this.#trialBranch, Number(control.getAttribute("data-month") ?? "0") || 0);
        return true;
      case "gl-choice": {
        const choice = control.getAttribute("data-choice");
        if (choice === "approve" || choice === "decline") {
          this.#choice = choice;
          this.#host.repaint();
        }
        return true;
      }
      case "gl-cancel":
        this.#selectedApproval = null;
        this.#approvalJournal = null;
        this.#error = "";
        this.#host.repaint();
        return true;
      case "gl-decide":
        void this.#decide(control.getAttribute("data-id") ?? "", this.#choice === "approve");
        return true;
      case "gl-filter": {
        const filter = control.getAttribute("data-filter");
        if (filter === "all" || filter === "pending" || filter === "posted" || filter === "draft" || filter === "void") {
          this.#journalFilter = filter;
          this.#host.repaint();
        }
        return true;
      }
      case "gl-select-journal":
        this.#reveal = "[data-journal-panel]";
        this.#selectedJournal = control.getAttribute("data-id") ?? null;
        this.#error = "";
        this.#notice = "";
        this.#confirm = null;
        this.#host.repaint();
        return true;
      case "gl-approve-journal": {
        // A parked journal is approved through its request, which posts it.
        const request = this.#approvals.find((a) => a.subject_ref === (control.getAttribute("data-id") ?? ""));
        if (request !== undefined) {
          this.#choice = "approve";
          void this.#decide(request.id, true);
        }
        return true;
      }
      case "gl-approve-route":
        void this.#approveOnRoute(control.getAttribute("data-id") ?? "");
        return true;
      case "gl-void":
        return this.#ask({ kind: "void", id: control.getAttribute("data-id") ?? "" });
      case "gl-reverse":
        return this.#ask({ kind: "reverse", id: control.getAttribute("data-id") ?? "" });
      case "gl-deactivate":
        return this.#ask({ kind: "deactivate", code: control.getAttribute("data-code") ?? "" });
      case "gl-close-period":
        return this.#ask({ kind: "close-period", id: control.getAttribute("data-id") ?? "" });
      case "gl-reopen-period":
        return this.#ask({ kind: "reopen", id: control.getAttribute("data-id") ?? "" });
      case "gl-close-year":
        return this.#ask({ kind: "close-year", year: Number(control.getAttribute("data-year") ?? "0") || 0 });
      case "gl-open-year":
        return this.#ask({ kind: "open-year" });
      case "gl-open-journal": {
        // The way to a late posted journal: the register holds it, so the desk opens it there.
        const id = control.getAttribute("data-id") ?? "";
        this.#selectedJournal = id;
        this.#tab = "journals";
        this.#error = "";
        this.#notice = "";
        this.#confirm = null;
        this.#reveal = "[data-journal-panel]";
        this.#host.repaint();
        this.#ensure("journals");
        return true;
      }
      case "gl-confirm-cancel":
        this.#confirm = null;
        this.#error = "";
        this.#host.repaint();
        return true;
      case "gl-confirm-go":
        void this.#confirmed();
        return true;
      case "gl-compose":
        this.#composing = true;
        this.#lineCount = 2;
        this.#review = null;
        this.#composeError = "";
        this.#reveal = "[data-composer]";
        this.#host.repaint();
        this.#ensure("journals");
        if (this.#chart.phase === "idle") void this.#readChart();
        if (this.#policy.phase === "idle") void this.#readPolicy();
        return true;
      case "gl-compose-cancel":
        this.#composing = false;
        this.#review = null;
        this.#composeError = "";
        this.#host.repaint();
        return true;
      case "gl-add-line":
        if (this.#lineCount < MAX_LINES) this.#lineCount += 1;
        this.#host.repaint();
        return true;
      case "gl-remove-line":
        if (this.#lineCount > 2) this.#lineCount -= 1;
        this.#host.repaint();
        return true;
      case "gl-review":
        this.#reviewDraft();
        return true;
      case "gl-review-back":
        this.#review = null;
        this.#host.repaint();
        return true;
      case "gl-post-go":
        void this.#post();
        return true;
      case "gl-add-account":
        void this.#addAccount();
        return true;
      case "gl-run-template":
        void this.#runTemplate(control.getAttribute("data-id") ?? "");
        return true;
      case "gl-export":
        void this.#export(Number(control.getAttribute("data-year") ?? "0") || 0);
        return true;
      case "gl-run-bis":
        void this.#runBis();
        return true;
      default:
        return false;
    }
  }

  /** Opens a confirm step: nothing is sent until the person presses the step's own button. */
  #ask(confirm: Confirm): boolean {
    this.#confirm = confirm;
    this.#error = "";
    this.#notice = "";
    this.#reveal = "[data-confirm]";
    this.#host.repaint();
    return true;
  }

  async #chooseTrial(branchId: string, month: number): Promise<void> {
    this.#trialBranch = branchId;
    this.#trialMonth = month;
    this.#host.repaint();
    const trial = await this.#api.trialBalance(branchId, this.#trialYear, month);
    if (this.#trialBranch !== branchId || this.#trialMonth !== month) return;
    if (trial.ok) {
      this.#trial = trial.value;
      this.#error = "";
    } else {
      this.#error = refusalWords("trial", trial.error, trial.status ?? 0);
    }
    this.#host.repaint();
  }

  /** Called after each render: brings a just selected panel into view. */
  afterRender(): void {
    if (this.#reveal === "") return;
    const selector = this.#reveal;
    this.#reveal = "";
    this.#host.reveal(selector);
  }

  async #selectApproval(id: string): Promise<void> {
    this.#selectedApproval = id;
    this.#choice = "approve";
    this.#error = "";
    this.#notice = "";
    this.#approvalJournal = null;
    this.#host.repaint();
    const row = this.#approvals.find((a) => a.id === id);
    if (row !== undefined && row.policy_type === "JE_OVER_THRESHOLD" && row.subject_ref !== undefined && row.subject_ref !== "") {
      const journal = await this.#api.journal(row.subject_ref);
      if (this.#selectedApproval === id && journal.ok) {
        this.#approvalJournal = journal.value;
        this.#host.repaint();
      }
    }
  }

  async #decide(id: string, approve: boolean): Promise<void> {
    const row = this.#approvals.find((a) => a.id === id);
    if (row === undefined || this.#busy) return;
    if (!canDecide(row, this.#host.userId())) {
      this.#error = refusalWords("decide", "separation of duties", 403);
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.#host.repaint();
    const note = this.#host.field("note").trim();
    const reason = note !== "" ? note : approve ? "Approved." : "Declined.";
    const answer = await this.#api.decide(id, approve, reason);
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("decide", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#notice = decidedWords(row, approve);
    this.#selectedApproval = null;
    this.#approvalJournal = null;
    await this.load();
  }

  // ---- the new journal ---------------------------------------------------------------------------

  /** Reads the form and checks each line's shape; the review opens only on a form that is whole. */
  #reviewDraft(): void {
    const memo = this.#host.field("memo");
    const lines: DraftLine[] = Array.from({ length: this.#lineCount }, (_, i) => {
      const key = String(i);
      return {
        account: this.#host.field("line-account", key),
        debit: this.#host.field("line-debit", key),
        credit: this.#host.field("line-credit", key),
        memo: this.#host.field("line-memo", key),
      };
    });
    const checked = checkDraft(memo, lines, parseAmountToCents);
    if (!checked.ok) {
      this.#composeError = checked.error;
      this.#review = null;
      this.#host.repaint();
      return;
    }
    const branchName = this.#host.field("branch");
    const branch = branchChoices(this.#branches).find((b) => b.name === branchName) ?? branchChoices(this.#branches)[0];
    this.#composeError = "";
    this.#review = { memo: memo.trim(), branchId: branch?.id ?? "", lines: checked.lines };
    this.#reveal = "[data-review]";
    this.#host.repaint();
  }

  /** The press that posts. The route has no idempotency key, so a lost answer is never retried by the desk. */
  async #post(): Promise<void> {
    const review = this.#review;
    if (review === null || this.#busy) return;
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.#host.repaint();
    const answer = await this.#api.createJournal({ branch_id: review.branchId, memo: review.memo, source: "MANUAL", lines: review.lines.map((l) => ({ ...l })) });
    this.#busy = false;
    if (!answer.ok) {
      this.#review = null;
      this.#composeError = refusalWords("post", answer.error, answer.status ?? 0);
      this.#reveal = "[data-compose-error]";
      this.#host.repaint();
      // A lost answer may have landed: the register shows the truth.
      if ((answer.status ?? 0) === 0 || (answer.status ?? 0) >= 500) await this.#refreshJournals();
      return;
    }
    const journal = answer.value;
    this.#notice = journal.derived_status === "PENDING_APPROVAL"
      ? `Parked as ${entryNo(journal)}. It is at or over the approval threshold, so a second approver posts it from the approvals queue.`
      : `Posted as ${entryNo(journal)}. It is in the ledger now.`;
    this.#composing = false;
    this.#review = null;
    this.#composeError = "";
    this.#clearComposer();
    this.#selectedJournal = journal.id;
    this.#tab = "journals";
    await this.load();
  }

  #clearComposer(): void {
    this.#host.setField("memo", "");
    for (let i = 0; i < this.#lineCount; i += 1) {
      const key = String(i);
      for (const name of ["line-account", "line-debit", "line-credit", "line-memo"]) this.#host.setField(name, "", key);
    }
    this.#lineCount = 2;
  }

  async #refreshJournals(): Promise<void> {
    const journals = await this.#api.journals();
    if (journals.ok) this.#journals = journals.value;
    this.#host.repaint();
  }

  // ---- the writes that wait for a yes ------------------------------------------------------------

  /** The one place a confirm step is carried out. A refusal keeps the step open so its reason can be fixed. */
  async #confirmed(): Promise<void> {
    const confirm = this.#confirm;
    if (confirm === null || this.#busy) return;
    const reason = this.#host.field("reason").trim();
    if (confirm.kind === "reopen" && reason === "") {
      this.#error = refusalWords("reopen", "gl: a reason is required", 422);
      this.#host.repaint();
      return;
    }
    if (confirm.kind === "open-year" && reason === "") {
      this.#error = refusalWords("open-year", "gl: a reason is required", 422);
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.#host.repaint();
    let words = "";
    let failed: { error: string; status?: number } | null = null;
    let action = "";
    if (confirm.kind === "close-period") {
      action = "close-period";
      const period = this.#periods.value.find((p) => p.id === confirm.id);
      const answer = await this.#api.closePeriod(confirm.id, reason);
      if (answer.ok) words = `${period === undefined ? "The period" : periodName(period)} is closed. No journal can post into it until it is reopened.`;
      else failed = answer;
    } else if (confirm.kind === "reopen") {
      action = "reopen";
      const period = this.#periods.value.find((p) => p.id === confirm.id);
      const answer = await this.#api.reopenPeriod(confirm.id, reason);
      if (answer.ok) words = `${period === undefined ? "The period" : periodName(period)} is open again. Your reason is on record.`;
      else failed = answer;
    } else if (confirm.kind === "close-year") {
      action = "close-year";
      const answer = await this.#api.closeYear(confirm.year, reason);
      if (answer.ok) {
        words = `Fiscal year ${answer.value.fiscal_year} is closed. Net income of ${formatMoney(answer.value.net_income_cents)} moved to Retained Earnings${answer.value.closing_journal_id === undefined ? " (nothing needed closing)" : ""}, and the year's periods are closed.`;
      } else failed = answer;
    } else if (confirm.kind === "open-year") {
      action = "open-year";
      const answer = await this.#api.openNextYear(reason, this.#nextOpenableYear);
      if (answer.ok) words = `Fiscal year ${answer.value.fiscal_year} is open. Your reason is on record.`;
      else failed = answer;
    } else if (confirm.kind === "reverse") {
      action = "reverse";
      const original = this.#journals.find((j) => j.id === confirm.id);
      const answer = await this.#api.reverseJournal(confirm.id);
      if (answer.ok) {
        words = `Reversed${original === undefined ? "" : ` ${entryNo(original)}`}. ${entryNo(answer.value)} carries the opposite lines, so the two net to nothing.`;
        this.#selectedJournal = answer.value.id;
      } else failed = answer;
    } else if (confirm.kind === "void") {
      action = "void";
      const answer = await this.#api.voidJournal(confirm.id);
      if (answer.ok) words = `${entryNo(answer.value)} is voided. It never touched the ledger.`;
      else failed = answer;
    } else {
      action = "deactivate";
      const answer = await this.#api.deactivateAccount(confirm.code);
      if (answer.ok) words = `Account ${answer.value.code} ${answer.value.name} is deactivated. It keeps its history and takes no new journals.`;
      else failed = answer;
    }
    this.#busy = false;
    if (failed !== null) {
      this.#error = refusalWords(action, failed.error, failed.status ?? 0);
      if ((failed.status ?? 0) === 0 || (failed.status ?? 0) >= 500) {
        // Nothing says whether it landed: close the step and reread what it touched.
        this.#confirm = null;
        this.#invalidate(confirm.kind);
        this.#reveal = "[data-error], [data-confirm-error]";
        this.#host.repaint();
        this.#ensure(this.#tab, true);
        if (confirm.kind === "reverse" || confirm.kind === "void") await this.#refreshJournals();
        return;
      }
      this.#reveal = "[data-error], [data-confirm-error]";
      this.#host.repaint();
      return;
    }
    this.#confirm = null;
    this.#notice = words;
    this.#invalidate(confirm.kind);
    await this.load();
  }

  /** What a change makes stale is read again. */
  #invalidate(kind: Confirm["kind"]): void {
    if (kind === "deactivate") this.#chart = idle(this.#chart.value);
    if (kind === "close-period" || kind === "reopen" || kind === "close-year") this.#periods = idle(this.#periods.value);
    if (kind === "close-year") this.#tie = idle(this.#tie.value);
    // The year that opens joins the periods, so both the periods and the late posted list are read again.
    if (kind === "open-year") {
      this.#periods = idle(this.#periods.value);
      this.#latePosted = idle(this.#latePosted.value);
    }
  }

  async #approveOnRoute(id: string): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.#host.repaint();
    const answer = await this.#api.approveJournal(id);
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("approve-journal", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#notice = `${entryNo(answer.value)} is posted to the ledger.`;
    await this.load();
  }

  async #addAccount(): Promise<void> {
    if (this.#busy) return;
    const code = this.#host.field("account-code").trim();
    const name = this.#host.field("account-name").trim();
    if (code === "" || name === "") {
      this.#error = "Give the account a code and a name.";
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.#host.repaint();
    const answer = await this.#api.createAccount({
      code,
      name,
      account_type: (this.#host.field("account-type") || "ASSET") as "ASSET" | "LIABILITY" | "EQUITY" | "REVENUE" | "EXPENSE",
      normal_balance: (this.#host.field("account-side") || "DEBIT") as "DEBIT" | "CREDIT",
      parent_id: "",
    });
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("account", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#notice = `Account ${answer.value.code} ${answer.value.name} is added. Journals can post to it now.`;
    for (const field of ["account-code", "account-name"]) this.#host.setField(field, "");
    await this.#readChart();
  }

  async #runTemplate(id: string): Promise<void> {
    if (this.#busy) return;
    const template = this.#recurring.value.find((t) => t.id === id);
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.#host.repaint();
    const answer = await this.#api.runRecurring(id);
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("run", answer.error, answer.status ?? 0);
      this.#host.repaint();
      if ((answer.status ?? 0) === 0 || (answer.status ?? 0) >= 500) await this.#refreshJournals();
      return;
    }
    this.#notice = `Draft ${entryNo(answer.value)} added from ${template?.name ?? "the template"}. It waits for a second approver: it never posts by itself.`;
    this.#selectedJournal = answer.value.id;
    this.#reveal = "[data-journal-panel]";
    await this.load();
  }

  async #export(year: number): Promise<void> {
    if (this.#busy || this.#exporting || year <= 0) return;
    this.#exporting = true;
    this.#error = "";
    this.#notice = "";
    this.#host.repaint();
    const answer = await this.#api.exportBooks(year, this.#trialMonth, this.#trialBranch);
    this.#exporting = false;
    if (!answer.ok) {
      this.#error = refusalWords("export", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    const part = this.#trialMonth === 0 ? "year" : `month-${String(this.#trialMonth).padStart(2, "0")}`;
    this.#host.download(answer.value.content, `gl-export-${year}-${part}.csv`);
    this.#notice = `The export has ${answer.value.row_count} ${answer.value.row_count === 1 ? "row" : "rows"} and is a draft in the parity format, not a filing.`;
    this.#host.repaint();
  }

  async #runBis(): Promise<void> {
    if (this.#busy) return;
    const year = Number(this.#host.field("bis-year").trim());
    const month = Number(this.#host.field("bis-month").trim());
    const toleranceText = this.#host.field("bis-tolerance").trim();
    const tolerance = toleranceText === "" ? 0 : parseAmountToCents(toleranceText);
    if (!Number.isInteger(year) || year <= 0 || !Number.isInteger(month) || month < 1 || month > 12) {
      this.#bis = { phase: "error", value: null, error: "Type a fiscal year and a month from 1 to 12." };
      this.#host.repaint();
      return;
    }
    if (tolerance === null) {
      this.#bis = { phase: "error", value: null, error: "The tolerance is not an amount. Type dollars, like 5.00, or leave it empty." };
      this.#host.repaint();
      return;
    }
    this.#bis = { phase: "loading", value: null, error: "" };
    this.#host.repaint();
    const answer = await this.#api.bisTrack(year, month, tolerance);
    this.#bis = answer.ok ? { phase: "ready", value: answer.value, error: "" } : { phase: "error", value: null, error: refusalWords("legacy-import", answer.error, answer.status ?? 0) };
    this.#host.repaint();
  }

  render(): TemplateResult {
    const userId = this.#host.userId();
    const books: BooksModel = {
      mode: this.#mode,
      chart: this.#chart,
      recurring: this.#recurring,
      periods: this.#periods,
      nextOpenableYear: this.#nextOpenableYear,
      latePosted: this.#latePosted,
      policy: this.#policy,
      tie: this.#tie,
      bis: this.#bis,
      legacyImport: this.#legacyImport,
      confirm: this.#confirm,
      composing: this.#composing,
      lineCount: this.#lineCount,
      review: this.#review,
      composeError: this.#composeError,
      trialMonth: this.#trialMonth,
      exporting: this.#exporting,
    };
    const model: GlModel = {
      phase: this.#phase,
      tab: this.#tab,
      userId,
      approvals: this.#approvals,
      selectedApproval: this.#selectedApproval,
      choice: this.#choice,
      approvalJournal: this.#approvalJournal,
      journals: this.#journals,
      journalFilter: this.#journalFilter,
      selectedJournal: this.#selectedJournal,
      trial: this.#trial,
      trialBranch: this.#trialBranch,
      branches: this.#branches,
      busy: this.#busy,
      error: this.#error,
      notice: this.#notice,
      postingQueueWords: this.#postingQueueWords,
      books,
    };
    return html`${renderGl(model)}`;
  }
}

/** JE-0007: a journal by its number, the way the register names it. */
function entryNo(journal: { entry_no: number }): string {
  return `JE-${String(journal.entry_no).padStart(4, "0")}`;
}

/** What a decision said, in the words the notice carries. */
export function decidedWords(row: IdentityApproval, approve: boolean): string {
  if (row.policy_type === "MARGIN_BELOW_MINIMUM") {
    return approve
      ? "Approved. The approval is spent on that one quote: a re-price asks again."
      : "Declined. The quote stays at the floor.";
  }
  if (row.policy_type === "JE_OVER_THRESHOLD") {
    return approve ? "Approved. The journal is posted to the ledger." : "Declined. The journal stays parked as a draft.";
  }
  return approve ? "Approved." : "Declined.";
}
