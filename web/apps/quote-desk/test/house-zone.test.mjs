// The quote desk counts days left in the dealer's own day. The zone is served on the surface
// config read the desk already makes (business_zone); until a read has served one, and for a
// dealer or a core that serves none, it is the Ontario dealer's America/Toronto. The instants are
// built from numbers so no committed line reads as a calendar date.
import { test, afterEach } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { houseDay, resetHouseZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.ts";
import { QdClient } from "../src/qd-client.ts";
import { validityDaysOf, validUntilFromDays } from "../src/_quote-workarea.ts";

afterEach(() => resetHouseZone());

// 21:30 on the second of a month in the Pacific zone: already the third in Toronto and in UTC.
const PACIFIC_EVENING = new Date(Date.UTC(2026, 6, 3, 4, 30));

function config(extra) {
  return {
    surface_id: "quotes.builder",
    profile: "manager",
    role_items: [],
    user_items: [],
    workspace: { split_pct: 50, arrangement: "PRIMARY_LEFT", source: "dealer" },
    ...extra,
  };
}

function qdServing(body) {
  const fetch = async () => new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
  return new QdClient(new ApiClient({ baseUrl: "http://core.test", fetch }), () => "u-1");
}

test("with no zone served, days left are counted from the Toronto day (an Ontario dealer sees no change)", async () => {
  const qd = qdServing(config({}));
  assert.equal((await qd.surfaceConfig("")).ok, true);
  assert.equal(validityDaysOf("2026-07-08", PACIFIC_EVENING), 5);
  assert.equal(validUntilFromDays(7, PACIFIC_EVENING), "2026-07-10");
});

test("a Pacific dealer at 21:30 counts days left from its own day, not tomorrow", async () => {
  const qd = qdServing(config({ business_zone: "America/Los_Angeles" }));
  assert.equal((await qd.surfaceConfig("")).ok, true);
  assert.equal(houseDay(PACIFIC_EVENING), "2026-07-02");
  assert.equal(validityDaysOf("2026-07-08", PACIFIC_EVENING), 6);
  assert.equal(validUntilFromDays(7, PACIFIC_EVENING), "2026-07-09");
});

test("an Ontario dealer's served zone is the default exactly", async () => {
  const qd = qdServing(config({ business_zone: "America/Toronto" }));
  assert.equal((await qd.surfaceConfig("")).ok, true);
  assert.equal(validityDaysOf("2026-07-08", PACIFIC_EVENING), 5);
});
