// The gates, as tests:
// 1. no token, no PIN, no password, no handle, no secret of any kind is ever
//    written by any code path in the package: a structural scan of the
//    sources for the persistent browser surfaces (localStorage,
//    document.cookie, indexedDB), a confinement proof that only the
//    throw-safe mirror touches the browser-session storage, a scan for
//    header names the package must never mint, and a runtime capture of
//    every mirrored write across the custody and lock code paths against an
//    injected clock. The structural scans are cheap tripwires for the plain
//    spellings; the run-time capture is the gate that counts (the README
//    names what the scans cannot catch).
// 2. the header providers match the core's middleware rule on X-Org: the
//    staff leg refuses a missing X-Org with 400, the tenant leg refuses a
//    present X-Org with 400.
// 3. the public surface exposes no role state, no unlock audit pair, and no
//    refresh handle.
// 4. no source or README carries an absolute workstation path.
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

const START_MS = 1_000_000_000_000;
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
  // browser-session storage behind the throw-safe mirror. This scan is a
  // tripwire for the plain spellings (the README names what it cannot
  // catch); the run-time capture below is the gate that counts.
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

test("the browser-session storage is touched only by the throw-safe mirror", () => {
  const touching = sources().filter((file) => file.text.includes("sessionStorage")).map((file) => file.name);
  assert.deepEqual(touching, ["session.ts"], "every other module goes through the mirror or injected doubles");
});

test("the only X- header the package can mint is the one it owns", () => {
  const allowed = new Set(["X-Org"]);
  for (const file of sources()) {
    const found = file.text.match(/(["'])X-[A-Za-z0-9-]+\1/g) ?? [];
    for (const literal of found) {
      const name = literal.slice(1, -1);
      assert.ok(
        allowed.has(name),
        `${file.name} carries a foreign header literal ${name}: the unlock audit pair is gone until the core can verify an unlock, and roles and anything else are the server's`,
      );
    }
  }
});

test("the header providers match the core's middleware rule on X-Org", () => {
  const middlewarePath = join(packageDir, "..", "..", "..", "core", "internal", "platform", "authz", "middleware.go");
  const go = readFileSync(middlewarePath, "utf8");
  const tenantStart = go.indexOf("func (g *Gate) serveTenant");
  const staffStart = go.indexOf("func (g *Gate) staffOrgSelection");
  assert.ok(tenantStart !== -1, "the tenant leg moved in core/internal/platform/authz/middleware.go: re-check the providers against it");
  assert.ok(staffStart !== -1, "the staff leg moved in core/internal/platform/authz/middleware.go: re-check the providers against it");
  const tenantLeg = go.slice(tenantStart, staffStart);
  assert.ok(
    tenantLeg.includes("len(r.Header.Values(HeaderXOrg)) > 0"),
    "the tenant leg no longer refuses a present X-Org: the tenant provider's no-org rule must be re-checked",
  );
  assert.ok(
    tenantLeg.includes("StatusBadRequest"),
    "the tenant leg's X-Org refusal is no longer a 400: the tenant provider's rule must be re-checked",
  );
  const staffLeg = go.slice(staffStart);
  assert.ok(
    staffLeg.includes("len(values) == 0"),
    "the staff leg no longer requires X-Org: the staff provider's rule must be re-checked",
  );
  assert.ok(
    staffLeg.includes("StatusBadRequest"),
    "the staff leg's missing-org refusal is no longer a 400: the staff provider's rule must be re-checked",
  );
});

test("the runtime code paths mirror no token, no PIN, no password, no handle", async () => {
  const recorded = new RecordedStorage();
  // The clock is injected: the fixture's token is live against it, so adopt
  // really succeeds and the capture is not vacuous (a stale fixture against
  // the real clock once let the gate pass with nothing captured).
  const sessions = new SessionStore({ mirror: new ThrowSafeMirror(SESSION_MIRROR_KEY, recorded), now: () => START_MS });
  const adopted = sessions.adopt({ token: FAKE_JWT, tokenExpiresAtMs: START_MS + 60_000, user: { ...FAKE_ACCOUNT } });
  assert.equal(adopted, true, "the fixture session must adopt for the capture to be real");
  const credentials = new FakeCredentialProvider();
  const lock = new DeviceLock({
    credentials,
    sessions,
    mirror: new ThrowSafeMirror(LOCK_MIRROR_KEY, recorded),
    now: () => START_MS,
    idleRelockMs: 3_600_000,
  });
  assert.equal((await lock.engage()).ok, true);
  credentials.present(FAKE_PIN);
  assert.equal((await lock.unlock()).ok, true);

  const allowedKeys = new Set([SESSION_MIRROR_KEY, LOCK_MIRROR_KEY, "hh-auth.storage-probe"]);
  const sessionWrites = recorded.writes.filter((w) => w.key === SESSION_MIRROR_KEY);
  const lockWrites = recorded.writes.filter((w) => w.key === LOCK_MIRROR_KEY);
  assert.ok(sessionWrites.length >= 1, "the session path wrote (the capture is not vacuous)");
  assert.ok(lockWrites.length >= 1, "the lock path wrote (the capture is not vacuous)");
  for (const write of recorded.writes) {
    assert.ok(allowedKeys.has(write.key), `an unexpected key was written: ${write.key}`);
    assert.equal(write.value.includes(FAKE_JWT), false, `the token reached storage (key ${write.key})`);
    assert.equal(write.value.includes(FAKE_PIN), false, `the PIN reached storage (key ${write.key})`);
    assert.equal(write.value.includes(FAKE_PASSWORD), false, `the password reached storage (key ${write.key})`);
    if (write.key === SESSION_MIRROR_KEY) {
      const payload = JSON.parse(write.value);
      assert.deepEqual(
        Object.keys(payload).sort(),
        ["email", "name", "userId", "v"],
        "the session mirror carries the display profile and nothing else",
      );
    }
    if (write.key === LOCK_MIRROR_KEY) {
      const payload = JSON.parse(write.value);
      assert.deepEqual(
        Object.keys(payload).sort(),
        ["credentialId", "engagedAtMs", "userId", "v"],
        "the lock mirror carries the bound user id and the credential id, nothing else",
      );
    }
  }
});

test("the public surface exposes no role state, no unlock audit pair, and no refresh handle", async () => {
  const auth = await import("../src/index.ts");
  const keys = Object.keys(auth);
  const offenders = (pattern) => keys.filter((key) => pattern.test(key));
  assert.deepEqual(offenders(/role/i), [], "no exported name carries role state: roles are the server's");
  assert.deepEqual(offenders(/handle/i), [], "no exported name carries a refresh handle: the package holds nothing to trade");
  assert.deepEqual(offenders(/unlocked|audit/i), [], "no exported name carries the unlock audit pair: gone until the core can verify an unlock");
  assert.equal(typeof auth.authHeaderProvider, "function", "the wiring is on the surface");
  assert.equal(typeof auth.SessionStore, "function", "the custody is on the surface");
  assert.equal(typeof auth.DeviceLock, "function", "the lock is on the surface");
  assert.equal(auth.FAKE_JWT, undefined, "the fake values stay behind the test entry point");
});

test("no source or README carries an absolute workstation path", () => {
  const files = [...sources(), { name: "README.md", text: readFileSync(join(packageDir, "README.md"), "utf8") }];
  for (const file of files) {
    assert.equal(file.text.includes("/home/"), false, `${file.name} carries a workstation path`);
    assert.equal(file.text.includes("/Users/"), false, `${file.name} carries a workstation path`);
  }
});
