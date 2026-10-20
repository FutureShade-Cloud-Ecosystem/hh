// The served house zone: the dealer's business day zone arrives on the surface config read
// (business_zone), the kit holds it, and every app names the dealer's day in it. An absent or
// unreadable zone is the Ontario dealer's America/Toronto, so an older core and an Ontario dealer
// see no change. The instants are built from numbers so no committed line reads as a calendar date.
import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';

import {
  DEFAULT_HOUSE_ZONE,
  houseDay,
  houseZone,
  stampDay,
  resetHouseZone,
  serveHouseZoneFrom,
} from './host-facts.ts';

afterEach(() => resetHouseZone());

// 21:30 on the second of a month in the Pacific zone (UTC-7 in the summer): 04:30 UTC the next
// day, so the UTC day and the Toronto day (00:30) are both one day ahead of the dealer's.
const PACIFIC_EVENING = new Date(Date.UTC(2026, 6, 3, 4, 30));

test('with nothing served the zone is the Ontario dealer\'s and the day is Toronto\'s', () => {
  assert.equal(DEFAULT_HOUSE_ZONE, 'America/Toronto');
  assert.equal(houseZone(), 'America/Toronto');
  assert.equal(houseDay(PACIFIC_EVENING), '2026-07-03');
});

test('a Pacific dealer at 21:30 is still on its own day, not tomorrow', () => {
  serveHouseZoneFrom({ surface_id: 'drive-run', business_zone: 'America/Los_Angeles' });
  assert.equal(houseZone(), 'America/Los_Angeles');
  assert.equal(houseDay(PACIFIC_EVENING), '2026-07-02');
});

test('an Ontario dealer\'s served zone changes nothing', () => {
  serveHouseZoneFrom({ business_zone: 'America/Toronto' });
  assert.equal(houseDay(PACIFIC_EVENING), '2026-07-03');
});

test('an absent, empty, non string or unknown zone keeps the default', () => {
  for (const config of [{}, { business_zone: '' }, { business_zone: 7 }, { business_zone: 'Mars/Olympus' }, null, 'x', []]) {
    serveHouseZoneFrom({ business_zone: 'America/Los_Angeles' });
    serveHouseZoneFrom(config);
    assert.equal(houseZone(), 'America/Toronto', JSON.stringify(config));
  }
});

test('a later read replaces the zone (a dealer change is live at the next config read)', () => {
  serveHouseZoneFrom({ business_zone: 'America/Los_Angeles' });
  serveHouseZoneFrom({ business_zone: 'America/Halifax' });
  assert.equal(houseZone(), 'America/Halifax');
});

test('a date only value stays its calendar day; a timestamp reads as its day in the house zone', () => {
  serveHouseZoneFrom({ business_zone: 'America/Los_Angeles' });
  assert.equal(stampDay('2026-07-03'), '2026-07-03');
  assert.equal(stampDay(PACIFIC_EVENING.toISOString()), '2026-07-02');
  assert.equal(stampDay('2026-07-03T04:30:00'), '2026-07-02');
  assert.equal(stampDay(''), '');
  assert.equal(stampDay(null), '');
  assert.equal(stampDay(undefined), '');
  assert.equal(stampDay('not a day'), 'not a day');
});
