// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-signature-logic.test.mjs; adapted for HH.

// Regression for the DIB-386 typed-name attestation gate (pos-signature-logic.ts).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  canSubmitAttestation,
  attestationBlockedReason,
  normalizeTypedName,
} from './pos-signature-logic.ts';

const draft = (over = {}) => ({ typedName: 'Homeowner A', affirmed: true, busy: false, ...over });

// ── DIB-386: the affirmative is a SEPARATE required act ─────────────────────
test('DIB-386: a typed name WITHOUT the affirmative cannot be submitted', () => {
 // This is the whole ruling: an attestation someone could produce by accident is not an
 // attestation. Typing a name is not agreeing to anything.
  assert.equal(canSubmitAttestation(draft({ affirmed: false })), false);
  assert.equal(attestationBlockedReason(draft({ affirmed: false })), 'Tick the confirmation to sign.');
});

test('DIB-386: the affirmative WITHOUT a name cannot be submitted either', () => {
  assert.equal(canSubmitAttestation(draft({ typedName: '' })), false);
  assert.equal(canSubmitAttestation(draft({ typedName: '   ' })), false, 'whitespace is not a name');
  assert.equal(canSubmitAttestation(draft({ typedName: '\t\n' })), false);
  assert.equal(attestationBlockedReason(draft({ typedName: '' })), 'Type the signer’s full name.');
});

test('DIB-386: both together: and only both: enable the signature', () => {
  assert.equal(canSubmitAttestation(draft()), true);
  assert.equal(attestationBlockedReason(draft()), '', 'no hint next to an enabled button');
});

test('DIB-386: an in-flight capture is not resubmittable (no double-signing)', () => {
  assert.equal(canSubmitAttestation(draft({ busy: true })), false);
  assert.equal(attestationBlockedReason(draft({ busy: true })), 'Signing…');
});

// ── the name that is actually sealed ────────────────────────────────────────
test('DIB-386: the sealed name is trimmed: surrounding whitespace is not part of a name', () => {
  assert.equal(normalizeTypedName('  Homeowner A  '), 'Homeowner A');
  assert.equal(normalizeTypedName('Homeowner A'), 'Homeowner A');
  assert.equal(normalizeTypedName('   '), '');
});
