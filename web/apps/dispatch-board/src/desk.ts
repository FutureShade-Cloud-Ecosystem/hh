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
  DispatchFreightRaiseView,
  DispatchMintedPickup,
  DispatchPolicy,
  DispatchPolicyChange,
  DispatchTruck,
  DispatchWillCall,
  Result,
} from "@hardscapehouse/api-client";
import { DispatchApi } from "./dispatch-api.js";
import {
  STOP_LABEL,
  assignTargets,
  bulkLoadOutcome,
  driverBody,
  historyFaultWords,
  manifestNotice,
  splitBody,
  splitNotice,
  splitRefusal,
  manifestRefusal,
  policyBody,
  policyRefusal,
  loadableStops,
  moveAnnouncement,
  plainSentence,
  pruneSelection,
  refusalWords,
  reopenNotice,
  reopenRunNotice,
  scheduleBody,
  scheduleNotice,
  scheduleRefusal,
  shiftISODate,
  validateReason,
  validateReschedule,
  validateSchedule,
  truckBody,
  validateDriver,
  validateOrderRef,
  validateSplit,
  validatePolicyFields,
  validateTruck,
  willCallNotice,
  willCallRefusal,
  type DriverFields,
  type PolicyFields,
  type SplitFields,
  type ScheduleFields,
  type TruckFields,
  type CardId,
} from "./dispatch-logic.js";
import { BLOCKED_WINDOW_WORDS, browserPrintDeps, openPrintWindow, printHtmlInto } from "./print-window.js";
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
  #scheduleError = "";
  // Will call: the order as typed, its codes, whether a lookup has run, and the code just minted (held in memory, shown once).
  // The scan trail open on a card: its read, in progress, done or refused.
  #trail: BoardModel["trail"] = null;
  // A parked freight ask: held until the person closes it, read again on request.
  #raise: DispatchFreightRaiseView | null = null;
  #policy: DispatchPolicy | null = null;
  #history: readonly DispatchPolicyChange[] = [];
  #historyFault = "";
  #wcOrder = "";
  #wcItems: readonly DispatchWillCall[] = [];
  #wcLooked = false;
  #wcError = "";
  #wcMinted: { code: DispatchMintedPickup; order: string } | null = null;
  // The day the schedule form's date field was last given its starting value for.
  #formSeeded: string | null = null;
  // The sheet whose starting field values the screen has already been given.
  #sheetSeeded: string | null = null;
  // The run currently opened in the run-detail drawer. Empty when the drawer is closed.
  #selectedRunId = "";
  // The list of "fix" lines the dispatcher has applied to the current view, kept in memory only.
  #appliedFixes: ReadonlySet<string> = new Set();
  // The suggest-runs panel is open on one load. Empty when the panel is closed.
  #suggestStopId = "";

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
    } else if (this.#tab === "settings") {
      const [policy, history] = await Promise.all([this.#api.policy(branch), this.#api.policyHistory(branch)]);
      if (!policy.ok) {
        this.#loadFailed("The dispatch rules", policy);
        return false;
      }
      this.#policy = policy.value;
      // The trail is the rules' company: a refusal leaves it out and says why.
      this.#history = history.ok ? (history.value.items ?? []) : [];
      this.#historyFault = history.ok ? "" : historyFaultWords(history.error, history.status ?? null);
    } else if (this.#tab === "willcall") {
      // Nothing on this tab is a board: a code list is read for the order that was named.
      if (this.#wcOrder !== "") await this.#readWillCalls();
    } else if (this.#tab === "fleet") {
      // The registry lists what is out of use too, so a retired truck can be brought back.
      const [trucks, drivers] = await Promise.all([this.#api.trucks(branch, true), this.#api.drivers(branch, true)]);
      if (!trucks.ok) {
        this.#loadFailed("The trucks", trucks);
        return false;
      }
      if (!drivers.ok) {
        this.#loadFailed("The drivers", drivers);
        return false;
      }
      this.#trucks = trucks.value.items ?? [];
      this.#drivers = drivers.value.items ?? [];
    } else if (this.#tab === "schedule") {
      // The schedule reads the day's books and the week ahead; it crews nothing, so it reads no crew lists.
      const [board, calendar] = await Promise.all([this.#api.dayBoard(branch, day), this.#api.calendar(branch, day, shiftISODate(day, 6))]);
      if (!board.ok) {
        this.#loadFailed("The day's books", board);
        return false;
      }
      this.#day = board.value;
      this.#calendar = calendar.ok ? (calendar.value.items ?? []) : [];
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
    this.#trail = null;
    this.#sheetSeeded = null;
    // Applied fixes ride only one view: a fresh board is honest about its own state.
    this.#appliedFixes = new Set();
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
      scheduleError: this.#scheduleError,
      trail: this.#trail,
      raise: this.#raise,
      policy: this.#policy,
      history: this.#history,
      historyFault: this.#historyFault,
      willCall: { order: this.#wcOrder, items: this.#wcItems, looked: this.#wcLooked, error: this.#wcError, minted: this.#wcMinted },
      runError: this.#runError,
      selectedRunId: this.#selectedRunId,
      appliedFixes: this.#appliedFixes,
      suggestStopId: this.#suggestStopId,
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
    if (this.#tab === "schedule") {
      const dateField = this.#host.find("[data-schedule] [data-field=\"day\"]");
      const seedKey = `${this.#branchId}:${this.#date}`;
      if (dateField instanceof HTMLInputElement && this.#formSeeded !== seedKey) {
        this.#formSeeded = seedKey;
        dateField.value = this.#date < this.#today ? this.#today : this.#date;
      }
    } else {
      this.#formSeeded = null;
    }
    const sheet = this.#sheet;
    if (sheet === null) return;
    const key = `${sheet.kind}:${sheet.id}`;
    if (this.#sheetSeeded === key) return;
    this.#sheetSeeded = key;
    // A fresh sheet takes what the person had typed (after a refusal) or the row it edits (data-initial).
    const saved = sheet.values ?? {};
    for (const el of this.#host.findAll("[data-sheet] [data-field]")) {
      const name = el.getAttribute("data-field") ?? "";
      const want = saved[name] ?? el.getAttribute("data-initial");
      if (want === null || want === undefined) continue;
      if (el instanceof HTMLInputElement) el.value = want;
      else if (el instanceof HTMLSelectElement) {
        const index = [...el.options].findIndex((o) => (o.getAttribute("data-id") ?? "") === want);
        if (index >= 0) el.selectedIndex = index;
      }
    }
    const to = this.#host.find("[data-sheet] [data-field=\"to_date\"]");
    if (to instanceof HTMLInputElement && saved.to_date === undefined) to.value = shiftISODate(this.#today, 1);
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
        const id = attr("data-tab-id");
        const next: Tab = id === "day" ? "day" : id === "schedule" ? "schedule" : id === "fleet" ? "fleet" : id === "willcall" ? "willcall" : id === "settings" ? "settings" : "load";
        if (next === this.#tab) return true;
        this.#tab = next;
        this.#sheet = null;
        // Leaving the tab forgets the order and the code: the box is empty when it comes back.
        this.#wcMinted = null;
        this.#wcOrder = "";
        this.#wcItems = [];
        this.#wcLooked = false;
        this.#wcError = "";
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
      case "select-run": {
        const id = attr("data-run-id");
        this.#selectedRunId = this.#selectedRunId === id ? "" : id;
        this.#host.repaint();
        return true;
      }
      case "run-close":
        this.#selectedRunId = "";
        this.#host.repaint();
        return true;
      case "apply-fix": {
        const key = `${attr("data-fix-runno")}:${attr("data-fix-kind")}`;
        const next = new Set(this.#appliedFixes);
        if (next.has(key)) next.delete(key);
        else next.add(key);
        this.#appliedFixes = next;
        this.#host.repaint();
        return true;
      }
      case "suggest-open": {
        const id = attr("data-stop-id");
        this.#suggestStopId = this.#suggestStopId === id ? "" : id;
        this.#host.repaint();
        return true;
      }
      case "suggest-close":
        this.#suggestStopId = "";
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
      case "schedule-submit":
        void this.#schedule();
        return true;
      case "raise-check":
        void this.#raiseCheck();
        return true;
      case "raise-close":
        this.#raise = null;
        this.#host.repaint();
        return true;
      case "print-manifest":
        // The window is opened inside the press, while the click still counts as the person's own act.
        this.#printManifest(attr("data-run-id"), openPrintWindow());
        return true;
      case "wc-lookup":
        void this.#wcLookup();
        return true;
      case "wc-mint":
        void this.#wcMint();
        return true;
      case "wc-print":
        window.print();
        return true;
      case "wc-dismiss":
        this.#wcMinted = null;
        this.#host.repaint();
        return true;
      case "fleet-toggle":
        void this.#fleetToggle(attr("data-fleet-kind"), attr("data-fleet-id"), attr("data-active-to") === "true");
        return true;
      case "new-run":
        this.#openSheet("new-run", "new");
        return true;
      case "open-sheet": {
        const kind = attr("data-sheet-kind") as Sheet["kind"];
        if (kind === "scan-trail") {
          void this.#openTrail(attr("data-stop-id"));
          return true;
        }
        this.#openSheet(kind, attr("data-stop-id") || attr("data-run-id") || attr("data-fleet-id") || attr("data-code-id"));
        return true;
      }
      case "sheet-cancel":
        this.#sheet = null;
        this.#trail = null;
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

  /** Enter in the will call order box looks the order up. (A change event would race the button the person is reaching for.) */
  key(target: Element, key: string): boolean {
    if (key === "Enter" && target.getAttribute("data-field") === "wc-order") {
      void this.#wcLookup();
      return true;
    }
    return false;
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
    this.#scheduleError = "";
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
  async #run(write: () => Promise<Failure | null>, okNotice: string, what = "That", reading = false, refusal?: (f: Failure) => string): Promise<boolean> {
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
      else this.#error = refusal !== undefined ? refusal(refused) : refusalWords(what, refused.error, refused.status ?? null);
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

  // ---- the manifest and the scan trail ---------------------------------------------------------

  /**
   * The manifest: the core's document is read, the print is recorded, and only then does the window
   * take it. A window the browser blocked, a manifest the core refused and a print the queue could
   * not record each stop the flow with a sentence, and no window is left open behind them.
   */
  async #printManifest(runId: string, win: ReturnType<typeof openPrintWindow>): Promise<void> {
    if (this.#busy) {
      (win as { close?: () => void } | null)?.close?.();
      return;
    }
    const runs = [...(this.#view?.loads.map((r) => r.run) ?? []), ...(this.#day?.runs ?? [])];
    const no = runs.find((r) => r.id === runId)?.run_no ?? "The run";
    this.#clearMessages();
    if (win === null) {
      this.#error = BLOCKED_WINDOW_WORDS;
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#host.repaint();
    const fail = (words: string): void => {
      win.close?.();
      this.#error = words;
    };
    const doc = await this.#api.runManifest(runId);
    if (!doc.ok) fail(manifestRefusal(doc.error, doc.status ?? null));
    else {
      const recorded = await this.#api.recordManifestPrint(runId);
      if (!recorded.ok) fail(manifestRefusal(recorded.error, recorded.status ?? null));
      else {
        const outcome = printHtmlInto(win, doc.value, browserPrintDeps);
        if (outcome.ok) this.#notice = manifestNotice(no);
        else this.#error = outcome.message;
      }
    }
    this.#busy = false;
    this.#host.repaint();
  }

  #stopById(id: string): DispatchStop | undefined {
    const all: DispatchStop[] = [...(this.#view?.staging ?? []), ...(this.#view?.loads.flatMap((r) => r.stops) ?? []), ...(this.#day?.stops ?? []), ...(this.#day?.unassigned ?? [])];
    return all.find((s) => s.id === id);
  }

  /** Reads a parked ask again. A refusal is the core's word, shown as the board's error; the card stays as it was. */
  async #raiseCheck(): Promise<void> {
    const raise = this.#raise;
    if (raise === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const r = await this.#api.freightRaise(raise.id);
    if (r.ok) this.#raise = r.value;
    else this.#error = refusalWords("Reading the ask", r.error, r.status ?? null);
    this.#busy = false;
    this.#host.repaint();
  }

  /** Opens a card's scan trail: the sheet shows at once and fills when the core answers. */
  async #openTrail(stopId: string): Promise<void> {
    const all: DispatchStop[] = [...(this.#view?.staging ?? []), ...(this.#view?.loads.flatMap((r) => r.stops) ?? []), ...(this.#day?.stops ?? []), ...(this.#day?.unassigned ?? [])];
    const stop = all.find((s) => s.id === stopId);
    if (stop === undefined) return;
    this.#clearMessages();
    this.#sheet = { kind: "scan-trail", id: stopId, error: "" };
    this.#sheetSeeded = null;
    this.#trail = { stopId, orderNo: stop.order_no ?? "the order", state: "loading" };
    this.#host.repaint();
    const result = await this.#api.scanTrail(stop.order_id);
    // The sheet may have been closed, or another card's opened, while the core was answering.
    if (this.#trail === null || this.#trail.stopId !== stopId) return;
    this.#trail = result.ok
      ? { stopId, orderNo: this.#trail.orderNo, state: "ready", value: result.value }
      : { stopId, orderNo: this.#trail.orderNo, state: "error", words: refusalWords("The scan trail", result.error, result.status ?? null) };
    this.#host.repaint();
  }

  // ---- will call ---------------------------------------------------------------------------------

  #wcTyped(): string {
    const el = this.#host.find("[data-willcall] [data-field=\"wc-order\"]");
    return el instanceof HTMLInputElement ? el.value.trim() : this.#wcOrder;
  }

  /** The codes for the order named. A refusal leaves the list as it was and says why beside the box. */
  async #readWillCalls(): Promise<boolean> {
    const result = await this.#api.willCalls(this.#wcOrder);
    if (result.ok) {
      this.#wcItems = result.value.items ?? [];
      this.#wcLooked = true;
      this.#wcError = "";
      return true;
    }
    this.#wcItems = [];
    this.#wcLooked = false;
    this.#wcError = willCallRefusal(result.error, result.status ?? null);
    return false;
  }

  async #wcLookup(): Promise<void> {
    if (this.#busy) return;
    const order = this.#wcTyped();
    this.#clearMessages();
    this.#wcError = "";
    const words = validateOrderRef(order);
    if (words !== "") {
      this.#wcError = words;
      this.#host.repaint();
      return;
    }
    // A different order is a different tag: the code on screen is not its.
    if (this.#wcMinted !== null && this.#wcMinted.order !== order) this.#wcMinted = null;
    this.#wcOrder = order;
    this.#busy = true;
    this.#host.repaint();
    await this.#readWillCalls();
    this.#busy = false;
    this.#host.repaint();
  }

  async #wcMint(): Promise<void> {
    if (this.#busy) return;
    const order = this.#wcTyped();
    this.#clearMessages();
    this.#wcError = "";
    const words = validateOrderRef(order);
    if (words !== "") {
      this.#wcError = words;
      this.#host.repaint();
      return;
    }
    this.#wcOrder = order;
    this.#busy = true;
    this.#wcMinted = null;
    this.#host.repaint();
    const result = await this.#api.mintWillCall(order);
    if (result.ok) {
      this.#wcMinted = { code: result.value, order };
      this.#notice = willCallNotice({ order_no: order, expires_at: result.value.expires_at });
      await this.#readWillCalls();
    } else {
      this.#wcError = willCallRefusal(result.error, result.status ?? null);
    }
    this.#busy = false;
    this.#host.repaint();
  }

  /** Takes a truck or a driver out of use, or puts them back: a rewrite of the row with the one flag changed. */
  async #fleetToggle(kind: string, id: string, active: boolean): Promise<void> {
    const truck = kind === "truck" ? this.#trucks.find((t) => t.id === id) : undefined;
    const driver = kind === "driver" ? this.#drivers.find((d) => d.id === id) : undefined;
    if (truck === undefined && driver === undefined) return;
    const name = truck?.code ?? driver?.name ?? "It";
    await this.#run(
      async () => {
        const r = truck !== undefined ? await this.#api.updateTruck(truck.id, { ...truck, is_active: active }) : await this.#api.updateDriver(id, { ...(driver as DispatchDriver), is_active: active });
        return r.ok ? null : r;
      },
      active ? `${name} is back in use.` : `${name} is out of use. It leaves the pick lists and stays on the books.`,
      "That change",
    );
  }

  /** Schedules the order in the form. A refusal stays beside the button and the form keeps what was typed. */
  async #schedule(): Promise<void> {
    if (this.#busy) return;
    const read = (name: string): string => {
      const el = this.#host.find(`[data-schedule] [data-field="${name}"]`);
      return el instanceof HTMLInputElement ? el.value : "";
    };
    const fields: ScheduleFields = {
      order: read("order"), date: read("day"), window: read("window"), contact: read("contact"), phone: read("phone"),
      address: read("address"), city: read("city"), postal: read("postal"), lat: read("lat"), lon: read("lon"), instructions: read("instructions"),
    };
    this.#clearMessages();
    const words = validateSchedule(fields, this.#today);
    if (words !== "") {
      this.#scheduleError = words;
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.scheduleStop(scheduleBody(fields));
    if (result.ok) {
      this.#notice = scheduleNotice(result.value);
      // The board follows the order to its day, and the form is ready for the next one.
      this.#date = result.value.scheduled_date;
      this.#sheet = null;
      for (const el of this.#host.findAll("[data-schedule] [data-field]")) {
        if (el instanceof HTMLInputElement && el.getAttribute("data-field") !== "day") el.value = "";
      }
    } else {
      this.#scheduleError = scheduleRefusal(result.error, result.status ?? null);
    }
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

  /** Everything typed in the open sheet, so a refusal can put it back. */
  #typed(): Record<string, string> {
    const out: Record<string, string> = {};
    for (const el of this.#host.findAll("[data-sheet] [data-field]")) {
      const name = el.getAttribute("data-field") ?? "";
      if (el instanceof HTMLInputElement) out[name] = el.value;
      else if (el instanceof HTMLSelectElement) out[name] = el.selectedOptions[0]?.getAttribute("data-id") ?? "";
    }
    return out;
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
    const typed = this.#typed();
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
        if (!ok) this.#restoreSheet(sheet, typed);
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
        if (!ok) this.#restoreSheet(sheet, typed);
        return;
      }
      case "cancel-run": {
        await this.#runAction(sheet.id, "cancel");
        return;
      }
      case "edit-policy": {
        const base = this.#policy;
        if (base === null) return;
        const f: PolicyFields = { prompt: typed.prompt ?? "", reopen: typed.reopen ?? "", visibility: typed.visibility ?? "", horizon: typed.horizon ?? "", view: typed.view ?? "" };
        const words = validatePolicyFields(f);
        if (words !== "") return this.#sheetRefused(words);
        const body = policyBody(f, base, this.#branchId);
        const ok = await this.#run(
          async () => {
            const r = await this.#api.savePolicy(body);
            return r.ok ? null : r;
          },
          "Saved. The board reads the new rules on its next read.",
          "Saving the rules",
          false,
          (failure) => policyRefusal(failure.error, failure.status ?? null),
        );
        if (!ok) this.#restoreSheet(sheet, typed);
        return;
      }
      case "split-freight": {
        const stop = this.#stopById(sheet.id);
        if (stop === undefined) return;
        const f: SplitFields = { loads: typed.loads ?? "", freight: typed.freight ?? "", amounts: typed.amounts ?? "" };
        const words = validateSplit(f);
        if (words !== "") return this.#sheetRefused(words);
        const body = splitBody(f, stop.order_id);
        let notice = "";
        const ok = await this.#run(
          async () => {
            const r = await this.#api.splitFreight(body);
            if (!r.ok) return r;
            notice = splitNotice(r.value);
            this.#raise = r.value.parked ?? null;
            return null;
          },
          "",
          "The split",
          false,
          (failure) => splitRefusal(failure.error, failure.status ?? null),
        );
        if (ok) {
          this.#notice = notice;
          this.#host.repaint();
        } else this.#restoreSheet(sheet, typed);
        return;
      }
      case "revoke-willcall": {
        const ok = await this.#run(
          async () => {
            const r = await this.#api.revokeWillCall(sheet.id);
            return r.ok ? null : r;
          },
          "The pickup code is cancelled. The tag already printed no longer works.",
          "Cancelling the code",
        );
        // The code just minted is gone with it; a refusal keeps the sheet beside its button.
        if (ok) this.#wcMinted = null;
        else this.#restoreSheet(sheet);
        return;
      }
      case "add-truck":
      case "edit-truck": {
        const f: TruckFields = { code: typed.code ?? "", name: typed.name ?? "", type: typed.type ?? "", capacity: typed.capacity ?? "", volume: typed.volume ?? "", pallets: typed.pallets ?? "" };
        const words = validateTruck(f);
        if (words !== "") return this.#sheetRefused(words);
        const base = sheet.kind === "edit-truck" ? this.#trucks.find((t) => t.id === sheet.id) : undefined;
        const body = truckBody(f, base, this.#branchId);
        const ok = await this.#run(
          async () => {
            const r = base === undefined ? await this.#api.createTruck(body) : await this.#api.updateTruck(base.id, body);
            return r.ok ? null : r;
          },
          base === undefined ? `${body.code} added to the trucks.` : `${body.code} saved.`,
          "Saving the truck",
        );
        if (!ok) this.#restoreSheet(sheet, typed);
        return;
      }
      case "add-driver":
      case "edit-driver": {
        const f: DriverFields = { name: typed.name ?? "", phone: typed.phone ?? "", licence: typed.licence ?? "", login: typed.login ?? "" };
        const words = validateDriver(f);
        if (words !== "") return this.#sheetRefused(words);
        const base = sheet.kind === "edit-driver" ? this.#drivers.find((d) => d.id === sheet.id) : undefined;
        const body = driverBody(f, base, this.#branchId);
        const ok = await this.#run(
          async () => {
            const r = base === undefined ? await this.#api.createDriver(body) : await this.#api.updateDriver(base.id, body);
            return r.ok ? null : r;
          },
          base === undefined ? `${body.name} added to the drivers.` : `${body.name} saved.`,
          "Saving the driver",
        );
        if (!ok) this.#restoreSheet(sheet, typed);
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
  #restoreSheet(sheet: Sheet, values?: Readonly<Record<string, string>>): void {
    this.#sheet = { ...sheet, error: this.#error, ...(values !== undefined ? { values } : {}) };
    this.#sheetSeeded = null;
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
