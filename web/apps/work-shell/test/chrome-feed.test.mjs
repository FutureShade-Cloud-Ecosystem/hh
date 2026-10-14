// What the chrome is fed: the sync chip and the offline banner follow the
// connection (and the outbox when a store is open; none is open in the host
// today, so idle), and the pins sit on a storage seam that degrades to memory
// when the platform storage throws.
import { test } from "node:test";
import assert from "node:assert/strict";
import { SHELL_SYNC_STATES } from "@hardscapehouse/shell-chrome/src/shell-state.js";
import { bannerModeFor, chipInputFor, pinStorageFrom } from "../src/chrome-feed.js";
import { fakeStorage } from "./support-data.mjs";

test("online and no store open is idle with no banner", () => {
  const chip = chipInputFor({ online: true });
  assert.equal(chip.state, "idle");
  assert.deepEqual([chip.openCards, chip.waiting, chip.attempts, chip.stalls], [0, 0, 0, 0]);
  assert.equal(bannerModeFor({ online: true }), "");
});

test("offline is the offline chip and the offline banner", () => {
  assert.equal(chipInputFor({ online: false }).state, "offline");
  assert.equal(bannerModeFor({ online: false }), "offline");
});

test("every state the feed can produce is one the chip knows", () => {
  for (const online of [true, false]) {
    assert.ok(SHELL_SYNC_STATES.includes(chipInputFor({ online }).state));
  }
});

test("an open store's runner state and outbox health, when handed in, drive the chip", () => {
  const chip = chipInputFor({
    online: true,
    store: { state: "pushing", openCards: 1, waiting: 3, attempts: 0, stalls: 0 },
  });
  assert.deepEqual(chip, { state: "pushing", openCards: 1, waiting: 3, attempts: 0, stalls: 0 });
  // offline outranks a store's idle: the person is told the truth about the connection
  assert.equal(chipInputFor({ online: false, store: { state: "idle", openCards: 0, waiting: 2, attempts: 0, stalls: 0 } }).state, "offline");
  assert.equal(chipInputFor({ online: false, store: { state: "idle", openCards: 0, waiting: 2, attempts: 0, stalls: 0 } }).waiting, 2);
});

test("the pin storage is the platform's when it works and memory when it throws", () => {
  const working = fakeStorage();
  const seam = pinStorageFrom(working);
  seam.write("k", "v");
  assert.equal(working.getItem("k"), "v");
  assert.equal(seam.read("k"), "v");

  const hostile = {
    getItem() {
      throw new Error("denied");
    },
    setItem() {
      throw new Error("denied");
    },
    removeItem() {
      throw new Error("denied");
    },
  };
  const degraded = pinStorageFrom(hostile);
  assert.doesNotThrow(() => degraded.write("k", "v"));
  assert.equal(degraded.read("k"), "v");
  assert.equal(pinStorageFrom(undefined).read("missing"), null);
});
