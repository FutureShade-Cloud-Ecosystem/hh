// UX-121 round 2: the source words live in one shared module, and the two slices that read a
// source read them there. The ledger's register names the source of a journal and the statements'
// rows name the source of a line: one source, one word, or the desk has to be read twice.
//
// The legs read the app's own sources, so a second copy of the map is caught wherever it is
// planted, and the shared module is imported and asked what it says.
//
// Run it with the app's own loader:
//   node --import ../../packages/api-client/src/_ts-js-loader.mjs --test test/ux121-shared-words.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const src = join(dirname(fileURLToPath(import.meta.url)), "..", "src");

/** Every source file of the app, the shared module among them. */
function sources(dir = src) {
  const out = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...sources(path));
    else if (entry.name.endsWith(".ts")) out.push(path);
  }
  return out;
}

const text = (path) => readFileSync(path, "utf8");

test("the source words live in one shared module, and both slices read them there", () => {
  const shared = join(src, "shared", "source-words.ts");
  assert.ok(existsSync(shared), "the shared source words module is missing");
  assert.match(text(shared), /export function sourceWord\(/, "the shared module does not carry the source word helper");

  for (const slice of ["gl/gl-views.ts", "finstmt/finstmt-views.ts"]) {
    const path = join(src, slice);
    assert.match(
      text(path),
      /from "\.\.\/shared\/source-words\.js"/,
      `${slice} does not read the source words from the shared module`,
    );
  }

  assert.doesNotMatch(
    text(join(src, "finstmt", "finstmt-logic.ts")),
    /SOURCE_WORD/,
    "the statements slice still carries its own source word map",
  );
});

test("no slice carries a second copy of the source word map", () => {
  const carriers = sources().filter((path) => /SOURCE_WORD\s*[:=]/.test(text(path)));
  assert.deepEqual(
    carriers.map((path) => path.slice(src.length + 1)),
    ["shared/source-words.ts"],
    "the source word map is carried by more than one file",
  );
});

test("the shared source words say what the desk says", async () => {
  const { sourceWord } = await import("../src/shared/source-words.ts");
  assert.equal(sourceWord("MANUAL"), "Manual");
  assert.equal(sourceWord("INVOICE"), "Invoice");
  assert.equal(sourceWord("PAYMENT"), "Payment");
  assert.equal(sourceWord("FULFILLMENT"), "Fulfillment");
  assert.equal(sourceWord("PO_RECEIPT"), "Purchase receipt");
  assert.equal(sourceWord("ADJUSTMENT"), "Adjustment");
  assert.equal(sourceWord("CLOSING"), "Year end closing");
});
