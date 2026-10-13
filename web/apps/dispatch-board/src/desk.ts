// The dispatch desk's controller: the state the screens draw from, the loads, and what each press
// does. It is not an element; the root element owns it, hands it a way to repaint and a way to find
// the form fields, and asks it for the template.
//
// Every write is held to the server: a load is on a truck only when the core says so, a run is
// dispatched only when the core answers, and the screen then shows what the core now says (the
// board it reads again), never what the app expected. A refusal reads in the core's own words, a
// walled move in words naming who may do it (dispatch-logic.ts). Nothing is stored.
import type { TemplateResult } from "lit";
import type {
  DispatchBoard,
  DispatchCalendarDay,
  DispatchDriver,
  DispatchGapPage,
  DispatchLoadBoardView,
  DispatchLoadStop,
  DispatchStop,
  DispatchStopPaymentHold,
  DispatchTruck,
  Result,
} from "@hardscapehouse/api-client";
import { DispatchApi } from "./dispatch-api.js";
import {
  STOP_LABEL,
  assignTargets,
  bulkLoadOutcome,
  loadableStops,
  moveAnnouncement,
  plainSentence,
  pruneSelection,
  refusalWords,
  reopenNotice,
  reopenRunNotice,
  shiftISODate,
  validateReason,
  validateReschedule,
  type CardId,
} from "./dispatch-logic.js";
import { renderDesk, type Axis, type BoardModel, type Sheet, type Span, type Tab } from "./views.js";

export interface DeskHost {
  /** Asks the root to repaint. */
  readonly repaint: () => void;
  /** A control or field the screen carries, found by a selector in the root's own shadow root. */
  readonly find: (selector: string) => Element | null;
  /** Every control or field that matches. */
  readonly findAll: (selector: string) => Element[];
  /** The day the board opens on; the root reads the clock, tests pin it. */
  readonly today: () => string;
}

type Failure = Extract<Result<unknown>, { ok: false }>;

// A write that does nothing: the board is read again and the messages are cleared.
const NOOP = async (): Promise<Failure | null> => null;

export class DispatchDesk {
  readonly #api: DispatchApi;
  readonly #host: DeskHost;

  #phase: BoardModel["phase"] = "loading";
  #tab: Tab = "load";
  #date: string;
  #today: string;
  #branches: readonly { id: string; name: string }[] = [];
  #branchId = "";
  #busy = false;
  #error = "";
  #info = "";
  #notice = "";
  #refusals: readonly string[] = [];
  #view: DispatchLoadBoardView | null = null;
  #card: CardId | "" = "";
  #axis: Axis = "TRUCK";
  #selected: ReadonlySet<string> = new Set();
  #gaps: DispatchGapPage | null = null;
  #calendar: readonly DispatchCalendarDay[] = [];
  #day: DispatchBoard | null = null;
  #trucks: readonly DispatchTruck[] = [];
  #drivers: readonly DispatchDriver[] = [];
  #holds: ReadonlyMap<string, DispatchStopPaymentHold> = new Map();
  #sheet: Sheet | null = null;
  #loading = false;
  #span: Span = "DAY";
  #runError: { id: string; words: string } | null = null;
  // The sheet whose starting field values the screen has already been given.
  #sheetSeeded: string | null = null;

  constructor(api: DispatchApi, host: DeskHost) {
    this.#api = api;
    this.#host = host;
    this.#today = host.today();
    this.#date = this.#today;
  }

  // ---- loads ------------------------------------------------------------------------------------

