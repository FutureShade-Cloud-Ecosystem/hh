// The server decides every figure of the tax screens; the app only formats (FROM-PRINCIPAL 73). This gate holds
// the tax slice's sources to that: no reduce, no float multiply of a typed rate, and no sum or difference of
// cents anywhere a person could read it as the app's own answer. It also holds the words: no Canadian tax word
// or currency in any string a person reads.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const taxDir = join(dirname(fileURLToPath(import.meta.url)), "..", "src", "tax");
const files = readdirSync(taxDir).filter((f) => f.endsWith(".ts"));

test("the tax sources compute no money", () => {
  const offenders = [];
  for (const name of files) {
    const lines = readFileSync(join(taxDir, name), "utf8").split("\n");
    lines.forEach((line, i) => {
      const code = line.replace(/\/\/.*$/, "");
      if (/\.reduce\(|parseFloat|Number\.parseFloat/.test(code)) offenders.push(`${name}:${i + 1}: ${line.trim()}`);
      if (/\*\s*100\b|\bcents?\s*[-+*/]\s*[\w.]*cents?\b|_cents\s*[-+]\s*[\w.]*_?cents/.test(code)) offenders.push(`${name}:${i + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(offenders, []);
});

test("no screen word names a Canadian tax or currency", () => {
  // The core's reason codes and field names are wire values and appear in tables that translate them; a string a
  // person reads is any quoted text outside those lookups. The words below must not appear in a view or the desk.
  const offenders = [];
  for (const name of files.filter((f) => /views|desk|styles/.test(f))) {
    const lines = readFileSync(join(taxDir, name), "utf8").split("\n");
    lines.forEach((line, i) => {
      const code = line.replace(/\/\/.*$/, "");
      if (/\b(?!GST\/HST\b)(?<!\bGST\/)(HST|GST)\b|\b(PST|CAD|Ontario|TCRD)\b|band.card|First Nation/i.test(code)) offenders.push(`${name}:${i + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(offenders, []);
});
