// The pure shell state, under test: the per user pin book over its injected
// storage seam, the rail's collapsed posture resolver, the app switcher model
// by role over an injected manifest slice, the current surface's breadcrumb
// words, and the sync state chip's words. The pin semantics port the original
// HardscapeOS dashboard-logic.ts pin rules (master 87d7941b): an unpin keeps
// its place, so a re-pin restores it instead of landing on the end, and the
// whole set is what persists. The rail resolver ports _rail-state.ts: silence
// means collapsed, an explicit expand keeps winning.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  PinBook,
  memoryPinStorage,
  pinStorageKey,
  SHELL_PIN_CAP,
  railCollapsedFromStored,
  railCollapsedToStored,
  RAIL_COLLAPSED_KEY,
  appsForRoles,
  switcherGroups,
  surfaceKey,
  currentBreadcrumb,
  initialsOf,
  SHELL_SYNC_STATES,
  syncChipView,
} from '../src/shell-state.ts';
import { SYNC_STATES } from '../../../../statecharts/sync/machine.ts';

// ── the pin book: per user, ordered, capped, persisted through the seam ──────────────────────────

test('add pins surfaces in the order they were pinned', () => {
  const book = new PinBook({ storage: memoryPinStorage() });
  assert.deepEqual(book.add('u1', 'yard-pick/pick-queue'), { ok: true, changed: true });
  assert.deepEqual(book.add('u1', 'drive-run/my-run'), { ok: true, changed: true });
  assert.deepEqual(book.pins('u1'), ['yard-pick/pick-queue', 'drive-run/my-run']);
});

test('add of an already pinned key changes nothing', () => {
  const book = new PinBook({ storage: memoryPinStorage() });
  book.add('u1', 'yard-pick/pick-queue');
  assert.deepEqual(book.add('u1', 'yard-pick/pick-queue'), { ok: true, changed: false });
  assert.deepEqual(book.pins('u1'), ['yard-pick/pick-queue']);
});

test('remove unpins, and re-pinning restores the old place instead of landing on the end', () => {
  const book = new PinBook({ storage: memoryPinStorage() });
  book.add('u1', 'yard-pick/pick-queue');
  book.add('u1', 'drive-run/my-run');
  book.add('u1', 'house-messages/inbox');
  assert.deepEqual(book.remove('u1', 'drive-run/my-run'), { ok: true, changed: true });
  assert.deepEqual(book.pins('u1'), ['yard-pick/pick-queue', 'house-messages/inbox']);
  assert.equal(book.isPinned('u1', 'drive-run/my-run'), false);
  assert.deepEqual(book.add('u1', 'drive-run/my-run'), { ok: true, changed: true });
  assert.deepEqual(
    book.pins('u1'),
    ['yard-pick/pick-queue', 'drive-run/my-run', 'house-messages/inbox'],
    'the re-pinned surface goes back where it was, not to the end',
  );
});

test('remove of a key the user never pinned is a calm no-op', () => {
  const book = new PinBook({ storage: memoryPinStorage() });
  assert.deepEqual(book.remove('u1', 'yard-pick/pick-queue'), { ok: true, changed: false });
});

test('move swaps along the rail; the ends refuse politely', () => {
  const book = new PinBook({ storage: memoryPinStorage() });
  book.add('u1', 'a/one');
  book.add('u1', 'b/two');
  book.add('u1', 'c/three');
  assert.deepEqual(book.move('u1', 'c/three', -1), { ok: true, changed: true });
  assert.deepEqual(book.pins('u1'), ['a/one', 'c/three', 'b/two']);
  assert.deepEqual(book.move('u1', 'c/three', 1), { ok: true, changed: true });
  assert.deepEqual(book.pins('u1'), ['a/one', 'b/two', 'c/three']);
  assert.deepEqual(book.move('u1', 'c/three', 1), { ok: true, changed: false }, 'the bottom end says so');
  assert.deepEqual(book.move('u1', 'a/one', -1), { ok: true, changed: false }, 'the top end says so');
  assert.deepEqual(book.move('u1', 'nope/none', 1), { ok: true, changed: false }, 'an unknown key moves nothing');
  assert.deepEqual(book.pins('u1'), ['a/one', 'b/two', 'c/three']);
});

