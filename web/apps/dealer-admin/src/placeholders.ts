// The house placeholders: the words a screen shows when it cannot name a
// thing. UX-106: a raw id on a dealer screen is a defect, and the shape of it
// is always the same. A screen holds an identifier it cannot turn into a
// name, and instead of a word it prints the identifier's own first block
// ("9a11e111") or the whole raw key.
//
// The rule these words carry:
//   - never an id fragment: a dealer cannot look up "9a11e111", so it names
//     nothing for them;
//   - never a plausible name: a made up name would be a lie the dealer
//     believes, and a wrong yard or a wrong vendor is worse than no name;
//   - always the same shape: "Unknown <the thing>", so the screen reads as
//     one family of placeholders rather than as a mishmash.
//
// Each word says WHAT is unknown, never WHO: a branch the branches route
// does not know is an "Unknown branch", not a guess at which yard it might
// be.
//
// The words live here, in the app that shows them, for the same reason the
// error words do (dealer-logic.ts): apps do not import apps, so the house
// rule is carried once per app rather than borrowed from a neighbour.

/** A branch the branches route does not know. */
export const UNKNOWN_BRANCH = "Unknown branch";

/** A category the catalog does not know. */
export const UNKNOWN_CATEGORY = "Unknown category";

/** A product the catalog does not know. */
export const UNKNOWN_PRODUCT = "Unknown product";

/** A vendor the read does not name. */
export const UNKNOWN_VENDOR = "Unknown vendor";

/** A customer the read does not name. */
export const UNKNOWN_CUSTOMER = "Unknown customer";

/** A person the trail attributes an action to but cannot name. */
export const UNKNOWN_ACTOR = "Unknown actor";

/** A person the queue credits with a request but cannot name. */
export const UNKNOWN_USER = "Unknown user";

/** A batch the read does not name. */
export const UNKNOWN_BATCH = "Unknown batch";

/** A record of any kind the screen cannot name. */
export const UNKNOWN_ENTITY = "Unknown entity";

/** A surface the configurator does not know. */
export const UNKNOWN_SURFACE = "Unknown surface";

/** A job shape the configurator does not know. */
export const UNKNOWN_SHAPE = "Unknown shape";

/** An action the roster does not name. */
export const UNKNOWN_ACTION = "Unknown action";

/** A view the app does not name. */
export const UNKNOWN_VIEW = "Unknown view";
