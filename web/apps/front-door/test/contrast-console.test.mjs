// The console mode's text inks, held to AA in both halves of the sheet, read
// the way the coming surfaces' gate reads the app's own rules: the rule's
// colour declaration resolved through the design system's palette reader,
// against the ground the text sits on (the card for the sign in card and the
// role entry, the page for the header band).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { contrast, palettes, resolveColor } from "../../../packages/design-system/test/_lib/sheet.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const CSS = readFileSync(join(here, "..", "src", "console-styles.ts"), "utf8");
const PALETTES = { brand: palettes().dark, light: palettes().light };
const AA = 4.5;

function inkOf(selector) {
  const at = CSS.indexOf(selector + " {");
  assert.ok(at >= 0, `console-styles.ts no longer declares ${selector}`);
  const body = CSS.slice(at).match(/\{([^}]*)\}/)[1];
  const m = body.match(/(?<![\w-])color\s*:\s*([^;]+);/);
  assert.ok(m, `${selector} declares no colour`);
  return m[1].trim();
}

const PAIRS = [
  { selector: ".dealer-sub", ground: "var(--hh-card)" },
  { selector: ".dealer-sub", ground: "var(--hh-page)" },
  { selector: ".signin-lead", ground: "var(--hh-card)" },
  { selector: ".status", ground: "var(--hh-card)" },
  { selector: ".status.error", ground: "var(--hh-card)" },
  { selector: ".methods-note", ground: "var(--hh-card)" },
  { selector: ".or", ground: "var(--hh-card)" },
  { selector: ".entry-card p", ground: "var(--hh-card)" },
  { selector: ".person .person-role", ground: "var(--hh-page)" },
  { selector: ".config-error p", ground: "var(--hh-page)" },
  { selector: ".dealer", ground: "var(--hh-page)" },
  { selector: ".apps-menu summary", ground: "var(--hh-page)" },
  { selector: ".apps-list button", ground: "var(--hh-card)" },
  { selector: ".signin-footer", ground: "var(--hh-page)" },
  { selector: ".quiet-control", ground: "var(--hh-page)" },
];

for (const { selector, ground } of PAIRS) {
  for (const [theme, vars] of Object.entries(PALETTES)) {
    test(`console text clears AA in ${theme}: ${selector} on ${ground}`, () => {
      const fg = resolveColor(inkOf(selector), vars);
      const bg = resolveColor(ground, vars);
      assert.ok(fg && bg, "both colours resolve");
      const ratio = contrast(fg, bg);
      assert.ok(ratio >= AA, `${selector} in ${theme}: ${fg} on ${bg} is ${ratio.toFixed(2)}:1, under ${AA}:1`);
    });
  }
}
