// The recorded-fixture harness, ported from HH Pro's
// /home/colton/Desktop/hh_pro_dibbits/src/core/supplier/__tests__/recorded.ts
// (a read-only reference) to this package's transport. Same semantics: a
// route table keyed "METHOD /route" feeds a FetchLike that records every call
// (method, url, route, query, headers, body) and answers from a per-route
// queue whose last entry repeats, so a route can be called twice without the
// test having to say how many times the client happens to call it. The
// transport never touches a network: there is no dialing anywhere in the
// package's tests.

/** Build one ResponseLike off a stub, exactly the recorded harness's respond. */
export function respond(stub) {
  const status = stub.status ?? 200;
  const text = stub.body === undefined ? "" : JSON.stringify(stub.body);
  return {
    status,
    ok: status >= 200 && status < 300,
    text: () => Promise.resolve(text),
  };
}

/**
 * `METHOD /route` -> the response, or a queue of responses consumed in order.
 * An unmatched route answers the harness's recorded 404 body, as HH Pro's
 * recorder answers its error404 fixture.
 */
export function recorder(routes) {
  const calls = [];
  const queues = new Map();
  for (const [key, value] of Object.entries(routes)) {
    queues.set(key, Array.isArray(value) ? [...value] : [value]);
  }

  const fetchLike = (url, init) => {
    const parsed = new URL(url);
    const query = {};
    parsed.searchParams.forEach((value, key) => {
      query[key] = value;
    });
    calls.push({
      method: init.method,
      url,
      route: parsed.pathname,
      query,
      headers: { ...init.headers },
      body: typeof init.body === "string" ? init.body : undefined,
    });

    const queue = queues.get(`${init.method} ${parsed.pathname}`);
    if (queue === undefined || queue.length === 0) {
      return Promise.resolve(respond({ status: 404, body: { error: "not found" } }));
    }
    const next = queue.length > 1 ? queue.shift() : queue[0];
    return Promise.resolve(respond(next));
  };

  return {
    fetch: fetchLike,
    calls,
    /** Every non-GET request seen: the transport-discipline assertions. */
    writes: () => calls.filter((call) => call.method !== "GET"),
  };
}

/**
 * The recorded harness's `need` (its `x!` for tests, without the `!`):
 * throwing here fails the test at the line that was wrong.
 */
export function need(value, what = "a value") {
  if (value === null || value === undefined) throw new Error(`expected ${what}, got ${value}`);
  return value;
}

/** Unwrap a Result for the happy-path assertions, failing loudly otherwise. */
export function unwrap(result) {
  if (!result.ok) throw new Error(`expected ok, got: ${result.error}`);
  return result.value;
}
