// The runner drives the pure sync machine (statecharts/sync/machine.ts, handed
// in by the shell) over a store and a transport. It owns no policy: the
// machine says what happens next, the runner performs the named effect and
// feeds the outcome back as an event. One event at a time, in order.
import { serialQueue } from "./sql.js";
import type { OfflineStore } from "./store.js";
import type { SyncTransport } from "./transport.js";

/** The events the runner sends, in the machine's own vocabulary. */
export type RunnerEvent =
  | { type: "ONLINE" }
  | { type: "OFFLINE" }
  | { type: "SYNC_REQUESTED" }
  | { type: "PUSH_DONE" }
  | { type: "PUSH_FAULT"; openCards: boolean }
  | { type: "PULL_DONE"; openCards: boolean }
  | { type: "PULL_FAULT"; openCards: boolean }
  | { type: "NETWORK_LOST" }
  | { type: "AUTH_EXPIRED" }
  | { type: "SESSION_REFRESHED"; online: boolean }
  | { type: "CARDS_CLEARED" };

/** The shape of statecharts/sync/machine.ts, structurally. */
export interface SyncMachine {
  readonly INITIAL: string;
  transition(state: string, event: RunnerEvent): { state: string; effects: readonly string[] };
}

export interface SyncRunnerOptions {
  store: OfflineStore;
  transport: SyncTransport;
  machine: SyncMachine;
}

export class SyncRunner {
  readonly #store: OfflineStore;
  readonly #transport: SyncTransport;
  readonly #machine: SyncMachine;
  readonly #queue = serialQueue();
  readonly #listeners = new Set<(state: string) => void>();
  readonly #unsubscribeCards: () => void;
  #state: string;
  #requestQueued = false;

  constructor(options: SyncRunnerOptions) {
    this.#store = options.store;
    this.#transport = options.transport;
    this.#machine = options.machine;
    this.#state = options.machine.INITIAL;
    this.#unsubscribeCards = options.store.cards.subscribe(() => {
      void this.#queue(async () => {
        if (this.#state === "conflict" && !(await this.#openCards())) await this.#step({ type: "CARDS_CLEARED" });
      });
    });
  }

  get state(): string {
    return this.#state;
  }

  /** Hear every state change. Returns the unsubscribe. */
  onState(listener: (state: string) => void): () => void {
    this.#listeners.add(listener);
    return () => void this.#listeners.delete(listener);
  }

  /** Feed an outside event (connectivity, session) and run whatever it starts, to rest. */
  dispatch(event: RunnerEvent): Promise<void> {
    return this.#queue(() => this.#step(event));
  }

  /** Ask for a cycle. Requests that arrive before the queued one starts join it. */
  request(): Promise<void> {
    if (this.#requestQueued) return this.settled();
    this.#requestQueued = true;
    return this.#queue(async () => {
      this.#requestQueued = false;
      await this.#step({ type: "SYNC_REQUESTED" });
    });
  }

  /** Resolves when everything queued so far has run. */
  settled(): Promise<void> {
    return this.#queue(async () => undefined);
  }

  stop(): void {
    this.#unsubscribeCards();
  }

  async #step(first: RunnerEvent): Promise<void> {
    let event: RunnerEvent | null = first;
    while (event) {
      const next = this.#machine.transition(this.#state, event);
      this.#set(next.state);
      event = null;
      for (const effect of next.effects) event = await this.#perform(effect);
    }
  }

  #set(state: string): void {
    if (state === this.#state) return;
    this.#state = state;
    for (const listener of [...this.#listeners]) listener(state);
  }

  async #openCards(): Promise<boolean> {
    try {
      return (await this.#store.cards.listOpen()).length > 0;
    } catch {
      return false;
    }
  }

  async #perform(effect: string): Promise<RunnerEvent> {
    try {
      if (effect === "push") {
        const out = await this.#store.replay(this.#transport);
        if (out.kind === "done") return { type: "PUSH_DONE" };
        if (out.kind === "offline") return { type: "NETWORK_LOST" };
        if (out.kind === "auth_expired") return { type: "AUTH_EXPIRED" };
        return { type: "PUSH_FAULT", openCards: await this.#openCards() };
      }
      const out = await this.#store.pull(this.#transport);
      if (out.kind === "done") return { type: "PULL_DONE", openCards: await this.#openCards() };
      if (out.kind === "offline") return { type: "NETWORK_LOST" };
      if (out.kind === "auth_expired") return { type: "AUTH_EXPIRED" };
      return { type: "PULL_FAULT", openCards: await this.#openCards() };
    } catch {
      // The store threw (the database failed): a fault, never a crash of the loop.
      const openCards = await this.#openCards();
      return effect === "push" ? { type: "PUSH_FAULT", openCards } : { type: "PULL_FAULT", openCards };
    }
  }
}
