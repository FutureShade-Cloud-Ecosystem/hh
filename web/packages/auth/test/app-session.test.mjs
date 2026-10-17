// The pieces a micro app shares with the front door to recover its own
// session: the persona memo (the one thing that crosses pages, a role id and
// never a bearer), the identity tap (moved here from the front door) and the
// persona route call (moved beside the exchange's REST adapter).
import { test } from "node:test";
import assert from "node:assert/strict";
import { PERSONA_MIRROR_KEY, PersonaMemo, tapIdentity, SESSION_MIRROR_KEY } from "../src/index.ts";
import { enterPersona } from "../src/appwrite-adapter.ts";
import { RecordedStorage, ThrowingStorage, FAKE_JWT } from "../src/testing.ts";

const PROBE_KEY = "hh-auth.storage-probe";
const GOOD = {
  token: "fake-core-token",
  expiresAtMs: 2_000_000_000_000,
  user: { user_id: "persona:driver", email: "driver@hh-demo.invalid", display_name: "Driver", role_code: "driver", role_name: "Driver", tier: 3, scope: "BRANCH" },
};
const answer = (status, body) => async () => new Response(body === undefined ? null : JSON.stringify(body), { status });

test("the persona memo's key is closed and distinct from the session mirror's", () => {
  assert.equal(PERSONA_MIRROR_KEY, "hh-auth.persona.v1");
  assert.notEqual(PERSONA_MIRROR_KEY, SESSION_MIRROR_KEY);
});

test("the memo stores exactly one persona id and nothing else", () => {
  const storage = new RecordedStorage();
  const memo = new PersonaMemo(storage);
  assert.equal(memo.read(), null);
  assert.equal(memo.write("sales-manager"), true);
  const written = storage.writes.filter((w) => w.key !== PROBE_KEY);
  assert.deepEqual(written.map((w) => w.key), [PERSONA_MIRROR_KEY]);
  assert.equal(JSON.parse(written[0].value), "sales-manager");
  assert.equal(memo.read(), "sales-manager");
  // a second page's memo over the same storage reads it back
  assert.equal(new PersonaMemo(storage).read(), "sales-manager");
  memo.clear();
  assert.equal(memo.read(), null);
  assert.equal(storage.size, 0);
});

test("the memo refuses anything that is not a persona id, and drops a corrupt entry", () => {
  const storage = new RecordedStorage();
  const memo = new PersonaMemo(storage);
  for (const bad of ["", "Sales", "a b", "x".repeat(64), FAKE_JWT, "eyJhbGciOi.payload.sig", "a/b", 7, null, undefined, { id: "driver" }]) {
    assert.equal(memo.write(bad), false, String(bad));
  }
  assert.equal(storage.writes.filter((w) => w.key !== PROBE_KEY).length, 0);
  storage.setItem(PERSONA_MIRROR_KEY, JSON.stringify({ id: "driver", token: "x" }));
  assert.equal(memo.read(), null);
  assert.equal(storage.size, 0, "an unusable entry is dropped from storage");
  storage.setItem(PERSONA_MIRROR_KEY, "not json");
  assert.equal(memo.read(), null);
  assert.equal(storage.size, 0);
});

test("the memo never throws on blocked storage and then lives in memory only", () => {
  const memo = new PersonaMemo(new ThrowingStorage());
  assert.equal(memo.write("driver"), true);
  assert.equal(memo.read(), "driver");
  memo.clear();
  assert.equal(memo.read(), null);
});

test("a persona entry posts the persona id, with the caller's bearer only when there is one", async () => {
  let seen;
  const fetchFn = async (url, init) => {
    seen = { url: String(url), init };
    return new Response(JSON.stringify(GOOD), { status: 200 });
  };
  const open = await enterPersona(fetchFn, "", "gable", "driver", null);
  assert.equal(open.ok, true);
  assert.equal(seen.url, "/api/v1/auth/persona");
  assert.deepEqual(JSON.parse(seen.init.body), { persona: "driver" });
  assert.equal("Authorization" in seen.init.headers, false);
  assert.equal(seen.init.credentials, "omit");
  assert.equal(seen.init.headers["X-Org"], "gable");
  await enterPersona(fetchFn, "https://core.example.test/", "gable", "driver", "fake-bearer");
  assert.equal(seen.url, "https://core.example.test/api/v1/auth/persona");
  assert.equal(seen.init.headers["Authorization"], "Bearer fake-bearer");
});

test("persona refusals map to a reason by status", async () => {
  for (const [status, reason] of [[401, "unauthorized"], [403, "forbidden"], [429, "rate_limited"], [400, "bad_request"], [503, "unavailable"]]) {
    assert.deepEqual(await enterPersona(answer(status), "", "gable", "driver", null), { ok: false, reason }, String(status));
  }
  assert.deepEqual(await enterPersona(async () => { throw new TypeError("down"); }, "", "gable", "driver", null), { ok: false, reason: "offline" });
});

test("the identity tap remembers the last accepted exchange user block and forwards everything", async () => {
  const calls = [];
  const surface = {
    createEmailPasswordSession: async (...a) => { calls.push(["create", ...a]); return { userId: "u", email: "e", name: "n" }; },
    createOAuth2Session: async (...a) => { calls.push(["oauth", ...a]); },
    getSession: async () => ({ userId: "u", email: "e", name: "n" }),
    exchange: async (org) => { calls.push(["exchange", org]); return { ok: true, answer: GOOD }; },
    deleteSession: async () => { calls.push(["delete"]); },
  };
  const tap = tapIdentity(surface);
  assert.equal(tap.identity(), null);
  await tap.surface.createEmailPasswordSession("a@b.invalid", "pw");
  await tap.surface.createOAuth2Session("google", "https://x.test/");
  await tap.surface.exchange("gable");
  await tap.surface.deleteSession();
  assert.deepEqual(calls.map((c) => c[0]), ["create", "oauth", "exchange", "delete"]);
  assert.equal(tap.identity().role_code, "driver");
  tap.forget();
  assert.equal(tap.identity(), null);
  const refused = tapIdentity({ ...surface, exchange: async () => ({ ok: false, code: "forbidden" }) });
  assert.deepEqual(await refused.surface.exchange("gable"), { ok: false, code: "forbidden" });
  assert.equal(refused.identity(), null);
});
