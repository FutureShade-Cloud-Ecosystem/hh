// The receivables screens' own contrast gate (the visual review's P2-1). The design system's
// matrix holds every declared ink on every flat surface, but the selected invoice row's ground is
// the row stripe (a tint resolved over the card), and no matrix measures a cascade: the review
// measured the stop coloured overdue text on that stripe at 4.01:1 in brand, under AA for 14px
// text. This gate reads the rules the app ships, resolves the stripe exactly as the sheet states
// it (the text token mixed over the card, the share read from the token itself), and holds the
// selected row's late cells and stop pills to AA in BOTH halves, following the app's own cascade:
// an override on the selected row replaces the base rule's ink, exactly as the browser would.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { contrast, palettes, resolveColor } from "../../../packages/design-system/test/_lib/sheet.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const STYLES = readFileSync(join(here, "..", "src", "ar", "ar-styles.ts"), "utf8");

const AA = 4.5;

// brand is the dark half's name in the sheet's own reader; light is light.
const PALETTES = { brand: palettes().dark, light: palettes().light };

/** Every rule block in the app's sheet as { selectors: string[], body: string }, comments stripped. */
function rules(css) {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, " ");
  const out = [];
  for (const m of stripped.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = m[1].split(",").map((s) => s.trim()).filter((s) => s !== "");
    if (selectors.length > 0) out.push({ selectors, body: m[2] });
  }
  return out;
}

function colorOf(body) {
  const m = body.match(/(?<![\w-])color\s*:\s*([^;]+);/);
  return m === null ? null : m[1].trim();
}

/** The ink a element reads: the base rule's colour, replaced by any selected row override. */
function resolvedInk(baseSelector, overrideMatch, vars, name) {
  const all = rules(STYLES);
  const base = all.find((r) => r.selectors.includes(baseSelector));
  assert.ok(base !== undefined, `the app no longer declares ${baseSelector}`);
  const baseColor = colorOf(base.body);
  assert.ok(baseColor !== null, `${baseSelector} declares no colour`);
  const override = all.find((r) => r.selectors.some((s) => s.includes('tr[aria-current="true"]') && overrideMatch(s)));
  const ink = override === undefined ? baseColor : colorOf(override.body);
  assert.ok(ink !== null, `the selected row override for ${name} declares no colour`);
  const hex = resolveColor(ink, vars);
  assert.ok(hex !== null, `${name}'s ink must resolve to a flat colour: ${ink}`);
  return { hex, overridden: override !== undefined };
}

/** The selected row's ground: the row stripe token, resolved over the card as the sheet states it. */
function selectedRowGround(vars) {
  const stripe = vars["--hh-row-alt"];
  const m = stripe.match(/^color-mix\(in srgb,\s*var\((--[a-z0-9-]+)\)\s+([\d.]+)%,\s*transparent\)$/);
  assert.ok(m !== null, `the row stripe token no longer reads as the sheet's tint: ${stripe}`);
  const ink = resolveColor(`var(${m[1]})`, vars);
  const card = resolveColor(vars["--hh-card"], vars);
  assert.ok(ink !== null && card !== null, "the stripe's mixends must resolve");
  const share = Number(m[2]) / 100;
  const channel = (i) => Math.round(Number("0x" + ink.slice(1 + i * 2, 3 + i * 2)) * share + Number("0x" + card.slice(1 + i * 2, 3 + i * 2)) * (1 - share));
  return `#${[0, 1, 2].map(channel).map((c) => c.toString(16).padStart(2, "0")).join("")}`;
}

const SELECTED = [
  { name: "the selected row's late text (td.late)", base: "td.late", override: (s) => s.endsWith("td.late") },
  { name: "the selected row's stop pill (.pill-stop)", base: ".pill-stop", override: (s) => s.includes(".pill-stop") },
];

for (const { name, base, override } of SELECTED) {
  for (const [theme, vars] of Object.entries(PALETTES)) {
    test(`the selected invoice row clears AA in ${theme}: ${name}`, () => {
      const ground = selectedRowGround(vars);
      const { hex, overridden } = resolvedInk(base, override, vars, name);
      const ratio = contrast(hex, ground);
      assert.ok(
        ratio >= AA,
        `${name} in ${theme}: ${hex}${overridden ? "" : " (the base rule; no selected row override)"} on ${ground} is ${ratio.toFixed(2)}:1, under the ${AA}:1 bar. ` +
          `Give the selected row an ink that holds on the stripe through the design system's existing tokens.`,
      );
    });
  }
}
