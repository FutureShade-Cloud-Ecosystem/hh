// The tenant client: an ApiClient whose header provider is the auth package's
// tenant one (Authorization while a session is live, and never X-Org: the core
// answers 400 to an X-Org on a tenant route). It reads the session's provider,
// so a sign out takes the bearer away from every client already built.
import { ApiClient, type FetchLike } from "@hardscapehouse/api-client";
import type { TenantSession } from "./session.js";

export interface TenantClientOptions {
  /** The base URL the tenant routes answer on; empty for same origin. */
  baseUrl?: string;
  fetch?: FetchLike;
}

export function tenantClient(session: TenantSession, options: TenantClientOptions = {}): ApiClient {
  return new ApiClient({ baseUrl: options.baseUrl ?? "", fetch: options.fetch, headers: session.headers });
}
