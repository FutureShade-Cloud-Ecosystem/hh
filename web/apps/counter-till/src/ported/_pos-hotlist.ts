// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-hotlist.ts; adapted for HH.

// _pos-hotlist.ts: the counter's HOT LIST, as a drillable tree. Pure; no DOM, no fetch.
//
// THE CLIENT'S ASK (the counter lead, and the back-office lead's volume note behind it): touchscreen tiles with
// photos, drilled category → subcategory → item: "bulk → mulch → black": and "something that
// BisTrack does not do". Bulk material volume dwarfs contracts and projects at this yard, so this
// is the HIGH-FREQUENCY path, not decoration. It supersedes the flat quick-pick grid.
//
// WHAT DID NOT CHANGE: the SET. Which products are on the hot list is still dealer curation
// through the settings kit (surface `pos.counter`, kind chip), still user-over-dealer whole-or-
// absent, still derived from what the yard actually sells when nobody has curated anything. Only
// the STRUCTURE changed: the substrate is untouched, which is why this arrived without a
// migration. The server sends flat rows carrying their two facet names; the tree is built here,
// because how a counter reads a grouping is presentation and must not be a schema decision.
//
// THE UNGROUPED CASE IS ORDINARY. A catalogue row with no application, or no collection, is not an
// error and is never dropped: it surfaces at the level where its grouping runs out, still one tap
// from the ticket. A curated tile that vanished because a column was blank would be the worst kind
// of silent configuration failure: the dealer set it, and it is simply not there.

import type { HotPick } from '@hardscapehouse/pos-kit/src/ported/pos-api.js';

/** Tiles shown at one level. The flat grid's old cap, applied where it belongs: per level. */
export const HOT_LEVEL_MAX = 12;

/** Where the drill currently is. Empty strings mean "not drilled that far yet". */
export interface HotPath {
  category: string;
  subcategory: string;
}

export const HOT_ROOT: HotPath = { category: '', subcategory: '' };

/** One tile at the current level: either a group to drill into, or a product to add. */
export interface HotTile {
  kind: 'group' | 'item';
  /** The group's name, or the product's name. */
  label: string;
  /** How many products sit under a group. Groups only; a tile that says "1" is worth a tap too. */
  count: number;
  /** The product, for an item tile. */
  pick?: HotPick;
  /** A representative product, so a GROUP tile can wear a photo instead of an empty square. */
  thumbFor?: HotPick;
}

/** Where a product lands when its grouping runs out. Sorted last; never a dropped tile. */
export const UNGROUPED_LABEL = 'Other';

const clean = (v: unknown): string => (typeof v === 'string' ? v.trim() : '');

/**
 * A facet value, as the tree names it. A blank facet is not a blank tile: it is the UNGROUPED
 * bucket, and it must resolve back the same way on the way DOWN as it did on the way up. (Getting
 * this wrong is how a curated product becomes a tile you can see and cannot open.)
 */
const facet = (v: unknown): string => clean(v) || UNGROUPED_LABEL;

/** The tiles to show at `path`, in the order they should be tapped through. */
export function hotTiles(items: readonly HotPick[] | null | undefined, path: HotPath, cap = HOT_LEVEL_MAX): HotTile[] {
  const all = (Array.isArray(items) ? items : []).filter((p) => p && typeof p.product_id === 'string');
  if (!path.category) return groupTiles(all, (p) => facet(p.category), cap);
  const inCat = all.filter((p) => facet(p.category) === path.category);
  if (!path.subcategory) {
    // A category whose members carry no second facet has nothing to drill INTO, so it shows its
    // products directly. Making someone tap through a subcategory named after the category is the
    // kind of empty ceremony a counter notices on the fiftieth sale of the day.
    const subs = new Set(inCat.map((p) => clean(p.subcategory)).filter((s) => s !== ''));
    if (subs.size === 0) return itemTiles(inCat, cap);
 // DIB-553: AND NEITHER DOES A SUBCATEGORY PER PRODUCT.
    //
    // The rule two lines up covers the case where the second facet is missing. This is the same
    // ceremony arriving through a facet that is PRESENT and unique: this dealer names a collection
    // per product, so Mulch's four members sat in four one-member subcategories and every add cost
    // a tap on a tile with exactly one thing behind it.
    //
    // MEASURED on the seeded counter before this line: adding Black Mulch from the hot list took
 // FOUR clicks: Items, Mulch, Black, Black Mulch: and three of them landed on tiles that look
    // exactly like a product tile and do not add a product. That is the whole of "single click adds
    // a product": the tile a person presses is not the product printed on it.
    //
    // The test is whether the level offers a CHOICE. One group per product is not a choice, it is
    // the same list with a tap in front of it. A level that groups eleven products into three
 // categories still groups and is untouched, and the ROOT is never flattened at all: the
    // category board is the dealer's curated arrangement and its muscle memory outranks one tap.
    if (subGroupCount(inCat) === inCat.length) return itemTiles(inCat, cap);
    return groupTiles(inCat, (p) => facet(p.subcategory), cap);
  }
  return itemTiles(
    inCat.filter((p) => facet(p.subcategory) === path.subcategory),
    cap,
  );
}

