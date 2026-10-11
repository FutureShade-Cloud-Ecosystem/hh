// The sync pull carries only the driver's own run. The runs, stops, pod and
// attachments scopes are the driver's offline set (manifest drive-run); the
// fence that holds each to the caller's own driver record has landed.
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
// name guessed: the runs scope may hold only the driver's own run ids, the
// stops scope only the stops those runs carry, and every pod or attachment row
// must name one of those stops.
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
  const runs = await pullAll(page, "runs");
  // The presence half: the walked run is in the scopes, so the loops and the money scan below run
  // over real rows, not over a leak-free emptiness that would hold by construction.
  expect(runs.length).toBeGreaterThanOrEqual(1);
  for (const row of runs) expect(ownRunIds.has(row.id), `runs scope carried run ${row.id}, which is not the driver's`).toBe(true);
  const stops = await pullAll(page, "stops");
  expect(stops.length).toBeGreaterThanOrEqual(1);
  for (const row of stops) expect(ownStopIds.has(row.id), `stops scope carried stop ${row.id}, which is not on the driver's run`).toBe(true);
  expect(describeHits([...scanPayload("runs", JSON.stringify(runs)), ...scanPayload("stops", JSON.stringify(stops))])).toEqual([]);
});

test("the pod and attachments scopes name only the driver's own stops", async ({ page }) => {
  const own = await myRuns(page);
  const ownStopIds = [...new Set(own.flatMap((r) => (r.stops as Array<{ id: string }>).map((s) => s.id)))];
  for (const scope of ["pod", "attachments"]) {
    const rows = await pullAll(page, scope);
    for (const row of rows) {
      const text = JSON.stringify(row);
      expect(ownStopIds.some((id) => text.includes(id)), `${scope} row ${row.id} names none of the driver's stops`).toBe(true);
    }
    expect(describeHits(scanPayload(scope, JSON.stringify(rows)))).toEqual([]);
  }
});
