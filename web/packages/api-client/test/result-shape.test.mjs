// The Result shape gate. Two halves:
// 1. a source scan proving every generated operation's signature returns
//    Promise<Result<...>> (the scan complements the runtime half: it covers
//    all operations, not just the replayed ones);
// 2. the runtime refusal-path proof: a 4xx, a 5xx, a transport error, a
//    malformed success body, an unusable path parameter, a failed header
//    provider, an aborted and a timed-out request each resolve
//    { ok: false, error } and never throw or reject; the HTTP-status refusals
//    carry that status on the Result.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import * as api from "../src/index.ts";
import { allResults, isOk, mapResult } from "../src/result.ts";
import { BODY_VARIANTS, ROUTES, TAG_NAMESPACES } from "../src/gen/manifest.ts";
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
  const emitted = ROUTES.filter((r) => !r.excluded);
  const variants = Object.values(BODY_VARIANTS).flat();
  assert.equal(operations, returnLines, "one checked return per operation");
  assert.equal(
    operations,
    emitted.length + variants.length,
    "one scanned function per emitted manifest route and per declared body variant",
  );
  assert.equal(operations, 699, "the document's operation count minus the excluded stream, plus the declared body variant");
  assert.equal(emitted.length, 698, "the document's operation count minus the excluded stream");
  assert.deepEqual(variants, ["feedbackCreateMultipart"], "the only multi content type body is feedbackCreate's multipart form");
  assert.equal(ROUTES.length, 699, "the manifest census keeps the full route table");
});

test("each declared body variant resolves to a live generated function beside its operation", (t) => {
  for (const [operationId, names] of Object.entries(BODY_VARIANTS)) {
    const route = ROUTES.find((r) => r.operationId === operationId);
    assert.notEqual(route, undefined, `${operationId} is a manifest route`);
    const group = api[TAG_NAMESPACES[route.tag]];
    assert.equal(typeof group[operationId], "function", `the primary ${operationId} is a function`);
    for (const name of names) {
      assert.equal(typeof group[name], "function", `${TAG_NAMESPACES[route.tag]}.${name} is a function`);
    }
  }
});

test("the only excluded route is the event stream, and it is absent from the surface", (t) => {
  const excluded = ROUTES.filter((r) => r.excluded);
  assert.deepEqual(
    excluded.map((r) => r.operationId),
    ["messagingStream"],
    "messagingStream is the one live event stream, excluded from the generated surface",
  );
  const group = api[TAG_NAMESPACES["staff-messaging"]];
  assert.equal(group.messagingStream, undefined, "no generated function for the excluded stream");
  assert.equal(typeof group.messagingMintStreamTicket, "function", "the stream ticket mint stays generated");
});

test("every manifest route resolves to a live generated function under the generated namespace map", (t) => {
  for (const route of ROUTES) {
    if (route.excluded) continue;
    const namespace = TAG_NAMESPACES[route.tag];
    assert.notEqual(namespace, undefined, `tag namespace for ${route.tag} exists in TAG_NAMESPACES`);
    const group = api[namespace];
    assert.notEqual(group, undefined, `tag namespace ${namespace} exists`);
    assert.equal(typeof group[route.operationId], "function", `${namespace}.${route.operationId} is a function`);
  }
});

test("the generated namespace map renames the reserved tag exactly", (t) => {
  assert.equal(TAG_NAMESPACES.public, "publicApi", "the document's public tag maps to publicApi");
  assert.notEqual(api.publicApi, undefined, "publicApi is on the generated surface");
});

test("a 4xx resolves the Error envelope's message with the status, and never throws", async (t) => {
  const rec = recorder({ "GET /api/v1/tenant/me": { status: 401, body: { error: "unauthorized" } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.error, "unauthorized");
    assert.equal(result.status, 401, "the HTTP status rides the error Result");
  }
});

test("a 5xx with an empty body resolves HTTP 500 with the status and never throws", async (t) => {
  const fetchLike = () => Promise.resolve(respond({ status: 500 }));
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.error, "HTTP 500");
    assert.equal(result.status, 500);
  }
});

test("a 5xx carrying the Error envelope surfaces that message verbatim", async (t) => {
  const fetchLike = () => Promise.resolve(respond({ status: 503, body: { error: "maintenance" } }));
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.error, "maintenance");
});

test("a transport failure resolves a refusal with no status and never throws", async (t) => {
  const fetchLike = () => Promise.reject(new Error("connection reset"));
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.match(result.error, /transport error/);
    assert.match(result.error, /connection reset/);
    assert.equal(result.status, undefined, "nothing answered, so no status rides the Result");
  }
});

test("a success status with a non-JSON body resolves a refusal, never garbage", async (t) => {
  const fetchLike = () => Promise.resolve({ status: 200, ok: true, text: () => Promise.resolve("<html>not json</html>") });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /not valid JSON/);
});

// The old pin here answered CSV text and asserted the raw string, which hid
// the binary corruption: the route's success content is format: binary (an
// xlsx workbook), so the client resolves bytes. The assertion is byte exact
// against a real workbook's leading bytes: the ZIP local file header every
// xlsx file starts with (a minimal PNG fixture gets the same treatment in
// test/binary.test.mjs).
const XLSX_LEAD = Uint8Array.from([
  0x50, 0x4b, 0x03, 0x04, // "PK\x03\x04", the ZIP local file header
  0x14, 0x00, 0x06, 0x00, 0x08, 0x00, 0x00, 0x00, 0x21, 0x00, 0xd2, 0x35, 0x9d, 0x31,
]);

