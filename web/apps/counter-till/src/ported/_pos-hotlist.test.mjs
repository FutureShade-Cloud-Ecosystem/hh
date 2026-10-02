// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-hotlist.test.mjs; adapted for HH.

// _pos-hotlist.test.mjs: the drillable photo HOT LIST (category → subcategory → item).
//
// The client's ask, verbatim: touchscreen tiles with photos, drilling category → subcategory →
// item: the example was bulk → mulch → black: and "something that BisTrack does not do". The
// back-office lead's volume note is why it matters: bulk material dwarfs contracts and projects
// here, so this is the high-frequency path, not decoration.
//
// WHAT THIS FILE PINS, AND WHY EACH ONE IS A REAL FAILURE MODE:
// · the drill and the ONE-TAP way back (a tree you can only descend is a trap)
// · the ungrouped case (a curated tile must never vanish because a catalogue column was blank)
// · the no-photo case (no error, no broken box, no empty square that reads as "loading")
// · the SET is still the dealer's curation: only the STRUCTURE changed
//
// node --import./src/_ts-js-loader.mjs --test src/_pos-hotlist.test.mjs (run from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  HOT_ROOT,
  HOT_LEVEL_MAX,
  UNGROUPED_LABEL,
  hotTiles,
  drillInto,
  drillUp,
  crumbs,
  pathExists,
} from './_pos-hotlist.ts';

// Comments stripped before any text scan: the house lesson (NOTES-FIX-471 §3): a gate that reads
// prose finds the defect it is quoting in the comment that explains the fix.
const code = (t) => t.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '').replace(/<!--[\s\S]*?-->/g, '');
const listSrc = code(readFileSync(new URL('./pos-hot-list.ts', import.meta.url), 'utf8'));
const terminalSrc = readFileSync(new URL('./pos-terminal.ts', import.meta.url), 'utf8');
const thumbSrc = readFileSync(new URL('./product-thumb.ts', import.meta.url), 'utf8');
const treeSrc = code(readFileSync(new URL('./_pos-hotlist.ts', import.meta.url), 'utf8'));

const p = (id, name, category, subcategory, over = {}) => ({
  product_id: id,
  sku: id.toUpperCase(),
  name,
  uom: 'YD',
  has_image: false,
  category,
  subcategory,
  ...over,
});

// The client's own example set, plus the shapes a real catalogue actually contains.
const SET = [
  p('m1', 'Black', 'Bulk', 'Mulch'),
  p('m2', 'Brown', 'Bulk', 'Mulch'),
  p('m3', 'Red', 'Bulk', 'Mulch'),
  p('s1', 'Limestone Screening', 'Bulk', 'Stone'),
  p('s2', 'River Rock', 'Bulk', 'Stone'),
  p('c1', 'Holland Paver', 'Hardscape', 'Pavers'),
  p('x1', 'Gift Card', '', ''),
];

// ── the drill ──────────────────────────────────────────────────────────────────────────────────

test('hot list: the root shows CATEGORIES, biggest first: the busiest bucket is the one a yard taps', () => {
 // AMENDED BY THE 27-AUG RULING (R2), keeping the name and what the gate protected. The order is
 // now first-appearance rather than a count sort, and the busiest bucket still leads because the
 // rows arrive ranked by what the yard actually sells. What the change bought is the half the
 // count sort made impossible: an admin who curates "which categories, THEIR ORDER": see below.
  const tiles = hotTiles(SET, HOT_ROOT);
  assert.deepEqual(tiles.map((t) => t.label), ['Bulk', 'Hardscape', UNGROUPED_LABEL]);
  assert.ok(tiles.every((t) => t.kind === 'group'));
  assert.equal(tiles[0].count, 5, 'and a group says how much is under it');
});

