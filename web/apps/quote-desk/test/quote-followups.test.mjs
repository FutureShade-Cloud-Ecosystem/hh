// The follow-ups panel's pure tables, ported from the ERP's _quote-followups
// test and adapted for HH. Two claims are pinned: the timeline reads in
// cadence order whatever order the server sends, and the panel never says a
// text was SENT when it was only queued. The contact sentence answers "has
// this customer heard from us, and when next" from the events and upcoming.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  channelWords,
  contactLine,
  queuedNotice,
  shareErrorMessage,
  stateLabel,
  summaryLine,
  timeline,
} from "../src/_quote-followups.js";

const HISTORY = {
  quote_id: "q-1",
  automated: true,
  plan: { quote_id: "q-1", enabled: true, channel: "BOTH", offsets_days: [3, 7, 14], started_at: "2026-08-01T13:00:00Z" },
  events: [
    { step_day: 3, channel: "SMS", status: "SKIPPED", reason: "no SMS consent", scheduled_for: "2026-08-04T13:00:00Z", occurred_at: "2026-08-04T14:02:00Z" },
    { step_day: 3, channel: "EMAIL", status: "SENT", scheduled_for: "2026-08-04T13:00:00Z", occurred_at: "2026-08-04T14:01:00Z" },
  ],
  upcoming: [
    { step_day: 14, channel: "EMAIL", due_at: "2026-08-15T13:00:00Z", scheduled: true },
    { step_day: 7, channel: "SMS", due_at: "2026-08-08T13:00:00Z", scheduled: true },
  ],
};

test("timeline merges past and future into one cadence ordered list", () => {
  assert.deepEqual(timeline(HISTORY).map((r) => `${r.day}/${r.channel}`), ["3/EMAIL", "3/SMS", "7/SMS", "14/EMAIL"]);
});

test("timeline marks only the not yet happened steps as future", () => {
  assert.deepEqual(timeline(HISTORY).map((r) => r.future), [false, false, true, true]);
});

test("timeline carries the skip reason through", () => {
  assert.equal(timeline(HISTORY).find((r) => r.state === "SKIPPED").detail, "no SMS consent");
});

test("timeline tolerates an empty or absent history", () => {
  assert.deepEqual(timeline(null), []);
  assert.deepEqual(timeline(undefined), []);
  assert.deepEqual(timeline({ quote_id: "q", events: [], upcoming: [], automated: false }), []);
});

test("a SENT step reads as Queued, never Sent", () => {
  assert.equal(stateLabel("SENT"), "Queued");
  assert.equal(stateLabel("SKIPPED"), "Not sent");
  assert.equal(stateLabel("CANCELLED"), "Cancelled");
  assert.equal(stateLabel("SCHEDULED"), "Scheduled");
});

test("queuedNotice says queued, plainly", () => {
  assert.equal(queuedNotice(), "Text queued.");
});

test("summaryLine tells no cadence from a cadence switched off", () => {
  assert.match(summaryLine({ quote_id: "q", events: [], upcoming: [], automated: false }), /No follow-up cadence/);
  const off = summaryLine({ ...HISTORY, automated: false, plan: { ...HISTORY.plan, enabled: false, disabled_why: "turned off by staff" } });
  assert.match(off, /OFF/);
  assert.match(off, /turned off by staff/);
  assert.match(off, /Nothing further will be sent/);
});

test("summaryLine states the cadence in days and the channel in words", () => {
  const on = summaryLine(HISTORY);
  assert.match(on, /day 3, day 7, day 14/);
  assert.match(on, /email and text/);
});

test("no sentence carries an em or en dash", () => {
  const lines = [summaryLine(null), summaryLine(HISTORY), shareErrorMessage(409, ""), shareErrorMessage(503, ""), contactLine(HISTORY, String), contactLine(null, String)];
  for (const line of lines) assert.doesNotMatch(line, /[\u2013\u2014]/);
});

test("channelWords never leaks an enum", () => {
  assert.equal(channelWords("SMS"), "text");
  assert.equal(channelWords("EMAIL"), "email");
  assert.equal(channelWords("BOTH"), "email and text");
});

test("shareErrorMessage keeps the server's own words, with a fallback only when it sent none", () => {
  assert.equal(shareErrorMessage(409, "the customer has not opted in to text messages"), "the customer has not opted in to text messages");
  assert.match(shareErrorMessage(409, ""), /not agreed to receive texts/);
  assert.match(shareErrorMessage(503, ""), /not configured/);
  assert.match(shareErrorMessage(403, ""), /belongs to another branch|do not have access/);
  assert.match(shareErrorMessage(404, ""), /no longer exists/);
  assert.equal(shareErrorMessage(500, ""), "Could not queue the text.");
});

test("contactLine: heard from us (the latest queued step) and when next (the earliest upcoming)", () => {
  const when = (iso) => `@${iso}`;
  const line = contactLine(HISTORY, when);
  assert.match(line, /Heard from us: day 3 email, queued @2026-08-04T14:01:00Z; next: day 7 text, @2026-08-08T13:00:00Z$/);
});

test("contactLine: a skipped step is not hearing from us; nothing sent says so; nothing next says so", () => {
  const when = (iso) => iso;
  const skippedOnly = { ...HISTORY, events: [HISTORY.events[0]] };
  assert.match(contactLine(skippedOnly, when), /^Not heard from us yet; next:/);
  const noneNext = { ...HISTORY, upcoming: [] };
  assert.match(contactLine(noneNext, when), /; nothing further is scheduled$/);
  assert.equal(contactLine({ quote_id: "q", events: [], upcoming: [], automated: false }, when), "");
  assert.equal(contactLine(null, when), "");
});
