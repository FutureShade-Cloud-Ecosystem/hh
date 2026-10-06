// The product tiles: one drawn swatch-and-icon tile per product category, a light and a dark
// variant of each (`pavers.svg`, `pavers-dark.svg`). They are what a product's picture shows when
// the product has no real picture, so a real photo can replace a tile later with no screen change.
//
// The tiles are repo files, bundled by the build; nothing is fetched from outside. They are drawn
// in their own colours and not in currentColor: a tile is shown through an img element, where an
// SVG cannot inherit the page's colour, so the light and dark pair is the theming.
//
// A category is whatever name the surface carries for a product's family (the application on a
// counter hit, the category on a hot list tile, the catalogue's own category name). An unknown
// name resolves to no tile, never to a guess: the caller keeps its own honest fallback.

export type ProductTileKey =
  | 'pavers'
  | 'walls'
  | 'steps'
  | 'sand-and-base'
  | 'aggregates'
  | 'mulch'
  | 'soil'
  | 'accessories';

export type ProductTileTheme = 'light' | 'dark';

// One literal URL per file: the bundler only rewrites `new URL('<literal>', import.meta.url)`.
const TILES: Record<ProductTileKey, Record<ProductTileTheme, string>> = {
  pavers: {
    light: new URL('./pavers.svg', import.meta.url).href,
    dark: new URL('./pavers-dark.svg', import.meta.url).href,
  },
  walls: {
    light: new URL('./walls.svg', import.meta.url).href,
    dark: new URL('./walls-dark.svg', import.meta.url).href,
  },
  steps: {
    light: new URL('./steps.svg', import.meta.url).href,
    dark: new URL('./steps-dark.svg', import.meta.url).href,
  },
  'sand-and-base': {
    light: new URL('./sand-and-base.svg', import.meta.url).href,
    dark: new URL('./sand-and-base-dark.svg', import.meta.url).href,
  },
  aggregates: {
    light: new URL('./aggregates.svg', import.meta.url).href,
    dark: new URL('./aggregates-dark.svg', import.meta.url).href,
  },
  mulch: {
    light: new URL('./mulch.svg', import.meta.url).href,
    dark: new URL('./mulch-dark.svg', import.meta.url).href,
  },
  soil: {
    light: new URL('./soil.svg', import.meta.url).href,
    dark: new URL('./soil-dark.svg', import.meta.url).href,
  },
  accessories: {
    light: new URL('./accessories.svg', import.meta.url).href,
    dark: new URL('./accessories-dark.svg', import.meta.url).href,
  },
};

export const PRODUCT_TILE_KEYS = Object.keys(TILES) as ProductTileKey[];

// The words each tile answers to, read from a category name split into lower case words. Order
// matters: the first tile with a matching word wins, so "Slabs & Steps" is steps and "Bedding
// Sand" is sand and base. The words come from the catalogue's own vocabulary (its category names
// and the application values the seeds file products under).
const WORDS: ReadonlyArray<readonly [ProductTileKey, readonly string[]]> = [
  ['sand-and-base', ['sand', 'base', 'bedding', 'drainage']],
  ['aggregates', ['aggregate', 'aggregates', 'stone', 'gravel', 'screenings']],
  ['mulch', ['mulch']],
  ['soil', ['soil', 'topsoil']],
  ['walls', ['wall', 'walls', 'coping']],
  ['steps', ['step', 'steps', 'slab', 'slabs', 'slabstep']],
  ['pavers', ['paver', 'pavers', 'patio']],
  ['accessories', ['accessory', 'accessories', 'edging', 'jointing', 'sealing', 'lighting', 'landscape']],
];

/** The tile a category name calls for, or null when the name is empty or none of the tiles' words. */
export function productTileKey(category: string | undefined | null): ProductTileKey | null {
  const words = (category ?? '').toLowerCase().split(/[^a-z0-9]+/).filter(Boolean);
  if (words.length === 0) return null;
  for (const [key, names] of WORDS) {
    if (words.some((w) => names.includes(w))) return key;
  }
  return null;
}

/** The URL of the tile for a category in a theme, or null when the category has no tile. */
export function productTileUrl(category: string | undefined | null, theme: ProductTileTheme): string | null {
  const key = productTileKey(category);
  return key === null ? null : TILES[key][theme];
}
