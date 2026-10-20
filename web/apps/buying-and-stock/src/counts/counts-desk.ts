// The counts area's controller: the state the screens draw from, the loads,
// and what each press does. The server holds every gate: the freeze, the
// maker's submission, the checker's approval and the post are the core's to
// refuse, and a refusal reads in words naming who may do the move.
import type { TemplateResult } from "lit";
import type { DeskHost } from "../purch/area.js";
import type { InvCountLine, InvCountNegativeStockRow, InvCountReasonCode, InvCountSheet, Result } from "@hardscapehouse/api-client";
import { CountsApi } from "./counts-api.js";
import { countsFromTyped, varianceNeedsReason } from "./counts-logic.js";
import { refusalWords, seatFor } from "../inventory/inventory-logic.js";
import { houseDay } from "../po/po-logic.js";
import { renderCounts, type CountsModel, type CountsTab } from "./counts-views.js";

type Field = (name: string, key?: string) => string;

const TABS: readonly CountsTab[] = ["sheets", "recon"];

export class CountsDesk {
  readonly #api: CountsApi;
  readonly #field: Field;
  readonly #repaint: () => void;

  #phase: CountsModel["phase"] = "loading";
  #tab: CountsTab = "sheets";
  #busy = false;
  #error = "";
  #notice = "";
  #sheetsLoaded = false;
  #sheets: readonly InvCountSheet[] = [];
  #branchNames: ReadonlyMap<string, string> = new Map();
  // The open sheet.
  #detail: CountsModel["detail"] = null;
  #reasons: readonly InvCountReasonCode[] = [];
  #blind = true;
  #counts = new Map<string, string>();
  #lineReasons = new Map<string, string>();
  #confirmingPost = false;
  #confirmingCancel = false;
  // The recon.
  #reconBranch = "";
  #recon: CountsModel["recon"] = null;
  #negatives: readonly InvCountNegativeStockRow[] = [];
  #reconBusy = false;

  // The signed in role's code decides the seat: it picks the words a walled move is named with.
  readonly #role: () => string;

  constructor(api: CountsApi, host: DeskHost) {
    this.#api = api;
    this.#repaint = host.repaint;
    this.#field = host.field;
    this.#role = host.role;
  }

