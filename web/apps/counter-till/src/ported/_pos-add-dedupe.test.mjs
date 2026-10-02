// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-add-dedupe.test.mjs; adapted for HH.

// _pos-add-dedupe.test.mjs: DIB-553's second half, on a DRIVEN clock.
//
// The measurement behind every case here: before this rule, ONE double-click on a hot-list tile put
// the product on the ticket at quantity 2 (verification/feedback9_08/L3/before: dark @1440,
// "after ONE double-click: lines = 1, first-line qty = 2"). After it, qty 1.
//
// BOUNCE 2: WHY THIS FILE LOOKS DIFFERENT. A refuter applied `if (false && isEchoAdd(…))` to both
// guarded components: behaviour off, source text intact, and all six tests in the first version of
// this file stayed GREEN, because the whole automated defence of the wiring was
// `assert.match(src, /isEchoAdd\(this\.lastAdd,/)`. That is foreman decision D6 verbatim. The same
// refuter found a THIRD add surface: pos-search-drop's result rows, the DEFAULT one, the one the
// counter types into: with no guard at all, selling two units on one double-click.
//
// Both are answered structurally rather than by adding assertions:
// • the decision, the state and the DISPATCH now live in `AddOnce`, so the components hold no
// expression of their own for a `false &&` to switch off, and every case below drives the real
// dispatcher against a real EventTarget: turning the rule off turns these RED;
// • the last two tests WALK THE TREE. The census test's name has always made a whole-tree claim
// ("every add path goes through it") and it used to read two named files and regex-match three
// strings. It now enumerates every.ts under src/, and a fourth add surface appearing anywhere
// fails it.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import {
  isEchoAdd,
  recordAdd,
  NO_LAST_ADD,
  ADD_DEDUPE_MS,
  ADD_EVENT,
  ADD_ECHO_EVENT,
  AddOnce,
} from './_pos-add-dedupe.ts';
import { echoAddMessage } from './_pos.ts';

const here = dirname(fileURLToPath(import.meta.url));
const hotSrc = readFileSync(join(here, 'pos-hot-list.ts'), 'utf8');
const gridSrc = readFileSync(join(here, 'pos-product-grid.ts'), 'utf8');

// ── the pure rule ─────────────────────────────────────────────────────────────────────────────

test('DIB-553: the second half of a double-click on the same product is swallowed', () => {
  const first = recordAdd('p-1', 1_000);
 // A real double-click's inter-click gap is ~150-250ms.
  assert.equal(isEchoAdd(first, 'p-1', 1_180), true);
  assert.equal(isEchoAdd(first, 'p-1', 1_250), true);
  assert.equal(isEchoAdd(first, 'p-1', 1_299), true);
});

test('DIB-553: a DELIBERATE second press of the same product is not swallowed', () => {
  const first = recordAdd('p-1', 1_000);
  assert.equal(isEchoAdd(first, 'p-1', 1_300), false, 'the boundary belongs to the person, not the echo');
  assert.equal(isEchoAdd(first, 'p-1', 2_400), false);
  assert.equal(ADD_DEDUPE_MS, 300, 'and the window is short enough that this is a normal gesture');
});

test('DIB-553: two DIFFERENT products in quick succession are a person working fast', () => {
 // A counter picking three items off one board does it in well under a second. Swallowing any of
 // those would be a worse bug than the one this rule fixes.
  const first = recordAdd('p-1', 1_000);
  assert.equal(isEchoAdd(first, 'p-2', 1_010), false);
  assert.equal(isEchoAdd(first, 'p-2', 1_299), false);
});

test('DIB-553: a product the wire did not identify is never treated as an echo', () => {
  assert.equal(isEchoAdd(recordAdd('', 1_000), '', 1_010), false, 'losing the line is worse than a double');
  assert.equal(isEchoAdd(NO_LAST_ADD, 'p-1', 1), false, 'and the first press of a fresh surface always lands');
});

test('DIB-553: a clock that jumps BACKWARDS does not swallow a real press', () => {
 // A system time change (or a test handing an older instant) must not read as "0ms ago".
  assert.equal(isEchoAdd(recordAdd('p-1', 5_000), 'p-1', 4_000), false);
});

// ── the DISPATCHER, against a real EventTarget on a driven clock ──────────────────────────────
//
// This is the half that did not exist before the bounce, and it is the half that goes red when the
// rule is turned OFF rather than removed. Every case below counts EVENTS, not source lines.

/** A stand-in for the add surface: a real EventTarget that records what it emitted. */
function surface() {
  const host = new EventTarget();
  const adds = [];
  const echoes = [];
  host.addEventListener(ADD_EVENT, (e) => adds.push(e.detail));
  host.addEventListener(ADD_ECHO_EVENT, (e) => echoes.push(e.detail));
  return { host, adds, echoes };
}

test('DIB-553 behaviour: one double-click on a surface dispatches ONE add', () => {
  const { host, adds, echoes } = surface();
  const a = new AddOnce();
  assert.equal(a.add(host, 'p-1', { id: 'p-1' }, 1_000), true);
  assert.equal(a.add(host, 'p-1', { id: 'p-1' }, 1_180), false, 'the second half of the gesture');
  assert.equal(adds.length, 1, 'ONE double-click must not sell two units');
  assert.equal(echoes.length, 1, 'and the swallow is never silent');
});

test('DIB-553 behaviour: the swallowed press names the product so the till can answer it', () => {
 // The terminal turns this into the same line flash a real add gets plus one sentence in the
 // role="status" line; it cannot do either without knowing WHICH line.
  const { host, echoes } = surface();
  const a = new AddOnce();
  a.add(host, 'p-1', { id: 'p-1', name: 'Red Mulch' }, 1_000);
  a.add(host, 'p-1', { id: 'p-1', name: 'Red Mulch' }, 1_100);
  assert.equal(echoes.length, 1);
  assert.equal(echoes[0].id, 'p-1');
  assert.deepEqual(echoes[0].product, { id: 'p-1', name: 'Red Mulch' });
});

