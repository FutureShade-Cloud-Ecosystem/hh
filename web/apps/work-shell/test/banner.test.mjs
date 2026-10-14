// The host opens no store and no outbox, and the desk apps are read cache or
// online only (H5: money never queues), so the offline banner must promise
// nothing about writes waiting: it runs the chrome's cacheOnly words. The
// banner is rendered with exactly the props the host feeds it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderKit } from "../../../packages/shell-chrome/test/lib/render.mjs";
import { ShellBanner } from "@hardscapehouse/shell-chrome/src/shell-banner.js";
import { bannerFor } from "../src/chrome-feed.js";

const text = (markup) => markup.replace(/<[^>]*>/g, " ").replace(/\s+/g, " ");

test("offline with no outbox feeds the banner cacheOnly and the words promise nothing", () => {
  const feed = bannerFor({ online: false });
  assert.equal(feed.mode, "offline");
  assert.equal(feed.cacheOnly, true);
  const words = text(renderKit(ShellBanner, feed));
  assert.match(words, /offline/i);
  assert.match(words, /copy on this device/i);
  assert.doesNotMatch(words, /writes? wait|outbox|send|safe/i);
});

test("online is no banner", () => {
  assert.deepEqual(bannerFor({ online: true }), { mode: "", cacheOnly: false, waiting: 0 });
  assert.equal(text(renderKit(ShellBanner, bannerFor({ online: true }))).trim(), "");
});

test("an outbox handed in (a store is open) gets the outbox words with its depth, not cacheOnly", () => {
  const feed = bannerFor({ online: false, outbox: { waiting: 3 } });
  assert.equal(feed.cacheOnly, false);
  assert.match(text(renderKit(ShellBanner, feed)), /3 changes in the outbox/);
});
