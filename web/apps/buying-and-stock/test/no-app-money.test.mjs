// The server converts, apportions and totals every figure of the purchasing areas; the app only
// formats (the Money Desk's rule for its receivables, held here for landed cost, vendors,
// receiving and bills). This gate holds those sources to it: no reduce, and no sum or difference
// of cents anywhere a person could read it as the app's own answer.
import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const src = join(dirname(fileURLToPath(import.meta.url)), "..", "src");
const AREAS = ["landed", "vendors", "receiving", "bills"];

test("the purchasing areas' sources compute no money", () => {
  const offenders = [];
  for (const area of AREAS) {
    const dir = join(src, area);
    if (!existsSync(dir)) continue;
    for (const name of readdirSync(dir).filter((f) => f.endsWith(".ts"))) {
      const lines = readFileSync(join(dir, name), "utf8").split("\n");
      lines.forEach((line, i) => {
        const code = line.replace(/\/\/.*$/, "");
        if (/\.reduce\(/.test(code)) offenders.push(`${area}/${name}:${i + 1}: ${line.trim()}`);
        if (/cents\s*[-+*/]\s*[\w.]*cents|cents\s*[-+*/]\s*\d|\d\s*[-+*/]\s*[\w.]*cents/i.test(code)) offenders.push(`${area}/${name}:${i + 1}: ${line.trim()}`);
        if (/\*\s*100\b|Math\.round\([^)]*\*/.test(code)) offenders.push(`${area}/${name}:${i + 1}: ${line.trim()}`);
      });
    }
  }
  assert.deepEqual(offenders, []);
});
