// The one sender for every write that goes out after an await, a then(), a timer or a queue: a write that outlives
// the moment the person asked for it. Such a write must not outlive the person or the quote it was asked on, so it
// is made in two steps: when the person acts, captureSend() takes down the session (the client the work area was
// connected with and the signed in person's id) and, for a write that stays with the quote on screen, the quote
// context (its id and its epoch); and at the moment of sending, deferredSend() reads both again and sends nothing
// when either has changed. test/write-census.mjs fails the build for any write of the client reached from a deferred
// position that is not the closure handed to deferredSend.
import type { Result } from "@hardscapehouse/api-client";
import type { QdClient } from "./qd-client.js";
import type { QuoteContext } from "./quote-context.js";

/** What the component that asked holds at the moment of sending. */
export interface SendNow {
  client: QdClient | null;
  /** False once the component has left the page (sign out removes it). */
  attached: boolean;
  /** The context of the quote on screen now; null for a write that belongs to no quote (the layout). */
  quote: QuoteContext | null;
}

/** The refusal's own shape: an error result, marked as one nothing was sent for. */
export type Unsent = Extract<Result<never>, { ok: false }> & { unsent: "session" | "quote" };

export interface SendCapture {
  readonly now: () => SendNow;
  readonly client: QdClient | null;
  readonly subject: string;
  readonly quoteId: string;
  readonly epoch: number;
  /** True for a write that belongs to its own quote wherever the person goes (an unsaved edit sent on leaving) or to no quote (the layout): only the session is checked. */
  readonly follows: boolean;
  /** Where the person can see the unsent edit; said only when the same person is still on the page. */
  readonly label: string;
  readonly say: (line: string) => void;
}

export interface CaptureOptions {
  /** The quote the write was asked on: its id and epoch are checked at send time unless `follows`. */
  quote?: QuoteContext;
  follows?: boolean;
  /** The words naming the quote (its number) for the refusal line. */
  label?: string;
  say?: (line: string) => void;
}

/** Take down the session and the quote when the person acts. */
export function captureSend(now: () => SendNow, options: CaptureOptions = {}): SendCapture {
  const here = now();
  return {
    now,
    client: here.client,
    subject: here.client?.callerId() ?? "",
    quoteId: options.quote?.id ?? "",
    epoch: options.quote?.epoch ?? 0,
    follows: options.follows === true || options.quote === undefined,
    label: options.label ?? "",
    say: options.say ?? (() => undefined),
  };
}

/** True when the answer is the sender's own refusal: nothing went to the server. */
export function wasUnsent(result: Result<unknown>): result is Unsent {
  return !result.ok && "unsent" in result;
}

function unsent(why: "session" | "quote"): Unsent {
  return { ok: false, error: NOT_SENT, unsent: why };
}

const NOT_SENT = "This change was not sent, because the quote or the session it was made on is no longer the one on screen.";

/**
 * Send one deferred write, or refuse. The session check: the same client, still attached, the same signed in person.
 * The quote check: the quote context now is the one captured (same id, same epoch, not left). A refusal sends nothing;
 * when it is the quote that moved and the same person is still on the page, it says where the unsent edit is.
 */
export async function deferredSend<T>(capture: SendCapture, send: () => Promise<Result<T>>): Promise<Result<T>> {
  const now = capture.now();
  const sameSession = now.client === capture.client && now.attached && (now.client?.callerId() ?? "") === capture.subject;
  if (!sameSession) return unsent("session");
  const sameQuote = capture.follows || (now.quote !== null && now.quote.id === capture.quoteId && now.quote.epoch === capture.epoch && !now.quote.stale);
  if (!sameQuote) {
    if (capture.label !== "") capture.say(`Your change to ${capture.label} was not sent, because you moved to another quote first. Open ${capture.label} and make it again.`);
    return unsent("quote");
  }
  return send();
}
