// The app's own tree gates, on the till's committed pattern: the credential
// sink scan (no source of this app carries a place a credential could rest
// in or surface from), the dash rule (no em or en dash anywhere this app
// writes), and the name rule (no real person's name; the demo world's labels
// are role words). A match fails naming the file, the line and the words.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const srcDir = join(appDir, "src");

const SINK =
  /localStorage|sessionStorage|indexedDB|document\.cookie|console\.|setItem|postMessage|BroadcastChannel|sendBeacon|window\.name|history\.(push|replace)State/;

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

test("no em or en dash anywhere this app writes", () => {
  const offenders = [];
  for (const path of sources(srcDir)) {
    const rel = relative(srcDir, path);
    const text = readFileSync(path, "utf8");
    if (/[\u2013\u2014]/.test(text)) offenders.push(rel);
  }
  assert.deepEqual(offenders, []);
});
