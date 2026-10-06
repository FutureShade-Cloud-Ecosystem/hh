// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-hotkeys.test.mjs; adapted for HH.

// _pos-hotkeys.test.mjs: the counter's F-key roster and the pinned legend (the ask N1).
//
// "a function key advances the step, an F12-style key takes payment": plus a persistent legend
// pinned at the bottom "so nobody memorises", showing the current step's keys and dimming what is
// not available. And the part that decides the architecture: the keys must be the DEALER's,
// because BisTrack refugees will want their old ones.
//
// So the roster is DATA (resolved server-side through the settings kit: see
// backend/internal/pos/hotkeys_test.go) and this file pins the frontend's half: the chord grammar,
// the exact-match rule that IS the browser escape, availability, and the legend's shape.
//
// node --import./src/_ts-js-loader.mjs --test src/_pos-hotkeys.test.mjs (run from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  asHotkeyRoster,
  chordFromEvent,
  resolveHotkey,
  available,
  legendKeys,
  keycap,
  stepAfter,
  FALLBACK_ROSTER,
  BISTRACK_ROSTER,
  validChord,
  collisionFor,
  HOTKEY_LABELS,
  HK_STEP_NEXT,
  HK_STEP_BACK,
  HK_PAY,
  HK_PARK,
  HK_SEARCH,
} from './_pos-hotkeys.ts';

const code = (t) => t.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '').replace(/<!--[\s\S]*?-->/g, '');
const terminalSrc = code(readFileSync(new URL('./pos-terminal.ts', import.meta.url), 'utf8'));
const legendSrc = code(readFileSync(new URL('./pos-hotkey-legend.ts', import.meta.url), 'utf8'));

const key = (k, mods = {}) => ({ key: k, shiftKey: false, ctrlKey: false, altKey: false, metaKey: false, ...mods });
const state = (over = {}) => ({ step: 'items', lineCount: 0, rungDone: false, ringBusy: false, tenderable: false, ...over });

// ── the roster is data, and the component never invents one ────────────────────────────────────

test('hotkeys: the roster arrives from the server; a malformed body degrades to the shipped fallback', () => {
  const wire = { keys: [{ action: 'pay', key: 'F12', browser_owned: true }] };
  assert.deepEqual(asHotkeyRoster(wire), [{ action: 'pay', key: 'f12', browser_owned: true }]);
  for (const bad of [null, undefined, {}, { keys: 'nope' }, { keys: [] }, 'garbage']) {
    assert.deepEqual(asHotkeyRoster(bad), FALLBACK_ROSTER, 'a till is never left with no keys');
  }
});

test('hotkeys: a row this build cannot RENDER draws no keycap: a dead key is worse than none', () => {
  const wire = { keys: [{ action: 'pay', key: 'f8' }, { action: 'teleport', key: 'f7' }] };
  assert.deepEqual(asHotkeyRoster(wire), [{ action: 'pay', key: 'f8', browser_owned: false }]);
  assert.equal(HOTKEY_LABELS['teleport'], undefined);
});

test('hotkeys: every label is chrome: one or two words, never a sentence', () => {
  for (const [id, label] of Object.entries(HOTKEY_LABELS)) {
    assert.ok(label.length > 0, `${id} has no label`);
    assert.ok(label.split(/\s+/).length <= 2, `${id}: "${label}" is not chrome`);
    assert.ok(!/[.!?]/.test(label), `${id}: "${label}" is a sentence`);
  }
});

test('hotkeys: the roster is NOT hard-coded in a component: the terminal reads it', () => {
  assert.match(terminalSrc, /void this\.loadHotkeys\(\)/);
  assert.match(terminalSrc, /this\.hotkeyRoster = asHotkeyRoster\(await posHotkeys\(this\.branchId\)\)/);
  assert.ok(!/'f8'|"f8"/.test(legendSrc), 'the legend component names no key at all');
  assert.match(legendSrc, /legendKeys\(this\.roster, this\.state\)/, 'it renders whatever the dealer configured');
});

// ── the chord grammar, and why ordinary typing is untouched ────────────────────────────────────

