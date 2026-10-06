// Ported from hardscapeos_dibbits master 87d7941b app/src/modal-focus.test.mjs; adapted for HH.

// E6: the modal focus trap's arithmetic, plus the app-wide gate that every modal-class component
// actually installs it.
//
// WHAT IS TESTED HERE AND WHAT IS NOT
// -----------------------------------
// nextTrapIndex/pickInitialIndex are pure integer functions precisely so the WRAP RULE can be pinned
// without a DOM. The DOM half of the trap (deepActiveElement descending shadow roots, getClientRects
// visibility, focus restoration) cannot be honestly faked in Node: a hand-rolled stub would assert
// that my mock behaves like my mock. That half is measured in a real Chromium keyboard walk, per
// modal, recorded in walkthrough-w6d/. Where the two ever disagree, Chromium wins.
//
// focusablesIn's composed-tree WALK is the one exception, added for DIB-564's refuted clause: a
// hand-built object satisfying exactly `matches`/`children`/`shadowRoot` is not "my mock behaves
// like my mock" in the way getClientRects/focus() would be: it exercises the REAL exported walk
// (composedChildren/collectComposed, reached only through focusablesIn) against a tree shaped like
// the one that starved a real control, and pins the fix at the level a Chromium walk cannot: the
// SHAPE of the traversal, byte-for-byte, on every future refactor.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { nextTrapIndex, pickInitialIndex, FOCUSABLE_SELECTOR, focusablesIn } from '@hardscapehouse/pos-kit/src/ported/modal-focus.ts';

const here = dirname(fileURLToPath(import.meta.url));
// modal-focus.ts moved into @hardscapehouse/pos-kit (web/packages/pos-kit,
// src/ported, same name) with the customer picker it serves; the source-gate
// reads below follow it there.
const kitPorted = join(here, '..', '..', '..', '..', 'packages', 'pos-kit', 'src', 'ported');
const read = (p) => readFileSync(join(here, p), 'utf8');
const readKit = (p) => readFileSync(join(kitPorted, p), 'utf8');

// ── 0 · the composed-tree walk (DIB-564 refuted: focusablesIn starved a nested shadow root) ──────
//
// A minimal fake Element: exactly the three members composedChildren/collectComposed touch
// (`tagName`, `shadowRoot`, `children`) plus `matches`, answered from a flag the test sets rather
// than real CSS parsing: the same "fake only what the code reads" posture popout-controller.test.mjs
// already takes for `globalThis.document`/`window` elsewhere in this suite.
function fakeEl({ tag = 'DIV', focusable = false, shadow = null, children = [] } = {}) {
  return {
    tagName: tag,
    shadowRoot: shadow,
    children,
    matches: (sel) => focusable && sel === FOCUSABLE_SELECTOR,
    hasAttribute: () => false,
    getAttribute: () => null,
  };
}
const fakeShadow = (children) => ({ children });

test('focusablesIn finds a focusable control inside a NESTED shadow root', () => {
 // The exact reproduction: a plain container (the sheet), a button before and after, and: in the
 // middle: a custom element (hos-settings-history) whose OWN shadow root holds a focusable
 // <summary>. A bare container.querySelectorAll(FOCUSABLE_SELECTOR) never crosses into `shadow`;
 // this must.
  const summary = fakeEl({ tag: 'SUMMARY', focusable: true });
  const closeBtn = fakeEl({ tag: 'BUTTON', focusable: true });
  const discardBtn = fakeEl({ tag: 'BUTTON', focusable: true });
  const history = fakeEl({ tag: 'HOS-SETTINGS-HISTORY', shadow: fakeShadow([summary]) });
  const container = fakeEl({ children: [closeBtn, history, discardBtn] });

  const found = focusablesIn(container);
  assert.equal(found.length, 3, `expected 3 focusables (found ${found.length}): the nested summary was starved`);
  assert.equal(found[0], closeBtn);
  assert.equal(found[1], summary, 'the nested shadow root\'s control must be found, in its DOCUMENT position');
  assert.equal(found[2], discardBtn);
});

test('focusablesIn crosses TWO shadow boundaries, still in document order', () => {
 // One nested custom element is the reproduction; the walk must not be a one-level special case.
  const innerBtn = fakeEl({ tag: 'BUTTON', focusable: true });
  const inner = fakeEl({ tag: 'HOS-INNER', shadow: fakeShadow([innerBtn]) });
  const outer = fakeEl({ tag: 'HOS-OUTER', shadow: fakeShadow([inner]) });
  const container = fakeEl({ children: [outer] });

  assert.deepEqual(focusablesIn(container), [innerBtn]);
});

