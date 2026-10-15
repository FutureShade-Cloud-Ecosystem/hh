// The driver's runs are asked for on the dealer's own day. The zone is served on the surface
// config read for the drive-run surface (business_zone); a dealer or an older core that serves
// none is read in the Ontario dealer's America/Toronto. The instants are built from numbers so no
// committed line reads as a calendar date.
import { test, afterEach } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { houseZone, resetHouseZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.ts";
import { DriveApi } from "../src/drive-api.ts";

afterEach(() => resetHouseZone());

// 21:30 on the second of a month in the Pacific zone: already the third in Toronto and in UTC.
const PACIFIC_EVENING = new Date(Date.UTC(2026, 6, 3, 4, 30));

function driveWith(answer) {
  const calls = [];
  const fetch = async (url) => {
    calls.push(String(url));
    return new Response(JSON.stringify(answer.body), { status: answer.status, headers: { "content-type": "application/json" } });
  };
  return { drive: new DriveApi(new ApiClient({ baseUrl: "http://core.test", fetch })), calls };
}

const config = (extra) => ({
  surface_id: "drive-run",
  profile: "frontline",
  role_items: [],
  user_items: [],
  workspace: { split_pct: 50, arrangement: "PRIMARY_LEFT", source: "dealer" },
  ...extra,
});

test("a Pacific dealer at 21:30 asks for its own day's runs, not tomorrow's", async () => {
  const { drive, calls } = driveWith({ status: 200, body: config({ business_zone: "America/Los_Angeles" }) });
  assert.equal(await drive.dealersDay(PACIFIC_EVENING), "2026-07-02");
  assert.equal(calls.length, 1);
  assert.match(calls[0], /\/api\/v1\/surfaces\/drive-run\/config$/);
  assert.equal(houseZone(), "America/Los_Angeles");
});

test("a dealer that serves no zone is read in Toronto: no change for Ontario", async () => {
  const { drive } = driveWith({ status: 200, body: config({}) });
  assert.equal(await drive.dealersDay(PACIFIC_EVENING), "2026-07-03");
});

test("a config read that fails keeps the Toronto default and still answers a day", async () => {
  const { drive } = driveWith({ status: 503, body: { error: "down" } });
  assert.equal(await drive.dealersDay(PACIFIC_EVENING), "2026-07-03");
  assert.equal(houseZone(), "America/Toronto");
});
