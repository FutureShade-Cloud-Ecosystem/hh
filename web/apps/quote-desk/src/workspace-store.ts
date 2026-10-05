// The work area's layout store: the one owner of the workspace row's state,
// its debounced save and its reset (the till's own panel layout store
// pattern, on the server row the settings kit holds for this surface, so the
// layout follows the person rather than the tab). The store never blocks or
// blanks the screen: a prefs outage is best effort, the shipped defaults
// stand, and every write is the whole row (split, arrangement, collapse,
// cost hide, and the open quote a refresh resumes).
import type { QdClient, SurfaceConfig } from "./qd-client.js";
import { quoteErrorMessage } from "./_quote-edit.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { captureSend, deferredSend, wasUnsent, type SendCapture, type SendNow } from "./deferred-send.js";
import type { QuoteArrangement } from "./_quote-workarea.js";

/** The workspace's whole state, one row. */
export interface WorkspaceState {
  splitPct: number;
  arrangement: QuoteArrangement;
  primaryCollapsed: boolean;
  secondaryCollapsed: boolean;
  costHidden: boolean;
  /** The quote a refresh resumes, or "" for compose. */
  openRef: string;
  /** Whose row this is: the person's own save, or the dealer default. */
  source: "user" | "dealer";
}

export const DEFAULT_WORKSPACE: WorkspaceState = {
  splitPct: 58,
  arrangement: "PRIMARY_LEFT",
  primaryCollapsed: false,
  secondaryCollapsed: false,
  costHidden: false,
  openRef: "",
  source: "dealer",
};

/**
 * The store. Constructed with the client and the surface config's own
 * workspace row; the caller reads state, mutates through the methods, and
 * the debounced save rides behind every change. A save refusal is never
 * thrown and never dropped: the screen keeps its state and onError carries the
 * words, which the work area shows on an error line; a later save that lands
 * clears it (onError with an empty line).
 */
export class WorkspaceStore {
  readonly state: WorkspaceState;
  #client: QdClient;
  #timer: ReturnType<typeof setTimeout> | undefined;
  /**
   * The writes waiting for their turn, in the order they were asked: a save of the whole row, or the reset's delete.
   * A save asked while another save is still waiting replaces it (the row is sent whole, as it stands then), but a
   * save never replaces a waiting delete and never jumps it: the reset and the changes after it both go, in order.
   */
  #queue: Array<{ kind: "save" | "delete"; run: () => Promise<void> }> = [];
  /** What the component holds now; the sender compares it with what was taken down when the person acted. */
  readonly #now: () => SendNow;
  /** The session as it stood at the person's last layout change, until its save is asked for. */
  #changed: SendCapture | null = null;
  /** The drain in progress: one request is out at a time, and what is asked while it is out goes after it. */
  #inFlight: Promise<void> | null = null;
  /** True once sign out or the page leaving stopped the store: nothing more is sent, whatever is queued is dropped. */
  #closed = false;
  /** The reset's own outcome, handed back by reset() when its turn in the queue has run. */
  #deleted = false;
  /** Told the words of the last save or reset outcome: a refusal in words, "" when it landed. */
  onError: (line: string) => void = () => undefined;

  constructor(client: QdClient, config: SurfaceConfig | null, now?: () => SendNow) {
    this.#client = client;
    this.#now = now ?? (() => ({ client: this.#client, attached: !this.#closed, quote: null }));
    const row = config?.workspace;
    this.state = row
      ? {
          splitPct: row.splitPct > 0 ? row.splitPct : DEFAULT_WORKSPACE.splitPct,
          arrangement: row.arrangement,
          primaryCollapsed: row.primaryCollapsed,
          secondaryCollapsed: row.secondaryCollapsed,
          costHidden: row.costHidden,
          openRef: row.openRef,
          source: row.source === "user" ? "user" : "dealer",
        }
      : { ...DEFAULT_WORKSPACE };
  }