test('hotkeys: only FUNCTION keys produce a chord: every letter still reaches the scan field', () => {
  assert.equal(chordFromEvent(key('F8')), 'f8');
  assert.equal(chordFromEvent(key('F12')), 'f12');
  assert.equal(chordFromEvent(key('F2', { shiftKey: true })), 'shift-f2');
  assert.equal(chordFromEvent(key('F4', { ctrlKey: true, altKey: true })), 'ctrl-alt-f4');
  for (const k of ['a', '8', 'Enter', 'Escape', ' ', 'F13', 'ArrowDown']) {
    assert.equal(chordFromEvent(key(k)), '', `${k} must stay the field's`);
  }
});

test('hotkeys: modifiers spell in ONE canonical order, matching the server\'s grammar', () => {
  assert.equal(chordFromEvent(key('F8', { shiftKey: true, ctrlKey: true, altKey: true })), 'shift-ctrl-alt-f8');
});

// ── THE BROWSER ESCAPE ─────────────────────────────────────────────────────────────────────────

test('hotkeys: a binding matches its EXACT chord: which is how a browser-owned key stays escapable', () => {
 // The client asked for F12 by name. Refusing it would be us overruling his ergonomics; what we
 // owe instead is that the browser's own gesture is always still reachable. Bind F12 to payment,
 // and Shift+F12 / Ctrl+F12 are simply unmatched: so preventDefault is never called on them.
  const roster = [{ action: HK_PAY, key: 'f12', browser_owned: true }];
  assert.equal(resolveHotkey(chordFromEvent(key('F12')), roster), HK_PAY);
  assert.equal(resolveHotkey(chordFromEvent(key('F12', { shiftKey: true })), roster), null);
  assert.equal(resolveHotkey(chordFromEvent(key('F12', { ctrlKey: true })), roster), null);
  assert.equal(resolveHotkey(chordFromEvent(key('F12', { metaKey: true })), roster), null);
});

test('hotkeys: an unbound key resolves to nothing, so its native meaning survives untouched', () => {
  assert.equal(resolveHotkey('f5', FALLBACK_ROSTER), null, 'F5 still reloads');
  assert.equal(resolveHotkey('f3', FALLBACK_ROSTER), null, 'F3 still finds');
  assert.equal(resolveHotkey('', FALLBACK_ROSTER), null);
  assert.equal(resolveHotkey('f8', null), null, 'no roster binds nothing');
});

test('hotkeys: the shipped fallback takes no key a browser owns', () => {
 // SUPERSEDED IN PART by ruling R1, and rewritten the way D-107 rewrote the DIB-425
 // floor: keep the name, pin what the gate was actually protecting.
 //
 // It protected a default that silently fights the host browser. The ruling changed exactly one
 // thing: the client asked for F12 BY NAME ("bistrack does... [F]12 to take a payment"), so the
 // pay key is a browser key on purpose and its escape rule is what keeps it honest. Every OTHER
 // shipped key must still be one no mainstream browser claims.
  const owned = new Set(['f1', 'f3', 'f5', 'f6', 'f7', 'f10', 'f11', 'f12']);
  for (const r of FALLBACK_ROSTER) {
    const bare = r.key.split('-').pop();
    if (r.action === HK_PAY) {
      assert.equal(r.key, 'f12', 'the one browser-owned default is the client-named payment key');
      continue;
    }
    assert.ok(!owned.has(bare) || r.key !== bare, `the default binds ${r.key}, which the browser owns`);
  }
});

test('hotkeys: the BisTrack defaults are F4 to advance and F12 to pay, and F8 is left unbound', () => {
 // The counter lead: "bistrack does [F]4 to move to the next step and then [F]12 to take a
 // payment." The frontend fallback and the server default are one roster or they are a bug.
  const by = Object.fromEntries(FALLBACK_ROSTER.map((r) => [r.action, r.key]));
  assert.equal(by[HK_STEP_NEXT], 'f4');
  assert.equal(by[HK_PAY], 'f12');
  assert.equal(by[HK_STEP_BACK], 'shift-f4', 'back pairs with the advance key rather than taking a key of its own');
  assert.ok(!FALLBACK_ROSTER.some((r) => r.key === 'f8'), 'the retired ring key is unbound, never rebound');
  assert.deepEqual([...BISTRACK_ROSTER], FALLBACK_ROSTER, 'reset-to-defaults and cold start are one list');
 // HH note: the reference cross-checked these defaults against its own Go core here; HH's core
 // carries its own hotkey defaults, so that half stayed with the reference (CLOSURE.md). The
 // wiring work item re-pins the two rosters against each other when the seam is live.
});

