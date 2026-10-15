// The Yard Pick stub's sync surface against the core's own writable sets.
//
// The stub drifted once: it admitted fields the core refuses (and the app
// sent them), so every leg passed against a server the core is not. This test
// reads the Go sets (core/internal, through test/_lib/gosets.mjs) and probes
// the stub with every name in the union of the sets and the names the app once
// sent: a name is refused as "not writable through sync" exactly when the
// core's set for that scope and op does not hold it.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";
import { GO_SETS, PROBE_EXTRAS, goMaxPickedLines, goPickedCompanions } from "./_lib/gosets.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
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
  child = spawn(process.execPath, [join(appDir, "test", "stub", "yard-stub.mjs")], {
    env: { YARD_STUB_PORT: String(port) },
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

const headers = { Authorization: "Bearer stub-token", "X-Org": "hh-demo", "Content-Type": "application/json" };

async function pullIds(scope) {
  const res = await fetch(`${base}/api/v1/sync/${scope}?since=0&limit=200`, { headers });
  const json = await res.json();
  return (json.items ?? []).map((i) => i.id);
}

let opCounter = 0;
async function push(scope, op, payload) {
  opCounter += 1;
  const client_id = `00000000-0000-4000-8000-${String(opCounter).padStart(12, "0")}`;
  const res = await fetch(`${base}/api/v1/sync/${scope}`, {
    method: "POST",
    headers,
    body: JSON.stringify({ ops: [{ client_id, op, payload }] }),
  });
  const json = await res.json();
  return json.results?.[0] ?? { status: "http", error: JSON.stringify(json) };
}

const NOT_WRITABLE = /is not writable through sync/;

const UNIVERSE = () => {
  const all = new Set(PROBE_EXTRAS);
  for (const make of Object.values(GO_SETS)) for (const k of make()) all.add(k);
  all.delete("id");
  return [...all].sort();
};

async function probe(scope, op, setName, idFor) {
  const set = GO_SETS[setName]();
  const id = await idFor();
  for (const field of UNIVERSE()) {
    const answer = await push(scope, op, { id, [field]: "x" });
    const refused = answer.status === "rejected" && NOT_WRITABLE.test(String(answer.error ?? ""));
    assert.equal(refused, !set.has(field), `${setName}: field ${field} should be ${set.has(field) ? "admitted" : "refused"} by the stub exactly as the core does (answer: ${JSON.stringify(answer)})`);
  }
}

const NEW_ID = () => "3a000000-0000-4000-9000-0000000fffff";

test("pick_tasks update: the stub admits exactly the core's writable fields", async () => {
  await probe("pick_tasks", "update", "pick_tasks update", async () => (await pullIds("pick_tasks"))[0]);
});

test("shortages create: the stub admits exactly the core's writable fields", async () => {
  await probe("shortages", "create", "shortages create", async () => NEW_ID());
});

test("shortages update: the stub admits exactly the core's writable fields", async () => {
  await probe("shortages", "update", "shortages update", async () => NEW_ID());
});

test("weights update: the stub admits exactly the core's writable fields", async () => {
  await probe("weights", "update", "weights update", async () => (await pullIds("pick_tasks"))[0]);
});

test("the stub's weights feed is the core's: no row carries a skid count", async () => {
  const res = await fetch(`${base}/api/v1/sync/weights?since=0&limit=200`, { headers });
  const json = await res.json();
  for (const item of json.items ?? []) assert.equal("skid_count" in item.data, false, "the core's weights feed is the NEED_WEIGHT task feed, not a skid count");
});


// ---- the pick record door (pick_tasks update, {id, picked: [line, ...]}) ----
//
// The core's door (core/internal/tasks/syncpick.go) takes the record as its own
// operation. These legs hold the stub to it: the line legs are the Go map's,
// nothing travels beside `picked` but the row address, the text rule is the
// service body's, and every sentence the stub answers is one the Go source holds.

const coreTasksSource = ["syncpick.go", "yard_service.go"].map((f) => readFileSync(new URL(`../../../../core/internal/tasks/${f}`, import.meta.url), "utf8")).join("\n");

/** A Go format string as a pattern: the stub's sentence must be the template with its verbs filled. */
function fromTemplate(template) {
  const body = template
    .replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
    .replace(/%d/g, "\\d+")
    .replace(/%s/g, ".+?")
    .replace(/%q/g, '"[^"]*"');
  return new RegExp(`^(tasks: )?${body}$`);
}

/** The answer's error is a sentence the Go source holds, with the verbs filled. */
function assertCoreWords(answer, template) {
  assert.equal(answer.status, "rejected", JSON.stringify(answer));
  assert.ok(coreTasksSource.includes(template), `the core's source does not hold "${template}": the stub invented it`);
  assert.match(String(answer.error), fromTemplate(template));
}

async function resetStub() {
  await fetch(`${base}/_stub/reset`, { method: "POST", headers, body: "{}" });
}

async function pullRow(scope, id) {
  const res = await fetch(`${base}/api/v1/sync/${scope}?since=0&limit=200`, { headers });
  return ((await res.json()).items ?? []).find((i) => i.id === id);
}

async function aTaskWithLines() {
  const res = await fetch(`${base}/api/v1/sync/pick_tasks?since=0&limit=200`, { headers });
  const row = ((await res.json()).items ?? []).find((i) => (i.data.ticket?.lines ?? []).length >= 2);
  return { id: row.id, lines: row.data.ticket.lines };
}

function fullLine(ticketLine, over = {}) {
  return {
    order_line_no: ticketLine.line_no,
    product_id: ticketLine.product_id,
    sku: ticketLine.sku,
    description: ticketLine.description,
    qty_ordered: ticketLine.qty_ordered,
    qty_picked: ticketLine.qty_ordered,
    uom: ticketLine.uom,
    short_reason: "",
    short_note: "",
    ...over,
  };
}

test("pick record: a line with all nine legs applies, the pulled row carries the recorded lines, a replay answers the same", async () => {
  await resetStub();
  const { id, lines } = await aTaskWithLines();
  const picked = lines.map((l) => fullLine(l));
  const first = await push("pick_tasks", "update", { id, picked });
  assert.equal(first.status, "applied", JSON.stringify(first));
  const row = await pullRow("pick_tasks", id);
  assert.equal(row.data.picked.length, lines.length);
  assert.equal(row.data.picked[0].order_line_no, lines[0].line_no);
  assert.ok(row.seq >= first.seq, "the task row re-appears in the pull");
});

test("pick record: the line legs are the Go map's, any other leg refuses the op whole", async () => {
  await resetStub();
  const { id, lines } = await aTaskWithLines();
  const legs = GO_SETS["picked line"]();
  for (const field of ["id", "task_id", "picked_at", "picked_by", "created_at", "reason", "note", "sku2"]) {
    assert.equal(legs.has(field), false, `${field} is not a line leg in the core`);
    const answer = await push("pick_tasks", "update", { id, picked: [{ ...fullLine(lines[0]), [field]: "x" }] });
    assertCoreWords(answer, 'picked line %d: field %q is not writable through sync');
  }
  for (const leg of legs) {
    const answer = await push("pick_tasks", "update", { id, picked: [fullLine(lines[0])].map((l) => (leg in l ? l : { ...l, [leg]: leg === "order_line_no" ? 1 : "" })) });
    assert.equal(answer.status, "applied", `the leg ${leg} is a core line leg, the stub admits it (${JSON.stringify(answer)})`);
  }
});

test("pick record: it is its own operation, nothing travels beside id and picked", async () => {
  await resetStub();
  const { id, lines } = await aTaskWithLines();
  const companions = goPickedCompanions();
  assert.deepEqual([...companions].sort(), ["id", "picked"]);
  for (const field of ["status", "stage_location", "stage_ref", "photo_url", "skid_count"]) {
    const answer = await push("pick_tasks", "update", { id, picked: [fullLine(lines[0])], [field]: field === "status" ? "IN_PROGRESS" : "x" });
    if (field === "skid_count") {
      assertCoreWords(answer, "field %q is not writable through sync");
    } else {
      assertCoreWords(answer, "field %q cannot travel with picked, a pick record is its own operation");
    }
  }
});

test("pick record: the shape and the bounds, in the core's words", async () => {
  await resetStub();
  const { id, lines } = await aTaskWithLines();
  assertCoreWords(await push("pick_tasks", "update", { id, picked: "x" }), "picked must be an array of lines");
  assertCoreWords(await push("pick_tasks", "update", { id, picked: [] }), "say what was picked, an empty record is not the same as a pick of nothing");
  const over = Array.from({ length: goMaxPickedLines() + 1 }, (_, i) => fullLine(lines[0], { order_line_no: i + 1 }));
  assertCoreWords(await push("pick_tasks", "update", { id, picked: over }), "that is more lines than any one ticket carries (max %d)");
  assertCoreWords(await push("pick_tasks", "update", { id, picked: [5] }), "picked line %d must be an object");
  assertCoreWords(await push("pick_tasks", "update", { id, picked: [{ ...fullLine(lines[0]), order_line_no: "1" }] }), "picked line %d: order_line_no must be an integer");
  assertCoreWords(await push("pick_tasks", "update", { id, picked: [{ ...fullLine(lines[0]), qty_picked: 3 }] }), "picked line %d: %s must be a string");
});

test("pick record: the service body's rules, in the core's words", async () => {
  await resetStub();
  const { id, lines } = await aTaskWithLines();
  const line = lines[0];
  assertCoreWords(await push("pick_tasks", "update", { id, picked: [fullLine(line, { order_line_no: 0 })] }), "every recorded line needs the ticket's own line number");
  assertCoreWords(await push("pick_tasks", "update", { id, picked: [fullLine(line), fullLine(line)] }), "line %d was sent twice, one number per line");
  const above = Number(line.qty_ordered) + 1;
  const aboveAnswer = await push("pick_tasks", "update", { id, picked: [fullLine(line, { qty_picked: String(above) })] });
  assert.equal(aboveAnswer.status, "rejected");
  assert.match(String(aboveAnswer.error), /more than was ordered/);
  const short = await push("pick_tasks", "update", { id, picked: [fullLine(line, { qty_picked: "0" })] });
  assert.equal(short.status, "rejected", "a short line with no reason is refused");
  assert.match(String(short.error), /is short, give the reason/);
});

test("pick record: the text rule, control and bidi characters refused, tab, LF and CR kept in notes", async () => {
  await resetStub();
  const { id, lines } = await aTaskWithLines();
  const line = lines[0];
  const text = "the %s cannot hold control characters, enter it as plain text";
  for (const [leg, name] of [["sku", "sku"], ["uom", "unit"], ["product_id", "product id"], ["short_reason", "short reason"]]) {
    for (const bad of ["a\u0000b", "a\tb", "a\nb", "a\u007fb", "a\u202eb"]) {
      const answer = await push("pick_tasks", "update", { id, picked: [fullLine(line, { [leg]: bad })] });
      assert.equal(answer.status, "rejected", `${leg} ${JSON.stringify(bad)}`);
      assert.match(String(answer.error), new RegExp(`line \\d+: the ${name} cannot hold control characters`));
    }
  }
  assert.ok(coreTasksSource.includes(`line %d: ${text}`));
  for (const leg of ["description", "short_note"]) {
    for (const bad of ["a\u0000b", "a\u001bb", "a\u007fb", "a\u2066b"]) {
      const answer = await push("pick_tasks", "update", { id, picked: [fullLine(line, { [leg]: bad })] });
      assert.equal(answer.status, "rejected", `${leg} ${JSON.stringify(bad)}`);
    }
    const kept = await push("pick_tasks", "update", { id, picked: [fullLine(line, { [leg]: "two\nlines\tand\rmore" })] });
    assert.equal(kept.status, "applied", `${leg} keeps tab, LF and CR (${JSON.stringify(kept)})`);
  }
});

test("pick record: a stale base answers the conflict envelope and applies nothing", async () => {
  await resetStub();
  const { id, lines } = await aTaskWithLines();
  const res = await fetch(`${base}/api/v1/sync/pick_tasks`, {
    method: "POST",
    headers,
    body: JSON.stringify({ ops: [{ client_id: "00000000-0000-4000-8000-0000000fa001", op: "update", base_seq: 0, payload: { id, picked: [fullLine(lines[0])] } }] }),
  });
  const answer = (await res.json()).results[0];
  assert.equal(answer.status, "conflict");
  assert.equal(answer.conflict.policy, "server_wins");
  assert.equal((await pullRow("pick_tasks", id)).data.picked.length, 0, "nothing was recorded");
});

// The pick record door (core/internal/tasks/syncpick.go): picked is its own operation with the direct
// door's line legs, and the rejection words are the core's.
test("pick_tasks update: picked is its own operation (the sync doors CI leg)", async () => {
  const id = (await pullIds("pick_tasks"))[0];
  const line = { order_line_no: 1, product_id: "", sku: "", description: "", qty_ordered: "1", qty_picked: "1", uom: "EA", short_reason: "", short_note: "" };
  const mixed = await push("pick_tasks", "update", { id, picked: [line], status: "ACCEPTED" });
  assert.equal(mixed.status, "rejected");
  assert.match(mixed.error, /cannot travel with picked, a pick record is its own operation/);
  const unknown = await push("pick_tasks", "update", { id, picked: [{ ...line, picked_at: "x" }] });
  assert.match(unknown.error, /picked line 1: field "picked_at" is not writable through sync/);
  const nul = await push("pick_tasks", "update", { id, picked: [{ ...line, short_note: "dock\u0000two" }] });
  assert.match(nul.error, /cannot hold control characters/);
  const notArray = await push("pick_tasks", "update", { id, picked: "x" });
  assert.match(notArray.error, /picked must be an array of lines/);
  const ok = await push("pick_tasks", "update", { id, picked: [{ ...line, qty_picked: "0", short_reason: "NOT_IN_YARD", short_note: "two\nlines\tand\r\nmore" }] });
  assert.equal(ok.status, "applied", JSON.stringify(ok));
});

test("shortages create: the parent task's seq moves, as the core's create bumps the task's pick seq", async () => {
  await resetStub();
  const { id } = await aTaskWithLines();
  const before = (await pullRow("pick_tasks", id)).seq;
  const answer = await push("shortages", "create", { id: "3a000000-0000-4000-9000-0000000fbb01", task_id: id, order_line_no: 1, description: "Flagstone", qty_short: "2", uom: "SQFT" });
  assert.equal(answer.status, "applied", JSON.stringify(answer));
  assert.ok((await pullRow("pick_tasks", id)).seq > before, "the task row re-appears in the pull with the counts");
});

// A cancelled pick takes no record through sync either (core/internal/tasks/syncpick.go): that op's
// rejection in the core's words, and the stub records nothing.
test("pick_tasks update: picked onto a cancelled pick is refused in the core's words", async () => {
  const id = (await pullIds("pick_tasks"))[0];
  const reassign = await fetch(`${base}/_stub/reassign`, { method: "POST", headers, body: JSON.stringify({ task_id: id }) });
  assert.equal(reassign.status, 200);
  const line = { order_line_no: 1, product_id: "", sku: "", description: "", qty_ordered: "1", qty_picked: "0", uom: "EA", short_reason: "NOT_IN_YARD", short_note: "" };
  const res = await push("pick_tasks", "update", { id, picked: [line] });
  assert.equal(res.status, "rejected");
  assert.match(res.error, /this pick was cancelled, its record cannot change/);
});

// The engine's order of decisions (core/internal/sync/engine.go): shape, dedup, load, STALE, then the module's Write.
// So a record based on a seq the server moved is superseded whether or not the task is open; only a blind
// record (no base) reaches the module, whose RecordPick refuses a closed pick. A leg that wants "superseded"
// must move an OPEN task's seq; a leg that wants the closed words must send a blind record.
async function stubPost(path, body) {
  const res = await fetch(`${base}${path}`, { method: "POST", headers, body: JSON.stringify(body) });
  assert.equal(res.status, 200, `${path} answered ${res.status}`);
}

async function recordOp(id, lines, baseSeq) {
  const res = await fetch(`${base}/api/v1/sync/pick_tasks`, {
    method: "POST",
    headers,
    body: JSON.stringify({ ops: [{ client_id: `00000000-0000-4000-8000-0000000fc${String(++opCounter).padStart(3, "0")}`, op: "update", ...(baseSeq !== undefined ? { base_seq: baseSeq } : {}), payload: { id, picked: [fullLine(lines[0])] } }] }),
  });
  return (await res.json()).results[0];
}

test("order of decisions: a touched OPEN task supersedes a based record and stays open", async () => {
  await resetStub();
  const { id, lines } = await aTaskWithLines();
  const base = (await pullRow("pick_tasks", id)).seq;
  await stubPost("/_stub/touch", { task_id: id });
  const row = await pullRow("pick_tasks", id);
  assert.ok(row.seq > base, "the seq moved");
  assert.notEqual(row.data.status, "CANCELLED", "the task is still open");
  const answer = await recordOp(id, lines, base);
  assert.equal(answer.status, "conflict");
  assert.equal(answer.conflict.policy, "server_wins");
});

test("order of decisions: stale is decided before closed, and a blind record on a cancelled pick gets the closed words", async () => {
  await resetStub();
  const { id, lines } = await aTaskWithLines();
  const base = (await pullRow("pick_tasks", id)).seq;
  await stubPost("/_stub/reassign", { task_id: id });
  assert.equal((await recordOp(id, lines, base)).status, "conflict", "based and stale: superseded, the closed status is never reached");
  const blind = await recordOp(id, lines, undefined);
  assert.equal(blind.status, "rejected");
  assert.match(blind.error, /this pick was cancelled, its record cannot change/);
});
