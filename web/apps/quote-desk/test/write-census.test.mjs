// The write census: every write of the client that the src sends from a deferred position goes through the one sender
// (src/deferred-send.ts). test/write-census.mjs decides by position; this holds the src to it, and holds the census to
// the shapes it must flag (the fixtures include the shape queueTermsPut had before the sender existed, and the shape
// that moves the capture itself inside the sender's call, so the capture is taken at send time).
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { makeProgram } from "./await-census.mjs";
import { srcWriteCensus, writeCensusOfText, writeMethods } from "./write-census.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");

const HEAD = 'import type { QdClient } from "./qd-client.js";\nimport { captureSend, deferredSend } from "./deferred-send.js";\nimport type { SendCapture, SendNow } from "./deferred-send.js";\n';

test("no write of the src is sent from a deferred position except through the sender", () => {
  const violations = srcWriteCensus().filter((row) => row.way === "VIOLATION");
  assert.deepEqual(
    violations.map((row) => `${row.file}:${row.line} ${row.symbol} ${row.call} (${row.reason})`),
    [],
  );
});

test("the write set is read off the client and the generated operations, and it is not empty or a guess", () => {
  const methods = writeMethods(makeProgram());
  assert.equal(methods.get("putTerms"), "PUT");
  assert.equal(methods.get("editLine"), "PATCH");
  assert.equal(methods.get("removeLine"), "DELETE");
  assert.equal(methods.get("createQuote"), "POST");
  assert.equal(methods.get("deleteMyWorkspace"), "DELETE");
  assert.equal(methods.has("readQuote"), false, "a read is not a write");
  assert.equal(methods.has("surfaceConfig"), false, "a read is not a write");
});

test("the sender calls the closure it is given exactly once, after its checks", () => {
  const text = readFileSync(join(appDir, "src", "deferred-send.ts"), "utf8");
  const sf = ts.createSourceFile("deferred-send.ts", text, ts.ScriptTarget.ES2022, true);
  let calls = 0;
  (function visit(n) {
    if (ts.isCallExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === "send") calls += 1;
    ts.forEachChild(n, visit);
  })(sf);
  assert.equal(calls, 1);
});

test("the census lists every write of the src, immediate ones too", () => {
  const rows = srcWriteCensus();
  assert.ok(rows.some((row) => row.call === "putTerms" && row.way === "sender"));
  assert.ok(rows.some((row) => row.call === "putMyWorkspace" && row.way === "sender"));
  assert.ok(rows.some((row) => row.call === "convertQuote" && row.way === "immediate"));
});

// The shape queueTermsPut had at b7edd1c: the send chained on the previous answer with then(), no check before it.
const OLD_QUEUE_TERMS_PUT = `${HEAD}
class A {
  client: QdClient | null = null;
  private before = Promise.resolve();
  private queueTermsPut<T>(send: () => Promise<T>): Promise<T> {
    const answer = this.before.then(send);
    this.before = answer.then(() => undefined, () => undefined);
    return answer;
  }
  flush(id: string) {
    const client = this.client;
    if (client === null) return;
    return this.queueTermsPut(() => client.putTerms(id, {} as never));
  }
}`;

const FLAGGED = [
  ["queueTermsPut as it was before the sender (the send chained on the previous answer)", OLD_QUEUE_TERMS_PUT],
  ["a write after an await", `${HEAD}class A { client: QdClient | null = null; async m(id: string) { await Promise.resolve(); await this.client?.putTerms(id, {} as never); } }`],
  ["a write in a then callback", `${HEAD}class A { client: QdClient | null = null; m(id: string, p: Promise<void>) { void p.then(() => this.client?.removeLine(id, 1)); } }`],
  ["a write in a timer", `${HEAD}class A { client: QdClient | null = null; m(id: string) { setTimeout(() => void this.client?.setValidUntil(id, ""), 10); } }`],
  ["a write in a method that is called from a timer", `${HEAD}class A { client: QdClient | null = null; async save(id: string) { await this.client?.deleteMyWorkspace(); } m(id: string) { setTimeout(() => void this.save(id), 10); } }`],
  ["a write in a loop that awaits", `${HEAD}class A { client: QdClient | null = null; async m(ids: string[]) { for (const id of ids) { await this.client?.removeLine(id, 1); } } }`],
];

for (const [name, source] of FLAGGED) {
  test(`the write census flags ${name}`, () => {
    const rows = writeCensusOfText(source);
    assert.ok(rows.length > 0, "the census found no write in the fixture");
    assert.ok(rows.some((row) => row.way === "VIOLATION"), `expected a violation, the census found: ${JSON.stringify(rows)}`);
  });
}

// The shape a tidy refactor could introduce (review PR 121 round 7, P2-1): the capture itself written inside the
// sender's call, so it is taken at send time, the capture compares now with now, and every check of the sender
// trivially passes. The row must come out a violation, not a sender.
const NEW_BAD_CAPTURE = `${HEAD}class W { client: QdClient | null = null; private before: Promise<unknown> = Promise.resolve(); now = (): SendNow => ({ client: this.client, attached: true, quote: null }); putTerms(): void { const client = this.client; if (client === null) return; this.before.then(() => deferredSend(captureSend(this.now), () => client.putTerms("q", {} as never))); } }`;