test('hot list: an ADMIN sets the category order, and a bigger bucket does not overrule it', () => {
 // R2(a): "an ADMIN curates the tree: which categories, their order, the items under each".
 // Curated order is row order, so the category an admin put first leads even when it is smallest.
  const curated = [
    p('c1', 'Holland Paver', 'Hardscape', 'Pavers'),
    p('m1', 'Black', 'Bulk', 'Mulch'),
    p('m2', 'Brown', 'Bulk', 'Mulch'),
    p('m3', 'Red', 'Bulk', 'Mulch'),
  ];
  assert.deepEqual(hotTiles(curated, HOT_ROOT).map((t) => t.label), ['Hardscape', 'Bulk'],
    'one Hardscape item beats three Bulk items because the admin said so');
 // Dragging the mulch to the front moves the whole category, in one gesture, with no other edit.
  const reordered = [curated[1], curated[2], curated[3], curated[0]];
  assert.deepEqual(hotTiles(reordered, HOT_ROOT).map((t) => t.label), ['Bulk', 'Hardscape']);
 // Subcategories obey the same rule one level down.
  const subs = [p('s1', 'River Rock', 'Bulk', 'Stone'), p('m1', 'Black', 'Bulk', 'Mulch'), p('m2', 'Brown', 'Bulk', 'Mulch')];
  assert.deepEqual(hotTiles(subs, drillInto(HOT_ROOT, 'Bulk')).map((t) => t.label), ['Stone', 'Mulch']);
});

test('hot list: Other still sinks last however the rows are ordered', () => {
 // It is where the grouping ran out, never a category anyone chose: so it never leads a board
 // just because an unfiled product happens to be the first row.
  const unfiledFirst = [p('x1', 'Gift Card', '', ''), p('m1', 'Black', 'Bulk', 'Mulch')];
  assert.deepEqual(hotTiles(unfiledFirst, HOT_ROOT).map((t) => t.label), ['Bulk', UNGROUPED_LABEL]);
});

test('hot list: bulk → mulch → black, the client\'s own path, three taps', () => {
  let path = HOT_ROOT;
  assert.deepEqual(hotTiles(SET, path).map((t) => t.label), ['Bulk', 'Hardscape', 'Other']);
  path = drillInto(path, 'Bulk');
  const subs = hotTiles(SET, path);
  assert.deepEqual(subs.map((t) => t.label), ['Mulch', 'Stone']);
  assert.ok(subs.every((t) => t.kind === 'group'));
  path = drillInto(path, 'Mulch');
  const items = hotTiles(SET, path);
  assert.deepEqual(items.map((t) => t.label), ['Black', 'Brown', 'Red']);
  assert.ok(items.every((t) => t.kind === 'item'), 'the third level ADDS: it does not drill further');
  assert.equal(items[0].pick.product_id, 'm1');
});

test('hot list: a category with no second facet shows its PRODUCTS: no ceremonial middle level', () => {
 // Tapping through a subcategory named after its own category is the kind of empty step a counter
 // notices on the fiftieth sale of the day.
  const flat = [p('a1', 'Bagged Salt', 'Winter', ''), p('a2', 'Ice Melt', 'Winter', '')];
  const tiles = hotTiles(flat, drillInto(HOT_ROOT, 'Winter'));
  assert.deepEqual(tiles.map((t) => t.kind), ['item', 'item']);
  assert.deepEqual(tiles.map((t) => t.label), ['Bagged Salt', 'Ice Melt']);
});

test('hot list: the drill stops at two levels: deeper does not exist', () => {
  const deep = drillInto(drillInto(HOT_ROOT, 'Bulk'), 'Mulch');
  assert.deepEqual(drillInto(deep, 'Anything'), deep, 'a third drill is a no-op, not a broken path');
});

// ── the way back is ONE tap, at any depth ──────────────────────────────────────────────────────

test('hot list: back from an item level lands among its SIBLINGS, not at the top of the store', () => {
  const deep = { category: 'Bulk', subcategory: 'Mulch' };
  assert.deepEqual(drillUp(deep), { category: 'Bulk', subcategory: '' });
  assert.deepEqual(drillUp(drillUp(deep)), HOT_ROOT);
  assert.deepEqual(drillUp(HOT_ROOT), HOT_ROOT, 'and back from the top is a no-op, never an error');
});

test('hot list: the breadcrumb is a POSITION: every level tappable, the current one marked', () => {
  assert.deepEqual(crumbs(HOT_ROOT).map((c) => [c.label, c.current]), [['All', true]]);
  assert.deepEqual(
    crumbs({ category: 'Bulk', subcategory: 'Mulch' }).map((c) => [c.label, c.current]),
    [['All', false], ['Bulk', false], ['Mulch', true]],
  );
});

