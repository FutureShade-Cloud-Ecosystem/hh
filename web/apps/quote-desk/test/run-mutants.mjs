// The mutation proof for the await ledger. For every entry of test/mutants.json this takes the guard away (the
// exact edit named by `find` and `replace`), runs the one test that must catch it, and expects that test RED. The
// source file is put back after each run, whatever the outcome. A mutant that survives, or an edit whose `find`
// text is not in the file exactly once, fails the run.
//
//   node test/run-mutants.mjs            all mutants
//   node test/run-mutants.mjs terms-read one mutant by id
//
// Not part of the default test script (it runs the browser suite many times); the ledger records each mutant's id
// and test, and test/await-ledger.test.mjs checks the ids and test names are real.
import { spawnSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const mutants = JSON.parse(readFileSync(join(appDir, "test", "mutants.json"), "utf8"));
const only = process.argv[2];
const results = [];

for (const mutant of mutants) {
  if (only !== undefined && mutant.id !== only) continue;
  const path = join(appDir, mutant.file);
  const original = readFileSync(path, "utf8");
  const parts = original.split(mutant.find);
  if (parts.length !== 2) {
    results.push({ id: mutant.id, outcome: `BAD EDIT (find text occurs ${parts.length - 1} times)` });
    continue;
  }
  try {
    writeFileSync(path, parts.join(mutant.replace));
    // A mutant is caught by a browser test, or by a unit test when it names the node runner (and its file).
    const run =
      mutant.runner === "node"
        ? spawnSync(process.execPath, ["--import", "../../packages/api-client/src/_ts-js-loader.mjs", "--test", "--test-name-pattern", mutant.test, mutant.testFile ?? "test/quote-context.test.mjs"], {
            cwd: appDir,
            encoding: "utf8",
            timeout: 240000,
          })
        : spawnSync("npx", ["playwright", "test", "--grep", mutant.test, "--reporter=line"], {
            cwd: appDir,
            env: { ...process.env, CI: "true" },
            encoding: "utf8",
            timeout: 240000,
          });
    const text = `${run.stdout}${run.stderr}`;
    const failed = mutant.runner === "node" ? run.status !== 0 : /\d+ failed/.test(text) && run.status !== 0;
    results.push({ id: mutant.id, outcome: failed ? "RED (killed)" : "GREEN (SURVIVED)", test: mutant.test });
    if (!failed) console.log(text.split("\n").filter((line) => !line.includes("Lit is in dev mode")).slice(-12).join("\n"));
  } finally {
    writeFileSync(path, original);
  }
}

for (const r of results) console.log(`${r.outcome.padEnd(18)} ${r.id}  ${r.test ?? ""}`);
if (results.some((r) => !r.outcome.startsWith("RED"))) process.exitCode = 1;