  /** Schedule the debounced whole-row save. */
  scheduleSave(): void {
    if (this.#timer !== undefined) clearTimeout(this.#timer);
    this.#changed = captureSend(this.#now);
    this.#timer = setTimeout(() => {
      this.#timer = undefined;
      void this.save();
    }, 400);
  }

  /**
   * The save itself: best effort, the person's row written whole, one request out at a time. A save asked for while
   * another is out is sent after it with the state as it is then, so two saves never reach the server in the wrong
   * order and the row it holds is the last one the person made (the quote a refresh resumes).
   */
  async save(): Promise<void> {
    if (this.#timer !== undefined) {
      clearTimeout(this.#timer);
      this.#timer = undefined;
    }
    this.state.source = "user";
    const capture = this.#changed ?? captureSend(this.#now);
    this.#changed = null;
    return this.#enqueue("save", () => this.#sendRow(capture));
  }

  /** The row as it stands, sent whole, under the session it was asked in or not at all. */
  async #sendRow(capture: SendCapture): Promise<void> {
    const result = await deferredSend(capture, () =>
      this.#client.putMyWorkspace({
        splitPct: this.state.splitPct,
        arrangement: this.state.arrangement,
        primaryCollapsed: this.state.primaryCollapsed,
        secondaryCollapsed: this.state.secondaryCollapsed,
        costHidden: this.state.costHidden,
        openRef: this.state.openRef,
      }),
    );
    // A refusal by the sender sent nothing, and the person it was for is not the one on the page: nothing is said.
    if (wasUnsent(result)) return;
    this.onError(result.ok ? "" : `The layout was not saved: ${spokenRefusal(quoteErrorMessage(result.status, result.error, "save the layout"))}`);
  }

  /** Put one write in the queue: it goes after the write before it. */
  #enqueue(kind: "save" | "delete", run: () => Promise<void>): Promise<void> {
    const last = this.#queue[this.#queue.length - 1];
    if (kind === "save" && last?.kind === "save") this.#queue[this.#queue.length - 1] = { kind, run };
    else this.#queue.push({ kind, run });
    if (this.#inFlight !== null) return this.#inFlight;
    this.#inFlight = this.#drain();
    return this.#inFlight;
  }

  async #drain(): Promise<void> {
    try {
      for (;;) {
        // Before EVERY further send: a store stopped at sign out or at the page leaving sends nothing more, and
        // what is queued is dropped with no word, because the only screen that could be told is gone or going and
        // nothing may be sent or said under the next person's session.
        if (this.#closed) break;
        const next = this.#queue.shift();
        if (next === undefined) break;
        await next.run();
      }
    } finally {
      this.#inFlight = null;
    }
  }

  /**
   * Stop the store: sign out calls it once the unsent work has been sent or given up on, and the work area calls it
   * when it leaves the page. Every send passes the drain, and the drain is what the closed flag stops, so a stopped
   * store sends nothing more and drops what is queued whatever door the ask came through.
   */
  close(): void {
    this.#closed = true;
    this.#queue = [];
    if (this.#timer !== undefined) {
      clearTimeout(this.#timer);
      this.#timer = undefined;
    }
  }

  /** The flush a sign-out or a hand-run check waits on. */
  async flush(): Promise<void> {
    if (this.#timer !== undefined) await this.save();
    await this.#inFlight;
  }

  /**
   * Reset the person's row: the dealer default comes back. The delete goes through the same queue as a save,
   * one write out at a time, so it can never jump a save still out and be overwritten by it when that save
   * lands after it. A refused delete changes nothing on screen (the saved row still stands, so the screen must
   * not claim otherwise); it answers false and says why through onError.
   */
  async reset(): Promise<boolean> {
    if (this.#timer !== undefined) {
      clearTimeout(this.#timer);
      this.#timer = undefined;
    }
    this.#deleted = false;
    const capture = captureSend(this.#now);
    // What the person changes while the delete is out is theirs: it is laid over the defaults when the delete lands.
    const before = { ...this.state };
    await this.#enqueue("delete", () => this.#deleteRow(capture, before));
    return this.#deleted;
  }

  async #deleteRow(capture: SendCapture, before: WorkspaceState): Promise<void> {
    const result = await deferredSend(capture, () => this.#client.deleteMyWorkspace());
    if (wasUnsent(result)) return;
    if (!result.ok) {
      this.onError(`The layout was not reset: ${spokenRefusal(quoteErrorMessage(result.status, result.error, "reset the layout"))}`);
      return;
    }
    this.onError("");
    const kept: Partial<WorkspaceState> = {};
    for (const key of Object.keys(DEFAULT_WORKSPACE) as Array<keyof WorkspaceState>) {
      if (key !== "source" && this.state[key] !== before[key]) Object.assign(kept, { [key]: this.state[key] });
    }
    Object.assign(this.state, DEFAULT_WORKSPACE, kept);
    this.#deleted = true;
  }
}