/**
 * Group tiles, IN THE ORDER THEIR FIRST MEMBER ARRIVED: which is the admin's order when the set is
 * curated and the yard's own sales rank when it is derived.
 *
 * WHY THIS AND NOT "BIGGEST BUCKET FIRST" (ruling R2). The ruling asks for an admin who curates
 * the tree: "which categories, THEIR ORDER, which items sit under each". Sorting by member count
 * made that impossible to express: an administrator who dragged the mulch to the front watched the
 * board reorder itself behind them, because the order was computed rather than chosen. First
 * appearance IS the expressible order: put the black mulch at the top of the curated set and Mulch
 * leads the bulk board. On a yard that has curated nothing the incoming rows are already ranked by
 * what actually sells, so the busiest bucket still leads: the property the old sort was reaching
 * for, arrived at from the data rather than imposed on it.
 *
 * The order is a pure function of the incoming row order, so it does not reshuffle between reads:
 * a moving target is the enemy of muscle memory, which was the other half of the old rule.
 *
 * Members with a BLANK facet are collected under UNGROUPED_LABEL rather than dropped.
 */
/** How many distinct subcategories a category's members fall into: DIB-553's "is this a choice?". */
function subGroupCount(items: readonly HotPick[]): number {
  return new Set(items.map((p) => facet(p.subcategory))).size;
}

function groupTiles(items: readonly HotPick[], key: (p: HotPick) => string, cap: number): HotTile[] {
  const groups = new Map<string, HotPick[]>();
  for (const p of items) {
    const k = key(p);
    const bucket = groups.get(k);
    if (bucket) bucket.push(p);
    else groups.set(k, [p]);
  }
  const tiles: HotTile[] = [];
  for (const [label, members] of groups) {
    // Map preserves insertion order, so this loop IS the curated order.
    tiles.push({
      kind: 'group',
      label,
      count: members.length,
      // A group borrows a photo from the FIRST curated member that has one, so the tile an admin
      // put at the top of the group is the tile the group wears.
      thumbFor: members.find((m) => m.has_image) ?? members[0],
    });
  }
  // 'Other' always sinks: it is where the grouping ran out, never a category anyone chose.
  tiles.sort((a, b) => Number(a.label === UNGROUPED_LABEL) - Number(b.label === UNGROUPED_LABEL));
  return tiles.slice(0, Math.max(0, cap));
}

function itemTiles(items: readonly HotPick[], cap: number): HotTile[] {
  return items.slice(0, Math.max(0, cap)).map((p) => ({ kind: 'item' as const, label: p.name, count: 1, pick: p }));
}

/**
 * drillInto: where a group tile takes you. Called only for group tiles; an item tile adds.
 *
 * When the tap lands on a group at root, the category is set. One level down, the subcategory.
 * Deeper than that does not exist: two levels was the client's example and it is also the depth at
 * which a tree stops being faster than typing.
 */
export function drillInto(path: HotPath, label: string): HotPath {
  if (!path.category) return { category: label, subcategory: '' };
  if (!path.subcategory) return { category: path.category, subcategory: label };
  return path;
}

/**
 * drillUp: ONE TAP BACK, at any depth. The client asked for a back/breadcrumb affordance that is
 * one tap, so this is one step, never a reset: a person who drilled bulk → mulch and wants bagged
 * mulch instead should land back among the bulk subcategories, not at the top of the store.
 */
export function drillUp(path: HotPath): HotPath {
  if (path.subcategory) return { category: path.category, subcategory: '' };
  return HOT_ROOT;
}

/**
 * crumbs: the breadcrumb, as the levels you can tap back to. The CURRENT level is included and
 * marked, so the trail reads as a position rather than as a list of links to elsewhere.
 */
export interface Crumb {
  label: string;
  path: HotPath;
  current: boolean;
}

export function crumbs(path: HotPath, rootLabel = 'All'): Crumb[] {
  const out: Crumb[] = [{ label: rootLabel, path: HOT_ROOT, current: !path.category }];
  if (path.category) {
    out.push({
      label: path.category,
      path: { category: path.category, subcategory: '' },
      current: !path.subcategory,
    });
  }
  if (path.subcategory) {
    out.push({ label: path.subcategory, path: { ...path }, current: true });
  }
  return out;
}

/** Is a path still valid against the items on hand? A curated set that changed must not strand. */
export function pathExists(items: readonly HotPick[] | null | undefined, path: HotPath): boolean {
  if (!path.category) return true;
  return hotTiles(items, path).length > 0;
}