test("the write census rules a capture taken inside the sender closure a violation", () => {
  const rows = writeCensusOfText(NEW_BAD_CAPTURE);
  const row = rows.find((candidate) => candidate.call === "putTerms");
  assert.ok(row !== undefined, `the census found no write in the fixture: ${JSON.stringify(rows)}`);
  assert.notEqual(row.way, "sender");
  assert.equal(row.way, "VIOLATION");
  assert.match(row.reason, /capture taken inside the sender closure/);
});

// Two shapes a tidy refactor could introduce, one step outside the send's own text (review PR 121 round 8, P2-1):
// the capture named as a local of the deferred position itself (a then() callback, or the store's queue turn), so it
// is taken when the position runs; and the capture helper called where the send is, after the awaits. Both are ruled
// senders by a span check alone, and both take the capture at send time.
const NEW_HOISTED_LOCAL = `${HEAD}class W { client: QdClient | null = null; private before: Promise<unknown> = Promise.resolve(); now = (): SendNow => ({ client: this.client, attached: true, quote: null }); putTerms(): void { const client = this.client; if (client === null) return; this.before.then(() => { const capture = captureSend(this.now); return deferredSend(capture, () => client.putTerms("q", {} as never)); }); } }`;

const NEW_INLINE_HELPER = `${HEAD}class W { client: QdClient | null = null; now = (): SendNow => ({ client: this.client, attached: true, quote: null }); captureFor(_: unknown): SendCapture { return captureSend(this.now); } share(): void { deferredSend(this.captureFor(null), () => this.client.shareBySMS("q", {} as never)); } }`;

test("the write census rules a capture hoisted into the deferred position a violation", () => {
  const rows = writeCensusOfText(NEW_HOISTED_LOCAL);
  const row = rows.find((candidate) => candidate.call === "putTerms");
  assert.ok(row !== undefined, `the census found no write in the fixture: ${JSON.stringify(rows)}`);
  assert.notEqual(row.way, "sender");
  assert.equal(row.way, "VIOLATION");
  assert.match(row.reason, /capture taken inside the sender closure or at a deferred position/);
});

test("the write census rules a capture helper called at the send a violation", () => {
  const rows = writeCensusOfText(NEW_INLINE_HELPER);
  const row = rows.find((candidate) => candidate.call === "shareBySMS");
  assert.ok(row !== undefined, `the census found no write in the fixture: ${JSON.stringify(rows)}`);
  assert.notEqual(row.way, "sender");
  assert.equal(row.way, "VIOLATION");
  assert.match(row.reason, /capture taken inside the sender closure or at a deferred position/);
});

// The census watches every channel a write can ride, not only the client's methods (review PR 121 round 9, P2-1):
// the pos kit's raw api() port, which the app's picker already creates accounts through, and any direct request
// door (fetch and its friends) carrying a method other than GET, case and spacing aside. A write deferred through
// either is a violation like any other, tagged by its channel; an immediate one is listed; one handed to the sender
// is allowed as ever.
const RAW_HEAD = `${HEAD}import { api } from "@hardscapehouse/pos-kit/src/ported/ports.js";\n`;

const DEFERRED_API_POST = `${RAW_HEAD}export async function create(body: string) { await Promise.resolve(); await api("/api/v1/customers", { method: "POST", body }); }`;

const IMMEDIATE_API_POST = `${RAW_HEAD}export function create(body: string) { return api("/api/v1/customers", { method: " post ", body }); }`;

const API_POST_THROUGH_SENDER = `${RAW_HEAD}import type { SendNow } from "./deferred-send.js";\nclass W { now = (): SendNow => ({ client: null, attached: true, quote: null }); create(body: string) { const cap = captureSend(this.now); return Promise.resolve().then(() => deferredSend(cap, () => api("/api/v1/customers", { method: "POST", body }))); } }`;

const DEFERRED_FETCH_PUT = `${HEAD}export async function save(body: string) { await Promise.resolve(); await fetch("/api/v1/things", { method: "PUT", body }); }`;

const RAW_READ = `${HEAD}export function read() { return fetch("/api/v1/things"); }`;

const DEFERRED_BEACON = `${HEAD}export async function ping() { await Promise.resolve(); navigator.sendBeacon("/api/v1/pings", "x"); }`;

const DEFERRED_XHR_OPEN = `${HEAD}export function put(body: string) { setTimeout(() => { const x = new XMLHttpRequest(); x.open("PUT", "/api/v1/things"); x.send(body); }, 10); }`;

const DEFERRED_SOCKET = `${HEAD}export async function listen() { await Promise.resolve(); return new WebSocket("/api/v1/stream"); }`;

