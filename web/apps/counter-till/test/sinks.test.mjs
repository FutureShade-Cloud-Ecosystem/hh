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
      if (SINK.test(line)) offenders.push(`${rel}:${index + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(offenders, []);
});
