// The micro apps' shared session recovery (bootCustody): in console mode a
// page recovers its own session on load (the first party Console cookie, then
// the exchange for a core token held in memory), or a persona entry from the
// one role id the front door left in session storage. Demo mode is today's
// controller over the demo account, untouched. Every case runs against fakes:
// a fake surface, a fake core fetch, a fake clock.
import { test } from "node:test";
import assert from "node:assert/strict";
import { SessionStore, PERSONA_MIRROR_KEY, PersonaMemo, PlatformUnreachableError } from "@hardscapehouse/auth";
import { FakeScheduler, RecordedStorage, fakeExchangeAnswer, fakeExchangeRefusal, FAKE_ACCOUNT } from "@hardscapehouse/auth/testing";
import { APP_IDS, APP_ROLES, ROLE_FLOWS, bootCustody, entryFor } from "./boot-custody.ts";

const START = 1_000_000_000_000;
const CONSOLE = {
  mode: "console",
  org: "gable",
  consoleEndpoint: "https://console.example.test/v1",
  consoleProjectId: "proj",
  coreBaseUrl: "https://core.example.test",
  personas: false,
  personasOpen: false,
  dealer: { name: "Gable Landscape Supply" },
};
const PUBLIC_COPY = { ...CONSOLE, personas: true, personasOpen: true };
const TESTERS_COPY = { ...CONSOLE, personas: true, personasOpen: false };

/** A fake surface: what getSession and exchange answer is set per test. */
function fakeSurface({ session = "ok", exchange } = {}) {
  const calls = [];
  const state = { session, exchange };
  const surface = {
    calls,
    state,
    createEmailPasswordSession: async () => FAKE_ACCOUNT,
    createOAuth2Session: async () => {},
    getSession: async () => {
      calls.push("getSession");
      if (state.session === "ok") return FAKE_ACCOUNT;
      if (state.session === "offline") throw new PlatformUnreachableError();
      throw new Error("no session");
    },
    exchange: async (org) => {
      calls.push(`exchange:${org}`);
      return state.exchange();
    },
    deleteSession: async () => {
      calls.push("deleteSession");
    },
  };
  return surface;
}

const answerFor = (roleCode, token = "fake-core-token") =>
  fakeExchangeAnswer(token, START + 900_000, { role_code: roleCode, role_name: `Label of ${roleCode}` });

function boot(config, { surface, fetchFn, storage = new RecordedStorage(), clock = new FakeScheduler(START), baseUrl } = {}) {
  const sessions = new SessionStore({ now: clock.now });
  const custody = bootCustody(config, {
    sessions,
    demoAccount: surface ?? fakeSurface({ exchange: () => answerFor("driver") }),
    demoOrg: "hh-demo",
    surface,
    fetch: fetchFn,
    storage,
    timers: clock,
    now: clock.now,
    navigate: (url) => navigated.push(url),
  });
  void baseUrl;
  return { custody, sessions, storage, clock };
}
const navigated = [];

/** Run begin and collect every outcome it reports until `count` have arrived. */
function collect(custody, appId, count) {
  const seen = [];
  let resolve;
  const done = new Promise((r) => (resolve = r));
  custody.begin(appId, (outcome) => {
    seen.push(outcome);
    if (seen.length >= count) resolve(seen);
  });
  return { seen, done };
}

const personaFetch = (log, { status = 200, roleCode = "driver" } = {}) => async (url, init) => {
  log.push({ url: String(url), init });
  if (status !== 200) return new Response(null, { status });
  return new Response(
    JSON.stringify({
      token: "fake-persona-token",
      expiresAtMs: START + 3_600_000,
      user: { user_id: `persona:${roleCode}`, email: "p@hh-demo.invalid", display_name: "Persona", role_code: roleCode, role_name: "Persona role", tier: 3, scope: "BRANCH" },
    }),
    { status: 200 },
  );
};

// ── the role matrix ───────────────────────────────────────────────────────

test("every app lists the role codes the matrix gives it, and every role code has a flow", () => {
  assert.deepEqual([...APP_IDS].sort(), Object.keys(APP_ROLES).sort());
  for (const roles of Object.values(APP_ROLES)) {
    assert.ok(roles.length > 0);
    for (const code of roles) assert.ok(ROLE_FLOWS[code], code);
  }
  assert.deepEqual(APP_ROLES["counter-till"], ["inside_sales", "sales_manager"]);
  assert.deepEqual(APP_ROLES["quote-desk"], ["inside_sales", "sales_manager"]);
  assert.deepEqual(APP_ROLES["drive-run"], ["driver"]);
  assert.deepEqual(APP_ROLES["yard-pick"], ["yard_manager", "yard_team"]);
  assert.deepEqual(APP_ROLES["money-desk"], ["payables_receivables", "financial_controller"]);
  assert.deepEqual(APP_ROLES["buying-and-stock"], ["yard_manager", "procurement_manager"]);
  assert.deepEqual(APP_ROLES["dealer-admin"], ["sales_manager"]);
  assert.deepEqual(APP_ROLES["manager-home"], ["sales_manager", "yard_manager", "payables_receivables", "procurement_manager", "financial_controller"]);
  assert.equal(ROLE_FLOWS["sales_manager"], "sales-manager");
  assert.equal(ROLE_FLOWS["inside_sales"], "sales");
});

