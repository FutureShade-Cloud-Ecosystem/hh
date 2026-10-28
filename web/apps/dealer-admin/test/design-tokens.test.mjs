// Dealer Admin's token lint (UX-117, principal item 337): every --hh- name the
// app's source names is a token the design system DEFINES, and no name rides a
// raw fallback.
//
// The defect this holds: picker-styles.ts asked for --hh-s-6 and
// --hh-shadow-pop. Neither is in the design system's tokens.css, so both
// resolved through their raw fallbacks (6px, and 0 8px 20px rgba(...)) and the
// app painted its own guess at the house's spacing grid and the house's
// elevation. A name that is not in the sheet is a name the sheet cannot hold
// its light and charcoal halves to, so the lint reads the sheet itself and
// holds the app to it.
//
// The sheet is read with its comments stripped FIRST: tokens.css says so in
// its own header, because the header names tokens next to colons and a parser
// that reads prose as a declaration swallows the real ones after it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const srcDir = join(here, "..", "src");
const tokensPath = join(here, "..", "..", "..", "packages", "design-system", "src", "tokens.css");

function sources(dir = srcDir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) out.push(...sources(path));
    else if (name.endsWith(".ts") || name.endsWith(".css")) {
      out.push([relative(srcDir, path), readFileSync(path, "utf8")]);
    }
  }
  return out;
}

/** Every --hh- name the sheet declares, in both halves (the dark :root and the
 * light [data-theme="light"] block). Comments go first. */
function definedTokens() {
  const sheet = readFileSync(tokensPath, "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  const names = new Set();
  for (const m of sheet.matchAll(/(--hh-[a-z0-9-]+)\s*:/g)) names.add(m[1]);
  return names;
}

/** The --hh- names a file names in a var(), with the line each sits on. */
function namedTokens(rel, text) {
  const out = [];
  for (const m of text.matchAll(/var\(\s*(--hh-[a-z0-9-]+)/g)) {
    out.push({ name: m[1], line: text.slice(0, m.index).split("\n").length });
  }
  return out.map((hit) => ({ ...hit, where: `${rel}:${hit.line}` }));
}

test("the design system's sheet is where the app's tokens come from", () => {
  const sheet = readFileSync(tokensPath, "utf8");
  assert.match(sheet, /--hh-s-4\s*:/, "the spacing scale is in the sheet");
  assert.match(sheet, /--hh-elev-2\s*:/, "the elevation set is in the sheet");
});

test("every --hh- token Dealer Admin names is defined in the design system's tokens.css", () => {
  const defined = definedTokens();
  const violations = [];
  for (const [rel, text] of sources()) {
    for (const hit of namedTokens(rel, text)) {
      if (!defined.has(hit.name)) {
        violations.push(`${hit.where}: ${hit.name} is not defined in tokens.css`);
      }
    }
  }
  assert.deepEqual(violations, [], `undefined design system tokens:\n${violations.join("\n")}`);
});

test("no --hh- token rides a raw fallback: the sheet defines every name the app uses", () => {
  const violations = [];
  for (const [rel, text] of sources()) {
    for (const m of text.matchAll(/var\(\s*(--hh-[a-z0-9-]+)\s*,/g)) {
      const line = text.slice(0, m.index).split("\n").length;
      violations.push(`${rel}:${line}: ${m[1]} carries a raw fallback`);
    }
  }
  assert.deepEqual(violations, [], `raw fallbacks on design system tokens:\n${violations.join("\n")}`);
});
