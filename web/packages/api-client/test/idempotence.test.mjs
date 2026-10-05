// The idempotence gate, in the ERP lane's proven pattern: regenerate into
// scratch directories and byte-compare, both between two fresh runs and
// against the committed generated tree. A document change without a
// regeneration fails here, and a nondeterministic generator fails here.
import { test } from "node:test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readdirSync, readFileSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";

const packageDir = dirname(dirname(fileURLToPath(import.meta.url)));
const repoRoot = join(packageDir, "..", "..", "..");
const generator = join(repoRoot, "scripts", "gen-client", "gen-client.mjs");
const committed = join(packageDir, "src", "gen");

const scratch = () => mkdtempSync(join(tmpdir(), "hh-api-client-gen-"));

const generate = (outDir) =>
  execFileSync(process.execPath, [generator, "--out", outDir], { cwd: repoRoot, encoding: "utf8" });

const snapshot = (dir) =>
  Object.fromEntries(readdirSync(dir).sort().map((f) => [f, readFileSync(join(dir, f), "utf8")]));

test("two fresh generations are byte-identical and match the committed tree", () => {
  const one = scratch();
  const two = scratch();
  try {
    const out1 = generate(one);
    const out2 = generate(two);
    if (!out1.includes("681 operations")) throw new Error(`the generator reported: ${out1.trim()}`);
    const a = snapshot(one);
    const b = snapshot(two);
    const c = snapshot(committed);

    const names = new Set([...Object.keys(a), ...Object.keys(b), ...Object.keys(c)]);
    for (const name of names) {
      if (a[name] === undefined) throw new Error(`${name}: missing from the first run`);
      if (b[name] === undefined) throw new Error(`${name}: missing from the second run`);
      if (c[name] === undefined) throw new Error(`${name}: missing from the committed tree`);
      if (a[name] !== b[name]) throw new Error(`${name}: two runs differ`);
      if (a[name] !== c[name]) throw new Error(`${name}: the committed tree differs from what the document produces`);
    }
  } finally {
    rmSync(one, { recursive: true, force: true });
    rmSync(two, { recursive: true, force: true });
  }
});
