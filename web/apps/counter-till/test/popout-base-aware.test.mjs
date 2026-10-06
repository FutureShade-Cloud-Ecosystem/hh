// Till pop-out base-awareness (HH-r15): the pop-out must use
// import.meta.env.BASE_URL so it lands under the app's own base, not the
// root. When built with --base=/till/ the pop-out must open /till/?popout=
// not /?popout= (the front door's picker). This test builds the app with
// --base=/till/ and reads the built output to assert the window.open call
// uses the base-aware path.
//
// Red: on main the built file contains window.open("/?popout=" -- the pop-out
// opens the front door's picker instead of the till's sale surface.
// Green: after the fix the built file contains window.open("/till/?popout=" --
// the base-aware path, a same-size focused window.
import { after, test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readdirSync, readFileSync, rmSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const scratchDir = join(appDir, "test", ".popout-base-build");

function* files(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* files(path);
    else yield path;
  }
}

/** Build the app with --base=/till/ and return the dist directory's files. */
function buildWithBaseTill() {
  const outDir = join(scratchDir, "base-till");
  const run = spawnSync(
    "npx",
    ["--no-install", "vite", "build", "--outDir", outDir, "--base", "/till/"],
    { cwd: appDir, encoding: "utf8", timeout: 300_000 },
  );
  assert.equal(run.status, 0, `the --base=/till/ build failed:\n${run.stdout}\n${run.stderr}`);
  return [...files(outDir)].map((path) => ({
    name: relative(outDir, path),
    text: readFileSync(path, "utf8"),
  }));
}

/**
 * The window.open call in the pop-out function. At source it reads:
 *   window.open(`${import.meta.env.BASE_URL}?popout=...`, ...)
 * Vite replaces BASE_URL at build time with the --base value, so a
 * --base=/till/ build produces window.open("/till/?popout=...", ...).
 * The test asserts the built output does NOT contain the root-absolute
 * form (/?popout=) which would open the front door on the demo box.
 */
test("the built app with --base=/till/ opens the pop-out under the till base, not the root", () => {
  const built = buildWithBaseTill();
  assert.ok(built.length > 0, "the build produced no files");

  // Find files that contain a window.open call with popout.
  const popoutFiles = built.filter((f) => f.text.includes("window.open") && f.text.includes("popout"));
  assert.ok(popoutFiles.length > 0, "no built file contains a window.open call for popout");

  for (const file of popoutFiles) {
    // The bug: on main the call uses root-absolute "/?popout=" which on the
    // demo box lands on the front door's picker, not the till.
    // The fix: Vite replaces BASE_URL with "/till/" at build time, so the
    // call becomes window.open("/till/?popout=", ...) -- the till's own page.
    assert.ok(
      !/\bwindow\.open\(`\/\?popout=/.test(file.text),
      `${file.name} contains the root-absolute form window.open(\`/?popout= -- ` +
        `the pop-out opens the front door on the demo box instead of the till`,
    );
    // After the fix the built call uses the base-aware path with the window
    // features string ("popup=yes,width=...") and the opener dimensions.
    assert.ok(
      /\bwindow\.open\(`\/till\/\?popout=/.test(file.text),
      `${file.name} does not contain the base-aware form window.open(\`/till/?popout= -- ` +
        `the pop-out must open under the app's base when built with --base=/till/`,
    );
    // The fix also adds window features (popup=yes,width=...,height=...).
    // The built call separates the three arguments across the minified stream.
    // We assert the base-aware URL argument AND the presence of the popup= feature.
    assert.ok(
      /popup=yes,width=\$\{window\.outerWidth\}/.test(file.text),
      `${file.name} does not contain the popup window features with opener dimensions`,
    );
  }
});

after(() => {
  rmSync(scratchDir, { recursive: true, force: true });
});
