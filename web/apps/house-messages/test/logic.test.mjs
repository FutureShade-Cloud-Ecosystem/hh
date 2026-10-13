// House Messages rules, pinned: the composer's size rule, the channel groups and their words, the
// order a stream reads in, the forced acknowledgement bubble's persistence rule, the ack arithmetic
// in words, the house queue's order, the notify modes a channel offers, and the sentences a refusal
// reads in. Every case is a rule that would be quietly wrong on screen.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  MAX_BODY_BYTES,
  MUTE_REFUSAL,
  ackProgressLabel,
  bodyBytes,
  bubbleFor,
  bubblePersistsAfter,
  canMute,
  channelGroups,
  channelTitle,
  composerState,
  eventWhy,
  isHouseChannel,
  messageKindWord,
  mergeOlder,
  modeWords,
  modesFor,
  plainSentence,
  queueOrder,
  refusalWords,
  repliesWord,
  streamOrder,
  totalUnread,
  unreadWord,
  waitedWords,
} from "../src/messages-logic.ts";

const ch = (over = {}) => ({
  id: "c1",
  kind: "TEAM",
  name: "Yard",
  topic: "",
  tier: "STANDARD",
  is_archived: false,
  created_at: "2025-01-01T00:00:00Z",
  unread_count: 0,
  mentioned: false,
  latest_at: null,
  ...over,
});
const msg = (over = {}) => ({
  id: "m1",
  channel_id: "c1",
  kind: "TEXT",
  body: "hello",
  reply_count: 0,
  created_at: "2025-01-01T10:00:00Z",
  ...over,
});

test("the size rule counts bytes, not characters, because the core cuts a longer body off", () => {
  assert.equal(MAX_BODY_BYTES, 8192);
  assert.equal(bodyBytes("abc"), 3);
  assert.equal(bodyBytes("é"), 2);
  assert.equal(bodyBytes("\u{1F4E6}"), 4);
});

test("the composer: empty and blank can not send, a body at the limit can, one byte over can not", () => {
  assert.equal(composerState("").canSend, false);
  assert.equal(composerState("   \n ").canSend, false);
  assert.equal(composerState("Pallets staged at door 2").canSend, true);
  assert.equal(composerState("a".repeat(8192)).canSend, true);
  const over = composerState("a".repeat(8193));
  assert.equal(over.canSend, false);
  assert.equal(over.over, 1);
  assert.match(over.words, /cut/);
  assert.match(over.words, /1 byte/);
});

test("the composer counts the trimmed body the way the core does", () => {
  const padded = composerState(`  ${"a".repeat(8192)}  `);
  assert.equal(padded.canSend, true);
  assert.equal(padded.bytes, 8192);
});

test("the composer words say how much room is left only when it is nearly gone", () => {
  assert.equal(composerState("short").words, "");
  assert.match(composerState("a".repeat(8100)).words, /92 bytes left/);
});

test("channel groups: direct messages, rooms, the branch, then approvals, each in the core's order", () => {
  const groups = channelGroups([
    ch({ id: "a", kind: "APPROVAL", name: "Approvals" }),
    ch({ id: "b", kind: "BRANCH", name: "Point Loma" }),
    ch({ id: "c", kind: "TOPIC", name: "Deliveries" }),
    ch({ id: "d", kind: "DM", name: "A colleague" }),
    ch({ id: "e", kind: "TEAM", name: "Yard" }),
  ]);
  assert.deepEqual(groups.map((g) => g.label), ["Direct messages", "Rooms", "Branch", "Approvals"]);
  assert.deepEqual(groups[1].channels.map((c) => c.id), ["c", "e"]);
});

test("a group with no channel is not drawn", () => {
  const groups = channelGroups([ch({ kind: "TEAM" })]);
  assert.deepEqual(groups.map((g) => g.label), ["Rooms"]);
});

test("unread words: a count, a cap, and a mention", () => {
  assert.equal(unreadWord(ch()), "");
  assert.equal(unreadWord(ch({ unread_count: 3 })), "3 unread");
  assert.equal(unreadWord(ch({ unread_count: 140 })), "99+ unread");
  assert.equal(unreadWord(ch({ unread_count: 2, mentioned: true })), "2 unread, you are mentioned");
  assert.equal(totalUnread([ch({ unread_count: 2 }), ch({ unread_count: 5 })]), 7);
});

test("a channel's title is its name, and a nameless one still reads", () => {
  assert.equal(channelTitle(ch({ name: "Yard" })), "Yard");
  assert.equal(channelTitle(ch({ name: "", kind: "DM" })), "Direct message");
  assert.equal(channelTitle(ch({ name: "", kind: "TEAM" })), "Untitled room");
});

test("a house channel is the one whose slug begins house-", () => {
  assert.equal(isHouseChannel(ch({ slug: "house-point-loma" })), true);
  assert.equal(isHouseChannel(ch({ slug: "yard-point-loma" })), false);
  assert.equal(isHouseChannel(ch({})), false);
});

test("the stream reads oldest first, ties broken by id, and the core's newest first order is reversed", () => {
  const page = [
    msg({ id: "m3", created_at: "2025-01-01T12:00:00Z" }),
    msg({ id: "m2", created_at: "2025-01-01T11:00:00Z" }),
    msg({ id: "m1", created_at: "2025-01-01T10:00:00Z" }),
  ];
  assert.deepEqual(streamOrder(page).map((m) => m.id), ["m1", "m2", "m3"]);
  const tie = [msg({ id: "b" }), msg({ id: "a" })];
  assert.deepEqual(streamOrder(tie).map((m) => m.id), ["a", "b"]);
});

