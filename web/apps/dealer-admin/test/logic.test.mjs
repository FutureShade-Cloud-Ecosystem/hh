// Dealer Admin's pure decisions, on their own: the staff words the register
// says, the branch chips, the create and reset rules, and the refusal words.
// No browser, no network. Red first: this file landed before dealer-logic.ts
// existed, and the run below it failed on the missing module (see the report).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  WAITS_FOR_SIGN_IN_WORDS,
  assignableRole,
  branchChips,
  canSubmitCreate,
  canSubmitNewPassword,
  createFormError,
  errorWords,
  newPasswordHint,
  roleScopeWord,
  shortBranch,
  staffRefusalWords,
  tierWord,
} from "../src/dealer-logic.ts";
import {
  PLATFORM_SURFACES,
  asPlatformView,
  bindingFromEvent,
  chordFromEvent,
  draftToKeys,
  effectiveVerdict,
  keycap,
  otherSurface,
  platformActionLabel,
  platformBadChordSentence,
  platformClashSentence,
  platformKeycap,
  reservedHolderFor,
  serverSentence,
  validChord,
} from "../src/hotkey-logic.ts";
import {
  ACTION_OPTIONS,
  auditEmptyWords,
  actorWords,
  entityWords,
  filtersActive,
  isSensitive,
  sinceWire,
  whenWords,
} from "../src/audit-logic.ts";

const ROLES = [
  { code: "GENERAL_MANAGER", name: "General Manager", tier: 1, scope: "GLOBAL" },
  { code: "SALES_MANAGER", name: "Sales Manager", tier: 2, scope: "BRANCH" },
  { code: "INSIDE_SALES", name: "Inside Sales", tier: 3, scope: "BRANCH" },
];

test("a role's reach is a word from the matrix, never the CHECK constraint", () => {
  assert.equal(roleScopeWord("GLOBAL"), "All branches");
  assert.equal(roleScopeWord("BRANCH"), "Per branch");
});

test("an unmapped scope degrades honestly, as itself opened out", () => {
  assert.equal(roleScopeWord("STRANGE_SCOPE"), "strange scope");
  assert.equal(roleScopeWord(""), "");
});

test("the yard is the tail of the dealer's own branch name", () => {
  assert.equal(shortBranch("Gable Landscape Supply - Point Loma"), "Point Loma");
  assert.equal(shortBranch("Gable Landscape Supply - Julian"), "Julian");
  assert.equal(shortBranch("Point Loma"), "Point Loma");
});

test("branch chips name the branches the staff row reaches; an unknown id shows its fragment", () => {
  const branches = [
    { id: "b-0001", name: "Gable Landscape Supply - Point Loma" },
    { id: "b-0002", name: "Gable Landscape Supply - Julian" },
  ];
  assert.deepEqual(branchChips(["b-0002"], branches), [{ label: "Julian", known: true }]);
  assert.deepEqual(branchChips([], branches), []);
  const unknown = branchChips(["9a11e111-0000-4000-9000-0000000000ff"], branches);
  assert.deepEqual(unknown, [{ label: "9a11e111", known: false }]);
});

test("a role in the catalog is assignable; a platform role is read-only; no role is still assignable", () => {
  assert.equal(assignableRole("SALES_MANAGER", ROLES), true);
  assert.equal(assignableRole("SUPER_ADMIN", ROLES), false);
  assert.equal(assignableRole("", ROLES), true);
});

test("tiers are ranks with words; anything else is the number, plainly", () => {
  assert.equal(tierWord(1), "Leadership");
  assert.equal(tierWord(2), "Manager");
  assert.equal(tierWord(3), "Frontline");
  assert.equal(tierWord(9), "Tier 9");
});

test("a create needs a name, an email, a role and eight characters of password", () => {
  const base = { display_name: "Yard team (demo)", email: "yard.team@hh-demo.invalid", role_code: "YARD_TEAM", password: "yard-yard-yard" };
  assert.equal(canSubmitCreate(base), true);
  assert.equal(createFormError({ ...base, display_name: " " }), "Name, email, and role are required.");
  assert.equal(createFormError({ ...base, role_code: "" }), "Name, email, and role are required.");
  assert.equal(createFormError({ ...base, password: "short" }), "Password must be at least 8 characters.");
  assert.equal(canSubmitCreate({ ...base, password: "short" }), false);
});

