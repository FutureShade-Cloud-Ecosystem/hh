// The harness day arithmetic, pinned. An offset counts the house zone's own
// calendar days, including across the zone's DST changes, where adding UTC
// days answered one day short in the fall and one day long in the spring for
// the morning UTC hours (the hours CI's evening Toronto runs sit in). These
// instants are the biting ones; the arithmetic must not depend on the hour a
// run happens to start at.
import { test } from "node:test";
import assert from "node:assert/strict";
import { houseDay, houseDayOffset } from "./house-day.mjs";

const DAY_MS = 86_400_000;
const dayNumber = (date) => Math.floor(Date.parse(`${date}T00:00:00Z`) / DAY_MS);
const gapBetween = (from, to) => dayNumber(to) - dayNumber(from);

test("an offset counts the zone's own calendar days, not UTC days", () => {
  // The fall change (2026-11-01): the evening UTC run that went red in CI,
  // where the register's nine day due term rendered "in 8 days".
  const fall = new Date("2026-10-27T04:36:00Z");
  assert.equal(houseDayOffset(9, fall), "2026-11-05");
  assert.equal(gapBetween(houseDayOffset(0, fall), houseDayOffset(9, fall)), 9);
  // The spring change (2027-03-14): the mirror case, one day long.
  const spring = new Date("2027-03-06T04:36:00Z");
  assert.equal(gapBetween(houseDayOffset(0, spring), houseDayOffset(9, spring)), 9);
  // The ordinary hours were never broken; they stay.
  assert.equal(houseDayOffset(9, new Date("2026-10-27T19:00:00Z")), "2026-11-05");
  assert.equal(houseDayOffset(-30, new Date("2026-10-27T19:00:00Z")), "2026-09-27");
});

test("houseDay is the zone's calendar date an instant falls on", () => {
  assert.equal(houseDay(new Date("2026-10-27T04:36:00Z")), "2026-10-27");
  assert.equal(houseDay(new Date("2026-10-27T03:00:00Z")), "2026-10-26");
});
