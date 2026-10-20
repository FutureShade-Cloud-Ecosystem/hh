#!/usr/bin/env node
// Splits the ci workflow's web suite across parallel shards.
//
//   node scripts/ci-web-shards.mjs plan <total>          print every shard's assignment
//   node scripts/ci-web-shards.mjs run <index> <total>   run shard <index> (1 based) of <total>
//
// Packages are listed at run time from packages/* and apps/*, so a new package
// lands in a shard without an edit here. WEIGHTS holds the measured minutes of
// typecheck plus test per package; a package missing from it takes
// DEFAULT_WEIGHT and still lands in a shard. SPLITS names the packages whose
// bare `playwright test` run is divided with --shard=k/m across that many
// parts; the first part also carries typecheck, the node tests and the
// package's extra Playwright configs. The evening guard's two date sensitive
// specs are one more unit, run under TZ=America/Toronto.
//
// Every shard validates every package first (the guards the old serial loop
// had), so no assignment can hide a package from a guard.
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export const DEFAULT_WEIGHT = 0.8;
export const EVENING_GUARD = "evening-guard-web";
export const BROWSER_PACKAGE = "design-system";

// Minutes, typecheck plus test, from main's last green ci log.
export const WEIGHTS = {
  "quote-desk": 24.8,
  "counter-till": 9.6,
  "money-desk": 5.0,
  "buying-and-stock": 2.5,
  "manager-home": 2.2,
  "front-door": 2.0,
  "drive-run": 1.8,
  "dispatch-board": 1.7,
  "house-messages": 1.5,
  "public-desk": 1.5,
  "dealer-admin": 1.2,
  "pro-procurement": 1.1,
  [EVENING_GUARD]: 2.0,
};
export const SPLITS = { "quote-desk": 3, "counter-till": 2 };

const webRoot = join(dirname(fileURLToPath(import.meta.url)), "..");

export function listPackages(root) {
  const found = [];
  for (const group of ["packages", "apps"]) {
    const base = join(root, group);
    if (!existsSync(base)) continue;
    for (const entry of readdirSync(base).sort()) {
      const dir = join(base, entry);
      if (!statSync(dir).isDirectory() || !existsSync(join(dir, "package.json"))) continue;
      const manifest = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
      found.push({ name: basename(dir), dir, scripts: manifest.scripts ?? {} });
    }
  }
  return found;
}

function hasTsSources(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "node_modules") continue;
    const full = join(dir, entry.name);
    if (entry.isDirectory() ? hasTsSources(full) : entry.name.endsWith(".ts")) return true;
  }
  return false;
}

// The old loop's guards, over every package. Returns the failure messages.
export function validatePackages(pkgs) {
  const failures = [];
  let withTests = 0;
  for (const pkg of pkgs) {
    if (!pkg.scripts.typecheck && hasTsSources(pkg.dir)) {
      failures.push(`${pkg.name} has TypeScript sources but no typecheck script; add one`);
    }
    if (pkg.scripts.test) withTests += 1;
    else if (existsSync(join(pkg.dir, "test"))) {
      failures.push(`${pkg.name} has a test directory but no test script; add one`);
    }
  }
  if (withTests < 1) failures.push("no web package has a test suite; the package listing is broken");
  return failures;
}

export function buildUnits(pkgs, weights, splits) {
  const units = [];
  for (const pkg of pkgs) {
    const weight = weights[pkg.name] ?? DEFAULT_WEIGHT;
    const parts = splits[pkg.name] ?? 1;
    for (let part = 1; part <= parts; part += 1) {
      units.push({ id: parts === 1 ? pkg.name : `${pkg.name}#${part}/${parts}`, pkg: pkg.name, part, parts, weight: weight / parts });
    }
  }
  units.push({ id: EVENING_GUARD, pkg: EVENING_GUARD, part: 1, parts: 1, weight: weights[EVENING_GUARD] ?? DEFAULT_WEIGHT });
  return units;
}

// Longest first into the lightest shard; ties break on id so the result is
// the same on every shard's runner.
export function assign(units, count) {
  const shards = Array.from({ length: count }, () => []);
  const loads = new Array(count).fill(0);
  const order = [...units].sort((a, b) => b.weight - a.weight || (a.id < b.id ? -1 : 1));
  for (const unit of order) {
    let best = 0;
    for (let i = 1; i < count; i += 1) if (loads[i] < loads[best]) best = i;
    shards[best].push(unit);
    loads[best] += unit.weight;
  }
  return shards;
}

export function splitTestScript(script) {
  return script.split("&&").map((part) => {
    const command = part.trim();
    return { command, shardable: command === "playwright test" };
  });
}

