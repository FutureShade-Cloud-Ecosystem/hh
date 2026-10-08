// The coming surfaces' own contrast gate (round 2, FROM-PRINCIPAL 9). The design system's
// matrix holds every text ink it declares on every flat surface, and its disabled pair
// (ink on fill) clears AA by its own pin; what no gate measured is this app's lighter
// disabled treatment, the coming role names and the hub's muted module names painted with
// the disabled INK on a transparent ground over the CARD. In brand that pair reads 4.30:1
// (#8499AD on #173550), under AA for text. This gate reads the rules the app ships,
// resolves them through the design system's own palette reader per theme, and holds every
// TEXT pair to AA and the icon marks to the non-text 3:1 bar, in BOTH halves. The coming
// name buttons are disabled controls, which WCAG exempts; the sheet's own ruling is
// asserted anyway ("both pairs clear AA even though WCAG exempts disabled controls: an
// honest grey is free"), and the hub's muted module names are not disabled controls at
// all: they open the polite coming-soon page.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { contrast, palettes, resolveColor } from "../../../packages/design-system/test/_lib/sheet.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const APP = readFileSync(join(here, "..", "src", "app-root.ts"), "utf8");

const AA = 4.5;
const NON_TEXT = 3.0;

// brand is the dark half's name in the sheet's own reader; light is light.
const PALETTES = { brand: palettes().dark, light: palettes().light };

test("the contrast helper agrees with the WCAG reference values", () => {
  assert.ok(Math.abs(contrast("#000000", "#ffffff") - 21) < 0.01);
  assert.ok(Math.abs(contrast("#ffffff", "#ffffff") - 1) < 0.01);
});

/** One rule body from the app's styles, found by its selector. */
function ruleBody(selector) {
  const at = APP.indexOf(selector + " {");
  assert.ok(at >= 0, `the app no longer declares ${selector}`);
  const body = APP.slice(at).match(/\{([^}]*)\}/);
  assert.ok(body, `${selector} has no readable body`);
  return body[1];
}

function declaration(body, prop) {
  // The lookbehind keeps `color` from matching inside `border-color`.
  const m = body.match(new RegExp("(?<![\\w-])" + prop + "\\s*:\\s*([^;]+);"));
  assert.ok(m, `no ${prop} declaration in the rule`);
  return m[1].trim();
}

/** The ground a rule's text sits on: the rule's own background when it paints one, else
 * the named container's (the coming name button paints transparent over its card). */
function groundOf(rule, containerSelector, vars) {
  const own = rule.match(new RegExp("(?<![\\w-])background\\s*:\\s*([^;]+);"));
  const decl = own && !/transparent|none/.test(own[1]) ? own[1].trim() : declaration(ruleBody(containerSelector), "background");
  const hex = resolveColor(decl, vars);
  assert.ok(hex, `the ground must resolve to a flat colour: ${decl}`);
  return hex;
}

// The pairs the round named, read as the app paints them: the coming role names and the
// hub's muted module names are TEXT (AA), the icons beside them are shapes (3:1).
const PAIRS = [
  { name: ".coming-role button (the coming role name)", ink: ".coming-role button", container: ".coming-card", bar: AA },
  { name: ".tile.muted .module (the hub's muted module name)", ink: ".tile.muted .module", container: ".tile", bar: AA },
  { name: ".tile.muted .mark (the muted tile's icon)", ink: ".tile.muted .mark", container: ".tile", bar: NON_TEXT },
];

for (const { name, ink, container, bar } of PAIRS) {
  for (const [theme, vars] of Object.entries(PALETTES)) {
    test(`the coming surfaces clear their bar in ${theme}: ${name}`, () => {
      const fg = resolveColor(declaration(ruleBody(ink), "color"), vars);
      assert.ok(fg, `the ink must resolve in ${theme}`);
      const bg = groundOf(ruleBody(ink), container, vars);
      const ratio = contrast(fg, bg);
      assert.ok(
        ratio >= bar,
        `${name} in ${theme}: ${fg} on ${bg} is ${ratio.toFixed(2)}:1, under the ${bar}:1 bar. Fix the pair through ` +
          `the design system's existing tokens; the disabled ink belongs on the disabled fill, and text on the card ` +
          `needs an ink the matrix holds there.`,
      );
    });
  }
}
