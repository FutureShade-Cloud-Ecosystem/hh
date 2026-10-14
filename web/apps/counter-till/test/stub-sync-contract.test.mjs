// The Counter Till stub's sync surface against the core's own registry. The
// till is read cache only (manifest mode read_cache_only, plan D5): its sync
// scopes are pull only, the money scopes refuse every push for every caller,
// and no scope the till holds admits a client write. The stub serves the
// read feeds in the core's projections and refuses every POST the way the
// engine does; the registry rows it leans on are read from the Go source, so
// the stub cannot call a scope writable that the core calls pull only.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const registry = readFileSync(join(appDir, "..", "..", "..", "core", "internal", "sync", "registry.go"), "utf8");
let child;
let base;
let headers;

/** The till's declared scopes (manifest.yaml, counter-till), pinned by work-offline's own manifest test. */
const TILL_SCOPES = ["customers", "products", "price_matrix", "parked_tickets", "invoices", "warranty_claims"];

async function freePort() {
  return new Promise((resolve) => {
    const probe = createServer();
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

before(async () => {
  const port = await freePort();
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [join(appDir, "test", "stub", "demo-stub.mjs")], { env: { CT_STUB_PORT: String(port) }, stdio: "ignore" });
  for (let i = 0; i < 100; i += 1) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) break;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  const session = await (await fetch(`${base}/api/v1/demo/session`, { method: "POST", headers: { "Content-Type": "application/json", "X-Org": "hh-demo" }, body: JSON.stringify({ flow: "sales" }) })).json();
  headers = { Authorization: `Bearer ${session.token}`, "X-Org": "hh-demo", "Content-Type": "application/json" };
});

after(() => {
  child?.kill();
});

const pull = async (scope, since = 0) => {
  const res = await fetch(`${base}/api/v1/sync/${scope}?since=${since}&limit=200`, { headers });
  return { status: res.status, json: await res.json() };
};
const push = async (scope, ops) => {
  const res = await fetch(`${base}/api/v1/sync/${scope}`, { method: "POST", headers, body: JSON.stringify({ ops }) });
  return { status: res.status, json: await res.json() };
};

/** The registry row of one scope: its policy word and whether it is marked money. */
function registryRow(scope) {
  const line = registry.split("\n").find((l) => l.includes(`"${scope}":`) && l.includes("Policy:"));
  assert.ok(line, `${scope} is in the registry`);
  return { pullOnly: /PolicyPullOnly/.test(line), money: /Money:\s*true/.test(line) };
}

test("every scope the till holds is served as a pull feed in the core's projection", async () => {
  for (const scope of TILL_SCOPES) {
    const { status, json } = await pull(scope);
    assert.equal(status, 200, scope);
    assert.equal(json.scope, scope);
    assert.ok(Array.isArray(json.items) && Array.isArray(json.tombstones), scope);
    assert.equal(typeof json.last_seq, "number", scope);
  }
  const products = (await pull("products")).json.items[0].data;
  for (const key of ["id", "sku", "name", "description", "manufacturer", "collection", "sold_by", "base_uom", "is_special_order", "is_active", "lead_time_days"]) assert.ok(key in products, `products.${key}`);
  const customers = (await pull("customers")).json.items[0].data;
  for (const key of ["id", "account_no", "name", "home_branch_id", "customer_type", "is_active"]) assert.ok(key in customers, `customers.${key}`);
  const band = (await pull("price_matrix")).json.items[0].data;
  for (const key of ["id", "list_price_cents", "price_uom"]) assert.ok(key in band, `price_matrix.${key}`);
  const parked = (await pull("parked_tickets")).json;
  assert.ok(parked.items.length >= 1);
  for (const key of ["id", "ticket_no", "branch_id", "customer_id", "sale_type", "notes", "status", "lines", "created_at", "updated_at"]) assert.ok(key in parked.items[0].data, `parked_tickets.${key}`);
});

test("a pull past the last seq is empty", async () => {
  const all = (await pull("products")).json;
  const after = (await pull("products", all.last_seq)).json;
  assert.deepEqual(after.items, []);
});

test("no scope the till holds admits a push: money scopes are refused to every caller, the rest are pull only, as the core's registry says", async () => {
  for (const scope of TILL_SCOPES) {
    const row = registryRow(scope);
    const { status, json } = await push(scope, [{ client_id: "00000000-0000-4000-8000-000000000001", op: "update", payload: { id: "00000000-0000-4000-8000-000000000002" } }]);
    if (row.money) {
      assert.equal(status, 403, `${scope} is money in the registry`);
    } else {
      assert.ok(row.pullOnly, `${scope} is pull only in the registry`);
      assert.equal(status, 400, scope);
      assert.match(json.error, /is pull-only/);
    }
  }
});

test("the stub counts every push the till makes, and an honest till makes none", async () => {
  const log = await (await fetch(`${base}/_stub/sync-log`)).json();
  assert.ok(log.pulls.length > 0);
  assert.ok(log.pushes.length >= TILL_SCOPES.length, "the probes above are the only pushes, and each was refused");
  for (const entry of log.pushes) assert.equal(entry.status === 400 || entry.status === 403, true);
});

test("an unknown scope is refused as the engine refuses it", async () => {
  const { status, json } = await pull("no_such_scope");
  assert.equal(status, 400);
  assert.match(json.error, /unknown sync scope/);
});
