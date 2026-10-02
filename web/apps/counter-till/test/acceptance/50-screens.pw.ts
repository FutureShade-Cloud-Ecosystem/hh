// The screenshots held to what a reader needs of them: each is a real picture
// of its own screen, so no two of the run's shots may be byte-identical (a
// repeated picture shows nothing of the second claim), and none is a blank
// page (a blank 1280 by 720 capture is 4254 bytes, measured, so the byte
// floor sits safely above it; a real shot of the story is far bigger). The
// shots' loaded state is held where each is taken (the shot helpers settle
// the page first: the surface's own in-flight word gone, the reads quiet, a
// mounted till showing its loaded chrome), and the exit script holds the
// restart leg's shot to the same rules across the whole folder. This spec
// runs after every spec that takes a shot and before the restart leg.
import { expect, test } from "@playwright/test";
import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

test("no two screenshots of the run are byte-identical, and none is empty", async ({}, testInfo) => {
  const dir =
    process.env.CT_SHOT_DIR ?? join(testInfo.config.rootDir, "..", "..", "acceptance-screens");
  expect(existsSync(dir), "the run's screenshot folder exists").toBe(true);
  const shots = readdirSync(dir).filter((name) => name.endsWith(".png"));
  expect(shots.length, "the run has taken screenshots").toBeGreaterThan(0);
  const seen = new Map<string, string>();
  for (const name of shots) {
    const bytes = readFileSync(join(dir, name));
    // A backstop only: the per shot content assertions are the real proof.
    expect(bytes.length, `${name} is not a blank page`).toBeGreaterThan(10000);
    const digest = createHash("sha256").update(bytes).digest("hex");
    const twin = seen.get(digest);
    expect(twin, `${name} is byte-identical to ${twin}`).toBeUndefined();
    seen.set(digest, name);
  }
});
