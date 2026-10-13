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
  assert.match(WAITS_FOR_SIGN_IN_WORDS, /identity/);
});
