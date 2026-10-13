// The console mode's state machine, driven with the auth package's fake
// account surface, an injected clock and fake fetch: starting, signed out,
// signing in, every refusal, the role entry, the persona cards, sign out and a
// session that ends underneath the page. Every value is fake.
import { test } from "node:test";
import assert from "node:assert/strict";
import { PlatformRefusedError, PlatformUnreachableError, ThrowSafeMirror } from "@hardscapehouse/auth";
import {
  FAKE_ACCOUNT,
  FAKE_PASSWORD,
  FakeAccountSurface,
  FakeScheduler,
  RecordedStorage,
  fakeExchangeAnswer,
  fakeExchangeRefusal,
} from "@hardscapehouse/auth/testing";
import { ConsoleSession } from "../src/console.js";
import { SIGN_IN_WORDS, PERSONA_REFUSAL_WORDS } from "../src/signin.js";
import { LIVE_ROLES } from "../src/roles.js";

const START = 1_000_000_000_000;
const CONFIG = {
  mode: "console",
  org: "gable",
  consoleEndpoint: "https://console.example.test/v1",
  consoleProjectId: "fake-project",
  coreBaseUrl: "",
  personas: false,
  personasOpen: false,
  dealer: { name: "Gable Landscape Supply", mark: "gable" },
};

function ownAnswer(roleCode = "inside_sales", roleName = "Inside Sales") {
  return (n, expiresAtMs) => fakeExchangeAnswer(`fake-core-token-${n}`, expiresAtMs, { role_code: roleCode, role_name: roleName });
}

class World {
  constructor({ config = CONFIG, methods = ["email_password", "google"], url = "https://hh.example.test/", account } = {}) {
    this.clock = new FakeScheduler(START);
    this.account = account ?? new FakeAccountSurface();
    this.account.setClock(this.clock.now);
    this.methods = methods;
    this.methodStatus = 200;
    this.personaStatus = 200;
    this.personaCalls = [];
    this.online = true;
    this.url = url;
    this.replaced = [];
    this.changes = 0;
    this.account.script(); // nothing queued
    const fetchFn = async (input, init = {}) => {
      const target = String(input);
      if (target.includes("/api/v1/auth/methods")) {
        this.methodsAsked = target;
        return this.methodStatus === 200
          ? new Response(JSON.stringify({ methods: this.methods }), { status: 200 })
          : new Response(null, { status: this.methodStatus });
      }
      if (target.includes("/api/v1/auth/persona")) {
        const body = JSON.parse(init.body);
        this.personaCalls.push({ body, headers: init.headers });
        if (this.personaStatus !== 200) return new Response(null, { status: this.personaStatus });
        // The core's persona ids are the flow ids; the answer names the role code (they differ for sales-manager).
        const live = LIVE_ROLES.find((role) => role.flow === body.persona);
        if (live === undefined) return new Response(null, { status: 403 });
        const result = fakeExchangeAnswer(`fake-persona-token-${body.persona}`, this.clock.now() + 600_000, {
          user_id: `persona:${body.persona}`,
          role_code: live.id,
          role_name: `Persona ${body.persona}`,
          display_name: `Persona ${body.persona}`,
        });
        return new Response(JSON.stringify(result.answer), { status: 200 });
      }
      throw new Error(`unexpected fetch ${target}`);
    };
    this.session = new ConsoleSession({
      config,
      account: this.account,
      fetch: fetchFn,
      storage: new RecordedStorage(),
      now: this.clock.now,
      timers: this.clock,
      online: () => this.online,
      pageUrl: () => this.url,
      replaceUrl: (next) => { this.replaced.push(next); this.url = next; },
      onChange: () => { this.changes += 1; },
    });
  }
}

const EMAIL = FAKE_ACCOUNT.email;

async function signedOutWorld(options) {
  const w = new World(options);
  await w.session.start();
  return w;
}

async function signedInWorld(options) {
  const w = await signedOutWorld(options);
  w.account.script(ownAnswer()(1, w.clock.now() + 900_000));
  const ok = await w.session.submit(EMAIL, FAKE_PASSWORD);
  assert.equal(ok, undefined);
  return w;
}

test("starting: methods are asked for the org, no session means the sign in screen with no notice", async () => {
  const w = new World();
  assert.equal(w.session.phase, "starting");
  assert.deepEqual(w.session.methods, { kind: "loading" });
  await w.session.start();
  assert.equal(w.session.phase, "signed-out");
  assert.deepEqual(w.session.methods, { kind: "ready", methods: ["email_password", "google"] });
  assert.equal(w.session.notice.text, "");
  assert.match(w.methodsAsked, /org=gable$/);
});

