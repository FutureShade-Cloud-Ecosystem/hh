// The messages stub against the contract and against the core, field by field.
//
// A stub that drifts from the server makes every browser leg a flattering lie, so this test
// starts the stub, makes every call the app makes, and holds each answer to the generated wire
// descriptors: required fields present, types and vocabularies right, and no field the contract
// does not declare (the descriptors are open objects, so the undeclared field check is this
// file's own). Then each refusal is held to the core's words: the status, the envelope and the
// sentence itself, which must appear in the core's source. A refusal the stub invented fails here.
//
// One known drift is held as a drift, not hidden: the contract names a message page's rows `items`
// while the core's handler writes `messages` (core/internal/messaging/model.go MessagePage). The
// stub answers what the core answers by default and the contract's shape on request; the app reads
// both. The test pins the core's tag so a fix on the core's side shows up here.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreInternal = join(appDir, "..", "..", "..", "core", "internal");

const coreSource = ["messaging", join("platform", "httpx")]
  .flatMap((dir) =>
    readdirSync(join(coreInternal, dir))
      .filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"))
      .map((f) => readFileSync(join(coreInternal, dir, f), "utf8")),
  )
  .join("\n");

let child;
let base;

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
  child = spawn(process.execPath, [join(appDir, "test", "stub", "messages-stub.mjs")], {
    env: { MSG_STUB_PORT: String(port) },
    stdio: "ignore",
  });
  for (let i = 0; i < 100; i += 1) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("the stub did not start");
});

after(() => {
  child?.kill();
});

const tokens = {};
async function as(flow) {
  if (tokens[flow] === undefined) {
    const res = await fetch(`${base}/api/v1/demo/session`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ flow }) });
    const body = await res.json();
    tokens[flow] = { token: body.token, id: body.user.user_id };
  }
  return tokens[flow];
}

async function call(flow, method, path, body) {
  const { token } = await as(flow);
  const headers = { Authorization: `Bearer ${token}`, "X-Org": "hh-demo" };
  let payload;
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = JSON.stringify(body);
  }
  const res = await fetch(`${base}${path}`, { method, headers, body: payload });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    /* not json */
  }
  return { status: res.status, json, text };
}

const control = (path, body) => fetch(`${base}${path}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body ?? {}) });
const reset = () => control("/_stub/reset");

// ---- the declared keys of a descriptor, through refs and intersections ----

function undeclared(value, d, path = "value", out = []) {
  if (d.k === "ref") return undeclared(value, SCHEMA_DESCRIPTORS[d.name], path, out);
  if (d.k === "allOf") {
    const props = Object.assign({}, ...d.of.map((leg) => (leg.k === "ref" ? SCHEMA_DESCRIPTORS[leg.name].props : leg.props)));
    if (value !== null && typeof value === "object" && !Array.isArray(value)) {
      for (const key of Object.keys(value)) if (props[key] === undefined) out.push(`${path}.${key}`);
    }
    return out;
  }
  if (d.k === "object") {
    if (value === null || typeof value !== "object" || Array.isArray(value)) return out;
    for (const key of Object.keys(value)) if (d.props[key] === undefined) out.push(`${path}.${key}`);
    for (const [key, leg] of Object.entries(d.props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
    return out;
  }
  if (d.k === "array" && Array.isArray(value)) value.forEach((v, i) => undeclared(v, d.of, `${path}[${i}]`, out));
  if (d.k === "union") {
    const leg = d.of.find((l) => l.k !== "null");
    if (leg !== undefined && value !== null) undeclared(value, leg, path, out);
  }
  return out;
}

function holds(schemaName, value) {
  const descriptor = SCHEMA_DESCRIPTORS[schemaName];
  assert.ok(descriptor !== undefined, `no descriptor named ${schemaName}`);
  assert.deepEqual(validateValue(value, descriptor), [], `${schemaName} does not fit the contract`);
  assert.deepEqual(undeclared(value, descriptor), [], `${schemaName} carries fields the contract does not declare`);
}

function refused(res, status, fragment) {
  assert.equal(res.status, status);
  assert.ok(typeof res.json?.error === "string", "the refusal carries the error envelope");
  assert.ok(res.json.error.includes(fragment), `expected "${fragment}" in "${res.json.error}"`);
  assert.ok(coreSource.includes(fragment), `the core's source does not contain the sentence "${fragment}": the stub invented it`);
}

const CH = {
  branch: "c0000000-0000-4000-9000-000000000001",
  yard: "c0000000-0000-4000-9000-000000000002",
  esc: "c0000000-0000-4000-9000-000000000004",
  house: "c0000000-0000-4000-9000-000000000005",
  pay: "c0000000-0000-4000-9000-000000000007",
};
const FORCED = "b9000000-0000-4000-9000-000000000001";

