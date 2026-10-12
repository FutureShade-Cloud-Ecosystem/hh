// The injected transport: how the outbox pushes and the store pulls. The
// shell hands in one built on the api-client (transport-api.ts); the tests
// hand in the fake server's. A refusal or a transport failure arrives as a
// Result error, never a throw: an error with no status never reached the
// server (offline), a 401 is an expired session, a 400, 403, 404, 413 or 501
// is the server's standing verdict on that request (the same answer comes on
// every retry, so retrying whole can never help), and any other status is an
// operational fault.
import type { Result, SyncPullResponse, SyncPushOperation, SyncPushResponse } from "@hardscapehouse/api-client";

export interface SyncTransport {
  push(scope: string, ops: SyncPushOperation[]): Promise<Result<SyncPushResponse>>;
  pull(scope: string, since: number, limit: number): Promise<Result<SyncPullResponse>>;
}

export type TransportFailure =
  | { kind: "offline"; message: string }
  | { kind: "auth_expired"; message: string }
  | { kind: "refused"; message: string; status: number }
  | { kind: "fault"; message: string; status: number };

/** The statuses the core answers a whole request with, deterministically (core/internal/sync/handler.go). */
export const REFUSAL_STATUSES: readonly number[] = [400, 403, 404, 413, 501];

/** Sort a failed Result: no status is offline, 401 is auth expired, a standing refusal is refused, anything else is a fault. */
export function classifyFailure(error: string, status?: number): TransportFailure {
  if (status === undefined) return { kind: "offline", message: error };
  if (status === 401) return { kind: "auth_expired", message: error };
  if (REFUSAL_STATUSES.includes(status)) return { kind: "refused", message: error, status };
  return { kind: "fault", message: error, status };
}