test("starting with a live platform session resumes it into the role entry", async () => {
  const account = new FakeAccountSurface();
  await account.createEmailPasswordSession(EMAIL, FAKE_PASSWORD);
  const w = new World({ account });
  account.script(ownAnswer()(1, START + 900_000));
  await w.session.start();
  assert.equal(w.session.phase, "signed-in");
  assert.equal(w.session.landed, true, "no persona cards to choose from, so it lands");
  assert.equal(w.session.who.roleCode, "inside_sales");
  assert.equal(w.session.bearer, "fake-core-token-1");
});

test("starting with personas on resumes to the entry, not past it", async () => {
  const account = new FakeAccountSurface();
  await account.createEmailPasswordSession(EMAIL, FAKE_PASSWORD);
  const w = new World({ account, config: { ...CONFIG, personas: true } });
  account.script(ownAnswer()(1, START + 900_000));
  await w.session.start();
  assert.equal(w.session.landed, false);
});

test("a resume refused as not allowed says so; one that cannot reach the console says offline; none says nothing", async () => {
  const account = new FakeAccountSurface();
  await account.createEmailPasswordSession(EMAIL, FAKE_PASSWORD);
  const w = new World({ account });
  account.script(fakeExchangeRefusal("forbidden"));
  await w.session.start();
  assert.equal(w.session.phase, "signed-out");
  assert.equal(w.session.notice.text, SIGN_IN_WORDS.forbidden);

  class Unreachable extends FakeAccountSurface {
    async getSession() { throw new PlatformUnreachableError(); }
  }
  const offline = new World({ account: new Unreachable() });
  await offline.session.start();
  assert.equal(offline.session.notice.text, SIGN_IN_WORDS.offline);
});

test("the methods route failing is a state of its own with a retry, and the form is not shown as if it worked", async () => {
  const w = new World();
  w.methodStatus = 503;
  await w.session.start();
  assert.deepEqual(w.session.methods, { kind: "failed", reason: "unavailable" });
  w.methodStatus = 200;
  await w.session.retryMethods();
  assert.equal(w.session.methods.kind, "ready");
});

test("only the methods the dealer allows are offered", async () => {
  const w = await signedOutWorld({ methods: ["microsoft", "magic_link"] });
  assert.deepEqual(w.session.methods, { kind: "ready", methods: [] });
  const g = await signedOutWorld({ methods: ["google"] });
  assert.deepEqual(g.session.methods, { kind: "ready", methods: ["google"] });
});

test("signing in: the live words say so, then the role entry with the person's own role", async () => {
  const w = await signedOutWorld();
  w.account.script(ownAnswer("sales_manager", "Sales Manager")(1, w.clock.now() + 900_000));
  const seen = [];
  const original = w.session;
  const pending = original.submit(EMAIL, FAKE_PASSWORD);
  seen.push([original.phase, original.notice.text]);
  await pending;
  assert.deepEqual(seen[0], ["signing-in", "Signing in."]);
  assert.equal(original.phase, "signed-in");
  assert.equal(original.landed, false);
  assert.equal(original.who.roleLabel, "Sales Manager");
  assert.equal(original.who.name, FAKE_ACCOUNT.name);
  assert.equal(original.notice.text, "");
});

test("wrong credentials: plain words, focus back to the password, the person still signed out", async () => {
  const w = await signedOutWorld();
  await w.session.submit(EMAIL, "not-the-password");
  assert.equal(w.session.phase, "signed-out");
  assert.equal(w.session.notice.text, SIGN_IN_WORDS.bad_credentials);
  assert.equal(w.session.notice.tone, "error");
  assert.equal(w.session.takeFocus(), "password");
  assert.equal(w.session.takeFocus(), null, "a focus request is consumed once");
});

test("each refusal of the exchange has its own words and returns focus to the button", async () => {
  const cases = [
    ["forbidden", SIGN_IN_WORDS.forbidden],
    ["rate_limited", SIGN_IN_WORDS.rate_limited],
    ["unavailable", SIGN_IN_WORDS.unavailable],
    ["offline", SIGN_IN_WORDS.offline],
    ["unauthorized", SIGN_IN_WORDS.generic],
  ];
  for (const [code, words] of cases) {
    const w = await signedOutWorld();
    w.account.script(fakeExchangeRefusal(code));
    await w.session.submit(EMAIL, FAKE_PASSWORD);
    assert.equal(w.session.phase, "signed-out", code);
    assert.equal(w.session.notice.text, words, code);
    assert.equal(w.session.takeFocus(), "submit", code);
    assert.equal(w.session.bearer, null, code);
  }
});

