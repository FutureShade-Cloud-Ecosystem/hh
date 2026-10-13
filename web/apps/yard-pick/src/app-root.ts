// Yard Pick's root element: the chrome and the way in. The way in is the
// demo's role cards (the shared kit's entry, the demo adapter behind the auth
// package's AccountSurface seam). After the mint the branch resolves once
// (the branches this login may reach; the demo yard team holds Trenton), the
// ports are wired with the caller's headers, and the board renders.
//
// The organisation header is the demo org by construction: the staff header
// provider refuses to send a request without X-Org, so no call ever leaves
// this app unresolved.
//
// The header is fed through plain attributes (signed-in, demo, compact) and,
// for the role label and theme, assignments in updated(): the design
// system's position gate refuses every "." and "@" binding and every bound
// value outside the data- and aria- attributes in a dependent package. Its
// two buttons ride data-action names and the host's click listener below
// acts on them.
//
// THE FIELD MIRROR. No control carries a bound value (the gate refuses it),
// so every input, select and textarea writes what the yard types into the
// mirrors below on input and change, and updated() writes each mirror back
// to its field after a render. A re-render never eats a typed number, and a
// field the yard has not touched keeps the value the model gave it.
import { html, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
// hos-app-header: the shared chrome element (decision log row 938).
import "@hardscapehouse/design-system/src/header.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { bootCustody, entryUnavailable, servedConsoleConfig, type AppStart } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { YardApi, type YardPick, type YardRecordLine } from "./yard-api.js";
import {
  RESOLVE_WALL_NAMES,
  actionDone,
  lineIsShort,
  pickSummary,
  qtyText,
  recordBlockedReason,
  shortOf,
  shortPickWords,
  validateStage,
  skidBlockedReason,
  skidWords,
  shortageSentWords,
  validateShortage,
  yardError,
} from "./yard-logic.js";
import { yardStyles } from "./styles.js";
import { renderEntry, renderPick, renderQueue, type EntryView } from "./views.js";

const DEMO_ORG = "hh-demo";

/** One drafted line: what the yard typed, mirrored from the field. */
interface Draft {
  qty: string;
  reason: string;
  note: string;
}

export class YardPickApp extends HHElement {
  static styles = [baseStyles, yardStyles];

  // The custody: holds the adopted demo mint in memory, nothing persisted.
  readonly #sessions = new SessionStore();

  // The demo client: no bearer token (demo routes are pre-auth by design).
  readonly #demoClient = new ApiClient({
    baseUrl: "",
    headers: async () => ({ "X-Org": DEMO_ORG }),
  });

  // The demo account surface: listFlows() and chooseFlow() over the core's
  // demo routes. It is the injected AccountSurface for the auth package.
  readonly #demoAccount: AccountSurface = new DemoAccount({ client: this.#demoClient });

  // The custody the served config chose: in demo mode the auth controller over the demo account, as
  // always; in console mode this page recovers its own session on load (boot-custody.ts).
  readonly #custody = bootCustody(servedConsoleConfig(), { sessions: this.#sessions, demoAccount: this.#demoAccount, demoOrg: DEMO_ORG });

  // The auth controller: owns sign-in, sign-out, and adoption.
  readonly #auth: AuthController = this.#custody.controller;

  // The staff client: carries the mint's bearer after adoption.
  readonly #staffClient = new ApiClient({
    baseUrl: this.#custody.coreBaseUrl,
    headers: authHeaderProvider({ callerClass: "staff", sessions: this.#sessions, activeOrg: () => this.#custody.org }),
  });

  // The yard's calls, over the staff client.
  readonly #yard = new YardApi(this.#staffClient);

  // The board's state. The branch is the one this login reaches (the demo
  // yard team holds Trenton); the queue names it on every read because a
  // branch-scoped caller must.
  #phase: "loading" | "ready" | "error" = "loading";
  #picks: YardPick[] = [];
  #branchId = "";
  #branchName = "";
  #error = "";
  #notice = "";
  #busy = false;
  #moreBehind = false;
  #nextCursor = "";

  // The open pick.
  #view: { kind: "queue" } | { kind: "pick"; id: string } = { kind: "queue" };
  #pick: YardPick | null = null;
  #drafts = new Map<number, Draft>();
  #editing: number | null = null;
  #sheetFor: number | null = null;
  #resolveFor = "";
  #skids: number | null = null;

  // The field mirrors: what the yard typed, keyed by field name (and line).
  #qtyMirror = new Map<number, string>();
  #reasonMirror = new Map<number, string>();
  #noteMirror = new Map<number, string>();
  #sheetQty = "";
  #sheetReason = "";
  #sheetNote = "";
  #skidReason = "";
  #skidNote = "";
  #stageWhere = "";
  #resolveText = "";

  // The way in: this app's own card, drawn by views.ts.
  #entryView: EntryView = { flows: { kind: "loading" }, busy: false, message: null };

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

  // The theme: light by default (the yard reads this in daylight), brand
  // (the dark half) one tap away. Held in memory only: nothing here persists.
  #theme: "light" | "dark" = "light";

  // The caller's own id, so the board can say "Claimed by you".
  #me = "";

  // The entry cards and every control ride data-action names; one host
  // listener reads them, as the till's does.
  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    const action = control?.getAttribute("data-action");
    if (action === "switch-role" || action === "sign-out") {
      this.#onSignOut();
      return;
    }
    if (action === "theme") {
      this.#theme = this.#theme === "light" ? "dark" : "light";
      this.requestUpdate();
      return;
    }
    if (action === "reload" || action === "more") {
      void this.#loadQueue(action !== "more");
      return;
    }
    if (action === "choose") {
      const flow = control?.getAttribute("data-flow") ?? "";
      if (flow !== "") void this.#chooseFlow(flow);
      return;
    }
    if (action === "back") {
      this.#view = { kind: "queue" };
      this.#pick = null;
      this.#error = "";
      this.#notice = "";
      // Back re-reads the whole board: a pick that moved while it was open
      // must not ride beside its own stale row.
      void this.#loadQueue(true);
      return;
    }
    if (action === "claim") {
      void this.#claim(control?.getAttribute("data-task-id") ?? "");
      return;
    }
    if (action === "open-pick") {
      void this.#openPick(control?.getAttribute("data-task-id") ?? "");
      return;
    }
    if (action === "start") {
      void this.#start(control?.getAttribute("data-task-id") ?? "");
      return;
    }
    if (action === "edit-line") {
      this.#openEditor(Number(control?.getAttribute("data-line-no") ?? "0"));
      return;
    }
    if (action === "qty-step") {
      this.#stepQty(Number(control?.getAttribute("data-line-no") ?? "0"), Number(control?.getAttribute("data-step") ?? "1"));
      return;
    }
    if (action === "qty-full" || action === "qty-none") {
      const lineNo = Number(control?.getAttribute("data-line-no") ?? "0");
      const line = this.#ticketLine(lineNo);
      if (line !== null) this.#setQty(lineNo, action === "qty-full" ? qtyText(line.qty_ordered) : "0");
      return;
    }
    if (action === "qty-done") {
      this.#editing = null;
      this.requestUpdate();
      return;
    }
    if (action === "pick-reason") {
      const scope = control?.getAttribute("data-scope") ?? "";
      const reason = control?.getAttribute("data-reason") ?? "";
      if (scope === "line") {
        const lineNo = Number(control?.getAttribute("data-line-no") ?? "0");
        this.#reasonMirror.set(lineNo, reason);
        this.#draftFor(lineNo).reason = reason;
      } else if (scope === "sheet") {
        this.#sheetReason = reason;
      } else if (scope === "skid") {
        this.#skidReason = reason;
      }
      this.requestUpdate();
      return;
    }
    if (action === "save-pick") {
      void this.#savePick();
      return;
    }
    if (action === "open-sheet") {
      this.#openSheet(Number(control?.getAttribute("data-line-no") ?? "0"));
      return;
    }
    if (action === "close-sheet") {
      this.#sheetFor = null;
      this.#sheetQty = "";
      this.#sheetReason = "";
      this.#sheetNote = "";
      this.requestUpdate();
      return;
    }
    if (action === "send-shortage") {
      void this.#sendShortage();
      return;
    }
    if (action === "resolve") {
      this.#resolveFor = control?.getAttribute("data-shortage-id") ?? "";
      this.#resolveText = "";
      this.requestUpdate();
      return;
    }
    if (action === "cancel-resolve") {
      this.#resolveFor = "";
      this.requestUpdate();
      return;
    }
    if (action === "send-resolve") {
      void this.#sendResolve(control?.getAttribute("data-shortage-id") ?? "");
      return;
    }
    if (action === "skid-step") {
      this.#stepSkids(Number(control?.getAttribute("data-step") ?? "1"));
      return;
    }
    if (action === "save-skids") {
      void this.#saveSkids();
      return;
    }
    if (action === "stage") {
      void this.#stage();
      return;
    }
  };

  // The form fields: quantities, reasons, notes, the sheet, the stage spot.
  readonly #onField = (event: Event): void => {
    const target = event.target;
    if (!(target instanceof HTMLElement)) return;
    const field = target.getAttribute("data-field");
    const lineNo = Number(target.getAttribute("data-line-no") ?? "0");
    if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement) {
      const value = target.value;
      if (field === "qty") {
        this.#qtyMirror.set(lineNo, value);
        this.#draftFor(lineNo).qty = value;
        this.requestUpdate();
        return;
      }
      if (field === "short-note") {
        this.#noteMirror.set(lineNo, value);
        this.#draftFor(lineNo).note = value;
        return;
      }
      if (field === "skid-note") {
        this.#skidNote = value;
        return;
      }
      if (field === "sheet-qty") {
        this.#sheetQty = value;
        // The SHORT tile reads live from what the yard types.
        this.requestUpdate();
        return;
      }
      if (field === "sheet-note") {
        this.#sheetNote = value;
        return;
      }
      if (field === "resolution") {
        this.#resolveText = value;
        return;
      }
      if (field === "stage-where") {
        this.#stageWhere = value;
        return;
      }
      return;
    }
    // The reasons are radio rows, not selects: they ride data-action clicks,
    // not change events, so nothing else needs mirroring here.
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onHostClick);
    // input and change do not leave this element's own shadow root (change is
    // not composed), so the form listeners sit on the root itself.
    this.renderRoot.addEventListener("input", this.#onField);
    this.renderRoot.addEventListener("change", this.#onField);
    if (this.#custody.mode === "console") {
      this.#custody.begin("yard-pick", (outcome) => this.#onRecovered(outcome));
      return;
    }
    const handoff = readFrontDoorHandoff();
    if (handoff.kind === "ready") {
      const adopted = this.#auth.adopt({
        token: handoff.session.token,
        tokenExpiresAtMs: handoff.session.tokenExpiresAtMs,
        user: {
          userId: handoff.session.user.userId,
          email: handoff.session.user.email,
          name: handoff.session.user.name,
        },
      });
      if (adopted) {
        this.#roleLabel = handoff.session.roleName;
        this.#me = handoff.session.user.userId;
        clearFrontDoorHandoff();
        void this.#loadWorld();
      }
      this.requestUpdate();
    } else {
      void this.loadFlows();
    }
  }

  // Console mode: this page recovered its own session, or could not. A role the matrix gives this
  // app lands on its screen; an unavailable page keeps the unavailable entry while the custody retries;
  // a session the front door must settle goes back there.
  #onRecovered(outcome: AppStart): void {
    if (outcome.kind === "ready") {
      this.#roleLabel = outcome.entry.roleLabel;
      this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
      this.#me = outcome.entry.userId;
      void this.#loadWorld();
    } else if (outcome.kind === "front-door" && outcome.why === "ended") {
      this.#onSignOut();
      return;
    } else {
      this.#entryView = entryUnavailable();
      if (outcome.kind === "front-door") this.#custody.toFrontDoor();
    }
    this.requestUpdate();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.renderRoot.removeEventListener("input", this.#onField);
    this.renderRoot.removeEventListener("change", this.#onField);
    super.disconnectedCallback();
  }

  // The role label and the theme are the header inputs the position gate
  // keeps out of a template (only data- and aria- attributes take a bound
  // value), so they are set on the element here, after each render. The same
  // pass writes every field mirror back to its control, skipping the one
  // the yard is typing into, so a re-render never eats a typed number.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const header = this.renderRoot.querySelector("hos-app-header") as (HTMLElement & { roleLabel: string; theme: string }) | null;
    if (header !== null) {
      header.roleLabel = this.#roleLabel;
      header.theme = this.#theme;
    }
    for (const field of Array.from(this.renderRoot.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>("[data-field]"))) {
      if (field === (document.activeElement as HTMLElement | null)) continue;
      const name = field.getAttribute("data-field") ?? "";
      const lineNo = Number(field.getAttribute("data-line-no") ?? "0");
      let value: string | null = null;
      if (name === "qty") value = this.#qtyMirror.get(lineNo) ?? "";
      else if (name === "short-note") value = this.#noteMirror.get(lineNo) ?? "";
      else if (name === "sheet-qty") value = this.#sheetQty;
      else if (name === "sheet-note") value = this.#sheetNote;
      else if (name === "skid-note") value = this.#skidNote;
      else if (name === "stage-where") value = this.#stageWhere;
      else if (name === "resolution") value = this.#resolveText;
      if (value !== null && field.value !== value) field.value = value;
    }
  }

  private async loadFlows(): Promise<void> {
    this.#entryView = { flows: { kind: "loading" }, busy: false, message: null };
    this.requestUpdate();
    const result = await (this.#demoAccount as DemoAccount).listFlows();
    if (result.ok) {
      this.#entryView = { flows: { kind: "ready", flows: result.value }, busy: false, message: null };
    } else {
      this.#entryView = { flows: { kind: "unavailable" }, busy: false, message: null };
    }
    this.requestUpdate();
  }

  // The branch the yard works: the branches this login may reach, the first
  // one the board opens on. A login that reaches none is told so, in words.
  async #loadWorld(): Promise<void> {
    this.#phase = "loading";
    this.requestUpdate();
    const branches = await this.#yard.branches();
    if (!branches.ok) {
      this.#phase = "error";
      this.#error = yardError(branches.error, branches.status, "The yard did not load. Check your signal and try again.");
      this.requestUpdate();
      return;
    }
    if (branches.value.length === 0) {
      this.#phase = "error";
      this.#error = "You are not set up on a yard. Ask your branch manager to link your login to a branch.";
      this.requestUpdate();
      return;
    }
    this.#branchId = branches.value[0]?.id ?? "";
    this.#branchName = branches.value[0]?.name ?? "";
    await this.#loadQueue(true);
  }

  async #loadQueue(reset: boolean): Promise<void> {
    if (this.#branchId === "") {
      await this.#loadWorld();
      return;
    }
    if (reset) this.#phase = "loading";
    this.requestUpdate();
    const cursor = reset ? "" : this.#nextCursor;
    const answer = await this.#yard.queue(this.#branchId, cursor);
    if (answer.ok) {
      this.#picks = reset ? answer.value.picks : [...this.#picks, ...answer.value.picks];
      this.#nextCursor = answer.value.nextCursor;
      this.#moreBehind = answer.value.nextCursor !== "";
      this.#phase = "ready";
      this.#error = "";
    } else {
      this.#phase = this.#picks.length > 0 ? "ready" : "error";
      this.#error = yardError(answer.error, answer.status, "The board did not load. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  // Re-read the open pick without blanking the screen: the server's record
  // wins, and a failed re-read keeps what is on the glass.
  async #refreshPick(): Promise<void> {
    if (this.#view.kind !== "pick" || this.#pick === null) return;
    const answer = await this.#yard.pick(this.#view.id);
    if (answer.ok) this.#pick = answer.value;
  }

  #ticketLine(lineNo: number): { line_no: number; product_id: string; sku: string; description: string; qty_ordered: string; uom: string } | null {
    const line = this.#pick?.ticket?.lines.find((l) => l.line_no === lineNo);
    return line === undefined ? null : { line_no: line.line_no, product_id: line.product_id, sku: line.sku, description: line.description, qty_ordered: line.qty_ordered, uom: line.uom };
  }

  #draftFor(lineNo: number): Draft {
    let draft = this.#drafts.get(lineNo);
    if (draft === undefined) {
      draft = { qty: "", reason: "", note: "" };
      this.#drafts.set(lineNo, draft);
    }
    return draft;
  }

  #openEditor(lineNo: number): void {
    if (lineNo <= 0 || this.#ticketLine(lineNo) === null) return;
    this.#editing = lineNo;
    const draft = this.#draftFor(lineNo);
    if (!this.#qtyMirror.has(lineNo)) {
      const recorded = this.#pick?.picked.find((p) => p.order_line_no === lineNo);
      this.#qtyMirror.set(lineNo, recorded !== undefined ? qtyText(recorded.qty_picked) : "");
      draft.qty = this.#qtyMirror.get(lineNo) ?? "";
      if (recorded !== undefined) {
        this.#reasonMirror.set(lineNo, recorded.short_reason);
        this.#noteMirror.set(lineNo, recorded.short_note);
        draft.reason = recorded.short_reason;
        draft.note = recorded.short_note;
      }
    }
    this.requestUpdate();
  }

  #setQty(lineNo: number, value: string): void {
    this.#qtyMirror.set(lineNo, value);
    this.#draftFor(lineNo).qty = value;
    this.requestUpdate();
  }

  #stepQty(lineNo: number, step: number): void {
    const line = this.#ticketLine(lineNo);
    if (line === null) return;
    const current = Number((this.#qtyMirror.get(lineNo) ?? "").trim());
    const base = Number.isFinite(current) ? current : 0;
    const next = Math.max(0, Number((base + step).toFixed(4)));
    this.#setQty(lineNo, String(next));
  }

  async #claim(taskId: string): Promise<void> {
    if (taskId === "" || this.#busy) return;
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const answer = await this.#yard.claim(taskId);
    this.#busy = false;
    if (answer.ok) {
      await this.#openPick(taskId);
      this.#notice = actionDone("claim");
    } else {
      this.#error = yardError(answer.error, answer.status, "That did not go through. Check your signal and try again.", {
        notFound: "That pick is no longer on the board.",
      });
    }
    this.requestUpdate();
  }

  async #openPick(taskId: string): Promise<void> {
    if (taskId === "") return;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const answer = await this.#yard.pick(taskId);
    if (answer.ok) {
      this.#pick = answer.value;
      this.#view = { kind: "pick", id: taskId };
      this.#drafts = new Map();
      this.#qtyMirror = new Map();
      this.#reasonMirror = new Map();
      this.#noteMirror = new Map();
      this.#editing = null;
      this.#sheetFor = null;
      this.#resolveFor = "";
      this.#skids = null;
      this.#skidReason = "";
      this.#skidNote = "";
    } else {
      this.#error = yardError(answer.error, answer.status, "That pick did not open. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  async #start(taskId: string): Promise<void> {
    if (taskId === "" || this.#busy) return;
    this.#busy = true;
    this.#error = "";
    this.requestUpdate();
    const answer = await this.#yard.start(taskId);
    this.#busy = false;
    if (answer.ok) {
      this.#notice = actionDone("start");
      await this.#refreshPick();
    } else {
      this.#error = yardError(answer.error, answer.status, "That did not go through. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  #draftedLines(): YardRecordLine[] {
    const ticket = this.#pick?.ticket;
    if (ticket === null || ticket === undefined) return [];
    const out: YardRecordLine[] = [];
    for (const line of ticket.lines) {
      const draft = this.#drafts.get(line.line_no);
      if (draft === undefined || draft.qty.trim() === "") continue;
      const short = lineIsShort(line.qty_ordered, draft.qty.trim());
      out.push({
        order_line_no: line.line_no,
        product_id: line.product_id,
        sku: line.sku,
        description: line.description,
        qty_ordered: line.qty_ordered,
        qty_picked: draft.qty.trim(),
        uom: line.uom,
        short_reason: short ? draft.reason : "",
        short_note: short ? draft.note.trim() : "",
      });
    }
    return out;
  }

  async #savePick(): Promise<void> {
    if (this.#view.kind !== "pick" || this.#busy) return;
    const lines = this.#draftedLines();
    if (lines.length === 0) {
      this.#error = "Say what was picked first: open a line and type how many came off the shelf.";
      this.requestUpdate();
      return;
    }
    const blocked = recordBlockedReason(lines);
    if (blocked !== "") {
      this.#error = blocked;
      this.requestUpdate();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const answer = await this.#yard.recordLines(this.#view.id, lines);
    this.#busy = false;
    if (answer.ok) {
      await this.#refreshPick();
      const firstShort = answer.value.find((p) => lineIsShort(p.qty_ordered, p.qty_picked));
      if (firstShort !== undefined) {
        const ticketLine = this.#pick?.ticket?.lines.find((l) => l.line_no === firstShort.order_line_no);
        this.#notice = shortPickWords(firstShort, ticketLine?.qty_backordered ?? "");
      } else {
        this.#notice = pickSummary(answer.value) || "The pick is recorded.";
      }
    } else {
      this.#error = yardError(answer.error, answer.status, "The pick did not save. Check your signal and try again.");
      await this.#refreshPick();
    }
    this.requestUpdate();
  }

  #openSheet(statedLineNo: number): void {
    const ticket = this.#pick?.ticket;
    if (ticket === null || ticket === undefined || ticket.lines.length === 0) return;
    let lineNo = statedLineNo;
    if (lineNo <= 0) {
      const shortDrafted = ticket.lines.find((l) => {
        const draft = this.#drafts.get(l.line_no);
        return draft !== undefined && draft.qty.trim() !== "" && lineIsShort(l.qty_ordered, draft.qty.trim());
      });
      lineNo = (shortDrafted ?? ticket.lines[0]).line_no;
    }
    this.#sheetFor = lineNo;
    // The board's SHORT tile starts from what the pull already measured: the
    // ordered less what was pulled, when the line has a number; empty when it
    // does not, never a confident guess.
    const line = ticket.lines.find((l) => l.line_no === lineNo);
    const pulled = line === undefined ? "" : (this.#drafts.get(lineNo)?.qty.trim() ?? this.#pick?.picked.find((p) => p.order_line_no === lineNo)?.qty_picked ?? "");
    this.#sheetQty = line === undefined || pulled === "" ? "" : shortOf(line.qty_ordered, pulled);
    this.#sheetReason = "";
    this.#sheetNote = "";
    this.#error = "";
    this.requestUpdate();
  }

  async #sendShortage(): Promise<void> {
    if (this.#view.kind !== "pick" || this.#sheetFor === null || this.#busy) return;
    const line = this.#ticketLine(this.#sheetFor);
    if (line === null) return;
    const qtyBad = validateShortage(this.#sheetQty);
    if (qtyBad !== "") {
      this.#error = qtyBad;
      this.requestUpdate();
      return;
    }
    if (this.#sheetReason === "") {
      this.#error = "Give the reason. The counter has to tell the customer why.";
      this.requestUpdate();
      return;
    }
    if (this.#sheetReason === "OTHER" && this.#sheetNote.trim() === "") {
      this.#error = "Say what happened in the note when the reason is Other.";
      this.requestUpdate();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.requestUpdate();
    const answer = await this.#yard.reportShortage(this.#view.id, {
      orderLineNo: line.line_no,
      description: line.description,
      qtyShort: this.#sheetQty.trim(),
      uom: line.uom,
      note: this.#sheetNote.trim(),
    });
    this.#busy = false;
    if (answer.ok) {
      this.#sheetFor = null;
      this.#sheetQty = "";
      this.#sheetReason = "";
      this.#sheetNote = "";
      this.#notice = shortageSentWords(answer.value);
      await this.#refreshPick();
    } else {
      this.#error = yardError(answer.error, answer.status, "The shortage did not send. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  async #sendResolve(shortageId: string): Promise<void> {
    if (this.#view.kind !== "pick" || shortageId === "" || this.#busy) return;
    const resolution = this.#resolveText.trim();
    if (resolution === "") {
      this.#error = "Say how it was settled. The counter reads this back to the customer.";
      this.requestUpdate();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.requestUpdate();
    const answer = await this.#yard.resolveShortage(this.#view.id, shortageId, resolution);
    this.#busy = false;
    if (answer.ok) {
      this.#resolveFor = "";
      this.#resolveText = "";
      this.#notice = `Shortage cleared: ${answer.value.resolution}`;
      await this.#refreshPick();
    } else {
      // The resolve door is the one list wall this app meets: a caller off the
      // shortage resolvers list is answered with the wall's own words.
      this.#error = yardError(answer.error, answer.status, "That did not go through. Check your signal and try again.", { wall: RESOLVE_WALL_NAMES });
    }
    this.requestUpdate();
  }

  #stepSkids(step: number): void {
    const base = this.#skids ?? 0;
    this.#skids = Math.max(0, base + step);
    this.requestUpdate();
  }

  async #saveSkids(): Promise<void> {
    if (this.#view.kind !== "pick" || this.#busy) return;
    const blocked = skidBlockedReason(this.#skids, this.#skidReason, this.#skidNote);
    if (blocked !== "") {
      this.#error = blocked;
      this.requestUpdate();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const answer = await this.#yard.recordSkids(this.#view.id, this.#skids as number, this.#skidReason, this.#skidNote.trim());
    this.#busy = false;
    if (answer.ok) {
      this.#skids = answer.value.skid_count;
      this.#notice = skidWords(answer.value);
    } else {
      this.#error = yardError(answer.error, answer.status, "The skid count did not save. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  async #stage(): Promise<void> {
    if (this.#view.kind !== "pick" || this.#busy) return;
    const locationBad = validateStage(this.#stageWhere);
    if (locationBad !== "") {
      this.#error = locationBad;
      this.requestUpdate();
      return;
    }
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const answer = await this.#yard.stage(this.#view.id, this.#stageWhere.trim(), "", "");
    this.#busy = false;
    if (answer.ok) {
      this.#stageWhere = "";
      this.#notice = actionDone("stage", { location: answer.value.stage_location, ref: answer.value.stage_ref });
      await this.#refreshPick();
    } else {
      this.#error = yardError(answer.error, answer.status, "The pick did not stage. Check your signal and try again.");
    }
    this.requestUpdate();
  }

  async #chooseFlow(flow: string): Promise<void> {
    this.#entryView = { ...this.#entryView, busy: true };
    this.requestUpdate();
    const result = await (this.#demoAccount as DemoAccount).chooseFlow(flow);
    if (result.ok) {
      const adopted = this.#auth.adopt(result.value);
      if (adopted) {
        this.#roleLabel = result.value.identity.roleName;
        this.#me = result.value.user.userId;
        this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
        void this.#loadWorld();
      } else {
        this.#entryView = { ...this.#entryView, busy: false, message: "Demo sign-in refused: the mint's session was not usable." };
      }
    } else {
      this.#entryView = { ...this.#entryView, busy: false, message: `Demo sign-in refused: ${result.error}` };
    }
    this.requestUpdate();
  }

  #onSignOut(): void {
    this.#sessions.clear();
    this.#phase = "loading";
    this.#picks = [];
    this.#branchId = "";
    this.#branchName = "";
    this.#error = "";
    this.#notice = "";
    this.#view = { kind: "queue" };
    this.#pick = null;
    clearFrontDoorHandoff();
    if (this.#custody.mode === "console") {
      this.#entryView = entryUnavailable();
      this.requestUpdate();
      void this.#custody.leave();
      return;
    }
    void this.loadFlows();
  }

  // The theme control rides the header's named slot; its word names the
  // theme a tap moves to. It is a control like any other: the app's own
  // 56 px floor applies.
  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Brand theme" : "Light theme";
    return html`<button slot="theme" type="button" class="btn btn-secondary theme-button" data-action="theme">${label}</button>`;
  }

  private renderWork(): TemplateResult {
    if (this.#view.kind === "pick" && this.#pick !== null) {
      const drafts = [...this.#drafts.entries()].map(([order_line_no, d]) => ({
        order_line_no,
        qty_picked: d.qty,
        short_reason: d.reason,
        short_note: d.note,
      }));
      return renderPick({
        pick: this.#pick,
        drafts,
        editing: this.#editing,
        sheetFor: this.#sheetFor,
        sheetQty: this.#sheetQty,
        sheetReason: this.#sheetReason,
        resolveFor: this.#resolveFor,
        busy: this.#busy,
        error: this.#error,
        notice: this.#notice,
        skids: this.#skids,
        skidReason: this.#skidReason,
      });
    }
    return renderQueue({
      phase: this.#phase,
      branchName: this.#branchName,
      picks: this.#picks,
      me: this.#me,
      error: this.#error,
      notice: this.#notice,
      moreBehind: this.#moreBehind,
    });
  }

  private renderSignedIn(): TemplateResult {
    return html`
      <hos-app-header signed-in demo compact product-name="Yard Pick" product-sub="Yard Pick · HH Field shell">${this.renderThemeControl()}</hos-app-header>
      <main>${this.renderWork()}</main>
    `;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) return this.renderSignedIn();
    return html`
      <hos-app-header demo compact product-name="Yard Pick" product-sub="Yard Pick · HH Field shell">${this.renderThemeControl()}</hos-app-header>
      <main>
        ${renderEntry(this.#entryView)}
      </main>
    `;
  }
}

defineElement("yard-pick-app", YardPickApp);
