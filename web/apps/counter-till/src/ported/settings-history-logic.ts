// Ported from hardscapeos_dibbits master 87d7941b app/src/settings-history-logic.ts; adapted for HH.

// settings-history-logic.ts: the words a settings change history says, and the rules for degrading
// honestly when it has none.
//
// ── WHY THIS IS A FILE AND NOT A METHOD ─────────────────────────────────────────────────────────
//
// The dispatch settings screen rendered its history as a PRIVATE METHOD, so it could not be
// imported. Two more histories were due: the counter's and receivables': and adding them the same
// way would have produced three hand-rolled tables of the same five columns. This repo already shows
// what deferring that costs: _order-edit.ts and _quote-edit.ts carry byte-identical helpers under
// different names, and nobody notices when one of them drifts.
//
// The DOM half lives next door in settings-history.ts. This half is DOM-free on purpose: a component
// that calls customElements.define at module scope cannot be imported outside a browser (the house
// split, DIB-471 §3), so the decisions are unit-tested here and the wiring is pinned by a scan.
//
// ── THE OTHER HALF OF THE PROBLEM: THE HISTORY PRINTED RAW ENUMS ────────────────────────────────
//
// A real dispatch row read `Who sees a load's dollar value | ALL_STAFF | MANAGER_UP`. The app-wide
// vocabulary gate could not see it, because that gate matches by PROPERTY NAME (`.status`,
// `.method`, `.kind`) and a history row's fields are called `before` and `after`: the column name
// says nothing about what is in it. So the enum went in raw for as long as the history existed.
//
// The cure is the same one every other surface uses: a map per knob, in the OWNING module's own
// logic/api file, with a default arm that lowercases rather than inventing a phrase. This file
// defines the SHAPE of that map and the two accessors; each settings screen supplies its own words,
// because the words a history uses must be the words the controls above it use: a history that
// calls a knob something the control does not is a history about a different screen.

/**
 * One recorded change, exactly as all three servers now serve it.
 *
 * `who` IS ALREADY A PERSON: "Alex Morgan (General Manager)": composed server-side from the audit
 * row's actor id and role. No user id ever reaches this type, which is the whole point: the original
 * dispatch ruling chose a role word specifically to keep an identifier off a screen, and resolving
 * the NAME on the server keeps that promise while answering the question a settings history is
 * actually asked. A row whose actor cannot be named degrades to the role word ("a branch manager"),
 * never to a blank and never to a uuid.
 */
export interface PolicyChangeRow {
  field: string;
  before: string;
  after: string;
  who: string;
  when: string;
}

/**
 * The words one settings screen uses for its own knobs and their values.
 *
 * `rule` : knob → the label the control above it carries. Not the column name.
 * `value`: knob → (stored value → the word that knob's own control offers for it). Per-knob and not
 * app-wide, because the same stored word means different things under different knobs:
 * MANAGER under `reopen_policy` is "a dispatch manager" and MANAGER_UP under
 * `load_value_visibility` is "managers and above". One flat map would have to pick.
 */
export interface SettingsVocabulary {
  readonly rule: Readonly<Record<string, string>>;
  readonly value: Readonly<Record<string, Readonly<Record<string, string>>>>;
}

/**
 * ON_OFF is the value map every boolean knob shares.
 *
 * The wire carries "true"/"false" because that is what a Go bool stringifies to, and neither word is
 * one anybody says about a setting. It is exported rather than copied into each vocabulary for the
 * same reason this file exists: five identical two-entry maps are five chances to have one of them
 * say "Yes/No" while the control beside it says "On/Off".
 */
export const ON_OFF: Readonly<Record<string, string>> = Object.freeze({
  true: 'On',
  false: 'Off',
});

/** de-underscored and lowercased: the shape both default arms degrade through. */
function plain(v: string): string {
  return String(v ?? '').toLowerCase().replace(/_/g, ' ');
}

/**
 * ruleWord names the knob a row is about, the way the controls on the same screen name it.
 *
 * The default arm degrades HONESTLY: an unmapped knob comes back de-underscored and sentence-cased,
 * so a rule added to the server later reads as slightly strange rather than as a confident wrong
 * title, and never reaches a person as SHOUTING_SNAKE_CASE. Never 'Unknown', and never '': a
 * history row with a blank first column looks broken.
 */
export function ruleWord(vocab: SettingsVocabulary, field: string): string {
  const mapped = vocab.rule[field];
  if (mapped) return mapped;
  const p = plain(field);
  return p ? p.charAt(0).toUpperCase() + p.slice(1) : '';
}

/**
 * valueWord renders one before/after cell.
 *
 * THREE ARMS, IN THIS ORDER, AND THE FIRST ONE IS THE INTERESTING ONE:
 *
 * 1. AN EMPTY VALUE IS "Not set". The server drops empty values at the audit record boundary, so a
 * knob whose pre-image was genuinely empty: an uncurated chip set, a text column on a
 * half-migrated row: arrives here as ''. An empty table cell reads as a rendering bug, and the
 * server deliberately does NOT invent a token for it (that would be a fabricated value sitting
 * in an append-only trail forever). Saying so in words is this layer's job, and "Not set" is a
 * statement about the record rather than a guess at what the value was.
 * 2. THE KNOB'S OWN WORD, from the map the owning module supplies.
 * 3. THE VALUE ITSELF, lowercased and de-underscored. Numbers pass through untouched, which is
 * what "40" and "90" want, and a stored word nobody has mapped yet reads as strange rather than
 * as an invented phrase.
 */
export function valueWord(vocab: SettingsVocabulary, field: string, raw: string): string {
  const v = String(raw ?? '');
  if (v === '') return 'Not set';
  const mapped = vocab.value[field]?.[v];
  if (mapped) return mapped;
  return plain(v);
}