  /** The first read of the desk: the caller's yard, then the board for the day. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const branches = await this.#api.branches();
    if (!branches.ok) return this.#loadFailed("The board", branches);
    this.#branches = branches.value.filter((b) => b.is_active).map((b) => ({ id: b.id, name: b.name }));
    const first = this.#branches[0];
    if (first === undefined) {
      this.#phase = "error";
      this.#error = "No yard is open to this login, so there is no board to read.";
      this.#host.repaint();
      return;
    }
    if (!this.#branches.some((b) => b.id === this.#branchId)) this.#branchId = first.id;
    const ok = await this.#readBoard();
    if (!ok) return;
    this.#phase = "ready";
    this.#host.repaint();
  }

  #loadFailed(what: string, result: Failure): void {
    this.#phase = "error";
    this.#error = refusalWords(what, result.error, result.status ?? null);
    this.#host.repaint();
  }

  /** The board for the tab and the day, the crew lists, and the holds. False when the board itself would not read. */
  async #readBoard(): Promise<boolean> {
    const day = this.#date;
    const branch = this.#branchId;
    if (this.#tab === "load") {
      const [view, trucks, drivers, gaps, calendar] = await Promise.all([
        this.#api.loadBoard(branch, day),
        this.#api.trucks(branch),
        this.#api.drivers(branch),
        this.#api.gaps(branch, day),
        this.#api.calendar(branch, day, shiftISODate(day, 6)),
      ]);
      if (!view.ok) {
        this.#loadFailed("The load board", view);
        return false;
      }
      this.#view = view.value;
      this.#trucks = trucks.ok ? (trucks.value.items ?? []) : [];
      this.#drivers = drivers.ok ? (drivers.value.items ?? []) : [];
      // The gaps and the week are the board's company: a refusal leaves them out, and says so.
      this.#gaps = gaps.ok ? gaps.value : null;
      this.#calendar = calendar.ok ? (calendar.value.items ?? []) : [];
      this.#holds = await this.#readHolds(view.value.loads.map((r) => r.run.id));
      this.#selected = new Set(pruneSelection([...this.#selected], [...view.value.staging, ...view.value.loads.flatMap((r) => r.stops)]));
    } else {
      const [board, trucks, drivers] = await Promise.all([this.#api.dayBoard(branch, day), this.#api.trucks(branch), this.#api.drivers(branch)]);
      if (!board.ok) {
        this.#loadFailed("The day board", board);
        return false;
      }
      this.#day = board.value;
      this.#trucks = trucks.ok ? (trucks.value.items ?? []) : [];
      this.#drivers = drivers.ok ? (drivers.value.items ?? []) : [];
      this.#holds = await this.#readHolds(board.value.runs.map((r) => r.id));
    }
    return true;
  }

  /** The payment holds of the runs on the board, one read each. A refusal leaves the hold words out; the core still refuses the load. */
  async #readHolds(runIds: readonly string[]): Promise<ReadonlyMap<string, DispatchStopPaymentHold>> {
    const reads = await Promise.all(runIds.map((id) => this.#api.paymentHolds(id)));
    const next = new Map<string, DispatchStopPaymentHold>();
    for (const read of reads) {
      if (!read.ok) continue;
      for (const hold of read.value.items ?? []) if (hold.held) next.set(hold.stop_id, hold);
    }
    return next;
  }

  /** The board after a write: quietly, keeping the messages the write just set. */
  async #reload(): Promise<void> {
    await this.#readBoard();
    this.#sheet = null;
    this.#sheetSeeded = null;
  }

  // ---- the model --------------------------------------------------------------------------------

  model(): BoardModel {
    const branch = this.#branches.find((b) => b.id === this.#branchId);
    return {
      phase: this.#phase,
      tab: this.#tab,
      date: this.#date,
      today: this.#today,
      branchName: branch?.name ?? "",
      branches: this.#branches,
      branchId: this.#branchId,
      busy: this.#busy,
      error: this.#error,
      info: this.#info,
      notice: this.#notice,
      refusals: this.#refusals,
      view: this.#view,
      card: this.#card,
      axis: this.#axis,
      selected: this.#selected,
      gaps: this.#gaps,
      calendar: this.#calendar,
      day: this.#day,
      trucks: this.#trucks,
      drivers: this.#drivers,
      holds: this.#holds,
      sheet: this.#sheet,
      loading: this.#loading,
      span: this.#span,
      runError: this.#runError,
    };
  }

  render(): TemplateResult {
    return renderDesk(this.model());
  }

  /**
   * After a render: the date field and the selects that name a current value are set to it (a bound
   * value is not allowed in a template), and a freshly opened sheet gets its starting values once.
   */
  seed(): void {
    for (const el of this.#host.findAll("[data-current]")) {
      const current = el.getAttribute("data-current") ?? "";
      if (el instanceof HTMLInputElement) {
        if (el.value !== current) el.value = current;
      } else if (el instanceof HTMLSelectElement) {
        const index = [...el.options].findIndex((o) => (o.getAttribute("data-id") ?? "") === current);
        el.selectedIndex = index < 0 ? 0 : index;
      }
    }
    const sheet = this.#sheet;
    if (sheet === null) return;
    const key = `${sheet.kind}:${sheet.id}`;
    if (this.#sheetSeeded === key) return;
    this.#sheetSeeded = key;
    const to = this.#host.find("[data-sheet] [data-field=\"to_date\"]");
    if (to instanceof HTMLInputElement) to.value = shiftISODate(this.#today, 1);
  }

  // ---- the press --------------------------------------------------------------------------------

  /** Acts on a control carrying data-action. Returns true when the press was this desk's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const attr = (name: string): string => control.getAttribute(name) ?? "";
    switch (action) {
      case "reload":
        void this.load();
        return true;
      case "refresh":
        void this.#run(NOOP, "", "That", true);
        return true;
      case "tab": {
        const next: Tab = attr("data-tab-id") === "day" ? "day" : "load";
        if (next === this.#tab) return true;
        this.#tab = next;
        this.#sheet = null;
        this.#clearMessages();
        void this.#run(NOOP, "", "That", true);
        return true;
      }
      case "day-prev":
        this.#goto(shiftISODate(this.#date, -1));
        return true;
      case "day-next":
        this.#goto(shiftISODate(this.#date, 1));
        return true;
      case "day-today":
        this.#goto(this.#today);
        return true;
      case "goto-day":
        this.#goto(attr("data-day"));
        return true;
      case "card": {
        const id = attr("data-card") as CardId;
        this.#card = this.#card === id ? "" : id;
        this.#host.repaint();
        return true;
      }
      case "card-clear":
        this.#card = "";
        this.#host.repaint();
        return true;
      case "span":
        this.#span = attr("data-span") === "WEEK" ? "WEEK" : "DAY";
        this.#host.repaint();
        return true;
      case "axis":
        this.#axis = attr("data-axis") === "STATUS" ? "STATUS" : "TRUCK";
        this.#host.repaint();
        return true;
      case "select": {
        const id = attr("data-stop-id");
        const next = new Set(this.#selected);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        this.#selected = next;
        this.#host.repaint();
        return true;
      }
      case "select-clear":
        this.#selected = new Set();
        this.#host.repaint();
        return true;
      case "mark-loaded":
        void this.#markLoaded();
        return true;
      case "auto-assign":
        void this.#autoAssign();
        return true;
      case "new-run":
        this.#openSheet("new-run", "new");
        return true;
      case "open-sheet": {
        const kind = attr("data-sheet-kind") as Sheet["kind"];
        this.#openSheet(kind, attr("data-stop-id") || attr("data-run-id"));
        return true;
      }
      case "sheet-cancel":
        this.#sheet = null;
        this.#sheetSeeded = null;
        this.#host.repaint();
        return true;
      case "sheet-submit":
        void this.#submitSheet();
        return true;
      case "commit":
        void this.#commit(attr("data-stop-id"));
        return true;
      case "assign-off":
        void this.#assignOff(attr("data-stop-id"));
        return true;
      case "stop-status":
        void this.#advance(attr("data-stop-id"), attr("data-status-to") as DispatchStop["status"]);
        return true;
      case "dispatch-run":
        void this.#runAction(attr("data-run-id"), "dispatch");
        return true;
      case "cancel-run":
        void this.#runAction(attr("data-run-id"), "cancel");
        return true;
      case "complete-run":
        void this.#runAction(attr("data-run-id"), "complete");
        return true;
      case "clear-reopen":
        void this.#runAction(attr("data-run-id"), "clear-reopen");
        return true;
      default:
        return false;
    }
  }

  /** A select changed. Returns true when it was this desk's. */
  change(target: Element): boolean {
    const action = target.getAttribute("data-action");
    const field = target.getAttribute("data-field");
    if (target instanceof HTMLSelectElement && action === "assign-pick") {
      const runId = target.selectedOptions[0]?.getAttribute("data-run-id") ?? "";
      const stopId = target.getAttribute("data-stop-id") ?? "";
      target.selectedIndex = 0;
      if (runId !== "") void this.#assign(stopId, runId);
      return true;
    }
    if (target instanceof HTMLSelectElement && action === "crew-pick") {
      void this.#crew(target.getAttribute("data-run-id") ?? "");
      return true;
    }
    if (target instanceof HTMLSelectElement && field === "branch") {
      const id = target.selectedOptions[0]?.getAttribute("data-id") ?? "";
      if (id !== "" && id !== this.#branchId) {
        this.#branchId = id;
        this.#selected = new Set();
        void this.#run(NOOP, "", "That", true);
      }
      return true;
    }
    if (target instanceof HTMLInputElement && field === "date") {
      this.#goto(target.value);
      return true;
    }
    return false;
  }

  // ---- the moves --------------------------------------------------------------------------------

  #clearMessages(): void {
    this.#error = "";
    this.#info = "";
    this.#notice = "";
    this.#refusals = [];
    this.#runError = null;
  }

  #goto(day: string): void {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(day) || day === this.#date) {
      this.#host.repaint();
      return;
    }
    this.#date = day;
    this.#sheet = null;
    this.#card = "";
    this.#clearMessages();
    void this.#run(NOOP, "", "That", true);
  }

