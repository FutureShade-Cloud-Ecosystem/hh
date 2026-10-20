// The bills desk's controller: the state the screens draw from, the loads, and what each press
// does. It is not an element; the root owns it, hands it a way to repaint, a way to read the form
// fields and the signed in role, and asks it for the template.
//
// Posting a bill is the consequential press: it books the amount owed. It stands behind a confirm
// step naming the bill, the vendor, the order and the server's own total; the core runs the three
// way match again at the post, and a bill that falls outside the tolerance is refused until a
// person accepts the variance with a note, a second step the screen shows in the core's figures.
// The post carries no key (the bill's status is the guard), so the bill is read again before the
// press and after it, and an answer that never arrives is settled by that read, never retried
// blind. Nothing is stored.
import type { TemplateResult } from "lit";
import type { Branch, PurchasingBill, PurchasingBillDetail, PurchasingCreateBillRequest, PurchasingPO, PurchasingPODetail, PurchasingPolicy, PurchasingSetPolicyRequest, PurchasingSetToleranceRequest, PurchasingTolerance, Result } from "@hardscapehouse/api-client";
import type { AreaDesk, DeskHost } from "../purch/area.js";
import { houseDay, poRows, refusalWords } from "../po/po-logic.js";
import { answerLost } from "../purch/purch-logic.js";
import { centsToField, formatMoney } from "../purch/purch-money.js";
import { BillsApi } from "./bills-api.js";
import { acceptWords, billBody, billRows, billStatusWord, canSetRules, canWriteBills, FALLBACK, policyBody, postConfirmWords, prefillCost, prefillQty, settleBill, toleranceBody, VISIBILITY, varianceRefusal } from "./bills-logic.js";
import { orderOptionText, renderBills, type BillConfirm, type BillsView } from "./bills-views.js";

type Failure = Extract<Result<unknown>, { ok: false }>;

export class BillsDesk implements AreaDesk {
  readonly #api: BillsApi;
  readonly #host: DeskHost;

  #phase: "loading" | "ready" | "error" = "loading";
  #view: BillsView = "list";
  #bills: readonly PurchasingBill[] = [];
  #statusFilter = "";
  #matchFilter = "";
  #billable: readonly PurchasingPO[] = [];
  #branches: readonly Branch[] = [];
  #branchId = "";
  #entryPo: PurchasingPODetail | null = null;
  #entryReading = false;
  #entryCurrency = "CAD";
  #detail: PurchasingBillDetail | null = null;
  #lineUoms: ReadonlyMap<string, string> = new Map();
  #confirm: { kind: BillConfirm; words: string } | null = null;
  #pendingTolerance: PurchasingSetToleranceRequest | null = null;
  #pendingPolicy: PurchasingSetPolicyRequest | null = null;
  #tolerance: PurchasingTolerance | null = null;
  #policy: PurchasingPolicy | null = null;
  #ruleVisibility = "MANAGER_UP";
  #ruleFallback = "CONFIRM";
  #busy = false;
  #error = "";
  #errorAt: "bill" | "rules" = "bill";
  #notice = "";
  // What the screen has already been given to seed, so a field is set once per change.
  #seedEntry = "";
  #seedRules = "";

  constructor(api: BillsApi, host: DeskHost) {
    this.#api = api;
    this.#host = host;
  }

  // ---- loads ----------------------------------------------------------------------------------

  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const [branches, orders] = await Promise.all([this.#api.branches(), this.#api.orders()]);
    if (!orders.ok) return this.#loadFailed(orders);
    if (branches.ok) {
      this.#branches = branches.value;
      this.#branchId = (branches.value.find((b) => b.is_default) ?? branches.value[0])?.id ?? "";
    }
    this.#billable = poRows(orders.value).filter((o) => ["ORDERED", "PARTIAL", "RECEIVED", "CLOSED"].includes(o.status));
    const bills = await this.#api.list(this.#branchId);
    if (!bills.ok) return this.#loadFailed(bills);
    this.#bills = billRows(bills.value);
    await this.#readRules();
    this.#phase = "ready";
    this.#host.repaint();
  }

  #loadFailed(result: Failure): void {
    this.#phase = "error";
    this.#error = refusalWords("load", result.error, result.status);
    this.#host.repaint();
  }

  async #readRules(): Promise<void> {
    const [tolerance, policy] = await Promise.all([this.#api.tolerance(this.#branchId), this.#api.policy(this.#branchId)]);
    this.#tolerance = tolerance.ok ? tolerance.value : null;
    this.#policy = policy.ok ? policy.value : null;
    if (policy.ok) {
      this.#ruleVisibility = policy.value.cost_visibility;
      this.#ruleFallback = policy.value.untagged_fallback;
    }
    this.#seedRules = "";
  }

  async #reloadList(): Promise<void> {
    const bills = await this.#api.list(this.#branchId, this.#statusFilter === "" ? undefined : (this.#statusFilter as "DRAFT"), this.#matchFilter === "" ? undefined : (this.#matchFilter as "MATCHED"));
    if (bills.ok) this.#bills = billRows(bills.value);
    else this.#fail(bills, "bill");
  }

