/**
 * Ported from HH Pro's Result convention:
 * /home/colton/Desktop/hh_pro_dibbits/src/core/lib/result.ts (a read-only
 * reference tree; only the convention is ported, with its rationale). It is a
 * convention, not a wire type.
 *
 * Actions return Result rather than throwing. Two reasons this matters there:
 * the UI needs to show a guard's reason on a rejected board drop, and the AI
 * tool layer needs that same string verbatim as a tool_result so the model can
 * self-correct. Exceptions would lose that symmetry. The api-client keeps the
 * same shape for the same reasons: a transport failure, a refused request, and
 * a domain refusal all reach the caller identically, as data.
 */

export type Result<T> = { ok: true; value: T } | { ok: false; error: string; status?: number; code?: string };

/**
 * The client code on the error leg for a 2xx answer whose body is not valid JSON. It carries no status (the server
 * answered success), so a caller that reads the status alone would take it for a lost signal; this code tells the
 * two apart. It is a client code, never a server one.
 */
export const MALFORMED_SUCCESS_CODE = "malformed_success_body";

export function ok<T>(value: T): Result<T> {
  return { ok: true, value };
}

/**
 * The error leg. status rides only on an HTTP-status refusal (a 4xx or 5xx
 * response): it is the status the server answered. A transport failure, an
 * aborted or timed-out request, a failed header provider, and a malformed
 * success body carry no status, because either nothing answered or what
 * answered answered success.
 */
export function err<T = never>(error: string, status?: number, code?: string): Result<T> {
  const base = status === undefined ? { ok: false as const, error } : { ok: false as const, error, status };
  return code === undefined ? base : { ...base, code };
}

export function isOk<T>(result: Result<T>): result is { ok: true; value: T } {
  return result.ok;
}

export function mapResult<T, U>(result: Result<T>, fn: (value: T) => U): Result<U> {
  return result.ok ? ok(fn(result.value)) : result;
}

/** Collect many results, failing on the first error. */
export function allResults<T>(results: readonly Result<T>[]): Result<T[]> {
  const values: T[] = [];
  for (const result of results) {
    if (!result.ok) return result;
    values.push(result.value);
  }
  return ok(values);
}
