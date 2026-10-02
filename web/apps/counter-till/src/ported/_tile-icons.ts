// Ported from hardscapeos_dibbits master 87d7941b app/src/_tile-icons.ts; adapted for HH.

// _tile-icons.ts: the dashboard's icons, as GEOMETRY.
//
// ── THE BUG THIS FILE EXISTS TO END ─────────────────────────────────────────────────────────────
//
// Every home tile used to draw its icon by putting a character in a <span>: the server's
// `TileMeta.Glyph` ("🛡️", "▦") for a provider tile, `DashboardMetric.glyph` ("🗂") for a shell
// card. A character is only an icon if the machine has a font that covers it. On a machine with no
// emoji font: the commander's, and any locked-down till: the browser draws its last-resort glyph
// instead: a box, and for the astral-plane codepoints a box printed with the hex inside it. That is
// where "01F 6E1" (U+1F6E1, Open claims) and "01F 5C2" (U+1F5C2, Open workspaces) came from, and
// why Pick queue (U+25A6) and Weights pending (U+2696) were bare tofu. It is not a data problem and
// it is not fixable by choosing better emoji: ANY character-as-icon mechanism fails this way.
//
// So the mechanism is gone. An icon here is a list of SVG path commands: geometry the renderer
// draws itself, with no font involved, in `currentColor` so it inherits the theme's ink. There is
// no code path left that can put a codepoint in an icon slot: the components read this registry by
// id and never touch `glyph` again. `_home-icons.test.mjs` fails if that regresses, if any tile id
// in the shipped catalog is unmapped, or if a single non-ASCII character appears in this file.
//
// The server's `glyph` field is UNTOUCHED on the wire: the payload still carries it, byte for
// byte, so nothing that reads the dashboard API changes shape. It is simply no longer what draws.
//
// ── DRAWING RULES ───────────────────────────────────────────────────────────────────────────────
// 24x24 viewBox, stroked not filled, 2 stroke, round caps and joins. One visual weight for every
// icon so a wall of tiles reads as one system, and no icon carries a colour of its own: colour on
// this dashboard means STATUS, and an icon that is merely an identity must not spend it.
//
// THE WEIGHT WAS 1.7 AND THE RULE ABOVE WAS NOT TRUE. The rule says one visual weight; the app
// shipped seven. A sweep of app/src found stroke-width at 2 (fifteen sites), 1.8 (ten), 1.6 (ten),
// 1.5, 1.9, 1.7 and 4. The registry's own two render sites were the 1.7, so the tiles this file
// exists to unify were lighter than most of the app around them. The number is now 2, which is both
// the majority weight already in the tree and the weight of the icon library Dibbits' design team
// supplied (120 icons, 24x24 grid, 2px stroke, rounded corners). `_home-icons.test.mjs` pins it, so
// the rule is now enforced rather than asserted.
//
// FOUR THINGS ARE DELIBERATELY NOT 2, and a future sweep should leave them alone:
// - portal/portal-shell.ts draws its nav on a 20x20 viewBox at 1.6. On a 20-unit grid that is
// proportionally 1.92 on a 24-unit one, so it already MATCHES this weight. Setting it to 2
// would make the phone nav heavier than everything else, which is the mistake a blind
// find-and-replace makes.
// - _tanya-mark.ts is a drawn identity mark, not a UI icon, and carries its own ruling.
// - product-thumb.ts is the stacked-boxes placeholder for a missing product image.
// - widget-builder.ts's 4 is a loading spinner ring.

/** One icon: the path commands that draw it, and the word a text alternative would use. */
export interface TileIcon {
  /** SVG path `d` strings, drawn in order, stroked in currentColor. Frozen: geometry the
   * registry hands out is read-only by contract (the generated registry deep-freezes every
   * array, and a consumer must never bend a shared drawing after load). */
  paths: readonly string[];
}

/**
 * The registry, keyed by the id the caller already has: a provider tile's `tile_id`
 * (internal/dashboard/model.go), a shell card's `DashboardMetric.id`, or a visualization SHAPE
 * (the `viz.*` keys, part two). Ids are add-only, exactly as tile ids are: a rename orphans an
 * icon and drops a tile back to the fallback.
 */