  async #open(id: string, notice = ""): Promise<void> {
    if (id === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#notice = notice;
    this.#host.repaint();
    const bill = await this.#api.get(id);
    if (bill.ok) {
      const po = await this.#api.order(bill.value.po_id);
      this.#lineUoms = po.ok ? new Map(po.value.lines.map((l) => [l.id, l.uom])) : new Map();
      this.#detail = bill.value;
      this.#confirm = null;
      this.#view = "detail";
    } else this.#fail(bill, "bill");
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- the press ------------------------------------------------------------------------------

  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const choice = control.getAttribute("data-choice") ?? "";
    switch (action) {
      case "reload":
        void this.load();
        return true;
      case "refresh":
        void this.#refresh();
        return true;
      case "status-filter":
        this.#statusFilter = ["DRAFT", "POSTED", "VOID"].includes(choice) ? choice : "";
        void this.#refresh();
        return true;
      case "match-filter":
        this.#matchFilter = ["MATCHED", "VARIANCE", "SHORT_BILLED", "UNMATCHED"].includes(choice) ? choice : "";
        void this.#refresh();
        return true;
      case "open-bill":
        void this.#open(control.getAttribute("data-bill-id") ?? "");
        return true;
      case "enter-bill":
        this.#clearMessages();
        this.#entryPo = null;
        this.#entryCurrency = "CAD";
        this.#seedEntry = "";
        this.#view = "enter";
        this.#host.repaint();
        return true;
      case "back":
        this.#clearMessages();
        this.#confirm = null;
        this.#detail = null;
        this.#view = "list";
        this.#host.repaint();
        return true;
      case "bill-currency":
        this.#entryCurrency = choice === "USD" ? "USD" : "CAD";
        this.#host.repaint();
        return true;
      case "save-bill":
        void this.#saveBill();
        return true;
      case "ask-post":
        this.#clearMessages();
        if (this.#detail !== null) this.#confirm = { kind: "post", words: postConfirmWords(this.#detail) };
        this.#host.repaint();
        return true;
      case "do-post":
        void this.#post(false);
        return true;
      case "do-accept":
        void this.#post(true);
        return true;
      case "confirm-cancel":
        this.#confirm = null;
        this.#pendingTolerance = null;
        this.#pendingPolicy = null;
        this.#host.repaint();
        return true;
      case "rule-visibility":
        this.#ruleVisibility = choice;
        this.#host.repaint();
        return true;
      case "rule-fallback":
        this.#ruleFallback = choice;
        this.#host.repaint();
        return true;
      case "ask-tolerance":
        this.#askTolerance();
        return true;
      case "do-tolerance":
        void this.#doTolerance();
        return true;
      case "ask-policy":
        this.#askPolicy();
        return true;
      case "do-policy":
        void this.#doPolicy();
        return true;
      default:
        return false;
    }
  }

  /** The order picked on the entry form changed: its lines are read, and the form starts from them. */
  typed(name: string, value: string): void {
    if (name !== "bill-po") return;
    const picked = this.#billable.find((o) => orderOptionText(o) === value);
    this.#seedEntry = "";
    if (picked === undefined) {
      this.#entryPo = null;
      this.#entryReading = false;
      this.#host.repaint();
      return;
    }
    this.#entryReading = true;
    this.#host.repaint();
    void this.#api.order(picked.id).then((po) => {
      // A later pick may have replaced this one while it was read.
      if (this.#host.field("bill-po") !== value) return;
      this.#entryReading = false;
      if (po.ok) this.#entryPo = po.value;
      else {
        this.#entryPo = null;
        this.#fail(po, "bill");
      }
      this.#host.repaint();
    });
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
  }

