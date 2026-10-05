// The await census, on the pattern of the PR 116 census: every asynchronous call in the app's src (every await, every
// then(), every timer, every promise made by hand, and every call an event handler starts without waiting for it)
// has an entry in AWAIT-LEDGER.md, and the ledger has no entry for code that no longer exists.
//
// An entry is made per file, symbol (the method or function the call sits in) and kind, with the count of calls of
// that kind inside it, so a new await in a method that is already ruled changes its count and fails here until the
// ledger says what guards it. A symbol of * stands for every symbol of that file and kind that has no row of its own.
//
// Every entry is GUARDED (it names the guard) or SAFE (it names why no quote can be touched by it). A GUARDED entry
// names the mutants that take the guard away and the tests that must go red without it; this test checks that those
// mutants and tests exist and that each mutant's edit still applies to the source exactly once. That the tests really
// go red is shown by test/run-mutants.mjs, which applies each edit and runs its test.
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { census, censusOfText, walk } from "./await-census.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const testDir = join(appDir, "test");

/** The ledger's rows. */
function readLedger() {
  const text = readFileSync(join(appDir, "AWAIT-LEDGER.md"), "utf8");
  const rows = [];
  for (const line of text.split("\n")) {
    if (!line.startsWith("| ") || line.startsWith("| File ") || line.startsWith("| ---")) continue;
    const cells = line.split("|").slice(1, -1).map((cell) => cell.trim());
    assert.equal(cells.length, 8, `a ledger row has eight cells: ${line.slice(0, 80)}`);
    const [file, symbol, kind, count, ruling, reason, mutants, tests] = cells;
    rows.push({ file, symbol, kind, count: Number(count), ruling, reason, mutants: mutants === "-" ? [] : mutants.split(",").map((m) => m.trim()), tests, line });
  }
  return { text, rows };
}

