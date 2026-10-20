// The live feed's state machine, pinned with a fake stream and a fake clock: what the screen says
// while the stream connects, runs, drops and comes back, and that a drop is never silent. No page,
// no network: the feed takes its ticket mint, its stream and its timers from the caller.
import { test } from "node:test";
import assert from "node:assert/strict";
import { LiveFeed, retryDelayMs, liveWords, POLL_LIVE_MS, POLL_FALLBACK_MS } from "../src/live.ts";

function clock() {
  let now = 0;
  let next = 1;
  const timers = new Map();
  return {
    timers: {
      set: (fn, ms) => {
        const id = next++;
        timers.set(id, { at: now + ms, fn });
        return id;
      },
      clear: (id) => void timers.delete(id),
    },
    // Advance, running every timer that falls due, earliest first.
    async tick(ms) {
      const end = now + ms;
      for (;;) {
        const due = [...timers.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
        if (due === undefined) break;
        timers.delete(due[0]);
        now = due[1].at;
        due[1].fn();
        await Promise.resolve();
        await Promise.resolve();
      }
      now = end;
    },
    pending: () => timers.size,
  };
}

function fakeStream() {
  const opened = [];
  return {
    opened,
    open(url) {
      const handlers = {};
      const source = {
        url,
        closed: false,
        addEventListener: (type, fn) => void (handlers[type] = fn),
        close: () => void (source.closed = true),
        emit: (type, data = "{}") => handlers[type]?.({ data }),
        open: () => handlers.open?.({}),
        fail: () => handlers.error?.({}),
      };
      opened.push(source);
      return source;
    },
  };
}

const ticketOk = (n = { count: 0 }) => async () => ({ ok: true, value: { ticket: `t${++n.count}`, expires_in_seconds: 60 } });

function harness(over = {}) {
  const c = clock();
  const s = fakeStream();
  const states = [];
  const nudges = [];
  const feed = new LiveFeed({
    mintTicket: over.mintTicket ?? ticketOk(),
    open: s.open,
    timers: c.timers,
    onState: (state) => states.push(state),
    onNudge: (kind) => nudges.push(kind),
  });
  return { c, s, states, nudges, feed };
}

const settle = async () => {
  for (let i = 0; i < 6; i += 1) await Promise.resolve();
};

test("the retry delay doubles from two seconds and stops at thirty", () => {
  assert.deepEqual([0, 1, 2, 3, 4, 5, 9].map(retryDelayMs), [2000, 4000, 8000, 16000, 30000, 30000, 30000]);
});

test("a stream with a ticket connects, then says it is live when it opens", async () => {
  const h = harness();
  h.feed.start();
  assert.equal(h.states.at(-1), "connecting");
  await settle();
  assert.equal(h.s.opened.length, 1);
  assert.match(h.s.opened[0].url, /\/api\/v1\/messaging\/stream\?ticket=t1$/);
  h.s.opened[0].open();
  assert.equal(h.states.at(-1), "live");
});

test("a message, an unread nudge and an approval decision each ask the screen to read again", async () => {
  const h = harness();
  h.feed.start();
  await settle();
  h.s.opened[0].open();
  h.s.opened[0].emit("message");
  h.s.opened[0].emit("unread");
  h.s.opened[0].emit("approval.decided");
  assert.deepEqual(h.nudges, ["message", "unread", "approval"]);
});

test("a drop is said, the stream closes, and a new ticket reconnects after the delay", async () => {
  const n = { count: 0 };
  const h = harness({ mintTicket: ticketOk(n) });
  h.feed.start();
  await settle();
  h.s.opened[0].open();
  h.s.opened[0].fail();
  assert.equal(h.states.at(-1), "reconnecting");
  assert.equal(h.s.opened[0].closed, true);
  await h.c.tick(1999);
  assert.equal(h.s.opened.length, 1);
  await h.c.tick(1);
  await settle();
  assert.equal(h.s.opened.length, 2);
  assert.match(h.s.opened[1].url, /ticket=t2$/);
  h.s.opened[1].open();
  assert.equal(h.states.at(-1), "live");
});

test("while the stream is down the feed asks the screen to read on the fallback cadence", async () => {
  const h = harness();
  h.feed.start();
  await settle();
  h.s.opened[0].open();
  h.s.opened[0].fail();
  await h.c.tick(POLL_FALLBACK_MS);
  assert.ok(h.nudges.includes("poll"), "a poll nudge arrives while reconnecting");
});

test("a live stream is still read on the slow cadence, because a stuck stream drops events", async () => {
  const h = harness();
  h.feed.start();
  await settle();
  h.s.opened[0].open();
  await h.c.tick(POLL_LIVE_MS);
  assert.deepEqual(h.nudges.filter((k) => k === "poll").length, 1);
});

test("a core that does not issue stream tickets (501) falls back to reading every few seconds, and says so", async () => {
  const h = harness({ mintTicket: async () => ({ ok: false, status: 501, error: "stream tickets not enabled; use the Bearer stream" }) });
  h.feed.start();
  await settle();
  assert.equal(h.states.at(-1), "polling");
  assert.equal(h.s.opened.length, 0);
  await h.c.tick(POLL_FALLBACK_MS);
  assert.ok(h.nudges.includes("poll"));
});

test("no answer at all from the ticket mint is offline, and the feed keeps trying", async () => {
  let calls = 0;
  const h = harness({ mintTicket: async () => (++calls < 3 ? { ok: false, error: "network" } : { ok: true, value: { ticket: "late", expires_in_seconds: 60 } }) });
  h.feed.start();
  await settle();
  assert.equal(h.states.at(-1), "offline");
  await h.c.tick(2000);
  await settle();
  assert.equal(h.states.at(-1), "offline");
  await h.c.tick(4000);
  await settle();
  assert.equal(h.s.opened.length, 1);
});

test("retry now reconnects at once instead of waiting out the delay", async () => {
  const h = harness();
  h.feed.start();
  await settle();
  h.s.opened[0].open();
  h.s.opened[0].fail();
  h.feed.retryNow();
  await settle();
  assert.equal(h.s.opened.length, 2);
});

test("stop closes the stream, clears every timer and ends the nudges", async () => {
  const h = harness();
  h.feed.start();
  await settle();
  h.s.opened[0].open();
  h.feed.stop();
  assert.equal(h.s.opened[0].closed, true);
  assert.equal(h.c.pending(), 0);
  h.s.opened[0].emit("message");
  assert.deepEqual(h.nudges, []);
});

test("a reconnect that succeeds resets the delay", async () => {
  const h = harness();
  h.feed.start();
  await settle();
  h.s.opened[0].fail();
  await h.c.tick(2000);
  await settle();
  h.s.opened[1].open();
  h.s.opened[1].fail();
  await h.c.tick(2000);
  await settle();
  assert.equal(h.s.opened.length, 3, "after a good open the next delay is two seconds again");
});

test("the words for each state", () => {
  assert.match(liveWords("live"), /Live/);
  assert.match(liveWords("connecting"), /Connecting/);
  assert.match(liveWords("reconnecting"), /Reconnecting/);
  assert.equal(liveWords("polling"), "Updates while this page is open");
  assert.match(liveWords("offline"), /Offline/);
  assert.match(liveWords("offline"), /nothing you send is saved/i);
});
