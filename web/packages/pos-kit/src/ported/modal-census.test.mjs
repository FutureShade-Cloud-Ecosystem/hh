// The kit's half of the modal focus census (the review item E6), carried over
// from the Counter Till's app tree when the customer picker moved here: every
// modal-class component in this tree actually installs the shared focus trap.
// The app's copy of the census keeps its claim over the app's modal surfaces;
// this copy makes the same claim over the kit's tree.
//
// A "modal-class" surface is defined by what it PAINTS, not by what it is
// called: a full-viewport scrim over the page. The idiom is found by scanning
// for the rendered container rather than by listing components someone
// remembered.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const read = (p) => readFileSync(join(here, p), 'utf8');

const MODAL_CONTAINER_RE = /class="(scrim|hos-dialog|overlay|backdrop)"/;

function componentFiles() {
  return readdirSync(here)
    .filter((f) => f.endsWith('.ts') && !f.includes('.test.') && f !== 'modal-focus.ts')
    .sort();
}

function modalComponents() {
  return componentFiles().filter((f) => MODAL_CONTAINER_RE.test(read(f)));
}

test('the modal scan finds the kit modal surfaces (guards the gate below)', () => {
  // Without this, a drifted regex scans zero components and the gate below
  // passes vacuously: the failure shape every gate in this repo is written to
  // avoid.
  const modals = modalComponents();
  // HH note: the kit's one modal-class component came over with the customer
  // picker; the till's other dialogs stayed with the app and its census.
  assert.ok(modals.length >= 1, `expected the scan to find the kit's modal surfaces; found ${modals.length}`);
  for (const anchor of ['pos-customer-picker.ts']) {
    assert.ok(modals.includes(anchor), `the modal scan missed ${anchor}`);
  }
});

test('E6: every modal-class component in the kit installs the shared focus trap', () => {
  // Importing ModalFocus without constructing it compiles, lints clean and
  // traps nothing: the same silent-no-op shape as an unapplied focusRing. So
  // the check is for the CONSTRUCTION.
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
      `src/ported/modal-focus.ts:\n  ${missing.sort().join('\n  ')}`,
  );
});

test('E6: a component that opens more than one dialog traps each of them', () => {
  // One ModalFocus would trap whichever container the resolver happened to
  // return and silently leave the other one open-bordered. Count containers,
  // count controllers.
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
