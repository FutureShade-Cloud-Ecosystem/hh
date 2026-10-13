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

// ── item 8d: one header band. The host frames a desk app with the flag, and carries its Theme and Feedback. ──
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { announceChromeAction } from "../src/frames.js";
import { CHROME_ACTION_MESSAGE, FRAMED_BY_WORK_SHELL, FRAMED_PARAM } from "@hardscapehouse/design-system/src/framed.js";

const hostSrc = readFileSync(fileURLToPath(new URL("../src/host-element.ts", import.meta.url)), "utf8");

test("the host asks the frame to act on Theme or Feedback with one message at its own origin", () => {
  const posted = [];
  const frame = { postMessage: (message, origin) => posted.push([message, origin]) };
  announceChromeAction(frame, "tauri://localhost", "theme");
  announceChromeAction(frame, "tauri://localhost", "feedback");
  assert.deepEqual(posted, [
    [{ type: CHROME_ACTION_MESSAGE, action: "theme" }, "tauri://localhost"],
    [{ type: CHROME_ACTION_MESSAGE, action: "feedback" }, "tauri://localhost"],
  ]);
});

test("a host with no content frame yet sends nothing", () => {
  assert.doesNotThrow(() => announceChromeAction(null, "tauri://localhost", "theme"));
});

test("every desk app frame is opened with the framed flag; the front door frame is not", () => {
  assert.ok(/data-testid="app-frame"[^>]*src=\$\{hhAddress\(framedAddress\(current\.path\)\)\}/.test(hostSrc), "the app frame's address carries the flag");
  assert.ok(/data-testid="door-frame"[^>]*src=\$\{hhAddress\(DOOR_PATH\)\}/.test(hostSrc), "the door frame keeps its plain address");
  assert.equal(FRAMED_PARAM, "hh-framed");
  assert.equal(FRAMED_BY_WORK_SHELL, "work-shell");
});

test("the account menu carries Theme and Feedback beside Switch role", () => {
  assert.ok(/data-action="theme"/.test(hostSrc), "a Theme control");
  assert.ok(/data-action="feedback"/.test(hostSrc), "a Feedback control");
});

test("the host listens for the frame's list of controls and shows only those", () => {
  assert.ok(/acceptChromeControls\(/.test(hostSrc), "the list is read through the one acceptor");
  assert.ok(/data-action="theme"/.test(hostSrc) && /data-action="feedback"/.test(hostSrc));
});