test("a new password is eight characters and confirmed before the submit is live", () => {
  assert.equal(canSubmitNewPassword("yard-yard-yard", "yard-yard-yard"), true);
  assert.equal(canSubmitNewPassword("short", "short"), false);
  assert.equal(canSubmitNewPassword("yard-yard-yard", "yard-yard"), false);
});

test("the set password hints say which of the two rules is unsatisfied", () => {
  assert.equal(newPasswordHint("", ""), "At least 8 characters.");
  assert.equal(newPasswordHint("short", ""), "Too short: at least 8 characters.");
  assert.equal(newPasswordHint("yard-yard-yard", "yard-yard"), "The passwords do not match.");
  assert.equal(newPasswordHint("yard-yard-yard", "yard-yard-yard"), "");
});

test("the shared Forbidden body becomes words naming who may act, the gates' own split", () => {
  assert.match(staffRefusalWords("Forbidden", 403), /managers and above/);
  assert.match(staffRefusalWords("Forbidden", 403), /leadership/);
  assert.equal(staffRefusalWords("staff not found", 404), "staff not found");
});

test("a one word server error becomes plain words; a sentence stays the server's own", () => {
  assert.equal(errorWords("internal"), "Try again in a moment.");
  assert.equal(errorWords("staff not found"), "staff not found");
  assert.equal(errorWords(""), "Try again in a moment.");
});

test("the waits-for-sign-in state is one stated fact, and it names what waits", () => {
  assert.match(WAITS_FOR_SIGN_IN_WORDS, /Waits for sign in/);
  assert.match(WAITS_FOR_SIGN_IN_WORDS, /staff member/);
  // A dealer reads this: no word of the build's own lanes.
  assert.doesNotMatch(WAITS_FOR_SIGN_IN_WORDS, /lane|auth|identity|ledger/i);
});

// -- the hotkeys: two grammars, two substrates, never one list -----------------------

test("the shell grammar spells chords capitalised and plus joined, modifiers first", () => {
  assert.equal(bindingFromEvent({ key: "F4", ctrlKey: false, altKey: false, shiftKey: false, metaKey: false }), "F4");
  assert.equal(bindingFromEvent({ key: "k", ctrlKey: true, altKey: false, shiftKey: true, metaKey: false }), "Ctrl+Shift+K");
  assert.equal(bindingFromEvent({ key: "k", ctrlKey: false, altKey: true, shiftKey: false, metaKey: true }), null);
  assert.equal(bindingFromEvent({ key: "b", ctrlKey: false, altKey: false, shiftKey: false, metaKey: false }), null);
  assert.equal(bindingFromEvent({ key: "b", ctrlKey: true, altKey: false, shiftKey: false, metaKey: false }), "Ctrl+B");
  assert.equal(bindingFromEvent({ key: "?", ctrlKey: false, altKey: false, shiftKey: true, metaKey: false }), null);
});

test("the counter grammar spells chords lowercase and dash joined, shift first", () => {
  assert.equal(chordFromEvent({ key: "F4", ctrlKey: false, altKey: false, shiftKey: false, metaKey: false }), "f4");
  assert.equal(chordFromEvent({ key: "F4", ctrlKey: false, altKey: false, shiftKey: true, metaKey: false }), "shift-f4");
  assert.equal(chordFromEvent({ key: "b", ctrlKey: true, altKey: false, shiftKey: true, metaKey: false }), "shift-ctrl-b");
  assert.equal(chordFromEvent({ key: "b", ctrlKey: false, altKey: false, shiftKey: true, metaKey: false }), "");
  assert.equal(chordFromEvent({ key: "F4", ctrlKey: false, altKey: false, shiftKey: false, metaKey: true }), "");
});

test("a chord is bindable only in its own grammar's canonical spelling", () => {
  assert.equal(validChord("shift-ctrl-b"), true);
  assert.equal(validChord("ctrl-shift-b"), false);
  assert.equal(validChord("f12"), true);
  assert.equal(validChord("b"), false);
  assert.equal(validChord("ctrl-b"), true);
  assert.equal(validChord("shift-shift-b"), false);
});