test("entryFor maps the exchange's user block to what an app needs, for every role code the matrix gives it", () => {
  for (const appId of APP_IDS) {
    for (const code of APP_ROLES[appId]) {
      const user = { user_id: "u1", email: "e@x.invalid", display_name: "A Name", role_code: code, role_name: "The Label", tier: 2, scope: "BRANCH" };
      assert.deepEqual(entryFor(appId, user, false), {
        userId: "u1",
        email: "e@x.invalid",
        name: "A Name",
        roleCode: code,
        roleLabel: "The Label",
        flow: ROLE_FLOWS[code],
        tier: 2,
        scope: "BRANCH",
        persona: false,
      });
    }
  }
});

test("entryFor refuses a role the matrix does not give the app, and a code it does not know", () => {
  const user = (role_code) => ({ user_id: "u1", email: "", display_name: "", role_code, role_name: "x", tier: 1, scope: "s" });
  assert.equal(entryFor("drive-run", user("inside_sales"), false), null);
  assert.equal(entryFor("counter-till", user("driver"), false), null);
  assert.equal(entryFor("counter-till", user("platform_admin"), false), null);
  assert.equal(entryFor("counter-till", user(""), true), null);
});

// ── demo mode is today's ──────────────────────────────────────────────────

test("without a console config the controller is built over the demo account and begin does nothing", async () => {
  for (const config of [null, { mode: "demo" }]) {
    const demo = fakeSurface({ exchange: () => answerFor("driver") });
    const { custody } = boot(config, { surface: undefined });
    assert.equal(custody.mode, "demo");
    assert.equal(custody.org, "hh-demo");
    assert.equal(custody.coreBaseUrl, "");
    assert.equal(custody.identity(), null);
    let called = false;
    custody.begin("drive-run", () => (called = true));
    await new Promise((r) => setImmediate(r));
    assert.equal(called, false);
    assert.equal(demo.calls.length, 0);
  }
});

// ── console mode: the partner's own session ───────────────────────────────

test("console mode: a first party session recovers, and the app gets the person's role from the exchange", async () => {
  for (const appId of APP_IDS) {
    for (const code of APP_ROLES[appId]) {
      const surface = fakeSurface({ exchange: () => answerFor(code) });
      const { custody, sessions } = boot(CONSOLE, { surface });
      assert.equal(custody.mode, "console");
      assert.equal(custody.org, "gable");
      const { done } = collect(custody, appId, 1);
      const [outcome] = await done;
      assert.equal(outcome.kind, "ready", `${appId} ${code}`);
      assert.equal(outcome.entry.roleCode, code);
      assert.equal(outcome.entry.roleLabel, `Label of ${code}`);
      assert.equal(outcome.entry.flow, ROLE_FLOWS[code]);
      assert.equal(outcome.entry.persona, false);
      assert.equal(sessions.current.token, "fake-core-token");
      assert.equal(custody.identity().role_code, code);
      assert.deepEqual(surface.calls.slice(0, 2), ["getSession", "exchange:gable"]);
      await custody.leave("/");
    }
  }
});

test("console mode: a role the app does not give goes back to the front door", async () => {
  const { custody } = boot(CONSOLE, { surface: fakeSurface({ exchange: () => answerFor("driver") }) });
  const [outcome] = await collect(custody, "counter-till", 1).done;
  assert.deepEqual(outcome, { kind: "front-door", why: "wrong_role" });
  await custody.leave("/");
});

test("console mode: no console session, and an ending refusal, go back to the front door", async () => {
  const none = boot(CONSOLE, { surface: fakeSurface({ session: "none", exchange: () => answerFor("driver") }) });
  assert.deepEqual((await collect(none.custody, "drive-run", 1).done)[0], { kind: "front-door", why: "no_session" });
  for (const code of ["unauthorized", "forbidden", "no_session"]) {
    const { custody, sessions } = boot(CONSOLE, { surface: fakeSurface({ exchange: () => fakeExchangeRefusal(code) }) });
    const [outcome] = await collect(custody, "drive-run", 1).done;
    assert.equal(outcome.kind, "front-door", code);
    assert.equal(sessions.current, null);
  }
});

