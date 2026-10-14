// The census of the served house zone: no app compiles a zone in, and the three apps that name
// the dealer's day (the counter till, the quote desk, the driver's app) each hand the body of the
// surface config read they already make to the kit, which holds the zone the core served. Source
// scans like the ones beside this file, because the failure they stop is a constant creeping back.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

const WEB = new URL('../../../../', import.meta.url).pathname;

function sources(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) out.push(...sources(path));
    else if (/\.ts$/.test(name) && !/\.d\.ts$/.test(name)) out.push(path);
  }
  return out;
}

const ROOTS = ['apps/counter-till/src', 'apps/quote-desk/src', 'apps/drive-run/src', 'packages/pos-kit/src'];

test('no app or kit source compiles a house zone in; the kit holds only the Ontario default', () => {
  for (const root of ROOTS) {
    for (const file of sources(join(WEB, root))) {
      const text = readFileSync(file, 'utf8');
      const here = file.slice(WEB.length);
      assert.doesNotMatch(text, /\bHOUSE_ZONE\b/, `${here} names the retired HOUSE_ZONE constant`);
      if (!here.endsWith('pos-kit/src/ported/host-facts.ts')) {
        assert.doesNotMatch(text, /America\/Toronto/, `${here} compiles a zone in`);
      }
    }
  }
});

test('each app hands its surface config read to the kit', () => {
  for (const file of [
    'apps/counter-till/src/ported/pos-terminal.ts',
    'apps/quote-desk/src/qd-client.ts',
    'apps/drive-run/src/drive-api.ts',
  ]) {
    assert.match(readFileSync(join(WEB, file), 'utf8'), /serveHouseZoneFrom\(/, `${file} does not serve the zone to the kit`);
  }
});
