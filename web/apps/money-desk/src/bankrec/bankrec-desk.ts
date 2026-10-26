// The bank reconciliation slice's state and its presses. The Financial Controller's desk: the accounts and the
// month end (open a month, refresh its figures, sign it off), the statement import, the matching (the auto match,
// a line paired by hand, a match undone) and the match rules (a new version approved).
//
// The app holds no authority and computes no money of its own. Who may do what is the server's answer; the screen
// holds back only what it knows before a press (a month whose difference is not zero cannot be signed off, a line
// that is not unmatched is not matched, an id that is not an id is not sent), and the server enforces it again.
// The month's difference is the statement balance less the ledger balance, worked out by the core when the month
// is opened or refreshed.
//
// Every control is a plain element carrying a data-action name; the host's one click listener hands it to press().
// Form fields carry data-field names; the host's input and change listeners hand them to input() and change(),
// which keep the typed values in the desk so a repaint, or a move to another tab, never loses them.
import { html, type TemplateResult } from "lit";
import type { BankRecAccount, BankRecMatch, BankRecMatchRule, BankRecReconciliation, BankRecStatementLine, GlJournal } from "@hardscapehouse/api-client";
import type { BankrecApi, LineStatus, TargetType } from "./bankrec-api.js";
import {
  importFailedWords,
  isUuid,
  periodWords,
  refusalWords,
  ruleSpec,
  ruleSpecFields,
  RULE_KINDS,
  type RuleFields,
} from "./bankrec-logic.js";
import { centsToField, formatMoney, parseBalanceToCents, parseToleranceToCents } from "./bankrec-money.js";
import { renderBankrec, type BankrecModel, type BrTab, type ImportView } from "./bankrec-views.js";

export interface BankrecHost {
  readonly repaint: () => void;
  /** Puts a value into a named field of the desk's render root, a select by the id of its option. */
  readonly setField: (name: string, value: string) => void;
  /** Brings the first element matching the selector into view in the desk's render root. */
  readonly reveal: (selector: string) => void;
}

/** The month before today: the month a statement just closed, from the host's own clock. */
function previousMonth(): { year: number; month: number } {
  const now = new Date();
  const before = new Date(now.getFullYear(), now.getMonth() - 1, 1);
  return { year: before.getFullYear(), month: before.getMonth() + 1 };
}

const TEXT_FIELDS = [
  "br-year",
  "br-balance",
  "br-csv",
  "br-from",
  "br-to",
  "br-target",
  "br-rule-code",
  "br-rule-name",
  "br-rule-priority",
  "br-rule-tolerance",
  "br-rule-descriptor",
  "br-rule-window",
  "br-rule-fee-account",
] as const;

export class BankrecDesk {
  readonly #api: BankrecApi;
  readonly #host: BankrecHost;
  #phase: BankrecModel["phase"] = "loading";
  #loaded = false;
  #tab: BrTab = "accounts";
  #accounts: BankRecAccount[] = [];
  #accountId = "";
  #recons: BankRecReconciliation[] = [];
  #confirmSignOff: string | null = null;
  #importView: ImportView | null = null;
  #filename = "";
  #lines: BankRecStatementLine[] = [];
  #linesNext: string | null = null;
  #linesBusy = false;
  #lineFilter: LineStatus | "" = "UNMATCHED";
  #selectedLine: string | null = null;
  #history: BankRecMatch[] | null = null;
  #confirmUndo = false;
  #targetType: TargetType = "PAYMENT";
  #journals: GlJournal[] = [];
  #rules: BankRecMatchRule[] = [];
  #rulesLoaded = false;
  #linesLoaded = false;
  // The last message brought into view, so a new outcome is shown to a person scrolled away from the banner.
  #lastSaid = "";
  #ruleKind: string = RULE_KINDS[0] ?? "gateway_ref_exact";
  #busy = false;
  #error = "";
  #notice = "";
  // What the person has typed or chosen. A select holds the id of its chosen option.
  readonly #form: Record<string, string> = {};
  // A selection asks the screen to bring its panel into view once it has rendered: on a phone the panel sits
  // under the whole list and a press would otherwise change nothing the person can see.
  #reveal = "";
  // Months opened with a zero balance derive their statement figure from the month's lines; a refresh of such a
  // month sends 0 again so the core keeps statement_balance_derived set and re-derives from the lines.
  readonly #derivedMonths = new Set<string>();
  // The fields to put back into the page after the next paint (a clone of a rule, a cleared form).
  #fill: Record<string, string> | null = null;

