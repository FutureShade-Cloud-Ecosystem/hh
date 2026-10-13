// The card sweep: no card number field exists anywhere in Pro Pay's source. The app has no text input at all (the card
// field is the payment processor's, in a frame of its own), no markup that names a card field, and no code that asks
// a frame for its contents or opens a processor's page by a scheme other than http or https.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const srcDir = join(dirname(fileURLToPath(import.meta.url)), "..", "src");

function* sources(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* sources(path);
    else if (entry.name.endsWith(".ts")) yield path;
  }
}

// What would make a card field, or a read of one, in markup or code.
const BANNED = [
  [/<input\b/i, "an input element"],
  [/<textarea\b/i, "a textarea element"],
  [/contenteditable/i, "an editable region"],
  [/autocomplete\s*=\s*["']?cc-/i, "a cc- autocomplete token"],
  [/\bcc-(number|csc|exp|name)\b/i, "a cc- field name"],
  [/card[-_]?number|cardnumber|\bpan\b|\bcvv\b|\bcvc\b|\bcsc\b/i, "a card field name"],
  [/contentDocument|contentWindow\.document/, "a read into a frame's document"],
  [/allow-same-origin/, "a frame flag that would open the frame to the app"],
  [/inputmode\s*=\s*["']?numeric/i, "a numeric entry mode"],
];

function offences(files) {
  const found = [];
  for (const [rel, text] of files) {
    text.split("\n").forEach((line, index) => {
      for (const [pattern, what] of BANNED) if (pattern.test(line)) found.push(`${rel}:${index + 1}: ${what}: ${line.trim()}`);
    });
  }
  return found;
}

test("the scanner bites: each planted offence is found", () => {
  const planted = [
    '<input type="text" name="x">',
    "<textarea></textarea>",
    '<div contenteditable="true"></div>',
    'autocomplete="cc-number"',
    "const cardNumber = 1;",
    "const cvv = 1;",
    "frame.contentDocument.body",
    'sandbox="allow-scripts allow-same-origin"',
    'inputmode="numeric"',
  ];
  for (const line of planted) assert.equal(offences([["planted.ts", line]]).length >= 1, true, line);
  assert.deepEqual(offences([["clean.ts", 'html`<button type="button" data-action="pay">Pay</button>`']]), []);
});

test("no source of Pro Pay carries a card field, or a way to read one", () => {
  const files = [...sources(srcDir)].map((path) => [relative(srcDir, path), readFileSync(path, "utf8")]);
  assert.ok(files.length >= 6, "the scan saw the app's sources");
  assert.deepEqual(offences(files), []);
});