test('focusablesIn: a light-DOM child that is NOT a shadow host still yields its own focusables', () => {
 // The fix must not turn INTO a "shadow roots only" special case that stops walking a plain nested
 // div's own children: the common, non-shadow case has to keep working exactly as it always did.
  const btn = fakeEl({ tag: 'BUTTON', focusable: true });
  const plainDiv = fakeEl({ children: [btn] }); // no shadowRoot at all
  const container = fakeEl({ children: [plainDiv] });

  assert.deepEqual(focusablesIn(container), [btn]);
});

// ── 0b · the <slot> branch of the composed walk ──────────────────────────────────────────────────
//
// SHIPPED UNCOVERED, AND A REFUTER PROVED IT: deleting the entire `<slot>` branch from
// composedChildren left the whole frontend suite at 3160 pass / 0 fail: in a helper 24 ModalFocus
// containers across 20 consumer files call on every Tab. The branch was written on the reasoning
// that "no shipped consumer slots content into a nested component TODAY", which is exactly the
// argument that stops being true without anyone noticing.
//
// A <slot> is the one place light DOM re-enters a shadow tree, and it is the one node whose RENDERED
// children are neither its DOM children nor a shadow root's: they are whatever is ASSIGNED to it,
// with its own children as fallback only when nothing is. Get it wrong in either direction and the
// trap either starves (skips slotted controls) or double-counts (visits fallback content nobody can
// see): and because `nextTrapIndex` treats this list AS tab order, a wrong list is a wrong wrap.
//
// `fakeSlot` fakes exactly the one extra member the branch reads, `assignedElements`, the same
// "fake only what the code reads" posture as `fakeEl` above.
function fakeSlot({ assigned = null, fallback = [] } = {}) {
  return {
    tagName: 'SLOT',
    shadowRoot: null,
    children: fallback,
    matches: () => false,
    hasAttribute: () => false,
    getAttribute: () => null,
    ...(assigned === null ? {} : { assignedElements: ({ flatten }) => (flatten ? assigned : assigned) }),
  };
}

test('focusablesIn walks what is ASSIGNED to a slot, not the slot\'s own fallback', () => {
 // The dialog's light-DOM control, projected into a nested component's shadow tree. A walk that
 // read `children` here would find the fallback nobody can see and MISS the control on screen:
 // the trap would send focus to an element that is not rendered.
  const slotted = fakeEl({ tag: 'BUTTON', focusable: true });
  const fallbackBtn = fakeEl({ tag: 'BUTTON', focusable: true });
  const slot = fakeSlot({ assigned: [slotted], fallback: [fallbackBtn] });
  const container = fakeEl({ children: [slot] });

  const found = focusablesIn(container);
  assert.deepEqual(found, [slotted], 'the slot rendered its assigned content; the fallback is not on screen');
});

test('focusablesIn falls back to a slot\'s own children when NOTHING is assigned', () => {
 // The other half of the platform rule: an unfilled slot renders its fallback, and those controls
 // ARE on screen and ARE tab stops.
  const fallbackBtn = fakeEl({ tag: 'BUTTON', focusable: true });
  const slot = fakeSlot({ assigned: [], fallback: [fallbackBtn] });
  const container = fakeEl({ children: [slot] });

  assert.deepEqual(focusablesIn(container), [fallbackBtn], 'an unfilled slot renders its fallback and it must be reachable');
});

test('slotted content lands at the SLOT\'s position among its siblings, not appended after them', () => {
 // ORDER is the half that a membership-only assertion misses. The trap's wrap arithmetic assumes
 // this list IS tab order, so slotted content appended at the end would make Shift+Tab off the
 // first control land on the wrong element even though every control was "found".
  const before = fakeEl({ tag: 'BUTTON', focusable: true });
  const slotted = fakeEl({ tag: 'INPUT', focusable: true });
  const after = fakeEl({ tag: 'BUTTON', focusable: true });
  const slot = fakeSlot({ assigned: [slotted] });
  const container = fakeEl({ children: [before, slot, after] });

  assert.deepEqual(focusablesIn(container), [before, slotted, after]);
});