function run(command, cwd, extraEnv = {}) {
  console.log(`\n$ ${command}   (in ${basename(cwd)})`);
  const bins = [join(cwd, "node_modules/.bin"), join(webRoot, "node_modules/.bin")];
  const result = spawnSync("sh", ["-c", command], {
    cwd,
    stdio: "inherit",
    env: { ...process.env, ...extraEnv, PATH: `${bins.join(":")}:${process.env.PATH}` },
  });
  if (result.status !== 0) {
    console.error(`FAILED (${result.status ?? result.signal}): ${command} in ${cwd}`);
    process.exit(result.status || 1);
  }
}

function runUnit(unit, pkgByName) {
  console.log(`\n=== ${unit.id} ===`);
  if (unit.pkg === EVENING_GUARD) {
    // The web half of item 55's guard: the date sensitive specs rerun on the
    // house zone; the legs pin their own browser and stub clocks.
    const env = { TZ: "America/Toronto" };
    run("npx --no-install playwright test test/run-sheet.pw.ts", join(webRoot, "apps/drive-run"), env);
    run("npx --no-install playwright test test/sale.pw.ts", join(webRoot, "apps/counter-till"), env);
    return 1;
  }
  const pkg = pkgByName.get(unit.pkg);
  const first = unit.part === 1;
  if (first && pkg.scripts.typecheck) run("npm run typecheck", pkg.dir);
  if (!pkg.scripts.test) return 0;
  if (unit.parts === 1) {
    run("npm test", pkg.dir);
    return 1;
  }
  const segments = splitTestScript(pkg.scripts.test);
  if (!segments.some((s) => s.shardable)) {
    console.error(`${unit.pkg} is split in SPLITS but its test script has no bare "playwright test" to shard`);
    process.exit(1);
  }
  for (const segment of segments) {
    if (segment.shardable) run(`${segment.command} --shard=${unit.part}/${unit.parts}`, pkg.dir);
    else if (first) run(segment.command, pkg.dir);
  }
  return 1;
}

function describe(shards) {
  shards.forEach((shard, i) => {
    const load = shard.reduce((a, u) => a + u.weight, 0).toFixed(1);
    console.log(`shard ${i + 1}/${shards.length} (about ${load} min): ${shard.map((u) => u.id).join(", ") || "(empty)"}`);
  });
}

function main(argv) {
  const [mode, a, b] = argv;
  const pkgs = listPackages(webRoot);
  const failures = validatePackages(pkgs);
  if (failures.length > 0) {
    for (const f of failures) console.error(f);
    process.exit(1);
  }
  const unknown = pkgs.filter((p) => !(p.name in WEIGHTS)).map((p) => p.name);
  const units = buildUnits(pkgs, WEIGHTS, SPLITS);
  if (mode === "plan") {
    const shards = assign(units, Number(a));
    describe(shards);
    if (unknown.length) console.log(`not in the weight table (default ${DEFAULT_WEIGHT} min): ${unknown.join(", ")}`);
    return;
  }
  if (mode === "run") {
    const index = Number(a);
    const total = Number(b);
    if (!Number.isInteger(index) || !Number.isInteger(total) || index < 1 || index > total) {
      console.error("usage: ci-web-shards.mjs run <index> <total>, 1 <= index <= total");
      process.exit(2);
    }
    const shards = assign(units, total);
    describe(shards);
    if (unknown.length) console.log(`not in the weight table (default ${DEFAULT_WEIGHT} min): ${unknown.join(", ")}`);
    const mine = shards[index - 1];
    if (mine.some((u) => u.pkg === BROWSER_PACKAGE)) {
      if (spawnSync("sh", ["-c", "command -v google-chrome >/dev/null 2>&1"]).status !== 0) {
        console.error("google-chrome is missing from the runner image: the design system's browser leg requires it (that leg fails rather than skip in CI)");
        process.exit(1);
      }
      run("google-chrome --version", webRoot);
    }
    const pkgByName = new Map(pkgs.map((p) => [p.name, p]));
    let suites = 0;
    for (const unit of mine) suites += runUnit(unit, pkgByName);
    console.log(`\nshard ${index}/${total}: ${mine.length} units, ${suites} suites ran`);
    if (mine.length > 0 && suites < 1) {
      console.error("this shard was assigned work but ran no test suite; the runner is broken");
      process.exit(1);
    }
    return;
  }
  console.error("usage: ci-web-shards.mjs plan <total> | run <index> <total>");
  process.exit(2);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main(process.argv.slice(2));
