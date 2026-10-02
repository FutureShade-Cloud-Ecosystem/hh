// Ported from hardscapeos_dibbits master 87d7941b app/src/_response-shape.ts; adapted for HH.

// _response-shape.ts: DIB-471: an unexpected response SHAPE is handled at the EDGE.
//
// WHAT THIS IS FOR
// ----------------
// Five surfaces (admin/staff, sales/catalogue, finance/gl, sales/quote, admin/account) were driven
// with one manufactured body on a 200:
//
// { items: [], results: [], rows: [], data: [], lines: [], entries: [], total: 0, count: 0 }
//
//: an OBJECT where the loaders expected an ARRAY. `admin/staff` threw `this.roles.map is not a
// function` mid-render and sat on "Loading staff…" with no way back. `sales/catalogue` caught the
// resulting TypeError in the same try block that catches a network failure and painted
// "Could not reach the API": an error, on a successful response.
//
// Whether that particular body is realistic is beside the point. A 2xx whose body is not the shape
// the caller expected is a thing that happens (a proxy that rewrites, a version skew, a partial
// rollout, a cache serving an older envelope), and there are exactly two honest reactions:
//
// the body PARSES but is not the expected shape -> EMPTY. Nothing was in it. Empty is never an
// error, so this must not paint role="alert".
// the body does not parse, or the status is not ok -> FAULT. Nothing was read. That IS an error
// and it must still say so, loudly.
//
// This module is the first half. It is deliberately DOM-free and imports nothing from the app, per
// the house convention for `_*.ts` helper modules (_catalogue.ts, _pos.ts, _quote-edit.ts), so it is
// unit-testable without a browser: see _response-shape.test.mjs.
//
// WHY NOT A GUARD IN render()
// ---------------------------
// `Array.isArray(this.rows) ? this.rows.map(...) : nothing` in the template would also stop the
// crash, and it would put the check in the wrong place: every render, every consumer, one forgotten
// call site away from the same defect. The shape is a property of the RESPONSE, so it is settled
// once where the response is read, and the component's state fields are arrays by construction.

/**
 * The envelope keys this app actually uses to wrap a collection, in the order they are tried.
 * `items` first because that is the paged envelope (`gl-api.ts Page<T>`); the rest are the shapes
 * the W6-D sweep's synthetic body and the older handlers use.
 */
export const ENVELOPE_KEYS = ['items', 'results', 'rows', 'data', 'entries', 'lines'] as const;

/**
 * Normalise a parsed JSON body into the array of rows a list surface wants to render.
 *
 * @param body whatever `res.json()` produced. Any type: that is the point.
 * @param keys envelope keys to look inside, most specific first. A caller that knows its own
 * envelope name passes it ahead of the defaults (`['staff',...ENVELOPE_KEYS]`).
 *
 * Returns the rows if the body is an array, or the first envelope key whose value is an array;
 * otherwise an EMPTY array. Never throws, and never signals a fault: a body it does not recognise
 * is a body with nothing in it, which is the empty state and not the error state.
 *
 * Note the deliberate asymmetry with `null`/`undefined`: `[]`, again. A handler that answers 204 or
 * `null` for "no rows" is answering the question, and turning that into an error banner would be the
 * same lie in a different costume.
 */
export function asRows<T>(body: unknown, keys: readonly string[] = ENVELOPE_KEYS): T[] {
  if (Array.isArray(body)) return body as T[];
  if (body && typeof body === 'object') {
    const rec = body as Record<string, unknown>;
    for (const key of keys) {
      if (Array.isArray(rec[key])) return rec[key] as T[];
    }
  }
  return [];
}
