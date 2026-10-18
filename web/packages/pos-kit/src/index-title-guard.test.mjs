// Repository guard: no app index.html may carry the literal "Hardscape House".
import { test } from "node:test";
import assert from "node:assert/strict";
import { execSync } from "node:child_process";
import { resolve, dirname } from "node:path";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";

const here =
  typeof import.meta.dirname === "string"
    ? import.meta.dirname
    : dirname(fileURLToPath(import.meta.url));

function findRepoRoot(start) {
  let dir = resolve(start);
  for (let i = 0; i < 10; i += 1) {
    if (existsSync(resolve(dir, "web/apps"))) return dir;
    const parent = resolve(dir, "..");
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error("could not locate repo root from " + start);
}

const repoRoot = findRepoRoot(here);

test("the literal 'Hardscape House' appears in no app index.html", () => {
  const out = execSync(
    'grep -R "Hardscape House" web/apps --include=index.html || true',
    { cwd: repoRoot, encoding: "utf8", shell: true }
  );
  assert.equal(out.trim(), "", `found "Hardscape House" in an index.html:\n${out}`);
});

test("the literal 'HH ' does not appear in any app index.html title tag", () => {
  const out = execSync(
    'grep -R "<title>" web/apps --include=index.html | grep "HH " || true',
    { cwd: repoRoot, encoding: "utf8", shell: true }
  );
  assert.equal(out.trim(), "", `found "HH " title prefix in an index.html:\n${out}`);
});
