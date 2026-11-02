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
// bare `playwright test` run is divided with --shard=k/m across `parts`
// units of the measured weights in `partWeights`; the package's typecheck, node
// tests and extra Playwright configs are one more unit (part 0, weight
// `extras`) so no part carries them on top of its share. The evening guard's two date sensitive
// specs are one more unit, run under TZ=America/Toronto. The Work shell's
// acceptance suite on the assembled real bundles (WS_DIST) is another: it
// assembles tauri/hh-work/dist, then runs the suite against it.
//
// Every shard validates every package first (the guards the old serial loop
// had), so no assignment can hide a package from a guard.
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export const DEFAULT_WEIGHT = 0.8;
export const EVENING_GUARD = "evening-guard-web";
// The two days the evening guard pins drive-run's evening legs to, far from any clock change.
export const EVENING_DAYLIGHT_DAY = "2027-07-14"; // mid summer: the dealer's zone is on daylight time
export const EVENING_STANDARD_DAY = "2027-01-13"; // mid winter: the dealer's zone is on standard time
export const WORK_SHELL_ASSEMBLED = "work-shell-assembled";
export const BROWSER_PACKAGE = "design-system";
// The two acceptance files whose tests are the assembled unit's point: they
// exercise the real desk bundles and only run when WS_DIST names a dist.
export const ASSEMBLED_LEGS = ["framed-band.pw.ts", "real-bundles.pw.ts"];

// Minutes of runner time per unit, read from the per unit timestamps of the
// first sharded run on a pull request (the unit's own typecheck plus test,
// setup excluded). Packages not listed take DEFAULT_WEIGHT.
export const WEIGHTS = {
  "money-desk": 7.8,
  "manager-home": 2.8,
  "front-door": 2.5,
  "drive-run": 2.5,
  "yard-pick": 2.5,
  "buying-and-stock": 2.4,
  "house-messages": 1.9,
  "dispatch-board": 1.8,
  "dealer-admin": 1.7,
  "pro-procurement": 1.6,
  "public-desk": 1.5,
  "pro-field": 1.0,
  "pro-pay": 1.0,
  "design-system": 0.8,
  "work-shell": 0.6,
  "_role-template": 0.2,
  auth: 0.2,
  "api-client": 0.1,
  "offline-store": 0.1,
  "pos-kit": 0.1,
  "pro-kit": 0.1,
  "shell-chrome": 0.1,
  "work-offline": 0.1,
  [EVENING_GUARD]: 4.5, // 3.7 plus 0.8 for the two pinned drive-run reruns, measured locally
  [WORK_SHELL_ASSEMBLED]: 1.0, // read from the per unit timestamps of its first sharded run, as above
};
// Playwright's --shard=k/m deals whole test files out, and a part's time
// moves several minutes between runs with the files it holds (measured: the
// same four Quote Desk parts ran 12.3, 4.6, 10.3, 7.0 and then 12.3, 8.1, 6.9,
// 9.3 minutes). So the split is fine grained and the parts are weighted
// equally at their mean, letting the packer average the noise out. extras is
// the package's typecheck, node tests and extra configs.
export const SPLITS = {
  "quote-desk": { parts: 8, partWeights: Array(8).fill(4.6), extras: 2.0 },
  "counter-till": { parts: 3, partWeights: Array(3).fill(4.2), extras: 1.2 },
};

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
    const split = splits[pkg.name];
    if (!split) {
      units.push({ id: pkg.name, pkg: pkg.name, part: 1, parts: 1, weight: weights[pkg.name] ?? DEFAULT_WEIGHT });
      continue;
    }
    units.push({ id: `${pkg.name}#extras`, pkg: pkg.name, part: 0, parts: split.parts, weight: split.extras });
    for (let part = 1; part <= split.parts; part += 1) {
      units.push({ id: `${pkg.name}#${part}/${split.parts}`, pkg: pkg.name, part, parts: split.parts, weight: split.partWeights[part - 1] });
    }
  }
  units.push({ id: EVENING_GUARD, pkg: EVENING_GUARD, part: 1, parts: 1, weight: weights[EVENING_GUARD] ?? DEFAULT_WEIGHT });
  units.push({ id: WORK_SHELL_ASSEMBLED, pkg: WORK_SHELL_ASSEMBLED, part: 1, parts: 1, weight: weights[WORK_SHELL_ASSEMBLED] ?? DEFAULT_WEIGHT });
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