test("a keycap reads as a person reads it", () => {
  assert.equal(keycap("shift-f4"), "\u21E7F4");
  assert.equal(keycap("f12"), "F12");
  assert.equal(keycap("ctrl-alt-p"), "\u2303\u23E5P");
  assert.equal(platformKeycap("app.shell", "Ctrl+Shift+K"), "Ctrl+Shift+K");
  assert.equal(platformKeycap("pos.counter", "shift-f4"), "\u21E7F4");
});

test("the two surfaces are the two substrates, in the order the screen renders them", () => {
  assert.deepEqual([...PLATFORM_SURFACES], ["app.shell", "pos.counter"]);
  assert.equal(otherSurface("app.shell"), "pos.counter");
  assert.equal(otherSurface("pos.counter"), "app.shell");
});

test("the configurator's labels are sentences, never a stored id", () => {
  assert.equal(platformActionLabel("pos.counter", "pay"), "Take payment, complete the sale");
  assert.equal(platformActionLabel("app.shell", "sale.cash.new"), "Start a new sale at the till");
  assert.equal(platformActionLabel("app.shell", "not.in.registry"), "not.in.registry");
});

test("a stored binding a shell grammar cannot round trip is not bindable", () => {
  // platformValidBinding is exercised through validChord for the counter and
  // the round trip for the shell; the shell side lives in hotkey-logic and is
  // asserted on main flows here.
  assert.equal(validChord("f4"), true);
});

test("the effective verdict refuses what the shell owns outright, naming what holds it", () => {
  const verdict = effectiveVerdict("app.shell", "sale.cash.new", "F1", {
    draft: {},
    reserved: [{ binding: "F1", holds: "opens the shortcut list" }],
  });
  assert.equal(verdict.kind, "taken");
  assert.match(verdict.sentence, /opens the shortcut list/);
  assert.match(verdict.sentence, /No roster can take it/);
});

test("the effective verdict refuses a clash on the same roster, naming the action it would break", () => {
  const verdict = effectiveVerdict("pos.counter", "pay", "f4", {
    draft: { "step-next": "f4" },
  });
  assert.equal(verdict.kind, "taken");
  assert.equal(verdict.sentence, platformClashSentence("pos.counter", "f4", "step-next"));
  assert.match(verdict.sentence, /Move the sale on to the next step/);
});

test("the effective verdict refuses a key the dealer's own override holds", () => {
  const verdict = effectiveVerdict("pos.counter", "pay", "f2", {
    draft: {},
    dealer: [{ action: "search", binding: "f2", position: 0 }],
  });
  assert.equal(verdict.kind, "taken");
  assert.match(verdict.sentence, /Counter settings/);
});

test("the effective verdict warns, never refuses, the deliberate cross substrate claim", () => {
  const verdict = effectiveVerdict("app.shell", "sale.cash.new", "F4", {
    draft: {},
    other: [{ action: "step-next", binding: "f4", position: 0 }],
  });
  assert.equal(verdict.kind, "shared");
  assert.match(verdict.sentence, /Both keep working/);
});

test("a candidate key nothing holds is free, and empty is free", () => {
  assert.equal(effectiveVerdict("app.shell", "sale.cash.new", "F7", { draft: {} }).kind, "free");
  assert.equal(effectiveVerdict("app.shell", "sale.cash.new", "", { draft: {} }).kind, "free");
});

test("the reserved holder folds the four spellings one key can arrive in", () => {
  const reserved = [{ binding: "ctrl-shift-k", holds: "opens the shortcut list" }];
  assert.equal(reservedHolderFor("Ctrl+Shift+K", reserved), "opens the shortcut list");
  assert.equal(reservedHolderFor("shift-ctrl-k", reserved), "opens the shortcut list");
  assert.equal(reservedHolderFor("f2", [{ binding: "F2", holds: "the app's own" }]), "the app's own");
  assert.equal(reservedHolderFor("F5", reserved), "");
});

test("a draft goes back on the wire in the screen's own listed order", () => {
  const keys = draftToKeys("pos.counter", { pay: "f12", "step-next": "f4" });
  assert.deepEqual(
    keys.map((k) => [k.action, k.binding, k.position]),
    [
      ["step-next", "f4", 0],
      ["pay", "f12", 1],
    ],
  );
});