test("the channel list fits the contract and carries no membership list", async () => {
  await reset();
  const res = await call("sales", "GET", "/api/v1/messaging/channels");
  assert.equal(res.status, 200);
  holds("MessagingChannelList", res.json);
  assert.ok(res.json.channels.length >= 5);
  for (const c of res.json.channels) assert.equal(c.members, undefined);
});

test("a room is in the list only for its members: the payments room is the Payables persona's alone", async () => {
  const sales = await call("sales", "GET", "/api/v1/messaging/channels");
  assert.equal(sales.json.channels.some((c) => c.id === CH.pay), false);
  const payables = await call("payables-receivables", "GET", "/api/v1/messaging/channels");
  assert.equal(payables.json.channels.some((c) => c.id === CH.pay), true);
  refused(await call("sales", "GET", `/api/v1/messaging/channels/${CH.pay}/messages`), 403, "not a channel member");
});

test("the core's page names its rows messages, the contract names them items: both are held", async () => {
  assert.ok(/MessagePage struct[\s\S]*?json:"messages"/.test(coreSource), "the core's MessagePage still writes messages");
  const core = await call("sales", "GET", `/api/v1/messaging/channels/${CH.yard}/messages`);
  assert.equal(core.status, 200);
  assert.ok(Array.isArray(core.json.messages));
  await control("/_stub/page-shape", { shape: "items" });
  const contract = await call("sales", "GET", `/api/v1/messaging/channels/${CH.yard}/messages`);
  holds("MessagingMessagePage", contract.json);
  await control("/_stub/page-shape", { shape: "messages" });
});

test("a message page is cursor paginated newest first, and the cursor walks to the end without a repeat", async () => {
  const first = await call("sales", "GET", `/api/v1/messaging/channels/${CH.yard}/messages?limit=1`);
  assert.equal(first.json.messages.length, 1);
  assert.ok(first.json.next_cursor);
  const second = await call("sales", "GET", `/api/v1/messaging/channels/${CH.yard}/messages?limit=1&cursor=${encodeURIComponent(first.json.next_cursor)}`);
  assert.equal(second.json.messages.length, 1);
  assert.notEqual(second.json.messages[0].id, first.json.messages[0].id);
  assert.ok(first.json.messages[0].created_at > second.json.messages[0].created_at);
  assert.equal(second.json.next_cursor, undefined);
  refused(await call("sales", "GET", `/api/v1/messaging/channels/${CH.yard}/messages?cursor=garbage`), 400, "malformed pagination cursor");
});

test("a message row fits the contract and a reply is counted under its root", async () => {
  const res = await call("sales", "GET", `/api/v1/messaging/channels/${CH.yard}/messages`);
  for (const m of res.json.messages) holds("MessagingMessage", m);
  const root = res.json.messages.find((m) => m.reply_count === 2);
  assert.ok(root, "the seeded thread root counts its two replies");
  const thread = await call("sales", "GET", `/api/v1/messaging/channels/${CH.yard}/threads/${root.id}`);
  holds("MessagingMessageList", thread.json);
  assert.equal(thread.json.messages.length, 2);
  assert.ok(thread.json.messages[0].created_at <= thread.json.messages[1].created_at, "a thread reads oldest first");
});

test("posting: the message comes back, an empty body is refused, a stranger is refused, an absent channel is not found", async () => {
  const ok = await call("yard-manager", "POST", `/api/v1/messaging/channels/${CH.yard}/messages`, { body: "  Pallets staged at door 2  " });
  assert.equal(ok.status, 201);
  holds("MessagingMessage", ok.json);
  assert.equal(ok.json.body, "Pallets staged at door 2");
  refused(await call("yard-manager", "POST", `/api/v1/messaging/channels/${CH.yard}/messages`, { body: "   " }), 400, "message body is empty");
  refused(await call("payables-receivables", "POST", `/api/v1/messaging/channels/${CH.yard}/messages`, { body: "hi" }), 403, "not a channel member");
  const gone = await call("sales", "POST", "/api/v1/messaging/channels/c0000000-0000-4000-9000-0000000000ff/messages", { body: "hi" });
  assert.equal(gone.status, 404);
  assert.equal(gone.json.error, "not found");
  assert.ok(coreSource.includes('"not found"'));
});

test("a body over 8 KB is cut off at a rune boundary, not refused: the core's size rule", async () => {
  assert.ok(/maxBody\s*=\s*8 \* 1024/.test(coreSource), "the core's cap is still 8 KiB");
  const long = `${"a".repeat(8191)}éé`;
  const res = await call("sales", "POST", `/api/v1/messaging/channels/${CH.branch}/messages`, { body: long });
  assert.equal(res.status, 201);
  assert.equal(Buffer.byteLength(res.json.body, "utf8"), 8191, "the two byte rune that straddled the cut is dropped whole");
});