test('the cap refuses a new pin, and hidden rows do not count toward it', () => {
  const storage = memoryPinStorage();
  const book = new PinBook({ storage, cap: SHELL_PIN_CAP });
  for (let i = 0; i < SHELL_PIN_CAP; i++) {
    assert.deepEqual(book.add('u1', `app-${i}/one`), { ok: true, changed: true });
  }
  assert.deepEqual(book.pins('u1').length, SHELL_PIN_CAP);
  assert.deepEqual(
    book.add('u1', 'app-extra/one'),
    { ok: false, reason: 'cap' },
    'one pin beyond the cap is refused, plainly',
  );
  assert.deepEqual(book.pins('u1').length, SHELL_PIN_CAP, 'the refused pin is not stored');
  // An unpin keeps its row, so a re-pin at the cap is a revival, not a new row.
  book.remove('u1', 'app-0/one');
  assert.deepEqual(book.add('u1', 'app-0/one'), { ok: true, changed: true });
  // Removing one frees the cap for a genuinely new pin.
  book.remove('u1', 'app-1/one');
  assert.deepEqual(book.add('u1', 'app-extra/one'), { ok: true, changed: true });
});

test('reset drops every row, so a fresh start is whole or absent', () => {
  const book = new PinBook({ storage: memoryPinStorage() });
  book.add('u1', 'a/one');
  book.add('u1', 'b/two');
  assert.deepEqual(book.reset('u1'), { ok: true, changed: true });
  assert.deepEqual(book.pins('u1'), []);
});

test('two users on one device never share pins: each user reads and writes its own key', () => {
  const storage = memoryPinStorage();
  const book = new PinBook({ storage });
  book.add('u1', 'yard-pick/pick-queue');
  assert.deepEqual(book.pins('u2'), [], 'the second person starts from their own empty book');
  book.add('u2', 'drive-run/my-run');
  assert.deepEqual(book.pins('u1'), ['yard-pick/pick-queue']);
  assert.deepEqual(book.pins('u2'), ['drive-run/my-run']);
  assert.deepEqual(
    [...storage.entries(pinStorageKey('u1'))],
    ['yard-pick/pick-queue'],
    'the seam is keyed by the pin key, and the payload holds only that user\u2019s pins',
  );
});

test('the seam receives the exact pin storage key, never the bare user id', () => {
  const seen = [];
  const book = new PinBook({
    storage: {
      read: (key) => {
        seen.push(['read', key]);
        return null;
      },
      write: (key) => {
        seen.push(['write', key]);
      },
    },
  });
  book.add('u1', 'a/one');
  assert.deepEqual(seen, [
    ['read', 'hh-shell.pins.v1.u1'],
    ['write', 'hh-shell.pins.v1.u1'],
  ]);
  assert.equal(pinStorageKey('u1'), 'hh-shell.pins.v1.u1');
});

test('odd user ids stay inside the pin keyspace and never collide', () => {
  const seen = new Set();
  const book = new PinBook({
    storage: {
      read: () => null,
      write: (key) => {
        seen.add(key);
      },
    },
  });
  const ids = ['a/b', '..', '__proto__', 'two words', 'a%2Fb', 'hh-shell.rail-collapsed'];
  for (const id of ids) book.add(id, 'x/y');
  assert.equal(seen.size, ids.length, 'every id has its own key');
  for (const key of seen) {
    assert.ok(key.startsWith('hh-shell.pins.v1.'), `${key} is under the pin prefix`);
    assert.notEqual(key, RAIL_COLLAPSED_KEY, 'no id reaches the rail posture key');
  }
  assert.ok(seen.has('hh-shell.pins.v1.a%2Fb'), 'a slash is encoded');
  assert.ok(seen.has('hh-shell.pins.v1.two%20words'), 'a space is encoded');
  assert.ok(seen.has('hh-shell.pins.v1.__proto__'), 'a prototype name is just a key');
  const second = new PinBook();
  second.add('__proto__', 'x/y');
  assert.deepEqual(second.pins('__proto__'), ['x/y']);
  assert.deepEqual(second.pins('other'), []);
});

