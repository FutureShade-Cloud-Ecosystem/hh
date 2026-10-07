// THE LIGHT-CONTRAST PINS (decision log row 987, Colton's B). The preview's
// paired shots put today's light surfaces too close together for Colton's
// eye, and he picked the stronger step: a darker page under a white card, a
// darker line between them, and the two text inks re-held at AA on the new
// surfaces. This gate pins the SURFACE steps and the INK holds the row
// names, read from the sheet's own light block, so the step cannot quietly
// regress the next time a value is touched:
//
//   - card against page at 1.19 (B's measured reading), line against card
//     at 1.70 (B's reading);
//   - text-dim and accent-text at AA (4.5) on BOTH the page and the card;
//   - the widget shadow keeps a brief first layer whose alpha clears 0.06,
//     so the front door's role cards lift in step with elev-1.
//
// The arithmetic is the shared helper's (the same WCAG 2.x relative
// luminance and ratio the measured-contrast gate uses), and the light values
// are read the way every gate here reads them: the sheet's light block over
// its base, comments stripped first. Row 987's floors are recorded at the
// preview's two-decimal measurement precision, so a pin holds the ratio to
// within half a hundredth of the recorded floor and no further (B's own
// card-on-page ratio is 1.18871, which READS 1.19 at that precision).

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { palette, resolveColor, contrast } from './_lib/sheet.mjs';

const light = palette('light');
const value = (tok) => light[tok];
const flat = (tok) => resolveColor(light[tok], light);

const CARD_PAGE_FLOOR = 1.19;
const LINE_CARD_FLOOR = 1.70;
const AA = 4.5;
const SHADOW_ALPHA_FLOOR = 0.06;
const HALF_HUNDREDTH = 0.005;

test("card against page holds row 987's floor (1.19 at the recorded precision)", () => {
  const card = flat('--hh-card');
  const page = flat('--hh-page');
  assert.ok(card && page, 'the light block must declare flat --hh-card and --hh-page values');
  const ratio = contrast(card, page);
  assert.ok(
    ratio >= CARD_PAGE_FLOOR - HALF_HUNDREDTH,
    `card ${card} on page ${page} reads ${ratio.toFixed(5)}:1, under the ${CARD_PAGE_FLOOR}:1 floor ` +
      `row 987 sets (the floor is recorded at two decimals; the pin accepts half a hundredth below it)`,
  );
});

test("line against card holds row 987's floor (1.70 at the recorded precision)", () => {
  const line = flat('--hh-line');
  const card = flat('--hh-card');
  assert.ok(line && card, 'the light block must declare flat --hh-line and --hh-card values');
  const ratio = contrast(line, card);
  assert.ok(
    ratio >= LINE_CARD_FLOOR - HALF_HUNDREDTH,
    `line ${line} on card ${card} reads ${ratio.toFixed(5)}:1, under the ${LINE_CARD_FLOOR}:1 floor ` +
      `row 987 sets (the floor is recorded at two decimals; the pin accepts half a hundredth below it)`,
  );
});

for (const ink of ['--hh-text-dim', '--hh-accent-text']) {
  for (const surface of ['--hh-page', '--hh-card']) {
    test(`${ink} holds AA (${AA}:1) on ${surface} in the light half`, () => {
      const fg = flat(ink);
      const bg = flat(surface);
      assert.ok(fg && bg, `${ink} and ${surface} must resolve to flat colours in the light palette`);
      const ratio = contrast(fg, bg);
      assert.ok(
        ratio >= AA,
        `${ink} ${fg} on ${surface} ${bg} reads ${ratio.toFixed(2)}:1, under the AA ${AA}:1 bar. ` +
          `Row 987 darkens the page, so each text ink is re-held at AA on BOTH surfaces: darken the ink.`,
      );
    });
  }
}

test('the light widget shadow lifts in step with elev-1: a brief first layer at or past the 0.06 alpha floor', () => {
  const shadow = value('--hh-widget-shadow');
  assert.equal(typeof shadow, 'string', 'the light block must declare --hh-widget-shadow as a shadow value');
  assert.ok(shadow.startsWith('0 '), `the widget shadow must stay a brief block starting with the 0 offset, got: ${shadow}`);
  const firstLayer = shadow.match(/rgba\([^)]*\)/);
  assert.ok(firstLayer, `the widget shadow's first layer must be an rgba colour, got: ${shadow}`);
  const alpha = parseFloat(firstLayer[0].split(',')[3]);
  assert.ok(
    alpha >= SHADOW_ALPHA_FLOOR,
    `the widget shadow's first layer alpha ${alpha} is under the ${SHADOW_ALPHA_FLOOR} floor: the front door's ` +
      `role cards ride this shadow, and row 987 lifts it in step with elev-1 so they lift like the rest`,
  );
});

// ROUND 7 (the visual review of the demo box): hairline borders on white still barely read on
// the till's step cards, the panes and the table rows. The line steps darker so a card, a pane
// and a row each have an edge the eye finds, the resting shadow's contact layer steps up with it,
// and every input takes ONE light fill (the card's white) so a field on a white card is not a
// grey slab. The ink holds above (text-dim and accent-text at AA on the page and the card) bind
// the page, which is why the separation comes from the line and the shadow, never a darker page.
const LINE_CARD_R7 = 2.5;
const LINE_PAGE_R7 = 2.0;
const HAIRLINE_ALPHA_R7 = 0.2;
const ELEV_CONTACT_ALPHA_R7 = 0.14;

test('round 7: the line reads against the white card at 2.5:1 and against the page at 2.0:1', () => {
  const line = flat('--hh-line');
  const card = flat('--hh-card');
  const page = flat('--hh-page');
  assert.ok(line && card && page, 'the light block must declare flat --hh-line, --hh-card and --hh-page');
  const onCard = contrast(line, card);
  const onPage = contrast(line, page);
  assert.ok(onCard >= LINE_CARD_R7, `line ${line} on card ${card} reads ${onCard.toFixed(2)}:1, under the round 7 ${LINE_CARD_R7}:1 floor`);
  assert.ok(onPage >= LINE_PAGE_R7, `line ${line} on page ${page} reads ${onPage.toFixed(2)}:1, under the round 7 ${LINE_PAGE_R7}:1 floor`);
});

test('round 7: the light hairline and the elev-1 contact layer step up with the line', () => {
  const hairline = parseFloat(value('--hh-hairline').split(',')[3]);
  assert.ok(hairline >= HAIRLINE_ALPHA_R7, `the light hairline alpha ${hairline} is under ${HAIRLINE_ALPHA_R7}`);
  const elev = value('--hh-elev-1');
  const first = elev.match(/rgba\([^)]*\)/);
  assert.ok(first, `elev-1 must start with an rgba layer, got: ${elev}`);
  const alpha = parseFloat(first[0].split(',')[3]);
  assert.ok(alpha >= ELEV_CONTACT_ALPHA_R7, `elev-1's first layer alpha ${alpha} is under ${ELEV_CONTACT_ALPHA_R7}`);
});

test('round 7: one light input fill, the card white, declared as --hh-field', () => {
  assert.equal(value('--hh-field')?.toUpperCase(), value('--hh-card').toUpperCase(),
    'the light --hh-field must be the card white: one fill for every input, never the page grey');
});
