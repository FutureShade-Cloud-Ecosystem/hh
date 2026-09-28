// The Result shape gate. Two halves:
// 1. a source scan proving every generated operation's signature returns
//    Promise<Result<...>> (the scan complements the runtime half: it covers
//    all operations, not just the replayed ones);
// 2. the runtime refusal-path proof: a 4xx, a 5xx, a transport error, and a
//    malformed success body each resolve { ok: false, error } and never
//    throw, with the refusal string the contract's Error envelope carries.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import * as api from "../src/index.ts";
import { allResults, isOk, mapResult } from "../src/result.ts";
import { ROUTES } from "../src/gen/manifest.ts";
import { recorder, respond, unwrap } from "./_recorded.mjs";

const packageDir = dirname(dirname(fileURLToPath(import.meta.url)));
const genDir = join(packageDir, "src", "gen");

test("every generated operation's signature returns Promise<Result<...>>", (t) => {
  const files = readdirSync(genDir).filter((f) => f.endsWith(".ts") && f !== "index.ts" && f !== "types.ts" && f !== "descriptors.ts" && f !== "manifest.ts");
  let operations = 0;
  let returnLines = 0;
  for (const file of files) {
    // The generator's fixed layout: a function opens as `export async function
    // name(` and its signature closes as `): Promise<Result<...>> {`, so a
    // line scan is exact even when the success type itself carries braces.
    let pending = null;
    for (const line of readFileSync(join(genDir, file), "utf8").split("\n")) {
      const fn = /^export async function (\w+)\($/.exec(line);
      if (fn) {
        pending = fn[1];
        operations++;
        continue;
      }
      const ret = /^\): (.*) \{$/.exec(line);
      if (ret) {
        assert.match(ret[1], /^Promise<Result<.*>>$/, `${file}: ${pending} returns a Result`);
        pending = null;
        returnLines++;
      }
    }
    assert.equal(pending, null, `${file}: every declared operation closes its signature`);
  }
  assert.equal(operations, returnLines, "one checked return per operation");
  assert.equal(operations, ROUTES.length, "one scanned operation per manifest route");
  assert.equal(operations, 672, "the document's operation count");
});

test("every manifest route resolves to a live generated function", (t) => {
  for (const route of ROUTES) {
    const namespace = route.tag.split("-").filter(Boolean).map((seg, i) => (i === 0 ? seg : seg[0].toUpperCase() + seg.slice(1))).join("");
    const group = api[namespace];
    assert.notEqual(group, undefined, `tag namespace ${namespace} exists`);
    assert.equal(typeof group[route.operationId], "function", `${namespace}.${route.operationId} is a function`);
  }
});

test("a 4xx resolves the Error envelope's message and never throws", async (t) => {
  const rec = recorder({ "GET /api/v1/tenant/me": { status: 401, body: { error: "unauthorized" } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.error, "unauthorized");
});

test("a 5xx with an empty body resolves HTTP 500 and never throws", async (t) => {
  const fetchLike = () => Promise.resolve(respond({ status: 500 }));
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.error, "HTTP 500");
});

test("a 5xx carrying the Error envelope surfaces that message verbatim", async (t) => {
  const fetchLike = () => Promise.resolve(respond({ status: 503, body: { error: "maintenance" } }));
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.error, "maintenance");
});

test("a transport failure resolves a refusal and never throws", async (t) => {
  const fetchLike = () => Promise.reject(new Error("connection reset"));
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /transport error/);
  if (!result.ok) assert.match(result.error, /connection reset/);
});

test("a success status with a non-JSON body resolves a refusal, never garbage", async (t) => {
  const fetchLike = () => Promise.resolve({ status: 200, ok: true, text: () => Promise.resolve("<html>not json</html>") });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /not valid JSON/);
});

test("a textResponse operation resolves the raw wire string", async (t) => {
  const fetchLike = () => Promise.resolve({ status: 200, ok: true, text: () => Promise.resolve("id,name\n1,Oaks") });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const result = await api.staffExport.exportTransactionsXlsx(client);
  assert.equal(result.ok, true);
  if (isOk(result)) assert.equal(result.value, "id,name\n1,Oaks");
});

test("the Result helpers narrow, map, and collect as the convention says", (t) => {
  assert.ok(isOk(api.ok(3)));
  assert.equal(mapResult(api.ok(2), (n) => n * 10).value, 20);
  assert.equal(api.err("no").ok, false);
  assert.deepEqual(allResults([api.ok(1), api.ok(2)]), { ok: true, value: [1, 2] });
  assert.deepEqual(allResults([api.ok(1), api.err("stop")]), { ok: false, error: "stop" });
});

test("the header provider reaches the wire, merged under the operation's own headers", async (t) => {
  const rec = recorder({ "GET /api/v1/tenant/me": { body: { id: "x" } } });
  const client = new api.ApiClient({
    baseUrl: "https://hh-core.example",
    fetch: rec.fetch,
    headers: () => ({ "X-Org": "dibbits", Authorization: "Bearer caller-owned" }),
  });
  unwrap(await api.tenantAuth.tenantGetMe(client));
  const call = rec.calls[0];
  assert.equal(call.headers["X-Org"], "dibbits");
  assert.equal(call.headers["Authorization"], "Bearer caller-owned");
});

test("query serialization omits absent filters, repeats arrays, and stringifies numbers", async (t) => {
  const rec = recorder({ "GET /api/v1/dashboard/widgets/data": { body: {} } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  unwrap(await api.staffDashboard.dashboardGetWidgetData(client, { key: ["revenue", "orders"], limit: 5 }));
  const call = rec.calls[0];
  assert.equal(call.url, "https://hh-core.example/api/v1/dashboard/widgets/data?key=revenue&key=orders&limit=5");
});

test("a missing path parameter is the one loud failure: there is no request worth refusing yet", async (t) => {
  const rec = recorder({});
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  await assert.rejects(() => api.tenantOrders.tenantGetOrder(client, undefined), /path parameter id is missing/);
  assert.equal(rec.calls.length, 0);
});