test('an empty user id is refused: no shared signed out book, nothing written', () => {
  const writes = [];
  const reads = [];
  const book = new PinBook({
    storage: {
      read: (key) => {
        reads.push(key);
        return null;
      },
      write: (key) => {
        writes.push(key);
      },
    },
  });
  for (const id of ['', '   ', undefined, null]) {
    assert.deepEqual(book.add(id, 'a/one'), { ok: false, reason: 'no-user' });
    assert.deepEqual(book.remove(id, 'a/one'), { ok: false, reason: 'no-user' });
    assert.deepEqual(book.move(id, 'a/one', 1), { ok: false, reason: 'no-user' });
    assert.deepEqual(book.reset(id), { ok: false, reason: 'no-user' });
    assert.deepEqual(book.pins(id), []);
    assert.equal(book.isPinned(id, 'a/one'), false);
  }
  assert.deepEqual(writes, [], 'nothing is written for no one');
  assert.deepEqual(reads, [], 'nothing is read for no one');
});

test('pins persist through the seam: a new book over the same storage reads the same pins', () => {
  const storage = memoryPinStorage();
  new PinBook({ storage }).add('u1', 'yard-pick/pick-queue');
  const second = new PinBook({ storage });
  assert.deepEqual(second.pins('u1'), ['yard-pick/pick-queue']);
});

test('a corrupt or unknown payload reads as an empty book, never as a throw', () => {
  const storage = memoryPinStorage();
  storage.write(pinStorageKey('u1'), '{not json');
  storage.write(pinStorageKey('u2'), 'null');
  storage.write(pinStorageKey('u3'), JSON.stringify({ v: 99, pins: [] }));
  storage.write(pinStorageKey('u4'), JSON.stringify({ v: 1, pins: [{ key: 7, visible: true }] }));
  const book = new PinBook({ storage });
  for (const user of ['u1', 'u2', 'u3', 'u4']) {
    assert.deepEqual(book.pins(user), [], `${user} reads as empty`);
  }
  assert.deepEqual(book.add('u1', 'a/one'), { ok: true, changed: true }, 'an add recovers over the bad payload');
  assert.deepEqual(book.pins('u1'), ['a/one']);
});

test('a throwing seam is not a fault in the model: the book answers and keeps its state', () => {
  const book = new PinBook({
    storage: {
      read: () => {
        throw new Error('denied');
      },
      write: () => {
        throw new Error('full');
      },
    },
  });
  assert.deepEqual(book.pins('u1'), []);
  assert.deepEqual(book.add('u1', 'a/one'), { ok: true, changed: true });
  assert.deepEqual(book.pins('u1'), ['a/one']);
});

// ── the rail posture, ported from the original's _rail-state tests ───────────────────────────────

test('no stored preference: the rail defaults collapsed (the whole ask)', () => {
  assert.equal(railCollapsedFromStored(null), true);
});

test('a stored collapse is collapsed, exactly as before', () => {
  assert.equal(railCollapsedFromStored('1'), true);
});

test('the stored mark of an explicit expand keeps winning', () => {
  assert.equal(railCollapsedFromStored('0'), false);
});

test('garbage and future values degrade to the default posture, never an error', () => {
  assert.equal(railCollapsedFromStored(''), true);
  assert.equal(railCollapsedFromStored('true'), true);
  assert.equal(railCollapsedFromStored('yes'), true);
  assert.equal(railCollapsedFromStored('2'), true);
});

