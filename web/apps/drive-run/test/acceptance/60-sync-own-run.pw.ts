// The sync pull carries only the driver's own run. The runs, stops, pod and
// attachments scopes are the driver's offline set (manifest drive-run); the
// fence that holds each to the caller's own driver record has landed.
//
// THE FENCE IS ABOUT THE DRIVER, NOT ABOUT THE DAY. The runs scope is not
// day filtered: it holds every run of the caller's driver record (the core's
// own predicate, r.driver_id = the caller's driver id), so the seeded world's
// other runs of the SAME driver come through with it: the fleet run the seed
// plants two days back, and the one it plants the day after. Reading the
// driver's own set off the DAY's my-runs answer (a list filtered by
// scheduled_date) calls those runs strangers, and that is the leg's own
// mistake: the world plants a second run the driver legitimately holds. The
// leg therefore states the fence the way the core holds it: every run the
// scope carries is on the caller's own driver record, and every stop the
// scope carries sits on one of those runs.
//
// THE SEEDED RUN IS IN THE SCOPES. A seeded row carries no seq (0213: carried
// rows keep seq NULL, and the seed never bumps a shared sequence), so what
// makes the run syncable is the walk this run already took: every move the
// story's earlier legs made drew the row a seq through the core's own routes.
// The org is the seed's own stamp, written the way the core's own inserts
// write it (a read of the org row, not a constant), on the run and on its
// stops, so the scopes' org predicate answers the row. The presence half of
// the fence (the driver's own run comes through) and the leak half (nothing
// else does) are both proved here, on the seeded box, over real rows.
//
// The invariants are by id and by the run's own references, never by a field
// name guessed: the runs scope may hold only runs on the caller's driver
// record, the stops scope only the stops of those runs, and every pod or
// attachment row must name one of those stops.
import { describeHits, scanPayload } from "../_lib/fence.mjs";
import { expect, myRuns, staff, test } from "./helpers.js";

async function pullAll(page: import("@playwright/test").Page, scope: string): Promise<Array<{ id: string; data: Record<string, unknown> }>> {
  const items: Array<{ id: string; data: Record<string, unknown> }> = [];
  let cursor = "";
  for (let guard = 0; guard < 50; guard += 1) {
    const query = cursor === "" ? "since=0&limit=500" : `since=0&limit=500&cursor=${encodeURIComponent(cursor)}`;
    const answer = await staff(page, "GET", `/api/v1/sync/${scope}?${query}`);
    expect(answer.status, `the ${scope} scope answers the Driver`).toBe(200);
    items.push(...(answer.body.items as Array<{ id: string; data: Record<string, unknown> }>));
    cursor = (answer.body.next_cursor as string | null | undefined) ?? "";
    if (cursor === "") break;
  }
  return items;
}

test("the runs and stops scopes hold only the driver's own run", async ({ page }) => {
  const own = await myRuns(page);
  const ownRunIds = new Set(own.map((r) => r.id as string));
  const ownStopIds = new Set(own.flatMap((r) => (r.stops as Array<{ id: string }>).map((s) => s.id)));
  // The caller's own driver record: the runs the driver is shown name it, and
  // the core's fence is that record, never the day.
  const driverIds = new Set(own.map((r) => r.driver_id as string));
  expect(driverIds.size, "the driver's own runs name one driver record").toBe(1);
  const runs = await pullAll(page, "runs");
  // The presence half: the walked run is in the scopes, so the loops and the money scan below run
  // over real rows, not over a leak-free emptiness that would hold by construction.
  expect(runs.length).toBeGreaterThanOrEqual(1);
  expect(runs.some((row) => ownRunIds.has(row.id)), "the runs scope does not carry the driver's own run").toBe(true);
  // The leak half: every run the scope carries is on the caller's own driver record.
  for (const row of runs) {
    expect(driverIds.has(row.data.driver_id as string), `runs scope carried run ${row.id}, which is not the driver's`).toBe(true);
  }
  const stops = await pullAll(page, "stops");
  expect(stops.length).toBeGreaterThanOrEqual(1);
  // Every stop the scope carries sits on one of the driver's own runs: the runs
  // the scope holds (all on the caller's record) and the day's runs.
  const driverRunIds = new Set([...ownRunIds, ...runs.map((row) => row.id)]);
  for (const row of stops) {
    expect(driverRunIds.has(row.data.run_id as string), `stops scope carried stop ${row.id}, which is not on one of the driver's runs`).toBe(true);
  }
  expect(describeHits([...scanPayload("runs", JSON.stringify(runs)), ...scanPayload("stops", JSON.stringify(stops))])).toEqual([]);
});

test("the pod and attachments scopes name only the driver's own stops", async ({ page }) => {
  const own = await myRuns(page);
  const ownStopIds = [...new Set(own.flatMap((r) => (r.stops as Array<{ id: string }>).map((s) => s.id)))];
  const stops = await pullAll(page, "stops");
  // The driver's own stops: the day's stops and the stops the stops scope
  // holds (every one of them on one of the driver's own runs, the leg above).
  const driverStopIds = [...new Set([...ownStopIds, ...stops.map((row) => row.id)])];
  for (const scope of ["pod", "attachments"]) {
    const rows = await pullAll(page, scope);
    for (const row of rows) {
      const text = JSON.stringify(row);
      expect(driverStopIds.some((id) => text.includes(id)), `${scope} row ${row.id} names none of the driver's stops`).toBe(true);
    }
    expect(describeHits(scanPayload(scope, JSON.stringify(rows)))).toEqual([]);
  }
});
