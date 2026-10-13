// The Counter Till in console mode (item 7d): the page recovers its own session on load, through the shared custody
// (pos-kit boot-custody.ts), with a fake surface. The whole recovery is the shared helper's; this suite holds
// what this app asks of it (its app id and the role codes the matrix gives it) and that the app's source
// really wires it. Every value is fake.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SessionStore } from "@hardscapehouse/auth";
import { FakeScheduler, RecordedStorage, fakeExchangeAnswer, fakeExchangeRefusal, FAKE_ACCOUNT } from "@hardscapehouse/auth/testing";
import { APP_ROLES, ROLE_FLOWS, bootCustody } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";

const APP = "counter-till";
const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const START = 1_000_000_000_000;
const CONFIG = {
  mode: "console",
  org: "gable",
  consoleEndpoint: "https://console.example.test/v1",
  consoleProjectId: "fake-project",
  coreBaseUrl: "https://core.example.test",
  personas: false,
  personasOpen: false,
  dealer: { name: "Gable Landscape Supply" },
};

function surface(exchange) {
  return {
    createEmailPasswordSession: async () => FAKE_ACCOUNT,
    createOAuth2Session: async () => {},
    getSession: async () => FAKE_ACCOUNT,
    exchange,
    deleteSession: async () => {},
  };
}

function boot(config, exchange) {
  const clock = new FakeScheduler(START);
  const sessions = new SessionStore({ now: clock.now });
  const custody = bootCustody(config, {
    sessions,
    demoAccount: surface(exchange),
    demoOrg: "hh-demo",
    surface: surface(exchange),
    storage: new RecordedStorage(),
    timers: clock,
    now: clock.now,
    navigate: () => {},
  });
  return { custody, sessions, clock };
}

const first = (custody) => new Promise((resolve) => custody.begin(APP, resolve));
const answer = (roleCode) => async () => fakeExchangeAnswer("fake-core-token", START + 900_000, { role_code: roleCode, role_name: "The Role" });

test("The Counter Till: a first party session recovers for every role code the matrix gives the app", async () => {
  assert.ok(APP_ROLES[APP].length > 0);
  for (const code of APP_ROLES[APP]) {
    const { custody, sessions } = boot(CONFIG, answer(code));
    const outcome = await first(custody);
    assert.equal(outcome.kind, "ready", code);
    assert.equal(outcome.entry.roleCode, code);
    assert.equal(outcome.entry.roleLabel, "The Role");
    assert.equal(outcome.entry.flow, ROLE_FLOWS[code]);
    assert.equal(sessions.current.token, "fake-core-token");
    await custody.leave("/");
  }
});

test("The Counter Till: an ending refusal goes back to the front door", async () => {
  for (const code of ["unauthorized", "forbidden", "no_session"]) {
    const { custody, sessions } = boot(CONFIG, async () => fakeExchangeRefusal(code));
    const outcome = await first(custody);
    assert.equal(outcome.kind, "front-door", code);
    assert.equal(sessions.current, null);
  }
});

test("The Counter Till: a transient refusal shows the unavailable state and the page retries", async () => {
  let live = false;
  const { custody, clock } = boot(CONFIG, async () => (live ? answer(APP_ROLES[APP][0])() : fakeExchangeRefusal("unavailable")));
  const seen = [];
  custody.begin(APP, (outcome) => seen.push(outcome));
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(seen.map((o) => o.kind), ["unavailable"]);
  live = true;
  await clock.advance(15_000);
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(seen.map((o) => o.kind), ["unavailable", "ready"]);
  await custody.leave("/");
});

test("The Counter Till: with no org there is nothing to recover and the front door shows the configuration fault", async () => {
  let asked = 0;
  const { custody } = boot({ ...CONFIG, org: "" }, async () => {
    asked += 1;
    return answer(APP_ROLES[APP][0])();
  });
  assert.deepEqual(await first(custody), { kind: "front-door", why: "config" });
  assert.equal(asked, 0);
});

test("The Counter Till: demo mode is the demo account's controller and begin does nothing", async () => {
  for (const config of [null, { mode: "demo" }]) {
    const { custody } = boot(config, answer("driver"));
    assert.equal(custody.mode, "demo");
    assert.equal(custody.org, "hh-demo");
    let called = false;
    custody.begin(APP, () => (called = true));
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(called, false);
  }
});

test("The Counter Till: the app's source builds its custody from the served config and starts it with its own id", () => {
  const root = readFileSync(join(appDir, "src", "app-root.ts"), "utf8");
  assert.match(root, /bootCustody\(servedConsoleConfig\(\)/);
  assert.match(root, /this\.#custody\.begin\("counter-till"/);
  assert.match(root, /this\.#custody\.coreBaseUrl/);
  assert.match(root, /activeOrg: \(\) => this\.#custody\.org/);
  const main = readFileSync(join(appDir, "src", "main.ts"), "utf8");
  assert.match(main, /loadAppRuntime\(\)\.then\(\(\) => import\("\.\/app-root\.js"\)\)/);
});
