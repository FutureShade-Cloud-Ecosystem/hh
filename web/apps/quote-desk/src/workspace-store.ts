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
  #saveNow: Promise<void> = Promise.resolve();
  /** Told the words of the last save or reset outcome: a refusal in words, "" when it landed. */
  onError: (line: string) => void = () => undefined;

  constructor(client: QdClient, config: SurfaceConfig | null) {
    this.#client = client;
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
    this.#timer = setTimeout(() => {
      this.#timer = undefined;
      void this.save();
    }, 400);
  }

  /** The save itself: best effort, the person's row written whole. */
  async save(): Promise<void> {
    if (this.#timer !== undefined) {
      clearTimeout(this.#timer);
      this.#timer = undefined;
    }
    this.state.source = "user";
    this.#saveNow = this.#client
      .putMyWorkspace({
        splitPct: this.state.splitPct,
        arrangement: this.state.arrangement,
        primaryCollapsed: this.state.primaryCollapsed,
        secondaryCollapsed: this.state.secondaryCollapsed,
        costHidden: this.state.costHidden,
        openRef: this.state.openRef,
      })
      .then((result) => {
        this.onError(result.ok ? "" : `The layout was not saved: ${spokenRefusal(quoteErrorMessage(result.status, result.error, "save the layout"))}`);
      });
    await this.#saveNow;
  }

  /** The flush a sign-out or a hand-run check waits on. */
  async flush(): Promise<void> {
    if (this.#timer !== undefined) await this.save();
    await this.#saveNow;
  }

  /**
   * Reset the person's row: the dealer default comes back. A refused delete
   * changes nothing on screen (the saved row still stands, so the screen must
   * not claim otherwise); it answers false and says why through onError.
   */
  async reset(): Promise<boolean> {
    if (this.#timer !== undefined) {
      clearTimeout(this.#timer);
      this.#timer = undefined;
    }
    const result = await this.#client.deleteMyWorkspace();
    if (!result.ok) {
      this.onError(`The layout was not reset: ${spokenRefusal(quoteErrorMessage(result.status, result.error, "reset the layout"))}`);
      return false;
    }
    this.onError("");
    Object.assign(this.state, DEFAULT_WORKSPACE);
    return true;
  }
}
