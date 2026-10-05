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
import { census, walk } from "./await-census.mjs";

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
    if (row.ruling === "SAFE" && row.mutants.length > 0) problems.push(`${where}: a safe entry has no guard to take away`);
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

test("the ledger follows the house rules: no em or en dash and no calendar date", () => {
  const { text } = readLedger();
  const dash = text.split("\n").flatMap((line, index) => (/[–—]/.test(line) ? [`line ${index + 1}`] : []));
  const date = text.split("\n").flatMap((line, index) => (/\b(19|20)\d\d[-/]\d\d[-/]\d\d\b/.test(line) ? [`line ${index + 1}`] : []));
  assert.deepEqual(dash, []);
  assert.deepEqual(date, []);
});
