// Dealer Admin's staff decisions, with no DOM in sight: the words the
// register says about a role's reach, the branch chips, the create and new
// password rules, and the refusal words. No browser, no network.
//
// Port provenance: the reach words are the original app's staff-roles-logic.ts
// (repository hardscapeos_dibbits, read only; see PROVENANCE.md), kept
// verbatim; the create and new password rules are staff-roles.ts's
// submitCreate and staff-reset.ts's own predicates; PROVENANCE.md carries the
// row per rule.
import type { BranchRow, RoleRow } from "./dealer-api.js";

// -- the words a state carries ----------------------------------------------------------------------

/** The plain fallback for any unknown closed-set value: lowercased, separators opened out. */
function plainly(value: string): string {
  return value.trim().toLowerCase().replace(/[_.-]+/g, " ").replace(/\s+/g, " ");
}

/**
 * A server error a person reads. A one word token (a status word, an enum) is
 * not a sentence; it becomes plain words. A sentence stays the server's own.
 * The same rule manager-home carries; apps do not import apps, so it lives here too.
 */
export function errorWords(error: string): string {
  const trimmed = error.trim();
  if (trimmed === "" || !trimmed.includes(" ")) return "Try again in a moment.";
  return trimmed;
}

// -- a role's reach ----------------------------------------------------------------------------------
//
// WHY THIS MAP EXISTS AT ALL. The original carried the same argument: the
// roles catalog's scope column answers one question for whoever audits the
// roster (how far does this role reach?), and the CHECK constraint's capital
// constant is not an answer a person reads. The words say the reach rather
// than the constant: GLOBAL is every branch the dealer has, so "All branches"
// and never a per-dealer count; BRANCH is "Per branch", and the branches it
// actually reaches sit in the very next column.

const ROLE_SCOPE_LABEL: Readonly<Record<string, string>> = Object.freeze({
  GLOBAL: "All branches",
  BRANCH: "Per branch",
});

/** A role's reach as words, degrading honestly: an unmapped value shows as itself. */
export function roleScopeWord(scope: string): string {
  return ROLE_SCOPE_LABEL[scope] ?? plainly(scope);
}

/** A tier is a rank, not a status: the words, and the number when the catalog grows. */
export function tierWord(tier: number): string {
  if (tier === 1) return "Leadership";
  if (tier === 2) return "Manager";
  if (tier === 3) return "Frontline";
  return `Tier ${tier}`;
}

/**
 * Is this row's role assignable from the catalog? A role the catalog does not
 * name (a platform role) renders read-only: it shows correctly instead of
 * collapsing to unassigned, and the inline select cannot demote it.
 */
export function assignableRole(roleCode: string, roles: readonly RoleRow[]): boolean {
  return roleCode === "" || roles.some((r) => r.code === roleCode);
}

// -- the branch chips --------------------------------------------------------------------------------

/** The yards of a dealer's own branch name. A name with no dash is left as it is. */
export function shortBranch(name: string): string {
  // The original split on the dash forms including the two escaped long
  // dashes; the escapes keep the parity without carrying the characters.
  const tail = name.split(/\s+[\u2013\u2014-]\s+/).pop();
  return tail !== undefined && tail.trim() !== "" ? tail.trim() : name;
}

export interface BranchChip {
  readonly label: string;
  readonly known: boolean;
}

/**
 * The chips a staff row shows for its branch grants: the yard name when the
 * branches route knows the id, the id's own first block when it does not.
 * Render only: this decides what a chip says, never who may edit it.
 */
export function branchChips(branchIds: readonly string[], branches: readonly BranchRow[]): BranchChip[] {
  return (branchIds ?? []).map((id) => {
    const hit = branches.find((b) => b.id === id);
    return hit === undefined ? { label: id.slice(0, 8), known: false } : { label: shortBranch(hit.name), known: true };
  });
}

// -- the create form ---------------------------------------------------------------------------------
//
// The core has no create staff route yet (the parity ledger holds the whole
// staff identity admin surface for the auth lane), so the form renders
// disabled and the marker words say so. The rules below are still the form's
// own: they are what the submit checks the day the route lands, and they are
// testable now.

/** One candidate staff member, as the create request carries it. */
export interface NewStaffForm {
  readonly display_name: string;
  readonly email: string;
  readonly password: string;
  readonly role_code: string;
}

/** The original's own submit checks, in its own order. */
export function createFormError(form: NewStaffForm): string {
  if (form.display_name.trim() === "" || form.email.trim() === "" || form.role_code === "") {
    return "Name, email, and role are required.";
  }
  if (form.password.length < 8) {
    return "Password must be at least 8 characters.";
  }
  return "";
}

export function canSubmitCreate(form: NewStaffForm): boolean {
  return createFormError(form) === "";
}

// -- the new password rules (the reset link's own page) ------------------------------------------------

/**
 * The set-new-password page's two rules: eight characters and confirmed.
 * The page itself is the shell's to host (the confirm route is the sign in
 * work's); the rules are the same either side of it.
 */
export function canSubmitNewPassword(next: string, confirm: string): boolean {
  return next.length >= 8 && next === confirm;
}

/** Which of the two rules is unsatisfied, as the field's hint says it. */
export function newPasswordHint(next: string, confirm: string): string {
  if (next.length === 0) return "At least 8 characters.";
  if (next.length < 8) return "Too short: at least 8 characters.";
  if (next !== confirm) return "The passwords do not match.";
  return "";
}

// -- the refusals ------------------------------------------------------------------------------------
//
// The core's gates: the staff read is a manager's (tier 1 or 2), the writes
// (create, assign role, reset link) are leadership's (tier 1 GLOBAL). The
// shared Forbidden body is one word; the screen adds the sentence naming who,
// and never invents authority the server did not grant.

/** Map a staff refusal to plain words naming who may act (FROM-PRINCIPAL 39). */
export function staffRefusalWords(error: string, status: number | undefined): string {
  if (status === 403) {
    return "You may not open this. The staff register is read by managers and above; creating staff, assigning roles and reset links are leadership's own.";
  }
  return error !== "" ? error : "Try again in a moment.";
}

// -- the waits-for-sign-in state ---------------------------------------------------------------------

/**
 * The one marker the disabled controls carry, and the sentence beside them.
 * Creating a staff member and linking their sign in wait for the identity
 * work (the parity ledger holds the staff identity admin surface for the
 * auth lane), so the controls render disabled with this word rather than
 * pretending a submit could go through.
 */
export const WAITS_FOR_SIGN_IN_WORDS =
  "Waits for sign in: creating a staff member and linking their identity land with the auth lane's sign in work.";
