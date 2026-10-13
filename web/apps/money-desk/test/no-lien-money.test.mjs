// The server decides every figure of the holdback; the app only formats (FROM-PRINCIPAL 73). This gate holds the
// liens and compliance slices' sources to that: no reduce, no float parse, multiply or round of a money figure, and
// no sum or difference of cents anywhere a person could read it as the app's own answer.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "src");
const FLOAT = /\.reduce\(|parseFloat|Number\.parseFloat|toFixed|Math\.round|Math\.floor|Math\.ceil/;
const CENTS = /\b\w*[cC]ents\w*\s*[-+*/]\s*[\w.]*[cC]ents|[cC]ents\w*\s*[*/]\s*\d|\d\s*[*/]\s*\w*[cC]ents/;

function offenders(code) {
  const code_ = code.replace(/\/\/.*$/, "");
  return FLOAT.test(code_) || CENTS.test(code_);
}

test("the liens and compliance sources compute no money", () => {
  const found = [];
  for (const dir of ["lien", "compliance"]) {
    for (const name of readdirSync(join(root, dir)).filter((f) => f.endsWith(".ts"))) {
      readFileSync(join(root, dir, name), "utf8")
        .split("\n")
        .forEach((line, i) => {
          if (offenders(line)) found.push(`${dir}/${name}:${i + 1}: ${line.trim()}`);
        });
    }
  }
  assert.deepEqual(found, []);
});

test("the gate does flag a sum of cents and a float parse", () => {
  const bad = ["const total = a.outstanding_cents + b.outstanding_cents;", "const dollars = parseFloat(text) * 100;", "const x = cents * 100;", "rows.reduce((n, r) => n + r.x, 0)"];
  assert.equal(bad.filter(offenders).length, bad.length);
});