export const TILE_ICONS: Record<string, TileIcon> = {
  // ── provider tiles ────────────────────────────────────────────────────────────────────────────
 // Today's sales: a till receipt with a torn foot.
  'sales.today': {
    paths: ['M6 3.5h12v17l-2-1.4-2 1.4-2-1.4-2 1.4-2-1.4z', 'M9 8h6', 'M9 12h6'],
  },
 // Open orders: a document with lines and a folded corner.
  'orders.open': {
    paths: ['M6 3h8l4 4v14H6z', 'M14 3v4h4', 'M9 12h6', 'M9 16h4'],
  },
 // Deliveries today: a box van.
  'deliveries.today': {
    paths: ['M2.5 6.5h11v9h-11z', 'M13.5 9.5h4l3 3.2v2.8h-7z', 'M6 15.5a2 2 0 1 0 4 0a2 2 0 1 0-4 0', 'M15 15.5a2 2 0 1 0 4 0a2 2 0 1 0-4 0'],
  },
 // Pick queue: stacked pick tickets waiting their turn.
  'picks.queue': {
    paths: ['M4 7.5h11v12H4z', 'M7 4.5h11v12', 'M7 11h5', 'M7 15h5'],
  },
 // AR aging: an invoice with a currency mark.
  'ar.aging': {
    paths: ['M5 3.5h14v17l-2.3-1.6-2.4 1.6-2.3-1.6-2.4 1.6L5 18.5z', 'M12 7v9', 'M14.4 9.1a2.4 2.4 0 0 0-2.4-1.6c-1.3 0-2.4.8-2.4 2s1.1 1.8 2.4 1.8 2.4.7 2.4 1.9-1.1 2-2.4 2a2.4 2.4 0 0 1-2.4-1.6'],
  },
 // Low stock: a carton with its lid seam.
  'inventory.low_stock': {
    paths: ['M3.5 7.5 12 3.5l8.5 4v9L12 20.5l-8.5-4z', 'M3.5 7.5 12 11.6l8.5-4.1', 'M12 11.6v8.9'],
  },
 // Open claims: a shield.
  'warranty.open_claims': {
    paths: ['M12 3.2 4.5 6.2v6c0 4.2 3.1 7.4 7.5 8.6 4.4-1.2 7.5-4.4 7.5-8.6v-6z', 'M9 12.2l2.2 2.2 4-4.4'],
  },
 // Unread messages: a speech bubble.
  'messaging.unread': {
    paths: ['M4 5.5h16v11H12l-5 4v-4H4z', 'M8 9h8', 'M8 12.5h5'],
  },
 // Stale picks: an hourglass: the queue that stopped moving.
  'picks.stale': {
    paths: ['M6.5 3.5h11', 'M6.5 20.5h11', 'M8 3.5v3.6L12 12l-4 4.9v3.6', 'M16 3.5v3.6L12 12l4 4.9v3.6'],
  },
 // Weights pending: a balance beam.
  'weight.pending': {
    paths: ['M12 4.5v15', 'M6 19.5h12', 'M4 8h16', 'M4 8 1.8 13.4h4.4z', 'M20 8l-2.2 5.4h4.4z'],
  },
 // Payments today: a banknote.
  'payments.today': {
    paths: ['M2.5 6h19v12h-19z', 'M9.6 12a2.4 2.4 0 1 0 4.8 0a2.4 2.4 0 1 0-4.8 0', 'M5.5 9v6', 'M18.5 9v6'],
  },

  // ── shell cards (app-shell.ts DashboardMetric ids) ────────────────────────────────────────────
 // Pending approvals: a decision stamped.
  approvals: {
    paths: ['M5 4.5h14v15H5z', 'M8.5 12l2.4 2.5 4.6-5'],
  },
 // Open workspaces: overlapping panes.
  'open-units': {
    paths: ['M3.5 7.5h12v12h-12z', 'M8.5 7.5v-3h12v12h-5'],
  },
 // System status: a live pulse.
  system: {
    paths: ['M2.5 12h4.2l2.3-6 3.6 12 2.4-6h6.5'],
  },

  // ── visualization shapes ──────────────────────────────────────────────────────────────────────
  // A composed chart wears the mark of WHAT IT DRAWS, not of the module it measures: on a band of
  // charts the useful distinction at a glance is bars-versus-proportion-versus-figures, and the
  // module is already the first word of the tile's label.
  'viz.time_series': { paths: ['M3.5 19.5V4', 'M3.5 19.5H21', 'M6.5 16l4-5 3.5 3 5-7'] },
  'viz.breakdown': { paths: ['M3.5 19.5V4', 'M3.5 19.5H21', 'M6.5 19.5v-5', 'M11 19.5v-9', 'M15.5 19.5v-6', 'M20 19.5v-11'] },
  'viz.single_value': { paths: ['M4 6.5h16', 'M4 12h9', 'M4 17.5h13'] },
  'viz.distribution': { paths: ['M3.5 8.5h17v7h-17z', 'M10 8.5v7', 'M14.5 8.5v7'] },
  // The three SHIPPED shapes of the curated metric catalog. `bar` reuses the breakdown mark and
 // `split` the distribution mark rather than drawing two more nearly-identical glyphs: one visual
  // weight for the whole system is the rule, and two icons for one idea is how that erodes.
  'viz.bar': { paths: ['M3.5 19.5V4', 'M3.5 19.5H21', 'M6.5 19.5v-5', 'M11 19.5v-9', 'M15.5 19.5v-6', 'M20 19.5v-11'] },
  'viz.split': { paths: ['M3.5 8.5h17v7h-17z', 'M10 8.5v7', 'M14.5 8.5v7'] },
  'viz.list': { paths: ['M4 6.5h16', 'M4 12h9', 'M4 17.5h13'] },

  // ── the neutral fallback ──────────────────────────────────────────────────────────────────────
  // An id nobody mapped still gets an ICON, never a codepoint and never an empty box: a plain
  // rounded square. Deliberately dull, so an unmapped tile is visible to whoever added it without
  // ever being illegible to the person using the dashboard.
  _fallback: { paths: ['M5 5.5h14v13H5z'] },
};

