// The name rule for what a tester reads: no dealer's name in any page this app serves from
// public/ (the tester pages included). History and provenance live in PROVENANCE.md, not here.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const publicDir = join(dirname(fileURLToPath(import.meta.url)), "..", "public");

test("no served page names a dealer in code", () => {
  const hits = [];
  for (const name of readdirSync(publicDir)) {
    if (!/\.(html|js|css|json|txt)$/.test(name)) continue;
    readFileSync(join(publicDir, name), "utf8")
      .split("\n")
      .forEach((line, i) => {
        if (/dibbits/i.test(line)) hits.push(`${name}:${i + 1}`);
      });
  }
  assert.deepEqual(hits, [], `a dealer's name is served to a person at ${hits.join(", ")}`);
});
