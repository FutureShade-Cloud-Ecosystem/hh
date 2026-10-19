// The console legs' fixed ports sit below the kernel's ephemeral range. Each app's console config takes five
// consecutive ports from a fixed base (test/console-harness.ts); a base inside 32768 to 60999 (Linux's default
// ip_local_port_range) can be handed to an outgoing connection at any moment, and a runner's EADDRINUSE then fails the
// whole web step after every leg passed. The bases are read from the configs' text.
//
// The guard scans every apps/<app>/test/*.config.ts file and picks the ones that call consoleHarness(...): the
// filename `console.config.ts` is conventional but not required, and a console config under any other name
// (for example `console-pro.config.ts`) must still be checked. The firstPort literal in a TS source can use
// the numeric separator `_` (a TS 2.7+ numeric literal feature), so the regex matches [\d_]+ and the underscores
// are stripped before Number() parses the value.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const appsDir = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "apps");
const proKitDir = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "pro-kit");
const EPHEMERAL_LOW = 32768;

function consoleConfigFor(app) {
  const testDir = join(appsDir, app, "test");
  if (!existsSync(testDir)) return [];
  const found = [];
  for (const file of readdirSync(testDir)) {
    if (!file.endsWith(".config.ts")) continue;
    const path = join(testDir, file);
    if (!readFileSync(path, "utf8").includes("consoleHarness(")) continue;
    found.push({ path, label: `${app}/${file}` });
  }
  return found;
}

function readFirstPort(path) {
  const src = readFileSync(path, "utf8");
  const m = /firstPort:\s*([\d_]+)/.exec(src);
  return { match: m, src };
}

test("every app's console legs take their five ports below the ephemeral range, without overlapping", () => {
  const bases = [];
  for (const app of readdirSync(appsDir)) {
    const configs = consoleConfigFor(app);
    for (const { path, label } of configs) {
      const { match } = readFirstPort(path);
      assert.ok(match, `${label}: the console config names its firstPort`);
      const base = Number(match[1].replaceAll("_", ""));
      assert.ok(
        base + 4 < EPHEMERAL_LOW,
        `${label}: console ports ${base} to ${base + 4} sit inside the ephemeral range`,
      );
      bases.push([label, base]);
    }
  }
  assert.ok(bases.length >= 3, "the console configs were found");
  bases.sort((a, b) => a[1] - b[1]);
  for (let i = 1; i < bases.length; i += 1) {
    assert.ok(
      bases[i][1] >= bases[i - 1][1] + 5,
      `${bases[i - 1][0]} and ${bases[i][0]} share console ports`,
    );
  }
});

test("the pro-kit free-port band does not overlap the console bases 28170..28189", () => {
  // The free-port probe skips only a port that is bound at that instant, so a Pro Playwright config handed
  // a port from inside a console base block collides with the console leg the moment that leg binds: the
  // exact EADDRINUSE flake the probe exists to remove. The guard reads the band constants from the module's
  // text (the band is a pair of literals, not a computed export) and asserts the whole band sits clear of
  // every console base block; a band overlapping 28170..28189 fails here.
  const src = readFileSync(join(proKitDir, "test-stub", "free-port.mjs"), "utf8");
  const low = /const BAND_LOW = (\d+);/.exec(src);
  const high = /const BAND_HIGH = (\d+);/.exec(src);
  assert.ok(low, "free-port.mjs names its BAND_LOW literal");
  assert.ok(high, "free-port.mjs names its BAND_HIGH literal");
  const bandLow = Number(low[1]);
  const bandHigh = Number(high[1]);
  assert.ok(bandLow <= bandHigh, "the band is not inverted");
  assert.ok(
    bandHigh < 28170 || bandLow > 28189,
    `the free-port band ${bandLow}..${bandHigh} overlaps the console bases 28170..28189`,
  );
  assert.ok(bandHigh < EPHEMERAL_LOW, `the band's top ${bandHigh} sits inside the ephemeral range`);
});

test("the firstPort regex accepts TS numeric separator underscores, parsed as plain integers", () => {
  // The regex is the one the guard above uses; these samples pin both
  // the form (\d+) that everyone writes today and the [\d_]+ form TS
  // 2.7+ allows and a careless guard would truncate on. Without the
  // underscore-tolerant pattern, `56_175` would be read as `56` and the
  // port would silently land outside the hand-out band.
  const cases = [
    { raw: "28170", parsed: 28170 },
    { raw: "56_175", parsed: 56175 },
    { raw: "1_000", parsed: 1000 },
    { raw: "281_175", parsed: 281175 },
    { raw: "30_000", parsed: 30000 },
    { raw: "1_2_3_4", parsed: 1234 },
  ];
  for (const c of cases) {
    const src = `firstPort: ${c.raw}`;
    const m = /firstPort:\s*([\d_]+)/.exec(src);
    assert.ok(m, `regex matched ${src}`);
    const value = Number(m[1].replaceAll("_", ""));
    assert.equal(value, c.parsed, `parsed value of ${c.raw}`);
    assert.ok(!Number.isNaN(value), `${c.raw} parsed to a number`);
    assert.ok(Number.isInteger(value), `${c.raw} parsed to an integer`);
  }
  // The regex must capture a numeric literal that follows the bareword
  // `firstPort`, with the same shape one would see in a TS source. A guard
  // that read `(\d+)` (the old form) would only see `56` and would not
  // notice the trailing `175` at all; we pin both the match and the parsed
  // integer for the brief's example `56_175`.
  const example = /firstPort:\s*([\d_]+)/.exec("firstPort: 56_175");
  assert.ok(example, "firstPort: 56_175 matches");
  assert.equal(example[1], "56_175", "the literal `_`-separated form is preserved");
  assert.equal(Number(example[1].replaceAll("_", "")), 56175, "stripping `_` then parsing yields the full integer");
});