test('a slot INSIDE a nested shadow root projects the host\'s light DOM back into the walk', () => {
 // The whole composed path in one tree, which is the shape a real consumer would produce:
 // container > <hos-panel> (a shadow host)
 // #shadow-root > <slot> <- assigned the panel's light-DOM button
 // Neither the plain querySelectorAll this file replaced NOR a shadow-only walk finds that button:
 // the first cannot cross into #shadow-root, the second stops at the slot and reads no children.
  const projected = fakeEl({ tag: 'BUTTON', focusable: true });
  const slot = fakeSlot({ assigned: [projected] });
  const panel = fakeEl({ tag: 'HOS-PANEL', shadow: fakeShadow([slot]), children: [projected] });
  const container = fakeEl({ children: [panel] });

  assert.deepEqual(focusablesIn(container), [projected], 'the slotted control is unreachable by keyboard');
});

test('a FOCUSABLE slotted node is walked THROUGH as well as counted', () => {
 // The branch returns the assigned nodes; collectComposed must then push AND keep descending. The
 // shape that makes this bite is already all over this app: a load-board card is
 // `<div class="card" tabindex="0">`: itself a tab stop: with a checkbox and buttons inside it.
 // A walk that pushed the card and stopped would leave every control on it unreachable while the
 // list still looked non-empty, so the trap would report success and the dispatcher could not
 // operate the dialog.
  const inner = fakeEl({ tag: 'INPUT', focusable: true });
  const card = fakeEl({ tag: 'DIV', focusable: true, children: [inner] }); // tabindex="0" card
  const slot = fakeSlot({ assigned: [card] });
  const container = fakeEl({ children: [slot] });

  assert.deepEqual(focusablesIn(container), [card, inner], 'the slotted card was counted but not walked into');
});

test('a focusable node in an ORDINARY shadow root is walked through too, not just counted', () => {
 // The same rule off the slot path, so the descent is pinned on the branch 24 shipped containers
 // actually take today rather than only on the one no consumer uses yet.
  const inner = fakeEl({ tag: 'BUTTON', focusable: true });
  const summary = fakeEl({ tag: 'SUMMARY', focusable: true, children: [inner] });
  const host = fakeEl({ tag: 'HOS-SETTINGS-HISTORY', shadow: fakeShadow([summary]) });
  const container = fakeEl({ children: [host] });

  assert.deepEqual(focusablesIn(container), [summary, inner]);
});

test('a SLOT with no assignedElements at all is walked as an ordinary element', () => {
 // Defensive, and not hypothetical: the walk is fed hand-built nodes by this very file, and a real
 // <slot> in a document that has never been attached to a shadow root has no assignment either.
 // The branch must not throw or swallow the node's children when the method is absent.
  const btn = fakeEl({ tag: 'BUTTON', focusable: true });
  const slot = fakeSlot({ assigned: null, fallback: [btn] });
  const container = fakeEl({ children: [slot] });

  assert.deepEqual(focusablesIn(container), [btn]);
});

// ── 1 · the wrap rule ──────────────────────────────────────────────────────────────────────────────

test('Tab in the middle of a dialog is left to the browser', () => {
 // The trap must return null: NOT an index: anywhere the native order is already right. A trap
 // that preventDefaults every Tab re-implements tab order and gets it wrong for radio groups,
 // positive tabindex and nested shadow roots.
  for (let i = 0; i < 4; i++) {
    assert.equal(nextTrapIndex(5, i, false), null, `forward Tab from ${i} of 5 should be native`);
  }
  for (let i = 1; i < 5; i++) {
    assert.equal(nextTrapIndex(5, i, true), null, `back Tab from ${i} of 5 should be native`);
  }
});

test('Tab off the last control wraps to the first', () => {
  assert.equal(nextTrapIndex(5, 4, false), 0);
  assert.equal(nextTrapIndex(1, 0, false), 0, 'a single control wraps to itself');
});

test('Shift+Tab off the first control wraps to the last', () => {
  assert.equal(nextTrapIndex(5, 0, true), 4);
  assert.equal(nextTrapIndex(1, 0, true), 0, 'a single control wraps to itself');
});

test('focus that has escaped the dialog is pulled back to the near edge', () => {
 // activeIndex < 0 means the focused element is not in the dialog: either focus never entered
 // (the invoker is still focused behind the scrim) or something moved it out. Forward Tab should
 // land on the first control, backward on the last: the edge the user was travelling toward.
  assert.equal(nextTrapIndex(3, -1, false), 0);
  assert.equal(nextTrapIndex(3, -1, true), 2);
});

