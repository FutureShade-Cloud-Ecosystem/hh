// The live feed: the core's server sent event stream, kept honest. The stream tells the screen when
// to read again; it carries no state the screen trusts. A dropped stream is a visible state with a
// retry, never a silent stop, and while it is down (and as a net under it while it is up, because a
// slow or stuck connection drops events) the feed asks the screen to read on a timer.
//
// The browser's EventSource can not send the Bearer header, so the stream is opened with a short
// lived, single use ticket the core mints for the signed in caller. A used ticket is spent, so every
// reconnect mints a new one. A core that does not issue tickets answers 501: the feed then says it
// reads every few seconds and keeps to that.
//
// The feed takes its mint, its stream and its timers from the caller, so the state machine is
// pinned in node without a page or a network.
import type { Result } from "@hardscapehouse/api-client";

export type LiveState = "connecting" | "live" | "reconnecting" | "polling" | "offline";
export type NudgeKind = "message" | "unread" | "approval" | "poll";

/** How often the screen reads on its own while the stream is live (a net under the stream). */
export const POLL_LIVE_MS = 30_000;
/** How often it reads while the stream is down or not issued. */
export const POLL_FALLBACK_MS = 10_000;

const FIRST_RETRY_MS = 2_000;
const LONGEST_RETRY_MS = 30_000;

/** The wait before reconnect number `attempt` (zero based): two seconds, doubling, to thirty. */
export function retryDelayMs(attempt: number): number {
  return Math.min(LONGEST_RETRY_MS, FIRST_RETRY_MS * 2 ** Math.max(0, attempt));
}

/** What the connection strip says, in the voice of the rest of the app. */
export function liveWords(state: LiveState): string {
  switch (state) {
    case "live":
      return "Live";
    case "connecting":
      return "Connecting";
    case "reconnecting":
      return "Reconnecting. Messages are read again every 10 seconds until the stream is back.";
    case "polling":
      return "Messages are read every 10 seconds.";
    case "offline":
      return "Offline. House Messages cannot reach the server, and nothing you send is saved until it can.";
  }
}

/** The slice of EventSource the feed uses. */
export interface StreamSource {
  addEventListener(type: string, listener: (event: { data?: string }) => void): void;
  close(): void;
}

export interface Timers {
  set(fn: () => void, ms: number): unknown;
  clear(handle: unknown): void;
}

export interface LiveOptions {
  readonly mintTicket: () => Promise<Result<{ ticket: string; expires_in_seconds: number }>>;
  readonly open: (url: string) => StreamSource;
  readonly timers: Timers;
  readonly onState: (state: LiveState) => void;
  readonly onNudge: (kind: NudgeKind, data: unknown) => void;
}

const STREAM_PATH = "/api/v1/messaging/stream";

export class LiveFeed {
  readonly #o: LiveOptions;
  #state: LiveState | null = null;
  #source: StreamSource | null = null;
  #attempt = 0;
  #retry: unknown = null;
  #poll: unknown = null;
  #running = false;
  // Each connection attempt has a generation; a late answer from an older one is dropped.
  #generation = 0;

  constructor(options: LiveOptions) {
    this.#o = options;
  }

  get state(): LiveState | null {
    return this.#state;
  }

  start(): void {
    if (this.#running) return;
    this.#running = true;
    this.#setState("connecting");
    void this.#connect();
  }

  stop(): void {
    this.#running = false;
    this.#generation += 1;
    this.#closeSource();
    this.#clearRetry();
    this.#clearPoll();
  }

  /** Reconnect at once, for the person who tapped Retry. */
  retryNow(): void {
    if (!this.#running) return;
    this.#generation += 1;
    this.#closeSource();
    this.#clearRetry();
    this.#setState("connecting");
    void this.#connect();
  }

  #setState(next: LiveState): void {
    if (this.#state === next) return;
    this.#state = next;
    // The poll interval depends on the state, so the timer is set again with the right one.
    this.#clearPoll();
    this.#schedulePoll();
    this.#o.onState(next);
  }

  #schedulePoll(): void {
    if (!this.#running) return;
    const ms = this.#state === "live" ? POLL_LIVE_MS : POLL_FALLBACK_MS;
    this.#poll = this.#o.timers.set(() => {
      this.#poll = null;
      if (!this.#running) return;
      this.#o.onNudge("poll", null);
      this.#schedulePoll();
    }, ms);
  }

  #clearPoll(): void {
    if (this.#poll !== null) this.#o.timers.clear(this.#poll);
    this.#poll = null;
  }

  #clearRetry(): void {
    if (this.#retry !== null) this.#o.timers.clear(this.#retry);
    this.#retry = null;
  }

  #closeSource(): void {
    this.#source?.close();
    this.#source = null;
  }

  #scheduleRetry(): void {
    const ms = retryDelayMs(this.#attempt);
    this.#attempt += 1;
    this.#retry = this.#o.timers.set(() => {
      this.#retry = null;
      if (!this.#running) return;
      void this.#connect();
    }, ms);
  }

  async #connect(): Promise<void> {
    const generation = ++this.#generation;
    const minted = await this.#o.mintTicket();
    if (!this.#running || generation !== this.#generation) return;
    if (!minted.ok) {
      if (minted.status === 501) {
        this.#setState("polling");
        return;
      }
      this.#setState("offline");
      this.#scheduleRetry();
      return;
    }
    const source = this.#o.open(`${STREAM_PATH}?ticket=${encodeURIComponent(minted.value.ticket)}`);
    this.#source = source;
    source.addEventListener("open", () => {
      if (!this.#running || generation !== this.#generation) return;
      this.#attempt = 0;
      this.#setState("live");
    });
    const nudge = (kind: NudgeKind) => (event: { data?: string }) => {
      if (!this.#running || generation !== this.#generation) return;
      this.#o.onNudge(kind, parse(event.data));
    };
    source.addEventListener("message", nudge("message"));
    source.addEventListener("unread", nudge("unread"));
    source.addEventListener("approval.decided", nudge("approval"));
    source.addEventListener("error", () => {
      if (!this.#running || generation !== this.#generation) return;
      this.#closeSource();
      this.#setState("reconnecting");
      this.#scheduleRetry();
    });
  }
}

function parse(data: string | undefined): unknown {
  if (data === undefined || data === "") return null;
  try {
    return JSON.parse(data);
  } catch {
    return null;
  }
}
