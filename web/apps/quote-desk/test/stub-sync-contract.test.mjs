// The Quote Desk stub's sync surface against the core's own rules.
//
// The Field stubs drifted once: they admitted fields the core refuses (and the
// app sent them), so every leg passed against a server the core is not. This
// test reads the Go sets (core/internal/quote/syncscope.go, through the work
// offline package's reader) and probes the stub with every name in the union
// of the sets and the names the desk could be tempted to send: a name is
// refused as "not writable through sync" exactly when the core's set for that
// op does not hold it. It also pins the engine behaviours the offline legs
// lean on: dedup before anything else, a draft only deleted, the per field
// merge envelope, a money scope refused for every caller, a pull only scope
// refused its POST.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { goFieldSet } from "@hardscapehouse/work-offline/gosets";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
let child;
let base;
let headers;

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
  child = spawn(process.execPath, [join(appDir, "test", "stub", "qd-stub.mjs")], { env: { QD_STUB_PORT: String(port) }, stdio: "ignore" });
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

const POINT_LOMA = "6f1c2b3a-0000-4000-8000-000000000001";
const MAPLE = "c0000000-0000-4000-9000-000000000001";
const NOT_WRITABLE = /is not writable through sync/;

let n = 0;
const uuid = () => `0d000000-0000-4000-9000-${String(++n).padStart(12, "0")}`;

async function pull(scope, since = 0) {
  const res = await fetch(`${base}/api/v1/sync/${scope}?since=${since}&limit=200`, { headers });
  return { status: res.status, json: await res.json() };
}

async function push(scope, ops) {
  const res = await fetch(`${base}/api/v1/sync/${scope}`, { method: "POST", headers, body: JSON.stringify({ ops }) });
  return { status: res.status, json: await res.json() };
}

async function one(scope, op, payload, extra = {}) {
  const client_id = uuid();
  const { status, json } = await push(scope, [{ client_id, op, payload, ...extra }]);
  return { status, result: json.results?.[0] ?? json, client_id };
}

const createSet = () => goFieldSet("quote/syncscope.go", "quoteCreateFields");
const updateSet = () => goFieldSet("quote/syncscope.go", "quoteWriteFields");

const PROBE_EXTRAS = ["status", "quote_no", "subtotal_cents", "tax_cents", "total_cents", "created_at", "customer_name", "lines", "discount", "terms", "salesperson_id", "owner_id", "deposit_pct", "note", "deleted"];

const universe = () => {
  const all = new Set([...createSet(), ...updateSet(), ...PROBE_EXTRAS]);
  all.delete("id");
  return [...all].sort();
};

test("quotes create: the stub admits exactly the core's writable fields", async () => {
  const set = createSet();
  for (const field of universe()) {
    const { result } = await one("quotes", "create", { id: uuid(), customer_id: MAPLE, branch_id: POINT_LOMA, [field]: "x" });
    const refused = result.status === "rejected" && NOT_WRITABLE.test(String(result.error ?? ""));
    assert.equal(refused, !set.has(field), `create: field ${field} should be ${set.has(field) ? "admitted" : "refused"} (answer: ${JSON.stringify(result)})`);
  }
});

test("quotes update: the stub admits exactly the core's writable fields", async () => {
  const id = uuid();
  const made = await one("quotes", "create", { id, customer_id: MAPLE, branch_id: POINT_LOMA });
  assert.equal(made.result.status, "applied");
  const set = updateSet();
  for (const field of universe()) {
    const { result } = await one("quotes", "update", { id, [field]: "x" });
    const refused = result.status === "rejected" && NOT_WRITABLE.test(String(result.error ?? ""));
    assert.equal(refused, !set.has(field), `update: field ${field} should be ${set.has(field) ? "admitted" : "refused"} (answer: ${JSON.stringify(result)})`);
  }
});

test("a created draft lands as a DRAFT with a server minted number, the caller's own, and shows in a pull with no money legs of the client's", async () => {
  const id = uuid();
  const { result } = await one("quotes", "create", { id, customer_id: MAPLE, branch_id: POINT_LOMA, valid_until: "2999-12-31" });
  assert.equal(result.status, "applied");
  assert.equal(typeof result.seq, "number");
  const row = (await pull("quotes")).json.items.find((item) => item.id === id);
  assert.ok(row, "the draft is in the quotes feed");
  assert.equal(row.data.status, "DRAFT");
  assert.match(row.data.quote_no, /^Q-\d+$/);
  assert.equal(row.data.customer_id, MAPLE);
  assert.equal(row.data.subtotal_cents, 0);
});

test("the same idempotency key twice makes one quote and answers the same seq", async () => {
  const client_id = uuid();
  const id = uuid();
  const first = (await push("quotes", [{ client_id, op: "create", payload: { id, customer_id: MAPLE, branch_id: POINT_LOMA } }])).json.results[0];
  const second = (await push("quotes", [{ client_id, op: "create", payload: { id, customer_id: MAPLE, branch_id: POINT_LOMA } }])).json.results[0];
  assert.equal(first.status, "applied");
  assert.equal(second.status, "applied");
  assert.equal(second.seq, first.seq);
  assert.equal((await pull("quotes")).json.items.filter((item) => item.id === id).length, 1);
});

