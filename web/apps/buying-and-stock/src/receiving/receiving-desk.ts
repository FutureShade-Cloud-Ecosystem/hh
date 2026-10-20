// The receiving desk's controller: the state the screens draw from, the loads, and what each press
// does. It is not an element; the root owns it, hands it a way to repaint and a way to read the
// form fields, and asks it for the template.
//
// The three presses that matter are consequential and each stands behind a confirm step that names
// what it will do: receive (goods move into stock), apply a landed cost to a receipt (posts to the
// books) and send the order (it leaves the building). Receiving carries no idempotency key in the
// contract, so an answer that never arrives is settled by reading the order again before another
// press is allowed. A send carries one: it is minted when the attempt begins and reused on every
// retry of that attempt, so a retry is one send, never two. After every write the screen shows what
// the core now holds, never what the answer expected. Nothing is stored.
import type { TemplateResult } from "lit";
import type { Branch, PurchasingAttachLandedCostRequest, PurchasingPODetail, PurchasingPOSend, PurchasingReceipt, PurchasingReceiveRequest, PurchasingVendorContactResult, PurchasingWorklistItem, Result } from "@hardscapehouse/api-client";
import type { AreaDesk, DeskHost } from "../purch/area.js";
import { houseDay, refusalWords } from "../po/po-logic.js";
import { answerLost } from "../purch/purch-logic.js";
import { ReceivingApi } from "./receiving-api.js";
import { attachBody, attachConfirmWords, costsHidden, documentFileName, fallbackAsk, receiveBody, receiveConfirmWords, sendConfirmWords, sendOutcome, worklistRows, type ReceivedPick } from "./receiving-logic.js";
import { renderReceiving, type ConfirmKind, type ErrorAt } from "./receiving-views.js";

type Failure = Extract<Result<unknown>, { ok: false }>;

/** Hands a downloaded text to the browser as a file, the way a link with a download name would. */
function saveText(name: string, text: string, mime: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: mime }));
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  URL.revokeObjectURL(url);
}

export class ReceivingDesk implements AreaDesk {
  readonly #api: ReceivingApi;
  readonly #host: DeskHost;

  #phase: "loading" | "ready" | "error" = "loading";
  #worklist: readonly PurchasingWorklistItem[] = [];
  #branches: readonly Branch[] = [];
  #po: PurchasingPODetail | null = null;
  #hidden = false;
  #sends: readonly PurchasingPOSend[] = [];
  #receipt: PurchasingReceipt | null = null;
  #attachCurrency = "CAD";
  #attachBasis = "VALUE";
  #confirm: { kind: ConfirmKind; words: string } | null = null;
  #pendingReceive: { body: PurchasingReceiveRequest; picked: ReceivedPick[] } | null = null;
  #pendingAttach: PurchasingAttachLandedCostRequest | null = null;
  #sendKey: string | null = null;
  #sendContact: PurchasingVendorContactResult | null = null;
  #sendResult: { tone: "good" | "watch" | "stop" | "plain"; words: string } | null = null;
  #busy = false;
  #error = "";
  #errorAt: ErrorAt = "order";
  #notice = "";
  // Fields the screen holds are cleared once after a receive or a landed cost has gone through.
  #clearRecv = false;
  #clearAttach = false;

  constructor(api: ReceivingApi, host: DeskHost) {
    this.#api = api;
    this.#host = host;
    api.watchZone(() => host.repaint());
  }

  // ---- loads ----------------------------------------------------------------------------------

  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const [branches, worklist] = await Promise.all([this.#api.branches(), this.#api.worklist()]);
    if (!worklist.ok) {
      this.#phase = "error";
      this.#error = refusalWords("load", worklist.error, worklist.status);
      this.#host.repaint();
      return;
    }
    if (branches.ok) this.#branches = branches.value;
    this.#worklist = worklistRows(worklist.value);
    this.#phase = "ready";
    this.#host.repaint();
  }

  async #reloadWorklist(): Promise<void> {
    const worklist = await this.#api.worklist();
    if (worklist.ok) this.#worklist = worklistRows(worklist.value);
    else this.#fail(worklist, "order");
  }

