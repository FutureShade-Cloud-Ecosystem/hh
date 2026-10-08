// No money in any payload. Every leg already fails on a money word in any
// answer the page received (helpers.ts, the auto watch); this leg reads the
// driver's own reads directly, after the story, and holds each to the same
// scan, so a payload the screens never asked for is held too.
import { describeHits, scanPayload, scanText } from "../_lib/fence.mjs";
import { expect, myRuns, staff, test } from "./helpers.js";

test("the driver's reads carry no money field and no money figure", async ({ page }) => {
  const runs = await myRuns(page);
  expect(runs.length).toBeGreaterThanOrEqual(1);
  const hits = [...scanPayload("my-runs", JSON.stringify(runs))];
  for (const stop of runs[0].stops) {
    const detail = await staff(page, "GET", `/api/v1/dispatch/stops/${stop.id}`);
    expect(detail.status).toBe(200);
    hits.push(...scanPayload(`stop ${stop.stop_no}`, JSON.stringify(detail.body)));
  }
  const run = await staff(page, "GET", `/api/v1/dispatch/runs/${runs[0].id}`);
  hits.push(...scanPayload("run", JSON.stringify(run.body)));
  expect(describeHits(hits)).toEqual([]);
});

test("the bundle the image serves under /drive/ carries no money word", async ({ page }) => {
  const index = await page.request.get("/drive/");
  expect(index.ok()).toBe(true);
  const html = await index.text();
  const assets = Array.from(html.matchAll(/(?:src|href)="(\/drive\/assets\/[^"]+\.(?:js|css))"/g)).map((m) => m[1] as string);
  expect(assets.length, "the page names its script").toBeGreaterThan(0);
  const hits = [];
  for (const asset of assets) {
    const body = await (await page.request.get(asset)).text();
    hits.push(...scanText(asset, body));
  }
  expect(describeHits(hits)).toEqual([]);
});