  constructor(api: BankrecApi, host: BankrecHost) {
    this.#api = api;
    this.#host = host;
    const prior = previousMonth();
    this.#form["br-year"] = String(prior.year);
    this.#form["br-month"] = String(prior.month);
    this.#form["br-rule-fee-split"] = "no";
  }

  /** The first load, when the controller opens the area: the accounts and the months of the chosen account. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const accounts = await this.#api.accounts();
    if (!accounts.ok) {
      this.#phase = "error";
      this.#error = refusalWords("load", accounts.error, accounts.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#accounts = accounts.value;
    if (!this.#accounts.some((a) => a.id === this.#accountId)) {
      this.#accountId = this.#accounts.find((a) => a.account_type === "BANK")?.id ?? this.#accounts[0]?.id ?? "";
    }
    this.#error = "";
    await this.#loadRecons();
    this.#phase = "ready";
    this.#loaded = true;
    this.#host.repaint();
  }

  /** The area was chosen: load once, then keep what is on screen. */
  ensureLoaded(): void {
    if (!this.#loaded && this.#phase !== "error" && !this.#busy) void this.load();
  }

  async #loadRecons(): Promise<void> {
    if (this.#accountId === "") {
      this.#recons = [];
      return;
    }
    const answer = await this.#api.reconciliations(this.#accountId);
    if (answer.ok) this.#recons = answer.value;
    else this.#error = refusalWords("load", answer.error, answer.status ?? 0);
  }

