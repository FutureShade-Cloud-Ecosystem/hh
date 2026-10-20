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
  isAckable,
  isHouseChannel,
  isSuperseded,
  messageKindWord,
  mergeOlder,
  modeWords,
  modesFor,
  plainSentence,
  queueOrder,
  refusalWords,
  repliesWord,
  streamOrder,
  timeWords,
  totalUnread,
  unreadSignature,
  unreadWord,
  waitedWords,
  youAcked,
  isAdminCaller,
  initialsOf,
  systemEventDetails,
  parseMentions,
  QUICK_REPLIES,
  groupConversationSubtitle,
  dmConversationPrompt,
  groupSystemCreatedMessage,
  filterRoster,
  toggleReactionList,
  canEditMessage,
  formatFileSize,
  formatMembershipRuleLine,
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
  assert.deepEqual(groups.map((g) => g.label), ["Direct messages", "", "Branch", "Approvals"]);
  assert.deepEqual(groups[1].channels.map((c) => c.id), ["c", "e"]);
});

test("a group with no channel is not drawn", () => {
  const groups = channelGroups([ch({ kind: "TEAM" })]);
  assert.deepEqual(groups.map((g) => g.label), [""]);
});

test("unread words: a count, a cap, and a mention", () => {
  assert.equal(unreadWord(ch()), "");
  assert.equal(unreadWord(ch({ unread_count: 3 })), "3 unread");
  assert.equal(unreadWord(ch({ unread_count: 140 })), "99+ unread");
  assert.equal(unreadWord(ch({ unread_count: 2, mentioned: true })), "2 unread, you are mentioned");
  assert.equal(totalUnread([ch({ unread_count: 2 }), ch({ unread_count: 5 })]), 7);
});

