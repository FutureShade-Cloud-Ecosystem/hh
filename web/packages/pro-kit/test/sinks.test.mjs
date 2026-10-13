// The kit's own tree gates, on the Pro apps' committed pattern: the credential
// sink scan (nothing of this package's source is a place a credential could rest
// in or surface from), and the dash rule (no em or en dash anywhere the package
// writes). The one sanctioned storage shape is the in memory double the session
// hands the auth custody (memoryStorage in session.ts), which implements the
// auth package's StorageLike interface and so must name setItem; it holds a Map
// and nothing else, and test/session.test.mjs proves no web storage is touched.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const pkgDir = join(dirname(fileURLToPath(import.meta.url)), "..");

const SINK = /localStorage|sessionStorage|indexedDB|document\.cookie|console\.|setItem|postMessage|BroadcastChannel|sendBeacon|window\.name|history\.(push|replace)State/;
const DASH = new RegExp("[\\u2013\\u2014]|&m" + "dash;|&n" + "dash;");

function* files(dir, pattern) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "node_modules" || entry.name.startsWith(".")) continue;
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* files(path, pattern);
    else if (pattern.test(entry.name)) yield path;
  }
}

test("no source of the kit carries a place a credential could rest in or surface from", () => {
  const offenders = [];
  for (const path of files(join(pkgDir, "src"), /\.ts$/)) {
    const rel = relative(pkgDir, path);
    readFileSync(path, "utf8").split("\n").forEach((line, index) => {
      if (!SINK.test(line)) return;
      const sanctioned = rel === "src/session.ts" && /setItem: \(key, value\) => void map\.set\(key, value\)|setItem\(key: string, value: string\)/.test(line);
      if (!sanctioned) offenders.push(`${rel}:${index + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(offenders, []);
});

test("the in memory storage double is the only line that names setItem", () => {
  const hits = [];
  for (const path of files(join(pkgDir, "src"), /\.ts$/)) {
    readFileSync(path, "utf8").split("\n").forEach((line, index) => {
      if (/setItem/.test(line)) hits.push(`${relative(pkgDir, path)}:${index + 1}`);
    });
  }
  assert.equal(hits.length, 1);
  assert.match(hits[0], /^src\/session\.ts:/);
});

test("no em or en dash anywhere the kit writes", () => {
  const offenders = [];
  for (const path of files(pkgDir, /\.(ts|mjs|json|html|md|css)$/)) {
    if (DASH.test(readFileSync(path, "utf8"))) offenders.push(relative(pkgDir, path));
  }
  assert.deepEqual(offenders, []);
});