// Passed tests per real-bundle leg in a Playwright JSON report. The report is
// one suite per file, each suite and spec naming its file, each spec carrying
// one test per project whose `results` hold a run status each; a test counts
// only when a result actually passed, so a skipped test never does.
export function assembledLegPasses(report) {
  const passed = {};
  for (const leg of ASSEMBLED_LEGS) passed[leg] = 0;
  const stack = [...(report?.suites ?? [])];
  while (stack.length > 0) {
    const suite = stack.pop();
    for (const child of suite?.suites ?? []) stack.push(child);
    for (const spec of suite?.specs ?? []) {
      const leg = ASSEMBLED_LEGS.find((name) => (spec?.file ?? suite?.file ?? "").endsWith(name));
      if (leg) {
        passed[leg] += (spec?.tests ?? []).filter((test) =>
          (test?.results ?? []).some((result) => result?.status === "passed")
        ).length;
      }
    }
  }
  return passed;
}

// False when a leg stopped running. Playwright exits 0 when every test skips,
// so an emptied WS_DIST or a stray test.skip would otherwise turn the assembled
// unit into a green no-op; the unit fails loud on this instead.
export function assembledLegsRan(report) {
  const passed = assembledLegPasses(report);
  return ASSEMBLED_LEGS.every((leg) => passed[leg] > 0);
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
    // The dealer's evening clock reads an hour apart on a daylight day and a standard day, so
    // the two drive-run specs rerun on one of each, pinned through DRIVE_EVENING_DATE.
    for (const day of [EVENING_DAYLIGHT_DAY, EVENING_STANDARD_DAY]) {
      run(
        "npx --no-install playwright test test/ux-17bc.pw.ts test/run-sheet.pw.ts",
        join(webRoot, "apps/drive-run"),
        { ...env, DRIVE_EVENING_DATE: day },
      );
    }
    return 1;
  }
  if (unit.pkg === WORK_SHELL_ASSEMBLED) {
    // The only gate on the framed band, the framed account menu and the framed
    // sign out: the same acceptance suite, on the real desk bundles the host
    // frames. Without --config Playwright would pick up the node unit files.
    // Playwright exits 0 when every test skips, so the run also writes its
    // JSON report and the unit fails loud unless both real-bundle legs passed
    // a test: an emptied WS_DIST or a stray test.skip fails here, not green.
    const repoRoot = join(webRoot, "..");
    const reportPath = join(webRoot, "apps/work-shell", "test-results", "assembled-legs.json");
    run("bash tauri/hh-work/assemble-dist.sh", repoRoot);
    run(
      "npx --no-install playwright test --config test/acceptance.config.ts --reporter=list,json",
      join(webRoot, "apps/work-shell"),
      {
        WS_DIST: join(repoRoot, "tauri/hh-work/dist"),
        PLAYWRIGHT_JSON_OUTPUT_FILE: reportPath,
      },
    );
    if (!existsSync(reportPath)) {
      console.error("the assembled real-bundle legs did not run: Playwright wrote no JSON report");
      process.exit(1);
    }
    const report = JSON.parse(readFileSync(reportPath, "utf8"));
    if (!assembledLegsRan(report)) {
      const passed = assembledLegPasses(report);
      const zero = ASSEMBLED_LEGS.filter((leg) => passed[leg] === 0)
        .map((leg) => `${leg} 0 passed`)
        .join(", ");
      console.error(`the assembled real-bundle legs did not run: ${zero}`);
      process.exit(1);
    }
    return 1;
  }
  const pkg = pkgByName.get(unit.pkg);
  const extras = unit.part === 0;
  const split = unit.parts > 1 || extras;
  // A split package's extras unit (or an unsplit package) carries the typecheck.
  if ((extras || !split) && pkg.scripts.typecheck) run("npm run typecheck", pkg.dir);
  if (!pkg.scripts.test) return 0;
  if (!split) {
    run("npm test", pkg.dir);
    return 1;
  }
  const segments = splitTestScript(pkg.scripts.test);
  if (!segments.some((s) => s.shardable)) {
    console.error(`${unit.pkg} is split in SPLITS but its test script has no bare "playwright test" to shard`);
    process.exit(1);
  }
  for (const segment of segments) {
    if (extras && !segment.shardable) run(segment.command, pkg.dir);
    else if (!extras && segment.shardable) run(`${segment.command} --shard=${unit.part}/${unit.parts}`, pkg.dir);
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
  const unknown = pkgs.filter((p) => !(p.name in WEIGHTS) && !(p.name in SPLITS)).map((p) => p.name);
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
