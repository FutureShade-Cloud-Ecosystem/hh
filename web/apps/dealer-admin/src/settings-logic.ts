// The deployment settings surface's decisions, with no DOM in sight: the
// four sections' form checks, the words only zone, and the refusals. No
// browser, no network.
//
// THE CORE'S OWN WORDS, SPOKEN BACK. The card rail's check mirrors the
// payment handler's own ("merchant_id is required"); the walkthrough line's
// budget is the tour package's MaxLine with its doctrine sentence
// (core/internal/tour/model.go); the quick chips' bounds are the settings
// kit's ValidateChipSet; the counter split's bounds are the kit's geometry
// constants. The client's copies only spare a round trip; the server's own
// 4xx stays the last word and is shown whole.
import type { BranchRow } from "./dealer-api.js";

// -- the card rail -----------------------------------------------------------------------------------

// -- the counter policy -------------------------------------------------------------------------------

export const LINE_PRESENTATIONS: readonly string[] = Object.freeze(["LINES", "CARDS"]);
export const LOOKUP_PRESENTATIONS: readonly string[] = Object.freeze(["CARDS", "ROWS"]);
export const VISIBILITY_CLASSES: readonly string[] = Object.freeze(["MANAGER_UP", "ALL_STAFF", "HIDDEN"]);
export const BALANCE_CLASSES: readonly string[] = Object.freeze(["ALL_STAFF", "MANAGER_UP", "HIDDEN"]);
export const WORKSPACE_ARRANGEMENTS: readonly string[] = Object.freeze(["LOOKUP_LEFT", "SALE_LEFT"]);

export function linePresentationWord(v: string): string {
  if (v === "LINES") return "Lines";
  if (v === "CARDS") return "Cards";
  return v;
}

export function lookupPresentationWord(v: string): string {
  if (v === "CARDS") return "Cards";
  if (v === "ROWS") return "Rows";
  return v;
}

export function marginVisibilityWord(v: string): string {
  if (v === "MANAGER_UP") return "Managers and above";
  if (v === "ALL_STAFF") return "Everyone";
  if (v === "HIDDEN") return "Hidden";
  return v;
}

export function workspaceArrangementWord(v: string): string {
  if (v === "LOOKUP_LEFT") return "Lookup on the left, the sale on the right";
  if (v === "SALE_LEFT") return "Sale on the left, the lookup on the right";
  return v;
}

/** The split's bounds, the kit's geometry constants in words. */
export function workspaceSplitError(splitPct: number): string {
  if (splitPct < 20 || splitPct > 80) {
    return "Workspace split must be between 20 and 80.";
  }
  return "";
}

/** The restocking fee is basis points: a whole number, never negative. */
export function restockingFeeError(bps: number): string {
  if (bps < 0) {
    return "The restocking fee cannot be negative.";
  }
  if (!Number.isInteger(bps)) {
    return "The restocking fee is a whole number of basis points.";
  }
  return "";
}

/** The curated chips: at most 8, each 1 to 40 characters with no comma. */
export function chipSetError(chips: readonly string[]): string {
  if (chips.length > 8) {
    return "Quick chips hold at most 8 categories.";
  }
  for (const c of chips) {
    const t = c.trim();
    if (t === "" || t.length > 40 || t.includes(",")) {
      return "Each quick chip category must be 1 to 40 characters with no comma.";
    }
  }
  return "";
}

/** The quick chips' field is one category per line. */
export function chipsOf(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

/** A shared Forbidden at the counter's policy becomes words naming who may act. */
export function counterPolicyRefusalWords(error: string, status: number | undefined): string {
  if (status === 403) {
    return "You may not set the counter's policy. Setting it is the dealer's administrators' own.";
  }
  return error !== "" ? error : "Try again in a moment.";
}

/** The history read is admin gated too; its refusal names who may read the trail. */
export function policyHistoryRefusalWords(error: string, status: number | undefined): string {
  if (status === 403) {
    return "You may not read the policy's history. The trail of who changed a knob is the administrators' own.";
  }
  return error !== "" ? error : "Try again in a moment.";
}

/**
 * One policy history row's who: the actor id's own block, the way the audit
 * trail's actor cell reads, or somebody when the row carries no name.
 */
export function policyHistoryWho(who: string): string {
  return who === "" ? "somebody" : who.slice(0, 8);
}

// -- the walkthrough ----------------------------------------------------------------------------------

/** The line budget, counted in characters, with the doctrine's own words. */
export const TOUR_MAX_LINE = 72;

export function tourLineError(line: string): string {
  const n = [...line].length;
  if (n === 0) {
    return "A step with nothing to say is a step to delete.";
  }
  if (n > TOUR_MAX_LINE) {
    return "A step says one line and the limit is " + TOUR_MAX_LINE + " characters, because a walkthrough speaks in headlines.";
  }
  return "";
}

/**
 * The platform gate's calm words for a reader who may not edit, and the
 * core's own sentence passes whole when a save is answered.
 */
export const TOUR_READONLY_WORDS =
  "The walkthrough's copy and its demo switch are the platform administrator's own.";

/** The walkthrough's off words: the read answers nothing when a knob is dark. */
export const TOUR_OFF_WORDS =
  "The walkthrough answers nothing here: the platform's demo switch or the dealer's own switch has it off.";

export function tourPlatformWords(error: string): string {
  return error;
}

// -- the brand ----------------------------------------------------------------------------------------

/**
 * The brand half's waits words. The pair is the parity ledger's own hold
 * (registered in the contract, not yet served by the core), so the section
 * renders whole and disabled, and the words say what waits, the way the
 * staff family's waits state does. A dealer reads them: they name no part
 * of how the product is built.
 */
export const BRAND_WAITS_WORDS =
  "Waits for the platform: the dealer's name, mark and receipt footer open for editing once the platform carries the dealer's brand record.";

// -- the branches (the counter policy is per branch; the form targets the org wide default) ------------

/** A branch's short word, the register's own rule, for the policy's scope line. */
export function branchScopeWord(branches: readonly BranchRow[]): string {
  return branches.length === 0 ? "Every branch (the dealer's default)" : "Every branch (the dealer's default); a branch can override it";
}
