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

const srcDir = join(dirname(fileURLToPath(import.meta.url)), "..", "src");

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

function* sources(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* sources(path);
    else if (entry.name.endsWith(".ts")) yield path;
  }
}

function scan(pattern, label) {
  const offenders = [];
  for (const path of sources(srcDir)) {
    const rel = relative(srcDir, path);
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
