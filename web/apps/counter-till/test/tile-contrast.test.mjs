// The selected tile's own contrast gate (the round 16 polish, PR 267 review P3-3).
// The design system's gates hold every pair ITS sheets and kits paint, and the
// row stripe is the one tinted pair they resolve by compositing; the active
// tile's gold wash is painted by THIS app, on a color-mix fill over another
// token, so no gate anywhere measured it. In light theme the wash sat over the
// page grey and the gold ink #896400 (AA on the flat page at 4.55:1) fell under
// AA on its own fill; this gate reads the recipe the terminal actually ships,
// resolves it through both token sheets per theme, composites the mix the way
// the browser does, and holds the pair to AA in BOTH halves. A recipe it cannot
// read fails rather than passes silently: the whole point of the gate is this
// one pair, so an unreadable recipe is a regression of the gate itself.
//
// The loud theme is not measured: the till offers Auto, Light and Brand only
// (theme.ts), and the loud block lives in the ported sheet for surfaces this
// app does not ship.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { contrast, palettes, parseDeclarations, resolveColor } from "../../../packages/design-system/test/_lib/sheet.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const TERMINAL_SRC = readFileSync(join(here, "..", "src", "ported", "pos-terminal.ts"), "utf8");
const KIT_TOKENS = readFileSync(
  join(here, "..", "..", "..", "packages", "pos-kit", "src", "ported", "styles", "tokens.css"),
  "utf8",
);

// One vars dict per theme: the design system's palette (its own reader, so the
// base vocabulary is parsed the way its gates parse it) plus the ported sheet's
// --hos-* aliases. The loud block is deliberately not read.
function grabBlock(css, selector) {
  const m = css.match(new RegExp(selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\s*\\{([\\s\\S]*?)\\}"));
  assert.ok(m, `token block not found: ${selector}`);
  return m[1];
}

function kitVars(theme, designPalette) {
  const root = parseDeclarations(grabBlock(KIT_TOKENS, ":root"));
  const light = theme === "light" ? parseDeclarations(grabBlock(KIT_TOKENS, ':root[data-theme="light"]')) : {};
  return { ...designPalette, ...root, ...light };
}

const DESIGN = palettes();
const VARS = { brand: kitVars("brand", DESIGN.dark), light: kitVars("light", DESIGN.light) };

// The WCAG arithmetic's own pin, so a broken helper fails as itself before any
// pair is trusted (the design system's gate pins the same reference values).
test("the contrast helper agrees with the WCAG reference values", () => {
  assert.ok(Math.abs(contrast("#000000", "#ffffff") - 21) < 0.01);
  assert.ok(Math.abs(contrast("#ffffff", "#ffffff") - 1) < 0.01);
});

/** One rule body from the terminal's styles, found by its selector. */
function ruleBody(selector) {
  const at = TERMINAL_SRC.indexOf(selector + " {");
  assert.ok(at >= 0, `the terminal no longer declares ${selector}`);
  const body = TERMINAL_SRC.slice(at).match(/\{([^}]*)\}/);
  assert.ok(body, `${selector} has no readable body`);
  return body[1];
}

function declaration(body, prop) {
  // The lookbehind keeps `color` from matching inside `border-color`, the way
  // the tile rules order their declarations.
  const m = body.match(new RegExp("(?<![\\w-])" + prop + "\\s*:\\s*([^;]+);"));
  assert.ok(m, `no ${prop} declaration in the rule`);
  return m[1].trim();
}

/** Resolves a color-mix(in srgb, var(--x) N%, var(--y)) fill by compositing,
 * the way the browser flattens it. Returns null for anything else. */
const CHANNEL = (h, i) => parseInt(h.slice(1 + 2 * i, 3 + 2 * i), 16);
function mixFill(decl, vars) {
  const m = decl.match(/color-mix\(\s*in\s+srgb\s*,\s*var\(\s*(--[a-z0-9-]+)\s*\)\s*([\d.]+)%\s*,\s*var\(\s*(--[a-z0-9-]+)\s*\)\s*\)/);
  if (!m) return null;
  const ink = resolveColor(`var(${m[1]})`, vars);
  const base = resolveColor(`var(${m[3]})`, vars);
  assert.ok(ink && base, `the tint's ends must resolve to flat colours: ${decl}`);
  const pct = parseFloat(m[2]) / 100;
  const mixed = [0, 1, 2].map((i) => Math.round(CHANNEL(base, i) * (1 - pct) + CHANNEL(ink, i) * pct));
  return "#" + mixed.map((c) => c.toString(16).padStart(2, "0")).join("");
}

// The selected tiles and the avatar the round named: the fulfilment tiles, the
// tender tiles, the payment shape and deposit method tiles (one shared recipe),
// and the customer avatar's initials. The resting tiles paint the dimmed ink on
// the page token, a pair the design system's flat matrix already holds.
const SELECTED = [
  { name: ".fulfil button.active (the fulfilment tile)", selector: ".fulfil button.active", size: 13 },
  { name: ".paytiles button.active (the tender tile)", selector: ".paytiles button.active", size: 13 },
  { name: ".dpmethods button.active (the shape and deposit tiles)", selector: ".dpmethods button.active", size: 13 },
  { name: ".custcard .avatar (the initials)", selector: ".custcard .avatar", size: 14 },
];

for (const { name, selector, size } of SELECTED) {
  for (const theme of ["brand", "light"]) {
    test(`the selected tile clears AA in ${theme}: ${name}`, () => {
      const body = ruleBody(selector);
      const ink = resolveColor(declaration(body, "color"), VARS[theme]);
      assert.ok(ink, `the ink must resolve to a flat colour in ${theme}`);
      const fill = mixFill(declaration(body, "background"), VARS[theme]);
      assert.ok(fill, `the fill must be a readable srgb color-mix the gate can composite, or the gate is blind: ${name}`);
      const ratio = contrast(ink, fill);
      assert.ok(
        ratio >= 4.5,
        `${name} in ${theme}: ${ink} on ${fill} is ${ratio.toFixed(2)}:1, under the AA 4.5:1 bar for ${size}px text. ` +
          `Fix the pair through the design system's existing tokens; brand mode must stay as it was.`,
      );
    });
  }
}

// The fix's own contract: the active tile's wash rides the FIELD token, the card
// white in light. In the brand half the field is the charcoal set's inset tone
// (#121517, one step below the page, as the board kit draws fields; decision log
// row 1763), where the navy set had held it equal to the page. The value is
// pinned so a re-value of the dark half fails here and the brand render is
// re-looked-at, not assumed.
const BRAND_FIELD = "#121517";
test("the active tile's fill base is the charcoal field tone in brand", () => {
  const body = ruleBody(".fulfil button.active");
  const m = declaration(body, "background").match(/,\s*var\(\s*(--[a-z0-9-]+)\s*\)\s*\)$/);
  assert.ok(m, "the active tile's fill must end in a token base the gate can name");
  const base = resolveColor(`var(${m[1]})`, VARS.brand);
  assert.equal(base, BRAND_FIELD, `the fill base ${m[1]} must be the charcoal field tone in brand (${base} vs ${BRAND_FIELD})`);
});