test("the write census rules a deferred write through the raw api() port a violation, tagged by its channel", () => {
  const rows = writeCensusOfText(DEFERRED_API_POST);
  const row = rows.find((candidate) => candidate.call === "api");
  assert.ok(row !== undefined, `the census found no api() port write in the fixture: ${JSON.stringify(rows)}`);
  assert.equal(row.channel, "api port");
  assert.equal(row.verb, "POST");
  assert.equal(row.way, "VIOLATION");
  assert.match(row.reason, /after an await/);
});

test("the write census lists an immediate api() port write, whatever the case or spacing of its method", () => {
  const rows = writeCensusOfText(IMMEDIATE_API_POST);
  assert.deepEqual(
    rows.map((row) => ({ call: row.call, verb: row.verb, channel: row.channel, way: row.way })),
    [{ call: "api", verb: "POST", channel: "api port", way: "immediate" }],
  );
});

test("the write census allows an api() port write routed through the sender", () => {
  const rows = writeCensusOfText(API_POST_THROUGH_SENDER);
  assert.deepEqual(rows.map((row) => row.way), ["sender"]);
});

test("the write census rules a deferred raw fetch write a violation, tagged by its channel", () => {
  const rows = writeCensusOfText(DEFERRED_FETCH_PUT);
  const row = rows.find((candidate) => candidate.call === "fetch");
  assert.ok(row !== undefined, `the census found no fetch write in the fixture: ${JSON.stringify(rows)}`);
  assert.equal(row.channel, "raw fetch");
  assert.equal(row.verb, "PUT");
  assert.equal(row.way, "VIOLATION");
  assert.match(row.reason, /after an await/);
});

test("the write census lists no row for a raw fetch read (no method, so a GET)", () => {
  const rows = writeCensusOfText(RAW_READ);
  assert.deepEqual(rows, []);
});

test("the write census rules a deferred beacon, a deferred XMLHttpRequest open and a deferred socket violations", () => {
  const beacon = writeCensusOfText(DEFERRED_BEACON);
  assert.ok(beacon.some((row) => row.call === "sendBeacon" && row.channel === "raw fetch" && row.way === "VIOLATION"), JSON.stringify(beacon));
  const xhr = writeCensusOfText(DEFERRED_XHR_OPEN);
  assert.ok(xhr.some((row) => row.call === "XMLHttpRequest.open" && row.verb === "PUT" && row.way === "VIOLATION"), JSON.stringify(xhr));
  const socket = writeCensusOfText(DEFERRED_SOCKET);
  assert.ok(socket.some((row) => row.call === "WebSocket" && row.channel === "raw fetch" && row.way === "VIOLATION"), JSON.stringify(socket));
});

test("the census classifies the picker's customer create: an immediate write on the api() port, deferred it would go red", () => {
  const rows = srcWriteCensus().filter((row) => row.file === "customer-picker.ts");
  assert.deepEqual(
    rows.map((row) => ({ call: row.call, verb: row.verb, channel: row.channel, way: row.way })),
    [{ call: "api", verb: "POST", channel: "api port", way: "immediate" }],
  );
});

// A client write method that only delegates to another write method writes as the one it calls (review PR 121
// round 9, P3-1): the chained name belongs in the write set, with the inner call's verb, and a deferred call of it
// is a violation. The shape is a subclass here because the probe cannot edit the client itself; the census reads
// the family the same way, so a method added to qd-client.ts that returns this.putTerms(...) is credited too.
const CHAINED_PUT = `import { QdClient } from "./qd-client.js";\n\nclass Chained extends QdClient {\n  putTermsChained(id: string, input: Parameters<QdClient["putTerms"]>[1]): ReturnType<QdClient["putTerms"]> {\n    return this.putTerms(id, input);\n  }\n}\n\nexport async function save(c: Chained) { await Promise.resolve(); await c.putTermsChained("q", {} as never); }`;

test("the write census credits a chained client write method: a deferred call of it is a violation", () => {
  const rows = writeCensusOfText(CHAINED_PUT);
  const row = rows.find((candidate) => candidate.call === "putTermsChained");
  assert.ok(row !== undefined, `the census found no row for the chained method: ${JSON.stringify(rows)}`);
  assert.equal(row.verb, "PUT");
  assert.equal(row.channel, "client");
  assert.equal(row.way, "VIOLATION");
  assert.match(row.reason, /after an await/);
});

const ALLOWED = [
  ["a write in the same tick as the person's action", `${HEAD}class A { client: QdClient | null = null; m(id: string) { return this.client?.putTerms(id, {} as never); } }`, "immediate"],
  [
    "a deferred write that is the closure handed to the sender",
    `${HEAD}class A { client: QdClient | null = null; private before = Promise.resolve(); now = (): SendNow => ({ client: this.client, attached: true, quote: null }); m(id: string) { const client = this.client; const cap = captureSend(this.now); if (client === null) return; return this.before.then(() => deferredSend(cap, () => client.putTerms(id, {} as never))); } }`,
    "sender",
  ],
];

for (const [name, source, way] of ALLOWED) {
  test(`the write census allows ${name}`, () => {
    const rows = writeCensusOfText(source);
    assert.deepEqual(rows.map((row) => row.way), [way]);
  });
}