  async #loadAccounts(): Promise<void> {
    const answer = await this.#api.accounts();
    if (answer.ok) this.#accounts = answer.value;
    else this.#error = refusalWords("load", answer.error, answer.status ?? 0);
  }

  async #loadLines(more: boolean): Promise<void> {
    if (this.#accountId === "") {
      this.#lines = [];
      this.#linesNext = null;
      this.#linesLoaded = true;
      return;
    }
    if (!more) this.#linesLoaded = false;
    const cursor = more ? this.#linesNext : null;
    const account = this.#accountId;
    const filter = this.#lineFilter;
    this.#linesBusy = true;
    this.#host.repaint();
    const answer = await this.#api.lines(account, filter, cursor);
    this.#linesBusy = false;
    // A reply for a state the person has since left is dropped.
    if (account !== this.#accountId || filter !== this.#lineFilter) return;
    if (!answer.ok) {
      this.#error = refusalWords("load", answer.error, answer.status ?? 0);
      return;
    }
    this.#linesLoaded = true;
    this.#lines = more ? [...this.#lines, ...answer.value.items] : answer.value.items;
    this.#linesNext = answer.value.next;
  }

  async #loadRules(): Promise<void> {
    const answer = await this.#api.rules();
    if (answer.ok) {
      this.#rules = answer.value;
      this.#rulesLoaded = true;
    } else this.#error = refusalWords("load", answer.error, answer.status ?? 0);
  }

  async #loadJournals(): Promise<void> {
    if (this.#journals.length > 0) return;
    const answer = await this.#api.postedJournals();
    if (answer.ok) this.#journals = answer.value;
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
  }

  /** A control was pressed; true when this slice owns it. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    if (action === null || !action.startsWith("br-")) return false;
    const id = control.getAttribute("data-id") ?? "";
    switch (action) {
      case "br-tab": {
        const tab = control.getAttribute("data-tab");
        if (tab === "accounts" || tab === "import" || tab === "match" || tab === "rules") void this.#openTab(tab);
        return true;
      }
      case "br-reload":
        this.#loaded = false;
        void this.load();
        return true;
      case "br-pick-account":
        void this.#chooseAccount(id);
        return true;
      case "br-open-month":
        void this.#openMonth();
        return true;
      case "br-refresh-month": {
        const row = this.#recons.find((r) => r.id === id);
        if (row !== undefined) void this.#refreshMonth(row);
        return true;
      }
      case "br-sign-off":
        this.#confirmSignOff = id;
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "br-cancel-sign-off":
        this.#confirmSignOff = null;
        this.#host.repaint();
        return true;
      case "br-confirm-sign-off":
        void this.#signOff(id);
        return true;
      case "br-import":
        void this.#importRows();
        return true;
      case "br-import-clear":
        this.#form["br-csv"] = "";
        this.#filename = "";
        this.#importView = null;
        this.#clearMessages();
        this.#fill = { "br-csv": "", "br-file": "" };
        this.#host.repaint();
        return true;
      case "br-go-match":
        void this.#openTab("match");
        return true;
      case "br-filter": {
        const f = control.getAttribute("data-filter");
        if (f === "" || f === "UNMATCHED" || f === "MATCHED" || f === "EXCLUDED") void this.#setFilter(f);
        return true;
      }
      case "br-more-lines":
        void this.#loadLines(true).then(() => this.#host.repaint());
        return true;
      case "br-auto":
        void this.#autoMatch();
        return true;
      case "br-select-line":
        this.#reveal = "[data-line-panel]";
        void this.#selectLine(id);
        return true;
      case "br-close-line":
        this.#selectedLine = null;
        this.#history = null;
        this.#confirmUndo = false;
        this.#host.repaint();
        return true;
      case "br-target": {
        const t = control.getAttribute("data-target");
        if (t === "PAYMENT" || t === "LODGEMENT" || t === "JOURNAL" || t === "GROUP") {
          this.#targetType = t;
          this.#form["br-target"] = "";
          this.#form["br-journal"] = "";
          this.#fill = { "br-target": "", "br-journal": "" };
          if (t === "JOURNAL") void this.#loadJournals().then(() => this.#host.repaint());
          this.#host.repaint();
        }
        return true;
      }
      case "br-match-line":
        void this.#matchByHand(id);
        return true;
      case "br-undo":
        this.#confirmUndo = true;
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "br-cancel-undo":
        this.#confirmUndo = false;
        this.#host.repaint();
        return true;
      case "br-confirm-undo":
        void this.#undo(id);
        return true;
      case "br-rule-clone": {
        const rule = this.#rules.find((r) => r.id === id);
        if (rule !== undefined) this.#cloneRule(rule);
        return true;
      }
      case "br-rule-save":
        void this.#saveRule();
        return true;
      default:
        return false;
    }
  }

  /** A field changed as the person typed: kept in the desk, so nothing typed is lost to a repaint. */
  input(control: Element): void {
    const name = control.getAttribute("data-field");
    if (name === null || !name.startsWith("br-")) return;
    if (control instanceof HTMLInputElement || control instanceof HTMLTextAreaElement) {
      this.#form[name] = control.value;
      // The balance hint reads the typed text; nothing else needs a repaint per key.
      if (name === "br-balance") this.#host.repaint();
    }
  }

  /** A field was left, a select chosen or a file picked. */
  change(control: Element): void {
    const name = control.getAttribute("data-field");
    if (name === null || !name.startsWith("br-")) return;
    if (control instanceof HTMLSelectElement) {
      const chosen = control.selectedOptions[0]?.getAttribute("data-id") ?? "";
      this.#form[name] = chosen;
      if (name === "br-account") void this.#chooseAccount(chosen);
      if (name === "br-rule-kind") {
        this.#ruleKind = chosen;
        this.#host.repaint();
      }
      return;
    }
    if (control instanceof HTMLInputElement && name === "br-file") {
      const file = control.files?.[0];
      if (file === undefined) return;
      void file.text().then((text) => {
        this.#filename = file.name;
        this.#form["br-csv"] = text;
        this.#importView = null;
        this.#fill = { "br-csv": text };
        this.#host.repaint();
      });
      return;
    }
    this.input(control);
  }

  /** Called after each render: brings a just selected panel into view and puts typed values back. */
  afterRender(): void {
    if (this.#phase !== "ready") return;
    for (const name of TEXT_FIELDS) this.#host.setField(name, this.#form[name] ?? "");
    this.#host.setField("br-account", this.#accountId);
    this.#host.setField("br-month", this.#form["br-month"] ?? "");
    this.#host.setField("br-journal", this.#form["br-journal"] ?? "");
    this.#host.setField("br-rule-kind", this.#ruleKind);
    this.#host.setField("br-rule-fee-split", this.#form["br-rule-fee-split"] ?? "no");
    if (this.#fill !== null) {
      const fill = this.#fill;
      this.#fill = null;
      for (const [name, value] of Object.entries(fill)) this.#host.setField(name, value);
    }
    const said = this.#error !== "" ? this.#error : this.#notice;
    if (said === "") this.#lastSaid = "";
    else if (said !== this.#lastSaid) {
      this.#lastSaid = said;
      if (this.#reveal === "") this.#reveal = this.#error !== "" ? "[data-error]" : "[data-notice]";
    }
    if (this.#reveal === "") return;
    const selector = this.#reveal;
    this.#reveal = "";
    this.#host.reveal(selector);
  }

  async #openTab(tab: BrTab): Promise<void> {
    this.#tab = tab;
    this.#clearMessages();
    this.#confirmSignOff = null;
    this.#host.repaint();
    if (tab === "match") {
      await this.#loadLines(false);
      this.#host.repaint();
    } else if (tab === "rules") {
      await this.#loadRules();
      this.#host.repaint();
    }
  }

  async #chooseAccount(id: string): Promise<void> {
    if (id === "" || id === this.#accountId) {
      this.#host.repaint();
      return;
    }
    this.#accountId = id;
    this.#selectedLine = null;
    this.#history = null;
    this.#confirmSignOff = null;
    this.#confirmUndo = false;
    this.#importView = null;
    this.#clearMessages();
    this.#host.repaint();
    await this.#loadRecons();
    if (this.#tab === "match") await this.#loadLines(false);
    this.#host.repaint();
  }

  // ---- month end --------------------------------------------------------------------------------------

  #monthInput(): { year: number; month: number } | string {
    const year = Number(this.#form["br-year"]?.trim());
    const month = Number(this.#form["br-month"]);
    if (!Number.isInteger(year) || year < 2000 || year > 2100) return "Enter the year as four digits.";
    if (!Number.isInteger(month) || month < 1 || month > 12) return "Pick the month.";
    return { year, month };
  }

  async #openMonth(): Promise<void> {
    if (this.#busy) return;
    this.#clearMessages();
    const when = this.#monthInput();
    if (typeof when === "string") {
      this.#error = when;
      this.#host.repaint();
      return;
    }
    const typed = (this.#form["br-balance"] ?? "").trim();
    const cents = parseBalanceToCents(typed);
    // A blank box is never sent as zero: the core reads a zero as "use the month's lines" and fills its own figure.
    if (typed === "" || cents === null) {
      this.#error = "Enter the closing balance from the statement, in dollars and cents, such as 25,255.75.";
      this.#host.repaint();
      return;
    }
    const typedZero = typed === "" ? false : cents === 0;
    const key = `${this.#accountId}:${when.year}:${when.month}`;
    if (typedZero) this.#derivedMonths.add(key);
    else this.#derivedMonths.delete(key);
    await this.#writeMonth(when.year, when.month, cents, typedZero);
  }

  async #refreshMonth(row: BankRecReconciliation): Promise<void> {
    if (this.#busy) return;
    this.#clearMessages();
    const derived = this.#derivedMonths.has(`${row.bank_account_id}:${row.year}:${row.month}`);
    await this.#writeMonth(row.year, row.month, derived ? 0 : row.statement_balance_cents, false);
  }

  async #writeMonth(year: number, month: number, cents: number, typedZero: boolean): Promise<void> {
    this.#busy = true;
    this.#host.repaint();
    const answer = await this.#api.openReconciliation(this.#accountId, year, month, cents);
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("open", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    const got = answer.value;
    const name = periodWords(got.year, got.month);
    this.#notice =
      typedZero && got.statement_balance_cents !== 0
        ? `${name} is open. You entered a zero balance, and the core replaced it with ${formatMoney(got.statement_balance_cents)}, the net of the month's statement lines. Enter the real closing balance if it is not zero.`
        : `${name} is open: the difference is worked out against the ledger as it stands now.`;
    await this.#loadRecons();
    this.#host.repaint();
  }

  async #signOff(id: string): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const answer = await this.#api.completeReconciliation(id);
    this.#busy = false;
    this.#confirmSignOff = null;
    if (!answer.ok) {
      this.#error = refusalWords("complete", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    const account = this.#accounts.find((a) => a.id === answer.value.bank_account_id)?.name ?? "this account";
    this.#notice = `${periodWords(answer.value.year, answer.value.month)} is signed off for ${account}. Its figures no longer change.`;
    await this.#loadRecons();
    await this.#loadAccounts();
    this.#host.repaint();
  }

  // ---- import -----------------------------------------------------------------------------------------

  async #importRows(): Promise<void> {
    if (this.#busy) return;
    this.#clearMessages();
    this.#importView = null;
    const account = this.#accounts.find((a) => a.id === this.#accountId);
    const content = this.#form["br-csv"] ?? "";
    if (account === undefined) {
      this.#error = "Pick the account the statement is for.";
      this.#host.repaint();
      return;
    }
    if (content.trim() === "") {
      this.#error = "Choose a statement file, or paste its rows, first.";
      this.#host.repaint();
      return;
    }
    const profile = account.account_type === "CREDIT_CARD" ? "visa-default" : "rbc-default";
    const filename = this.#filename !== "" ? this.#filename : "pasted.csv";
    this.#busy = true;
    this.#host.repaint();
    const answer = await this.#api.importStatement(account.id, profile, filename, content);
    this.#busy = false;
    if (!answer.ok) {
      // A file the parser cannot read is answered with the audit envelope; its sentence is the parser's.
      this.#importView = answer.status === 422 ? { kind: "failed", words: importFailedWords(answer.error) } : null;
      if (answer.status !== 422) this.#error = refusalWords("import", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    const r = answer.value;
    this.#importView = { kind: "ok", filename, parsed: r.parsed, inserted: r.inserted, duplicates: r.duplicates };
    await this.#loadAccounts();
    this.#host.repaint();
  }

  // ---- matching ---------------------------------------------------------------------------------------

  async #setFilter(f: LineStatus | ""): Promise<void> {
    this.#lineFilter = f;
    this.#selectedLine = null;
    this.#history = null;
    this.#clearMessages();
    this.#host.repaint();
    await this.#loadLines(false);
    this.#host.repaint();
  }

  async #autoMatch(): Promise<void> {
    if (this.#busy) return;
    this.#clearMessages();
    const from = (this.#form["br-from"] ?? "").trim();
    const to = (this.#form["br-to"] ?? "").trim();
    if (from !== "" && to !== "" && to < from) {
      this.#error = "The end date is before the start date.";
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#host.repaint();
    const answer = await this.#api.autoMatch(this.#accountId, from, to);
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("match", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    const matched = answer.value.matched;
    const left = answer.value.unmatched;
    this.#notice =
      matched === 0
        ? `Auto match found nothing new to pair. ${left === 1 ? "1 credit stays unmatched" : `${left} credits stay unmatched`}.`
        : `Auto match paired ${matched === 1 ? "1 line" : `${matched} lines`}, and posted their clearing entries. ${left === 1 ? "1 credit stays unmatched" : `${left} credits stay unmatched`}: match ${left === 1 ? "it" : "them"} by hand. Refresh the month's figures to see the difference move.`;
    await this.#loadLines(false);
    await this.#loadAccounts();
    await this.#loadRecons();
    this.#host.repaint();
  }

  async #selectLine(id: string): Promise<void> {
    this.#selectedLine = id;
    this.#history = null;
    this.#confirmUndo = false;
    this.#clearMessages();
    this.#targetType = "PAYMENT";
    this.#form["br-target"] = "";
    this.#form["br-journal"] = "";
    this.#fill = { "br-target": "", "br-journal": "" };
    this.#host.repaint();
    const history = await this.#api.history(id);
    if (this.#selectedLine !== id) return;
    if (history.ok) this.#history = history.value;
    else {
      this.#history = [];
      this.#error = refusalWords("load", history.error, history.status ?? 0);
    }
    this.#host.repaint();
  }

  async #matchByHand(lineId: string): Promise<void> {
    if (this.#busy) return;
    this.#clearMessages();
    const type = this.#targetType;
    let ref = "";
    if (type === "JOURNAL") {
      ref = this.#form["br-journal"] ?? "";
      if (ref === "") {
        this.#error = "Pick the journal the line clears against.";
        this.#host.repaint();
        return;
      }
    } else {
      ref = (this.#form["br-target"] ?? "").trim();
      const label = type === "PAYMENT" ? "payment" : type === "LODGEMENT" ? "lodgement" : "group";
      if (ref === "" && type !== "GROUP") {
        this.#error = `Paste the ${label} id the line clears against.`;
        this.#host.repaint();
        return;
      }
      if (ref !== "" && !isUuid(ref)) {
        this.#error = `That is not a ${label} id. It looks like 2f6a1c00-0000-4000-9000-0000000000f1: copy it from the ${label} itself.`;
        this.#host.repaint();
        return;
      }
    }
    this.#busy = true;
    this.#host.repaint();
    const answer = await this.#api.manualMatch(lineId, type, ref.toLowerCase());
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("match", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#notice =
      type === "PAYMENT" || type === "LODGEMENT"
        ? "Matched. The clearing entry posted to the ledger."
        : "Matched. No new entry was posted: the line is paired to something already in the ledger.";
    this.#selectedLine = null;
    this.#history = null;
    await this.#loadLines(false);
    await this.#loadAccounts();
    this.#host.repaint();
  }

  async #undo(matchId: string): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const answer = await this.#api.unmatch(matchId);
    this.#busy = false;
    this.#confirmUndo = false;
    if (!answer.ok) {
      this.#error = refusalWords("unmatch", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#notice = "Match undone. The line is unmatched again. For a payment or lodgement the reversing entry posted.";
    const kept = this.#selectedLine;
    await this.#loadLines(false);
    await this.#loadAccounts();
    if (kept !== null && this.#lines.some((l) => l.id === kept)) {
      await this.#selectLine(kept);
      // selectLine clears messages; the outcome stays.
      this.#notice = "Match undone. The line is unmatched again. For a payment or lodgement the reversing entry posted.";
    } else {
      this.#selectedLine = null;
      this.#history = null;
    }
    this.#host.repaint();
  }

  // ---- rules ------------------------------------------------------------------------------------------

  #cloneRule(rule: BankRecMatchRule): void {
    const f = ruleSpecFields(rule.spec);
    this.#ruleKind = f.kind;
    this.#form["br-rule-code"] = rule.code;
    this.#form["br-rule-name"] = rule.name;
    this.#form["br-rule-priority"] = String(rule.priority);
    this.#form["br-rule-tolerance"] = centsToField(f.toleranceCents);
    this.#form["br-rule-descriptor"] = f.descriptor;
    this.#form["br-rule-window"] = String(f.windowDays);
    this.#form["br-rule-fee-split"] = f.feeSplit ? "yes" : "no";
    this.#form["br-rule-fee-account"] = f.feeAccount;
    this.#clearMessages();
    this.#reveal = "[data-rule-form]";
    this.#host.repaint();
  }

  async #saveRule(): Promise<void> {
    if (this.#busy) return;
    this.#clearMessages();
    const code = (this.#form["br-rule-code"] ?? "").trim();
    const name = (this.#form["br-rule-name"] ?? "").trim();
    if (code === "") {
      this.#error = "Give the rule a code, such as gateway-ref-exact.";
      this.#host.repaint();
      return;
    }
    const priorityText = (this.#form["br-rule-priority"] ?? "").trim();
    const priority = priorityText === "" ? 100 : Number(priorityText);
    if (!Number.isInteger(priority) || priority < 1) {
      this.#error = "Priority is a whole number from 1: the lowest number is tried first.";
      this.#host.repaint();
      return;
    }
    const fuzzy = this.#ruleKind === "descriptor_fuzzy";
    let toleranceCents = 0;
    if (!fuzzy) {
      const typed = (this.#form["br-rule-tolerance"] ?? "").trim();
      const parsed = typed === "" ? 0 : parseToleranceToCents(typed);
      if (parsed === null) {
        this.#error = "Enter the tolerance in dollars and cents, such as 0.50, or leave it empty for an exact amount.";
        this.#host.repaint();
        return;
      }
      toleranceCents = parsed;
    }
    const descriptor = (this.#form["br-rule-descriptor"] ?? "").trim();
    const windowText = (this.#form["br-rule-window"] ?? "").trim();
    const windowDays = windowText === "" ? 0 : Number(windowText);
    if (fuzzy && descriptor === "") {
      this.#error = "Say what the statement description contains, such as RUN.";
      this.#host.repaint();
      return;
    }
    if (fuzzy && (!Number.isInteger(windowDays) || windowDays < 0 || windowDays > 30)) {
      this.#error = "The date window is a whole number of days from 0 to 30.";
      this.#host.repaint();
      return;
    }
    const fields: RuleFields = {
      kind: this.#ruleKind,
      toleranceCents,
      descriptor,
      windowDays,
      feeSplit: this.#form["br-rule-fee-split"] === "yes",
      feeAccount: (this.#form["br-rule-fee-account"] ?? "").trim(),
    };
    this.#busy = true;
    this.#host.repaint();
    const answer = await this.#api.createRule(code, name === "" ? code : name, priority, ruleSpec(fields));
    this.#busy = false;
    if (!answer.ok) {
      this.#error = refusalWords("rule", answer.error, answer.status ?? 0);
      this.#host.repaint();
      return;
    }
    this.#notice = `Version ${answer.value.version} of ${answer.value.code} is in use. The version before it is retired.`;
    await this.#loadRules();
    this.#host.repaint();
  }

  render(): TemplateResult {
    const model: BankrecModel = {
      phase: this.#phase,
      tab: this.#tab,
      accounts: this.#accounts,
      accountId: this.#accountId,
      recons: this.#recons,
      confirmSignOff: this.#confirmSignOff,
      balanceText: this.#form["br-balance"] ?? "",
      importView: this.#importView,
      lines: this.#lines,
      linesNext: this.#linesNext,
      linesBusy: this.#linesBusy,
      lineFilter: this.#lineFilter,
      selectedLine: this.#selectedLine,
      history: this.#history,
      confirmUndo: this.#confirmUndo,
      targetType: this.#targetType,
      journals: this.#journals,
      rules: this.#rules,
      rulesLoaded: this.#rulesLoaded,
      linesLoaded: this.#linesLoaded,
      ruleKind: this.#ruleKind,
      busy: this.#busy,
      error: this.#error,
      notice: this.#notice,
    };
    return html`${renderBankrec(model)}`;
  }
}