test("a console throttle at sign in is the wait wording", async () => {
  class Throttled extends FakeAccountSurface {
    async createEmailPasswordSession() { throw new PlatformRefusedError(429); }
  }
  const w = await signedOutWorld({ account: new Throttled() });
  await w.session.submit(EMAIL, FAKE_PASSWORD);
  assert.equal(w.session.notice.text, SIGN_IN_WORDS.rate_limited);
});

test("offline: the browser being offline answers at once, without calling anything", async () => {
  const w = await signedOutWorld();
  w.online = false;
  await w.session.submit(EMAIL, FAKE_PASSWORD);
  assert.equal(w.session.notice.text, SIGN_IN_WORDS.offline);
  assert.equal(w.account.creationCount, 0);
  assert.equal(w.account.exchangeCount, 0);
});

test("empty fields are asked for before any call", async () => {
  const w = await signedOutWorld();
  await w.session.submit("", "");
  assert.equal(w.session.notice.text, "Enter your email and password.");
  assert.equal(w.session.takeFocus(), "email");
  await w.session.submit(EMAIL, "");
  assert.equal(w.session.takeFocus(), "password");
  assert.equal(w.account.creationCount, 0);
});

test("a second submit while one is in flight does nothing", async () => {
  const w = await signedOutWorld();
  const first = w.session.submit(EMAIL, FAKE_PASSWORD);
  await w.session.submit(EMAIL, FAKE_PASSWORD);
  await first;
  assert.equal(w.account.creationCount, 1);
});

test("the password is never kept: no field of the session holds it", async () => {
  const w = await signedInWorld();
  const dump = JSON.stringify({ phase: w.session.phase, who: w.session.who, notice: w.session.notice, methods: w.session.methods });
  assert.equal(dump.includes(FAKE_PASSWORD), false);
});

test("continue as the person's own role lands them", async () => {
  const w = await signedInWorld();
  w.session.continueOwn();
  assert.equal(w.session.landed, true);
});

test("google goes to the console with this page as the way back, and a failed return says so and cleans the address", async () => {
  const w = await signedOutWorld({ url: "https://hh.example.test/?x=1" });
  await w.session.google();
  assert.equal(w.account.oauthCount, 1);
  const failed = new World({ url: "https://hh.example.test/?signin=failed" });
  await failed.session.start();
  assert.equal(failed.session.notice.text, SIGN_IN_WORDS.google_failed);
  assert.deepEqual(failed.replaced, ["https://hh.example.test/"]);
});

test("sign out ends the session and says so", async () => {
  const w = await signedInWorld();
  await w.session.signOut();
  assert.equal(w.session.phase, "signed-out");
  assert.equal(w.session.who, null);
  assert.equal(w.session.bearer, null);
  assert.equal(w.account.deleteCount >= 1, true);
  assert.equal(w.session.notice.text, "You are signed out.");
});

test("a session that ends underneath the page returns to sign in with the session ended words", async () => {
  const w = await signedInWorld();
  w.account.script(fakeExchangeRefusal("no_session")); // the refresh finds the console session gone
  await w.clock.advance(900_000 - 60_000);
  w.session.tick();
  assert.equal(w.session.phase, "signed-out");
  assert.equal(w.session.notice.text, SIGN_IN_WORDS.no_session);
  assert.equal(w.session.bearer, null);
});

test("persona cards: an entry adopts the answer, lands, and arms no refresh", async () => {
  const w = await signedOutWorld({ config: { ...CONFIG, personas: true, personasOpen: true } });
  await w.session.persona("driver");
  assert.equal(w.session.phase, "signed-in");
  assert.equal(w.session.landed, true);
  assert.equal(w.session.who.roleCode, "driver");
  assert.equal(w.session.who.persona, true);
  assert.deepEqual(w.personaCalls[0].body, { persona: "driver" });
  assert.equal("Authorization" in w.personaCalls[0].headers, false, "the open copy sends no bearer");
  await w.clock.advance(300_000);
  assert.equal(w.account.exchangeCount, 0, "no refresh ran");
  await w.clock.advance(400_000);
  w.session.tick();
  assert.equal(w.session.phase, "signed-out", "the persona token ended at its expiry");
  assert.equal(w.session.notice.text, SIGN_IN_WORDS.no_session);
});

test("the testers' copy sends the partner's bearer to the persona route", async () => {
  const w = await signedInWorld({ config: { ...CONFIG, personas: true } });
  await w.session.persona("yard-team");
  assert.equal(w.personaCalls[0].headers.Authorization, "Bearer fake-core-token-1");
});