  /**
   * One write, then the board again. A refusal is shown in the core's own words and the board is
   * read again too, because a 409 usually means the world moved under the screen.
   */
  async #run(write: () => Promise<Failure | null>, okNotice: string, what = "That", reading = false): Promise<boolean> {
    if (this.#busy) return false;
    this.#busy = true;
    this.#loading = reading;
    this.#clearMessages();
    this.#host.repaint();
    let refused: Failure | null = null;
    try {
      refused = await write();
    } finally {
      if (refused === null) this.#notice = okNotice;
      else this.#error = refusalWords(what, refused.error, refused.status ?? null);
      await this.#reload();
      this.#busy = false;
      this.#loading = false;
      this.#host.repaint();
    }
    return refused === null;
  }

  async #assign(stopId: string, runId: string): Promise<void> {
    const view = this.#view;
    const stop = view === null ? undefined : [...view.staging, ...view.loads.flatMap((r) => r.stops)].find((s) => s.id === stopId);
    const day = this.#day?.unassigned.find((s) => s.id === stopId);
    const target = view !== null && stop !== undefined ? assignTargets(view, stop).find((t) => t.runId === runId) : undefined;
    const label = stop?.order_no ?? day?.order_no ?? "The load";
    await this.#run(
      async () => {
        const r = await this.#api.assignStop(stopId, runId);
        return r.ok ? null : r;
      },
      stop !== undefined && target !== undefined ? moveAnnouncement(stop, target) : `${label} put on the run.`,
      "Putting it on the truck",
    );
  }

  async #assignOff(stopId: string): Promise<void> {
    const view = this.#view;
    const stop = view === null ? undefined : view.loads.flatMap((r) => r.stops).find((s) => s.id === stopId);
    await this.#run(
      async () => {
        const r = await this.#api.assignStop(stopId, "");
        return r.ok ? null : r;
      },
      stop === undefined ? "Taken off its truck." : moveAnnouncement(stop, null),
      "Taking it off the truck",
    );
  }

  async #commit(stopId: string): Promise<void> {
    const stop = this.#view?.staging.find((s) => s.id === stopId);
    await this.#run(
      async () => {
        const r = await this.#api.commitStop(stopId);
        return r.ok ? null : r;
      },
      `${stop?.order_no ?? "The load"} committed for ${stop?.scheduled_date ?? "its day"}. It is a promise now.`,
      "Committing the date",
    );
  }

  async #advance(stopId: string, to: DispatchStop["status"], reason = ""): Promise<void> {
    const all: DispatchStop[] = [...(this.#view?.staging ?? []), ...(this.#view?.loads.flatMap((r) => r.stops) ?? []), ...(this.#day?.stops ?? []), ...(this.#day?.unassigned ?? [])];
    const stop = all.find((s) => s.id === stopId);
    await this.#run(
      async () => {
        const r = await this.#api.advanceStop(stopId, to, reason);
        return r.ok ? null : r;
      },
      `${stop?.order_no ?? "The stop"} is now ${(STOP_LABEL[to] ?? to).toLowerCase()}.`,
      "That move",
    );
  }

  async #runAction(runId: string, act: "dispatch" | "cancel" | "complete" | "clear-reopen"): Promise<void> {
    const runs = [...(this.#view?.loads.map((r) => r.run) ?? []), ...(this.#day?.runs ?? [])];
    const run = runs.find((r) => r.id === runId);
    const no = run?.run_no ?? "The truck";
    const call = {
      dispatch: async () => (await this.#api.dispatchRun(runId)) as Result<unknown>,
      cancel: async () => (await this.#api.cancelRun(runId)) as Result<unknown>,
      complete: async () => (await this.#api.completeRun(runId)) as Result<unknown>,
      "clear-reopen": async () => (await this.#api.clearRunReopen(runId)) as Result<unknown>,
    }[act];
    const words = {
      dispatch: `${no} dispatched. Every ready load on it is loaded.`,
      cancel: `${no} cancelled. Its loads are back in the staging lane.`,
      complete: `${no} completed.`,
      "clear-reopen": `The reopen marker is off ${no}.`,
    }[act];
    const what = { dispatch: "Dispatching it", cancel: "Cancelling it", complete: "Completing it", "clear-reopen": "Clearing the marker" }[act];
    const ok = await this.#run(
      async () => {
        const r = await call();
        return r.ok ? null : r;
      },
      words,
      what,
    );
    // A refusal belongs to the truck it was pressed on: it shows in that lane, in the core's words.
    if (!ok && this.#error !== "") {
      this.#runError = { id: runId, words: `${no} did not go: ${this.#error}` };
      this.#error = "";
      this.#host.repaint();
    }
  }

  async #crew(runId: string): Promise<void> {
    const pick = (field: string): string => {
      const el = this.#host.find(`select[data-field="${field}"][data-run-id="${runId}"]`);
      return el instanceof HTMLSelectElement ? (el.selectedOptions[0]?.getAttribute("data-id") ?? "") : "";
    };
    await this.#run(
      async () => {
        const r = await this.#api.crew(runId, pick("crew-truck"), pick("crew-driver"));
        return r.ok ? null : r;
      },
      "Crew set.",
      "Setting the crew",
    );
  }

  async #markLoaded(): Promise<void> {
    const view = this.#view;
    if (view === null || this.#busy) return;
    const all: DispatchLoadStop[] = [...view.staging, ...view.loads.flatMap((r) => r.stops)];
    const ids = loadableStops(all, this.#selected).map((s) => s.id);
    if (ids.length === 0) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const result = await this.#api.markLoaded(ids);
    if (result.ok) {
      const outcome = bulkLoadOutcome(result.value.items ?? []);
      this.#notice = outcome.notice;
      this.#refusals = outcome.refusals;
      // Selection that took is done; what refused stays ticked so it can be looked at.
      const refused = new Set((result.value.items ?? []).filter((o) => !o.loaded).map((o) => o.stop_id));
      this.#selected = new Set([...this.#selected].filter((id) => refused.has(id)));
    } else {
      this.#error = refusalWords("Loading", result.error, result.status ?? null);
    }
    await this.#reload();
    this.#busy = false;
    this.#host.repaint();
  }

  /** The auto assign seam: the core build says what would answer it, and the board shows that sentence as information. */
  async #autoAssign(): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const result = await this.#api.autoAssign();
    if (result.ok) this.#notice = "Loads assigned.";
    else if (result.status === 501) this.#info = `Auto assign: ${plainSentence(result.error)}.`;
    else this.#error = refusalWords("Auto assign", result.error, result.status ?? null);
    await this.#reload();
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- the sheets -------------------------------------------------------------------------------

  #openSheet(kind: Sheet["kind"], id: string): void {
    if (id === "") return;
    this.#sheet = { kind, id, error: "" };
    this.#sheetSeeded = null;
    this.#clearMessages();
    this.#host.repaint();
  }

  #fieldValue(name: string): string {
    const el = this.#host.find(`[data-sheet] [data-field="${name}"]`);
    if (el instanceof HTMLInputElement) return el.value;
    if (el instanceof HTMLSelectElement) return el.selectedOptions[0]?.getAttribute("data-id") ?? "";
    return "";
  }

  #sheetRefused(words: string): void {
    if (this.#sheet !== null) this.#sheet = { ...this.#sheet, error: words };
    this.#host.repaint();
  }

  async #submitSheet(): Promise<void> {
    const sheet = this.#sheet;
    if (sheet === null || this.#busy) return;
    const reason = this.#fieldValue("reason");
    switch (sheet.kind) {
      case "new-run": {
        await this.#run(
          async () => {
            const r = await this.#api.openRun(this.#branchId, this.#date, this.#fieldValue("truck"), this.#fieldValue("driver"), reason.trim());
            return r.ok ? null : r;
          },
          "Truck opened. Put loads on it from the staging lane.",
          "Opening the truck",
        );
        return;
      }
      case "reschedule-stop":
      case "reschedule-run": {
        const to = this.#fieldValue("to_date");
        const current = this.#currentDateOf(sheet);
        const words = validateReschedule(to, reason, current, this.#today);
        if (words !== "") return this.#sheetRefused(words);
        const ok = await this.#run(
          async () => {
            const r = sheet.kind === "reschedule-stop" ? await this.#api.rescheduleStop(sheet.id, to, reason.trim()) : await this.#api.rescheduleRun(sheet.id, to, reason.trim());
            return r.ok ? null : r;
          },
          `Moved to ${to}. The customer's date changed with it.`,
          "Moving it",
        );
        if (!ok) this.#restoreSheet(sheet);
        return;
      }
      case "reopen-stop":
      case "reopen-run": {
        const words = validateReason(reason);
        if (words !== "") return this.#sheetRefused(words);
        const stop = this.#view?.loads.flatMap((r) => r.stops).find((s) => s.id === sheet.id) ?? this.#view?.staging.find((s) => s.id === sheet.id);
        const row = this.#view?.loads.find((r) => r.run.id === sheet.id);
        const ok = await this.#run(
          async () => {
            const r = sheet.kind === "reopen-stop" ? await this.#api.reopenStop(sheet.id, reason.trim()) : await this.#api.reopenRun(sheet.id, reason.trim());
            return r.ok ? null : r;
          },
          sheet.kind === "reopen-stop" ? reopenNotice(stop ?? { order_no: undefined, run_id: undefined }) : row !== undefined ? reopenRunNotice(row) : "Reopened.",
          "Reopening it",
        );
        if (!ok) this.#restoreSheet(sheet);
        return;
      }
      case "cancel-run": {
        await this.#runAction(sheet.id, "cancel");
        return;
      }
      case "fail-stop": {
        if (reason.trim() === "") return this.#sheetRefused("Say what stopped this delivery. The yard reads it.");
        await this.#advance(sheet.id, "FAILED", reason.trim());
        return;
      }
      default:
        return;
    }
  }

  /** After a refused submit the sheet stays open with the core's words beside its own button. */
  #restoreSheet(sheet: Sheet): void {
    this.#sheet = { ...sheet, error: this.#error };
    this.#sheetSeeded = `${sheet.kind}:${sheet.id}`;
    this.#error = "";
    this.#host.repaint();
  }

  #currentDateOf(sheet: Sheet): string {
    if (sheet.kind === "reschedule-run") {
      return this.#view?.loads.find((r) => r.run.id === sheet.id)?.run.scheduled_date ?? this.#day?.runs.find((r) => r.id === sheet.id)?.scheduled_date ?? "";
    }
    const all: DispatchStop[] = [...(this.#view?.staging ?? []), ...(this.#view?.loads.flatMap((r) => r.stops) ?? []), ...(this.#day?.stops ?? []), ...(this.#day?.unassigned ?? [])];
    return all.find((s) => s.id === sheet.id)?.scheduled_date ?? "";
  }
}
