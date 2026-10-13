// The quick actions' pure decisions, on their own: where an action goes on
// this box, the one word the bar draws, the icon the label names, the intent
// channel a click speaks on, and the whitelist projection over the wire. No
// browser, no network. Every case is a fact the core holds (the registry, the
// gate order) or the original screen carried (DIB-434).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  INTENT_POS_NEW_TICKET,
  actionHref,
  actionIconId,
  findAction,
  savedWords,
  wordOf,
} from "../src/quick-logic.ts";
import { toQuickAction, toQuickActionsMine } from "../src/quick-api.ts";
import {
  INTENT_EVENT,
  clearPendingIntent,
  peekPendingIntent,
  publishIntent,
  takeIntent,
} from "../src/quick-intent.ts";

const newSale = {
  action_id: "sale.new",
  label: "+ Sale",
  glyph: "＋",
  title: "Start a new counter sale at the till.",
  unit: "sales",
  screen: "pos",
  intent: INTENT_POS_NEW_TICKET,
};

// The intent channel touches `window`; the Node runner has none. A three-field
// stub is enough: the channel only ever dispatches an event.
if (typeof globalThis.window === "undefined") {
  globalThis.window = {
    dispatchEvent: () => true,
    addEventListener: () => {},
    removeEventListener: () => {},
  };
}

test("an action opens the box's own app for the destination the core names", () => {
  // The core names the destination in the shell's own keys (unit sales, screen
  // pos). On this box that surface is the till app at its deployment prefix.
  assert.equal(actionHref(newSale), "/till/");
  // A destination no app serves yet answers null: the landing says so in
  // words instead of drawing a dead button, the same honesty the needs rows
  // carry for surfaces this box does not serve.
  assert.equal(actionHref({ ...newSale, unit: "books", screen: "ledger" }), null);
  assert.equal(actionHref({ ...newSale, unit: "", screen: "" }), null);
});

test("findAction cannot reach an action the server withheld", () => {
  // The role gate and the admin toggle are resolved server side; the client is
  // handed a list. A future palette looking an action up by id gets null for
  // anything not in that list, never a catalog entry it could still fire.
  assert.equal(findAction([], "sale.new"), null);
  assert.equal(findAction([newSale], "sale.new")?.action_id, "sale.new");
  assert.equal(findAction([newSale], "quote.new"), null);
});

test("the visible word loses one leading plus; the wire label stays byte for byte", () => {
  assert.equal(wordOf("+ Sale"), "Sale");
  assert.equal(wordOf("Sale"), "Sale");
  assert.equal(wordOf(""), "");
  const action = toQuickAction(newSale);
  assert.equal(action.label, "+ Sale", "the wire field is untouched");
});

test("the mark a lead-plus action draws is the add mark; every other action draws its module's approved default", () => {
  assert.equal(actionIconId(newSale), "pencil:add");
  assert.equal(actionIconId({ ...newSale, label: "Sale" }), "lucide:receipt");
  // An unknown screen falls to the icon consumer's own fallback at draw time;
  // the decision here only names the id the screen's default table carries.
  assert.equal(actionIconId({ ...newSale, label: "Sale", screen: "ledger" }), "lucide:square");
});

test("the saved confirmation is the action's own words plus the live sentence", () => {
  assert.equal(savedWords("+ Sale"), "+ Sale saved. Everyone picks it up on their next page load.");
  assert.equal(savedWords(""), "Saved. Everyone picks it up on their next page load.");
});

test("the mine projection whitelists the caller's resolved list, and nothing else rides through", () => {
  const answer = toQuickActionsMine({
    profile: "manager.sales",
    actions: [{ ...newSale, lead: "Jumps to Point of Sale with a fresh ticket.", extra: "dropped" }],
  });
  assert.equal(answer.profile, "manager.sales");
  assert.equal(answer.actions.length, 1);
  assert.deepEqual(Object.keys(answer.actions[0]).sort(), [
    "action_id",
    "glyph",
    "intent",
    "label",
    "lead",
    "screen",
    "title",
    "unit",
  ]);
  assert.equal(answer.actions[0].lead, "Jumps to Point of Sale with a fresh ticket.");
  // A malformed answer degrades to the empty list, never a throw: the bar is
  // optional chrome that renders nothing rather than a broken one.
  assert.deepEqual(toQuickActionsMine(null).actions, []);
  assert.deepEqual(toQuickActionsMine({ profile: "", actions: "no" }).actions, []);
});

test("the action projection keeps only what the screens use, with safe types", () => {
  const action = toQuickAction({ ...newSale, glyph: 42, title: undefined });
  assert.equal(action.glyph, "", "a non-string glyph degrades to empty");
  assert.equal(action.title, "");
  assert.equal(toQuickAction(null).action_id, "");
});

test("an intent is consumed exactly once", () => {
  clearPendingIntent();
  publishIntent({ module: "pos", intent: INTENT_POS_NEW_TICKET, actionId: "sale.new" });
  assert.equal(peekPendingIntent()?.intent, INTENT_POS_NEW_TICKET);
  const first = takeIntent("pos");
  assert.equal(first?.intent, INTENT_POS_NEW_TICKET);
  // The mounted-versus-mounting race: whichever of the listener and the mount
  // read runs first wins, and the other gets nothing. Acting twice would reset
  // a till the cashier already began typing into.
  assert.equal(takeIntent("pos"), null);
});

test("an intent addressed elsewhere is left in place, not swallowed", () => {
  clearPendingIntent();
  publishIntent({ module: "pos", intent: INTENT_POS_NEW_TICKET, actionId: "sale.new" });
  assert.equal(takeIntent("quotes"), null, "the wrong module must not consume it");
  assert.equal(takeIntent("pos")?.actionId, "sale.new", "the right module still finds it");
});

test("a module ignores an intent it does not handle, and leaves it for a sibling", () => {
  clearPendingIntent();
  publishIntent({ module: "pos", intent: "pos.something.else", actionId: "x" });
  assert.equal(takeIntent("pos", [INTENT_POS_NEW_TICKET]), null);
  assert.equal(peekPendingIntent()?.intent, "pos.something.else", "still there for a handler that wants it");
  clearPendingIntent();
});

test("an unclaimed intent expires instead of ambushing a later visit", () => {
  clearPendingIntent();
  const realNow = Date.now;
  try {
    publishIntent({ module: "pos", intent: INTENT_POS_NEW_TICKET, actionId: "sale.new" });
    Date.now = () => realNow() + 11_000; // past the ten second window
    assert.equal(takeIntent("pos"), null, "a later, unrelated visit must not inherit a forgotten click");
  } finally {
    Date.now = realNow;
  }
  clearPendingIntent();
});

test("the channel announces on the window so an already-mounted destination hears it", () => {
  clearPendingIntent();
  let heard = null;
  const prev = globalThis.window.dispatchEvent;
  globalThis.window.dispatchEvent = (e) => {
    heard = e;
    return true;
  };
  try {
    publishIntent({ module: "pos", intent: INTENT_POS_NEW_TICKET, actionId: "sale.new" });
    assert.equal(heard?.type, INTENT_EVENT);
    assert.equal(heard?.detail?.module, "pos");
  } finally {
    globalThis.window.dispatchEvent = prev;
    clearPendingIntent();
  }
});