test('an empty dialog yields no index to move to', () => {
 // No focusables means there is no ITEM to focus; returning an index here would throw on
 // items[target].focus(). Note what this does NOT mean: the Chromium walk found that treating
 // "no index" as "let the browser Tab" lets focus walk out of a modal whose content has not
 // loaded yet (till-close's Z-report step). The controller handles count===0 BEFORE consulting
 // this function, swallowing the Tab and holding focus on the container: see the source gate
 // below, which pins that ordering.
  assert.equal(nextTrapIndex(0, -1, false), null);
  assert.equal(nextTrapIndex(0, 0, true), null);
  assert.equal(nextTrapIndex(-1, 0, false), null);
});

test('the controller swallows Tab in an empty dialog rather than deferring to the browser', () => {
 // A behaviour this Node suite cannot execute (it needs real focus) but CAN pin structurally: the
 // empty-dialog branch must come before nextTrapIndex is consulted, and it must preventDefault.
 // If a refactor moves the count===0 check after the arithmetic, the modal leaks focus again and
 // only the Chromium walk would notice.
  const src = readKit('modal-focus.ts');
  const emptyBranch = src.indexOf('if (items.length === 0) {');
  const arithmetic = src.indexOf('nextTrapIndex(items.length');
  assert.ok(emptyBranch > 0, 'the empty-dialog branch is gone: an unloaded modal will leak focus');
  assert.ok(arithmetic > 0, 'sanity: the trap still calls nextTrapIndex');
  assert.ok(
    emptyBranch < arithmetic,
    'the empty-dialog branch must be checked BEFORE the wrap arithmetic, or a dialog with no ' +
      'focusable content falls through to "let the browser Tab" and focus leaves the modal',
  );
  const branchBody = src.slice(emptyBranch, arithmetic);
  assert.match(branchBody, /e\.preventDefault\(\)/, 'the empty-dialog branch must swallow the Tab');
  assert.match(branchBody, /container\.focus\(\)/, 'the empty-dialog branch must hold focus on the container');
});

test('the controller re-establishes focus the browser dropped on a re-render', () => {
 // Also from the Chromium walk: a wizard step change removes the focused control, the browser
 // resets focus to <body>, and because the keydown listener lives on the HOST, Escape silently
 // stops working. The recovery must stay narrowed to document.body: recovering from ANY outside
 // location makes the two-dialog panes fight over focus on every update.
  const src = readKit('modal-focus.ts');
  assert.match(src, /recoverIfDropped/, 'the dropped-focus recovery is gone: a wizard step change strands the user');
  const fn = src.slice(src.indexOf('private recoverIfDropped'));
  assert.match(
    fn.slice(0, 400),
    /active\s*!==\s*document\.body/,
    'the recovery must fire ONLY when focus fell to <body>; recovering from any outside location ' +
      'starts a focus tug-of-war between the two controllers on rule-editor/quantity-breaks/customer-pricing',
  );
});

// ── 2 · initial focus ──────────────────────────────────────────────────────────────────────────────

test('initial focus is the first control unless a component names one', () => {
  assert.equal(pickInitialIndex(4, -1), 0);
  assert.equal(pickInitialIndex(4, 2), 2, 'data-autofocus wins');
  assert.equal(pickInitialIndex(0, -1), null, 'nothing focusable -> caller falls back to container');
});

test('a stale data-autofocus index cannot send focus out of bounds', () => {
 // The index comes from findIndex over a list measured this render; a component that conditionally
 // renders its autofocus target could hand over an index past the end. Falling back to the first
 // control is correct; items[7] of a 3-item dialog would throw inside a keydown handler.
  assert.equal(pickInitialIndex(3, 7), 0);
  assert.equal(pickInitialIndex(3, 3), 0, 'off-by-one at the boundary falls back too');
});

// ── 3 · the focusable selector ─────────────────────────────────────────────────────────────────────