  /** The action key the refusal words answer; the seat names the wall for the rest. */
  #words(action: string): string {
    if (action === "count-approve") return action;
    if (seatFor(this.#role()) === "read") return "write";
    if (seatFor(this.#role()) === "work") return "manage";
    return action;
  }

  /** The first load, when the area is opened: the branch names, then the sheets. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#repaint();
    const [branches, sheets] = await Promise.all([this.#api.branches(), this.#api.sheets()]);
    if (!branches.ok || !sheets.ok) {
      this.#phase = "ready";
      this.#fail("load", (!branches.ok ? branches : sheets) as Extract<Result<unknown>, { ok: false }>);
      this.#repaint();
      return;
    }
    this.#branchNames = new Map(branches.value.map((b) => [b.id, b.name]));
    this.#sheets = sheets.value.items ?? [];
    this.#sheetsLoaded = true;
    this.#phase = "ready";
    this.#repaint();
  }

  async #open(tab: CountsTab): Promise<void> {
    this.#tab = tab;
    this.#clearMessages();
    if (tab === "sheets" && !this.#sheetsLoaded) await this.#readSheets();
    this.#repaint();
  }

  async #readSheets(): Promise<void> {
    const sheets = await this.#api.sheets();
    if (sheets.ok) {
      this.#sheets = sheets.value.items ?? [];
      this.#sheetsLoaded = true;
    } else {
      this.#fail("load", sheets);
    }
  }

  #fail(action: string, result: Extract<Result<unknown>, { ok: false }>): void {
    this.#notice = "";
    this.#error = refusalWords(this.#words(action), result.error, result.status);
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
  }

  /** Acts on a control carrying data-action. Returns true when the press was this slice's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const attr = (name: string): string => control.getAttribute(name) ?? "";
    switch (action) {
      case "counts-reload":
        void this.load();
        return true;
      case "toggle-blind":
        this.#blind = (control as HTMLInputElement).checked;
        this.#repaint();
        return true;
      case void 0:
        return false;
      case "counts-tab": {
        const tab = attr("data-counts-tab") as CountsTab;
        if (!TABS.includes(tab)) return true;
        void this.#open(tab);
        return true;
      }
      case "sheets-reload":
        void this.#readSheets().then(() => this.#repaint());
        return true;
      case "new-sheet":
        void this.#create();
        return true;
      case "open-sheet":
        void this.#openSheet(attr("data-sheet-id"));
        return true;
      case "close-sheet":
        this.#detail = null;
        this.#confirmingPost = false;
        this.#confirmingCancel = false;
        this.#counts = new Map();
        this.#lineReasons = new Map();
        this.#blind = true;
        this.#clearMessages();
        this.#repaint();
        return true;
      case "freeze-sheet":
        void this.#freeze();
        return true;
      case "submit-counts":
        void this.#submit();
        return true;
      case "approve-sheet":
        void this.#approve();
        return true;
      case "post-sheet":
        this.#confirmingPost = true;
        this.#clearMessages();
        this.#repaint();
        return true;
      case "post-cancel":
        this.#confirmingPost = false;
        this.#repaint();
        return true;
      case "post-confirm":
        void this.#post();
        return true;
      case "cancel-sheet":
        this.#confirmingCancel = true;
        this.#clearMessages();
        this.#repaint();
        return true;
      case "cancel-back":
        this.#confirmingCancel = false;
        this.#repaint();
        return true;
      case "cancel-confirm":
        void this.#cancelSheet();
        return true;
      case "recon-run":
        void this.#reconRun();
        return true;
      default:
        return false;
    }
  }

  async #create(): Promise<void> {
    if (this.#busy) return;
    const branch = this.#field("sheet-branch");
    if (branch === "") {
      this.#error = "Pick the branch the count is for.";
      this.#repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const made = await this.#api.createSheet({ branch_id: branch, scope: this.#field("sheet-scope") || "FULL", notes: this.#field("sheet-notes") });
    if (!made.ok) {
      this.#fail("sheet-create", made);
      this.#busy = false;
      this.#repaint();
      return;
    }
    await this.#readSheets();
    this.#busy = false;
    await this.#openSheet(made.value.id);
  }

  async #openSheet(id: string): Promise<void> {
    if (id === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const [detail, reasons] = await Promise.all([this.#api.sheet(id), this.#api.reasonCodes()]);
    if (!detail.ok) {
      this.#fail("load", detail);
      this.#busy = false;
      this.#repaint();
      return;
    }
    this.#detail = detail.value;
    this.#reasons = reasons.ok ? (reasons.value.items ?? []) : [];
    // A line already counted seeds its field, so an edit starts from the maker's figure.
    this.#counts = new Map((detail.value.lines ?? []).filter((l) => l.counted_qty_base !== null).map((l) => [l.id, String(l.counted_qty_base)]));
    this.#lineReasons = new Map((detail.value.lines ?? []).filter((l) => l.reason_code !== undefined && l.reason_code !== "").map((l) => [l.id, l.reason_code as string]));
    this.#blind = true;
    this.#confirmingPost = false;
    this.#confirmingCancel = false;
    this.#busy = false;
    this.#repaint();
  }

  async #freeze(): Promise<void> {
    const detail = this.#detail;
    if (detail === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const frozen = await this.#api.freeze(detail.sheet.id);
    if (!frozen.ok) {
      this.#fail("sheet-freeze", frozen);
      this.#busy = false;
      this.#repaint();
      return;
    }
    this.#busy = false;
    await this.#openSheet(detail.sheet.id);
  }

  /** The maker's submission: the typed fields are read, the checks run, the server decides the rest. */
  async #submit(): Promise<void> {
    const detail = this.#detail;
    if (detail === null || this.#busy) return;
    const lines: { product_id: string; location_id: string; counted_qty_base: number; reason_code: string }[] = [];
    for (const line of detail.lines ?? []) {
      const typed = this.#field("counted", line.id);
      if (typed === "") continue;
      const check = countsFromTyped(typed);
      if (!check.ok) {
        this.#error = check.words;
        this.#repaint();
        return;
      }
      const variance = check.qty - line.snapshot_qty_base;
      const reason = this.#field("reason", line.id) || this.#lineReasons.get(line.id) || "";
      if (varianceNeedsReason({ counted_qty_base: check.qty, snapshot_qty_base: line.snapshot_qty_base }) && reason === "") {
        this.#error = "Every variance needs a reason code.";
        this.#repaint();
        return;
      }
      lines.push({ product_id: line.product_id, location_id: line.location_id, counted_qty_base: check.qty, reason_code: variance === 0 ? "" : reason });
    }
    if (lines.length === 0) {
      this.#error = "Enter at least one counted quantity before submitting.";
      this.#repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const counted = await this.#api.submitCounts(detail.sheet.id, lines);
    if (!counted.ok) {
      this.#fail("sheet-counts", counted);
      this.#busy = false;
      this.#repaint();
      return;
    }
    this.#busy = false;
    await this.#openSheet(detail.sheet.id);
    this.#notice = "The count is in. A checker who is not the maker approves it before anything posts.";
    this.#repaint();
  }

  async #approve(): Promise<void> {
    const detail = this.#detail;
    if (detail === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const approved = await this.#api.approve(detail.sheet.id);
    if (!approved.ok) {
      this.#fail("count-approve", approved);
      this.#busy = false;
      this.#repaint();
      return;
    }
    this.#busy = false;
    await this.#openSheet(detail.sheet.id);
    this.#notice = "Approved. The sheet can post now that a checker who is not the maker has signed it.";
    this.#repaint();
  }

  async #post(): Promise<void> {
    const detail = this.#detail;
    if (detail === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const posted = await this.#api.post(detail.sheet.id);
    if (!posted.ok) {
      this.#fail("count-approve", posted);
      this.#confirmingPost = false;
      this.#busy = false;
      this.#repaint();
      return;
    }
    this.#detail = posted.value;
    this.#confirmingPost = false;
    this.#notice = "Posted. The variances moved the stock and the reason-coded journal is written; posting again never doubles it.";
    this.#busy = false;
    await this.#readSheets();
    this.#repaint();
  }

  async #cancelSheet(): Promise<void> {
    const detail = this.#detail;
    if (detail === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const cancelled = await this.#api.cancel(detail.sheet.id);
    this.#confirmingCancel = false;
    if (!cancelled.ok) {
      this.#fail("sheet-cancel", cancelled);
      this.#busy = false;
      this.#repaint();
      return;
    }
    this.#notice = `Sheet ${detail.sheet.sheet_no} cancelled. Nothing was posted.`;
    this.#busy = false;
    await this.#readSheets();
    await this.#openSheet(detail.sheet.id);
    this.#repaint();
  }

  async #reconRun(): Promise<void> {
    const branch = this.#field("recon-branch");
    if (branch === "" || this.#busy) return;
    this.#reconBranch = branch;
    this.#reconBusy = true;
    this.#clearMessages();
    this.#repaint();
    const [recon, negatives] = await Promise.all([this.#api.recon(branch), this.#api.negativeStock()]);
    if (!recon.ok) {
      this.#fail("recon", recon);
    } else {
      this.#recon = { current: recon.value.current, history: recon.value.history?.items ?? [] };
    }
    if (negatives.ok) this.#negatives = negatives.value.items ?? [];
    this.#reconBusy = false;
    this.#repaint();
  }

  /** A counted figure as typed, kept so a re-render keeps the row's select and variance. */
  typed(name: string, value: string, id?: string): void {
    if (name !== "counted" || id === undefined || id === "") return;
    this.#counts = new Map(this.#counts).set(id, value);
    this.#seededLine = null;
    this.#repaint();
  }

  /** The fields already given a starting value, so a re-render does not fight the reader. */
  #seededLine: string | null = null;

  seed(set: (name: string, value: string, key?: string) => void): void {
    const detail = this.#detail;
    if (detail === null) return;
    const stamp = `${detail.sheet.id}:${[...(this.#counts.entries())].map(([k, v]) => `${k}=${v}`).join(",")}`;
    if (this.#seededLine === stamp) return;
    for (const [id, value] of this.#counts) set("counted", value, id);
    for (const [id, value] of this.#lineReasons) set("reason", value, id);
    this.#seededLine = stamp;
  }

  render(): TemplateResult {
    const model: CountsModel = {
      phase: this.#phase,
      tab: this.#tab,
      busy: this.#busy,
      error: this.#error,
      notice: this.#notice,
      branchNames: this.#branchNames,
      today: houseDay(),
      sheets: this.#sheets,
      detail: this.#detail,
      reasons: this.#reasons,
      blind: this.#blind,
      counts: this.#counts,
      lineReasons: this.#lineReasons,
      confirmingPost: this.#confirmingPost,
      confirmingCancel: this.#confirmingCancel,
      reconBranch: this.#reconBranch,
      recon: this.#recon,
      negatives: this.#negatives,
      reconBusy: this.#reconBusy,
    };
    return renderCounts(model);
  }
}