/** The id the fallback is stored under. Exported so the drift test can name it. */
export const FALLBACK_ICON_ID = '_fallback';

/**
 * The icon for an id. Never returns undefined and never returns text: an unmapped id gets the
 * neutral square, which is a rendering decision, not an error: a tile that shipped after this
 * build must still draw.
 *
 * A pinned visualization's tile id is `viz:<uuid>` and carries its shape after a second colon
 * (`viz:<uuid>:time_series`); the shape decides the icon, so every pinned chart wears the mark of
 * what it draws.
 *
 * THE KEY IS ONLY AS WELL BEHAVED AS THE WIRE IS. The id arrives from a server payload (the
 * dashboard-editor's layout rows), so it can be a number or an object, and a plain-object lookup
 * resolves an id that names something every object inherits (`constructor`, `__proto__`,
 * `toString`, `hasOwnProperty`, `valueOf`) through Object.prototype: an inherited function,
 * truthy, with no `paths`, which made the builder throw. So a non-string id is simply absent, and
 * membership is OWN-KEY ONLY (`Object.hasOwn`); anything absent draws the neutral square. The
 * same rule holds in the consumer layer's lookups (`ownValue` in _icon-consumer.ts), and
 * _icon-consumer.test.mjs feeds every name and a non-string id through the real render.
 */
export function tileIcon(id: string): TileIcon {
  if (typeof id !== 'string') return TILE_ICONS[FALLBACK_ICON_ID];
  if (id.startsWith('viz:')) {
    const shape = id.split(':')[2] ?? '';
    return TILE_ICONS[`viz.${shape}`] ?? TILE_ICONS[FALLBACK_ICON_ID];
  }
  // A COMPOSED chart id is `viz.<metric>-<dimension>-<shape>` (the curated metric catalog's stored
  // key, _viz-tile.composeId). The shape is the last dash-separated part, and it is what decides
 // the icon: the same rule as the colon form above, over the id shape the substrate actually
  // stores. A composed id whose shape this build does not draw falls back like anything else.
  // (These two constructed keys always begin `viz.`, which no Object.prototype member does, so a
  // plain index cannot resolve an inherited name here.)
  if (id.startsWith('viz.') && id.includes('-')) {
    const shape = id.slice(id.lastIndexOf('-') + 1);
    return TILE_ICONS[`viz.${shape}`] ?? TILE_ICONS[FALLBACK_ICON_ID];
  }
  return Object.hasOwn(TILE_ICONS, id) ? TILE_ICONS[id] : TILE_ICONS[FALLBACK_ICON_ID];
}

/** True when `id` has an icon of its own (i.e. does not fall back). The drift test's predicate. */
export function hasTileIcon(id: string): boolean {
  return Object.prototype.hasOwnProperty.call(TILE_ICONS, id) && id !== FALLBACK_ICON_ID;
}

/** The ids from `ids` that would fall back. Empty is the passing state. */
export function unmappedIconIds(ids: readonly string[]): string[] {
  return ids.filter((id) => !hasTileIcon(id));
}
