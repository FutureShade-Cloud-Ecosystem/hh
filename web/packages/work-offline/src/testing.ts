// Test doubles for the Work offline layer, kept off the public entry (they
// read the core's Go source from disk). The fake sync server of the offline
// store answers anything; the real engine does not. The strict transport here
// refuses what the core's quotes store refuses, reading the writable sets from
// core/internal itself, so a payload that drifts from the server fails a test
// instead of passing every leg against a flattering double.
import { ok, type SyncPushOperation, type SyncPushResult } from "@hardscapehouse/api-client";
import type { SyncTransport } from "@hardscapehouse/offline-store";
import type { FakeSyncServer } from "@hardscapehouse/offline-store/testing";
import { goFieldSet } from "./gosets.mjs";

export { goFieldSet };

export interface StrictQuotes {
  transport: SyncTransport;
  /** Every `<scope> <op>: <field>` the core would have refused, in the order they were pushed. */
  offenders: string[];
}

/**
 * The fake server behind the core's quotes rules: a create or update that
 * names a field outside the Go set is refused as the core refuses it, the
 * operation alone, with the module's words; an update that declares only the
 * id moves nothing and is refused. Everything else goes to the fake server.
 */
export function strictQuotesTransport(server: FakeSyncServer): StrictQuotes {
  const inner = server.transport();
  const offenders: string[] = [];
  const create = goFieldSet("quote/syncscope.go", "quoteCreateFields");
  const update = goFieldSet("quote/syncscope.go", "quoteWriteFields");
  return {
    offenders,
    transport: {
      pull: (scope, since, limit) => inner.pull(scope, since, limit),
      async push(scope, ops) {
        if (scope !== "quotes") return inner.push(scope, ops);
        const results: SyncPushResult[] = [];
        for (const op of ops) {
          const refusal = quotesRefusal(op, create, update);
          if (refusal !== null) {
            for (const field of refusal.fields) offenders.push(`quotes ${op.op}: ${field}`);
            results.push({ client_id: op.client_id, status: "rejected", error: refusal.message });
            continue;
          }
          const answer = await inner.push(scope, [op]);
          if (!answer.ok) return answer;
          results.push(...answer.value.results);
        }
        return ok({ results });
      },
    },
  };
}

function quotesRefusal(op: SyncPushOperation, create: Set<string>, update: Set<string>): { message: string; fields: string[] } | null {
  if (op.op === "delete") return null;
  const allowed = op.op === "create" ? create : update;
  const bad = Object.keys(op.payload ?? {}).filter((k) => !allowed.has(k));
  if (bad.length > 0) return { message: `quote: field ${JSON.stringify(bad[0])} is not writable through sync`, fields: bad };
  if (op.op === "update" && !Object.keys(op.payload ?? {}).includes("valid_until")) {
    return { message: "quote: an update that moves nothing refuses", fields: [] };
  }
  return null;
}
