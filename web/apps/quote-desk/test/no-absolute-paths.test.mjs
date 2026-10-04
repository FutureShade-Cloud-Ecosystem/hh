// No file under the app may carry a workstation absolute path: a path that
// exists on one machine fails on CI (the screenshots once did exactly that).
// The needle is built in two halves so this file does not trip itself.
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { extname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const appDir = fileURLToPath(new URL("..", import.meta.url));
const needle = "/ho" + "me/";
const skipDirs = new Set(["node_modules", "test-results", "dist", "playwright-report"]);
const skipExts = new Set([".woff2", ".png", ".jpg", ".ico"]);

function* walk(dir) {
  for (const name of readdirSync(dir)) {
    if (skipDirs.has(name)) continue;
    const path = join(dir, name);
    if (statSync(path).isDirectory()) yield* walk(path);
    else if (!skipExts.has(extname(name))) yield path;
  }
}

test("no file under the quote desk app carries an absolute workstation path", () => {
  const hits = [];
  for (const path of walk(appDir)) {
    if (readFileSync(path, "utf8").includes(needle)) hits.push(path.slice(appDir.length));
  }
  assert.deepEqual(hits, []);
});
