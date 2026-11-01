// The landed cost desk's controller: the state the screens draw from, the loads, and what each
// press does. It is not an element; the root owns it, hands it a way to repaint and a way to read
// the form fields, and asks it for the template.
//
// Every write is held to the server. A capture is a draft only when the core made it; an apply, a
// late freight apply and a void each stand behind a confirm step that names the order, the basis
// and the server's own total, and afterwards the screen shows what the core now holds (the
// document read again), never what the answer said: the core answers an apply of a document that
// is not a draft like a real one. These routes carry no idempotency key (the contract declares
// none; the status guards make a replay a no-op), so one press makes one request, the controls are
// held while it runs, and an answer that never arrives is settled by reading the document again
// before another press is allowed. Nothing is stored.
import type { TemplateResult } from "lit";
import type { Branch, LandedCost, LandedCostBasis, LandedCostStatus, PurchasingPO, Result } from "@hardscapehouse/api-client";
import type { AreaDesk, DeskHost } from "../purch/area.js";
import { houseDay, poRows, refusalWords } from "../po/po-logic.js";
import { answerLost } from "../purch/purch-logic.js";
import { LandedApi } from "./landed-api.js";
import { BASES, buildCreate, landedRows, receivedOrders, retroCandidates, settleWords, statusWord, type Action } from "./landed-logic.js";
import { orderOptionText, priorOptionText, renderLanded, type CaptureModel, type DetailModel, type LandedModel, type LandedView } from "./landed-views.js";

type Failure = Extract<Result<unknown>, { ok: false }>;

const FRESH_CAPTURE: CaptureModel = { po: null, reading: false, currency: "CAD", basis: "VALUE", excludeSpecial: true, retro: false };

export class LandedDesk implements AreaDesk {
  readonly #api: LandedApi;
  readonly #host: DeskHost;

  #phase: LandedModel["phase"] = "loading";
  #view: LandedView = "list";
  #items: readonly LandedCost[] = [];
  #filter = "";
  #orders: readonly PurchasingPO[] = [];
  #branches: readonly Branch[] = [];
  #branchNames: ReadonlyMap<string, string> = new Map();
  #branchId = "";
  #capture: CaptureModel = FRESH_CAPTURE;
  #detail: DetailModel | null = null;
  #confirm: Action | null = null;
  #busy = false;
  #error = "";
  #notice = "";

  constructor(api: LandedApi, host: DeskHost) {
    this.#api = api;
    this.#host = host;
    api.watchZone(() => host.repaint());
  }

  // ---- loads ----------------------------------------------------------------------------------

  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const [branches, orders] = await Promise.all([this.#api.branches(), this.#api.orders()]);
    if (!branches.ok) return this.#loadFailed(branches);
    if (!orders.ok) return this.#loadFailed(orders);
    this.#branches = branches.value;
    this.#branchNames = new Map(branches.value.map((b) => [b.id, b.name]));
    // A scoped caller must name a branch the read can use: the default one, else the first it reaches.
    this.#branchId = (branches.value.find((b) => b.is_default) ?? branches.value[0])?.id ?? "";
    this.#orders = receivedOrders(poRows(orders.value));
    const items = await this.#api.list(this.#branchId, this.#statusFilter());
    if (!items.ok) return this.#loadFailed(items);
    this.#items = landedRows(items.value);
    this.#phase = "ready";
    this.#host.repaint();
  }

  #loadFailed(result: Failure): void {
    this.#phase = "error";
    this.#error = refusalWords("load", result.error, result.status, result.code);
    this.#host.repaint();
  }