test('hotkeys: a letter chord needs Ctrl or Alt, so ordinary typing still reaches the scan box', () => {
 // Colton floated "control shift b" by name, so the grammar takes modifier chords over letters:
 // but Shift alone is how a capital letter is typed, and the scan box owns every typed key.
  assert.equal(chordFromEvent(key('b', { ctrlKey: true })), 'ctrl-b');
  assert.equal(chordFromEvent(key('B', { ctrlKey: true, shiftKey: true })), 'shift-ctrl-b');
  assert.equal(chordFromEvent(key('7', { altKey: true })), 'alt-7');
  assert.equal(chordFromEvent(key('b')), '', 'a bare letter is typing');
  assert.equal(chordFromEvent(key('B', { shiftKey: true })), '', 'a capital letter is still typing');
  assert.equal(chordFromEvent(key('b', { ctrlKey: true, metaKey: true })), '', 'Meta is never bindable');
});

test('hotkeys: the editor refuses a bad chord and names the action a key would collide with', () => {
  assert.ok(validChord('f4') && validChord('shift-f4') && validChord('ctrl-b') && validChord('shift-ctrl-alt-f8'));
  assert.ok(!validChord('b'), 'a bare letter types');
  assert.ok(!validChord('shift-b'), 'shift alone types');
  assert.ok(!validChord('f13') && !validChord('') && !validChord('meta-f4'));
  assert.ok(!validChord('ctrl-shift-b'), 'one spelling per chord: canonical order only');
  assert.ok(!validChord('shift-shift-f4'));
 // The collision is named by the action it would break, BEFORE the save, not after.
  assert.equal(collisionFor(FALLBACK_ROSTER, HK_PARK, 'f4'), HK_STEP_NEXT);
  assert.equal(collisionFor(FALLBACK_ROSTER, HK_STEP_NEXT, 'f4'), null, 'a key does not collide with itself');
  assert.equal(collisionFor(FALLBACK_ROSTER, HK_PARK, 'ctrl-b'), null);
});

test('hotkeys: the terminal yields to the SHELL and never swallows an unavailable key', () => {
 // Two maps on one window is one map from the operator's point of view, or it is a bug.
  assert.match(terminalSrc, /if \(e\.defaultPrevented\) return;/);
  assert.match(terminalSrc, /if \(!this\.runHotkey\(action\)\) return;[^]{0,80}e\.preventDefault\(\);/,
    'preventDefault happens only after something actually happened');
});

test('hotkeys: a DIMMED counter key never reaches the shell map and opens a tab mid-sale', () => {
 // The defect this pins: the shell binds F1-F4/F9 for its own dealer map on the same window, so a
 // counter key whose action is unavailable used to fall through: F4 on the payment step walked
 // the clerk to the product catalogue. The counter claims the CHORD (stopPropagation) whether or
 // not the action ran; it claims the BROWSER's meaning (preventDefault) only when it ran.
  const body = terminalSrc.slice(terminalSrc.indexOf('private onPosKey'), terminalSrc.indexOf('private runHotkey'));
  const stop = body.indexOf('e.stopPropagation()');
  const guard = body.indexOf('if (!this.runHotkey(action)) return;');
  const prevent = body.indexOf('e.preventDefault()');
  assert.ok(stop > 0 && guard > 0 && prevent > 0, 'all three calls are present');
  assert.ok(stop < guard, 'the chord is claimed from the shell BEFORE the availability guard');
  assert.ok(guard < prevent, 'the browser keeps its own meaning until the action actually ran');
});

// ── availability: a dimmed keycap means what a disabled control means ──────────────────────────

