// The no signal guard of the Work shell. Money never queues (plan B.3, D5):
// every tender, payment, approval, price change and any other money moving
// action resolves online, and with no signal it answers one honest state and
// does nothing else. There is no outbox in this path: the guard sits in front
// of the transport, so a refused write is never stored, never retried and
// never replayed later.
//
// One rule covers an app's whole write surface because it lives in the fetch
// the app's client is built on: a write verb offline is refused before
// anything dials, a read is untouched. The sync transport (the quote draft
// outbox) is built on a separate, unguarded client; the only things that ride
// it are the scopes the core admits a client write to.
import { err, type FetchLike, type RequestInitLike, type ResponseLike, type Result } from "@hardscapehouse/api-client";

/** The words the glass shows, whichever app and whichever action. */
export const NO_SIGNAL_ERROR = "No signal. This needs the server and does not wait in a queue: try again when you are back online.";

/** Whether the device has a connection right now. */
export interface SignalSource {
  online(): boolean;
}

/** The platform's own word, read each time it is asked. */
export function browserSignal(): SignalSource {
  return { online: () => (globalThis as { navigator?: { onLine?: boolean } }).navigator?.onLine !== false };
}

/** A signal the caller sets: the tests', and a harness that cuts the network on purpose. */
export function fixedSignal(initial = true): SignalSource & { set(online: boolean): void } {
  let current = initial;
  return {
    online: () => current,
    set(online: boolean) {
      current = online;
    },
  };
}

/** The refusal as a Result. It carries no status: nothing answered. */
export function noSignal<T = never>(): Result<T> {
  return err(NO_SIGNAL_ERROR);
}

/** Whether a Result is this refusal (a server refusal and a lost answer are not). */
export function isNoSignal(result: Result<unknown>): boolean {
  return !result.ok && result.error === NO_SIGNAL_ERROR;
}

/**
 * Run a money moving action only with a signal. Offline, the action does not
 * run at all. Online, it runs once and its answer comes back as it is: a
 * server refusal, or an answer lost in flight, is the app's own to word, since
 * nothing says a write that was sent did not land.
 */
export async function onlineOnly<T>(signal: SignalSource, action: () => Promise<Result<T>>): Promise<Result<T>> {
  if (!signal.online()) return noSignal<T>();
  return action();
}

const READ_VERBS = new Set(["GET", "HEAD", "OPTIONS"]);

function refusedResponse(): ResponseLike {
  const body = JSON.stringify({ error: NO_SIGNAL_ERROR });
  return {
    status: 0,
    ok: false,
    text: async () => body,
    arrayBuffer: async () => new TextEncoder().encode(body).buffer as ArrayBuffer,
  };
}

const platformFetch: FetchLike = (url, init) => (globalThis.fetch as unknown as FetchLike)(url, init);

/**
 * A fetch for an app's client: a write verb with no signal is answered as the
 * refusal above, with status 0 and the shared envelope, so the api client
 * hands every app a Result whose error is exactly NO_SIGNAL_ERROR.
 */
export function noSignalFetch(base: FetchLike | undefined, signal: SignalSource): FetchLike {
  const inner = base ?? platformFetch;
  return async (url: string, init: RequestInitLike): Promise<ResponseLike> => {
    if (!READ_VERBS.has(init.method.toUpperCase()) && !signal.online()) return refusedResponse();
    return inner(url, init);
  };
}
