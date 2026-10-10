// The vendored faces (visual review round 1, typography): the ERP shows
// Outfit and the app fell back to a system sans. The four Outfit weights ship
// under public/fonts with the licence text beside them, declared by
// src/fonts.css and imported by the entry, the Public Desk's own pattern.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");

test("the vendored Outfit faces ship with their licence text beside them", () => {
  const fonts = join(appDir, "public", "fonts");
  const files = readdirSync(fonts);
  assert.equal(files.filter((name) => name.startsWith("outfit-") && name.endsWith(".woff2")).length, 4);
  assert.ok(files.includes("OFL.txt"), "OFL.txt must sit beside the font files");
  const text = readFileSync(join(fonts, "OFL.txt"), "utf8");
  assert.match(text, /^Copyright .* The Outfit Project Authors/);
  assert.match(text, /SIL OPEN FONT LICENSE Version 1\.1/);
  assert.match(text, /PERMISSION & CONDITIONS/);
  assert.match(text, /DISCLAIMER/);
});

test("fonts.css declares the four weights and the entry imports it", () => {
  const css = readFileSync(join(appDir, "src", "fonts.css"), "utf8");
  for (const weight of ["400", "500", "600", "700"]) {
    assert.match(css, new RegExp(`font-weight: ${weight};[\\s\\S]*?outfit-latin-${weight}-normal\\.woff2`));
  }
  assert.match(readFileSync(join(appDir, "src", "main.ts"), "utf8"), /import "\.\/fonts\.css";/);
});