test("a persona refusal shows in the card that earned it and the others keep working", async () => {
  const w = await signedOutWorld({ config: { ...CONFIG, personas: true, personasOpen: true } });
  w.personaStatus = 429;
  await w.session.persona("driver");
  assert.equal(w.session.phase, "signed-out");
  assert.equal(w.session.personaRefusal("driver"), PERSONA_REFUSAL_WORDS.rate_limited);
  assert.equal(w.session.personaRefusal("yard-team"), "");
  w.personaStatus = 200;
  await w.session.persona("yard-team");
  assert.equal(w.session.phase, "signed-in");
  assert.equal(w.session.personaRefusal("driver"), PERSONA_REFUSAL_WORDS.rate_limited, "the other card's refusal stands until its own retry");
});

test("a persona retry clears that card's earlier refusal", async () => {
  const w = await signedOutWorld({ config: { ...CONFIG, personas: true, personasOpen: true } });
  w.personaStatus = 503;
  await w.session.persona("driver");
  assert.equal(w.session.personaRefusal("driver"), PERSONA_REFUSAL_WORDS.unavailable);
  w.personaStatus = 200;
  await w.session.persona("driver");
  assert.equal(w.session.personaRefusal("driver"), "");
});

test("persona entry offline is refused in the card without a call; an unknown persona id is ignored", async () => {
  const w = await signedOutWorld({ config: { ...CONFIG, personas: true, personasOpen: true } });
  w.online = false;
  await w.session.persona("driver");
  assert.equal(w.session.personaRefusal("driver"), PERSONA_REFUSAL_WORDS.offline);
  assert.equal(w.personaCalls.length, 0);
  w.online = true;
  await w.session.persona("not-a-role");
  assert.equal(w.personaCalls.length, 0);
});

test("persona routes are never called when the deployment has none", async () => {
  const w = await signedOutWorld();
  await w.session.persona("driver");
  assert.equal(w.personaCalls.length, 0);
});

test("switching role from a persona restores the person's own session; with none it returns to sign in", async () => {
  const account = new FakeAccountSurface();
  const w = await signedOutWorld({ account, config: { ...CONFIG, personas: true } });
  account.script(ownAnswer("sales_manager", "Sales Manager")(1, w.clock.now() + 900_000));
  await w.session.submit(EMAIL, FAKE_PASSWORD);
  await w.session.persona("driver");
  assert.equal(w.session.who.persona, true);
  account.script(ownAnswer("sales_manager", "Sales Manager")(2, w.clock.now() + 900_000));
  await w.session.switchRole();
  assert.equal(w.session.who.roleCode, "sales_manager");
  assert.equal(w.session.who.persona, false);
  assert.equal(w.session.landed, false);

  const open = await signedOutWorld({ config: { ...CONFIG, personas: true, personasOpen: true } });
  await open.session.persona("driver");
  await open.session.switchRole();
  assert.equal(open.session.phase, "signed-out");
  assert.equal(open.session.bearer, null);
});

test("nothing bearer capable is written to storage", async () => {
  const storage = new RecordedStorage();
  const w = new World();
  const session = new ConsoleSession({
    config: CONFIG,
    account: w.account,
    fetch: async () => new Response(JSON.stringify({ methods: ["email_password"] }), { status: 200 }),
    storage,
    now: w.clock.now,
    timers: w.clock,
    online: () => true,
    pageUrl: () => "https://hh.example.test/",
    replaceUrl: () => {},
    onChange: () => {},
  });
  await session.start();
  w.account.script(ownAnswer()(1, w.clock.now() + 900_000));
  await session.submit(EMAIL, FAKE_PASSWORD);
  for (const write of storage.writes) {
    assert.equal(write.value.includes("fake-core-token"), false, write.key);
    assert.equal(write.value.includes(FAKE_PASSWORD), false, write.key);
  }
});

test("onChange fires as the state moves", async () => {
  const w = await signedInWorld();
  assert.ok(w.changes > 3);
});

test("the persona id sent is the core's flow id, not the role code (sales-manager differs from sales_manager)", async () => {
  const w = await signedOutWorld({ config: { ...CONFIG, personas: true, personasOpen: true } });
  await w.session.persona("sales-manager");
  assert.deepEqual(w.personaCalls[0].body, { persona: "sales-manager" });
  assert.equal(w.session.who.roleCode, "sales_manager", "the answer's role code still drives the landing");
  assert.equal(w.session.phase, "signed-in");
  const w2 = await signedOutWorld({ config: { ...CONFIG, personas: true, personasOpen: true } });
  await w2.session.persona("sales_manager");
  assert.equal(w2.personaCalls.length, 0, "a role code is not a persona id");
  assert.equal(w2.session.phase, "signed-out");
});
