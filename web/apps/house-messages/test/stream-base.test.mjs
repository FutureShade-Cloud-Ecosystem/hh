// The stream URL's base: in console mode the ticket mint rides the custody's coreBaseUrl, and the
// stream must open on that same base. Before the fix the stream was opened on the path alone, so a
// split-origin console sent each single-use ticket in a query string to the page's host, where the
// feed never connected. This suite boots a console custody the way the page does and drives the same
// streamUrlOnCore the app root's openStream calls. Every value is fake.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SessionStore } from "@hardscapehouse/auth";
import { FakeScheduler, RecordedStorage, fakeExchangeAnswer, FAKE_ACCOUNT } from "@hardscapehouse/auth/testing";
import { bootCustody } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { streamUrlOnCore } from "../src/app-root.ts";

const APP = "house-messages";
const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const START = 1_000_000_000_000;
const CONFIG = {
  mode: "console",
  org: "gable",
  consoleEndpoint: "https://console.example.test/v1",
  consoleProjectId: "fake-project",
  coreBaseUrl: "https://core.example",
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

// Boot a console custody with a first party exchange that answers, then hand the app root's
// openStream a LiveFeed-style stream path and capture the URL the EventSource would receive.
function bootConsoleCoreBaseUrl(coreBaseUrl) {
  const clock = new FakeScheduler(START);
  const sessions = new SessionStore({ now: clock.now });
  const exchange = async () => fakeExchangeAnswer("fake-core-token", START + 900_000, { role_code: "driver", role_name: "The Role" });
  const custody = bootCustody({ ...CONFIG, coreBaseUrl }, {
    sessions,
    demoAccount: surface(exchange),
    demoOrg: "hh-demo",
    surface: surface(exchange),
    storage: new RecordedStorage(),
    timers: clock,
    now: clock.now,
    navigate: () => {},
  });
  return { custody, sessions };
}

test("House Messages: a console custody opens the stream on the core's base URL, one slash, full path", async () => {
  const { custody, sessions } = bootConsoleCoreBaseUrl("https://core.example");
  const outcome = await new Promise((resolve) => custody.begin(APP, resolve));
  assert.equal(outcome.kind, "ready");
  assert.equal(custody.coreBaseUrl, "https://core.example");
  const opened = [];
  const openStream = (url) => {
    const captured = streamUrlOnCore(custody.coreBaseUrl, url);
    opened.push(captured);
    return { addEventListener: () => {}, close: () => {} };
  };
  openStream("/api/v1/messaging/stream?ticket=t1");
  const url = opened[0];
  assert.ok(url.startsWith("https://core.example"), `the stream opens on the core base: ${url}`);
  assert.ok(url.endsWith("/api/v1/messaging/stream?ticket=t1"), `the stream keeps its full path: ${url}`);
  assert.ok(!url.includes("//api/"), `no double slash where the base meets the path: ${url}`);
  assert.doesNotMatch(url, /^\/api\//, "the stream path is never left relative in console mode");
  await custody.leave("/");
  assert.equal(sessions.current, null);
});

test("House Messages: a configured base with a trailing slash still opens one slash, not two", () => {
  const url = streamUrlOnCore("https://core.example/", "/api/v1/messaging/stream?ticket=t1");
  assert.equal(url, "https://core.example/api/v1/messaging/stream?ticket=t1");
});

test("House Messages: with no configured base (demo mode) the stream stays on the relative path", () => {
  assert.equal(streamUrlOnCore("", "/api/v1/messaging/stream?ticket=t1"), "/api/v1/messaging/stream?ticket=t1");
  assert.equal(streamUrlOnCore(null, "/api/v1/messaging/stream?ticket=t1"), "/api/v1/messaging/stream?ticket=t1");
});

test("House Messages: the app root's openStream builds the EventSource URL through streamUrlOnCore", () => {
  const root = readFileSync(join(appDir, "src", "app-root.ts"), "utf8");
  assert.match(root, /openStream: \(url\) => new EventSource\(streamUrlOnCore\(this\.#custody\.coreBaseUrl, url\)\)/);
});