  /** The order, its cost payload and its sends, read together; the screen shows what the core holds. */
  async #readOrder(id: string): Promise<boolean> {
    const [po, costs, sends] = await Promise.all([this.#api.order(id), this.#api.costs(id), this.#api.sends(id)]);
    if (!po.ok) {
      this.#fail(po, "order");
      return false;
    }
    this.#po = po.value;
    this.#hidden = costs.ok ? costsHidden(costs.value) : false;
    this.#sends = sends.ok ? (sends.value.items ?? []) : [];
    return true;
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
      case "open-order":
        void this.#openOrder(control.getAttribute("data-po-id") ?? "");
        return true;
      case "close-order":
        this.#po = null;
        this.#receipt = null;
        this.#resetAttempt();
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "open-receipt":
        void this.#openReceipt(control.getAttribute("data-receipt-id") ?? "");
        return true;
      case "close-receipt":
        this.#receipt = null;
        this.#confirm = null;
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "attach-currency":
        this.#attachCurrency = choice === "USD" ? "USD" : "CAD";
        this.#host.repaint();
        return true;
      case "attach-basis":
        this.#attachBasis = choice;
        this.#host.repaint();
        return true;
      case "ask-receive":
        this.#askReceive();
        return true;
      case "do-receive":
        void this.#doReceive();
        return true;
      case "ask-attach":
        this.#askAttach();
        return true;
      case "do-attach":
        void this.#doAttach();
        return true;
      case "ask-send":
        void this.#askSend();
        return true;
      case "do-send":
        void this.#doSend(false);
        return true;
      case "do-send-fallback":
        void this.#doSend(true);
        return true;
      case "download-csv":
        void this.#download("csv");
        return true;
      case "download-html":
        void this.#download("html");
        return true;
      case "confirm-cancel":
        this.#confirm = null;
        this.#pendingReceive = null;
        this.#pendingAttach = null;
        this.#host.repaint();
        return true;
      default:
        return false;
    }
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
    this.#sendResult = null;
  }