test("a create names a customer and a branch the org holds, or is refused", async () => {
  assert.equal((await one("quotes", "create", { id: uuid(), customer_id: "not-a-uuid", branch_id: POINT_LOMA })).result.status, "rejected");
  assert.equal((await one("quotes", "create", { id: uuid(), customer_id: MAPLE, branch_id: "not-a-uuid" })).result.status, "rejected");
  assert.equal((await one("quotes", "create", { id: uuid(), customer_id: "c0000000-0000-4000-9000-0000000000ff", branch_id: POINT_LOMA })).result.status, "rejected");
  assert.equal((await one("quotes", "create", { id: uuid(), customer_id: MAPLE, branch_id: "6f1c2b3a-0000-4000-8000-0000000000ff" })).result.status, "rejected");
  assert.equal((await one("quotes", "create", { id: uuid(), customer_id: MAPLE, branch_id: POINT_LOMA, valid_until: "31/12/2999" })).result.status, "rejected");
});

test("an update that declares only the id moves nothing and is refused", async () => {
  const id = uuid();
  await one("quotes", "create", { id, customer_id: MAPLE, branch_id: POINT_LOMA });
  const { result } = await one("quotes", "update", { id });
  assert.equal(result.status, "rejected");
  assert.match(result.error, /moves nothing/);
});

test("a stale update merges by field: applied, with the envelope the engine writes", async () => {
  const id = uuid();
  const created = await one("quotes", "create", { id, customer_id: MAPLE, branch_id: POINT_LOMA });
  const baseSeq = created.result.seq;
  const desk = await one("quotes", "update", { id, valid_until: "2999-09-09" });
  assert.equal(desk.result.status, "applied");
  const mine = await one("quotes", "update", { id, valid_until: "2999-06-30" }, { base_seq: baseSeq });
  assert.equal(mine.result.status, "applied");
  assert.equal(mine.result.conflict.policy, "last_write_wins_by_field");
  assert.equal(mine.result.conflict.applied, true);
  const row = (await pull("quotes")).json.items.find((item) => item.id === id);
  assert.equal(String(row.data.valid_until).slice(0, 10), "2999-06-30");
});

test("only a draft is withdrawn through sync, and the withdrawal reaches the feed as a tombstone", async () => {
  const draft = uuid();
  await one("quotes", "create", { id: draft, customer_id: MAPLE, branch_id: POINT_LOMA });
  const gone = await one("quotes", "delete", { id: draft });
  assert.equal(gone.result.status, "applied");
  const feed = (await pull("quotes")).json;
  assert.ok(feed.tombstones.some((t) => t.id === draft));
  assert.ok(!feed.items.some((item) => item.id === draft));
  const sent = (await pull("quotes")).json.items.find((item) => item.data.status === "SENT");
  if (sent !== undefined && /^[0-9a-f-]{36}$/.test(sent.id)) {
    const refused = await one("quotes", "delete", { id: sent.id });
    assert.equal(refused.result.status, "rejected");
  }
});

test("a money scope is refused for every caller, and a pull only scope is refused its POST", async () => {
  const money = await push("price_matrix", [{ client_id: uuid(), op: "update", payload: { id: uuid(), list_price_cents: 1 } }]);
  assert.equal(money.status, 403);
  const pullOnly = await push("customers", [{ client_id: uuid(), op: "update", payload: { id: MAPLE } }]);
  assert.equal(pullOnly.status, 400);
  assert.match(pullOnly.json.error, /is pull-only/);
  const unknown = await push("no_such_scope", [{ client_id: uuid(), op: "create", payload: {} }]);
  assert.equal(unknown.status, 400);
  assert.match(unknown.json.error, /unknown sync scope/);
});

test("the read scopes of the desk are served in the core's projections", async () => {
  const products = (await pull("products")).json;
  assert.equal(products.scope, "products");
  assert.ok(products.items.length >= 3);
  for (const key of ["id", "sku", "name", "description", "manufacturer", "collection", "sold_by", "base_uom", "is_special_order", "is_active", "lead_time_days"]) assert.ok(key in products.items[0].data, `products.${key}`);
  const customers = (await pull("customers")).json;
  for (const key of ["id", "account_no", "name", "home_branch_id", "customer_type", "is_active"]) assert.ok(key in customers.items[0].data, `customers.${key}`);
  const matrix = (await pull("price_matrix")).json;
  for (const key of ["id", "list_price_cents", "price_uom"]) assert.ok(key in matrix.items[0].data, `price_matrix.${key}`);
  const quotes = (await pull("quotes")).json;
  for (const key of ["id", "quote_no", "customer_id", "status", "valid_until", "subtotal_cents", "tax_cents", "total_cents", "created_at"]) assert.ok(key in quotes.items[0].data, `quotes.${key}`);
});

test("a pull past the last seq is empty, and the cursor never moves backwards", async () => {
  const all = (await pull("quotes")).json;
  const after = (await pull("quotes", all.last_seq)).json;
  assert.deepEqual(after.items, []);
  assert.equal(after.last_seq, all.last_seq);
});
