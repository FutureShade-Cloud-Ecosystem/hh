// The legacy import check is neutrally labelled: the first dealer's legacy system is named only inside
// data (the source word a dealer's configuration carries), never in a string a person reads. This scans
// every string and template literal in the app's source, after comments are removed, and holds the
// word out of them. Identifiers (wire field names, type names) are code, not literals, and stay.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const src = join(dirname(fileURLToPath(import.meta.url)), "..", "src");

function files(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? files(join(dir, e.name)) : e.name.endsWith(".ts") ? [join(dir, e.name)] : []));
}

/** The text of every string literal and template literal's static text; comments and ${} code skipped. */
function literals(code) {
  const out = [];
  let i = 0;
  const n = code.length;
  function template() {
    // i is just past the opening backtick
    let text = "";
    while (i < n && code[i] !== "`") {
      if (code[i] === "\\") {
        text += code[i + 1] ?? "";
        i += 2;
      } else if (code[i] === "$" && code[i + 1] === "{") {
        i += 2;
        out.push(text);
        text = "";
        expr();
      } else {
        text += code[i];
        i += 1;
      }
    }
    i += 1;
    out.push(text);
  }
  function expr() {
    // scan code until the matching close brace
    let depth = 1;
    while (i < n && depth > 0) scanOne((c) => {
      if (c === "{") depth += 1;
      if (c === "}") depth -= 1;
    });
  }
  function scanOne(onPunct) {
    const c = code[i];
    if (c === "/" && code[i + 1] === "/") {
      while (i < n && code[i] !== "\n") i += 1;
    } else if (c === "/" && code[i + 1] === "*") {
      i += 2;
      while (i < n && !(code[i] === "*" && code[i + 1] === "/")) i += 1;
      i += 2;
    } else if (c === '"' || c === "'") {
      let text = "";
      i += 1;
      while (i < n && code[i] !== c && code[i] !== "\n") {
        if (code[i] === "\\") {
          text += code[i + 1] ?? "";
          i += 2;
        } else {
          text += code[i];
          i += 1;
        }
      }
      i += 1;
      out.push(text);
    } else if (c === "`") {
      i += 1;
      template();
    } else {
      onPunct?.(c);
      i += 1;
    }
  }
  while (i < n) scanOne();
  return out;
}

test("no string a person reads names the first dealer's legacy system", () => {
  const offenders = [];
  for (const file of files(src)) {
    for (const text of literals(readFileSync(file, "utf8"))) {
      // A wire vocabulary word (an all capitals enum value) is data, never read by a person.
      if (/^[A-Z0-9_]+$/.test(text.trim())) continue;
      if (/bis\s*track/i.test(text)) offenders.push(`${file.slice(src.length + 1)}: ${text.trim().slice(0, 70)}`);
    }
  }
  assert.deepEqual(offenders, []);
});

test("the scanner sees a template's text and skips comments and identifiers", () => {
  const sample = "// BisTrack in a comment\nconst a = `Run the BisTrack check ${row.bistrack_cents}`;\nconst b = row.bistrack_cents; /* BisTrack */";
  const found = literals(sample);
  assert.ok(found.some((t) => /BisTrack/.test(t)));
  assert.equal(found.filter((t) => /bistrack_cents|comment/i.test(t)).length, 0);
});