test('the written posture is the one the resolver reads: the pair round trips', () => {
  assert.equal(railCollapsedFromStored(railCollapsedToStored(true)), true);
  assert.equal(railCollapsedFromStored(railCollapsedToStored(false)), false);
  assert.ok(RAIL_COLLAPSED_KEY.includes('rail'), 'the storage key names the rail');
});

// ── the app switcher model by role, over an injected manifest slice ──────────────────────────────

const FRONTENDS = [
  { id: 'counter-till', name: 'Counter Till', roles: ['inside_sales', 'branch_manager'] },
  { id: 'yard-pick', name: 'Yard Pick and Receive', roles: ['yard_team', 'yard_manager'] },
  { id: 'drive-run', name: 'HH Drive', roles: ['driver', 'yard_manager'] },
  { id: 'money-desk', name: 'Money Desk', roles: ['financial_controller'] },
];

test('apps for roles keeps manifest order and carries only the apps the role holds', () => {
  const apps = appsForRoles(FRONTENDS, ['yard_manager']);
  assert.deepEqual(apps.map((a) => a.id), ['yard-pick', 'drive-run']);
  assert.deepEqual(appsForRoles(FRONTENDS, ['yard_team', 'driver']).map((a) => a.id), ['yard-pick', 'drive-run']);
  assert.deepEqual(appsForRoles(FRONTENDS, ['financial_controller']).map((a) => a.id), ['money-desk']);
});

test('no roles means no apps, and an unknown role means no apps', () => {
  assert.deepEqual(appsForRoles(FRONTENDS, []), []);
  assert.deepEqual(appsForRoles(FRONTENDS, ['astronaut']), []);
});

test('the manifest slice is read, never written', () => {
  const slice = FRONTENDS.map((f) => Object.freeze({ ...f }));
  appsForRoles(Object.freeze(slice), ['yard_team']);
  assert.deepEqual(slice, FRONTENDS);
});

test('switcher groups carry each app with its injected surfaces, in manifest order', () => {
  const groups = switcherGroups({
    frontends: FRONTENDS,
    roles: ['yard_manager'],
    surfaces: new Map([
      ['yard-pick', [{ id: 'pick-queue', label: 'Pick queue' }]],
      ['drive-run', [{ id: 'my-run', label: 'My run' }]],
    ]),
  });
  assert.deepEqual(
    groups.map((g) => g.app.id),
    ['yard-pick', 'drive-run'],
  );
  assert.deepEqual(groups[0].surfaces.map((s) => s.id), ['pick-queue']);
  assert.deepEqual(groups[1].surfaces.map((s) => s.id), ['my-run']);
});

test('an app with no surfaces entry is front door only: its group carries no surfaces', () => {
  const groups = switcherGroups({ frontends: FRONTENDS, roles: ['yard_team'], surfaces: new Map() });
  assert.deepEqual(groups.map((g) => g.app.id), ['yard-pick']);
  assert.deepEqual(groups[0].surfaces, []);
});

// ── the current surface: keys and breadcrumb words ───────────────────────────────────────────────

test('a front door pin is a bare app key; a surface pin carries the app and the surface', () => {
  assert.equal(surfaceKey({ appId: 'yard-pick', surfaceId: '' }), 'yard-pick');
  assert.equal(surfaceKey({ appId: 'yard-pick', surfaceId: 'pick-queue' }), 'yard-pick/pick-queue');
});

test('the breadcrumb reads App, then surface, the band\u2019s own words', () => {
  const base = {
    frontends: FRONTENDS,
    roles: ['yard_manager'],
    surfaces: new Map([
      ['yard-pick', [{ id: 'pick-queue', label: 'Pick queue' }]],
      ['drive-run', [{ id: 'my-run', label: 'My run' }]],
    ]),
  };
  assert.equal(currentBreadcrumb({ ...base, current: { appId: 'yard-pick', surfaceId: 'pick-queue' } }), 'Yard Pick and Receive › Pick queue');
  assert.equal(currentBreadcrumb({ ...base, current: { appId: 'drive-run', surfaceId: '' } }), 'HH Drive');
  assert.equal(currentBreadcrumb({ ...base, current: null }), '');
  assert.equal(currentBreadcrumb({ ...base, current: { appId: 'nope', surfaceId: '' } }), '');
});

