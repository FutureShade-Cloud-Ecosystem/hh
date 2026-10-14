// The frame guard: a desk app inside the host's content frame ends its own
// walk with window.location.assign("/") (Switch role, no session). At the
// shell's origin root that is the host itself, so a framed host posts one
// message to its parent instead of mounting, and the parent accepts that
// message only from its own content frame, from its own origin.
import { test } from "node:test";
import assert from "node:assert/strict";
import { SWITCH_ROLE_MESSAGE, acceptSwitchRole, announceSwitchRole, isFramed } from "../src/frames.js";

test("a window whose parent is another window is framed; the top window is not", () => {
  const top = {};
  top.parent = top;
  assert.equal(isFramed(top), false);
  assert.equal(isFramed({ parent: top }), true);
});

test("a framed host announces Switch role to its parent, scoped to its own origin", () => {
  const posted = [];
  const parent = { postMessage: (message, origin) => posted.push([message, origin]) };
  announceSwitchRole({ parent, location: { origin: "tauri://localhost" } });
  assert.deepEqual(posted, [[{ type: SWITCH_ROLE_MESSAGE }, "tauri://localhost"]]);
});

test("the parent accepts the message only from its content frame at its own origin with the exact type", () => {
  const frame = {};
  const good = { origin: "http://127.0.0.1:1", source: frame, data: { type: SWITCH_ROLE_MESSAGE } };
  assert.equal(acceptSwitchRole(good, { origin: "http://127.0.0.1:1", contentWindow: frame }), true);
  assert.equal(acceptSwitchRole({ ...good, origin: "http://evil.example" }, { origin: "http://127.0.0.1:1", contentWindow: frame }), false);
  assert.equal(acceptSwitchRole({ ...good, source: {} }, { origin: "http://127.0.0.1:1", contentWindow: frame }), false);
  assert.equal(acceptSwitchRole({ ...good, data: { type: "other" } }, { origin: "http://127.0.0.1:1", contentWindow: frame }), false);
  assert.equal(acceptSwitchRole({ ...good, data: "hh-work-shell:switch-role" }, { origin: "http://127.0.0.1:1", contentWindow: frame }), false);
  assert.equal(acceptSwitchRole(good, { origin: "http://127.0.0.1:1", contentWindow: null }), false);
});
