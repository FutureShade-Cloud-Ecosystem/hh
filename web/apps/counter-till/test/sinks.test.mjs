// The credential's sink scan, as a committed gate (the review round's P2-1).
// The approver's token travels this app only between its mint and its one
// send, so no source line may carry a place a credential could rest in or
// surface from: no storage accessor, no printed word, no cookie, no channel,
// no beacon, no URL or history write. The patterns are the review's own,
// stated here so the gate can be read and argued with; a match fails naming
// the file, the line and the words, and nothing in src may match, because
// this app's contract with the person who taps the manager's card is that the
// credential is memory for one call and nothing else.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const srcDir = join(dirname(fileURLToPath(import.meta.url)), "..", "src");

const SINK =
  /localStorage|sessionStorage|indexedDB|document\.cookie|console\.|setItem|postMessage|BroadcastChannel|sendBeacon|window\.name|history\.(push|replace)State|location\.(hash|search|href) *=/;

// The one file allowed to touch storage holds the till's pane swap flag. It is still scanned, with
// exactly two spans taken off each line before the scan: the read and the write of its one key.
// Anything else on any line (another key, a cookie, a message, a beacon, a fetch) still fails.
const PANE_FILE = "pane-memory.ts";
const PANE_ALLOWED = [
  /globalThis\.localStorage\?\.getItem\(KEY\)/g,
  /globalThis\.localStorage\?\.setItem\(KEY, swapped \? "1" : "0"\)/g,
];
const PANE_EXTRA = /fetch|XMLHttpRequest|WebSocket|EventSource|import\(|require\(|eval|Function\(/;
function withoutPaneAllowance(line) {
  let out = line;
  for (const span of PANE_ALLOWED) out = out.replace(span, "");
  return PANE_EXTRA.test(out) ? `${out} <not allowed in the pane file>` + "localStorage" : out;
}

function* sources(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* sources(path);
    else if (entry.name.endsWith(".ts")) yield path;
  }
}

test("no source of the app carries a place a credential could rest in or surface from", () => {
  const offenders = [];
  for (const path of sources(srcDir)) {
    const rel = relative(srcDir, path);
    const lines = readFileSync(path, "utf8").split("\n");
    lines.forEach((line, index) => {
      if (SINK.test(rel === PANE_FILE ? withoutPaneAllowance(line) : line)) offenders.push(`${rel}:${index + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(offenders, []);
});

test("the pane memory file keeps exactly one key, one read and one write", () => {
  const text = readFileSync(join(srcDir, PANE_FILE), "utf8");
  assert.equal([...text.matchAll(/const KEY = /g)].length, 1);
  assert.match(text, /const KEY = "hh-till-panes-swapped";/);
  assert.equal([...text.matchAll(/getItem\(/g)].length, 1);
  assert.equal([...text.matchAll(/setItem\(/g)].length, 1);
  assert.doesNotMatch(text, /token|credential|approver|secret|password/i);
});

test("the pane file's allowance is narrow: anything beyond its one key still trips the scan", () => {
  const line = (text) => SINK.test(withoutPaneAllowance(text));
  assert.equal(line('return globalThis.localStorage?.getItem(KEY) === "1";'), false);
  assert.equal(line('globalThis.localStorage?.setItem(KEY, swapped ? "1" : "0");'), false);
  assert.equal(line('globalThis.localStorage?.getItem("other");'), true);
  assert.equal(line('globalThis.localStorage?.setItem("other", "1");'), true);
  assert.equal(line("document.cookie = x;"), true);
  assert.equal(line("window.postMessage(x, '*');"), true);
  assert.equal(line("navigator.sendBeacon(u, x);"), true);
  assert.equal(line("void fetch(u);"), true);
  assert.equal(line("globalThis.sessionStorage.getItem(KEY);"), true);
  assert.equal(line("indexedDB.open(KEY);"), true);
});
