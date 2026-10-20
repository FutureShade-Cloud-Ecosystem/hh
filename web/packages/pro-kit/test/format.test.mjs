// Money, tax, quantity and day words. US dollars; tax exactly as the core
// returns it (never recomputed here); an unknown figure reads as a placeholder,
// never as a confident zero; ids and totals ride the mono face.
import { test } from "node:test";
import assert from "node:assert/strict";
import { DEFAULT_HOUSE_ZONE, resetHouseZone, serveHouseZoneFrom } from "../../pos-kit/src/ported/host-facts.ts";
import * as format from "../src/format.ts";

const { NO_FIGURE, money, totalsLine, qtyWithUnit, houseDay, calendarDay, monoStyles } = format;

test("money reads as US dollars with grouping and no currency letters", () => {
  assert.equal(money(123400), "$1,234.00");
  assert.equal(money(0), "$0.00");
  assert.equal(money(5), "$0.05");
  assert.equal(money(-123400), "-$1,234.00");
  assert.doesNotMatch(money(104864000), /[A-Za-z]/);
});

test("an absent or unusable money figure reads as Not priced, never n/a or a zero", () => {
  assert.equal(NO_FIGURE, "Not priced");
  for (const value of [null, undefined, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.equal(money(value), "Not priced");
  }
  assert.notEqual(NO_FIGURE, "$0.00");
  assert.notEqual(NO_FIGURE, "n/a");
});

test("the totals line shows the core's own tax, even when it is not the rate times the subtotal", () => {
  // 7.75 percent of 10000 is 775; the core said 800 (a tax exempt line mix, for instance). It is shown as returned.
  const line = totalsLine({ subtotal_cents: 10000, tax_cents: 800, total_cents: 10800 });
  assert.deepEqual(line, { subtotal: "$100.00", tax: "$8.00", total: "$108.00" });
});

test("the totals line leaves a missing tax or total as Not priced", () => {
  assert.deepEqual(totalsLine({ subtotal_cents: 10000 }), { subtotal: "$100.00", tax: "Not priced", total: "Not priced" });
});

test("a quantity keeps the core's decimal string and appends its unit, or reads Not counted when absent", () => {
  assert.equal(qtyWithUnit("12.5", "SF"), "12.5 SF");
  assert.equal(qtyWithUnit("3", ""), "3");
  assert.equal(qtyWithUnit(null, "EA"), "Not counted");
  assert.equal(qtyWithUnit(undefined, "EA"), "Not counted");
  assert.equal(qtyWithUnit("   ", "SF"), "Not counted");
});

test("a day reads in the dealer's own zone with the year, not UTC, and reads Not set when absent", () => {
  // 02:30 UTC is the evening of the day before in the shared default zone in every season (UTC minus four or five),
  // and 05:30 UTC is already the new day in the shared default zone while still the evening before in Los Angeles.
  resetHouseZone();
  const moment = new Date();
  moment.setUTCHours(2, 30, 0, 0);
  const utcDayBefore = new Date(moment.getTime() - 86_400_000);
  const expected = new Intl.DateTimeFormat("en-US", { timeZone: "UTC", month: "short", day: "numeric", year: "numeric" }).format(utcDayBefore);
  assert.equal(houseDay(moment.toISOString()), expected);
  assert.match(houseDay(moment.toISOString()), /^[A-Z][a-z]{2} \d{1,2}, \d{4}$/);
  const boundary = new Date();
  boundary.setUTCHours(5, 30, 0, 0);
  const inDefault = new Intl.DateTimeFormat("en-US", { timeZone: DEFAULT_HOUSE_ZONE, month: "short", day: "numeric", year: "numeric" }).format(boundary);
  const inPacific = new Intl.DateTimeFormat("en-US", { timeZone: "America/Los_Angeles", month: "short", day: "numeric", year: "numeric" }).format(boundary);
  assert.notEqual(inDefault, inPacific);
  assert.equal(houseDay(boundary.toISOString()), inDefault);
  serveHouseZoneFrom({ business_zone: "America/Los_Angeles" });
  try {
    assert.equal(houseDay(boundary.toISOString()), inPacific);
  } finally {
    resetHouseZone();
  }
  assert.equal(houseDay("not a date"), "Not set");
  assert.equal(houseDay(null), "Not set");
  assert.equal(houseDay(undefined), "Not set");
});

test("a moment reads in the shared host-facts zone with the year and clock time, or Not set when absent", () => {
  assert.equal(typeof format.houseMoment, "function");
  assert.equal("DEFAULT_PRO_ZONE" in format, false);
  assert.equal("serveProZoneFrom" in format, false);
  assert.equal("proZone" in format, false);
  resetHouseZone();
  const moment = new Date();
  moment.setUTCHours(15, 15, 0, 0);
  const inDefault = new Intl.DateTimeFormat("en-US", {
    timeZone: DEFAULT_HOUSE_ZONE,
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(moment);
  assert.equal(format.houseMoment(moment.toISOString()), inDefault);
  assert.match(format.houseMoment(moment.toISOString()), /^[A-Z][a-z]{2} \d{1,2}, \d{4}, \d{1,2}:\d{2} (AM|PM)$/);
  assert.equal(format.houseMoment("not a date"), "Not set");
  assert.equal(format.houseMoment(null), "Not set");
  serveHouseZoneFrom({ business_zone: "America/Los_Angeles" });
  try {
    const pacific = new Intl.DateTimeFormat("en-US", {
      timeZone: "America/Los_Angeles",
      month: "short",
      day: "numeric",
      year: "numeric",
      hour: "numeric",
      minute: "2-digit",
    }).format(moment);
    assert.notEqual(pacific, inDefault);
    assert.equal(format.houseMoment(moment.toISOString()), pacific);
  } finally {
    resetHouseZone();
  }
});

test("the mono sheet names the mono token for ids and totals", () => {
  assert.match(String(monoStyles), /--hh-font-mono/);
});

test("a date only value reads as the same day with its year in every zone, never shifted, or Not set when absent", () => {
  // The core sends due and issue dates as a bare day. Reading one as a UTC instant and showing it in the dealer's
  // zone would move it back a day; a bare day is already the dealer's day. The year is the current one, computed.
  const year = String(new Date().getUTCFullYear());
  assert.equal(calendarDay(`${year}-03-01`), `Mar 1, ${year}`);
  assert.equal(calendarDay(`${year}-03-09`), `Mar 9, ${year}`);
  assert.equal(calendarDay(`${year}-12-31`), `Dec 31, ${year}`);
  assert.equal(calendarDay(`${year}-03-09T08:00:00Z`), `Mar 9, ${year}`, "a time on the end is ignored, not converted");
  assert.equal(calendarDay("soon"), "Not set");
  assert.equal(calendarDay(undefined), "Not set");
  assert.equal(calendarDay(`${year}-13-40`), "Not set");
});
