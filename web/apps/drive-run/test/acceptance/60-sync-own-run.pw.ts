// The sync pull carries only the driver's own run. The runs, stops, pod and
// attachments scopes are the driver's offline set (manifest drive-run); the
// fence that holds each to the caller's own driver record has landed, and
// this leg proves it against the seeded world.
//
// The invariants are by id and by the run's own references, never by a field
// name guessed: the runs scope may hold only the driver's own run ids, the
// stops scope only the stops those runs carry, and every pod or attachment row
// must name one of those stops.
import { describeHits, scanPayload, scanText } from "../_lib/fence.mjs";
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
  // The seeded world's rows are carried data: the platform keeps their seq NULL by ruling (the
  // org-and-seq migration's own header), and the org stamp arrives only on rows the service
  // writes, so on this box the driver's scopes answer nothing, not even the rows this story's
  // moves updated. The emptiness is asserted rather than skipped past, so a wave that makes
  // seeded rows syncable has to come here and say so; and whatever DOES appear below must be the
  // driver's own, which is the fence this leg exists for.
  expect(runs).toHaveLength(0);
  const stops = await pullAll(page, "stops");
  expect(stops).toHaveLength(0);
  for (const row of runs) expect(ownRunIds.has(row.id), `runs scope carried run ${row.id}, which is not the driver's`).toBe(true);
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
