// The transport the shells inject: the generated syncPush and syncPull over an
// ApiClient. Kept apart from transport.ts so the store's core never loads the
// client runtime it is handed.
import { sync, type ApiCallOptions, type ApiClient } from "@hardscapehouse/api-client";
import type { SyncTransport } from "./transport.js";

export function apiClientTransport(client: ApiClient, call?: ApiCallOptions): SyncTransport {
  return {
    push: (scope, ops) => sync.syncPush(client, scope, { ops }, call),
    pull: (scope, since, limit) => sync.syncPull(client, scope, { since, limit }, call),
  };
}