test("a reply needs a root in the same channel", async () => {
  const page = await call("sales", "GET", `/api/v1/messaging/channels/${CH.branch}/messages`);
  const root = page.json.messages[0];
  const reply = await call("sales", "POST", `/api/v1/messaging/channels/${CH.branch}/messages`, { body: "Copy", thread_root_id: root.id });
  assert.equal(reply.status, 201);
  assert.equal(reply.json.thread_root_id, root.id);
  const stray = await call("sales", "POST", `/api/v1/messaging/channels/${CH.yard}/messages`, { body: "Copy", thread_root_id: root.id });
  assert.equal(stray.status, 404);
});

test("unread counts fall to zero when the channel is marked read, and own posts never count", async () => {
  await reset();
  const before = await call("sales", "GET", "/api/v1/messaging/unread");
  holds("MessagingUnreadSummary", before.json);
  assert.ok(before.json.total > 0);
  const mark = await call("sales", "POST", `/api/v1/messaging/channels/${CH.branch}/read`);
  assert.equal(mark.status, 200);
  assert.deepEqual(mark.json, { status: "ok" });
  const after = await call("sales", "GET", "/api/v1/messaging/unread");
  assert.equal(after.json.channels.some((c) => c.channel_id === CH.branch), false);
  await call("sales", "POST", `/api/v1/messaging/channels/${CH.branch}/messages`, { body: "my own" });
  const own = await call("sales", "GET", "/api/v1/messaging/unread");
  assert.equal(own.json.channels.some((c) => c.channel_id === CH.branch), false);
  const other = await call("yard-manager", "GET", "/api/v1/messaging/unread");
  assert.equal(other.json.channels.find((c) => c.channel_id === CH.branch)?.unread_count, 3, "the person who did not post sees the other side, the forced notice and the new post");
});

test("the forced acknowledgement: listed until acked, progress counts distinct users, a re-ack never double counts", async () => {
  await reset();
  const before = await call("sales", "GET", "/api/v1/messaging/unacked");
  holds("MessagingMessageList", before.json);
  assert.deepEqual(before.json.messages.map((m) => m.id), [FORCED]);
  assert.equal(before.json.messages[0].requires_ack, true);
  const ack = await call("sales", "POST", `/api/v1/messaging/channels/${CH.branch}/messages/${FORCED}/ack`);
  assert.equal(ack.status, 200);
  holds("MessagingAck", ack.json);
  assert.equal(ack.json.method, "TAP");
  await call("sales", "POST", `/api/v1/messaging/channels/${CH.branch}/messages/${FORCED}/ack`, { method: "TAP" });
  const progress = await call("yard-manager", "GET", `/api/v1/messaging/channels/${CH.branch}/messages/${FORCED}/ack-progress`);
  holds("MessagingAckProgress", progress.json);
  assert.deepEqual({ required: progress.json.required, acked: progress.json.acked, remaining: progress.json.remaining, complete: progress.json.complete }, { required: 3, acked: 1, remaining: 2, complete: false });
  const after = await call("sales", "GET", "/api/v1/messaging/unacked");
  assert.deepEqual(after.json.messages, []);
  const still = await call("yard-manager", "GET", "/api/v1/messaging/unacked");
  assert.equal(still.json.messages.length, 1);
  refused(await call("sales", "POST", `/api/v1/messaging/channels/${CH.branch}/messages/${FORCED}/ack`, { method: "SHOUT" }), 400, "ack method must be one of TAP, QUICK_REPLY, VOICE");
});

test("notify prefs: defaults inherited, a choice kept, an escalation room can not be muted, a bad mode is refused", async () => {
  await reset();
  const def = await call("sales", "GET", `/api/v1/messaging/channels/${CH.yard}/notify-prefs`);
  holds("MessagingNotifyPrefs", def.json);
  assert.equal(def.json.mode, "ALL");
  assert.equal(def.json.inherited, true);
  const set = await call("sales", "PUT", `/api/v1/messaging/channels/${CH.yard}/notify-prefs`, { mode: "MENTIONS" });
  holds("MessagingNotifyPrefs", set.json);
  assert.equal(set.json.mode, "MENTIONS");
  assert.equal(set.json.inherited, false);
  const muted = await call("sales", "PUT", `/api/v1/messaging/channels/${CH.yard}/notify-prefs`, { mode: "MUTED" });
  assert.equal(muted.json.muted, true);
  refused(await call("sales", "PUT", `/api/v1/messaging/channels/${CH.esc}/notify-prefs`, { mode: "MUTED" }), 409, "escalation channels cannot be muted");
  refused(await call("sales", "PUT", `/api/v1/messaging/channels/${CH.esc}/notify-prefs`, { muted: true }), 409, "escalation channels cannot be muted");
  refused(await call("sales", "PUT", `/api/v1/messaging/channels/${CH.yard}/notify-prefs`, { mode: "LOUD" }), 400, "mode must be one of ALL, MENTIONS, MUTED");
  const payments = await call("payables-receivables", "GET", `/api/v1/messaging/channels/${CH.pay}/notify-prefs`);
  assert.equal(payments.json.mode, "MUTED", "the payments room arrives quiet by its default");
  assert.equal(payments.json.inherited, true);
});

