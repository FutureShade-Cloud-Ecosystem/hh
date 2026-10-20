// White label (UX-7b): no source file under web/ names the demo dealer. The dealer's name comes from the
// tenant config. Tests, stubs, fixtures, tester pages, demo pages and documents are the demo world and are
// left out of the scan.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const WEB = fileURLToPath(new URL("../../../", import.meta.url));
const NAME = "Gable Landscape Supply";
const SKIP_DIRS = new Set(["node_modules", "dist", ".vite", "test", "tests", "test-stub", "stub", "demo", "public", "docs", "e2e"]);
const EXT = /\.(ts|mjs|js|html|css|json)$/;

function* walk(dir) {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) {
      if (!SKIP_DIRS.has(entry)) yield* walk(path);
    } else if (EXT.test(entry) && !/\.test\.|\.pw\.|-stub\.|package-lock/.test(entry)) {
      yield path;
    }
  }
}

test("no source file under web/ contains the demo dealer's name", () => {
  const hits = [];
  for (const file of walk(WEB)) {
    if (readFileSync(file, "utf8").includes(NAME)) hits.push(relative(WEB, file).split(sep).join("/"));
  }
  assert.deepEqual(hits, [], `the demo dealer's name is built into: ${hits.join(", ")}`);
});