test("a channel's title uses its display name when given, or maps core slugs to dealer words", () => {
  assert.equal(channelTitle(ch({ name: "Yard" })), "Yard");
  assert.equal(channelTitle(ch({ slug: "house-julian", name: "#house-julian" })), "House, Julian");
  assert.equal(channelTitle(ch({ slug: "portal-inbox", name: "#portal-inbox" })), "Portal inbox");
  assert.equal(channelTitle(ch({ slug: "alerts-all", name: "#alerts-all" })), "All alerts");
  assert.equal(channelTitle(ch({ slug: "house-unassigned", name: "#house-unassigned" })), "Unassigned");
  assert.equal(channelTitle(ch({ slug: "drivers-point-loma", name: "#drivers-point-loma" })), "Drivers, Point Loma");
  assert.equal(channelTitle(ch({ slug: "yard-point-loma", name: "#yard-point-loma" })), "Yard, Point Loma");
  assert.equal(channelTitle(ch({ slug: "yard-plm", name: "#yard-point-loma" })), "Yard, Point Loma");
  assert.equal(channelTitle(ch({ slug: "house-plm", name: "#house-point-loma" })), "House, Point Loma");
  assert.equal(channelTitle(ch({ slug: "house-coastal-bend", name: "#house-coastal-bend" })), "House, Coastal Bend");
  assert.equal(channelTitle(ch({ slug: "house-coastal-bend", name: "" })), "House, Coastal Bend");
  // No caller feeds a branch map, so the title takes the channel alone (r3 P3).
  assert.equal(channelTitle.length, 1);
  assert.equal(channelTitle(ch({ slug: "office", name: "#office" })), "Office");
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

test("time words: a clock time today, a short date and time before, a year only when it is not this one", () => {
  const now = new Date(2025, 8, 10, 15, 0).getTime();
  const iso = (y, m, d, h, min) => new Date(y, m, d, h, min).toISOString();
  assert.equal(timeWords(iso(2025, 8, 10, 10, 40), now), "10:40 AM");
  assert.equal(timeWords(iso(2025, 8, 3, 6, 5), now), "Sep 3, 6:05 AM");
  assert.equal(timeWords(iso(2024, 11, 24, 13, 15), now), "Dec 24, 2024, 1:15 PM");
  assert.equal(timeWords("not a time", now), "");
});

test("a message asks for an acknowledgement only when the core flagged it, in any room", () => {
  assert.equal(isAckable(msg({ requires_ack: true })), true);
  assert.equal(isAckable(msg({ kind: "EVENT" })), false, "the escalation room's mirror is not asked of anyone: acking is done on the home message");
  assert.equal(isAckable(msg({ kind: "TEXT" })), false, "a person's words are not asked to be acknowledged");
});

test("a superseded notice is one a newer fact replaced, by either of the core's two markers", () => {
  assert.equal(isSuperseded(msg()), false);
  assert.equal(isSuperseded(msg({ superseded_by: "m9" })), true);
  assert.equal(isSuperseded(msg({ superseded_at: "2025-01-01T00:00:00Z" })), true);
});

test("you acknowledged when your user id is among the acks", () => {
  const progress = { message_id: "m", required: 3, acked: 1, remaining: 2, complete: false, acks: [{ message_id: "m", user_id: "u1", method: "TAP", acked_at: "2025-01-01T00:00:00Z" }] };
  assert.equal(youAcked(progress, "u1"), true);
  assert.equal(youAcked(progress, "u2"), false);
  assert.equal(youAcked(null, "u1"), false);
});

test("the unread signature is the same for the same counts in any order, and moves when a count, a mention or a latest time does", () => {
  const a = { channel_id: "a", unread_count: 1, mentioned: false, latest_at: "2025-01-01T00:00:00Z" };
  const b = { channel_id: "b", unread_count: 2, mentioned: false, latest_at: "2025-01-02T00:00:00Z" };
  const base = unreadSignature({ total: 3, channels: [a, b] });
  assert.equal(unreadSignature({ total: 3, channels: [b, a] }), base);
  assert.notEqual(unreadSignature({ total: 4, channels: [{ ...a, unread_count: 2 }, b] }), base);
  assert.notEqual(unreadSignature({ total: 3, channels: [{ ...a, mentioned: true }, b] }), base);
  assert.notEqual(unreadSignature({ total: 3, channels: [a, { ...b, latest_at: "2025-01-03T00:00:00Z" }] }), base);
  assert.equal(unreadSignature({ total: 0, channels: [] }), unreadSignature({ total: 0, channels: null }));
});

test("admin caller: general manager, dealer admin, and tier 1 are admins; others are not", () => {
  assert.equal(isAdminCaller({ roleCode: "general_manager", tier: 1 }), true);
  assert.equal(isAdminCaller({ roleCode: "dealer_admin", tier: 2 }), true);
  assert.equal(isAdminCaller({ roleCode: "custom_role", tier: 1 }), true);
  assert.equal(isAdminCaller({ roleCode: "inside_sales", tier: 3 }), false);
  assert.equal(isAdminCaller({ roleCode: "yard_manager", tier: 2 }), false);
  assert.equal(isAdminCaller({ roleCode: "payables_receivables", tier: 3 }), false);
  assert.equal(isAdminCaller(null), false);
  assert.equal(isAdminCaller(undefined), false);
});

test("initials of: two letters for multi-word names, single letter for single words, trimmed", () => {
  assert.equal(initialsOf("Marcus Webb"), "MW");
  assert.equal(initialsOf("Priya Shah"), "PS");
  assert.equal(initialsOf("Elena Ruiz"), "ER");
  assert.equal(initialsOf("Dana"), "D");
  assert.equal(initialsOf("Inside Sales (demo)"), "IS");
  assert.equal(initialsOf("Yard manager (demo)"), "YM");
  assert.equal(initialsOf(""), "");
});

test("system event details: never display System author name, derives source, icon, and deep link", () => {
  const yardEvt = systemEventDetails({
    kind: "EVENT",
    channel_slug: "yard-point-loma",
    ref: { topic: "yard.picking", entity: "order:SO-PLM-1042" },
  });
  assert.equal(yardEvt.source, "Yard, Point Loma");
  assert.equal(yardEvt.icon, "truck");
  assert.deepEqual(yardEvt.actionPill, { label: "Open SO-PLM-1042", entity: "order:SO-PLM-1042" });
  assert.notEqual(yardEvt.source.toLowerCase(), "system");

  const stockEvt = systemEventDetails({
    kind: "EVENT",
    ref: { topic: "stock.receipt", entity: "po:PO-PLM-136" },
  });
  assert.equal(stockEvt.source, "Receiving");
  assert.equal(stockEvt.icon, "box");
  assert.deepEqual(stockEvt.actionPill, { label: "Open PO-PLM-136", entity: "po:PO-PLM-136" });
  assert.notEqual(stockEvt.source.toLowerCase(), "system");

  const creditEvt = systemEventDetails({
    kind: "EVENT",
    ref: { topic: "order.credit_hold", entity: "order:SO-1042" },
  });
  assert.equal(creditEvt.source, "Credit");
  assert.equal(creditEvt.icon, "shield");
  assert.deepEqual(creditEvt.actionPill, { label: "Open SO-1042", entity: "order:SO-1042" });
  assert.notEqual(creditEvt.source.toLowerCase(), "system");
});

test("parse mentions: extracts @mentions as chips and keeps text intact", () => {
  const tokens = parseMentions("Can @Elena check the short pallet for @Marcus today?");
  assert.deepEqual(tokens, [
    { type: "text", value: "Can " },
    { type: "mention", value: "@Elena" },
    { type: "text", value: " check the short pallet for " },
    { type: "mention", value: "@Marcus" },
    { type: "text", value: " today?" },
  ]);
  assert.deepEqual(parseMentions("No mentions here"), [
    { type: "text", value: "No mentions here" },
  ]);
});

test("quick replies: four standard chips", () => {
  assert.deepEqual(QUICK_REPLIES, ["On it", "Done", "Need a hand", "Call me"]);
});

test("C2 logic: group direct message subtitle matches board wording", () => {
  assert.equal(groupConversationSubtitle(4, false), "A group direct message, 4 people including you");
  assert.equal(groupConversationSubtitle(3, true), "A named group, 3 people including you");
});

test("C2 logic: empty DM start prompt matches board wording", () => {
  const prompt = dmConversationPrompt("Tom Alvarez");
  assert.equal(prompt.title, "This is the start of your conversation with Tom Alvarez");
  assert.equal(prompt.subtitle, "Only the two of you see it. Photos and files you share here show under Files and photos.");
});

test("C2 logic: group creation system text matches board wording", () => {
  const line = groupSystemCreatedMessage("Saturday runs", ["Marcus Webb", "Priya Shah", "Elena Ruiz"]);
  assert.equal(line, "You created the group Saturday runs and added Marcus Webb, Priya Shah and Elena Ruiz.");
});

test("C2 logic: roster filter searches across name, role, branch and excludes caller", () => {
  const roster = [
    { user_id: "u1", display_name: "Marcus Webb", role_name: "Yard staff", branch_name: "Point Loma" },
    { user_id: "u2", display_name: "Priya Shah", role_name: "Dispatch", branch_name: "Point Loma" },
    { user_id: "u3", display_name: "Tom Alvarez", role_name: "Driver", branch_name: "Julian" },
  ];
  const matched = filterRoster(roster, "julian", "u1");
  assert.equal(matched.length, 1);
  assert.equal(matched[0].display_name, "Tom Alvarez");

  const excludeMe = filterRoster(roster, "", "u1");
  assert.equal(excludeMe.length, 2);
  assert.equal(excludeMe.some((r) => r.user_id === "u1"), false);
});

test("C3 logic: reactions list toggle adds or removes user reaction correctly", () => {
  const list = [{ emoji: "Thanks", count: 1, mine: false, users: ["u2"] }];
  const added = toggleReactionList(list, "Thanks", "u1");
  assert.equal(added.find((r) => r.emoji === "Thanks")?.count, 2);
  assert.equal(added.find((r) => r.emoji === "Thanks")?.mine, true);

  const removed = toggleReactionList(added, "Thanks", "u1");
  assert.equal(removed.find((r) => r.emoji === "Thanks")?.count, 1);
  assert.equal(removed.find((r) => r.emoji === "Thanks")?.mine, false);
});

test("C3 logic: canEditMessage checks 10 minute window, author match, non-ackable, and TEXT kind", () => {
  const now = 1000000;
  const recentMsg = {
    id: "m1",
    author_id: "me",
    kind: "TEXT",
    created_at: new Date(now - 5 * 60 * 1000).toISOString(),
    requires_ack: false,
  };
  assert.equal(canEditMessage(recentMsg, "me", now), true);
  assert.equal(canEditMessage(recentMsg, "other", now), false);

  const oldMsg = { ...recentMsg, created_at: new Date(now - 15 * 60 * 1000).toISOString() };
  assert.equal(canEditMessage(oldMsg, "me", now), false);

  const ackMsg = { ...recentMsg, requires_ack: true };
  assert.equal(canEditMessage(ackMsg, "me", now), false);

  const eventMsg = { ...recentMsg, kind: "EVENT" };
  assert.equal(canEditMessage(eventMsg, "me", now), false);

  const deletedMsg = { ...recentMsg, deleted_at: new Date(now).toISOString() };
  assert.equal(canEditMessage(deletedMsg, "me", now), false);
});

test("C4 logic: membership rule line formatting for automatic joins and leaves", () => {
  const line = formatMembershipRuleLine({
    roles: ["Yard staff", "Driver"],
    branch: "Point Loma",
  });
  assert.equal(line, "Role Yard staff, Driver at Point Loma");
});

test("C6 logic: formatFileSize formats bytes to KB or MB", () => {
  assert.equal(formatFileSize(84 * 1024), "84 KB");
  assert.equal(formatFileSize(2 * 1024 * 1024), "2 MB");
});


