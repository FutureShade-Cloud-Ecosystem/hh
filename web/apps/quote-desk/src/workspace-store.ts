// The work area's layout store: the one owner of the workspace row's state,
// its debounced save and its reset (the till's own panel layout store
// pattern, on the server row the settings kit holds for this surface, so the
// layout follows the person rather than the tab). The store never blocks or
// blanks the screen: a prefs outage is best effort, the shipped defaults
// stand, and every write is the whole row (split, arrangement, collapse,
// cost hide, and the open quote a refresh resumes).
import type { QdClient, SurfaceConfig } from "./qd-client.js";
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
 * the debounced save rides behind every change. A save refusal is held on
 * the store's own error line and never thrown: the screen keeps its state
 * and the words say what failed, exactly the posture the till's store keeps.
 */
export class WorkspaceStore {
  readonly state: WorkspaceState;
  #client: QdClient;
  #timer: ReturnType<typeof setTimeout> | undefined;
  #saveNow: Promise<void> = Promise.resolve();

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
      .then(() => undefined);
    await this.#saveNow;
  }

  /** The flush a sign-out or a hand-run check waits on. */
  async flush(): Promise<void> {
    if (this.#timer !== undefined) await this.save();
    await this.#saveNow;
  }

  /** Reset the person's row: the dealer default comes back. */
  async reset(): Promise<void> {
    if (this.#timer !== undefined) {
      clearTimeout(this.#timer);
      this.#timer = undefined;
    }
    await this.#client.deleteMyWorkspace();
    Object.assign(this.state, DEFAULT_WORKSPACE);
  }
}