  #fail(result: Failure, at: ErrorAt): void {
    this.#notice = "";
    this.#error = refusalWords("receive", result.error, result.status);
    this.#errorAt = at;
  }

  /** Leaving an order (or finishing with it) ends the attempts that belonged to it. */
  #resetAttempt(): void {
    this.#confirm = null;
    this.#pendingReceive = null;
    this.#pendingAttach = null;
    this.#sendKey = null;
    this.#sendContact = null;
  }

  async #refresh(): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    await this.#reloadWorklist();
    if (this.#po !== null) await this.#readOrder(this.#po.id);
    this.#busy = false;
    this.#host.repaint();
  }

  async #openOrder(id: string): Promise<void> {
    if (id === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#resetAttempt();
    this.#receipt = null;
    this.#clearRecv = true;
    this.#host.repaint();
    await this.#readOrder(id);
    this.#busy = false;
    this.#host.repaint();
  }

  async #openReceipt(id: string): Promise<void> {
    if (id === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#confirm = null;
    this.#host.repaint();
    const rc = await this.#api.receipt(id);
    if (rc.ok) {
      this.#receipt = rc.value;
      this.#clearAttach = true;
    } else this.#fail(rc, "receipt");
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- receiving ------------------------------------------------------------------------------

  #askReceive(): void {
    const po = this.#po;
    if (po === null || this.#busy) return;
    const counted: Record<string, string> = {};
    for (const line of po.lines) counted[line.id] = this.#host.field(`recv-${line.id}`);
    const checked = receiveBody(counted, this.#host.field("recv-notes"), po);
    this.#clearMessages();
    if (!checked.ok) {
      this.#error = checked.words;
      this.#errorAt = "order";
      this.#host.repaint();
      return;
    }
    this.#pendingReceive = { body: checked.body, picked: checked.picked };
    this.#confirm = { kind: "receive", words: receiveConfirmWords(po, checked.picked) };
    this.#host.repaint();
  }

  async #doReceive(): Promise<void> {
    const po = this.#po;
    const pending = this.#pendingReceive;
    if (po === null || pending === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const done = await this.#api.receive(po.id, pending.body);
    this.#confirm = null;
    this.#pendingReceive = null;
    if (done.ok) {
      await this.#reloadWorklist();
      await this.#readOrder(po.id);
      this.#clearRecv = true;
      const now = this.#po;
      this.#notice = `Received against ${po.po_no}: receipt ${done.value.grn_no}.\nThe order reads ${now === null ? "updated" : now.status.toLowerCase()} now, and the goods are in stock.`;
    } else if (answerLost(done)) {
      // No receive key exists, so a second press could move the same goods twice: read the order and say what it holds.
      await this.#readOrder(po.id);
      this.#clearRecv = true;
      this.#error = "No clear answer came back, so it is not known whether the goods were received. The order has been read again: check the Received column and the receipts before counting anything in a second time.";
      this.#errorAt = "order";
    } else {
      this.#fail(done, "order");
    }
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- a landed cost on a receipt -------------------------------------------------------------

  #askAttach(): void {
    const rc = this.#receipt;
    if (rc === null || this.#busy) return;
    const checked = attachBody({
      freight: this.#host.field("at-freight"),
      duty: this.#host.field("at-duty"),
      handling: this.#host.field("at-handling"),
      currency: this.#attachCurrency,
      fx: this.#host.field("at-fx"),
      basis: this.#attachBasis,
    });
    this.#clearMessages();
    if (!checked.ok) {
      this.#error = checked.words;
      this.#errorAt = "receipt";
      this.#host.repaint();
      return;
    }
    this.#pendingAttach = checked.body;
    this.#confirm = { kind: "attach", words: attachConfirmWords(rc.grn_no, checked.body) };
    this.#host.repaint();
  }

  async #doAttach(): Promise<void> {
    const rc = this.#receipt;
    const body = this.#pendingAttach;
    if (rc === null || body === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const done = await this.#api.attachLandedCost(rc.id, body);
    this.#confirm = null;
    this.#pendingAttach = null;
    if (done.ok) {
      this.#receipt = done.value;
      if (this.#po !== null) await this.#readOrder(this.#po.id);
      this.#notice = `Landed cost applied to receipt ${rc.grn_no}.\nIt raised the cost of those goods and is in Landed cost.`;
    } else {
      // A refusal, or an answer that never came: either way read the receipt, the core holds the truth.
      const again = await this.#api.receipt(rc.id);
      if (again.ok) this.#receipt = again.value;
      if (answerLost(done)) {
        // The core applies the landed cost before it marks the receipt, so a bare receipt proves nothing:
        // never advise applying again; send the person to the landed costs, which carry the receipt's number.
        this.#error = again.ok
          ? `No clear answer came back, so it is not known whether the landed cost was applied. The receipt has been read again. Before applying anything, check Landed cost for one on ${rc.grn_no}: a second would double the cost of these goods.`
          : "No clear answer came back and the receipt could not be read back. Refresh, and check Landed cost for one on this receipt, before doing anything else.";
        this.#errorAt = "receipt";
      } else this.#fail(done, "receipt");
    }
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- the document and the send --------------------------------------------------------------

  async #download(kind: "csv" | "html"): Promise<void> {
    const po = this.#po;
    if (po === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const doc = await this.#api.document(po.id, kind);
    if (doc.ok) saveText(documentFileName(po.po_no, kind), doc.value, kind === "csv" ? "text/csv" : "text/html");
    else this.#fail(doc, "send");
    this.#busy = false;
    this.#host.repaint();
  }

  async #askSend(): Promise<void> {
    const po = this.#po;
    if (po === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const contact = await this.#api.contact(po.vendor_id);
    this.#sendContact = contact.ok ? contact.value : null;
    // One key for the attempt: every retry of this send carries it, so the core makes one send of them.
    if (this.#sendKey === null) this.#sendKey = crypto.randomUUID();
    this.#confirm = { kind: "send", words: sendConfirmWords(po, this.#sendContact) };
    this.#busy = false;
    this.#host.repaint();
  }

  async #doSend(fallback: boolean): Promise<void> {
    const po = this.#po;
    const key = this.#sendKey;
    if (po === null || key === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const done = await this.#api.send(po.id, key, fallback);
    if (done.ok) {
      const out = sendOutcome(done.value);
      this.#sendResult = out;
      const sends = await this.#api.sends(po.id);
      if (sends.ok) this.#sends = sends.value.items ?? [];
      this.#confirm = null;
      this.#sendKey = null;
    } else if (answerLost(done)) {
      // The key stays: pressing Send again is the same attempt, which the core answers once.
      this.#confirm = null;
      this.#error = "No clear answer came back, so it is not known whether the order went. Press Send again: it is the same attempt, so it cannot go twice.";
      this.#errorAt = "send";
    } else if (!fallback && done.status === 400 && fallbackAsk(done.error)) {
      this.#confirm = {
        kind: "send-fallback",
        words: `Nobody is tagged for ordering at ${po.vendor_name}. The vendor record has one address. Send ${po.po_no} to it? Tag an ordering contact in Vendors to avoid this question next time.`,
      };
    } else {
      this.#confirm = null;
      this.#sendKey = null;
      this.#fail(done, "send");
    }
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- the template ---------------------------------------------------------------------------

  /** After a render: the fields the screen holds are emptied once after a receive or a landed cost went through. */
  seed(set: (name: string, value: string) => void): void {
    if (this.#clearRecv && this.#po !== null) {
      for (const line of this.#po.lines) set(`recv-${line.id}`, "");
      set("recv-notes", "");
      this.#clearRecv = false;
    }
    if (this.#clearAttach && this.#receipt !== null) {
      for (const name of ["at-freight", "at-duty", "at-handling", "at-fx"]) set(name, "");
      this.#clearAttach = false;
    }
  }

  render(): TemplateResult {
    return renderReceiving({
      phase: this.#phase,
      worklist: this.#worklist,
      branchNames: new Map(this.#branches.map((b) => [b.id, b.name])),
      po: this.#po,
      hidden: this.#hidden,
      sends: this.#sends,
      receipt: this.#receipt,
      attachCurrency: this.#attachCurrency,
      attachBasis: this.#attachBasis,
      confirm: this.#confirm,
      busy: this.#busy,
      error: this.#phase === "error" || this.#error !== "" ? this.#error : "",
      errorAt: this.#errorAt,
      notice: this.#notice,
      sendResult: this.#sendResult,
      today: houseDay(),
    });
  }
}
