// The read through: a GET that has a cached answer is served from the device
// when the network does not answer, in the very shape the app already parses,
// so the app's own code is not changed and still works. The shell is told
// (a hook) whether the last read was the device's copy or the server's, with
// how old the copy is, so the glass can say it.
//
// Only a lost network falls back: a server's refusal (403, 409, 5xx) is the
// server's word and stands. Only a GET is ever answered here; a write goes
// where it was going (and the no signal guard, composed outside this, refuses
// it with no signal before it gets here).
import type { FetchLike, RequestInitLike, ResponseLike } from "@hardscapehouse/api-client";
import type { SignalSource } from "./signal.js";

export interface CachedAnswer {
  /** The JSON body, in the shape the server's route answers. */
  body: unknown;
  /** How old the copy is, in words ("Pulled 3 min ago"). */
  words: string;
}

export interface CachedRoute {
  match(url: URL): boolean;
  /** The cached answer, or null when the device holds nothing to answer with. */
  answer(url: URL): Promise<CachedAnswer | null>;
}

export interface ReadThroughHooks {
  /** A read was answered from the device's copy. */
  onCached?(words: string): void;
  /** A read was answered by the server. */
  onLive?(): void;
}

function parse(url: string): URL {
  return new URL(url, "http://work.local");
}

function cachedResponse(answer: CachedAnswer): ResponseLike {
  const body = JSON.stringify(answer.body);
  return {
    status: 200,
    ok: true,
    text: async () => body,
    arrayBuffer: async () => new TextEncoder().encode(body).buffer as ArrayBuffer,
  };
}

async function fallback(routes: readonly CachedRoute[], url: URL, hooks: ReadThroughHooks): Promise<CachedAnswer | null> {
  const route = routes.find((r) => r.match(url));
  if (route === undefined) return null;
  const answer = await route.answer(url);
  if (answer !== null) hooks.onCached?.(answer.words);
  return answer;
}

const platformFetch: FetchLike = (url, init) => (globalThis.fetch as unknown as FetchLike)(url, init);

/** The FetchLike form, for an ApiClient. */
export function readThroughFetch(base: FetchLike | undefined, signal: SignalSource, routes: readonly CachedRoute[], hooks: ReadThroughHooks = {}): FetchLike {
  const inner = base ?? platformFetch;
  return async (url: string, init: RequestInitLike): Promise<ResponseLike> => {
    if (init.method.toUpperCase() !== "GET") return inner(url, init);
    const parsed = parse(url);
    if (!routes.some((r) => r.match(parsed))) return inner(url, init);
    if (!signal.online()) {
      const answer = await fallback(routes, parsed, hooks);
      return answer !== null ? cachedResponse(answer) : inner(url, init);
    }
    try {
      const response = await inner(url, init);
      if (response.ok) hooks.onLive?.();
      return response;
    } catch (error) {
      const answer = await fallback(routes, parsed, hooks);
      if (answer === null) throw error;
      return cachedResponse(answer);
    }
  };
}

type WebFetch = (input: string, init?: RequestInit) => Promise<Response>;

const platformWebFetch: WebFetch = (input, init) => globalThis.fetch(input, init);

/** The platform fetch form, for the ported surface's api() seam. */
export function readThroughWebFetch(base: WebFetch | undefined, signal: SignalSource, routes: readonly CachedRoute[], hooks: ReadThroughHooks = {}): WebFetch {
  const inner = base ?? platformWebFetch;
  const json = (answer: CachedAnswer): Response => new Response(JSON.stringify(answer.body), { status: 200, headers: { "Content-Type": "application/json" } });
  return async (input: string, init?: RequestInit): Promise<Response> => {
    if ((init?.method ?? "GET").toUpperCase() !== "GET") return inner(input, init);
    const parsed = parse(input);
    if (!routes.some((r) => r.match(parsed))) return inner(input, init);
    if (!signal.online()) {
      const answer = await fallback(routes, parsed, hooks);
      return answer !== null ? json(answer) : inner(input, init);
    }
    try {
      const response = await inner(input, init);
      if (response.ok) hooks.onLive?.();
      return response;
    } catch (error) {
      const answer = await fallback(routes, parsed, hooks);
      if (answer === null) throw error;
      return json(answer);
    }
  };
}