test('hot list: the way back is in the GRID too: the thumb is already there, so the exit should be', () => {
  assert.match(listSrc, /data-testid="hot-back"/, 'a Back tile at the head of the grid');
  assert.match(listSrc, /this\.go\(drillUp\(this\.path\)\)/);
  assert.match(listSrc, /data-testid="hot-crumbs"/, 'and a tappable trail above it');
  assert.match(listSrc, /aria-current=\$\{c\.current \? 'true' : 'false'\}/);
});

// ── nothing may silently disappear ─────────────────────────────────────────────────────────────

test('hot list: a product with NO facets lands in Other: a curated tile never vanishes on a blank column', () => {
  const root = hotTiles(SET, HOT_ROOT);
  const other = root.find((t) => t.label === UNGROUPED_LABEL);
  assert.ok(other, 'the ungrouped bucket exists');
  assert.equal(other.count, 1);
  assert.equal(root[root.length - 1].label, UNGROUPED_LABEL, 'and it always sinks: nobody chose it as a category');
  assert.deepEqual(hotTiles(SET, drillInto(HOT_ROOT, UNGROUPED_LABEL)).map((t) => t.label), ['Gift Card']);
});

test('hot list: a level shows at most a dozen tiles: the flat grid\'s cap, applied where it belongs', () => {
  assert.equal(HOT_LEVEL_MAX, 12);
  const wide = Array.from({ length: 40 }, (_, i) => p(`w${i}`, `W${i}`, 'Bulk', `Sub${i}`));
  assert.equal(hotTiles(wide, HOT_ROOT).length, 1, 'one category');
  assert.equal(hotTiles(wide, drillInto(HOT_ROOT, 'Bulk')).length, 12, 'twelve at a time, not forty');
});

test('hot list: a malformed or empty answer is an empty tile set, never a crash', () => {
  for (const bad of [null, undefined, 'no', 7, [null, {}]]) assert.deepEqual(hotTiles(bad, HOT_ROOT), []);
});

test('hot list: a drilled path that no longer exists is detected, so nobody is stranded on an empty level', () => {
  assert.equal(pathExists(SET, { category: 'Bulk', subcategory: 'Mulch' }), true);
  assert.equal(pathExists(SET, { category: 'Retired', subcategory: '' }), false);
  assert.equal(pathExists(SET, HOT_ROOT), true);
  assert.match(listSrc, /if \(!pathExists\(items, this\.path\)\) this\.path = HOT_ROOT;/);
});

// ── the photo, and the absence of one ──────────────────────────────────────────────────────────

test('hot list: no photo is a NORMAL state: a drawn mark, never an error and never a broken box', () => {
  assert.match(listSrc, /<hos-product-thumb/, 'the tiles use the one thumbnail component');
  assert.match(thumbSrc, /@state\(\) private failed = false;/, 'which owns the fallback');
  assert.ok(!/onerror|broken/i.test(listSrc), 'the tile itself never handles an image failure: it cannot');
  assert.match(treeSrc, /thumbFor: members\.find\(\(m\) => m\.has_image\) \?\? members\[0\]/,
    'a GROUP borrows a photo from a member that has one, so a category is not blank because its first product is');
});

test("hot list: a subcategory per product is not a choice: DIB-553's fourth tap is gone", () => {
 // The seeded catalogue names a collection per product, so every mulch sat alone in its own
 // subcategory: Items -> Mulch -> Black -> Black Mulch was FOUR clicks, three of them on tiles
 // that look exactly like a product tile and add nothing.
  const mulch = [
    p('m1', 'Black Mulch', 'Mulch', 'Black'),
    p('m2', 'Brown Mulch', 'Mulch', 'Brown'),
    p('m3', 'Cedar Mulch', 'Mulch', 'Cedar'),
    p('m4', 'Red Mulch', 'Mulch', 'Red'),
  ];
  const tiles = hotTiles(mulch, drillInto(HOT_ROOT, 'Mulch'));
  assert.deepEqual(tiles.map((t) => t.kind), ['item', 'item', 'item', 'item']);
  assert.deepEqual(tiles.map((t) => t.label), ['Black Mulch', 'Brown Mulch', 'Cedar Mulch', 'Red Mulch']);
  assert.equal(tiles[0].pick.product_id, 'm1', 'and the tile carries the product, so ONE click adds it');
});

