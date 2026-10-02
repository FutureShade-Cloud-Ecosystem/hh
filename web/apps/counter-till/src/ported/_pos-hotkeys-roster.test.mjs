// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-hotkeys.test.mjs (the
// default-roster cross-check half, re-pinned against HH's core); adapted for HH.

// The wiring item's roster cross-check, owed by the port (CLOSURE.md,
// "Reference tests brought along"): the reference's cross-check read ITS Go
// core's hotkey file and pinned the frontend fallback roster against it. This
// one reads HH's own core (core/internal/pos/hotkeys.go, the file the contract
// serves /api/v1/pos/hotkeys from) and holds the same claim: the client's
// shipped fallback and the server's shipped default are ONE roster, so a cold
// counter whose roster read failed shows exactly the keys the server would
// have bound. Node built-in runner, same loader as the other ported suites.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';


import { FALLBACK_ROSTER } from './_pos-hotkeys.ts';

const here = new URL('.', import.meta.url).pathname;
// The climb from this file to the repository root: five directories.
const coreHotkeys = readFileSync(
  new URL('../../../../../core/internal/pos/hotkeys.go', import.meta.url),
  'utf8',
);

// The Go literal list, read out of the served source: the default roster is
// the source of truth and the frontend fallback must be its mirror.
function goDefaultRoster(source) {
  const block = source.match(/var defaultHotkeys = \[\]PosHotkey\{([\s\S]*?)\n\}/);
  assert.notEqual(block, null, 'the core file carries the defaultHotkeys literal');
  const rows = [];
  for (const m of block[1].matchAll(/\{Action:\s*Hotkey(\w+),\s*Key:\s*"([^"]+)"\}/g)) {
    rows.push({ action: m[1], key: m[2] });
  }
  return rows;
}

test('the frontend fallback roster is the core default roster, key for key', () => {
  const server = goDefaultRoster(coreHotkeys);
  assert.notEqual(server.length, 0, 'the core ships a non-empty default roster');
  // The client's action words are the wire's own kebab spellings (step-next,
  // step-back, pay, park, search); the Go constants are the CamelCase of the
  // same words. Compare both in kebab, beside the raw chord spelling.
  const kebab = (word) => word.replace(/([a-z0-9])([A-Z])/g, '$1-$2').toLowerCase();
  assert.deepEqual(
    FALLBACK_ROSTER.map((row) => ({ action: kebab(row.action), key: row.key })),
    server.map((row) => ({ action: kebab(row.action), key: row.key })),
    'one roster, two spellings of the same five bindings',
  );
});

test('the core default keeps the BisTrack pay key and the frontend mirrors it', () => {
  // F12 is a browser-owned key the core binds deliberately (the core file
  // says why in its own words) and marks browser-owned on the wire when
  // served; the frontend fallback must carry the same deliberate bind, or a
  // cold counter would pay on a different key than a configured one.
  const server = goDefaultRoster(coreHotkeys);
  assert.equal(server.some((row) => row.key === 'f12' && row.action === 'Pay'), true);
  assert.equal(
    FALLBACK_ROSTER.some((row) => row.key === 'f12' && row.action === 'pay'),
    true,
  );
});
