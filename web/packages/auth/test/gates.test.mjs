// The gates, as tests:
// 1. no token, no PIN, no password, no handle, no secret of any kind is ever
//    written by any code path in the package: a structural scan of the
//    sources for the persistent browser surfaces (localStorage,
//    document.cookie, indexedDB), a confinement proof that only the
//    throw-safe mirror touches the browser-session storage, a scan for
//    header names the package must never mint, and a runtime capture of
//    every write across the sign-in, custody, and lock code paths against an
//    injected clock, with the platform's storage globals swapped for
//    recording fakes so the production resolution path is the one captured.
//    The structural scans are cheap tripwires for the plain spellings; the
//    run-time capture is the gate that counts (the README names what the
//    scans cannot catch).
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
  AuthController,
  DeviceLock,
  SessionStore,
  LOCK_MIRROR_KEY,
  SESSION_MIRROR_KEY,
} from "../src/index.ts";
import {
  FAKE_ACCOUNT,
  FAKE_JWT,
  FAKE_PASSWORD,
  FAKE_PIN,
  FakeAccountSurface,
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

test("the api-client is imported through its exports map, never by a relative path into its src", () => {
  // The scan matches import specifiers (static and dynamic), not prose: a
  // comment naming an api-client source path documents, it does not import.
  const offenders = sources().filter((file) =>
    /(?:from|import)\s*\(?\s*["'][^"']*api-client\/src/.test(file.text),
  );
  assert.deepEqual(
    offenders.map((file) => file.name),
    [],
    "a relative import into the api-client's src walks past its exports map: import from @hh/api-client",
  );
  const importers = sources().filter((file) => file.text.includes(`from "@hh/api-client"`));
  assert.deepEqual(
    importers.map((file) => file.name).sort(),
    ["client.ts", "lock.ts"].sort(),
    "the Result convention and the HeaderProvider type come from the workspace sibling by its bare name",
  );
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

test("the runtime code paths write no token, no PIN, no password, no handle into any storage", async () => {
  // The platform's storage globals are swapped for recording fakes for the
  // length of the test, and the flows below run through DEFAULT-CONSTRUCTED
  // stores, the production resolution path. A write to the real global
  // session storage reached through the swapped global at call time,
  // including one through a computed property name evaluated when the write
  // happens, lands in a recording fake and is seen here; an injected
  // recording storage alone once missed exactly that. An alias of a storage
  // object taken BEFORE the swap holds the real accessor and a write
  // through it is NOT seen: that limit is recorded in the README.
  // document, caches and indexedDB are swapped and restored here too, so
  // the stubs outlive this test no more than the storages do.
  const sessionGlobal = new RecordedStorage();
  const localGlobal = new RecordedStorage();
  const cookieWrites = [];
  const cacheWrites = [];
  const dbWrites = [];
  // Every surface this test swaps is restored in the finally: the recording
  // fakes and stubs must outlive the test no more than the storages do, or
  // later tests in this file would run against this test's recordings.
  const swappedNames = ["sessionStorage", "localStorage", "document", "caches", "indexedDB"];
  const saved = swappedNames.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]);
  try {
    Object.defineProperty(globalThis, "sessionStorage", { value: sessionGlobal, configurable: true, writable: true });
    Object.defineProperty(globalThis, "localStorage", { value: localGlobal, configurable: true, writable: true });
    Object.defineProperty(globalThis, "document", {
      value: { set cookie(value) { cookieWrites.push(String(value)); } },
      configurable: true,
    });
    Object.defineProperty(globalThis, "caches", {
      value: {
        open: async () => ({ put: async (request, response) => cacheWrites.push({ request, response }) }),
        match: async () => undefined,
        delete: async () => false,
      },
      configurable: true,
    });
    Object.defineProperty(globalThis, "indexedDB", {
      value: { open: (name) => dbWrites.push(name) },
      configurable: true,
    });

    // The clock is injected: the fixture's token is live against it, so the
    // sign-in really adopts and the capture is not vacuous (a stale fixture
    // against the real clock once let the gate pass with nothing captured).
    const now = () => START_MS;
    const account = new FakeAccountSurface();
    account.setClock(now);
    const sessions = new SessionStore({ now }); // the mirror resolves to the global fake
    assert.equal(sessions.degraded, false, "the swapped-in global storage must probe as working");
    const controller = new AuthController({ account, sessions });
    const signedIn = await controller.signInWithPassword(FAKE_ACCOUNT.email, FAKE_PASSWORD);
    assert.equal(signedIn.ok, true, `the fixture sign-in must succeed for the capture to be real: ${signedIn.ok ? "" : signedIn.error}`);

    const credentials = new FakeCredentialProvider();
    const lock = new DeviceLock({ credentials, sessions, now, openWindowMs: 3_600_000 }); // the mirror resolves to the global fake
    assert.equal((await lock.engage()).ok, true);
    credentials.present(FAKE_PIN);
    assert.equal((await lock.unlock()).ok, true);

    const allowedKeys = new Set([SESSION_MIRROR_KEY, LOCK_MIRROR_KEY, "hh-auth.storage-probe"]);
    for (const [surface, recording] of [["sessionStorage", sessionGlobal], ["localStorage", localGlobal]]) {
      for (const write of recording.writes) {
        assert.ok(allowedKeys.has(write.key), `an unexpected key reached ${surface}: ${write.key}`);
        assert.equal(write.value.includes(FAKE_JWT), false, `the token reached ${surface} (key ${write.key})`);
        assert.equal(write.value.includes(FAKE_PIN), false, `the PIN reached ${surface} (key ${write.key})`);
        assert.equal(write.value.includes(FAKE_PASSWORD), false, `the password reached ${surface} (key ${write.key})`);
        if (write.key === SESSION_MIRROR_KEY) {
          const payload = JSON.parse(write.value);
          assert.deepEqual(
            Object.keys(payload).sort(),
            ["email", "name", "userId", "v"],
            `the session mirror carries the display profile and nothing else (${surface})`,
          );
        }
        if (write.key === LOCK_MIRROR_KEY) {
          const payload = JSON.parse(write.value);
          assert.deepEqual(
            Object.keys(payload).sort(),
            ["credentialId", "engagedAtMs", "userId", "v"],
            `the lock mirror carries the bound user id and the credential id, nothing else (${surface})`,
          );
        }
      }
    }
    assert.ok(
      sessionGlobal.writes.some((w) => w.key === SESSION_MIRROR_KEY),
      "the session path wrote the swapped-in global (the capture is not vacuous)",
    );
    assert.ok(
      sessionGlobal.writes.some((w) => w.key === LOCK_MIRROR_KEY),
      "the lock path wrote the swapped-in global (the capture is not vacuous)",
    );
    assert.equal(localGlobal.writes.length, 0, "localStorage recorded nothing: no persistent write exists");
    assert.deepEqual(cookieWrites, [], "document.cookie recorded nothing");
    assert.deepEqual(cacheWrites, [], "the Cache API recorded nothing");
    assert.deepEqual(dbWrites, [], "indexedDB recorded nothing");
  } finally {
    for (const [name, descriptor] of saved) {
      if (descriptor === undefined) delete globalThis[name];
      else Object.defineProperty(globalThis, name, descriptor);
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