test('hot list: a level that really does group is left alone: the rule is "is this a choice"', () => {
 // Two subcategories over four products IS a choice, and it still costs its tap.
  const bulk = [
    p('a1', 'Black Mulch', 'Bulk', 'Mulch'),
    p('a2', 'Brown Mulch', 'Bulk', 'Mulch'),
    p('a3', 'River Rock', 'Bulk', 'Stone'),
    p('a4', 'HPB', 'Bulk', 'Stone'),
  ];
  const tiles = hotTiles(bulk, drillInto(HOT_ROOT, 'Bulk'));
  assert.deepEqual(tiles.map((t) => t.label), ['Mulch', 'Stone']);
  assert.ok(tiles.every((t) => t.kind === 'group'));
});

test('hot list: the ROOT is never flattened: the category board is the dealer\'s arrangement', () => {
 // One product per category at the root would flatten under a naive rule. It must not: the
 // categories are what an administrator arranged, and their muscle memory outranks one tap.
  const sparse = [p('s1', 'Black Mulch', 'Mulch', 'Black'), p('s2', 'River Rock', 'Aggregate', 'River')];
  const tiles = hotTiles(sparse, HOT_ROOT);
  assert.deepEqual(tiles.map((t) => t.label), ['Mulch', 'Aggregate']);
  assert.ok(tiles.every((t) => t.kind === 'group'));
});

test('hot list: group tiles carry a photo hint even when the representative has none', () => {
  const mixed = [p('g1', 'No pic', 'Bulk', 'Mulch'), p('g2', 'Has pic', 'Bulk', 'Mulch', { has_image: true })];
  assert.equal(hotTiles(mixed, HOT_ROOT)[0].thumbFor.product_id, 'g2');
  const none = [p('g3', 'No pic', 'Bulk', 'Mulch')];
  assert.equal(hotTiles(none, HOT_ROOT)[0].thumbFor.product_id, 'g3', 'and falls back to the first, never to undefined');
});

// ── the substrate did not move ─────────────────────────────────────────────────────────────────

test('hot list: the SET is still the dealer\'s curation: only the STRUCTURE changed', () => {
  assert.match(listSrc, /await quickPicks\(this\.branchId, this\.customerId\)/,
    'the same GET /pos/quick-picks the flat grid read: same settings-kit surface, same fallbacks');
  assert.match(listSrc, /hotTiles,/, 'and the tree is built here, in the frontend, where presentation belongs');
  assert.match(listSrc, /hotTiles\(this\.items, this\.path, HOT_LEVEL_MAX\)/);
});

test('hot list: big targets, one tab stop, and it stands down while a search term is up', () => {
  assert.match(listSrc, /@media \(pointer: coarse\)/, 'a touch till gets bigger tiles and a 44px crumb');
  assert.match(listSrc, /min-height: 116px/, 'and the tiles are thumb-sized to begin with');
  assert.match(listSrc, /tabindex=\$\{this\.roveIdx === i \? '0' : '-1'\}/, 'ONE tab stop for the whole grid');
  assert.match(listSrc, /private onGridKey\(e: KeyboardEvent\): void/, 'arrows move within it');
  assert.match(terminalSrc, /this\.lookupTerm\.trim\(\)\.length >= 2 \|\| this\.panels\[PANEL_HOTLIST\] === false\s*\n\s*\? nothing\s*\n\s*: html`<hos-pos-hot-list/,
    'two ways to pick a product on screen at once is the "two add paths" finding again: and a station that turned the hot list off gets neither');
});

test('hot list: the tiles wear the CUSTOMER\'s price, and the same picture rules as the search rows', () => {
  assert.match(listSrc, /pricePicture\(p\.price\)/, 'one decision, both surfaces');
  assert.match(listSrc, /pic\.showRetail \? html`<span class="was"/, 'retail struck only when they pay less');
  assert.match(listSrc, /pic\.showSaving/, 'and the saving only when there is one');
  assert.ok(!/[-*/]\s*retail_cents|retail_cents\s*[-*/]/.test(listSrc), 'no arithmetic on a tile');
});
