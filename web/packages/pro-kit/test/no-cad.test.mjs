// The Pro surface is US dollars with California tax: no CA$ or US$ prefix, no
// CAD, and none of the Canadian tax names, anywhere a Pro person reads (the kit,
// the stub and the three apps' sources). Same sweep shape as pos-kit's
// no-currency-prefix suite, widened to the words. Test files are skipped: they
// may name what they forbid.
import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const WEB = fileURLToPath(new URL("../../../", import.meta.url));
const ROOTS = ["packages/pro-kit", "apps/pro-procurement", "apps/pro-pay", "apps/pro-field"];
const SKIP_DIRS = new Set(["node_modules", "dist", "test-results", "playwright-report"]);
const SOURCE = /\.(ts|mjs|js|html|css|md)$/;
const TESTS = /\.(test\.[a-z]+|pw\.ts)$/;
const FORBIDDEN = new RegExp(["\\b(CA|US)\\$", "\\bCAD\\b", "\\b(HST|GST|PST|QST)\\b", "Canad", "\\bCDN\\b", "en-CA"].join("|"));

function walk(dir, out) {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIRS.has(name)) continue;
    const full = join(dir, name);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (SOURCE.test(name) && !TESTS.test(name)) out.push(full);
  }
}

test("no CA$, US$, CAD or Canadian tax name in anything a Pro person reads", () => {
  const files = [];
  // An app that has not landed yet has nothing to sweep; the apps' own commits add them to the walk by existing.
  for (const root of ROOTS) if (existsSync(join(WEB, root))) walk(join(WEB, root), files);
  assert.ok(files.length > 5, "the walk found the Pro sources");
  const hits = [];
  for (const file of files) {
    readFileSync(file, "utf8").split("\n").forEach((line, i) => {
      if (FORBIDDEN.test(line)) hits.push(`${relative(WEB, file).split(sep).join("/")}:${i + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(hits, [], `Canadian money or tax words in Pro sources:\n${hits.join("\n")}`);
});
