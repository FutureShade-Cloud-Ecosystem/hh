// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-counter-settings-logic.ts; adapted for HH.

// pos-counter-settings-logic.ts: the words the counter settings screen says about the dealer's
// counter rules.
//
// ── WHAT THESE KNOBS ARE ────────────────────────────────────────────────────────────────────────
//
// pos_policy: the dealer-level counter behaviour the server already validates in words: how a
// ticket's lines paint, what the workspace opens on, and the two VISIBILITY GATES that decide who
// may receive a dealer's own margin and a customer's past-due balance. Those last two are why the
// counter needed a change history at all: before this, `DISPATCH_POLICY_CHANGED` was the only
// policy-change action in the program, so the dispatch rules recorded who moved them and the two
// sharpest gates in the counter did not.
//
// ── WHY A LOGIC FILE AND NOT A MAP INSIDE THE COMPONENT ─────────────────────────────────────────
//
// pos-counter-settings.ts calls customElements.define at module scope and cannot be imported outside
// a browser, so words living inside it can only ever be scanned, not asserted. The house rule the
// app-wide vocabulary gate names in its own failure message is that words go in a map in the
// module's own logic file with a default arm that lowercases rather than inventing. This is it, and
// it is DOM-free.
//
// ── THE WORDS ARE THE SERVER'S VOCABULARY, TURNED INTO SENTENCES ────────────────────────────────
//
// Every value below is a CHECK-constrained column value (migrations 0165/0166/0167/0170/0181) and
// each is spelled here the way the counter itself talks about it. `valueWord`'s last arm still
// lowercases an unmapped value rather than inventing one, so a knob added to the server later reads
// as strange in the history rather than as a confident lie about who can see a margin.

import { ON_OFF, type SettingsVocabulary } from './settings-history-logic.js';

/** Each pos_policy knob, named the way a counter administrator would say it. */
export const POS_RULE_WORDS: Readonly<Record<string, string>> = Object.freeze({
  cod_enabled: 'Cash-on-demand sales allowed',
  restocking_fee_bps: 'Restocking fee, basis points',
  line_presentation: 'How a ticket line paints',
  quick_chips_enabled: 'The quick category row',
  workspace_split_pct: "The lookup panel's share of the screen",
  workspace_arrangement: 'Which panel opens on the left',
  lookup_presentation: 'How lookup results paint',
  margin_visibility: 'Who sees margin and profit',
  balance_visibility: "Who sees a customer's past-due balance",
  quick_chip_set: 'The curated quick categories',
  counter_layout: 'Which counter a till opens on',
});

/**
 * line_presentation: LINES | CARDS (0165).
 *
 * SEPARATE FROM THE LOOKUP MAP BELOW, and the pair is a worked example of why a value map is
 * PER-KNOB rather than app-wide: both columns admit CARDS and only one of them admits LINES, while
 * the lookup's dense option is spelled ROWS. One flat map would have to pick a vocabulary, and the
 * one it picked would be wrong for the other knob.
 */
const LINE_PRESENTATION_WORDS: Readonly<Record<string, string>> = Object.freeze({
  LINES: 'As a dense list',
  CARDS: 'As cards',
});

/** lookup_presentation: CARDS | ROWS (0167). */
const LOOKUP_PRESENTATION_WORDS: Readonly<Record<string, string>> = Object.freeze({
  CARDS: 'As product cards',
  ROWS: 'As a dense list',
});

/** workspace_arrangement: 0166. */
const ARRANGEMENT_WORDS: Readonly<Record<string, string>> = Object.freeze({
  LOOKUP_LEFT: 'Lookup on the left',
  SALE_LEFT: 'The ticket on the left',
});

/**
 * The visibility vocabulary, shared by margin_visibility (0170) and balance_visibility (0181).
 *
 * ONE IDEA, TWO KNOBS: deliberately, and the server says so where it declares them: "a dealer
 * configuring the counter meets ONE idea about visibility, not two". Their DEFAULTS differ because
 * they protect opposite things; their words must not.
 */
const VISIBILITY_WORDS: Readonly<Record<string, string>> = Object.freeze({
  MANAGER_UP: 'Managers and above',
  ALL_STAFF: 'Every staff member',
  HIDDEN: 'Nobody',
});

/** The vocabulary the shared history component renders this screen's rows through. */
export const POS_HISTORY_WORDS: SettingsVocabulary = Object.freeze({
  rule: POS_RULE_WORDS,
  value: Object.freeze({
    cod_enabled: ON_OFF,
    quick_chips_enabled: ON_OFF,
    line_presentation: LINE_PRESENTATION_WORDS,
    lookup_presentation: LOOKUP_PRESENTATION_WORDS,
    workspace_arrangement: ARRANGEMENT_WORDS,
    margin_visibility: VISIBILITY_WORDS,
    balance_visibility: VISIBILITY_WORDS,
    // restocking_fee_bps and workspace_split_pct are integers, and a number is already the word for
 // itself. quick_chip_set is a dealer's OWN category names joined by the server: translating a
    // dealer's word would be this app deciding it knows what they meant.
  }),
});