  /** A rules write whose answer was lost may have been made: read the rules again and say it is not known. */
  async #failRules(result: Failure): Promise<void> {
    this.#fail(result, "rules");
    if (!answerLost(result)) return;
    await this.#readRules();
    this.#error = "No clear answer came back, so it is not known whether the change was made. The rules have been read again: check them before changing them a second time.";
  }

  #fail(result: Failure, at: "bill" | "rules"): void {
    this.#notice = "";
    this.#error = refusalWords(at === "rules" ? "tolerance" : "bill", result.error, result.status);
    this.#errorAt = at;
  }

  async #refresh(): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    await this.#reloadList();
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- entering a bill ------------------------------------------------------------------------

  async #saveBill(): Promise<void> {
    if (this.#busy) return;
    const po = this.#entryPo;
    const lines: Record<string, { qty: string; cost: string }> = {};
    for (const l of po?.lines ?? []) lines[l.id] = { qty: this.#host.field(`bq-${l.id}`), cost: this.#host.field(`bc-${l.id}`) };
    const picked = this.#billable.find((o) => orderOptionText(o) === this.#host.field("bill-po"));
    const checked = billBody(
      {
        billNo: this.#host.field("bill-no"),
        billDate: this.#host.field("bill-date"),
        dueDate: this.#host.field("bill-due"),
        tax: this.#host.field("bill-tax"),
        currency: this.#entryCurrency,
        fx: this.#host.field("bill-fx"),
        lines,
      },
      picked === undefined ? null : po,
    );
    this.#clearMessages();
    if (!checked.ok) {
      this.#error = checked.words;
      this.#errorAt = "bill";
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#host.repaint();
    const made = await this.#api.create(checked.body as PurchasingCreateBillRequest);
    if (made.ok) {
      await this.#reloadList();
      this.#busy = false;
      const state = made.value.match_state;
      await this.#open(made.value.id, `Entered bill ${made.value.bill_no} as a draft.\nIt matched ${state === "MATCHED" ? "within the tolerance" : state === "VARIANCE" ? "outside the tolerance" : state === "SHORT_BILLED" ? "short billed" : "with nothing to match"}. Nothing is owed until it is posted.`);
      return;
    }
    if (answerLost(made)) {
      await this.#reloadList();
      this.#error = "No clear answer came back, so it is not known whether the bill was entered. The list has been read again: look for it before entering it a second time.";
      this.#errorAt = "bill";
      this.#view = "list";
    } else this.#fail(made, "bill");
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- posting --------------------------------------------------------------------------------

  async #post(accept: boolean): Promise<void> {
    const detail = this.#detail;
    if (detail === null || this.#busy) return;
    let note = "";
    if (accept) {
      note = this.#host.field("accept-note").trim();
      if (note === "") {
        this.#error = "Write why you are accepting the variance: the note is kept with the bill.";
        this.#errorAt = "bill";
        this.#host.repaint();
        return;
      }
    }
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const id = detail.id;
    // Read it again before the press: the bill may have moved since it was opened.
    const fresh = await this.#api.get(id);
    if (fresh.ok && fresh.value.status !== detail.status) {
      this.#detail = fresh.value;
      this.#confirm = null;
      this.#error = `This bill changed while you had it open: it reads ${billStatusWord(fresh.value.status).toLowerCase()} now. Nothing was sent.`;
      this.#errorAt = "bill";
      await this.#reloadList();
      this.#busy = false;
      this.#host.repaint();
      return;
    }
    const before = fresh.ok ? fresh.value.status : detail.status;
    const done = await this.#api.post(id, accept ? { note } : undefined);
    if (!done.ok && !answerLost(done)) {
      const variance = varianceRefusal(done.error);
      if (variance !== null && !accept) {
        // The core re-ran the match and the bill is outside the tolerance: read it so the lines show
        // the verdict that blocked the post, then ask for the acceptance.
        const again = await this.#api.get(id);
        if (again.ok) this.#detail = again.value;
        this.#confirm = { kind: "accept", words: acceptWords(detail, variance) };
        await this.#reloadList();
      } else {
        this.#fail(done, "bill");
        this.#confirm = null;
      }
      this.#busy = false;
      this.#host.repaint();
      return;
    }
    // Settled against the bill as the core now holds it, also when the answer never arrived.
    const bill = await this.#api.get(id);
    if (!bill.ok) {
      this.#error = "No clear answer came back and the bill could not be read back. Refresh before doing anything else.";
      this.#errorAt = "bill";
      this.#confirm = null;
      this.#busy = false;
      this.#host.repaint();
      return;
    }
    this.#detail = bill.value;
    this.#confirm = null;
    const problem = settleBill(bill.value.status, before);
    if (problem !== null) {
      this.#error = problem;
      this.#errorAt = "bill";
    } else {
      this.#notice = `Posted bill ${bill.value.bill_no} for ${formatMoney(bill.value.total_cents)}.\nIt is owed to ${bill.value.vendor_name} now${accept ? ", with the variance accepted and your note kept" : ""}.`;
    }
    await this.#reloadList();
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- the match rules ------------------------------------------------------------------------

  #askTolerance(): void {
    if (this.#busy) return;
    const checked = toleranceBody({ qty: this.#host.field("tol-qty"), price: this.#host.field("tol-price"), abs: this.#host.field("tol-abs") }, this.#tolerance?.is_branch_override === true ? this.#branchId : "");
    this.#clearMessages();
    if (!checked.ok) {
      this.#error = checked.words;
      this.#errorAt = "rules";
      this.#host.repaint();
      return;
    }
    this.#pendingTolerance = checked.body;
    const b = checked.body;
    this.#confirm = {
      kind: "tolerance",
      words: `Change the match tolerance to quantity ${b.qty_tolerance_pct}%, price ${b.price_tolerance_pct}% and a price allowance of ${formatMoney(b.price_tolerance_abs_cents)}. Bills inside it match; bills outside it need a person to accept the variance before they post. It applies to bills entered or posted from now on.`,
    };
    this.#host.repaint();
  }

  async #doTolerance(): Promise<void> {
    const body = this.#pendingTolerance;
    if (body === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const done = await this.#api.setTolerance(body);
    this.#confirm = null;
    this.#pendingTolerance = null;
    if (done.ok) {
      this.#tolerance = done.value;
      this.#seedRules = "";
      this.#notice = "The match tolerance is changed.\nIt applies to bills entered or posted from now on.";
    } else await this.#failRules(done);
    this.#busy = false;
    this.#host.repaint();
  }

  #askPolicy(): void {
    if (this.#busy) return;
    const checked = policyBody({ visibility: this.#ruleVisibility, fallback: this.#ruleFallback }, this.#policy?.is_branch_override === true ? this.#branchId : "");
    this.#clearMessages();
    if (!checked.ok) {
      this.#error = checked.words;
      this.#errorAt = "rules";
      this.#host.repaint();
      return;
    }
    this.#pendingPolicy = checked.body;
    const vis = VISIBILITY.find((v) => v.id === checked.body.cost_visibility)?.word.toLowerCase() ?? "";
    const fb = FALLBACK.find((f) => f.id === checked.body.untagged_fallback)?.word.toLowerCase() ?? "";
    this.#confirm = {
      kind: "policy",
      words: `Change the purchasing policy: cost is visible to ${vis}, and an order with nobody tagged for ordering will ${fb}. Prices and values leave the purchase screens for everyone the setting excludes.`,
    };
    this.#host.repaint();
  }

  async #doPolicy(): Promise<void> {
    const body = this.#pendingPolicy;
    if (body === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const done = await this.#api.setPolicy(body);
    this.#confirm = null;
    this.#pendingPolicy = null;
    if (done.ok) {
      this.#policy = done.value;
      this.#ruleVisibility = done.value.cost_visibility;
      this.#ruleFallback = done.value.untagged_fallback;
      this.#notice = "The purchasing policy is changed.";
    } else await this.#failRules(done);
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- the template ---------------------------------------------------------------------------

  /** After a render: the entry form starts from the order, and the tolerance form from the rule now in force. */
  seed(set: (name: string, value: string) => void): void {
    const po = this.#entryPo;
    if (this.#view === "enter" && po !== null) {
      const key = `e:${po.id}`;
      if (this.#seedEntry !== key) {
        for (const l of po.lines) {
          set(`bq-${l.id}`, prefillQty(l));
          set(`bc-${l.id}`, prefillCost(l));
        }
        this.#seedEntry = key;
      }
    }
    const t = this.#tolerance;
    if (this.#view === "list" && t !== null) {
      const key = `r:${t.qty_tolerance_pct}:${t.price_tolerance_pct}:${t.price_tolerance_abs_cents}`;
      if (this.#seedRules !== key) {
        set("tol-qty", String(t.qty_tolerance_pct));
        set("tol-price", String(t.price_tolerance_pct));
        set("tol-abs", centsToField(t.price_tolerance_abs_cents));
        this.#seedRules = key;
      }
    }
  }

  render(): TemplateResult {
    const role = this.#host.role();
    return renderBills({
      phase: this.#phase,
      view: this.#view,
      bills: this.#bills,
      statusFilter: this.#statusFilter,
      matchFilter: this.#matchFilter,
      billable: this.#billable,
      branchNames: new Map(this.#branches.map((b) => [b.id, b.name])),
      entryPo: this.#entryPo,
      entryReading: this.#entryReading,
      entryCurrency: this.#entryCurrency,
      detail: this.#detail,
      lineUoms: this.#lineUoms,
      confirm: this.#confirm,
      tolerance: this.#tolerance,
      policy: this.#policy,
      ruleVisibility: this.#ruleVisibility,
      ruleFallback: this.#ruleFallback,
      canWrite: canWriteBills(role),
      canSetRules: canSetRules(role),
      busy: this.#busy,
      error: this.#phase === "error" || this.#error !== "" ? this.#error : "",
      errorAt: this.#errorAt,
      notice: this.#notice,
      today: houseDay(),
    });
  }
}
