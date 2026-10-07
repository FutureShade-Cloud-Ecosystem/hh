// The seam rule as a build gate (HH-DEMO-1, the Quote Desk's adopt,
// decision log rows 893, 899 and 906): the front door's handoff seam exists
// ONLY where the demo is compiled in. A default build of this app must carry
// neither the front door's session key name nor the adopt that reads it, and
// a demo mode build must carry both. This test builds the app both ways into
// its own scratch directory and reads the built files, because the rule is
// about what SHIPS, not what the sources happen to import: the branch could
// sit in the source and still be dead code the minifier keeps, so the proof
// is the output. The shape is the till's (web/apps/counter-till/test/
// no-default-adopt.test.mjs, demo/front-door 385cd8d6), mirrored for this
// desk.
//
// What the markers are, and why the adopt's name is one of them only in
// spirit: the minifier renames private class members, so the method's own
// name appears in NO build, demo included. The key name is a string literal
// and survives every build, so it is the load-bearing marker. The adopt's
// compiled presence is pinned two ways instead: the default build must not
// carry even the mangled name's source spelling, and the call itself must
// ride only inside the demo build mode gate at source (the third test), the
// same discipline the sink gate (sinks.test.mjs) holds in this app.
import { after, test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readdirSync, readFileSync, rmSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const scratchDir = join(appDir, "test", ".no-default-adopt-build");

/** The front door's own session record key: the seam's one string artifact. */
const FRONT_DOOR_KEY = "hh-demo.front-door.session.v1";
/** The carried theme choice's key: a demo seam like the handoff's, so a default build carries none of it. */
const THEME_KEY = "hh-demo.theme.v1";
/** The adopt's source name: absent from every build's output, asserted anyway. */
const ADOPT_NAME = "adoptFromFrontDoor";

function* files(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* files(path);
    else yield path;
  }
}

/**
 * Build the app the named way and return the dist directory's files. The
 * default way is no flag at all (the same `vite build` the served product
 * rides); the demo way passes the Vite mode flag the demo image's build
 * carries. Each build empties its own outDir under this test's scratch root,
 * which the test removes whole when it ends.
 */
function build(label, args) {
  const outDir = join(scratchDir, label);
  const run = spawnSync("npx", ["--no-install", "vite", "build", "--outDir", outDir, ...args], {
    cwd: appDir,
    encoding: "utf8",
    timeout: 300_000,
  });
  assert.equal(run.status, 0, `the ${label} build failed:\n${run.stdout}\n${run.stderr}`);
  return [...files(outDir)].map((path) => ({
    name: relative(outDir, path),
    text: readFileSync(path, "utf8"),
  }));
}

test("a default build of the desk carries neither the front door's key nor the adopt", () => {
  const built = build("default", []);
  assert.ok(built.length > 0, "the default build produced no files");
  for (const file of built) {
    assert.ok(!file.text.includes(FRONT_DOOR_KEY), `${file.name} carries the front door's key name`);
    assert.ok(!file.text.includes(ADOPT_NAME), `${file.name} carries the adopt's name`);
    assert.ok(!file.text.includes(THEME_KEY), `${file.name} carries the carried theme's key name`);
  }
});

test("a demo mode build of the desk ships the reader and the key it reads", () => {
  const built = build("demo", ["--mode", "demo"]);
  const carriers = built.filter((file) => file.text.includes(FRONT_DOOR_KEY));
  assert.ok(carriers.length > 0, "the demo build carries the front door's key name nowhere");
  assert.ok(built.some((file) => file.text.includes(THEME_KEY)), "the demo build carries the carried theme's key nowhere");
});

test("the adopt call rides only inside the demo build mode gate, at source", () => {
  const source = readFileSync(join(appDir, "src", "app-root.ts"), "utf8");
  const calls = source.match(/this\.#adoptFromFrontDoor\(/g) ?? [];
  assert.equal(calls.length, 1, "the adopt must have exactly one call site");
  const gated =
    /if \(import\.meta\.env\.MODE === "demo"\) \{\s*\n\s*void this\.#adoptFromFrontDoor\(\);\s*\n\s*\}/.test(
      source,
    );
  assert.ok(gated, "the adopt's one call site must sit inside the demo build mode gate");
});

after(() => {
  rmSync(scratchDir, { recursive: true, force: true });
});
