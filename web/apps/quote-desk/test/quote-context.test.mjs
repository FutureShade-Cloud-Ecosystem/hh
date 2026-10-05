// The quote context: the one mechanism every read and write of a quote leans on, so its few rules are pinned
// here on their own, apart from any screen.
import { test } from "node:test";
import assert from "node:assert/strict";
import { QuoteContext, QuoteScope, waitUpTo } from "../src/quote-context.js";

const later = (value) => new Promise((resolve) => setTimeout(() => resolve(value), 5));

test("an answer comes back while the context is live", async () => {
  const ctx = new QuoteContext("q-1", 1);
  assert.equal(await ctx.guarded(later("answer")), "answer");
  assert.equal(ctx.stale, false);
});

test("an answer is dropped when the context is cancelled while the call is out", async () => {
  const ctx = new QuoteContext("q-1", 1);
  const call = ctx.guarded(later("late"));
  ctx.cancel();
  assert.equal(await call, undefined);
  assert.equal(ctx.stale, true);
});

test("a context cancelled before the call answers drops it, and one cancelled after the answer keeps it", async () => {
  const ctx = new QuoteContext("q-1", 1);
  const kept = await ctx.guarded(later("kept"));
  ctx.cancel();
  assert.equal(kept, "kept");
  assert.equal(await ctx.guarded(Promise.resolve("now")), undefined);
});

test("opening a quote cancels the context of the quote before and counts the epochs", () => {
  const scope = new QuoteScope();
  const first = scope.open("q-1");
  const second = scope.open("q-2");
  assert.equal(first.stale, true);
  assert.equal(second.stale, false);
  assert.equal(scope.current, second);
  assert.equal(second.id, "q-2");
  assert.ok(second.epoch > first.epoch);
});

test("opening the same quote again is a new context: work still out for the first visit is dropped", async () => {
  const scope = new QuoteScope();
  const visit = scope.open("q-1");
  const call = visit.guarded(later("old visit"));
  scope.open("q-2");
  const back = scope.open("q-1");
  assert.equal(await call, undefined);
  assert.equal(back.stale, false);
  assert.notEqual(back, visit);
});

test("a newer read of the same quote supersedes an older one still out: the older answer is dropped", async () => {
  const ctx = new QuoteContext("q-1", 1);
  const older = ctx.startRead();
  const call = ctx.guardedRead(older, later("older"));
  const newer = ctx.startRead();
  assert.ok(newer > older);
  assert.equal(await call, undefined);
  assert.equal(await ctx.guardedRead(newer, later("newer")), "newer");
});

test("a read answered before a newer one began is kept", async () => {
  const ctx = new QuoteContext("q-1", 1);
  const first = ctx.startRead();
  assert.equal(await ctx.guardedRead(first, Promise.resolve("first")), "first");
  ctx.startRead();
});

test("waitUpTo says whether the work finished inside the bound", async () => {
  assert.equal(await waitUpTo(500, later("done")), true);
  assert.equal(await waitUpTo(20, new Promise(() => undefined)), false);
});

test("a read cancelled while it was out is dropped even when it is the newest read", async () => {
  const ctx = new QuoteContext("q-1", 1);
  const seq = ctx.startRead();
  const call = ctx.guardedRead(seq, later("late"));
  ctx.cancel();
  assert.equal(await call, undefined);
});

test("guarded hands the dropped answer to onLate, and only a dropped one", async () => {
  const ctx = new QuoteContext("q-1", 1);
  const late = [];
  assert.equal(await ctx.guarded(Promise.resolve("kept"), (answer) => late.push(answer)), "kept");
  const call = ctx.guarded(later("dropped"), (answer) => late.push(answer));
  ctx.cancel();
  assert.equal(await call, undefined);
  assert.deepEqual(late, ["dropped"]);
});
