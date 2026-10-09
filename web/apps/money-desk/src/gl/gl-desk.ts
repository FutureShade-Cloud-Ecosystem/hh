// The ledger slice's state and its presses. The Financial Controller's desk:
// the approvals queue it decides (floor requests, parked journals, freight
// and the credit exceptions), the journal register, and the trial balance.
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
import type { Branch, GlJournal, GlTrialBalanceResult, IdentityApproval } from "@hardscapehouse/api-client";
import type { GlApi } from "./gl-api.js";
import { canDecide, refusalWords } from "./gl-logic.js";
import { renderGl, type GlModel, type GlTab, type JournalFilter } from "./gl-views.js";

export interface GlHost {
  readonly repaint: () => void;
  /** The text of a named field in the desk's own render root. */
  readonly field: (name: string) => string;
  /** The signed in caller's user id, for the four eyes rule. */
  readonly userId: () => string;
  /** Brings the first element matching the selector into view in the desk's render root. */
  readonly reveal: (selector: string) => void;
}

export class GlDesk {
  readonly #api: GlApi;
  readonly #host: GlHost;
  #phase: GlModel["phase"] = "loading";
  #tab: GlTab = "approvals";
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
  // A selection asks the screen to bring its panel into view once it has rendered: on a phone the
  // panel sits under the whole list and a press would otherwise change nothing the person can see.
  #reveal = "";
  #branches: Branch[] = [];
  #busy = false;
  #error = "";
  #notice = "";

  constructor(api: GlApi, host: GlHost) {
    this.#api = api;
    this.#host = host;
  }

  /** The first load after sign in: approvals, journals, trial balance and the branch words. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const [approvals, journals, trial, branches] = await Promise.all([
      this.#api.pendingApprovals(),
      this.#api.journals(),
      this.#api.trialBalance(this.#trialBranch),
      this.#api.branches(),
    ]);
    if (!approvals.ok) {
      this.#phase = "error";
      this.#error = refusalWords("load", approvals.error, approvals.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#approvals = approvals.value;
    this.#journals = journals.ok ? journals.value : [];
    this.#trial = trial.ok ? trial.value : null;
    this.#branches = branches.ok ? branches.value : [];
    // The ledger's own refusals are said, not hidden, but they never hide the queue.
    this.#error = !journals.ok
      ? refusalWords("journals", journals.error, journals.status ?? 0)
      : !trial.ok
        ? refusalWords("trial", trial.error, trial.status ?? 0)
        : "";
    this.#phase = "ready";
    this.#host.repaint();
  }

  /** A control was pressed; true when this slice owns it. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    if (action === null) return false;
    switch (action) {
      case "gl-tab": {
        const tab = control.getAttribute("data-tab");
        if (tab === "approvals" || tab === "journals" || tab === "trial") {
          this.#tab = tab;
          this.#error = "";
          this.#notice = "";
          this.#host.repaint();
        }
        return true;
      }
      case "gl-reload":
        void this.load();
        return true;
      case "gl-select-approval":
        this.#reveal = "[data-decide-panel]";
        void this.#selectApproval(control.getAttribute("data-id") ?? "");
        return true;
      case "gl-trial-branch":
        void this.#chooseBranch(control.getAttribute("data-branch") ?? "");
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
      default:
        return false;
    }
  }

  async #chooseBranch(branchId: string): Promise<void> {
    this.#trialBranch = branchId;
    const trial = await this.#api.trialBalance(branchId);
    if (this.#trialBranch !== branchId) return;
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

  render(): TemplateResult {
    const userId = this.#host.userId();
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
    };
    return html`${renderGl(model)}`;
  }
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
