// The quote context: the one mechanism that keeps work belonging to one quote
// from landing on another when the person switches quotes (or scope, or branch).
//
// A context is made when a quote is opened and cancelled when it is left. Every
// read and every write captures the context at its START, binds itself to the
// context's own id (never to a property read after an await), checks the
// context after EVERY await, and applies an answer to state, or issues a
// follow-up write, only while the context is still live.
//
//   const ctx = this.ctx;                                  // capture at the start
//   const result = await ctx.guarded(client.readQuote(ctx.id));
//   if (result === undefined) return;                      // the quote changed: say nothing
//
// guarded() resolves to undefined when the context went stale while the call
// was out, so a caller cannot forget the check by accident.
export class QuoteContext {
  readonly id: string;
  /** Counts the contexts a scope has opened: a context made later has a bigger epoch. */
  readonly epoch: number;
  private aborted = false;
  private reads = 0;

  constructor(id: string, epoch: number) {
    this.id = id;
    this.epoch = epoch;
  }

  /** A context that is already cancelled: what a screen that has left the page hands out, so nothing it still holds can issue a request. */
  static cancelled(id: string): QuoteContext {
    const dead = new QuoteContext(id, 0);
    dead.cancel();
    return dead;
  }

  /** True once the quote this context belongs to has been left. */
  get stale(): boolean {
    return this.aborted;
  }

  cancel(): void {
    this.aborted = true;
  }

  /**
   * Begins a read of this quote and numbers it. A read begun later supersedes every older one still out, so reads of
   * one quote land in the order they were asked: the answers of an older read are dropped by guardedRead.
   */
  startRead(): number {
    this.reads += 1;
    return this.reads;
  }

  /** True while no newer read of this quote has begun. */
  isLatestRead(seq: number): boolean {
    return seq === this.reads;
  }

  /** The answer of read number `seq`, or undefined when the context went stale or a newer read began while the call was out. */
  async guardedRead<T>(seq: number, call: Promise<T>): Promise<T | undefined> {
    const answer = await call;
    return this.aborted || seq !== this.reads ? undefined : answer;
  }

  /**
   * The answer, or undefined when the context went stale while the call was in flight. A write that was out when the
   * quote was left may well have landed on the server: `onLate` is handed that dropped answer, so the caller can
   * count it or tell the screens that the quote changed, without applying it to the quote now open.
   */
  async guarded<T>(call: Promise<T>, onLate?: (answer: T) => void): Promise<T | undefined> {
    const answer = await call;
    if (!this.aborted) return answer;
    onLate?.(answer);
    return undefined;
  }
}

/** Holds the live context for one place that shows a quote, and replaces it (cancelling the old) on each open. */
export class QuoteScope {
  private epoch = 0;
  private live: QuoteContext = new QuoteContext("", 0);

  get current(): QuoteContext {
    return this.live;
  }

  /** Cancel the context in use and start a fresh one for the quote now open. */
  open(id: string): QuoteContext {
    this.live.cancel();
    this.epoch += 1;
    this.live = new QuoteContext(id, this.epoch);
    return this.live;
  }
}

/**
 * Sign out asks everything that holds unsent work to send it while the session
 * still stands: a listener pushes its own flush onto detail.waits and the host
 * waits for them all before it ends the session.
 */
export const SIGN_OUT_EVENT = "qd-sign-out";

export interface SignOutDetail {
  waits: Promise<void>[];
}

/** How long a wait on unsent work lasts before the host goes on without it. */
export const UNSENT_WAIT_MS = 3000;

/** Waits for a promise for at most `ms`; true when it finished first, false when the wait timed out. */
export async function waitUpTo(ms: number, work: Promise<unknown>): Promise<boolean> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timedOut = new Promise<false>((resolve) => {
    timer = setTimeout(() => resolve(false), ms);
  });
  const finished = work.then(
    () => true as const,
    () => true as const,
  );
  const landed = await Promise.race([finished, timedOut]);
  if (timer !== undefined) clearTimeout(timer);
  return landed;
}
