// The server decides every figure of the ledger; the app only formats
// (FROM-PRINCIPAL 73). This gate holds the ledger slice's sources to that: no
// reduce, and no sum or difference of cents anywhere a person could read it as
// the app's own answer.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const glDir = join(dirname(fileURLToPath(import.meta.url)), "..", "src", "gl");

test("the ledger sources compute no money", () => {
  const offenders = [];
  for (const name of readdirSync(glDir).filter((f) => f.endsWith(".ts"))) {
    const lines = readFileSync(join(glDir, name), "utf8").split("\n");
    lines.forEach((line, i) => {
      const code = line.replace(/\/\/.*$/, "");
      if (/allocateOldestFirst|\.reduce\(/.test(code)) offenders.push(`${name}:${i + 1}: ${line.trim()}`);
      if (/_cents\s*[-+]\s*[\w.]*_?cents|cents\s*[-+]\s*(r|l|p|a)\.\w*cents/.test(code)) offenders.push(`${name}:${i + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(offenders, []);
});
