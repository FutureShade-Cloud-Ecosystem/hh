// Names and words, never ids (round 10 review): no screen falls back to the first eight characters of an id.
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import test from "node:test";

test("no source file shows an id fragment where a name is missing", () => {
  const dir = new URL("../src/", import.meta.url);
  const hits = [];
  for (const name of readdirSync(dir)) {
    if (!name.endsWith(".ts")) continue;
    readFileSync(new URL(name, dir), "utf8").split("\n").forEach((line, i) => {
      if (/\.slice\(0,\s*8\)/.test(line)) hits.push(`${name}:${i + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(hits, []);
});