test('DIB-553 behaviour: a deliberate press past the window dispatches a SECOND add', () => {
  const { host, adds, echoes } = surface();
  const a = new AddOnce();
  a.add(host, 'p-1', { id: 'p-1' }, 1_000);
  assert.equal(a.add(host, 'p-1', { id: 'p-1' }, 1_400), true);
  assert.equal(adds.length, 2, 'the guard must never cost a person a second unit they asked for');
  assert.equal(echoes.length, 0);
});

test('DIB-553 behaviour: two different products inside the window both land', () => {
  const { host, adds } = surface();
  const a = new AddOnce();
  a.add(host, 'p-1', { id: 'p-1' }, 1_000);
  a.add(host, 'p-2', { id: 'p-2' }, 1_010);
  a.add(host, 'p-3', { id: 'p-3' }, 1_020);
  assert.equal(adds.length, 3, 'a counter picking three items off one board');
});

test('DIB-553 behaviour: a ticket change resets the guard, so the next press is never lost', () => {
 // The scoping smell the refuter labelled: `last` is surface state and the cart is the terminal's.
 // Click (1 line) → the terminal empties the cart → click the SAME product again produced ZERO
 // lines, because the surface still thought it was holding an echo.
  const { host, adds } = surface();
  const a = new AddOnce();
  a.add(host, 'p-1', { id: 'p-1' }, 1_000);
  a.reset(); // the terminal bumped ticketEpoch: New Sale, remove line, resume, snapshot, qty edit
  assert.equal(a.add(host, 'p-1', { id: 'p-1' }, 1_010), true, 'the line it was protecting is gone');
  assert.equal(adds.length, 2);
});

// ── the RECEIVING side of the ticket-epoch wiring (round 3) ───────────────────────────────────
//
// Bounce 2 said "no component holds an expression a `false &&` can switch off" and every guarded
// surface held exactly one: `if (changed.has('ticketEpoch')) this.adder.reset();`, defended by a
// regex that `false &&` satisfies perfectly. The comparison moved into AddOnce.ticketAt so these
// cases can reach it; the components now call it unconditionally.

test('DIB-553 behaviour: a NEW ticket generation forgets the held press', () => {
  const { host, adds } = surface();
  const a = new AddOnce();
  a.ticketAt(0);
  a.add(host, 'p-1', { id: 'p-1' }, 1_000);
  a.ticketAt(1); // the terminal removed the line / started a new sale / resumed a parked ticket
 // The refuter measured this exact gesture in Chromium: a re-add at a 121ms gap after the line
 // was removed LANDS at baseline and is SWALLOWED with the receiving side mutated off.
  assert.equal(a.add(host, 'p-1', { id: 'p-1' }, 1_121), true, 'a real press must not be lost to a stale latch');
  assert.equal(adds.length, 2);
});

test('DIB-553 behaviour: the SAME generation, seen again, is not a ticket change', () => {
 // willUpdate runs on every render, so this is called constantly. If an unchanged generation reset
 // the guard, the guard would never fire at all and one double-click would sell two units again.
  const { host, adds, echoes } = surface();
  const a = new AddOnce();
  a.ticketAt(7);
  a.add(host, 'p-1', { id: 'p-1' }, 1_000);
  a.ticketAt(7);
  a.ticketAt(7);
  assert.equal(a.add(host, 'p-1', { id: 'p-1' }, 1_180), false, 'still the second half of one gesture');
  assert.equal(adds.length, 1);
  assert.equal(echoes.length, 1);
});

test('DIB-553 behaviour: a surface\'s FIRST sighting of a generation costs nothing', () => {
 // A fresh surface is told the epoch before it has dispatched anything; that must not be read as
 // a ticket change that swallows or duplicates anything.
  const { host, adds } = surface();
  const a = new AddOnce();
  a.ticketAt(0);
  assert.equal(a.add(host, 'p-1', { id: 'p-1' }, 1_000), true);
  assert.equal(adds.length, 1);
});

test('DIB-553 behaviour: the guard is per surface instance, not per module', () => {
 // Two mounted surfaces must not swallow each other's presses: the search dropdown and the
 // browse grid are on screen together and show the same products.
  const one = surface();
  const two = surface();
  const a = new AddOnce();
  const b = new AddOnce();
  a.add(one.host, 'p-1', { id: 'p-1' }, 1_000);
  b.add(two.host, 'p-1', { id: 'p-1' }, 1_010);
  assert.equal(one.adds.length, 1);
  assert.equal(two.adds.length, 1);
});

test('DIB-553: the swallowed press is answered with the quantity that is on the ticket', () => {
  const out = echoAddMessage('Red Mulch', 1, 'CYD');
  assert.ok(out);
  assert.equal(out.kind, 'warn', 'the till did something other than what the hand asked for');
  assert.match(out.msg, /Red Mulch/);
  assert.match(out.msg, /still 1 CYD/, 'the outcome, not just an apology');
  assert.match(out.msg, /Change the amount on the line/, 'and where the answer is');
  assert.equal(echoAddMessage('Red Mulch', 0, 'CYD'), null, 'never claim a line that is not there');
});

// HH note: the reference file's last three tests made the whole-tree claim by walking the whole
// reference src and naming every add path and its protection (and the quote-line writers beyond
// the mechanism). Those claims are about the reference repository's own tree, so they stayed with
// it; the wiring work item owes this app its own census when the surface is mounted (CLOSURE.md).