test("console mode: a transient refusal shows the unavailable state and the page retries until it lands", async () => {
  for (const code of ["unavailable", "rate_limited", "offline"]) {
    let live = false;
    const surface = fakeSurface({ exchange: () => (live ? answerFor("driver") : fakeExchangeRefusal(code)) });
    const { custody, clock } = boot(CONSOLE, { surface });
    const { seen, done } = collect(custody, "drive-run", 2);
    await new Promise((r) => setImmediate(r));
    assert.equal(seen.length, 1);
    assert.deepEqual(seen[0], { kind: "unavailable", reason: code });
    live = true;
    await clock.advance(15_000);
    const outcomes = await done;
    assert.equal(outcomes[1].kind, "ready", code);
    await custody.leave("/");
  }
});

test("console mode: a console that cannot be reached is unavailable, not a sign out", async () => {
  const { custody } = boot(CONSOLE, { surface: fakeSurface({ session: "offline", exchange: () => answerFor("driver") }) });
  const [outcome] = await collect(custody, "drive-run", 1).done;
  assert.deepEqual(outcome, { kind: "unavailable", reason: "offline" });
  await custody.leave("/");
});

test("console mode: no org means no resume, and the front door shows the configuration fault", async () => {
  const surface = fakeSurface({ exchange: () => answerFor("driver") });
  const { custody } = boot({ ...CONSOLE, org: "" }, { surface });
  const [outcome] = await collect(custody, "drive-run", 1).done;
  assert.deepEqual(outcome, { kind: "front-door", why: "config" });
  assert.equal(surface.calls.length, 0);
});

test("a session that ends after ready sends the page back to the front door", async () => {
  const { custody, sessions, clock } = boot(CONSOLE, { surface: fakeSurface({ exchange: () => answerFor("driver") }) });
  const { done, seen } = collect(custody, "drive-run", 2);
  await new Promise((r) => setImmediate(r));
  assert.equal(seen[0].kind, "ready");
  sessions.clear();
  await clock.advance(2_000);
  assert.deepEqual((await done)[1], { kind: "front-door", why: "ended" });
});

// ── personas ──────────────────────────────────────────────────────────────

test("public copy: the remembered persona id is asked of the core with no bearer, and the page is signed in as that role", async () => {
  const storage = new RecordedStorage();
  new PersonaMemo(storage).write("driver");
  const log = [];
  const surface = fakeSurface({ session: "none", exchange: () => fakeExchangeRefusal("no_session") });
  const { custody, sessions } = boot(PUBLIC_COPY, { surface, fetchFn: personaFetch(log), storage });
  const [outcome] = await collect(custody, "drive-run", 1).done;
  assert.equal(outcome.kind, "ready");
  assert.equal(outcome.entry.persona, true);
  assert.equal(outcome.entry.roleCode, "driver");
  assert.equal(log.length, 1);
  assert.equal(log[0].url, "https://core.example.test/api/v1/auth/persona");
  assert.deepEqual(JSON.parse(log[0].init.body), { persona: "driver" });
  assert.equal("Authorization" in log[0].init.headers, false);
  assert.equal(log[0].init.headers["X-Org"], "gable");
  assert.equal(sessions.current.token, "fake-persona-token");
  assert.equal(surface.calls.length, 0, "no console session is asked for on the public copy");
  await custody.leave("/");
});

test("testers' copy: the partner is resumed first and the persona route carries the partner's bearer", async () => {
  const storage = new RecordedStorage();
  new PersonaMemo(storage).write("driver");
  const log = [];
  const surface = fakeSurface({ exchange: () => answerFor("sales_manager", "fake-partner-token") });
  const { custody, sessions } = boot(TESTERS_COPY, { surface, fetchFn: personaFetch(log), storage });
  const [outcome] = await collect(custody, "drive-run", 1).done;
  assert.equal(outcome.kind, "ready");
  assert.equal(outcome.entry.roleCode, "driver");
  assert.equal(outcome.entry.persona, true);
  assert.deepEqual(surface.calls.slice(0, 2), ["getSession", "exchange:gable"]);
  assert.equal(log[0].init.headers["Authorization"], "Bearer fake-partner-token");
  assert.equal(sessions.current.token, "fake-persona-token");
  await custody.leave("/");
});

test("testers' copy: with no partner session the persona is not asked for at all", async () => {
  const storage = new RecordedStorage();
  new PersonaMemo(storage).write("driver");
  const log = [];
  const surface = fakeSurface({ session: "none", exchange: () => fakeExchangeRefusal("no_session") });
  const { custody } = boot(TESTERS_COPY, { surface, fetchFn: personaFetch(log), storage });
  const [outcome] = await collect(custody, "drive-run", 1).done;
  assert.equal(outcome.kind, "front-door");
  assert.equal(log.length, 0);
});