test("a binaryResponse operation resolves the wire bytes byte for byte", async (t) => {
  const fetchLike = () => Promise.resolve(respond({ bytes: XLSX_LEAD }));
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const result = await api.staffExport.exportTransactionsXlsx(client);
  assert.equal(result.ok, true);
  if (isOk(result)) {
    assert.ok(result.value instanceof Uint8Array, "the value is bytes");
    assert.deepEqual(result.value, XLSX_LEAD, "byte for byte what the wire carried");
  }
});

// The text response path (the plain string formats: text/csv, text/html)
// resolves the wire string verbatim, never a JSON parse of it. One replay per
// declared format.
test("a text/html success resolves the raw wire text", async (t) => {
  const receipt = "<html><body>receipt for sale r-1</body></html>";
  const rec = recorder({ "GET /api/v1/pos/sales/r-1/receipt": { text: receipt } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  const value = unwrap(await api.staffPrinting.printingGetReceipt(client, "r-1"));
  assert.equal(value, receipt, "the receipt HTML, verbatim");
});

test("a text/csv success resolves the raw wire text", async (t) => {
  const csv = "invoice_id,total_cents\nINV-1,101700\n";
  const rec = recorder({ "GET /api/v1/ar/register/export": { text: csv } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  const value = unwrap(await api.staffBilling.billingExportRegister(client));
  assert.equal(value, csv, "the register CSV, verbatim");
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

// The unusable path parameters: each resolves an error Result naming the
// parameter, and nothing is dialed, because there is no request worth
// refusing yet.
test("a missing path parameter resolves an error Result and dials nothing", async (t) => {
  const rec = recorder({});
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  const result = await api.tenantOrders.tenantGetOrder(client, undefined);
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /path parameter id is missing/);
  assert.equal(rec.calls.length, 0);
});

test("an empty path parameter value resolves an error Result and dials nothing", async (t) => {
  const rec = recorder({});
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  const result = await api.tenantOrders.tenantGetOrder(client, "");
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /path parameter id is empty/);
  assert.equal(rec.calls.length, 0);
});

test("a dot-segment path parameter resolves an error Result and dials nothing", async (t) => {
  for (const segment of [".", ".."]) {
    const rec = recorder({});
    const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
    const result = await api.tenantOrders.tenantGetOrder(client, segment);
    assert.equal(result.ok, false);
    if (!result.ok) assert.match(result.error, new RegExp(`path parameter id is the path segment ${segment.replaceAll(".", "\\.")}$`));
    assert.equal(rec.calls.length, 0, `${segment} never reaches the wire`);
  }
});

test("a header provider that throws resolves an error Result and never throws", async (t) => {
  const rec = recorder({});
  const client = new api.ApiClient({
    baseUrl: "https://hh-core.example",
    fetch: rec.fetch,
    headers: () => {
      throw new Error("the token reader is down");
    },
  });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /header provider failed: the token reader is down/);
  assert.equal(rec.calls.length, 0);
});

test("a header provider that rejects resolves an error Result and never rejects", async (t) => {
  const rec = recorder({});
  const client = new api.ApiClient({
    baseUrl: "https://hh-core.example",
    fetch: rec.fetch,
    headers: () => Promise.reject(new Error("the org lookup refused")),
  });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /header provider failed: the org lookup refused/);
  assert.equal(rec.calls.length, 0);
});

// A header value the platform would refuse (a bare CR and LF here) is
// refused by the transport before anything dials: the platform's own refusal
// message echoes the value, and header values are where tokens travel, so
// the error names the header, never its value.
test("a refused header value resolves an error naming the header, never the value", async (t) => {
  const rec = recorder({});
  const client = new api.ApiClient({
    baseUrl: "https://hh-core.example",
    fetch: rec.fetch,
    headers: () => ({ "X-Org": "dibbits", Authorization: "Bearer leaked\r\ninjected: 1" }),
  });
  const result = await api.tenantAuth.tenantGetMe(client);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.match(result.error, /the Authorization header value is not a valid header value/);
    assert.doesNotMatch(result.error, /leaked/, "the refused value never lands in the error string");
  }
  assert.equal(rec.calls.length, 0, "the poisoned request never dialed");
});

test("a request past its timeout resolves an error Result naming the timeout", async (t) => {
  const fetchLike = (url, init) =>
    new Promise((resolve, reject) => {
      init.signal.addEventListener("abort", () => reject(init.signal.reason));
    });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const result = await api.tenantAuth.tenantGetMe(client, { timeoutMs: 20 });
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /timed out after 20 ms/);
});

test("a request aborted by the caller resolves an error Result naming the abort", async (t) => {
  const controller = new AbortController();
  const fetchLike = (url, init) =>
    new Promise((resolve, reject) => {
      init.signal.addEventListener("abort", () => reject(init.signal.reason));
    });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: fetchLike });
  const pending = api.tenantAuth.tenantGetMe(client, { signal: controller.signal });
  queueMicrotask(() => controller.abort());
  const result = await pending;
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /the request was aborted/);
});