test('initials come from the role label words, never a person name', () => {
  assert.equal(initialsOf('Yard Team'), 'YT');
  assert.equal(initialsOf('driver'), 'D');
  assert.equal(initialsOf('Sales (demo), Sales'), 'S', 'the name before the comma, no parenthetical');
  assert.equal(initialsOf('Dana Reyes, General Manager'), 'DR');
  assert.equal(initialsOf(''), '');
  assert.equal(initialsOf('   '), '');
});

// ── the sync state chip: the machine\u2019s states as the band shows them ──────────────────────────

test('the chip names exactly the sync statechart\u2019s states, no more and no fewer', () => {
  assert.deepEqual([...SHELL_SYNC_STATES], [...SYNC_STATES]);
});

test('each state reads as one plain word or phrase, at its own colour kind', () => {
  const labelOf = (state) => syncChipView({ state, openCards: 0, waiting: 0, attempts: 0, stalls: 0 }).label;
  const kindOf = (state) => syncChipView({ state, openCards: 0, waiting: 0, attempts: 0, stalls: 0 }).kind;
  assert.equal(labelOf('idle'), 'Synced');
  assert.equal(kindOf('idle'), 'good');
  assert.equal(labelOf('pulling'), 'Pulling');
  assert.equal(kindOf('pulling'), 'busy');
  assert.equal(labelOf('pushing'), 'Pushing');
  assert.equal(kindOf('pushing'), 'busy');
  assert.equal(labelOf('offline'), 'Offline');
  assert.equal(kindOf('offline'), 'stop');
  assert.equal(labelOf('unreachable'), 'Reconnecting');
  assert.equal(kindOf('unreachable'), 'busy');
  assert.equal(labelOf('conflict'), '0 to review');
  assert.equal(kindOf('conflict'), 'stop');
  assert.equal(labelOf('auth_expired'), 'Sign in expired');
  assert.equal(kindOf('auth_expired'), 'stop');
});

test('conflict counts the open cards in the chip word itself', () => {
  assert.equal(syncChipView({ state: 'conflict', openCards: 2, waiting: 0, attempts: 0, stalls: 0 }).label, '2 to review');
});

test('pushing names the writes waiting behind it', () => {
  const view = syncChipView({ state: 'pushing', openCards: 0, waiting: 3, attempts: 0, stalls: 0 });
  assert.ok(view.note.includes('3'), 'the outbox depth rides the note');
});

test('a stall shows at once, in words, whatever the state', () => {
  const view = syncChipView({ state: 'idle', openCards: 0, waiting: 2, attempts: 1, stalls: 1 });
  assert.ok(view.note.length > 0, 'the stall is named, not silent');
  assert.equal(view.alarm, false, 'one attempt is not an alarm');
  assert.ok(!/retry/i.test(view.note), 'one attempt earns no retry words');
});

test('two attempts on a stuck write alarm, and say so in words, never colour alone', () => {
  const view = syncChipView({ state: 'pushing', openCards: 0, waiting: 4, attempts: 2, stalls: 0 });
  assert.equal(view.alarm, true);
  assert.ok(/retry/i.test(view.note), 'the word carries the alarm beside the colour');
});

test('one attempt, even offline, never alarms', () => {
  const view = syncChipView({ state: 'offline', openCards: 0, waiting: 1, attempts: 1, stalls: 0 });
  assert.equal(view.alarm, false);
});

test('a stall and a conflict read together: the count keeps its word, the stall keeps its note', () => {
  const view = syncChipView({ state: 'conflict', openCards: 1, waiting: 2, attempts: 0, stalls: 1 });
  assert.equal(view.label, '1 to review');
  assert.ok(view.note.length > 0);
});