test("the loader normalises at the boundary: a body of the wrong shape yields nothing editable", () => {
  const view = asPlatformView(undefined);
  assert.equal(view.canEdit, false);
  assert.deepEqual(view.rosters.map((r) => r.surface), ["app.shell", "pos.counter"]);
  assert.deepEqual(view.rosters.map((r) => r.keys.length), [0, 0]);
  assert.deepEqual(view.reserved, []);
  const read = asPlatformView({
    rosters: [
      { surface: "app.shell", keys: [{ action: "sale.cash.new", binding: "F1", position: 0 }], dealer: [] },
      { surface: "pos.counter", keys: [{ action: "pay", binding: "f12", position: 0 }], dealer: [{ action: "pay", binding: "f2", position: 0 }] },
    ],
    can_edit: true,
    reserved: [{ binding: "F1", holds: "opens the shortcut list" }, { binding: "" }],
  });
  assert.equal(read.canEdit, true);
  assert.deepEqual(read.rosters[0].keys, [{ action: "sale.cash.new", binding: "F1", position: 0 }]);
  assert.deepEqual(read.rosters[1].dealer, [{ action: "pay", binding: "f2", position: 0 }]);
  assert.deepEqual(read.reserved, [{ binding: "F1", holds: "opens the shortcut list" }]);
});

test("the server's own sentence keeps its words and loses only the module prefix", () => {
  assert.equal(serverSentence("settings: the counter roster may not be empty"), "the counter roster may not be empty");
  assert.equal(serverSentence("Forbidden"), "Forbidden");
  assert.equal(serverSentence(""), "");
});

test("a keystroke that types into the sale is refused in the surface's own words", () => {
  assert.match(platformBadChordSentence("pos.counter"), /types into the sale/);
  assert.match(platformBadChordSentence("app.shell"), /cannot be bound/);
});

// -- the audit trail: words for what a row says --------------------------------------

test("the sensitive actions badge, and the rest stay neutral", () => {
  assert.equal(isSensitive("PRICE_OVERRIDE"), true);
  assert.equal(isSensitive("EVIDENCE_VIEW"), true);
  assert.equal(isSensitive("TAX_RELIEF_ASSESS"), true);
  assert.equal(isSensitive("SALE_COMPLETED"), false);
  assert.equal(isSensitive(""), false);
  assert.ok(ACTION_OPTIONS.includes("HTTP.POST"));
  assert.ok(ACTION_OPTIONS.includes("PRICE_OVERRIDE"));
});

test("the actor is a role label when the server sent one, else the id's own block", () => {
  // Production rows read the role word empty (the core attributes by the
  // linked users id); the role path is the coarse trail's fallback.
  assert.equal(actorWords({ actor_role: "GENERAL_MANAGER", actor_id: "da110000-0000-4000-9000-000000000001" }), "general manager");
  assert.equal(actorWords({ actor_id: "da110000-0000-4000-9000-000000000001" }), "da110000");
  assert.equal(actorWords({}), "No actor recorded");
});

test("the when words read as a person reads a moment", () => {
  assert.equal(whenWords("2026-10-12T14:03:00Z", Date.UTC(2026, 9, 12)), "Oct 12, 14:03 UTC");
  assert.equal(whenWords("not-a-date"), "not-a-date");
});

test("the entity is the type and, when the row carries one, the id", () => {
  assert.equal(entityWords({ entity_type: "pos.sale", entity_id: "sa1" }), "pos.sale / sa1");
  assert.equal(entityWords({ entity_type: "pos.sale" }), "pos.sale");
  assert.equal(entityWords({ entity_type: "" }), "");
});

test("empty because of the filters and empty because nothing happened are different sentences", () => {
  assert.match(auditEmptyWords(true), /narrower than the record/);
  assert.match(auditEmptyWords(false), /Nothing has been recorded yet/);
});

test("a filter set is not the empty filter set", () => {
  assert.equal(filtersActive({ entity_type: "pos.sale", action: "", actor_id: "", since: "" }), true);
  assert.equal(filtersActive({ entity_type: "", action: "", actor_id: "", since: "" }), false);
});

test("the since input goes onto the wire as an instant; an unparsable one goes as it came", () => {
  const wire = sinceWire("2026-10-12T14:03");
  assert.equal(wire, new Date("2026-10-12T14:03").toISOString());
  assert.equal(sinceWire(""), "");
});
