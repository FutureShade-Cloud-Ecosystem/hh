// Unit tests for the app's plain modules: the one money helper, and the
// house-day conversion the sale's requested date rides. These run in the same
// Playwright pass (the file is *.pw.ts so the repository root's bun drift
// gate does not pick them up), but they drive no browser: they hold the
// boundary conversions that everything on the counter depends on, including
// the refused forms the first review found the parser accepting.
import { expect, test } from "@playwright/test";
import { formatMargin, formatMoney, parseAmountToCents } from "../src/money.js";
import { DEFAULT_HOUSE_ZONE, houseDayInZone } from "../src/till-client.js";

test("the tendered parser accepts what a till is handed", () => {
  expect(parseAmountToCents("50")).toBe(5000);
  expect(parseAmountToCents("50.5")).toBe(5050);
  expect(parseAmountToCents("50.55")).toBe(5055);
  expect(parseAmountToCents("0.05")).toBe(5);
  expect(parseAmountToCents("0.5")).toBe(50);
  expect(parseAmountToCents(" 40.55 ")).toBe(4055);
  expect(parseAmountToCents("999999999.99")).toBe(99999999999);
});

test("the tendered parser refuses a third decimal, and everything else that is not an amount", () => {
  // The slip the review probed: a third decimal is a mistyped amount, and ten
  // times the money must never leave the till.
  expect(parseAmountToCents("20.005")).toBeNull();
  expect(parseAmountToCents("40.555")).toBeNull();
  expect(parseAmountToCents("1.999")).toBeNull();
  expect(parseAmountToCents("12.")).toBeNull();
  expect(parseAmountToCents(".50")).toBeNull();
  expect(parseAmountToCents("")).toBeNull();
  expect(parseAmountToCents("   ")).toBeNull();
  expect(parseAmountToCents("abc")).toBeNull();
  expect(parseAmountToCents("-5")).toBeNull();
  expect(parseAmountToCents("1,234")).toBeNull();
  expect(parseAmountToCents("1e5")).toBeNull();
  expect(parseAmountToCents("0x10")).toBeNull();
  // The digits bound: ten digits before the point is not a counter amount.
  expect(parseAmountToCents("1234567890")).toBeNull();
});

test("formatMoney renders integer cents, and the placeholder for what the core left out", () => {
  expect(formatMoney(0)).toBe("$0.00");
  expect(formatMoney(1968)).toBe("$19.68");
  expect(formatMoney(7232)).toBe("$72.32");
  expect(formatMoney(123456789)).toBe("$1,234,567.89");
  expect(formatMoney(-311)).toBe("-$3.11");
  expect(formatMoney(null)).toBe("(not priced)");
  expect(formatMoney(undefined)).toBe("(not priced)");
  expect(formatMoney(4.5)).toBe("(not priced)");
});

test("formatMargin rounds half away from zero on both signs", () => {
  expect(formatMargin(1585)).toBe("15.9%");
  expect(formatMargin(3100)).toBe("31.0%");
  expect(formatMargin(0)).toBe("0.0%");
  expect(formatMargin(-155)).toBe("-1.6%");
  expect(formatMargin(155)).toBe("1.6%");
  expect(formatMargin(null)).toBe("(no margin)");
  expect(formatMargin(undefined)).toBe("(no margin)");
});

test("the sale's requested date is the house zone's day, never the browser's UTC day", () => {
  // Evening house time: ten in the night in Toronto is tomorrow on the UTC
  // clock. The instant is fixed, so the test is deterministic at any hour the
  // suite runs.
  const eveningHouseTime = new Date("2026-01-31T21:30:00-05:00"); // 2026-02-01T02:30Z
  expect(houseDayInZone("UTC", eveningHouseTime)).toBe("2026-02-01");
  expect(houseDayInZone(DEFAULT_HOUSE_ZONE, eveningHouseTime)).toBe("2026-01-31");
  // The morning edge: half past midnight in Toronto is still yesterday on the
  // UTC clock.
  const earlyHouseTime = new Date("2026-03-01T00:30:00-05:00"); // 2026-03-01T05:30Z
  expect(houseDayInZone(DEFAULT_HOUSE_ZONE, earlyHouseTime)).toBe("2026-03-01");
  expect(houseDayInZone("UTC", earlyHouseTime)).toBe("2026-03-01");
  // The default read is now, and answers a date-shaped string.
  expect(houseDayInZone(DEFAULT_HOUSE_ZONE)).toMatch(/^[0-9]{4}-[0-9]{2}-[0-9]{2}$/);
});