  #statusFilter(): LandedCostStatus | undefined {
    return this.#filter === "" ? undefined : (this.#filter as LandedCostStatus);
  }

  /** The list again, quietly; a refusal is said in words and the list already drawn stays. */
  async #reloadList(): Promise<void> {
    const items = await this.#api.list(this.#branchId, this.#statusFilter());
    if (items.ok) this.#items = landedRows(items.value);
    else this.#fail(items);
  }

  /** Opens one landed cost; a notice given is kept on the screen it opens (a save says it saved). */
  async #open(id: string, notice = ""): Promise<void> {
    if (id === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#notice = notice;
    this.#host.repaint();
    const doc = await this.#api.get(id);
    if (!doc.ok) {
      this.#fail(doc);
    } else {
      const [po, preview] = await Promise.all([
        doc.value.po_id === "" ? Promise.resolve(null) : this.#api.order(doc.value.po_id),
        this.#api.preview(id),
      ]);
      this.#detail = {
        doc: doc.value,
        preview: preview.ok ? preview.value : null,
        previewError: preview.ok ? "" : refusalWords("landed", preview.error, preview.status, preview.code),
        compare: doc.value.allocation_basis,
        po: po !== null && po.ok ? po.value : null,
      };
      this.#confirm = null;
      this.#view = "detail";
    }
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
      case "filter":
        void this.#setFilter(choice);
        return true;
      case "open":
        void this.#open(control.getAttribute("data-landed-id") ?? "");
        return true;
      case "new":
        this.#clearMessages();
        this.#capture = FRESH_CAPTURE;
        this.#view = "capture";
        this.#host.repaint();
        return true;
      case "back":
        this.#clearMessages();
        this.#confirm = null;
        this.#detail = null;
        this.#view = "list";
        this.#host.repaint();
        return true;
      case "currency":
        this.#capture = { ...this.#capture, currency: choice === "USD" ? "USD" : "CAD" };
        this.#host.repaint();
        return true;
      case "basis":
        this.#capture = { ...this.#capture, basis: BASES.find((b) => b === choice) ?? "VALUE" };
        this.#host.repaint();
        return true;
      case "toggle-special":
        this.#capture = { ...this.#capture, excludeSpecial: !this.#capture.excludeSpecial };
        this.#host.repaint();
        return true;
      case "toggle-retro":
        this.#capture = { ...this.#capture, retro: !this.#capture.retro };
        this.#host.repaint();
        return true;
      case "save":
        void this.#save();
        return true;
      case "compare":
        void this.#compare(choice);
        return true;
      case "ask-apply":
      case "ask-retro":
      case "ask-void":
        this.#confirm = (action.slice(4) as Action);
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "confirm-cancel":
        this.#confirm = null;
        this.#host.repaint();
        return true;
      case "do-apply":
      case "do-retro":
      case "do-void":
        void this.#act(action.slice(3) as Action);
        return true;
      default:
        return false;
    }
  }

  /** The picked order changed: its lines are read, for the retro count and the branch it belongs to. */
  typed(name: string, value: string): void {
    if (name !== "lc-po") return;
    const picked = this.#orders.find((o) => orderOptionText(o) === value);
    if (picked === undefined) {
      this.#capture = { ...this.#capture, po: null, reading: false };
      this.#host.repaint();
      return;
    }
    this.#capture = { ...this.#capture, reading: true };
    this.#host.repaint();
    void this.#api.order(picked.id).then((po) => {
      // A later pick may have replaced this one while it was read.
      if (this.#host.field("lc-po") !== value) return;
      if (po.ok) this.#capture = { ...this.#capture, po: po.value, reading: false };
      else {
        this.#capture = { ...this.#capture, po: null, reading: false };
        this.#fail(po);
      }
      this.#host.repaint();
    });
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
  }

  #fail(result: Failure): void {
    this.#notice = "";
    this.#error = refusalWords("landed", result.error, result.status, result.code);
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

  async #setFilter(choice: string): Promise<void> {
    this.#filter = FILTER_IDS.includes(choice) ? choice : "";
    await this.#refresh();
  }

  async #compare(basis: string): Promise<void> {
    const detail = this.#detail;
    const chosen = BASES.find((b) => b === basis);
    if (detail === null || chosen === undefined || this.#busy) return;
    this.#detail = { ...detail, compare: chosen, preview: null, previewError: "" };
    this.#host.repaint();
    const preview = await this.#api.preview(detail.doc.id, chosen as LandedCostBasis);
    // A later comparison may have replaced this one while it was read.
    if (this.#detail?.compare !== chosen) return;
    this.#detail = {
      ...this.#detail,
      preview: preview.ok ? preview.value : null,
      previewError: preview.ok ? "" : refusalWords("landed", preview.error, preview.status, preview.code),
    };
    this.#host.repaint();
  }

  // ---- the writes -----------------------------------------------------------------------------

  async #save(): Promise<void> {
    if (this.#busy) return;
    const cap = this.#capture;
    const onHand: Record<string, string> = {};
    for (const line of cap.po?.lines ?? []) onHand[line.id] = this.#host.field(`onhand-${line.id}`);
    const picked = this.#orders.find((o) => orderOptionText(o) === this.#host.field("lc-po"));
    const retroText = this.#host.field("lc-retro-of");
    const prior = cap.po === null ? undefined : retroCandidates(this.#items, cap.po.id).find((p) => priorOptionText(p, this.#today()) === retroText);
    const check = buildCreate(
      {
        poNo: picked?.po_no ?? "",
        currency: cap.currency,
        fx: this.#host.field("lc-fx"),
        freight: this.#host.field("lc-freight"),
        duty: this.#host.field("lc-duty"),
        handling: this.#host.field("lc-handling"),
        basis: cap.basis,
        excludeSpecial: cap.excludeSpecial,
        retro: cap.retro,
        retroOfId: prior?.id ?? "",
        onHand,
      },
      cap.po,
    );
    if (!check.ok) {
      this.#error = check.words;
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const made = await this.#api.create(check.body);
    if (made.ok) {
      await this.#reloadList();
      this.#busy = false;
      await this.#open(made.value.id, `Saved as a draft on ${check.body.po_ref}.\nNothing is added to the cost of the goods until it is applied.`);
      return;
    }
    if (answerLost(made)) {
      // The answer never arrived: the draft may exist. Read the list so the person sees it before saving again.
      await this.#reloadList();
      this.#error = "No clear answer came back, so it is not known whether the draft was saved. The list has been read again: look for it before saving a second time.";
      this.#view = "list";
    } else {
      this.#fail(made);
    }
    this.#busy = false;
    this.#host.repaint();
  }

  async #act(action: Action): Promise<void> {
    const detail = this.#detail;
    if (detail === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const id = detail.doc.id;
    // Read it again before the press: the document may have moved since it was opened, and a
    // consequential press is made on what the core holds now, never on what the screen remembers.
    const fresh = await this.#api.get(id);
    if (fresh.ok && fresh.value.status !== detail.doc.status) {
      this.#detail = { ...detail, doc: fresh.value, compare: fresh.value.allocation_basis };
      this.#confirm = null;
      this.#error = `This landed cost changed while you had it open: it reads ${statusWord(fresh.value.status).toLowerCase()} now. Nothing was sent. Check it, then go on if it still applies.`;
      await this.#reloadList();
      this.#busy = false;
      this.#host.repaint();
      return;
    }
    const before = fresh.ok ? fresh.value.status : detail.doc.status;
    const answered = action === "apply" ? await this.#api.apply(id) : action === "retro" ? await this.#api.retro(id) : await this.#api.void(id);
    if (!answered.ok && !answerLost(answered)) {
      this.#fail(answered);
      this.#busy = false;
      this.#host.repaint();
      return;
    }
    // Settled against the document as the core now holds it, also when the answer never arrived.
    const doc = await this.#api.get(id);
    if (!doc.ok) {
      this.#error = answered.ok
        ? "The core accepted it, but the document could not be read back. Refresh before doing anything else."
        : "No clear answer came back and the document could not be read back. Refresh before doing anything else.";
      this.#confirm = null;
      this.#busy = false;
      this.#host.repaint();
      return;
    }
    const preview = await this.#api.preview(id);
    this.#detail = { ...detail, doc: doc.value, preview: preview.ok ? preview.value : null, previewError: "", compare: doc.value.allocation_basis };
    this.#confirm = null;
    const problem = settleWords(action, doc.value.status, before);
    if (problem !== null) {
      this.#error = problem;
    } else {
      const order = doc.value.po_ref === "" ? "the order" : doc.value.po_ref;
      this.#notice =
        action === "void"
          ? `Voided: the landed cost on ${order} is reversed.\nThe cost of the goods is back to what it was before it was applied.`
          : `${action === "retro" ? "Late freight applied" : "Applied"} to ${order}.\nIt reads ${statusWord(doc.value.status).toLowerCase()} now, and the cost of the goods includes it.`;
    }
    await this.#reloadList();
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- the template ---------------------------------------------------------------------------

  render(): TemplateResult {
    return renderLanded({
      phase: this.#phase,
      view: this.#view,
      items: this.#items,
      filter: this.#filter,
      orders: this.#orders,
      branchNames: this.#branchNames,
      branchCount: this.#branches.length,
      capture: this.#capture,
      detail: this.#detail,
      confirm: this.#confirm,
      busy: this.#busy,
      error: this.#phase === "error" || this.#error !== "" ? this.#error : "",
      notice: this.#notice,
      today: this.#today(),
    });
  }

  /** Today in the house zone: the stamps on screen read against it. */
  #today(): string {
    return houseDay();
  }
}

const FILTER_IDS = ["", "DRAFT", "POSTED", "RETRO_ADJUSTED", "VOIDED"];