function testTitles() {
  const titles = [];
  for (const file of walk(testDir, (name) => name.endsWith(".pw.ts") || name.endsWith(".test.mjs"))) {
    const source = readFileSync(file, "utf8");
    for (const match of source.matchAll(/\btest\(\s*("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|`(?:[^`\\]|\\.)*`)/g)) titles.push(match[1].slice(1, -1));
  }
  return titles;
}

const mutants = JSON.parse(readFileSync(join(testDir, "mutants.json"), "utf8"));

test("every asynchronous call in the app's src has a ledger entry with the right count", () => {
  const { rows } = readLedger();
  const found = census();
  const wild = rows.filter((row) => row.symbol === "*");
  const explicit = new Map(rows.filter((row) => row.symbol !== "*").map((row) => [`${row.file}\t${row.symbol}\t${row.kind}`, row]));
  const problems = [];
  const coveredByWild = new Map(wild.map((row) => [`${row.file}\t${row.kind}`, 0]));
  for (const [key, count] of found) {
    const [file, symbol, kind] = key.split("\t");
    const row = explicit.get(key);
    if (row !== undefined) {
      if (row.count !== count) problems.push(`${file} ${symbol} ${kind}: the code has ${count}, the ledger says ${row.count}`);
      continue;
    }
    if (coveredByWild.has(`${file}\t${kind}`)) {
      coveredByWild.set(`${file}\t${kind}`, coveredByWild.get(`${file}\t${kind}`) + count);
      continue;
    }
    problems.push(`${file} ${symbol} ${kind} (${count}): no ledger entry; add it with its guard or its reason it is safe`);
  }
  for (const row of wild) {
    const seen = coveredByWild.get(`${row.file}\t${row.kind}`);
    if (seen !== row.count) problems.push(`${row.file} * ${row.kind}: the code has ${seen} outside the named rows, the ledger says ${row.count}`);
  }
  for (const [key, row] of explicit) {
    if (!found.has(key)) problems.push(`${row.file} ${row.symbol} ${row.kind}: the ledger names code that no longer exists`);
  }
  assert.deepEqual(problems, []);
});

test("every ledger entry rules the call guarded or safe, and says why", () => {
  const { rows } = readLedger();
  const problems = [];
  for (const row of rows) {
    const where = `${row.file} ${row.symbol} ${row.kind}`;
    if (row.ruling !== "GUARDED" && row.ruling !== "SAFE") problems.push(`${where}: the ruling is GUARDED or SAFE, not "${row.ruling}"`);
    if (row.reason.length < 25) problems.push(`${where}: the guard or the reason is missing`);
    if (!Number.isInteger(row.count) || row.count < 1) problems.push(`${where}: the count is not a number`);
    if (row.ruling === "GUARDED" && row.mutants.length === 0) problems.push(`${where}: a guarded entry names the mutants that take its guard away`);
  }
  assert.deepEqual(problems, []);
});

test("every mutant the ledger names exists, still applies to the source once, and has a real test", () => {
  const { rows } = readLedger();
  const titles = testTitles();
  const byId = new Map(mutants.map((m) => [m.id, m]));
  const problems = [];
  for (const row of rows) {
    for (const id of row.mutants) {
      const mutant = byId.get(id);
      if (mutant === undefined) {
        problems.push(`${row.file} ${row.symbol}: the mutant ${id} is not in test/mutants.json`);
        continue;
      }
      const source = readFileSync(join(appDir, mutant.file), "utf8");
      const times = source.split(mutant.find).length - 1;
      if (times !== 1) problems.push(`${id}: its edit applies ${times} times to ${mutant.file}, it must apply once`);
      if (!titles.some((title) => title.includes(mutant.test))) problems.push(`${id}: no test is titled with "${mutant.test}"`);
      if (!row.tests.includes(mutant.test)) problems.push(`${row.file} ${row.symbol}: the Tests cell does not name "${mutant.test}" for ${id}`);
    }
  }
  assert.deepEqual(problems, []);
});

test("every mutant is named by a ledger entry, and a mutant changes the source", () => {
  const { rows } = readLedger();
  const named = new Set(rows.flatMap((row) => row.mutants));
  const problems = [];
  for (const mutant of mutants) {
    if (!named.has(mutant.id)) problems.push(`${mutant.id}: no ledger entry names this mutant`);
    if (mutant.find === mutant.replace) problems.push(`${mutant.id}: the edit changes nothing`);
  }
  assert.deepEqual(problems, []);
});

// The needles are built in pieces so this file does not trip the dash rule itself.
const DASH = new RegExp(`[${String.fromCharCode(0x2013)}${String.fromCharCode(0x2014)}]`);

test("the ledger follows the house rules: no em or en dash and no calendar date", () => {
  const { text } = readLedger();
  const dash = text.split("\n").flatMap((line, index) => (DASH.test(line) ? [`line ${index + 1}`] : []));
  const date = text.split("\n").flatMap((line, index) => (/\b(19|20)\d\d[-/]\d\d[-/]\d\d\b/.test(line) ? [`line ${index + 1}`] : []));
  assert.deepEqual(dash, []);
  assert.deepEqual(date, []);
});

// The census sees every kind of call that starts or waits for asynchronous work, not only the ones with an await.
const BLIND_SPOTS = [
  ["a bare call to the client that nobody waits for", "class A { client: { put(): Promise<void> } | null = null; m() { this.client?.put(); } }", "floating call"],
  ["an unvoided call to a method of another object", "declare const store: { flush(): Promise<void> }; class A { m() { store.flush(); } }", "floating call"],
  ["a catch chain", "class A { m(p: Promise<void>) { void p.catch(() => undefined); } }", "catch"],
  ["a finally chain", "class A { m(p: Promise<void>) { void p.finally(() => undefined); } }", "finally"],
  ["a listener whose arrow body returns a promise", "class A { async g() {} onX = () => this.g(); }", "promise arrow"],
  ["a promise kept in a field", "class A { p: Promise<void> | null = null; async g() {} m() { this.p = this.g(); } }", "stored promise"],
  ["a for await loop", "class A { async m(it: AsyncIterable<number>) { for await (const x of it) { void x; } } }", "for await"],
  // Round 5 (the principal's P3-2): six shapes the census missed, each fed as its own small fixture source.
  ["a block bodied arrow that returns the promise it starts", "class A { async g(): Promise<number> { return 1; } onX = () => { return this.g(); }; }", "promise arrow"],
  ["an unawaited stored promise kept in a local by declaration", "class A { async g() {} m() { const p = this.g(); void p; } }", "stored promise"],
  ["a short circuit statement whose arm starts the work", "class A { c = false; async load() {} m() { this.c && this.load(); } }", "floating call"],
  ["a ternary statement with a promise branch", "class A { c = false; async a() {} async b() {} m() { this.c ? this.a() : this.b(); } }", "floating call"],
  ["a promise passed to another call as an argument", "declare function run(p: Promise<void>): void; class A { async g() {} m() { run(this.g()); } }", "floating call"],
  ["a field initializer that starts async work", "class A { async g() {} p = this.g(); }", "stored promise"],
];

for (const [name, source, kind] of BLIND_SPOTS) {
  test(`the census counts ${name}`, () => {
    const kinds = [...censusOfText(source).keys()].map((key) => key.split("\t")[2]);
    assert.ok(kinds.includes(kind), `expected a ${kind}, the census found: ${kinds.join(", ") || "nothing"}`);
  });
}

test("the census does not count a call that is voided or awaited as floating", () => {
  const source = "class A { async g() {} m() { void this.g(); } async n() { await this.g(); } }";
  const kinds = [...censusOfText(source).keys()].map((key) => key.split("\t")[2]);
  assert.ok(!kinds.includes("floating call"));
});
