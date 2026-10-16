// The manifest's print window: the document rides a blob address into a window the click opened, the
// print dialog is that window's own, called on its load, and a blocked window is a sentence, not a silence.
import { test } from "node:test";
import assert from "node:assert/strict";
import { printHtmlInto } from "../src/print-window.ts";

function fakeWindow() {
  const calls = [];
  let onLoad = null;
  return {
    calls,
    load: () => onLoad?.(),
    win: {
      location: { assign: (url) => calls.push(["assign", url]) },
      addEventListener: (type, cb) => {
        calls.push(["listen", type]);
        onLoad = cb;
      },
      focus: () => calls.push(["focus"]),
      print: () => calls.push(["print"]),
      close: () => calls.push(["close"]),
    },
  };
}
const deps = (log) => ({
  toUrl: (markup) => {
    log.push(["toUrl", markup]);
    return "blob:manifest-1";
  },
  revoke: (url) => log.push(["revoke", url]),
  later: (fn, ms) => log.push(["later", ms, fn]),
});

test("the document is carried into the window by address, and printed only when that window has loaded it", () => {
  const w = fakeWindow();
  const log = [];
  const out = printHtmlInto(w.win, "<p>manifest</p>", deps(log));
  assert.deepEqual(out, { ok: true });
  assert.deepEqual(log[0], ["toUrl", "<p>manifest</p>"]);
  assert.deepEqual(w.calls, [["listen", "load"], ["assign", "blob:manifest-1"]]);
  assert.equal(w.calls.some((c) => c[0] === "print"), false, "nothing prints before the window has the document");
  w.load();
  assert.deepEqual(w.calls.slice(2), [["focus"], ["print"]]);
  const later = log.find((e) => e[0] === "later");
  assert.equal(later[1], 60_000, "the address is spent a minute after, behind the print dialog's own read");
  later[2]();
  assert.deepEqual(log.at(-1), ["revoke", "blob:manifest-1"]);
});

test("a window the browser blocked is a sentence that says what to allow", () => {
  const out = printHtmlInto(null, "<p>x</p>", deps([]));
  assert.equal(out.ok, false);
  assert.match(out.message, /blocked the manifest window.*allow pop-ups/i);
});

test("a window that cannot take the document is closed and says so", () => {
  const w = fakeWindow();
  w.win.location.assign = () => {
    throw new Error("nope");
  };
  const out = printHtmlInto(w.win, "<p>x</p>", deps([]));
  assert.equal(out.ok, false);
  assert.match(out.message, /could not be opened/i);
  assert.equal(w.calls.some((c) => c[0] === "close"), true);
});