test('hotkeys: Next and Back are live only where there is a step to go to', () => {
  assert.equal(available(HK_STEP_NEXT, state({ step: 'customer' })), true);
  assert.equal(available(HK_STEP_NEXT, state({ step: 'summary' })), false, 'nothing after the last step');
  assert.equal(available(HK_STEP_BACK, state({ step: 'customer' })), false, 'nothing before the first');
  assert.equal(available(HK_STEP_BACK, state({ step: 'payment' })), true);
  assert.deepEqual(
    ['customer', 'items', 'payment', 'summary'].map((s) => stepAfter(s, 1)),
    ['items', 'payment', 'summary', null],
  );
  assert.equal(stepAfter('customer', -1), null);
  assert.equal(stepAfter('', 1), 'customer', 'a closed accordion advances into the first step');
});

test('hotkeys: Pay is live on exactly the CTA\'s own predicate: lines, a tender, not rung, not busy', () => {
  const ready = state({ lineCount: 2, tenderable: true });
  assert.equal(available(HK_PAY, ready), true);
  assert.equal(available(HK_PAY, { ...ready, lineCount: 0 }), false);
  assert.equal(available(HK_PAY, { ...ready, tenderable: false }), false);
  assert.equal(available(HK_PAY, { ...ready, rungDone: true }), false);
  assert.equal(available(HK_PAY, { ...ready, ringBusy: true }), false);
});

test('hotkeys: Park needs something to park; Search stands down once the sale is rung', () => {
  assert.equal(available(HK_PARK, state({ lineCount: 0 })), false);
  assert.equal(available(HK_PARK, state({ lineCount: 1 })), true);
  assert.equal(available(HK_PARK, state({ lineCount: 1, rungDone: true })), false);
  assert.equal(available(HK_SEARCH, state()), true);
  assert.equal(available(HK_SEARCH, state({ rungDone: true })), false);
});

