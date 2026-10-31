// The statement month is the house day's month before, whatever zone the process holds.
//
// The stub's statementMonth() read the host's local calendar, while the stub's sign off gate reads the house day
// (Toronto). Under bun the whole web tree runs in one process, and a sibling app's test that switches
// process.env.TZ leaves the zone switched, so on the last evening of a month the world and the test disagreed on
// the month and seven bank rec legs went red on main. Each reading below runs in a child process with the zone
// set from the start, which holds under node and bun alike.
import test from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const WORLD = fileURLToPath(new URL("./stub/bankrec-world.mjs", import.meta.url));

function monthIn(zone, iso) {
  const code = `import(${JSON.stringify(WORLD)}).then((m) => console.log(JSON.stringify(m.statementMonth(new Date(${JSON.stringify(iso)})))))`;
  const out = execFileSync(process.execPath, ["-e", code], { env: { PATH: process.env.PATH, TZ: zone }, encoding: "utf8" });
  return JSON.parse(out.trim());
}

// [instant, the statement month the house day gives]
const CASES = [
  ["2026-10-31T15:40:00Z", { year: 2026, month: 9 }], // last day of October, midday in Toronto
  ["2026-11-01T03:59:00Z", { year: 2026, month: 9 }], // 23:59 Toronto on 2026-10-31, already November in UTC
  ["2026-11-01T04:30:00Z", { year: 2026, month: 10 }], // 00:30 Toronto on 2026-11-01
  ["2026-10-15T16:00:00Z", { year: 2026, month: 9 }], // an ordinary mid month day
  ["2027-01-01T05:30:00Z", { year: 2026, month: 12 }], // 00:30 Toronto on New Year's Day
];

for (const zone of ["UTC", "Asia/Tokyo", "Pacific/Kiritimati", "Pacific/Pago_Pago", "America/Toronto"]) {
  test(`the statement month follows the house day, with the process zone at ${zone}`, () => {
    for (const [iso, want] of CASES) assert.deepEqual(monthIn(zone, iso), want, `${iso} in ${zone}`);
  });
}
