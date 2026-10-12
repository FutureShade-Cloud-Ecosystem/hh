// The injected transport: how the outbox pushes and the store pulls. The
// shell hands in one built on the api-client (transport-api.ts); the tests
// hand in the fake server's. A refusal or a transport failure arrives as a
// Result error, never a throw: an error with no status never reached the
// server (offline), a 401 is an expired session, and any other status is an
// operational fault.
import type { Result, SyncPullResponse, SyncPushOperation, SyncPushResponse } from "@hardscapehouse/api-client";

export interface SyncTransport {
  push(scope: string, ops: SyncPushOperation[]): Promise<Result<SyncPushResponse>>;
  pull(scope: string, since: number, limit: number): Promise<Result<SyncPullResponse>>;
}

export type TransportFailure =
  | { kind: "offline"; message: string }
  | { kind: "auth_expired"; message: string }
  | { kind: "fault"; message: string; status: number };

/** Sort a failed Result: no status is offline, 401 is auth expired, anything else is a fault. */
export function classifyFailure(error: string, status?: number): TransportFailure {
  if (status === undefined) return { kind: "offline", message: error };
  if (status === 401) return { kind: "auth_expired", message: error };
  return { kind: "fault", message: error, status };
}
