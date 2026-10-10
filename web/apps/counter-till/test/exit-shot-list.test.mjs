// The exit script's screenshot ledger, held to the legs it counts (PR 279's
// merge-up, gap 7 of the round 17 acceptance inventory). The script names
// every screenshot the acceptance story takes and pins its count, but nothing
// read the legs back: the returns work renamed the sales pass's shots, took
// three out of the manager pass, and the deposits legs had never been named
// at all, so the ledger checked a list no run could produce and called every
// run drifted. This gate derives the list from the legs themselves (the two
// helpers that write into the shot dir, shot() and shotParts()) and fails
// while the script's list, its count checks, and the legs disagree, so the
// next renamed walk fails here in the unit chain instead of in the exit run.
//
// The restart leg is counted apart: the exit script runs the story first with
// --grep-invert restart, checks the ledger there, and only then runs the
// restart leg, whose one shot the script counts as the story's count plus one.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const ACCEPTANCE = join(here, "acceptance");
const EXIT_SCRIPT = readFileSync(join(ACCEPTANCE, "exit-test.sh"), "utf8");

const RESTART_LEG = "90-restart.pw.ts";

/** Every shot name a leg writes, through either writer in helpers.ts. */
function legShots(file) {
  return [
    ...readFileSync(join(ACCEPTANCE, file), "utf8").matchAll(
      /\b(?:shot|shotParts)\(page, "([^"]+)"/g,
    ),
  ].map((m) => m[1]);
}

const legs = readdirSync(ACCEPTANCE)
  .filter((file) => file.endsWith(".pw.ts"))
  .sort();
const storyShots = legs.filter((file) => file !== RESTART_LEG).flatMap(legShots).sort();
const restartShots = legShots(RESTART_LEG);

test("the shot reader reads the legs it counts", () => {
  assert.ok(
    legs.includes(RESTART_LEG),
    `the restart leg ${RESTART_LEG} is not where this gate looks for it`,
  );
  assert.ok(storyShots.length > 0, "no story shot found: the reader is blind");
});

test("the restart leg takes exactly its one shot", () => {
  assert.deepEqual(
    restartShots,
    [restartShots[0]],
    `the exit script counts the restart leg as the story's count plus ONE shot; it takes ${restartShots.length}`,
  );
});

test("no story shot name is taken twice", () => {
  const doubled = storyShots.filter((name, at) => storyShots.indexOf(name) !== at);
  assert.deepEqual(doubled, [], `two legs write the same shot name: ${doubled.join(", ")}`);
});

test("the exit script's expected list is exactly the legs' own story shots", () => {
  const list = EXIT_SCRIPT.match(/^expected="([^"]+)"/m);
  assert.ok(list, "the exit script no longer carries its expected= list");
  const expected = list[1]
    .split("\n")
    .map((name) => name.trim())
    .filter(Boolean)
    .sort();
  const missing = expected.filter((name) => !storyShots.includes(name));
  const extra = storyShots.filter((name) => !expected.includes(name));
  assert.deepEqual(
    [missing, extra],
    [[], []],
    `the exit script's expected list and the legs' shot() names disagree (the list holds ${expected.length}, the legs take ${storyShots.length}). ` +
      `named in the list but taken by no leg: ${missing.join(", ") || "none"}. ` +
      `taken by a leg but missing from the list: ${extra.join(", ") || "none"}. ` +
      `Derive the list from the legs (every shot() and shotParts() name in test/acceptance minus the restart leg), never hand pick it.`,
  );
});

test("the exit script's count checks follow its own list", () => {
  const storyCount = EXIT_SCRIPT.match(/\[ "\$count" = "(\d+)" \]/);
  const restartCount = EXIT_SCRIPT.match(/\[ "\$restart_shots" = "(\d+)" \]/);
  assert.ok(storyCount, "the exit script no longer pins the story's screenshot count");
  assert.ok(restartCount, "the exit script no longer pins the restart run's screenshot count");
  assert.equal(
    storyCount[1],
    String(storyShots.length),
    `the story count check pins ${storyCount[1]} but the legs take ${storyShots.length} story shots`,
  );
  assert.equal(
    restartCount[1],
    String(storyShots.length + 1),
    `the restart count check pins ${restartCount[1]} but the run ends at the story's ${storyShots.length} plus the restart leg's one`,
  );
});