test("a persona refusal that ends goes to the front door and forgets the id; a transient one keeps it and retries", async () => {
  for (const status of [400, 401, 403]) {
    const storage = new RecordedStorage();
    new PersonaMemo(storage).write("driver");
    const { custody } = boot(PUBLIC_COPY, { surface: fakeSurface(), fetchFn: personaFetch([], { status }), storage });
    const [outcome] = await collect(custody, "drive-run", 1).done;
    assert.equal(outcome.kind, "front-door", String(status));
    assert.equal(new PersonaMemo(storage).read(), null, "the id is forgotten");
  }
  for (const status of [429, 503]) {
    const storage = new RecordedStorage();
    new PersonaMemo(storage).write("driver");
    const { custody } = boot(PUBLIC_COPY, { surface: fakeSurface(), fetchFn: personaFetch([], { status }), storage });
    const [outcome] = await collect(custody, "drive-run", 1).done;
    assert.equal(outcome.kind, "unavailable", String(status));
    assert.equal(new PersonaMemo(storage).read(), "driver", "the id is kept for the retry");
    await custody.leave("/");
  }
});

test("a persona for a role the app does not give goes back to the front door", async () => {
  const storage = new RecordedStorage();
  new PersonaMemo(storage).write("driver");
  const { custody } = boot(PUBLIC_COPY, { surface: fakeSurface(), fetchFn: personaFetch([]), storage });
  const [outcome] = await collect(custody, "counter-till", 1).done;
  assert.deepEqual(outcome, { kind: "front-door", why: "wrong_role" });
});

test("a remembered id on a copy without personas is dropped and the page resumes the person's own session", async () => {
  const storage = new RecordedStorage();
  new PersonaMemo(storage).write("driver");
  const log = [];
  const { custody } = boot(CONSOLE, { surface: fakeSurface({ exchange: () => answerFor("driver") }), fetchFn: personaFetch(log), storage });
  const [outcome] = await collect(custody, "drive-run", 1).done;
  assert.equal(outcome.kind, "ready");
  assert.equal(outcome.entry.persona, false);
  assert.equal(log.length, 0);
  assert.equal(new PersonaMemo(storage).read(), null);
  await custody.leave("/");
});

test("the session storage holds a persona id and never a token, across a whole persona hop", async () => {
  const storage = new RecordedStorage();
  new PersonaMemo(storage).write("driver");
  const { custody } = boot(TESTERS_COPY, {
    surface: fakeSurface({ exchange: () => answerFor("sales_manager", "fake-partner-token") }),
    fetchFn: personaFetch([]),
    storage,
  });
  await collect(custody, "drive-run", 1).done;
  for (const write of storage.writes) {
    assert.equal(write.value.includes("fake-partner-token"), false, write.key);
    assert.equal(write.value.includes("fake-persona-token"), false, write.key);
    assert.equal(write.value.includes("Bearer"), false, write.key);
  }
  const memo = storage.writes.filter((w) => w.key === PERSONA_MIRROR_KEY);
  assert.ok(memo.every((w) => JSON.parse(w.value) === "driver"));
  await custody.leave("/");
});

// ── leaving ───────────────────────────────────────────────────────────────

test("leave signs out, forgets the persona id, and goes to the front door only where the app is served under a prefix", async () => {
  const storage = new RecordedStorage();
  new PersonaMemo(storage).write("driver");
  const surface = fakeSurface({ session: "none", exchange: () => fakeExchangeRefusal("no_session") });
  const { custody, sessions } = boot(PUBLIC_COPY, { surface, fetchFn: personaFetch([]), storage });
  await collect(custody, "drive-run", 1).done;
  assert.ok(sessions.current);
  navigated.length = 0;
  assert.equal(await custody.leave("/"), false, "at the origin root the front door is not there");
  assert.deepEqual(navigated, []);
  assert.equal(sessions.current, null);
  assert.equal(new PersonaMemo(storage).read(), null);
  assert.equal(custody.identity(), null);
  assert.equal(await custody.leave("/drive/"), true);
  assert.deepEqual(navigated, ["/"]);
});

test("toFrontDoor navigates once under a prefix and never at the root", () => {
  const { custody } = boot(CONSOLE, { surface: fakeSurface() });
  navigated.length = 0;
  assert.equal(custody.toFrontDoor("/"), false);
  assert.equal(custody.toFrontDoor("/till/"), true);
  assert.deepEqual(navigated, ["/"]);
});
