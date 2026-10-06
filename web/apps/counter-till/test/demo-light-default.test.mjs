// The demo build starts in light (decision log row 978): the till's
// ThemeController initial value is "auto" in a default build and "light" in a
// demo build. The proof is the shipped bundle, because the Vite mode gate is
// constant-folded at build time. This test builds the app both ways into its
// own scratch directory and reads the ThemeController's minified initializer.
import { after, test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readdirSync, readFileSync, rmSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const scratchDir = join(appDir, "test", ".demo-light-default-build");

/** Matches the minified ThemeController class and captures its first field's string value. */
const THEME_CONTROLLER_INIT = /class\{#[a-z]=`(\w+)`;#[a-z]=null;#[a-z]=\(\)=>this\.apply\(\)/;

function* files(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* files(path);
    else yield path;
  }
}

/**
 * Build the app the named way and return the dist directory's files. The
 * default way is no flag at all; the demo way passes the Vite mode flag the
 * demo image's build carries. Each build empties its own outDir under this
 * test's scratch root, which the test removes whole when it ends.
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

function findThemeControllerInit(built) {
  const jsFiles = built.filter((file) => file.name.endsWith(".js"));
  assert.ok(jsFiles.length > 0, "no JavaScript files were built");
  for (const file of jsFiles) {
    const match = file.text.match(THEME_CONTROLLER_INIT);
    if (match) return { value: match[1], file: file.name };
  }
  assert.fail("the ThemeController initializer was not found in any built JS file");
}

test("a default build of the till starts the ThemeController on Auto", () => {
  const built = build("default", []);
  assert.ok(built.length > 0, "the default build produced no files");
  const init = findThemeControllerInit(built);
  assert.equal(init.value, "auto", `${init.file}: ThemeController starts on ${init.value}, expected Auto`);
});

test("a demo mode build of the till starts the ThemeController on Light", () => {
  const built = build("demo", ["--mode", "demo"]);
  const init = findThemeControllerInit(built);
  assert.equal(init.value, "light", `${init.file}: ThemeController starts on ${init.value}, expected Light`);
});

test("the ThemeController initial value rides only inside the demo build mode gate, at source", () => {
  const source = readFileSync(join(appDir, "src", "theme.ts"), "utf8");
  const field = source.match(/#choice:\s*ThemeChoice\s*=\s*([^;]+);/);
  assert.ok(field, "the ThemeController's #choice field was not found");
  const initializer = field[1].replace(/\s+/g, " ");
  assert.ok(
    initializer.includes('import.meta.env.MODE === "demo"') && initializer.includes('"light"') && initializer.includes('"auto"'),
    "the ThemeController initial value must gate Light on demo mode and fall back to Auto",
  );
});

after(() => {
  rmSync(scratchDir, { recursive: true, force: true });
});
