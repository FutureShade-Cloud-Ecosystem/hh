// The page's credential scan, as a committed gate. This page runs before any
// authentication: the token in the link is the only credential it will ever
// hold, so no source line may carry a place a credential could rest in or
// surface from, and no source line may reach for the auth package or name a
// credential header at all. The patterns are stated here so the gate can be
// read and argued with; a match fails naming the file, the line and the
// words, and nothing in src may match.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const srcDir = join(appDir, "src");

// A place a credential could rest in or surface from: no storage accessor, no
// printed word, no cookie, no channel, no beacon, no URL or history write.
const SINK =
  /localStorage|sessionStorage|indexedDB|document\.cookie|console\.|setItem|postMessage|BroadcastChannel|sendBeacon|window\.name|history\.(push|replace)State|location\.(hash|search|href) *=/;

// The page ships none of the session machinery: no auth package import, no
// session store, no header provider, and no credential header named anywhere
// (matched without case, so a comment cannot carry what the code may not).
const AUTH_PACKAGE = /@hardscapehouse\/auth/;
const SESSION_MACHINERY = /SessionStore|authHeaderProvider|AuthController/;
const CREDENTIAL_HEADER = /authorization|bearer|x-org/i;

// Every file the browser is served from source: the TypeScript, the
// stylesheets, and the entry page itself (its inline script runs before any
// module and is as good a place for a credential sink as any module).
function* sources(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* sources(path);
    else if (entry.name.endsWith(".ts") || entry.name.endsWith(".css")) yield path;
  }
  if (dir === srcDir) yield join(appDir, "index.html");
}

function scan(pattern, label) {
  const offenders = [];
  for (const path of sources(srcDir)) {
    const rel = relative(appDir, path);
    const lines = readFileSync(path, "utf8").split("\n");
    lines.forEach((line, index) => {
      if (pattern.test(line)) offenders.push(`${rel}:${index + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(offenders, [], `${label} matched in the files listed above`);
}

test("no source of the page carries a place a credential could rest in or surface from", () => {
  scan(SINK, "the storage and surface sink scan");
});

test("no source of the page reaches for the auth package or the session machinery", () => {
  scan(AUTH_PACKAGE, "the auth package scan");
  scan(SESSION_MACHINERY, "the session machinery scan");
});

test("no source of the page names a credential header", () => {
  scan(CREDENTIAL_HEADER, "the credential header scan");
});

test("the scan covers the entry page and every stylesheet, not the TypeScript alone", () => {
  const scanned = [...sources(srcDir)].map((path) => relative(appDir, path));
  assert.ok(scanned.includes("index.html"), "index.html (its inline script) must be scanned");
  assert.ok(scanned.includes(join("src", "fonts.css")), "src/fonts.css must be scanned");
  assert.ok(scanned.some((path) => path.endsWith(".ts")), "the TypeScript must still be scanned");
});

test("the entry page tells the browser to send no referrer", () => {
  // The token rides the address bar; a referrer would carry it to whatever the
  // page ever loads or links, so the page sends none at all.
  const page = readFileSync(join(appDir, "index.html"), "utf8");
  assert.match(page, /<meta\s+name="referrer"\s+content="no-referrer"\s*\/?>/);
});

test("the page's styling comes from the design system's tokens and its button primitive", () => {
  const source = readFileSync(join(srcDir, "quote-public.ts"), "utf8");
  assert.deepEqual(source.match(/border-radius:\s*\d+px/g) ?? [], [], "radii are tokens, not raw px");
  assert.deepEqual(
    source.match(/(?:padding|margin|gap)[a-z-]*:[^;]*\b\d+px/g) ?? [],
    [],
    "paddings, margins and gaps ride the --hh-s-* grid, not raw px",
  );
  assert.match(source, /import \{[^}]*\bbutton\b[^}]*\} from "@hardscapehouse\/design-system\/src\/primitives\.js"/);
  assert.match(source, /\bbutton\(\{/);
  assert.doesNotMatch(source, /<button\b/, "no hand-rolled button element");
  assert.doesNotMatch(source, /var\(--hh-accent\)/, "the fill is the primitive's --brand-fill, not a raw accent");
});