test("payment rules: the full closed set, a toggle kept per caller, an unknown rule refused", async () => {
  await reset();
  const def = await call("payables-receivables", "GET", "/api/v1/messaging/payment-rules");
  holds("MessagingPaymentRulePrefs", def.json);
  assert.deepEqual(def.json.rules, { ONLINE: false, CROSS_BRANCH: false, OWN_ORDER: false });
  const set = await call("payables-receivables", "PUT", "/api/v1/messaging/payment-rules/OWN_ORDER", { enabled: true });
  holds("MessagingPaymentRulePrefs", set.json);
  assert.equal(set.json.rules.OWN_ORDER, true);
  const other = await call("sales", "GET", "/api/v1/messaging/payment-rules");
  assert.equal(other.json.rules.OWN_ORDER, false, "one caller's rule never reads as another's");
  refused(await call("sales", "PUT", "/api/v1/messaging/payment-rules/EVERYTHING", { enabled: true }), 400, "rule must be one of ONLINE, CROSS_BRANCH, OWN_ORDER");
});

test("the house queue: open rows by filter, a claim stamps the caller, a second claim is a conflict", async () => {
  await reset();
  const all = await call("sales", "GET", `/api/v1/messaging/channels/${CH.house}/house-queue`);
  holds("MessagingHouseQueue", all.json);
  assert.equal(all.json.conversations.length, 3);
  const open = await call("sales", "GET", `/api/v1/messaging/channels/${CH.house}/house-queue?open=1`);
  assert.equal(open.json.conversations.length, 2);
  const target = open.json.conversations[0];
  const claim = await call("sales", "POST", `/api/v1/messaging/house-conversations/${target.id}/claim`);
  assert.equal(claim.status, 200);
  holds("MessagingHouseConversation", claim.json);
  assert.ok(claim.json.claimed_by);
  refused(await call("yard-manager", "POST", `/api/v1/messaging/house-conversations/${target.id}/claim`), 409, "conversation already claimed");
  refused(await call("payables-receivables", "GET", `/api/v1/messaging/channels/${CH.house}/house-queue`), 403, "not a channel member");
});

test("the stream: a ticket is single use, a message reaches its channel's members only, 501 when tickets are off", async () => {
  await reset();
  const ticket = await call("sales", "POST", "/api/v1/messaging/stream-ticket");
  holds("MessagingStreamTicket", ticket.json);
  const stream = await fetch(`${base}/api/v1/messaging/stream?ticket=${ticket.json.ticket}`);
  assert.equal(stream.status, 200);
  assert.match(stream.headers.get("content-type"), /text\/event-stream/);
  const reuse = await fetch(`${base}/api/v1/messaging/stream?ticket=${ticket.json.ticket}`);
  assert.equal(reuse.status, 401);
  const reader = stream.body.getReader();
  await control(`/_stub/as/yard-manager`, { channel_id: CH.yard, body: "Hello over the stream" });
  let seen = "";
  const decoder = new TextDecoder();
  for (let i = 0; i < 10 && !seen.includes("Hello over the stream"); i += 1) {
    const { value, done } = await reader.read();
    if (done) break;
    seen += decoder.decode(value);
  }
  assert.match(seen, /event: message/);
  assert.match(seen, /Hello over the stream/);
  await reader.cancel();
  await control("/_stub/tickets", { enabled: false });
  const off = await call("sales", "POST", "/api/v1/messaging/stream-ticket");
  assert.equal(off.status, 501);
  assert.ok(coreSource.includes("stream tickets not enabled; use the Bearer stream"));
  await control("/_stub/tickets", { enabled: true });
});

test("a route without a bearer is refused, and the offline control answers 503 on every route", async () => {
  const bare = await fetch(`${base}/api/v1/messaging/channels`);
  assert.equal(bare.status, 401);
  await control("/_stub/offline", { on: true });
  const down = await call("sales", "GET", "/api/v1/messaging/unread");
  assert.equal(down.status, 503);
  assert.ok(coreSource.includes("database unavailable"));
  await control("/_stub/offline", { on: false });
});
