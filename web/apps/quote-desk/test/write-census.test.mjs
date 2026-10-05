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

const HEAD = 'import type { QdClient } from "./qd-client.js";\nimport { captureSend, deferredSend } from "./deferred-send.js";\nimport type { SendNow } from "./deferred-send.js";\n';

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
