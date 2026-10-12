// Shared rig for the outbox, pull and runner suites: an in-memory store over
// node:sqlite, the Field profile's scopes, and a fake server that mimics the
// Go engine's push and pull (dedup before conflict, ordered replay, partial
// refusal, operational fault fails the push).
import { migrate } from "../src/schema.ts";
import { scopeBookFor } from "../src/scopes.ts";
import { nodeSqliteExecutor, FakeSyncServer } from "../src/testing.ts";

export const uuid = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;

export const FIELD_APPS = ["yard-pick", "drive-run"];

export async function baseRig(apps = FIELD_APPS) {
  const sql = nodeSqliteExecutor();
  const scopes = scopeBookFor(apps);
  await migrate(sql, scopes.declared());
  let tick = 0;
  const now = () => `2000-01-01T00:00:${String(++tick).padStart(2, "0")}Z`;
  let keyN = 1000;
  const newKey = () => uuid(++keyN);
  const server = new FakeSyncServer();
  // Rows 1 to 9 of the two scopes the tests update exist on the server already,
  // pick_tasks first so uuid(n) sits at server seq n.
  for (const scope of ["pick_tasks", "shortages"]) for (let n = 1; n <= 9; n++) server.seed(scope, uuid(n), { n });
  return { sql, scopes, now, newKey, server, transport: server.transport() };
}

/** An update of pick task n, based on the seq the server gave it. */
export const pick = (n, extra = {}) => ({ scope: "pick_tasks", op: "update", payload: { id: uuid(n), picked: n, ...extra }, baseSeq: n });
