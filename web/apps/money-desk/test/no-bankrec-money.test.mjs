// The server decides every figure of the reconciliation; the app only formats (FROM-PRINCIPAL 73). This gate holds
// the bank reconciliation slice's sources to that: no reduce, no float multiply or divide of a money figure, and no
// sum or difference of cents anywhere a person could read it as the app's own answer.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const dir = join(dirname(fileURLToPath(import.meta.url)), "..", "src", "bankrec");

test("the bank reconciliation sources compute no money", () => {
  const offenders = [];
  for (const name of readdirSync(dir).filter((f) => f.endsWith(".ts"))) {
    const lines = readFileSync(join(dir, name), "utf8").split("\n");
    lines.forEach((line, i) => {
      const code = line.replace(/\/\/.*$/, "");
      if (/\.reduce\(|parseFloat|Number\.parseFloat|toFixed|Math\.round|Math\.floor|Math\.ceil/.test(code)) offenders.push(`${name}:${i + 1}: ${line.trim()}`);
      if (/\b\w*[cC]ents\w*\s*[-+*/]\s*[\w.]*[cC]ents|[cC]ents\w*\s*[*/]\s*\d|\d\s*[*/]\s*\w*[cC]ents/.test(code)) offenders.push(`${name}:${i + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(offenders, []);
});

test("the gate does flag a sum of cents and a float parse", () => {
  const bad = ["const total = a.amount_cents + b.amount_cents;", "const dollars = parseFloat(text) * 100;", "const x = cents * 100;", "rows.reduce((n, r) => n + r.x, 0)"];
  const flagged = bad.filter(
    (code) =>
      /\.reduce\(|parseFloat|Number\.parseFloat|toFixed|Math\.round|Math\.floor|Math\.ceil/.test(code) ||
      /\b\w*[cC]ents\w*\s*[-+*/]\s*[\w.]*[cC]ents|[cC]ents\w*\s*[*/]\s*\d|\d\s*[*/]\s*\w*[cC]ents/.test(code),
  );
  assert.equal(flagged.length, bad.length);
});