test('hotkeys: availability derives from the SAME facts the controls disable on', () => {
  assert.match(terminalSrc, /private hotkeyState\(\): HotkeyState \{[^]{0,400}tenderable: this\.payMethod !== '' && this\.payMethod !== 'SPLIT'/);
  assert.match(terminalSrc, /lineCount: this\.cart\.length/);
  assert.match(terminalSrc, /rungDone: this\.ringDone\(\)/);
});

// ── the legend ─────────────────────────────────────────────────────────────────────────────────

test('legend: the keys never MOVE: every bound action shows at every step, dimmed when not live', () => {
 // A legend whose contents rearrange as the sale progresses is a legend nobody can build muscle
 // memory against, which was the entire point of asking for one.
  const early = legendKeys(FALLBACK_ROSTER, state({ step: 'customer' }));
  const late = legendKeys(FALLBACK_ROSTER, state({ step: 'payment', lineCount: 2, tenderable: true }));
  assert.deepEqual(early.map((k) => k.action), late.map((k) => k.action));
  assert.deepEqual(early.map((k) => k.action), FALLBACK_ROSTER.map((r) => r.action), 'and in roster order');
  assert.equal(early.find((k) => k.action === HK_PAY).live, false, 'Pay is dim before there is a ticket…');
  assert.equal(late.find((k) => k.action === HK_PAY).live, true, '…and lights when the sale is tenderable');
});

test('legend: a keycap is a symbol, not a sentence', () => {
  assert.equal(keycap('f8'), 'F8');
  assert.equal(keycap('shift-f2'), '⇧F2');
  assert.equal(keycap('ctrl-alt-f4'), '⌃⌥F4');
  assert.equal(keycap(''), '');
});

test('legend: it is chrome: pinned, not focusable, not clickable, and not announced', () => {
 // AMENDED BY THE 27-AUG RULING (R1), keeping the name and what the gate protected: the KEYCAPS
 // are still chrome: not clickable, not announced, no tab stops: because every action on the bar
 // has a real control on the screen and duplicating them here would put five more stops between
 // the scan field and the ticket. What the ruling added is exactly one control: the dismiss, for
 // the person who does not use keys and wants the pixels back. One stop, not six.
  assert.match(legendSrc, /position: fixed;/);
  assert.match(legendSrc, /bottom: 0;/);
  assert.match(legendSrc, /aria-hidden="true"/, 'the keycaps are not announced');
  assert.match(legendSrc, /pointer-events: none;/, 'the BAR cannot intercept a click meant for the sale');
  const buttons = legendSrc.match(/<button/g) ?? [];
  assert.equal(buttons.length, 1, 'exactly one control on the legend: the dismiss');
  assert.match(legendSrc, /data-testid="pos-legend-dismiss"/);
  assert.match(legendSrc, /aria-label="Hide the key legend"/, 'and it has a real accessible name');
  assert.ok(!/tabindex/.test(legendSrc), 'no keycap is a tab stop');
});

test('legend: dismissing it hides the BAR and never the keys, and the way back needs no admin', () => {
 // The back-office lead: "No, I don't even know what that is": the same side that wants the
 // screen real estate back. The counter lead wants the keys. Both, at one dealer, on one screen.
  assert.match(legendSrc, /pos-legend-dismiss/, 'the bar reports the dismissal, it does not decide it');
  assert.ok(!/roster\s*=\s*\[\]|this\.roster\s*=/.test(legendSrc), 'the legend never edits the roster');
 // The terminal hides the STRIP on a panel flag and leaves the listener and the roster alone.
  assert.match(terminalSrc, /this\.panels\[PANEL_LEGEND\] === false\s*\n?\s*\? nothing/);
  const listener = terminalSrc.slice(terminalSrc.indexOf('private onPosKey'), terminalSrc.indexOf('private runHotkey'));
  assert.ok(!/panels/.test(listener), 'a dismissed legend does not touch the key listener');
 // And the way back is a counter control, not a settings screen an administrator has to open.
  assert.match(terminalSrc, /data-testid="pos-view"/);
  assert.match(terminalSrc, /data-testid=\$\{`pos-view-\$\{id\}`\}/, 'one switch per registered panel');
});

test('legend: every action it advertises still has a visible control on the counter', () => {
 // A key is a shortcut, never the only path (R1). Next/Back are the flow stepper, Pay is the
 // complete-sale CTA, Park is the always-on util button, Search is the scan box itself.
  assert.match(terminalSrc, /<hos-pos-flow-steps/, 'Next / Back: the tracked stepper is clickable');
  assert.match(terminalSrc, /class="complete-cta"|complete-cta/, 'Pay: the gold CTA');
  assert.match(terminalSrc, /@click=\$\{this\.parkCurrent\}/, 'Park: the always-on util button');
  assert.match(terminalSrc, /<hos-pos-scan-box/, 'Search: the one field the counter types into');
});

test('legend: an unavailable key DIMS rather than disappearing, and a browser-owned key is marked', () => {
  assert.match(legendSrc, /\.k\.off \{\s*\n\s*opacity: 0\.42;/);
  assert.match(legendSrc, /k\.live \? '' : 'off'/);
  assert.match(legendSrc, /\.k\.host \.cap \{\s*\n\s*border-style: dashed;/);
  const owned = legendKeys([{ action: HK_PAY, key: 'f12', browser_owned: true }], state());
  assert.equal(owned[0].browserOwned, true);
});

test('legend: an empty roster renders NOTHING: never an empty bar across the foot of the screen', () => {
  assert.deepEqual(legendKeys([], state()), []);
  assert.deepEqual(legendKeys(null, state()), []);
  assert.match(legendSrc, /if \(keys\.length === 0\) return nothing;/);
});

test('legend: it is mounted once, at the terminal\'s root, outside the scrolling column', () => {
  assert.match(terminalSrc, /<hos-pos-hotkey-legend\s*\n\s*\.roster=\$\{this\.hotkeyRoster\}\s*\n\s*\.state=\$\{this\.hotkeyState\(\)\}/);
  assert.equal((terminalSrc.match(/<hos-pos-hotkey-legend/g) ?? []).length, 1);
 // THE CLAIM SURVIVED THE NUMBER (the wave-1 layout contract). This line used to pin the
 // literal `96px`, with a SECOND literal: 132px, under a max-width:1180 media query: reserving a
 // different foot on a touch till. Both were guesses at the height of two fixed bars this file
 // cannot see: the legend across the bottom, and the shell's Feedback pill sitting on top of its
 // right-hand end. The pill is now arithmetic over its own declarations (--hos-fab-lane = 22 + 38
 // + 22 = 82, tokens.css) and the legend measures 35 on the live stack, so the lane is the greater
 // of two OVERLAPPING bars at both pointer types and one value serves every width. What this test
 // asserts is exactly what it asserted before: the column reserves the foot, so the legend never
 // covers the last control: and what it now also refuses is the surface going back to guessing.
  const flowcol = terminalSrc.match(/\.flowcol \{[^}]*\}/g) ?? [];
  // THE COUNT GREW BY ONE, NAMED (the till fill work). The base column rule
  // and its touch-till loosening are the stepper's; the panels view's column
  // is a third rule, scoped to the layout pick through the host attribute,
  // and it owes the same no-guesses answer to the foot: it ends the grid's
  // column above the legend's lane (--pos-legend-lane, named on the host
  // beside --pos-column-max) rather than above a reserved blank.
  assert.equal(flowcol.length, 3, 'the base column rule, its touch-till loosening, and the panels view column');
  assert.match(flowcol[0], /padding: var\(--hos-space-4\) var\(--hos-space-5\) calc\(var\(--hos-fab-lane\) \+ var\(--hos-safe-bottom\)\);/,
    'and the column reserves the foot so the legend never covers the last control');
  assert.match(terminalSrc, /:host\(\[data-counter-layout='PANELS'\]\) \.flowcol \{/,
    'the third rule is the panels view column, scoped to the layout pick');
  assert.match(flowcol[2], /padding: var\(--hos-space-1\) var\(--hos-space-4\) var\(--pos-legend-lane\);/,
    'and it reserves the legend lane the same way, by name');
  assert.ok(
    flowcol.every((r) => !/\d+px/.test(r)),
    'every number in the sale column is a token: a bare pixel here is a guess at something this file does not own',
  );
});

test('the counter has ONE measure, and both things that cap read it', () => {
 // WHY THIS LIVES BESIDE THE LEGEND TEST. The claim above is the same claim one level up: the
 // counter is a surface that cannot see the chrome around it, so every width it commits to has to
 // come from somewhere nameable. Wave 1 made 'pos' a `work` pane and took the shell's 1000px cap
 // away, at which point the counter's own two caps: the sale column and the Counter-settings
 // overlay: stopped being incidental (980 against a 1000px pane) and became the whole answer.
 // They were 980 and 760, two literals with no relationship, and the failure mode of two literals
 // that must agree is that one of them gets edited.
  assert.match(terminalSrc, /--pos-column-max: 1180px;/, 'the counter names its measure once, on its host');
  assert.equal((terminalSrc.match(/--pos-column-max: /g) ?? []).length, 1, 'declared once, or it is not one measure');
  assert.match(terminalSrc, /\.flowcol \{[^}]*width: min\(100%, var\(--pos-column-max\)\);/,
    'the sale column reads the measure');
  assert.match(terminalSrc, /\.cspanel \{[^}]*width: min\(var\(--pos-column-max\), 100%\);/,
    'and so does the Counter-settings overlay');
 // BORDER-BOX ON BOTH, because a cap that is not the painted box is not a cap. Content-box turned
 // `min(100%, 980px)` into 1020 painted pixels and `min(760px, 100%)` into 794, and at 1024 it put
 // 28px of gutter on one side of the sale column and 4px on the other.
  for (const rule of ['\\.flowcol', '\\.cspanel']) {
    const m = new RegExp(`${rule} \\{[^}]*box-sizing: border-box;`);
    assert.match(terminalSrc, m, `${rule.replace('\\', '')} must mean the painted box, not the box plus its padding`);
  }
 // The overlay is NOT a wall: min() keeps it inside a 1024 till, where the scrim's own 16px
 // gutters leave 992 and the panel takes all of it.
  assert.doesNotMatch(terminalSrc, /\.cspanel \{[^}]*width: 1180px/, 'a fixed width would overflow a narrow till');
});

test('counter settings answers the width it is given with COLUMNS, not one long row', () => {
 // The form half of the same contract. A form pane goes fluid like a work pane, and a form that
 // does nothing about it ships a 1146px row with a label at x=0 and a 44px keycap at x=1100:
 // the dead-space defect re-created inside the surface that was supposed to have fixed it.
 // The recipe is the app's (--hos-pane-columns over --hos-form-col, tokens.css) and is used
 // verbatim so that if the app's form column ever moves, this moves with it.
  const settingsSrc = code(readFileSync(new URL('./pos-counter-settings.ts', import.meta.url), 'utf8'));
  const root = /\[data-testid='pos-counter-settings'\] \{[^}]*\}/.exec(settingsSrc);
  assert.ok(root, 'the settings root has no layout rule at all');
  assert.match(root[0], /display: grid;/);
  assert.match(root[0], /grid-template-columns: var\(--hos-pane-columns\);/,
    'the recipe is the shared one, not a second set of numbers invented here');
  assert.match(root[0], /align-items: start;/, 'five boxes of five heights must not be stretched to the tallest');
 // NO MEDIA QUERY. auto-fit reads the PANEL's real width, so a popped-out window and a narrowed
 // browser get the right column count with no breakpoint for anyone to keep in step.
  assert.doesNotMatch(settingsSrc, /@media[^{]*width/, 'the column count is auto-fit\'s job, not a breakpoint\'s');
 // The two widest rows span every track..trow is SEVEN columns and measures ~470px before the
 // product name has any width; packed into a 320px track it would ellipsise every name on screen.
  assert.match(settingsSrc, /\[data-testid='cs-tiles'\],\s*\n\s*hos-settings-history,/,
    'the tile board and the history take the whole panel');
 // And the stack's own margin went with the change, or the grid would show 30px between rows and
 // 20px between columns and read as a bug.
  assert.match(settingsSrc, /\.box \{[^}]*margin-bottom: 0;/, 'the gap owns the rhythm now, not a leftover margin');
});


// ── what the keyboard walkthrough found, and what now stops it coming back ──────────────────────

test('hotkeys: the counter listens in CAPTURE, because the shell already binds these keys', () => {
 // THE DEFECT THIS PINS. The shipped dealer map (0171) binds F1-F4, F8 and F9. The shell listens
 // on bubble and calls preventDefault; the counter's listener yields to defaultPrevented. Result:
 // every advertised counter key was dead on arrival, and pressing the key printed on the legend
 // opened a new Sales tab under the clerk mid-sale. Found by driving a real sale with a keyboard.
  assert.match(terminalSrc, /window\.addEventListener\('keydown', this\.onPosKey, true\);/,
    'capture phase: the innermost surface with a binding sees the key first');
  assert.match(terminalSrc, /window\.removeEventListener\('keydown', this\.onPosKey, true\);/,
    'and the same phase on the way out, or the listener leaks');
 // A key the counter HANDLED must not also reach the shell map. The claim now happens one line
 // EARLIER than it used to: stopPropagation runs for any chord on the counter's roster, so a
 // dimmed key cannot fall through to the shell either (see the dimmed-key test above): which
 // covers the handled case a fortiori.
  const body = terminalSrc.slice(terminalSrc.indexOf('private onPosKey'), terminalSrc.indexOf('private runHotkey'));
  assert.match(body, /e\.stopPropagation\(\);[^]{0,120}e\.preventDefault\(\);/,
    'a key the counter HANDLED must not also reach the shell map');
});

test('hotkeys: a key the counter does NOT handle propagates to the shell untouched', () => {
 // The claim is scoped by mounting and by handling: the listener exists only while the counter is
 // on screen, and it claims a key only when runHotkey actually did something. Everything else:
 // the shell's other bindings, F5, the browser's own keys: is left alone.
  assert.match(terminalSrc, /if \(!this\.runHotkey\(action\)\) return;/);
  assert.match(terminalSrc, /if \(!action\) return;/, 'an unbound key is not touched at all');
  assert.match(terminalSrc, /if \(e\.defaultPrevented\) return;/, 'and a key someone upstream already took is left taken');
});

test('accordion: a step move lands focus IN the step: never on <body>', () => {
 // The region lives in hos-pos-step-card's SHADOW root, so the old
 // `this.renderRoot.querySelector('[data-testid="step-panel-…"]')` fallback could never resolve.
 // Focus silently fell to <body> after every F-key step move, which puts the next Tab back at the
 // top of the document: the counter's version of being thrown out of the screen.
  assert.match(terminalSrc, /private stepRegion\(step: FlowStepId\): HTMLElement \| null \{[^]{0,220}card\?\.shadowRoot\?\.querySelector/);
  assert.match(terminalSrc, /if \(region\) region\.focus\?\.\(\{ preventScroll: true \}\);\s*\n\s*else this\.renderRoot\.querySelector<HTMLElement>\(`\[data-testid="step-head-/,
    'and the last resort is the header, which is a real control: not the document body');
});

test('terminal: the counter takes the keyboard on mount, and only if nobody else has it', () => {
 // Opening POS used to leave focus on <body>: thirteen Tab presses from the top of the document
 // before a counter control, on a screen whose premise is that a cashier picks up the gun and
 // shoots. The scan box's own autofocus cannot help: at mount the Items card is closed and the
 // box is not rendered at all.
  assert.match(terminalSrc, /protected firstUpdated\(\): void/);
  assert.match(terminalSrc, /if \(active && active !== document\.body\) return;/,
    'a surface that grabs focus unconditionally is worse than one that never grabs it');
  assert.match(terminalSrc, /if \(this\.openStep\) this\.openAccordion\(this\.openStep\);/);
});

test('CTA keycap: the Complete-sale chip names the key that actually rings the sale', () => {
 // UNION TRAIN 14. There are TWO hotkey substrates and they are not the same table:
 // · the COUNTER roster: surface_role_items kind `action` on pos.counter, defaulting to
 // internal/pos/hotkeys.go's defaultHotkeys. This is what onPosKey listens to.
 // · the SHELL's map: dealer_hotkeys over 0191's `app.shell` roster, the app-wide nav keys.
 //
 // The CTA's keycap used to render the SHELL's binding. That was invisible while both said F8.
 // Ruling R1 moved the counter's pay key to F12 and left the shell alone, at which
 // point the counter's primary button began advertising a key that is deliberately UNBOUND:
 // press F8, nothing rings. The chip must therefore come from the roster that rings the sale.
  assert.match(
    terminalSrc,
    /private payKeycap\(\): string \| null \{[^]{0,200}this\.hotkeyRoster\.find\(\(r\) => r && r\.action === HK_PAY\)/,
    'the CTA keycap must be derived from the counter roster, not from a shell-supplied binding',
  );
  assert.match(terminalSrc, /data-testid="pay-hotkey-chip"[^]{0,120}\$\{this\.payKeycap\(\)\}/,
    'and the chip must render that value');
 // D-111 FINISHED WHAT TRAIN 14 STARTED, AND THIS LINE RECORDS THE DIFFERENCE. Train 14 corrected
 // the READ and kept the shell binding as a fallback for a build whose counter roster had no pay
 // row. The ruling retires `sale.pay` from the shell map outright, so the fallback has nothing to
 // read and is gone. The assertion below used to be `match(/return this\.payHotkey;/)`: the
 // fallback's existence. It is now its ABSENCE, because a single source is the only arrangement in
 // which these two facts cannot drift apart again.
  assert.doesNotMatch(terminalSrc, /payHotkey/,
    'the till holds no shell-supplied pay binding at all: one source for the chip, by ruling');
  assert.match(terminalSrc, /return mine\?\.key \? keycap\(mine\.key\) : null;/,
    'and no pay row means no chip, rather than a chip naming some other map’s key');
});

test('CTA keycap: what it says agrees with the shipped default roster: F12, not F8', () => {
 // The end-to-end fact the ruling cares about: a BisTrack refugee reads the button and presses
 // the key on it. FALLBACK_ROSTER is what a cold counter runs on, so the chip it produces must be
 // the pay key R1 named.
  const pay = FALLBACK_ROSTER.find((r) => r.action === HK_PAY);
  assert.equal(pay.key, 'f12');
  assert.equal(keycap(pay.key), 'F12');
  assert.notEqual(keycap(pay.key), 'F8', 'F8 is unbound by R1: a chip that says F8 is a lie');
});
