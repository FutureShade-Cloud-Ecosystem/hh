// The gates, as tests:
// 1. no token or secret is ever written by any code path in the package: a
//    structural scan of the sources for the persistent browser surfaces
//    (localStorage, document.cookie, indexedDB), a confinement proof that
//    only the throw-safe mirror touches sessionStorage, a scan for header
//    names the package must never mint, and a runtime capture of every
//    mirrored write across the custody and lock code paths;
// 2. the public surface exposes no role state at all (the HH Pro rule,
//    cited in src/client.ts).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  DeviceLock,
  SessionStore,
  ThrowSafeMirror,
  LOCK_MIRROR_KEY,
  SESSION_MIRROR_KEY,
} from "../src/index.ts";
import {
  FAKE_ACCOUNT,
  FAKE_JWT,
  FAKE_PASSWORD,
  FAKE_PIN,
  FakeCredentialProvider,
  RecordedStorage,
} from "../src/testing.ts";

const packageDir = dirname(dirname(fileURLToPath(import.meta.url)));
const srcDir = join(packageDir, "src");

function sources() {
  return readdirSync(srcDir)
    .filter((name) => name.endsWith(".ts") || name.endsWith(".mjs"))
    .map((name) => ({ name, text: readFileSync(join(srcDir, name), "utf8") }));
}

test("no persistent browser storage surface is reachable from the package sources", () => {
  // The scan targets the surfaces' use paths, not the words: any member
  // access or index of localStorage or indexedDB, and any document.cookie
  // mention, fails. The only storage the package may touch is the
  // sessionStorage behind the throw-safe mirror.
  const forbidden = [/\b(localStorage|indexedDB)\s*[.[]\s*[A-Za-z_$'"[]/, /document\.cookie/];
  for (const file of sources()) {
    for (const pattern of forbidden) {
      assert.equal(
        pattern.test(file.text),
        false,
        `${file.name} touches a persistent browser storage surface (matched ${pattern}): the package writes no persistent surface`,
      );
    }
  }
});

test("sessionStorage is touched only by the throw-safe mirror", () => {
  const touching = sources().filter((file) => file.text.includes("sessionStorage")).map((file) => file.name);
  assert.deepEqual(touching, ["session.ts"], "every other module goes through the mirror or injected doubles");
});

test("the only X- headers the package can mint are the three it owns", () => {
  const allowed = new Set(["X-Org", "X-Unlocked-By", "X-Unlocked-At"]);
  for (const file of sources()) {
    const found = file.text.match(/(["'])X-[A-Za-z0-9-]+\1/g) ?? [];
    for (const literal of found) {
      const name = literal.slice(1, -1);
      assert.ok(
        allowed.has(name),
        `${file.name} carries a foreign header literal ${name}: roles and anything else are the server's`,
      );
    }
  }
});

test("the runtime code paths mirror no token, no PIN, no password", async () => {
  const recorded = new RecordedStorage();
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded) });
  sessions.adopt({
    token: FAKE_JWT,
    tokenExpiresAtMs: 1_000_000_000_000 + 60_000,
    user: { ...FAKE_ACCOUNT },
    refreshHandle: { handle: "fake-refresh-handle", expiresAtMs: 1_000_000_000_000 + 3_600_000 },
  });
  const credentials = new FakeCredentialProvider();
  const lock = new DeviceLock({ credentials, mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded) });
  assert.equal((await lock.engage(FAKE_ACCOUNT.userId)).ok, true);
  credentials.present(FAKE_PIN);
  assert.equal((await lock.unlock()).ok, true);

  const allowedKeys = new Set([SESSION_MIRROR_KEY, LOCK_MIRROR_KEY, "hh-auth.storage-probe"]);
  assert.ok(recorded.writes.length >= 2, "both code paths wrote (the capture is not vacuous)");
  for (const write of recorded.writes) {
    assert.ok(allowedKeys.has(write.key), `an unexpected key was written: ${write.key}`);
    assert.equal(write.value.includes(FAKE_JWT), false, `the token reached storage (key ${write.key})`);
    assert.equal(write.value.includes(FAKE_PIN), false, `the PIN reached storage (key ${write.key})`);
    assert.equal(write.value.includes(FAKE_PASSWORD), false, `the password reached storage (key ${write.key})`);
  }
});

test("the public surface exposes no role state at all", async () => {
  const auth = await import("../src/index.ts");
  const keys = Object.keys(auth);
  const offenders = keys.filter((key) => /role/i.test(key));
  assert.deepEqual(offenders, [], "no exported name carries role state: roles are the server's");
  assert.equal(typeof auth.authHeaderProvider, "function", "the wiring is on the surface");
  assert.equal(typeof auth.SessionStore, "function", "the custody is on the surface");
  assert.equal(typeof auth.DeviceLock, "function", "the lock is on the surface");
  assert.equal(auth.FAKE_JWT, undefined, "the fake values stay behind the test entry point");
});
