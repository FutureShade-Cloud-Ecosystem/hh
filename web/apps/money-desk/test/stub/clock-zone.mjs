// The stub worlds' month arithmetic, and the one zone a contract test and the stub it spawns share.
//
// A stub world dates its month from its own process's clock (the month before today, the month the desk opens).
// A contract test that spawns the stub and computes the same month in its own process agrees with it only while
// both processes read the same local day. Under bun they need not: bun runs every test file in one process, a
// file that assigns process.env.TZ can leave bun on that zone after it restores the variable, and the stub child
// starts on the runner's zone. Near a month's turn the two then name different months (main's run 41212453853:
// the test read October from Asia/Tokyo's November 1 while the stub, on UTC's October 31, planted September).
//
// So a contract test spawns its stub with TZ set to STUB_ZONE and computes its month in that same zone, read
// through Intl with the zone named, which no process zone can move. A stub started by Playwright passes no zone
// and keeps the host's local month, the one the desk in the browser opens.

/** The zone a contract test pins its stub to, and computes its own months in. */
export const STUB_ZONE = "UTC";

/**
 * The month n months before now's month: from the host's local clock when no zone is named, else from the
 * named zone's calendar. The step is whole months from the month itself, never from a day of the month.
 */
export function monthsBefore(n, timeZone, now = new Date()) {
  let year;
  let month;
  if (timeZone === undefined) {
    year = now.getFullYear();
    month = now.getMonth() + 1;
  } else {
    const parts = new Intl.DateTimeFormat("en-CA", { timeZone, year: "numeric", month: "numeric" }).formatToParts(now);
    year = Number(parts.find((p) => p.type === "year").value);
    month = Number(parts.find((p) => p.type === "month").value);
  }
  const index = year * 12 + (month - 1) - n;
  return { year: Math.floor(index / 12), month: (index % 12) + 1 };
}
