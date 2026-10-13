// Item 8e: Switch role asks the framed app to run its own sign out and waits
// for its acknowledgement, bounded; whether or not the frame answers, the host
// then revokes what it can reach (switch-role.test.mjs). The wait, the nonce
// and the source check live in requestFrameSignOut.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { SIGNED_OUT_MESSAGE, SIGN_OUT_REQUEST_MESSAGE } from "@hardscapehouse/design-system/src/framed.js";
import { SIGN_OUT_WAIT_MS } from "../src/config.js";
import { requestFrameSignOut } from "../src/sign-out.js";

const ORIGIN = "http://127.0.0.1:9";

/** A fake host window, one fake frame, and timers the test fires by hand. */
function rig() {
  const listeners = new Set();
  const sent = [];
  const frameWindow = { postMessage: (message, origin) => sent.push([message, origin]) };
  const timers = [];
  const win = {
    addEventListener: (type, fn) => type === "message" && listeners.add(fn),
    removeEventListener: (type, fn) => type === "message" && listeners.delete(fn),
  };
  const deps = {
    frame: { contentWindow: frameWindow },
    origin: ORIGIN,
    win,
    nonce: () => "n0nce1",
    setTimer: (fn, ms) => {
      const timer = { fn, ms, cleared: false };
      timers.push(timer);
      return timer;
    },
    clearTimer: (timer) => {
      timer.cleared = true;
    },
  };
  const deliver = (event) => [...listeners].forEach((fn) => fn(event));
  return { deps, sent, listeners, timers, frameWindow, deliver };
}

test("the wait is a named constant of two seconds", () => {
  assert.equal(SIGN_OUT_WAIT_MS, 2000);
});

test("the frame is asked once with a nonce, and its acknowledgement ends the wait early", async () => {
  const { deps, sent, listeners, timers, frameWindow, deliver } = rig();
  const outcome = requestFrameSignOut(deps);
  assert.deepEqual(sent, [[{ type: SIGN_OUT_REQUEST_MESSAGE, nonce: "n0nce1" }, ORIGIN]]);
  assert.equal(timers.length, 1);
  assert.equal(timers[0].ms, SIGN_OUT_WAIT_MS, "bounded by the named constant");
  deliver({ origin: ORIGIN, source: frameWindow, data: { type: SIGNED_OUT_MESSAGE, nonce: "n0nce1" } });
  assert.equal(await outcome, "acknowledged");
  assert.equal(timers[0].cleared, true, "the timer is cleared");
  assert.equal(listeners.size, 0, "the listener is removed");
});

test("a frame that never answers is given up on after the bounded wait", async () => {
  const { deps, listeners, timers } = rig();
  const outcome = requestFrameSignOut(deps);
  timers[0].fn();
  assert.equal(await outcome, "timed-out");
  assert.equal(listeners.size, 0);
});

test("an acknowledgement from another window, another origin, another nonce or another type does not count", async () => {
  const { deps, timers, frameWindow, deliver } = rig();
  const outcome = requestFrameSignOut(deps);
  deliver({ origin: ORIGIN, source: {}, data: { type: SIGNED_OUT_MESSAGE, nonce: "n0nce1" } });
  deliver({ origin: "http://evil.example", source: frameWindow, data: { type: SIGNED_OUT_MESSAGE, nonce: "n0nce1" } });
  deliver({ origin: ORIGIN, source: frameWindow, data: { type: SIGNED_OUT_MESSAGE, nonce: "guess" } });
  deliver({ origin: ORIGIN, source: frameWindow, data: { type: SIGN_OUT_REQUEST_MESSAGE, nonce: "n0nce1" } });
  timers[0].fn();
  assert.equal(await outcome, "timed-out", "none of the four ended the wait");
});

test("a late acknowledgement after the wait is spent changes nothing", async () => {
  const { deps, timers, frameWindow, deliver } = rig();
  const outcome = requestFrameSignOut(deps);
  timers[0].fn();
  assert.equal(await outcome, "timed-out");
  assert.doesNotThrow(() => deliver({ origin: ORIGIN, source: frameWindow, data: { type: SIGNED_OUT_MESSAGE, nonce: "n0nce1" } }));
});

test("no frame mounted: nothing is sent, nothing waits", async () => {
  const { deps, sent, listeners, timers } = rig();
  assert.equal(await requestFrameSignOut({ ...deps, frame: null }), "no-frame");
  assert.equal(await requestFrameSignOut({ ...deps, frame: { contentWindow: null } }), "no-frame");
  assert.deepEqual(sent, []);
  assert.equal(listeners.size, 0);
  assert.equal(timers.length, 0);
});

const hostSrc = readFileSync(fileURLToPath(new URL("../src/host-element.ts", import.meta.url)), "utf8");

test("the host's Switch role asks the frame first and revokes itself either way", () => {
  assert.ok(/requestFrameSignOut\(/.test(hostSrc), "Switch role asks the frame through the one function");
  assert.ok(/await requestFrameSignOut\([\s\S]*?\)[\s\S]*?endSession\(/.test(hostSrc), "the host's own revocation follows the wait, whatever it answered");
});

test("a frame that ended its own walk is not asked again, and a second Switch role while one runs does nothing", () => {
  assert.ok(/#leaving/.test(hostSrc), "a leave in progress is remembered");
  assert.ok(/ask: false/.test(hostSrc), "the frame's own end of walk skips the ask");
});