test("the stream shows top level messages only, replies belong to a thread", () => {
  const rows = [msg({ id: "r", thread_root_id: "m1" }), msg({ id: "m1" })];
  assert.deepEqual(streamOrder(rows).map((m) => m.id), ["m1"]);
});

test("an older page merges under what is held, without a duplicate", () => {
  const held = [msg({ id: "m2", created_at: "2025-01-01T11:00:00Z" })];
  const older = [msg({ id: "m2", created_at: "2025-01-01T11:00:00Z" }), msg({ id: "m1", created_at: "2025-01-01T10:00:00Z" })];
  assert.deepEqual(mergeOlder(held, older).map((m) => m.id), ["m1", "m2"]);
});

test("replies word: none, one, many", () => {
  assert.equal(repliesWord(0), "Reply");
  assert.equal(repliesWord(1), "1 reply");
  assert.equal(repliesWord(4), "4 replies");
});

test("a message's kind in words: text is the person's, an event is the system's, an approval is a card", () => {
  assert.equal(messageKindWord(msg()), "");
  assert.equal(messageKindWord(msg({ kind: "EVENT" })), "System notice");
  assert.equal(messageKindWord(msg({ kind: "APPROVAL" })), "Approval");
});

test("an event says why it reached you only from the rules the core named", () => {
  assert.equal(eventWhy(undefined), "");
  assert.equal(eventWhy({ topic: "payment.received" }), "");
  assert.equal(eventWhy({ topic: "payment.received", rules: ["OWN_ORDER"] }), "Reached you because: your own order");
  assert.equal(
    eventWhy({ topic: "payment.received", rules: ["ONLINE", "CROSS_BRANCH"] }),
    "Reached you because: an online payment, a payment at another branch",
  );
});

test("the bubble: nothing to ack shows nothing", () => {
  assert.equal(bubbleFor([]), null);
  assert.equal(bubbleFor(null), null);
});

test("the bubble picks the oldest live forced ack message, never a superseded one", () => {
  const rows = [
    msg({ id: "new", requires_ack: true, created_at: "2025-01-02T00:00:00Z" }),
    msg({ id: "old", requires_ack: true, created_at: "2025-01-01T00:00:00Z" }),
    msg({ id: "stale", requires_ack: true, created_at: "2024-12-01T00:00:00Z", superseded_by: "new" }),
    msg({ id: "plain", created_at: "2023-01-01T00:00:00Z" }),
  ];
  assert.equal(bubbleFor(rows).id, "old");
});

test("the bubble persists after one ack while another is waiting", () => {
  const rows = [msg({ id: "a", requires_ack: true }), msg({ id: "b", requires_ack: true, created_at: "2025-01-02T00:00:00Z" })];
  assert.equal(bubblePersistsAfter(rows, "a"), true);
  assert.equal(bubblePersistsAfter(rows.slice(0, 1), "a"), false);
});

test("ack arithmetic in words, and the zero required case is not a completion", () => {
  assert.equal(ackProgressLabel(null), "No one is assigned to acknowledge this.");
  assert.equal(ackProgressLabel({ message_id: "m", required: 0, acked: 0, remaining: 0, complete: false, acks: [] }), "No one is assigned to acknowledge this.");
  assert.equal(ackProgressLabel({ message_id: "m", required: 4, acked: 1, remaining: 3, complete: false, acks: [] }), "1 of 4 acknowledged · 3 to go");
  assert.equal(ackProgressLabel({ message_id: "m", required: 4, acked: 4, remaining: 0, complete: true, acks: [] }), "Acknowledged by everyone · 4 of 4");
});

test("the house queue: unclaimed first, the longest waiting first, claimed after", () => {
  const rows = [
    { id: "claimed", first_at: "2025-01-01T00:00:00Z", claimed_by: "u" },
    { id: "late", first_at: "2025-01-03T00:00:00Z" },
    { id: "early", first_at: "2025-01-02T00:00:00Z" },
  ];
  assert.deepEqual(queueOrder(rows).map((r) => r.id), ["early", "late", "claimed"]);
});

test("waiting words: minutes, hours, days, and nothing for a bad time", () => {
  const now = Date.parse("2025-01-05T12:00:00Z");
  assert.equal(waitedWords("2025-01-05T11:30:00Z", now), "30 min");
  assert.equal(waitedWords("2025-01-05T09:00:00Z", now), "3 h");
  assert.equal(waitedWords("2025-01-02T12:00:00Z", now), "3 d");
  assert.equal(waitedWords("not a time", now), "");
});

test("an escalation channel offers all and mentions, never muted, and says why", () => {
  assert.equal(canMute("ESCALATION"), false);
  assert.equal(canMute("FIREHOSE"), true);
  assert.deepEqual(modesFor("ESCALATION"), ["ALL", "MENTIONS"]);
  assert.deepEqual(modesFor("STANDARD"), ["ALL", "MENTIONS", "MUTED"]);
  assert.match(MUTE_REFUSAL, /escalation/i);
  assert.equal(modeWords("MENTIONS"), "Mentions only");
});

test("a server sentence loses its module prefix and a refusal keeps the core's words", () => {
  assert.equal(plainSentence("messaging: nope"), "nope");
  assert.equal(plainSentence("escalation channels cannot be muted"), "escalation channels cannot be muted");
  assert.equal(refusalWords("The post", "message body is empty", 400), "message body is empty");
});

test("refusals with no sentence still say who and what", () => {
  assert.match(refusalWords("The post", "", null), /could not reach the server/);
  assert.match(refusalWords("The post", "", 0), /could not reach the server/);
  assert.match(refusalWords("The post", "", 401), /session ended/);
  assert.match(refusalWords("The post", "", 403), /not a member/);
  assert.match(refusalWords("The post", "boom", 500), /nothing changed/);
  assert.match(refusalWords("The post", "", 404), /no longer there/);
});
