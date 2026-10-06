// The product tiles are clean assets: every category has a light and a dark drawing, each a
// self-contained SVG (viewBox set, no transforms, no scripts, no outside references), and the
// resolver answers every category word with the tile that carries it and nothing else.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

import { PRODUCT_TILE_KEYS, productTileKey, productTileUrl } from '../src/assets/product-tiles/index.js';

const dir = join(new URL('..', import.meta.url).pathname, 'src', 'assets', 'product-tiles');

test('every tile key has a light and a dark file, and no other svg is in the folder', () => {
  const want = PRODUCT_TILE_KEYS.flatMap((k) => [`${k}.svg`, `${k}-dark.svg`]).sort();
  const have = readdirSync(dir).filter((f) => f.endsWith('.svg')).sort();
  assert.deepEqual(have, want);
});

test('every tile is a clean svg: viewBox, no transform, no script, no outside reference', () => {
  for (const file of readdirSync(dir).filter((f) => f.endsWith('.svg'))) {
    const text = readFileSync(join(dir, file), 'utf8');
    assert.match(text, /^<svg [^>]*viewBox="0 0 120 120"/, `${file} has no viewBox`);
    assert.doesNotMatch(text, /transform=/i, `${file} carries a transform`);
    assert.doesNotMatch(text, /<script|<image|<foreignObject|href=|url\(|@import|on\w+=/i, `${file} reaches outside itself`);
    assert.doesNotMatch(text, /[\u2013\u2014]/, `${file} carries a dash the house rules refuse`);
  }
});

test('the dark drawing differs from the light one for every tile', () => {
  for (const key of PRODUCT_TILE_KEYS) {
    assert.notEqual(readFileSync(join(dir, `${key}.svg`), 'utf8'), readFileSync(join(dir, `${key}-dark.svg`), 'utf8'), key);
  }
});

test('the resolver maps the catalogue vocabulary to tiles, and never guesses', () => {
  const cases = {
    Paver: 'pavers', Patio: 'pavers', Pavers: 'pavers', PAVER: 'pavers',
    Wall: 'walls', 'Retaining Walls': 'walls', Coping: 'walls',
    Step: 'steps', 'Slabs & Steps': 'steps', SLABSTEP: 'steps',
    Sand: 'sand-and-base', Base: 'sand-and-base', Bedding: 'sand-and-base', Drainage: 'sand-and-base',
    Aggregate: 'aggregates', Aggregates: 'aggregates',
    Mulch: 'mulch', Soil: 'soil', Topsoil: 'soil',
    Accessories: 'accessories', ACCESSORY: 'accessories', Edging: 'accessories', Jointing: 'accessories', Lighting: 'accessories',
  };
  for (const [name, key] of Object.entries(cases)) assert.equal(productTileKey(name), key, name);
  for (const name of ['', '  ', 'Bulk', 'Gift cards', undefined, null]) assert.equal(productTileKey(name), null, String(name));
  assert.ok(productTileUrl('Paver', 'light').endsWith('/pavers.svg'));
  assert.ok(productTileUrl('Paver', 'dark').endsWith('/pavers-dark.svg'));
  assert.equal(productTileUrl('Bulk', 'light'), null);
});
