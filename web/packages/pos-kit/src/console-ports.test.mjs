// The console legs' fixed ports sit below the kernel's ephemeral range. Each app's console config takes five
// consecutive ports from a fixed base (test/console-harness.ts); a base inside 32768 to 60999 (Linux's default
// ip_local_port_range) can be handed to an outgoing connection at any moment, and a runner's EADDRINUSE then fails the
// whole web step after every leg passed. The bases are read from the configs' text.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const appsDir = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "apps");
const EPHEMERAL_LOW = 32768;

test("every app's console legs take their five ports below the ephemeral range, without overlapping", () => {
  const bases = [];
  for (const app of readdirSync(appsDir)) {
    const config = join(appsDir, app, "test", "console.config.ts");
    if (!existsSync(config)) continue;
    const m = /firstPort:\s*(\d+)/.exec(readFileSync(config, "utf8"));
    assert.ok(m, `${app}: the console config names its firstPort`);
    const base = Number(m[1]);
    assert.ok(base + 4 < EPHEMERAL_LOW, `${app}: console ports ${base} to ${base + 4} sit inside the ephemeral range`);
    bases.push([app, base]);
  }
  assert.ok(bases.length >= 3, "the console configs were found");
  bases.sort((a, b) => a[1] - b[1]);
  for (let i = 1; i < bases.length; i++) {
    assert.ok(bases[i][1] >= bases[i - 1][1] + 5, `${bases[i - 1][0]} and ${bases[i][0]} share console ports`);
  }
});
