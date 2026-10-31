// The stub worlds' month arithmetic (test/stub/clock-zone.mjs): a month before is counted in whole months from
// the month itself, and a named zone's calendar decides the month, so a contract test and the stub it spawns on
// STUB_ZONE name the same month whatever zone either process was left on.
import { test } from "node:test";
import assert from "node:assert/strict";
import { STUB_ZONE, monthsBefore } from "./stub/clock-zone.mjs";

// The instant of main's run 41212453853: still October 31 on UTC, already November 1 in Tokyo.
const TURN = new Date(Date.UTC(2026, 9, 31, 16, 0, 0));

test("the zone named decides the month: one instant is September's month before on UTC and October's in Tokyo", () => {
  assert.deepEqual(monthsBefore(1, "UTC", TURN), { year: 2026, month: 9 });
  assert.deepEqual(monthsBefore(1, "Asia/Tokyo", TURN), { year: 2026, month: 10 });
  assert.equal(STUB_ZONE, "UTC", "the contract tests pin their stub to UTC");
});

test("whole months back from the month itself: the 31st after a 30 day month, March after February, and January", () => {
  assert.deepEqual(monthsBefore(1, "UTC", new Date(Date.UTC(2026, 9, 31, 12))), { year: 2026, month: 9 });
  assert.deepEqual(monthsBefore(1, "UTC", new Date(Date.UTC(2026, 2, 31, 12))), { year: 2026, month: 2 });
  assert.deepEqual(monthsBefore(1, "UTC", new Date(Date.UTC(2026, 2, 29, 12))), { year: 2026, month: 2 });
  assert.deepEqual(monthsBefore(1, "UTC", new Date(Date.UTC(2026, 0, 15, 12))), { year: 2025, month: 12 });
  assert.deepEqual(monthsBefore(2, "UTC", new Date(Date.UTC(2026, 1, 10, 12))), { year: 2025, month: 12 });
});