test('the focusable selector excludes tabindex="-1" and covers the non-native controls', () => {
 // [tabindex="-1"] must be OUT: it is what a dialog CONTAINER is marked with so it can take initial
 // focus without becoming a tab stop. If it were in the list, every dialog would gain a phantom
 // first stop on its own scrim.
  assert.ok(!FOCUSABLE_SELECTOR.includes('[tabindex="-1"]:not') , 'sanity: parse below is meaningful');
  assert.match(FOCUSABLE_SELECTOR, /\[tabindex\]:not\(\[tabindex="-1"\]\)/);
  for (const native of ['button:not([disabled])', 'input:not([disabled])', 'select:not([disabled])', 'textarea:not([disabled])', 'a[href]', 'summary']) {
    assert.ok(FOCUSABLE_SELECTOR.includes(native), `selector is missing ${native}`);
  }
});

// ── 4 · E6: every modal-class component installs the trap ──────────────────────────────────────────

// The gate that makes this stick. Without it, the next modal ships with a scrim and no trap: which
// is exactly how all 17 of the existing ones got here.
//
// A "modal-class" surface is defined by what it PAINTS, not by what it is called: a full-viewport
// scrim over the page. The four idioms below are the app's, found by scanning for the rendered
// container rather than by listing components someone remembered.
const MODAL_CONTAINER_RE = /class="(scrim|hos-dialog|overlay|backdrop)"/;

function componentFiles() {
  return readdirSync(here)
    .filter((f) => f.endsWith('.ts') && !f.includes('.test.') && f !== 'modal-focus.ts')
    .sort();
}

function modalComponents() {
  return componentFiles().filter((f) => MODAL_CONTAINER_RE.test(read(f)));
}

test('the modal scan finds the app modals (guards the gate below)', () => {
 // Without this, a drifted regex scans zero components and the gate below passes vacuously: the
 // failure shape every gate in this repo is written to avoid.
  const modals = modalComponents();
  // HH note: the census is this tree's, not the reference app's: six modal-class components came
  // over, and the reference's other screens (till-close, kiosk-takeover, fallback-modal,
  // rule-editor) stayed unported, so their anchors left with them (CLOSURE.md).
  // HH note: the customer picker moved into @hardscapehouse/pos-kit
  // (web/packages/pos-kit, src/ported); the kit's modal census copy anchors
  // it there, and this tree keeps the five below.
  assert.ok(modals.length >= 5, `expected the scan to find the ported modal surfaces; found ${modals.length}`);
  for (const anchor of ['pos-parked.ts', 'pos-return.ts', 'pos-assess-relief.ts', 'pos-terminal.ts', 'quick-action-start-guard.ts']) {
    assert.ok(modals.includes(anchor), `the modal scan missed ${anchor}`);
  }
});

test('E6: every modal-class component installs the shared focus trap', () => {
 // Importing ModalFocus without constructing it compiles, lints clean and traps nothing: the same
 // silent-no-op shape as an unapplied focusRing. So the check is for the CONSTRUCTION.
  const missing = [];
  for (const file of modalComponents()) {
    const src = read(file);
    if (!/new ModalFocus\(/.test(src)) missing.push(file);
  }
  assert.deepEqual(
    missing.sort(),
    [],
    `these components paint a full-viewport modal scrim but never construct a ModalFocus, so a ` +
      `keyboard user tabs straight out of the dialog into the dimmed page behind it. Add ` +
      `\`new ModalFocus(this, { container: ..., isOpen: ..., onEscape: ... })\` from ` +
      `src/modal-focus.ts:\n  ${missing.sort().join('\n  ')}`,
  );
});

test('E6: a component that opens more than one dialog traps each of them', () => {
 // rule-editor, quantity-breaks and customer-pricing each raise TWO dialogs from one element. One
 // ModalFocus would trap whichever container the resolver happened to return and silently leave the
 // other one open-bordered. Count containers, count controllers.
  const shortfalls = [];
  for (const file of modalComponents()) {
    const src = read(file);
    const containers = (src.match(/class="(scrim|hos-dialog|overlay|backdrop)"/g) ?? []).length;
    const controllers = (src.match(/new ModalFocus\(/g) ?? []).length;
    if (controllers < containers) shortfalls.push(`${file}: ${containers} dialog(s), ${controllers} trap(s)`);
  }
  assert.deepEqual(
    shortfalls.sort(),
    [],
    `each modal container needs its own ModalFocus: one controller cannot trap two dialogs:\n  ` +
      shortfalls.sort().join('\n  '),
  );
});

// HH note: the non-dismissable-modals gate read kiosk-takeover.ts and till-close.ts, two
// reference screens that are not ported; the gate stayed with the reference (CLOSURE.md). The
// wiring work item re-pins it if HH grows modals that block on a recorded acknowledgement.
